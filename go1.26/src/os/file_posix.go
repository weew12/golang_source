// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || (js && wasm) || wasip1 || windows

package os

import (
	"runtime"
	"syscall"
	"time"
)

// Close 关闭 [File]，使其无法再用于 I/O。
// 在支持 [File.SetDeadline] 的文件上，任何挂起的 I/O 操作
// 都将被取消，并立即返回 [ErrClosed] 错误。
// 如果已经调用过 Close，Close 会返回错误。
func (f *File) Close() error {
	if f == nil {
		return ErrInvalid
	}
	return f.file.close()
}

// read 从 File 中读取最多 len(b) 个字节。
// 它返回读取的字节数和错误（如果有）。
func (f *File) read(b []byte) (n int, err error) {
	n, err = f.pfd.Read(b)
	runtime.KeepAlive(f)
	return n, err
}

// pread 从 File 中从字节偏移量 off 开始读取 len(b) 个字节。
// 它返回读取的字节数和错误（如果有）。
// EOF 通过读取计数为零且 err 设置为 nil 来表示。
func (f *File) pread(b []byte, off int64) (n int, err error) {
	n, err = f.pfd.Pread(b, off)
	runtime.KeepAlive(f)
	return n, err
}

// write 向 File 写入 len(b) 个字节。
// 它返回写入的字节数和错误（如果有）。
func (f *File) write(b []byte) (n int, err error) {
	n, err = f.pfd.Write(b)
	runtime.KeepAlive(f)
	return n, err
}

// pwrite 从字节偏移量 off 开始向 File 写入 len(b) 个字节。
// 它返回写入的字节数和错误（如果有）。
func (f *File) pwrite(b []byte, off int64) (n int, err error) {
	n, err = f.pfd.Pwrite(b, off)
	runtime.KeepAlive(f)
	return n, err
}

// syscallMode 从 Go 的可移植模式位返回特定于 syscall 的模式位。
func syscallMode(i FileMode) (o uint32) {
	o |= uint32(i.Perm())
	if i&ModeSetuid != 0 {
		o |= syscall.S_ISUID
	}
	if i&ModeSetgid != 0 {
		o |= syscall.S_ISGID
	}
	if i&ModeSticky != 0 {
		o |= syscall.S_ISVTX
	}
	// Go 的 ModeTemporary 没有映射（仅 Plan 9）。
	return
}

// 参见 file.go:Chmod 中的文档。
func chmod(name string, mode FileMode) error {
	longName := fixLongPath(name)
	e := ignoringEINTR(func() error {
		return syscall.Chmod(longName, syscallMode(mode))
	})
	if e != nil {
		return &PathError{Op: "chmod", Path: name, Err: e}
	}
	return nil
}

// 参见 file.go:(*File).Chmod 中的文档。
func (f *File) chmod(mode FileMode) error {
	if err := f.checkValid("chmod"); err != nil {
		return err
	}
	if e := f.pfd.Fchmod(syscallMode(mode)); e != nil {
		return f.wrapErr("chmod", e)
	}
	return nil
}

// Chown 更改指定文件的数字 uid 和 gid。
// 如果文件是符号链接，它会更改链接目标的 uid 和 gid。
// uid 或 gid 为 -1 表示不更改该值。
// 如果出错，错误的类型为 [*PathError]。
//
// 在 Windows 或 Plan 9 上，Chown 总是返回 [syscall.EWINDOWS] 或
// [syscall.EPLAN9] 错误，并包装在 [*PathError] 中。
func Chown(name string, uid, gid int) error {
	e := ignoringEINTR(func() error {
		return syscall.Chown(name, uid, gid)
	})
	if e != nil {
		return &PathError{Op: "chown", Path: name, Err: e}
	}
	return nil
}

// Lchown 更改指定文件的数字 uid 和 gid。
// 如果文件是符号链接，它会更改链接本身的 uid 和 gid。
// 如果出错，错误的类型为 [*PathError]。
//
// 在 Windows 上，它总是返回 [syscall.EWINDOWS] 错误，
// 并包装在 [*PathError] 中。
func Lchown(name string, uid, gid int) error {
	e := ignoringEINTR(func() error {
		return syscall.Lchown(name, uid, gid)
	})
	if e != nil {
		return &PathError{Op: "lchown", Path: name, Err: e}
	}
	return nil
}

// Chown 更改指定文件的数字 uid 和 gid。
// 如果出错，错误的类型为 [*PathError]。
//
// 在 Windows 上，它总是返回 [syscall.EWINDOWS] 错误，
// 并包装在 [*PathError] 中。
func (f *File) Chown(uid, gid int) error {
	if err := f.checkValid("chown"); err != nil {
		return err
	}
	if e := f.pfd.Fchown(uid, gid); e != nil {
		return f.wrapErr("chown", e)
	}
	return nil
}

// Truncate 更改文件的大小。
// 它不会改变 I/O 偏移量。
// 如果出错，错误的类型为 [*PathError]。
func (f *File) Truncate(size int64) error {
	if err := f.checkValid("truncate"); err != nil {
		return err
	}
	if e := f.pfd.Ftruncate(size); e != nil {
		return f.wrapErr("truncate", e)
	}
	return nil
}

// Sync 将文件的当前内容提交到稳定存储。
// 通常，这意味着将文件系统对最近写入数据的内存副本
// 刷新到磁盘。
func (f *File) Sync() error {
	if err := f.checkValid("sync"); err != nil {
		return err
	}
	if e := f.pfd.Fsync(); e != nil {
		return f.wrapErr("sync", e)
	}
	return nil
}

// Chtimes 更改指定文件的访问时间和修改时间，
// 类似于 Unix 的 utime() 或 utimes() 函数。
// 零值 [time.Time] 将使对应的文件时间保持不变。
//
// 底层文件系统可能会将值截断或舍入为
// 精度更低的时间单位。
// 如果出错，错误的类型为 [*PathError]。
func Chtimes(name string, atime time.Time, mtime time.Time) error {
	utimes := chtimesUtimes(atime, mtime)
	if e := syscall.UtimesNano(fixLongPath(name), utimes[0:]); e != nil {
		return &PathError{Op: "chtimes", Path: name, Err: e}
	}
	return nil
}

func chtimesUtimes(atime, mtime time.Time) [2]syscall.Timespec {
	var utimes [2]syscall.Timespec
	set := func(i int, t time.Time) {
		if t.IsZero() {
			utimes[i] = syscall.Timespec{Sec: _UTIME_OMIT, Nsec: _UTIME_OMIT}
		} else {
			utimes[i] = syscall.NsecToTimespec(t.UnixNano())
		}
	}
	set(0, atime)
	set(1, mtime)
	return utimes
}

// Chdir 将当前工作目录更改为该文件，
// 该文件必须是目录。
// 如果出错，错误的类型为 [*PathError]。
func (f *File) Chdir() error {
	if err := f.checkValid("chdir"); err != nil {
		return err
	}
	if e := f.pfd.Fchdir(); e != nil {
		return f.wrapErr("chdir", e)
	}
	return nil
}

// setDeadline 设置读取和写入截止时间。
func (f *File) setDeadline(t time.Time) error {
	if err := f.checkValid("SetDeadline"); err != nil {
		return err
	}
	return f.pfd.SetDeadline(t)
}

// setReadDeadline 设置读取截止时间。
func (f *File) setReadDeadline(t time.Time) error {
	if err := f.checkValid("SetReadDeadline"); err != nil {
		return err
	}
	return f.pfd.SetReadDeadline(t)
}

// setWriteDeadline 设置写入截止时间。
func (f *File) setWriteDeadline(t time.Time) error {
	if err := f.checkValid("SetWriteDeadline"); err != nil {
		return err
	}
	return f.pfd.SetWriteDeadline(t)
}

// checkValid 检查 f 是否可用于使用。
// 如果不可用，则返回合适的错误，可能包含操作名 op。
func (f *File) checkValid(op string) error {
	if f == nil {
		return ErrInvalid
	}
	return nil
}

// ignoringEINTR 进行函数调用，并在返回
// EINTR 错误时重复调用。尽管我们用 SA_RESTART 安装了所有
// 信号处理器，但似乎仍需要这样做：参见 #22838、#38033、#38836、#40846。
// 此外 #20400 和 #36644 是未设置 SA_RESTART 就
// 安装信号处理器的问题。这些都不是常见情况，
// 但数量足够多，以致我们似乎无法避免
// 一个 EINTR 循环。
func ignoringEINTR(fn func() error) error {
	for {
		err := fn()
		if err != syscall.EINTR {
			return err
		}
	}
}

// ignoringEINTR2 与 ignoringEINTR 相同，但返回一个额外的值。
func ignoringEINTR2[T any](fn func() (T, error)) (T, error) {
	for {
		v, err := fn()
		if err != syscall.EINTR {
			return v, err
		}
	}
}
