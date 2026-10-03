// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/filepathlite"
	"internal/godebug"
	"internal/syscall/windows"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// fileStat 是 Stat 和 Lstat 返回的 FileInfo 的实现。
type fileStat struct {
	name string

	// 来自 ByHandleFileInformation、Win32FileAttributeData、Win32finddata 和 GetFileInformationByHandleEx
	FileAttributes uint32
	CreationTime   syscall.Filetime
	LastAccessTime syscall.Filetime
	LastWriteTime  syscall.Filetime
	FileSizeHigh   uint32
	FileSizeLow    uint32

	// 来自 Win32finddata 和 GetFileInformationByHandleEx
	ReparseTag uint32

	// syscall.GetFileType 返回的值
	filetype uint32

	// 用于实现 SameFile
	sync.Mutex
	path             string
	vol              uint32
	idxhi            uint32
	idxlo            uint32
	appendNameToPath bool
}

// newFileStatFromGetFileInformationByHandle 调用 GetFileInformationByHandle
// 来收集关于文件句柄 h 的所有必需信息。
func newFileStatFromGetFileInformationByHandle(path string, h syscall.Handle) (fs *fileStat, err error) {
	var d syscall.ByHandleFileInformation
	err = syscall.GetFileInformationByHandle(h, &d)
	if err != nil {
		return nil, &PathError{Op: "GetFileInformationByHandle", Path: path, Err: err}
	}

	var reparseTag uint32
	if d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		var ti windows.FILE_ATTRIBUTE_TAG_INFO
		err = windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&ti)), uint32(unsafe.Sizeof(ti)))
		if err != nil {
			return nil, &PathError{Op: "GetFileInformationByHandleEx", Path: path, Err: err}
		}
		reparseTag = ti.ReparseTag
	}

	return &fileStat{
		name:           filepathlite.Base(path),
		FileAttributes: d.FileAttributes,
		CreationTime:   d.CreationTime,
		LastAccessTime: d.LastAccessTime,
		LastWriteTime:  d.LastWriteTime,
		FileSizeHigh:   d.FileSizeHigh,
		FileSizeLow:    d.FileSizeLow,
		vol:            d.VolumeSerialNumber,
		idxhi:          d.FileIndexHigh,
		idxlo:          d.FileIndexLow,
		ReparseTag:     reparseTag,
		// fileStat.path 被 os.SameFile 用来判断是否需要
		// 获取 vol、idxhi 和 idxlo。但这些已经被设置，
		// 因此将 fileStat.path 设为 "" 以防止 os.SameFile 再次执行该操作。
	}, nil
}

// newFileStatFromWin32FileAttributeData 将所有必需信息
// 从 syscall.Win32FileAttributeData d 复制到新创建的 fileStat 中。
func newFileStatFromWin32FileAttributeData(d *syscall.Win32FileAttributeData) *fileStat {
	return &fileStat{
		FileAttributes: d.FileAttributes,
		CreationTime:   d.CreationTime,
		LastAccessTime: d.LastAccessTime,
		LastWriteTime:  d.LastWriteTime,
		FileSizeHigh:   d.FileSizeHigh,
		FileSizeLow:    d.FileSizeLow,
	}
}

// newFileStatFromFileIDBothDirInfo 将所有必需信息
// 从 windows.FILE_ID_BOTH_DIR_INFO d 复制到新创建的 fileStat 中。
func newFileStatFromFileIDBothDirInfo(d *windows.FILE_ID_BOTH_DIR_INFO) *fileStat {
	// FILE_ID_BOTH_DIR_INFO 的 MSDN 文档并不完全正确。
	// FileAttributes 可以包含文件当前设置的任何文件属性，
	// 而不仅仅是文档中列出的那些。
	// 如果文件是重解析点，EaSize 包含重解析标签。
	return &fileStat{
		FileAttributes: d.FileAttributes,
		CreationTime:   d.CreationTime,
		LastAccessTime: d.LastAccessTime,
		LastWriteTime:  d.LastWriteTime,
		FileSizeHigh:   uint32(d.EndOfFile >> 32),
		FileSizeLow:    uint32(d.EndOfFile),
		ReparseTag:     d.EaSize,
		idxhi:          uint32(d.FileID >> 32),
		idxlo:          uint32(d.FileID),
	}
}

// newFileStatFromFileFullDirInfo 将所有必需信息
// 从 windows.FILE_FULL_DIR_INFO d 复制到新创建的 fileStat 中。
func newFileStatFromFileFullDirInfo(d *windows.FILE_FULL_DIR_INFO) *fileStat {
	return &fileStat{
		FileAttributes: d.FileAttributes,
		CreationTime:   d.CreationTime,
		LastAccessTime: d.LastAccessTime,
		LastWriteTime:  d.LastWriteTime,
		FileSizeHigh:   uint32(d.EndOfFile >> 32),
		FileSizeLow:    uint32(d.EndOfFile),
		ReparseTag:     d.EaSize,
	}
}

// newFileStatFromWin32finddata 将所有必需信息
// 从 syscall.Win32finddata d 复制到新创建的 fileStat 中。
func newFileStatFromWin32finddata(d *syscall.Win32finddata) *fileStat {
	fs := &fileStat{
		FileAttributes: d.FileAttributes,
		CreationTime:   d.CreationTime,
		LastAccessTime: d.LastAccessTime,
		LastWriteTime:  d.LastWriteTime,
		FileSizeHigh:   d.FileSizeHigh,
		FileSizeLow:    d.FileSizeLow,
	}
	if d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		// 根据 https://learn.microsoft.com/en-us/windows/win32/api/minwinbase/ns-minwinbase-win32_find_dataw：
		// “如果 dwFileAttributes 成员包含 FILE_ATTRIBUTE_REPARSE_POINT
		// 属性，则该成员指定重解析点标签。否则，该
		// 值未定义，不应使用。”
		fs.ReparseTag = d.Reserved0
	}
	return fs
}

// isReparseTagNameSurrogate 判断某个标签关联的
// 重解析点是否是另一个命名实体（例如已挂载的文件夹）的替代物。
//
// 参见 https://learn.microsoft.com/en-us/windows/win32/api/winnt/nf-winnt-isreparsetagnamesurrogate
// 和 https://learn.microsoft.com/en-us/windows/win32/fileio/reparse-point-tags。
func (fs *fileStat) isReparseTagNameSurrogate() bool {
	// 对于 IO_REPARSE_TAG_SYMLINK 和 IO_REPARSE_TAG_MOUNT_POINT 为 true。
	return fs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 && fs.ReparseTag&0x20000000 != 0
}

func (fs *fileStat) Size() int64 {
	return int64(fs.FileSizeHigh)<<32 + int64(fs.FileSizeLow)
}

var winsymlink = godebug.New("winsymlink")

func (fs *fileStat) Mode() FileMode {
	m := fs.mode()
	if winsymlink.Value() == "0" {
		old := fs.modePreGo1_23()
		if old != m {
			winsymlink.IncNonDefault()
			m = old
		}
	}
	return m
}

func (fs *fileStat) mode() (m FileMode) {
	if fs.FileAttributes&syscall.FILE_ATTRIBUTE_READONLY != 0 {
		m |= 0444
	} else {
		m |= 0666
	}

	// Windows 会为指向目录的重解析点（例如符号链接和挂载点）
	// 报告 FILE_ATTRIBUTE_DIRECTORY 位。
	// 然而，我们遵循符号链接的 POSIX 语义，不设置模式位。
	// 这样用户就可以不跟随链接地遍历目录，
	// 只需调用 "fi, err := os.Lstat(name); err == nil && fi.IsDir()"。
	// 请注意，POSIX 只定义了符号链接的语义，并未定义
	// 挂载点或其他替代重解析点的语义，但为了一致性
	// 我们以相同方式对待它们。此外，挂载点可能包含无限
	// 循环，因此不做特殊处理地遍历它们是不安全的。
	if !fs.isReparseTagNameSurrogate() {
		if fs.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
			m |= ModeDir | 0111
		}

		switch fs.filetype {
		case syscall.FILE_TYPE_PIPE:
			m |= ModeNamedPipe
		case syscall.FILE_TYPE_CHAR:
			m |= ModeDevice | ModeCharDevice
		}
	}

	if fs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		switch fs.ReparseTag {
		case syscall.IO_REPARSE_TAG_SYMLINK:
			m |= ModeSymlink
		case windows.IO_REPARSE_TAG_AF_UNIX:
			m |= ModeSocket
		case windows.IO_REPARSE_TAG_DEDUP:
			// 如果在 Windows Server 上启用了数据去重（Data Deduplication）服务，其
			// 优化作业在运行时可能会将常规文件转换为 IO_REPARSE_TAG_DEDUP。
			//
			// 然而，DEDUP 重解析点在大多数方面与
			// 常规文件相似：它们仍然支持对持久化数据的
			// 随机访问读写，并且不应像网络文件系统那样
			// 引入意外的延迟或不可用性。
			//
			// Go 程序可能会使用 ModeIrregular 来过滤掉不寻常的文件（例如
			// Linux 上的原始设备文件、POSIX FIFO 特殊文件等），因此
			// 为了避免文件在常规与非常规之间不可预测地变化，我们将
			// DEDUP 文件视为足够接近常规文件，从而按常规文件处理。
		default:
			m |= ModeIrregular
		}
	}
	return
}

// modePreGo1_23 返回该 fileStat 的 FileMode，使用 Go 1.23 之前
// 确定文件模式的逻辑。
// 该逻辑很微妙且文档不足，因此最好将其
// 与新的逻辑分开。
func (fs *fileStat) modePreGo1_23() (m FileMode) {
	if fs.FileAttributes&syscall.FILE_ATTRIBUTE_READONLY != 0 {
		m |= 0444
	} else {
		m |= 0666
	}
	if fs.ReparseTag == syscall.IO_REPARSE_TAG_SYMLINK ||
		fs.ReparseTag == windows.IO_REPARSE_TAG_MOUNT_POINT {
		return m | ModeSymlink
	}
	if fs.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		m |= ModeDir | 0111
	}
	switch fs.filetype {
	case syscall.FILE_TYPE_PIPE:
		m |= ModeNamedPipe
	case syscall.FILE_TYPE_CHAR:
		m |= ModeDevice | ModeCharDevice
	}
	if fs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		if fs.ReparseTag == windows.IO_REPARSE_TAG_AF_UNIX {
			m |= ModeSocket
		}
		if m&ModeType == 0 {
			if fs.ReparseTag == windows.IO_REPARSE_TAG_DEDUP {
				// 参见 fs.Mode 中的注释。
			} else {
				m |= ModeIrregular
			}
		}
	}
	return m
}

func (fs *fileStat) ModTime() time.Time {
	return time.Unix(0, fs.LastWriteTime.Nanoseconds())
}

// Sys 返回文件 fs 的 syscall.Win32FileAttributeData。
func (fs *fileStat) Sys() any {
	return &syscall.Win32FileAttributeData{
		FileAttributes: fs.FileAttributes,
		CreationTime:   fs.CreationTime,
		LastAccessTime: fs.LastAccessTime,
		LastWriteTime:  fs.LastWriteTime,
		FileSizeHigh:   fs.FileSizeHigh,
		FileSizeLow:    fs.FileSizeLow,
	}
}

func (fs *fileStat) loadFileId() error {
	fs.Lock()
	defer fs.Unlock()
	if fs.path == "" {
		// 已经完成
		return nil
	}
	var path string
	if fs.appendNameToPath {
		path = fixLongPath(fs.path + `\` + fs.name)
	} else {
		path = fs.path
	}
	pathp, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	// 根据 https://learn.microsoft.com/en-us/windows/win32/fileio/reparse-points-and-file-operations，
	// “使用 CreateFile 函数的应用程序在打开文件时，如果该文件是重解析
	// 点，则应指定 FILE_FLAG_OPEN_REPARSE_POINT 标志。”
	//
	// 并且根据 https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew，
	// “如果文件不是重解析点，则该标志会被忽略。”
	//
	// 因此我们无条件地设置 FILE_FLAG_OPEN_REPARSE_POINT，因为我们想要
	// 关于重解析点本身的信息。
	//
	// 如果文件是符号链接，则在创建 fileStat 时符号链接目标应该已经被
	// 解析，因此我们无需担心在这里再次解析符号链接重解析点。
	attrs := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS | syscall.FILE_FLAG_OPEN_REPARSE_POINT)

	h, err := syscall.CreateFile(pathp, 0, 0, nil, syscall.OPEN_EXISTING, attrs, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	var i syscall.ByHandleFileInformation
	err = syscall.GetFileInformationByHandle(h, &i)
	if err != nil {
		return err
	}
	fs.path = ""
	fs.vol = i.VolumeSerialNumber
	fs.idxhi = i.FileIndexHigh
	fs.idxlo = i.FileIndexLow
	return nil
}

// saveInfoFromPath 保存文件的完整路径，供 os.SameFile 稍后使用，
// 并根据 path 设置 name。
func (fs *fileStat) saveInfoFromPath(path string) error {
	fs.path = path
	if !filepathlite.IsAbs(fs.path) {
		var err error
		fs.path, err = syscall.FullPath(fs.path)
		if err != nil {
			return &PathError{Op: "FullPath", Path: path, Err: err}
		}
	}
	fs.name = filepathlite.Base(path)
	return nil
}

func sameFile(fs1, fs2 *fileStat) bool {
	e := fs1.loadFileId()
	if e != nil {
		return false
	}
	e = fs2.loadFileId()
	if e != nil {
		return false
	}
	return fs1.vol == fs2.vol && fs1.idxhi == fs2.idxhi && fs1.idxlo == fs2.idxlo
}

// 用于测试。
func atime(fi FileInfo) time.Time {
	return time.Unix(0, fi.Sys().(*syscall.Win32FileAttributeData).LastAccessTime.Nanoseconds())
}
