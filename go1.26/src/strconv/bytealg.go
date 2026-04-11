// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !compiler_bootstrap

package strconv

import "internal/bytealg"

// index 返回 c 在 s 中第一次出现的索引，如果不存在则返回 -1。
func index(s string, c byte) int {
	return bytealg.IndexByteString(s, c)
}
