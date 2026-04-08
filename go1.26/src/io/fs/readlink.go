// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

// ReadLinkFS 是支持读取符号链接的文件系统所实现的接口。
type ReadLinkFS interface {
	FS

	// ReadLink 返回指定符号链接的目标路径。
	// 若发生错误，错误类型应为 [*PathError]。
	ReadLink(name string) (string, error)

	// Lstat 返回描述指定文件的 [FileInfo]。
	// 若文件为符号链接，返回的 [FileInfo] 描述的是符号链接本身。
	// Lstat 不会尝试解析链接指向的目标文件。
	// 若发生错误，错误类型应为 [*PathError]。
	Lstat(name string) (FileInfo, error)
}

// ReadLink 返回指定符号链接的目标路径。
//
// 若 fsys 未实现 [ReadLinkFS] 接口，ReadLink 会返回错误。
func ReadLink(fsys FS, name string) (string, error) {
	sym, ok := fsys.(ReadLinkFS)
	if !ok {
		return "", &PathError{Op: "readlink", Path: name, Err: ErrInvalid}
	}
	return sym.ReadLink(name)
}

// Lstat 返回描述指定文件的 [FileInfo]。
// 若文件为符号链接，返回的 [FileInfo] 描述的是符号链接本身。
// Lstat 不会尝试解析链接指向的目标文件。
//
// 若 fsys 未实现 [ReadLinkFS] 接口，Lstat 的行为与 [Stat] 完全一致。
func Lstat(fsys FS, name string) (FileInfo, error) {
	sym, ok := fsys.(ReadLinkFS)
	if !ok {
		return Stat(fsys, name)
	}
	return sym.Lstat(name)
}
