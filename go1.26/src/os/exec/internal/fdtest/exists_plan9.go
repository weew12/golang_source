// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build plan9

package fdtest

import (
	"syscall"
)

const errBadFd = syscall.ErrorString("fd out of range or not open")

// Exists 如果 fd 是有效的文件描述符则返回 true。
func Exists(fd uintptr) bool {
	var buf [1]byte
	_, err := syscall.Fstat(int(fd), buf[:])
	return err != errBadFd
}
