// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// fs 包定义了文件系统的基础接口。
// 文件系统可由宿主操作系统提供，也可由其他包提供。
//
// # 路径名称
//
// 本包中的所有接口均使用统一的路径名称语法，与宿主操作系统无关。
//
// 路径名称为 UTF-8 编码、非根目录、以斜杠分隔的路径元素序列，例如 “x/y/z”。
// 路径名称不能包含 “.”、“..” 或空字符串元素，
// 唯一特例是可使用 “.” 表示根目录。
// 路径不能以斜杠开头或结尾：“/x” 和 “x/” 均为无效路径。
//
// # 测试
//
// 测试文件系统实现时，可使用 testing/fstest 包提供的支持。
package fs

import (
	"internal/oserror"
	"time"
	"unicode/utf8"
)

// FS 提供对分层文件系统的访问能力。
//
// FS 接口是文件系统所需实现的最小接口。
// 文件系统可以实现额外的接口（例如 ReadFileFS），
// 以提供扩展或优化后的功能。
//
// 可使用 testing/fstest.TestFS 测试 FS 接口实现的正确性。
type FS interface {
	// Open 打开指定名称的文件。
	// 必须调用 [File.Close] 释放相关资源。
	//
	// 当 Open 返回错误时，该错误应为 *PathError 类型，
	// 其中 Op 字段设为 "open"，Path 字段设为 name，
	// Err 字段用于描述具体问题。
	//
	// Open 应拒绝打开不满足 ValidPath(name) 的名称，
	// 并返回 Err 字段为 ErrInvalid 或 ErrNotExist 的 *PathError。
	Open(name string) (File, error)
}

// ValidPath 判断给定的路径名称是否可用于 Open 调用。
//
// 注意：所有系统（包括 Windows）的路径均使用斜杠分隔。
// 包含反斜杠、冒号等其他字符的路径会被视为有效路径，
// 但 FS 实现绝不能将这些字符解析为路径元素分隔符。
// 更多详情见 [Path Names] 章节。
//
// [Path Names]: https://pkg.go.dev/io/fs#hdr-Path_Names
func ValidPath(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}

	if name == "." {
		// 特殊情况
		return true
	}

	// 遍历路径中的所有元素，逐一校验
	for {
		i := 0
		for i < len(name) && name[i] != '/' {
			i++
		}
		elem := name[:i]
		if elem == "" || elem == "." || elem == ".." {
			return false
		}
		if i == len(name) {
			return true // 到达合法结尾
		}
		name = name[i+1:]
	}
}

// File 提供对单个文件的访问能力。
// File 接口是文件所需实现的最小接口。
// 目录文件还应实现 [ReadDirFile] 接口。
// 文件可实现 io.ReaderAt 或 io.Seeker 接口作为优化方案。
type File interface {
	Stat() (FileInfo, error)
	Read([]byte) (int, error)
	Close() error
}

// DirEntry 是从目录中读取的条目
// （通过 ReadDir 函数或 ReadDirFile 的 ReadDir 方法获取）。
type DirEntry interface {
	// Name 返回条目描述的文件（或子目录）名称。
	// 该名称仅为路径的最后一个元素（基础名称），而非完整路径。
	// 例如：Name 会返回 "hello.go"，而非 "home/gopher/hello.go"。
	Name() string

	// IsDir 判断该条目是否为目录。
	IsDir() bool

	// Type 返回条目的类型位。
	// 类型位是标准 FileMode 位的子集，与 FileMode.Type 方法返回值一致。
	Type() FileMode

	// Info 返回条目描述的文件或子目录的 FileInfo。
	// 返回的 FileInfo 可能来自原始目录读取时，也可能来自调用 Info 时。
	// 若目录读取后文件被删除或重命名，Info 可能返回满足 errors.Is(err, ErrNotExist) 的错误。
	// 若条目为符号链接，Info 返回链接本身的信息，而非链接目标的信息。
	Info() (FileInfo, error)
}

// ReadDirFile 是可通过 ReadDir 方法读取条目的目录文件。
// 所有目录文件都应实现此接口。
// （允许任意文件实现此接口，但非目录文件调用 ReadDir 应返回错误。）
type ReadDirFile interface {
	File

	// ReadDir 读取目录内容，返回最多 n 个按目录顺序排列的 DirEntry 切片。
	// 对同一文件的后续调用会继续返回后续的 DirEntry。
	//
	// 若 n > 0，ReadDir 最多返回 n 个 DirEntry 结构体。
	// 这种情况下，若 ReadDir 返回空切片，会同时返回非空错误说明原因。
	// 到达目录末尾时，错误为 io.EOF。
	//（ReadDir 必须直接返回 io.EOF，而非包装 io.EOF 的错误。）
	//
	// 若 n <= 0，ReadDir 一次性返回目录中所有剩余的 DirEntry。
	// 这种情况下，若 ReadDir 执行成功（读取到目录末尾），会返回切片和 nil 错误。
	// 若在到达目录末尾前遇到错误，
	// ReadDir 会返回截至该点已读取的 DirEntry 列表和非空错误。
	ReadDir(n int) ([]DirEntry, error)
}

// 文件系统通用错误。
// 文件系统返回的错误可通过 errors.Is 与这些错误进行匹配判断。
var (
	ErrInvalid    = errInvalid()    // "无效参数"
	ErrPermission = errPermission() // "权限不足"
	ErrExist      = errExist()      // "文件已存在"
	ErrNotExist   = errNotExist()   // "文件不存在"
	ErrClosed     = errClosed()     // "文件已关闭"
)

func errInvalid() error    { return oserror.ErrInvalid }
func errPermission() error { return oserror.ErrPermission }
func errExist() error      { return oserror.ErrExist }
func errNotExist() error   { return oserror.ErrNotExist }
func errClosed() error     { return oserror.ErrClosed }

// FileInfo 描述文件信息，由 Stat 方法返回。
type FileInfo interface {
	Name() string       // 文件的基础名称
	Size() int64        // 普通文件的字节长度；其他文件为系统依赖值
	Mode() FileMode     // 文件模式位
	ModTime() time.Time // 修改时间
	IsDir() bool        // Mode().IsDir() 的简写
	Sys() any           // 底层数据源（可返回 nil）
}

// FileMode 表示文件的模式和权限位。
// 这些位在所有系统上定义一致，因此文件信息可跨系统移植。
// 并非所有位都适用于所有系统。
// 唯一强制要求的位是目录对应的 ModeDir。
type FileMode uint32

// 定义的文件模式位是 FileMode 的最高有效位。
// 最低 9 位是标准的 Unix rwxrwxrwx 权限位。
// 这些位的值应视为公共 API 的一部分，可用于网络协议或磁盘存储：
// 绝不允许修改，仅可新增位定义。
const (
	// 单个字母是 String 方法格式化时使用的缩写
	ModeDir        FileMode = 1 << (32 - 1 - iota) // d: 目录
	ModeAppend                                     // a: 仅追加模式
	ModeExclusive                                  // l: 独占使用
	ModeTemporary                                  // T: 临时文件；仅 Plan 9 系统
	ModeSymlink                                    // L: 符号链接
	ModeDevice                                     // D: 设备文件
	ModeNamedPipe                                  // p: 命名管道（FIFO）
	ModeSocket                                     // S: Unix 域套接字
	ModeSetuid                                     // u: 设置用户ID
	ModeSetgid                                     // g: 设置组ID
	ModeCharDevice                                 // c: Unix 字符设备（需同时设置 ModeDevice）
	ModeSticky                                     // t: 粘滞位
	ModeIrregular                                  // ?: 非规则文件；无其他已知属性

	// 类型位掩码。普通文件无任何类型位设置。
	ModeType = ModeDir | ModeSymlink | ModeNamedPipe | ModeSocket | ModeDevice | ModeCharDevice | ModeIrregular

	ModePerm FileMode = 0777 // Unix 权限位
)

func (m FileMode) String() string {
	const str = "dalTLDpSugct?"
	var buf [32]byte // Mode 是 uint32 类型
	w := 0
	for i, c := range str {
		if m&(1<<uint(32-1-i)) != 0 {
			buf[w] = byte(c)
			w++
		}
	}
	if w == 0 {
		buf[w] = '-'
		w++
	}
	const rwx = "rwxrwxrwx"
	for i, c := range rwx {
		if m&(1<<uint(9-1-i)) != 0 {
			buf[w] = byte(c)
		} else {
			buf[w] = '-'
		}
		w++
	}
	return string(buf[:w])
}

// IsDir 判断 m 是否表示目录。
// 即检查 m 中是否设置了 ModeDir 位。
func (m FileMode) IsDir() bool {
	return m&ModeDir != 0
}

// IsRegular 判断 m 是否表示普通文件。
// 即检查未设置任何文件类型位。
func (m FileMode) IsRegular() bool {
	return m&ModeType == 0
}

// Perm 返回 m 中的 Unix 权限位 (m & [ModePerm])。
func (m FileMode) Perm() FileMode {
	return m & ModePerm
}

// Type 返回 m 中的类型位 (m & [ModeType] )。
func (m FileMode) Type() FileMode {
	return m & ModeType
}

// PathError 记录错误信息，以及引发错误的操作和文件路径。
type PathError struct {
	Op   string
	Path string
	Err  error
}

func (e *PathError) Error() string { return e.Op + " " + e.Path + ": " + e.Err.Error() }

func (e *PathError) Unwrap() error { return e.Err }

// Timeout 判断该错误是否为超时错误。
func (e *PathError) Timeout() bool {
	t, ok := e.Err.(interface{ Timeout() bool })
	return ok && t.Timeout()
}
