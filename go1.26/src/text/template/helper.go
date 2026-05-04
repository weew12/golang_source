// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 辅助函数，使构造模板更容易。

package template

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// 解析模板的函数和方法。

// Must 是一个辅助函数，包装返回 (*Template, error) 的函数调用，
// 如果错误非 nil 则 panic。它旨在用于变量初始化，例如
//
//	var t = template.Must(template.New("name").Parse("text"))
func Must(t *Template, err error) *Template {
	if err != nil {
		panic(err)
	}
	return t
}

// ParseFiles 创建一个新的 [Template] 并从命名文件解析模板定义。
// 返回的模板的名称将是第一个文件的基本名称和解析内容。
// 必须至少有一个文件。如果发生错误，解析停止，返回的 *Template 为 nil。
//
// 当解析不同目录中具有相同名称的多个文件时，
// 最后提到的那个将是最终结果。例如，
// ParseFiles("a/foo", "b/foo") 将 "b/foo" 存储为名为 "foo" 的模板，
// 而 "a/foo" 不可用。
func ParseFiles(filenames ...string) (*Template, error) {
	return parseFiles(nil, readFileOS, filenames...)
}

// ParseFiles 解析命名文件并将结果模板与 t 关联。
// 如果发生错误，解析停止，返回的模板为 nil；否则为 t。
// 必须至少有一个文件。由于 ParseFiles 创建的模板由参数文件的基本名称
//（参见 [filepath.Base]）命名，t 通常应该具有文件的基本名称之一。
// 如果不是，根据调用 ParseFiles 前 t 的内容，t.Execute 可能失败。
// 在这种情况下，使用 t.ExecuteTemplate 执行有效模板。
//
// 当解析不同目录中具有相同名称的多个文件时，
// 最后提到的那个将是最终结果。
func (t *Template) ParseFiles(filenames ...string) (*Template, error) {
	t.init()
	return parseFiles(t, readFileOS, filenames...)
}

// parseFiles 是方法和函数的辅助函数。如果参数模板为 nil，
// 则从第一个文件创建它。
func parseFiles(t *Template, readFile func(string) (string, []byte, error), filenames ...string) (*Template, error) {
	if len(filenames) == 0 {
		// 实际上不是问题，但要保持一致。
		return nil, fmt.Errorf("template: no files named in call to ParseFiles")
	}
	for _, filename := range filenames {
		name, b, err := readFile(filename)
		if err != nil {
			return nil, err
		}
		s := string(b)
		// 如果尚未定义，第一个模板成为返回值，
		// 我们使用那个模板进行后续 New 调用以将所有模板关联在一起。
		// 此外，如果此文件与 t 同名，此文件成为 t 的内容，所以
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

// ParseGlob 创建一个新的 [Template] 并从 pattern 标识的文件解析模板定义。
// 文件根据 [filepath.Match] 的语义匹配，pattern 必须至少匹配一个文件。
// 返回的模板将具有匹配 pattern 的第一个文件的 [filepath.Base] 名称和（解析的）内容。
// ParseGlob 等价于使用 pattern 匹配的文件列表调用 [ParseFiles]。
//
// 当解析不同目录中具有相同名称的多个文件时，
// 最后提到的那个将是最终结果。
func ParseGlob(pattern string) (*Template, error) {
	return parseGlob(nil, pattern)
}

// ParseGlob 解析由 pattern 标识的文件中的模板定义，并将结果模板与 t 关联。
// 文件根据 [filepath.Match] 的语义匹配，pattern 必须至少匹配一个文件。
// ParseGlob 等价于使用 pattern 匹配的文件列表调用 [Template.ParseFiles]。
//
// 当解析不同目录中具有相同名称的多个文件时，
// 最后提到的那个将是最终结果。
func (t *Template) ParseGlob(pattern string) (*Template, error) {
	t.init()
	return parseGlob(t, pattern)
}

// parseGlob 是函数和方法 ParseGlob 的实现。
func parseGlob(t *Template, pattern string) (*Template, error) {
	filenames, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(filenames) == 0 {
		return nil, fmt.Errorf("template: pattern matches no files: %#q", pattern)
	}
	return parseFiles(t, readFileOS, filenames...)
}

// ParseFS 类似于 [Template.ParseFiles] 或 [Template.ParseGlob]，
// 但从文件系统 fsys 读取而不是从主机操作系统读取。
// 它接受 glob 模式列表（参见 [path.Match]）。
//（请注意，大多数文件名作为匹配仅自身的 glob 模式。）
func ParseFS(fsys fs.FS, patterns ...string) (*Template, error) {
	return parseFS(nil, fsys, patterns)
}

// ParseFS 类似于 [Template.ParseFiles] 或 [Template.ParseGlob]，
// 但从文件系统 fsys 读取而不是从主机操作系统读取。
// 它接受 glob 模式列表（参见 [path.Match]）。
//（请注意，大多数文件名作为匹配仅自身的 glob 模式。）
func (t *Template) ParseFS(fsys fs.FS, patterns ...string) (*Template, error) {
	t.init()
	return parseFS(t, fsys, patterns)
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
