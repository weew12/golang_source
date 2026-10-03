// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import "internal/testlog"

// Stat 返回描述指定文件的 [FileInfo]。
// 如果出错，错误的类型为 [*PathError]。
func Stat(name string) (FileInfo, error) {
	testlog.Stat(name)
	return statNolog(name)
}

// Lstat 返回描述指定文件的 [FileInfo]。
// 如果该文件是符号链接，返回的 FileInfo
// 描述的是该符号链接。Lstat 不会尝试跟随该链接。
// 如果出错，错误的类型为 [*PathError]。
//
// 在 Windows 上，如果该文件是一个作为另一个命名实体（例如符号链接或
// 挂载文件夹）替代的重解析点，返回的 FileInfo 描述该重解析点，
// 并且不会尝试解析它。
func Lstat(name string) (FileInfo, error) {
	testlog.Stat(name)
	return lstatNolog(name)
}

// stathook 在测试中设置
var stathook func(f *File, name string) (FileInfo, error)
