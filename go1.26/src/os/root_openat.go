// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || windows || wasip1

package os

import (
	"runtime"
	"slices"
	"sync"
	"syscall"
	"time"
)

// 针对具有相对于目录打开文件的函数的平台
// 的 root 实现。
type root struct {
	name string

	// 当有操作正在使用 fd 时，refs 会递增。
	// 调用 Close 时会设置 closed。
	// 当 closed 为 true 且 refs 为 0 时，fd 会被关闭。
	mu     sync.Mutex
	fd     sysfdType
	refs   int  // 活跃操作的数量
	closed bool // 关闭时设置
}

func (r *root) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed && r.refs == 0 {
		syscall.Close(r.fd)
	}
	r.closed = true
	runtime.SetFinalizer(r, nil) // 不再需要 finalizer
	return nil
}

func (r *root) incref() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	r.refs++
	return nil
}

func (r *root) decref() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refs <= 0 {
		panic("bad Root refcount")
	}
	r.refs--
	if r.closed && r.refs == 0 {
		syscall.Close(r.fd)
	}
}

func (r *root) Name() string {
	return r.name
}

func rootChmod(r *Root, name string, mode FileMode) error {
	_, err := doInRoot(r, name, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, chmodat(parent, name, mode)
	})
	if err != nil {
		return &PathError{Op: "chmodat", Path: name, Err: err}
	}
	return nil
}

func rootChown(r *Root, name string, uid, gid int) error {
	_, err := doInRoot(r, name, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, chownat(parent, name, uid, gid)
	})
	if err != nil {
		return &PathError{Op: "chownat", Path: name, Err: err}
	}
	return nil
}

func rootLchown(r *Root, name string, uid, gid int) error {
	_, err := doInRoot(r, name, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, lchownat(parent, name, uid, gid)
	})
	if err != nil {
		return &PathError{Op: "lchownat", Path: name, Err: err}
	}
	return err
}

func rootChtimes(r *Root, name string, atime time.Time, mtime time.Time) error {
	_, err := doInRoot(r, name, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, chtimesat(parent, name, atime, mtime)
	})
	if err != nil {
		return &PathError{Op: "chtimesat", Path: name, Err: err}
	}
	return err
}

func rootMkdir(r *Root, name string, perm FileMode) error {
	_, err := doInRoot(r, name, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, mkdirat(parent, name, perm)
	})
	if err != nil {
		return &PathError{Op: "mkdirat", Path: name, Err: err}
	}
	return nil
}

func rootMkdirAll(r *Root, fullname string, perm FileMode) error {
	// doInRoot 依次打开每个路径元素。
	//
	// openDirFunc 打开除最后一个路径组件之外的所有组件。
	// 通常默认的 openDirFunc 只是用 O_DIRECTORY 打开目录。
	// 这里我们将其替换为一个会沿途创建缺失目录的函数。
	openDirFunc := func(parent sysfdType, name string) (sysfdType, error) {
		for try := range 2 {
			fd, err := rootOpenDir(parent, name)
			switch err.(type) {
			case nil, errSymlink:
				return fd, err
			}
			if try > 0 || !IsNotExist(err) {
				return 0, &PathError{Op: "openat", Err: err}
			}
			// 在 EEXIST 时重试，因为目录可能已经在 rootOpenDir 和
			// mkdirat 调用之间被另一个进程或线程创建了。
			if err := mkdirat(parent, name, perm); err != nil && err != syscall.EEXIST {
				return 0, &PathError{Op: "mkdirat", Err: err}
			}
		}
		panic("unreachable")
	}
	// openLastComponentFunc 打开最后一个路径组件。
	openLastComponentFunc := func(parent sysfdType, name string) (struct{}, error) {
		err := mkdirat(parent, name, perm)
		if err == syscall.EEXIST {
			mode, e := modeAt(parent, name)
			if e == nil {
				if mode.IsDir() {
					// MkdirAll 的目标是一个已存在的目录。
					err = nil
				} else if mode&ModeSymlink != 0 {
					// MkdirAll 的目标是一个符号链接。
					// 为了与 os.MkdirAll 保持一致，
					// 如果该链接解析为目录则视为成功。
					// 我们在这里不返回 errSymlink，因为我们
					// 不想在链接目标不存在时创建它。
					fi, e := r.Stat(fullname)
					if e == nil && fi.Mode().IsDir() {
						err = nil
					}
				}
			}
		}
		switch err.(type) {
		case nil, errSymlink:
			return struct{}{}, err
		}
		return struct{}{}, &PathError{Op: "mkdirat", Err: err}
	}
	_, err := doInRoot(r, fullname, openDirFunc, openLastComponentFunc)
	if err != nil {
		if _, ok := err.(*PathError); !ok {
			err = &PathError{Op: "mkdirat", Path: fullname, Err: err}
		}
	}
	return err
}

func rootReadlink(r *Root, name string) (string, error) {
	target, err := doInRoot(r, name, nil, func(parent sysfdType, name string) (string, error) {
		return readlinkat(parent, name)
	})
	if err != nil {
		return "", &PathError{Op: "readlinkat", Path: name, Err: err}
	}
	return target, nil
}

func rootRemove(r *Root, name string) error {
	_, err := doInRoot(r, name, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, removeat(parent, name)
	})
	if err != nil {
		return &PathError{Op: "removeat", Path: name, Err: err}
	}
	return nil
}

func rootRemoveAll(r *Root, name string) error {
	// 与 os.RemoveAll 保持一致：从名称中去除末尾的 /，
	// 以便 RemoveAll("not_a_directory/") 成功。
	for len(name) > 0 && IsPathSeparator(name[len(name)-1]) {
		name = name[:len(name)-1]
	}
	if endsWithDot(name) {
		// 与 os.RemoveAll 保持一致：尝试删除 . 时返回 EINVAL
		return &PathError{Op: "RemoveAll", Path: name, Err: syscall.EINVAL}
	}
	_, err := doInRoot(r, name, nil, func(parent sysfdType, name string) (struct{}, error) {
		return struct{}{}, removeAllFrom(parent, name)
	})
	if IsNotExist(err) {
		return nil
	}
	if err != nil {
		return &PathError{Op: "RemoveAll", Path: name, Err: underlyingError(err)}
	}
	return err
}

func rootRename(r *Root, oldname, newname string) error {
	_, err := doInRoot(r, oldname, nil, func(oldparent sysfdType, oldname string) (struct{}, error) {
		_, err := doInRoot(r, newname, nil, func(newparent sysfdType, newname string) (struct{}, error) {
			return struct{}{}, renameat(oldparent, oldname, newparent, newname)
		})
		return struct{}{}, err
	})
	if err != nil {
		return &LinkError{"renameat", oldname, newname, err}
	}
	return err
}

func rootLink(r *Root, oldname, newname string) error {
	_, err := doInRoot(r, oldname, nil, func(oldparent sysfdType, oldname string) (struct{}, error) {
		_, err := doInRoot(r, newname, nil, func(newparent sysfdType, newname string) (struct{}, error) {
			return struct{}{}, linkat(oldparent, oldname, newparent, newname)
		})
		return struct{}{}, err
	})
	if err != nil {
		return &LinkError{"linkat", oldname, newname, err}
	}
	return err
}

// doInRoot 对 Root 中的某个路径执行操作。
//
// 它会以包含最后一个路径元素的目录的 FD 或句柄，
// 以及最后一个路径元素的名称来调用 f。
//
// 例如，给定路径 a/b/c，它会以 a/b 的 FD 和名称 "c" 调用 f。
//
// 如果 openDirFunc 非 nil，则会调用它来打开中间路径元素。
// 例如，给定路径 a/b/c，openDirFunc 将依次被调用来打开 a 和 a/b。
//
// f 或 openDirFunc 可以返回 errSymlink，以表示该路径元素是一个
// 应当被跟随的符号链接。注意这可能导致 f 以不同的名称被多次调用。
// 例如，给定指向 "target" 的符号链接路径 "link"，
// f 会以路径 "link" 被调用，返回 errSymlink("target")，然后再次以
// 路径 "target" 被调用。
//
// 如果 f 或 openDirFunc 返回一个 *PathError，doInRoot 会将 PathError.Path 设置为
// 导致该错误的完整路径。
func doInRoot[T any](r *Root, name string, openDirFunc func(parent sysfdType, name string) (sysfdType, error), f func(parent sysfdType, name string) (T, error)) (ret T, err error) {
	if err := r.root.incref(); err != nil {
		return ret, err
	}
	defer r.root.decref()

	parts, suffixSep, err := splitPathInRoot(name, nil, nil)
	if err != nil {
		return ret, err
	}
	if openDirFunc == nil {
		openDirFunc = rootOpenDir
	}

	rootfd := r.root.fd
	dirfd := rootfd
	defer func() {
		if dirfd != rootfd {
			syscall.Close(dirfd)
		}
	}()

	// 在解析 .. 路径组件时，我们从根重新开始路径解析。
	// （我们无法通过 openat(dir, "..") 向上移动到父目录，
	// 因为 dir 可能自我们打开它以来已被移动。）
	// 为了限制恶意路径可能导致我们执行的打开次数，我们设置了
	// 路径步骤总数以及由 .. 组件引起的重启总数的上限。
	// 如果*两个*上限都被超出，我们就中止该操作。
	const maxSteps = 255
	const maxRestarts = 8

	i := 0
	steps := 0
	restarts := 0
	symlinks := 0
Loop:
	for {
		steps++
		if steps > maxSteps && restarts > maxRestarts {
			return ret, syscall.ENAMETOOLONG
		}

		if parts[i] == ".." {
			// 解析一个或多个父级（".."）路径组件。
			//
			// 重写原始路径，
			// 移除被 ".." 组件抵消的元素，
			// 然后从头开始。
			restarts++
			end := i + 1
			for end < len(parts) && parts[end] == ".." {
				end++
			}
			count := end - i
			if count > i {
				return ret, errPathEscapes
			}
			parts = slices.Delete(parts, i-count, end)
			if len(parts) == 0 {
				parts = []string{"."}
			}
			i = 0
			if dirfd != rootfd {
				syscall.Close(dirfd)
			}
			dirfd = rootfd
			continue
		}

		if i == len(parts)-1 {
			// 这是最后一个路径元素。
			// 调用 f 来决定如何处理它。
			// 如果 f 返回 errSymlink，则该元素是一个
			// 应当被跟随的符号链接。
			// suffixSep 包含任何尾随的分隔符字符，
			// 此时我们将它们重新连接到最后一部分。
			ret, err = f(dirfd, parts[i]+suffixSep)
			if err == nil {
				return
			}
		} else {
			var fd sysfdType
			fd, err = openDirFunc(dirfd, parts[i])
			if err == nil {
				if dirfd != rootfd {
					syscall.Close(dirfd)
				}
				dirfd = fd
			}
		}

		switch e := err.(type) {
		case nil:
		case errSymlink:
			symlinks++
			if symlinks > rootMaxSymlinks {
				return ret, syscall.ELOOP
			}
			newparts, newSuffixSep, err := splitPathInRoot(string(e), parts[:i], parts[i+1:])
			if err != nil {
				return ret, err
			}
			if i == len(parts)-1 {
				// suffixSep 包含链接目标中的任何尾随路径分隔符
				// 字符。
				// 如果我们要替换路径的剩余部分，则保留这些字符。
				// 如果我们要替换路径的某个中间组件，
				// 则忽略它们，因为中间组件必须始终
				// 是目录。
				suffixSep = newSuffixSep
			}
			if len(newparts) < i || !slices.Equal(parts[:i], newparts[:i]) {
				// 我们已经遍历过的路径中的某个组件
				// 发生了变化。我们需要从根重新开始解析。
				i = 0
				if dirfd != rootfd {
					syscall.Close(dirfd)
				}
				dirfd = rootfd
			}
			parts = newparts
			continue Loop
		case *PathError:
			// 这相当于 strings.Join(parts[:i+1], PathSeparator)。
			e.Path = parts[0]
			for _, part := range parts[1 : i+1] {
				e.Path += string(PathSeparator) + part
			}
			return ret, e
		default:
			return ret, err
		}

		i++
	}
}

// errSymlink 报告正在操作的文件实际上是一个符号链接，
// 以及该符号链接的目标。
type errSymlink string

func (errSymlink) Error() string { panic("errSymlink is not user-visible") }
