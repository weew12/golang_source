// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"errors"
	"internal/syscall/windows"
	"runtime"
	"syscall"
	"time"
)

// 注意，Process.handle 永远不会为 nil，因为 Windows 总是需要一个句柄。
// 手动创建的 Process 字面量是无效的。

func (p *Process) wait() (ps *ProcessState, err error) {
	handle, status := p.handleTransientAcquire()
	switch status {
	case statusDone:
		return nil, ErrProcessDone
	case statusReleased:
		return nil, syscall.EINVAL
	}
	defer p.handleTransientRelease()

	s, e := syscall.WaitForSingleObject(syscall.Handle(handle), syscall.INFINITE)
	switch s {
	case syscall.WAIT_OBJECT_0:
		break
	case syscall.WAIT_FAILED:
		return nil, NewSyscallError("WaitForSingleObject", e)
	default:
		return nil, errors.New("os: unexpected result from WaitForSingleObject")
	}
	var ec uint32
	e = syscall.GetExitCodeProcess(syscall.Handle(handle), &ec)
	if e != nil {
		return nil, NewSyscallError("GetExitCodeProcess", e)
	}
	var u syscall.Rusage
	e = syscall.GetProcessTimes(syscall.Handle(handle), &u.CreationTime, &u.ExitTime, &u.KernelTime, &u.UserTime)
	if e != nil {
		return nil, NewSyscallError("GetProcessTimes", e)
	}

	// 为了兼容性，我们在这里使用 statusReleased
	// 而不是 statusDone。
	p.doRelease(statusReleased)

	return &ProcessState{p.Pid, syscall.WaitStatus{ExitCode: ec}, &u}, nil
}

func (p *Process) signal(sig Signal) error {
	handle, status := p.handleTransientAcquire()
	switch status {
	case statusDone:
		return ErrProcessDone
	case statusReleased:
		return syscall.EINVAL
	}
	defer p.handleTransientRelease()

	if sig == Kill {
		var terminationHandle syscall.Handle
		e := syscall.DuplicateHandle(^syscall.Handle(0), syscall.Handle(handle), ^syscall.Handle(0), &terminationHandle, syscall.PROCESS_TERMINATE, false, 0)
		if e != nil {
			return NewSyscallError("DuplicateHandle", e)
		}
		runtime.KeepAlive(p)
		defer syscall.CloseHandle(terminationHandle)
		e = syscall.TerminateProcess(syscall.Handle(terminationHandle), 1)
		return NewSyscallError("TerminateProcess", e)
	}
	// TODO(rsc)：也要处理 Interrupt 吗？
	return syscall.Errno(syscall.EWINDOWS)
}

func (ph *processHandle) closeHandle() {
	syscall.CloseHandle(syscall.Handle(ph.handle))
}

func findProcess(pid int) (p *Process, err error) {
	const da = syscall.STANDARD_RIGHTS_READ |
		syscall.PROCESS_QUERY_INFORMATION | syscall.SYNCHRONIZE
	h, e := syscall.OpenProcess(da, false, uint32(pid))
	if e != nil {
		return nil, NewSyscallError("OpenProcess", e)
	}
	return newHandleProcess(pid, uintptr(h)), nil
}

func init() {
	cmd := windows.UTF16PtrToString(syscall.GetCommandLine())
	if len(cmd) == 0 {
		arg0, _ := Executable()
		Args = []string{arg0}
	} else {
		Args = commandLineToArgv(cmd)
	}
}

// appendBSBytes 将 n 个 '\\' 字节追加到 b，并返回得到的切片。
func appendBSBytes(b []byte, n int) []byte {
	for ; n > 0; n-- {
		b = append(b, '\\')
	}
	return b
}

// readNextArg 将命令行字符串 cmd 拆分为下一个
// 参数和命令行剩余部分。
func readNextArg(cmd string) (arg []byte, rest string) {
	var b []byte
	var inquote bool
	var nslash int
	for ; len(cmd) > 0; cmd = cmd[1:] {
		c := cmd[0]
		switch c {
		case ' ', '\t':
			if !inquote {
				return appendBSBytes(b, nslash), cmd[1:]
			}
		case '"':
			b = appendBSBytes(b, nslash/2)
			if nslash%2 == 0 {
				// 使用 http://daviddeley.com/autohotkey/parameters/parameters.htm
				// 5.2 节中 “Prior to 2008” 规则
				// 来处理双引号引号
				if inquote && len(cmd) > 1 && cmd[1] == '"' {
					b = append(b, c)
					cmd = cmd[1:]
				}
				inquote = !inquote
			} else {
				b = append(b, c)
			}
			nslash = 0
			continue
		case '\\':
			nslash++
			continue
		}
		b = appendBSBytes(b, nslash)
		nslash = 0
		b = append(b, c)
	}
	return appendBSBytes(b, nslash), ""
}

// commandLineToArgv 将命令行拆分为单独的参数
// 字符串，遵循记录在
// http://daviddeley.com/autohotkey/parameters/parameters.htm#WINARGV 的 Windows 约定。
func commandLineToArgv(cmd string) []string {
	var args []string
	for len(cmd) > 0 {
		if cmd[0] == ' ' || cmd[0] == '\t' {
			cmd = cmd[1:]
			continue
		}
		var arg []byte
		arg, cmd = readNextArg(cmd)
		args = append(args, string(arg))
	}
	return args
}

func ftToDuration(ft *syscall.Filetime) time.Duration {
	n := int64(ft.HighDateTime)<<32 + int64(ft.LowDateTime) // 以 100 纳秒为单位
	return time.Duration(n*100) * time.Nanosecond
}

func (p *ProcessState) userTime() time.Duration {
	return ftToDuration(&p.rusage.UserTime)
}

func (p *ProcessState) systemTime() time.Duration {
	return ftToDuration(&p.rusage.KernelTime)
}
