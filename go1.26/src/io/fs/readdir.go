// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"errors"
	"internal/bytealg"
	"slices"
)

// ReadDirFS 是文件系统实现的接口，
// 该接口为 [ReadDir] 提供了优化的实现。
type ReadDirFS interface {
	FS

	// ReadDir 读取指定名称的目录，
	// 并返回按文件名排序的目录条目列表。
	ReadDir(name string) ([]DirEntry, error)
}

// ReadDir 读取指定名称的目录，
// 并返回按文件名排序的目录条目列表。
//
// 若文件系统实现了 [ReadDirFS] 接口，ReadDir 会调用 fs.ReadDir。
// 否则，ReadDir 会调用 fs.Open，并对返回的文件调用 ReadDir 和 Close。
func ReadDir(fsys FS, name string) ([]DirEntry, error) {
	if fsys, ok := fsys.(ReadDirFS); ok {
		return fsys.ReadDir(name)
	}

	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	dir, ok := file.(ReadDirFile)
	if !ok {
		return nil, &PathError{Op: "readdir", Path: name, Err: errors.New("not implemented")}
	}

	list, err := dir.ReadDir(-1)
	slices.SortFunc(list, func(a, b DirEntry) int {
		return bytealg.CompareString(a.Name(), b.Name())
	})
	return list, err
}

// dirInfo 是基于 FileInfo 实现的 DirEntry。
type dirInfo struct {
	fileInfo FileInfo
}

func (di dirInfo) IsDir() bool {
	return di.fileInfo.IsDir()
}

func (di dirInfo) Type() FileMode {
	return di.fileInfo.Mode().Type()
}

func (di dirInfo) Info() (FileInfo, error) {
	return di.fileInfo, nil
}

func (di dirInfo) Name() string {
	return di.fileInfo.Name()
}

func (di dirInfo) String() string {
	return FormatDirEntry(di)
}

// FileInfoToDirEntry 返回一个从 info 中读取信息的 [DirEntry] 。
// 若 info 为 nil，FileInfoToDirEntry 返回 nil。
func FileInfoToDirEntry(info FileInfo) DirEntry {
	if info == nil {
		return nil
	}
	return dirInfo{fileInfo: info}
}
