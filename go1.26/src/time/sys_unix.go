// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || (js && wasm) || wasip1

package time

import (
	"errors"
	"runtime"
	"syscall"
)

// 用于测试：任何会中断 sleep 的行为
func interrupt() {
	// wasi 中没有机制可以中断用于实现 runtime.usleep 的
	// poll_oneoff 调用，因此此函数什么都不做，这
	// 在某种程度上削弱了 TestSleep 的意义，但验证进程
	// 调用 time.Sleep 时时间会流逝，总比
	// 完全跳过该测试要好。
	if runtime.GOOS != "wasip1" {
		syscall.Kill(syscall.Getpid(), syscall.SIGCHLD)
	}
}

func open(name string) (uintptr, error) {
	fd, err := syscall.Open(name, syscall.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	return uintptr(fd), nil
}

func read(fd uintptr, buf []byte) (int, error) {
	return syscall.Read(int(fd), buf)
}

func closefd(fd uintptr) {
	syscall.Close(int(fd))
}

func preadn(fd uintptr, buf []byte, off int) error {
	whence := seekStart
	if off < 0 {
		whence = seekEnd
	}
	if _, err := syscall.Seek(int(fd), int64(off), whence); err != nil {
		return err
	}
	for len(buf) > 0 {
		m, err := syscall.Read(int(fd), buf)
		if m <= 0 {
			if err == nil {
				return errors.New("short read")
			}
			return err
		}
		buf = buf[m:]
	}
	return nil
}
