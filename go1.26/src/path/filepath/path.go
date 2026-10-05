// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package filepath 实现了以与目标操作系统定义的文件路径
// 相兼容的方式操作文件名路径的实用例程。
//
// filepath 包使用正斜杠或反斜杠，
// 具体取决于操作系统。要处理诸如 URL 这类
// 无论操作系统如何都始终使用正斜杠的路径，
// 请参见 [path] 包。
package filepath

import (
	"errors"
	"internal/bytealg"
	"internal/filepathlite"
	"io/fs"
	"os"
	"slices"
)

const (
	Separator     = os.PathSeparator
	ListSeparator = os.PathListSeparator
)

// Clean 通过纯粹的词法处理返回与 path 等价的最短路径名。
// 它迭代地应用以下规则，直到无法再做进一步处理：
//
//  1. 将多个 [Separator] 元素替换为单个元素。
//  2. 消除每个 . 路径名元素（当前目录）。
//  3. 消除每个内部的 .. 路径名元素（父目录）
//     以及位于它前面的非 .. 元素。
//  4. 消除位于有根路径开头的 .. 元素：
//     即，假设 Separator 为 '/'，将路径开头的 "/.." 替换为 "/"。
//
// 返回的路径只有在表示根目录时才以斜杠结尾，
// 例如 Unix 上的 "/" 或 Windows 上的 `C:\`。
//
// 最后，所有出现的斜杠都被替换为 Separator。
//
// 如果此过程的结果是空字符串，Clean
// 返回字符串 "."。
//
// 在 Windows 上，Clean 除了将出现的 "/" 替换为 `\` 之外，
// 不会修改卷名。
// 例如，Clean("//host/share/../x") 返回 `\\host\share\x`。
//
// 另见 Rob Pike, “Lexical File Names in Plan 9 or
// Getting Dot-Dot Right,”
// https://9p.io/sys/doc/lexnames.html
func Clean(path string) string {
	return filepathlite.Clean(path)
}

// IsLocal 报告 path 仅使用词法分析是否具有以下所有性质：
//
//   - 位于对其求值所在目录为根的子树内
//   - 不是绝对路径
//   - 不为空
//   - 在 Windows 上，不是诸如 "NUL" 之类的保留名称
//
// 如果 IsLocal(path) 返回 true，那么
// Join(base, path) 将始终产生一个包含在 base 内的路径，并且
// Clean(path) 将始终产生一个没有 ".." 路径元素的无根路径。
//
// IsLocal 是纯粹的词法操作。
// 特别地，它不考虑文件系统中可能存在的
// 任何符号链接的影响。
func IsLocal(path string) bool {
	return filepathlite.IsLocal(path)
}

// Localize 将以斜杠分隔的路径转换为操作系统路径。
// 输入路径必须是 [io/fs.ValidPath] 所报告的有效路径。
//
// 如果路径无法被操作系统表示，Localize 返回错误。
// 例如，路径 a\b 在 Windows 上会被拒绝，因为在 Windows 上 \ 是分隔符
// 字符，不能作为文件名的一部分。
//
// Localize 返回的路径将始终是本地路径，如 IsLocal 所报告的那样。
func Localize(path string) (string, error) {
	return filepathlite.Localize(path)
}

// ToSlash 返回将 path 中每个分隔符字符
// 替换为斜杠（'/'）字符后的结果。多个分隔符
// 会被替换为多个斜杠。
func ToSlash(path string) string {
	return filepathlite.ToSlash(path)
}

// FromSlash 返回将 path 中每个斜杠（'/'）字符
// 替换为分隔符字符后的结果。多个斜杠会被替换
// 为多个分隔符。
//
// 另见 Localize 函数，它将 io/fs 包所使用的
// 以斜杠分隔的路径转换为操作系统路径。
func FromSlash(path string) string {
	return filepathlite.FromSlash(path)
}

// SplitList 拆分由操作系统特定的 [ListSeparator] 连接的路径列表，
// 通常见于 PATH 或 GOPATH 环境变量中。
// 与 strings.Split 不同，SplitList 在传入空字符串时
// 返回空切片。
func SplitList(path string) []string {
	return splitList(path)
}

// Split 在最后一个 [Separator] 之后立即拆分 path，
// 将其分为目录和文件名两部分。
// 如果 path 中没有 Separator，Split 返回空 dir
// 并将 file 设为 path。
// 返回值具有 path = dir+file 的性质。
func Split(path string) (dir, file string) {
	return filepathlite.Split(path)
}

// Join 将任意数量的路径元素连接为单个路径，
// 用操作系统特定的 [Separator] 分隔它们。空元素
// 会被忽略。结果会被 Clean。然而，如果参数
// 列表为空或其所有元素都为空，Join 返回
// 空字符串。
// 在 Windows 上，仅当第一个非空元素是 UNC 路径时，
// 结果才会是 UNC 路径。
func Join(elem ...string) string {
	return join(elem)
}

// Ext 返回 path 所使用的文件名扩展名。
// 扩展名是从 path 最后一个元素中最后一个点
// 开始的后缀；如果没有点，
// 则为空。
func Ext(path string) string {
	return filepathlite.Ext(path)
}

// EvalSymlinks 返回对任何符号链接求值之后的路径名。
// 如果 path 是相对路径，结果将相对于当前目录，
// 除非其中一个组件是绝对符号链接。
// EvalSymlinks 会对结果调用 [Clean]。
func EvalSymlinks(path string) (string, error) {
	return evalSymlinks(path)
}

// IsAbs 报告 path 是否为绝对路径。
func IsAbs(path string) bool {
	return filepathlite.IsAbs(path)
}

// Abs 返回 path 的绝对表示。
// 如果 path 不是绝对路径，它将与当前工作目录
// 连接以转换为绝对路径。给定文件的绝对
// 路径名不保证唯一。
// Abs 会对结果调用 [Clean]。
func Abs(path string) (string, error) {
	return abs(path)
}

func unixAbs(path string) (string, error) {
	if IsAbs(path) {
		return Clean(path), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return Join(wd, path), nil
}

// Rel 返回一个相对路径，当它与 basePath 以分隔符连接时，
// 在词法上等价于 targPath。也就是说，
// [Join](basePath, Rel(basePath, targPath)) 等价于 targPath 本身。
//
// 返回的路径将始终相对于 basePath，即使 basePath 和
// targPath 没有共享任何元素。Rel 会对结果调用 [Clean]。
//
// 如果 targPath 无法相对于 basePath 表示，
// 或者计算它需要知道当前工作目录，则返回错误。
func Rel(basePath, targPath string) (string, error) {
	baseVol := VolumeName(basePath)
	targVol := VolumeName(targPath)
	base := Clean(basePath)
	targ := Clean(targPath)
	if sameWord(targ, base) {
		return ".", nil
	}
	base = base[len(baseVol):]
	targ = targ[len(targVol):]
	if base == "." {
		base = ""
	} else if base == "" && filepathlite.VolumeNameLen(baseVol) > 2 /* 是UNC */ {
		// 将任何匹配 `\\host\share` basePath 的 targetpath 视为绝对路径。
		base = string(Separator)
	}

	// 不能使用 IsAbs——`\a` 和 `a` 在 Windows 中都是相对路径。
	baseSlashed := len(base) > 0 && base[0] == Separator
	targSlashed := len(targ) > 0 && targ[0] == Separator
	if baseSlashed != targSlashed || !sameWord(baseVol, targVol) {
		return "", errors.New("Rel: can't make " + targPath + " relative to " + basePath)
	}
	// 将 base[b0:bi] 和 targ[t0:ti] 定位到第一个不同的元素。
	bl := len(base)
	tl := len(targ)
	var b0, bi, t0, ti int
	for {
		for bi < bl && base[bi] != Separator {
			bi++
		}
		for ti < tl && targ[ti] != Separator {
			ti++
		}
		if !sameWord(targ[t0:ti], base[b0:bi]) {
			break
		}
		if bi < bl {
			bi++
		}
		if ti < tl {
			ti++
		}
		b0 = bi
		t0 = ti
	}
	if base[b0:bi] == ".." {
		return "", errors.New("Rel: can't make " + targPath + " relative to " + basePath)
	}
	if b0 != bl {
		// 还剩下 base 元素。必须在向下之前先向上。
		seps := bytealg.CountString(base[b0:bl], Separator)
		size := 2 + seps*3
		if tl != t0 {
			size += 1 + tl - t0
		}
		buf := make([]byte, size)
		n := copy(buf, "..")
		for i := 0; i < seps; i++ {
			buf[n] = Separator
			copy(buf[n+1:], "..")
			n += 3
		}
		if t0 != tl {
			buf[n] = Separator
			copy(buf[n+1:], targ[t0:])
		}
		return Clean(string(buf)), nil
	}
	return targ[t0:], nil
}

// SkipDir 用作 [WalkFunc] 的返回值，以指示
// 调用中指定的目录应被跳过。它不会
// 被任何函数作为错误返回。
var SkipDir error = fs.SkipDir

// SkipAll 用作 [WalkFunc] 的返回值，以指示
// 所有剩余的文件和目录都应被跳过。它不会
// 被任何函数作为错误返回。
var SkipAll error = fs.SkipAll

// WalkFunc 是 [Walk] 为访问每个文件或目录
// 而调用的函数类型。
//
// path 参数包含作为 Walk 参数的路径作为前缀。
// 也就是说，如果 Walk 以根参数 "dir" 调用，并在该目录中
// 找到一个名为 "a" 的文件，则 walk 函数将以
// 参数 "dir/a" 被调用。
//
// 目录和文件通过 Join 连接，这可能会清理
// 目录名：如果 Walk 以根参数 "x/../dir" 调用，
// 并在该目录中找到一个名为 "a" 的文件，则 walk 函数
// 将以参数 "dir/a" 被调用，而不是 "x/../dir/a"。
//
// info 参数是命名路径的 fs.FileInfo。
//
// 函数返回的错误结果控制 Walk 如何继续。
// 如果函数返回特殊值 [SkipDir]，Walk 会跳过
// 当前目录（若 info.IsDir() 为 true 则为 path，
// 否则为 path 的父目录）。如果函数返回特殊值 [SkipAll]，
// Walk 会跳过所有剩余的文件和目录。否则，如果函数
// 返回非 nil 错误，Walk 会完全停止并返回该错误。
//
// err 参数报告与 path 相关的错误，表明 Walk
// 不会进入该目录。函数可以决定如何
// 处理该错误；如前所述，返回该错误将
// 导致 Walk 停止遍历整棵树。
//
// Walk 在两种情况下会以非 nil 的 err 参数调用该函数。
//
// 首先，如果对根目录或树中的任何目录或文件
// 执行 [os.Lstat] 失败，Walk 会以 path 设为该
// 目录或文件的路径、info 设为 nil、err 设为
// os.Lstat 返回的错误来调用该函数。
//
// 其次，如果某个目录的 Readdirnames 方法失败，Walk 会
// 以 path 设为该目录的路径、info 设为描述该目录的
// [fs.FileInfo]、err 设为 Readdirnames 返回的错误
// 来调用该函数。
type WalkFunc func(path string, info fs.FileInfo, err error) error

var lstat = os.Lstat // 用于测试

// walkDir 递归下降 path，调用 walkDirFn。
func walkDir(path string, d fs.DirEntry, walkDirFn fs.WalkDirFunc) error {
	if err := walkDirFn(path, d, nil); err != nil || !d.IsDir() {
		if err == SkipDir && d.IsDir() {
			// 成功跳过目录。
			err = nil
		}
		return err
	}

	dirs, err := os.ReadDir(path)
	if err != nil {
		// 第二次调用，用于报告 ReadDir 错误。
		err = walkDirFn(path, d, err)
		if err != nil {
			if err == SkipDir && d.IsDir() {
				err = nil
			}
			return err
		}
	}

	for _, d1 := range dirs {
		path1 := Join(path, d1.Name())
		if err := walkDir(path1, d1, walkDirFn); err != nil {
			if err == SkipDir {
				break
			}
			return err
		}
	}
	return nil
}

// walk 递归下降 path，调用 walkFn。
func walk(path string, info fs.FileInfo, walkFn WalkFunc) error {
	if !info.IsDir() {
		return walkFn(path, info, nil)
	}

	names, err := readDirNames(path)
	err1 := walkFn(path, info, err)
	// 如果 err != nil，walk 无法进入此目录。
	// err1 != nil 意味着 walkFn 希望 walk 跳过此目录或停止遍历。
	// 因此，如果 err 和 err1 中有一个不为 nil，walk 将返回。
	if err != nil || err1 != nil {
		// 调用方的行为由返回值控制，该返回值由
		// walkFn 决定。walkFn 可以忽略 err 并返回 nil。
		// 如果 walkFn 返回 SkipDir 或 SkipAll，它将被调用方处理。
		// 所以 walk 应返回 walkFn 返回的任何值。
		return err1
	}

	for _, name := range names {
		filename := Join(path, name)
		fileInfo, err := lstat(filename)
		if err != nil {
			if err := walkFn(filename, fileInfo, err); err != nil && err != SkipDir {
				return err
			}
		} else {
			err = walk(filename, fileInfo, walkFn)
			if err != nil {
				if !fileInfo.IsDir() || err != SkipDir {
					return err
				}
			}
		}
	}
	return nil
}

// WalkDir 遍历以 root 为根的文件树，为树中的每个文件或目录
// 调用 fn，包括 root。
//
// 访问文件和目录时产生的所有错误都由 fn 过滤：
// 详情参见 [fs.WalkDirFunc] 文档。
//
// 文件按字典序遍历，这使输出具有确定性，
// 但要求 WalkDir 在开始遍历该目录之前
// 将整个目录读入内存。
//
// WalkDir 不跟随符号链接。
//
// WalkDir 调用 fn 时使用的路径采用适合操作系统
// 的分隔符字符。这与 [io/fs.WalkDir] 不同，后者始终
// 使用以斜杠分隔的路径。
func WalkDir(root string, fn fs.WalkDirFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walkDir(root, fs.FileInfoToDirEntry(info), fn)
	}
	if err == SkipDir || err == SkipAll {
		return nil
	}
	return err
}

// Walk 遍历以 root 为根的文件树，为树中的每个文件或目录
// 调用 fn，包括 root。
//
// 访问文件和目录时产生的所有错误都由 fn 过滤：
// 详情参见 [WalkFunc] 文档。
//
// 文件按字典序遍历，这使输出具有确定性，
// 但要求 Walk 在开始遍历该目录之前
// 将整个目录读入内存。
//
// Walk 不跟随符号链接。
//
// Walk 的效率低于 Go 1.16 引入的 [WalkDir]，
// 后者避免对每个访问的文件或目录调用 os.Lstat。
func Walk(root string, fn WalkFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walk(root, info, fn)
	}
	if err == SkipDir || err == SkipAll {
		return nil
	}
	return err
}

// readDirNames 读取 dirname 指定的目录，并返回
// 排序后的目录项名称列表。
func readDirNames(dirname string) ([]string, error) {
	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
}

// Base 返回 path 的最后一个元素。
// 在提取最后一个元素之前会移除尾部路径分隔符。
// 如果 path 为空，Base 返回 "."。
// 如果 path 完全由分隔符组成，Base 返回单个分隔符。
func Base(path string) string {
	return filepathlite.Base(path)
}

// Dir 返回 path 中除最后一个元素外的所有部分，通常就是 path 的目录。
// 去掉最后一个元素后，Dir 会对 path 调用 [Clean] 并
// 移除尾部斜杠。
// 如果 path 为空，Dir 返回 "."。
// 如果 path 完全由分隔符组成，Dir 返回单个分隔符。
// 返回的路径不以分隔符结尾，除非它是根目录。
func Dir(path string) string {
	return filepathlite.Dir(path)
}

// VolumeName 返回前导卷名。
// 给定 "C:\foo\bar"，在 Windows 上它返回 "C:"。
// 给定 "\\host\share\foo"，它返回 "\\host\share"。
// 在其他平台上它返回 ""。
func VolumeName(path string) string {
	return filepathlite.VolumeName(path)
}
