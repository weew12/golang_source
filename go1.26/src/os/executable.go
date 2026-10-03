// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

// Executable 返回启动当前进程的可执行文件的路径名。
// 不保证该路径仍指向正确的可执行文件。如果使用符号链接
// 启动进程，取决于操作系统，结果可能是符号链接
// 或其指向的路径。如果需要稳定的结果，
// [path/filepath.EvalSymlinks] 可能有帮助。
//
// 除非发生错误，Executable 返回绝对路径。
//
// 主要用例是查找相对于可执行文件位置的资源。
func Executable() (string, error) {
	return executable()
}
