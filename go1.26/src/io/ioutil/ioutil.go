// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// ioutil 包实现了一些 I/O 工具函数。
//
// 已弃用：自 Go 1.16 起，相同功能现已由 [io] 包或 [os] 包提供，
// 新代码中应优先使用这些包的实现。
// 详情请参阅具体函数的文档。
package ioutil

import (
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// ReadAll 从 r 中读取数据直至发生错误或 EOF，并返回读取到的数据。
// 调用成功时返回 err == nil，而非 err == EOF。由于 ReadAll 被定义为从数据源读取直至 EOF，
// 因此不会将 Read 操作返回的 EOF 视为需要上报的错误。
//
// 已弃用：自 Go 1.16 起，该函数仅直接调用 [io.ReadAll] 。
//
//go:fix inline
func ReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// ReadFile 读取指定文件名的文件并返回文件内容。
// 调用成功时返回 err == nil，而非 err == EOF。由于 ReadFile 会读取整个文件，
// 因此不会将 Read 操作返回的 EOF 视为需要上报的错误。
//
// 已弃用：自 Go 1.16 起，该函数仅直接调用 [os.ReadFile] 。
//
//go:fix inline
func ReadFile(filename string) ([]byte, error) {
	return os.ReadFile(filename)
}

// WriteFile 将数据写入指定文件名的文件。
// 若文件不存在，WriteFile 会以权限 perm（应用 umask 之前）创建文件；
// 否则 WriteFile 会在写入前截断文件，且不修改文件原有权限。
//
// 已弃用：自 Go 1.16 起，该函数仅直接调用 [os.WriteFile] 。
//
//go:fix inline
func WriteFile(filename string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(filename, data, perm)
}

// ReadDir 读取指定目录名的目录，并返回目录内容的 fs.FileInfo 列表，
// 列表按文件名排序。若读取目录时发生错误，
// ReadDir 会返回空的目录条目和对应的错误。
//
// 已弃用：自 Go 1.16 起，[os.ReadDir] 是更高效、更规范的选择：
// 它返回 [fs.DirEntry] 列表而非 [fs.FileInfo] ，
// 且在目录读取中途发生错误时，会返回已读取的部分结果。
//
// 若你仍需获取 [fs.FileInfo] 列表，可通过以下方式实现：
//
//	entries, err := os.ReadDir(dirname)
//	if err != nil { ... }
//	infos := make([]fs.FileInfo, 0, len(entries))
//	for _, entry := range entries {
//		info, err := entry.Info()
//		if err != nil { ... }
//		infos = append(infos, info)
//	}
func ReadDir(dirname string) ([]fs.FileInfo, error) {
	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	list, err := f.Readdir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	slices.SortFunc(list, func(a, b os.FileInfo) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return list, nil
}

// NopCloser 返回一个包装了指定 Reader r 的 ReadCloser，其 Close 方法为空操作。
//
// 已弃用：自 Go 1.16 起，该函数仅直接调用 [io.NopCloser] 。
//
//go:fix inline
func NopCloser(r io.Reader) io.ReadCloser {
	return io.NopCloser(r)
}

// Discard 是一个 io.Writer，所有对它的 Write 调用都会成功执行，且不做任何实际操作。
//
// 已弃用：自 Go 1.16 起，该变量仅为 [io.Discard] 。
var Discard io.Writer = io.Discard
