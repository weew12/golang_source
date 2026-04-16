// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"fmt"
	"reflect"
)

// 来自可信来源的内容字符串。
type (
	// CSS 封装了已知安全的内容，匹配以下任意一种：
	//   1. CSS3 样式表产生式，如 `p { color: purple }`。
	//   2. CSS3 规则产生式，如 `a[href=~"https:"].foo#bar`。
	//   3. CSS3 声明产生式，如 `color: red; margin: 2px`。
	//   4. CSS3 值产生式，如 `rgba(0, 0, 255, 127)`。
	// 参见 https://www.w3.org/TR/css3-syntax/#parsing 和
	// https://web.archive.org/web/20090211114933/http://w3.org/TR/css3-syntax#style
	//
	// 使用此类型存在安全风险：
	// 封装的内容应来自可信来源，因为它将被原样包含在模板输出中。
	CSS string

	// HTML 封装了已知安全的 HTML 文档片段。
	// 它不应用于来自第三方的 HTML，或包含未闭合标签或注释的 HTML。
	// 可靠的 HTML 净化器的输出和经本包转义的模板输出可以安全地用于 HTML。
	//
	// 使用此类型存在安全风险：
	// 封装的内容应来自可信来源，因为它将被原样包含在模板输出中。
	HTML string

	// HTMLAttr 封装了来自可信来源的 HTML 属性，
	// 例如 ` dir="ltr"`。
	//
	// 使用此类型存在安全风险：
	// 封装的内容应来自可信来源，因为它将被原样包含在模板输出中。
	HTMLAttr string

	// JS 封装了已知安全的 EcmaScript5 表达式，例如 `(x + y * z())`。
	// 模板作者有责任确保类型化表达式不会破坏预期的优先级，
	// 并且不存在语句/表达式歧义，例如传递像
	// "{ foo: bar() }\n['foo']()" 这样的表达式，
	// 它既是有效的 Expression 也是有效的 Program，但含义完全不同。
	//
	// 使用此类型存在安全风险：
	// 封装的内容应来自可信来源，因为它将被原样包含在模板输出中。
	//
	// 使用 JS 包含有效但不可信的 JSON 是不安全的。
	// 一个安全的替代方案是使用 json.Unmarshal 解析 JSON，然后将结果对象
	// 传递到模板中，在 JavaScript 上下文中呈现时它将被转换为净化后的 JSON。
	JS string

	// JSStr 封装了一系列字符，旨在嵌入 JavaScript 表达式中引号之间。
	// 该字符串必须匹配一系列 StringCharacter：
	//   StringCharacter :: SourceCharacter 但不包括 `\` 或 LineTerminator
	//                    | EscapeSequence
	// 注意不允许 LineContinuation。
	// JSStr("foo\\nbar") 是可以的，但 JSStr("foo\\\nbar") 不行。
	//
	// 使用此类型存在安全风险：
	// 封装的内容应来自可信来源，因为它将被原样包含在模板输出中。
	JSStr string

	// URL 封装了已知安全的 URL 或 URL 子串（参见 RFC 3986）。
	// 来自可信来源的 URL（如 `javascript:checkThatFormNotEditedBeforeLeavingPage()`）
	// 应该被包含在页面中，但默认情况下动态 `javascript:` URL 会被过滤掉，
	// 因为它们是经常被利用的注入向量。
	//
	// 使用此类型存在安全风险：
	// 封装的内容应来自可信来源，因为它将被原样包含在模板输出中。
	URL string

	// Srcset 封装了已知安全的 srcset 属性
	// （参见 https://w3c.github.io/html/semantics-embedded-content.html#element-attrdef-img-srcset）。
	//
	// 使用此类型存在安全风险：
	// 封装的内容应来自可信来源，因为它将被原样包含在模板输出中。
	Srcset string
)

type contentType uint8

const (
	contentTypePlain contentType = iota
	contentTypeCSS
	contentTypeHTML
	contentTypeHTMLAttr
	contentTypeJS
	contentTypeJSStr
	contentTypeURL
	contentTypeSrcset
	// contentTypeUnsafe 在 attr.go 中用于影响嵌入内容和网络消息的
	// 形成、审核或解释方式的值；或网络消息携带的凭据。
	contentTypeUnsafe
)

// indirect 返回值，在必要时进行多次解引用以到达基本类型（或 nil）。
func indirect(a any) any {
	if a == nil {
		return nil
	}
	if t := reflect.TypeOf(a); t.Kind() != reflect.Pointer {
		// 如果不是指针，则避免创建 reflect.Value。
		return a
	}
	v := reflect.ValueOf(a)
	for v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	return v.Interface()
}

var (
	errorType       = reflect.TypeFor[error]()
	fmtStringerType = reflect.TypeFor[fmt.Stringer]()
)

// indirectToStringerOrError 返回值，在必要时进行多次解引用以到达基本类型（或 nil）
// 或 fmt.Stringer 或 error 的实现。
func indirectToStringerOrError(a any) any {
	if a == nil {
		return nil
	}
	v := reflect.ValueOf(a)
	for !v.Type().Implements(fmtStringerType) && !v.Type().Implements(errorType) && v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	return v.Interface()
}

// stringify 将其参数转换为字符串和内容类型。
// 所有指针都会被解引用，与 text/template 包中的行为一致。
func stringify(args ...any) (string, contentType) {
	if len(args) == 1 {
		switch s := indirect(args[0]).(type) {
		case string:
			return s, contentTypePlain
		case CSS:
			return string(s), contentTypeCSS
		case HTML:
			return string(s), contentTypeHTML
		case HTMLAttr:
			return string(s), contentTypeHTMLAttr
		case JS:
			return string(s), contentTypeJS
		case JSStr:
			return string(s), contentTypeJSStr
		case URL:
			return string(s), contentTypeURL
		case Srcset:
			return string(s), contentTypeSrcset
		}
	}
	i := 0
	for _, arg := range args {
		// 为了向后兼容，我们跳过无类型的 nil 参数。
		// 否则它们将被输出为转义后的 <nil>。
		// 参见 issue 25875。
		if arg == nil {
			continue
		}

		args[i] = indirectToStringerOrError(arg)
		i++
	}
	return fmt.Sprint(args[:i]...), contentTypePlain
}
