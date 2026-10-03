// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 对 pidfd 的支持是在若干个 Linux 版本中逐步加入的：
//  v5.1：pidfd_send_signal 系统调用；
//  v5.2：clone 系统调用的 CLONE_PIDFD 标志；
//  v5.3：pidfd_open 系统调用、clone3 系统调用；
//  v5.4：waitid 系统调用对 P_PIDFD idtype 的支持；
//  v5.6：pidfd_getfd 系统调用。
//
// 注意：其他 Linux 实现可能不遵循此顺序。例如，
// QEMU 用户模式 7.2 添加了 pidfd_open，但直到 8.0
// 才添加 CLONE_PIDFD。

package os

import (
	"internal/syscall/unix"
	"runtime"
	"sync"
	"syscall"
	_ "unsafe" // 用于 linkname
)

// ensurePidfd 初始化 sysAttr 中的 PidFD 字段（如果尚未设置）。
// 它返回原始或修改后的 SysProcAttr 结构体，以及一个标志，
// 指示使用前是否应复制 PidFD。
func ensurePidfd(sysAttr *syscall.SysProcAttr) (*syscall.SysProcAttr, bool) {
	if !pidfdWorks() {
		return sysAttr, false
	}

	var pidfd int

	if sysAttr == nil {
		return &syscall.SysProcAttr{
			PidFD: &pidfd,
		}, false
	}
	if sysAttr.PidFD == nil {
		newSys := *sysAttr // 复制
		newSys.PidFD = &pidfd
		return &newSys, false
	}

	return sysAttr, true
}

// getPidfd 返回 sysAttr.PidFD 的值（如果设置了 needDup，则返回其副本），
// 以及一个标志，指示该值是否可以使用。
func getPidfd(sysAttr *syscall.SysProcAttr, needDup bool) (uintptr, bool) {
	if !pidfdWorks() {
		return 0, false
	}

	h := *sysAttr.PidFD
	if needDup {
		dupH, e := unix.Fcntl(h, syscall.F_DUPFD_CLOEXEC, 0)
		if e != nil {
			return 0, false
		}
		h = dupH
	}
	return uintptr(h), true
}

// pidfdFind 返回 pid 的进程句柄。
func pidfdFind(pid int) (uintptr, error) {
	if !pidfdWorks() {
		return 0, syscall.ENOSYS
	}

	h, err := unix.PidFDOpen(pid, 0)
	if err != nil {
		return 0, convertESRCH(err)
	}
	return h, nil
}

// pidfdWait 等待进程完成，
// 并将进程状态更新为 done。
func (p *Process) pidfdWait() (*ProcessState, error) {
	// 使用 pidfd 时，不存在 wait/kill 竞态（在 CL 23967 中描述），
	// 因为不存在 PID 回收问题（换句话说，与 PID 不同，
	// pidfd 保证指向某一个特定进程）。因此，不需要
	// pidWait 中的变通方案（blockUntilWaitable + sigMu）。
	//
	// 当关闭 pidfd 时，我们_确实_需要小心 pidfd 的 FD 编号被重用。
	// 详见 handle。
	handle, status := p.handleTransientAcquire()
	switch status {
	case statusDone:
		// 进程已经完成了 Wait，或者未被
		// pidfdFind 找到。返回 ECHILD 以与 wait
		// 系统调用会返回的结果保持一致。
		return nil, NewSyscallError("wait", syscall.ECHILD)
	case statusReleased:
		return nil, syscall.EINVAL
	}
	defer p.handleTransientRelease()

	var (
		info   unix.SiginfoChild
		rusage syscall.Rusage
	)
	err := ignoringEINTR(func() error {
		return unix.Waitid(unix.P_PIDFD, int(handle), &info, syscall.WEXITED, &rusage)
	})
	if err != nil {
		return nil, NewSyscallError("waitid", err)
	}

	// 将 Process 状态更新为 statusDone。
	// 这也会释放对句柄的一个引用。
	p.doRelease(statusDone)

	return &ProcessState{
		pid:    int(info.Pid),
		status: info.WaitStatus(),
		rusage: &rusage,
	}, nil
}

// pidfdSendSignal 向进程发送信号。
func (p *Process) pidfdSendSignal(s syscall.Signal) error {
	handle, status := p.handleTransientAcquire()
	switch status {
	case statusDone:
		return ErrProcessDone
	case statusReleased:
		return errProcessReleased
	}
	defer p.handleTransientRelease()

	return convertESRCH(unix.PidFDSendSignal(handle, s))
}

// pidfdWorks 返回我们能否在此系统上使用 pidfd。
func pidfdWorks() bool {
	return checkPidfdOnce() == nil
}

// checkPidfdOnce 用于仅检查一次 pidfd 是否可用。
var checkPidfdOnce = sync.OnceValue(checkPidfd)

// checkPidfd 检查所有必需的 pidfd 相关系统调用是否可用。这
// 包括 pidfd_open 和 pidfd_send_signal 系统调用、idtype 为 P_PIDFD 的
// waitid 系统调用，以及 clone(CLONE_PIDFD)。
//
// pidfd 系统调用不可用的原因包括内核较旧，以及
// 执行环境中的上述系统调用受到 seccomp 或类似技术的限制。
func checkPidfd() error {
	// 在 Android 版本 < 12 中，seccomp 不允许 pidfd 相关的系统调用，
	// 并会触发 SIGSYS 信号。参见 issue #69065。
	if runtime.GOOS == "android" {
		ignoreSIGSYS()
		defer restoreSIGSYS()
	}

	// 获取当前进程的 pidfd（打开 "/proc/self" 对
	// waitid 不起作用）。
	fd, err := unix.PidFDOpen(syscall.Getpid(), 0)
	if err != nil {
		return NewSyscallError("pidfd_open", err)
	}
	defer syscall.Close(int(fd))

	// 检查 waitid(P_PIDFD) 是否可用。
	err = ignoringEINTR(func() error {
		var info unix.SiginfoChild
		// 我们实际上并不关心 info，但传递 nil 指针
		// 会让 valgrind 报错，因为 0x0 不可寻址。
		return unix.Waitid(unix.P_PIDFD, int(fd), &info, syscall.WEXITED, nil)
	})
	// 预期 waitid 返回 ECHILD，因为我们不是自己的父进程。
	if err != syscall.ECHILD {
		return NewSyscallError("pidfd_wait", err)
	}

	// 检查 pidfd_send_signal 是否可用（应能向自身发送 0）。
	if err := unix.PidFDSendSignal(fd, 0); err != nil {
		return NewSyscallError("pidfd_send_signal", err)
	}

	// 验证 clone(CLONE_PIDFD) 是否可用。
	//
	// 这本不必要，因为 pidfd_open 是在 Linux 5.3 中添加的，
	// 晚于 Linux 5.2 中的 CLONE_PIDFD，但某些替代的 Linux
	// 实现可能不遵循此顺序。
	if err := checkClonePidfd(); err != nil {
		return err
	}

	return nil
}

// 由 syscall 提供。
//
//go:linkname checkClonePidfd
func checkClonePidfd() error

// 由 runtime 提供。
//
//go:linkname ignoreSIGSYS
func ignoreSIGSYS()

//go:linkname restoreSIGSYS
func restoreSIGSYS()
