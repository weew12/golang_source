// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || darwin || dragonfly || freebsd || (js && wasm) || netbsd || openbsd || solaris || wasip1

package os

// 根据 sticky(8)，open(2) 和 mkdir(2) 都不会创建
// 设置了 sticky 位的文件。
const supportsCreateWithStickyBit = false
