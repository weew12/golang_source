// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package parse 为 text/template 和 html/template 定义的模板构建解析树。
// 客户端应该使用这些包来构造模板，而不是这个包，
// 这个包提供了共享的内部数据结构，不打算供一般使用。
package parse

import (
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// Tree 是单个解析模板的表示。
type Tree struct {
	Name      string    // 树表示的模板的名称。
	ParseName string    // 解析期间顶级模板的名称，用于错误消息。
	Root      *ListNode // 树的顶层根节点。
	Mode      Mode      // 解析模式。
	text      string    // 用于创建模板（或其父模板）的文本
	// 仅用于解析；解析后清除。
	funcs      []map[string]any
	lex        *lexer
	token      [3]item // 解析器的三个词法项前瞻。
	peekCount  int
	vars       []string // 当前定义的变量。
	treeSet    map[string]*Tree
	actionLine int // 开始动作的左分隔符的行
	rangeDepth int
	stackDepth int // 嵌套括号表达式的深度
}

// Mode 值是一组标志（或 0）。模式控制解析器行为。
type Mode uint

const (
	ParseComments Mode = 1 << iota // 解析注释并将其添加到 AST
	SkipFuncCheck                  // 不检查函数是否已定义
)

// maxStackDepth 是允许的嵌套括号表达式的最大深度。
var maxStackDepth = 10000

// init 为 WebAssembly 减少 maxStackDepth，因为其栈较小。
func init() {
	if runtime.GOARCH == "wasm" {
		maxStackDepth = 1000
	}
}

// Copy 返回 [Tree] 的副本。丢弃任何解析状态。
func (t *Tree) Copy() *Tree {
	if t == nil {
		return nil
	}
	return &Tree{
		Name:      t.Name,
		ParseName: t.ParseName,
		Root:      t.Root.CopyList(),
		text:      t.text,
	}
}

// Parse 返回从模板名称到 [Tree] 的映射，通过解析参数字符串中描述的模板创建。
// 顶级模板将被给予指定的名称。如果遇到错误，解析停止并返回空 map 和错误。
func Parse(name, text, leftDelim, rightDelim string, funcs ...map[string]any) (map[string]*Tree, error) {
	treeSet := make(map[string]*Tree)
	t := New(name)
	t.text = text
	_, err := t.Parse(text, leftDelim, rightDelim, treeSet, funcs...)
	return treeSet, err
}

// next 返回下一个词法项。
func (t *Tree) next() item {
	if t.peekCount > 0 {
		t.peekCount--
	} else {
		t.token[0] = t.lex.nextItem()
	}
	return t.token[t.peekCount]
}

// backup 将输入流后退一个词法项。
func (t *Tree) backup() {
	t.peekCount++
}

// backup2 将输入流后退两个词法项。
// 第零个词法项已经在那里了。
func (t *Tree) backup2(t1 item) {
	t.token[1] = t1
	t.peekCount = 2
}

// backup3 将输入流后退三个词法项
// 第零个词法项已经在那里了。
func (t *Tree) backup3(t2, t1 item) { // 反向顺序：我们正在推回。
	t.token[1] = t1
	t.token[2] = t2
	t.peekCount = 3
}

// peek 返回但不消费下一个词法项。
func (t *Tree) peek() item {
	if t.peekCount > 0 {
		return t.token[t.peekCount-1]
	}
	t.peekCount = 1
	t.token[0] = t.lex.nextItem()
	return t.token[0]
}

// nextNonSpace 返回下一个非空格词法项。
func (t *Tree) nextNonSpace() (token item) {
	for {
		token = t.next()
		if token.typ != itemSpace {
			break
		}
	}
	return token
}

// peekNonSpace 返回但不消费下一个非空格词法项。
func (t *Tree) peekNonSpace() item {
	token := t.nextNonSpace()
	t.backup()
	return token
}

// 解析。

// New 分配具有给定名称的新解析树。
func New(name string, funcs ...map[string]any) *Tree {
	return &Tree{
		Name:  name,
		funcs: funcs,
	}
}

// ErrorContext 返回节点在输入文本中位置的文本表示。
// 仅当节点没有指向树内部的指针时，才使用接收者，这在旧代码中可能发生。
func (t *Tree) ErrorContext(n Node) (location, context string) {
	pos := int(n.Position())
	tree := n.tree()
	if tree == nil {
		tree = t
	}
	text := tree.text[:pos]
	byteNum := strings.LastIndex(text, "\n")
	if byteNum == -1 {
		byteNum = pos // 在第一行上。
	} else {
		byteNum++ // 在换行符之后。
		byteNum = pos - byteNum
	}
	lineNum := 1 + strings.Count(text, "\n")
	context = n.String()
	return fmt.Sprintf("%s:%d:%d", tree.ParseName, lineNum, byteNum), context
}

// errorf 格式化错误并终止处理。
func (t *Tree) errorf(format string, args ...any) {
	t.Root = nil
	format = fmt.Sprintf("template: %s:%d: %s", t.ParseName, t.token[0].line, format)
	panic(fmt.Errorf(format, args...))
}

// error 终止处理。
func (t *Tree) error(err error) {
	t.errorf("%s", err)
}

// expect 消费下一个词法项并保证它具有所需的类型。
func (t *Tree) expect(expected itemType, context string) item {
	token := t.nextNonSpace()
	if token.typ != expected {
		t.unexpected(token, context)
	}
	return token
}

// expectOneOf 消费下一个词法项并保证它具有所需类型之一。
func (t *Tree) expectOneOf(expected1, expected2 itemType, context string) item {
	token := t.nextNonSpace()
	if token.typ != expected1 && token.typ != expected2 {
		t.unexpected(token, context)
	}
	return token
}

// unexpected 抱怨词法项并终止处理。
func (t *Tree) unexpected(token item, context string) {
	if token.typ == itemError {
		extra := ""
		if t.actionLine != 0 && t.actionLine != token.line {
			extra = fmt.Sprintf(" in action started at %s:%d", t.ParseName, t.actionLine)
			if strings.HasSuffix(token.val, " action") {
				extra = extra[len(" in action"):] // avoid "action in action"
			}
		}
		t.errorf("%s%s", token, extra)
	}
	t.errorf("unexpected %s in %s", token, context)
}

// recover 是将 panic 转换为从 Parse 顶层返回的处理程序。
func (t *Tree) recover(errp *error) {
	e := recover()
	if e != nil {
		if _, ok := e.(runtime.Error); ok {
			panic(e)
		}
		if t != nil {
			t.stopParse()
		}
		*errp = e.(error)
	}
}

// startParse 初始化解析器，使用词法分析器。
func (t *Tree) startParse(funcs []map[string]any, lex *lexer, treeSet map[string]*Tree) {
	t.Root = nil
	t.lex = lex
	t.vars = []string{"$"}
	t.funcs = funcs
	t.treeSet = treeSet
	t.stackDepth = 0
	lex.options = lexOptions{
		emitComment: t.Mode&ParseComments != 0,
		breakOK:     !t.hasFunction("break"),
		continueOK:  !t.hasFunction("continue"),
	}
}

// stopParse 终止解析。
func (t *Tree) stopParse() {
	t.lex = nil
	t.vars = nil
	t.funcs = nil
	t.treeSet = nil
}

// Parse 解析模板定义字符串以构造用于执行的模板表示。
// 如果任一动作分隔符字符串为空，则使用默认值（"{{" 或 "}}"）。
// 嵌入式模板定义被添加到 treeSet map。
func (t *Tree) Parse(text, leftDelim, rightDelim string, treeSet map[string]*Tree, funcs ...map[string]any) (tree *Tree, err error) {
	defer t.recover(&err)
	t.ParseName = t.Name
	lexer := lex(t.Name, text, leftDelim, rightDelim)
	t.startParse(funcs, lexer, treeSet)
	t.text = text
	t.parse()
	t.add()
	t.stopParse()
	return t, nil
}

// add 将树添加到 t.treeSet。
func (t *Tree) add() {
	tree := t.treeSet[t.Name]
	if tree == nil || IsEmptyTree(tree.Root) {
		t.treeSet[t.Name] = t
		return
	}
	if !IsEmptyTree(t.Root) {
		t.errorf("template: multiple definition of template %q", t.Name)
	}
}

// IsEmptyTree 报告此树（节点）是否除了空格或注释之外为空。
func IsEmptyTree(n Node) bool {
	switch n := n.(type) {
	case nil:
		return true
	case *ActionNode:
	case *CommentNode:
		return true
	case *IfNode:
	case *ListNode:
		for _, node := range n.Nodes {
			if !IsEmptyTree(node) {
				return false
			}
		}
		return true
	case *RangeNode:
	case *TemplateNode:
	case *TextNode:
		return len(bytes.TrimSpace(n.Text)) == 0
	case *WithNode:
	default:
		panic("unknown node: " + n.String())
	}
	return false
}

// parse 是模板的顶层解析器，本质上与 itemList 相同，只是它也解析 {{define}} 动作。
// 它运行到 EOF。
func (t *Tree) parse() {
	t.Root = t.newList(t.peek().pos)
	for t.peek().typ != itemEOF {
		if t.peek().typ == itemLeftDelim {
			delim := t.next()
			if t.nextNonSpace().typ == itemDefine {
				newT := New("definition") // name will be updated once we know it.
				newT.text = t.text
				newT.Mode = t.Mode
				newT.ParseName = t.ParseName
				newT.startParse(t.funcs, t.lex, t.treeSet)
				newT.parseDefinition()
				continue
			}
			t.backup2(delim)
		}
		switch n := t.textOrAction(); n.Type() {
		case nodeEnd, nodeElse:
			t.errorf("unexpected %s", n)
		default:
			t.Root.append(n)
		}
	}
}

// parseDefinition 解析 {{define}} ... {{end}} 模板定义并将其安装到 t.treeSet 中。
// "define" 关键字已经被扫描。
func (t *Tree) parseDefinition() {
	const context = "define clause"
	name := t.expectOneOf(itemString, itemRawString, context)
	var err error
	t.Name, err = strconv.Unquote(name.val)
	if err != nil {
		t.error(err)
	}
	t.expect(itemRightDelim, context)
	var end Node
	t.Root, end = t.itemList()
	if end.Type() != nodeEnd {
		t.errorf("unexpected %s in %s", end, context)
	}
	t.add()
	t.stopParse()
}

// itemList:
//
//	textOrAction*
//
// 在 {{end}} 或 {{else}} 处终止，分别返回。
func (t *Tree) itemList() (list *ListNode, next Node) {
	list = t.newList(t.peekNonSpace().pos)
	for t.peekNonSpace().typ != itemEOF {
		n := t.textOrAction()
		switch n.Type() {
		case nodeEnd, nodeElse:
			return list, n
		}
		list.append(n)
	}
	t.errorf("unexpected EOF")
	return
}

// textOrAction:
//
//	text | comment | action
func (t *Tree) textOrAction() Node {
	switch token := t.nextNonSpace(); token.typ {
	case itemText:
		return t.newText(token.pos, token.val)
	case itemLeftDelim:
		t.actionLine = token.line
		defer t.clearActionLine()
		return t.action()
	case itemComment:
		return t.newComment(token.pos, token.val)
	default:
		t.unexpected(token, "input")
	}
	return nil
}

func (t *Tree) clearActionLine() {
	t.actionLine = 0
}

// Action:
//
//	control
//	command ("|" command)*
//
// 左分隔符已过。现在获取动作。
// 第一个词可能是关键字，如 range。
func (t *Tree) action() (n Node) {
	switch token := t.nextNonSpace(); token.typ {
	case itemBlock:
		return t.blockControl()
	case itemBreak:
		return t.breakControl(token.pos, token.line)
	case itemContinue:
		return t.continueControl(token.pos, token.line)
	case itemElse:
		return t.elseControl()
	case itemEnd:
		return t.endControl()
	case itemIf:
		return t.ifControl()
	case itemRange:
		return t.rangeControl()
	case itemTemplate:
		return t.templateControl()
	case itemWith:
		return t.withControl()
	}
	t.backup()
	token := t.peek()
	// 不要弹出变量；它们持续到 "end"。
	return t.newAction(token.pos, token.line, t.pipeline("command", itemRightDelim))
}

// Break:
//
//	{{break}}
//
// Break 关键字已过。
func (t *Tree) breakControl(pos Pos, line int) Node {
	if token := t.nextNonSpace(); token.typ != itemRightDelim {
		t.unexpected(token, "{{break}}")
	}
	if t.rangeDepth == 0 {
		t.errorf("{{break}} outside {{range}}")
	}
	return t.newBreak(pos, line)
}

// Continue:
//
//	{{continue}}
//
// Continue 关键字已过。
func (t *Tree) continueControl(pos Pos, line int) Node {
	if token := t.nextNonSpace(); token.typ != itemRightDelim {
		t.unexpected(token, "{{continue}}")
	}
	if t.rangeDepth == 0 {
		t.errorf("{{continue}} outside {{range}}")
	}
	return t.newContinue(pos, line)
}

// Pipeline:
//
//	declarations? command ('|' command)*
func (t *Tree) pipeline(context string, end itemType) (pipe *PipeNode) {
	token := t.peekNonSpace()
	pipe = t.newPipeline(token.pos, token.line, nil)
	// 有声明或赋值吗？
decls:
	if v := t.peekNonSpace(); v.typ == itemVariable {
		t.next()
		// 由于空格是一个词法项，在最坏情况下我们需要 3 个词法项前瞻：
		// 在 "$x foo" 中，我们需要读取 "foo"（而不是 ":="）来知道 $x 是一个
		// 参数变量而不是声明。所以记住与变量相邻的词法项，以便在需要时推回。
		tokenAfterVariable := t.peek()
		next := t.peekNonSpace()
		switch {
		case next.typ == itemAssign, next.typ == itemDeclare:
			pipe.IsAssign = next.typ == itemAssign
			t.nextNonSpace()
			pipe.Decl = append(pipe.Decl, t.newVariable(v.pos, v.val))
			t.vars = append(t.vars, v.val)
		case next.typ == itemChar && next.val == ",":
			t.nextNonSpace()
			pipe.Decl = append(pipe.Decl, t.newVariable(v.pos, v.val))
			t.vars = append(t.vars, v.val)
			if context == "range" && len(pipe.Decl) < 2 {
				switch t.peekNonSpace().typ {
				case itemVariable, itemRightDelim, itemRightParen:
					// range 管道中的第二个初始化变量
					goto decls
				default:
					t.errorf("range can only initialize variables")
				}
			}
			t.errorf("too many declarations in %s", context)
		case tokenAfterVariable.typ == itemSpace:
			t.backup3(v, tokenAfterVariable)
		default:
			t.backup2(v)
		}
	}
	for {
		switch token := t.nextNonSpace(); token.typ {
		case end:
			// 此时，管道已完成
			t.checkPipeline(pipe, context)
			return
		case itemBool, itemCharConstant, itemComplex, itemDot, itemField, itemIdentifier,
			itemNumber, itemNil, itemRawString, itemString, itemVariable, itemLeftParen:
			t.backup()
			pipe.append(t.command())
		default:
			t.unexpected(token, context)
		}
	}
}

func (t *Tree) checkPipeline(pipe *PipeNode, context string) {
	// 拒绝空管道
	if len(pipe.Cmds) == 0 {
		t.errorf("missing value for %s", context)
	}
	// 只有管道的第一个命令可以以非可执行操作数开头
	for i, c := range pipe.Cmds[1:] {
		switch c.Args[0].Type() {
		case NodeBool, NodeDot, NodeNil, NodeNumber, NodeString:
			// 对于 A|B|C，管道阶段 2 是 B
			t.errorf("non executable command in pipeline stage %d", i+2)
		}
	}
}

func (t *Tree) parseControl(context string) (pos Pos, line int, pipe *PipeNode, list, elseList *ListNode) {
	defer t.popVars(len(t.vars))
	pipe = t.pipeline(context, itemRightDelim)
	if context == "range" {
		t.rangeDepth++
	}
	var next Node
	list, next = t.itemList()
	if context == "range" {
		t.rangeDepth--
	}
	switch next.Type() {
	case nodeEnd: //done
	case nodeElse:
		// "else if" 和 "else with" 的特殊情况。
		// 如果 "else" 后面直接跟着 "if" 或 "with"，
		// elseControl 会留下 "if" 或 "with" 词法项待处理。将
		//	{{if a}}_{{else if b}}_{{end}}
		//  {{with a}}_{{else with b}}_{{end}}
		// 视为
		//	{{if a}}_{{else}}{{if b}}_{{end}}{{end}}
		//  {{with a}}_{{else}}{{with b}}_{{end}}{{end}}.
		// 为此，像往常一样解析 "if" 或 "with" 并在 {{end}} 处停止；
		// 假定后续有 {{end}}。此技术即使对于长的 if-else-if 链也有效。
		if context == "if" && t.peek().typ == itemIf {
			t.next() // 消费 "if" 词法项。
			elseList = t.newList(next.Position())
			elseList.append(t.ifControl())
		} else if context == "with" && t.peek().typ == itemWith {
			t.next()
			elseList = t.newList(next.Position())
			elseList.append(t.withControl())
		} else {
			elseList, next = t.itemList()
			if next.Type() != nodeEnd {
				t.errorf("expected end; found %s", next)
			}
		}
	}
	return pipe.Position(), pipe.Line, pipe, list, elseList
}

// If:
//
//	{{if pipeline}} itemList {{end}}
//	{{if pipeline}} itemList {{else}} itemList {{end}}
//
// If 关键字已过。
func (t *Tree) ifControl() Node {
	return t.newIf(t.parseControl("if"))
}

// Range:
//
//	{{range pipeline}} itemList {{end}}
//	{{range pipeline}} itemList {{else}} itemList {{end}}
//
// Range 关键字已过。
func (t *Tree) rangeControl() Node {
	r := t.newRange(t.parseControl("range"))
	return r
}

// With:
//
//	{{with pipeline}} itemList {{end}}
//	{{with pipeline}} itemList {{else}} itemList {{end}}
//
// If 关键字已过。
func (t *Tree) withControl() Node {
	return t.newWith(t.parseControl("with"))
}

// End:
//
//	{{end}}
//
// End 关键字已过。
func (t *Tree) endControl() Node {
	return t.newEnd(t.expect(itemRightDelim, "end").pos)
}

// Else:
//
//	{{else}}
//
// Else 关键字已过。
func (t *Tree) elseControl() Node {
	peek := t.peekNonSpace()
	// "{{else if ... " 和 "{{else with ..." 将被视为
	// "{{else}}{{if ..." 和 "{{else}}{{with ..."。
	// 所以在这里返回 else 节点。
	if peek.typ == itemIf || peek.typ == itemWith {
		return t.newElse(peek.pos, peek.line)
	}
	token := t.expect(itemRightDelim, "else")
	return t.newElse(token.pos, token.line)
}

// Block:
//
//	{{block stringValue pipeline}}
//
// Block 关键字已过。
// 名称必须是可求值为字符串的东西。
// 管道是强制的。
func (t *Tree) blockControl() Node {
	const context = "block clause"

	token := t.nextNonSpace()
	name := t.parseTemplateName(token, context)
	pipe := t.pipeline(context, itemRightDelim)

	block := New(name) // name will be updated once we know it.
	block.text = t.text
	block.Mode = t.Mode
	block.ParseName = t.ParseName
	block.startParse(t.funcs, t.lex, t.treeSet)
	var end Node
	block.Root, end = block.itemList()
	if end.Type() != nodeEnd {
		t.errorf("unexpected %s in %s", end, context)
	}
	block.add()
	block.stopParse()

	return t.newTemplate(token.pos, token.line, name, pipe)
}

// Template:
//
//	{{template stringValue pipeline}}
//
// Template 关键字已过。名称必须是可求值为字符串的东西。
func (t *Tree) templateControl() Node {
	const context = "template clause"
	token := t.nextNonSpace()
	name := t.parseTemplateName(token, context)
	var pipe *PipeNode
	if t.nextNonSpace().typ != itemRightDelim {
		t.backup()
		// 不要弹出变量；它们持续到 "end"。
		pipe = t.pipeline(context, itemRightDelim)
	}
	return t.newTemplate(token.pos, token.line, name, pipe)
}

func (t *Tree) parseTemplateName(token item, context string) (name string) {
	switch token.typ {
	case itemString, itemRawString:
		s, err := strconv.Unquote(token.val)
		if err != nil {
			t.error(err)
		}
		name = s
	default:
		t.unexpected(token, context)
	}
	return
}

// command:
//
//	operand (space operand)*
//
// 以空格分隔的参数，直到管道字符或右分隔符。
// 我们消费管道字符但留下右分隔符来终止动作。
func (t *Tree) command() *CommandNode {
	cmd := t.newCommand(t.peekNonSpace().pos)
	for {
		t.peekNonSpace() // skip leading spaces.
		operand := t.operand()
		if operand != nil {
			cmd.append(operand)
		}
		switch token := t.next(); token.typ {
		case itemSpace:
			continue
		case itemRightDelim, itemRightParen:
			t.backup()
		case itemPipe:
			// 这里什么都没有；下面 break 循环
		default:
			t.unexpected(token, "operand")
		}
		break
	}
	if len(cmd.Args) == 0 {
		t.errorf("empty command")
	}
	return cmd
}

// operand:
//
//	term .Field*
//
// 操作数是命令的空格分隔组件，可能是字段访问的术语。
// 返回 nil 意味着下一个项不是操作数。
func (t *Tree) operand() Node {
	node := t.term()
	if node == nil {
		return nil
	}
	if t.peek().typ == itemField {
		chain := t.newChain(t.peek().pos, node)
		for t.peek().typ == itemField {
			chain.Add(t.next().val)
		}
		// 与原始 API 的兼容性：如果术语类型为 NodeField
		// 或 NodeVariable，只需将更多字段放在原始术语上。
		// 否则，保留 Chain 节点。
		// 这里检测到涉及字面值的明显解析错误。
		// 更复杂的错误情况必须在执行时处理。
		switch node.Type() {
		case NodeField:
			node = t.newField(chain.Position(), chain.String())
		case NodeVariable:
			node = t.newVariable(chain.Position(), chain.String())
		case NodeBool, NodeString, NodeNumber, NodeNil, NodeDot:
			t.errorf("unexpected . after term %q", node.String())
		default:
			node = chain
		}
	}
	return node
}

// term:
//
//	literal (number, string, nil, boolean)
//	function (identifier)
//	.
//	.Field
//	$
//	'(' pipeline ')'
//
// 术语是一个简单的"表达式"。
// 返回 nil 意味着下一个项不是术语。
func (t *Tree) term() Node {
	switch token := t.nextNonSpace(); token.typ {
	case itemIdentifier:
		checkFunc := t.Mode&SkipFuncCheck == 0
		if checkFunc && !t.hasFunction(token.val) {
			t.errorf("function %q not defined", token.val)
		}
		return NewIdentifier(token.val).SetTree(t).SetPos(token.pos)
	case itemDot:
		return t.newDot(token.pos)
	case itemNil:
		return t.newNil(token.pos)
	case itemVariable:
		return t.useVar(token.pos, token.val)
	case itemField:
		return t.newField(token.pos, token.val)
	case itemBool:
		return t.newBool(token.pos, token.val == "true")
	case itemCharConstant, itemComplex, itemNumber:
		number, err := t.newNumber(token.pos, token.val, token.typ)
		if err != nil {
			t.error(err)
		}
		return number
	case itemLeftParen:
		if t.stackDepth >= maxStackDepth {
			t.errorf("max expression depth exceeded")
		}
		t.stackDepth++
		defer func() { t.stackDepth-- }()
		return t.pipeline("parenthesized pipeline", itemRightParen)
	case itemString, itemRawString:
		s, err := strconv.Unquote(token.val)
		if err != nil {
			t.error(err)
		}
		return t.newString(token.pos, token.val, s)
	}
	t.backup()
	return nil
}

// hasFunction 报告函数名是否存在于 Tree 的 map 中。
func (t *Tree) hasFunction(name string) bool {
	for _, funcMap := range t.funcs {
		if funcMap == nil {
			continue
		}
		if funcMap[name] != nil {
			return true
		}
	}
	return false
}

// popVars 将变量列表修剪到指定的长度
func (t *Tree) popVars(n int) {
	t.vars = t.vars[:n]
}

// useVar 返回变量引用的节点。如果变量未定义则报错。
func (t *Tree) useVar(pos Pos, name string) Node {
	v := t.newVariable(pos, name)
	for _, varName := range t.vars {
		if varName == v.Ident[0] {
			return v
		}
	}
	t.errorf("undefined variable %q", v.Ident[0])
	return nil
}
