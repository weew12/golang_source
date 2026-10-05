// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"bytes"
	"strings"

	"encoding/json/jsontext"
)

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
	const hex = "0123456789abcdef"
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
	b, err := jsontext.AppendFormat(b, src,
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
		jsontext.PreserveRawStrings(true))
	if err != nil {
		return transformSyntacticError(err)
	}
	dst.Write(b)
	return nil
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
	// 在 v2 中，仅允许空格和制表符，而 v1 允许任意字符。
	dstLen := len(dst)
	if len(strings.Trim(prefix, " \t"))+len(strings.Trim(indent, " \t")) > 0 {
		// 使用长度正确的占位符空格，之后再替换。
		invalidPrefix, invalidIndent := prefix, indent
		prefix = strings.Repeat(" ", len(prefix))
		indent = strings.Repeat(" ", len(indent))
		defer func() {
			b := dst[dstLen:]
			for i := bytes.IndexByte(b, '\n'); i >= 0; i = bytes.IndexByte(b, '\n') {
				b = b[i+len("\n"):]
				n := len(b) - len(bytes.TrimLeft(b, " ")) // len(prefix)+n*len(indent)
				spaces := b[:n]
				spaces = spaces[copy(spaces, invalidPrefix):]
				for len(spaces) > 0 {
					spaces = spaces[copy(spaces, invalidIndent):]
				}
				b = b[n:]
			}
		}()
	}

	dst, err := jsontext.AppendFormat(dst, src,
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
		jsontext.PreserveRawStrings(true),
		jsontext.Multiline(true),
		jsontext.WithIndentPrefix(prefix),
		jsontext.WithIndent(indent))
	if err != nil {
		return dst[:dstLen], transformSyntacticError(err)
	}

	// 在 v2 中，尾随空白会被丢弃，而 v1 会保留它。
	if n := len(src) - len(bytes.TrimRight(src, " \n\r\t")); n > 0 {
		dst = append(dst, src[len(src)-n:]...)
	}
	return dst, nil
}
