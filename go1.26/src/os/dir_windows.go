// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/syscall/windows"
	"io"
	"io/fs"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// 如果 File 描述的是一个目录，则提供辅助信息
type dirInfo struct {
	mu sync.Mutex
	// buf 是切片指针，这样在将 buf 归还给 dirBufPool 时，
	// 切片头不会逃逸到堆上。
	buf   *[]byte // 用于目录 I/O 的缓冲区
	bufp  int     // buf 中下一条记录的位置
	h     syscall.Handle
	vol   uint32
	class uint32 // buf 中条目的类型
	path  string // 绝对目录路径，如果文件系统支持 FILE_ID_BOTH_DIR_INFO 则为空
}

const (
	// dirBufSize 是 dirInfo 缓冲区的大小。
	// 缓冲区必须足够大，至少能容纳一个条目。
	// 仅文件名就可能达到 512 字节（MAX_PATH*2），而
	// FILE_ID_BOTH_DIR_INFO 结构的固定部分为 105 字节，
	// 因此 dirBufSize 不应设置为低于 1024 字节（512+105+安全缓冲区）。
	// Windows 8.1 及更早版本仅支持最大 64 kB 的缓冲区大小。
	dirBufSize = 64 * 1024 // 64kB
)

var dirBufPool = sync.Pool{
	New: func() any {
		// 缓冲区长度必须至少为一个块。
		buf := make([]byte, dirBufSize)
		return &buf
	},
}

func (d *dirInfo) close() {
	d.h = 0
	if d.buf != nil {
		dirBufPool.Put(d.buf)
		d.buf = nil
	}
}

// allowReadDirFileID 指示当底层文件系统支持时，
// File.readdir 是否应尝试使用 FILE_ID_BOTH_DIR_INFO。
// 对测试很有用。
var allowReadDirFileID = true

func (d *dirInfo) init(h syscall.Handle) {
	d.h = h
	d.class = windows.FileFullDirectoryRestartInfo
	// 先前的设置已足以读取目录条目。
	// 以下代码仅用于支持 os.SameFile。

	// 查询一次 d.vol 并复用该值是安全的。
	// 硬链接不允许引用其他卷中的文件。
	// 联结（junction）和符号链接可以引用其他卷中的文件和目录，
	// 但重解析点仍应位于父卷中。
	var flags uint32
	err := windows.GetVolumeInformationByHandle(h, nil, 0, &d.vol, nil, &flags, nil, 0)
	if err != nil {
		d.vol = 0 // 置零，以防 Windows 向其写入垃圾数据。
		// 如果无法获取卷信息，就无法使用 os.SameFile，
		// 但仍可以读取目录条目。
		return
	}
	if flags&windows.FILE_SUPPORTS_OBJECT_IDS == 0 {
		// 文件系统不支持对象 ID，无需继续。
		return
	}
	if allowReadDirFileID && flags&windows.FILE_SUPPORTS_OPEN_BY_FILE_ID != 0 {
		// 如果可用，使用 FileIdBothDirectoryRestartInfo，因为它返回文件 ID
		// 而无需打开文件。
		d.class = windows.FileIdBothDirectoryRestartInfo
	} else {
		// 如果 FileIdBothDirectoryRestartInfo 不可用但支持对象 ID，
		// 则获取目录路径，以便 os.SameFile 可以用它打开文件
		// 并检索文件 ID。
		d.path, _ = windows.FinalPath(h, windows.FILE_NAME_OPENED)
	}
}

func (file *File) readdir(n int, mode readdirMode) (names []string, dirents []DirEntry, infos []FileInfo, err error) {
	// 如果此文件没有 dirInfo，则创建一个。
	var d *dirInfo
	for {
		d = file.dirinfo.Load()
		if d != nil {
			break
		}
		d = new(dirInfo)
		d.init(file.pfd.Sysfd)
		if file.dirinfo.CompareAndSwap(nil, d) {
			break
		}
		// 我们在竞争中失败了：重试。
		d.close()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.buf == nil {
		d.buf = dirBufPool.Get().(*[]byte)
	}

	wantAll := n <= 0
	if wantAll {
		n = -1
	}
	for n != 0 {
		// 如有必要，重新填充缓冲区
		if d.bufp == 0 {
			err = windows.GetFileInformationByHandleEx(file.pfd.Sysfd, d.class, (*byte)(unsafe.Pointer(&(*d.buf)[0])), uint32(len(*d.buf)))
			runtime.KeepAlive(file)
			if err != nil {
				if err == syscall.ERROR_NO_MORE_FILES {
					// 优化：我们可以将缓冲区归还到池中，没有其他内容可读取。
					dirBufPool.Put(d.buf)
					d.buf = nil
					break
				}
				if err == syscall.ERROR_FILE_NOT_FOUND &&
					(d.class == windows.FileIdBothDirectoryRestartInfo || d.class == windows.FileFullDirectoryRestartInfo) {
					// 当 info class 为 FileIdBothDirectoryRestartInfo 时，GetFileInformationByHandleEx 未记录返回的错误码，
					// 但 MS-FSA 2.1.5.6.3 [1] 规定，底层文件系统驱动在读取空根目录时应返回 STATUS_NO_SUCH_FILE，
					// 而 Windows 会将其映射为 ERROR_FILE_NOT_FOUND。
					// 注意，某些文件系统驱动可能永远不会返回此错误码，因为规范在这种情况下允许返回 "." 和 ".."
					// 条目，从而使目录看起来非空。
					// 误报的可能性非常低，因为我们知道该目录存在，否则 GetVolumeInformationByHandle
					// 会失败，并且该句柄仍然有效，因为我们还没有关闭它。
					// 参见 go.dev/issue/61159。
					// [1] https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-fsa/fa8194e0-53ec-413b-8315-e8fa85396fd8
					break
				}
				if s, _ := file.Stat(); s != nil && !s.IsDir() {
					err = &PathError{Op: "readdir", Path: file.name, Err: syscall.ENOTDIR}
				} else {
					err = &PathError{Op: "GetFileInformationByHandleEx", Path: file.name, Err: err}
				}
				return
			}
			if d.class == windows.FileIdBothDirectoryRestartInfo {
				d.class = windows.FileIdBothDirectoryInfo
			} else if d.class == windows.FileFullDirectoryRestartInfo {
				d.class = windows.FileFullDirectoryInfo
			}
		}
		// 排空缓冲区
		var islast bool
		for n != 0 && !islast {
			var nextEntryOffset uint32
			var nameslice []uint16
			entry := unsafe.Pointer(&(*d.buf)[d.bufp])
			if d.class == windows.FileIdBothDirectoryInfo {
				info := (*windows.FILE_ID_BOTH_DIR_INFO)(entry)
				nextEntryOffset = info.NextEntryOffset
				nameslice = unsafe.Slice(&info.FileName[0], info.FileNameLength/2)
			} else {
				info := (*windows.FILE_FULL_DIR_INFO)(entry)
				nextEntryOffset = info.NextEntryOffset
				nameslice = unsafe.Slice(&info.FileName[0], info.FileNameLength/2)
			}
			d.bufp += int(nextEntryOffset)
			islast = nextEntryOffset == 0
			if islast {
				d.bufp = 0
			}
			if (len(nameslice) == 1 && nameslice[0] == '.') ||
				(len(nameslice) == 2 && nameslice[0] == '.' && nameslice[1] == '.') {
				// 忽略 "." 和 ".." 并避免为它们分配字符串。
				continue
			}
			name := syscall.UTF16ToString(nameslice)
			if mode == readdirName {
				names = append(names, name)
			} else {
				var f *fileStat
				if d.class == windows.FileIdBothDirectoryInfo {
					f = newFileStatFromFileIDBothDirInfo((*windows.FILE_ID_BOTH_DIR_INFO)(entry))
				} else {
					f = newFileStatFromFileFullDirInfo((*windows.FILE_FULL_DIR_INFO)(entry))
					if d.path != "" {
						// 推迟将条目名追加到父目录路径，直到真正需要时，
						// 以避免分配可能用不到的字符串。
						// 它目前仅在 os.SameFile 中使用。
						f.appendNameToPath = true
						f.path = d.path
					}
				}
				f.name = name
				f.vol = d.vol
				if mode == readdirDirEntry {
					dirents = append(dirents, dirEntry{f})
				} else {
					infos = append(infos, f)
				}
			}
			n--
		}
	}
	if !wantAll && len(names)+len(dirents)+len(infos) == 0 {
		return nil, nil, nil, io.EOF
	}
	return names, dirents, infos, nil
}

type dirEntry struct {
	fs *fileStat
}

func (de dirEntry) Name() string            { return de.fs.Name() }
func (de dirEntry) IsDir() bool             { return de.fs.IsDir() }
func (de dirEntry) Type() FileMode          { return de.fs.Mode().Type() }
func (de dirEntry) Info() (FileInfo, error) { return de.fs, nil }

func (de dirEntry) String() string {
	return fs.FormatDirEntry(de)
}
