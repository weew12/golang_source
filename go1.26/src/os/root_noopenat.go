// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (js && wasm) || plan9

package os

import (
	"errors"
	"internal/filepathlite"
	"internal/stringslite"
	"sync/atomic"
	"syscall"
	"time"
)

// 针对没有 openat 的平台的 root 实现。
// 目前为 plan9 和 js。
type root struct {
	name   string
	closed atomic.Bool
}

// openRootNolog 即 OpenRoot。
func openRootNolog(name string) (*Root, error) {
	r, err := newRoot(name)
	if err != nil {
		return nil, &PathError{Op: "open", Path: name, Err: err}
	}
	return r, nil
}

// openRootInRoot 即 Root.OpenRoot。
func openRootInRoot(r *Root, name string) (*Root, error) {
	if err := checkPathEscapes(r, name); err != nil {
		return nil, &PathError{Op: "openat", Path: name, Err: err}
	}
	r, err := newRoot(joinPath(r.root.name, name))
	if err != nil {
		return nil, &PathError{Op: "openat", Path: name, Err: err}
	}
	return r, nil
}

// newRoot 返回一个新的 Root。
// 如果 fd 不是目录，它会关闭 fd 并返回错误。
func newRoot(name string) (*Root, error) {
	fi, err := Stat(name)
	if err != nil {
		return nil, err.(*PathError).Err
	}
	if !fi.IsDir() {
		return nil, errors.New("not a directory")
	}
	return &Root{&root{name: name}}, nil
}

func (r *root) Close() error {
	// 为了与那些 Root.Close 会关闭句柄的平台保持一致，
	// 将该 Root 标记为已关闭，并让后续调用返回错误。
	r.closed.Store(true)
	return nil
}

func (r *root) Name() string {
	return r.name
}

// rootOpenFileNolog 即 Root.OpenFile。
func rootOpenFileNolog(r *Root, name string, flag int, perm FileMode) (*File, error) {
	if err := checkPathEscapes(r, name); err != nil {
		return nil, &PathError{Op: "openat", Path: name, Err: err}
	}
	f, err := openFileNolog(joinPath(r.root.name, name), flag, perm)
	if err != nil {
		return nil, &PathError{Op: "openat", Path: name, Err: underlyingError(err)}
	}
	return f, nil
}

func rootStat(r *Root, name string, lstat bool) (FileInfo, error) {
	var fi FileInfo
	var err error
	if lstat {
		err = checkPathEscapesLstat(r, name)
		if err == nil {
			fi, err = Lstat(joinPath(r.root.name, name))
		}
	} else {
		err = checkPathEscapes(r, name)
		if err == nil {
			fi, err = Stat(joinPath(r.root.name, name))
		}
	}
	if err != nil {
		return nil, &PathError{Op: "statat", Path: name, Err: underlyingError(err)}
	}
	return fi, nil
}

func rootChmod(r *Root, name string, mode FileMode) error {
	if err := checkPathEscapes(r, name); err != nil {
		return &PathError{Op: "chmodat", Path: name, Err: err}
	}
	if err := Chmod(joinPath(r.root.name, name), mode); err != nil {
		return &PathError{Op: "chmodat", Path: name, Err: underlyingError(err)}
	}
	return nil
}

func rootChown(r *Root, name string, uid, gid int) error {
	if err := checkPathEscapes(r, name); err != nil {
		return &PathError{Op: "chownat", Path: name, Err: err}
	}
	if err := Chown(joinPath(r.root.name, name), uid, gid); err != nil {
		return &PathError{Op: "chownat", Path: name, Err: underlyingError(err)}
	}
	return nil
}

func rootLchown(r *Root, name string, uid, gid int) error {
	if err := checkPathEscapesLstat(r, name); err != nil {
		return &PathError{Op: "lchownat", Path: name, Err: err}
	}
	if err := Lchown(joinPath(r.root.name, name), uid, gid); err != nil {
		return &PathError{Op: "lchownat", Path: name, Err: underlyingError(err)}
	}
	return nil
}

func rootChtimes(r *Root, name string, atime time.Time, mtime time.Time) error {
	if err := checkPathEscapes(r, name); err != nil {
		return &PathError{Op: "chtimesat", Path: name, Err: err}
	}
	if err := Chtimes(joinPath(r.root.name, name), atime, mtime); err != nil {
		return &PathError{Op: "chtimesat", Path: name, Err: underlyingError(err)}
	}
	return nil
}

func rootMkdir(r *Root, name string, perm FileMode) error {
	if err := checkPathEscapes(r, name); err != nil {
		return &PathError{Op: "mkdirat", Path: name, Err: err}
	}
	if err := Mkdir(joinPath(r.root.name, name), perm); err != nil {
		return &PathError{Op: "mkdirat", Path: name, Err: underlyingError(err)}
	}
	return nil
}

func rootMkdirAll(r *Root, name string, perm FileMode) error {
	// 这里我们只检查 errPathEscapes。
	// 对于诸如 ENOTDIR 之类的错误（路径上某处出现了非目录文件），
	// 我们让 MkdirAll 生成错误。
	// MkdirAll 会返回一个引用错误确切位置的 PathError，
	// 我们希望保留这一特性。
	if err := checkPathEscapes(r, name); err == errPathEscapes {
		return &PathError{Op: "mkdirat", Path: name, Err: err}
	}
	prefix := r.root.name + string(PathSeparator)
	if err := MkdirAll(prefix+name, perm); err != nil {
		if pe, ok := err.(*PathError); ok {
			pe.Op = "mkdirat"
			pe.Path = stringslite.TrimPrefix(pe.Path, prefix)
			return pe
		}
		return &PathError{Op: "mkdirat", Path: name, Err: underlyingError(err)}
	}
	return nil
}

func rootRemove(r *Root, name string) error {
	if err := checkPathEscapesLstat(r, name); err != nil {
		return &PathError{Op: "removeat", Path: name, Err: err}
	}
	if endsWithDot(name) {
		// 我们不想允许删除根本身，因此对此进行检查。
		if filepathlite.Clean(name) == "." {
			return &PathError{Op: "removeat", Path: name, Err: errPathEscapes}
		}
	}
	if err := Remove(joinPath(r.root.name, name)); err != nil {
		return &PathError{Op: "removeat", Path: name, Err: underlyingError(err)}
	}
	return nil
}

func rootRemoveAll(r *Root, name string) error {
	if endsWithDot(name) {
		// 与 os.RemoveAll 保持一致：尝试删除 . 时返回 EINVAL
		return &PathError{Op: "RemoveAll", Path: name, Err: syscall.EINVAL}
	}
	if err := checkPathEscapesLstat(r, name); err != nil {
		if err == syscall.ENOTDIR {
			// 某个中间路径组件不是目录。
			// RemoveAll 将此视为成功（因为目标不存在）。
			return nil
		}
		return &PathError{Op: "RemoveAll", Path: name, Err: err}
	}
	if err := RemoveAll(joinPath(r.root.name, name)); err != nil {
		return &PathError{Op: "RemoveAll", Path: name, Err: underlyingError(err)}
	}
	return nil
}

func rootReadlink(r *Root, name string) (string, error) {
	if err := checkPathEscapesLstat(r, name); err != nil {
		return "", &PathError{Op: "readlinkat", Path: name, Err: err}
	}
	name, err := Readlink(joinPath(r.root.name, name))
	if err != nil {
		return "", &PathError{Op: "readlinkat", Path: name, Err: underlyingError(err)}
	}
	return name, nil
}

func rootRename(r *Root, oldname, newname string) error {
	if err := checkPathEscapesLstat(r, oldname); err != nil {
		return &PathError{Op: "renameat", Path: oldname, Err: err}
	}
	if err := checkPathEscapesLstat(r, newname); err != nil {
		return &PathError{Op: "renameat", Path: newname, Err: err}
	}
	err := Rename(joinPath(r.root.name, oldname), joinPath(r.root.name, newname))
	if err != nil {
		return &LinkError{"renameat", oldname, newname, underlyingError(err)}
	}
	return nil
}

func rootLink(r *Root, oldname, newname string) error {
	if err := checkPathEscapesLstat(r, oldname); err != nil {
		return &PathError{Op: "linkat", Path: oldname, Err: err}
	}
	fullOldName := joinPath(r.root.name, oldname)
	if fs, err := Lstat(fullOldName); err == nil && fs.Mode()&ModeSymlink != 0 {
		return &PathError{Op: "linkat", Path: oldname, Err: errors.New("cannot create a hard link to a symlink")}
	}
	if err := checkPathEscapesLstat(r, newname); err != nil {
		return &PathError{Op: "linkat", Path: newname, Err: err}
	}
	err := Link(fullOldName, joinPath(r.root.name, newname))
	if err != nil {
		return &LinkError{"linkat", oldname, newname, underlyingError(err)}
	}
	return nil
}

func rootSymlink(r *Root, oldname, newname string) error {
	if err := checkPathEscapesLstat(r, newname); err != nil {
		return &PathError{Op: "symlinkat", Path: newname, Err: err}
	}
	err := Symlink(oldname, joinPath(r.root.name, newname))
	if err != nil {
		return &LinkError{"symlinkat", oldname, newname, underlyingError(err)}
	}
	return nil
}
