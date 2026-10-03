// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 我们曾经对 Darwin 使用这段代码，但根据 issue #19314，
// 即使使用 WEXITED，当进程被停止时 waitid 也会返回。

//go:build linux

package os

import (
	"internal/syscall/unix"
	"runtime"
	"syscall"
)

// blockUntilWaitable 尝试阻塞，直到对 p.Wait 的调用
// 能够立即成功，并报告是否已做到这一点。
// 它实际上并不调用 p.Wait。
func (p *Process) blockUntilWaitable() (bool, error) {
	var info unix.SiginfoChild
	err := ignoringEINTR(func() error {
		return unix.Waitid(unix.P_PID, p.Pid, &info, syscall.WEXITED|syscall.WNOWAIT, nil)
	})
	runtime.KeepAlive(p)
	if err != nil {
		// waitid 自 Linux 2.6.9 起就已可用，但
		// 据报道在 Windows 上的 Ubuntu 中不可用。
		// 参见 issue 16610。
		if err == syscall.ENOSYS {
			return false, nil
		}
		return false, NewSyscallError("waitid", err)
	}
	return true, nil
}
