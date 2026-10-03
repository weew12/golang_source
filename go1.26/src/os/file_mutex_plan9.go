// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

// Plan 9 的文件锁支持。它使用
// internal/poll 包的 fdMutex。

// incref 为文件添加一个引用。如果文件
// 已经关闭，则返回错误。此方法定义在 File 上，
// 以便我们可以进行 nil 检查。
func (f *File) incref(op string) (err error) {
	if f == nil {
		return ErrInvalid
	}
	if !f.fdmu.Incref() {
		err = ErrClosed
		if op != "" {
			err = &PathError{Op: op, Path: f.name, Err: err}
		}
	}
	return err
}

// decref 移除对文件的一个引用。如果这是最后一个
// 剩余的引用，并且文件已被标记为关闭，
// 则真正关闭它。
func (file *file) decref() error {
	if file.fdmu.Decref() {
		return file.destroy()
	}
	return nil
}

// readLock 为文件添加一个引用并为其加读锁。
// 如果文件已经关闭，则返回错误。
func (file *file) readLock() error {
	if !file.fdmu.ReadLock() {
		return ErrClosed
	}
	return nil
}

// readUnlock 移除对文件的一个引用并解除其读锁。
// 如果文件被标记为已关闭且没有剩余的
// 引用，它还会关闭该文件。
func (file *file) readUnlock() {
	if file.fdmu.ReadUnlock() {
		file.destroy()
	}
}

// writeLock 为文件添加一个引用并为其加写锁。
// 如果文件已经关闭，则返回错误。
func (file *file) writeLock() error {
	if !file.fdmu.WriteLock() {
		return ErrClosed
	}
	return nil
}

// writeUnlock 移除对文件的一个引用并解除其写锁。
// 如果文件被标记为已关闭且没有剩余的
// 引用，它还会关闭该文件。
func (file *file) writeUnlock() {
	if file.fdmu.WriteUnlock() {
		file.destroy()
	}
}
