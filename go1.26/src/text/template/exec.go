// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"errors"
	"fmt"
	"internal/fmtsort"
	"io"
	"reflect"
	"runtime"
	"strings"
	"text/template/parse"
)

// maxExecDepth 指定模板中模板的最大执行栈深度。此限制仅在意外递归模板调用时实际达到。
// 这允许我们返回错误而不是触发栈溢出。
var maxExecDepth = initMaxExecDepth()

func initMaxExecDepth() int {
	if runtime.GOARCH == "wasm" {
		return 1000
	}
	return 100000
}

// state 表示执行的状态。它不是模板的一部分，
// 因此同一模板的多次执行可以并行进行。
type state struct {
	tmpl  *Template
	wr    io.Writer
	node  parse.Node // 当前节点，用于错误报告
	vars  []variable // 变量值的下推栈。
	depth int        // 执行中的模板栈的高度。
}

// variable 保存变量（如 $、$x 等）的动态值。
type variable struct {
	name  string
	value reflect.Value
}

// push 将新变量压入栈中。
func (s *state) push(name string, value reflect.Value) {
	s.vars = append(s.vars, variable{name, value})
}

// mark 返回变量栈的长度。
func (s *state) mark() int {
	return len(s.vars)
}

// pop 将变量栈弹出到标记处。
func (s *state) pop(mark int) {
	s.vars = s.vars[0:mark]
}

// setVar 用给定的名称覆盖最后声明的变量。
// 用于变量赋值。
func (s *state) setVar(name string, value reflect.Value) {
	for i := s.mark() - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			s.vars[i].value = value
			return
		}
	}
	s.errorf("undefined variable: %s", name)
}

// setTopVar 覆盖栈上的第 n 个变量。用于 range 迭代。
func (s *state) setTopVar(n int, value reflect.Value) {
	s.vars[len(s.vars)-n].value = value
}

// varValue 返回命名变量的值。
func (s *state) varValue(name string) reflect.Value {
	for i := s.mark() - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			return s.vars[i].value
		}
	}
	s.errorf("undefined variable: %s", name)
	return zero
}

var zero reflect.Value

type missingValType struct{}

var missingVal = reflect.ValueOf(missingValType{})

var missingValReflectType = reflect.TypeFor[missingValType]()

func isMissing(v reflect.Value) bool {
	return v.IsValid() && v.Type() == missingValReflectType
}

// at 标记状态在节点 n 上，用于错误报告。
func (s *state) at(node parse.Node) {
	s.node = node
}

// doublePercent 如果需要，将字符串中的 % 替换为 %%，
// 以便可以安全地用于 Printf 格式字符串。
func doublePercent(str string) string {
	return strings.ReplaceAll(str, "%", "%%")
}

// TODO: 如果 ExecError 更细分会更好，但 ErrorContext 嵌入模板名称的方式
// 使处理过于笨拙。

// ExecError 是 Execute 在求值模板时出错时返回的自定义错误类型。
//（如果发生写入错误，则返回实际错误；它不会是 ExecError 类型。）
type ExecError struct {
	Name string // 模板名称。
	Err  error  // 预格式化的错误。
}

func (e ExecError) Error() string {
	return e.Err.Error()
}

func (e ExecError) Unwrap() error {
	return e.Err
}

// errorf 记录 ExecError 并终止处理。
func (s *state) errorf(format string, args ...any) {
	name := doublePercent(s.tmpl.Name())
	if s.node == nil {
		format = fmt.Sprintf("template: %s: %s", name, format)
	} else {
		location, context := s.tmpl.ErrorContext(s.node)
		format = fmt.Sprintf("template: %s: executing %q at <%s>: %s", location, name, doublePercent(context), format)
	}
	panic(ExecError{
		Name: s.tmpl.Name(),
		Err:  fmt.Errorf(format, args...),
	})
}

// writeError 是 Execute 在写入输出时出错时内部使用的包装类型。
// 我们在 errRecover 中剥离包装。注意这不是 error 的实现，
// 因此它不能作为错误值从包中逃逸。
type writeError struct {
	Err error // 原始错误。
}

func (s *state) writeError(err error) {
	panic(writeError{
		Err: err,
	})
}

// errRecover 是将 panic 转换为从 Parse 顶层返回的处理程序。
func errRecover(errp *error) {
	e := recover()
	if e != nil {
		switch err := e.(type) {
		case runtime.Error:
			panic(e)
		case writeError:
			*errp = err.Err // 剥离包装。
		case ExecError:
			*errp = err // 保留包装。
		default:
			panic(e)
		}
	}
}

// ExecuteTemplate 将与 t 关联的具有给定名称的模板应用于指定的数据对象，
// 并将输出写入 wr。如果执行模板或写入其输出时发生错误，
// 执行停止，但部分结果可能已写入输出 writer。
// 模板可以安全地并行执行，尽管如果并行执行共享一个 Writer，输出可能会交错。
func (t *Template) ExecuteTemplate(wr io.Writer, name string, data any) error {
	tmpl := t.Lookup(name)
	if tmpl == nil {
		return fmt.Errorf("template: no template %q associated with template %q", name, t.name)
	}
	return tmpl.Execute(wr, data)
}

// Execute 将解析后的模板应用于指定的数据对象，并将输出写入 wr。
// 如果执行模板或写入其输出时发生错误，执行停止，但部分结果可能已写入输出 writer。
// 模板可以安全地并行执行，尽管如果并行执行共享一个 Writer，输出可能会交错。
//
// 如果 data 是 [reflect.Value]，模板应用于 reflect.Value 持有的具体值，
// 如同 [fmt.Print]。
func (t *Template) Execute(wr io.Writer, data any) error {
	return t.execute(wr, data)
}

func (t *Template) execute(wr io.Writer, data any) (err error) {
	defer errRecover(&err)
	value, ok := data.(reflect.Value)
	if !ok {
		value = reflect.ValueOf(data)
	}
	state := &state{
		tmpl: t,
		wr:   wr,
		vars: []variable{{"$", value}},
	}
	if t.Tree == nil || t.Root == nil {
		state.errorf("%q is an incomplete or empty template", t.Name())
	}
	state.walk(value, t.Root)
	return
}

// DefinedTemplates 返回一个字符串，列出已定义的模板，
// 前缀为 "; defined templates are: "。如果没有，则返回空字符串。
// 用于在此处和 [html/template] 中生成错误消息。
func (t *Template) DefinedTemplates() string {
	if t.common == nil {
		return ""
	}
	var b strings.Builder
	t.muTmpl.RLock()
	defer t.muTmpl.RUnlock()
	for name, tmpl := range t.tmpl {
		if tmpl.Tree == nil || tmpl.Root == nil {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("; defined templates are: ")
		} else {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", name)
	}
	return b.String()
}

// 用于 panic 的哨兵错误，用于从 range 循环中提前退出。
var (
	walkBreak    = errors.New("break")
	walkContinue = errors.New("continue")
)

// Walk 函数逐步执行模板结构的主要部分，生成输出。
func (s *state) walk(dot reflect.Value, node parse.Node) {
	s.at(node)
	switch node := node.(type) {
	case *parse.ActionNode:
		// 不要弹出变量，以便它们持续到下一个 end。
		// 此外，如果动作声明了变量，不要打印结果。
		val := s.evalPipeline(dot, node.Pipe)
		if len(node.Pipe.Decl) == 0 {
			s.printValue(node, val)
		}
	case *parse.BreakNode:
		panic(walkBreak)
	case *parse.CommentNode:
	case *parse.ContinueNode:
		panic(walkContinue)
	case *parse.IfNode:
		s.walkIfOrWith(parse.NodeIf, dot, node.Pipe, node.List, node.ElseList)
	case *parse.ListNode:
		for _, node := range node.Nodes {
			s.walk(dot, node)
		}
	case *parse.RangeNode:
		s.walkRange(dot, node)
	case *parse.TemplateNode:
		s.walkTemplate(dot, node)
	case *parse.TextNode:
		if _, err := s.wr.Write(node.Text); err != nil {
			s.writeError(err)
		}
	case *parse.WithNode:
		s.walkIfOrWith(parse.NodeWith, dot, node.Pipe, node.List, node.ElseList)
	default:
		s.errorf("unknown node: %s", node)
	}
}

// walkIfOrWith 遍历 'if' 或 'with' 节点。这两种控制结构的行为相同，
// 除了 'with' 设置 dot。
func (s *state) walkIfOrWith(typ parse.NodeType, dot reflect.Value, pipe *parse.PipeNode, list, elseList *parse.ListNode) {
	defer s.pop(s.mark())
	val := s.evalPipeline(dot, pipe)
	truth, ok := isTrue(indirectInterface(val))
	if !ok {
		s.errorf("if/with can't use %v", val)
	}
	if truth {
		if typ == parse.NodeWith {
			s.walk(val, list)
		} else {
			s.walk(dot, list)
		}
	} else if elseList != nil {
		s.walk(dot, elseList)
	}
}

// IsTrue 报告值是否"真"（在其类型的零值意义上），
// 以及值是否有有意义的真值。这是 if 和其他类似动作使用的真值定义。
func IsTrue(val any) (truth, ok bool) {
	return isTrue(reflect.ValueOf(val))
}

func isTrue(val reflect.Value) (truth, ok bool) {
	if !val.IsValid() {
		// Something like var x interface{}, never set. It's a form of nil.
		return false, true
	}
	switch val.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		truth = val.Len() > 0
	case reflect.Bool:
		truth = val.Bool()
	case reflect.Complex64, reflect.Complex128:
		truth = val.Complex() != 0
	case reflect.Chan, reflect.Func, reflect.Pointer, reflect.UnsafePointer, reflect.Interface:
		truth = !val.IsNil()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		truth = val.Int() != 0
	case reflect.Float32, reflect.Float64:
		truth = val.Float() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		truth = val.Uint() != 0
	case reflect.Struct:
		truth = true // Struct values are always true.
	default:
		return
	}
	return truth, true
}

func (s *state) walkRange(dot reflect.Value, r *parse.RangeNode) {
	s.at(r)
	defer func() {
		if r := recover(); r != nil && r != walkBreak {
			panic(r)
		}
	}()
	defer s.pop(s.mark())
	val, _ := indirect(s.evalPipeline(dot, r.Pipe))
	// mark top of stack before any variables in the body are pushed.
	mark := s.mark()
	oneIteration := func(index, elem reflect.Value) {
		if len(r.Pipe.Decl) > 0 {
			if r.Pipe.IsAssign {
				// With two variables, index comes first.
				// With one, we use the element.
				if len(r.Pipe.Decl) > 1 {
					s.setVar(r.Pipe.Decl[0].Ident[0], index)
				} else {
					s.setVar(r.Pipe.Decl[0].Ident[0], elem)
				}
			} else {
				// Set top var (lexically the second if there
				// are two) to the element.
				s.setTopVar(1, elem)
			}
		}
		if len(r.Pipe.Decl) > 1 {
			if r.Pipe.IsAssign {
				s.setVar(r.Pipe.Decl[1].Ident[0], elem)
			} else {
				// Set next var (lexically the first if there
				// are two) to the index.
				s.setTopVar(2, index)
			}
		}
		defer s.pop(mark)
		defer func() {
			// Consume panic(walkContinue)
			if r := recover(); r != nil && r != walkContinue {
				panic(r)
			}
		}()
		s.walk(elem, r.List)
	}
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if len(r.Pipe.Decl) > 1 {
			s.errorf("can't use %v to iterate over more than one variable", val)
			break
		}
		run := false
		for v := range val.Seq() {
			run = true
			// Pass element as second value, as we do for channels.
			oneIteration(reflect.Value{}, v)
		}
		if !run {
			break
		}
		return
	case reflect.Array, reflect.Slice:
		if val.Len() == 0 {
			break
		}
		for i := 0; i < val.Len(); i++ {
			oneIteration(reflect.ValueOf(i), val.Index(i))
		}
		return
	case reflect.Map:
		if val.Len() == 0 {
			break
		}
		om := fmtsort.Sort(val)
		for _, m := range om {
			oneIteration(m.Key, m.Value)
		}
		return
	case reflect.Chan:
		if val.IsNil() {
			break
		}
		if val.Type().ChanDir() == reflect.SendDir {
			s.errorf("range over send-only channel %v", val)
			break
		}
		i := 0
		for ; ; i++ {
			elem, ok := val.Recv()
			if !ok {
				break
			}
			oneIteration(reflect.ValueOf(i), elem)
		}
		if i == 0 {
			break
		}
		return
	case reflect.Invalid:
		break // An invalid value is likely a nil map, etc. and acts like an empty map.
	case reflect.Func:
		if val.Type().CanSeq() {
			if len(r.Pipe.Decl) > 1 {
				s.errorf("can't use %v iterate over more than one variable", val)
				break
			}
			run := false
			for v := range val.Seq() {
				run = true
				// Pass element as second value,
				// as we do for channels.
				oneIteration(reflect.Value{}, v)
			}
			if !run {
				break
			}
			return
		}
		if val.Type().CanSeq2() {
			run := false
			for i, v := range val.Seq2() {
				run = true
				if len(r.Pipe.Decl) > 1 {
					oneIteration(i, v)
				} else {
					// If there is only one range variable,
					// oneIteration will use the
					// second value.
					oneIteration(reflect.Value{}, i)
				}
			}
			if !run {
				break
			}
			return
		}
		fallthrough
	default:
		s.errorf("range can't iterate over %v", val)
	}
	if r.ElseList != nil {
		s.walk(dot, r.ElseList)
	}
}

func (s *state) walkTemplate(dot reflect.Value, t *parse.TemplateNode) {
	s.at(t)
	tmpl := s.tmpl.Lookup(t.Name)
	if tmpl == nil {
		s.errorf("template %q not defined", t.Name)
	}
	if s.depth == maxExecDepth {
		s.errorf("exceeded maximum template depth (%v)", maxExecDepth)
	}
	// 变量由管道声明会持久化。
	dot = s.evalPipeline(dot, t.Pipe)
	newState := *s
	newState.depth++
	newState.tmpl = tmpl
	// 没有动态作用域：模板调用不继承变量。
	newState.vars = []variable{{"$", dot}}
	newState.walk(dot, tmpl.Root)
}

// Eval 函数求值管道、命令及其元素，并通过检查字段、调用方法等
// 从数据结构中提取值。这些值的打印只通过 walk 函数进行。

// evalPipeline 返回通过求值管道获得的值。如果管道有变量声明，
// 变量将被压入栈中。因此，调用者在依赖于管道值执行命令后应弹出栈。
func (s *state) evalPipeline(dot reflect.Value, pipe *parse.PipeNode) (value reflect.Value) {
	if pipe == nil {
		return
	}
	s.at(pipe)
	value = missingVal
	for _, cmd := range pipe.Cmds {
		value = s.evalCommand(dot, cmd, value) // 前一个值是这一个的最终参数。
		// 如果对象有 type interface{}，向下深入一层到内部的东西。
		if value.Kind() == reflect.Interface && value.Type().NumMethod() == 0 {
			value = value.Elem()
		}
	}
	for _, variable := range pipe.Decl {
		if pipe.IsAssign {
			s.setVar(variable.Ident[0], value)
		} else {
			s.push(variable.Ident[0], value)
		}
	}
	return value
}

func (s *state) notAFunction(args []parse.Node, final reflect.Value) {
	if len(args) > 1 || !isMissing(final) {
		s.errorf("can't give argument to non-function %s", args[0])
	}
}

func (s *state) evalCommand(dot reflect.Value, cmd *parse.CommandNode, final reflect.Value) reflect.Value {
	firstWord := cmd.Args[0]
	switch n := firstWord.(type) {
	case *parse.FieldNode:
		return s.evalFieldNode(dot, n, cmd.Args, final)
	case *parse.ChainNode:
		return s.evalChainNode(dot, n, cmd.Args, final)
	case *parse.IdentifierNode:
		// 必须是函数。
		return s.evalFunction(dot, n, cmd, cmd.Args, final)
	case *parse.PipeNode:
		// 带括号的管道。参数都在管道内部；final 必须不存在。
		s.notAFunction(cmd.Args, final)
		return s.evalPipeline(dot, n)
	case *parse.VariableNode:
		return s.evalVariableNode(dot, n, cmd.Args, final)
	}
	s.at(firstWord)
	s.notAFunction(cmd.Args, final)
	switch word := firstWord.(type) {
	case *parse.BoolNode:
		return reflect.ValueOf(word.True)
	case *parse.DotNode:
		return dot
	case *parse.NilNode:
		s.errorf("nil is not a command")
	case *parse.NumberNode:
		return s.idealConstant(word)
	case *parse.StringNode:
		return reflect.ValueOf(word.Text)
	}
	s.errorf("can't evaluate command %q", firstWord)
	panic("not reached")
}

// idealConstant 在我们不知道类型的上下文中调用以返回数字的值。
// 在这种情况下，数字的语法告诉我们它的类型，我们使用 Go 规则来解析。
// 注意在这种情况下不存在 uint 理想常量——值必须是 int 类型。
func (s *state) idealConstant(constant *parse.NumberNode) reflect.Value {
	// 这些是理想常量，但我们不知道类型也没有上下文。（如果是方法参数，我们会知道需要什么。）
	// 语法在某种程度上指导我们。
	s.at(constant)
	switch {
	case constant.IsComplex:
		return reflect.ValueOf(constant.Complex128) // incontrovertible.

	case constant.IsFloat &&
		!isHexInt(constant.Text) && !isRuneInt(constant.Text) &&
		strings.ContainsAny(constant.Text, ".eEpP"):
		return reflect.ValueOf(constant.Float64)

	case constant.IsInt:
		n := int(constant.Int64)
		if int64(n) != constant.Int64 {
			s.errorf("%s overflows int", constant.Text)
		}
		return reflect.ValueOf(n)

	case constant.IsUint:
		s.errorf("%s overflows int", constant.Text)
	}
	return zero
}

func isRuneInt(s string) bool {
	return len(s) > 0 && s[0] == '\''
}

func isHexInt(s string) bool {
	return len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') && !strings.ContainsAny(s, "pP")
}

func (s *state) evalFieldNode(dot reflect.Value, field *parse.FieldNode, args []parse.Node, final reflect.Value) reflect.Value {
	s.at(field)
	return s.evalFieldChain(dot, dot, field, field.Ident, args, final)
}

func (s *state) evalChainNode(dot reflect.Value, chain *parse.ChainNode, args []parse.Node, final reflect.Value) reflect.Value {
	s.at(chain)
	if len(chain.Field) == 0 {
		s.errorf("internal error: no fields in evalChainNode")
	}
	if chain.Node.Type() == parse.NodeNil {
		s.errorf("indirection through explicit nil in %s", chain)
	}
	// (pipe).Field1.Field2 has pipe as .Node, fields as .Field. Eval the pipeline, then the fields.
	pipe := s.evalArg(dot, nil, chain.Node)
	return s.evalFieldChain(dot, pipe, chain, chain.Field, args, final)
}

func (s *state) evalVariableNode(dot reflect.Value, variable *parse.VariableNode, args []parse.Node, final reflect.Value) reflect.Value {
	// $x.Field has $x as the first ident, Field as the second. Eval the var, then the fields.
	s.at(variable)
	value := s.varValue(variable.Ident[0])
	if len(variable.Ident) == 1 {
		s.notAFunction(args, final)
		return value
	}
	return s.evalFieldChain(dot, value, variable, variable.Ident[1:], args, final)
}

// evalFieldChain 求值 .X.Y.Z，可能后跟参数。
// dot 是求值参数的环境，而 receiver 是沿着链遍历的值。
func (s *state) evalFieldChain(dot, receiver reflect.Value, node parse.Node, ident []string, args []parse.Node, final reflect.Value) reflect.Value {
	n := len(ident)
	for i := 0; i < n-1; i++ {
		receiver = s.evalField(dot, ident[i], node, nil, missingVal, receiver)
	}
	// 现在如果是方法，它会得到参数。
	return s.evalField(dot, ident[n-1], node, args, final, receiver)
}

func (s *state) evalFunction(dot reflect.Value, node *parse.IdentifierNode, cmd parse.Node, args []parse.Node, final reflect.Value) reflect.Value {
	s.at(node)
	name := node.Ident
	function, isBuiltin, ok := findFunction(name, s.tmpl)
	if !ok {
		s.errorf("%q is not a defined function", name)
	}
	return s.evalCall(dot, function, isBuiltin, cmd, name, args, final)
}

// evalField 求值类似 (.Field) 或 (.Field arg1 arg2) 的表达式。
// 'final' 参数表示前一个管道值的返回值（如果有）。
func (s *state) evalField(dot reflect.Value, fieldName string, node parse.Node, args []parse.Node, final, receiver reflect.Value) reflect.Value {
	if !receiver.IsValid() {
		if s.tmpl.option.missingKey == mapError { // 将无效值视为缺失的映射键。
			s.errorf("nil data; no entry for key %q", fieldName)
		}
		return zero
	}
	typ := receiver.Type()
	receiver, isNil := indirect(receiver)
	if receiver.Kind() == reflect.Interface && isNil {
		// 在 nil 接口上调用方法不能工作。
		// 下面的 MethodByName 方法调用会 panic。
		s.errorf("nil pointer evaluating %s.%s", typ, fieldName)
		return zero
	}

	// 除非是接口，需要获取类型 *T 的值以保证我们看到 T 和 *T 的所有方法。
	ptr := receiver
	if ptr.Kind() != reflect.Interface && ptr.Kind() != reflect.Pointer && ptr.CanAddr() {
		ptr = ptr.Addr()
	}
	if method := ptr.MethodByName(fieldName); method.IsValid() {
		return s.evalCall(dot, method, false, node, fieldName, args, final)
	}
	hasArgs := len(args) > 1 || !isMissing(final)
	// 不是方法；必须是结构体的字段或映射的元素。
	switch receiver.Kind() {
	case reflect.Struct:
		tField, ok := receiver.Type().FieldByName(fieldName)
		if ok {
			field, err := receiver.FieldByIndexErr(tField.Index)
			if !tField.IsExported() {
				s.errorf("%s is an unexported field of struct type %s", fieldName, typ)
			}
			if err != nil {
				s.errorf("%v", err)
			}
			// 如果是函数，我们必须调用它。
			if hasArgs {
				s.errorf("%s has arguments but cannot be invoked as function", fieldName)
			}
			return field
		}
	case reflect.Map:
		// 如果是映射，尝试使用字段名作为键。
		nameVal := reflect.ValueOf(fieldName)
		if nameVal.Type().AssignableTo(receiver.Type().Key()) {
			if hasArgs {
				s.errorf("%s is not a method but has arguments", fieldName)
			}
			result := receiver.MapIndex(nameVal)
			if !result.IsValid() {
				switch s.tmpl.option.missingKey {
				case mapInvalid:
					// 只使用无效值。
				case mapZeroValue:
					result = reflect.Zero(receiver.Type().Elem())
				case mapError:
					s.errorf("map has no entry for key %q", fieldName)
				}
			}
			return result
		}
	case reflect.Pointer:
		etyp := receiver.Type().Elem()
		if etyp.Kind() == reflect.Struct {
			if _, ok := etyp.FieldByName(fieldName); !ok {
				// 如果没有这样的字段，说"无法求值"而不是"nil 指针求值"。
				break
			}
		}
		if isNil {
			s.errorf("nil pointer evaluating %s.%s", typ, fieldName)
		}
	}
	s.errorf("can't evaluate field %s in type %s", fieldName, typ)
	panic("not reached")
}

var (
	errorType        = reflect.TypeFor[error]()
	fmtStringerType  = reflect.TypeFor[fmt.Stringer]()
	reflectValueType = reflect.TypeFor[reflect.Value]()
)

// evalCall 执行函数或方法调用。如果是方法，fun 已经绑定了接收者，
// 所以它看起来就像函数调用。如果 arg 列表非空，它（以 shell 的方式）包括 arg[0] 作为函数本身。
func (s *state) evalCall(dot, fun reflect.Value, isBuiltin bool, node parse.Node, name string, args []parse.Node, final reflect.Value) reflect.Value {
	if args != nil {
		args = args[1:] // 第零个 arg 是函数名/节点；不传递给函数。
	}
	typ := fun.Type()
	numIn := len(args)
	if !isMissing(final) {
		numIn++
	}
	numFixed := len(args)
	if typ.IsVariadic() {
		numFixed = typ.NumIn() - 1 // 最后一个 arg 是可变参数。
		if numIn < numFixed {
			s.errorf("wrong number of args for %s: want at least %d got %d", name, typ.NumIn()-1, len(args))
		}
	} else if numIn != typ.NumIn() {
		s.errorf("wrong number of args for %s: want %d got %d", name, typ.NumIn(), numIn)
	}
	if err := goodFunc(name, typ); err != nil {
		s.errorf("%v", err)
	}

	unwrap := func(v reflect.Value) reflect.Value {
		if v.Type() == reflectValueType {
			v = v.Interface().(reflect.Value)
		}
		return v
	}

	// 内置 and/or 的特殊情况，它们会短路。
	if isBuiltin && (name == "and" || name == "or") {
		argType := typ.In(0)
		var v reflect.Value
		for _, arg := range args {
			v = s.evalArg(dot, argType, arg).Interface().(reflect.Value)
			if truth(v) == (name == "or") {
				// 这个值已经被 .Interface().(reflect.Value) 解包了
				return v
			}
		}
		if !final.Equal(missingVal) {
			// and/or 的最后一个参数来自管道。
			// 我们没有在 earlier 参数上短路，所以我们要返回这个参数。
			// 我们不必求值 final，但我们确实要检查它的类型。
			// 然后，因为我们将要返回它，我们必须解包它。
			v = unwrap(s.validateType(final, argType))
		}
		return v
	}

	// 构建 arg 列表。
	argv := make([]reflect.Value, numIn)
	// 必须求值参数。先求值固定参数。
	i := 0
	for ; i < numFixed && i < len(args); i++ {
		argv[i] = s.evalArg(dot, typ.In(i), args[i])
	}
	// 现在是 ... 参数。
	if typ.IsVariadic() {
		argType := typ.In(typ.NumIn() - 1).Elem() // 参数是一个切片。
		for ; i < len(args); i++ {
			argv[i] = s.evalArg(dot, argType, args[i])
		}
	}
	// 如有需要添加 final 值。
	if !isMissing(final) {
		t := typ.In(typ.NumIn() - 1)
		if typ.IsVariadic() {
			if numIn-1 < numFixed {
				// 添加的 final 参数对应于函数的固定参数。
				// 根据实际参数的类型进行验证。
				t = typ.In(numIn - 1)
			} else {
				// 添加的 final 参数对应于可变部分。
				// 根据可变切片元素的类型进行验证。
				t = t.Elem()
			}
		}
		argv[i] = s.validateType(final, t)
	}

	// "call" 内置函数的特殊情况。
	// 将被调用的函数名作为第一个参数插入。
	if isBuiltin && name == "call" {
		var calleeName string
		if len(args) == 0 {
			// final 必须存在，否则我们会在上面出错。
			calleeName = final.String()
		} else {
			calleeName = args[0].String()
		}
		argv = append([]reflect.Value{reflect.ValueOf(calleeName)}, argv...)
		fun = reflect.ValueOf(call)
	}

	v, err := safeCall(fun, argv)
	// 如果有非 nil 的错误，停止执行并将错误返回给调用者。
	if err != nil {
		s.at(node)
		s.errorf("error calling %s: %w", name, err)
	}
	return unwrap(v)
}

// canBeNil 报告无类型的 nil 是否可以赋值给该类型。参见 reflect.Zero。
func canBeNil(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	case reflect.Struct:
		return typ == reflectValueType
	}
	return false
}

// validateType 保证值有效且可赋值给类型。
func (s *state) validateType(value reflect.Value, typ reflect.Type) reflect.Value {
	if !value.IsValid() {
		if typ == nil {
			// 一个无类型的 nil interface{}。接受为适当的 nil 值。
			return reflect.ValueOf(nil)
		}
		if canBeNil(typ) {
			// 像上面一样，但使用非 nil 类型的零值。
			return reflect.Zero(typ)
		}
		s.errorf("invalid value; expected %s", typ)
	}
	if typ == reflectValueType && value.Type() != typ {
		return reflect.ValueOf(value)
	}
	if typ != nil && !value.Type().AssignableTo(typ) {
		if value.Kind() == reflect.Interface && !value.IsNil() {
			value = value.Elem()
			if value.Type().AssignableTo(typ) {
				return value
			}
			// fallthrough
		}
		// 解引用或间接一次是否可以工作？我们可以做更多，
		// 就像我们对方法接收者所做的那样，但这变得很乱，
		// 而且方法接收者受到更多约束，所以在那里更有意义。
		// 此外，几乎总是只需要一次。
		switch {
		case value.Kind() == reflect.Pointer && value.Type().Elem().AssignableTo(typ):
			value = value.Elem()
			if !value.IsValid() {
				s.errorf("dereference of nil pointer of type %s", typ)
			}
		case reflect.PointerTo(value.Type()).AssignableTo(typ) && value.CanAddr():
			value = value.Addr()
		default:
			s.errorf("wrong type for value; expected %s; got %s", typ, value.Type())
		}
	}
	return value
}

func (s *state) evalArg(dot reflect.Value, typ reflect.Type, n parse.Node) reflect.Value {
	s.at(n)
	switch arg := n.(type) {
	case *parse.DotNode:
		return s.validateType(dot, typ)
	case *parse.NilNode:
		if canBeNil(typ) {
			return reflect.Zero(typ)
		}
		s.errorf("cannot assign nil to %s", typ)
	case *parse.FieldNode:
		return s.validateType(s.evalFieldNode(dot, arg, []parse.Node{n}, missingVal), typ)
	case *parse.VariableNode:
		return s.validateType(s.evalVariableNode(dot, arg, nil, missingVal), typ)
	case *parse.PipeNode:
		return s.validateType(s.evalPipeline(dot, arg), typ)
	case *parse.IdentifierNode:
		return s.validateType(s.evalFunction(dot, arg, arg, nil, missingVal), typ)
	case *parse.ChainNode:
		return s.validateType(s.evalChainNode(dot, arg, nil, missingVal), typ)
	}
	switch typ.Kind() {
	case reflect.Bool:
		return s.evalBool(typ, n)
	case reflect.Complex64, reflect.Complex128:
		return s.evalComplex(typ, n)
	case reflect.Float32, reflect.Float64:
		return s.evalFloat(typ, n)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return s.evalInteger(typ, n)
	case reflect.Interface:
		if typ.NumMethod() == 0 {
			return s.evalEmptyInterface(dot, n)
		}
	case reflect.Struct:
		if typ == reflectValueType {
			return reflect.ValueOf(s.evalEmptyInterface(dot, n))
		}
	case reflect.String:
		return s.evalString(typ, n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return s.evalUnsignedInteger(typ, n)
	}
	s.errorf("can't handle %s for arg of type %s", n, typ)
	panic("not reached")
}

func (s *state) evalBool(typ reflect.Type, n parse.Node) reflect.Value {
	s.at(n)
	if n, ok := n.(*parse.BoolNode); ok {
		value := reflect.New(typ).Elem()
		value.SetBool(n.True)
		return value
	}
	s.errorf("expected bool; found %s", n)
	panic("not reached")
}

func (s *state) evalString(typ reflect.Type, n parse.Node) reflect.Value {
	s.at(n)
	if n, ok := n.(*parse.StringNode); ok {
		value := reflect.New(typ).Elem()
		value.SetString(n.Text)
		return value
	}
	s.errorf("expected string; found %s", n)
	panic("not reached")
}

func (s *state) evalInteger(typ reflect.Type, n parse.Node) reflect.Value {
	s.at(n)
	if n, ok := n.(*parse.NumberNode); ok && n.IsInt {
		value := reflect.New(typ).Elem()
		value.SetInt(n.Int64)
		return value
	}
	s.errorf("expected integer; found %s", n)
	panic("not reached")
}

func (s *state) evalUnsignedInteger(typ reflect.Type, n parse.Node) reflect.Value {
	s.at(n)
	if n, ok := n.(*parse.NumberNode); ok && n.IsUint {
		value := reflect.New(typ).Elem()
		value.SetUint(n.Uint64)
		return value
	}
	s.errorf("expected unsigned integer; found %s", n)
	panic("not reached")
}

func (s *state) evalFloat(typ reflect.Type, n parse.Node) reflect.Value {
	s.at(n)
	if n, ok := n.(*parse.NumberNode); ok && n.IsFloat {
		value := reflect.New(typ).Elem()
		value.SetFloat(n.Float64)
		return value
	}
	s.errorf("expected float; found %s", n)
	panic("not reached")
}

func (s *state) evalComplex(typ reflect.Type, n parse.Node) reflect.Value {
	if n, ok := n.(*parse.NumberNode); ok && n.IsComplex {
		value := reflect.New(typ).Elem()
		value.SetComplex(n.Complex128)
		return value
	}
	s.errorf("expected complex; found %s", n)
	panic("not reached")
}

func (s *state) evalEmptyInterface(dot reflect.Value, n parse.Node) reflect.Value {
	s.at(n)
	switch n := n.(type) {
	case *parse.BoolNode:
		return reflect.ValueOf(n.True)
	case *parse.DotNode:
		return dot
	case *parse.FieldNode:
		return s.evalFieldNode(dot, n, nil, missingVal)
	case *parse.IdentifierNode:
		return s.evalFunction(dot, n, n, nil, missingVal)
	case *parse.NilNode:
		// NilNode is handled in evalArg, the only place that calls here.
		s.errorf("evalEmptyInterface: nil (can't happen)")
	case *parse.NumberNode:
		return s.idealConstant(n)
	case *parse.StringNode:
		return reflect.ValueOf(n.Text)
	case *parse.VariableNode:
		return s.evalVariableNode(dot, n, nil, missingVal)
	case *parse.PipeNode:
		return s.evalPipeline(dot, n)
	}
	s.errorf("can't handle assignment of %s to empty interface argument", n)
	panic("not reached")
}

// indirect 返回间接寻址末尾的项，并返回一个 bool 指示它是否为 nil。
// 如果返回的 bool 为 true，返回值的 kind 将是 pointer 或 interface。
func indirect(v reflect.Value) (rv reflect.Value, isNil bool) {
	for ; v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface; v = v.Elem() {
		if v.IsNil() {
			return v, true
		}
	}
	return v, false
}

// indirectInterface 返回接口值中的具体值，否则返回零 reflect.Value。
// 也就是说，如果 v 代表接口值 x，结果与 reflect.ValueOf(x) 相同：
// x 是接口值这一事实被忘记了。
func indirectInterface(v reflect.Value) reflect.Value {
	if v.Kind() != reflect.Interface {
		return v
	}
	if v.IsNil() {
		return reflect.Value{}
	}
	return v.Elem()
}

// printValue 将值的文本表示写入模板的输出。
func (s *state) printValue(n parse.Node, v reflect.Value) {
	s.at(n)
	iface, ok := printableValue(v)
	if !ok {
		s.errorf("can't print %s of type %s", n, v.Type())
	}
	_, err := fmt.Fprint(s.wr, iface)
	if err != nil {
		s.writeError(err)
	}
}

// printableValue 返回 v 中最适合调用格式化打印机的（可能是间接的）接口值。
func printableValue(v reflect.Value) (any, bool) {
	if v.Kind() == reflect.Pointer {
		v, _ = indirect(v) // fmt.Fprint handles nil.
	}
	if !v.IsValid() {
		return "<no value>", true
	}

	if !v.Type().Implements(errorType) && !v.Type().Implements(fmtStringerType) {
		if v.CanAddr() && (reflect.PointerTo(v.Type()).Implements(errorType) || reflect.PointerTo(v.Type()).Implements(fmtStringerType)) {
			v = v.Addr()
		} else {
			switch v.Kind() {
			case reflect.Chan, reflect.Func:
				return nil, false
			}
		}
	}
	return v.Interface(), true
}
