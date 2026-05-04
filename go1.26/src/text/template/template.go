// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"maps"
	"reflect"
	"sync"
	"text/template/parse"
)

// common 持有相关模板之间共享的信息。
type common struct {
	tmpl   map[string]*Template // 从名称到已定义模板的映射。
	muTmpl sync.RWMutex         // 保护 tmpl
	option option
	// 我们使用两个映射，一个用于解析，一个用于执行。
	// 这种分离使 API 更清晰，因为它不会向客户端暴露反射。
	muFuncs    sync.RWMutex // 保护 parseFuncs 和 execFuncs
	parseFuncs FuncMap
	execFuncs  map[string]reflect.Value
}

// Template 是解析后模板的表示。*parse.Tree 字段仅导出供 [html/template] 使用，
// 所有其他客户端应将其视为未导出。
type Template struct {
	name string
	*parse.Tree
	*common
	leftDelim  string
	rightDelim string
}

// New 分配一个具有给定名称的新的未定义模板。
func New(name string) *Template {
	t := &Template{
		name: name,
	}
	t.init()
	return t
}

// Name 返回模板的名称。
func (t *Template) Name() string {
	return t.name
}

// New 分配一个与给定模板关联的新未定义模板，并具有相同的分隔符。
// 这种关联是传递的，允许一个模板通过 {{template}} 动作调用另一个模板。
//
// 因为关联的模板共享底层数据，所以不能安全地并行构建模板。
// 一旦模板构建完成，它们就可以并行执行。
func (t *Template) New(name string) *Template {
	t.init()
	nt := &Template{
		name:       name,
		common:     t.common,
		leftDelim:  t.leftDelim,
		rightDelim: t.rightDelim,
	}
	return nt
}

// init 保证 t 有有效的 common 结构。
func (t *Template) init() {
	if t.common == nil {
		c := new(common)
		c.tmpl = make(map[string]*Template)
		c.parseFuncs = make(FuncMap)
		c.execFuncs = make(map[string]reflect.Value)
		t.common = c
	}
}

// Clone 返回模板的副本，包括所有关联的模板。实际表示不被复制，
// 但关联模板的名称空间被复制，因此在副本中对 [Template.Parse] 的进一步调用
// 会将模板添加到副本而不是原始模板。Clone 可用于准备通用模板，
// 并通过在克隆后添加变体来将它们与其他模板一起使用。
func (t *Template) Clone() (*Template, error) {
	nt := t.copy(nil)
	nt.init()
	if t.common == nil {
		return nt, nil
	}
	nt.option = t.option
	t.muTmpl.RLock()
	defer t.muTmpl.RUnlock()
	for k, v := range t.tmpl {
		if k == t.name {
			nt.tmpl[t.name] = nt
			continue
		}
		// 关联的模板共享 nt 的 common 结构。
		tmpl := v.copy(nt.common)
		nt.tmpl[k] = tmpl
	}
	t.muFuncs.RLock()
	defer t.muFuncs.RUnlock()
	maps.Copy(nt.parseFuncs, t.parseFuncs)
	maps.Copy(nt.execFuncs, t.execFuncs)
	return nt, nil
}

// copy 返回 t 的浅拷贝，common 设置为参数。
func (t *Template) copy(c *common) *Template {
	return &Template{
		name:       t.name,
		Tree:       t.Tree,
		common:     c,
		leftDelim:  t.leftDelim,
		rightDelim: t.rightDelim,
	}
}

// AddParseTree 将参数解析树与模板 t 关联，给予它指定的名称。
// 如果模板未被定义，此树成为其定义。如果已被定义且已有该名称，
// 则替换现有定义；否则创建、定义并返回新模板。
func (t *Template) AddParseTree(name string, tree *parse.Tree) (*Template, error) {
	t.init()
	t.muTmpl.Lock()
	defer t.muTmpl.Unlock()
	nt := t
	if name != t.name {
		nt = t.New(name)
	}
	// 即使 nt == t，我们也需要将其安装到 common.tmpl 映射中。
	if t.associate(nt, tree) || nt.Tree == nil {
		nt.Tree = tree
	}
	return nt, nil
}

// Templates 返回与 t 关联的已定义模板的切片。
func (t *Template) Templates() []*Template {
	if t.common == nil {
		return nil
	}
	// 返回切片以便不暴露映射。
	t.muTmpl.RLock()
	defer t.muTmpl.RUnlock()
	m := make([]*Template, 0, len(t.tmpl))
	for _, v := range t.tmpl {
		m = append(m, v)
	}
	return m
}

// Delims 将动作分隔符设置为指定的字符串，用于后续调用 [Template.Parse]、
// [Template.ParseFiles] 或 [Template.ParseGlob]。
// 嵌套模板定义将继承这些设置。空分隔符表示相应的默认值：{{ 或 }}。
// 返回值是模板，因此可以链式调用。
func (t *Template) Delims(left, right string) *Template {
	t.init()
	t.leftDelim = left
	t.rightDelim = right
	return t
}

// Funcs 将参数映射的元素添加到模板的函数映射中。
// 必须在解析模板之前调用。如果映射中的值不是具有适当返回类型的函数，
// 或者名称在语法上不能用作模板中的函数，它会 panic。
// 覆盖映射中的元素是合法的。返回值是模板，因此可以链式调用。
func (t *Template) Funcs(funcMap FuncMap) *Template {
	t.init()
	t.muFuncs.Lock()
	defer t.muFuncs.Unlock()
	addValueFuncs(t.execFuncs, funcMap)
	addFuncs(t.parseFuncs, funcMap)
	return t
}

// Lookup 返回与 t 关联的具有给定名称的模板。
// 如果没有这样的模板或模板没有定义，则返回 nil。
func (t *Template) Lookup(name string) *Template {
	if t.common == nil {
		return nil
	}
	t.muTmpl.RLock()
	defer t.muTmpl.RUnlock()
	return t.tmpl[name]
}

// Parse 将 text 解析为模板主体 for t。
// text 中的命名模板定义（{{define ...}} 或 {{block ...}} 语句）
// 定义了与 t 关联的附加模板，并从 t 自身的定义中移除。
//
// 可以在 successive 调用 Parse 中重新定义模板。
// 主体仅包含空白和注释的模板定义被视为空，不会替换现有模板的主体。
// 这允许使用 Parse 添加新的命名模板定义而不覆盖主模板主体。
func (t *Template) Parse(text string) (*Template, error) {
	t.init()
	t.muFuncs.RLock()
	trees, err := parse.Parse(t.name, text, t.leftDelim, t.rightDelim, t.parseFuncs, builtins())
	t.muFuncs.RUnlock()
	if err != nil {
		return nil, err
	}
	// 将新解析的树（包括 t 的树）添加到我们的 common 结构中。
	for name, tree := range trees {
		if _, err := t.AddParseTree(name, tree); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// associate 将新模板安装到与 t 关联的模板组中。
// 已知两者共享 common 结构。布尔返回值报告是否将此树存储为 t.Tree。
func (t *Template) associate(new *Template, tree *parse.Tree) bool {
	if new.common != t.common {
		panic("internal error: associate not common")
	}
	if old := t.tmpl[new.name]; old != nil && parse.IsEmptyTree(tree.Root) && old.Tree != nil {
		// 如果存在该名称的模板，不要用空模板替换它。
		return false
	}
	t.tmpl[new.name] = new
	return true
}
