// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testing 为 Go 包的自动化测试提供支持。
// 它旨在与 "go test" 命令配合使用，该命令会自动执行任何形式的函数
//
//	func TestXxx(*testing.T)
//
// 其中 Xxx 不以小写字母开头。函数名用于标识该测试例程。
//
// 在这些函数中，使用 [T.Error]、[T.Fail] 或相关方法来表示失败。
//
// 要编写新的测试套件，请创建一个文件，其中包含此处所述的 TestXxx 函数，
// 并将该文件命名为以 "_test.go" 结尾。
// 该文件将被排除在常规包构建之外，但在运行 "go test" 命令时会被包含进来。
//
// 测试文件可以与被测包位于同一包中，
// 也可以位于带有后缀 "_test" 的对应包中。
//
// 如果测试文件位于同一包中，它可以引用包内未导出的标识符，如下例所示：
//
//	package abs
//
//	import "testing"
//
//	func TestAbs(t *testing.T) {
//	    got := abs(-1)
//	    if got != 1 {
//	        t.Errorf("abs(-1) = %d; want 1", got)
//	    }
//	}
//
// 如果文件位于单独的 "_test" 包中，则必须显式导入被测包，
// 并且只能使用其导出的标识符。这被称为 "黑盒" 测试。
//
//	package abs_test
//
//	import (
//		"testing"
//
//		"path_to_pkg/abs"
//	)
//
//	func TestAbs(t *testing.T) {
//	    got := abs.Abs(-1)
//	    if got != 1 {
//	        t.Errorf("Abs(-1) = %d; want 1", got)
//	    }
//	}
//
// 更多详情，请运行 [go help test] 和 [go help testflag]。
//
// # 基准测试
//
// 形式如下的函数
//
//	func BenchmarkXxx(*testing.B)
//
// 被视为基准测试，当提供 -bench 标志时由 "go test" 命令执行。基准测试按顺序运行。
//
// 关于测试标志的说明，请参阅 [go help testflag]。
//
// 一个基准测试函数示例看起来像这样：
//
//	func BenchmarkRandInt(b *testing.B) {
//	    for b.Loop() {
//	        rand.Int()
//	    }
//	}
//
// 输出
//
//	BenchmarkRandInt-8   	68453040	        17.8 ns/op
//
// 表示循环体运行了 68453040 次，每次循环的速度为 17.8 ns。
//
// 只对循环体计时，因此基准测试可以在调用 b.Loop 之前进行耗时的准备工作，
// 这些准备不会被计入基准测试的测量结果：
//
//	func BenchmarkBigLen(b *testing.B) {
//	    big := NewBig()
//	    for b.Loop() {
//	        big.Len()
//	    }
//	}
//
// 如果基准测试需要在并行环境下测试性能，可以使用
// RunParallel 辅助函数；此类基准测试旨在与 go test -cpu 标志一起使用：
//
//	func BenchmarkTemplateParallel(b *testing.B) {
//	    templ := template.Must(template.New("test").Parse("Hello, {{.}}!"))
//	    b.RunParallel(func(pb *testing.PB) {
//	        var buf bytes.Buffer
//	        for pb.Next() {
//	            buf.Reset()
//	            templ.Execute(&buf, "World")
//	        }
//	    })
//	}
//
// 基准测试结果格式的详细规范见
// https://go.dev/design/14313-benchmark-format。
//
// 在 [golang.org/x/perf/cmd] 中有处理基准测试结果的标准工具。
// 特别是，[golang.org/x/perf/cmd/benchstat] 执行
// 统计上稳健的 A/B 比较。
//
// # b.N 风格的基准测试
//
// 在引入 [B.Loop] 之前，基准测试使用 B.N 以不同的风格编写。例如：
//
//	func BenchmarkRandInt(b *testing.B) {
//	    for range b.N {
//	        rand.Int()
//	    }
//	}
//
// 在这种风格的基准测试中，基准测试函数必须运行目标代码 b.N 次。
// 基准测试函数会被多次调用，并调整 b.N，直到基准测试函数持续足够长的时间
// 以进行可靠计时。这也意味着循环之前执行的任何准备工作可能会运行多次。
//
// 如果基准测试在运行前需要一些耗时的准备工作，应显式重置计时器：
//
//	func BenchmarkBigLen(b *testing.B) {
//	    big := NewBig()
//	    b.ResetTimer()
//	    for range b.N {
//	        big.Len()
//	    }
//	}
//
// 新的基准测试应优先使用 [B.Loop]，它更稳健也更高效。
//
// # 示例
//
// 该包还会运行并验证示例代码。示例函数可以包含一个以 "Output:" 开头的
// 结尾行注释，在运行测试时会将其与该函数的标准输出进行比较。
// （比较会忽略前导和尾随空格。）以下是一个示例的示例：
//
//	func ExampleHello() {
//	    fmt.Println("hello")
//	    // Output: hello
//	}
//
//	func ExampleSalutations() {
//	    fmt.Println("hello, and")
//	    fmt.Println("goodbye")
//	    // Output:
//	    // hello, and
//	    // goodbye
//	}
//
// 注释前缀 "Unordered output:" 类似于 "Output:"，但匹配任意行顺序：
//
//	func ExamplePerm() {
//	    for _, value := range Perm(5) {
//	        fmt.Println(value)
//	    }
//	    // Unordered output: 4
//	    // 2
//	    // 1
//	    // 3
//	    // 0
//	}
//
// 没有输出注释的示例函数会被编译但不会被执行。
//
// 为包、函数 F、类型 T 以及类型 T 上的方法 M 声明示例的命名约定为：
//
//	func Example() { ... }
//	func ExampleF() { ... }
//	func ExampleT() { ... }
//	func ExampleT_M() { ... }
//
// 通过为名称附加不同的后缀，可以为包/类型/函数/方法提供多个示例函数。
// 后缀必须以小写字母开头。
//
//	func Example_suffix() { ... }
//	func ExampleF_suffix() { ... }
//	func ExampleT_suffix() { ... }
//	func ExampleT_M_suffix() { ... }
//
// 当整个测试文件只包含一个示例函数、至少一个其他函数/类型/变量/常量
// 声明，且不包含任何测试或基准测试函数时，整个测试文件会作为示例呈现。
//
// # 模糊测试
//
// 'go test' 和 testing 包支持模糊测试（fuzzing），这是一种用随机生成的
// 输入调用函数以发现单元测试未预料到的缺陷的测试技术。
//
// 形式如下的函数
//
//	func FuzzXxx(*testing.F)
//
// 被视为模糊测试。
//
// 例如：
//
//	func FuzzHex(f *testing.F) {
//	  for _, seed := range [][]byte{{}, {0}, {9}, {0xa}, {0xf}, {1, 2, 3, 4}} {
//	    f.Add(seed)
//	  }
//	  f.Fuzz(func(t *testing.T, in []byte) {
//	    enc := hex.EncodeToString(in)
//	    out, err := hex.DecodeString(enc)
//	    if err != nil {
//	      t.Fatalf("%v: decode: %v", in, err)
//	    }
//	    if !bytes.Equal(in, out) {
//	      t.Fatalf("%v: not equal after round trip: %v", in, out)
//	    }
//	  })
//	}
//
// 模糊测试会维护一个种子语料库（seed corpus），即一组默认运行的输入，
// 并可为输入生成提供种子。种子输入可以通过调用 [F.Add] 注册，
// 也可以通过将文件存储在包含模糊测试的包内的 testdata/fuzz/<Name>
// 目录中（其中 <Name> 是模糊测试的名称）。种子输入是可选的，
// 但当提供一组具有良好代码覆盖率的小型种子输入时，模糊测试引擎可以更高效地
// 发现缺陷。这些种子输入也可以用作通过模糊测试发现的缺陷的回归测试。
//
// 模糊测试中传递给 [F.Fuzz] 的函数被视为模糊测试目标（fuzz target）。
// 模糊测试目标必须接受一个 [*T] 参数，后跟一个或多个用于随机输入的参数。
// 传递给 [F.Add] 的参数类型必须与这些参数的类型完全一致。
// 模糊测试目标可以像测试一样以相同方式表示发现了问题：
// 通过调用 [T.Fail]（或任何调用它的方法，如 [T.Error] 或 [T.Fatal]）或通过 panic。
//
// 当启用模糊测试时（通过将 -fuzz 标志设置为匹配特定模糊测试的正则表达式），
// 会使用通过对种子输入反复进行随机更改而生成的参数来调用模糊测试目标。
// 在支持的平台上，'go test' 会使用模糊测试覆盖率插桩来编译测试可执行文件。
// 模糊测试引擎利用该插桩来查找并缓存能扩大覆盖率的输入，从而提高
// 发现缺陷的可能性。如果模糊测试目标对某个输入失败，模糊测试引擎
// 会将导致失败的输入写入包目录内 testdata/fuzz/<Name> 目录中的文件。
// 该文件随后会作为种子输入。如果无法写入该位置（例如，
// 因为目录是只读的），模糊测试引擎会改为将该文件写入构建缓存内的
// 模糊测试缓存目录。
//
// 当模糊测试被禁用时，会使用通过 [F.Add] 注册的种子输入以及来自
// testdata/fuzz/<Name> 的种子输入来调用模糊测试目标。在此模式下，
// 模糊测试的行为很像常规测试，其子测试由 [F.Fuzz] 而非 [T.Run] 启动。
//
// 关于模糊测试的文档，请参阅 https://go.dev/doc/fuzz。
//
// # 跳过
//
// 测试或基准测试可以在运行时通过调用 [T.Skip] 或 [B.Skip] 来跳过：
//
//	func TestTimeConsuming(t *testing.T) {
//	    if testing.Short() {
//	        t.Skip("skipping test in short mode.")
//	    }
//	    ...
//	}
//
// 如果输入无效，可以在模糊测试目标中使用 [T.Skip] 方法，
// 但不应将其视为导致失败的输入。例如：
//
//	func FuzzJSONMarshaling(f *testing.F) {
//	    f.Fuzz(func(t *testing.T, b []byte) {
//	        var v interface{}
//	        if err := json.Unmarshal(b, &v); err != nil {
//	            t.Skip()
//	        }
//	        if _, err := json.Marshal(v); err != nil {
//	            t.Errorf("Marshal: %v", err)
//	        }
//	    })
//	}
//
// # 子测试与子基准测试
//
// [T.Run] 和 [B.Run] 方法允许定义子测试和子基准测试，
// 而无需为每个分别定义函数。这使得表驱动基准测试和创建
// 分层测试等用法成为可能。它还提供了一种共享公共
// 准备和清理代码的方式：
//
//	func TestFoo(t *testing.T) {
//	    // <setup code>
//	    t.Run("A=1", func(t *testing.T) { ... })
//	    t.Run("A=2", func(t *testing.T) { ... })
//	    t.Run("B=1", func(t *testing.T) { ... })
//	    // <tear-down code>
//	}
//
// 每个子测试和子基准测试都有一个唯一名称：顶层测试的名称与
// 传递给 Run 的名称序列的组合，以斜杠分隔，并可选择附带一个
// 用于消歧的尾随序号。
//
// -run、-bench 和 -fuzz 命令行标志的参数是一个不锚定的正则表达式，
// 用于匹配测试的名称。对于包含多个以斜杠分隔元素的测试（例如子测试），
// 该参数本身也以斜杠分隔，其中的表达式依次匹配每个名称元素。
// 由于它是不锚定的，空表达式会匹配任意字符串。
// 例如，用 "匹配" 表示 "其名称包含"：
//
//	go test -run ''        # Run all tests.
//	go test -run Foo       # Run top-level tests matching "Foo", such as "TestFooBar".
//	go test -run Foo/A=    # For top-level tests matching "Foo", run subtests matching "A=".
//	go test -run /A=1      # For all top-level tests, run subtests matching "A=1".
//	go test -fuzz FuzzFoo  # Fuzz the target matching "FuzzFoo"
//
// -run 参数也可以用于运行种子语料库中的特定值，以便调试。例如：
//
//	go test -run=FuzzFoo/9ddb952d9814
//
// 可以同时设置 -fuzz 和 -run 标志，以便对某个目标进行模糊测试，
// 但跳过所有其他测试的执行。
//
// 子测试还可以用于控制并行性。父测试只有在所有子测试完成后才会
// 完成。在此示例中，所有测试都彼此并行运行，且仅彼此并行运行，
// 无论可能定义了哪些其他顶层测试：
//
//	func TestGroupedParallel(t *testing.T) {
//	    for _, tc := range tests {
//	        t.Run(tc.Name, func(t *testing.T) {
//	            t.Parallel()
//	            ...
//	        })
//	    }
//	}
//
// Run 会等到并行的子测试全部完成后才返回，这提供了一种
// 在一组并行测试之后进行清理的方式：
//
//	func TestTeardownParallel(t *testing.T) {
//	    // This Run will not return until the parallel tests finish.
//	    t.Run("group", func(t *testing.T) {
//	        t.Run("Test1", parallelTest1)
//	        t.Run("Test2", parallelTest2)
//	        t.Run("Test3", parallelTest3)
//	    })
//	    // <tear-down code>
//	}
//
// # Main
//
// 有时测试或基准测试程序需要在执行前后进行额外的准备或清理。
// 有时还需要控制哪些代码在主线程上运行。为支持这些
// 及其他情况，如果测试文件包含如下函数：
//
//	func TestMain(m *testing.M)
//
// 那么生成的测试将调用 TestMain(m)，而不是直接运行测试或基准测试。
// TestMain 在主 goroutine 中运行，可以在调用 m.Run 前后进行任何必要的
// 准备和清理工作。m.Run 会返回一个退出码，可将其传递给 [os.Exit]。
// 如果 TestMain 返回，测试包装器会自行将 m.Run 的结果传递给 [os.Exit]。
//
// 调用 TestMain 时，flag.Parse 尚未运行。如果 TestMain 依赖于
// 命令行标志（包括 testing 包的标志），它应显式调用 [flag.Parse]。
// 当测试或基准测试函数运行时，命令行标志始终已被解析。
//
// TestMain 的一个简单实现是：
//
//	func TestMain(m *testing.M) {
//		// call flag.Parse() here if TestMain uses flags
//		m.Run()
//	}
//
// TestMain 是一个底层原语，对于普通的测试需求（普通测试函数即可满足）
// 应该是不必要的。
//
// [go help test]: https://pkg.go.dev/cmd/go#hdr-Test_packages
// [go help testflag]: https://pkg.go.dev/cmd/go#hdr-Testing_flags
package testing

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"internal/race"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"runtime/trace"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	_ "unsafe" // 用于 linkname
)

var initRan bool

var (
	parallelStart atomic.Int64 // 已启动的并行测试数量
	parallelStop  atomic.Int64 // 已停止的并行测试数量
)

// Init 注册 testing 标志。这些标志由 "go test" 命令在运行测试函数前自动注册，
// 因此只有在不使用 "go test" 而调用诸如 Benchmark 之类的函数时才需要 Init。
//
// Init 并发调用不安全。如果已经调用过，则不会产生任何效果。
func Init() {
	if initRan {
		return
	}
	initRan = true
	// short 标志请求测试运行得更快，但其功能由测试编写者自己提供。
	// testing 包只是它的归属。all.bash 安装脚本会设置它以使安装更高效，
	// 但默认情况下该标志是关闭的，这样普通的 "go test" 会执行包的完整测试。
	short = flag.Bool("test.short", false, "run smaller test suite to save time")

	// failfast 标志请求在第一次测试失败后停止测试执行。
	failFast = flag.Bool("test.failfast", false, "do not start new tests after the first test failure")

	// 用于创建 profile 文件等的目录。当从 "go test" 运行时，
	// 二进制文件总是在包的源目录中运行；此标志让 "go test" 告诉
	// 二进制文件将文件写到运行 "go test" 命令所在的目录。
	outputDir = flag.String("test.outputdir", "", "write profiles to `dir`")
	artifacts = flag.Bool("test.artifacts", false, "store test artifacts in test.,outputdir")
	// 在测试运行时进行报告；默认成功时保持静默。
	flag.Var(&chatty, "test.v", "verbose: print additional output")
	count = flag.Uint("test.count", 1, "run tests and benchmarks `n` times")
	coverProfile = flag.String("test.coverprofile", "", "write a coverage profile to `file`")
	gocoverdir = flag.String("test.gocoverdir", "", "write coverage intermediate files to this directory")
	matchList = flag.String("test.list", "", "list tests, examples, and benchmarks matching `regexp` then exit")
	match = flag.String("test.run", "", "run only tests and examples matching `regexp`")
	skip = flag.String("test.skip", "", "do not list or run tests matching `regexp`")
	memProfile = flag.String("test.memprofile", "", "write an allocation profile to `file`")
	memProfileRate = flag.Int("test.memprofilerate", 0, "set memory allocation profiling `rate` (see runtime.MemProfileRate)")
	cpuProfile = flag.String("test.cpuprofile", "", "write a cpu profile to `file`")
	blockProfile = flag.String("test.blockprofile", "", "write a goroutine blocking profile to `file`")
	blockProfileRate = flag.Int("test.blockprofilerate", 1, "set blocking profile `rate` (see runtime.SetBlockProfileRate)")
	mutexProfile = flag.String("test.mutexprofile", "", "write a mutex contention profile to the named file after execution")
	mutexProfileFraction = flag.Int("test.mutexprofilefraction", 1, "if >= 0, calls runtime.SetMutexProfileFraction()")
	panicOnExit0 = flag.Bool("test.paniconexit0", false, "panic on call to os.Exit(0)")
	traceFile = flag.String("test.trace", "", "write an execution trace to `file`")
	timeout = flag.Duration("test.timeout", 0, "panic test binary after duration `d` (default 0, timeout disabled)")
	cpuListStr = flag.String("test.cpu", "", "comma-separated `list` of cpu counts to run each test with")
	parallel = flag.Int("test.parallel", runtime.GOMAXPROCS(0), "run at most `n` tests in parallel")
	testlog = flag.String("test.testlogfile", "", "write test action log to `file` (for use only by cmd/go)")
	shuffle = flag.String("test.shuffle", "off", "randomize the execution order of tests and benchmarks")
	fullPath = flag.Bool("test.fullpath", false, "show full file names in error messages")

	initBenchmarkFlags()
	initFuzzFlags()
}

var (
	// 在 Init 期间注册的标志。
	short                *bool
	failFast             *bool
	outputDir            *string
	artifacts            *bool
	chatty               chattyFlag
	count                *uint
	coverProfile         *string
	gocoverdir           *string
	matchList            *string
	match                *string
	skip                 *string
	memProfile           *string
	memProfileRate       *int
	cpuProfile           *string
	blockProfile         *string
	blockProfileRate     *int
	mutexProfile         *string
	mutexProfileFraction *int
	panicOnExit0         *bool
	traceFile            *string
	timeout              *time.Duration
	cpuListStr           *string
	parallel             *int
	shuffle              *string
	testlog              *string
	fullPath             *bool

	haveExamples bool // 是否有示例？

	cpuList     []int
	testlogFile *os.File
	artifactDir string

	numFailed atomic.Uint32 // 测试失败次数

	running sync.Map // map[string]time.Time，记录正在运行、未暂停的测试
)

type chattyFlag struct {
	on   bool // 以某种形式设置了 -v
	json bool // 设置了 -v=test2json，使输出对 test2json 更友好
}

func (*chattyFlag) IsBoolFlag() bool { return true }

func (f *chattyFlag) Set(arg string) error {
	switch arg {
	default:
		return fmt.Errorf("invalid flag -test.v=%s", arg)
	case "true", "test2json":
		f.on = true
		f.json = arg == "test2json"
	case "false":
		f.on = false
		f.json = false
	}
	return nil
}

func (f *chattyFlag) String() string {
	if f.json {
		return "test2json"
	}
	if f.on {
		return "true"
	}
	return "false"
}

func (f *chattyFlag) Get() any {
	if f.json {
		return "test2json"
	}
	return f.on
}

const marker = byte(0x16) // ^V，用于分帧

func (f *chattyFlag) prefix() string {
	if f.json {
		return string(marker)
	}
	return ""
}

type chattyPrinter struct {
	w          io.Writer
	lastNameMu sync.Mutex // 保护 lastName
	lastName   string     // 在 chatty 模式下最后打印的测试名称
	json       bool       // -v=json 输出模式
}

func newChattyPrinter(w io.Writer) *chattyPrinter {
	return &chattyPrinter{w: w, json: chatty.json}
}

// prefix 类似于 chatty.prefix，但使用 p.json 而非 chatty.json。
// 使用 p.json 允许测试在不修改全局变量的情况下检查 json 行为。
// 为方便起见，我们允许 p == nil，并将其视为非 json 模式
// （因为它根本不是 chatty）。
func (p *chattyPrinter) prefix() string {
	if p != nil && p.json {
		return string(marker)
	}
	return ""
}

// Updatef 向 w 打印有关指定测试状态的消息。
//
// 格式化后的消息必须包含测试名称本身。
func (p *chattyPrinter) Updatef(testName, format string, args ...any) {
	p.lastNameMu.Lock()
	defer p.lastNameMu.Unlock()

	// 由于该消息已经暗示与某个特定的新测试相关联，
	// 我们无需检查旧的测试名称是什么，也无需为它额外记录一行 NAME。
	// （我们反正都会更新它，而且当前消息已经包含了测试名称。）
	p.lastName = testName
	fmt.Fprintf(p.w, p.prefix()+format, args...)
}

// Printf 打印由指定测试生成的消息，该消息不一定提及该测试的名称本身。
func (p *chattyPrinter) Printf(testName, format string, args ...any) {
	p.lastNameMu.Lock()
	defer p.lastNameMu.Unlock()

	if p.lastName == "" {
		p.lastName = testName
	} else if p.lastName != testName {
		fmt.Fprintf(p.w, "%s=== NAME  %s\n", p.prefix(), testName)
		p.lastName = testName
	}

	fmt.Fprintf(p.w, format, args...)
}

// 在为了修饰日志消息而跳过辅助函数时，允许经过的最大栈帧数。
const maxStackLen = 50

// common 保存 T 和 B 之间共有的元素，并
// 包含诸如 Errorf 之类的共有方法。
type common struct {
	mu          sync.RWMutex         // 保护这组字段
	output      []byte               // 由测试或基准测试生成的输出。
	w           io.Writer            // 用于 flushToParent。
	o           *outputWriter        // 写入输出。
	ran         bool                 // 测试或基准测试（或其某个子测试）已被执行。
	failed      bool                 // 测试或基准测试已失败。
	skipped     bool                 // 测试或基准测试已被跳过。
	done        bool                 // 测试已完成且所有子测试也已完成。
	helperPCs   map[uintptr]struct{} // 在写入文件/行信息时要跳过的函数
	helperNames map[string]struct{}  // helperPCs 转换后的函数名
	cleanups    []func()             // 在测试结束时调用的可选函数
	cleanupName string               // cleanup 函数的名称。
	cleanupPc   []uintptr            // 调用 Cleanup 时的栈回溯。
	finished    bool                 // 测试函数已完成。
	inFuzzFn    bool                 // 如果这是模糊测试目标，表示它是否正在运行。
	isSynctest  bool

	chatty         *chattyPrinter // 若设置了 chatty 标志，则为 chattyPrinter 的副本。
	bench          bool           // 当前测试是否为基准测试。
	hasSub         atomic.Bool    // 是否存在子基准测试。
	cleanupStarted atomic.Bool    // 已注册的 cleanup 回调是否已开始执行
	runner         string         // 运行该测试的 tRunner 的函数名。
	isParallel     bool           // 该测试是否为并行的。

	parent     *common
	level      int       // 测试或基准测试的嵌套深度。
	creator    []uintptr // 如果 level > 0，则为父级调用 t.Run 时的栈回溯。
	modulePath string
	importPath string
	name       string            // 测试或基准测试的名称。
	start      highPrecisionTime // 测试或基准测试开始的时间
	duration   time.Duration
	barrier    chan bool // 用于通知并行的子测试它们可以启动。未使用 T.Parallel 时（B）或不可用时（模糊测试时）为 nil。
	signal     chan bool // 用于通知测试已完成。
	sub        []*T      // 将要并行运行的子测试队列。

	lastRaceErrors  atomic.Int64 // 测试或其子测试期间看到的 race.Errors 的最大值。
	raceErrorLogged atomic.Bool

	tempDirMu  sync.Mutex
	tempDir    string
	tempDirErr error
	tempDirSeq int32

	artifactDirOnce sync.Once
	artifactDir     string
	artifactDirErr  error

	ctx       context.Context
	cancelCtx context.CancelFunc
}

// Short 报告是否设置了 -test.short 标志。
func Short() bool {
	if short == nil {
		panic("testing: Short called before Init")
	}
	// 捕获那些在未先调用 flag.Parse 的情况下从 TestMain 调用此函数的代码。
	if !flag.Parsed() {
		panic("testing: Short called before Parse")
	}

	return *short
}

// testBinary 由 cmd/go 设置为 "1"，表示这是由 "go test" 构建的二进制文件。
// 该值由 cmd/link 的 -X 选项设置为 "1"。我们假设正因为这是可能的，
// 编译器不会以“这是一个从未改变的未导出包级变量”为由将 testBinary
// 优化为常量。如果编译器将来开始实现这样的优化，
// 我们就需要某种技术将该变量标记为“由 cmd/link -X 选项更改”。
var testBinary = "0"

// Testing 报告当前代码是否正在测试中运行。
// 在由 "go test" 创建的程序中它会报告 true，
// 在由 "go build" 创建的程序中报告 false。
func Testing() bool {
	return testBinary == "1"
}

// CoverMode 报告测试覆盖率模式被设置为什么。
// 取值为 "set"、"count" 或 "atomic"。如果未启用测试覆盖率，
// 返回值将为空。
func CoverMode() string {
	return cover.mode
}

// Verbose 报告是否设置了 -test.v 标志。
func Verbose() bool {
	// 与 Short 中相同。
	if !flag.Parsed() {
		panic("testing: Verbose called before Parse")
	}
	return chatty.on
}

func (c *common) checkFuzzFn(name string) {
	if c.inFuzzFn {
		panic(fmt.Sprintf("testing: f.%s was called inside the fuzz target, use t.%s instead", name, name))
	}
}

// frameSkip 从跳过 skip 个帧之后开始搜索，寻找第一个位于
// 未被标记为 helper 的函数中的调用者帧，并返回该帧。
// 如果它找到某个 tRunner 函数，且该函数是进入测试的入口点
// 并且该测试不是子测试，则停止搜索。
// 调用此函数时必须持有 c.mu。
func (c *common) frameSkip(skip int) runtime.Frame {
	// 如果搜索继续进入父测试，我们将不得不临时持有
	// 其 mu。如果随后返回，我们需要将其解锁。
	shouldUnlock := false
	defer func() {
		if shouldUnlock {
			c.mu.Unlock()
		}
	}()
	var pc [maxStackLen]uintptr
	// 多跳过两个帧，以计入此函数
	// 和 runtime.Callers 本身。
	n := runtime.Callers(skip+2, pc[:])
	if n == 0 {
		panic("testing: zero callers found")
	}
	frames := runtime.CallersFrames(pc[:n])
	var firstFrame, prevFrame, frame runtime.Frame
	for more := true; more; prevFrame = frame {
		frame, more = frames.Next()
		if frame.Function == "runtime.gopanic" {
			continue
		}
		if frame.Function == c.cleanupName {
			frames = runtime.CallersFrames(c.cleanupPc)
			continue
		}
		if firstFrame.PC == 0 {
			firstFrame = frame
		}
		if frame.Function == c.runner {
			// 我们已经一路向上找到调用测试函数的 tRunner
			// （因此用户一定是在该测试函数内部调用了 tb.Helper）。
			// 如果这是顶层测试，只跳到测试函数本身为止。
			// 如果我们处于子测试中，则在父测试中继续搜索，
			// 从创建该子测试的 Run 调用点开始。
			if c.level > 1 {
				frames = runtime.CallersFrames(c.creator)
				parent := c.parent
				// 在此之后我们不再关注当前的 c，
				// 因此应该解锁它的 mu，除非它是原始的接收者，
				// 在这种情况下我们的调用者不希望我们那样做。
				if shouldUnlock {
					c.mu.Unlock()
				}
				c = parent
				// 记住在我们不再需要 c.mu 时将其解锁，无论是因为
				// 我们又向上了一层嵌套，还是因为返回了。
				shouldUnlock = true
				c.mu.Lock()
				continue
			}
			return prevFrame
		}
		// 如果自上次进行转换以来又添加了更多 helper PC
		if c.helperNames == nil {
			c.helperNames = make(map[string]struct{})
			for pc := range c.helperPCs {
				c.helperNames[pcToName(pc)] = struct{}{}
			}
		}
		if _, ok := c.helperNames[frame.Function]; !ok {
			// 找到了一帧不在 helper 函数内的帧。
			return frame
		}
	}
	return firstFrame
}

// flushToParent 先使用给定的格式和参数写入头部，
// 然后将 c.output 写入父级。
func (c *common) flushToParent(testName, format string, args ...any) {
	p := c.parent
	p.mu.Lock()
	defer p.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.output) > 0 {
		// 将当前的 c.output 添加到打印中，
		// 然后安排该打印替换 c.output。
		// （这会在 --- FAIL 行之后显示记录的输出。）
		format += "%s"
		args = append(args[:len(args):len(args)], c.output)
		c.output = c.output[:0]
	}

	if c.chatty != nil && (p.w == c.chatty.w || c.chatty.json) {
		// 我们正在刷新到实际的输出，因此要追踪此输出与某个特定测试
		// 相关联（具体来说，下一个输出*不*与该测试相关联）。
		//
		// 此外，如果 c.output 非空，则本次写入相对于其他测试的输出必须是
		// 原子的，这样我们就不会在我们的 '--- PASS' 块中间出现令人困惑的
		// '=== NAME' 行。无论是人还是 cmd/test2json 都不容易解析这些内容。
		// （参见 https://go.dev/issue/40771。）
		//
		// 如果使用了 test2json，我们绝不刷新到父测试，
		// 这样 json 流会在子测试完成时显示它们。
		// （参见 https://go.dev/issue/29811。）
		c.chatty.Updatef(testName, format, args...)
	} else {
		// 我们正在刷新到父测试的输出缓冲区，当它最终刷新到 stdout 时，
		// 其自身会跟随一个测试名称头部。
		fmt.Fprintf(p.w, c.chatty.prefix()+format, args...)
	}
}

type indenter struct {
	c *common
}

const indent = "    "

func (w indenter) Write(b []byte) (n int, err error) {
	n = len(b)
	for len(b) > 0 {
		end := bytes.IndexByte(b, '\n')
		if end == -1 {
			end = len(b)
		} else {
			end++
		}
		// 4 个空格的缩进会将短横线整齐地与父级的状态指示符对齐。
		line := b[:end]
		if line[0] == marker {
			w.c.output = append(w.c.output, marker)
			line = line[1:]
		}
		w.c.output = append(w.c.output, indent...)
		w.c.output = append(w.c.output, line...)
		b = b[end:]
	}
	return
}

// fmtDuration 返回以 "87.00s" 形式表示 d 的字符串。
func fmtDuration(d time.Duration) string {
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// TB 是 [T]、[B] 和 [F] 共有的接口。
type TB interface {
	ArtifactDir() string
	Attr(key, value string)
	Cleanup(func())
	Error(args ...any)
	Errorf(format string, args ...any)
	Fail()
	FailNow()
	Failed() bool
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Helper()
	Log(args ...any)
	Logf(format string, args ...any)
	Name() string
	Setenv(key, value string)
	Chdir(dir string)
	Skip(args ...any)
	SkipNow()
	Skipf(format string, args ...any)
	Skipped() bool
	TempDir() string
	Context() context.Context
	Output() io.Writer

	// 一个私有方法，用于阻止用户实现该接口，
	// 这样将来向该接口添加内容就不会
	// 违反 Go 1 兼容性。
	private()
}

var (
	_ TB = (*T)(nil)
	_ TB = (*B)(nil)
)

// T 是传递给 Test 函数的类型，用于管理测试状态并支持格式化测试日志。
//
// 当测试函数返回或调用 [T.FailNow]、[T.Fatal]、[T.Fatalf]、[T.SkipNow]、[T.Skip] 或 [T.Skipf]
// 中的任一方法时，测试结束。这些方法以及 [T.Parallel] 方法
// 只能从运行该测试函数的 goroutine 中调用。
//
// 其他的报告方法，例如 [T.Log] 和 [T.Error] 的各种变体，
// 可以从多个 goroutine 同时调用。
type T struct {
	common
	denyParallel bool
	tstate       *testState // 用于运行测试和子测试。
}

func (c *common) private() {}

// Name 返回正在运行的（子）测试或基准测试的名称。
//
// 该名称将包含测试的名称以及任何嵌套子测试的名称。
// 如果两个同级的子测试同名，Name 会附加一个后缀
// 以保证返回的名称唯一。
func (c *common) Name() string {
	return c.name
}

func (c *common) setRan() {
	if c.parent != nil {
		c.parent.setRan()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ran = true
}

// Fail 将该函数标记为已失败，但会继续执行。
func (c *common) Fail() {
	if c.parent != nil {
		c.parent.Fail()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// 需要锁定 c.done，以便与父测试中对 c.done 的检查同步。
	if c.done {
		panic("Fail in goroutine after " + c.name + " has completed")
	}
	c.failed = true
}

// Failed 报告该函数是否已失败。
func (c *common) Failed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.done && int64(race.Errors()) > c.lastRaceErrors.Load() {
		c.mu.RUnlock()
		c.checkRaces()
		c.mu.RLock()
	}

	return c.failed
}

// FailNow 将该函数标记为已失败，并通过调用 [runtime.Goexit]
// （随后会运行当前 goroutine 中的所有延迟调用）来停止其执行。
// 执行将在下一个测试或基准测试处继续。
// FailNow 必须从运行测试或基准测试函数的 goroutine 中调用，
// 而不是从测试期间创建的其他 goroutine 中调用。
// 调用 FailNow 不会停止那些其他 goroutine。
func (c *common) FailNow() {
	c.checkFuzzFn("FailNow")
	c.Fail()

	// 调用 runtime.Goexit 将退出该 goroutine，这会
	// 运行此 goroutine 中的延迟函数，最终会运行
	// tRunner 中的延迟语句，从而向测试循环
	// 发出该测试已完成的信号。
	//
	// 此代码的先前版本是这样写的：
	//
	//	c.duration = ...
	//	c.signal <- c.self
	//	runtime.Goexit()
	//
	// 这个先前版本重复了代码（无论如何那些行都在
	// tRunner 中），但更糟糕的是，runtime.Goexit 隐含的
	// goroutine 清理不保证在测试退出前完成。如果测试延迟了
	// 一个重要的 cleanup 函数（比如删除临时文件），就无法保证
	// 它会在测试失败时运行。因为我们现在在栈顶的延迟函数中
	// 向 c.signal 发送信号，所以我们知道只有在所有其他
	// 已堆叠的延迟调用完成后才会发送。
	c.mu.Lock()
	c.finished = true
	c.mu.Unlock()
	runtime.Goexit()
}

// log 生成输出。它总是处于相同的栈深度。log 会插入
// 缩进，并在必要时插入最后的换行符。它会在字符串前
// 加上调用点的文件和行号。
func (c *common) log(s string) {
	s = strings.TrimSuffix(s, "\n")

	// 第二行及后续行缩进 4 个空格。这是在
	// outputWriter 提供的缩进之外的额外缩进。
	s = strings.ReplaceAll(s, "\n", "\n"+indent)
	s += "\n"

	n := c.destination()
	if n == nil {
		// 该测试及其所有父级都已完成。日志无法输出。
		panic("Log in goroutine after " + c.name + " has completed: " + s)
	}

	// 加上调用点作为前缀。它通过跳过 3 个函数来定位：
	// callSite + log + 公共函数
	s = n.callSite(3) + s

	// 输出缓冲的日志。
	n.flushPartial()

	n.o.Write([]byte(s))
}

// destination 选择应将输出追加到的测试。如果该测试
// 尚未完成，则返回它。否则，它查找最近的未完成父级。
func (c *common) destination() *common {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.done && !c.isSynctest {
		return c
	}
	for parent := c.parent; parent != nil; parent = parent.parent {
		parent.mu.Lock()
		defer parent.mu.Unlock()
		if !parent.done {
			return parent
		}
	}
	return nil
}

// callSite 检索并格式化调用点的文件和行号。
func (c *common) callSite(skip int) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	frame := c.frameSkip(skip)
	file := frame.File
	line := frame.Line
	if file != "" {
		if *fullPath {
			// 如果是相对路径，则在最后一个文件名分隔符处截断文件名。
		} else {
			file = filepath.Base(file)
		}
	} else {
		file = "???"
	}
	if line == 0 {
		line = 1
	}

	return fmt.Sprintf("%s:%d: ", file, line)
}

// flushPartial 检查缓冲区中的不完整日志并输出它们。
func (c *common) flushPartial() {
	partial := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return (c.o != nil) && (len(c.o.partial) > 0)
	}

	if partial() {
		c.o.Write([]byte("\n"))
	}
}

// Output 返回一个 Writer，它写入与 TB.Log 相同的测试输出流。
// 输出会像 TB.Log 行一样缩进，但 Output 不会
// 添加源位置或换行符。输出在内部按行缓冲，
// 调用 TB.Log 或测试结束时都会隐式刷新缓冲区，
// 并随后添加一个换行符。在测试函数及其所有
// 父级返回之后，不得调用 Output 或 Write 方法。
func (c *common) Output() io.Writer {
	c.checkFuzzFn("Output")
	n := c.destination()
	if n == nil {
		panic("Output called after " + c.name + " has completed")
	}
	return n.o
}

// setOutputWriter 初始化一个 outputWriter 并将其设置为 common 字段。
func (c *common) setOutputWriter() {
	c.o = &outputWriter{c: c}
}

// outputWriter 缓冲、格式化并写入日志消息。
type outputWriter struct {
	c       *common
	partial []byte // 上次 Write 的不完整（不含 '\n'）后缀
}

// Write 将日志消息写入测试的输出流，并进行适当的格式化和
// 缩进。在测试函数及其所有父级返回之后不得调用它。
func (o *outputWriter) Write(p []byte) (int, error) {
	// 如果从不再活动的顶层 *TB 调用，o 可能为 nil。
	// 在这种情况下忽略该消息即可。
	if o == nil || o.c == nil {
		return 0, nil
	}
	if o.c.destination() == nil {
		panic("Write called after " + o.c.name + " has completed")
	}

	o.c.mu.Lock()
	defer o.c.mu.Unlock()

	// 最后一个元素是不完整的一行。
	lines := bytes.SplitAfter(p, []byte("\n"))
	last := len(lines) - 1 // 不变式：0 <= last
	for i, line := range lines[:last] {
		// 输出上次调用留下的不完整行。
		if i == 0 && len(o.partial) > 0 {
			line = slices.Concat(o.partial, line)
			o.partial = o.partial[:0]
		}
		o.writeLine(line)
	}
	// 保存不完整行供下次调用使用。
	o.partial = append(o.partial, lines[last]...)

	return len(p), nil
}

// writeLine 为给定行生成输出。
func (o *outputWriter) writeLine(b []byte) {
	if !o.c.done && (o.c.chatty != nil) {
		if o.c.bench {
			// 基准测试不打印 === CONT，因此我们应跳过测试
			// 打印器，直接打印到 stdout。
			fmt.Printf("%s%s", indent, b)
		} else {
			o.c.chatty.Printf(o.c.name, "%s%s", indent, b)
		}
		return
	}
	o.c.output = append(o.c.output, indent...)
	o.c.output = append(o.c.output, b...)
}

// Log 使用默认格式（类似于 [fmt.Println]）格式化其参数，
// 并将文本记录到错误日志中。对于测试，只有测试失败或设置了
// -test.v 标志时才会打印该文本。对于基准测试，文本始终会打印，
// 以避免性能依赖于 -test.v 标志的取值。
// 在测试或基准测试返回后调用 Log 是错误的。
func (c *common) Log(args ...any) {
	c.checkFuzzFn("Log")
	c.log(fmt.Sprintln(args...))
}

// Logf 按照格式（类似于 [fmt.Printf]）格式化其参数，
// 并将文本记录到错误日志中。如果未提供，会添加最后的换行符。
// 对于测试，只有测试失败或设置了 -test.v 标志时才会打印
// 该文本。对于基准测试，文本始终会打印，以避免性能
// 依赖于 -test.v 标志的取值。
// 在测试或基准测试返回后调用 Logf 是错误的。
func (c *common) Logf(format string, args ...any) {
	c.checkFuzzFn("Logf")
	c.log(fmt.Sprintf(format, args...))
}

// Error 等价于依次调用 Log 和 Fail。
func (c *common) Error(args ...any) {
	c.checkFuzzFn("Error")
	c.log(fmt.Sprintln(args...))
	c.Fail()
}

// Errorf 等价于依次调用 Logf 和 Fail。
func (c *common) Errorf(format string, args ...any) {
	c.checkFuzzFn("Errorf")
	c.log(fmt.Sprintf(format, args...))
	c.Fail()
}

// Fatal 等价于依次调用 Log 和 FailNow。
func (c *common) Fatal(args ...any) {
	c.checkFuzzFn("Fatal")
	c.log(fmt.Sprintln(args...))
	c.FailNow()
}

// Fatalf 等价于依次调用 Logf 和 FailNow。
func (c *common) Fatalf(format string, args ...any) {
	c.checkFuzzFn("Fatalf")
	c.log(fmt.Sprintf(format, args...))
	c.FailNow()
}

// Skip 等价于依次调用 Log 和 SkipNow。
func (c *common) Skip(args ...any) {
	c.checkFuzzFn("Skip")
	c.log(fmt.Sprintln(args...))
	c.SkipNow()
}

// Skipf 等价于依次调用 Logf 和 SkipNow。
func (c *common) Skipf(format string, args ...any) {
	c.checkFuzzFn("Skipf")
	c.log(fmt.Sprintf(format, args...))
	c.SkipNow()
}

// SkipNow 将该测试标记为已被跳过，并通过调用 [runtime.Goexit]
// 停止其执行。
// 如果测试失败（参见 Error、Errorf、Fail）随后又被跳过，
// 它仍会被视为已失败。
// 执行将在下一个测试或基准测试处继续。另请参见 FailNow。
// SkipNow 必须从运行该测试的 goroutine 中调用，而不是从测试期间
// 创建的其他 goroutine 中调用。调用 SkipNow 不会停止
// 那些其他 goroutine。
func (c *common) SkipNow() {
	c.checkFuzzFn("SkipNow")
	c.mu.Lock()
	c.skipped = true
	c.finished = true
	c.mu.Unlock()
	runtime.Goexit()
}

// Skipped 报告该测试是否被跳过。
func (c *common) Skipped() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.skipped
}

// Helper 将调用函数标记为测试辅助函数。
// 在打印文件和行信息时，该函数将被跳过。
// Helper 可以从多个 goroutine 同时调用。
func (c *common) Helper() {
	if c.isSynctest {
		c = c.parent
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.helperPCs == nil {
		c.helperPCs = make(map[uintptr]struct{})
	}
	// 此处重复 callerName 的代码以省去遍历一个栈帧
	var pc [1]uintptr
	n := runtime.Callers(2, pc[:]) // 跳过 runtime.Callers + Helper
	if n == 0 {
		panic("testing: zero callers found")
	}
	if _, found := c.helperPCs[pc[0]]; !found {
		c.helperPCs[pc[0]] = struct{}{}
		c.helperNames = nil // map 会在下次需要时被重新创建
	}
}

// Cleanup 注册一个在测试（或子测试）及其所有子测试完成时
// 调用的函数。Cleanup 函数将按最后添加、最先调用的顺序调用。
func (c *common) Cleanup(f func()) {
	c.checkFuzzFn("Cleanup")
	var pc [maxStackLen]uintptr
	// 多跳过两个帧，以计入此函数和 runtime.Callers 本身。
	n := runtime.Callers(2, pc[:])
	cleanupPc := pc[:n]

	fn := func() {
		defer func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.cleanupName = ""
			c.cleanupPc = nil
		}()

		name := callerName(0)
		c.mu.Lock()
		c.cleanupName = name
		c.cleanupPc = cleanupPc
		c.mu.Unlock()

		f()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanups = append(c.cleanups, fn)
}

// ArtifactDir 返回一个目录，测试应在其中存储输出文件。
// 当提供 -artifacts 标志时，该目录位于输出目录之下。
// 否则，ArtifactDir 返回一个在测试完成后被删除的临时目录。
//
// 每个测试包中的每个测试或子测试都有唯一的 artifact 目录。
// 在同一测试或子测试中重复调用 ArtifactDir 会返回相同的目录。
// 子测试的输出不位于父测试的输出目录之下。
func (c *common) ArtifactDir() string {
	c.checkFuzzFn("ArtifactDir")
	c.artifactDirOnce.Do(func() {
		c.artifactDir, c.artifactDirErr = c.makeArtifactDir()
	})
	if c.artifactDirErr != nil {
		c.Fatalf("ArtifactDir: %v", c.artifactDirErr)
	}
	return c.artifactDir
}

func hashString(s string) (h uint64) {
	// FNV，此处使用它是为了避免对 maphash 的依赖。
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return
}

// makeArtifactDir 为测试创建 artifact 目录。
// artifact 目录为：
//
//	<output dir>/_artifacts/<test package>/<test name>/<random>
//
// 测试包是去除了模块名前缀的包导入路径。
// 测试名称如果太长会被截断。
// 路径中的特殊字符会被移除。
func (c *common) makeArtifactDir() (string, error) {
	if !*artifacts {
		return c.makeTempDir()
	}

	// 如果测试名称长于 maxNameSize，则截断它，并用完整名称的哈希
	// 替换最后 hashSize 个字节。
	const maxNameSize = 64
	name := strings.ReplaceAll(c.name, "/", "__")
	if len(name) > maxNameSize {
		h := fmt.Sprintf("%0x", hashString(name))
		name = name[:maxNameSize-len(h)] + h
	}

	// 从导入路径中移除模块路径前缀。
	pkg := strings.TrimPrefix(c.importPath, c.modulePath+"/")

	// 使用 / 连接，而非 filepath.Join：导入路径以 / 分隔，
	// 我们不希望 removeSymbolsExcept 在 Windows 上剥除 \ 分隔符。
	base := "/" + pkg + "/" + name
	base = removeSymbolsExcept(base, "!#$%&()+,-.=@^_{}~ /")
	base, err := filepath.Localize(base)
	if err != nil {
		// 此名称无法安全地转换为本地文件路径。
		// 丢弃它，只使用 _artifacts/<random>。
		base = ""
	}

	artifactBase := filepath.Join(artifactDir, base)
	if err := os.MkdirAll(artifactBase, 0o777); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(artifactBase, "")
	if err != nil {
		return "", err
	}
	if c.chatty != nil {
		c.chatty.Updatef(c.name, "=== ARTIFACTS %s %v\n", c.name, dir)
	}
	return dir, nil
}

func removeSymbolsExcept(s, allowed string) string {
	mapper := func(r rune) rune {
		if unicode.IsLetter(r) ||
			unicode.IsNumber(r) ||
			strings.ContainsRune(allowed, r) {
			return r
		}
		return -1 // 不允许的符号
	}
	return strings.Map(mapper, s)
}

// TempDir 返回一个供测试使用的临时目录。
// 当测试及其所有子测试完成时，该目录会被自动删除。
// 后续每次调用 TempDir 都会返回一个唯一的目录；
// 如果目录创建失败，TempDir 会通过调用 Fatal 终止测试。
// 如果设置了环境变量 GOTMPDIR，临时目录将在
// 该目录之下某处创建。
func (c *common) TempDir() string {
	c.checkFuzzFn("TempDir")
	dir, err := c.makeTempDir()
	if err != nil {
		c.Fatalf("TempDir: %v", err)
	}
	return dir
}

func (c *common) makeTempDir() (string, error) {
	// 为测试创建的所有临时目录使用单个父目录，
	// 每个目录按顺序编号。
	c.tempDirMu.Lock()
	var nonExistent bool
	if c.tempDir == "" { // 通常是 js/wasm 的情况
		nonExistent = true
	} else {
		_, err := os.Stat(c.tempDir)
		nonExistent = os.IsNotExist(err)
		if err != nil && !nonExistent {
			return "", err
		}
	}

	if nonExistent {
		c.Helper()

		pattern := c.Name()
		// 限制磁盘上文件名的长度。
		// 切片产生的无效 rune 会被下面的 strings.Map 丢弃。
		pattern = pattern[:min(len(pattern), 64)]

		// 从目录名中去掉不寻常的字符（例如路径分隔符或
		// 与 glob 交互的字符），以避免
		// 出现意外的 os.MkdirTemp 行为。
		const allowed = "!#$%&()+,-.=@^_{}~ "
		pattern = removeSymbolsExcept(pattern, allowed)

		c.tempDir, c.tempDirErr = os.MkdirTemp(os.Getenv("GOTMPDIR"), pattern)
		if c.tempDirErr == nil {
			c.Cleanup(func() {
				if err := removeAll(c.tempDir); err != nil {
					c.Errorf("TempDir RemoveAll cleanup: %v", err)
				}
			})
		}
	}

	if c.tempDirErr == nil {
		c.tempDirSeq++
	}
	seq := c.tempDirSeq
	c.tempDirMu.Unlock()

	if c.tempDirErr != nil {
		return "", c.tempDirErr
	}

	dir := fmt.Sprintf("%s%c%03d", c.tempDir, os.PathSeparator, seq)
	if err := os.Mkdir(dir, 0o777); err != nil {
		return "", err
	}
	return dir, nil
}

// removeAll 类似于 os.RemoveAll，但会重试 Windows 的 "Access is denied."
// 错误，直到一个任意设定的超时时间。
//
// 已知这些错误至少会在 windows-amd64-2012 构建器上偶发出现
// （https://go.dev/issue/50051），并且只有在测试遗留了一个仍处于
// 打开状态的临时文件，或测试因其他原因没有删除权限时，才会
// 合理地发生。在合理失败的情况下，失败的测试可能会多花一点时间
// 才失败，但一旦测试被修复，多余的延迟就会消失。
func removeAll(path string) error {
	const arbitraryTimeout = 2 * time.Second
	var (
		start     time.Time
		nextSleep = 1 * time.Millisecond
	)
	for {
		err := os.RemoveAll(path)
		if !isWindowsRetryable(err) {
			return err
		}
		if start.IsZero() {
			start = time.Now()
		} else if d := time.Since(start) + nextSleep; d >= arbitraryTimeout {
			return err
		}
		time.Sleep(nextSleep)
		nextSleep += time.Duration(rand.Int63n(int64(nextSleep)))
	}
}

// Setenv 调用 [os.Setenv]，并使用 Cleanup 在测试后将
// 环境变量恢复为其原始值。
//
// 由于 Setenv 会影响整个进程，因此不能在并行测试或
// 具有并行祖先的测试中使用。
func (c *common) Setenv(key, value string) {
	c.checkFuzzFn("Setenv")
	prevValue, ok := os.LookupEnv(key)

	if err := os.Setenv(key, value); err != nil {
		c.Fatalf("cannot set environment variable: %v", err)
	}

	if ok {
		c.Cleanup(func() {
			os.Setenv(key, prevValue)
		})
	} else {
		c.Cleanup(func() {
			os.Unsetenv(key)
		})
	}
}

// Chdir 调用 [os.Chdir]，并使用 Cleanup 在测试后将当前
// 工作目录恢复为其原始值。在 Unix 上，它还会在测试期间
// 设置 PWD 环境变量。
//
// 由于 Chdir 会影响整个进程，因此不能在并行测试或
// 具有并行祖先的测试中使用。
func (c *common) Chdir(dir string) {
	c.checkFuzzFn("Chdir")
	oldwd, err := os.Open(".")
	if err != nil {
		c.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		c.Fatal(err)
	}
	// 在 POSIX 平台上，PWD 表示“当前工作目录的绝对路径名”。
	// 由于我们正在更改工作目录，因此也应设置或更新 PWD
	// 以反映这一点。
	switch runtime.GOOS {
	case "windows", "plan9":
		// Windows 和 Plan 9 不使用 PWD 变量。
	default:
		if !filepath.IsAbs(dir) {
			dir, err = os.Getwd()
			if err != nil {
				c.Fatal(err)
			}
		}
		c.Setenv("PWD", dir)
	}
	c.Cleanup(func() {
		err := oldwd.Chdir()
		oldwd.Close()
		if err != nil {
			// 如果我们无法回到原始工作目录，继续测试是不安全的。
			// 由于我们持有一个 dirfd，这种情况极不可能发生。
			panic("testing.Chdir: " + err.Error())
		}
	})
}

// Context 返回一个 context，它会在 Cleanup 注册的函数
// 被调用之前被取消。
//
// Cleanup 函数可以等待任何在测试或基准测试完成前
// 通过 [context.Context.Done] 关闭的资源。
func (c *common) Context() context.Context {
	c.checkFuzzFn("Context")
	return c.ctx
}

// Attr 发出一个与该测试关联的测试属性。
//
// 键不能包含空白字符。
// 值不能包含换行符或回车符。
//
// 不同属性键的含义由持续集成系统和测试框架
// 自行决定。
//
// 测试属性会立即在测试日志中发出，
// 但它们应被视为无序的。
func (c *common) Attr(key, value string) {
	if strings.ContainsFunc(key, unicode.IsSpace) {
		c.Errorf("disallowed whitespace in attribute key %q", key)
		return
	}
	if strings.ContainsAny(value, "\r\n") {
		c.Errorf("disallowed newline in attribute value %q", value)
		return
	}
	if c.chatty == nil {
		return
	}
	c.chatty.Updatef(c.name, "=== ATTR  %s %v %v\n", c.name, key, value)
}

// panicHandling 控制 runCleanup 使用的 panic 处理方式。
type panicHandling int

const (
	normalPanic panicHandling = iota
	recoverAndReturnPanic
)

// runCleanup 在测试结束时调用。
// 如果 ph 为 recoverAndReturnPanic，它将捕获 panic，并在有值时
// 返回恢复的值。
func (c *common) runCleanup(ph panicHandling) (panicVal any) {
	c.cleanupStarted.Store(true)
	defer c.cleanupStarted.Store(false)

	if ph == recoverAndReturnPanic {
		defer func() {
			panicVal = recover()
		}()
	}

	// 确保如果某个 cleanup 函数发生 panic，
	// 我们仍会运行剩余的 cleanup 函数。
	defer func() {
		c.mu.Lock()
		recur := len(c.cleanups) > 0
		c.mu.Unlock()
		if recur {
			c.runCleanup(normalPanic)
		}
	}()

	if c.cancelCtx != nil {
		c.cancelCtx()
	}

	for {
		var cleanup func()
		c.mu.Lock()
		if len(c.cleanups) > 0 {
			last := len(c.cleanups) - 1
			cleanup = c.cleanups[last]
			c.cleanups = c.cleanups[:last]
		}
		c.mu.Unlock()
		if cleanup == nil {
			return nil
		}
		cleanup()
	}
}

// resetRaces 更新 c.parent 的数据竞争错误计数（如果 c 没有父级，
// 则为全局计数），并更新 c.lastRaceErrors 以与之匹配。
//
// 在此次调用 resetRaces 之前发生的任何竞争都不会
// 归因于 c。
func (c *common) resetRaces() {
	if c.parent == nil {
		c.lastRaceErrors.Store(int64(race.Errors()))
	} else {
		c.lastRaceErrors.Store(c.parent.checkRaces())
	}
}

// checkRaces 检查数据竞争错误的全局计数自 c 的计数上次重置以来
// 是否增加了。
//
// 如果增加了，它会将 c 标记为因这些竞争而失败（并为其中第一次
// 竞争记录一个错误），同时更新 c 的父级的竞争计数，这样如果
// 它们当前处于挂起状态（例如在调用 T.Run 中），就不会为该竞争
// 记录单独的错误。
//
// 注意，如果多个测试并行执行，它们可能因同一个竞争而被
// 标记为失败。
func (c *common) checkRaces() (raceErrors int64) {
	raceErrors = int64(race.Errors())
	for {
		last := c.lastRaceErrors.Load()
		if raceErrors <= last {
			// 所有竞争都已报告过了。
			return raceErrors
		}
		if c.lastRaceErrors.CompareAndSwap(last, raceErrors) {
			break
		}
	}

	if c.raceErrorLogged.CompareAndSwap(false, true) {
		// 这是该测试遇到的第一次竞争。
		// 将该测试标记为失败，并只记录一次原因。
		// （注意，竞争检测器本身仍会为它检测到的任何后续竞争
		// 写入 goroutine 转储。）
		c.Errorf("race detected during execution of test")
	}

	// 更新该测试的父级，使它们不重复报告该竞争。
	parent := c.parent
	for parent != nil {
		for {
			last := parent.lastRaceErrors.Load()
			if raceErrors <= last {
				// 该竞争已由另一个（很可能是并行的）子测试报告过了。
				return raceErrors
			}
			if parent.lastRaceErrors.CompareAndSwap(last, raceErrors) {
				break
			}
		}
		parent = parent.parent
	}

	return raceErrors
}

// callerName 返回跳过 skip 个帧之后（其中 0 表示当前函数）
// 调用者的函数名（带包路径限定）。
func callerName(skip int) string {
	var pc [1]uintptr
	n := runtime.Callers(skip+2, pc[:]) // 跳过 skip 个 + runtime.Callers + callerName
	if n == 0 {
		panic("testing: zero callers found")
	}
	return pcToName(pc[0])
}

func pcToName(pc uintptr) string {
	pcs := []uintptr{pc}
	frames := runtime.CallersFrames(pcs)
	frame, _ := frames.Next()
	return frame.Function
}

const parallelConflict = `testing: test using t.Setenv, t.Chdir, or cryptotest.SetGlobalRandom can not use t.Parallel`

// Parallel 表示此测试要与其他并行测试并行（且仅与其他并行测试）
// 运行。当一个测试因使用 -test.count 或 -test.cpu 而被运行多次时，
// 单个测试的多个实例从不会彼此并行运行。
func (t *T) Parallel() {
	if t.isParallel {
		panic("testing: t.Parallel called multiple times")
	}
	if t.isSynctest {
		panic("testing: t.Parallel called inside synctest bubble")
	}
	if t.denyParallel {
		panic(parallelConflict)
	}
	if t.parent.barrier == nil {
		// 模糊测试时 T.Parallel 无效。
		// 多个进程可能并行运行，但每个进程一次只能运行一个输入，
		// 这样我们才能将崩溃归因于特定的输入。
		return
	}

	t.isParallel = true

	// 我们不希望把等待串行测试所花的时间计入测试时长。
	// 记录到目前为止经过的时间，并在之后重置计时器。
	t.duration += highPrecisionTimeSince(t.start)

	// 添加到将由父级释放的测试列表中。
	t.parent.sub = append(t.parent.sub, t)

	// 报告该测试到目前为止执行期间发生的任何竞争。
	//
	// 我们将假设从这里到我们解除阻塞之间发生的任何竞争
	// 都不是由该子测试引起的。这个假设通常成立，
	// 但如果测试在其余部分阻塞于 Parallel 调用时生成了一个
	// 在后台发生竞争的 goroutine，它就可能出错。
	// 如果发生这种情况，我们会把后台竞争错误地归因于某个
	// 其他测试，或者根本不归因于任何测试——但这种假阴性
	// 极其罕见，不值得为测试在调用 Parallel 期间完全挂起
	// 这一常见情况添加竞争报告噪音。
	t.checkRaces()

	if t.chatty != nil {
		t.chatty.Updatef(t.name, "=== PAUSE %s\n", t.name)
	}
	running.Delete(t.name)

	t.signal <- true   // 释放正在调用的测试。
	<-t.parent.barrier // 等待父测试完成。
	t.tstate.waitParallel()
	parallelStart.Add(1)

	if t.chatty != nil {
		t.chatty.Updatef(t.name, "=== CONT  %s\n", t.name)
	}
	running.Store(t.name, highPrecisionTimeNow())
	t.start = highPrecisionTimeNow()

	// 重置本地竞争计数器，以忽略此 goroutine 被阻塞期间发生的任何竞争，
	// 例如在父测试或其他并行子测试中发生的竞争。
	//
	// （注意，我们这里不调用 parent.checkRaces：
	// 如果其他并行子测试已经引入了竞争，我们希望让它们报告
	// 那些竞争，而不是将其归因于父级。）
	t.lastRaceErrors.Store(int64(race.Errors()))
}

// checkParallel 由 [testing/cryptotest.SetGlobalRandom] 调用。
//
//go:linkname checkParallel testing.checkParallel
func checkParallel(t *T) {
	t.checkParallel()
}

func (t *T) checkParallel() {
	// 具有并行祖先的非并行子测试仍可能与其他测试并行运行：
	// 它们仅相对于同一父级的其他子测试才是非并行的。
	// 由于 SetEnv 或 Chdir 之类的调用会影响整个进程，
	// 因此如果当前测试或任何父级是并行的，我们就需要禁止这些调用。
	for c := &t.common; c != nil; c = c.parent {
		if c.isParallel {
			panic(parallelConflict)
		}
	}

	t.denyParallel = true
}

// Setenv 调用 os.Setenv(key, value)，并使用 Cleanup 在测试后
// 将环境变量恢复为其原始值。
//
// 由于 Setenv 会影响整个进程，因此不能在并行测试或
// 具有并行祖先的测试中使用。
func (t *T) Setenv(key, value string) {
	t.checkParallel()
	t.common.Setenv(key, value)
}

// Chdir 调用 [os.Chdir]，并使用 Cleanup 在测试后将当前
// 工作目录恢复为其原始值。在 Unix 上，它还会在测试期间
// 设置 PWD 环境变量。
//
// 由于 Chdir 会影响整个进程，因此不能在并行测试或
// 具有并行祖先的测试中使用。
func (t *T) Chdir(dir string) {
	t.checkParallel()
	t.common.Chdir(dir)
}

// InternalTest 是一个内部类型，但因跨包使用而导出；
// 它是 "go test" 命令实现的一部分。
type InternalTest struct {
	Name string
	F    func(*T)
}

var errNilPanicOrGoexit = errors.New("test executed panic(nil) or runtime.Goexit")

func tRunner(t *T, fn func(t *T)) {
	t.runner = callerName(0)

	// 当此 goroutine 结束时——无论是由于 fn(t) 正常返回，
	// 还是由于测试失败触发了对 runtime.Goexit 的调用——
	// 记录时长并发送一个表示测试已完成的信号。
	defer func() {
		t.checkRaces()

		// TODO(#61034): 这个检查放在这里是不对的。
		if t.Failed() {
			numFailed.Add(1)
		}

		// 检查测试是否不适当地发生了 panic 或 Goexit。
		//
		// 如果这发生在普通测试中，则打印输出但继续 panic。
		// tRunner 在自己的 goroutine 中调用，因此这会终止进程。
		//
		// 如果这发生在模糊测试期间，则从 panic 中恢复并将其视为
		// 普通失败。让进程继续运行以找到导致 panic 的短输入非常重要。
		err := recover()
		signal := true

		t.mu.RLock()
		finished := t.finished
		t.mu.RUnlock()
		if !finished && err == nil {
			err = errNilPanicOrGoexit
			for p := t.parent; p != nil; p = p.parent {
				p.mu.RLock()
				finished = p.finished
				p.mu.RUnlock()
				if finished {
					if !t.isParallel {
						t.Errorf("%v: subtest may have called FailNow on a parent test", err)
						err = nil
					}
					signal = false
					break
				}
			}
		}

		if err != nil && t.tstate.isFuzzing {
			prefix := "panic: "
			if err == errNilPanicOrGoexit {
				prefix = ""
			}
			t.Errorf("%s%s\n%s\n", prefix, err, string(debug.Stack()))
			t.mu.Lock()
			t.finished = true
			t.mu.Unlock()
			err = nil
		}

		// 使用延迟调用以确保即使某个 cleanup 函数调用了 t.FailNow，
		// 我们也会报告测试已完成。参见 issue 41355。
		didPanic := false
		defer func() {
			// 只有在测试不发生 panic 时才报告测试已完成，
			// 否则测试二进制文件可能会在 panic 报告给用户之前退出。
			// 参见 issue 41479。
			if didPanic {
				return
			}
			if err != nil {
				panic(err)
			}
			running.Delete(t.name)
			if t.isParallel {
				parallelStop.Add(1)
			}
			t.signal <- signal
		}()

		doPanic := func(err any) {
			t.Fail()
			if r := t.runCleanup(recoverAndReturnPanic); r != nil {
				t.Logf("cleanup panicked with %v", r)
			}
			// 在终止之前将输出日志一直刷新到根。
			// 如果此 *T 是 synctest bubble，则跳过此操作，因为它不是子测试。
			for root := &t.common; !root.isSynctest && root.parent != nil; root = root.parent {
				root.mu.Lock()
				root.duration += highPrecisionTimeSince(root.start)
				d := root.duration
				root.mu.Unlock()
				// 输出缓冲的日志。
				root.flushPartial()
				root.flushToParent(root.name, "--- FAIL: %s (%s)\n", root.name, fmtDuration(d))
				if r := root.parent.runCleanup(recoverAndReturnPanic); r != nil {
					fmt.Fprintf(root.parent.w, "cleanup panicked with %v", r)
				}
			}
			didPanic = true
			panic(err)
		}
		if err != nil {
			doPanic(err)
		}

		t.duration += highPrecisionTimeSince(t.start)

		if len(t.sub) > 0 {
			// 运行并行的子测试。

			// 减少该测试的运行计数并将其标记为不再运行。
			t.tstate.release()
			running.Delete(t.name)

			// 释放并行的子测试。
			close(t.barrier)
			// 等待子测试完成。
			for _, sub := range t.sub {
				<-sub.signal
			}

			// 运行任何 cleanup 回调，并将该测试标记为正在运行，
			// 以防 cleanup 挂起。
			cleanupStart := highPrecisionTimeNow()
			running.Store(t.name, cleanupStart)
			err := t.runCleanup(recoverAndReturnPanic)
			t.duration += highPrecisionTimeSince(cleanupStart)
			if err != nil {
				doPanic(err)
			}
			t.checkRaces()
			if !t.isParallel {
				// 重新获取串行测试的计数。参见 Run 中的注释。
				t.tstate.waitParallel()
			}
		} else if t.isParallel {
			// 仅当该测试作为并行测试运行时才释放它的计数。
			// 参见 Run 方法中的注释。
			t.tstate.release()
		}
		// 输出缓冲的日志。
		for root := &t.common; root.parent != nil; root = root.parent {
			root.flushPartial()
		}
		t.report() // 在所有子测试完成后进行报告。

		// 不要锁定 t.done，以便在用户没有适当地同步 goroutine 时
		// 让竞争检测器能够检测到竞争。
		t.done = true
		if t.parent != nil && !t.hasSub.Load() {
			t.setRan()
		}
	}()
	defer func() {
		if len(t.sub) == 0 {
			t.runCleanup(normalPanic)
		}
	}()

	t.start = highPrecisionTimeNow()
	t.resetRaces()
	fn(t)

	// 当调用 FailNow 时，此处之后的代码不会被执行
	t.mu.Lock()
	t.finished = true
	t.mu.Unlock()
}

// Run 将 f 作为 t 的一个名为 name 的子测试运行。它在一个单独的
// goroutine 中运行 f，并阻塞直到 f 返回或调用 t.Parallel 成为并行测试。
// Run 报告 f 是否成功（或至少在调用 t.Parallel 之前没有失败）。
//
// Run 可以从多个 goroutine 同时调用，但所有此类调用都必须在
// t 的外层测试函数返回之前返回。
func (t *T) Run(name string, f func(t *T)) bool {
	if t.isSynctest {
		panic("testing: t.Run called inside synctest bubble")
	}
	if t.cleanupStarted.Load() {
		panic("testing: t.Run called during t.Cleanup")
	}

	t.hasSub.Store(true)
	testName, ok, _ := t.tstate.match.fullName(&t.common, name)
	if !ok || shouldFailFast() {
		return true
	}
	// 在此调用点记录栈回溯，这样如果子测试函数
	// ——它在单独的栈上运行——被标记为 helper，我们就能
	// 继续沿栈向上遍历进入父测试。
	var pc [maxStackLen]uintptr
	n := runtime.Callers(2, pc[:])

	// 没有理由从父级继承此 context。用户代码无法观察到
	// 后台 context 与来自父测试的 context 之间的区别。
	ctx, cancelCtx := context.WithCancel(context.Background())
	t = &T{
		common: common{
			barrier:    make(chan bool),
			signal:     make(chan bool, 1),
			name:       testName,
			modulePath: t.modulePath,
			importPath: t.importPath,
			parent:     &t.common,
			level:      t.level + 1,
			creator:    pc[:n],
			chatty:     t.chatty,
			ctx:        ctx,
			cancelCtx:  cancelCtx,
		},
		tstate: t.tstate,
	}
	t.w = indenter{&t.common}
	t.setOutputWriter()

	if t.chatty != nil {
		t.chatty.Updatef(t.name, "=== RUN   %s\n", t.name)
	}
	running.Store(t.name, highPrecisionTimeNow())

	// 我们没有在调用 tRunner 之前减少该测试的运行计数、
	// 之后又将其增加，而是依赖 tRunner 保持计数正确。
	// 这确保了一连串串行测试运行时不会被抢占，即使它们的
	// 父级是一个并行测试也是如此。当 *parallel == 1 时，
	// 这尤其能减少意外情况。
	go tRunner(t, f)

	// 父 goroutine 将阻塞，直到子测试完成或调用 Parallel，
	// 但通常我们不知道父 goroutine 是顶层测试函数还是
	// 它产生的其他某个 goroutine。
	// 为避免令人困惑的假阴性，即使父级在典型情况下处于阻塞状态，
	// 我们也将其保留在 running map 中。

	if !<-t.signal {
		// 此时，很可能是某个子测试对某个父测试调用了 FailNow。
		// 继续沿调用链向上中止。
		runtime.Goexit()
	}

	if t.chatty != nil && t.chatty.json {
		t.chatty.Updatef(t.parent.name, "=== NAME  %s\n", t.parent.name)
	}
	return !t.failed
}

// testingSynctestTest 在一个 synctest bubble 内运行 f。
// 它由 synctest.Test 从已创建的 bubble 内部调用。
//
//go:linkname testingSynctestTest testing/synctest.testingSynctestTest
func testingSynctestTest(t *T, f func(*T)) (ok bool) {
	if t.cleanupStarted.Load() {
		panic("testing: synctest.Run called during t.Cleanup")
	}

	var pc [maxStackLen]uintptr
	n := runtime.Callers(2, pc[:])

	ctx, cancelCtx := context.WithCancel(context.Background())
	t2 := &T{
		common: common{
			barrier:    make(chan bool),
			signal:     make(chan bool, 1),
			name:       t.name,
			parent:     &t.common,
			level:      t.level + 1,
			creator:    pc[:n],
			chatty:     t.chatty,
			ctx:        ctx,
			cancelCtx:  cancelCtx,
			isSynctest: true,
		},
		tstate: t.tstate,
	}

	go tRunner(t2, f)
	if !<-t2.signal {
		// 此时，很可能是某个子测试对某个父测试调用了 FailNow。
		// 继续沿调用链向上中止。
		runtime.Goexit()
	}
	return !t2.failed
}

// Deadline 报告测试二进制文件将超过 -timeout 标志所指定
// 超时的时间。
//
// 如果 -timeout 标志表示“无超时”（0），则 ok 结果为 false。
func (t *T) Deadline() (deadline time.Time, ok bool) {
	if t.isSynctest {
		// 向使用假时钟的测试返回真实时钟的截止时间毫无意义。
		// 我们可以返回“无超时”，但 panic 能让用户更容易
		// 捕获该错误。
		panic("testing: t.Deadline called inside synctest bubble")
	}
	deadline = t.tstate.deadline
	return deadline, !deadline.IsZero()
}

// testState 保存所有测试共有的所有字段。这包括用于最多
// 运行 *parallel 个测试的同步原语。
type testState struct {
	match    *matcher
	deadline time.Time

	// 在用于为模糊测试目标生成随机输入的状态中，isFuzzing 为 true。
	// 在运行普通测试以及将模糊测试作为单元测试运行时
	// （不带 -fuzz，或 -fuzz 不匹配时），isFuzzing 为 false。
	isFuzzing bool

	mu sync.Mutex

	// 用于通知测试已准备好并行运行的通道。
	startParallel chan bool

	// running 是当前并行运行的测试数量。
	// 这不包括正在等待子测试完成的测试。
	running int

	// numWaiting 是等待并行运行的测试数量。
	numWaiting int

	// maxParallel 是 parallel 标志的副本。
	maxParallel int
}

func newTestState(maxParallel int, m *matcher) *testState {
	return &testState{
		match:         m,
		startParallel: make(chan bool),
		maxParallel:   maxParallel,
		running:       1, // 将主（串行）测试的计数设为 1。
	}
}

func (s *testState) waitParallel() {
	s.mu.Lock()
	if s.running < s.maxParallel {
		s.running++
		s.mu.Unlock()
		return
	}
	s.numWaiting++
	s.mu.Unlock()
	<-s.startParallel
}

func (s *testState) release() {
	s.mu.Lock()
	if s.numWaiting == 0 {
		s.running--
		s.mu.Unlock()
		return
	}
	s.numWaiting--
	s.mu.Unlock()
	s.startParallel <- true // 挑选一个等待中的测试来运行。
}

// 不应再有人使用 func Main。
// 参见 func Main 的文档注释，改用 MainStart。
var errMain = errors.New("testing: unexpected use of func Main")

type matchStringOnly func(pat, str string) (bool, error)

func (f matchStringOnly) MatchString(pat, str string) (bool, error)   { return f(pat, str) }
func (f matchStringOnly) StartCPUProfile(w io.Writer) error           { return errMain }
func (f matchStringOnly) StopCPUProfile()                             {}
func (f matchStringOnly) WriteProfileTo(string, io.Writer, int) error { return errMain }
func (f matchStringOnly) ModulePath() string                          { return "" }
func (f matchStringOnly) ImportPath() string                          { return "" }
func (f matchStringOnly) StartTestLog(io.Writer)                      {}
func (f matchStringOnly) StopTestLog() error                          { return errMain }
func (f matchStringOnly) SetPanicOnExit0(bool)                        {}
func (f matchStringOnly) CoordinateFuzzing(time.Duration, int64, time.Duration, int64, int, []corpusEntry, []reflect.Type, string, string) error {
	return errMain
}
func (f matchStringOnly) RunFuzzWorker(func(corpusEntry) error) error { return errMain }
func (f matchStringOnly) ReadCorpus(string, []reflect.Type) ([]corpusEntry, error) {
	return nil, errMain
}
func (f matchStringOnly) CheckCorpus([]any, []reflect.Type) error { return nil }
func (f matchStringOnly) ResetCoverage()                          {}
func (f matchStringOnly) SnapshotCoverage()                       {}

func (f matchStringOnly) InitRuntimeCoverage() (mode string, tearDown func(string, string) (string, error), snapcov func() float64) {
	return
}

// Main 是一个内部函数，是 "go test" 命令实现的一部分。
// 它被导出是因为它跨包使用，并且早于 "internal" 包。
// "go test" 不再使用它，但尽可能为其他使用 Main 来模拟 "go test"
// 的系统保留了它，然而随着 testing 包添加新功能，Main 有时无法更新。
// 模拟 "go test" 的系统应更新为使用 [MainStart]。
func Main(matchString func(pat, str string) (bool, error), tests []InternalTest, benchmarks []InternalBenchmark, examples []InternalExample) {
	os.Exit(MainStart(matchStringOnly(matchString), tests, benchmarks, nil, examples).Run())
}

// M 是传递给 TestMain 函数以运行实际测试的类型。
type M struct {
	deps        testDeps
	tests       []InternalTest
	benchmarks  []InternalBenchmark
	fuzzTargets []InternalFuzzTarget
	examples    []InternalExample

	timer     *time.Timer
	afterOnce sync.Once

	numRun int

	// 传递给 os.Exit 的值，外层测试 func main
	// 测试框架会用此代码调用 os.Exit。参见 #34129。
	exitCode int
}

// testDeps 是一个内部接口，表示由测试生成的 main 包
// 传入本包的功能。该接口的规范实现是
// testing/internal/testdeps 的 TestDeps。
type testDeps interface {
	ImportPath() string
	ModulePath() string
	MatchString(pat, str string) (bool, error)
	SetPanicOnExit0(bool)
	StartCPUProfile(io.Writer) error
	StopCPUProfile()
	StartTestLog(io.Writer)
	StopTestLog() error
	WriteProfileTo(string, io.Writer, int) error
	CoordinateFuzzing(time.Duration, int64, time.Duration, int64, int, []corpusEntry, []reflect.Type, string, string) error
	RunFuzzWorker(func(corpusEntry) error) error
	ReadCorpus(string, []reflect.Type) ([]corpusEntry, error)
	CheckCorpus([]any, []reflect.Type) error
	ResetCoverage()
	SnapshotCoverage()
	InitRuntimeCoverage() (mode string, tearDown func(coverprofile string, gocoverdir string) (string, error), snapcov func() float64)
}

// MainStart 供 'go test' 生成的测试使用。
// 它不应被直接调用，也不受 Go 1 兼容性文档的约束。
// 它的签名可能随版本发布而变化。
func MainStart(deps testDeps, tests []InternalTest, benchmarks []InternalBenchmark, fuzzTargets []InternalFuzzTarget, examples []InternalExample) *M {
	registerCover(deps.InitRuntimeCoverage())
	Init()
	return &M{
		deps:        deps,
		tests:       tests,
		benchmarks:  benchmarks,
		fuzzTargets: fuzzTargets,
		examples:    examples,
	}
}

var (
	testingTesting bool
	realStderr     *os.File
)

// Run 运行测试。它返回一个要传递给 os.Exit 的退出码。
// 当所有测试通过时退出码为零，任何类型的失败时
// 退出码非零。要获得机器可读的测试结果，请解析
// 'go test -json' 的输出。
func (m *M) Run() (code int) {
	defer func() {
		code = m.exitCode
	}()

	// 统计对 m.Run 的调用次数。
	// 我们原本只预期调用 1 次，但没有强制这一点，
	// 而现在实际使用中有测试多次调用 m.Run。
	// 唉。go.dev/issue/23129。
	m.numRun++

	// TestMain 可能已经调用了 flag.Parse。
	if !flag.Parsed() {
		flag.Parse()
	}

	if chatty.json {
		// 使用 -v=json 时，stdout 和 stderr 指向同一个管道，
		// 该管道通向 test2json。一般来说，操作系统能很好地
		// 确保通过不同文件描述符对同一管道的写入被整体传递，
		// 因此同时向 stdout 写 AAA、向 stderr 写 BBB 会在管道上
		// 产生 AAABBB 或 BBBAAA，而不是像 AABBBA 这样的结果。
		// 然而，例外情况是管道被填满时：在这种情况下，
		// Go 使用非阻塞 I/O 意味着写入 AAA 或 BBB 可能被拆分到
		// 多个系统调用中，从而完全有可能得到像 AABBBA 这样的输出。
		// 如果我们改为在管道上使用阻塞 I/O，操作系统内核中也会
		// 出现同样的问题。这种交错的输出可能会在一行 TestFoo 的
		// 中间打印不相关的消息，从而干扰 test2json。
		// 设置 os.Stderr = os.Stdout 将使它们共享同一个 pfd，
		// 该 pfd 会为每次程序写入持有锁，从而防止任何交错。
		//
		// 如果总是设置 Stderr = Stdout 或许也不错，或者在我们能
		// 判断它们是不是同一个文件时这样做，但目前 -v=json 是
		// 一个非常明确的信号。让这两个文件相同可能会导致意外，
		// 如果程序关闭了 os.Stdout 却期望能继续写入 os.Stderr，
		// 但很难看出为什么测试会认为自己能以那种方式接管全局状态。
		//
		// 此修复只对输出来自 Go 代码本身的程序有帮助。
		// 它对于那些在 Go 测试写入输出的同时有子进程写入
		// stderr 或 stdout 的程序没有帮助。
		// 当输出来自运行时（例如使用 print/println 函数）时也没有帮助，
		// 因为该代码直接写入 fd 2，没有任何锁定。
		// 我们保留 realStderr 以防止 fd 2 被关闭。
		//
		// 参见 go.dev/issue/33419。
		realStderr = os.Stderr
		os.Stderr = os.Stdout
	}

	if *parallel < 1 {
		fmt.Fprintln(os.Stderr, "testing: -parallel can only be given a positive integer")
		flag.Usage()
		m.exitCode = 2
		return
	}
	if *matchFuzz != "" && *fuzzCacheDir == "" {
		fmt.Fprintln(os.Stderr, "testing: -test.fuzzcachedir must be set if -test.fuzz is set")
		flag.Usage()
		m.exitCode = 2
		return
	}

	if *matchList != "" {
		listTests(m.deps.MatchString, m.tests, m.benchmarks, m.fuzzTargets, m.examples)
		m.exitCode = 0
		return
	}

	if *shuffle != "off" {
		var n int64
		var err error
		if *shuffle == "on" {
			n = time.Now().UnixNano()
		} else {
			n, err = strconv.ParseInt(*shuffle, 10, 64)
			if err != nil {
				fmt.Fprintln(os.Stderr, `testing: -shuffle should be "off", "on", or a valid integer:`, err)
				m.exitCode = 2
				return
			}
		}
		fmt.Println("-test.shuffle", n)
		rng := rand.New(rand.NewSource(n))
		rng.Shuffle(len(m.tests), func(i, j int) { m.tests[i], m.tests[j] = m.tests[j], m.tests[i] })
		rng.Shuffle(len(m.benchmarks), func(i, j int) { m.benchmarks[i], m.benchmarks[j] = m.benchmarks[j], m.benchmarks[i] })
	}

	parseCpuList()

	m.before()
	defer m.after()

	// 除非这是模糊测试的工作进程，否则运行测试、示例和基准测试。
	// 工作进程在其父进程完成此工作之后启动，它们不应重复此工作。
	if !*isFuzzWorker {
		deadline := m.startAlarm()
		haveExamples = len(m.examples) > 0
		testRan, testOk := runTests(m.deps.ModulePath(), m.deps.ImportPath(), m.deps.MatchString, m.tests, deadline)
		fuzzTargetsRan, fuzzTargetsOk := runFuzzTests(m.deps, m.fuzzTargets, deadline)
		exampleRan, exampleOk := runExamples(m.deps.MatchString, m.examples)
		m.stopAlarm()
		if !testRan && !exampleRan && !fuzzTargetsRan && *matchBenchmarks == "" && *matchFuzz == "" {
			fmt.Fprintln(os.Stderr, "testing: warning: no tests to run")
			if testingTesting && *match != "^$" {
				// 如果这种情况发生在测试 package testing 自身时，可能是
				// package testing 自身关于何时运行测试的逻辑出了问题，
				// 那样的话每个测试都不会运行任何内容却会成功，
				// 且没有明显的方式检测这个问题（因为没有测试在运行）。
				// 所以在测试 package testing 自身时，把 'no tests to run' 视为硬性失败。
				fmt.Print(chatty.prefix(), "FAIL: package testing must run tests\n")
				testOk = false
			}
		}
		anyFailed := !testOk || !exampleOk || !fuzzTargetsOk || !runBenchmarks(m.deps.ImportPath(), m.deps.MatchString, m.benchmarks)
		if !anyFailed && race.Errors() > 0 {
			fmt.Print(chatty.prefix(), "testing: race detected outside of test execution\n")
			anyFailed = true
		}
		if anyFailed {
			fmt.Print(chatty.prefix(), "FAIL\n")
			m.exitCode = 1
			return
		}
	}

	fuzzingOk := runFuzzing(m.deps, m.fuzzTargets)
	if !fuzzingOk {
		fmt.Print(chatty.prefix(), "FAIL\n")
		if *isFuzzWorker {
			m.exitCode = fuzzWorkerExitCode
		} else {
			m.exitCode = 1
		}
		return
	}

	m.exitCode = 0
	if !*isFuzzWorker {
		fmt.Print(chatty.prefix(), "PASS\n")
	}
	return
}

func (t *T) report() {
	if t.parent == nil {
		return
	}
	if t.isSynctest {
		return // t.parent 将负责报告
	}
	dstr := fmtDuration(t.duration)
	format := "--- %s: %s (%s)\n"
	if t.Failed() {
		t.flushToParent(t.name, format, "FAIL", t.name, dstr)
	} else if t.chatty != nil {
		if t.Skipped() {
			t.flushToParent(t.name, format, "SKIP", t.name, dstr)
		} else {
			t.flushToParent(t.name, format, "PASS", t.name, dstr)
		}
	}
}

func listTests(matchString func(pat, str string) (bool, error), tests []InternalTest, benchmarks []InternalBenchmark, fuzzTargets []InternalFuzzTarget, examples []InternalExample) {
	if _, err := matchString(*matchList, "non-empty"); err != nil {
		fmt.Fprintf(os.Stderr, "testing: invalid regexp in -test.list (%q): %s\n", *matchList, err)
		os.Exit(1)
	}

	for _, test := range tests {
		if ok, _ := matchString(*matchList, test.Name); ok {
			fmt.Println(test.Name)
		}
	}
	for _, bench := range benchmarks {
		if ok, _ := matchString(*matchList, bench.Name); ok {
			fmt.Println(bench.Name)
		}
	}
	for _, fuzzTarget := range fuzzTargets {
		if ok, _ := matchString(*matchList, fuzzTarget.Name); ok {
			fmt.Println(fuzzTarget.Name)
		}
	}
	for _, example := range examples {
		if ok, _ := matchString(*matchList, example.Name); ok {
			fmt.Println(example.Name)
		}
	}
}

// RunTests 是一个内部函数，但因需要跨包调用而导出；
// 它是 "go test" 命令实现的一部分。
func RunTests(matchString func(pat, str string) (bool, error), tests []InternalTest) (ok bool) {
	var deadline time.Time
	if *timeout > 0 {
		deadline = time.Now().Add(*timeout)
	}
	ran, ok := runTests("", "", matchString, tests, deadline)
	if !ran && !haveExamples {
		fmt.Fprintln(os.Stderr, "testing: warning: no tests to run")
	}
	return ok
}

func runTests(modulePath, importPath string, matchString func(pat, str string) (bool, error), tests []InternalTest, deadline time.Time) (ran, ok bool) {
	ok = true
	for _, procs := range cpuList {
		runtime.GOMAXPROCS(procs)
		for i := uint(0); i < *count; i++ {
			if shouldFailFast() {
				break
			}
			if i > 0 && !ran {
				// 第一次迭代时没有测试可运行。
				// 这不会改变，因此没有理由继续尝试。
				break
			}
			ctx, cancelCtx := context.WithCancel(context.Background())
			tstate := newTestState(*parallel, newMatcher(matchString, *match, "-test.run", *skip))
			tstate.deadline = deadline
			t := &T{
				common: common{
					signal:     make(chan bool, 1),
					barrier:    make(chan bool),
					w:          os.Stdout,
					ctx:        ctx,
					cancelCtx:  cancelCtx,
					modulePath: modulePath,
					importPath: importPath,
				},
				tstate: tstate,
			}
			if Verbose() {
				t.chatty = newChattyPrinter(t.w)
			}
			tRunner(t, func(t *T) {
				for _, test := range tests {
					t.Run(test.Name, test.F)
				}
			})
			select {
			case <-t.signal:
			default:
				panic("internal error: tRunner exited without sending on t.signal")
			}
			ok = ok && !t.Failed()
			ran = ran || t.ran
		}
	}
	return ran, ok
}

// before 在所有测试之前运行。
func (m *M) before() {
	if *memProfileRate > 0 {
		runtime.MemProfileRate = *memProfileRate
	}
	if *cpuProfile != "" {
		f, err := os.Create(toOutputDir(*cpuProfile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: %s\n", err)
			return
		}
		if err := m.deps.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't start cpu profile: %s\n", err)
			f.Close()
			return
		}
		// 可以保存 f，以便 after 调用 f.Close；但不值得这么做。
	}
	if *traceFile != "" {
		f, err := os.Create(toOutputDir(*traceFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: %s\n", err)
			return
		}
		if err := trace.Start(f); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't start tracing: %s\n", err)
			f.Close()
			return
		}
		// 可以保存 f，以便 after 调用 f.Close；但不值得这么做。
	}
	if *blockProfile != "" && *blockProfileRate >= 0 {
		runtime.SetBlockProfileRate(*blockProfileRate)
	}
	if *mutexProfile != "" && *mutexProfileFraction >= 0 {
		runtime.SetMutexProfileFraction(*mutexProfileFraction)
	}
	if *coverProfile != "" && CoverMode() == "" {
		fmt.Fprintf(os.Stderr, "testing: cannot use -test.coverprofile because test binary was not built with coverage enabled\n")
		os.Exit(2)
	}
	if *gocoverdir != "" && CoverMode() == "" {
		fmt.Fprintf(os.Stderr, "testing: cannot use -test.gocoverdir because test binary was not built with coverage enabled\n")
		os.Exit(2)
	}
	if *artifacts {
		var err error
		artifactDir, err = filepath.Abs(toOutputDir("_artifacts"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: cannot make -test.outputdir absolute: %v\n", err)
			os.Exit(2)
		}
		if err := os.Mkdir(artifactDir, 0o777); err != nil && !errors.Is(err, os.ErrExist) {
			fmt.Fprintf(os.Stderr, "testing: %v\n", err)
			os.Exit(2)
		}
	}
	if *testlog != "" {
		// 注意：此处不使用 toOutputDir。
		// 该文件供 cmd/go 使用，而非用户。
		var f *os.File
		var err error
		if m.numRun == 1 {
			f, err = os.Create(*testlog)
		} else {
			f, err = os.OpenFile(*testlog, os.O_WRONLY, 0)
			if err == nil {
				f.Seek(0, io.SeekEnd)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: %s\n", err)
			os.Exit(2)
		}
		m.deps.StartTestLog(f)
		testlogFile = f
	}
	if *panicOnExit0 {
		m.deps.SetPanicOnExit0(true)
	}
}

// after 在所有测试之后运行。
func (m *M) after() {
	m.afterOnce.Do(func() {
		m.writeProfiles()
	})

	// 每次运行后都恢复 PanicOnExit0，因为我们在每次运行前
	// 将其设为 true。否则，如果 m.Run 被调用多次，
	// os.Exit(0) 的行为在第二次运行后将无法恢复。
	if *panicOnExit0 {
		m.deps.SetPanicOnExit0(false)
	}
}

func (m *M) writeProfiles() {
	if *testlog != "" {
		if err := m.deps.StopTestLog(); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't write %s: %s\n", *testlog, err)
			os.Exit(2)
		}
		if err := testlogFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't write %s: %s\n", *testlog, err)
			os.Exit(2)
		}
	}
	if *cpuProfile != "" {
		m.deps.StopCPUProfile() // 将 profile 刷写到磁盘
	}
	if *traceFile != "" {
		trace.Stop() // 将 trace 刷写到磁盘
	}
	if *memProfile != "" {
		f, err := os.Create(toOutputDir(*memProfile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: %s\n", err)
			os.Exit(2)
		}
		runtime.GC() // 使所有统计信息具体化
		if err = m.deps.WriteProfileTo("allocs", f, 0); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't write %s: %s\n", *memProfile, err)
			os.Exit(2)
		}
		f.Close()
	}
	if *blockProfile != "" && *blockProfileRate >= 0 {
		f, err := os.Create(toOutputDir(*blockProfile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: %s\n", err)
			os.Exit(2)
		}
		if err = m.deps.WriteProfileTo("block", f, 0); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't write %s: %s\n", *blockProfile, err)
			os.Exit(2)
		}
		f.Close()
	}
	if *mutexProfile != "" && *mutexProfileFraction >= 0 {
		f, err := os.Create(toOutputDir(*mutexProfile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: %s\n", err)
			os.Exit(2)
		}
		if err = m.deps.WriteProfileTo("mutex", f, 0); err != nil {
			fmt.Fprintf(os.Stderr, "testing: can't write %s: %s\n", *mutexProfile, err)
			os.Exit(2)
		}
		f.Close()
	}
	if CoverMode() != "" {
		coverReport()
	}
}

// toOutputDir 返回按需重定位到 outputDir 的文件名。
// 采用简单实现以避免引入 path/filepath。
func toOutputDir(path string) string {
	if *outputDir == "" || path == "" {
		return path
	}
	// 在 Windows 上，这种做法很笨拙，但仅通过查找盘符和冒号，
	// 我们几乎总能给出正确结果。
	// 绝对路径总是带有一个盘符（忽略 UNC）。
	// 问题：如果 path == "C:A" 且 outputdir == "C:\Go"，该怎么做并不明确，
	// 但即便如此 path/filepath 也帮不上忙。
	// TODO: 值得做得更好吗？大概不值得，因为只有
	// 在 go test 的管理下才会执行到这里。
	if runtime.GOOS == "windows" && len(path) >= 2 {
		letter, colon := path[0], path[1]
		if ('a' <= letter && letter <= 'z' || 'A' <= letter && letter <= 'Z') && colon == ':' {
			// 如果 path 以盘符开头，无论如何我们都只能保留它。
			return path
		}
	}
	if os.IsPathSeparator(path[0]) {
		return path
	}
	return fmt.Sprintf("%s%c%s", *outputDir, os.PathSeparator, path)
}

// startAlarm 在需要时启动定时器。
func (m *M) startAlarm() time.Time {
	if *timeout <= 0 {
		return time.Time{}
	}

	deadline := time.Now().Add(*timeout)
	m.timer = time.AfterFunc(*timeout, func() {
		m.after()
		debug.SetTraceback("all")
		extra := ""

		if list := runningList(); len(list) > 0 {
			var b strings.Builder
			b.WriteString("\nrunning tests:")
			for _, name := range list {
				b.WriteString("\n\t")
				b.WriteString(name)
			}
			extra = b.String()
		}
		panic(fmt.Sprintf("test timed out after %v%s", *timeout, extra))
	})
	return deadline
}

// runningList 返回正在运行的测试列表。
func runningList() []string {
	var list []string
	running.Range(func(k, v any) bool {
		list = append(list, fmt.Sprintf("%s (%v)", k.(string), highPrecisionTimeSince(v.(highPrecisionTime)).Round(time.Second)))
		return true
	})
	slices.Sort(list)
	return list
}

// stopAlarm 关闭定时器。
func (m *M) stopAlarm() {
	if *timeout > 0 {
		m.timer.Stop()
	}
}

func parseCpuList() {
	for val := range strings.SplitSeq(*cpuListStr, ",") {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		cpu, err := strconv.Atoi(val)
		if err != nil || cpu <= 0 {
			fmt.Fprintf(os.Stderr, "testing: invalid value %q for -test.cpu\n", val)
			os.Exit(1)
		}
		cpuList = append(cpuList, cpu)
	}
	if cpuList == nil {
		cpuList = append(cpuList, runtime.GOMAXPROCS(-1))
	}
}

func shouldFailFast() bool {
	return *failFast && numFailed.Load() > 0
}
