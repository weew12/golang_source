// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import "io"

// ReadFileFS 是文件系统实现的接口，
// 该接口为 [ReadFile] 提供了优化的实现。
type ReadFileFS interface {
	FS

	// ReadFile 读取指定名称的文件并返回其内容。
	// 调用成功时返回 nil 错误，而非 io.EOF。
	//（因为 ReadFile 会读取整个文件，最终读取操作预期的 EOF
	// 不会被视为需要上报的错误。）
	//
	// 调用者可以修改返回的字节切片。
	// 该方法应返回底层数据的副本。
	ReadFile(name string) ([]byte, error)
}

// ReadFile 从文件系统 fs 中读取指定名称的文件并返回其内容。
// 调用成功时返回 nil 错误，而非 [io.EOF]。
// （因为 ReadFile 会读取整个文件，最终读取操作预期的 EOF
// 不会被视为需要上报的错误。）
//
// 若 fs 实现了 [ReadFileFS] 接口，ReadFile 会调用 fs.ReadFile。
// 否则，ReadFile 会调用 fs.Open，并对返回的 [File] 调用 Read 和 Close。
func ReadFile(fsys FS, name string) ([]byte, error) {
	if fsys, ok := fsys.(ReadFileFS); ok {
		return fsys.ReadFile(name)
	}

	file, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var size int
	if info, err := file.Stat(); err == nil {
		size64 := info.Size()
		if int64(int(size64)) == size64 {
			size = int(size64)
		}
	}

	data := make([]byte, 0, size+1)
	for {
		if len(data) >= cap(data) {
			d := append(data[:cap(data)], 0)
			data = d[:len(data)]
		}
		n, err := file.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return data, err
		}
	}
}
