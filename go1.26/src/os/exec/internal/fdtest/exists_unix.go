// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || wasm

// Package fdtest 提供了用于处理跨 exec 的文件描述符的测试辅助工具。
package fdtest

import (
	"syscall"
)

// Exists 如果 fd 是有效的文件描述符则返回 true。
func Exists(fd uintptr) bool {
	var s syscall.Stat_t
	err := syscall.Fstat(int(fd), &s)
	return err != syscall.EBADF
}
