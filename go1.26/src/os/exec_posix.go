// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || (js && wasm) || wasip1 || windows

package os

import (
	"internal/strconv"
	"internal/syscall/execenv"
	"runtime"
	"syscall"
)

// 在所有系统上，os 包中保证存在的唯一信号值是 os.Interrupt（向进程发送中断）
// 和 os.Kill（强制进程退出）。在 Windows 上，使用 os.Process.Signal
// 向进程发送 os.Interrupt 未实现；它会返回错误而不是发送信号。
var (
	Interrupt Signal = syscall.SIGINT
	Kill      Signal = syscall.SIGKILL
)

func startProcess(name string, argv []string, attr *ProcAttr) (p *Process, err error) {
	// 如果没有 SysProcAttr（即没有 Chroot 或改变 UID/GID），
	// 则再次检查我们想要 chdir 进入的目录是否存在。
	// 这样可以让我们把错误表达得更清晰。
	if attr != nil && attr.Sys == nil && attr.Dir != "" {
		if _, err := Stat(attr.Dir); err != nil {
			pe := err.(*PathError)
			pe.Op = "chdir"
			return nil, pe
		}
	}

	attrSys, shouldDupPidfd := ensurePidfd(attr.Sys)
	sysattr := &syscall.ProcAttr{
		Dir: attr.Dir,
		Env: attr.Env,
		Sys: attrSys,
	}
	if sysattr.Env == nil {
		sysattr.Env, err = execenv.Default(sysattr.Sys)
		if err != nil {
			return nil, err
		}
	}
	sysattr.Files = make([]uintptr, 0, len(attr.Files))
	for _, f := range attr.Files {
		sysattr.Files = append(sysattr.Files, f.Fd())
	}

	pid, h, e := syscall.StartProcess(name, argv, sysattr)

	// 确保我们不会运行 attr.Files 的终结器。
	runtime.KeepAlive(attr)

	if e != nil {
		return nil, &PathError{Op: "fork/exec", Path: name, Err: e}
	}

	// 对于 Windows，上面的 syscall.StartProcess 已经返回了一个进程句柄。
	if runtime.GOOS != "windows" {
		var ok bool
		h, ok = getPidfd(sysattr.Sys, shouldDupPidfd)
		if !ok {
			return newPIDProcess(pid), nil
		}
	}

	return newHandleProcess(pid, h), nil
}

func (p *Process) kill() error {
	return p.Signal(Kill)
}

func (p *Process) withHandle(f func(handle uintptr)) error {
	if p.handle == nil {
		return ErrNoHandle
	}
	handle, status := p.handleTransientAcquire()
	switch status {
	case statusDone:
		return ErrProcessDone
	case statusReleased:
		return errProcessReleased
	}
	defer p.handleTransientRelease()
	f(handle)

	return nil
}

// ProcessState 存储由 Wait 报告的有关进程的信息。
type ProcessState struct {
	pid    int                // 进程的 id。
	status syscall.WaitStatus // 系统相关的状态信息。
	rusage *syscall.Rusage
}

// Pid 返回已退出进程的进程 id。
func (p *ProcessState) Pid() int {
	return p.pid
}

func (p *ProcessState) exited() bool {
	return p.status.Exited()
}

func (p *ProcessState) success() bool {
	return p.status.ExitStatus() == 0
}

func (p *ProcessState) sys() any {
	return p.status
}

func (p *ProcessState) sysUsage() any {
	return p.rusage
}

func (p *ProcessState) String() string {
	if p == nil {
		return "<nil>"
	}
	status := p.Sys().(syscall.WaitStatus)
	res := ""
	switch {
	case status.Exited():
		code := status.ExitStatus()
		if runtime.GOOS == "windows" && uint(code) >= 1<<16 { // windows 使用大的十六进制数
			res = "exit status 0x" + strconv.FormatUint(uint64(code), 16)
		} else { // Unix 系统使用小的十进制整数
			res = "exit status " + strconv.Itoa(code) // unix
		}
	case status.Signaled():
		res = "signal: " + status.Signal().String()
	case status.Stopped():
		res = "stop signal: " + status.StopSignal().String()
		if status.StopSignal() == syscall.SIGTRAP && status.TrapCause() != 0 {
			res += " (trap " + strconv.Itoa(status.TrapCause()) + ")"
		}
	case status.Continued():
		res = "continued"
	}
	if status.CoreDump() {
		res += " (core dumped)"
	}
	return res
}

// ExitCode 返回已退出进程的退出码，如果进程尚未退出或被信号终止，则返回 -1。
func (p *ProcessState) ExitCode() int {
	// 如果进程尚未启动，则返回 -1。
	if p == nil {
		return -1
	}
	return p.status.ExitStatus()
}
