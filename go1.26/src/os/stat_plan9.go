// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"syscall"
	"time"
)

const bitSize16 = 2

func fileInfoFromStat(d *syscall.Dir) *fileStat {
	fs := &fileStat{
		name:    d.Name,
		size:    d.Length,
		modTime: time.Unix(int64(d.Mtime), 0),
		sys:     d,
	}
	fs.mode = FileMode(d.Mode & 0777)
	if d.Mode&syscall.DMDIR != 0 {
		fs.mode |= ModeDir
	}
	if d.Mode&syscall.DMAPPEND != 0 {
		fs.mode |= ModeAppend
	}
	if d.Mode&syscall.DMEXCL != 0 {
		fs.mode |= ModeExclusive
	}
	if d.Mode&syscall.DMTMP != 0 {
		fs.mode |= ModeTemporary
	}
	// 将所有不由 #M 提供的文件视为设备文件。
	if d.Type != 'M' {
		fs.mode |= ModeDevice
	}
	// 将所有由 #c 提供的文件视为字符设备文件。
	if d.Type == 'c' {
		fs.mode |= ModeCharDevice
	}
	return fs
}

// arg 是一个打开的 *File 或路径字符串。
func dirstat(arg any) (*syscall.Dir, error) {
	var name string
	var err error

	size := syscall.STATFIXLEN + 16*4

	for i := 0; i < 2; i++ {
		buf := make([]byte, bitSize16+size)

		var n int
		switch a := arg.(type) {
		case *File:
			name = a.name
			if err := a.incref("fstat"); err != nil {
				return nil, err
			}
			n, err = syscall.Fstat(a.sysfd, buf)
			a.decref()
		case string:
			name = a
			n, err = syscall.Stat(a, buf)
		default:
			panic("phase error in dirstat")
		}

		if n < bitSize16 {
			return nil, &PathError{Op: "stat", Path: name, Err: err}
		}

		// 从 stat 消息中取出真实大小。
		size = int(uint16(buf[0]) | uint16(buf[1])<<8)

		// 如果 stat 消息大于我们的缓冲区，我们将
		// 循环一遍并分配一个足够大的缓冲区。
		if size <= n {
			d, err := syscall.UnmarshalDir(buf[:n])
			if err != nil {
				return nil, &PathError{Op: "stat", Path: name, Err: err}
			}
			return d, nil
		}

	}

	if err == nil {
		err = syscall.ErrBadStat
	}

	return nil, &PathError{Op: "stat", Path: name, Err: err}
}

// statNolog 为 Plan 9 实现 Stat。
func statNolog(name string) (FileInfo, error) {
	d, err := dirstat(name)
	if err != nil {
		return nil, err
	}
	return fileInfoFromStat(d), nil
}

// lstatNolog 为 Plan 9 实现 Lstat。
func lstatNolog(name string) (FileInfo, error) {
	return statNolog(name)
}

// 用于测试。
func atime(fi FileInfo) time.Time {
	return time.Unix(int64(fi.Sys().(*syscall.Dir).Atime), 0)
}
