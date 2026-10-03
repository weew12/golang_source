// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows

package os

import (
	"errors"
	"internal/filepathlite"
	"internal/stringslite"
	"internal/syscall/windows"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// rootCleanPath 使用 GetFullPathName 执行词法路径清理。
//
// 在 Windows 上，文件名会在文件操作开始时进行词法清理。
// 例如，在 Windows 上路径 `a\..\b` 完全等价于单独的 `b`，
// 即使 `a` 不存在或不是目录。
//
// 我们使用 Windows API 函数 GetFullPathName 来执行此清理。
// 我们也可以自己实现，但这里有许多微妙的行为，
// 交给操作系统处理可以保持一致。
// （例如，`a\.\` 会被清理为 `a\`。）
//
// GetFullPathName 处理的是绝对路径，而我们的输入路径是相对的。
// 我们通过在路径前添加固定前缀 \\?\? 使其变为绝对路径。
//
// 我们想要检测使用 .. 组件逃逸出根的路径。
// 我们通过确保清理后的路径仍以 \\?\? 开头来实现这一点。
// 我们通过拒绝任何包含 ? 的输入路径（? 在 Windows 文件名中
// 不是合法字符）来捕获包含 ..\?\. 组件的边界情况。
func rootCleanPath(s string, prefix, suffix []string) (string, error) {
	// 拒绝包含 ? 组件的路径（见上文）。
	if stringslite.IndexByte(s, '?') >= 0 {
		return "", windows.ERROR_INVALID_NAME
	}

	const fixedPrefix = `\\?\?`
	buf := []byte(fixedPrefix)
	for _, p := range prefix {
		buf = append(buf, '\\')
		buf = append(buf, []byte(p)...)
	}
	buf = append(buf, '\\')
	buf = append(buf, []byte(s)...)
	for _, p := range suffix {
		buf = append(buf, '\\')
		buf = append(buf, []byte(p)...)
	}
	s = string(buf)

	s, err := syscall.FullPath(s)
	if err != nil {
		return "", err
	}

	s, ok := stringslite.CutPrefix(s, fixedPrefix)
	if !ok {
		return "", errPathEscapes
	}
	s = stringslite.TrimPrefix(s, `\`)
	if s == "" {
		s = "."
	}

	if !filepathlite.IsLocal(s) {
		return "", errPathEscapes
	}

	return s, nil
}

// sysfdType 是文件句柄的原生类型
// （在 Unix 上为 int，在 Windows 上为 syscall.Handle），
// 以便编写可移植的辅助函数。
type sysfdType = syscall.Handle

// openRootNolog 即 OpenRoot。
func openRootNolog(name string) (*Root, error) {
	if name == "" {
		return nil, &PathError{Op: "open", Path: name, Err: syscall.ENOENT}
	}
	path := fixLongPath(name)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &PathError{Op: "open", Path: name, Err: err}
	}
	return newRoot(fd, name)
}

// newRoot 返回一个新的 Root。
// 如果 fd 不是目录，它会关闭 fd 并返回错误。
func newRoot(fd syscall.Handle, name string) (*Root, error) {
	// 检查这是一个目录。
	//
	// 如果这里出现任何错误，忽略它们；最坏的情况是我们创建了一个
	// 在你尝试使用它时会返回错误的 Root。
	var fi syscall.ByHandleFileInformation
	err := syscall.GetFileInformationByHandle(fd, &fi)
	if err == nil && fi.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
		syscall.CloseHandle(fd)
		return nil, &PathError{Op: "open", Path: name, Err: errors.New("not a directory")}
	}

	r := &Root{&root{
		fd:   fd,
		name: name,
	}}
	runtime.SetFinalizer(r.root, (*root).Close)
	return r, nil
}

// openRootInRoot 即 Root.OpenRoot。
func openRootInRoot(r *Root, name string) (*Root, error) {
	fd, err := doInRoot(r, name, nil, rootOpenDir)
	if err != nil {
		return nil, &PathError{Op: "openat", Path: name, Err: err}
	}
	return newRoot(fd, joinPath(r.Name(), name))
}

// rootOpenFileNolog 即 Root.OpenFile。
func rootOpenFileNolog(root *Root, name string, flag int, perm FileMode) (*File, error) {
	fd, err := doInRoot(root, name, nil, func(parent syscall.Handle, name string) (syscall.Handle, error) {
		return openat(parent, name, uint64(flag), perm)
	})
	if err != nil {
		return nil, &PathError{Op: "openat", Path: name, Err: err}
	}
	nonblocking := flag&windows.O_FILE_FLAG_OVERLAPPED != 0
	return newFile(fd, joinPath(root.Name(), name), kindOpenFile, nonblocking), nil
}

func openat(dirfd syscall.Handle, name string, flag uint64, perm FileMode) (syscall.Handle, error) {
	h, err := windows.Openat(dirfd, name, flag|syscall.O_CLOEXEC|windows.O_NOFOLLOW_ANY, syscallMode(perm))
	if err == syscall.ELOOP || err == syscall.ENOTDIR {
		if link, err := readReparseLinkAt(dirfd, name); err == nil {
			return syscall.InvalidHandle, errSymlink(link)
		}
	}
	return h, err
}

func readReparseLinkAt(dirfd syscall.Handle, name string) (string, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return "", err
	}
	objAttrs := &windows.OBJECT_ATTRIBUTES{
		ObjectName: objectName,
	}
	if dirfd != syscall.InvalidHandle {
		objAttrs.RootDirectory = dirfd
	}
	objAttrs.Length = uint32(unsafe.Sizeof(*objAttrs))
	var h syscall.Handle
	err = windows.NtCreateFile(
		&h,
		windows.FILE_GENERIC_READ,
		objAttrs,
		&windows.IO_STATUS_BLOCK{},
		nil,
		uint32(syscall.FILE_ATTRIBUTE_NORMAL),
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		nil,
		0,
	)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	return readReparseLinkHandle(h)
}

func rootOpenDir(parent syscall.Handle, name string) (syscall.Handle, error) {
	h, err := openat(parent, name, syscall.O_RDONLY|syscall.O_CLOEXEC|windows.O_DIRECTORY, 0)
	if err == syscall.ERROR_FILE_NOT_FOUND {
		// Windows 会返回：
		//   - 如果叶子之前的任何路径组件不存在或不是目录，
		//     则返回 ERROR_PATH_NOT_FOUND。
		//   - 如果叶子不存在，则返回 ERROR_FILE_NOT_FOUND。
		//
		// 这与 Unix 行为不同，Unix 的行为是：
		//   - 如果任何路径组件（包括叶子）不存在，则返回 ENOENT。
		//   - 如果叶子之前的任何路径组件不是目录，则返回 ENOTDIR。
		//
		// 我们将 syscall.ENOENT 映射为 ERROR_FILE_NOT_FOUND，将 syscall.ENOTDIR
		// 映射为 ERROR_PATH_NOT_FOUND，但 Windows 的错误并不完全对应。
		//
		// 为了与 os.Open 保持一致，这里将 ERROR_FILE_NOT_FOUND 转换为
		// ERROR_PATH_NOT_FOUND，因为我们打开的是一个非叶子路径组件。
		err = syscall.ERROR_PATH_NOT_FOUND
	}
	return h, err
}

func rootStat(r *Root, name string, lstat bool) (FileInfo, error) {
	if len(name) > 0 && IsPathSeparator(name[len(name)-1]) {
		// 当文件名以路径分隔符结尾时，
		// Lstat 的行为类似于 Stat。
		//
		// 此行为并非基于此处有原则的决定，
		// 而仅仅是基于 Lstat 确实如此表现的经验证据。
		lstat = false
	}
	fi, err := doInRoot(r, name, nil, func(parent syscall.Handle, n string) (FileInfo, error) {
		fd, err := openat(parent, n, windows.O_FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return nil, err
		}
		defer syscall.CloseHandle(fd)
		fi, err := statHandle(name, fd)
		if err != nil {
			return nil, err
		}
		if !lstat && fi.(*fileStat).isReparseTagNameSurrogate() {
			link, err := readReparseLinkHandle(fd)
			if err != nil {
				return nil, err
			}
			return nil, errSymlink(link)
		}
		return fi, nil
	})
	if err != nil {
		return nil, &PathError{Op: "statat", Path: name, Err: err}
	}
	return fi, nil
}

func rootSymlink(r *Root, oldname, newname string) error {
	if oldname == "" {
		return syscall.EINVAL
	}

	// CreateSymbolicLinkW 会将卷相对路径转换为绝对路径。
	// 我们也这样做。
	if filepathlite.VolumeNameLen(oldname) > 0 && !filepathlite.IsAbs(oldname) {
		p, err := syscall.FullPath(oldname)
		if err == nil {
			oldname = p
		}
	}

	// 如果 oldname 能解析为根中的目录，则创建目录链接。
	// 否则，创建文件链接。
	var flags windows.SymlinkatFlags
	if filepathlite.VolumeNameLen(oldname) == 0 && !IsPathSeparator(oldname[0]) {
		// oldname 是相对于包含 newname 的目录的路径。
		// 将 newname 的目录前置到它前面，使其成为相对于根的路径。
		// 例如，如果 oldname=old 且 newname=a\new，则 destPath=a\old。
		destPath := oldname
		if dir := dirname(newname); dir != "." {
			destPath = dir + `\` + oldname
		}
		fi, err := r.Stat(destPath)
		if err == nil && fi.IsDir() {
			flags |= windows.SYMLINKAT_DIRECTORY
		}
	}

	// 经验表明，CreateSymbolicLinkW 似乎仅当目标不包含卷名时
	// 才设置 relative 标志。
	if filepathlite.VolumeNameLen(oldname) == 0 {
		flags |= windows.SYMLINKAT_RELATIVE
	}

	_, err := doInRoot(r, newname, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, windows.Symlinkat(oldname, parent, name, flags)
	})
	if err != nil {
		return &LinkError{"symlinkat", oldname, newname, err}
	}
	return nil
}

func chmodat(parent syscall.Handle, name string, mode FileMode) error {
	// 目前，在 Windows 上 os.Chmod("symlink") 会作用于 "symlink"，
	// 而不是它指向的任何文件。
	//
	// 这是否是期望的行为尚不确定：https://go.dev/issue/71492
	//
	// 目前，与 os.Symlink 保持一致。
	// 传入 O_FILE_FLAG_OPEN_REPARSE_POINT 会让我们打开指定的文件本身，
	// 而不是它链接到的任何文件。
	//
	// 如果我们将来想改变这一点，可以改为传入 O_NOFOLLOW_ANY，
	// 并在遇到符号链接时返回 errSymlink：
	//
	//     if err == syscall.ELOOP || err == syscall.ENOTDIR {
	//         if link, err := readReparseLinkAt(parent, name); err == nil {
	//                 return errSymlink(link)
	//         }
	//     }
	h, err := windows.Openat(parent, name, syscall.O_CLOEXEC|windows.O_FILE_FLAG_OPEN_REPARSE_POINT|windows.O_WRITE_ATTRS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)

	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return err
	}
	attrs := d.FileAttributes

	if mode&syscall.S_IWRITE != 0 {
		attrs &^= syscall.FILE_ATTRIBUTE_READONLY
	} else {
		attrs |= syscall.FILE_ATTRIBUTE_READONLY
	}
	if attrs == d.FileAttributes {
		return nil
	}

	var fbi windows.FILE_BASIC_INFO
	fbi.FileAttributes = attrs
	return windows.SetFileInformationByHandle(h, windows.FileBasicInfo, unsafe.Pointer(&fbi), uint32(unsafe.Sizeof(fbi)))
}

func chownat(parent syscall.Handle, name string, uid, gid int) error {
	return syscall.EWINDOWS // 与 syscall.Chown 保持一致
}

func lchownat(parent syscall.Handle, name string, uid, gid int) error {
	return syscall.EWINDOWS // 与 syscall.Lchown 保持一致
}

func mkdirat(dirfd syscall.Handle, name string, perm FileMode) error {
	return windows.Mkdirat(dirfd, name, syscallMode(perm))
}

func removeat(dirfd syscall.Handle, name string) error {
	return windows.Deleteat(dirfd, name, 0)
}

func removefileat(dirfd syscall.Handle, name string) error {
	return windows.Deleteat(dirfd, name, windows.FILE_NON_DIRECTORY_FILE)
}

func removedirat(dirfd syscall.Handle, name string) error {
	return windows.Deleteat(dirfd, name, windows.FILE_DIRECTORY_FILE)
}

func chtimesat(dirfd syscall.Handle, name string, atime time.Time, mtime time.Time) error {
	h, err := windows.Openat(dirfd, name, syscall.O_CLOEXEC|windows.O_NOFOLLOW_ANY|windows.O_WRITE_ATTRS, 0)
	if err == syscall.ELOOP || err == syscall.ENOTDIR {
		if link, err := readReparseLinkAt(dirfd, name); err == nil {
			return errSymlink(link)
		}
	}
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	a := syscall.Filetime{}
	w := syscall.Filetime{}
	if !atime.IsZero() {
		a = syscall.NsecToFiletime(atime.UnixNano())
	}
	if !mtime.IsZero() {
		w = syscall.NsecToFiletime(mtime.UnixNano())
	}
	return syscall.SetFileTime(h, nil, &a, &w)
}

func renameat(oldfd syscall.Handle, oldname string, newfd syscall.Handle, newname string) error {
	return windows.Renameat(oldfd, oldname, newfd, newname)
}

func linkat(oldfd syscall.Handle, oldname string, newfd syscall.Handle, newname string) error {
	return windows.Linkat(oldfd, oldname, newfd, newname)
}

func readlinkat(dirfd syscall.Handle, name string) (string, error) {
	fd, err := openat(dirfd, name, windows.O_FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(fd)
	return readReparseLinkHandle(fd)
}

func modeAt(parent syscall.Handle, name string) (FileMode, error) {
	fd, err := openat(parent, name, windows.O_FILE_FLAG_OPEN_REPARSE_POINT|windows.O_DIRECTORY, 0)
	if err != nil {
		return 0, err
	}
	defer syscall.CloseHandle(fd)
	fi, err := statHandle(name, fd)
	if err != nil {
		return 0, err
	}
	return fi.Mode(), nil
}
