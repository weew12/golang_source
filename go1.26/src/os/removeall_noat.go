// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (js && wasm) || plan9

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

	// rmdir 系统调用在 Plan 9 上允许删除 "."，
	// 因此我们不允许这样做，以保持与 RemoveAll 的
	// "at" 实现一致。
	if endsWithDot(path) {
		return &PathError{Op: "RemoveAll", Path: path, Err: syscall.EINVAL}
	}

	// 简单情形：如果 Remove 成功，就完成了。
	err := Remove(path)
	if err == nil || IsNotExist(err) {
		return nil
	}

	// 否则，这是否是一个需要我们递归进入的目录？
	dir, serr := Lstat(path)
	if serr != nil {
		if serr, ok := serr.(*PathError); ok && (IsNotExist(serr.Err) || serr.Err == syscall.ENOTDIR) {
			return nil
		}
		return serr
	}
	if !dir.IsDir() {
		// 不是目录；返回来自 Remove 的错误。
		return err
	}

	// 删除内容并返回第一个错误。
	err = nil
	for {
		fd, err := Open(path)
		if err != nil {
			if IsNotExist(err) {
				// 已被其他人删除。
				return nil
			}
			return err
		}

		const reqSize = 1024
		var names []string
		var readErr error

		for {
			numErr := 0
			names, readErr = fd.Readdirnames(reqSize)

			for _, name := range names {
				err1 := RemoveAll(path + string(PathSeparator) + name)
				if err == nil {
					err = err1
				}
				if err1 != nil {
					numErr++
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
		fd.Close()

		if readErr == io.EOF {
			break
		}
		// 如果 Readdirnames 返回了错误，就使用它。
		if err == nil {
			err = readErr
		}
		if len(names) == 0 {
			break
		}

		// 我们不想不必要地重新打开目录，所以如果从 Readdirnames
		// 获得的名称少于请求的数量，就尝试现在直接删除该目录。
		// 如果成功，我们就完成了。
		if len(names) < reqSize {
			err1 := Remove(path)
			if err1 == nil || IsNotExist(err1) {
				return nil
			}

			if err != nil {
				// 我们在删除目录内容时遇到了一些错误，且由于
				// 读取到的名称少于请求的数量，很可能没有更多
				// 文件需要删除了。不要再循环回来再次读取目录。
				// 我们很可能会得到相同的错误。
				return err
			}
		}
	}

	// 删除目录。
	err1 := Remove(path)
	if err1 == nil || IsNotExist(err1) {
		return nil
	}
	if runtime.GOOS == "windows" && IsPermission(err1) {
		if fs, err := Stat(path); err == nil {
			if err = Chmod(path, FileMode(0200|int(fs.Mode()))); err == nil {
				err1 = Remove(path)
			}
		}
	}
	if err == nil {
		err = err1
	}
	return err
}
