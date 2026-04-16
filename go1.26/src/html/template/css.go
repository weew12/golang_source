// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// endsWithCSSKeyword 报告 b 是否以一个不区分大小写地匹配小写 kw 的标识符结尾。
func endsWithCSSKeyword(b []byte, kw string) bool {
	i := len(b) - len(kw)
	if i < 0 {
		// 太短。
		return false
	}
	if i != 0 {
		r, _ := utf8.DecodeLastRune(b[:i])
		if isCSSNmchar(r) {
			// 太长。
			return false
		}
	}
	// 许多 CSS 关键字（如 "!important"）可以包含编码字符，
	// 但根据 https://www.w3.org/TR/css3-syntax/#TOK-URI ，URI 产生式不允许这样做。
	// 此函数不尝试识别编码后的关键字。例如，
	// 给定 "\75\72\6c" 和 "url"，此函数返回 false。
	return string(bytes.ToLower(b[i:])) == kw
}

// isCSSNmchar 报告该 rune 是否允许出现在 CSS 标识符的任何位置。
func isCSSNmchar(r rune) bool {
	// 基于 CSS3 nmchar 产生式，但忽略多 rune 转义序列。
	// https://www.w3.org/TR/css3-syntax/#SUBTOK-nmchar
	return 'a' <= r && r <= 'z' ||
		'A' <= r && r <= 'Z' ||
		'0' <= r && r <= '9' ||
		r == '-' ||
		r == '_' ||
		// 以下为非 ASCII 情况。
		0x80 <= r && r <= 0xd7ff ||
		0xe000 <= r && r <= 0xfffd ||
		0x10000 <= r && r <= 0x10ffff
}

// decodeCSS 对给定的 stringchar 序列解码 CSS3 转义。
// 如果没有变化，返回输入；否则返回由新数组支持的切片。
// https://www.w3.org/TR/css3-syntax/#SUBTOK-stringchar 定义了 stringchar。
func decodeCSS(s []byte) []byte {
	i := bytes.IndexByte(s, '\\')
	if i == -1 {
		return s
	}
	// 码点的 UTF-8 序列长度永远不会超过 1 加上表示该码点所需的
	// 十六进制位数，因此 len(s) 是输出长度的上界。
	b := make([]byte, 0, len(s))
	for len(s) != 0 {
		i := bytes.IndexByte(s, '\\')
		if i == -1 {
			i = len(s)
		}
		b, s = append(b, s[:i]...), s[i:]
		if len(s) < 2 {
			break
		}
		// https://www.w3.org/TR/css3-syntax/#SUBTOK-escape
		// escape ::= unicode | '\' [#x20-#x7E#x80-#xD7FF#xE000-#xFFFD#x10000-#x10FFFF]
		if isHex(s[1]) {
			// https://www.w3.org/TR/css3-syntax/#SUBTOK-unicode
			//   unicode ::= '\' [0-9a-fA-F]{1,6} wc?
			j := 2
			for j < len(s) && j < 7 && isHex(s[j]) {
				j++
			}
			r := hexDecode(s[1:j])
			if r > unicode.MaxRune {
				r, j = r/16, j-1
			}
			n := utf8.EncodeRune(b[len(b):cap(b)], r)
			// 末尾的可选空格允许十六进制序列后面跟随字面十六进制字符。
			// string(decodeCSS([]byte(`\A B`))) == "\nB"
			b, s = b[:len(b)+n], skipCSSSpace(s[j:])
		} else {
			// `\\` 解码为 `\`，`\"` 解码为 `"`。
			_, n := utf8.DecodeRune(s[1:])
			b, s = append(b, s[1:1+n]...), s[1+n:]
		}
	}
	return b
}

// isHex 报告给定字符是否是十六进制数字。
func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// hexDecode 解码短十六进制数字序列："10" -> 16。
func hexDecode(s []byte) rune {
	n := '\x00'
	for _, c := range s {
		n <<= 4
		switch {
		case '0' <= c && c <= '9':
			n |= rune(c - '0')
		case 'a' <= c && c <= 'f':
			n |= rune(c-'a') + 10
		case 'A' <= c && c <= 'F':
			n |= rune(c-'A') + 10
		default:
			panic(fmt.Sprintf("Bad hex digit in %q", s))
		}
	}
	return n
}

// skipCSSSpace 返回 c 的后缀，跳过单个空白字符。
func skipCSSSpace(c []byte) []byte {
	if len(c) == 0 {
		return c
	}
	// wc ::= #x9 | #xA | #xC | #xD | #x20
	switch c[0] {
	case '\t', '\n', '\f', ' ':
		return c[1:]
	case '\r':
		// 这与 CSS3 的 wc 产生式不同，因为后者包含一个可能的规范错误，
		// 即 wc 包含 nl（换行）中的所有单字节序列但不包含 CRLF。
		if len(c) >= 2 && c[1] == '\n' {
			return c[2:]
		}
		return c[1:]
	}
	return c
}

// isCSSSpace 报告 b 是否是 wc 中定义的 CSS 空白字符。
func isCSSSpace(b byte) bool {
	switch b {
	case '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// cssEscaper 使用 \<hex>+ 转义来转义 HTML 和 CSS 特殊字符。
func cssEscaper(args ...any) string {
	s, _ := stringify(args...)
	var b strings.Builder
	r, w, written := rune(0), 0, 0
	for i := 0; i < len(s); i += w {
		// 参见 htmlEscaper 中的注释。
		r, w = utf8.DecodeRuneInString(s[i:])
		var repl string
		switch {
		case int(r) < len(cssReplacementTable) && cssReplacementTable[r] != "":
			repl = cssReplacementTable[r]
		default:
			continue
		}
		if written == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[written:i])
		b.WriteString(repl)
		written = i + w
		if repl != `\\` && (written == len(s) || isHex(s[written]) || isCSSSpace(s[written])) {
			b.WriteByte(' ')
		}
	}
	if written == 0 {
		return s
	}
	b.WriteString(s[written:])
	return b.String()
}

var cssReplacementTable = []string{
	0:    `\0`,
	'\t': `\9`,
	'\n': `\a`,
	'\f': `\c`,
	'\r': `\d`,
	// 将 HTML 特殊字符编码为十六进制，以便输出可以嵌入到
	// HTML 属性中而无需进一步编码。
	'"':  `\22`,
	'&':  `\26`,
	'\'': `\27`,
	'(':  `\28`,
	')':  `\29`,
	'+':  `\2b`,
	'/':  `\2f`,
	':':  `\3a`,
	';':  `\3b`,
	'<':  `\3c`,
	'>':  `\3e`,
	'\\': `\\`,
	'{':  `\7b`,
	'}':  `\7d`,
}

var expressionBytes = []byte("expression")
var mozBindingBytes = []byte("mozbinding")

// cssValueFilter 允许输出中包含无害的 CSS 值，包括 CSS 数量（10px 或 25%）、
// ID 或 class 字面量（#foo、.bar）、关键字值（inherit、blue）和颜色（#888）。
// 它过滤掉不安全的值，例如那些影响 token 边界的值，
// 以及任何可能执行脚本的内容。
func cssValueFilter(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeCSS {
		return s
	}
	b, id := decodeCSS([]byte(s)), make([]byte, 0, 64)

	// CSS3 错误处理规定要遵循字符串边界，
	// 参见 https://www.w3.org/TR/css3-syntax/#error-handling ：
	//     格式错误的声明。用户代理必须通过读取到声明末尾来处理解析声明时
	//     遇到的意外 token，同时遵守 ()、[]、{}、""、'' 的匹配对规则，
	//     并正确处理转义。例如，格式错误的声明可能缺少属性、冒号 (:) 或值。
	// 因此我们需要确保值中没有不匹配的括号或引号字符，
	// 以防止浏览器在可能嵌入 JavaScript 源码的字符串内部重新开始解析。
	for i, c := range b {
		switch c {
		case 0, '"', '\'', '(', ')', '/', ';', '@', '[', '\\', ']', '`', '{', '}', '<', '>':
			return filterFailsafe
		case '-':
			// 禁止 <!-- 或 -->。
			// -- 不应出现在有效标识符中。
			if i != 0 && b[i-1] == '-' {
				return filterFailsafe
			}
		default:
			if c < utf8.RuneSelf && isCSSNmchar(rune(c)) {
				id = append(id, c)
			}
		}
	}
	id = bytes.ToLower(id)
	if bytes.Contains(id, expressionBytes) || bytes.Contains(id, mozBindingBytes) {
		return filterFailsafe
	}
	return string(b)
}
