// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sync"
	"text/template"
	"text/template/parse"
)

// Template 是 "text/template" 的特化 Template，用于产生安全的 HTML 文档片段。
type Template struct {
	// 如果转义失败则为持久错误，如果成功则为 escapeOK。
	escapeErr error
	// 我们可以嵌入 text/template 字段，但不嵌入更安全，因为
	// 我们需要保持我们的名称空间版本和底层模板的名称空间同步。
	text *template.Template
	// 底层模板的解析树，已更新为 HTML 安全。
	Tree       *parse.Tree
	*nameSpace // 所有关联模板共用
}

// escapeOK 是一个哨兵值，用于指示转义有效。
var escapeOK = fmt.Errorf("template escaped correctly")

// nameSpace 是关联中所有模板共享的数据结构。
type nameSpace struct {
	mu      sync.Mutex
	set     map[string]*Template
	escaped bool
	esc     escaper
}

// Templates 返回与 t 关联的模板切片，包括 t 本身。
func (t *Template) Templates() []*Template {
	ns := t.nameSpace
	ns.mu.Lock()
	defer ns.mu.Unlock()
	// 返回切片以避免暴露 map。
	m := make([]*Template, 0, len(ns.set))
	for _, v := range ns.set {
		m = append(m, v)
	}
	return m
}

// Option 为模板设置选项。选项由字符串描述，
// 可以是简单字符串或 "key=value"。选项字符串中最多可以有一个等号。
// 如果选项字符串无法识别或其他方面无效，Option 会 panic。
//
// 已知选项：
//
// missingkey：控制执行期间当 map 使用不存在的键进行索引时的行为。
//
//	"missingkey=default" 或 "missingkey=invalid"
//		默认行为：不做任何操作并继续执行。
//		如果打印，索引操作的结果是字符串 "<no value>"。
//	"missingkey=zero"
//		操作返回 map 类型元素的零值。
//	"missingkey=error"
//		执行立即停止并返回错误。
func (t *Template) Option(opt ...string) *Template {
	t.text.Option(opt...)
	return t
}

// checkCanParse 检查是否可以解析模板。如果不可以，返回错误。
func (t *Template) checkCanParse() error {
	if t == nil {
		return nil
	}
	t.nameSpace.mu.Lock()
	defer t.nameSpace.mu.Unlock()
	if t.nameSpace.escaped {
		return fmt.Errorf("html/template: cannot Parse after Execute")
	}
	return nil
}

// escape 转义所有关联的模板。
func (t *Template) escape() error {
	t.nameSpace.mu.Lock()
	defer t.nameSpace.mu.Unlock()
	t.nameSpace.escaped = true
	if t.escapeErr == nil {
		if t.Tree == nil {
			return fmt.Errorf("template: %q is an incomplete or empty template", t.Name())
		}
		if err := escapeTemplate(t, t.text.Root, t.Name()); err != nil {
			return err
		}
	} else if t.escapeErr != escapeOK {
		return t.escapeErr
	}
	return nil
}

// Execute 将已解析的模板应用到指定的数据对象，将输出写入 wr。
// 如果执行模板或写入输出时发生错误，执行会停止，
// 但部分结果可能已经被写入到输出 writer 中。
// 模板可以安全地并行执行，但如果并行执行共享同一个 Writer，
// 输出可能会交错。
func (t *Template) Execute(wr io.Writer, data any) error {
	if err := t.escape(); err != nil {
		return err
	}
	return t.text.Execute(wr, data)
}

// ExecuteTemplate 将与 t 关联的具有给定名称的模板应用到指定的数据对象，
// 并将输出写入 wr。如果执行模板或写入输出时发生错误，执行会停止，
// 但部分结果可能已经被写入到输出 writer 中。
// 模板可以安全地并行执行，但如果并行执行共享同一个 Writer，
// 输出可能会交错。
func (t *Template) ExecuteTemplate(wr io.Writer, name string, data any) error {
	tmpl, err := t.lookupAndEscapeTemplate(name)
	if err != nil {
		return err
	}
	return tmpl.text.Execute(wr, data)
}

// lookupAndEscapeTemplate 保证具有给定名称的模板已被转义，
// 如果无法转义则返回错误。它返回命名的模板。
func (t *Template) lookupAndEscapeTemplate(name string) (tmpl *Template, err error) {
	t.nameSpace.mu.Lock()
	defer t.nameSpace.mu.Unlock()
	t.nameSpace.escaped = true
	tmpl = t.set[name]
	if tmpl == nil {
		return nil, fmt.Errorf("html/template: %q is undefined", name)
	}
	if tmpl.escapeErr != nil && tmpl.escapeErr != escapeOK {
		return nil, tmpl.escapeErr
	}
	if tmpl.text.Tree == nil || tmpl.text.Root == nil {
		return nil, fmt.Errorf("html/template: %q is an incomplete template", name)
	}
	if t.text.Lookup(name) == nil {
		panic("html/template internal error: template escaping out of sync")
	}
	if tmpl.escapeErr == nil {
		err = escapeTemplate(tmpl, tmpl.text.Root, name)
	}
	return tmpl, err
}

// DefinedTemplates 返回一个列出已定义模板的字符串，
// 前缀为 "; defined templates are: "。如果没有已定义的模板，
// 则返回空字符串。用于生成错误消息。
func (t *Template) DefinedTemplates() string {
	return t.text.DefinedTemplates()
}

// Parse 将 text 解析为 t 的模板主体。
// text 中的命名模板定义（{{define ...}} 或 {{block ...}} 语句）
// 定义与 t 关联的附加模板，并从 t 本身的定义中移除。
//
// 在对 t 或任何关联模板首次使用 [Template.Execute] 之前，
// 可以在对 Parse 的连续调用中重新定义模板。
// 主体仅包含空白和注释的模板定义被视为空的，不会替换现有模板的主体。
// 这允许使用 Parse 添加新的命名模板定义而不覆盖主模板主体。
func (t *Template) Parse(text string) (*Template, error) {
	if err := t.checkCanParse(); err != nil {
		return nil, err
	}

	ret, err := t.text.Parse(text)
	if err != nil {
		return nil, err
	}

	// 一般来说，所有命名模板可能都在底层发生了变化。
	// 无论如何，可能已经定义了一些新的模板。
	// template.Template 集合已更新；更新我们的。
	t.nameSpace.mu.Lock()
	defer t.nameSpace.mu.Unlock()
	for _, v := range ret.Templates() {
		name := v.Name()
		tmpl := t.set[name]
		if tmpl == nil {
			tmpl = t.new(name)
		}
		tmpl.text = v
		tmpl.Tree = v.Tree
	}
	return t, nil
}

// AddParseTree 使用名称和解析树创建一个新模板并将其与 t 关联。
//
// 如果 t 或任何关联模板已被执行，则返回错误。
func (t *Template) AddParseTree(name string, tree *parse.Tree) (*Template, error) {
	if err := t.checkCanParse(); err != nil {
		return nil, err
	}

	t.nameSpace.mu.Lock()
	defer t.nameSpace.mu.Unlock()
	text, err := t.text.AddParseTree(name, tree)
	if err != nil {
		return nil, err
	}
	ret := &Template{
		nil,
		text,
		text.Tree,
		t.nameSpace,
	}
	t.set[name] = ret
	return ret, nil
}

// Clone 返回模板的副本，包括所有关联模板。实际表示不会被复制，
// 但关联模板的名称空间会被复制，因此在副本中对 [Template.Parse] 的进一步调用
// 将把模板添加到副本而不是原始模板。[Template.Clone] 可用于准备通用模板，
// 并在克隆完成后添加变体来与其他模板的变体定义一起使用。
//
// 如果 t 已被执行，则返回错误。
func (t *Template) Clone() (*Template, error) {
	t.nameSpace.mu.Lock()
	defer t.nameSpace.mu.Unlock()
	if t.escapeErr != nil {
		return nil, fmt.Errorf("html/template: cannot Clone %q after it has executed", t.Name())
	}
	textClone, err := t.text.Clone()
	if err != nil {
		return nil, err
	}
	ns := &nameSpace{set: make(map[string]*Template)}
	ns.esc = makeEscaper(ns)
	ret := &Template{
		nil,
		textClone,
		textClone.Tree,
		ns,
	}
	ret.set[ret.Name()] = ret
	for _, x := range textClone.Templates() {
		name := x.Name()
		src := t.set[name]
		if src == nil || src.escapeErr != nil {
			return nil, fmt.Errorf("html/template: cannot Clone %q after it has executed", t.Name())
		}
		x.Tree = x.Tree.Copy()
		ret.set[name] = &Template{
			nil,
			x,
			x.Tree,
			ret.nameSpace,
		}
	}
	// 返回与此模板名称关联的模板。
	return ret.set[ret.Name()], nil
}

// New 分配一个具有给定名称的新 HTML 模板。
func New(name string) *Template {
	ns := &nameSpace{set: make(map[string]*Template)}
	ns.esc = makeEscaper(ns)
	tmpl := &Template{
		nil,
		template.New(name),
		nil,
		ns,
	}
	tmpl.set[name] = tmpl
	return tmpl
}

// New 分配一个与给定模板关联且具有相同分隔符的新 HTML 模板。
// 关联是传递性的，允许一个模板通过 {{template}} action 调用另一个。
//
// 如果具有给定名称的模板已存在，新的 HTML 模板将替换它。
// 现有模板将被重置并与 t 解除关联。
func (t *Template) New(name string) *Template {
	t.nameSpace.mu.Lock()
	defer t.nameSpace.mu.Unlock()
	return t.new(name)
}

// new 是 New 的实现，不持有锁。
func (t *Template) new(name string) *Template {
	tmpl := &Template{
		nil,
		t.text.New(name),
		nil,
		t.nameSpace,
	}
	if existing, ok := tmpl.set[name]; ok {
		emptyTmpl := New(existing.Name())
		*existing = *emptyTmpl
	}
	tmpl.set[name] = tmpl
	return tmpl
}

// Name 返回模板的名称。
func (t *Template) Name() string {
	return t.text.Name()
}

type FuncMap = template.FuncMap

// Funcs 将参数 map 中的元素添加到模板的函数 map 中。
// 必须在模板解析之前调用。
// 如果 map 中的值不是具有适当返回类型的函数，则会 panic。
// 但是，覆盖 map 中的元素是合法的。返回值是模板，因此调用可以链式进行。
func (t *Template) Funcs(funcMap FuncMap) *Template {
	t.text.Funcs(template.FuncMap(funcMap))
	return t
}

// Delims 将 action 分隔符设置为指定的字符串，用于后续对 [Template.Parse]、
// [ParseFiles] 或 [ParseGlob] 的调用。嵌套的模板定义将继承这些设置。
// 空分隔符代表相应的默认值：{{ 或 }}。
// 返回值是模板，因此调用可以链式进行。
func (t *Template) Delims(left, right string) *Template {
	t.text.Delims(left, right)
	return t
}

// Lookup 返回与 t 关联的具有给定名称的模板，如果不存在这样的模板则返回 nil。
func (t *Template) Lookup(name string) *Template {
	t.nameSpace.mu.Lock()
	defer t.nameSpace.mu.Unlock()
	return t.set[name]
}

// Must 是一个辅助函数，它封装了对返回 ([*Template], error) 的函数的调用，
// 如果错误非 nil 则 panic。它旨在用于变量初始化，例如
//
//	var t = template.Must(template.New("name").Parse("html"))
func Must(t *Template, err error) *Template {
	if err != nil {
		panic(err)
	}
	return t
}

// ParseFiles 创建一个新的 [Template] 并从命名文件中解析模板定义。
// 返回的模板的名称将具有第一个文件的（基础）名称和（已解析的）内容。
// 必须至少有一个文件。如果发生错误，解析停止且返回的 [*Template] 为 nil。
//
// 当解析不同目录中同名的多个文件时，最后提到的那个将是最终结果。
// 例如，ParseFiles("a/foo", "b/foo") 将 "b/foo" 存储为名为 "foo" 的模板，
// 而 "a/foo" 不可用。
func ParseFiles(filenames ...string) (*Template, error) {
	return parseFiles(nil, readFileOS, filenames...)
}

// ParseFiles 解析命名文件并将结果模板与 t 关联。如果发生错误，
// 解析停止且返回的模板为 nil；否则为 t。必须至少有一个文件。
//
// 当解析不同目录中同名的多个文件时，最后提到的那个将是最终结果。
//
// 如果 t 或任何关联模板已被执行，ParseFiles 返回错误。
func (t *Template) ParseFiles(filenames ...string) (*Template, error) {
	return parseFiles(t, readFileOS, filenames...)
}

// parseFiles 是方法和函数的辅助函数。如果参数模板为 nil，它将从第一个文件创建。
func parseFiles(t *Template, readFile func(string) (string, []byte, error), filenames ...string) (*Template, error) {
	if err := t.checkCanParse(); err != nil {
		return nil, err
	}

	if len(filenames) == 0 {
		// 不是真正的问题，但保持一致。
		return nil, fmt.Errorf("html/template: no files named in call to ParseFiles")
	}
	for _, filename := range filenames {
		name, b, err := readFile(filename)
		if err != nil {
			return nil, err
		}
		s := string(b)
		// 如果尚未定义，第一个模板成为返回值，
		// 我们使用它进行后续的 New 调用以将所有模板关联在一起。
		// 此外，如果此文件与 t 同名，则此文件成为 t 的内容，因此
		//  t, err := New(name).Funcs(xxx).ParseFiles(name)
		// 可以工作。否则我们创建一个与 t 关联的新模板。
		var tmpl *Template
		if t == nil {
			t = New(name)
		}
		if name == t.Name() {
			tmpl = t
		} else {
			tmpl = t.New(name)
		}
		_, err = tmpl.Parse(s)
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

// ParseGlob 创建一个新的 [Template] 并从模式标识的文件中解析模板定义。
// 文件按照 filepath.Match 的语义进行匹配，且模式必须匹配至少一个文件。
// 返回的模板将具有模式匹配的第一个文件的（基础）名称和（已解析的）内容。
// ParseGlob 等价于使用模式匹配的文件列表调用 [ParseFiles]。
//
// 当解析不同目录中同名的多个文件时，最后提到的那个将是最终结果。
func ParseGlob(pattern string) (*Template, error) {
	return parseGlob(nil, pattern)
}

// ParseGlob 解析模式标识的文件中的模板定义并将结果模板与 t 关联。
// 文件按照 filepath.Match 的语义进行匹配，且模式必须匹配至少一个文件。
// ParseGlob 等价于使用模式匹配的文件列表调用 t.ParseFiles。
//
// 当解析不同目录中同名的多个文件时，最后提到的那个将是最终结果。
//
// 如果 t 或任何关联模板已被执行，ParseGlob 返回错误。
func (t *Template) ParseGlob(pattern string) (*Template, error) {
	return parseGlob(t, pattern)
}

// parseGlob 是函数和方法 ParseGlob 的实现。
func parseGlob(t *Template, pattern string) (*Template, error) {
	if err := t.checkCanParse(); err != nil {
		return nil, err
	}
	filenames, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(filenames) == 0 {
		return nil, fmt.Errorf("html/template: pattern matches no files: %#q", pattern)
	}
	return parseFiles(t, readFileOS, filenames...)
}

// IsTrue 报告值是否为"真"——即不是其类型的零值，
// 以及该值是否具有有意义的真值。这是 if 和其他类似 action 使用的真值定义。
func IsTrue(val any) (truth, ok bool) {
	return template.IsTrue(val)
}

// ParseFS 类似于 [ParseFiles] 或 [ParseGlob]，但从文件系统 fs 读取而不是
// 从宿主操作系统的文件系统读取。它接受一个 glob 模式列表。
// （注意大多数文件名本身就是只匹配自身的 glob 模式。）
func ParseFS(fs fs.FS, patterns ...string) (*Template, error) {
	return parseFS(nil, fs, patterns)
}

// ParseFS 类似于 [Template.ParseFiles] 或 [Template.ParseGlob]，但从文件系统 fs 读取
// 而不是从宿主操作系统的文件系统读取。它接受一个 glob 模式列表。
// （注意大多数文件名本身就是只匹配自身的 glob 模式。）
func (t *Template) ParseFS(fs fs.FS, patterns ...string) (*Template, error) {
	return parseFS(t, fs, patterns)
}

func parseFS(t *Template, fsys fs.FS, patterns []string) (*Template, error) {
	var filenames []string
	for _, pattern := range patterns {
		list, err := fs.Glob(fsys, pattern)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("template: pattern matches no files: %#q", pattern)
		}
		filenames = append(filenames, list...)
	}
	return parseFiles(t, readFileFS(fsys), filenames...)
}

func readFileOS(file string) (name string, b []byte, err error) {
	name = filepath.Base(file)
	b, err = os.ReadFile(file)
	return
}

func readFileFS(fsys fs.FS) func(string) (string, []byte, error) {
	return func(file string) (name string, b []byte, err error) {
		name = path.Base(file)
		b, err = fs.ReadFile(fsys, file)
		return
	}
}
