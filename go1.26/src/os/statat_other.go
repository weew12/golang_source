// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (js && wasm) || plan9

package os

func (f *File) lstatatNolog(name string) (FileInfo, error) {
	// 这些平台没有 fstatat，因此改用 stat。
	return Lstat(f.name + "/" + name)
}
