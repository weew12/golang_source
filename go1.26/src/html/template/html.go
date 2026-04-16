// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// htmlNospaceEscaper 转义内容以便嵌入未加引号的属性值中。
func htmlNospaceEscaper(args ...any) string {
	s, t := stringify(args...)
	if s == "" {
		return filterFailsafe
	}
	if t == contentTypeHTML {
		return htmlReplacer(stripTags(s), htmlNospaceNormReplacementTable, false)
	}
	return htmlReplacer(s, htmlNospaceReplacementTable, false)
}

// attrEscaper 转义内容以便嵌入加引号的属性值中。
func attrEscaper(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeHTML {
		return htmlReplacer(stripTags(s), htmlNormReplacementTable, true)
	}
	return htmlReplacer(s, htmlReplacementTable, true)
}

// rcdataEscaper 转义内容以便嵌入 RCDATA 元素体中。
func rcdataEscaper(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeHTML {
		return htmlReplacer(s, htmlNormReplacementTable, true)
	}
	return htmlReplacer(s, htmlReplacementTable, true)
}

// htmlEscaper 转义内容以便嵌入 HTML 文本中。
func htmlEscaper(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeHTML {
		return s
	}
	return htmlReplacer(s, htmlReplacementTable, true)
}

// htmlReplacementTable 包含在加引号的属性值或文本节点内部需要转义的 rune。
var htmlReplacementTable = []string{
	// https://www.w3.org/TR/html5/syntax.html#attribute-value-(unquoted)-state
	// U+0000 NULL 解析错误。将 U+FFFD 替换字符追加到当前属性的值中。
	// "
	// 以及类似地
	// https://www.w3.org/TR/html5/syntax.html#before-attribute-value-state
	0:    "\uFFFD",
	'"':  "&#34;",
	'&':  "&amp;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'>':  "&gt;",
}

// htmlNormReplacementTable 类似于 htmlReplacementTable，但不包含 '&'
// 以避免对已有实体过度编码。
var htmlNormReplacementTable = []string{
	0:    "\uFFFD",
	'"':  "&#34;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'>':  "&gt;",
}

// htmlNospaceReplacementTable 包含在未加引号的属性值内部需要转义的 rune。
// 转义的 rune 集合是 HTML 特殊字符与在浏览器中运行以下 JS 确定的字符的并集：
// <div id=d></div>
// <script>(function () {
// var a = [], d = document.getElementById("d"), i, c, s;
// for (i = 0; i < 0x10000; ++i) {
//
//	c = String.fromCharCode(i);
//	d.innerHTML = "<span title=" + c + "lt" + c + "></span>"
//	s = d.getElementsByTagName("SPAN")[0];
//	if (!s || s.title !== c + "lt" + c) { a.push(i.toString(16)); }
//
// }
// document.write(a.join(", "));
// })()</script>
var htmlNospaceReplacementTable = []string{
	0:    "&#xfffd;",
	'\t': "&#9;",
	'\n': "&#10;",
	'\v': "&#11;",
	'\f': "&#12;",
	'\r': "&#13;",
	' ':  "&#32;",
	'"':  "&#34;",
	'&':  "&amp;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'=':  "&#61;",
	'>':  "&gt;",
	// 在属性值（未加引号）和属性值之前状态中的解析错误。
	// 被 IE 视为引号字符。
	'`': "&#96;",
}

// htmlNospaceNormReplacementTable 类似于 htmlNospaceReplacementTable，但不包含 '&'
// 以避免对已有实体过度编码。
var htmlNospaceNormReplacementTable = []string{
	0:    "&#xfffd;",
	'\t': "&#9;",
	'\n': "&#10;",
	'\v': "&#11;",
	'\f': "&#12;",
	'\r': "&#13;",
	' ':  "&#32;",
	'"':  "&#34;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'=':  "&#61;",
	'>':  "&gt;",
	// 在属性值（未加引号）和属性值之前状态中的解析错误。
	// 被 IE 视为引号字符。
	'`': "&#96;",
}

// htmlReplacer 返回将 s 中的 rune 根据 replacementTable 替换后的结果，
// 当 badRunes 为 true 时，某些坏 rune 允许不经转义通过。
func htmlReplacer(s string, replacementTable []string, badRunes bool) string {
	written, b := 0, new(strings.Builder)
	r, w := rune(0), 0
	for i := 0; i < len(s); i += w {
		// 不能使用 'for range s'，因为我们需要保留输入中 rune 的宽度。
		// 如果遇到解码错误，输入宽度将不等于 utf8.RuneLen(r)，我们将溢出缓冲区。
		r, w = utf8.DecodeRuneInString(s[i:])
		if int(r) < len(replacementTable) {
			if repl := replacementTable[r]; len(repl) != 0 {
				if written == 0 {
					b.Grow(len(s))
				}
				b.WriteString(s[written:i])
				b.WriteString(repl)
				written = i + w
			}
		} else if badRunes {
			// 空操作。
			// IE 不允许在未加引号的属性中使用这些范围。
		} else if 0xfdd0 <= r && r <= 0xfdef || 0xfff0 <= r && r <= 0xffff {
			if written == 0 {
				b.Grow(len(s))
			}
			fmt.Fprintf(b, "%s&#x%x;", s[written:i], r)
			written = i + w
		}
	}
	if written == 0 {
		return s
	}
	b.WriteString(s[written:])
	return b.String()
}

// stripTags 接受一段 HTML 片段并仅返回文本内容。
// 例如，`<b>&iexcl;Hi!</b> <script>...</script>` -> `&iexcl;Hi! `。
func stripTags(html string) string {
	var b strings.Builder
	s, c, i, allText := []byte(html), context{}, 0, true
	// 使用转换函数帮助我们避免破坏
	// `<div title="1>2">` 或 `I <3 Ponies!`。
	for i != len(s) {
		if c.delim == delimNone {
			st := c.state
			// 使用 RCDATA 而不是解析为 JS 或 CSS 样式。
			if c.element != elementNone && !isInTag(st) {
				st = stateRCDATA
			}
			d, nread := transitionFunc[st](c, s[i:])
			i1 := i + nread
			if c.state == stateText || c.state == stateRCDATA {
				// 输出到标签或注释开始位置之前的文本。
				j := i1
				if d.state != c.state {
					for j1 := j - 1; j1 >= i; j1-- {
						if s[j1] == '<' {
							j = j1
							break
						}
					}
				}
				b.Write(s[i:j])
			} else {
				allText = false
			}
			c, i = d, i1
			continue
		}
		i1 := i + bytes.IndexAny(s[i:], delimEnds[c.delim])
		if i1 < i {
			break
		}
		if c.delim != delimSpaceOrTagEnd {
			// 消耗任何引号。
			i1++
		}
		c, i = context{state: stateTag, element: c.element}, i1
	}
	if allText {
		return html
	} else if c.state == stateText || c.state == stateRCDATA {
		b.Write(s[i:])
	}
	return b.String()
}

// htmlNameFilter 接受 HTML 属性或标签名称的有效部分，或已知安全的 HTML 属性。
func htmlNameFilter(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeHTMLAttr {
		return s
	}
	if len(s) == 0 {
		// 避免违反结构保持属性。
		// <input checked {{.K}}={{.V}}>。
		// 如果没有这个检查，当 .K 为空时 .V 是 checked 的值，
		// 否则 .V 是名为 .K 的属性的值。
		return filterFailsafe
	}
	s = strings.ToLower(s)
	if t := attrType(s); t != contentTypePlain {
		// TODO: 拆分属性和元素名称部分过滤器，以便我们能识别已知属性。
		return filterFailsafe
	}
	for _, r := range s {
		switch {
		case '0' <= r && r <= '9':
		case 'a' <= r && r <= 'z':
		default:
			return filterFailsafe
		}
	}
	return s
}

// commentEscaper 无论输入是什么都返回空字符串。
// 注释内容不对应任何已解析的结构或人类可读的内容，
// 因此最简单和最安全的策略是丢弃插值到注释中的内容。
// 无论静态注释内容是否从模板中移除，此方法都同样有效。
func commentEscaper(args ...any) string {
	return ""
}
