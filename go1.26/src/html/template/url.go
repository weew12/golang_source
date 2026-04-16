// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"fmt"
	"strings"
)

// urlFilter 返回其输入，除非输入包含不安全的 scheme，
// 此时会使整个 URL 失效。
//
// 导致无法在没有用户交互的情况下逆转的意外副作用的 scheme 被视为不安全。
// 例如，点击 "javascript:" 链接会立即触发 JavaScript 代码执行。
//
// 此过滤器保守地假设除以下 scheme 外的所有 scheme 都是不安全的：
//   - http:   导航到新网站，可能会打开新窗口或标签页。
//     这些副作用可以通过导航回之前的网站或关闭窗口或标签页来逆转。
//     在与新网站进一步交互之前不会发生不可逆的更改。
//   - https:  与 http 相同。
//   - mailto: 打开电子邮件程序并开始新草稿。此副作用在用户
//     明确点击发送之前是不可逆的；可以通过关闭电子邮件程序来撤销。
//
// 要允许包含其他 scheme 的 URL 绕过此过滤器，开发者必须
// 通过将其封装在 template.URL 值中来明确表示此类 URL 是预期的且安全的。
func urlFilter(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeURL {
		return s
	}
	if !isSafeURL(s) {
		return "#" + filterFailsafe
	}
	return s
}

// isSafeURL 在 s 是相对 URL 或 URL 的协议属于 (http, https, mailto) 时返回 true。
func isSafeURL(s string) bool {
	if protocol, _, ok := strings.Cut(s, ":"); ok && !strings.Contains(protocol, "/") {
		if !strings.EqualFold(protocol, "http") && !strings.EqualFold(protocol, "https") && !strings.EqualFold(protocol, "mailto") {
			return false
		}
	}
	return true
}

// urlEscaper 产生可以嵌入 URL 查询中的输出。
// 输出可以嵌入 HTML 属性中而无需进一步转义。
func urlEscaper(args ...any) string {
	return urlProcessor(false, args...)
}

// urlNormalizer 规范化 URL 内容，使其可以嵌入引号分隔的字符串
// 或括号分隔的 url(...) 中。
// 规范化器不编码所有 HTML 特殊字符。具体来说，它不编码 '&'，
// 因此正确嵌入 HTML 属性需要将 '&' 转义为 '&amp;'。
func urlNormalizer(args ...any) string {
	return urlProcessor(true, args...)
}

// urlProcessor 规范化（当 norm 为 true 时）或转义其输入以产生
// 有效的层级或不透明 URL 部分。
func urlProcessor(norm bool, args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeURL {
		norm = true
	}
	var b strings.Builder
	if processURLOnto(s, norm, &b) {
		return b.String()
	}
	return s
}

// processURLOnto 将与其输入对应的规范化 URL 追加到 b，
// 并报告追加的内容是否与 s 不同。
func processURLOnto(s string, norm bool, b *strings.Builder) bool {
	b.Grow(len(s) + 16)
	written := 0
	// 下面的字节循环假设所有 URL 使用 UTF-8 作为内容编码。
	// 这类似于 RFC 3987 第 3.1 节定义的 URI 到 IRI 编码方案，
	// 并且与 EcmaScript 内置函数 encodeURIComponent 行为相同。
	// 在 Content-type: text/html;charset=UTF-8 的页面中，
	// 它不应导致 URL 的错误编码。
	for i, n := 0, len(s); i < n; i++ {
		c := s[i]
		switch c {
		// 单引号和括号是 RFC 3986 中的 sub-delims，但我们对它们进行转义，
		// 以便输出可以嵌入单引号属性和未加引号的 CSS url(...) 结构中。
		// 单引号在 URL 中是保留的，但仅在 RFC 3986 附录中已废弃的
		// "mark" 规则中使用，因此可以安全编码。
		case '!', '#', '$', '&', '*', '+', ',', '/', ':', ';', '=', '?', '@', '[', ']':
			if norm {
				continue
			}
		// 根据 RFC 3986 第 2.3 节为非保留字符
		// "为保持一致性，URI 生产者不应创建范围在
		// ALPHA (%41-%5A 和 %61-%7A)、DIGIT (%30-%39)、连字符 (%2D)、
		// 句号 (%2E)、下划线 (%5F) 或波浪号 (%7E) 中的百分号编码八位组"
		case '-', '.', '_', '~':
			continue
		case '%':
			// 规范化时不重新编码有效的转义。
			if norm && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
				continue
			}
		default:
			// 根据 RFC 3986 第 2.3 节为非保留字符
			if 'a' <= c && c <= 'z' {
				continue
			}
			if 'A' <= c && c <= 'Z' {
				continue
			}
			if '0' <= c && c <= '9' {
				continue
			}
		}
		b.WriteString(s[written:i])
		fmt.Fprintf(b, "%%%02x", c)
		written = i + 1
	}
	b.WriteString(s[written:])
	return written != 0
}

// srcsetFilterAndEscaper 过滤和规范化 srcset 值，
// 这些值是以逗号分隔的 URL 后跟元数据。
func srcsetFilterAndEscaper(args ...any) string {
	s, t := stringify(args...)
	switch t {
	case contentTypeSrcset:
		return s
	case contentTypeURL:
		// 规范化会去除所有将图片 URL 与其元数据分隔的 HTML 空白。
		var b strings.Builder
		if processURLOnto(s, true, &b) {
			s = b.String()
		}
		// 此外，逗号将一个源与另一个源分隔。
		return strings.ReplaceAll(s, ",", "%2c")
	}

	var b strings.Builder
	written := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			filterSrcsetElement(s, written, i, &b)
			b.WriteString(",")
			written = i + 1
		}
	}
	filterSrcsetElement(s, written, len(s), &b)
	return b.String()
}

// 源自 https://play.golang.org/p/Dhmj7FORT5
const htmlSpaceAndASCIIAlnumBytes = "\x00\x36\x00\x00\x01\x00\xff\x03\xfe\xff\xff\x07\xfe\xff\xff\x07"

// isHTMLSpace 当且仅当 c 是根据
// https://infra.spec.whatwg.org/#ascii-whitespace 定义的空白字符时为 true。
func isHTMLSpace(c byte) bool {
	return (c <= 0x20) && 0 != (htmlSpaceAndASCIIAlnumBytes[c>>3]&(1<<uint(c&0x7)))
}

func isHTMLSpaceOrASCIIAlnum(c byte) bool {
	return (c < 0x80) && 0 != (htmlSpaceAndASCIIAlnumBytes[c>>3]&(1<<uint(c&0x7)))
}

func filterSrcsetElement(s string, left int, right int, b *strings.Builder) {
	start := left
	for start < right && isHTMLSpace(s[start]) {
		start++
	}
	end := right
	for i := start; i < right; i++ {
		if isHTMLSpace(s[i]) {
			end = i
			break
		}
	}
	if url := s[start:end]; isSafeURL(url) {
		// 如果图片元数据仅包含空格或字母数字，
		// 则不需要对其进行 URL 规范化。
		metadataOk := true
		for i := end; i < right; i++ {
			if !isHTMLSpaceOrASCIIAlnum(s[i]) {
				metadataOk = false
				break
			}
		}
		if metadataOk {
			b.WriteString(s[left:start])
			processURLOnto(url, true, b)
			b.WriteString(s[end:right])
			return
		}
	}
	b.WriteString("#")
	b.WriteString(filterFailsafe)
}
