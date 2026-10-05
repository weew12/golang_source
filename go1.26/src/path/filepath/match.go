// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filepath

import (
	"errors"
	"internal/filepathlite"
	"os"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"
)

// ErrBadPattern 表示模式格式错误。
var ErrBadPattern = errors.New("syntax error in pattern")

// Match 报告 name 是否匹配 shell 文件名模式。
// 模式语法为：
//
//	pattern:
//		{ term }
//	term:
//		'*'         匹配任意非 Separator 字符序列
//		'?'         匹配任意单个非 Separator 字符
//		'[' [ '^' ] { character-range } ']'
//		            字符类（必须非空）
//		c           匹配字符 c (c != '*', '?', '\\', '[')
//		'\\' c      匹配字符 c （Windows 上除外）
//
//	character-range:
//		c           匹配字符 c (c != '\\', '-', ']')
//		'\\' c      匹配字符 c （Windows 上除外）
//		lo '-' hi   对于 lo <= c <= hi 匹配字符 c
//
// 模式中的路径段必须用 [Separator] 分隔。
//
// Match 要求 pattern 匹配整个 name，而不仅仅是子串。
// 唯一可能返回的错误是 [ErrBadPattern]，当 pattern
// 格式错误时。
//
// 在 Windows 上，转义被禁用。取而代之的是，'\\' 被视为
// 路径分隔符。
func Match(pattern, name string) (matched bool, err error) {
Pattern:
	for len(pattern) > 0 {
		var star bool
		var chunk string
		star, chunk, pattern = scanChunk(pattern)
		if star && chunk == "" {
			// 结尾的 * 匹配字符串剩余部分，除非其中含有 /。
			return !strings.Contains(name, string(Separator)), nil
		}
		// 在当前位置查找匹配。
		t, ok, err := matchChunk(chunk, name)
		// 如果这是最后一个块，确保我们已经用尽了 name，
		// 否则，即使仍可通过星号完成匹配，
		// 也会得到错误的结果
		if ok && (len(t) == 0 || len(pattern) > 0) {
			name = t
			continue
		}
		if err != nil {
			return false, err
		}
		if star {
			// 尝试跳过 i+1 个字节进行匹配。
			// 不能跳过 /。
			for i := 0; i < len(name) && name[i] != Separator; i++ {
				t, ok, err := matchChunk(chunk, name[i+1:])
				if ok {
					// 如果这是最后一个块，确保我们已经用尽了 name
					if len(pattern) == 0 && len(t) > 0 {
						continue
					}
					name = t
					continue Pattern
				}
				if err != nil {
					return false, err
				}
			}
		}
		return false, nil
	}
	return len(name) == 0, nil
}

// scanChunk 获取 pattern 的下一个片段，它是一个非星号字符串，
// 之前可能带有一个星号。
func scanChunk(pattern string) (star bool, chunk, rest string) {
	for len(pattern) > 0 && pattern[0] == '*' {
		pattern = pattern[1:]
		star = true
	}
	inrange := false
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			// 错误检查在 matchChunk 中处理：坏模式。
			if runtime.GOOS != "windows" && i+1 < len(pattern) {
				i++
			}
		case '[':
			inrange = true
		case ']':
			inrange = false
		case '*':
			if !inrange {
				return star, pattern[:i], pattern[i:]
			}
		}
	}
	return star, pattern, ""
}

// matchChunk 检查 chunk 是否匹配 s 的开头。
// 如果是，它返回 s 的剩余部分（匹配之后的部分）。
// Chunk 由所有单字符操作符组成：字面量、字符类和 ?。
func matchChunk(chunk, s string) (rest string, ok bool, err error) {
	// failed 记录匹配是否已失败。
	// 匹配失败后，循环继续处理 chunk，
	// 检查模式是否格式良好，但不再读取 s。
	failed := false
	for len(chunk) > 0 {
		failed = failed || len(s) == 0
		switch chunk[0] {
		case '[':
			// 字符类
			var r rune
			if !failed {
				var n int
				r, n = utf8.DecodeRuneInString(s)
				s = s[n:]
			}
			chunk = chunk[1:]
			// 可能取反
			negated := false
			if len(chunk) > 0 && chunk[0] == '^' {
				negated = true
				chunk = chunk[1:]
			}
			// 解析所有范围
			match := false
			nrange := 0
			for {
				if len(chunk) > 0 && chunk[0] == ']' && nrange > 0 {
					chunk = chunk[1:]
					break
				}
				var lo, hi rune
				if lo, chunk, err = getEsc(chunk); err != nil {
					return "", false, err
				}
				hi = lo
				if chunk[0] == '-' {
					if hi, chunk, err = getEsc(chunk[1:]); err != nil {
						return "", false, err
					}
				}
				match = match || lo <= r && r <= hi
				nrange++
			}
			failed = failed || match == negated

		case '?':
			if !failed {
				failed = s[0] == Separator
				_, n := utf8.DecodeRuneInString(s)
				s = s[n:]
			}
			chunk = chunk[1:]

		case '\\':
			if runtime.GOOS != "windows" {
				chunk = chunk[1:]
				if len(chunk) == 0 {
					return "", false, ErrBadPattern
				}
			}
			fallthrough

		default:
			if !failed {
				failed = chunk[0] != s[0]
				s = s[1:]
			}
			chunk = chunk[1:]
		}
	}
	if failed {
		return "", false, nil
	}
	return s, true, nil
}

// getEsc 从 chunk 中获取一个可能被转义的字符，用于字符类。
func getEsc(chunk string) (r rune, nchunk string, err error) {
	if len(chunk) == 0 || chunk[0] == '-' || chunk[0] == ']' {
		err = ErrBadPattern
		return
	}
	if chunk[0] == '\\' && runtime.GOOS != "windows" {
		chunk = chunk[1:]
		if len(chunk) == 0 {
			err = ErrBadPattern
			return
		}
	}
	r, n := utf8.DecodeRuneInString(chunk)
	if r == utf8.RuneError && n == 1 {
		err = ErrBadPattern
	}
	nchunk = chunk[n:]
	if len(nchunk) == 0 {
		err = ErrBadPattern
	}
	return
}

// Glob 返回所有匹配 pattern 的文件名，如果没有匹配的文件
// 则返回 nil。模式的语法与 [Match] 中相同。
// 模式可以描述层级名称，例如
// /usr/*/bin/ed（假设 [Separator] 为 '/'）。
//
// Glob 忽略文件系统错误，例如读取目录时的 I/O 错误。
// 唯一可能返回的错误是 [ErrBadPattern]，当 pattern
// 格式错误时。
func Glob(pattern string) (matches []string, err error) {
	return globWithLimit(pattern, 0)
}

func globWithLimit(pattern string, depth int) (matches []string, err error) {
	// 此限制用于防止栈耗尽问题。参见 CVE-2022-30632。
	const pathSeparatorsLimit = 10000
	if depth == pathSeparatorsLimit {
		return nil, ErrBadPattern
	}

	// 检查 pattern 格式是否良好。
	if _, err := Match(pattern, ""); err != nil {
		return nil, err
	}
	if !hasMeta(pattern) {
		if _, err = os.Lstat(pattern); err != nil {
			return nil, nil
		}
		return []string{pattern}, nil
	}

	dir, file := Split(pattern)
	volumeLen := 0
	if runtime.GOOS == "windows" {
		volumeLen, dir = cleanGlobPathWindows(dir)
	} else {
		dir = cleanGlobPath(dir)
	}

	if !hasMeta(dir[volumeLen:]) {
		return glob(dir, file, nil)
	}

	// 防止无限递归。参见 issue 15879。
	if dir == pattern {
		return nil, ErrBadPattern
	}

	var m []string
	m, err = globWithLimit(dir, depth+1)
	if err != nil {
		return
	}
	for _, d := range m {
		matches, err = glob(d, file, matches)
		if err != nil {
			return
		}
	}
	return
}

// cleanGlobPath 为 glob 匹配准备 path。
func cleanGlobPath(path string) string {
	switch path {
	case "":
		return "."
	case string(Separator):
		// 不对 path 做任何处理
		return path
	default:
		return path[0 : len(path)-1] // 去掉尾部分隔符
	}
}

// cleanGlobPathWindows 是 cleanGlobPath 的 windows 版本。
func cleanGlobPathWindows(path string) (prefixLen int, cleaned string) {
	vollen := filepathlite.VolumeNameLen(path)
	switch {
	case path == "":
		return 0, "."
	case vollen+1 == len(path) && os.IsPathSeparator(path[len(path)-1]): // /, \, C:\ and C:/
		// 不对 path 做任何处理
		return vollen + 1, path
	case vollen == len(path) && len(path) == 2: // C:
		return vollen, path + "." // 将 C: 转换为 C:.
	default:
		if vollen >= len(path) {
			vollen = len(path) - 1
		}
		return vollen, path[0 : len(path)-1] // 去掉尾部分隔符
	}
}

// glob 在目录 dir 中搜索匹配 pattern 的文件，
// 并将它们追加到 matches。如果目录无法
// 打开，它返回现有的 matches。新匹配
// 按字典序添加。
func glob(dir, pattern string, matches []string) (m []string, e error) {
	m = matches
	fi, err := os.Stat(dir)
	if err != nil {
		return // 忽略 I/O 错误
	}
	if !fi.IsDir() {
		return // 忽略 I/O 错误
	}
	d, err := os.Open(dir)
	if err != nil {
		return // 忽略 I/O 错误
	}
	defer d.Close()

	names, _ := d.Readdirnames(-1)
	slices.Sort(names)

	for _, n := range names {
		matched, err := Match(pattern, n)
		if err != nil {
			return m, err
		}
		if matched {
			m = append(m, Join(dir, n))
		}
	}
	return
}

// hasMeta 报告 path 是否包含 Match 所识别的
// 任意魔法字符。
func hasMeta(path string) bool {
	magicChars := `*?[`
	if runtime.GOOS != "windows" {
		magicChars = `*?[\`
	}
	return strings.ContainsAny(path, magicChars)
}
