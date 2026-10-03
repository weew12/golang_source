// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"io"
	"runtime"
	"syscall"
	"unsafe"
)

// 如果 File 描述的是一个目录，则提供辅助信息
type dirInfo struct {
	dir uintptr // 指向 dirent.h 中 DIR 结构的指针
}

func (d *dirInfo) close() {
	if d.dir == 0 {
		return
	}
	closedir(d.dir)
	d.dir = 0
}

func (f *File) readdir(n int, mode readdirMode) (names []string, dirents []DirEntry, infos []FileInfo, err error) {
	// 如果此文件没有 dirinfo，则创建一个。
	var d *dirInfo
	for {
		d = f.dirinfo.Load()
		if d != nil {
			break
		}
		dir, call, errno := f.pfd.OpenDir()
		if errno != nil {
			return nil, nil, nil, &PathError{Op: call, Path: f.name, Err: errno}
		}
		d = &dirInfo{dir: dir}
		if f.dirinfo.CompareAndSwap(nil, d) {
			break
		}
		// 我们在竞争中失败了：重试。
		d.close()
	}

	size := n
	if size <= 0 {
		size = 100
		n = -1
	}

	var dirent syscall.Dirent
	var entptr *syscall.Dirent
	for len(names)+len(dirents)+len(infos) < size || n == -1 {
		if errno := readdir_r(d.dir, &dirent, &entptr); errno != 0 {
			if errno == syscall.EINTR {
				continue
			}
			return names, dirents, infos, &PathError{Op: "readdir", Path: f.name, Err: errno}
		}
		if entptr == nil { // 文件末尾
			break
		}
		// 当目录条目已被删除但尚未从目录中移除时，
		// Darwin 可能返回零 inode。getdirentries(2) 的手册页指出，
		// 程序有责任跳过这些条目：
		//
		//   getdirentries() 的使用者应跳过 d_fileno = 0 的条目，
		//   因为此类条目表示文件已被删除但尚未从目录条目中移除。
		//
		if dirent.Ino == 0 {
			continue
		}
		name := (*[len(syscall.Dirent{}.Name)]byte)(unsafe.Pointer(&dirent.Name))[:]
		for i, c := range name {
			if c == 0 {
				name = name[:i]
				break
			}
		}
		// 在分配字符串之前先检查无用的名称。
		if string(name) == "." || string(name) == ".." {
			continue
		}
		if mode == readdirName {
			names = append(names, string(name))
		} else if mode == readdirDirEntry {
			de, err := newUnixDirent(f, string(name), dtToType(dirent.Type))
			if IsNotExist(err) {
				// 文件在 readdir 与 stat 之间消失了。
				// 将其视为不存在。
				continue
			}
			if err != nil {
				return nil, dirents, nil, err
			}
			dirents = append(dirents, de)
		} else {
			info, err := f.lstatat(string(name))
			if IsNotExist(err) {
				// 文件在 readdir + stat 之间消失了。
				// 将其视为不存在。
				continue
			}
			if err != nil {
				return nil, nil, infos, err
			}
			infos = append(infos, info)
		}
		runtime.KeepAlive(f)
	}

	if n > 0 && len(names)+len(dirents)+len(infos) == 0 {
		return nil, nil, nil, io.EOF
	}
	return names, dirents, infos, nil
}

func dtToType(typ uint8) FileMode {
	switch typ {
	case syscall.DT_BLK:
		return ModeDevice
	case syscall.DT_CHR:
		return ModeDevice | ModeCharDevice
	case syscall.DT_DIR:
		return ModeDir
	case syscall.DT_FIFO:
		return ModeNamedPipe
	case syscall.DT_LNK:
		return ModeSymlink
	case syscall.DT_REG:
		return 0
	case syscall.DT_SOCK:
		return ModeSocket
	}
	return ^FileMode(0)
}

// 在 syscall/syscall_darwin.go 中实现。

//go:linkname closedir syscall.closedir
func closedir(dir uintptr) (err error)

//go:linkname readdir_r syscall.readdir_r
func readdir_r(dir uintptr, entry *syscall.Dirent, result **syscall.Dirent) (res syscall.Errno)
