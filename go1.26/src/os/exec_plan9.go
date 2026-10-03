// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/strconv"
	"syscall"
	"time"
)

// 在所有系统上，os 包中保证存在的唯一信号值是 Interrupt（向进程发送中断）
// 和 Kill（强制进程退出）。Interrupt 未在 Windows 上实现；
// 将其与 [os.Process.Signal] 一起使用会返回错误。
var (
	Interrupt Signal = syscall.Note("interrupt")
	Kill      Signal = syscall.Note("kill")
)

func startProcess(name string, argv []string, attr *ProcAttr) (p *Process, err error) {
	sysattr := &syscall.ProcAttr{
		Dir: attr.Dir,
		Env: attr.Env,
		Sys: attr.Sys,
	}

	sysattr.Files = make([]uintptr, 0, len(attr.Files))
	for _, f := range attr.Files {
		sysattr.Files = append(sysattr.Files, f.Fd())
	}

	pid, _, e := syscall.StartProcess(name, argv, sysattr)
	if e != nil {
		return nil, &PathError{Op: "fork/exec", Path: name, Err: e}
	}

	return newPIDProcess(pid), nil
}

func (p *Process) writeProcFile(file string, data string) error {
	f, e := OpenFile("/proc/"+strconv.Itoa(p.Pid)+"/"+file, O_WRONLY, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.Write([]byte(data))
	return e
}

func (p *Process) signal(sig Signal) error {
	switch p.pidStatus() {
	case statusDone:
		return ErrProcessDone
	case statusReleased:
		return syscall.ENOENT
	}

	if e := p.writeProcFile("note", sig.String()); e != nil {
		return NewSyscallError("signal", e)
	}
	return nil
}

func (p *Process) kill() error {
	return p.signal(Kill)
}

func (p *Process) wait() (ps *ProcessState, err error) {
	var waitmsg syscall.Waitmsg

	switch p.pidStatus() {
	case statusReleased:
		return nil, ErrInvalid
	}

	err = syscall.WaitProcess(p.Pid, &waitmsg)
	if err != nil {
		return nil, NewSyscallError("wait", err)
	}

	p.doRelease(statusDone)
	ps = &ProcessState{
		pid:    waitmsg.Pid,
		status: &waitmsg,
	}
	return ps, nil
}

func (p *Process) withHandle(_ func(handle uintptr)) error {
	return ErrNoHandle
}

func findProcess(pid int) (p *Process, err error) {
	// Plan 9 上的空操作。
	return newPIDProcess(pid), nil
}

// ProcessState 存储由 Wait 报告的有关进程的信息。
type ProcessState struct {
	pid    int              // 进程的 id。
	status *syscall.Waitmsg // 系统相关的状态信息。
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
	return p.status
}

func (p *ProcessState) userTime() time.Duration {
	return time.Duration(p.status.Time[0]) * time.Millisecond
}

func (p *ProcessState) systemTime() time.Duration {
	return time.Duration(p.status.Time[1]) * time.Millisecond
}

func (p *ProcessState) String() string {
	if p == nil {
		return "<nil>"
	}
	return "exit status: " + p.status.Msg
}

// ExitCode 返回已退出进程的退出码，如果进程尚未退出或被信号终止，则返回 -1。
func (p *ProcessState) ExitCode() int {
	// 如果进程尚未启动，则返回 -1。
	if p == nil {
		return -1
	}
	return p.status.ExitStatus()
}
