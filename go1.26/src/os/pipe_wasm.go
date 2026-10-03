// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm

package os

import "syscall"

// Pipe 返回一对相连的 File；从 r 读取到的内容正是写入 w 的字节。
// 如果发生错误，它会返回这些文件和该错误。
func Pipe() (r *File, w *File, err error) {
	// GOOS=js 和 GOOS=wasip1 都没有管道。
	return nil, nil, NewSyscallError("pipe", syscall.ENOSYS)
}
