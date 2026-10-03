// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || (js && wasm) || wasip1

package os

import (
	"internal/poll"
	"internal/syscall/unix"
	"io/fs"
	"runtime"
	"sync/atomic"
	"syscall"
	_ "unsafe" // 用于 go:linkname
)

const _UTIME_OMIT = unix.UTIME_OMIT

// fixLongPath 在非 Windows 平台上为空操作。
func fixLongPath(path string) string {
	return path
}

func rename(oldname, newname string) error {
	fi, err := Lstat(newname)
	if err == nil && fi.IsDir() {
		// 这个函数可能返回两种独立的错误：
		// 一种是因为 oldname 有问题，另一种是因为 newname 有问题。
		// 此时我们已经确定 newname 有问题。
		// 但以防 oldname 也有问题，优先返回
		// oldname 的错误，因为这是我们历史上的做法。
		// 然而，如果旧名称和新名称不同，但它们
		// 指向同一个文件，这意味着在不区分大小写的
		// 文件系统上进行仅大小写不同的重命名，这是可以的。
		if ofi, err := Lstat(oldname); err != nil {
			if pe, ok := err.(*PathError); ok {
				err = pe.Err
			}
			return &LinkError{"rename", oldname, newname, err}
		} else if newname == oldname || !SameFile(fi, ofi) {
			return &LinkError{"rename", oldname, newname, syscall.EEXIST}
		}
	}
	err = ignoringEINTR(func() error {
		return syscall.Rename(oldname, newname)
	})
	if err != nil {
		return &LinkError{"rename", oldname, newname, err}
	}
	return nil
}

// file 是 *File 的真实表示。
// 额外的间接层确保 os 的任何客户端
// 都无法覆盖这些数据，否则可能导致终结器
// 关闭错误的文件描述符。
type file struct {
	pfd         poll.FD
	name        string
	dirinfo     atomic.Pointer[dirInfo] // 除非正在读取目录，否则为 nil
	nonblock    bool                    // 我们是否设置了非阻塞模式
	stdoutOrErr bool                    // 这是否是 stdout 或 stderr
	appendMode  bool                    // 文件是否以追加方式打开
	inRoot      bool                    // 文件是否在 Root 中打开
}

// fd 是 Fd 的 Unix 实现。
func (f *File) fd() uintptr {
	if f == nil {
		return ^(uintptr(0))
	}

	// 如果我们将文件描述符置于非阻塞模式，
	// 那么在返回它之前要将其设置为阻塞模式，
	// 因为从历史上看，我们总是返回以阻塞模式
	// 打开的描述符。File 会继续工作，
	// 但任何阻塞操作都会占用一个线程。
	if f.nonblock {
		f.pfd.SetBlocking()
	}

	return uintptr(f.pfd.Sysfd)
}

// newFileFromNewFile 由 [NewFile] 调用。
func newFileFromNewFile(fd uintptr, name string) *File {
	fdi := int(fd)
	if fdi < 0 {
		return nil
	}

	flags, err := unix.Fcntl(fdi, syscall.F_GETFL, 0)
	if err != nil {
		flags = 0
	}
	f := newFile(fdi, name, kindNewFile, unix.HasNonblockFlag(flags))
	f.appendMode = flags&syscall.O_APPEND != 0
	return f
}

// net_newUnixFile 是由 net.conn.File 调用的隐藏入口点。
// 这样做是为了让非阻塞网络连接在代码调用 Fd 方法时
// 变为阻塞。我们不希望对 NewFile 的直接调用有这种
// 行为：将非阻塞描述符传给 NewFile 后，若你用 Fd 取回它，
// 它应保持非阻塞。但对于 net.conn.File，
// 对 NewFile 的调用对用户是隐藏的。从历史上看，在这种情况下
// Fd 方法返回的是阻塞描述符，我们希望
// 保留这一行为，因为现有代码期望并依赖它。
//
//go:linkname net_newUnixFile net.newUnixFile
func net_newUnixFile(fd int, name string) *File {
	if fd < 0 {
		panic("invalid FD")
	}

	return newFile(fd, name, kindSock, true)
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
	// kindNoPoll 表示我们不应将该描述符置于
	// 非阻塞模式，因为我们知道它不是管道或 FIFO。
	// 由 openDirAt 和 openDirNolog 用于目录。
	kindNoPoll
)

// newFile 类似于 NewFile，但如果是从 OpenFile 或 Pipe 调用
// （如 kind 参数中所传），它会尝试将该文件添加到
// 运行时轮询器。
func newFile(fd int, name string, kind newFileKind, nonBlocking bool) *File {
	f := &File{&file{
		pfd: poll.FD{
			Sysfd:         fd,
			IsStream:      true,
			ZeroReadIsEOF: true,
		},
		name:        name,
		stdoutOrErr: fd == 1 || fd == 2,
	}}

	pollable := kind == kindOpenFile || kind == kindPipe || kind == kindSock || nonBlocking

	// 像 *BSD/Darwin 上 kqueue 中的普通文件和 FIFO
	// 这类东西可能无法正常工作（或按其手册所述并不准确）。
	// 因此，我们应避免将它们添加到基于 kqueue 的
	// netpoller。更多上下文请查看 #19093、#24164 和 #66239。
	//
	// 如果该 fd 是通过 OpenFile 之外的任何路径传给我们的，
	// 我们假定那些调用方知道自己在做什么，因此不会
	// 执行此检查，并允许将其添加到 kqueue。
	if kind == kindOpenFile {
		switch runtime.GOOS {
		case "darwin", "ios", "dragonfly", "freebsd", "netbsd", "openbsd":
			var st syscall.Stat_t
			err := ignoringEINTR(func() error {
				return syscall.Fstat(fd, &st)
			})
			typ := st.Mode & syscall.S_IFMT
			// 不要在 *BSD 上对普通文件尝试使用 kqueue。
			// 在 FreeBSD 上，普通文件总是被
			// 报告为可写就绪。
			// 在 Dragonfly、NetBSD 和 OpenBSD 上，该 fd 只会
			// 被通知一次就绪（读和写都如此）。
			// Issue 19093。
			// 也不要将目录添加到 netpoller。
			if err == nil && (typ == syscall.S_IFREG || typ == syscall.S_IFDIR) {
				pollable = false
			}

			// 除了上述针对普通文件描述的行为之外，
			// 在 Darwin 上，kqueue 无法正常处理 fifo：
			// 关闭最后一个写端不会为任何读端触发
			// kqueue 事件。参见 issue #24164。
			if (runtime.GOOS == "darwin" || runtime.GOOS == "ios") && typ == syscall.S_IFIFO {
				pollable = false
			}
		}
	}

	clearNonBlock := false
	if pollable {
		// 该描述符已经处于非阻塞模式。
		// 只有在我们将该文件置于非阻塞模式时，
		// 才会设置 f.nonblock。
		if nonBlocking {
			// 参见 net_newUnixFile 上的注释。
			if kind == kindSock {
				f.nonblock = true // 告知 Fd 返回阻塞描述符
			}
		} else if err := syscall.SetNonblock(fd, true); err == nil {
			f.nonblock = true
			clearNonBlock = true
		} else {
			pollable = false
		}
	}

	// 这里的错误表示向 netpoll 系统注册失败。
	// 对于 epoll/kqueue 不支持的文件描述符，
	// 例如 Linux 系统上的磁盘文件，可能会发生这种情况。
	// 我们假定任何真正的错误都会在之后的 I/O 中
	// 显现出来。
	// 如果是我们设置的阻塞行为，我们确实会将其恢复。
	if pollErr := f.pfd.Init("file", pollable); pollErr != nil && clearNonBlock {
		if err := syscall.SetNonblock(fd, false); err == nil {
			f.nonblock = false
		}
	}

	runtime.SetFinalizer(f.file, (*file).close)
	return f
}

func sigpipe() // 在 runtime 包中实现

// epipecheck 如果我们在标准输出或标准错误上遇到 EPIPE 错误，
// 则引发 SIGPIPE。参见 os/signal 中的 SIGPIPE 文档，
// 以及 issue 11845。
func epipecheck(file *File, e error) {
	if e == syscall.EPIPE && file.stdoutOrErr {
		sigpipe()
	}
}

// DevNull 是操作系统 “空设备” 的名称。
// 在类 Unix 系统上，它是 "/dev/null"；在 Windows 上，是 "NUL"。
const DevNull = "/dev/null"

// openFileNolog 是 OpenFile 的 Unix 实现。
// 此处的改动如果相关，应同步反映到 openDirAt 和 openDirNolog。
func openFileNolog(name string, flag int, perm FileMode) (*File, error) {
	setSticky := false
	if !supportsCreateWithStickyBit && flag&O_CREATE != 0 && perm&ModeSticky != 0 {
		if _, err := Stat(name); IsNotExist(err) {
			setSticky = true
		}
	}

	var (
		r int
		s poll.SysFile
		e error
	)
	// 根据 issues 11180 和 39237，我们必须在这里检查 EINTR。
	ignoringEINTR(func() error {
		r, s, e = open(name, flag|syscall.O_CLOEXEC, syscallMode(perm))
		return e
	})
	if e != nil {
		return nil, &PathError{Op: "open", Path: name, Err: e}
	}

	// open(2) 本身在 *BSD 和 Solaris 上不会处理粘滞位
	if setSticky {
		setStickyBit(name)
	}

	// 这里与 fork/exec 存在竞态，我们只能
	// 接受它。参见 ../syscall/exec_unix.go。
	if !supportsCloseOnExec {
		syscall.CloseOnExec(r)
	}

	f := newFile(r, name, kindOpenFile, unix.HasNonblockFlag(flag))
	f.pfd.SysFile = s
	return f, nil
}

func openDirNolog(name string) (*File, error) {
	var (
		r int
		s poll.SysFile
		e error
	)
	ignoringEINTR(func() error {
		r, s, e = open(name, O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY, 0)
		return e
	})
	if e != nil {
		return nil, &PathError{Op: "open", Path: name, Err: e}
	}

	if !supportsCloseOnExec {
		syscall.CloseOnExec(r)
	}

	f := newFile(r, name, kindNoPoll, false)
	f.pfd.SysFile = s
	return f, nil
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
// 如果出错，错误的类型为 [*PathError]。
func Truncate(name string, size int64) error {
	e := ignoringEINTR(func() error {
		return syscall.Truncate(name, size)
	})
	if e != nil {
		return &PathError{Op: "truncate", Path: name, Err: e}
	}
	return nil
}

// Remove 删除指定文件或（空）目录。
// 如果出错，错误的类型为 [*PathError]。
func Remove(name string) error {
	// 系统调用接口迫使我们知道
	// name 是文件还是目录。
	// 两种都尝试：平均而言这比
	// 先 Stat 再用对应的方法更省。
	e := ignoringEINTR(func() error {
		return syscall.Unlink(name)
	})
	if e == nil {
		return nil
	}
	e1 := ignoringEINTR(func() error {
		return syscall.Rmdir(name)
	})
	if e1 == nil {
		return nil
	}

	// 两者都失败了：弄清该返回哪个错误。
	// OS X 和 Linux 在 unlink(dir) 是否
	// 返回 EISDIR 上存在差异，因此不能用它。然而，
	// 两者都一致认为 rmdir(file) 返回 ENOTDIR，
	// 所以我们可以用它来决定哪个错误是真正的错误。
	// 如果给定的是错误的文件路径，如 /etc/passwd/foo，
	// Rmdir 也可能返回 ENOTDIR，但在那种情况下，
	// 两个错误都会是 ENOTDIR，所以使用 unlink
	// 返回的错误是没问题的。
	if e1 != syscall.ENOTDIR {
		e = e1
	}
	return &PathError{Op: "remove", Path: name, Err: e}
}

func tempDir() string {
	dir := Getenv("TMPDIR")
	if dir == "" {
		if runtime.GOOS == "android" {
			dir = "/data/local/tmp"
		} else {
			dir = "/tmp"
		}
	}
	return dir
}

// Link 将 newname 创建为指向 oldname 文件的硬链接。
// 如果出错，错误的类型为 *LinkError。
func Link(oldname, newname string) error {
	e := ignoringEINTR(func() error {
		return syscall.Link(oldname, newname)
	})
	if e != nil {
		return &LinkError{"link", oldname, newname, e}
	}
	return nil
}

// Symlink 将 newname 创建为指向 oldname 的符号链接。
// 在 Windows 上，指向不存在的 oldname 的符号链接会创建文件符号链接；
// 如果 oldname 之后作为目录被创建，该符号链接将无法工作。
// 如果出错，错误的类型为 *LinkError。
func Symlink(oldname, newname string) error {
	e := ignoringEINTR(func() error {
		return syscall.Symlink(oldname, newname)
	})
	if e != nil {
		return &LinkError{"symlink", oldname, newname, e}
	}
	return nil
}

func readlink(name string) (string, error) {
	for len := 128; ; len *= 2 {
		b := make([]byte, len)
		n, err := ignoringEINTR2(func() (int, error) {
			return fixCount(syscall.Readlink(name, b))
		})
		// 缓冲区太小
		if (runtime.GOOS == "aix" || runtime.GOOS == "wasip1") && err == syscall.ERANGE {
			continue
		}
		if err != nil {
			return "", &PathError{Op: "readlink", Path: name, Err: err}
		}
		if n < len {
			return string(b[0:n]), nil
		}
	}
}

type unixDirent struct {
	parent string
	name   string
	typ    FileMode
	info   FileInfo
}

func (d *unixDirent) Name() string   { return d.name }
func (d *unixDirent) IsDir() bool    { return d.typ.IsDir() }
func (d *unixDirent) Type() FileMode { return d.typ }

func (d *unixDirent) Info() (FileInfo, error) {
	if d.info != nil {
		return d.info, nil
	}
	return Lstat(d.parent + "/" + d.name)
}

func (d *unixDirent) String() string {
	return fs.FormatDirEntry(d)
}

func newUnixDirent(parent *File, name string, typ FileMode) (DirEntry, error) {
	ude := &unixDirent{
		parent: parent.name,
		name:   name,
		typ:    typ,
	}
	// 当父文件是在 Root 中打开时，
	// 我们不能使用惰性 lstat 来加载 FileInfo。
	// 这里使用 lstatat。
	if typ != ^FileMode(0) && !parent.inRoot {
		return ude, nil
	}

	info, err := parent.lstatat(name)
	if err != nil {
		return nil, err
	}

	ude.typ = info.Mode().Type()
	ude.info = info
	return ude, nil
}
