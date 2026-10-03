// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/filepathlite"
	"syscall"
)

// MkdirAll 创建一个名为 path 的目录，
// 以及任何必要的父目录，并返回 nil，
// 否则返回错误。
// 权限位 perm（在 umask 之前）用于 MkdirAll
// 创建的所有目录。
// 如果 path 已经是一个目录，MkdirAll 什么都不做
// 并返回 nil。
func MkdirAll(path string, perm FileMode) error {
	// 快速路径：如果我们能判断 path 是目录还是文件，就以成功或错误结束。
	dir, err := Stat(path)
	if err == nil {
		if dir.IsDir() {
			return nil
		}
		return &PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
	}

	// 慢速路径：确保父目录存在，然后对 path 调用 Mkdir。

	// 通过先移除任何结尾的路径分隔符，然后向后扫描
	// 直到找到路径分隔符或到达字符串开头，
	// 从 path 中提取父文件夹。
	i := len(path) - 1
	for i >= 0 && IsPathSeparator(path[i]) {
		i--
	}
	for i >= 0 && !IsPathSeparator(path[i]) {
		i--
	}
	if i < 0 {
		i = 0
	}

	// 如果存在父目录，且它不是卷名，
	// 则递归以确保父目录存在。
	if parent := path[:i]; len(parent) > len(filepathlite.VolumeName(path)) {
		err = MkdirAll(parent, perm)
		if err != nil {
			return err
		}
	}

	// 父目录现在存在；调用 Mkdir 并使用其结果。
	err = Mkdir(path, perm)
	if err != nil {
		// 通过再次检查目录不存在来处理
		// 类似 "foo/." 的参数。
		dir, err1 := Lstat(path)
		if err1 == nil && dir.IsDir() {
			return nil
		}
		return err
	}
	return nil
}

// RemoveAll 移除 path 及其包含的任何子项。
// 它会尽力移除所有内容，但会返回它遇到的
// 第一个错误。如果 path 不存在，RemoveAll
// 返回 nil（无错误）。
// 如果出错，错误的类型为 [*PathError]。
func RemoveAll(path string) error {
	return removeAll(path)
}

// endsWithDot 报告 path 的最后一个组成部分是否为 "."。
func endsWithDot(path string) bool {
	if path == "." {
		return true
	}
	if len(path) >= 2 && path[len(path)-1] == '.' && IsPathSeparator(path[len(path)-2]) {
		return true
	}
	return false
}
