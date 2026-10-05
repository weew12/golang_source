// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.jsonv2

package json

import "bytes"

// HTMLEscape 将经过 JSON 编码的 src 追加到 dst，其中字符串字面量内的
// <、>、&、U+2028 和 U+2029 字符被改为 \u003c、\u003e、\u0026、\u2028、\u2029，
// 以便 JSON 可以安全地嵌入到 HTML <script> 标签内。
// 出于历史原因，Web 浏览器不遵循 <script> 标签内的标准 HTML 转义，
// 因此必须使用另一种 JSON 编码。
func HTMLEscape(dst *bytes.Buffer, src []byte) {
	dst.Grow(len(src))
	dst.Write(appendHTMLEscape(dst.AvailableBuffer(), src))
}

func appendHTMLEscape(dst, src []byte) []byte {
	// 这些字符只能出现在字符串字面量中，
	// 因此只需一次一个字节地扫描字符串。
	start := 0
	for i, c := range src {
		if c == '<' || c == '>' || c == '&' {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			start = i + 1
		}
		// 转换 U+2028 和 U+2029（E2 80 A8 和 E2 80 A9）。
		if c == 0xE2 && i+2 < len(src) && src[i+1] == 0x80 && src[i+2]&^1 == 0xA8 {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hex[src[i+2]&0xF])
			start = i + len("\u2029")
		}
	}
	return append(dst, src[start:]...)
}

// Compact 将经过 JSON 编码的 src（去掉无意义的空白字符）追加到 dst。
func Compact(dst *bytes.Buffer, src []byte) error {
	dst.Grow(len(src))
	b := dst.AvailableBuffer()
	b, err := appendCompact(b, src, false)
	dst.Write(b)
	return err
}

func appendCompact(dst, src []byte, escape bool) ([]byte, error) {
	origLen := len(dst)
	scan := newScanner()
	defer freeScanner(scan)
	start := 0
	for i, c := range src {
		if escape && (c == '<' || c == '>' || c == '&') {
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			start = i + 1
		}
		// 转换 U+2028 和 U+2029（E2 80 A8 和 E2 80 A9）。
		if escape && c == 0xE2 && i+2 < len(src) && src[i+1] == 0x80 && src[i+2]&^1 == 0xA8 {
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			dst = append(dst, '\\', 'u', '2', '0', '2', hex[src[i+2]&0xF])
			start = i + 3
		}
		v := scan.step(scan, c)
		if v >= scanSkipSpace {
			if v == scanError {
				break
			}
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			start = i + 1
		}
	}
	if scan.eof() == scanError {
		return dst[:origLen], scan.err
	}
	if start < len(src) {
		dst = append(dst, src[start:]...)
	}
	return dst, nil
}

func appendNewline(dst []byte, prefix, indent string, depth int) []byte {
	dst = append(dst, '\n')
	dst = append(dst, prefix...)
	for i := 0; i < depth; i++ {
		dst = append(dst, indent...)
	}
	return dst
}

// indentGrowthFactor 指定对 JSON 输入进行缩进时的增长因子。
// 经验测量表明，对于一些压缩后的 JSON（缩进为单个制表符），
// 增长因子在 1.4x 到 1.8x 之间。
// 指定一个略大于观测值的增长因子，
// 以降低 appendIndent 中分配内存的概率。
// 不超过 2 的因子可确保浪费的空间永远不超过 50%。
const indentGrowthFactor = 2

// Indent 将经过 JSON 编码的 src 的缩进形式追加到 dst。
// JSON 对象或数组中的每个元素都从新的缩进行开始，
// 该行以 prefix 开头，后跟一个或多个 indent 副本（根据缩进嵌套层级而定）。
// 追加到 dst 的数据不以 prefix 或任何缩进开头，以便更容易嵌入到其他格式化的 JSON 数据中。
// 尽管 src 开头的前导空白字符（空格、制表符、回车、换行）会被丢弃，
// 但 src 末尾的尾随空白字符会被保留并复制到 dst。
// 例如，如果 src 没有尾随空格，dst 也没有；
// 如果 src 以尾随换行结尾，dst 也会以尾随换行结尾。
func Indent(dst *bytes.Buffer, src []byte, prefix, indent string) error {
	dst.Grow(indentGrowthFactor * len(src))
	b := dst.AvailableBuffer()
	b, err := appendIndent(b, src, prefix, indent)
	dst.Write(b)
	return err
}

func appendIndent(dst, src []byte, prefix, indent string) ([]byte, error) {
	origLen := len(dst)
	scan := newScanner()
	defer freeScanner(scan)
	needIndent := false
	depth := 0
	for _, c := range src {
		scan.bytes++
		v := scan.step(scan, c)
		if v == scanSkipSpace {
			continue
		}
		if v == scanError {
			break
		}
		if needIndent && v != scanEndObject && v != scanEndArray {
			needIndent = false
			depth++
			dst = appendNewline(dst, prefix, indent, depth)
		}

		// 输出语义上不重要的字节
		// （特别是字符串中的标点符号），保持原样。
		if v == scanContinue {
			dst = append(dst, c)
			continue
		}

		// 在真正的标点符号周围添加空格。
		switch c {
		case '{', '[':
			// 延迟缩进，以便空对象和空数组被格式化为 {} 和 []。
			needIndent = true
			dst = append(dst, c)
		case ',':
			dst = append(dst, c)
			dst = appendNewline(dst, prefix, indent, depth)
		case ':':
			dst = append(dst, c, ' ')
		case '}', ']':
			if needIndent {
				// 抑制空对象/数组中的缩进
				needIndent = false
			} else {
				depth--
				dst = appendNewline(dst, prefix, indent, depth)
			}
			dst = append(dst, c)
		default:
			dst = append(dst, c)
		}
	}
	if scan.eof() == scanError {
		return dst[:origLen], scan.err
	}
	return dst, nil
}
