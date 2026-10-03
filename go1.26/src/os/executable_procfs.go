// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package os

import (
	"errors"
	"internal/stringslite"
	"runtime"
)

func executable() (string, error) {
	var procfn string
	switch runtime.GOOS {
	default:
		return "", errors.New("Executable not implemented for " + runtime.GOOS)
	case "linux", "android":
		procfn = "/proc/self/exe"
	}
	path, err := Readlink(procfn)

	// 当可执行文件已被删除时，Readlink 返回的
	// 路径会附加 " (deleted)"。
	return stringslite.TrimSuffix(path, " (deleted)"), err
}
