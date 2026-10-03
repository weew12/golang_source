// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || dragonfly || freebsd || (js && wasm) || wasip1 || linux || netbsd || openbsd || solaris

package os

import (
	"internal/byteorder"
	"internal/goarch"
	"io"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// 如果 File 描述的是一个目录，则提供辅助信息
type dirInfo struct {
	mu   sync.Mutex
	buf  *[]byte // 用于目录 I/O 的缓冲区
	nbuf int     // buf 的长度；Getdirentries 的返回值
	bufp int     // buf 中下一条记录的位置。
}

const (
	// 大于 5760，以规避 https://golang.org/issue/24015。
	blockSize = 8192
)

var dirBufPool = sync.Pool{
	New: func() any {
		// 缓冲区长度必须至少为一个块。
		buf := make([]byte, blockSize)
		return &buf
	},
}

func (d *dirInfo) close() {
	if d.buf != nil {
		dirBufPool.Put(d.buf)
		d.buf = nil
	}
}

func (f *File) readdir(n int, mode readdirMode) (names []string, dirents []DirEntry, infos []FileInfo, err error) {
	// 如果此文件没有 dirInfo，则创建一个。
	var d *dirInfo
	for {
		d = f.dirinfo.Load()
		if d != nil {
			break
		}
		newD := new(dirInfo)
		if f.dirinfo.CompareAndSwap(nil, newD) {
			d = newD
			break
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.buf == nil {
		d.buf = dirBufPool.Get().(*[]byte)
	}

	// 为下面的实现改变 n 的含义。
	//
	// 上面的 n 是针对公开接口的语义："如果 n <= 0，
	// Readdir 在单个切片中返回目录中的所有 FileInfo"。
	//
	// 但在下面，我们只用负数表示循环直到末尾，
	// 用正数表示有界，正数在到达 0 时终止。
	if n == 0 {
		n = -1
	}

	for n != 0 {
		// 如有必要，重新填充缓冲区
		if d.bufp >= d.nbuf {
			d.bufp = 0
			var errno error
			d.nbuf, errno = f.pfd.ReadDirent(*d.buf)
			runtime.KeepAlive(f)
			if errno != nil {
				return names, dirents, infos, &PathError{Op: "readdirent", Path: f.name, Err: errno}
			}
			if d.nbuf <= 0 {
				// 优化：我们可以将缓冲区归还到池中，没有其他内容可读取。
				dirBufPool.Put(d.buf)
				d.buf = nil
				break // 文件末尾
			}
		}

		// 排空缓冲区
		buf := (*d.buf)[d.bufp:d.nbuf]
		reclen, ok := direntReclen(buf)
		if !ok || reclen > uint64(len(buf)) {
			break
		}
		rec := buf[:reclen]
		d.bufp += int(reclen)
		ino, ok := direntIno(rec)
		if !ok {
			break
		}
		// 面向 wasip1 构建时，宿主运行时可能运行在 Windows 上，
		// 或者可能暴露一个没有 inode 概念的远程文件系统。
		// 因此，我们不能假设跳过零 inode 的条目是安全的。
		// 某些 Linux 文件系统（旧的 XFS、FUSE）可能返回零 inode 的有效文件。
		if ino == 0 && runtime.GOOS != "linux" && runtime.GOOS != "wasip1" {
			continue
		}
		const namoff = uint64(unsafe.Offsetof(syscall.Dirent{}.Name))
		namlen, ok := direntNamlen(rec)
		if !ok || namoff+namlen > uint64(len(rec)) {
			break
		}
		name := rec[namoff : namoff+namlen]
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
		if n > 0 { // 参见上面关于 'n == 0' 的注释
			n--
		}
		if mode == readdirName {
			names = append(names, string(name))
		} else if mode == readdirDirEntry {
			de, err := newUnixDirent(f, string(name), direntType(rec))
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
	}

	if n > 0 && len(names)+len(dirents)+len(infos) == 0 {
		return nil, nil, nil, io.EOF
	}
	return names, dirents, infos, nil
}

// readInt 以本机字节序返回偏移 off 处的 size 字节无符号整数。
func readInt(b []byte, off, size uintptr) (u uint64, ok bool) {
	if len(b) < int(off+size) {
		return 0, false
	}
	if goarch.BigEndian {
		return readIntBE(b[off:], size), true
	}
	return readIntLE(b[off:], size), true
}

func readIntBE(b []byte, size uintptr) uint64 {
	switch size {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(byteorder.BEUint16(b))
	case 4:
		return uint64(byteorder.BEUint32(b))
	case 8:
		return uint64(byteorder.BEUint64(b))
	default:
		panic("syscall: readInt with unsupported size")
	}
}

func readIntLE(b []byte, size uintptr) uint64 {
	switch size {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(byteorder.LEUint16(b))
	case 4:
		return uint64(byteorder.LEUint32(b))
	case 8:
		return uint64(byteorder.LEUint64(b))
	default:
		panic("syscall: readInt with unsupported size")
	}
}
