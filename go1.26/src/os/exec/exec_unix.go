// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !plan9 && !windows

package exec

import (
	"io/fs"
	"syscall"
)

// skipStdinCopyError 可选地指定一个函数，用于报告所提供的 stdin 复制错误
// 是否应被忽略。
func skipStdinCopyError(err error) bool {
	// 如果程序在其他方面成功完成，则忽略向 stdin 复制时出现的 EPIPE 错误。
	// 参见 Issue 9173。
	pe, ok := err.(*fs.PathError)
	return ok &&
		pe.Op == "write" && pe.Path == "|1" &&
		pe.Err == syscall.EPIPE
}
