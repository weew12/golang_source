// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package path

import (
	"errors"
	"internal/bytealg"
	"unicode/utf8"
)

// ErrBadPattern 表示模式格式错误。
var ErrBadPattern = errors.New("syntax error in pattern")

// Match 报告 name 是否匹配 shell 模式。
// 模式语法为：
//
//	pattern:
//		{ term }
//	term:
//		'*'         匹配任意非 / 字符序列
//		'?'         匹配任意单个非 / 字符
//		'[' [ '^' ] { character-range } ']'
//		            字符类（必须非空）
//		c           匹配字符 c (c != '*', '?', '\\', '[')
//		'\\' c      匹配字符 c
//
//	character-range:
//		c           匹配字符 c (c != '\\', '-', ']')
//		'\\' c      匹配字符 c
//		lo '-' hi   对于 lo <= c <= hi 匹配字符 c
//
// Match 要求 pattern 匹配整个 name，而不仅仅是子串。
// 唯一可能返回的错误是 [ErrBadPattern]，当 pattern
// 格式错误时。
func Match(pattern, name string) (matched bool, err error) {
Pattern:
	for len(pattern) > 0 {
		var star bool
		var chunk string
		star, chunk, pattern = scanChunk(pattern)
		if star && chunk == "" {
			// 结尾的 * 匹配字符串剩余部分，除非其中含有 /。
			return bytealg.IndexByteString(name, '/') < 0, nil
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
			for i := 0; i < len(name) && name[i] != '/'; i++ {
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
		// 在无错误地返回 false 之前，
		// 检查 pattern 的剩余部分在语法上是否有效。
		for len(pattern) > 0 {
			_, chunk, pattern = scanChunk(pattern)
			if _, _, err := matchChunk(chunk, ""); err != nil {
				return false, err
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
			if i+1 < len(pattern) {
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
				failed = s[0] == '/'
				_, n := utf8.DecodeRuneInString(s)
				s = s[n:]
			}
			chunk = chunk[1:]

		case '\\':
			chunk = chunk[1:]
			if len(chunk) == 0 {
				return "", false, ErrBadPattern
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
	if chunk[0] == '\\' {
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
