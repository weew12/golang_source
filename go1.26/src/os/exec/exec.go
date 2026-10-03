// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package exec 运行外部命令。它封装了 os.StartProcess，使重新映射
// stdin 和 stdout、用管道连接 I/O 以及进行其他调整变得更加容易。
//
// 与 C 及其他语言中的 "system" 库调用不同，os/exec 包有意不调用
// 系统 shell，也不展开任何 glob 模式，或处理通常由 shell 完成的
// 其他扩展、管道或重定向。该包的行为更像 C 的 "exec" 系列函数。
// 要展开 glob 模式，可以直接调用 shell（注意转义任何危险输入），
// 或者使用 [path/filepath] 包的 Glob 函数。要展开环境变量，
// 请使用包 os 的 ExpandEnv。
//
// 注意，本包中的示例假定使用 Unix 系统。它们可能无法在 Windows 上
// 运行，也无法在 go.dev 和 pkg.go.dev 所使用的 Go Playground 中运行。
//
// # 当前目录中的可执行文件
//
// 函数 [Command] 和 [LookPath] 会按照宿主操作系统的约定，在当前路径
// 所列出的目录中查找程序。数十年来，操作系统一直将当前目录包含在
// 这一搜索中，有时是隐式包含，有时则是通过默认配置显式地如此设置。
// 现代实践认为，包含当前目录通常是出乎意料的，并且往往会导致安全问题。
//
// 为了避免这些安全问题，自 Go 1.19 起，本包不会再使用相对于当前目录的
// 隐式或显式路径条目来解析程序。也就是说，如果你运行 [LookPath]("go")，
// 无论在 Unix 上返回 ./go 还是在 Windows 上返回 .\go.exe，都不会成功，
// 无论路径如何配置。相反，如果通常的路径算法会得出那样的结果，这些函数
// 会返回一个满足 [errors.Is](err, [ErrDot]) 的错误 err。
//
// 例如，考虑以下两个程序片段：
//
//	path, err := exec.LookPath("prog")
//	if err != nil {
//		log.Fatal(err)
//	}
//	use(path)
//
// 以及
//
//	cmd := exec.Command("prog")
//	if err := cmd.Run(); err != nil {
//		log.Fatal(err)
//	}
//
// 无论当前路径如何配置，它们都不会找到并运行 ./prog 或 .\prog.exe。
//
// 如果代码总是想从当前目录运行程序，可以将其改写为
// 使用 "./prog" 而不是 "prog"。
//
// 如果代码坚持要包含来自相对路径条目的结果，
// 则可以用 errors.Is 检查来覆盖该错误：
//
//	path, err := exec.LookPath("prog")
//	if errors.Is(err, exec.ErrDot) {
//		err = nil
//	}
//	if err != nil {
//		log.Fatal(err)
//	}
//	use(path)
//
// 以及
//
//	cmd := exec.Command("prog")
//	if errors.Is(cmd.Err, exec.ErrDot) {
//		cmd.Err = nil
//	}
//	if err := cmd.Run(); err != nil {
//		log.Fatal(err)
//	}
//
// 设置环境变量 GODEBUG=execerrdot=0 可以完全禁用 ErrDot 的生成，
// 为无法应用更有针对性修复的程序临时恢复 Go 1.19 之前的行为。
// Go 的未来版本可能会移除对该变量的支持。
//
// 在添加此类覆盖之前，请务必理解这样做的安全影响。
// 更多信息参见 https://go.dev/blog/path-security。
package exec

import (
	"bytes"
	"context"
	"errors"
	"internal/godebug"
	"internal/syscall/execenv"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Error 在 [LookPath] 无法将某个文件归类为可执行文件时返回。
type Error struct {
	// Name 是发生错误的文件名。
	Name string
	// Err 是底层错误。
	Err error
}

func (e *Error) Error() string {
	return "exec: " + strconv.Quote(e.Name) + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// 如果进程以成功的状态码退出，但其输出管道在命令的 WaitDelay 到期之前
// 未关闭，[Cmd.Wait] 会返回 ErrWaitDelay。
var ErrWaitDelay = errors.New("exec: WaitDelay expired before I/O complete")

// wrappedError 在不依赖 fmt.Errorf 的情况下包装一个错误。
type wrappedError struct {
	prefix string
	err    error
}

func (w wrappedError) Error() string {
	return w.prefix + ": " + w.err.Error()
}

func (w wrappedError) Unwrap() error {
	return w.err
}

// Cmd 表示一个正在准备或运行的外部命令。
//
// 在调用 Cmd 的 [Cmd.Start]、[Cmd.Run]、[Cmd.Output] 或
// [Cmd.CombinedOutput] 方法之后，Cmd 不能被重用。
type Cmd struct {
	// Path 是要运行的命令的路径。
	//
	// 这是唯一必须设置为非零值的字段。如果 Path 是相对路径，
	// 则相对于 Dir 求值。
	Path string

	// Args 保存命令行参数，其中 Args[0] 是命令本身。
	// 如果 Args 字段为空或为 nil，Run 使用 {Path}。
	//
	// 在典型用法中，Path 和 Args 都通过调用 Command 来设置。
	Args []string

	// Env 指定进程的环境。每一项的形式为 "key=value"。
	// 如果 Env 为 nil，新进程使用当前进程的环境。
	// 如果 Env 包含重复的环境键，则每个重复键只使用切片中的最后一个值。
	// 在 Windows 上有一个特殊情况：如果缺少 SYSTEMROOT，且它没有被显式
	// 设置为空字符串，则会始终添加 SYSTEMROOT。
	//
	// 另请参见 Dir 字段，它可能会设置环境中的 PWD。
	Env []string

	// Dir 指定命令的工作目录。
	// 如果 Dir 为空字符串，Run 在调用进程的当前目录中运行命令。
	//
	// 在 Unix 系统上，如果未另行指定，Dir 的值还会决定子进程的 PWD
	// 环境变量。Unix 进程并不以名称来表示其工作目录，而是以对文件树中
	// 某个节点的隐式引用来表示。因此，如果子进程通过调用诸如 C 的 getcwd
	// 之类的函数（该函数通过向上遍历文件树来计算规范名称）来获取其工作
	// 目录，那么当 Dir 的值是涉及符号链接的别名时，它将无法还原 Dir 的
	// 原始值。然而，如果子进程调用 Go 的 [os.Getwd] 或 GNU C 的
	// get_current_dir_name，并且 PWD 的值是当前目录的别名，那么这些函数
	// 将返回 PWD 的值，该值与 Dir 的值相匹配。
	Dir string

	// Stdin 指定进程的标准输入。
	//
	// 如果 Stdin 为 nil，进程从空设备（os.DevNull）读取。
	//
	// 如果 Stdin 是一个 *os.File，进程的标准输入会直接连接到该文件。
	//
	// 否则，在命令执行期间，会有一个单独的 goroutine 从 Stdin 读取数据，
	// 并通过管道将其传递给命令。在这种情况下，Wait 要等到该 goroutine
	// 停止复制才会完成，这可能是因为它已到达 Stdin 的末尾（EOF 或读取
	// 错误），或者因为向管道写入返回了错误，或者因为设置了非零的 WaitDelay
	// 并已到期。
	Stdin io.Reader

	// Stdout 和 Stderr 指定进程的标准输出和标准错误。
	//
	// 如果其中任何一个为 nil，Run 会将对应的文件描述符连接到空设备
	// （os.DevNull）。
	//
	// 如果其中任何一个是 *os.File，进程对应的输出会直接连接到该文件。
	//
	// 否则，在命令执行期间，会有一个单独的 goroutine 通过管道从进程读取数据，
	// 并将其传递给对应的 Writer。在这种情况下，Wait 要等到该 goroutine
	// 到达 EOF、遇到错误或非零的 WaitDelay 到期才会完成。
	//
	// 如果 Stdout 和 Stderr 是同一个 writer，并且其类型可以用 == 比较，
	// 那么同一时刻至多只有一个 goroutine 会调用 Write。
	Stdout io.Writer
	Stderr io.Writer

	// ExtraFiles 指定由新进程继承的额外打开文件。它不包括标准输入、
	// 标准输出或标准错误。如果非 nil，第 i 项会成为文件描述符 3+i。
	//
	// ExtraFiles 在 Windows 上不受支持。
	ExtraFiles []*os.File

	// SysProcAttr 保存可选的、特定于操作系统的属性。
	// Run 会将其作为 os.ProcAttr 的 Sys 字段传递给 os.StartProcess。
	SysProcAttr *syscall.SysProcAttr

	// Process 是底层的进程，在启动之后才存在。
	Process *os.Process

	// ProcessState 包含已退出进程的信息。
	// 如果进程启动成功，Wait 或 Run 会在命令完成时填充其 ProcessState。
	ProcessState *os.ProcessState

	// ctx 是传递给 CommandContext 的 context（如果有）。
	ctx context.Context

	Err error // LookPath 错误（如果有）。

	// 如果 Cancel 非 nil，则命令必须是通过 CommandContext 创建的，
	// 并且当命令的 Context 完成时，会调用 Cancel。默认情况下，
	// CommandContext 将 Cancel 设置为调用命令 Process 的 Kill 方法。
	//
	// 通常，自定义的 Cancel 会向命令的 Process 发送信号，但它也可以
	// 采取其他操作来发起取消，例如关闭 stdin 或 stdout 管道，或在网络
	// 套接字上发送关闭请求。
	//
	// 如果在调用 Cancel 之后命令以成功状态退出，并且 Cancel 返回的错误
	// 不等价于 os.ErrProcessDone，则 Wait 及类似方法会返回一个非 nil
	// 错误：要么是包装了 Cancel 所返回错误的错误，要么是来自 Context 的错误。
	// （如果命令以非成功状态退出，或者 Cancel 返回的错误包装了
	// os.ErrProcessDone，则 Wait 及类似方法会继续返回命令通常的退出状态。）
	//
	// 如果 Cancel 设置为 nil，当命令的 Context 完成时不会立即发生任何事情，
	// 但非零的 WaitDelay 仍会生效。例如，这对于绕过那些不支持关闭信号
	// 但预期总能快速完成的命令中的死锁可能会很有用。
	//
	// 如果 Start 返回非 nil 错误，则不会调用 Cancel。
	Cancel func() error

	// 如果 WaitDelay 非零，它会限制等待 Wait 中两个意外延迟来源所花费的
	// 时间：在关联的 Context 被取消后未能退出的子进程，以及已退出但未关闭
	// 其 I/O 管道的子进程。
	//
	// WaitDelay 计时器在关联的 Context 完成时启动，或者在调用 Wait 观察到
	// 子进程已退出时启动，以先发生者为准。当延迟耗尽时，命令会关闭子进程
	// 和/或其 I/O 管道。
	//
	// 如果子进程未能退出 —— 也许是因为它忽略了或未能收到来自 Cancel 函数的
	// 关闭信号，或者因为没有设置 Cancel 函数 —— 那么它将使用
	// os.Process.Kill 被终止。
	//
	// 然后，如果与子进程通信的 I/O 管道仍然打开，则会关闭这些管道，
	// 以解除当前阻塞在 Read 或 Write 调用上的任何 goroutine。
	//
	// 如果管道因 WaitDelay 而被关闭，没有发生 Cancel 调用，并且命令以其他
	// 方式以成功状态退出，则 Wait 及类似方法会返回 ErrWaitDelay 而不是 nil。
	//
	// 如果 WaitDelay 为零（默认值），I/O 管道会被读取直到 EOF，而在命令
	// 成为孤儿的子进程也关闭其管道描述符之前，EOF 可能不会出现。
	WaitDelay time.Duration

	// childIOFiles 保存子进程的 stdin、stdout 和/或 stderr 文件中由 Cmd
	// 自身打开（而非由调用者提供）的那些文件的关闭器。它们应在被子进程
	// 继承后尽快关闭。
	childIOFiles []io.Closer

	// parentIOPipes 保存连接到子进程 stdin、stdout 和/或 stderr 流的任何
	// 管道中，父进程这一端由 Cmd 自身打开（而非由调用者提供）的那些管道的
	// 关闭器。它们应在 Wait 看到命令和复制 goroutine 退出之后，或在
	// WaitDelay 到期之后关闭。
	parentIOPipes []io.Closer

	// goroutine 保存一组要执行的闭包，用于向命令的 I/O 管道复制数据和/或
	// 从其中复制数据。
	goroutine []func() error

	// 如果 goroutineErr 非 nil，则当所有复制 goroutine 都完成后，
	// 它会接收来自复制 goroutine 的第一个错误。
	// 一旦接收到错误，goroutineErr 就会被设置为 nil。
	goroutineErr <-chan error

	// 如果 ctxResult 非 nil，它会恰好接收一次 watchCtx 的结果。
	ctxResult <-chan ctxResult

	// 创建 Command 时保存的栈，条件是 GODEBUG 包含 execwait=2。
	// 用于调试泄漏。
	createdByStack []byte

	// 很久以前的一次安全发布中，我们创建了 x/sys/execabs，
	// 它操纵了此结构体中未导出的 lookPathErr 错误字段。在 Go 1.19 中，
	// 我们将该字段导出为上面所示的 Err error，但我们必须保留 lookPathErr，
	// 以便针对新工具链构建的旧程序使用。
	// String 和 Start 方法会优先在 lookPathErr 中查找错误，而不是在 Err 中，
	// 以保留 execabs 设置的错误。
	//
	// 一般来说，我们不保证这种对 reflect 的误用，但这次对 reflect 的误用
	// 是我们自己所为 —— 它是修复该安全问题的各种糟糕选项中最不坏的一个，
	// 而且人们依赖那些旧版本的 execabs 继续工作。
	// 结果是，我们必须永远保留这个变量，这是一道兼容性伤疤。
	//
	// 更多背景参见 https://go.dev/blog/path-security
	// 和 https://go.dev/issue/43724。
	lookPathErr error

	// cachedLookExtensions 缓存调用 lookExtensions 的结果。
	// 当以绝对路径调用 Command 时会设置它，让 Command 完成解析扩展名的工作，
	// 这样 Start 就无需再次进行解析。这仅在 Windows 上使用。
	cachedLookExtensions struct{ in, out string }

	// startCalled 记录曾尝试调用 Start，无论结果如何。
	// （在 go.dev/issue/77075 解决之前，我们使用 atomic.SwapInt32，
	// 而不是 atomic.Bool.Swap，以避免触发 copylocks vet 检查。）
	startCalled int32
}

// ctxResult 报告监视与正在运行的命令关联的 Context（并在需要时发送
// 相应信号）的结果。
type ctxResult struct {
	err error

	// 如果 timer 非 nil，它会在 Context 完成之后 WaitDelay 已到期时触发。
	//
	// （如果 timer 为 nil，这意味着在命令完成之前 Context 尚未完成，
	// 或者没有设置 WaitDelay，或者 WaitDelay 已经到期且其效果已经应用。）
	timer *time.Timer
}

var execwait = godebug.New("#execwait")
var execerrdot = godebug.New("execerrdot")

// Command 返回 [Cmd] 结构体，用于以给定的参数执行指定的程序。
//
// 它在返回的结构体中只设置 Path 和 Args。
//
// 如果 name 不包含路径分隔符，Command 会使用 [LookPath] 在可能的情况下
// 将 name 解析为完整路径。否则，它直接使用 name 作为 Path。
//
// 返回的 Cmd 的 Args 字段由命令名后跟 arg 的元素构成，因此 arg 不应包含
// 命令名本身。例如，Command("echo", "hello")。
// Args[0] 始终是 name，而不是可能解析后的 Path。
//
// 在 Windows 上，进程将整条命令行作为单个字符串接收，并自行进行解析。
// Command 会将 Args 组合并加引号，形成一条命令行字符串，其算法与使用
// CommandLineToArgvW 的应用程序兼容（这是最常见的方式）。值得注意的例外
// 是 msiexec.exe 和 cmd.exe（以及由此而来的所有批处理文件），它们使用了
// 不同的去引号算法。在这些或其他类似情况下，你可以自行加引号，并在
// SysProcAttr.CmdLine 中提供完整的命令行，同时让 Args 保持为空。
func Command(name string, arg ...string) *Cmd {
	cmd := &Cmd{
		Path: name,
		Args: append([]string{name}, arg...),
	}

	if v := execwait.Value(); v != "" {
		if v == "2" {
			// 获取调用者的栈。（这等价于 runtime/debug.Stack，
			// 为了不导入整个包而进行了复制。）
			stack := make([]byte, 1024)
			for {
				n := runtime.Stack(stack, false)
				if n < len(stack) {
					stack = stack[:n]
					break
				}
				stack = make([]byte, 2*len(stack))
			}

			if i := bytes.Index(stack, []byte("\nos/exec.Command(")); i >= 0 {
				stack = stack[i+1:]
			}
			cmd.createdByStack = stack
		}

		runtime.SetFinalizer(cmd, func(c *Cmd) {
			if c.Process != nil && c.ProcessState == nil {
				debugHint := ""
				if c.createdByStack == nil {
					debugHint = " (set GODEBUG=execwait=2 to capture stacks for debugging)"
				} else {
					os.Stderr.WriteString("GODEBUG=execwait=2 detected a leaked exec.Cmd created by:\n")
					os.Stderr.Write(c.createdByStack)
					os.Stderr.WriteString("\n")
					debugHint = ""
				}
				panic("exec: Cmd started a Process but leaked without a call to Wait" + debugHint)
			}
		})
	}

	if filepath.Base(name) == name {
		lp, err := LookPath(name)
		if lp != "" {
			// 即使 err 非 nil 也要更新 cmd.Path。
			// 如果 err 是 ErrDot（尤其是在 Windows 上），lp 可能包含
			// 一个已解析的扩展名（如 .exe 或 .bat），应当予以保留。
			cmd.Path = lp
		}
		if err != nil {
			cmd.Err = err
		}
	} else if runtime.GOOS == "windows" && filepath.IsAbs(name) {
		// 我们可能需要从 PATHEXT 添加文件名扩展名，
		// 或者验证一个已经存在的扩展名。
		// 由于路径是绝对的，其扩展名应当是明确的，
		// 且与 cmd.Dir 无关，因此我们现在就可以继续并缓存这次查找。
		//
		// 注意，我们不在这里为相对路径缓存任何内容，因为在从本函数返回之后
		// cmd.Dir 可能会被设置，这可能导致命令解析到不同的扩展名。
		if lp, err := lookExtensions(name, ""); err == nil {
			cmd.cachedLookExtensions.in, cmd.cachedLookExtensions.out = name, lp
		} else {
			cmd.Err = err
		}
	}
	return cmd
}

// CommandContext 类似 [Command]，但包含一个 context。
//
// 如果所提供的 context 在命令自行完成之前变为已完成状态，
// 则该 context 会被用于中断进程（通过调用 cmd.Cancel 或
// [os.Process.Kill]）。
//
// CommandContext 将命令的 Cancel 函数设置为调用其 Process 的 Kill 方法，
// 并让其 WaitDelay 保持未设置。调用者可以在启动命令之前修改这些字段，
// 以更改取消行为。
func CommandContext(ctx context.Context, name string, arg ...string) *Cmd {
	if ctx == nil {
		panic("nil Context")
	}
	cmd := Command(name, arg...)
	cmd.ctx = ctx
	cmd.Cancel = func() error {
		return cmd.Process.Kill()
	}
	return cmd
}

// String 返回 c 的便于阅读的描述。
// 它仅用于调试。
// 特别地，它不适合用作 shell 的输入。
// String 的输出可能因 Go 版本而异。
func (c *Cmd) String() string {
	if c.Err != nil || c.lookPathErr != nil {
		// 无法解析路径；报告最初请求的路径（加上参数）
		return strings.Join(c.Args, " ")
	}
	// 报告确切的可执行文件路径（加上参数）
	b := new(strings.Builder)
	b.WriteString(c.Path)
	for _, a := range c.Args[1:] {
		b.WriteByte(' ')
		b.WriteString(a)
	}
	return b.String()
}

// interfaceEqual 防止对两个底层类型不可比较的接口进行相等性测试时发生 panic。
func interfaceEqual(a, b any) bool {
	defer func() {
		recover()
	}()
	return a == b
}

func (c *Cmd) argv() []string {
	if len(c.Args) > 0 {
		return c.Args
	}
	return []string{c.Path}
}

func (c *Cmd) childStdin() (*os.File, error) {
	if c.Stdin == nil {
		f, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		c.childIOFiles = append(c.childIOFiles, f)
		return f, nil
	}

	if f, ok := c.Stdin.(*os.File); ok {
		return f, nil
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	c.childIOFiles = append(c.childIOFiles, pr)
	c.parentIOPipes = append(c.parentIOPipes, pw)
	c.goroutine = append(c.goroutine, func() error {
		_, err := io.Copy(pw, c.Stdin)
		if skipStdinCopyError(err) {
			err = nil
		}
		if err1 := pw.Close(); err == nil {
			err = err1
		}
		return err
	})
	return pr, nil
}

func (c *Cmd) childStdout() (*os.File, error) {
	return c.writerDescriptor(c.Stdout)
}

func (c *Cmd) childStderr(childStdout *os.File) (*os.File, error) {
	if c.Stderr != nil && interfaceEqual(c.Stderr, c.Stdout) {
		return childStdout, nil
	}
	return c.writerDescriptor(c.Stderr)
}

// writerDescriptor 返回一个 os.File，子进程可以向其写入数据，
// 从而将数据发送到 w。
//
// 如果 w 为 nil，writerDescriptor 返回一个写入 os.DevNull 的 File。
func (c *Cmd) writerDescriptor(w io.Writer) (*os.File, error) {
	if w == nil {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return nil, err
		}
		c.childIOFiles = append(c.childIOFiles, f)
		return f, nil
	}

	if f, ok := w.(*os.File); ok {
		return f, nil
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	c.childIOFiles = append(c.childIOFiles, pw)
	c.parentIOPipes = append(c.parentIOPipes, pr)
	c.goroutine = append(c.goroutine, func() error {
		_, err := io.Copy(w, pr)
		pr.Close() // 以防 io.Copy 因写入错误而停止
		return err
	})
	return pw, nil
}

func closeDescriptors(closers []io.Closer) {
	for _, fd := range closers {
		fd.Close()
	}
}

// Run 启动指定的命令并等待其完成。
//
// 如果命令能够运行，复制 stdin、stdout 和 stderr 没有问题，
// 并且以零退出状态退出，则返回的错误为 nil。
//
// 如果命令启动了但未能成功完成，则错误类型为 [*ExitError]。
// 其他情况下可能返回其他错误类型。
//
// 如果调用 goroutine 已使用 [runtime.LockOSThread] 锁定了操作系统线程，
// 并修改了任何可继承的操作系统级线程状态（例如 Linux 或 Plan 9 的
// 命名空间），则新进程将继承调用者的线程状态。
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// Start 启动指定的命令，但不等待其完成。
//
// 如果 Start 成功返回，则 c.Process 字段会被设置。
//
// 成功调用 Start 之后，必须调用 [Cmd.Wait] 方法以释放相关的系统资源。
func (c *Cmd) Start() error {
	// 在延迟执行失败清理之前，检查是否有重复的 Start 调用。
	// 如果前一次 Start 调用成功，我们不希望错误地关闭其管道。
	// 即使第一次调用没有创建进程，调用两次 Start 也是错误的。
	if atomic.SwapInt32(&c.startCalled, 1) != 0 {
		return errors.New("exec: already started")
	}

	started := false
	defer func() {
		closeDescriptors(c.childIOFiles)
		c.childIOFiles = nil

		if !started {
			closeDescriptors(c.parentIOPipes)
			c.parentIOPipes = nil
			c.goroutine = nil // 有助 GC，便于管道 fd 的终结
		}
	}()

	if c.Path == "" && c.Err == nil && c.lookPathErr == nil {
		c.Err = errors.New("exec: no command")
	}
	if c.Err != nil || c.lookPathErr != nil {
		if c.lookPathErr != nil {
			return c.lookPathErr
		}
		return c.Err
	}
	lp := c.Path
	if runtime.GOOS == "windows" {
		if c.Path == c.cachedLookExtensions.in {
			// 如果 Command 是以绝对路径调用的，我们已解析出其扩展名，
			// 不应该需要再次解析（前提是在调用 Command 和 Start 之间
			// c.Path 没有被设置为其他值）。
			lp = c.cachedLookExtensions.out
		} else {
			// 如果 *Cmd 完全没有通过 Command 创建，或者 Command 是以
			// 相对路径调用的，我们就必须等到现在才解析它，以防 c.Dir 被更改。
			//
			// 不幸的是，我们不能将结果写回 c.Path，因为程序可能假定它们
			// 可以在读取路径的同时并发调用 Start。（在 Unix 平台上这样做
			// 是安全且无数据竞争的，而且用户可能不会在所有平台上用竞态
			// 检测器测试；参见 https://go.dev/issue/62596。）
			//
			// 因此，我们会将完全解析后的路径传递给 os.StartProcess，
			// 但保持 c.Path 不变：遗漏一点日志信息似乎比触发令人意外的
			// 数据竞争危害更小，而且如果用户真的在意那点日志信息，
			// 他们总是可以用 LookPath 来解析它。
			var err error
			lp, err = lookExtensions(c.Path, c.Dir)
			if err != nil {
				return err
			}
		}
	}
	if c.Cancel != nil && c.ctx == nil {
		return errors.New("exec: command with a non-nil Cancel was not created with CommandContext")
	}
	if c.ctx != nil {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}
	}

	childFiles := make([]*os.File, 0, 3+len(c.ExtraFiles))
	stdin, err := c.childStdin()
	if err != nil {
		return err
	}
	childFiles = append(childFiles, stdin)
	stdout, err := c.childStdout()
	if err != nil {
		return err
	}
	childFiles = append(childFiles, stdout)
	stderr, err := c.childStderr(stdout)
	if err != nil {
		return err
	}
	childFiles = append(childFiles, stderr)
	childFiles = append(childFiles, c.ExtraFiles...)

	env, err := c.environ()
	if err != nil {
		return err
	}

	c.Process, err = os.StartProcess(lp, c.argv(), &os.ProcAttr{
		Dir:   c.Dir,
		Files: childFiles,
		Env:   env,
		Sys:   c.SysProcAttr,
	})
	if err != nil {
		return err
	}
	started = true

	// 除非有 goroutine 要启动，否则不要分配 goroutineErr 通道。
	if len(c.goroutine) > 0 {
		goroutineErr := make(chan error, 1)
		c.goroutineErr = goroutineErr

		type goroutineStatus struct {
			running  int
			firstErr error
		}
		statusc := make(chan goroutineStatus, 1)
		statusc <- goroutineStatus{running: len(c.goroutine)}
		for _, fn := range c.goroutine {
			go func(fn func() error) {
				err := fn()

				status := <-statusc
				if status.firstErr == nil {
					status.firstErr = err
				}
				status.running--
				if status.running == 0 {
					goroutineErr <- status.firstErr
				} else {
					statusc <- status
				}
			}(fn)
		}
		c.goroutine = nil // 允许 goroutine 的闭包在完成后被 GC。
	}

	// 如果当命令的 Context 到期时有任何事情要做，
	// 就启动一个 goroutine 来监视取消。
	//
	// （即使命令是由 CommandContext 创建的，某个辅助库也可能已将其
	// Cancel 字段显式设置回 nil，表示命令在取消之后终究应被允许继续运行。）
	if (c.Cancel != nil || c.WaitDelay != 0) && c.ctx != nil && c.ctx.Done() != nil {
		resultc := make(chan ctxResult)
		c.ctxResult = resultc
		go c.watchCtx(resultc)
	}

	return nil
}

// watchCtx 监视 c.ctx，直到它能够向 resultc 发送结果。
//
// 如果在能够发送结果之前 c.ctx 已完成，watchCtx 会调用 c.Cancel，
// 和/或在 c.WaitDelay 到期后杀死 cmd.Process。
//
// watchCtx 会操纵 c.goroutineErr，因此必须在调用 c.awaitGoroutines 之前
// 接收其结果。
func (c *Cmd) watchCtx(resultc chan<- ctxResult) {
	select {
	case resultc <- ctxResult{}:
		return
	case <-c.ctx.Done():
	}

	var err error
	if c.Cancel != nil {
		if interruptErr := c.Cancel(); interruptErr == nil {
			// 我们似乎已成功中断了命令，因此即使命令以退出码 0 退出，
			// 从此刻起的任何程序行为都可能是由 ctx 引起的。
			err = c.ctx.Err()
		} else if errors.Is(interruptErr, os.ErrProcessDone) {
			// 进程已经结束：只是我们还没有注意到。
			// （也许是尚未调用 c.Wait，或者它恰好与 c.ctx 被取消发生了竞态。）
			// 不要注入不必要的错误。
		} else {
			err = wrappedError{
				prefix: "exec: canceling Cmd",
				err:    interruptErr,
			}
		}
	}
	if c.WaitDelay == 0 {
		resultc <- ctxResult{err: err}
		return
	}

	timer := time.NewTimer(c.WaitDelay)
	select {
	case resultc <- ctxResult{err: err, timer: timer}:
		// c.Process.Wait 已返回，我们已将计时器交给 c.Wait。
		// 从这里开始，它将负责 goroutine 的关闭。
		return
	case <-timer.C:
	}

	killed := false
	if killErr := c.Process.Kill(); killErr == nil {
		// 我们似乎已杀死了进程。除非 Kill 信号与成功退出发生竞态，
		// 否则 c.Process.Wait 应向 c.Wait 返回非 nil 错误；如果确实
		// 发生了这种情况，我们不应报告虚假错误，所以这里不要把 err
		// 设置为任何值。
		killed = true
	} else if !errors.Is(killErr, os.ErrProcessDone) {
		err = wrappedError{
			prefix: "exec: killing Cmd",
			err:    killErr,
		}
	}

	if c.goroutineErr != nil {
		select {
		case goroutineErr := <-c.goroutineErr:
			// 仅当我们没有理由相信该错误是由上面对 Cancel 或 Kill 的调用
			// 引起时，才转发 goroutineErr。
			if err == nil && !killed {
				err = goroutineErr
			}
		default:
			// 关闭子进程的 I/O 管道，以防它遗弃了某个继承了这些管道
			// 且仍将它们保持打开的子进程（参见 https://go.dev/issue/23019）。
			//
			// 只有在发送完我们打算发送到进程的所有信号（通过上面的 Signal
			// 或 Kill）之后，我们才关闭 goroutine 管道：如果我们向进程发送
			// SIGKILL，我们更希望它死于 SIGKILL，而不是 SIGPIPE。
			// （不过，这仍可能导致任何成为孤儿的子进程以 SIGPIPE 终止。）
			closeDescriptors(c.parentIOPipes)
			// 等待复制 goroutine 完成，但报告 ErrWaitDelay 作为错误：
			// 这里的任何其他错误都可能是关闭管道导致的。
			_ = <-c.goroutineErr
			if err == nil {
				err = ErrWaitDelay
			}
		}

		// 由于我们已经接收了 c.goroutineErr 唯一的结果，
		// 将其设置为 nil 以防止 awaitGoroutines 在其上阻塞。
		c.goroutineErr = nil
	}

	resultc <- ctxResult{err: err}
}

// ExitError 报告命令未成功退出。
type ExitError struct {
	*os.ProcessState

	// Stderr 保存来自 Cmd.Output 方法的标准错误输出的一个子集，
	// 前提是标准错误没有以其他方式被收集。
	//
	// 如果错误输出很长，Stderr 可能只包含输出的前缀和后缀，
	// 中间部分会被替换为关于省略字节数的文本。
	//
	// Stderr 供调试使用，用于包含在错误消息中。
	// 有其他需求的用户应按需重定向 Cmd.Stderr。
	Stderr []byte
}

func (e *ExitError) Error() string {
	return e.ProcessState.String()
}

// Wait 等待命令退出，并等待向 stdin 复制或从 stdout 或 stderr 复制的
// 任何操作完成。
//
// 命令必须已由 [Cmd.Start] 启动。
//
// 如果命令能够运行，复制 stdin、stdout 和 stderr 没有问题，
// 并且以零退出状态退出，则返回的错误为 nil。
//
// 如果命令无法运行或未能成功完成，则错误类型为 [*ExitError]。
// 对于 I/O 问题，可能返回其他错误类型。
//
// 如果 c.Stdin、c.Stdout 或 c.Stderr 中的任何一个不是 [*os.File]，
// Wait 还会等待向进程复制或从进程复制的相应 I/O 循环完成。
//
// Wait 会释放与 [Cmd] 关联的任何资源。
func (c *Cmd) Wait() error {
	if c.Process == nil {
		return errors.New("exec: not started")
	}
	if c.ProcessState != nil {
		return errors.New("exec: Wait was already called")
	}

	state, err := c.Process.Wait()
	if err == nil && !state.Success() {
		err = &ExitError{ProcessState: state}
	}
	c.ProcessState = state

	var timer *time.Timer
	if c.ctxResult != nil {
		watch := <-c.ctxResult
		timer = watch.timer
		// 如果 c.Process.Wait 返回了错误，则优先采用它。
		// 否则，报告来自 watchCtx goroutine 的任何错误，
		// 例如 Context 取消或 WaitDelay 超时。
		if err == nil && watch.err != nil {
			err = watch.err
		}
	}

	if goroutineErr := c.awaitGoroutines(timer); err == nil {
		// 仅当程序在其他方面自行正常退出时，才报告来自复制 goroutine 的错误。
		// 否则，复制错误可能是由异常终止引起的。
		err = goroutineErr
	}
	closeDescriptors(c.parentIOPipes)
	c.parentIOPipes = nil

	return err
}

// awaitGoroutines 等待向命令的 I/O 管道复制或从其中复制数据的 goroutine
// 的结果。
//
// 如果 goroutine 完成之前 c.WaitDelay 已到期，awaitGoroutines 会强制
// 关闭它们的管道并返回 ErrWaitDelay。
//
// 如果 timer 非 nil，它必须在 c.WaitDelay 结束时向 timer.C 发送信号。
func (c *Cmd) awaitGoroutines(timer *time.Timer) error {
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		c.goroutineErr = nil
	}()

	if c.goroutineErr == nil {
		return nil // 没有正在运行的 goroutine 需要等待。
	}

	if timer == nil {
		if c.WaitDelay == 0 {
			return <-c.goroutineErr
		}

		select {
		case err := <-c.goroutineErr:
			// 避免启动计时器的开销。
			return err
		default:
		}

		// 没有启动已有的计时器：要么命令没有关联的 Context，
		// 要么 c.Process.Wait 在 Context 完成之前就已完成。
		timer = time.NewTimer(c.WaitDelay)
	}

	select {
	case <-timer.C:
		closeDescriptors(c.parentIOPipes)
		// 等待复制 goroutine 完成，但忽略任何错误
		// （因为它很可能是关闭管道导致的）。
		_ = <-c.goroutineErr
		return ErrWaitDelay

	case err := <-c.goroutineErr:
		return err
	}
}

// Output 运行命令并返回其标准输出。
// 返回的任何错误通常都是 [*ExitError] 类型。
// 如果 c.Stderr 为 nil 且返回的错误是 [*ExitError] 类型，
// Output 会填充所返回错误的 Stderr 字段。
func (c *Cmd) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout bytes.Buffer
	c.Stdout = &stdout

	captureErr := c.Stderr == nil
	if captureErr {
		c.Stderr = &prefixSuffixSaver{N: 32 << 10}
	}

	err := c.Run()
	if err != nil && captureErr {
		if ee, ok := err.(*ExitError); ok {
			ee.Stderr = c.Stderr.(*prefixSuffixSaver).Bytes()
		}
	}
	return stdout.Bytes(), err
}

// CombinedOutput 运行命令并返回其合并后的标准输出和标准错误。
func (c *Cmd) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if c.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	var b bytes.Buffer
	c.Stdout = &b
	c.Stderr = &b
	err := c.Run()
	return b.Bytes(), err
}

// StdinPipe 返回一个管道，命令启动时该管道会连接到命令的标准输入。
// 在 [Cmd.Wait] 看到命令退出后，该管道会自动关闭。
// 调用者只需调用 Close 即可让管道更早关闭。
// 例如，如果要运行的命令在标准输入关闭之前不会退出，
// 调用者必须关闭该管道。
func (c *Cmd) StdinPipe() (io.WriteCloser, error) {
	if c.Stdin != nil {
		return nil, errors.New("exec: Stdin already set")
	}
	if c.Process != nil {
		return nil, errors.New("exec: StdinPipe after process started")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stdin = pr
	c.childIOFiles = append(c.childIOFiles, pr)
	c.parentIOPipes = append(c.parentIOPipes, pw)
	return pw, nil
}

// StdoutPipe 返回一个管道，命令启动时该管道会连接到命令的标准输出。
//
// [Cmd.Wait] 会在看到命令退出后关闭该管道，因此大多数调用者无需自行关闭它。
// 因此，在从管道读取的所有操作完成之前调用 Wait 是不正确的。
// 同理，在使用 StdoutPipe 时调用 [Cmd.Run] 也是不正确的。
// 惯用法参见示例。
func (c *Cmd) StdoutPipe() (io.ReadCloser, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if c.Process != nil {
		return nil, errors.New("exec: StdoutPipe after process started")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stdout = pw
	c.childIOFiles = append(c.childIOFiles, pw)
	c.parentIOPipes = append(c.parentIOPipes, pr)
	return pr, nil
}

// StderrPipe 返回一个管道，命令启动时该管道会连接到命令的标准错误。
//
// [Cmd.Wait] 会在看到命令退出后关闭该管道，因此大多数调用者无需自行关闭它。
// 因此，在从管道读取的所有操作完成之前调用 Wait 是不正确的。
// 同理，在使用 StderrPipe 时使用 [Cmd.Run] 也是不正确的。
// 惯用法参见 StdoutPipe 的示例。
func (c *Cmd) StderrPipe() (io.ReadCloser, error) {
	if c.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	if c.Process != nil {
		return nil, errors.New("exec: StderrPipe after process started")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stderr = pw
	c.childIOFiles = append(c.childIOFiles, pw)
	c.parentIOPipes = append(c.parentIOPipes, pr)
	return pr, nil
}

// prefixSuffixSaver 是一个 io.Writer，它保留写入其中的前 N 个字节和
// 后 N 个字节。Bytes() 方法会用一条漂亮的错误消息将其重建。
type prefixSuffixSaver struct {
	N         int // 前缀或后缀的最大大小
	prefix    []byte
	suffix    []byte // 一旦 len(suffix) == N 就成为环形缓冲区
	suffixOff int    // 写入 suffix 的偏移量
	skipped   int64

	// TODO(bradfitz)：我们可以只保留一个大的 []byte，用其中一部分作为前缀，
	// 为 '... Omitting N bytes ...' 消息预留空间，然后是环形缓冲区后缀，
	// 并在调用 Bytes() 时只需重新排列环形缓冲区后缀，但就目前而言，
	// 仅仅为了错误消息似乎不值得这样做。反正它也只有约 64KB。
}

func (w *prefixSuffixSaver) Write(p []byte) (n int, err error) {
	lenp := len(p)
	p = w.fill(&w.prefix, p)

	// 只保留 suffix 数据的最后 w.N 个字节。
	if overage := len(p) - w.N; overage > 0 {
		p = p[overage:]
		w.skipped += int64(overage)
	}
	p = w.fill(&w.suffix, p)

	// 如果 p 非空，说明 w.suffix 现在已满。以环形方式覆盖它。
	for len(p) > 0 { // 0、1 或 2 次迭代。
		n := copy(w.suffix[w.suffixOff:], p)
		p = p[n:]
		w.skipped += int64(n)
		w.suffixOff += n
		if w.suffixOff == w.N {
			w.suffixOff = 0
		}
	}
	return lenp, nil
}

// fill 将 p 中最多 len(p) 个字节追加到 *dst，使得 *dst 不会增长到
// 超过 w.N。它返回 p 中未被追加的后缀部分。
func (w *prefixSuffixSaver) fill(dst *[]byte, p []byte) (pRemain []byte) {
	if remain := w.N - len(*dst); remain > 0 {
		add := min(len(p), remain)
		*dst = append(*dst, p[:add]...)
		p = p[add:]
	}
	return p
}

func (w *prefixSuffixSaver) Bytes() []byte {
	if w.suffix == nil {
		return w.prefix
	}
	if w.skipped == 0 {
		return append(w.prefix, w.suffix...)
	}
	var buf bytes.Buffer
	buf.Grow(len(w.prefix) + len(w.suffix) + 50)
	buf.Write(w.prefix)
	buf.WriteString("\n... omitting ")
	buf.WriteString(strconv.FormatInt(w.skipped, 10))
	buf.WriteString(" bytes ...\n")
	buf.Write(w.suffix[w.suffixOff:])
	buf.Write(w.suffix[:w.suffixOff])
	return buf.Bytes()
}

// environ 返回命令在当前配置下运行时所处环境的尽力而为的副本。
// 如果在计算环境时发生错误，它会与这份尽力而为的副本一同返回。
func (c *Cmd) environ() ([]string, error) {
	var err error

	env := c.Env
	if env == nil {
		env, err = execenv.Default(c.SysProcAttr)
		if err != nil {
			env = os.Environ()
			// 注意，尽管 env 被覆盖，非 nil 的 err 仍会被保留。
		}

		if c.Dir != "" {
			switch runtime.GOOS {
			case "windows", "plan9":
				// Windows 和 Plan 9 不使用 PWD 变量，因此我们无需保持其准确。
			default:
				// 在 POSIX 平台上，PWD 表示“当前工作目录的绝对路径名”。
				// 由于我们要更改命令的工作目录，我们也应该更新 PWD 以反映这一点。
				//
				// 不幸的是，我们并非总是这样做，因此（如
				// https://go.dev/issue/50599 中所提议的）为了避免意外的连带
				// 损害，我们仅在 Env 为 nil 时才隐式更新 PWD。这样一来，
				// 我们就更不可能覆盖对该变量有意做出的更改。
				if pwd, absErr := filepath.Abs(c.Dir); absErr == nil {
					env = append(env, "PWD="+pwd)
				} else if err == nil {
					err = absErr
				}
			}
		}
	}

	env, dedupErr := dedupEnv(env)
	if err == nil {
		err = dedupErr
	}
	return addCriticalEnv(env), err
}

// Environ 返回命令在当前配置下运行时所处环境的副本。
func (c *Cmd) Environ() []string {
	//  有意忽略错误：environ 无论如何都会返回尽力而为的环境。
	env, _ := c.environ()
	return env
}

// dedupEnv 返回 env 的副本，其中任何重复项都被移除，优先保留靠后的值。
// 不符合正常环境 "key=value" 形式的项会原样保留。
// 除 Plan 9 外，包含 NUL 字符的项会被移除，并与其余值一同返回一个错误。
func dedupEnv(env []string) ([]string, error) {
	return dedupEnvCase(runtime.GOOS == "windows", runtime.GOOS == "plan9", env)
}

// dedupEnvCase 是带大小写选项（用于测试）的 dedupEnv。
// 如果 caseInsensitive 为 true，则忽略键的大小写。
// 如果 nulOK 为 false，则允许包含 NUL 字符的项。
func dedupEnvCase(caseInsensitive, nulOK bool, env []string) ([]string, error) {
	// 以相反顺序构造输出，以保留每个键的最后一次出现。
	var err error
	out := make([]string, 0, len(env))
	saw := make(map[string]bool, len(env))
	for n := len(env); n > 0; n-- {
		kv := env[n-1]

		// 拒绝环境变量中的 NUL 以防止安全问题（#56284）；
		// 但 Plan 9 除外，它将 NUL 用作 os.PathListSeparator（#56544）。
		if !nulOK && strings.IndexByte(kv, 0) != -1 {
			err = errors.New("exec: environment variable contains NUL")
			continue
		}

		i := strings.Index(kv, "=")
		if i == 0 {
			// 我们在实践中观察到 Windows 上存在带单个前导 "=" 的键。
			// TODO(#49886)：我们应只将第一个前导 "=" 作为键的一部分，
			// 还是任意解析多个前导 "=" 直到遇到非 "="？
			i = strings.Index(kv[1:], "=") + 1
		}
		if i < 0 {
			if kv != "" {
				// 该条目不符合 "key=value" 形式（而这是它应该具有的形式）。
				// 暂且原样保留。
				// TODO(#52436)：我们应该剥离还是拒绝这些伪造的条目？
				out = append(out, kv)
			}
			continue
		}
		k := kv[:i]
		if caseInsensitive {
			k = strings.ToLower(k)
		}
		if saw[k] {
			continue
		}

		saw[k] = true
		out = append(out, kv)
	}

	// 现在反转切片，以恢复原始顺序。
	for i := 0; i < len(out)/2; i++ {
		j := len(out) - i - 1
		out[i], out[j] = out[j], out[i]
	}

	return out, err
}

// addCriticalEnv 添加操作系统上所需（或至少几乎总是所需）的任何关键环境变量。
// 目前这仅用于 Windows。
func addCriticalEnv(env []string) []string {
	if runtime.GOOS != "windows" {
		return env
	}
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(k, "SYSTEMROOT") {
			// 我们已经有了它。
			return env
		}
	}
	return append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
}

// ErrDot 表示由于路径中存在 ‘.’（无论是隐式还是显式），路径查找解析到了
// 当前目录中的可执行文件。详情参见包文档。
//
// 注意，本包中的函数不会直接返回 ErrDot。
// 代码应使用 errors.Is(err, ErrDot) 而不是 err == ErrDot，
// 来测试返回的错误 err 是否由该情况引起。
var ErrDot = errors.New("cannot run executable found relative to current directory")

// validateLookPath 排除那些不可能成为有效可执行文件名的路径。
// 参见 issue #74466 和 CVE-2025-47906。
func validateLookPath(s string) error {
	switch s {
	case "", ".", "..":
		return ErrNotFound
	}
	return nil
}
