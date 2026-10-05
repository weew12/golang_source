// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"unicode"
	"unicode/utf8"
)

// foldName 返回一个折叠字符串，使得 foldName(x) == foldName(y)
// 类似于 strings.EqualFold(x, y)，但忽略下划线和连字符。
// 这使 foldName 能够匹配常见的命名约定。
func foldName(in []byte) []byte {
	// 这可内联，以利用 "function outlining"。
	// 参见 https://blog.filippo.io/efficient-go-apis-with-the-inliner/
	var arr [32]byte // 足以容纳大多数 JSON 名称
	return appendFoldedName(arr[:0], in)
}
func appendFoldedName(out, in []byte) []byte {
	for i := 0; i < len(in); {
		// 处理单字节 ASCII。
		if c := in[i]; c < utf8.RuneSelf {
			if c != '_' && c != '-' {
				if 'a' <= c && c <= 'z' {
					c -= 'a' - 'A'
				}
				out = append(out, c)
			}
			i++
			continue
		}
		// 处理多字节 Unicode。
		r, n := utf8.DecodeRune(in[i:])
		out = utf8.AppendRune(out, foldRune(r))
		i += n
	}
	return out
}

// foldRune 是 unicode.SimpleFold 的一个变体，对于同一折叠集合中的
// 所有 rune，它返回相同的 rune。
//
// 不变式：
//
//	foldRune(x) == foldRune(y) ⇔ strings.EqualFold(string(x), string(y))
func foldRune(r rune) rune {
	for {
		r2 := unicode.SimpleFold(r)
		if r2 <= r {
			return r2 // 折叠集中最小的字符
		}
		r = r2
	}
}
