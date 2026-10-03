// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package os 为操作系统功能提供与平台无关的接口。
// 其设计类似 Unix，不过错误处理是 Go 风格的；
// 失败的调用返回 error 类型的值，而不是错误编号。
// 通常，错误中会包含更多信息。例如，
// 如果接受文件名的调用失败，如 [Open] 或 [Stat]，打印时错误中
// 会包含导致失败的文件名，且其类型为
// [*PathError]，可以对其进行解包以获取更多信息。
//
// os 接口旨在跨所有操作系统保持统一。
// 并非普遍可用的功能出现在特定于系统的 syscall 包中。
//
// 下面是一个简单示例，打开一个文件并读取其中一部分。
//
//	file, err := os.Open("file.go") // 用于读取访问。
//	if err != nil {
//		log.Fatal(err)
//	}
//
// 如果打开失败，错误字符串会不言自明，例如
//
//	open file.go: no such file or directory
//
// 随后可以将文件的数据读入一个字节切片。Read 和
// Write 从参数切片的长度获取其字节数。
//
//	data := make([]byte, 100)
//	count, err := file.Read(data)
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Printf("read %d bytes: %q\n", count, data[:count])
//
// # 并发
//
// [File] 的方法对应文件系统操作。所有方法都
// 可安全并发使用。一个 File 上的最大并发
// 操作数可能受操作系统或系统限制。这个
// 数字应该很高，但超过它可能会降低性能或导致其他问题。
package os

import (
	"errors"
	"internal/filepathlite"
	"internal/poll"
	"internal/testlog"
	"io"
	"io/fs"
	"runtime"
	"slices"
	"syscall"
	"time"
	"unsafe"
)

// Name 返回文件呈现给 Open 时的名称。
//
// 在 [Close] 之后调用 Name 是安全的。
func (f *File) Name() string { return f.name }

// Stdin、Stdout 和 Stderr 是指向标准输入、
// 标准输出和标准错误文件描述符的已打开的 File。
//
// 注意，Go 运行时在发生 panic 和崩溃时会写入标准错误；
// 关闭 Stderr 可能导致这些消息被写到别处，
// 也许会写到之后打开的文件中。
var (
	Stdin  = NewFile(uintptr(syscall.Stdin), "/dev/stdin")
	Stdout = NewFile(uintptr(syscall.Stdout), "/dev/stdout")
	Stderr = NewFile(uintptr(syscall.Stderr), "/dev/stderr")
)

// OpenFile 的标志，对底层系统的标志进行了封装。
// 并非所有标志都在给定的系统上实现。
const (
	// 必须指定 O_RDONLY、O_WRONLY 或 O_RDWR 中的恰好一个。
	O_RDONLY int = syscall.O_RDONLY // 以只读方式打开文件。
	O_WRONLY int = syscall.O_WRONLY // 以只写方式打开文件。
	O_RDWR   int = syscall.O_RDWR   // 以读写方式打开文件。
	// 其余的值可以按位或进来以控制行为。
	O_APPEND int = syscall.O_APPEND // 写入时向文件追加数据。
	O_CREATE int = syscall.O_CREAT  // 如果文件不存在则创建新文件。
	O_EXCL   int = syscall.O_EXCL   // 与 O_CREATE 一起使用，文件必须不存在。
	O_SYNC   int = syscall.O_SYNC   // 以同步 I/O 方式打开。
	O_TRUNC  int = syscall.O_TRUNC  // 打开时截断常规可写文件。
)

// Seek 的 whence 值。
//
// Deprecated: 请使用 io.SeekStart、io.SeekCurrent 和 io.SeekEnd。
const (
	SEEK_SET int = 0 // 相对于文件起始位置 seek
	SEEK_CUR int = 1 // 相对于当前偏移量 seek
	SEEK_END int = 2 // 相对于末尾 seek
)

// LinkError 记录链接、符号链接或重命名系统调用期间的错误
// 以及导致该错误的路径。
type LinkError struct {
	Op  string
	Old string
	New string
	Err error
}

func (e *LinkError) Error() string {
	return e.Op + " " + e.Old + " " + e.New + ": " + e.Err.Error()
}

func (e *LinkError) Unwrap() error {
	return e.Err
}

// NewFile 使用给定的文件描述符和名称返回一个新的 [File]。
// 如果 fd 不是有效的文件描述符，返回的值将为 nil。
//
// NewFile 的行为在某些平台上有所不同：
//
//   - 在 Unix 上，如果 fd 处于非阻塞模式，NewFile 会尝试返回一个可轮询的文件。
//   - 在 Windows 上，如果 fd 是为异步 I/O 打开的（即在 [syscall.CreateFile] 调用中
//     指定了 [syscall.FILE_FLAG_OVERLAPPED]），NewFile 会通过将 fd 与
//     Go 运行时的 I/O 完成端口关联，尝试返回一个可轮询的文件。
//     如果关联失败，I/O 操作将以同步方式执行。
//
// 只有可轮询的文件才支持 [File.SetDeadline]、[File.SetReadDeadline] 和 [File.SetWriteDeadline]。
//
// 在将其传给 NewFile 之后，fd 可能会在 [File.Fd] 的注释中所描述的相同条件下变为无效，
// 并且适用相同的约束。
func NewFile(fd uintptr, name string) *File {
	return newFileFromNewFile(fd, name)
}

// Read 从 File 中读取最多 len(b) 个字节并存储到 b 中。
// 它返回读取的字节数以及遇到的任何错误。
// 在文件末尾，Read 返回 0、io.EOF。
func (f *File) Read(b []byte) (n int, err error) {
	if err := f.checkValid("read"); err != nil {
		return 0, err
	}
	n, e := f.read(b)
	return n, f.wrapErr("read", e)
}

// ReadAt 从 File 中从字节偏移量 off 开始读取 len(b) 个字节。
// 它返回读取的字节数和错误（如果有）。
// 当 n < len(b) 时，ReadAt 总是返回非 nil 的错误。
// 在文件末尾，该错误为 io.EOF。
func (f *File) ReadAt(b []byte, off int64) (n int, err error) {
	if err := f.checkValid("read"); err != nil {
		return 0, err
	}

	if off < 0 {
		return 0, &PathError{Op: "readat", Path: f.name, Err: errors.New("negative offset")}
	}

	for len(b) > 0 {
		m, e := f.pread(b, off)
		if e != nil {
			err = f.wrapErr("read", e)
			break
		}
		n += m
		b = b[m:]
		off += int64(m)
	}
	return
}

// ReadFrom 实现 io.ReaderFrom。
func (f *File) ReadFrom(r io.Reader) (n int64, err error) {
	if err := f.checkValid("write"); err != nil {
		return 0, err
	}
	n, handled, e := f.readFrom(r)
	if !handled {
		return genericReadFrom(f, r) // 不做包装
	}
	return n, f.wrapErr("write", e)
}

// noReadFrom 可以与其他类型一起嵌入，
// 以隐藏那个其他类型的 ReadFrom 方法。
type noReadFrom struct{}

// ReadFrom 隐藏另一个 ReadFrom 方法。
// 它永远不应该被调用。
func (noReadFrom) ReadFrom(io.Reader) (int64, error) {
	panic("can't happen")
}

// fileWithoutReadFrom 实现 *File 除 ReadFrom 之外的所有方法。
// 这样做的目的是允许 ReadFrom 调用 io.Copy
// 而不会导致对 ReadFrom 的递归调用。
type fileWithoutReadFrom struct {
	noReadFrom
	*File
}

func genericReadFrom(f *File, r io.Reader) (int64, error) {
	return io.Copy(fileWithoutReadFrom{File: f}, r)
}

// Write 从 b 向 File 写入 len(b) 个字节。
// 它返回写入的字节数和错误（如果有）。
// 当 n != len(b) 时，Write 返回非 nil 的错误。
func (f *File) Write(b []byte) (n int, err error) {
	if err := f.checkValid("write"); err != nil {
		return 0, err
	}
	n, e := f.write(b)
	if n < 0 {
		n = 0
	}
	if n != len(b) {
		err = io.ErrShortWrite
	}

	epipecheck(f, e)

	if e != nil {
		err = f.wrapErr("write", e)
	}

	return n, err
}

var errWriteAtInAppendMode = errors.New("os: invalid use of WriteAt on file opened with O_APPEND")

// WriteAt 从字节偏移量 off 开始向 File 写入 len(b) 个字节。
// 它返回写入的字节数和错误（如果有）。
// 当 n != len(b) 时，WriteAt 返回非 nil 的错误。
//
// 如果文件是以 [O_APPEND] 标志打开的，WriteAt 会返回错误。
func (f *File) WriteAt(b []byte, off int64) (n int, err error) {
	if err := f.checkValid("write"); err != nil {
		return 0, err
	}
	if f.appendMode {
		return 0, errWriteAtInAppendMode
	}

	if off < 0 {
		return 0, &PathError{Op: "writeat", Path: f.name, Err: errors.New("negative offset")}
	}

	for len(b) > 0 {
		m, e := f.pwrite(b, off)
		if e != nil {
			err = f.wrapErr("write", e)
			break
		}
		n += m
		b = b[m:]
		off += int64(m)
	}
	return
}

// WriteTo 实现 io.WriterTo。
func (f *File) WriteTo(w io.Writer) (n int64, err error) {
	if err := f.checkValid("read"); err != nil {
		return 0, err
	}
	n, handled, e := f.writeTo(w)
	if handled {
		return n, f.wrapErr("read", e)
	}
	return genericWriteTo(f, w) // 不做包装
}

// noWriteTo 可以与其他类型一起嵌入，
// 以隐藏那个其他类型的 WriteTo 方法。
type noWriteTo struct{}

// WriteTo 隐藏另一个 WriteTo 方法。
// 它永远不应该被调用。
func (noWriteTo) WriteTo(io.Writer) (int64, error) {
	panic("can't happen")
}

// fileWithoutWriteTo 实现 *File 除 WriteTo 之外的所有方法。
// 这样做的目的是允许 WriteTo 调用 io.Copy
// 而不会导致对 WriteTo 的递归调用。
type fileWithoutWriteTo struct {
	noWriteTo
	*File
}

func genericWriteTo(f *File, w io.Writer) (int64, error) {
	return io.Copy(w, fileWithoutWriteTo{File: f})
}

// Seek 将文件上下一次 Read 或 Write 的偏移量设置为 offset，
// 其解释取决于 whence：0 表示相对于文件起始位置，1 表示
// 相对于当前偏移量，2 表示相对于末尾。
// 它返回新的偏移量和错误（如果有）。
// 对以 [O_APPEND] 打开的文件，Seek 的行为未指定。
func (f *File) Seek(offset int64, whence int) (ret int64, err error) {
	if err := f.checkValid("seek"); err != nil {
		return 0, err
	}
	r, e := f.seek(offset, whence)
	if e == nil && f.dirinfo.Load() != nil && r != 0 {
		e = syscall.EISDIR
	}
	if e != nil {
		return 0, f.wrapErr("seek", e)
	}
	return r, nil
}

// WriteString 类似于 Write，但写入的是字符串 s 的内容，
// 而不是字节切片。
func (f *File) WriteString(s string) (n int, err error) {
	b := unsafe.Slice(unsafe.StringData(s), len(s))
	return f.Write(b)
}

// Mkdir 使用指定的名称和权限位（在 umask 之前）创建一个新目录。
// 如果出错，错误的类型为 [*PathError]。
func Mkdir(name string, perm FileMode) error {
	longName := fixLongPath(name)
	e := ignoringEINTR(func() error {
		return syscall.Mkdir(longName, syscallMode(perm))
	})

	if e != nil {
		return &PathError{Op: "mkdir", Path: name, Err: e}
	}

	// mkdir(2) 本身在 *BSD 和 Solaris 上不会处理粘滞位
	if !supportsCreateWithStickyBit && perm&ModeSticky != 0 {
		e = setStickyBit(name)

		if e != nil {
			Remove(name)
			return e
		}
	}

	return nil
}

// setStickyBit 将 ModeSticky 添加到 path 的权限位中，非原子操作。
func setStickyBit(name string) error {
	fi, err := Stat(name)
	if err != nil {
		return err
	}
	return Chmod(name, fi.Mode()|ModeSticky)
}

// Chdir 将当前工作目录更改为指定的目录。
// 如果出错，错误的类型为 [*PathError]。
func Chdir(dir string) error {
	if e := syscall.Chdir(dir); e != nil {
		testlog.Open(dir) // 观察可能不存在的目录
		return &PathError{Op: "chdir", Path: dir, Err: e}
	}
	if runtime.GOOS == "windows" {
		abs := filepathlite.IsAbs(dir)
		getwdCache.Lock()
		if abs {
			getwdCache.dir = dir
		} else {
			getwdCache.dir = ""
		}
		getwdCache.Unlock()
	}
	if log := testlog.Logger(); log != nil {
		wd, err := Getwd()
		if err == nil {
			log.Chdir(wd)
		}
	}
	return nil
}

// Open 打开指定的文件以供读取。如果成功，可以使用返回的文件上的
// 方法进行读取；关联的文件描述符具有模式 [O_RDONLY]。
// 如果出错，错误的类型为 [*PathError]。
func Open(name string) (*File, error) {
	return OpenFile(name, O_RDONLY, 0)
}

// Create 创建或截断指定的文件。如果文件已存在，
// 则将其截断。如果文件不存在，则以模式 0o666
// （在 umask 之前）创建。如果成功，可以使用返回的 File 上的
// 方法进行 I/O；关联的文件描述符具有模式 [O_RDWR]。
// 包含该文件的目录必须已经存在。
// 如果出错，错误的类型为 [*PathError]。
func Create(name string) (*File, error) {
	return OpenFile(name, O_RDWR|O_CREATE|O_TRUNC, 0666)
}

// OpenFile 是通用的 open 调用；大多数用户会改用 Open
// 或 Create。它使用指定的标志（[O_RDONLY] 等）打开指定的文件。
// 如果文件不存在，并且传入了 [O_CREATE] 标志，
// 则以模式 perm（在 umask 之前）创建它；
// 包含它的目录必须存在。如果成功，
// 可以使用返回的 File 上的方法进行 I/O。
// 如果出错，错误的类型为 [*PathError]。
func OpenFile(name string, flag int, perm FileMode) (*File, error) {
	testlog.Open(name)
	f, err := openFileNolog(name, flag, perm)
	if err != nil {
		return nil, err
	}
	f.appendMode = flag&O_APPEND != 0

	return f, nil
}

var errPathEscapes = errors.New("path escapes from parent")

// openDir 打开一个被假定为目录的文件。因此，它会跳过
// 使文件描述符变为非阻塞的系统调用，因为这些调用耗时
// 并且会在目录的文件描述符上失败。
func openDir(name string) (*File, error) {
	testlog.Open(name)
	return openDirNolog(name)
}

// Rename 将 oldpath 重命名（移动）为 newpath。
// 如果 newpath 已存在且不是目录，Rename 会替换它。
// 如果 newpath 已存在且是目录，Rename 会返回错误。
// 当 oldpath 和 newpath 位于不同目录时，可能适用特定于操作系统的限制。
// 即使在同一个目录内，在非 Unix 平台上 Rename 也不是原子操作。
// 如果出错，错误的类型为 *LinkError。
func Rename(oldpath, newpath string) error {
	return rename(oldpath, newpath)
}

// Readlink 返回指定符号链接的目标。
// 如果出错，错误的类型为 [*PathError]。
//
// 如果链接目标是相对路径，Readlink 会返回相对路径，
// 而不会将其解析为绝对路径。
func Readlink(name string) (string, error) {
	return readlink(name)
}

// syscall 包中的许多函数返回的计数是 -1 而不是 0。
// 使用 fixCount(call()) 代替 call() 可以修正该计数。
func fixCount(n int, err error) (int, error) {
	if n < 0 {
		n = 0
	}
	return n, err
}

// checkWrapErr 是用于启用检查 poll.ErrFileClosing 意外包装错误的测试钩子。
// 它在 export_test.go 中被设置为 true，供测试使用（包括模糊测试）。
var checkWrapErr = false

// wrapErr 包装在对已打开文件的操作期间发生的错误。
// 它原样传递 io.EOF，否则将
// poll.ErrFileClosing 转换为 ErrClosed，并将错误包装进一个 PathError。
func (f *File) wrapErr(op string, err error) error {
	if err == nil || err == io.EOF {
		return err
	}
	if err == poll.ErrFileClosing {
		err = ErrClosed
	} else if checkWrapErr && errors.Is(err, poll.ErrFileClosing) {
		panic("unexpected error wrapping poll.ErrFileClosing: " + err.Error())
	}
	return &PathError{Op: op, Path: f.name, Err: err}
}

// TempDir 返回用于临时文件的默认目录。
//
// 在 Unix 系统上，如果 $TMPDIR 非空则返回它，否则返回 /tmp。
// 在 Windows 上，它使用 GetTempPath，返回 %TMP%、%TEMP%、%USERPROFILE%
// 或 Windows 目录中第一个非空的值。
// 在 Plan 9 上，它返回 /tmp。
//
// 该目录既不保证存在，也不保证具有可访问的权限。
func TempDir() string {
	return tempDir()
}

// UserCacheDir 返回用于用户特定缓存数据的默认根目录。
// 用户应该在这个目录下创建自己的应用程序特定的子目录，
// 并使用那个子目录。
//
// 在 Unix 系统上，如果 $XDG_CACHE_HOME 非空，则返回它，
// 其含义由 https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html 指定，
// 否则返回 $HOME/.cache。
// 在 Darwin 上，它返回 $HOME/Library/Caches。
// 在 Windows 上，它返回 %LocalAppData%。
// 在 Plan 9 上，它返回 $home/lib/cache。
//
// 如果无法确定位置（例如未定义 $HOME），或
// $XDG_CACHE_HOME 中的路径是相对路径，则会返回错误。
func UserCacheDir() (string, error) {
	var dir string

	switch runtime.GOOS {
	case "windows":
		dir = Getenv("LocalAppData")
		if dir == "" {
			return "", errors.New("%LocalAppData% is not defined")
		}

	case "darwin", "ios":
		dir = Getenv("HOME")
		if dir == "" {
			return "", errors.New("$HOME is not defined")
		}
		dir += "/Library/Caches"

	case "plan9":
		dir = Getenv("home")
		if dir == "" {
			return "", errors.New("$home is not defined")
		}
		dir += "/lib/cache"

	default: // Unix
		dir = Getenv("XDG_CACHE_HOME")
		if dir == "" {
			dir = Getenv("HOME")
			if dir == "" {
				return "", errors.New("neither $XDG_CACHE_HOME nor $HOME are defined")
			}
			dir += "/.cache"
		} else if !filepathlite.IsAbs(dir) {
			return "", errors.New("path in $XDG_CACHE_HOME is relative")
		}
	}

	return dir, nil
}

// UserConfigDir 返回用于用户特定配置数据的默认根目录。
// 用户应该在这个目录下创建自己的应用程序特定的
// 子目录，并使用那个子目录。
//
// 在 Unix 系统上，如果 $XDG_CONFIG_HOME 非空，则返回它，
// 其含义由 https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html 指定，
// 否则返回 $HOME/.config。
// 在 Darwin 上，它返回 $HOME/Library/Application Support。
// 在 Windows 上，它返回 %AppData%。
// 在 Plan 9 上，它返回 $home/lib。
//
// 如果无法确定位置（例如未定义 $HOME），或
// $XDG_CONFIG_HOME 中的路径是相对路径，则会返回错误。
func UserConfigDir() (string, error) {
	var dir string

	switch runtime.GOOS {
	case "windows":
		dir = Getenv("AppData")
		if dir == "" {
			return "", errors.New("%AppData% is not defined")
		}

	case "darwin", "ios":
		dir = Getenv("HOME")
		if dir == "" {
			return "", errors.New("$HOME is not defined")
		}
		dir += "/Library/Application Support"

	case "plan9":
		dir = Getenv("home")
		if dir == "" {
			return "", errors.New("$home is not defined")
		}
		dir += "/lib"

	default: // Unix
		dir = Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			dir = Getenv("HOME")
			if dir == "" {
				return "", errors.New("neither $XDG_CONFIG_HOME nor $HOME are defined")
			}
			dir += "/.config"
		} else if !filepathlite.IsAbs(dir) {
			return "", errors.New("path in $XDG_CONFIG_HOME is relative")
		}
	}

	return dir, nil
}

// UserHomeDir 返回当前用户的主目录。
//
// 在 Unix（包括 macOS）上，它返回 $HOME 环境变量。
// 在 Windows 上，它返回 %USERPROFILE%。
// 在 Plan 9 上，它返回 $home 环境变量。
//
// 如果环境中未设置期望的变量，UserHomeDir
// 会返回特定于平台的默认值或非 nil 的错误。
func UserHomeDir() (string, error) {
	env, enverr := "HOME", "$HOME"
	switch runtime.GOOS {
	case "windows":
		env, enverr = "USERPROFILE", "%userprofile%"
	case "plan9":
		env, enverr = "home", "$home"
	}
	if v := Getenv(env); v != "" {
		return v, nil
	}
	// 在某些操作系统上，主目录并非总是已定义的。
	switch runtime.GOOS {
	case "android":
		return "/sdcard", nil
	case "ios":
		return "/", nil
	}
	return "", errors.New(enverr + " is not defined")
}

// Chmod 将指定文件的模式更改为 mode。
// 如果文件是符号链接，它会更改链接目标的模式。
// 如果出错，错误的类型为 [*PathError]。
//
// 根据操作系统的不同，会使用模式位的不同子集。
//
// 在 Unix 上，使用模式的权限位、[ModeSetuid]、[ModeSetgid] 和
// [ModeSticky]。
//
// 在 Windows 上，只使用模式的 0o200 位（属主可写）；
// 它控制文件的只读属性是被设置还是被清除。
// 其他位目前未使用。为了与 Go 1.12
// 及更早版本兼容，请使用非零的 mode。对只读
// 文件使用模式 0o400，对可读可写文件使用 0o600。
//
// 在 Plan 9 上，使用模式的权限位、[ModeAppend]、[ModeExclusive]
// 和 [ModeTemporary]。
func Chmod(name string, mode FileMode) error { return chmod(name, mode) }

// Chmod 将文件的模式更改为 mode。
// 如果出错，错误的类型为 [*PathError]。
func (f *File) Chmod(mode FileMode) error { return f.chmod(mode) }

// SetDeadline 为 File 设置读取和写入截止时间。
// 它等价于同时调用 SetReadDeadline 和 SetWriteDeadline。
//
// 只有某些类型的文件支持设置截止时间。对不支持截止时间的文件
// 调用 SetDeadline 将返回 ErrNoDeadline。
// 在大多数系统上，普通文件不支持截止时间，但管道支持。
//
// 截止时间是一个绝对时间，超过它之后 I/O 操作会失败并返回
// 错误，而不是阻塞。截止时间适用于所有未来的和挂起的
// I/O，而不仅仅是紧随其后的 Read 或 Write 调用。
// 在超过截止时间之后，可以通过将截止时间设置到未来
// 来刷新连接。
//
// 如果超过截止时间，对 Read 或 Write 或其他 I/O
// 方法的调用会返回一个包装了 ErrDeadlineExceeded 的错误。
// 这可以使用 errors.Is(err, os.ErrDeadlineExceeded) 来检测。
// 该错误实现了 Timeout 方法，调用 Timeout
// 方法会返回 true，但还有其他一些错误
// 即使未超过截止时间，Timeout 也会返回 true。
//
// 可以通过在成功的 Read 或 Write 调用之后反复延长
// 截止时间来实现空闲超时。
//
// t 的零值表示 I/O 操作不会超时。
func (f *File) SetDeadline(t time.Time) error {
	return f.setDeadline(t)
}

// SetReadDeadline 为未来的 Read 调用以及任何
// 当前阻塞的 Read 调用设置截止时间。
// t 的零值表示 Read 不会超时。
// 并非所有文件都支持设置截止时间；参见 SetDeadline。
func (f *File) SetReadDeadline(t time.Time) error {
	return f.setReadDeadline(t)
}

// SetWriteDeadline 为任何未来的 Write 调用以及任何
// 当前阻塞的 Write 调用设置截止时间。
// 即使 Write 超时，它也可能返回 n > 0，表示
// 部分数据已成功写入。
// t 的零值表示 Write 不会超时。
// 并非所有文件都支持设置截止时间；参见 SetDeadline。
func (f *File) SetWriteDeadline(t time.Time) error {
	return f.setWriteDeadline(t)
}

// SyscallConn 返回一个原始文件。
// 这实现了 syscall.Conn 接口。
func (f *File) SyscallConn() (syscall.RawConn, error) {
	if err := f.checkValid("SyscallConn"); err != nil {
		return nil, err
	}
	return newRawConn(f)
}

// Fd 返回引用已打开文件的系统文件描述符或句柄。
// 如果 f 已关闭，则该描述符变为无效。
// 如果 f 被垃圾回收，终结器可能会关闭该描述符，
// 使其无效；有关终结器何时可能运行的更多信息，
// 请参见 [runtime.SetFinalizer]。
//
// 不要关闭返回的描述符；这可能导致之后
// 关闭 f 时关闭了一个不相关的描述符。
//
// Fd 的行为在某些平台上有所不同：
//
//   - 在 Unix 和 Windows 上，[File.SetDeadline] 方法将停止工作。
//   - 在 Windows 上，如果文件上没有并发的 I/O
//     操作，文件描述符将与 Go 运行时的 I/O 完成端口解除关联。
//
// 在大多数用途中，更推荐使用 f.SyscallConn 方法。
func (f *File) Fd() uintptr {
	return f.fd()
}

// DirFS 返回一个文件系统（fs.FS），其根目录是以 dir 为根的目录树。
//
// 请注意，DirFS("/prefix") 只保证它向操作系统发起的 Open 调用
// 会以 "/prefix" 开头：DirFS("/prefix").Open("file") 等同于
// os.Open("/prefix/file")。因此，如果 /prefix/file 是一个指向 /prefix 树
// 之外的符号链接，那么使用 DirFS 并不会比使用 os.Open
// 更能阻止访问。此外，对于相对路径 DirFS("prefix") 返回的 fs.FS 的根
// 会受到之后调用 Chdir 的影响。因此，当目录树
// 包含任意内容时，DirFS 并不是 chroot 式安全机制的通用替代品。
//
// 使用 [Root.FS] 来获取一个能防止通过符号链接逃逸出树的 fs.FS。
//
// dir 目录不能为 ""。
//
// 结果实现了 [io/fs.StatFS]、[io/fs.ReadFileFS]、[io/fs.ReadDirFS] 和
// [io/fs.ReadLinkFS]。
func DirFS(dir string) fs.FS {
	return dirFS(dir)
}

var _ fs.StatFS = dirFS("")
var _ fs.ReadFileFS = dirFS("")
var _ fs.ReadDirFS = dirFS("")
var _ fs.ReadLinkFS = dirFS("")

type dirFS string

func (dir dirFS) Open(name string) (fs.File, error) {
	fullname, err := dir.join(name)
	if err != nil {
		return nil, &PathError{Op: "open", Path: name, Err: err}
	}
	f, err := Open(fullname)
	if err != nil {
		// DirFS 接受适合 GOOS 的字符串，
		// 而这里的 name 参数始终以斜杠分隔。
		// dir.join 会将两者混合；为了
		// 错误报告，将其还原。
		err.(*PathError).Path = name
		return nil, err
	}
	return f, nil
}

// ReadFile 方法为该目录中具有给定名称的文件调用 [ReadFile]
// 函数。该函数为小文件和特殊文件系统提供了
// 健壮的处理。
// 通过此方法，dirFS 实现了 [io/fs.ReadFileFS]。
func (dir dirFS) ReadFile(name string) ([]byte, error) {
	fullname, err := dir.join(name)
	if err != nil {
		return nil, &PathError{Op: "readfile", Path: name, Err: err}
	}
	b, err := ReadFile(fullname)
	if err != nil {
		if e, ok := err.(*PathError); ok {
			// 参见 dirFS.Open 中的注释。
			e.Path = name
		}
		return nil, err
	}
	return b, nil
}

// ReadDir 读取指定目录，返回其所有目录项，按文件名排序。
// 通过此方法，dirFS 实现了 [io/fs.ReadDirFS]。
func (dir dirFS) ReadDir(name string) ([]DirEntry, error) {
	fullname, err := dir.join(name)
	if err != nil {
		return nil, &PathError{Op: "readdir", Path: name, Err: err}
	}
	entries, err := ReadDir(fullname)
	if err != nil {
		if e, ok := err.(*PathError); ok {
			// 参见 dirFS.Open 中的注释。
			e.Path = name
		}
		return nil, err
	}
	return entries, nil
}

func (dir dirFS) Stat(name string) (fs.FileInfo, error) {
	fullname, err := dir.join(name)
	if err != nil {
		return nil, &PathError{Op: "stat", Path: name, Err: err}
	}
	f, err := Stat(fullname)
	if err != nil {
		// 参见 dirFS.Open 中的注释。
		err.(*PathError).Path = name
		return nil, err
	}
	return f, nil
}

func (dir dirFS) Lstat(name string) (fs.FileInfo, error) {
	fullname, err := dir.join(name)
	if err != nil {
		return nil, &PathError{Op: "lstat", Path: name, Err: err}
	}
	f, err := Lstat(fullname)
	if err != nil {
		// 参见 dirFS.Open 中的注释。
		err.(*PathError).Path = name
		return nil, err
	}
	return f, nil
}

func (dir dirFS) ReadLink(name string) (string, error) {
	fullname, err := dir.join(name)
	if err != nil {
		return "", &PathError{Op: "readlink", Path: name, Err: err}
	}
	return Readlink(fullname)
}

// join 返回 name 在 dir 中的路径。
func (dir dirFS) join(name string) (string, error) {
	if dir == "" {
		return "", errors.New("os: DirFS with empty root")
	}
	name, err := filepathlite.Localize(name)
	if err != nil {
		return "", ErrInvalid
	}
	if IsPathSeparator(dir[len(dir)-1]) {
		return string(dir) + name, nil
	}
	return string(dir) + string(PathSeparator) + name, nil
}

// ReadFile 读取指定文件并返回其内容。
// 成功的调用返回 err == nil，而不是 err == EOF。
// 因为 ReadFile 读取整个文件，所以它不会将 Read 返回的 EOF
// 视为需要报告的错误。
func ReadFile(name string) ([]byte, error) {
	f, err := Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return readFileContents(statOrZero(f), f.Read)
}

func statOrZero(f *File) int64 {
	if fi, err := f.Stat(); err == nil {
		return fi.Size()
	}
	return 0
}

// readFileContents 使用提供的 read 函数
// （在测试中除外的 *os.File.Read）一次或多次读取文件内容，直到遇到错误。
//
// 提供的 size 是文件的 stat 大小，对于像
// /proc 这样不报告大小的文件，它可能为 0。
func readFileContents(statSize int64, read func([]byte) (int, error)) ([]byte, error) {
	zeroSize := statSize == 0

	// 确定初始切片应设为多大。对于大小已知
	// 且能装入内存的文件，使用该大小 + 1。否则，使用一个小缓冲区，
	// 之后我们会扩大它。
	var size int
	if int64(int(statSize)) == statSize {
		size = int(statSize)
	}
	size++ // 为 EOF 时的最后一次读取留一个字节

	const minBuf = 512
	// 如果文件声称大小很小，则至少读取 512 字节。特别是，
	// Linux 的 /proc 中的文件声称大小为 0，但如果以小片段
	// 读取则无法正常工作，因此初始读取 1 个字节不会
	// 正确工作。
	if size < minBuf {
		size = minBuf
	}

	data := make([]byte, 0, size)
	for {
		n, err := read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return data, err
		}

		// 如果我们已用尽容量，或者该文件是像 /proc 那样大小为
		// 零的文件，则扩大缓冲区。根据 Issue 72080，我们总是希望
		// 以非微小的缓冲区大小对零长度文件发起 Read 调用。
		capRemain := cap(data) - len(data)
		if capRemain == 0 || (zeroSize && capRemain < minBuf) {
			data = slices.Grow(data, minBuf)
		}
	}
}

// WriteFile 将数据写入指定文件，必要时创建它。
// 如果文件不存在，WriteFile 会以权限 perm（在 umask 之前）创建它；
// 否则 WriteFile 在写入前先截断它，而不改变权限。
// 由于 WriteFile 需要多次系统调用才能完成，操作中途失败
// 可能会使文件处于部分写入的状态。
func WriteFile(name string, data []byte, perm FileMode) error {
	f, err := OpenFile(name, O_WRONLY|O_CREATE|O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err1 := f.Close(); err1 != nil && err == nil {
		err = err1
	}
	return err
}
