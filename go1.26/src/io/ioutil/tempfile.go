// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ioutil

import (
	"os"
)

// TempFile 在目录 dir 中创建一个新的临时文件，
// 以读写模式打开该文件，并返回生成的 *[os.File] 。
// 文件名基于 pattern 生成，并在末尾追加一个随机字符串。
// 若 pattern 包含 "*"，随机字符串会替换最后一个 "*"。
// 若 dir 为空字符串，TempFile 会使用系统默认的临时文件目录
// （详见 [os.TempDir] ）。
// 多个程序同时调用 TempFile 不会生成重复的文件。
// 调用者可通过 f.Name() 获取文件的路径名。
// 调用者需自行负责在不再使用时删除该文件。
//
// 已弃用：自 Go 1.17 起，该函数仅直接调用 [os.CreateTemp] 。
//
//go:fix inline
func TempFile(dir, pattern string) (f *os.File, err error) {
	return os.CreateTemp(dir, pattern)
}

// TempDir 在目录 dir 中创建一个新的临时目录。
// 目录名基于 pattern 生成，并在末尾追加一个随机字符串。
// 若 pattern 包含 "*"，随机字符串会替换最后一个 "*"。
// TempDir 返回新目录的名称。
// 若 dir 为空字符串，TempDir 会使用系统默认的临时文件目录
// （详见 [os.TempDir] ）。
// 多个程序同时调用 TempDir 不会生成重复的目录。
// 调用者需自行负责在不再使用时删除该目录。
//
// 已弃用：自 Go 1.17 起，该函数仅直接调用 [os.MkdirTemp] 。
//
//go:fix inline
func TempDir(dir, pattern string) (name string, err error) {
	return os.MkdirTemp(dir, pattern)
}
