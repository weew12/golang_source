// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || wasip1

package os

import (
	"errors"
	"internal/syscall/unix"
	"runtime"
	"syscall"
	"time"
)

// sysfdType 是文件句柄的原生类型
// （在 Unix 上为 int，在 Windows 上为 syscall.Handle），
// 以便编写可移植的辅助函数。
type sysfdType = int

// openRootNolog 即 OpenRoot。
func openRootNolog(name string) (*Root, error) {
	var fd int
	err := ignoringEINTR(func() error {
		var err error
		fd, _, err = open(name, syscall.O_CLOEXEC, 0)
		return err
	})
	if err != nil {
		return nil, &PathError{Op: "open", Path: name, Err: err}
	}
	return newRoot(fd, name)
}

// newRoot 返回一个新的 Root。
// 如果 fd 不是目录，它会关闭 fd 并返回错误。
func newRoot(fd int, name string) (*Root, error) {
	var fs fileStat
	err := ignoringEINTR(func() error {
		return syscall.Fstat(fd, &fs.sys)
	})
	fillFileStatFromSys(&fs, name)
	if err == nil && !fs.IsDir() {
		syscall.Close(fd)
		return nil, &PathError{Op: "open", Path: name, Err: errors.New("not a directory")}
	}

	// 这里与 fork/exec 存在竞态，我们
	// 愿意接受它。参见 ../syscall/exec_unix.go。
	if !supportsCloseOnExec {
		syscall.CloseOnExec(fd)
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
	fd, err := doInRoot(r, name, nil, func(parent int, name string) (fd int, err error) {
		ignoringEINTR(func() error {
			fd, err = unix.Openat(parent, name, syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
			if isNoFollowErr(err) {
				err = checkSymlink(parent, name, err)
			}
			return err
		})
		return fd, err
	})
	if err != nil {
		return nil, &PathError{Op: "openat", Path: name, Err: err}
	}
	return newRoot(fd, joinPath(r.Name(), name))
}

// rootOpenFileNolog 即 Root.OpenFile。
func rootOpenFileNolog(root *Root, name string, flag int, perm FileMode) (*File, error) {
	fd, err := doInRoot(root, name, nil, func(parent int, name string) (fd int, err error) {
		ignoringEINTR(func() error {
			fd, err = unix.Openat(parent, name, syscall.O_NOFOLLOW|syscall.O_CLOEXEC|flag, uint32(perm))
			if err != nil {
				// 在使用 O_CREATE|O_EXCL 时绝不跟随符号链接，无论
				// 操作系统返回什么错误。
				isCreateExcl := flag&(O_CREATE|O_EXCL) == (O_CREATE | O_EXCL)
				if !isCreateExcl && (isNoFollowErr(err) || err == syscall.ENOTDIR) {
					err = checkSymlink(parent, name, err)
				}
				// 对于悬空符号链接，AIX 返回 ELOOP 而不是 EEXIST。
				// 将其转换为 EEXIST，以匹配 ErrExists。
				if isCreateExcl && err == syscall.ELOOP {
					err = syscall.EEXIST
				}
			}
			return err
		})
		return fd, err
	})
	if err != nil {
		return nil, &PathError{Op: "openat", Path: name, Err: err}
	}
	f := newFile(fd, joinPath(root.Name(), name), kindOpenFile, unix.HasNonblockFlag(flag))
	f.inRoot = true
	return f, nil
}

func rootOpenDir(parent int, name string) (int, error) {
	var (
		fd  int
		err error
	)
	ignoringEINTR(func() error {
		fd, err = unix.Openat(parent, name, syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_DIRECTORY, 0)
		if isNoFollowErr(err) || err == syscall.ENOTDIR {
			err = checkSymlink(parent, name, err)
		} else if err == syscall.ENOTSUP || err == syscall.EOPNOTSUPP {
			// ENOTSUP 和 EOPNOTSUPP 通常（但并非总是）是相同的 errno。
			// 将两者都转换为 ENOTDIR，因为这表明某个非末尾
			// 路径组件不是目录。
			err = syscall.ENOTDIR
		}
		return err
	})
	return fd, err
}

func rootStat(r *Root, name string, lstat bool) (FileInfo, error) {
	fi, err := doInRoot(r, name, nil, func(parent sysfdType, n string) (FileInfo, error) {
		var fs fileStat
		if err := unix.Fstatat(parent, n, &fs.sys, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, err
		}
		fillFileStatFromSys(&fs, name)
		if !lstat && fs.Mode()&ModeSymlink != 0 {
			return nil, checkSymlink(parent, n, syscall.ELOOP)
		}
		return &fs, nil
	})
	if err != nil {
		return nil, &PathError{Op: "statat", Path: name, Err: err}
	}
	return fi, nil
}

func rootSymlink(r *Root, oldname, newname string) error {
	_, err := doInRoot(r, newname, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, symlinkat(oldname, parent, name)
	})
	if err != nil {
		return &LinkError{"symlinkat", oldname, newname, err}
	}
	return nil
}

// 在使用 fchmodat、fchownat 等的系统上，我们存在竞态条件：
// 当 "name" 是符号链接时，Root.Chmod("name") 应作用于该链接的目标。
// 然而，fchmodat 不允许我们仅在文件不是符号链接时才对其进行 chmod；
// AT_SYMLINK_NOFOLLOW 参数会使命名操作作用于符号链接本身。
//
// 我们尽力而为：首先检查操作目标是否为符号链接，
// 只有当它不是符号链接时才尝试 fchmodat。如果目标在检查
// 与 fchmodat 之间被替换，我们将对符号链接进行 chmod 而不是跟随它。
//
// 这个竞态条件令人遗憾，但不会导致逃逸出根：
// 我们可能作用于错误的文件，但该文件将包含在根内。
func afterResolvingSymlink(parent int, name string, f func() error) error {
	if err := checkSymlink(parent, name, nil); err != nil {
		return err
	}
	return f()
}

func chmodat(parent int, name string, mode FileMode) error {
	return afterResolvingSymlink(parent, name, func() error {
		return ignoringEINTR(func() error {
			return unix.Fchmodat(parent, name, syscallMode(mode), unix.AT_SYMLINK_NOFOLLOW)
		})
	})
}

func chownat(parent int, name string, uid, gid int) error {
	return afterResolvingSymlink(parent, name, func() error {
		return ignoringEINTR(func() error {
			return unix.Fchownat(parent, name, uid, gid, unix.AT_SYMLINK_NOFOLLOW)
		})
	})
}

func lchownat(parent int, name string, uid, gid int) error {
	return ignoringEINTR(func() error {
		return unix.Fchownat(parent, name, uid, gid, unix.AT_SYMLINK_NOFOLLOW)
	})
}

func chtimesat(parent int, name string, atime time.Time, mtime time.Time) error {
	return afterResolvingSymlink(parent, name, func() error {
		return ignoringEINTR(func() error {
			utimes := chtimesUtimes(atime, mtime)
			return unix.Utimensat(parent, name, &utimes, unix.AT_SYMLINK_NOFOLLOW)
		})
	})
}

func mkdirat(fd int, name string, perm FileMode) error {
	return ignoringEINTR(func() error {
		return unix.Mkdirat(fd, name, syscallMode(perm))
	})
}

func removeat(fd int, name string) error {
	// 系统调用接口迫使我们知道
	// 我们删除的是文件还是目录。两者都尝试一下。
	e := ignoringEINTR(func() error {
		return unix.Unlinkat(fd, name, 0)
	})
	if e == nil {
		return nil
	}
	e1 := ignoringEINTR(func() error {
		return unix.Unlinkat(fd, name, unix.AT_REMOVEDIR)
	})
	if e1 == nil {
		return nil
	}
	// 两者都失败了。关于我们如何决定返回哪个错误，参见 Remove 中的注释。
	if e1 != syscall.ENOTDIR {
		return e1
	}
	return e
}

func removefileat(fd int, name string) error {
	return ignoringEINTR(func() error {
		return unix.Unlinkat(fd, name, 0)
	})
}

func removedirat(fd int, name string) error {
	return ignoringEINTR(func() error {
		return unix.Unlinkat(fd, name, unix.AT_REMOVEDIR)
	})
}

func renameat(oldfd int, oldname string, newfd int, newname string) error {
	return unix.Renameat(oldfd, oldname, newfd, newname)
}

func linkat(oldfd int, oldname string, newfd int, newname string) error {
	return unix.Linkat(oldfd, oldname, newfd, newname, 0)
}

func symlinkat(oldname string, newfd int, newname string) error {
	return unix.Symlinkat(oldname, newfd, newname)
}

func modeAt(parent int, name string) (FileMode, error) {
	var fs fileStat
	if err := unix.Fstatat(parent, name, &fs.sys, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, err
	}
	fillFileStatFromSys(&fs, name)
	return fs.mode, nil
}

// checkSymlink 解析 parent 中的符号链接 name，
// 并返回带有链接内容的 errSymlink。
//
// 如果 name 不是符号链接，则返回 origError。
func checkSymlink(parent int, name string, origError error) error {
	link, err := readlinkat(parent, name)
	if err != nil {
		return origError
	}
	return errSymlink(link)
}

func readlinkat(fd int, name string) (string, error) {
	for len := 128; ; len *= 2 {
		b := make([]byte, len)
		var (
			n int
			e error
		)
		ignoringEINTR(func() error {
			n, e = unix.Readlinkat(fd, name, b)
			return e
		})
		if e == syscall.ERANGE {
			continue
		}
		if e != nil {
			return "", e
		}
		n = max(n, 0)
		if n < len {
			return string(b[0:n]), nil
		}
	}
}
