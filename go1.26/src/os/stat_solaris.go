// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/filepathlite"
	"syscall"
	"time"
)

// 这些常量不在 syscall 包中，因为该包已冻结。
// 取值来自 golang.org/x/sys/unix。
const (
	_S_IFNAM  = 0x5000
	_S_IFDOOR = 0xd000
	_S_IFPORT = 0xe000
)

func fillFileStatFromSys(fs *fileStat, name string) {
	fs.name = filepathlite.Base(name)
	fs.size = fs.sys.Size
	fs.modTime = time.Unix(fs.sys.Mtim.Unix())
	fs.mode = FileMode(fs.sys.Mode & 0777)
	switch fs.sys.Mode & syscall.S_IFMT {
	case syscall.S_IFBLK:
		fs.mode |= ModeDevice
	case syscall.S_IFCHR:
		fs.mode |= ModeDevice | ModeCharDevice
	case syscall.S_IFDIR:
		fs.mode |= ModeDir
	case syscall.S_IFIFO:
		fs.mode |= ModeNamedPipe
	case syscall.S_IFLNK:
		fs.mode |= ModeSymlink
	case syscall.S_IFREG:
		// 无需处理
	case syscall.S_IFSOCK:
		fs.mode |= ModeSocket
	case _S_IFNAM, _S_IFDOOR, _S_IFPORT:
		fs.mode |= ModeIrregular
	}
	if fs.sys.Mode&syscall.S_ISGID != 0 {
		fs.mode |= ModeSetgid
	}
	if fs.sys.Mode&syscall.S_ISUID != 0 {
		fs.mode |= ModeSetuid
	}
	if fs.sys.Mode&syscall.S_ISVTX != 0 {
		fs.mode |= ModeSticky
	}
}

// 用于测试。
func atime(fi FileInfo) time.Time {
	return time.Unix(fi.Sys().(*syscall.Stat_t).Atim.Unix())
}
