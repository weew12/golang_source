// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"
)

// jsWhitespace 包含所有 JS 空白字符，由 \s 字符类定义。
// 参见 https://developer.mozilla.org/en-US/docs/Web/JavaScript/Guide/Regular_expressions/Character_classes。
const jsWhitespace = "\f\n\r\t\v\u0020\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// nextJSCtx 返回确定给定 token 序列之后的斜杠是开始一个正则表达式还是
// 除法运算符（/ 或 /=）的上下文。
//
// 此函数假设 token 序列不包含任何字符串 token、注释 token、
// 正则表达式字面量 token 或除法运算符。
//
// 对于某些有效但无意义的 JavaScript 程序（如 "x = ++/foo/i"，与 "x++/foo/i" 完全不同），
// 此函数会失败，但目前未发现在任何已知有用的程序上失败。
// 它基于 JavaScript 2.0 词法语法草案，需要一个 token 的回溯：
// https://www.mozilla.org/js/language/js20-2000-07/rationale/syntax.html
func nextJSCtx(s []byte, preceding jsCtx) jsCtx {
	// 去除所有 JS 空白字符
	s = bytes.TrimRight(s, jsWhitespace)
	if len(s) == 0 {
		return preceding
	}

	// 以下所有 case 都在单字节 UTF-8 组中。
	switch c, n := s[len(s)-1], len(s); c {
	case '+', '-':
		// ++ 和 -- 不是正则表达式前驱，但 + 和 - 无论用作中缀还是前缀运算符都是。
		start := n - 1
		// 计算相邻的减号或加号数量。
		for start > 0 && s[start-1] == c {
			start--
		}
		if (n-start)&1 == 1 {
			// 对于尾部减号会到达此处，因为 "---" 等同于 "-- -"。
			return jsCtxRegexp
		}
		return jsCtxDivOp
	case '.':
		// 处理 "42."。
		if n != 1 && '0' <= s[n-2] && s[n-2] <= '9' {
			return jsCtxDivOp
		}
		return jsCtxRegexp
	// 语言规范第 7.7 节中所有标点符号的后缀，
	// 这些标点符号仅结束上面未处理的二元运算符。
	case ',', '<', '>', '=', '*', '%', '&', '|', '^', '?':
		return jsCtxRegexp
	// 语言规范第 7.7 节中所有标点符号的后缀，
	// 这些标点符号是上面未处理的前缀运算符。
	case '!', '~':
		return jsCtxRegexp
	// 匹配语言规范第 7.7 节中所有标点符号，
	// 这些标点符号是上面未处理的开括号。
	case '(', '[':
		return jsCtxRegexp
	// 匹配语言规范第 7.7 节中所有标点符号，
	// 这些标点符号出现在表达式开始之前。
	case ':', ';', '{':
		return jsCtxRegexp
	// 注意：闭括号（'}'、']'、')'）出现在除法运算符之前，
	// 在 default 分支中处理，但 '}' 可以出现在除法运算符之前，如
	//    ({ valueOf: function () { return 42 } } / 2
	// 这是有效的，但实际上开发者不会对对象字面量做除法，
	// 因此我们的启发式规则对以下代码效果很好：
	//    function () { ... }  /foo/.test(x) && sideEffect();
	// ')' 标点符号可以出现在正则表达式之前，如
	//     if (b) /foo/.test(x) && ...
	// 但这比以下情况出现的可能性小得多：
	//     (a + b) / c
	case '}':
		return jsCtxRegexp
	default:
		// 查找 IdentifierName 并判断它是否是可以出现在正则表达式之前的关键字。
		j := n
		for j > 0 && isJSIdentPart(rune(s[j-1])) {
			j--
		}
		if regexpPrecederKeywords[string(s[j:])] {
			return jsCtxRegexp
		}
	}
	// 否则是上面未列出的标点符号，
	// 或者是出现在除法运算符之前的字符串，或者是出现在除法运算符之前的标识符。
	return jsCtxDivOp
}

// regexpPrecederKeywords 是一组可以在 JS 源码中出现在正则表达式之前的保留 JS 关键字。
var regexpPrecederKeywords = map[string]bool{
	"break":      true,
	"case":       true,
	"continue":   true,
	"delete":     true,
	"do":         true,
	"else":       true,
	"finally":    true,
	"in":         true,
	"instanceof": true,
	"return":     true,
	"throw":      true,
	"try":        true,
	"typeof":     true,
	"void":       true,
}

var jsonMarshalType = reflect.TypeFor[json.Marshaler]()

// indirectToJSONMarshaler 返回值，在必要时进行多次解引用以到达基本类型（或 nil）
// 或 json.Marshal 的实现。
func indirectToJSONMarshaler(a any) any {
	// text/template 现在支持将无类型 nil 作为函数调用参数传递，
	// 因此我们必须支持它。否则我们将在下面 panic，因为无法对无效的
	// reflect.Value 调用 Type 或 Interface 方法。参见 golang.org/issue/18716。
	if a == nil {
		return nil
	}

	v := reflect.ValueOf(a)
	for !v.Type().Implements(jsonMarshalType) && v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	return v.Interface()
}

var scriptTagRe = regexp.MustCompile("(?i)<(/?)script")

// jsValEscaper 将其输入转义为一个 JS 表达式（第 11.14 节），
// 该表达式没有副作用也没有外部自由变量（NaN、Infinity 除外）。
func jsValEscaper(args ...any) string {
	var a any
	if len(args) == 1 {
		a = indirectToJSONMarshaler(args[0])
		switch t := a.(type) {
		case JS:
			return string(t)
		case JSStr:
			// TODO: 规范化引号。
			return `"` + string(t) + `"`
		case json.Marshaler:
			// 不作为 Stringer 处理。
		case fmt.Stringer:
			a = t.String()
		}
	} else {
		for i, arg := range args {
			args[i] = indirectToJSONMarshaler(arg)
		}
		a = fmt.Sprint(args...)
	}
	// TODO: 在调用 Marshal 之前检测循环，因为 Marshal 对循环数据会无限循环。
	// 这可能是一个不可接受的 DoS 风险。
	b, err := json.Marshal(a)
	if err != nil {
		// 虽然标准 JSON 编组器不会在错误消息中包含用户控制的信息，
		// 但如果类型有 MarshalJSON 方法，则错误消息的内容无法保证。
		// 由于我们将错误作为注释的一部分插入模板中，因此我们尝试
		// 防止错误终止注释或脚本块本身。
		//
		// 具体来说，我们：
		//   * 将 "*/" 注释结束标记替换为 "* /"，这不会终止注释
		//   * 将 "<script" 和 "</script" 替换为 "\x3Cscript" 和 "\x3C/script"
		//     （不区分大小写），将 "<!--" 替换为 "\x3C!--"，
		//     以防止混淆脚本块终止语义
		//
		// 我们还在注释前放置一个空格，以便如果它紧挨除法运算符，
		// 不会被变成行注释：
		//     x/{{y}}
		// 变成
		//     x//* error marshaling y:
		//          second line of error message */null
		errStr := err.Error()
		errStr = string(scriptTagRe.ReplaceAll([]byte(errStr), []byte(`\x3C${1}script`)))
		errStr = strings.ReplaceAll(errStr, "*/", "* /")
		errStr = strings.ReplaceAll(errStr, "<!--", `\x3C!--`)
		return fmt.Sprintf(" /* %s */null ", errStr)
	}

	// TODO: 可能需要后处理输出以防止其包含
	// "<!--"、"-->"、"<![CDATA["、"]]>" 或 "</script"，
	// 以防自定义编组器产生包含这些内容的输出。
	// 注意：不要使用 \x 转义来节省字节，因为它不兼容 JSON，
	// 而此转义器支持 ld+json 内容类型。
	if len(b) == 0 {
		// 在 `x=y/{{.}}*z` 中，产生 "" 的 json.Marshaler 不应导致
		// 输出 `x=y/*z`。
		return " null "
	}
	first, _ := utf8.DecodeRune(b)
	last, _ := utf8.DecodeLastRune(b)
	var buf strings.Builder
	// 防止 IdentifierName 和 NumericLiteral 与关键字连在一起：
	// in、instanceof、typeof、void
	pad := isJSIdentPart(first) || isJSIdentPart(last)
	if pad {
		buf.WriteByte(' ')
	}
	written := 0
	// 确保 json.Marshal 转义码点 U+2028 和 U+2029，
	// 使其处于有效 JS 的 JSON 子集范围内。
	for i := 0; i < len(b); {
		rune, n := utf8.DecodeRune(b[i:])
		repl := ""
		if rune == 0x2028 {
			repl = `\u2028`
		} else if rune == 0x2029 {
			repl = `\u2029`
		}
		if repl != "" {
			buf.Write(b[written:i])
			buf.WriteString(repl)
			written = i + n
		}
		i += n
	}
	if buf.Len() != 0 {
		buf.Write(b[written:])
		if pad {
			buf.WriteByte(' ')
		}
		return buf.String()
	}
	return string(b)
}

// jsStrEscaper 产生一个可以包含在 JavaScript 源码中引号之间、
// 嵌入 HTML5 <script> 元素中的 JavaScript 中、
// 或 HTML5 事件处理器属性（如 onclick）中的字符串。
func jsStrEscaper(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeJSStr {
		return replace(s, jsStrNormReplacementTable)
	}
	return replace(s, jsStrReplacementTable)
}

func jsTmplLitEscaper(args ...any) string {
	s, _ := stringify(args...)
	return replace(s, jsBqStrReplacementTable)
}

// jsRegexpEscaper 行为类似于 jsStrEscaper，但会转义正则表达式特殊字符，
// 使结果在包含于正则表达式字面量中时被当作字面文本处理。
// /foo{{.X}}bar/ 匹配字符串 "foo" 后跟 {{.X}} 的字面文本再跟字符串 "bar"。
func jsRegexpEscaper(args ...any) string {
	s, _ := stringify(args...)
	s = replace(s, jsRegexpReplacementTable)
	if s == "" {
		// 当 .X == "" 时，/{{.X}}/ 不应产生行注释。
		return "(?:)"
	}
	return s
}

// replace 将 s 中的每个 rune r 替换为 replacementTable[r]，
// 前提是 r < len(replacementTable)。如果 replacementTable[r] 为空字符串，
// 则不进行替换。
// 它还将 rune U+2028 和 U+2029 替换为原始字符串 `\u2028` 和 `\u2029`。
func replace(s string, replacementTable []string) string {
	var b strings.Builder
	r, w, written := rune(0), 0, 0
	for i := 0; i < len(s); i += w {
		// 参见 htmlEscaper 中的注释。
		r, w = utf8.DecodeRuneInString(s[i:])
		var repl string
		switch {
		case int(r) < len(lowUnicodeReplacementTable):
			repl = lowUnicodeReplacementTable[r]
		case int(r) < len(replacementTable) && replacementTable[r] != "":
			repl = replacementTable[r]
		case r == '\u2028':
			repl = `\u2028`
		case r == '\u2029':
			repl = `\u2029`
		default:
			continue
		}
		if written == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[written:i])
		b.WriteString(repl)
		written = i + w
	}
	if written == 0 {
		return s
	}
	b.WriteString(s[written:])
	return b.String()
}

var lowUnicodeReplacementTable = []string{
	0: `\u0000`, 1: `\u0001`, 2: `\u0002`, 3: `\u0003`, 4: `\u0004`, 5: `\u0005`, 6: `\u0006`,
	'\a': `\u0007`,
	'\b': `\u0008`,
	'\t': `\t`,
	'\n': `\n`,
	'\v': `\u000b`, // "\v" 在 IE 6 上等于 "v"。
	'\f': `\f`,
	'\r': `\r`,
	0xe:  `\u000e`, 0xf: `\u000f`, 0x10: `\u0010`, 0x11: `\u0011`, 0x12: `\u0012`, 0x13: `\u0013`,
	0x14: `\u0014`, 0x15: `\u0015`, 0x16: `\u0016`, 0x17: `\u0017`, 0x18: `\u0018`, 0x19: `\u0019`,
	0x1a: `\u001a`, 0x1b: `\u001b`, 0x1c: `\u001c`, 0x1d: `\u001d`, 0x1e: `\u001e`, 0x1f: `\u001f`,
}

var jsStrReplacementTable = []string{
	0:    `\u0000`,
	'\t': `\t`,
	'\n': `\n`,
	'\v': `\u000b`, // "\v" 在 IE 6 上等于 "v"。
	'\f': `\f`,
	'\r': `\r`,
	// 将 HTML 特殊字符编码为十六进制，以便输出可以嵌入
	// HTML 属性中而无需进一步编码。
	'"':  `\u0022`,
	'`':  `\u0060`,
	'&':  `\u0026`,
	'\'': `\u0027`,
	'+':  `\u002b`,
	'/':  `\/`,
	'<':  `\u003c`,
	'>':  `\u003e`,
	'\\': `\\`,
}

// jsBqStrReplacementTable 类似于 jsStrReplacementTable，但还包含
// JS 模板字面量的特殊字符：$、{ 和 }。
var jsBqStrReplacementTable = []string{
	0:    `\u0000`,
	'\t': `\t`,
	'\n': `\n`,
	'\v': `\u000b`, // "\v" 在 IE 6 上等于 "v"。
	'\f': `\f`,
	'\r': `\r`,
	// 将 HTML 特殊字符编码为十六进制，以便输出可以嵌入
	// HTML 属性中而无需进一步编码。
	'"':  `\u0022`,
	'`':  `\u0060`,
	'&':  `\u0026`,
	'\'': `\u0027`,
	'+':  `\u002b`,
	'/':  `\/`,
	'<':  `\u003c`,
	'>':  `\u003e`,
	'\\': `\\`,
	'$':  `\u0024`,
	'{':  `\u007b`,
	'}':  `\u007d`,
}

// jsStrNormReplacementTable 类似于 jsStrReplacementTable，但不会过度编码
// 已有的转义，因为此表没有 `\` 的条目。
var jsStrNormReplacementTable = []string{
	0:    `\u0000`,
	'\t': `\t`,
	'\n': `\n`,
	'\v': `\u000b`, // "\v" 在 IE 6 上等于 "v"。
	'\f': `\f`,
	'\r': `\r`,
	// 将 HTML 特殊字符编码为十六进制，以便输出可以嵌入
	// HTML 属性中而无需进一步编码。
	'"':  `\u0022`,
	'&':  `\u0026`,
	'\'': `\u0027`,
	'`':  `\u0060`,
	'+':  `\u002b`,
	'/':  `\/`,
	'<':  `\u003c`,
	'>':  `\u003e`,
}
var jsRegexpReplacementTable = []string{
	0:    `\u0000`,
	'\t': `\t`,
	'\n': `\n`,
	'\v': `\u000b`, // "\v" 在 IE 6 上等于 "v"。
	'\f': `\f`,
	'\r': `\r`,
	// 将 HTML 特殊字符编码为十六进制，以便输出可以嵌入
	// HTML 属性中而无需进一步编码。
	'"':  `\u0022`,
	'$':  `\$`,
	'&':  `\u0026`,
	'\'': `\u0027`,
	'(':  `\(`,
	')':  `\)`,
	'*':  `\*`,
	'+':  `\u002b`,
	'-':  `\-`,
	'.':  `\.`,
	'/':  `\/`,
	'<':  `\u003c`,
	'>':  `\u003e`,
	'?':  `\?`,
	'[':  `\[`,
	'\\': `\\`,
	']':  `\]`,
	'^':  `\^`,
	'{':  `\{`,
	'|':  `\|`,
	'}':  `\}`,
}

// isJSIdentPart 报告给定的 rune 是否是 JS 标识符部分。
// 它不处理所有非拉丁字母、连接符和组合标记，
// 但处理了可以出现在数字字面量或关键字中的每个码点。
func isJSIdentPart(r rune) bool {
	switch {
	case r == '$':
		return true
	case '0' <= r && r <= '9':
		return true
	case 'A' <= r && r <= 'Z':
		return true
	case r == '_':
		return true
	case 'a' <= r && r <= 'z':
		return true
	}
	return false
}

// isJSType 报告给定的 MIME 类型是否应被视为 JavaScript。
//
// 它用于确定带有 type 属性的 script 标签是否是 JavaScript 容器。
func isJSType(mimeType string) bool {
	// 根据
	//   https://www.w3.org/TR/html5/scripting-1.html#attr-script-type
	//   https://tools.ietf.org/html/rfc7231#section-3.1.1
	//   https://tools.ietf.org/html/rfc4329#section-3
	//   https://www.ietf.org/rfc/rfc4627.txt
	// 丢弃参数
	mimeType, _, _ = strings.Cut(mimeType, ";")
	mimeType = strings.ToLower(mimeType)
	mimeType = strings.TrimSpace(mimeType)
	switch mimeType {
	case
		"application/ecmascript",
		"application/javascript",
		"application/json",
		"application/ld+json",
		"application/x-ecmascript",
		"application/x-javascript",
		"module",
		"text/ecmascript",
		"text/javascript",
		"text/javascript1.0",
		"text/javascript1.1",
		"text/javascript1.2",
		"text/javascript1.3",
		"text/javascript1.4",
		"text/javascript1.5",
		"text/jscript",
		"text/livescript",
		"text/x-ecmascript",
		"text/x-javascript":
		return true
	default:
		return false
	}
}
