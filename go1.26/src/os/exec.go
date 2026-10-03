// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"errors"
	"internal/testlog"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	// ErrProcessDone 表示一个 [Process] 已完成。
	ErrProcessDone = errors.New("os: process already finished")
	// errProcessReleased 表示一个 [Process] 已被释放。
	errProcessReleased = errors.New("os: process already released")
	// ErrNoHandle 表示一个 [Process] 没有句柄。
	ErrNoHandle = errors.New("os: process handle unavailable")
)

// processStatus 描述 [Process] 的状态。
type processStatus uint32

const (
	// statusOK 表示 Process 已准备好使用。
	statusOK processStatus = iota

	// statusDone 表示不应再使用该 PID/句柄，因为
	// 进程已完成（已成功对其执行 Wait）。
	statusDone

	// statusReleased 表示不应再使用该 PID/句柄，
	// 因为进程已被释放。
	statusReleased
)

// Process 存储由 [StartProcess] 创建的进程的信息。
type Process struct {
	Pid int

	// state 包含原子的进程状态。
	//
	// 它由 processStatus 字段组成，
	// 这些字段指示进程是否已完成/已释放。
	state atomic.Uint32

	// 仅在 handle 为 nil 时使用
	sigMu sync.RWMutex // 避免 wait 与 signal 之间的竞态

	// handle 若非 nil，则是指向一个保存
	// 操作系统特定进程句柄的结构的指针。
	// 该指针在创建 Process 时设置，
	// 之后永不改变。
	// 它是指向单独内存分配的指针，
	// 以便我们可以使用 runtime.AddCleanup。
	handle *processHandle

	// cleanup 用于清理进程句柄。
	cleanup runtime.Cleanup
}

// processHandle 保存进程的操作系统句柄。
// 它仅用于支持该概念的系统，
// 目前是 Linux 和 Windows。
// 它维护句柄的引用计数，
// 并在引用降至零时关闭句柄。
type processHandle struct {
	// 实际的句柄。不应直接使用此字段。
	// 请改用 acquire 和 release 方法。
	//
	// 在 Windows 上，这是由 OpenProcess 返回的句柄。
	// 在 Linux 上，这是一个 pidfd。
	handle uintptr

	// 活动引用的数量。当它降至零时，
	// 句柄将被关闭。
	refs atomic.Int32
}

// acquire 增加一个引用并返回句柄。
// 布尔结果报告 acquire 是否成功；
// 如果句柄已关闭则会失败。
// 每次成功的 acquire 调用都应与一次 release 调用配对。
func (ph *processHandle) acquire() (uintptr, bool) {
	for {
		refs := ph.refs.Load()
		if refs < 0 {
			panic("internal error: negative process handle reference count")
		}
		if refs == 0 {
			return 0, false
		}
		if ph.refs.CompareAndSwap(refs, refs+1) {
			return ph.handle, true
		}
	}
}

// release 释放对句柄的一次引用。
func (ph *processHandle) release() {
	for {
		refs := ph.refs.Load()
		if refs <= 0 {
			panic("internal error: too many releases of process handle")
		}
		if ph.refs.CompareAndSwap(refs, refs-1) {
			if refs == 1 {
				ph.closeHandle()
			}
			return
		}
	}
}

// newPIDProcess 为给定的 PID 返回一个 [Process]。
func newPIDProcess(pid int) *Process {
	p := &Process{
		Pid: pid,
	}
	return p
}

// newHandleProcess 返回一个具有给定 PID 和句柄的 [Process]。
func newHandleProcess(pid int, handle uintptr) *Process {
	ph := &processHandle{
		handle: handle,
	}

	// 将引用计数初始化为 1，
	// 表示来自所返回 Process 的引用。
	ph.refs.Store(1)

	p := &Process{
		Pid:    pid,
		handle: ph,
	}

	p.cleanup = runtime.AddCleanup(p, (*processHandle).release, ph)

	return p
}

// newDoneProcess 为给定的 PID 返回一个
// 已标记为完成的 [Process]。它用于 Unix 系统上
// 已知进程不存在的情况。
func newDoneProcess(pid int) *Process {
	p := &Process{
		Pid: pid,
	}
	p.state.Store(uint32(statusDone)) // 没有持久引用，因为没有句柄。
	return p
}

// handleTransientAcquire 返回进程句柄，或者，
// 如果进程尚未就绪，返回当前状态。
func (p *Process) handleTransientAcquire() (uintptr, processStatus) {
	if p.handle == nil {
		panic("handleTransientAcquire called in invalid mode")
	}

	status := processStatus(p.state.Load())
	if status != statusOK {
		return 0, status
	}
	h, ok := p.handle.acquire()
	if ok {
		return h, statusOK
	}

	// 这种情况意味着句柄已被关闭。
	// 我们总是在关闭句柄之前将状态设置为非零。
	// 如果走到这里，那么状态必定是在
	// 我们上面刚检查过之后被设置为非零的。
	status = processStatus(p.state.Load())
	if status == statusOK {
		panic("inconsistent process status")
	}
	return 0, status
}

// handleTransientRelease 释放由 handleTransientAcquire 返回的句柄。
func (p *Process) handleTransientRelease() {
	if p.handle == nil {
		panic("handleTransientRelease called in invalid mode")
	}
	p.handle.release()
}

// pidStatus 返回当前进程状态。
func (p *Process) pidStatus() processStatus {
	if p.handle != nil {
		panic("pidStatus called in invalid mode")
	}

	return processStatus(p.state.Load())
}

// ProcAttr 保存将应用于由 StartProcess 启动的
// 新进程的属性。
type ProcAttr struct {
	// 如果 Dir 非空，子进程会在创建进程之前
	// 切换到该目录。
	Dir string
	// 如果 Env 非 nil，它以 Environ 返回的形式给出
	// 新进程的环境变量。
	// 如果为 nil，将使用 Environ 的结果。
	Env []string
	// Files 指定新进程继承的已打开文件。前三个条目
	// 分别对应标准输入、标准输出和标准错误。
	// 根据底层操作系统的不同，实现可能支持额外的条目。
	// nil 条目表示该文件在进程启动时被关闭。
	// 在 Unix 系统上，StartProcess 会将这些 File 值
	// 改为阻塞模式，这意味着 SetDeadline 将停止工作，
	// 并且调用 Close 不会中断 Read 或 Write。
	Files []*File

	// 操作系统特定的进程创建属性。
	// 注意，设置此字段意味着你的程序
	// 在某些操作系统上可能无法正确执行，
	// 甚至无法编译。
	Sys *syscall.SysProcAttr
}

// Signal 表示操作系统信号。
// 通常的底层实现依赖于操作系统：
// 在 Unix 上它是 syscall.Signal。
type Signal interface {
	String() string
	Signal() // 用于与其他 Stringer 区分
}

// Getpid 返回调用者的进程 id。
func Getpid() int { return syscall.Getpid() }

// Getppid 返回调用者父进程的进程 id。
func Getppid() int { return syscall.Getppid() }

// FindProcess 根据 pid 查找正在运行的进程。
//
// 它返回的 [Process] 可用于获取
// 底层操作系统进程的信息。
//
// 在 Unix 系统上，无论进程是否存在，FindProcess 总是成功
// 并为给定的 pid 返回一个 Process。要测试进程是否
// 实际存在，请查看 p.Signal(syscall.Signal(0)) 是否报告错误。
func FindProcess(pid int) (*Process, error) {
	return findProcess(pid)
}

// StartProcess 使用由 name、argv 和 attr 指定的程序、参数和属性
// 启动一个新进程。argv 切片将成为新进程中的 [os.Args]，
// 因此它通常以程序名开头。
//
// 如果调用 goroutine 已使用 [runtime.LockOSThread] 锁定了操作系统线程，
// 并修改了任何可继承的操作系统级线程状态
// （例如 Linux 或 Plan 9 命名空间），则新进程
// 将继承调用者的线程状态。
//
// StartProcess 是一个低级接口。[os/exec] 包提供了
// 更高级的接口。
//
// 如果发生错误，其类型将是 [*PathError]。
func StartProcess(name string, argv []string, attr *ProcAttr) (*Process, error) {
	testlog.Open(name)
	return startProcess(name, argv, attr)
}

// Release 释放与 [Process] p 关联的任何资源，
// 使其在以后不可用。
// 仅在未调用 [Process.Wait] 时才需要调用 Release。
func (p *Process) Release() error {
	// 遗憾的是，由于历史原因，在 Windows 以外的系统上，
	// Release 会将 Pid 字段设置为 -1。
	// 这会导致竞态检测器在并发调用 Release 时
	// 报告问题，但我们现在无法更改它。
	if runtime.GOOS != "windows" {
		p.Pid = -1
	}

	oldStatus := p.doRelease(statusReleased)

	// 为了向后兼容，仅在 Windows 上，
	// 我们对第二次调用 Release 返回 EINVAL。
	if runtime.GOOS == "windows" {
		if oldStatus == statusReleased {
			return syscall.EINVAL
		}
	}

	return nil
}

// doRelease 释放一个 [Process]，将状态设置为 newStatus。
// 如果之前的状态不是 statusOK，则什么也不做。
// 它返回之前的状态。
func (p *Process) doRelease(newStatus processStatus) processStatus {
	for {
		state := p.state.Load()
		oldStatus := processStatus(state)
		if oldStatus != statusOK {
			return oldStatus
		}

		if !p.state.CompareAndSwap(state, uint32(newStatus)) {
			continue
		}

		// 我们已成功释放 Process。
		// 如果它有句柄，则释放在
		// newHandleProcess 中创建的引用。
		if p.handle != nil {
			// 无需再进行清理。
			// 我们必须在调用 release 之前停止 cleanup；
			// 否则 cleanup 可能与 release 并发运行，
			// 这会导致引用计数无效，从而引发 panic。
			p.cleanup.Stop()

			p.handle.release()
		}

		return statusOK
	}
}

// Kill 使 [Process] 立即退出。Kill 不会等到
// Process 实际退出。它只杀死 Process 本身，
// 而不杀死它可能启动的任何其他进程。
func (p *Process) Kill() error {
	return p.kill()
}

// Wait 等待 [Process] 退出，然后返回一个
// 描述其状态的 ProcessState 以及错误（如果有）。
// Wait 释放与 Process 关联的任何资源。
// 在大多数操作系统上，Process 必须是当前进程的
// 子进程，否则将返回错误。
func (p *Process) Wait() (*ProcessState, error) {
	return p.wait()
}

// Signal 向 [Process] 发送信号。
// 在 Windows 上发送 [Interrupt] 尚未实现。
func (p *Process) Signal(sig Signal) error {
	return p.signal(sig)
}

// WithHandle 调用提供的函数 f，并将一个有效的进程句柄
// 作为参数。该句柄保证在 f 返回之前一直指向进程 p，
// 即使 p 已终止。此函数不能在
// [Process.Release] 或 [Process.Wait] 之后使用。
//
// 如果不支持进程句柄或句柄不可用，
// 它返回 [ErrNoHandle]。目前，进程句柄在
// Linux 5.4 或更高版本（pidfd）以及 Windows 上受支持。
func (p *Process) WithHandle(f func(handle uintptr)) error {
	return p.withHandle(f)
}

// UserTime 返回已退出进程及其子进程的用户 CPU 时间。
func (p *ProcessState) UserTime() time.Duration {
	return p.userTime()
}

// SystemTime 返回已退出进程及其子进程的系统 CPU 时间。
func (p *ProcessState) SystemTime() time.Duration {
	return p.systemTime()
}

// Exited 报告程序是否已退出。
// 在 Unix 系统上，如果程序因调用 exit 而退出，则报告 true，
// 如果程序因信号而终止，则报告 false。
func (p *ProcessState) Exited() bool {
	return p.exited()
}

// Success 报告程序是否成功退出，
// 例如在 Unix 上以退出状态 0 退出。
func (p *ProcessState) Success() bool {
	return p.success()
}

// Sys 返回关于进程的依赖于系统的退出信息。
// 将其转换为合适的底层类型，
// 例如 Unix 上的 [syscall.WaitStatus]，以访问其内容。
func (p *ProcessState) Sys() any {
	return p.sys()
}

// SysUsage 返回关于已退出进程的依赖于系统的资源使用信息。
// 将其转换为合适的底层类型，
// 例如 Unix 上的 [*syscall.Rusage]，以访问其内容。
// （在 Unix 上，*syscall.Rusage 与 getrusage(2) 手册页中
// 定义的 struct rusage 匹配。）
func (p *ProcessState) SysUsage() any {
	return p.sysUsage()
}
