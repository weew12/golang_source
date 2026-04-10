// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bytes

import (
	"iter"
	"unicode"
	"unicode/utf8"
)

// Lines 返回字节切片 s 中以换行符结尾的行的迭代器。
// 迭代器产生的行包含其结尾的换行符。
// 如果 s 为空，迭代器不会产生任何行。
// 如果 s 不以换行符结尾，最后产生的行将不以换行符结尾。
// 它返回一个单次使用的迭代器。
func Lines(s []byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		for len(s) > 0 {
			var line []byte
			if i := IndexByte(s, '\n'); i >= 0 {
				line, s = s[:i+1], s[i+1:]
			} else {
				line, s = s, nil
			}
			if !yield(line[:len(line):len(line)]) {
				return
			}
		}
	}
}

// splitSeq 是 SplitSeq 或 SplitAfterSeq，通过在结果中包含 sep 的字节数（不包含或全部包含）来配置。
func splitSeq(s, sep []byte, sepSave int) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		if len(sep) == 0 {
			for len(s) > 0 {
				_, size := utf8.DecodeRune(s)
				if !yield(s[:size:size]) {
					return
				}
				s = s[size:]
			}
			return
		}
		for {
			i := Index(s, sep)
			if i < 0 {
				break
			}
			frag := s[:i+sepSave]
			if !yield(frag[:len(frag):len(frag)]) {
				return
			}
			s = s[i+len(sep):]
		}
		yield(s[:len(s):len(s)])
	}
}

// SplitSeq 返回 s 中所有被 sep 分隔的子切片的迭代器。
// 迭代器产生的子切片与 [Split]
// 但不会构建一个包含这些子切片的新切片。
// 它返回一个单次使用的迭代器。
func SplitSeq(s, sep []byte) iter.Seq[[]byte] {
	return splitSeq(s, sep, 0)
}

// SplitAfterSeq 返回 s 中在每个 sep 实例后分割的子切片的迭代器。
// 迭代器产生的子切片与 [SplitAfter]
// 但不会构建一个包含这些子切片的新切片。
// 它返回一个单次使用的迭代器。
func SplitAfterSeq(s, sep []byte) iter.Seq[[]byte] {
	return splitSeq(s, sep, len(sep))
}

// FieldsSeq 返回 s 中围绕连续空白字符（由 [unicode.IsSpace] 定义）分割的子切片的迭代器。
// 迭代器产生的子切片与 [Fields]
// 但不会构建一个包含这些子切片的新切片。
func FieldsSeq(s []byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		start := -1
		for i := 0; i < len(s); {
			size := 1
			r := rune(s[i])
			isSpace := asciiSpace[s[i]] != 0
			if r >= utf8.RuneSelf {
				r, size = utf8.DecodeRune(s[i:])
				isSpace = unicode.IsSpace(r)
			}
			if isSpace {
				if start >= 0 {
					if !yield(s[start:i:i]) {
						return
					}
					start = -1
				}
			} else if start < 0 {
				start = i
			}
			i += size
		}
		if start >= 0 {
			yield(s[start:len(s):len(s)])
		}
	}
}

// FieldsFuncSeq 返回 s 中围绕连续满足 f(c) 的 Unicode 码点分割的子切片的迭代器。
// 迭代器产生的子切片与 [FieldsFunc]
// 但不会构建一个包含这些子切片的新切片。
func FieldsFuncSeq(s []byte, f func(rune) bool) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		start := -1
		for i := 0; i < len(s); {
			r, size := utf8.DecodeRune(s[i:])
			if f(r) {
				if start >= 0 {
					if !yield(s[start:i:i]) {
						return
					}
					start = -1
				}
			} else if start < 0 {
				start = i
			}
			i += size
		}
		if start >= 0 {
			yield(s[start:len(s):len(s)])
		}
	}
}
