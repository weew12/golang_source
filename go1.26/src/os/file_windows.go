// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"errors"
	"internal/filepathlite"
	"internal/godebug"
	"internal/poll"
	"internal/syscall/windows"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// file 是 *File 的真实表示。
// 额外的间接层确保 os 的任何客户端
// 都无法覆盖这些数据，否则可能导致终结器
// 关闭错误的文件描述符。
type file struct {
	pfd        poll.FD
	name       string
	dirinfo    atomic.Pointer[dirInfo] // 除非正在读取目录，否则为 nil
	appendMode bool                    // 文件是否以追加方式打开
}

// fd 是 Fd 的 Windows 实现。
func (file *File) fd() uintptr {
	if file == nil {
		return uintptr(syscall.InvalidHandle)
	}
	// 尝试将文件与运行时轮询器解除关联。
	// File.Fd 不返回错误，因此我们没有办法
	// 报告它。我们就忽略它。何时调用它由调用方
	// 在没有并发 IO 操作时决定。
	_ = file.pfd.DisassociateIOCP()
	return uintptr(file.pfd.Sysfd)
}

// newFileKind 描述传给 newFile 的文件种类。
type newFileKind int

const (
	// kindNewFile 表示该描述符是通过 NewFile 传给我们的。
	kindNewFile newFileKind = iota
	// kindOpenFile 表示该描述符是使用
	// Open、Create 或 OpenFile 打开的。
	kindOpenFile
	// kindPipe 表示该描述符是使用 Pipe 打开的。
	kindPipe
	// kindSock 表示该描述符是一个网络文件描述符，
	// 它由 net 包创建，并使用 net_newUnixFile 打开。
	kindSock
	// kindConsole 表示该描述符是一个控制台句柄。
	kindConsole
)

// newFile 使用给定的文件句柄和名称返回一个新的 File。
// 与 NewFile 不同，它不检查 h 是否为 syscall.InvalidHandle。
// 如果 nonBlocking 为 true，它会尝试将该文件添加到运行时轮询器。
func newFile(h syscall.Handle, name string, kind newFileKind, nonBlocking bool) *File {
	typ := "file"
	switch kind {
	case kindNewFile, kindOpenFile:
		t, err := syscall.GetFileType(h)
		if err != nil || t == syscall.FILE_TYPE_CHAR {
			var m uint32
			if syscall.GetConsoleMode(h, &m) == nil {
				typ = "console"
				// 控制台句柄始终是阻塞的。
				break
			}
		} else if t == syscall.FILE_TYPE_PIPE {
			typ = "pipe"
		}
		// NewFile 不知道句柄是阻塞还是非阻塞，
		// 因此我们在这里尝试检测。如果句柄
		// 是阻塞的且有未完成的 I/O 操作，这个调用可能会阻塞。
		//
		// 对 Stdin 避免这样做，它几乎总是阻塞的，并且在 "os" 包
		// 初始化时可能正被其他进程使用。
		// 参见 go.dev/issue/75949 和 go.dev/issue/76391。
		if kind == kindNewFile && h != syscall.Stdin {
			nonBlocking, _ = windows.IsNonblock(h)
		}
	case kindPipe:
		typ = "pipe"
	case kindSock:
		typ = "file+net"
	case kindConsole:
		typ = "console"
	default:
		panic("newFile with unknown kind")
	}

	f := &File{&file{
		pfd: poll.FD{
			Sysfd:         h,
			IsStream:      true,
			ZeroReadIsEOF: true,
		},
		name: name,
	}}
	runtime.SetFinalizer(f.file, (*file).close)

	// 忽略初始化错误。
	// 假定任何问题都会在之后的 I/O 中显现。
	f.pfd.Init(typ, nonBlocking)
	return f
}

// newConsoleFile 创建一个将用作控制台的 File。
func newConsoleFile(h syscall.Handle, name string) *File {
	return newFile(h, name, kindConsole, false)
}

// newFileFromNewFile 由 [NewFile] 调用。
func newFileFromNewFile(fd uintptr, name string) *File {
	h := syscall.Handle(fd)
	if h == syscall.InvalidHandle {
		return nil
	}
	return newFile(h, name, kindNewFile, false)
}

// net_newWindowsFile 是由 net.conn.File 调用的隐藏入口点。
// 这样做是为了让 File.pfd.close 方法调用 [syscall.Closesocket]
// 而不是 [syscall.CloseHandle]。
//
//go:linkname net_newWindowsFile net.newWindowsFile
func net_newWindowsFile(h syscall.Handle, name string) *File {
	if h == syscall.InvalidHandle {
		panic("invalid FD")
	}
	return newFile(h, name, kindSock, true)
}

func epipecheck(file *File, e error) {
}

// DevNull 是操作系统 “空设备” 的名称。
// 在类 Unix 系统上，它是 "/dev/null"；在 Windows 上，是 "NUL"。
const DevNull = "NUL"

// openFileNolog 是 OpenFile 的 Windows 实现。
func openFileNolog(name string, flag int, perm FileMode) (*File, error) {
	if name == "" {
		return nil, &PathError{Op: "open", Path: name, Err: syscall.ENOENT}
	}
	path := fixLongPath(name)
	r, err := syscall.Open(path, flag|syscall.O_CLOEXEC, syscallMode(perm))
	if err != nil {
		return nil, &PathError{Op: "open", Path: name, Err: err}
	}
	nonblocking := flag&windows.O_FILE_FLAG_OVERLAPPED != 0
	return newFile(r, name, kindOpenFile, nonblocking), nil
}

func openDirNolog(name string) (*File, error) {
	return openFileNolog(name, O_RDONLY|windows.O_DIRECTORY, 0)
}

func (file *file) close() error {
	if file == nil {
		return syscall.EINVAL
	}
	if info := file.dirinfo.Swap(nil); info != nil {
		info.close()
	}
	var err error
	if e := file.pfd.Close(); e != nil {
		if e == poll.ErrFileClosing {
			e = ErrClosed
		}
		err = &PathError{Op: "close", Path: file.name, Err: e}
	}

	// 不再需要终结器了
	runtime.SetFinalizer(file, nil)
	return err
}

// seek 将文件上下一次 Read 或 Write 的偏移量设置为 offset，
// 其解释取决于 whence：0 表示相对于文件起始位置，1 表示
// 相对于当前偏移量，2 表示相对于末尾。
// 它返回新的偏移量和错误（如果有）。
func (f *File) seek(offset int64, whence int) (ret int64, err error) {
	if info := f.dirinfo.Swap(nil); info != nil {
		// 释放缓存的 dirinfo，这样如果我们再次将此文件
		// 作为目录访问，就会分配一个新的。参见 #35767 和 #37161。
		info.close()
	}
	ret, err = f.pfd.Seek(offset, whence)
	runtime.KeepAlive(f)
	return ret, err
}

// Truncate 更改指定文件的大小。
// 如果文件是符号链接，它会更改链接目标的大小。
func Truncate(name string, size int64) error {
	f, e := OpenFile(name, O_WRONLY, 0666)
	if e != nil {
		return e
	}
	defer f.Close()
	e1 := f.Truncate(size)
	if e1 != nil {
		return e1
	}
	return nil
}

// Remove 删除指定文件或目录。
// 如果出错，错误的类型为 [*PathError]。
func Remove(name string) error {
	p, e := syscall.UTF16PtrFromString(fixLongPath(name))
	if e != nil {
		return &PathError{Op: "remove", Path: name, Err: e}
	}

	// Go 文件接口迫使我们知道
	// name 是文件还是目录。两种都尝试。
	e = syscall.DeleteFile(p)
	if e == nil {
		return nil
	}
	e1 := syscall.RemoveDirectory(p)
	if e1 == nil {
		return nil
	}

	// 两者都失败了：弄清该返回哪个错误。
	if e1 != e {
		a, e2 := syscall.GetFileAttributes(p)
		if e2 != nil {
			e = e2
		} else {
			if a&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
				e = e1
			} else if a&syscall.FILE_ATTRIBUTE_READONLY != 0 {
				if e1 = syscall.SetFileAttributes(p, a&^syscall.FILE_ATTRIBUTE_READONLY); e1 == nil {
					if e = syscall.DeleteFile(p); e == nil {
						return nil
					}
				}
			}
		}
	}
	return &PathError{Op: "remove", Path: name, Err: e}
}

func rename(oldname, newname string) error {
	e := windows.Rename(fixLongPath(oldname), fixLongPath(newname))
	if e != nil {
		return &LinkError{"rename", oldname, newname, e}
	}
	return nil
}

// Pipe 返回一对相连的 File；从 r 读取会返回写入 w 的字节。
// 它返回这两个文件和错误（如果有）。返回文件底层的
// Windows 句柄被标记为可由子进程继承。
func Pipe() (r *File, w *File, err error) {
	var p [2]syscall.Handle
	e := syscall.Pipe(p[:])
	if e != nil {
		return nil, nil, NewSyscallError("pipe", e)
	}
	// syscall.Pipe 总是返回一个非阻塞句柄。
	return newFile(p[0], "|0", kindPipe, false), newFile(p[1], "|1", kindPipe, false), nil
}

var useGetTempPath2 = sync.OnceValue(func() bool {
	return windows.ErrorLoadingGetTempPath2() == nil
})

func tempDir() string {
	getTempPath := syscall.GetTempPath
	if useGetTempPath2() {
		getTempPath = windows.GetTempPath2
	}
	n := uint32(syscall.MAX_PATH)
	for {
		b := make([]uint16, n)
		n, _ = getTempPath(uint32(len(b)), &b[0])
		if n > uint32(len(b)) {
			continue
		}
		if n == 3 && b[1] == ':' && b[2] == '\\' {
			// 对于像 C:\ 这样的路径，什么都不做。
		} else if n > 0 && b[n-1] == '\\' {
			// 否则移除结尾的 \。
			n--
		}
		return syscall.UTF16ToString(b[:n])
	}
}

// Link 将 newname 创建为指向 oldname 文件的硬链接。
// 如果出错，错误的类型为 *LinkError。
func Link(oldname, newname string) error {
	n, err := syscall.UTF16PtrFromString(fixLongPath(newname))
	if err != nil {
		return &LinkError{"link", oldname, newname, err}
	}
	o, err := syscall.UTF16PtrFromString(fixLongPath(oldname))
	if err != nil {
		return &LinkError{"link", oldname, newname, err}
	}
	err = syscall.CreateHardLink(n, o, 0)
	if err != nil {
		return &LinkError{"link", oldname, newname, err}
	}
	return nil
}

// Symlink 将 newname 创建为指向 oldname 的符号链接。
// 在 Windows 上，指向不存在的 oldname 的符号链接会创建文件符号链接；
// 如果 oldname 之后作为目录被创建，该符号链接将无法工作。
// 如果出错，错误的类型为 *LinkError。
func Symlink(oldname, newname string) error {
	// '/' 在链接内容中不起作用
	oldname = filepathlite.FromSlash(oldname)

	// 当 oldname 是相对路径时，需要它的确切位置来判断它是否是目录
	destpath := oldname
	if v := filepathlite.VolumeName(oldname); v == "" {
		if len(oldname) > 0 && IsPathSeparator(oldname[0]) {
			// oldname 相对于包含 newname 的卷。
			if v = filepathlite.VolumeName(newname); v != "" {
				// 显式添加卷前缀，因为它可能与当前工作
				// 目录的卷不同。
				destpath = v + oldname
			}
		} else {
			// oldname 相对于 newname。
			destpath = dirname(newname) + `\` + oldname
		}
	}

	fi, err := Stat(destpath)
	isdir := err == nil && fi.IsDir()

	n, err := syscall.UTF16PtrFromString(fixLongPath(newname))
	if err != nil {
		return &LinkError{"symlink", oldname, newname, err}
	}
	var o *uint16
	if filepathlite.IsAbs(oldname) {
		o, err = syscall.UTF16PtrFromString(fixLongPath(oldname))
	} else {
		// 对于相对符号链接，不要在 oldname 上使用 fixLongPath，
		// 因为它会把名称变成绝对路径，从而创建
		// 一个绝对符号链接而不是相对符号链接。
		// 请注意，对于超过 MAX_PATH 的相对符号链接，CreateSymbolicLinkW
		// 不会失败，因此这并不能阻止
		// 创建任意长的路径名。
		o, err = syscall.UTF16PtrFromString(oldname)
	}
	if err != nil {
		return &LinkError{"symlink", oldname, newname, err}
	}

	var flags uint32 = windows.SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE
	if isdir {
		flags |= syscall.SYMBOLIC_LINK_FLAG_DIRECTORY
	}
	err = syscall.CreateSymbolicLink(n, o, flags)
	if err != nil {
		// 非特权创建标志在 Windows 10
		//（1703，v10.0.14972）以下不受支持。不带它重试。
		flags &^= windows.SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE
		err = syscall.CreateSymbolicLink(n, o, flags)
		if err != nil {
			return &LinkError{"symlink", oldname, newname, err}
		}
	}
	return nil
}

// openSymlink 使用带 FILE_FLAG_OPEN_REPARSE_POINT 参数的
// CreateFile Windows API 进行调用，这样如果 path 是符号链接，
// Windows 就不会跟随它。
// openSymlink 返回已打开的文件句柄。
func openSymlink(path string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	attrs := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS)
	// 使用 FILE_FLAG_OPEN_REPARSE_POINT，否则 CreateFile 会跟随符号链接。
	// 参见 https://docs.microsoft.com/en-us/windows/desktop/FileIO/symbolic-link-effects-on-file-systems-functions#createfile-and-createfiletransacted
	attrs |= syscall.FILE_FLAG_OPEN_REPARSE_POINT
	h, err := syscall.CreateFile(p, 0, 0, nil, syscall.OPEN_EXISTING, attrs, 0)
	if err != nil {
		return 0, err
	}
	return h, nil
}

var winreadlinkvolume = godebug.New("winreadlinkvolume")

// normaliseLinkPath 将由
// DeviceIoControl(h, FSCTL_GET_REPARSE_POINT, ...) 返回的绝对路径
// 转换为所有 Windows API 都能接受的路径。
// 例如，它会转换
//
//	\??\C:\foo\bar 为 C:\foo\bar
//	\??\UNC\foo\bar 为 \\foo\bar
//	\??\Volume{abc}\ 为 \\?\Volume{abc}\
func normaliseLinkPath(path string) (string, error) {
	if len(path) < 4 || path[:4] != `\??\` {
		// 意外的路径，原样返回
		return path, nil
	}
	// 我们有一个以 \??\ 开头的路径
	s := path[4:]
	switch {
	case len(s) >= 2 && s[1] == ':': // \??\C:\foo\bar
		return s, nil
	case len(s) >= 4 && s[:4] == `UNC\`: // \??\UNC\foo\bar
		return `\\` + s[4:], nil
	}

	// \??\Volume{abc}\
	if winreadlinkvolume.Value() != "0" {
		return `\\?\` + path[4:], nil
	}
	winreadlinkvolume.IncNonDefault()

	h, err := openSymlink(path)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)

	buf := make([]uint16, 100)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), windows.VOLUME_NAME_DOS)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buf)) {
			break
		}
		buf = make([]uint16, n)
	}
	s = syscall.UTF16ToString(buf)
	if len(s) > 4 && s[:4] == `\\?\` {
		s = s[4:]
		if len(s) > 3 && s[:3] == `UNC` {
			// 返回类似 \\server\share\... 的路径
			return `\` + s[3:], nil
		}
		return s, nil
	}
	return "", errors.New("GetFinalPathNameByHandle returned unexpected path: " + s)
}

func readReparseLink(path string) (string, error) {
	h, err := openSymlink(path)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	return readReparseLinkHandle(h)
}

func readReparseLinkHandle(h syscall.Handle) (string, error) {
	rdbbuf := make([]byte, syscall.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var bytesReturned uint32
	err := syscall.DeviceIoControl(h, syscall.FSCTL_GET_REPARSE_POINT, nil, 0, &rdbbuf[0], uint32(len(rdbbuf)), &bytesReturned, nil)
	if err != nil {
		return "", err
	}

	rdb := (*windows.REPARSE_DATA_BUFFER)(unsafe.Pointer(&rdbbuf[0]))
	switch rdb.ReparseTag {
	case syscall.IO_REPARSE_TAG_SYMLINK:
		rb := (*windows.SymbolicLinkReparseBuffer)(unsafe.Pointer(&rdb.DUMMYUNIONNAME))
		s := rb.Path()
		if rb.Flags&windows.SYMLINK_FLAG_RELATIVE != 0 {
			return s, nil
		}
		return normaliseLinkPath(s)
	case windows.IO_REPARSE_TAG_MOUNT_POINT:
		return normaliseLinkPath((*windows.MountPointReparseBuffer)(unsafe.Pointer(&rdb.DUMMYUNIONNAME)).Path())
	default:
		// 该路径不是符号链接或 junction，而是另一种类型的
		// 重解析点
		return "", syscall.ENOENT
	}
}

func readlink(name string) (string, error) {
	s, err := readReparseLink(fixLongPath(name))
	if err != nil {
		return "", &PathError{Op: "readlink", Path: name, Err: err}
	}
	return s, nil
}
