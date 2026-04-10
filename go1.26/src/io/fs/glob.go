// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"path"
)

// GlobFS 是具备 Glob 方法的文件系统接口。
type GlobFS interface {
	FS

	// Glob 返回所有匹配模式 pattern 的文件名称，
	// 为顶层 Glob 函数提供具体实现。
	Glob(pattern string) ([]string, error)
}

// Glob 返回所有匹配模式 pattern 的文件名称；
// 若不存在匹配文件，则返回 nil。
// 模式的语法与 [path.Match] 完全一致。
// 模式可描述层级化路径名称，例如 usr/*/bin/ed。
//
// Glob 会忽略文件系统错误（如读取目录时的 I/O 错误）。
// 唯一可能返回的错误是 [path.ErrBadPattern] ，用于表示模式格式非法。
//
// 若 fs 实现了 [GlobFS] 接口，Glob 会直接调用 fs.Glob。
// 否则，Glob 会使用 [ReadDir] 遍历目录树，查找匹配模式的文件。
func Glob(fsys FS, pattern string) (matches []string, err error) {
	return globWithLimit(fsys, pattern, 0)
}

func globWithLimit(fsys FS, pattern string, depth int) (matches []string, err error) {
	// 添加此限制是为了防止栈溢出问题。详见
	// CVE-2022-30630。
	const pathSeparatorsLimit = 10000
	if depth > pathSeparatorsLimit {
		return nil, path.ErrBadPattern
	}
	if fsys, ok := fsys.(GlobFS); ok {
		return fsys.Glob(pattern)
	}

	// 检查模式格式是否合法。
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	if !hasMeta(pattern) {
		if _, err = Stat(fsys, pattern); err != nil {
			return nil, nil
		}
		return []string{pattern}, nil
	}

	dir, file := path.Split(pattern)
	dir = cleanGlobPath(dir)

	if !hasMeta(dir) {
		return glob(fsys, dir, file, nil)
	}

	// 防止无限递归。详见 issue 15879。
	if dir == pattern {
		return nil, path.ErrBadPattern
	}

	var m []string
	m, err = globWithLimit(fsys, dir, depth+1)
	if err != nil {
		return nil, err
	}
	for _, d := range m {
		matches, err = glob(fsys, d, file, matches)
		if err != nil {
			return
		}
	}
	return
}

// cleanGlobPath 为通配符匹配预处理路径。
func cleanGlobPath(path string) string {
	switch path {
	case "":
		return "."
	default:
		return path[0 : len(path)-1] // 切除末尾的路径分隔符
	}
}

// glob 在目录 dir 中搜索匹配模式 pattern 的文件，
// 并将其追加到 matches 切片中，返回更新后的切片。
// 若无法打开目录，glob 会返回原有的 matches 结果。
// 新增的匹配项按字典序添加。
func glob(fs FS, dir, pattern string, matches []string) (m []string, e error) {
	m = matches
	infos, err := ReadDir(fs, dir)
	if err != nil {
		return // 忽略 I/O 错误
	}

	for _, info := range infos {
		n := info.Name()
		matched, err := path.Match(pattern, n)
		if err != nil {
			return m, err
		}
		if matched {
			m = append(m, path.Join(dir, n))
		}
	}
	return
}

// hasMeta 判断路径是否包含 path.Match 可识别的任意通配符特殊字符。
func hasMeta(path string) bool {
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '*', '?', '[', '\\':
			return true
		}
	}
	return false
}
