// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"bytes"
	"strings"
)

// transitionFunc 是文本节点的上下文转换函数数组。
// 转换函数接受一个上下文和模板文本输入，返回更新后的上下文和从输入前端消耗的字节数。
var transitionFunc = [...]func(context, []byte) (context, int){
	stateText:           tText,
	stateTag:            tTag,
	stateAttrName:       tAttrName,
	stateAfterName:      tAfterName,
	stateBeforeValue:    tBeforeValue,
	stateHTMLCmt:        tHTMLCmt,
	stateRCDATA:         tSpecialTagEnd,
	stateAttr:           tAttr,
	stateURL:            tURL,
	stateMetaContent:    tMetaContent,
	stateMetaContentURL: tMetaContentURL,
	stateSrcset:         tURL,
	stateJS:             tJS,
	stateJSDqStr:        tJSDelimited,
	stateJSSqStr:        tJSDelimited,
	stateJSRegexp:       tJSDelimited,
	stateJSTmplLit:      tJSTmpl,
	stateJSBlockCmt:     tBlockCmt,
	stateJSLineCmt:      tLineCmt,
	stateJSHTMLOpenCmt:  tLineCmt,
	stateJSHTMLCloseCmt: tLineCmt,
	stateCSS:            tCSS,
	stateCSSDqStr:       tCSSStr,
	stateCSSSqStr:       tCSSStr,
	stateCSSDqURL:       tCSSStr,
	stateCSSSqURL:       tCSSStr,
	stateCSSURL:         tCSSStr,
	stateCSSBlockCmt:    tBlockCmt,
	stateCSSLineCmt:     tLineCmt,
	stateError:          tError,
}

var commentStart = []byte("<!--")
var commentEnd = []byte("-->")

// tText 是文本状态的上下文转换函数。
func tText(c context, s []byte) (context, int) {
	k := 0
	for {
		i := k + bytes.IndexByte(s[k:], '<')
		if i < k || i+1 == len(s) {
			return c, len(s)
		} else if i+4 <= len(s) && bytes.Equal(commentStart, s[i:i+4]) {
			return context{state: stateHTMLCmt}, i + 4
		}
		i++
		end := false
		if s[i] == '/' {
			if i+1 == len(s) {
				return c, len(s)
			}
			end, i = true, i+1
		}
		j, e := eatTagName(s, i)
		if j != i {
			if end {
				e = elementNone
			}
			// 我们找到了一个 HTML 标签。
			return context{state: stateTag, element: e}, j
		}
		k = j
	}
}

var elementContentType = [...]state{
	elementNone:     stateText,
	elementScript:   stateJS,
	elementStyle:    stateCSS,
	elementTextarea: stateRCDATA,
	elementTitle:    stateRCDATA,
	elementMeta:     stateText,
}

// tTag 是标签状态的上下文转换函数。
func tTag(c context, s []byte) (context, int) {
	// 查找属性名称。
	i := eatWhiteSpace(s, 0)
	if i == len(s) {
		return c, len(s)
	}
	if s[i] == '>' {
		// 对 <meta> 进行特殊处理，因为它没有结束标签，
		// 我们希望为其转换到正确的状态/元素。
		if c.element == elementMeta {
			return context{state: stateText, element: elementNone}, i + 1
		}
		return context{
			state:   elementContentType[c.element],
			element: c.element,
		}, i + 1
	}
	j, err := eatAttrName(s, i)
	if err != nil {
		return context{state: stateError, err: err}, len(s)
	}
	state, attr := stateTag, attrNone
	if i == j {
		return context{
			state: stateError,
			err:   errorf(ErrBadHTML, nil, 0, "expected space, attr name, or end of tag, but got %q", s[i:]),
		}, len(s)
	}

	attrName := strings.ToLower(string(s[i:j]))
	if c.element == elementScript && attrName == "type" {
		attr = attrScriptType
	} else if c.element == elementMeta && attrName == "content" {
		attr = attrMetaContent
	} else {
		switch attrType(attrName) {
		case contentTypeURL:
			attr = attrURL
		case contentTypeCSS:
			attr = attrStyle
		case contentTypeJS:
			attr = attrScript
		case contentTypeSrcset:
			attr = attrSrcset
		}
	}

	if j == len(s) {
		state = stateAttrName
	} else {
		state = stateAfterName
	}
	return context{state: state, element: c.element, attr: attr}, j
}

// tAttrName 是 stateAttrName 的上下文转换函数。
func tAttrName(c context, s []byte) (context, int) {
	i, err := eatAttrName(s, 0)
	if err != nil {
		return context{state: stateError, err: err}, len(s)
	} else if i != len(s) {
		c.state = stateAfterName
	}
	return c, i
}

// tAfterName 是 stateAfterName 的上下文转换函数。
func tAfterName(c context, s []byte) (context, int) {
	// 查找值的起始位置。
	i := eatWhiteSpace(s, 0)
	if i == len(s) {
		return c, len(s)
	} else if s[i] != '=' {
		// 由于标签结束 '>' 和无值属性而出现。
		c.state = stateTag
		return c, i
	}
	c.state = stateBeforeValue
	// 消耗 "="。
	return c, i + 1
}

var attrStartStates = [...]state{
	attrNone:        stateAttr,
	attrScript:      stateJS,
	attrScriptType:  stateAttr,
	attrStyle:       stateCSS,
	attrURL:         stateURL,
	attrSrcset:      stateSrcset,
	attrMetaContent: stateMetaContent,
}

// tBeforeValue 是 stateBeforeValue 的上下文转换函数。
func tBeforeValue(c context, s []byte) (context, int) {
	i := eatWhiteSpace(s, 0)
	if i == len(s) {
		return c, len(s)
	}
	// 查找属性分隔符。
	delim := delimSpaceOrTagEnd
	switch s[i] {
	case '\'':
		delim, i = delimSingleQuote, i+1
	case '"':
		delim, i = delimDoubleQuote, i+1
	}
	c.state, c.delim = attrStartStates[c.attr], delim
	return c, i
}

// tHTMLCmt 是 stateHTMLCmt 的上下文转换函数。
func tHTMLCmt(c context, s []byte) (context, int) {
	if i := bytes.Index(s, commentEnd); i != -1 {
		return context{}, i + 3
	}
	return c, len(s)
}

// specialTagEndMarkers 将元素类型映射到不区分大小写地标志特殊标签体结束的字符序列。
var specialTagEndMarkers = [...][]byte{
	elementScript:   []byte("script"),
	elementStyle:    []byte("style"),
	elementTextarea: []byte("textarea"),
	elementTitle:    []byte("title"),
	elementMeta:     []byte(""),
}

var (
	specialTagEndPrefix = []byte("</")
	tagEndSeparators    = []byte("> \t\n\f/")
)

// tSpecialTagEnd 是原始文本和 RCDATA 元素状态的上下文转换函数。
func tSpecialTagEnd(c context, s []byte) (context, int) {
	if c.element != elementNone {
		// 脚本字面量中的 script 结束标签（"</script"）会被忽略，
		// 以便我们能正确地转义它们。
		if c.element == elementScript && (isInScriptLiteral(c.state) || isComment(c.state)) {
			return c, len(s)
		}
		if i := indexTagEnd(s, specialTagEndMarkers[c.element]); i != -1 {
			return context{}, i
		}
	}
	return c, len(s)
}

// indexTagEnd 以不区分大小写的方式查找特殊标签结束的索引，如果未找到则返回 -1。
func indexTagEnd(s []byte, tag []byte) int {
	res := 0
	plen := len(specialTagEndPrefix)
	for len(s) > 0 {
		// 首先尝试查找标签结束前缀
		i := bytes.Index(s, specialTagEndPrefix)
		if i == -1 {
			return i
		}
		s = s[i+plen:]
		// 如果还有空间，则尝试匹配实际标签
		if len(tag) <= len(s) && bytes.EqualFold(tag, s[:len(tag)]) {
			s = s[len(tag):]
			// 检查标签后面是否跟着正确的分隔符
			if len(s) > 0 && bytes.IndexByte(tagEndSeparators, s[0]) != -1 {
				return res + i
			}
			res += len(tag)
		}
		res += i + plen
	}
	return -1
}

// tAttr 是属性状态的上下文转换函数。
func tAttr(c context, s []byte) (context, int) {
	return c, len(s)
}

// tURL 是 URL 状态的上下文转换函数。
func tURL(c context, s []byte) (context, int) {
	if bytes.ContainsAny(s, "#?") {
		c.urlPart = urlPartQueryOrFrag
	} else if len(s) != eatWhiteSpace(s, 0) && c.urlPart == urlPartNone {
		// HTML5 对属性使用 "可能被空格包围的有效 URL"：
		// https://www.w3.org/TR/html5/index.html#attributes-1
		c.urlPart = urlPartPreQuery
	}
	return c, len(s)
}

// tJS 是 JS 状态的上下文转换函数。
func tJS(c context, s []byte) (context, int) {
	i := bytes.IndexAny(s, "\"`'/{}<-#")
	if i == -1 {
		// 整个输入都是非字符串、非注释、非正则表达式的 token。
		c.jsCtx = nextJSCtx(s, c.jsCtx)
		return c, len(s)
	}
	c.jsCtx = nextJSCtx(s[:i], c.jsCtx)
	switch s[i] {
	case '"':
		c.state, c.jsCtx = stateJSDqStr, jsCtxRegexp
	case '\'':
		c.state, c.jsCtx = stateJSSqStr, jsCtxRegexp
	case '`':
		c.state, c.jsCtx = stateJSTmplLit, jsCtxRegexp
	case '/':
		switch {
		case i+1 < len(s) && s[i+1] == '/':
			c.state, i = stateJSLineCmt, i+1
		case i+1 < len(s) && s[i+1] == '*':
			c.state, i = stateJSBlockCmt, i+1
		case c.jsCtx == jsCtxRegexp:
			c.state = stateJSRegexp
		case c.jsCtx == jsCtxDivOp:
			c.jsCtx = jsCtxRegexp
		default:
			return context{
				state: stateError,
				err:   errorf(ErrSlashAmbig, nil, 0, "'/' could start a division or regexp: %.32q", s[i:]),
			}, len(s)
		}
	// 由于历史原因，ECMAScript 支持 HTML 风格的注释，参见附录
	// B.1.1 "类 HTML 注释"。这些注释的处理方式有些令人困惑。
	// 不支持多行注释，即开启和关闭标记之间行上的任何内容都不被视为注释，
	// 但开启或关闭标记之后同一行上的任何内容都会被忽略。
	// 因此，我们只是将任何以 "<!--" 或 "-->" 为前缀的行
	// 视为实际以 "//" 为前缀并继续处理。
	case '<':
		if i+3 < len(s) && bytes.Equal(commentStart, s[i:i+4]) {
			c.state, i = stateJSHTMLOpenCmt, i+3
		}
	case '-':
		if i+2 < len(s) && bytes.Equal(commentEnd, s[i:i+3]) {
			c.state, i = stateJSHTMLCloseCmt, i+2
		}
	// ECMAScript 还支持 "hashbang" 注释行，参见第 12.5 节。
	case '#':
		if i+1 < len(s) && s[i+1] == '!' {
			c.state, i = stateJSLineCmt, i+1
		}
	case '{':
		// 只有当我们在模板字面量内部时，才需要跟踪花括号深度。
		if len(c.jsBraceDepth) == 0 {
			return c, i + 1
		}
		c.jsBraceDepth[len(c.jsBraceDepth)-1]++
	case '}':
		if len(c.jsBraceDepth) == 0 {
			return c, i + 1
		}
		// 在 JS 上下文中，似乎没有花括号可以被转义而不产生语法错误的情况。
		// 因此我们可以将 "\}" 计为 "}" 并继续，
		// 脚本已经损坏，因为完整的解析器无论如何都会失败。
		c.jsBraceDepth[len(c.jsBraceDepth)-1]--
		if c.jsBraceDepth[len(c.jsBraceDepth)-1] >= 0 {
			return c, i + 1
		}
		c.jsBraceDepth = c.jsBraceDepth[:len(c.jsBraceDepth)-1]
		c.state = stateJSTmplLit
	default:
		panic("unreachable")
	}
	return c, i + 1
}

func tJSTmpl(c context, s []byte) (context, int) {
	var k int
	for {
		i := k + bytes.IndexAny(s[k:], "`\\$")
		if i < k {
			break
		}
		switch s[i] {
		case '\\':
			i++
			if i == len(s) {
				return context{
					state: stateError,
					err:   errorf(ErrPartialEscape, nil, 0, "unfinished escape sequence in JS string: %q", s),
				}, len(s)
			}
		case '$':
			if len(s) >= i+2 && s[i+1] == '{' {
				c.jsBraceDepth = append(c.jsBraceDepth, 0)
				c.state = stateJS
				return c, i + 2
			}
		case '`':
			// end
			c.state = stateJS
			return c, i + 1
		}
		k = i + 1
	}

	return c, len(s)
}

// tJSDelimited 是 JS 字符串和正则表达式状态的上下文转换函数。
func tJSDelimited(c context, s []byte) (context, int) {
	specials := `\"`
	switch c.state {
	case stateJSSqStr:
		specials = `\'`
	case stateJSRegexp:
		specials = `\/[]`
	}

	k, inCharset := 0, false
	for {
		i := k + bytes.IndexAny(s[k:], specials)
		if i < k {
			break
		}
		switch s[i] {
		case '\\':
			i++
			if i == len(s) {
				return context{
					state: stateError,
					err:   errorf(ErrPartialEscape, nil, 0, "unfinished escape sequence in JS string: %q", s),
				}, len(s)
			}
		case '[':
			inCharset = true
		case ']':
			inCharset = false
		case '/':
			// 如果 "</script" 出现在正则表达式字面量中，'/' 不应关闭
			// 正则表达式字面量，它稍后会在 escapeText 中被转义为
			// "\x3C/script"。
			if i > 0 && i+7 <= len(s) && bytes.Equal(bytes.ToLower(s[i-1:i+7]), []byte("</script")) {
				i++
			} else if !inCharset {
				c.state, c.jsCtx = stateJS, jsCtxDivOp
				return c, i + 1
			}
		default:
			// 结束分隔符
			if !inCharset {
				c.state, c.jsCtx = stateJS, jsCtxDivOp
				return c, i + 1
			}
		}
		k = i + 1
	}

	if inCharset {
		// 如果需要在字符集中进行插值，可以通过丰富 context 来修复此问题。
		return context{
			state: stateError,
			err:   errorf(ErrPartialCharset, nil, 0, "unfinished JS regexp charset: %q", s),
		}, len(s)
	}

	return c, len(s)
}

var blockCommentEnd = []byte("*/")

// tBlockCmt 是 /*注释*/ 状态的上下文转换函数。
func tBlockCmt(c context, s []byte) (context, int) {
	i := bytes.Index(s, blockCommentEnd)
	if i == -1 {
		return c, len(s)
	}
	switch c.state {
	case stateJSBlockCmt:
		c.state = stateJS
	case stateCSSBlockCmt:
		c.state = stateCSS
	default:
		panic(c.state.String())
	}
	return c, i + 2
}

// tLineCmt 是 //注释状态和 JS 类 HTML 注释状态的上下文转换函数。
func tLineCmt(c context, s []byte) (context, int) {
	var lineTerminators string
	var endState state
	switch c.state {
	case stateJSLineCmt, stateJSHTMLOpenCmt, stateJSHTMLCloseCmt:
		lineTerminators, endState = "\n\r\u2028\u2029", stateJS
	case stateCSSLineCmt:
		lineTerminators, endState = "\n\f\r", stateCSS
		// 行注释不是任何已发布 CSS 标准的一部分，
		// 但被 4 大主流浏览器支持。
		// 这将行注释定义为
		//     LINECOMMENT ::= "//" [^\n\f\d]*
		// 因为 https://www.w3.org/TR/css3-syntax/#SUBTOK-nl 定义了换行：
		//     nl ::= #xA | #xD #xA | #xD | #xC
	default:
		panic(c.state.String())
	}

	i := bytes.IndexAny(s, lineTerminators)
	if i == -1 {
		return c, len(s)
	}
	c.state = endState
	// 根据 EcmaScript 5 第 7.4 节：https://es5.github.io/#x7.4
	// "但是，行末的 LineTerminator 不被视为单行注释的一部分；
	// 它由词法语法单独识别，并成为语法分析的输入元素流的一部分。"
	return c, i
}

// tCSS 是 CSS 状态的上下文转换函数。
func tCSS(c context, s []byte) (context, int) {
	// CSS 带引号的字符串几乎从不使用，除了：
	// (1) URL，如 background: "/foo.png"
	// (2) 多单词字体名称，如 font-family: "Times New Roman"
	// (3) 内联列表中 content 值的列表分隔符：
	//    <style>
	//    ul.inlineList { list-style: none; padding:0 }
	//    ul.inlineList > li { display: inline }
	//    ul.inlineList > li:before { content: ", " }
	//    ul.inlineList > li:first-child:before { content: "" }
	//    </style>
	//    <ul class=inlineList><li>One<li>Two<li>Three</ul>
	// (4) 属性值选择器，如 a[href="http://example.com/"]
	//
	// 我们保守地将所有字符串视为 URL，但做了一些让步以避免混淆。
	//
	// 在 (1) 中，我们的保守假设是合理的。
	// 在 (2) 中，有效的字体名称不包含 ':'、'?' 或 '#'，因此我们的
	// 保守假设是可以的，因为我们永远不会转换过 urlPartPreQuery。
	// 在 (3) 中，我们的协议启发式不应被触发，且 '?' 或 '#' 后面
	// 不应有非空格内容，因此只要我们仅对 RFC 3986 保留字符进行百分号编码就可以。
	// 在 (4) 中，对于 URL 属性我们应进行 URL 转义，对于其他属性，
	// 如果我们的保守假设对实际代码造成问题，我们有属性名称可用。

	k := 0
	for {
		i := k + bytes.IndexAny(s[k:], `("'/`)
		if i < k {
			return c, len(s)
		}
		switch s[i] {
		case '(':
			// 向左查找 url。
			p := bytes.TrimRight(s[:i], "\t\n\f\r ")
			if endsWithCSSKeyword(p, "url") {
				j := len(s) - len(bytes.TrimLeft(s[i+1:], "\t\n\f\r "))
				switch {
				case j != len(s) && s[j] == '"':
					c.state, j = stateCSSDqURL, j+1
				case j != len(s) && s[j] == '\'':
					c.state, j = stateCSSSqURL, j+1
				default:
					c.state = stateCSSURL
				}
				return c, j
			}
		case '/':
			if i+1 < len(s) {
				switch s[i+1] {
				case '/':
					c.state = stateCSSLineCmt
					return c, i + 2
				case '*':
					c.state = stateCSSBlockCmt
					return c, i + 2
				}
			}
		case '"':
			c.state = stateCSSDqStr
			return c, i + 1
		case '\'':
			c.state = stateCSSSqStr
			return c, i + 1
		}
		k = i + 1
	}
}

// tCSSStr 是 CSS 字符串和 URL 状态的上下文转换函数。
func tCSSStr(c context, s []byte) (context, int) {
	var endAndEsc string
	switch c.state {
	case stateCSSDqStr, stateCSSDqURL:
		endAndEsc = `\"`
	case stateCSSSqStr, stateCSSSqURL:
		endAndEsc = `\'`
	case stateCSSURL:
		// 未加引号的 URL 以换行或右括号结束。
		// 以下包含 wc（空白字符）和 nl。
		endAndEsc = "\\\t\n\f\r )"
	default:
		panic(c.state.String())
	}

	k := 0
	for {
		i := k + bytes.IndexAny(s[k:], endAndEsc)
		if i < k {
			c, nread := tURL(c, decodeCSS(s[k:]))
			return c, k + nread
		}
		if s[i] == '\\' {
			i++
			if i == len(s) {
				return context{
					state: stateError,
					err:   errorf(ErrPartialEscape, nil, 0, "unfinished escape sequence in CSS string: %q", s),
				}, len(s)
			}
		} else {
			c.state = stateCSS
			return c, i + 1
		}
		c, _ = tURL(c, decodeCSS(s[:i+1]))
		k = i + 1
	}
}

// tError 是错误状态的上下文转换函数。
func tError(c context, s []byte) (context, int) {
	return c, len(s)
}

// tMetaContent 是 meta content 属性状态的上下文转换函数。
func tMetaContent(c context, s []byte) (context, int) {
	for i := 0; i < len(s); i++ {
		if i+3 <= len(s)-1 && bytes.Equal(bytes.ToLower(s[i:i+4]), []byte("url=")) {
			c.state = stateMetaContentURL
			return c, i + 4
		}
	}
	return c, len(s)
}

// tMetaContentURL 是 meta content 属性状态中 "url=" 部分的上下文转换函数。
func tMetaContentURL(c context, s []byte) (context, int) {
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			c.state = stateMetaContent
			return c, i + 1
		}
	}
	return c, len(s)
}

// eatAttrName 返回最大的 j，使得 s[i:j] 是一个属性名称。
// 如果 s[i:] 看起来不像是以属性名称开头（例如遇到引号而前面没有等号），
// 则返回错误。
func eatAttrName(s []byte, i int) (int, *Error) {
	for j := i; j < len(s); j++ {
		switch s[j] {
		case ' ', '\t', '\n', '\f', '\r', '=', '>':
			return j, nil
		case '\'', '"', '<':
			// 这些在 HTML5 中会导致解析警告，
			// 如果在模板的属性名称中看到它们，则表明存在严重问题。
			return -1, errorf(ErrBadHTML, nil, 0, "%q in attribute name: %.32q", s[j:j+1], s)
		default:
			// 空操作。
		}
	}
	return len(s), nil
}

var elementNameMap = map[string]element{
	"script":   elementScript,
	"style":    elementStyle,
	"textarea": elementTextarea,
	"title":    elementTitle,
	"meta":     elementMeta,
}

// asciiAlpha 报告 c 是否是 ASCII 字母。
func asciiAlpha(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z'
}

// asciiAlphaNum 报告 c 是否是 ASCII 字母或数字。
func asciiAlphaNum(c byte) bool {
	return asciiAlpha(c) || '0' <= c && c <= '9'
}

// eatTagName 返回最大的 j，使得 s[i:j] 是一个标签名称，并返回标签类型。
func eatTagName(s []byte, i int) (int, element) {
	if i == len(s) || !asciiAlpha(s[i]) {
		return i, elementNone
	}
	j := i + 1
	for j < len(s) {
		x := s[j]
		if asciiAlphaNum(x) {
			j++
			continue
		}
		// 允许 "x-y" 或 "x:y"，但不允许 "x-"、"-y" 或 "x--y"。
		if (x == ':' || x == '-') && j+1 < len(s) && asciiAlphaNum(s[j+1]) {
			j += 2
			continue
		}
		break
	}
	return j, elementNameMap[strings.ToLower(string(s[i:j]))]
}

// eatWhiteSpace 返回最大的 j，使得 s[i:j] 是空白字符。
func eatWhiteSpace(s []byte, i int) int {
	for j := i; j < len(s); j++ {
		switch s[j] {
		case ' ', '\t', '\n', '\f', '\r':
			// 空操作。
		default:
			return j
		}
	}
	return len(s)
}
