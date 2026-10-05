// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonwire"
)

type isZeroer interface {
	IsZero() bool
}

var isZeroerType = reflect.TypeFor[isZeroer]()

type structFields struct {
	flattened       []structField // 按深度优先顺序排列
	byActualName    map[string]*structField
	byFoldedName    map[string][]*structField
	inlinedFallback *structField
}

// reindex 重新计算 index，以避免运行时进行边界检查。
//
// 在 [makeStructFields] 中构造每个 [structField] 期间，
// index 字段以 0 为起始索引。然而，在它返回之前，
// 第 0 个字段被存储到 index0 中，而 index 存储剩余部分。
func (sf *structFields) reindex() {
	reindex := func(f *structField) {
		f.index0 = f.index[0]
		f.index = f.index[1:]
		if len(f.index) == 0 {
			f.index = nil // 避免固定底层切片
		}
	}
	for i := range sf.flattened {
		reindex(&sf.flattened[i])
	}
	if sf.inlinedFallback != nil {
		reindex(sf.inlinedFallback)
	}
}

// lookupByFoldedName 通过大小写不敏感匹配查找 name，
// 该匹配还会忽略短横线和下划线的存在。
func (fs *structFields) lookupByFoldedName(name []byte) []*structField {
	return fs.byFoldedName[string(foldName(name))]
}

type structField struct {
	id      int   // 广度优先排序中的唯一数字 ID
	index0  int   // 根据 [reflect.Type.FieldByIndex] 在结构体中的第 0 个索引
	index   []int // 根据 [reflect.Type.FieldByIndex] 的第 1 个索引及剩余索引
	typ     reflect.Type
	fncs    *arshaler
	isZero  func(addressableValue) bool
	isEmpty func(addressableValue) bool
	fieldOptions
}

var errNoExportedFields = errors.New("Go struct has no exported fields")

func makeStructFields(root reflect.Type) (fs structFields, serr *SemanticError) {
	orErrorf := func(serr *SemanticError, t reflect.Type, f string, a ...any) *SemanticError {
		return cmp.Or(serr, &SemanticError{GoType: t, Err: fmt.Errorf(f, a...)})
	}

	// 为广度优先搜索设置一个队列。
	var queueIndex int
	type queueEntry struct {
		typ           reflect.Type
		index         []int
		visitChildren bool // 是否递归访问此结构体中的内联字段
	}
	queue := []queueEntry{{root, nil, true}}
	seen := map[reflect.Type]bool{root: true}

	// 对所有可达字段执行广度优先搜索。
	// 这确保了 len(f.index) 将单调递增。
	var allFields, inlinedFallbacks []structField
	for queueIndex < len(queue) {
		qe := queue[queueIndex]
		queueIndex++

		t := qe.typ
		inlinedFallbackIndex := -1         // 当前结构体中最后一个内联回退字段的索引
		namesIndex := make(map[string]int) // 当前结构体中具有给定 JSON 对象名的每个字段的索引
		var hasAnyJSONTag bool             // 是否有任意 Go 结构体字段带有 `json` 标签
		var hasAnyJSONField bool           // 当前结构体中是否存在任意可 JSON 序列化的字段
		for i := range t.NumField() {
			sf := t.Field(i)
			_, hasTag := sf.Tag.Lookup("json")
			hasAnyJSONTag = hasAnyJSONTag || hasTag
			options, ignored, err := parseFieldOptions(sf)
			if err != nil {
				serr = cmp.Or(serr, &SemanticError{GoType: t, Err: err})
			}
			if ignored {
				continue
			}
			hasAnyJSONField = true
			f := structField{
				// 分配一个新的切片（len=N+1）以同时保存
				// 父索引（len=N）和当前索引（len=1）。
				// 这样做是为了避免覆写父索引的内存。
				index:        append(append(make([]int, 0, len(qe.index)+1), qe.index...), i),
				typ:          sf.Type,
				fieldOptions: options,
			}
			if sf.Anonymous && !f.hasName {
				if indirectType(f.typ).Kind() != reflect.Struct {
					serr = orErrorf(serr, t, "embedded Go struct field %s of non-struct type must be explicitly given a JSON name", sf.Name)
				} else {
					f.inline = true // 由不使用显式名称的 Go 嵌入所隐含
				}
			}
			if f.inline || f.unknown {
				// 处理一个内联字段，它与
				// 零个或多个 JSON 对象成员之间进行序列化。

				switch f.fieldOptions {
				case fieldOptions{name: f.name, quotedName: f.quotedName, inline: true}:
				case fieldOptions{name: f.name, quotedName: f.quotedName, unknown: true}:
				case fieldOptions{name: f.name, quotedName: f.quotedName, inline: true, unknown: true}:
					serr = orErrorf(serr, t, "Go struct field %s cannot have both `inline` and `unknown` specified", sf.Name)
					f.inline = false // 让 `unknown` 优先
				default:
					serr = orErrorf(serr, t, "Go struct field %s cannot have any options other than `inline` or `unknown` specified", sf.Name)
					if f.hasName {
						continue // 无效的内联字段；视为被忽略
					}
					f.fieldOptions = fieldOptions{name: f.name, quotedName: f.quotedName, inline: f.inline, unknown: f.unknown}
					if f.inline && f.unknown {
						f.inline = false // 让 `unknown` 优先
					}
				}

				// 拒绝任何带有自定义序列化的类型，
				// 否则将无法知道要内联哪些子字段。
				tf := indirectType(f.typ)
				if implementsAny(tf, allMethodTypes...) && tf != jsontextValueType {
					serr = orErrorf(serr, t, "inlined Go struct field %s of type %s must not implement marshal or unmarshal methods", sf.Name, tf)
				}

				// 处理一个内联字段，它与
				// 由 Go 结构体支持的有限数量的 JSON 对象成员之间进行序列化。
				if tf.Kind() == reflect.Struct {
					if f.unknown {
						serr = orErrorf(serr, t, "inlined Go struct field %s of type %s with `unknown` tag must be a Go map of string key or a jsontext.Value", sf.Name, tf)
						continue // 无效的内联字段；视为被忽略
					}
					if qe.visitChildren {
						queue = append(queue, queueEntry{tf, f.index, !seen[tf]})
					}
					seen[tf] = true
					continue
				} else if !sf.IsExported() {
					serr = orErrorf(serr, t, "inlined Go struct field %s is not exported", sf.Name)
					continue // 无效的内联字段；视为被忽略
				}

				// 处理一个内联字段，它与由 Go map 或 jsontext.Value 支持的
				// 任意数量的 JSON 对象成员之间进行序列化。
				switch {
				case tf == jsontextValueType:
					f.fncs = nil // 在 arshal_inlined.go 中特殊处理
				case tf.Kind() == reflect.Map && tf.Key().Kind() == reflect.String:
					if implementsAny(tf.Key(), allMethodTypes...) {
						serr = orErrorf(serr, t, "inlined map field %s of type %s must have a string key that does not implement marshal or unmarshal methods", sf.Name, tf)
						continue // 无效的内联字段；视为被忽略
					}
					f.fncs = lookupArshaler(tf.Elem())
				default:
					serr = orErrorf(serr, t, "inlined Go struct field %s of type %s must be a Go struct, Go map of string key, or jsontext.Value", sf.Name, tf)
					continue // 无效的内联字段；视为被忽略
				}

				// 拒绝同一结构体中的多个内联回退字段。
				if inlinedFallbackIndex >= 0 {
					serr = orErrorf(serr, t, "inlined Go struct fields %s and %s cannot both be a Go map or jsontext.Value", t.Field(inlinedFallbackIndex).Name, sf.Name)
					// 仍然将 f 追加到 inlinedFallbacks，因为在返回之前
					// 仍会检查主导的内联回退字段。
				}
				inlinedFallbackIndex = i

				inlinedFallbacks = append(inlinedFallbacks, f)
			} else {
				// 处理与单个 JSON 对象成员之间进行序列化的
				// 普通 Go 结构体字段。

				// 未导出字段无法被序列化，但结构体类型的
				// 嵌入字段除外，因为它们可能提升自身的导出字段。
				if !sf.IsExported() {
					tf := indirectType(f.typ)
					if !(sf.Anonymous && tf.Kind() == reflect.Struct) {
						serr = orErrorf(serr, t, "Go struct field %s is not exported", sf.Name)
						continue
					}
					// 遗憾的是，未导出字段上的方法
					// 仍然无法被调用。
					if implementsAny(tf, allMethodTypes...) ||
						(f.omitzero && implementsAny(tf, isZeroerType)) {
						serr = orErrorf(serr, t, "Go struct field %s is not exported for method calls", sf.Name)
						continue
					}
				}

				// 提供一个使用类型 IsZero 方法的函数。
				switch {
				case sf.Type.Kind() == reflect.Interface && sf.Type.Implements(isZeroerType):
					f.isZero = func(va addressableValue) bool {
						// 避免在 nil 接口或
						// 含 nil 指针的非 nil 接口上调用 IsZero 导致 panic。
						return va.IsNil() || (va.Elem().Kind() == reflect.Pointer && va.Elem().IsNil()) || va.Interface().(isZeroer).IsZero()
					}
				case sf.Type.Kind() == reflect.Pointer && sf.Type.Implements(isZeroerType):
					f.isZero = func(va addressableValue) bool {
						// 避免在 nil 指针上调用 IsZero 导致 panic。
						return va.IsNil() || va.Interface().(isZeroer).IsZero()
					}
				case sf.Type.Implements(isZeroerType):
					f.isZero = func(va addressableValue) bool { return va.Interface().(isZeroer).IsZero() }
				case reflect.PointerTo(sf.Type).Implements(isZeroerType):
					f.isZero = func(va addressableValue) bool { return va.Addr().Interface().(isZeroer).IsZero() }
				}

				// 提供一个函数，用于判断该值是否会
				// 序列化为空的 JSON 值。
				switch sf.Type.Kind() {
				case reflect.String, reflect.Map, reflect.Array, reflect.Slice:
					f.isEmpty = func(va addressableValue) bool { return va.Len() == 0 }
				case reflect.Pointer, reflect.Interface:
					f.isEmpty = func(va addressableValue) bool { return va.IsNil() }
				}

				// 拒绝同一结构体中具有相同名称的多个字段。
				if j, ok := namesIndex[f.name]; ok {
					serr = orErrorf(serr, t, "Go struct fields %s and %s conflict over JSON object name %q", t.Field(j).Name, sf.Name, f.name)
					// 仍然将 f 追加到 allFields，因为在返回之前
					// 仍会检查主导字段。
				}
				namesIndex[f.name] = i

				f.id = len(allFields)
				f.fncs = lookupArshaler(sf.Type)
				allFields = append(allFields, f)
			}
		}

		// NOTE: json 包的新用户偶尔会对
		// 未导出字段被忽略感到惊讶。这是必然的，因为我们
		// 无法在不使用 unsafe 的情况下用 Go 反射直接内省此类字段。
		//
		// 为减少此处的困扰，拒绝序列化任何满足以下条件的 Go 结构体：
		// 没有可 JSON 序列化的字段、至少有一个 Go 结构体字段，
		// 且不存在任何 `json` 标签。例如，
		// errors.New 返回的错误将无法序列化。
		isEmptyStruct := t.NumField() == 0
		if !isEmptyStruct && !hasAnyJSONTag && !hasAnyJSONField {
			serr = cmp.Or(serr, &SemanticError{GoType: t, Err: errNoExportedFields})
		}
	}

	// 按精确名称对字段排序（先按深度打破平局，
	// 再按是否存在显式提供的 JSON 名称打破平局）。
	// 从每组同名字段中选出主导字段。
	// 如果多个字段具有相同名称，则主导字段
	// 是在最浅深度处单独存在的那个，
	// 或者是唯一被标注 JSON 名称的那个。
	// 否则，该组不存在主导字段。
	flattened := allFields[:0]
	slices.SortStableFunc(allFields, func(x, y structField) int {
		return cmp.Or(
			strings.Compare(x.name, y.name),
			cmp.Compare(len(x.index), len(y.index)),
			boolsCompare(!x.hasName, !y.hasName))
	})
	for len(allFields) > 0 {
		n := 1 // 具有相同精确名称的字段数量
		for n < len(allFields) && allFields[n-1].name == allFields[n].name {
			n++
		}
		if n == 1 || len(allFields[0].index) != len(allFields[1].index) || allFields[0].hasName != allFields[1].hasName {
			flattened = append(flattened, allFields[0]) // 仅当存在主导字段时才保留该字段
		}
		allFields = allFields[n:]
	}

	// 按广度优先顺序对字段排序，
	// 以便用尽可能小的值重新编号 ID。
	// 这优化了 uintSet 的使用，使其能放入 64 位位集中。
	slices.SortFunc(flattened, func(x, y structField) int {
		return cmp.Compare(x.id, y.id)
	})
	for i := range flattened {
		flattened[i].id = i
	}

	// 按深度优先顺序对字段排序，
	// 这是字段被序列化的典型顺序。
	slices.SortFunc(flattened, func(x, y structField) int {
		return slices.Compare(x.index, y.index)
	})

	// 计算 byActualName 映射中的字段映射。
	// 预先折叠所有名称，以便快速查找折叠后的名称。
	fs = structFields{
		flattened:    flattened,
		byActualName: make(map[string]*structField, len(flattened)),
		byFoldedName: make(map[string][]*structField, len(flattened)),
	}
	for i, f := range fs.flattened {
		foldedName := string(foldName([]byte(f.name)))
		fs.byActualName[f.name] = &fs.flattened[i]
		fs.byFoldedName[foldedName] = append(fs.byFoldedName[foldedName], &fs.flattened[i])
	}
	for foldedName, fields := range fs.byFoldedName {
		if len(fields) > 1 {
			// 冲突的 ignoreCase 名称的优先级顺序
			// 是按广度优先顺序，而非深度优先顺序。
			slices.SortFunc(fields, func(x, y *structField) int {
				return cmp.Compare(x.id, y.id)
			})
			fs.byFoldedName[foldedName] = fields
		}
	}
	if n := len(inlinedFallbacks); n == 1 || (n > 1 && len(inlinedFallbacks[0].index) != len(inlinedFallbacks[1].index)) {
		fs.inlinedFallback = &inlinedFallbacks[0] // 主导的内联回退字段
	}
	fs.reindex()
	return fs, serr
}

// indirectType 解包一层指针间接引用，
// 类似于 Go 仅允许嵌入 T 或 *T，
// 而不允许 **T 或 P（即命名指针）。
func indirectType(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer && t.Name() == "" {
		t = t.Elem()
	}
	return t
}

// matchFoldedName 根据选项匹配大小写不敏感的名称。
// 它假定 foldName(f.name) == foldName(name)。
//
// 如果指定了 `case:ignore` 标签选项
// 或指定了 MatchCaseInsensitiveNames 调用选项
// （且未指定 `case:strict` 标签选项），则使用大小写不敏感匹配。
// 在功能上，`case:ignore` 和 `case:strict` 标签选项优先。
//
// v1 的大小写不敏感定义基于 strings.EqualFold 运作，
// 会严格比较短横线和下划线，
// 而 v2 的定义会忽略短横线和下划线的存在。
// 因此，如果指定了 MatchCaseSensitiveDelimiter 调用选项，
// 则匹配进一步限制为使用 strings.EqualFold。
func (f *structField) matchFoldedName(name []byte, flags *jsonflags.Flags) bool {
	if f.casing == caseIgnore || (flags.Get(jsonflags.MatchCaseInsensitiveNames) && f.casing != caseStrict) {
		if !flags.Get(jsonflags.MatchCaseSensitiveDelimiter) || strings.EqualFold(string(name), f.name) {
			return true
		}
	}
	return false
}

const (
	caseIgnore = 1
	caseStrict = 2
)

type fieldOptions struct {
	name           string
	quotedName     string // 根据 RFC 8785 第 3.2.2.2 节引用后的名称。
	hasName        bool
	nameNeedEscape bool
	casing         int8 // 为 0、caseIgnore 或 caseStrict
	inline         bool
	unknown        bool
	omitzero       bool
	omitempty      bool
	string         bool
	format         string
}

// parseFieldOptions 将 Go 结构体字段中的 `json` 标签解析为
// 一组结构化的选项，用于配置诸如
// JSON 成员名称和其他特性等参数。
func parseFieldOptions(sf reflect.StructField) (out fieldOptions, ignored bool, err error) {
	tag, hasTag := sf.Tag.Lookup("json")
	tagOrig := tag

	// 检查此字段是否被显式忽略。
	if tag == "-" {
		return fieldOptions{}, true, nil
	}

	// 检查此字段是否未导出且未被嵌入，
	// Go 反射无法为序列化而修改此类字段。
	//
	// 未导出类型的嵌入字段仍然能够
	// 转发导出字段，这些字段可以进行 JSON 序列化。
	// 这在技术上处于 Go 语言所允许的边缘，
	// 但最新的决定是允许这样做。
	//
	// 参见 https://go.dev/issue/24153 和 https://go.dev/issue/32772。
	if !sf.IsExported() && !sf.Anonymous {
		// 在未导出字段上指定标签选项暗示用户错误。
		if hasTag {
			err = cmp.Or(err, fmt.Errorf("unexported Go struct field %s cannot have non-ignored `json:%q` tag", sf.Name, tag))
		}
		return fieldOptions{}, true, err
	}

	// 确定此 Go 字段的 JSON 成员名称。用户指定的名称
	// 可以作为标识符或单引号字符串提供。
	// 单引号字符串允许名称中包含任意字符。
	// 参见 https://go.dev/issue/2718 和 https://go.dev/issue/3546。
	out.name = sf.Name // 始终以大写字符开头
	if len(tag) > 0 && !strings.HasPrefix(tag, ",") {
		// 为了与 v1 更好地兼容，接受几乎任何未转义的名称。
		n := len(tag) - len(strings.TrimLeftFunc(tag, func(r rune) bool {
			return !strings.ContainsRune(",\\'\"`", r) // 保留逗号、反斜杠和引号
		}))
		name := tag[:n]

		// 如果下一个字符不是逗号，则该名称要么
		// 格式错误（如果 n > 0），要么是单引号名称。
		// 无论哪种情况，都调用 consumeTagOption 进一步处理。
		var err2 error
		if !strings.HasPrefix(tag[n:], ",") && len(name) != len(tag) {
			name, n, err2 = consumeTagOption(tag)
			if err2 != nil {
				err = cmp.Or(err, fmt.Errorf("Go struct field %s has malformed `json` tag: %v", sf.Name, err2))
			}
		}
		if !utf8.ValidString(name) {
			err = cmp.Or(err, fmt.Errorf("Go struct field %s has JSON object name %q with invalid UTF-8", sf.Name, name))
			name = string([]rune(name)) // 用 utf8.RuneError 替换无效的 UTF-8
		}
		if name == "-" && tag[0] == '-' {
			defer func() { // 延迟执行以便让其他错误优先
				err = cmp.Or(err, fmt.Errorf("Go struct field %s has JSON object name %q; either "+
					"use `json:\"-\"` to ignore the field or "+
					"use `json:\"'-'%s` to specify %q as the name", sf.Name, out.name, strings.TrimPrefix(strconv.Quote(tagOrig), `"-`), name))
			}()
		}
		if err2 == nil {
			out.hasName = true
			out.name = name
		}
		tag = tag[n:]
	}
	b, _ := jsonwire.AppendQuote(nil, out.name, &jsonflags.Flags{})
	out.quotedName = string(b)
	out.nameNeedEscape = jsonwire.NeedEscape(out.name)

	// 处理任何附加的标签选项（如果有）。
	var wasFormat bool
	seenOpts := make(map[string]bool)
	for len(tag) > 0 {
		// 消费逗号分隔符。
		if tag[0] != ',' {
			err = cmp.Or(err, fmt.Errorf("Go struct field %s has malformed `json` tag: invalid character %q before next option (expecting ',')", sf.Name, tag[0]))
		} else {
			tag = tag[len(","):]
			if len(tag) == 0 {
				err = cmp.Or(err, fmt.Errorf("Go struct field %s has malformed `json` tag: invalid trailing ',' character", sf.Name))
				break
			}
		}

		// 消费并处理标签选项。
		opt, n, err2 := consumeTagOption(tag)
		if err2 != nil {
			err = cmp.Or(err, fmt.Errorf("Go struct field %s has malformed `json` tag: %v", sf.Name, err2))
		}
		rawOpt := tag[:n]
		tag = tag[n:]
		switch {
		case wasFormat:
			err = cmp.Or(err, fmt.Errorf("Go struct field %s has `format` tag option that was not specified last", sf.Name))
		case strings.HasPrefix(rawOpt, "'") && strings.TrimFunc(opt, isLetterOrDigit) == "":
			err = cmp.Or(err, fmt.Errorf("Go struct field %s has unnecessarily quoted appearance of `%s` tag option; specify `%s` instead", sf.Name, rawOpt, opt))
		}
		switch opt {
		case "case":
			if !strings.HasPrefix(tag, ":") {
				err = cmp.Or(err, fmt.Errorf("Go struct field %s is missing value for `case` tag option; specify `case:ignore` or `case:strict` instead", sf.Name))
				break
			}
			tag = tag[len(":"):]
			opt, n, err2 := consumeTagOption(tag)
			if err2 != nil {
				err = cmp.Or(err, fmt.Errorf("Go struct field %s has malformed value for `case` tag option: %v", sf.Name, err2))
				break
			}
			rawOpt := tag[:n]
			tag = tag[n:]
			if strings.HasPrefix(rawOpt, "'") {
				err = cmp.Or(err, fmt.Errorf("Go struct field %s has unnecessarily quoted appearance of `case:%s` tag option; specify `case:%s` instead", sf.Name, rawOpt, opt))
			}
			switch opt {
			case "ignore":
				out.casing |= caseIgnore
			case "strict":
				out.casing |= caseStrict
			default:
				err = cmp.Or(err, fmt.Errorf("Go struct field %s has unknown `case:%s` tag value", sf.Name, rawOpt))
			}
		case "inline":
			out.inline = true
		case "unknown":
			out.unknown = true
		case "omitzero":
			out.omitzero = true
		case "omitempty":
			out.omitempty = true
		case "string":
			out.string = true
		case "format":
			if !strings.HasPrefix(tag, ":") {
				err = cmp.Or(err, fmt.Errorf("Go struct field %s is missing value for `format` tag option", sf.Name))
				break
			}
			tag = tag[len(":"):]
			opt, n, err2 := consumeTagOption(tag)
			if err2 != nil {
				err = cmp.Or(err, fmt.Errorf("Go struct field %s has malformed value for `format` tag option: %v", sf.Name, err2))
				break
			}
			tag = tag[n:]
			out.format = opt
			wasFormat = true
		default:
			// 拒绝与受支持选项相似的键。
			// 这可以捕获诸如 "omitEmpty" 或 "omit_empty" 之类的无效变体。
			normOpt := strings.ReplaceAll(strings.ToLower(opt), "_", "")
			switch normOpt {
			case "case", "inline", "unknown", "omitzero", "omitempty", "string", "format":
				err = cmp.Or(err, fmt.Errorf("Go struct field %s has invalid appearance of `%s` tag option; specify `%s` instead", sf.Name, opt, normOpt))
			}

			// NOTE: 其他所有内容都被忽略。这并不意味着
			// 插入任意标签选项是向前兼容的，因为
			// 此包的未来版本可能会理解该标签。
		}

		// 拒绝重复项。
		switch {
		case out.casing == caseIgnore|caseStrict:
			err = cmp.Or(err, fmt.Errorf("Go struct field %s cannot have both `case:ignore` and `case:strict` tag options", sf.Name))
		case seenOpts[opt]:
			err = cmp.Or(err, fmt.Errorf("Go struct field %s has duplicate appearance of `%s` tag option", sf.Name, rawOpt))
		}
		seenOpts[opt] = true
	}
	return out, false, err
}

// consumeTagOption 消费下一个选项，
// 它要么是 Go 标识符，要么是单引号字符串。
// 如果下一个选项无效，则返回 in 中直到下一个逗号为止的所有内容，
// 并报告错误。
func consumeTagOption(in string) (string, int, error) {
	// 为了与 v1 的旧版兼容性，假定选项以逗号分隔。
	i := strings.IndexByte(in, ',')
	if i < 0 {
		i = len(in)
	}

	switch r, _ := utf8.DecodeRuneInString(in); {
	// 选项作为 Go 标识符。
	case r == '_' || unicode.IsLetter(r):
		n := len(in) - len(strings.TrimLeftFunc(in, isLetterOrDigit))
		return in[:n], n, nil
	// 选项作为单引号字符串。
	case r == '\'':
		// 该语法与双引号 Go 字符串字面量几乎相同，
		// 但使用单引号作为终止符。采用自定义
		// 语法的原因是反引号和双引号都无法
		// 在结构体标签中原样使用。
		//
		// 将单引号字符串转换为双引号字符串，并依赖
		// strconv.Unquote 处理其余部分。
		var inEscape bool
		b := []byte{'"'}
		n := len(`'`)
		for len(in) > n {
			r, rn := utf8.DecodeRuneInString(in[n:])
			switch {
			case inEscape:
				if r == '\'' {
					b = b[:len(b)-1] // 移除转义字符：`\'` => `'`
				}
				inEscape = false
			case r == '\\':
				inEscape = true
			case r == '"':
				b = append(b, '\\') // 插入转义字符：`"` => `\"`
			case r == '\'':
				b = append(b, '"')
				n += len(`'`)
				out, err := strconv.Unquote(string(b))
				if err != nil {
					return in[:i], i, fmt.Errorf("invalid single-quoted string: %s", in[:n])
				}
				return out, n, nil
			}
			b = append(b, in[n:][:rn]...)
			n += rn
		}
		if n > 10 {
			n = 10 // 限制错误中打印的上下文数量
		}
		return in[:i], i, fmt.Errorf("single-quoted string not terminated: %s...", in[:n])
	case len(in) == 0:
		return in[:i], i, io.ErrUnexpectedEOF
	default:
		return in[:i], i, fmt.Errorf("invalid character %q at start of option (expecting Unicode letter or single quote)", r)
	}
}

func isLetterOrDigit(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// boolsCompare 比较 x 和 y，将 false 排在 true 之前。
func boolsCompare(x, y bool) int {
	switch {
	case !x && y:
		return -1
	default:
		return 0
	case x && !y:
		return +1
	}
}
