// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"bytes"
	"fmt"
	"html"
	"internal/godebug"
	"io"
	"maps"
	"regexp"
	"text/template"
	"text/template/parse"
)

// escapeTemplate 重写命名模板（必须与 t 关联），以保证任何命名模板的输出
// 都被正确转义。如果没有返回错误，则命名模板已被修改。
// 否则命名模板已被标记为不可用。
func escapeTemplate(tmpl *Template, node parse.Node, name string) error {
	c, _ := tmpl.esc.escapeTree(context{}, node, name, 0)
	var err error
	if c.err != nil {
		err, c.err.Name = c.err, name
	} else if c.state != stateText {
		err = &Error{ErrEndContext, nil, name, 0, fmt.Sprintf("ends in a non-text context: %v", c)}
	}
	if err != nil {
		// 阻止执行不安全的模板。
		if t := tmpl.set[name]; t != nil {
			t.escapeErr = err
			t.text.Tree = nil
			t.Tree = nil
		}
		return err
	}
	tmpl.esc.commit()
	if t := tmpl.set[name]; t != nil {
		t.escapeErr = escapeOK
		t.Tree = t.text.Tree
	}
	return nil
}

// evalArgs 将参数列表格式化为字符串。它等价于
// fmt.Sprint(args...)，但会解引用所有指针。
func evalArgs(args ...any) string {
	// 针对单个字符串参数的简单常见情况的优化。
	if len(args) == 1 {
		if s, ok := args[0].(string); ok {
			return s
		}
	}
	for i, arg := range args {
		args[i] = indirectToStringerOrError(arg)
	}
	return fmt.Sprint(args...)
}

// funcMap 将命令名称映射到使其输入安全的函数。
var funcMap = template.FuncMap{
	"_html_template_attrescaper":      attrEscaper,
	"_html_template_commentescaper":   commentEscaper,
	"_html_template_cssescaper":       cssEscaper,
	"_html_template_cssvaluefilter":   cssValueFilter,
	"_html_template_htmlnamefilter":   htmlNameFilter,
	"_html_template_htmlescaper":      htmlEscaper,
	"_html_template_jsregexpescaper":  jsRegexpEscaper,
	"_html_template_jsstrescaper":     jsStrEscaper,
	"_html_template_jstmpllitescaper": jsTmplLitEscaper,
	"_html_template_jsvalescaper":     jsValEscaper,
	"_html_template_nospaceescaper":   htmlNospaceEscaper,
	"_html_template_rcdataescaper":    rcdataEscaper,
	"_html_template_srcsetescaper":    srcsetFilterAndEscaper,
	"_html_template_urlescaper":       urlEscaper,
	"_html_template_urlfilter":        urlFilter,
	"_html_template_urlnormalizer":    urlNormalizer,
	"_eval_args_":                     evalArgs,
}

// escaper 收集关于模板的类型推断和使模板注入安全所需的更改。
type escaper struct {
	// ns 是此 escaper 关联的 nameSpace。
	ns *nameSpace
	// output[templateName] 是已被名称改编以包含其输入上下文的 templateName 的输出上下文。
	output map[string]context
	// derived[c.mangle(name)] 映射到从名为 name 的模板在起始上下文 c 下派生的模板。
	derived map[string]*template.Template
	// called[templateName] 是已调用的改编模板名称的集合。
	called map[string]bool
	// xxxNodeEdits 是在 commit 期间要应用的累积编辑。
	// 这些编辑不会立即应用，以防模板集在不同的转义上下文中执行给定模板。
	actionNodeEdits   map[*parse.ActionNode][]string
	templateNodeEdits map[*parse.TemplateNode]string
	textNodeEdits     map[*parse.TextNode][]byte
	// rangeContext 保存关于当前 range 循环的上下文。
	rangeContext *rangeContext
}

// rangeContext 保存关于当前 range 循环的信息。
type rangeContext struct {
	outer     *rangeContext // 外层循环
	breaks    []context     // 每个 break action 处的上下文
	continues []context     // 每个 continue action 处的上下文
}

// makeEscaper 为给定的集合创建一个空白的 escaper。
func makeEscaper(n *nameSpace) escaper {
	return escaper{
		n,
		map[string]context{},
		map[string]*template.Template{},
		map[string]bool{},
		map[*parse.ActionNode][]string{},
		map[*parse.TemplateNode]string{},
		map[*parse.TextNode][]byte{},
		nil,
	}
}

// filterFailsafe 是一个无害的词，由净化函数代替不安全的值输出。
// 它不是任何编程语言的关键字，不包含特殊字符，不为空，
// 并且当它出现在输出中时足够独特，开发者可以通过搜索引擎找到问题的来源。
const filterFailsafe = "ZgotmplZ"

// escape 转义一个模板节点。
func (e *escaper) escape(c context, n parse.Node) context {
	switch n := n.(type) {
	case *parse.ActionNode:
		return e.escapeAction(c, n)
	case *parse.BreakNode:
		c.n = n
		e.rangeContext.breaks = append(e.rangeContext.breaks, c)
		return context{state: stateDead}
	case *parse.CommentNode:
		return c
	case *parse.ContinueNode:
		c.n = n
		e.rangeContext.continues = append(e.rangeContext.continues, c)
		return context{state: stateDead}
	case *parse.IfNode:
		return e.escapeBranch(c, &n.BranchNode, "if")
	case *parse.ListNode:
		return e.escapeList(c, n)
	case *parse.RangeNode:
		return e.escapeBranch(c, &n.BranchNode, "range")
	case *parse.TemplateNode:
		return e.escapeTemplate(c, n)
	case *parse.TextNode:
		return e.escapeText(c, n)
	case *parse.WithNode:
		return e.escapeBranch(c, &n.BranchNode, "with")
	}
	panic("escaping " + n.String() + " is unimplemented")
}

var debugAllowActionJSTmpl = godebug.New("jstmpllitinterp")

var htmlmetacontenturlescape = godebug.New("htmlmetacontenturlescape")

// escapeAction 转义一个 action 模板节点。
func (e *escaper) escapeAction(c context, n *parse.ActionNode) context {
	if len(n.Pipe.Decl) != 0 {
		// 局部变量赋值，不是插值。
		return c
	}
	c = nudge(c)
	// 检查管道中是否有不允许的预定义转义器使用。
	for pos, idNode := range n.Pipe.Cmds {
		node, ok := idNode.Args[0].(*parse.IdentifierNode)
		if !ok {
			// 预定义转义器 "esc" 永远不会作为 Chain 或 Field 节点中的标识符出现，因为：
			// - "esc.x ..." 是无效的，因为预定义转义器返回字符串，字符串没有方法、键或字段。
			// - "... .esc" 是无效的，因为预定义转义器是全局函数，不是任何类型的方法或字段。
			// 因此，忽略这两种节点类型是安全的。
			continue
		}
		ident := node.Ident
		if _, ok := predefinedEscapers[ident]; ok {
			if pos < len(n.Pipe.Cmds)-1 ||
				c.state == stateAttr && c.delim == delimSpaceOrTagEnd && ident == "html" {
				return context{
					state: stateError,
					err:   errorf(ErrPredefinedEscaper, n, n.Line, "predefined escaper %q disallowed in template", ident),
				}
			}
		}
	}
	s := make([]string, 0, 3)
	switch c.state {
	case stateError:
		return c
	case stateURL, stateCSSDqStr, stateCSSSqStr, stateCSSDqURL, stateCSSSqURL, stateCSSURL:
		switch c.urlPart {
		case urlPartNone:
			s = append(s, "_html_template_urlfilter")
			fallthrough
		case urlPartPreQuery:
			switch c.state {
			case stateCSSDqStr, stateCSSSqStr:
				s = append(s, "_html_template_cssescaper")
			default:
				s = append(s, "_html_template_urlnormalizer")
			}
		case urlPartQueryOrFrag:
			s = append(s, "_html_template_urlescaper")
		case urlPartUnknown:
			return context{
				state: stateError,
				err:   errorf(ErrAmbigContext, n, n.Line, "%s appears in an ambiguous context within a URL", n),
			}
		default:
			panic(c.urlPart.String())
		}
	case stateMetaContent:
		// 在下面的 delim 检查中处理。
	case stateMetaContentURL:
		if htmlmetacontenturlescape.Value() != "0" {
			s = append(s, "_html_template_urlfilter")
		} else {
			// 我们没有一个很好的地方来递增这个计数，因为很难知道我们是否
			// 实际在 _html_template_urlfilter 中转义了任何 URL，
			// 因为它没有关于执行上下文等的信息。这可能是我们能做的最好的了。
			htmlmetacontenturlescape.IncNonDefault()
		}
	case stateJS:
		s = append(s, "_html_template_jsvalescaper")
		// 值之后的斜杠开始一个除法运算符。
		c.jsCtx = jsCtxDivOp
	case stateJSDqStr, stateJSSqStr:
		s = append(s, "_html_template_jsstrescaper")
	case stateJSTmplLit:
		s = append(s, "_html_template_jstmpllitescaper")
	case stateJSRegexp:
		s = append(s, "_html_template_jsregexpescaper")
	case stateCSS:
		s = append(s, "_html_template_cssvaluefilter")
	case stateText:
		s = append(s, "_html_template_htmlescaper")
	case stateRCDATA:
		s = append(s, "_html_template_rcdataescaper")
	case stateAttr:
		// 在下面的 delim 检查中处理。
	case stateAttrName, stateTag:
		c.state = stateAttrName
		s = append(s, "_html_template_htmlnamefilter")
	case stateSrcset:
		s = append(s, "_html_template_srcsetescaper")
	default:
		if isComment(c.state) {
			s = append(s, "_html_template_commentescaper")
		} else {
			panic("unexpected state " + c.state.String())
		}
	}
	switch c.delim {
	case delimNone:
		// 原始文本内容不需要额外转义。
	case delimSpaceOrTagEnd:
		s = append(s, "_html_template_nospaceescaper")
	default:
		s = append(s, "_html_template_attrescaper")
	}
	e.editActionNode(n, s)
	return c
}

// ensurePipelineContains 确保管道按顺序以 s 中标识符对应的命令结尾。
// 如果管道以预定义转义器（即 "html" 或 "urlquery"）结尾，则将其与 s 中的标识符合并。
func ensurePipelineContains(p *parse.PipeNode, s []string) {
	if len(s) == 0 {
		// 如果没有要插入的转义器，不重写管道。
		return
	}
	// 前置条件：p.Cmds 最多包含一个预定义转义器，且该转义器位于
	// p.Cmds[len(p.Cmds)-1]。由于 escapeAction 中的检查，此前置条件始终为真。
	pipelineLen := len(p.Cmds)
	if pipelineLen > 0 {
		lastCmd := p.Cmds[pipelineLen-1]
		if idNode, ok := lastCmd.Args[0].(*parse.IdentifierNode); ok {
			if esc := idNode.Ident; predefinedEscapers[esc] {
				// 管道以预定义转义器结尾。
				if len(p.Cmds) == 1 && len(lastCmd.Args) > 1 {
					// 特殊情况：管道形式为 {{ esc arg1 arg2 ... argN }}，
					// 其中 esc 是预定义转义器，arg1...argN 是其参数。
					// 将其转换为等价形式
					// {{ _eval_args_ arg1 arg2 ... argN | esc }}，以便 esc 可以轻松
					// 与 s 中的转义器合并。
					lastCmd.Args[0] = parse.NewIdentifier("_eval_args_").SetTree(nil).SetPos(lastCmd.Args[0].Position())
					p.Cmds = appendCmd(p.Cmds, newIdentCmd(esc, p.Position()))
					pipelineLen++
				}
				// 如果我们即将插入的 s 中的任何命令等价于预定义转义器，则使用预定义转义器代替。
				dup := false
				for i, escaper := range s {
					if escFnsEq(esc, escaper) {
						s[i] = idNode.Ident
						dup = true
					}
				}
				if dup {
					// 预定义转义器将与 s 中的转义器一起被插入，因此不要将其复制到重写的管道中。
					pipelineLen--
				}
			}
		}
	}
	// 重写管道，在管道末尾创建 s 中的转义器。
	newCmds := make([]*parse.CommandNode, pipelineLen, pipelineLen+len(s))
	insertedIdents := make(map[string]bool)
	for i := 0; i < pipelineLen; i++ {
		cmd := p.Cmds[i]
		newCmds[i] = cmd
		if idNode, ok := cmd.Args[0].(*parse.IdentifierNode); ok {
			insertedIdents[normalizeEscFn(idNode.Ident)] = true
		}
	}
	for _, name := range s {
		if !insertedIdents[normalizeEscFn(name)] {
			// 当两个模板通过 AddParseTree 共享底层解析树，且一个模板在另一个之后执行时，
			// 此检查确保在第一次转义过程中已插入管道的转义器不会被再次插入。
			newCmds = appendCmd(newCmds, newIdentCmd(name, p.Position()))
		}
	}
	p.Cmds = newCmds
}

// predefinedEscapers 包含等价于某些上下文转义器的模板预定义转义器。与 equivEscapers 保持同步。
var predefinedEscapers = map[string]bool{
	"html":     true,
	"urlquery": true,
}

// equivEscapers 将上下文转义器匹配到等价的预定义模板转义器。
var equivEscapers = map[string]string{
	// 以下 HTML 转义器对提供等价的安全保证，因为它们都转义 '\000'、'\''、'"'、'&'、'<' 和 '>'。
	"_html_template_attrescaper":   "html",
	"_html_template_htmlescaper":   "html",
	"_html_template_rcdataescaper": "html",
	// 这两个 URL 转义器通过对 RFC 3986 第 2.2 节中指定的所有保留字符进行百分号编码，
	// 生成可安全嵌入 URL 查询的 URL。
	"_html_template_urlescaper": "urlquery",
	// 这两个函数实际上并不等价；urlquery 更严格，因为它转义保留字符（如 '#'），
	// 而 _html_template_urlnormalizer 不会。因此只有将 _html_template_urlnormalizer
	// 替换为 urlquery 是安全的（这在 ensurePipelineContains 中发生），反过来则不安全。
	// 我们保留此条目以保持在 Go 1.9 之前编写的模板的行为，这些模板可能依赖于此替换的发生。
	"_html_template_urlnormalizer": "urlquery",
}

// escFnsEq 报告两个转义函数是否等价。
func escFnsEq(a, b string) bool {
	return normalizeEscFn(a) == normalizeEscFn(b)
}

// normalizeEscFn(a) 等于 normalizeEscFn(b)，对于任何等价的转义器函数名称对 a 和 b。
func normalizeEscFn(e string) string {
	if norm := equivEscapers[e]; norm != "" {
		return norm
	}
	return e
}

// redundantFuncs[a][b] 意味着对所有 x，funcMap[b](funcMap[a](x)) == funcMap[a](x)。
var redundantFuncs = map[string]map[string]bool{
	"_html_template_commentescaper": {
		"_html_template_attrescaper": true,
		"_html_template_htmlescaper": true,
	},
	"_html_template_cssescaper": {
		"_html_template_attrescaper": true,
	},
	"_html_template_jsregexpescaper": {
		"_html_template_attrescaper": true,
	},
	"_html_template_jsstrescaper": {
		"_html_template_attrescaper": true,
	},
	"_html_template_jstmpllitescaper": {
		"_html_template_attrescaper": true,
	},
	"_html_template_urlescaper": {
		"_html_template_urlnormalizer": true,
	},
}

// appendCmd 将给定命令追加到命令管道末尾，除非它与最后一个命令冗余。
func appendCmd(cmds []*parse.CommandNode, cmd *parse.CommandNode) []*parse.CommandNode {
	if n := len(cmds); n != 0 {
		last, okLast := cmds[n-1].Args[0].(*parse.IdentifierNode)
		next, okNext := cmd.Args[0].(*parse.IdentifierNode)
		if okLast && okNext && redundantFuncs[last.Ident][next.Ident] {
			return cmds
		}
	}
	return append(cmds, cmd)
}

// newIdentCmd 生成一个包含单个标识符节点的命令。
func newIdentCmd(identifier string, pos parse.Pos) *parse.CommandNode {
	return &parse.CommandNode{
		NodeType: parse.NodeCommand,
		Args:     []parse.Node{parse.NewIdentifier(identifier).SetTree(nil).SetPos(pos)}, // TODO: SetTree.
	}
}

// nudge 返回从输入上下文跟随空字符串转换后产生的上下文。
// 例如，解析：
//
//	`<a href=`
//
// 将以 context{stateBeforeValue, attrURL} 结束，但再解析一个字符：
//
//	`<a href=x`
//
// 将以 context{stateURL, delimSpaceOrTagEnd, ...} 结束。
// 当看到 'x' 时发生两个转换：
// (1) 从 before-value 状态转换到 start-of-value 状态，不消耗任何字符。
//
// (2) 消耗 'x' 并转换过第一个值字符。
// 在这种情况下，nudge 产生 (1) 发生后的上下文。
func nudge(c context) context {
	switch c.state {
	case stateTag:
		// 在 `<foo {{.}}` 中，action 应发出一个属性。
		c.state = stateAttrName
	case stateBeforeValue:
		// 在 `<foo bar={{.}}` 中，action 是一个无分隔符的值。
		c.state, c.delim, c.attr = attrStartStates[c.attr], delimSpaceOrTagEnd, attrNone
	case stateAfterName:
		// 在 `<foo bar {{.}}` 中，action 是一个属性名。
		c.state, c.attr = stateAttrName, attrNone
	}
	return c
}

// join 合并分支模板节点的两个上下文。如果任一输入上下文是错误上下文，
// 或输入上下文不同，则结果为错误上下文。
func join(a, b context, node parse.Node, nodeName string) context {
	if a.state == stateError {
		return a
	}
	if b.state == stateError {
		return b
	}
	if a.state == stateDead {
		return b
	}
	if b.state == stateDead {
		return a
	}
	if a.eq(b) {
		return a
	}

	c := a
	c.urlPart = b.urlPart
	if c.eq(b) {
		// 上下文仅在 urlPart 上不同。
		c.urlPart = urlPartUnknown
		return c
	}

	c = a
	c.jsCtx = b.jsCtx
	if c.eq(b) {
		// 上下文仅在 jsCtx 上不同。
		c.jsCtx = jsCtxUnknown
		return c
	}

	// 允许调整后的上下文与未调整的上下文合并。
	// 这意味着
	//   <p title={{if .C}}{{.}}{{end}}
	// 即使 else 分支在 stateBeforeValue 中结束，也以未加引号的值状态结束。
	if c, d := nudge(a), nudge(b); !(c.eq(a) && d.eq(b)) {
		if e := join(c, d, node, nodeName); e.state != stateError {
			return e
		}
	}

	return context{
		state: stateError,
		err:   errorf(ErrBranchEnd, node, 0, "{{%s}} branches end in different contexts: %v, %v", nodeName, a, b),
	}
}

// escapeBranch 转义分支模板节点："if"、"range" 和 "with"。
func (e *escaper) escapeBranch(c context, n *parse.BranchNode, nodeName string) context {
	if nodeName == "range" {
		e.rangeContext = &rangeContext{outer: e.rangeContext}
	}
	c0 := e.escapeList(c, n.List)
	if nodeName == "range" {
		if c0.state != stateError {
			c0 = joinRange(c0, e.rangeContext)
		}
		e.rangeContext = e.rangeContext.outer
		if c0.state == stateError {
			return c0
		}

		// "range" 节点的 "true" 分支可以执行多次。
		// 我们检查执行 n.List 一次是否与执行 n.List 两次产生相同的上下文。
		e.rangeContext = &rangeContext{outer: e.rangeContext}
		c1, _ := e.escapeListConditionally(c0, n.List, nil)
		c0 = join(c0, c1, n, nodeName)
		if c0.state == stateError {
			e.rangeContext = e.rangeContext.outer
			// 明确表示这是循环重新进入时的问题，
			// 因为开发者在调试模板时往往会忽略该分支。
			c0.err.Line = n.Line
			c0.err.Description = "on range loop re-entry: " + c0.err.Description
			return c0
		}
		c0 = joinRange(c0, e.rangeContext)
		e.rangeContext = e.rangeContext.outer
		if c0.state == stateError {
			return c0
		}
	}
	c1 := e.escapeList(c, n.ElseList)
	return join(c0, c1, n, nodeName)
}

func joinRange(c0 context, rc *rangeContext) context {
	// 将 break 和 continue 语句处的上下文合并到整体循环体上下文中。
	// 理论上我们可以区别对待 break 和 continue，但目前将它们都视为返回循环开始处（然后可能停止）就足够了。
	for _, c := range rc.breaks {
		c0 = join(c0, c, c.n, "range")
		if c0.state == stateError {
			c0.err.Line = c.n.(*parse.BreakNode).Line
			c0.err.Description = "at range loop break: " + c0.err.Description
			return c0
		}
	}
	for _, c := range rc.continues {
		c0 = join(c0, c, c.n, "range")
		if c0.state == stateError {
			c0.err.Line = c.n.(*parse.ContinueNode).Line
			c0.err.Description = "at range loop continue: " + c0.err.Description
			return c0
		}
	}
	return c0
}

// escapeList 转义一个列表模板节点。
func (e *escaper) escapeList(c context, n *parse.ListNode) context {
	if n == nil {
		return c
	}
	for _, m := range n.Nodes {
		c = e.escape(c, m)
		if c.state == stateDead {
			break
		}
	}
	return c
}

// escapeListConditionally 转义一个列表节点，但仅当推断和输出上下文满足 filter 时
// 才保留 e 中的编辑和推断。它返回对输出上下文的最佳猜测，以及 filter 的结果
// （与 e 是否被更新相同）。
func (e *escaper) escapeListConditionally(c context, n *parse.ListNode, filter func(*escaper, context) bool) (context, bool) {
	e1 := makeEscaper(e.ns)
	e1.rangeContext = e.rangeContext
	// 使类型推断对 f 可用。
	maps.Copy(e1.output, e.output)
	c = e1.escapeList(c, n)
	ok := filter != nil && filter(&e1, c)
	if ok {
		// 将推断和编辑从 e1 复制回 e。
		maps.Copy(e.output, e1.output)
		maps.Copy(e.derived, e1.derived)
		maps.Copy(e.called, e1.called)
		for k, v := range e1.actionNodeEdits {
			e.editActionNode(k, v)
		}
		for k, v := range e1.templateNodeEdits {
			e.editTemplateNode(k, v)
		}
		for k, v := range e1.textNodeEdits {
			e.editTextNode(k, v)
		}
	}
	return c, ok
}

// escapeTemplate 转义一个 {{template}} 调用节点。
func (e *escaper) escapeTemplate(c context, n *parse.TemplateNode) context {
	c, name := e.escapeTree(c, n, n.Name, n.Line)
	if name != n.Name {
		e.editTemplateNode(n, name)
	}
	return c
}

// escapeTree 根据需要从给定上下文开始转义命名模板，并返回其输出上下文。
func (e *escaper) escapeTree(c context, node parse.Node, name string, line int) (context, string) {
	// 将模板名称与输入上下文进行名称改编以生成可靠的标识符。
	dname := c.mangle(name)
	e.called[dname] = true
	if out, ok := e.output[dname]; ok {
		// 已经转义过。
		return out, dname
	}
	t := e.template(name)
	if t == nil {
		// 两种情况：模板存在但为空，或从未被提及。在错误消息中区分这两种情况。
		if e.ns.set[name] != nil {
			return context{
				state: stateError,
				err:   errorf(ErrNoSuchTemplate, node, line, "%q is an incomplete or empty template", name),
			}, dname
		}
		return context{
			state: stateError,
			err:   errorf(ErrNoSuchTemplate, node, line, "no such template %q", name),
		}, dname
	}
	if dname != name {
		// 使用在早先对 escapeTemplate 的调用中用不同顶层模板派生的任何模板，
		// 或在必要时进行克隆。
		dt := e.template(dname)
		if dt == nil {
			dt = template.New(dname)
			dt.Tree = &parse.Tree{Name: dname, Root: t.Root.CopyList()}
			e.derived[dname] = dt
		}
		t = dt
	}
	return e.computeOutCtx(c, t), dname
}

// computeOutCtx 接受模板及其起始上下文，计算输出上下文并将任何推断存储在 e 中。
func (e *escaper) computeOutCtx(c context, t *template.Template) context {
	// 将上下文传播到整个主体。
	c1, ok := e.escapeTemplateBody(c, t)
	if !ok {
		// 通过假设 c1 为输出上下文来查找不动点。
		if c2, ok2 := e.escapeTemplateBody(c1, t); ok2 {
			c1, ok = c2, true
		}
		// 如果两种假设都不成立，则使用 c1 作为错误上下文。
	}
	if !ok && c1.state != stateError {
		return context{
			state: stateError,
			err:   errorf(ErrOutputContext, t.Tree.Root, 0, "cannot compute output context for template %s", t.Name()),
		}
	}
	return c1
}

// escapeTemplateBody 假设给定的输出上下文来转义给定的模板，
// 返回对输出上下文的最佳猜测以及假设是否正确。
func (e *escaper) escapeTemplateBody(c context, t *template.Template) (context, bool) {
	filter := func(e1 *escaper, c1 context) bool {
		if c1.state == stateError {
			// 不更新输入 escaper e。
			return false
		}
		if !e1.called[t.Name()] {
			// 如果 t 没有被递归调用，则 c1 是准确的输出上下文。
			return true
		}
		// 如果 c1 与我们假设的输出上下文匹配，则 c1 是准确的。
		return c.eq(c1)
	}
	// 我们需要假设一个输出上下文，以便递归模板调用走 escapeTree 的快速路径，
	// 而不是无限递归。简单地假设输入上下文与输出相同，超过 90% 的情况下都有效。
	e.output[t.Name()] = c
	return e.escapeListConditionally(c, t.Tree.Root, filter)
}

// delimEnds 将每个 delim 映射到终止它的字符串。
var delimEnds = [...]string{
	delimDoubleQuote: `"`,
	delimSingleQuote: "'",
	// 通过在各种浏览器中运行以下代码经验性确定。
	// var div = document.createElement("DIV");
	// for (var i = 0; i < 0x10000; ++i) {
	//   div.innerHTML = "<span title=x" + String.fromCharCode(i) + "-bar>";
	//   if (div.getElementsByTagName("SPAN")[0].title.indexOf("bar") < 0)
	//     document.write("<p>U+" + i.toString(16));
	// }
	delimSpaceOrTagEnd: " \t\n\f\r>",
}

var (
	// 根据 WHATWG HTML 规范第 4.12.1.3 节，处理 <!--、<script 和 </script
	// 开始标签在 JS 字面量（即字符串、正则表达式和注释）中出现时有极其复杂的规则。
	// 规范建议使用一个简单的解决方案，而不是实现晦涩的 ABNF，
	// 即简单地用 \x3C 转义开始括号。我们使用下面的正则表达式来实现，
	// 因为它使不区分大小写的查找替换更加简单。
	specialScriptTagRE          = regexp.MustCompile("(?i)<(script|/script|!--)")
	specialScriptTagReplacement = []byte("\\x3C$1")
)

func containsSpecialScriptTag(s []byte) bool {
	return specialScriptTagRE.Match(s)
}

func escapeSpecialScriptTags(s []byte) []byte {
	return specialScriptTagRE.ReplaceAll(s, specialScriptTagReplacement)
}

var doctypeBytes = []byte("<!DOCTYPE")

// escapeText 转义一个文本模板节点。
func (e *escaper) escapeText(c context, n *parse.TextNode) context {
	s, written, i, b := n.Text, 0, 0, new(bytes.Buffer)
	for i != len(s) {
		c1, nread := contextAfterText(c, s[i:])
		i1 := i + nread
		if c.state == stateText || c.state == stateRCDATA {
			end := i1
			if c1.state != c.state {
				for j := end - 1; j >= i; j-- {
					if s[j] == '<' {
						end = j
						break
					}
				}
			}
			for j := i; j < end; j++ {
				if s[j] == '<' && !bytes.HasPrefix(bytes.ToUpper(s[j:]), doctypeBytes) {
					b.Write(s[written:j])
					b.WriteString("&lt;")
					written = j + 1
				}
			}
		} else if isComment(c.state) && c.delim == delimNone {
			switch c.state {
			case stateJSBlockCmt:
				// https://es5.github.io/#x7.4:
				// "注释的行为类似于空白，会被丢弃，但如果 MultiLineComment
				// 包含行终止符字符，则整个注释被视为
				// LineTerminator，用于语法解析。"
				if bytes.ContainsAny(s[written:i1], "\n\r\u2028\u2029") {
					b.WriteByte('\n')
				} else {
					b.WriteByte(' ')
				}
			case stateCSSBlockCmt:
				b.WriteByte(' ')
			}
			written = i1
		}
		if c.state != c1.state && isComment(c1.state) && c1.delim == delimNone {
			// 保留 written 和注释开始之间的部分。
			cs := i1 - 2
			if c1.state == stateHTMLCmt || c1.state == stateJSHTMLOpenCmt {
				// "<!--" 而不是 "/*" 或 "//"
				cs -= 2
			} else if c1.state == stateJSHTMLCloseCmt {
				// "-->" 而不是 "/*" 或 "//"
				cs -= 1
			}
			b.Write(s[written:cs])
			written = i1
		}
		if isInScriptLiteral(c.state) && containsSpecialScriptTag(s[i:i1]) {
			b.Write(s[written:i])
			b.Write(escapeSpecialScriptTags(s[i:i1]))
			written = i1
		}
		if i == i1 && c.state == c1.state {
			panic(fmt.Sprintf("infinite loop from %v to %v on %q..%q", c, c1, s[:i], s[i:]))
		}
		c, i = c1, i1
	}

	if written != 0 && c.state != stateError {
		if !isComment(c.state) || c.delim != delimNone {
			b.Write(n.Text[written:])
		}
		e.editTextNode(n, b.Bytes())
	}
	return c
}

// contextAfterText 从上下文 c 开始，从 s 的前面消耗一些标记，
// 然后返回这些标记之后的上下文和未处理的后缀。
func contextAfterText(c context, s []byte) (context, int) {
	if c.delim == delimNone {
		c1, i := tSpecialTagEnd(c, s)
		if i == 0 {
			// 已经看到特殊结束标签（`</script>`），且其前面的所有内容都已被消耗。
			return c1, 0
		}
		// 考虑到任何结束标签为止的所有内容。
		return transitionFunc[c.state](c, s[:i])
	}

	// 我们位于属性值的开头。

	i := bytes.IndexAny(s, delimEnds[c.delim])
	if i == -1 {
		i = len(s)
	}
	if c.delim == delimSpaceOrTagEnd {
		// https://www.w3.org/TR/html5/syntax.html#attribute-value-(unquoted)-state
		// 下面列出的字符被视为错误字符。
		// 报错是因为 HTML 解析器对以下情况可能有不同理解：
		// "<a id= onclick=f("     是否在 id 或 onclick 的值内部结束，
		// "<a class=`foo "        是否在值内部结束，
		// "<a style=font:'Arial'" 需要开引号修复。
		// IE 将 '`' 视为引号字符。
		if j := bytes.IndexAny(s[:i], "\"'<=`"); j >= 0 {
			return context{
				state: stateError,
				err:   errorf(ErrBadHTML, nil, 0, "%q in unquoted attr: %q", s[j:j+1], s[:i]),
			}, len(s)
		}
	}
	if i == len(s) {
		// 保持在属性内部。
		// 解码值以便非 HTML 规则可以轻松处理
		//     <button onclick="alert(&quot;Hi!&quot;)">
		// 而无需对标记边界进行实体解码。
		for u := []byte(html.UnescapeString(string(s))); len(u) != 0; {
			c1, i1 := transitionFunc[c.state](c, u)
			c, u = c1, u[i1:]
		}
		return c, len(s)
	}

	element := c.element

	// 如果这是 "script" 标签内的非 JS "type" 属性，则不将内容视为 JS。
	if c.state == stateAttr && c.element == elementScript && c.attr == attrScriptType && !isJSType(string(s[:i])) {
		element = elementNone
	}

	if c.delim != delimSpaceOrTagEnd {
		// 消耗任何引号。
		i++
	}
	// 退出属性时，我们丢弃除 state 和 element 之外的所有状态信息。
	return context{state: stateTag, element: element}, i
}

// editActionNode 记录对 action 管道的更改以供稍后提交。
func (e *escaper) editActionNode(n *parse.ActionNode, cmds []string) {
	if _, ok := e.actionNodeEdits[n]; ok {
		panic(fmt.Sprintf("node %s shared between templates", n))
	}
	e.actionNodeEdits[n] = cmds
}

// editTemplateNode 记录对 {{template}} 被调用者的更改以供稍后提交。
func (e *escaper) editTemplateNode(n *parse.TemplateNode, callee string) {
	if _, ok := e.templateNodeEdits[n]; ok {
		panic(fmt.Sprintf("node %s shared between templates", n))
	}
	e.templateNodeEdits[n] = callee
}

// editTextNode 记录对文本节点的更改以供稍后提交。
func (e *escaper) editTextNode(n *parse.TextNode, text []byte) {
	if _, ok := e.textNodeEdits[n]; ok {
		panic(fmt.Sprintf("node %s shared between templates", n))
	}
	e.textNodeEdits[n] = text
}

// commit 应用对 action 和模板调用所需的更改以进行上下文自动转义，
// 并将任何派生模板添加到集合中。
func (e *escaper) commit() {
	for name := range e.output {
		e.template(name).Funcs(funcMap)
	}
	// 与此 escaper 关联的名称空间中的任何模板都可用于
	// 将派生模板添加到底层 text/template 名称空间。
	tmpl := e.arbitraryTemplate()
	for _, t := range e.derived {
		if _, err := tmpl.text.AddParseTree(t.Name(), t.Tree); err != nil {
			panic("error adding derived template")
		}
	}
	for n, s := range e.actionNodeEdits {
		ensurePipelineContains(n.Pipe, s)
	}
	for n, name := range e.templateNodeEdits {
		n.Name = name
	}
	for n, s := range e.textNodeEdits {
		n.Text = s
	}
	// 重置特定于此次提交的状态，以便在后续调用 commit 时不会将相同的更改重新应用到模板。
	e.called = make(map[string]bool)
	e.actionNodeEdits = make(map[*parse.ActionNode][]string)
	e.templateNodeEdits = make(map[*parse.TemplateNode]string)
	e.textNodeEdits = make(map[*parse.TextNode][]byte)
}

// template 根据改编后的模板名称返回命名模板。
func (e *escaper) template(name string) *template.Template {
	// 与此 escaper 关联的名称空间中的任何模板都可用于
	// 在底层 text/template 名称空间中查找模板。
	t := e.arbitraryTemplate().text.Lookup(name)
	if t == nil {
		t = e.derived[name]
	}
	return t
}

// arbitraryTemplate 从与 e 关联的名称空间中返回一个任意模板，如果没有找到模板则 panic。
func (e *escaper) arbitraryTemplate() *Template {
	for _, t := range e.ns.set {
		return t
	}
	panic("no templates in name space")
}

// 转发函数，使客户端只需导入此包即可使用 text/template 的通用转义函数。

// HTMLEscape 将纯文本数据 b 的转义 HTML 等价物写入 w。
func HTMLEscape(w io.Writer, b []byte) {
	template.HTMLEscape(w, b)
}

// HTMLEscapeString 返回纯文本数据 s 的转义 HTML 等价物。
func HTMLEscapeString(s string) string {
	return template.HTMLEscapeString(s)
}

// HTMLEscaper 返回其参数的文本表示的转义 HTML 等价物。
func HTMLEscaper(args ...any) string {
	return template.HTMLEscaper(args...)
}

// JSEscape 将纯文本数据 b 的转义 JavaScript 等价物写入 w。
func JSEscape(w io.Writer, b []byte) {
	template.JSEscape(w, b)
}

// JSEscapeString 返回纯文本数据 s 的转义 JavaScript 等价物。
func JSEscapeString(s string) string {
	return template.JSEscapeString(s)
}

// JSEscaper 返回其参数的文本表示的转义 JavaScript 等价物。
func JSEscaper(args ...any) string {
	return template.JSEscaper(args...)
}

// URLQueryEscaper 返回其参数的文本表示的转义值，格式适合嵌入 URL 查询。
func URLQueryEscaper(args ...any) string {
	return template.URLQueryEscaper(args...)
}
