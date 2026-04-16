// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"fmt"
	"text/template/parse"
)

// context 描述 HTML 解析器在到达由特定模板节点求值产生的 HTML 部分时必须处于的状态。
//
// context 类型的零值是生成 HTML 片段的模板的起始上下文，
// 如 https://www.w3.org/TR/html5/syntax.html#the-end 所定义，
// 其中上下文元素为 null。
type context struct {
	state   state
	delim   delim
	urlPart urlPart
	jsCtx   jsCtx
	// jsBraceDepth 包含每个 JS 模板字面量字符串插值表达式中
	// 我们已遇到的花括号的当前深度。这用于确定下一个 } 是否会
	// 关闭 JS 模板字面量字符串插值表达式。
	jsBraceDepth []int
	attr         attr
	element      element
	n            parse.Node // 用于 range break/continue
	err          *Error
}

func (c context) String() string {
	var err error
	if c.err != nil {
		err = c.err
	}
	return fmt.Sprintf("{%v %v %v %v %v %v %v}", c.state, c.delim, c.urlPart, c.jsCtx, c.attr, c.element, err)
}

// eq 报告两个上下文是否相等。
func (c context) eq(d context) bool {
	return c.state == d.state &&
		c.delim == d.delim &&
		c.urlPart == d.urlPart &&
		c.jsCtx == d.jsCtx &&
		c.attr == d.attr &&
		c.element == d.element &&
		c.err == d.err
}

// mangle 生成一个包含后缀的标识符，使其与使用不同上下文混淆的模板名称区分开来。
func (c context) mangle(templateName string) string {
	// 默认上下文的混淆名称就是输入的 templateName。
	if c.state == stateText {
		return templateName
	}
	s := templateName + "$htmltemplate_" + c.state.String()
	if c.delim != delimNone {
		s += "_" + c.delim.String()
	}
	if c.urlPart != urlPartNone {
		s += "_" + c.urlPart.String()
	}
	if c.jsCtx != jsCtxRegexp {
		s += "_" + c.jsCtx.String()
	}
	if c.attr != attrNone {
		s += "_" + c.attr.String()
	}
	if c.element != elementNone {
		s += "_" + c.element.String()
	}
	return s
}

// state 描述高层级的 HTML 解析器状态。
//
// 它限定了元素栈的顶部，进而限定了 HTML 插入模式，
// 但也包含与 HTML5 解析算法中任何内容都不对应的状态，
// 因为 HTML 语法中的单个 token 产生式在模板中可能包含嵌入的 action。
// 例如，由以下代码产生的带引号的 HTML 属性
//
//	<div title="Hello {{.World}}">
//
// 在 HTML 语法中是单个 token，但在模板中跨越多个节点。
type state uint8

//go:generate stringer -type state

const (
	// stateText 是已解析的字符数据。当 HTML 解析器的解析位置在 HTML 标签、
	// 指令、注释和特殊元素体之外时，处于此状态。
	stateText state = iota
	// stateTag 出现在 HTML 属性之前或标签结束之前。
	stateTag
	// stateAttrName 出现在属性名称内部。
	// 它出现在 ` ^name^ = value` 中 ^ 标记之间。
	stateAttrName
	// stateAfterName 出现在属性名称结束之后但等号之前。
	// 它出现在 ` name^ ^= value` 中 ^ 标记之间。
	stateAfterName
	// stateBeforeValue 出现在等号之后但值之前。
	// 它出现在 ` name =^ ^value` 中 ^ 标记之间。
	stateBeforeValue
	// stateHTMLCmt 出现在 <!-- HTML 注释 --> 内部。
	stateHTMLCmt
	// stateRCDATA 出现在 RCDATA 元素（<textarea> 或 <title>）内部，
	// 如 https://www.w3.org/TR/html5/syntax.html#elements-0 所述。
	stateRCDATA
	// stateAttr 出现在内容为文本的 HTML 属性内部。
	stateAttr
	// stateURL 出现在内容为 URL 的 HTML 属性内部。
	stateURL
	// stateSrcset 出现在 HTML srcset 属性内部。
	stateSrcset
	// stateJS 出现在事件处理器或 script 元素内部。
	stateJS
	// stateJSDqStr 出现在 JavaScript 双引号字符串内部。
	stateJSDqStr
	// stateJSSqStr 出现在 JavaScript 单引号字符串内部。
	stateJSSqStr
	// stateJSTmplLit 出现在 JavaScript 反引号字符串内部。
	stateJSTmplLit
	// stateJSRegexp 出现在 JavaScript 正则表达式字面量内部。
	stateJSRegexp
	// stateJSBlockCmt 出现在 JavaScript /* 块注释 */ 内部。
	stateJSBlockCmt
	// stateJSLineCmt 出现在 JavaScript // 行注释内部。
	stateJSLineCmt
	// stateJSHTMLOpenCmt 出现在 JavaScript <!-- 类 HTML 注释内部。
	stateJSHTMLOpenCmt
	// stateJSHTMLCloseCmt 出现在 JavaScript --> 类 HTML 注释内部。
	stateJSHTMLCloseCmt
	// stateCSS 出现在 <style> 元素或 style 属性内部。
	stateCSS
	// stateCSSDqStr 出现在 CSS 双引号字符串内部。
	stateCSSDqStr
	// stateCSSSqStr 出现在 CSS 单引号字符串内部。
	stateCSSSqStr
	// stateCSSDqURL 出现在 CSS 双引号 url("...") 内部。
	stateCSSDqURL
	// stateCSSSqURL 出现在 CSS 单引号 url('...') 内部。
	stateCSSSqURL
	// stateCSSURL 出现在 CSS 无引号 url(...) 内部。
	stateCSSURL
	// stateCSSBlockCmt 出现在 CSS /* 块注释 */ 内部。
	stateCSSBlockCmt
	// stateCSSLineCmt 出现在 CSS // 行注释内部。
	stateCSSLineCmt
	// stateError 是一种传染性错误状态，处于任何有效
	// HTML/CSS/JS 结构之外。
	stateError
	// stateMetaContent 出现在 HTML meta 元素的 content 属性内部。
	stateMetaContent
	// stateMetaContentURL 出现在 HTML meta 元素 content 属性中的 "url=" 标记内部。
	stateMetaContentURL
	// stateDead 标记 {{break}} 或 {{continue}} 之后不可达的代码。
	stateDead
)

// isComment 对于包含面向模板作者和维护者（而非终端用户或机器）内容的任何状态返回 true。
func isComment(s state) bool {
	switch s {
	case stateHTMLCmt, stateJSBlockCmt, stateJSLineCmt, stateJSHTMLOpenCmt, stateJSHTMLCloseCmt, stateCSSBlockCmt, stateCSSLineCmt:
		return true
	}
	return false
}

// isInTag 返回 s 是否仅出现在 HTML 标签内部。
func isInTag(s state) bool {
	switch s {
	case stateTag, stateAttrName, stateAfterName, stateBeforeValue, stateAttr:
		return true
	}
	return false
}

// isInScriptLiteral 在 s 是 <script> 标签内的字面量状态之一时返回 true，
// 因此 "<!--"、"<script" 和 "</script" 的出现需要被特殊处理。
func isInScriptLiteral(s state) bool {
	// 忽略注释状态（stateJSBlockCmt、stateJSLineCmt、
	// stateJSHTMLOpenCmt、stateJSHTMLCloseCmt），因为它们的内容已从输出中省略。
	switch s {
	case stateJSDqStr, stateJSSqStr, stateJSTmplLit, stateJSRegexp:
		return true
	}
	return false
}

// delim 是将结束当前 HTML 属性的分隔符。
type delim uint8

//go:generate stringer -type delim

const (
	// delimNone 出现在任何属性之外。
	delimNone delim = iota
	// delimDoubleQuote 在双引号 (") 关闭属性时出现。
	delimDoubleQuote
	// delimSingleQuote 在单引号 (') 关闭属性时出现。
	delimSingleQuote
	// delimSpaceOrTagEnd 在空格或右尖括号 (>) 关闭属性时出现。
	delimSpaceOrTagEnd
)

// urlPart 标识 RFC 3986 分层 URL 中的某个部分，以允许使用不同的编码策略。
type urlPart uint8

//go:generate stringer -type urlPart

const (
	// urlPartNone 在不处于 URL 中或可能在 URL 起始位置时出现：
	// "^http://auth/path?k=v#frag" 中的 ^。
	urlPartNone urlPart = iota
	// urlPartPreQuery 出现在 scheme、authority 或 path 中；
	// "h^ttp://auth/path^?k=v#frag" 中 ^ 标记之间。
	urlPartPreQuery
	// urlPartQueryOrFrag 出现在查询部分中；
	// "http://auth/path?^k=v#frag^" 中 ^ 标记之间。
	urlPartQueryOrFrag
	// urlPartUnknown 由于查询分隔符前后上下文的合并而出现。
	urlPartUnknown
)

// jsCtx 确定 '/' 是开始一个正则表达式字面量还是一个除法运算符。
type jsCtx uint8

//go:generate stringer -type jsCtx

const (
	// jsCtxRegexp 出现在 '/' 会开始一个正则表达式字面量的位置。
	jsCtxRegexp jsCtx = iota
	// jsCtxDivOp 出现在 '/' 会开始一个除法运算符的位置。
	jsCtxDivOp
	// jsCtxUnknown 出现在由于上下文合并导致 '/' 含义不明确的位置。
	jsCtxUnknown
)

// element 标识处于开始标签或特殊体内部时的 HTML 元素。
// 某些 HTML 元素（例如 <script> 和 <style>）的体的处理方式
// 与 stateText 不同，因此元素类型对于在标签结束时
// 过渡到正确的上下文以及标识体的结束分隔符是必要的。
type element uint8

//go:generate stringer -type element

const (
	// elementNone 出现在特殊标签或特殊元素体之外。
	elementNone element = iota
	// elementScript 对应具有 JS MIME 类型或没有 type 属性的原始文本 <script> 元素。
	elementScript
	// elementStyle 对应原始文本 <style> 元素。
	elementStyle
	// elementTextarea 对应 RCDATA <textarea> 元素。
	elementTextarea
	// elementTitle 对应 RCDATA <title> 元素。
	elementTitle
	// elementMeta 对应 HTML <meta> 元素。
	elementMeta
)

//go:generate stringer -type attr

// attr 标识处于属性内部时的当前 HTML 属性，
// 即从 stateAttrName 开始直到 stateTag/stateText（不含）。
type attr uint8

const (
	// attrNone 对应普通属性或无属性。
	attrNone attr = iota
	// attrScript 对应事件处理器属性。
	attrScript
	// attrScriptType 对应 script HTML 元素中的 type 属性。
	attrScriptType
	// attrStyle 对应值为 CSS 的 style 属性。
	attrStyle
	// attrURL 对应值为 URL 的属性。
	attrURL
	// attrSrcset 对应 srcset 属性。
	attrSrcset
	// attrMetaContent 对应 meta HTML 元素中的 content 属性。
	attrMetaContent
)
