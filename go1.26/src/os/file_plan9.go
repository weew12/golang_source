// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/bytealg"
	"internal/poll"
	"internal/stringslite"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// fixLongPath 在非 Windows 平台上为空操作。
func fixLongPath(path string) string {
	return path
}

// file 是 *File 的真实表示。
// 额外的间接层确保 os 的任何客户端
// 都无法覆盖这些数据，否则可能导致终结器
// 关闭错误的文件描述符。
type file struct {
	fdmu       poll.FDMutex
	sysfd      int
	name       string
	dirinfo    atomic.Pointer[dirInfo] // 除非正在读取目录，否则为 nil
	appendMode bool                    // 文件是否以追加方式打开
}

// fd 是 Fd 的 Plan 9 实现。
func (f *File) fd() uintptr {
	if f == nil {
		return ^(uintptr(0))
	}
	return uintptr(f.sysfd)
}

// newFileFromNewFile 由 [NewFile] 调用。
func newFileFromNewFile(fd uintptr, name string) *File {
	fdi := int(fd)
	if fdi < 0 {
		return nil
	}
	f := &File{&file{sysfd: fdi, name: name}}
	runtime.SetFinalizer(f.file, (*file).close)
	return f
}

// 如果 File 描述的是一个目录，则为辅助信息
type dirInfo struct {
	mu   sync.Mutex
	buf  [syscall.STATMAX]byte // 用于目录 I/O 的缓冲区
	nbuf int                   // buf 的长度；Read 的返回值
	bufp int                   // buf 中下一条记录的位置。
}

func epipecheck(file *File, e error) {
}

// DevNull 是操作系统 “空设备” 的名称。
// 在类 Unix 系统上，它是 "/dev/null"；在 Windows 上，是 "NUL"。
const DevNull = "/dev/null"

// syscallMode 从 Go 的可移植模式位返回特定于 syscall 的模式位。
func syscallMode(i FileMode) (o uint32) {
	o |= uint32(i.Perm())
	if i&ModeAppend != 0 {
		o |= syscall.DMAPPEND
	}
	if i&ModeExclusive != 0 {
		o |= syscall.DMEXCL
	}
	if i&ModeTemporary != 0 {
		o |= syscall.DMTMP
	}
	return
}

// openFileNolog 是 OpenFile 的 Plan 9 实现。
func openFileNolog(name string, flag int, perm FileMode) (*File, error) {
	var (
		fd     int
		e      error
		create bool
		excl   bool
		trunc  bool
		append bool
	)

	if flag&O_CREATE == O_CREATE {
		flag = flag & ^O_CREATE
		create = true
	}
	if flag&O_EXCL == O_EXCL {
		excl = true
	}
	if flag&O_TRUNC == O_TRUNC {
		trunc = true
	}
	// O_APPEND 在 Plan 9 上是模拟实现的
	if flag&O_APPEND == O_APPEND {
		flag = flag &^ O_APPEND
		append = true
	}

	if (create && trunc) || excl {
		fd, e = syscall.Create(name, flag, syscallMode(perm))
	} else {
		fd, e = syscall.Open(name, flag)
		if IsNotExist(e) && create {
			fd, e = syscall.Create(name, flag, syscallMode(perm))
			if e != nil {
				return nil, &PathError{Op: "create", Path: name, Err: e}
			}
		}
	}

	if e != nil {
		return nil, &PathError{Op: "open", Path: name, Err: e}
	}

	if append {
		if _, e = syscall.Seek(fd, 0, io.SeekEnd); e != nil {
			return nil, &PathError{Op: "seek", Path: name, Err: e}
		}
	}

	return NewFile(uintptr(fd), name), nil
}

func openDirNolog(name string) (*File, error) {
	f, e := openFileNolog(name, O_RDONLY, 0)
	if e != nil {
		return nil, e
	}
	d, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	if !d.IsDir() {
		f.Close()
		return nil, &PathError{Op: "open", Path: name, Err: syscall.ENOTDIR}
	}
	return f, nil
}

// Close 关闭 File，使其无法再用于 I/O。
// 在支持 SetDeadline 的文件上，任何挂起的 I/O 操作
// 都将被取消，并立即返回 ErrClosed 错误。
// 如果已经调用过 Close，Close 会返回错误。
func (f *File) Close() error {
	if f == nil {
		return ErrInvalid
	}
	return f.file.close()
}

func (file *file) close() error {
	if !file.fdmu.IncrefAndClose() {
		return &PathError{Op: "close", Path: file.name, Err: ErrClosed}
	}

	// 此时我们应该取消任何挂起的 I/O。
	// 在 Plan 9 上我们该怎么做？

	err := file.decref()

	// 不再需要终结器了
	runtime.SetFinalizer(file, nil)
	return err
}

// destroy 真正关闭该描述符。当没有剩余的引用时，
// 由 decref、readUnlock
// 和 writeUnlock 方法调用它。
func (file *file) destroy() error {
	var err error
	if e := syscall.Close(file.sysfd); e != nil {
		err = &PathError{Op: "close", Path: file.name, Err: e}
	}
	return err
}

// Stat 返回描述该文件的 FileInfo 结构。
// 如果出错，错误的类型为 [*PathError]。
func (f *File) Stat() (FileInfo, error) {
	if f == nil {
		return nil, ErrInvalid
	}
	d, err := dirstat(f)
	if err != nil {
		return nil, err
	}
	return fileInfoFromStat(d), nil
}

// Truncate 更改文件的大小。
// 它不会改变 I/O 偏移量。
// 如果出错，错误的类型为 [*PathError]。
func (f *File) Truncate(size int64) error {
	if f == nil {
		return ErrInvalid
	}

	var d syscall.Dir
	d.Null()
	d.Length = size

	var buf [syscall.STATFIXLEN]byte
	n, err := d.Marshal(buf[:])
	if err != nil {
		return &PathError{Op: "truncate", Path: f.name, Err: err}
	}

	if err := f.incref("truncate"); err != nil {
		return err
	}
	defer f.decref()

	if err = syscall.Fwstat(f.sysfd, buf[:n]); err != nil {
		return &PathError{Op: "truncate", Path: f.name, Err: err}
	}
	return nil
}

const chmodMask = uint32(syscall.DMAPPEND | syscall.DMEXCL | syscall.DMTMP | ModePerm)

func (f *File) chmod(mode FileMode) error {
	if f == nil {
		return ErrInvalid
	}
	var d syscall.Dir

	odir, e := dirstat(f)
	if e != nil {
		return &PathError{Op: "chmod", Path: f.name, Err: e}
	}
	d.Null()
	d.Mode = odir.Mode&^chmodMask | syscallMode(mode)&chmodMask

	var buf [syscall.STATFIXLEN]byte
	n, err := d.Marshal(buf[:])
	if err != nil {
		return &PathError{Op: "chmod", Path: f.name, Err: err}
	}

	if err := f.incref("chmod"); err != nil {
		return err
	}
	defer f.decref()

	if err = syscall.Fwstat(f.sysfd, buf[:n]); err != nil {
		return &PathError{Op: "chmod", Path: f.name, Err: err}
	}
	return nil
}

// Sync 将文件的当前内容提交到稳定存储。
// 通常，这意味着将文件系统对最近写入数据的内存副本
// 刷新到磁盘。
func (f *File) Sync() error {
	if f == nil {
		return ErrInvalid
	}
	var d syscall.Dir
	d.Null()

	var buf [syscall.STATFIXLEN]byte
	n, err := d.Marshal(buf[:])
	if err != nil {
		return &PathError{Op: "sync", Path: f.name, Err: err}
	}

	if err := f.incref("sync"); err != nil {
		return err
	}
	defer f.decref()

	if err = syscall.Fwstat(f.sysfd, buf[:n]); err != nil {
		return &PathError{Op: "sync", Path: f.name, Err: err}
	}
	return nil
}

// read 从 File 中读取最多 len(b) 个字节。
// 它返回读取的字节数和错误（如果有）。
func (f *File) read(b []byte) (n int, err error) {
	if err := f.readLock(); err != nil {
		return 0, err
	}
	defer f.readUnlock()
	n, e := fixCount(syscall.Read(f.sysfd, b))
	if n == 0 && len(b) > 0 && e == nil {
		return 0, io.EOF
	}
	return n, e
}

// pread 从 File 中从字节偏移量 off 开始读取 len(b) 个字节。
// 它返回读取的字节数和错误（如果有）。
// EOF 通过读取计数为零且 err 设置为 nil 来表示。
func (f *File) pread(b []byte, off int64) (n int, err error) {
	if err := f.readLock(); err != nil {
		return 0, err
	}
	defer f.readUnlock()
	n, e := fixCount(syscall.Pread(f.sysfd, b, off))
	if n == 0 && len(b) > 0 && e == nil {
		return 0, io.EOF
	}
	return n, e
}

// write 向 File 写入 len(b) 个字节。
// 它返回写入的字节数和错误（如果有）。
// 由于 Plan 9 保留消息边界，永远不允许
// 零字节写入。
func (f *File) write(b []byte) (n int, err error) {
	if err := f.writeLock(); err != nil {
		return 0, err
	}
	defer f.writeUnlock()
	if len(b) == 0 {
		return 0, nil
	}
	return fixCount(syscall.Write(f.sysfd, b))
}

// pwrite 从字节偏移量 off 开始向 File 写入 len(b) 个字节。
// 它返回写入的字节数和错误（如果有）。
// 由于 Plan 9 保留消息边界，永远不允许
// 零字节写入。
func (f *File) pwrite(b []byte, off int64) (n int, err error) {
	if err := f.writeLock(); err != nil {
		return 0, err
	}
	defer f.writeUnlock()
	if len(b) == 0 {
		return 0, nil
	}
	return fixCount(syscall.Pwrite(f.sysfd, b, off))
}

// seek 将文件上下一次 Read 或 Write 的偏移量设置为 offset，
// 其解释取决于 whence：0 表示相对于文件起始位置，1 表示
// 相对于当前偏移量，2 表示相对于末尾。
// 它返回新的偏移量和错误（如果有）。
func (f *File) seek(offset int64, whence int) (ret int64, err error) {
	if err := f.incref(""); err != nil {
		return 0, err
	}
	defer f.decref()
	// 释放缓存的 dirinfo，这样如果我们再次将此文件作为目录
	// 访问，就会分配一个新的。参见 #35767 和 #37161。
	f.dirinfo.Store(nil)
	return syscall.Seek(f.sysfd, offset, whence)
}

// Truncate 更改指定文件的大小。
// 如果文件是符号链接，它会更改链接目标的大小。
// 如果出错，错误的类型为 [*PathError]。
func Truncate(name string, size int64) error {
	var d syscall.Dir

	d.Null()
	d.Length = size

	var buf [syscall.STATFIXLEN]byte
	n, err := d.Marshal(buf[:])
	if err != nil {
		return &PathError{Op: "truncate", Path: name, Err: err}
	}
	if err = syscall.Wstat(name, buf[:n]); err != nil {
		return &PathError{Op: "truncate", Path: name, Err: err}
	}
	return nil
}

// Remove 删除指定文件或目录。
// 如果出错，错误的类型为 [*PathError]。
func Remove(name string) error {
	if e := syscall.Remove(name); e != nil {
		return &PathError{Op: "remove", Path: name, Err: e}
	}
	return nil
}

func rename(oldname, newname string) error {
	dirname := oldname[:bytealg.LastIndexByteString(oldname, '/')+1]
	if stringslite.HasPrefix(newname, dirname) {
		newname = newname[len(dirname):]
	} else {
		return &LinkError{"rename", oldname, newname, ErrInvalid}
	}

	// 如果去掉 oldname 前缀后 newname 仍包含斜杠，
	// 则该重命名是跨目录的，必须被拒绝。
	if bytealg.LastIndexByteString(newname, '/') >= 0 {
		return &LinkError{"rename", oldname, newname, ErrInvalid}
	}

	var d syscall.Dir

	d.Null()
	d.Name = newname

	buf := make([]byte, syscall.STATFIXLEN+len(d.Name))
	n, err := d.Marshal(buf[:])
	if err != nil {
		return &LinkError{"rename", oldname, newname, err}
	}

	// 如果 newname 已存在且不是目录，rename 会替换它。
	f, err := Stat(dirname + newname)
	if err == nil && !f.IsDir() {
		Remove(dirname + newname)
	}

	if err = syscall.Wstat(oldname, buf[:n]); err != nil {
		return &LinkError{"rename", oldname, newname, err}
	}
	return nil
}

// 参见 file.go:Chmod 中的文档。
func chmod(name string, mode FileMode) error {
	var d syscall.Dir

	odir, e := dirstat(name)
	if e != nil {
		return &PathError{Op: "chmod", Path: name, Err: e}
	}
	d.Null()
	d.Mode = odir.Mode&^chmodMask | syscallMode(mode)&chmodMask

	var buf [syscall.STATFIXLEN]byte
	n, err := d.Marshal(buf[:])
	if err != nil {
		return &PathError{Op: "chmod", Path: name, Err: err}
	}
	if err = syscall.Wstat(name, buf[:n]); err != nil {
		return &PathError{Op: "chmod", Path: name, Err: err}
	}
	return nil
}

// Chtimes 更改指定文件的访问时间和修改时间，
// 类似于 Unix 的 utime() 或 utimes() 函数。
// 零值 time.Time 将使对应的文件时间保持不变。
//
// 底层文件系统可能会将值截断或舍入为
// 精度更低的时间单位。
// 如果出错，错误的类型为 [*PathError]。
func Chtimes(name string, atime time.Time, mtime time.Time) error {
	var d syscall.Dir

	d.Null()
	d.Atime = uint32(atime.Unix())
	d.Mtime = uint32(mtime.Unix())
	if atime.IsZero() {
		d.Atime = 0xFFFFFFFF
	}
	if mtime.IsZero() {
		d.Mtime = 0xFFFFFFFF
	}

	var buf [syscall.STATFIXLEN]byte
	n, err := d.Marshal(buf[:])
	if err != nil {
		return &PathError{Op: "chtimes", Path: name, Err: err}
	}
	if err = syscall.Wstat(name, buf[:n]); err != nil {
		return &PathError{Op: "chtimes", Path: name, Err: err}
	}
	return nil
}

// Pipe 返回一对相连的 File；从 r 读取会返回
// 写入 w 的字节。它返回这两个文件和错误（如果有）。
func Pipe() (r *File, w *File, err error) {
	var p [2]int

	if e := syscall.Pipe(p[0:]); e != nil {
		return nil, nil, NewSyscallError("pipe", e)
	}

	return NewFile(uintptr(p[0]), "|0"), NewFile(uintptr(p[1]), "|1"), nil
}

// Plan 9 上不支持

// Link 将 newname 创建为指向 oldname 文件的硬链接。
// 如果出错，错误的类型为 *LinkError。
func Link(oldname, newname string) error {
	return &LinkError{"link", oldname, newname, syscall.EPLAN9}
}

// Symlink 将 newname 创建为指向 oldname 的符号链接。
// 在 Windows 上，指向不存在的 oldname 的符号链接会创建文件符号链接；
// 如果 oldname 之后作为目录被创建，该符号链接将无法工作。
// 如果出错，错误的类型为 *LinkError。
func Symlink(oldname, newname string) error {
	return &LinkError{"symlink", oldname, newname, syscall.EPLAN9}
}

func readlink(name string) (string, error) {
	return "", &PathError{Op: "readlink", Path: name, Err: syscall.EPLAN9}
}

// Chown 更改指定文件的数字 uid 和 gid。
// 如果文件是符号链接，它会更改链接目标的 uid 和 gid。
// uid 或 gid 为 -1 表示不更改该值。
// 如果出错，错误的类型为 [*PathError]。
//
// 在 Windows 或 Plan 9 上，Chown 总是返回 [syscall.EWINDOWS] 或
// [syscall.EPLAN9] 错误，并包装在 [*PathError] 中。
func Chown(name string, uid, gid int) error {
	return &PathError{Op: "chown", Path: name, Err: syscall.EPLAN9}
}

// Lchown 更改指定文件的数字 uid 和 gid。
// 如果文件是符号链接，它会更改链接本身的 uid 和 gid。
// 如果出错，错误的类型为 [*PathError]。
func Lchown(name string, uid, gid int) error {
	return &PathError{Op: "lchown", Path: name, Err: syscall.EPLAN9}
}

// Chown 更改指定文件的数字 uid 和 gid。
// 如果出错，错误的类型为 [*PathError]。
func (f *File) Chown(uid, gid int) error {
	if f == nil {
		return ErrInvalid
	}
	return &PathError{Op: "chown", Path: f.name, Err: syscall.EPLAN9}
}

func tempDir() string {
	dir := Getenv("TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return dir
}

// Chdir 将当前工作目录更改为该文件，
// 该文件必须是目录。
// 如果出错，错误的类型为 [*PathError]。
func (f *File) Chdir() error {
	if err := f.incref("chdir"); err != nil {
		return err
	}
	defer f.decref()
	if e := syscall.Fchdir(f.sysfd); e != nil {
		return &PathError{Op: "chdir", Path: f.name, Err: e}
	}
	return nil
}

// setDeadline 设置读取和写入截止时间。
func (f *File) setDeadline(time.Time) error {
	if err := f.checkValid("SetDeadline"); err != nil {
		return err
	}
	return poll.ErrNoDeadline
}

// setReadDeadline 设置读取截止时间。
func (f *File) setReadDeadline(time.Time) error {
	if err := f.checkValid("SetReadDeadline"); err != nil {
		return err
	}
	return poll.ErrNoDeadline
}

// setWriteDeadline 设置写入截止时间。
func (f *File) setWriteDeadline(time.Time) error {
	if err := f.checkValid("SetWriteDeadline"); err != nil {
		return err
	}
	return poll.ErrNoDeadline
}

// checkValid 检查 f 是否可用于使用，但并不准备
// 实际使用它。如果 f 未就绪，checkValid 会返回合适的
// 错误，可能包含操作名 op。
func (f *File) checkValid(op string) error {
	if f == nil {
		return ErrInvalid
	}
	if err := f.incref(op); err != nil {
		return err
	}
	return f.decref()
}

type rawConn struct{}

func (c *rawConn) Control(f func(uintptr)) error {
	return syscall.EPLAN9
}

func (c *rawConn) Read(f func(uintptr) bool) error {
	return syscall.EPLAN9
}

func (c *rawConn) Write(f func(uintptr) bool) error {
	return syscall.EPLAN9
}

func newRawConn(file *File) (*rawConn, error) {
	return nil, syscall.EPLAN9
}

func ignoringEINTR(fn func() error) error {
	return fn()
}

func ignoringEINTR2[T any](fn func() (T, error)) (T, error) {
	return fn()
}
