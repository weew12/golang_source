// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm

package exec

import (
	"errors"
)

// ErrNotFound 是路径搜索未能找到可执行文件时产生的错误。
var ErrNotFound = errors.New("executable file not found in $PATH")

// LookPath 在 PATH 环境变量所指定的目录中搜索名为 file 的可执行文件。
// 如果 file 包含斜杠，则直接尝试它，不查询 PATH。
// 结果可以是绝对路径，也可以是相对于当前目录的路径。
func lookPath(file string) (string, error) {
	// Wasm 无法执行进程，因此表现得好像完全没有可执行文件一样。
	return "", &Error{file, ErrNotFound}
}

// lookExtensions 在非 Windows 平台上是一个空操作，
// 因为这些平台不将可执行文件限制为特定的扩展名。
func lookExtensions(path, dir string) (string, error) {
	return path, nil
}
