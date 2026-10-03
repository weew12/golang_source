// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix || wasip1 || windows

package os

import (
	"io"
	"runtime"
	"syscall"
)

func removeAll(path string) error {
	if path == "" {
		// 静默失败以保持与 RemoveAll 先前行为的兼容性。
		// 参见 issue 28830。
		return nil
	}

	// rmdir 系统调用不允许删除 "."，
	// 因此我们也不允许这样做。
	if endsWithDot(path) {
		return &PathError{Op: "RemoveAll", Path: path, Err: syscall.EINVAL}
	}

	// 简单情形：如果 Remove 成功，就完成了。
	err := Remove(path)
	if err == nil || IsNotExist(err) {
		return nil
	}

	// RemoveAll 通过从父目录中删除路径的基础部分来进行递归
	parentDir, base := splitPath(path)

	flag := O_RDONLY
	if runtime.GOOS == "windows" {
		// 在 Windows 上，进程可能没有父目录的读取权限，
		// 但仍然可以删除其中的文件。参见 https://go.dev/issue/74134。
		// 即使没有读取权限，我们也可以通过传入
		// O_WRONLY | O_RDWR 标志来打开文件，该标志会映射为 FILE_READ_ATTRIBUTES。
		flag = O_WRONLY | O_RDWR
	}
	parent, err := OpenFile(parentDir, flag, 0)
	if IsNotExist(err) {
		// 如果父目录不存在，base 也不可能存在。静默失败
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()

	if err := removeAllFrom(sysfdType(parent.Fd()), base); err != nil {
		if pathErr, ok := err.(*PathError); ok {
			pathErr.Path = parentDir + string(PathSeparator) + pathErr.Path
			err = pathErr
		}
		return err
	}
	return nil
}

func removeAllFrom(parentFd sysfdType, base string) error {
	// 简单情形：如果 Unlink（即 remove）成功，就完成了。
	err := removefileat(parentFd, base)
	if err == nil || IsNotExist(err) {
		return nil
	}

	// EISDIR 表示这是一个目录，我们需要删除其内容。
	// EPERM 或 EACCES 表示我们没有父目录的写权限，
	// 但该条目仍可能是一个需要删除其内容的目录。
	// 否则直接返回该错误。
	if err != syscall.EISDIR && err != syscall.EPERM && err != syscall.EACCES {
		return &PathError{Op: "unlinkat", Path: base, Err: err}
	}
	uErr := err

	// 删除目录的各项条目。
	var recurseErr error
	for {
		const reqSize = 1024
		var respSize int

		// 打开要递归进入的目录。
		file, err := openDirAt(parentFd, base)
		if err != nil {
			if IsNotExist(err) {
				return nil
			}
			if err == syscall.ENOTDIR || isErrNoFollow(err) {
				// 不是目录；返回来自 unix.Unlinkat 的错误。
				return &PathError{Op: "unlinkat", Path: base, Err: uErr}
			}
			recurseErr = &PathError{Op: "openfdat", Path: base, Err: err}
			break
		}

		for {
			numErr := 0

			names, readErr := file.Readdirnames(reqSize)
			// 除 EOF 之外的错误都应阻止我们继续。
			if readErr != nil && readErr != io.EOF {
				file.Close()
				if IsNotExist(readErr) {
					return nil
				}
				return &PathError{Op: "readdirnames", Path: base, Err: readErr}
			}

			respSize = len(names)
			for _, name := range names {
				err := removeAllFrom(sysfdType(file.Fd()), name)
				if err != nil {
					if pathErr, ok := err.(*PathError); ok {
						pathErr.Path = base + string(PathSeparator) + pathErr.Path
					}
					numErr++
					if recurseErr == nil {
						recurseErr = err
					}
				}
			}

			// 如果我们能删除任意一个条目，就跳出以开始新的迭代。
			// 否则，丢弃当前名称，获取下一批条目并尝试删除它们。
			if numErr != reqSize {
				break
			}
		}

		// 从目录中删除文件可能导致操作系统重新排列它。
		// 简单地再次调用 Readdirnames 可能会跳过某些条目。
		// 避免这种情况的唯一可靠方法是关闭并重新打开
		// 目录。参见 issue 20841。
		file.Close()

		// 当到达目录末尾时结束
		if respSize < reqSize {
			break
		}
	}

	// 删除目录本身。
	unlinkError := removedirat(parentFd, base)
	if unlinkError == nil || IsNotExist(unlinkError) {
		return nil
	}

	if recurseErr != nil {
		return recurseErr
	}
	return &PathError{Op: "unlinkat", Path: base, Err: unlinkError}
}

// openDirAt 打开相对于文件描述符 dirfd 所引用目录的目录名。
// 如果 name 不是目录（这包括指向目录的符号链接），它应返回错误。
// 除此之外，它的行为应与 openFileNolog 相同。
//
// 它的行为像 openFileNolog 而不是 OpenFile，因为
// 我们将要（尝试）删除该文件。
// 该文件的内容与测试缓存无关。
func openDirAt(dirfd sysfdType, name string) (*File, error) {
	fd, err := rootOpenDir(dirfd, name)
	if err != nil {
		return nil, err
	}
	return newDirFile(fd, name)
}
