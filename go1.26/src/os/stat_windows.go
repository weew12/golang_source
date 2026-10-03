// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"errors"
	"internal/filepathlite"
	"internal/syscall/windows"
	"syscall"
	"unsafe"
)

// Stat 返回描述文件的 [FileInfo] 结构。
// 如果出错，错误的类型为 [*PathError]。
func (file *File) Stat() (FileInfo, error) {
	if file == nil {
		return nil, ErrInvalid
	}
	return statHandle(file.name, file.pfd.Sysfd)
}

// stat 同时实现文件的 Stat 和 Lstat。
func stat(funcname, name string, followSurrogates bool) (FileInfo, error) {
	if len(name) == 0 {
		return nil, &PathError{Op: funcname, Path: name, Err: syscall.Errno(syscall.ERROR_PATH_NOT_FOUND)}
	}
	namep, err := syscall.UTF16PtrFromString(fixLongPath(name))
	if err != nil {
		return nil, &PathError{Op: funcname, Path: name, Err: err}
	}

	// 首先尝试 GetFileAttributesEx，因为它比 CreateFile 更快。
	// 详情参见 https://golang.org/issues/19922#issuecomment-300031421。
	var fa syscall.Win32FileAttributeData
	err = syscall.GetFileAttributesEx(namep, syscall.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&fa)))
	if errors.Is(err, ErrNotExist) {
		return nil, &PathError{Op: "GetFileAttributesEx", Path: name, Err: err}
	}
	if err == nil && fa.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		// 不是另一个命名实体的替代，因为它不是任何类型的重解析点。
		// 目前从 GetFileAttributesEx 获得的信息已经足够。
		fs := newFileStatFromWin32FileAttributeData(&fa)
		if err := fs.saveInfoFromPath(name); err != nil {
			return nil, err
		}
		return fs, nil
	}

	// GetFileAttributesEx 对于像 c:\pagefile.sys 这样的文件会失败并返回
	// ERROR_SHARING_VIOLATION 错误。对于这类文件使用 FindFirstFile。
	if err == windows.ERROR_SHARING_VIOLATION {
		var fd syscall.Win32finddata
		sh, err := syscall.FindFirstFile(namep, &fd)
		if err != nil {
			return nil, &PathError{Op: "FindFirstFile", Path: name, Err: err}
		}
		syscall.FindClose(sh)
		if fd.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
			// 不是另一个命名实体的替代。FindFirstFile 已经足够。
			fs := newFileStatFromWin32finddata(&fd)
			if err := fs.saveInfoFromPath(name); err != nil {
				return nil, err
			}
			return fs, nil
		}
	}

	// 使用 CreateFile 来判断该文件是否是一个名称替代，如果是，
	// 则保存关于链接目标的信息。
	// 设置 FILE_FLAG_BACKUP_SEMANTICS，以便即使 name 指向目录，
	// CreateFile 也会创建句柄。
	var flags uint32 = syscall.FILE_FLAG_BACKUP_SEMANTICS | syscall.FILE_FLAG_OPEN_REPARSE_POINT
	h, err := syscall.CreateFile(namep, 0, 0, nil, syscall.OPEN_EXISTING, flags, 0)

	if err == windows.ERROR_INVALID_PARAMETER {
		// 像 "\\.\con" 这样的控制台句柄需要通用读取访问权限。参见
		// https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew#consoles。
		// 我们之前没有设置它，因为通常读取属性并不需要它，
		// 而且某些文件可能不允许。
		h, err = syscall.CreateFile(namep, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, flags, 0)
	}
	if err != nil {
		// 由于 CreateFile 失败了，我们无法判断 name 是指向一个
		// 名称替代，还是某种其他类型的重解析点。由于我们无法返回一个
		// Mode 已知准确的 FileInfo，我们必须返回一个错误。
		return nil, &PathError{Op: "CreateFile", Path: name, Err: err}
	}

	fi, err := statHandle(name, h)
	syscall.CloseHandle(h)
	if err == nil && followSurrogates && fi.(*fileStat).isReparseTagNameSurrogate() {
		// 为了获取关于链接目标的信息，我们重新打开该文件，这次不使用
		// FILE_FLAG_OPEN_REPARSE_POINT，并检查得到的句柄。
		// （参见 https://devblogs.microsoft.com/oldnewthing/20100212-00/?p=14963。）
		h, err = syscall.CreateFile(namep, 0, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			// name 指向一个符号链接，但我们无法解析该符号链接目标。
			return nil, &PathError{Op: "CreateFile", Path: name, Err: err}
		}
		defer syscall.CloseHandle(h)
		return statHandle(name, h)
	}
	return fi, err
}

func statHandle(name string, h syscall.Handle) (FileInfo, error) {
	ft, err := syscall.GetFileType(h)
	if err != nil {
		return nil, &PathError{Op: "GetFileType", Path: name, Err: err}
	}
	switch ft {
	case syscall.FILE_TYPE_PIPE, syscall.FILE_TYPE_CHAR:
		return &fileStat{name: filepathlite.Base(name), filetype: ft}, nil
	}
	fs, err := newFileStatFromGetFileInformationByHandle(name, h)
	if err != nil {
		return nil, err
	}
	fs.filetype = ft
	return fs, nil
}

// statNolog 为 Windows 实现 Stat。
func statNolog(name string) (FileInfo, error) {
	return stat("Stat", name, true)
}

// lstatNolog 为 Windows 实现 Lstat。
func lstatNolog(name string) (FileInfo, error) {
	followSurrogates := false
	if name != "" && IsPathSeparator(name[len(name)-1]) {
		// 我们尝试为 Lstat 的路径解析实现 POSIX 语义
		// （依据 https://pubs.opengroup.org/onlinepubs/9699919799.2013edition/basedefs/V1_chap04.html#tag_04_12）：
		// 路径中最后一个分隔符之前的符号链接必须被解析。由于
		// 这种情况下最后一个分隔符位于最后一个路径元素之后，我们应当
		// 跟随最后一个路径元素中的符号链接。
		followSurrogates = true
	}
	return stat("Lstat", name, followSurrogates)
}
