// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"fmt"
	"text/template/parse"
)

// Error 描述模板转义过程中遇到的问题。
type Error struct {
	// ErrorCode 描述错误的种类。
	ErrorCode ErrorCode
	// Node 是导致问题的节点（如果已知）。
	// 如果非 nil，它将覆盖 Name 和 Line。
	Node parse.Node
	// Name 是遇到错误的模板名称。
	Name string
	// Line 是模板源码中的错误行号，0 表示未知。
	Line int
	// Description 是对问题的可读描述。
	Description string
}

// ErrorCode 是一种错误的错误码。
type ErrorCode int

// 我们为转义模板时出现的每种错误定义了错误码，但转义后的模板在运行时也可能失败。
//
// 输出："ZgotmplZ"
// 示例：
//
//	<img src="{{.X}}">
//	其中 {{.X}} 求值为 `javascript:...`
//
// 讨论：
//
//	"ZgotmplZ" 是一个特殊值，表示不安全的内容在运行时到达了 CSS 或 URL 上下文。
//	上述示例的输出将是
//	  <img src="#ZgotmplZ">
//	如果数据来自可信来源，使用内容类型使其免于过滤：URL(`javascript:...`)。
const (
	// OK 表示没有错误。
	OK ErrorCode = iota

	// ErrAmbigContext："... 出现在 URL 中的歧义上下文中"
	// 示例：
	//   <a href="
	//      {{if .C}}
	//        /path/
	//      {{else}}
	//        /search?q=
	//      {{end}}
	//      {{.X}}
	//   ">
	// 讨论：
	//   {{.X}} 处于歧义的 URL 上下文中，因为根据 {{.C}} 的值，
	//   它可能是 URL 后缀或查询参数。
	//   将 {{.X}} 移入条件分支可消除歧义：
	//   <a href="{{if .C}}/path/{{.X}}{{else}}/search?q={{.X}}">
	ErrAmbigContext

	// ErrBadHTML："期望空格、属性名或标签结束，但得到了……"，
	//   "……在未加引号的属性中"，"……在属性名中"
	// 示例：
	//   <a href = /search?q=foo>
	//   <href=foo>
	//   <form na<e=...>
	//   <option selected<
	// 讨论：
	//   这通常是由 HTML 元素中的拼写错误引起的，但某些字符在标签名、
	//   属性名和未加引号的属性值中是被禁止的，因为它们可能引发解析器歧义。
	//   给所有属性加引号是最佳策略。
	ErrBadHTML

	// ErrBranchEnd："{{if}} 分支在不同的上下文中结束"
	// 示例：
	//   {{if .C}}<a href="{{end}}{{.X}}
	//   <script {{with .T}}type="{{.}}"{{end}}>
	// 讨论：
	//   html/template 包静态地检查通过 {{if}}、{{range}} 或 {{with}} 的每条路径，
	//   以转义后续的管道。第一个示例是歧义的，因为 {{.X}} 可能是 HTML 文本节点，
	//   也可能是 HTML 属性中的 URL 前缀。{{.X}} 的上下文用于确定如何转义它，
	//   但该上下文取决于 {{.C}} 的运行时值，而这在静态分析时是未知的。
	//   第二个示例是歧义的，因为 script type 属性可以改变脚本内容所需的转义类型。
	//
	//   问题通常是缺少引号或尖括号之类的东西，或者可以通过重构来将两个上下文
	//   放入 if、range 或 with 的不同分支中来避免。如果问题出在对一个不应为空
	//   的集合进行 {{range}} 时，添加一个虚拟的 {{else}} 可能会有帮助。
	ErrBranchEnd

	// ErrEndContext："……以非文本上下文结束：……"
	// 示例：
	//   <div
	//   <div title="no close quote>
	//   <script>f()
	// 讨论：
	//   执行的模板应产生 HTML 的 DocumentFragment。
	//   没有闭合标签就结束的模板将触发此错误。
	//   不应在 HTML 上下文中使用的模板或产生不完整 Fragment 的模板不应直接执行。
	//
	//   {{define "main"}} <script>{{template "helper"}}</script> {{end}}
	//   {{define "helper"}} document.write(' <div title=" ') {{end}}
	//
	//   "helper" 不产生有效的文档片段，因此不应直接执行。
	ErrEndContext

	// ErrNoSuchTemplate："没有这样的模板……"
	// 示例：
	//   {{define "main"}}<div {{template "attrs"}}>{{end}}
	//   {{define "attrs"}}href="{{.URL}}"{{end}}
	// 讨论：
	//   html/template 包会查看模板调用来计算上下文。
	//   这里 "attrs" 中的 {{.URL}} 在从 "main" 调用时必须被视为 URL，
	//   但如果在解析 "main" 时 "attrs" 尚未定义，你将得到此错误。
	ErrNoSuchTemplate

	// ErrOutputContext："无法计算模板的输出上下文……"
	// 示例：
	//   {{define "t"}}{{if .T}}{{template "t" .T}}{{end}}{{.H}}",{{end}}
	// 讨论：
	//   递归模板没有在其开始的相同上下文中结束，因此无法计算可靠的输出上下文。
	//   检查命名模板中是否有拼写错误。
	//   如果模板不应在命名的起始上下文中被调用，请检查是否在意外的上下文中调用了该模板。
	//   也许可以重构递归模板使其不再递归。
	ErrOutputContext

	// ErrPartialCharset："未完成的 JS 正则表达式字符集……"
	// 示例：
	//     <script>var pattern = /foo[{{.Chars}}]/</script>
	// 讨论：
	//   html/template 包不支持在正则表达式字面量字符集中进行插值。
	ErrPartialCharset

	// ErrPartialEscape："未完成的转义序列……"
	// 示例：
	//   <script>alert("\{{.X}}")</script>
	// 讨论：
	//   html/template 包不支持反斜杠后面跟随 action。
	//   这通常是一个错误，有更好的解决方案；例如
	//     <script>alert("{{.X}}")</script>
	//   应该可以工作，如果 {{.X}} 是部分转义序列（如 "xA0"），
	//   则将整个序列标记为安全内容：JSStr(`\xA0`)
	ErrPartialEscape

	// ErrRangeLoopReentry："在 range 循环重新进入时：……"
	// 示例：
	//   <script>var x = [{{range .}}'{{.}},{{end}}]</script>
	// 讨论：
	//   如果对 range 的一次迭代会使其在与先前不同的上下文中结束，
	//   则不存在单一的上下文。在此示例中，缺少一个引号，因此不清楚
	//   {{.}} 是应在 JS 字符串内部还是在 JS 值上下文中。
	//   第二次迭代将产生类似于
	//
	//     <script>var x = ['firstValue,'secondValue]</script>
	ErrRangeLoopReentry

	// ErrSlashAmbig：'/' 可能开始除法或正则表达式。
	// 示例：
	//   <script>
	//     {{if .C}}var x = 1{{end}}
	//     /-{{.N}}/i.test(x) ? doThis : doThat();
	//   </script>
	// 讨论：
	//   上面的示例可能产生 `var x = 1/-2/i.test(s)...`，
	//   其中第一个 '/' 是数学除法运算符；也可能产生 `/-2/i.test(s)`，
	//   其中第一个 '/' 开始一个正则表达式字面量。
	//   检查分支内是否缺少分号，并可能添加括号以明确你的意图。
	ErrSlashAmbig

	// ErrPredefinedEscaper："预定义转义器……在模板中不被允许"
	// 示例：
	//   <div class={{. | html}}>Hello<div>
	// 讨论：
	//   html/template 包已经对所有管道进行上下文转义，以产生防止代码注入的安全
	//   HTML 输出。使用预定义转义器 "html" 或 "urlquery" 手动转义管道输出是
	//   不必要的，并且可能影响 Go 1.8 及更早版本中转义管道输出的正确性或安全性。
	//
	//   在大多数情况下（如上面的示例），可以通过简单地从管道中移除预定义转义器
	//   并让上下文自动转义器处理管道的转义来解决此错误。在其他情况下，如果
	//   预定义转义器出现在管道中间，后续命令期望转义后的输入，例如
	//     {{.X | html | makeALink}}
	//   其中 makeALink 执行
	//     return `<a href="`+input+`">link</a>`
	//   请考虑重构周围的模板以利用上下文自动转义器，即
	//     <a href="{{.X}}">link</a>
	//
	//   为了简化向 Go 1.9 及更高版本的迁移，"html" 和 "urlquery" 将继续被允许
	//   作为管道中的最后一个命令。但是，如果管道出现在未加引号的属性值上下文中，
	//   则 "html" 不被允许。在新模板中应完全避免使用 "html" 和 "urlquery"。
	ErrPredefinedEscaper

	// ErrJSTemplate："……出现在 JS 模板字面量中"
	// 示例：
	//     <script>var tmpl = `{{.Interp}}`</script>
	// 讨论：
	//   html/template 包不支持在 JS 模板字面量内部使用 action。
	//
	// Deprecated: 当 action 出现在 JS 模板字面量中时，不再返回 ErrJSTemplate。
	// JS 模板字面量内部的 action 现在会按预期进行转义。
	ErrJSTemplate
)

func (e *Error) Error() string {
	switch {
	case e.Node != nil:
		loc, _ := (*parse.Tree)(nil).ErrorContext(e.Node)
		return fmt.Sprintf("html/template:%s: %s", loc, e.Description)
	case e.Line != 0:
		return fmt.Sprintf("html/template:%s:%d: %s", e.Name, e.Line, e.Description)
	case e.Name != "":
		return fmt.Sprintf("html/template:%s: %s", e.Name, e.Description)
	}
	return "html/template: " + e.Description
}

// errorf 根据格式字符串 f 和 args 创建一个错误。
// 模板 Name 仍需单独提供。
func errorf(k ErrorCode, node parse.Node, line int, f string, args ...any) *Error {
	return &Error{k, node, "", line, fmt.Sprintf(f, args...)}
}
