// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filepath

import (
	"os"
	"strings"
	"syscall"
)

func evalSymlinks(path string) (string, error) {
	// Plan 9 没有符号链接，因此无需替换。
	if len(path) > 0 {
		// 检查 path 的有效性
		_, err := os.Lstat(path)
		if err != nil {
			// 返回与其他操作系统相同的错误值。
			if strings.Contains(err.Error(), "not a directory") {
				err = syscall.ENOTDIR
			}
			return "", err
		}
	}
	return Clean(path), nil
}
