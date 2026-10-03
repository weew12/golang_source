// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || (js && wasm) || wasip1

package os

import (
	"errors"
	"syscall"
	"time"
)

const (
	// Process.Pid 的特殊值。
	pidUnset    = 0
	pidReleased = -1
)

func (p *Process) wait() (ps *ProcessState, err error) {
	// 我们拥有的是哪种类型的 Process？
	if p.handle != nil {
		// pidfd
		return p.pidfdWait()
	} else {
		// 普通 PID
		return p.pidWait()
	}
}

func (p *Process) pidWait() (*ProcessState, error) {
	// TODO(go.dev/issue/67642)：当存在并发的 Wait 调用时，
	// 如果在进程完成其等待后 PID 被复用，
	// 其中一个调用可能会等待错误的进程。
	//
	// 在此处检查 statusDone 并非完整的修复，
	// 因为在 blockUntilWaitable 之前，该 PID 仍可能被等待并被复用。
	switch p.pidStatus() {
	case statusReleased:
		return nil, syscall.EINVAL
	}

	// 如果我们可以阻塞直到 Wait4 立即成功，就阻塞。
	ready, err := p.blockUntilWaitable()
	if err != nil {
		return nil, err
	}
	if ready {
		// 现在、在调用 Wait4 之前就把进程标记为已完成，
		// 这样 Process.pidSignal 就不会发送信号。
		p.doRelease(statusDone)
		// 在 sigMu 上获取写锁，以等待任何
		// 正在进行的 signal 方法调用完成。
		p.sigMu.Lock()
		p.sigMu.Unlock()
	}

	var (
		status syscall.WaitStatus
		rusage syscall.Rusage
	)
	pid1, err := ignoringEINTR2(func() (int, error) {
		return syscall.Wait4(p.Pid, &status, 0, &rusage)
	})
	if err != nil {
		return nil, NewSyscallError("wait", err)
	}
	p.doRelease(statusDone)
	return &ProcessState{
		pid:    pid1,
		status: status,
		rusage: &rusage,
	}, nil
}

func (p *Process) signal(sig Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return errors.New("os: unsupported signal type")
	}

	// 我们拥有的是哪种类型的 Process？
	if p.handle != nil {
		// pidfd
		return p.pidfdSendSignal(s)
	} else {
		// 普通 PID
		return p.pidSignal(s)
	}
}

func (p *Process) pidSignal(s syscall.Signal) error {
	if p.Pid == pidReleased {
		return errProcessReleased
	}
	if p.Pid == pidUnset {
		return errors.New("os: process not initialized")
	}

	p.sigMu.RLock()
	defer p.sigMu.RUnlock()

	switch p.pidStatus() {
	case statusDone:
		return ErrProcessDone
	case statusReleased:
		return errProcessReleased
	}

	return convertESRCH(syscall.Kill(p.Pid, s))
}

func convertESRCH(err error) error {
	if err == syscall.ESRCH {
		return ErrProcessDone
	}
	return err
}

func findProcess(pid int) (p *Process, err error) {
	h, err := pidfdFind(pid)
	if err == ErrProcessDone {
		// 我们不能在这里返回错误，因为用户并不期望如此。
		// 相反，返回一个已经处于 “done” 状态的进程，
		// 让后续的 Signal 或 Wait 调用捕获它。
		return newDoneProcess(pid), nil
	} else if err != nil {
		// 忽略 pidfdFind 返回的其他错误，因为调用方
		// 并不期望这些错误。回退到使用 PID。
		return newPIDProcess(pid), nil
	}
	// 使用该句柄。
	return newHandleProcess(pid, h), nil
}

func (p *ProcessState) userTime() time.Duration {
	return time.Duration(p.rusage.Utime.Nano()) * time.Nanosecond
}

func (p *ProcessState) systemTime() time.Duration {
	return time.Duration(p.rusage.Stime.Nano()) * time.Nanosecond
}
