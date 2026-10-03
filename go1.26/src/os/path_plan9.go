// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

const (
	PathSeparator     = '/'    // 特定于操作系统的路径分隔符
	PathListSeparator = '\000' // 特定于操作系统的路径列表分隔符
)

// IsPathSeparator 报告 c 是否为目录分隔符字符。
func IsPathSeparator(c uint8) bool {
	return PathSeparator == c
}
