// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

// StatFS 是具备 Stat 方法的文件系统接口。
type StatFS interface {
	FS

	// Stat 返回描述文件的 FileInfo。
	// 若发生错误，错误类型应为 *PathError。
	Stat(name string) (FileInfo, error)
}

// Stat 从文件系统中返回描述指定文件的 [FileInfo]。
//
// 若 fs 实现了 [StatFS] 接口，Stat 会调用 fs.Stat。
// 否则，Stat 会打开 [File] 并获取其文件信息。
func Stat(fsys FS, name string) (FileInfo, error) {
	if fsys, ok := fsys.(StatFS); ok {
		return fsys.Stat(name)
	}

	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.Stat()
}
