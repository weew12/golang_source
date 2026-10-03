// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"io/fs"
	"syscall"
)

// Getpagesize 返回底层系统的内存页大小。
func Getpagesize() int { return syscall.Getpagesize() }

// File 表示一个打开的文件描述符。
//
// File 的方法可安全地并发使用。
type File struct {
	*file // 特定于操作系统
}

// FileInfo 描述一个文件，由 [Stat] 和 [Lstat] 返回。
type FileInfo = fs.FileInfo

// FileMode 表示一个文件的模式和权限位。
// 这些位在所有系统上具有相同的定义，因此
// 关于文件的信息可以可移植地从一个系统
// 移动到另一个系统。并非所有位都适用于所有系统。
// 对于目录，唯一必需的位是 [ModeDir]。
type FileMode = fs.FileMode

// 定义的文件模式位是 [FileMode] 的最高有效位。
// 九个最低有效位是标准的 Unix rwxrwxrwx 权限。
// 这些位的值应被视为公共 API 的一部分，
// 可能会用于线协议或磁盘表示：它们不得
// 被更改，尽管可能会新增一些位。
const (
	// 单个字母是 String 方法格式化时
	// 使用的缩写。
	ModeDir        = fs.ModeDir        // d: 是一个目录
	ModeAppend     = fs.ModeAppend     // a: 仅追加
	ModeExclusive  = fs.ModeExclusive  // l: 独占使用
	ModeTemporary  = fs.ModeTemporary  // T: 临时文件；仅 Plan 9
	ModeSymlink    = fs.ModeSymlink    // L: 符号链接
	ModeDevice     = fs.ModeDevice     // D: 设备文件
	ModeNamedPipe  = fs.ModeNamedPipe  // p: 命名管道（FIFO）
	ModeSocket     = fs.ModeSocket     // S: Unix 域套接字
	ModeSetuid     = fs.ModeSetuid     // u: setuid
	ModeSetgid     = fs.ModeSetgid     // g: setgid
	ModeCharDevice = fs.ModeCharDevice // c: Unix 字符设备，当设置了 ModeDevice 时
	ModeSticky     = fs.ModeSticky     // t: sticky
	ModeIrregular  = fs.ModeIrregular  // ?: 非常规文件；关于此文件没有其他已知信息

	// 类型位的掩码。对于常规文件，不会设置任何位。
	ModeType = fs.ModeType

	ModePerm = fs.ModePerm // Unix 权限位，0o777
)

func (fs *fileStat) Name() string { return fs.name }
func (fs *fileStat) IsDir() bool  { return fs.Mode().IsDir() }

// SameFile 报告 fi1 和 fi2 是否描述同一个文件。
// 例如，在 Unix 上这意味着两个底层结构的设备
// 和 inode 字段完全相同；在其他系统上，
// 判断可能基于路径名。
// SameFile 仅适用于由本包的 [Stat] 返回的结果。
// 在其他情况下它返回 false。
func SameFile(fi1, fi2 FileInfo) bool {
	fs1, ok1 := fi1.(*fileStat)
	fs2, ok2 := fi2.(*fileStat)
	if !ok1 || !ok2 {
		return false
	}
	return sameFile(fs1, fs2)
}
