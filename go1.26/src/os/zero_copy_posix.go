// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || js || wasip1 || windows

package os

import (
	"io"
	"syscall"
)

// wrapSyscallError 接收一个错误和一个系统调用名。如果该错误
// 是 syscall.Errno，则使用该系统调用名将其包装为 os.SyscallError。
func wrapSyscallError(name string, err error) error {
	if _, ok := err.(syscall.Errno); ok {
		err = NewSyscallError(name, err)
	}
	return err
}

// tryLimitedReader 尝试将 io.Reader 断言为 io.LimitedReader，如果断言成功，
// 它返回该 io.LimitedReader、底层的 io.Reader 以及剩余的字节数，
// 否则只返回原始的 io.Reader 和理论上无限的剩余字节数。
func tryLimitedReader(r io.Reader) (*io.LimitedReader, io.Reader, int64) {
	var remain int64 = 1<<63 - 1 // 默认情况下，复制直到 EOF

	lr, ok := r.(*io.LimitedReader)
	if !ok {
		return nil, r, remain
	}

	remain = lr.N
	return lr, lr.R, remain
}
