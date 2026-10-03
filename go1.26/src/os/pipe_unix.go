// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || darwin

package os

import "syscall"

// Pipe 返回一对相连的 File；从 r 读取到的内容正是写入 w 的字节。
// 如果发生错误，它会返回这些文件和该错误。
func Pipe() (r *File, w *File, err error) {
	var p [2]int

	// 有关锁的说明，参见 ../syscall/exec.go。
	syscall.ForkLock.RLock()
	e := syscall.Pipe(p[0:])
	if e != nil {
		syscall.ForkLock.RUnlock()
		return nil, nil, NewSyscallError("pipe", e)
	}
	syscall.CloseOnExec(p[0])
	syscall.CloseOnExec(p[1])
	syscall.ForkLock.RUnlock()

	return newFile(p[0], "|0", kindPipe, false), newFile(p[1], "|1", kindPipe, false), nil
}
