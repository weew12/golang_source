// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package html 提供用于转义和反转义 HTML 文本的函数。
package html

import (
	"strings"
	"unicode/utf8"
)

// 这些替换允许与假定 Windows-1252 编码的旧数字实体保持兼容。
// https://html.spec.whatwg.org/multipage/parsing.html#numeric-character-reference-end-state
var replacementTable = [...]rune{
	'\u20AC', // 第一个条目是 0x80 应被替换为的字符。
	'\u0081',
	'\u201A',
	'\u0192',
	'\u201E',
	'\u2026',
	'\u2020',
	'\u2021',
	'\u02C6',
	'\u2030',
	'\u0160',
	'\u2039',
	'\u0152',
	'\u008D',
	'\u017D',
	'\u008F',
	'\u0090',
	'\u2018',
	'\u2019',
	'\u201C',
	'\u201D',
	'\u2022',
	'\u2013',
	'\u2014',
	'\u02DC',
	'\u2122',
	'\u0161',
	'\u203A',
	'\u0153',
	'\u009D',
	'\u017E',
	'\u0178', // 最后一个条目是 0x9F。
	// 0x00->'\uFFFD' 通过程序逻辑处理。
	// 0x0D->'\u000D' 是空操作。
}

// unescapeEntity 从 b[src:] 读取类似 "&lt;" 的实体，并将对应的 "<" 写入 b[dst:]，
// 返回递增后的 dst 和 src 游标。
// 前置条件：b[src] == '&' && dst <= src。
func unescapeEntity(b []byte, dst, src int, entity map[string]rune, entity2 map[string][2]rune) (dst1, src1 int) {
	const attribute = false

	// http://www.whatwg.org/specs/web-apps/current-work/multipage/tokenization.html#consume-a-character-reference

	// i 从 1 开始，因为我们已经知道 s[0] == '&'。
	i, s := 1, b[src:]

	if len(s) <= 1 {
		b[dst] = b[src]
		return dst + 1, src + 1
	}

	if s[i] == '#' {
		if len(s) <= 3 { // 我们至少需要 "&#."。
			b[dst] = b[src]
			return dst + 1, src + 1
		}
		i++
		c := s[i]
		hex := false
		if c == 'x' || c == 'X' {
			hex = true
			i++
		}

		x := '\x00'
		for i < len(s) {
			c = s[i]
			i++
			if hex {
				if '0' <= c && c <= '9' {
					x = 16*x + rune(c) - '0'
					continue
				} else if 'a' <= c && c <= 'f' {
					x = 16*x + rune(c) - 'a' + 10
					continue
				} else if 'A' <= c && c <= 'F' {
					x = 16*x + rune(c) - 'A' + 10
					continue
				}
			} else if '0' <= c && c <= '9' {
				x = 10*x + rune(c) - '0'
				continue
			}
			if c != ';' {
				i--
			}
			break
		}

		if i <= 3 { // 没有匹配到任何字符。
			b[dst] = b[src]
			return dst + 1, src + 1
		}

		if 0x80 <= x && x <= 0x9F {
			// 将 Windows-1252 字符替换为对应的 UTF-8 等效字符。
			x = replacementTable[x-0x80]
		} else if x == 0 || (0xD800 <= x && x <= 0xDFFF) || x > 0x10FFFF {
			// 将无效字符替换为替换字符。
			x = '\uFFFD'
		}

		return dst + utf8.EncodeRune(b[dst:], x), src + i
	}

	// 尽可能消耗最多的字符，使已消耗的字符匹配某个命名引用。

	for i < len(s) {
		c := s[i]
		i++
		// 小写字符在实体中更常见，所以我们优先检查它们。
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
			continue
		}
		if c != ';' {
			i--
		}
		break
	}

	entityName := s[1:i]
	if len(entityName) == 0 {
		// 空操作。
	} else if attribute && entityName[len(entityName)-1] != ';' && len(s) > i && s[i] == '=' {
		// 空操作。
	} else if x := entity[string(entityName)]; x != 0 {
		return dst + utf8.EncodeRune(b[dst:], x), src + i
	} else if x := entity2[string(entityName)]; x[0] != 0 {
		dst1 := dst + utf8.EncodeRune(b[dst:], x[0])
		return dst1 + utf8.EncodeRune(b[dst1:], x[1]), src + i
	} else if !attribute {
		maxLen := len(entityName) - 1
		if maxLen > longestEntityWithoutSemicolon {
			maxLen = longestEntityWithoutSemicolon
		}
		for j := maxLen; j > 1; j-- {
			if x := entity[string(entityName[:j])]; x != 0 {
				return dst + utf8.EncodeRune(b[dst:], x), src + j + 1
			}
		}
	}

	dst1, src1 = dst+i, src+i
	copy(b[dst:dst1], b[src:src1])
	return dst1, src1
}

var htmlEscaper = strings.NewReplacer(
	`&`, "&amp;",
	`'`, "&#39;", // "&#39;" 比 "&apos;" 更短，且 apos 直到 HTML5 才被加入 HTML。
	`<`, "&lt;",
	`>`, "&gt;",
	`"`, "&#34;", // "&#34;" 比 "&quot;" 更短。
)

// EscapeString 将特殊字符（如 "<"）转义为 "&lt;"。
// 它仅转义五个这样的字符：<、>、&、' 和 "。
// [UnescapeString](EscapeString(s)) == s 始终成立，但反过来并不总是成立。
func EscapeString(s string) string {
	return htmlEscaper.Replace(s)
}

// UnescapeString 将实体（如 "&lt;"）反转义为 "<"。它能反转义的实体范围
// 比 [EscapeString] 转义的范围更大。例如，"&aacute;" 会被反转义为 "á"，
// "&#225;" 和 "&#xE1;" 也是如此。
// UnescapeString([EscapeString](s)) == s 始终成立，但反过来并不总是成立。
func UnescapeString(s string) string {
	i := strings.IndexByte(s, '&')

	if i < 0 {
		return s
	}

	b := []byte(s)
	entity, entity2 := entityMaps()
	dst, src := unescapeEntity(b, i, i, entity, entity2)
	for len(s[src:]) > 0 {
		if s[src] == '&' {
			i = 0
		} else {
			i = strings.IndexByte(s[src:], '&')
		}
		if i < 0 {
			dst += copy(b[dst:], s[src:])
			break
		}

		if i > 0 {
			copy(b[dst:], s[src:src+i])
		}
		dst, src = unescapeEntity(b, dst+i, src+i, entity, entity2)
	}
	return string(b[:dst])
}
