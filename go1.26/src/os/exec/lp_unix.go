// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package exec

import (
	"errors"
	"internal/syscall/unix"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrNotFound 是路径搜索未能找到可执行文件时产生的错误。
var ErrNotFound = errors.New("executable file not found in $PATH")

func findExecutable(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	m := d.Mode()
	if m.IsDir() {
		return syscall.EISDIR
	}
	err = unix.Eaccess(file, unix.X_OK)
	// ENOSYS 表示 Eaccess 不可用或未实现。
	// EPERM 可能由采用 seccomp 的 Linux 容器返回。
	// 在这两种情况下，都回退到检查权限位。
	if err == nil || (err != syscall.ENOSYS && err != syscall.EPERM) {
		return err
	}
	if m&0111 != 0 {
		return nil
	}
	return fs.ErrPermission
}

func lookPath(file string) (string, error) {
	// NOTE(rsc)：我希望这里能采用 Plan 9 的行为
	// （仅当 file 以 / 或 ./ 或 ../ 开头时才绕过 path），
	// 但那样就无法匹配所有 Unix shell。

	if err := validateLookPath(file); err != nil {
		return "", &Error{file, err}
	}

	if strings.Contains(file, "/") {
		err := findExecutable(file)
		if err == nil {
			return file, nil
		}
		return "", &Error{file, err}
	}
	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			// Unix shell 语义：路径元素 "" 表示 "."
			dir = "."
		}
		path := filepath.Join(dir, file)
		if err := findExecutable(path); err == nil {
			if !filepath.IsAbs(path) {
				if execerrdot.Value() != "0" {
					return path, &Error{file, ErrDot}
				}
				execerrdot.IncNonDefault()
			}
			return path, nil
		}
	}
	return "", &Error{file, ErrNotFound}
}

// lookExtensions 在非 Windows 平台上是一个空操作，
// 因为这些平台不将可执行文件限制为特定的扩展名。
func lookExtensions(path, dir string) (string, error) {
	return path, nil
}
