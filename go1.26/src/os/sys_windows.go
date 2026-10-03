// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/syscall/windows"
	"syscall"
)

func hostname() (name string, err error) {
	// 使用 PhysicalDnsHostname 以在集群中唯一标识主机
	const format = windows.ComputerNamePhysicalDnsHostname

	n := uint32(64)
	for {
		b := make([]uint16, n)
		err := windows.GetComputerNameEx(format, &b[0], &n)
		if err == nil {
			return syscall.UTF16ToString(b[:n]), nil
		}
		if err != syscall.ERROR_MORE_DATA {
			return "", NewSyscallError("ComputerNameEx", err)
		}

		// 如果我们收到了 ERROR_MORE_DATA，但 n 没有变大，
		// 说明出了问题，我们可能陷入了无限循环
		if n <= uint32(len(b)) {
			return "", NewSyscallError("ComputerNameEx", err)
		}
	}
}
