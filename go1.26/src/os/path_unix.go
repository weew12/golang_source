// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || (js && wasm) || wasip1

package os

const (
	PathSeparator     = '/' // 特定于操作系统的路径分隔符
	PathListSeparator = ':' // 特定于操作系统的路径列表分隔符
)

// IsPathSeparator 报告 c 是否为目录分隔符字符。
func IsPathSeparator(c uint8) bool {
	return PathSeparator == c
}

// splitPath 返回基本名称和父目录。
func splitPath(path string) (string, string) {
	// 如果找不到更好的父目录，则该路径相对于 "here"
	dirname := "."

	// 移除除一个前导斜杠之外的所有斜杠。
	for len(path) > 1 && path[0] == '/' && path[1] == '/' {
		path = path[1:]
	}

	i := len(path) - 1

	// 移除结尾的斜杠。
	for ; i > 0 && path[i] == '/'; i-- {
		path = path[:i]
	}

	// 如果路径中没有斜杠，base 就是 path
	basename := path

	// 移除前导的目录路径
	for i--; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				dirname = path[:1]
			} else {
				dirname = path[:i]
			}
			basename = path[i+1:]
			break
		}
	}

	return dirname, basename
}
