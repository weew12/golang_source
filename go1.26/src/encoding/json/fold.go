// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.jsonv2

package json

import (
	"unicode"
	"unicode/utf8"
)

// foldName 返回一个折叠后的字符串，使得 foldName(x) == foldName(y)
// 与 bytes.EqualFold(x, y) 等价。
func foldName(in []byte) []byte {
	// 此函数可内联，以利用"函数外提"优化。
	var arr [32]byte // 足以容纳大多数 JSON 名称
	return appendFoldedName(arr[:0], in)
}

func appendFoldedName(out, in []byte) []byte {
	for i := 0; i < len(in); {
		// 处理单字节 ASCII。
		if c := in[i]; c < utf8.RuneSelf {
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			out = append(out, c)
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

// foldRune 返回同一折叠集中所有 rune 中最小的 rune。
func foldRune(r rune) rune {
	for {
		r2 := unicode.SimpleFold(r)
		if r2 <= r {
			return r2
		}
		r = r2
	}
}
