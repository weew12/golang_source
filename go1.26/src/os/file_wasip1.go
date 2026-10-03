// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1

package os

import "internal/poll"

// PollFD 返回该文件的 poll.FD。
//
// 标准库中其他也导入 internal/poll 的包（如 net）
// 可以使用类型断言来访问这个扩展方法，
// 从而将 *poll.FD 传给 poll.Splice 之类的函数。
//
// 在 net.rawConn 中有一个等价的函数。
//
// PollFD 不打算在标准库之外使用。
func (f *file) PollFD() *poll.FD {
	return &f.pfd
}
