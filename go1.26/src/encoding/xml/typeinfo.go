// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// typeInfo 保存某个类型的 xml 表示的详细信息。
type typeInfo struct {
	xmlname *fieldInfo
	fields  []fieldInfo
}

// fieldInfo 保存单个字段的 xml 表示的详细信息。
type fieldInfo struct {
	idx     []int
	name    string
	xmlns   string
	flags   fieldFlags
	parents []string
}

type fieldFlags int

const (
	fElement fieldFlags = 1 << iota
	fAttr
	fCDATA
	fCharData
	fInnerXML
	fComment
	fAny

	fOmitEmpty

	fMode = fElement | fAttr | fCDATA | fCharData | fInnerXML | fComment | fAny

	xmlName = "XMLName"
)

var tinfoMap sync.Map // map[reflect.Type]*typeInfo

var nameType = reflect.TypeFor[Name]()

// getTypeInfo 返回类型 typ 的 typeInfo 结构，
// 其中包含封送（marshal）和反封送（unmarshal）所需的详细信息。
func getTypeInfo(typ reflect.Type) (*typeInfo, error) {
	if ti, ok := tinfoMap.Load(typ); ok {
		return ti.(*typeInfo), nil
	}

	tinfo := &typeInfo{}
	if typ.Kind() == reflect.Struct && typ != nameType {
		n := typ.NumField()
		for i := 0; i < n; i++ {
			f := typ.Field(i)
			if (!f.IsExported() && !f.Anonymous) || f.Tag.Get("xml") == "-" {
				continue // 私有字段
			}

			// 对于嵌入的结构体，嵌入其字段。
			if f.Anonymous {
				t := f.Type
				if t.Kind() == reflect.Pointer {
					t = t.Elem()
				}
				if t.Kind() == reflect.Struct {
					inner, err := getTypeInfo(t)
					if err != nil {
						return nil, err
					}
					if tinfo.xmlname == nil {
						tinfo.xmlname = inner.xmlname
					}
					for _, finfo := range inner.fields {
						finfo.idx = append([]int{i}, finfo.idx...)
						if err := addFieldInfo(typ, tinfo, &finfo); err != nil {
							return nil, err
						}
					}
					continue
				}
			}

			finfo, err := structFieldInfo(typ, &f)
			if err != nil {
				return nil, err
			}

			if f.Name == xmlName {
				tinfo.xmlname = finfo
				continue
			}

			// 如果该字段与其他字段不冲突，则添加它。
			if err := addFieldInfo(typ, tinfo, finfo); err != nil {
				return nil, err
			}
		}
	}

	ti, _ := tinfoMap.LoadOrStore(typ, tinfo)
	return ti.(*typeInfo), nil
}

// structFieldInfo 为 f 构建并返回一个 fieldInfo。
func structFieldInfo(typ reflect.Type, f *reflect.StructField) (*fieldInfo, error) {
	finfo := &fieldInfo{idx: f.Index}

	// 如有必要，从 xml 命名空间中拆出标签。
	tag := f.Tag.Get("xml")
	if ns, t, ok := strings.Cut(tag, " "); ok {
		finfo.xmlns, tag = ns, t
	}

	// 解析标志。
	tokens := strings.Split(tag, ",")
	if len(tokens) == 1 {
		finfo.flags = fElement
	} else {
		tag = tokens[0]
		for _, flag := range tokens[1:] {
			switch flag {
			case "attr":
				finfo.flags |= fAttr
			case "cdata":
				finfo.flags |= fCDATA
			case "chardata":
				finfo.flags |= fCharData
			case "innerxml":
				finfo.flags |= fInnerXML
			case "comment":
				finfo.flags |= fComment
			case "any":
				finfo.flags |= fAny
			case "omitempty":
				finfo.flags |= fOmitEmpty
			}
		}

		// 校验所用的标志。
		valid := true
		switch mode := finfo.flags & fMode; mode {
		case 0:
			finfo.flags |= fElement
		case fAttr, fCDATA, fCharData, fInnerXML, fComment, fAny, fAny | fAttr:
			if f.Name == xmlName || tag != "" && mode != fAttr {
				valid = false
			}
		default:
			// 这也会捕获单个字段中的多个模式。
			valid = false
		}
		if finfo.flags&fMode == fAny {
			finfo.flags |= fElement
		}
		if finfo.flags&fOmitEmpty != 0 && finfo.flags&(fElement|fAttr) == 0 {
			valid = false
		}
		if !valid {
			return nil, fmt.Errorf("xml: invalid tag in field %s of type %s: %q",
				f.Name, typ, f.Tag.Get("xml"))
		}
	}

	// 不允许在无名称的情况下使用 xmlns。
	if finfo.xmlns != "" && tag == "" {
		return nil, fmt.Errorf("xml: namespace without name in field %s of type %s: %q",
			f.Name, typ, f.Tag.Get("xml"))
	}

	if f.Name == xmlName {
		// XMLName 字段记录 XML 元素名称。不要
		// 像往常一样处理它，因为它的名称应默认为
		// 空，而不是字段名。
		finfo.name = tag
		return finfo, nil
	}

	if tag == "" {
		// 如果标签的名称部分完全为空，则在可行时
		// 从底层结构体的 XMLName 获取默认值，
		// 否则使用字段名。
		if xmlname := lookupXMLName(f.Type); xmlname != nil {
			finfo.xmlns, finfo.name = xmlname.xmlns, xmlname.name
		} else {
			finfo.name = f.Name
		}
		return finfo, nil
	}

	// 准备字段名和父级。
	parents := strings.Split(tag, ">")
	if parents[0] == "" {
		parents[0] = f.Name
	}
	if parents[len(parents)-1] == "" {
		return nil, fmt.Errorf("xml: trailing '>' in field %s of type %s", f.Name, typ)
	}
	finfo.name = parents[len(parents)-1]
	if len(parents) > 1 {
		if (finfo.flags & fElement) == 0 {
			return nil, fmt.Errorf("xml: %s chain not valid with %s flag", tag, strings.Join(tokens[1:], ","))
		}
		finfo.parents = parents[:len(parents)-1]
	}

	// 如果字段类型有 XMLName 字段，则名称必须匹配，
	// 这样封送和反封送的行为都
	// 直接且无歧义。
	if finfo.flags&fElement != 0 {
		ftyp := f.Type
		xmlname := lookupXMLName(ftyp)
		if xmlname != nil && xmlname.name != finfo.name {
			return nil, fmt.Errorf("xml: name %q in tag of %s.%s conflicts with name %q in %s.XMLName",
				finfo.name, typ, f.Name, xmlname.name, ftyp)
		}
	}
	return finfo, nil
}

// lookupXMLName 返回 typ 的 XMLName 字段（如果存在且具有
// 有效的 xml 字段标签）对应的 fieldInfo，否则
// 返回 nil。
func lookupXMLName(typ reflect.Type) (xmlname *fieldInfo) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil
	}
	for i, n := 0, typ.NumField(); i < n; i++ {
		f := typ.Field(i)
		if f.Name != xmlName {
			continue
		}
		finfo, err := structFieldInfo(typ, &f)
		if err == nil && finfo.name != "" {
			return finfo
		}
		// 也把错误视为不存在的字段标签，
		// 并让 getTypeInfo 自身报告该错误。
		break
	}
	return nil
}

// 如果没有冲突，addFieldInfo 会将 finfo 添加到 tinfo.fields；
// 或者如果冲突来自比 finfo 更深层嵌入结构中获取的先前字段，
// 也会添加 finfo。在后一种情况下，冲突的条目会被丢弃。
// 当某个字段的路径（父级 + 名称）本身是另一条路径的前缀，
// 或者两条路径完全匹配时，就发生冲突。
// 字段路径共享一个共同的、较短的前缀是允许的。
func addFieldInfo(typ reflect.Type, tinfo *typeInfo, newf *fieldInfo) error {
	var conflicts []int
Loop:
	// 首先，找出所有冲突。大多数能正常工作的代码都不会有冲突。
	for i := range tinfo.fields {
		oldf := &tinfo.fields[i]
		if oldf.flags&fMode != newf.flags&fMode {
			continue
		}
		if oldf.xmlns != "" && newf.xmlns != "" && oldf.xmlns != newf.xmlns {
			continue
		}
		minl := min(len(newf.parents), len(oldf.parents))
		for p := 0; p < minl; p++ {
			if oldf.parents[p] != newf.parents[p] {
				continue Loop
			}
		}
		if len(oldf.parents) > len(newf.parents) {
			if oldf.parents[len(newf.parents)] == newf.name {
				conflicts = append(conflicts, i)
			}
		} else if len(oldf.parents) < len(newf.parents) {
			if newf.parents[len(oldf.parents)] == oldf.name {
				conflicts = append(conflicts, i)
			}
		} else {
			if newf.name == oldf.name && newf.xmlns == oldf.xmlns {
				conflicts = append(conflicts, i)
			}
		}
	}
	// 没有冲突，则添加新字段并返回。
	if conflicts == nil {
		tinfo.fields = append(tinfo.fields, *newf)
		return nil
	}

	// 如果任何冲突更浅，则忽略新字段。
	// 这与嵌入时 Go 的字段解析规则一致。
	for _, i := range conflicts {
		if len(tinfo.fields[i].idx) < len(newf.idx) {
			return nil
		}
	}

	// 否则，如果它们中有任何一个处于相同深度层级，则视为错误。
	for _, i := range conflicts {
		oldf := &tinfo.fields[i]
		if len(oldf.idx) == len(newf.idx) {
			f1 := typ.FieldByIndex(oldf.idx)
			f2 := typ.FieldByIndex(newf.idx)
			return &TagPathError{typ, f1.Name, f1.Tag.Get("xml"), f2.Name, f2.Tag.Get("xml")}
		}
	}

	// 否则，新字段更浅，因此优先级更高，
	// 于是从 tinfo 中丢弃冲突字段，并追加新字段。
	for c := len(conflicts) - 1; c >= 0; c-- {
		i := conflicts[c]
		copy(tinfo.fields[i:], tinfo.fields[i+1:])
		tinfo.fields = tinfo.fields[:len(tinfo.fields)-1]
	}
	tinfo.fields = append(tinfo.fields, *newf)
	return nil
}

// TagPathError 表示反封送过程中因使用了路径冲突的
// 字段标签而导致的错误。
type TagPathError struct {
	Struct       reflect.Type
	Field1, Tag1 string
	Field2, Tag2 string
}

func (e *TagPathError) Error() string {
	return fmt.Sprintf("%s field %q with tag %q conflicts with field %q with tag %q", e.Struct, e.Field1, e.Tag1, e.Field2, e.Tag2)
}

const (
	initNilPointers     = true
	dontInitNilPointers = false
)

// value 返回 v 中与 finfo 对应的字段值。
// 它等价于 v.FieldByIndex(finfo.idx)，但当传入
// initNilPointers 时，它会按需初始化并解引用指针。
// 当传入 dontInitNilPointers 且遇到 nil 指针时，该函数
// 返回一个零值 reflect.Value。
func (finfo *fieldInfo) value(v reflect.Value, shouldInitNilPointers bool) reflect.Value {
	for i, x := range finfo.idx {
		if i > 0 {
			t := v.Type()
			if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
				if v.IsNil() {
					if !shouldInitNilPointers {
						return reflect.Value{}
					}
					v.Set(reflect.New(v.Type().Elem()))
				}
				v = v.Elem()
			}
		}
		v = v.Field(x)
	}
	return v
}
