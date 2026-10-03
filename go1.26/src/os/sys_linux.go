// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"runtime"
	"syscall"
)

func hostname() (name string, err error) {
	// 首先尝试 uname，因为它只需要一次系统调用，而且
	// 在 Android 上不允许读取 /proc。
	var un syscall.Utsname
	err = syscall.Uname(&un)

	var buf [512]byte // 足以容纳一个 DNS 名称。
	for i, b := range un.Nodename[:] {
		buf[i] = uint8(b)
		if b == 0 {
			name = string(buf[:i])
			break
		}
	}
	// 如果我们得到了一个名称，且它不可能是被截断的
	// （Nodename 为 65 字节），就返回它。
	if err == nil && len(name) > 0 && len(name) < 64 {
		return name, nil
	}
	if runtime.GOOS == "android" {
		if name != "" {
			return name, nil
		}
		return "localhost", nil
	}

	f, err := Open("/proc/sys/kernel/hostname")
	if err != nil {
		return "", err
	}
	defer f.Close()

	n, err := f.Read(buf[:])
	if err != nil {
		return "", err
	}

	if n > 0 && buf[n-1] == '\n' {
		n--
	}
	return string(buf[:n]), nil
}
