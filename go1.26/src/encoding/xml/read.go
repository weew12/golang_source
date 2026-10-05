// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)

// BUG(rsc)：XML 元素与数据结构之间的映射本身存在缺陷：
// XML 元素是有序的匿名值集合，
// 而数据结构是无序的具名值集合。
// 对于更适合数据结构的文本表示，参见 [encoding/json]。

// Unmarshal 解析 XML 编码的数据，并将结果存储到 v 指向的值中，
// v 必须是任意的结构体、切片或字符串。格式良好但不适合放入 v 的
// 数据会被丢弃。
//
// 由于 Unmarshal 使用 reflect 包，它只能赋值给导出（首字母大写）的
// 字段。Unmarshal 使用区分大小写的比较，将 XML 元素名称与标签值
// 和结构体字段名进行匹配。
//
// Unmarshal 使用以下规则将 XML 元素映射到结构体。
// 在这些规则中，字段的标签指的是结构体字段标签中键 'xml'
// 关联的值（见上方的示例）。
//
//   - 如果结构体有一个类型为 []byte 或 string、标签为
//     ",innerxml" 的字段，Unmarshal 会把嵌套在该元素内的原始 XML
//     累积到该字段中。其余规则仍然适用。
//
//   - 如果结构体有一个名为 XMLName、类型为 Name 的字段，
//     Unmarshal 会把元素名称记录到该字段中。
//
//   - 如果 XMLName 字段有一个形如 "name" 或 "namespace-URL name"
//     的关联标签，则 XML 元素必须具有给定的名称（以及可选的
//     命名空间），否则 Unmarshal 会返回错误。
//
//   - 如果 XML 元素有一个属性，其名称与某个结构体字段名匹配，
//     且该字段有关联标签包含 ",attr"，或者在形如 "name,attr" 的结构体
//     字段标签中显式给出名称，则 Unmarshal 会把属性值记录到该字段中。
//
//   - 如果 XML 元素有一个未被上一条规则处理的属性，并且结构体有一个
//     关联标签包含 ",any,attr" 的字段，则 Unmarshal 会把属性值记录到
//     第一个这样的字段中。
//
//   - 如果 XML 元素包含字符数据，该数据会被累积到第一个标签为
//     ",chardata" 的结构体字段中。该结构体字段的类型可以是 []byte 或
//     string。如果没有这样的字段，字符数据会被丢弃。
//
//   - 如果 XML 元素包含注释，它们会被累积到第一个标签为
//     ",comment" 的结构体字段中。该结构体字段的类型可以是 []byte 或
//     string。如果没有这样的字段，注释会被丢弃。
//
//   - 如果 XML 元素包含一个子元素，其名称与形如 "a" 或 "a>b>c" 的标签
//     前缀匹配，unmarshal 会深入 XML 结构查找具有给定名称的元素，
//     并将最内层的元素映射到该结构体字段。以 ">" 开头的标签等价于
//     以字段名后跟 ">" 开头的标签。
//
//   - 如果 XML 元素包含一个子元素，其名称与某个结构体字段的 XMLName
//     标签匹配，且该结构体字段没有按照上一条规则显式给出名称标签，
//     unmarshal 会把该子元素映射到该结构体字段。
//
//   - 如果 XML 元素包含一个子元素，其名称与某个没有任何模式标志
//     （",attr"、",chardata" 等）的字段匹配，Unmarshal 会把该子元素
//     映射到该结构体字段。
//
//   - 如果 XML 元素包含一个子元素，它未匹配以上任何规则，并且结构体
//     有一个标签为 ",any" 的字段，unmarshal 会把该子元素映射到该
//     结构体字段。
//
//   - 匿名结构体字段的处理方式如同其值的字段是外层结构体的一部分。
//
//   - 标签为 "-" 的结构体字段永远不会被反封送。
//
// 如果 Unmarshal 遇到实现了 Unmarshaler 接口的字段类型，Unmarshal 会调用
// 其 UnmarshalXML 方法从 XML 元素生成值。否则，如果该值实现了
// [encoding.TextUnmarshaler]，Unmarshal 会调用该值的 UnmarshalText 方法。
//
// Unmarshal 通过将该元素字符数据的拼接结果保存到 string 或 []byte，
// 从而将 XML 元素映射到 string 或 []byte。保存的 []byte 永远不会为 nil。
//
// Unmarshal 通过将值保存到 string 或切片中，从而将属性值映射到 string 或 []byte。
//
// Unmarshal 通过将属性（包括其名称）保存到 [Attr]，从而将属性值映射到 [Attr]。
//
// Unmarshal 通过扩展切片的长度并将元素或属性映射到新创建的值，
// 从而将 XML 元素或属性值映射到切片。
//
// Unmarshal 通过将值设为字符串所表示的布尔值，从而将 XML 元素或
// 属性值映射到 bool。空白会被去除并忽略。
//
// Unmarshal 通过将字段设为按十进制解释字符串值的结果，从而将 XML 元素或
// 属性值映射到整数或浮点字段。不检查溢出。空白会被去除并忽略。
//
// Unmarshal 通过记录元素名称，从而将 XML 元素映射到 Name。
//
// Unmarshal 通过将指针设为新分配的值、然后将元素映射到该值，
// 从而将 XML 元素映射到指针。
//
// 缺失的元素或空属性值会被反封送为零值。
// 如果该字段是切片，则会将零值追加到该字段。否则，该字段
// 会被设为其零值。
func Unmarshal(data []byte, v any) error {
	return NewDecoder(bytes.NewReader(data)).Decode(v)
}

// Decode 的工作方式与 [Unmarshal] 类似，不同之处在于它读取解码器
// 流以找到起始元素。
func (d *Decoder) Decode(v any) error {
	return d.DecodeElement(v, nil)
}

// DecodeElement 的工作方式与 [Unmarshal] 类似，不同之处在于它接收
// 一个指向起始 XML 元素的指针，用于解码到 v。
// 当客户端自己读取了一些原始 XML token，但又希望将某些元素
// 交由 [Unmarshal] 处理时，它会很有用。
func (d *Decoder) DecodeElement(v any, start *StartElement) error {
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Pointer {
		return errors.New("non-pointer passed to Unmarshal")
	}

	if val.IsNil() {
		return errors.New("nil pointer passed to Unmarshal")
	}
	return d.unmarshal(val.Elem(), start, 0)
}

// UnmarshalError 表示反封送过程中的一个错误。
type UnmarshalError string

func (e UnmarshalError) Error() string { return string(e) }

// Unmarshaler 是那些能够反封送自身 XML 元素描述的对象所实现的
// 接口。
//
// UnmarshalXML 解码以给定起始元素开始的单个 XML 元素。
// 如果它返回错误，对 Unmarshal 的外层调用会停止并
// 返回该错误。
// UnmarshalXML 必须恰好消费一个 XML 元素。
// 一种常见的实现策略是使用 d.DecodeElement 将其反封送到一个
// 布局与预期 XML 匹配的独立值中，然后
// 将该值中的数据复制到接收者中。
// 另一种常见策略是使用 d.Token 逐个 token 地处理
// XML 对象。
// UnmarshalXML 不得使用 d.RawToken。
type Unmarshaler interface {
	UnmarshalXML(d *Decoder, start StartElement) error
}

// UnmarshalerAttr 是那些能够反封送自身 XML 属性描述的对象所实现的
// 接口。
//
// UnmarshalXMLAttr 解码单个 XML 属性。
// 如果它返回错误，对 [Unmarshal] 的外层调用会停止并
// 返回该错误。
// UnmarshalXMLAttr 仅用于字段标签中带有
// "attr" 选项的结构体字段。
type UnmarshalerAttr interface {
	UnmarshalXMLAttr(attr Attr) error
}

// receiverType 返回在诸如 "%s.MethodName" 的表达式中使用的接收者类型。
func receiverType(val any) string {
	t := reflect.TypeOf(val)
	if t.Name() != "" {
		return t.String()
	}
	return "(" + t.String() + ")"
}

// unmarshalInterface 将单个 XML 元素反封送到 val。
// start 是该元素的起始标签。
func (d *Decoder) unmarshalInterface(val Unmarshaler, start *StartElement) error {
	// 记录解码器必须在与 start 对应的结束标签处停止。
	d.pushEOF()

	d.unmarshalDepth++
	err := val.UnmarshalXML(d, *start)
	d.unmarshalDepth--
	if err != nil {
		d.popEOF()
		return err
	}

	if !d.popEOF() {
		return fmt.Errorf("xml: %s.UnmarshalXML did not consume entire <%s> element", receiverType(val), start.Name.Local)
	}

	return nil
}

// unmarshalTextInterface 将单个 XML 元素反封送到 val。
// 元素中包含的字符数据（但不含其子元素）会被传递给文本
// 反封送器。
func (d *Decoder) unmarshalTextInterface(val encoding.TextUnmarshaler) error {
	var buf []byte
	depth := 1
	for depth > 0 {
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case CharData:
			if depth == 1 {
				buf = append(buf, t...)
			}
		case StartElement:
			depth++
		case EndElement:
			depth--
		}
	}
	return val.UnmarshalText(buf)
}

// unmarshalAttr 将单个 XML 属性反封送到 val。
func (d *Decoder) unmarshalAttr(val reflect.Value, attr Attr) error {
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			val.Set(reflect.New(val.Type().Elem()))
		}
		val = val.Elem()
	}
	if val.CanInterface() {
		// 这是一个接收者为非指针的反封送器，
		// 因此很可能不正确，但我们照做不误。
		if unmarshaler, ok := reflect.TypeAssert[UnmarshalerAttr](val); ok {
			return unmarshaler.UnmarshalXMLAttr(attr)
		}
	}
	if val.CanAddr() {
		pv := val.Addr()
		if pv.CanInterface() {
			if unmarshaler, ok := reflect.TypeAssert[UnmarshalerAttr](pv); ok {
				return unmarshaler.UnmarshalXMLAttr(attr)
			}
		}
	}

	// 不是 UnmarshalerAttr；尝试 encoding.TextUnmarshaler。
	if val.CanInterface() {
		// 这是一个接收者为非指针的反封送器，
		// 因此很可能不正确，但我们照做不误。
		if textUnmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](val); ok {
			return textUnmarshaler.UnmarshalText([]byte(attr.Value))
		}
	}
	if val.CanAddr() {
		pv := val.Addr()
		if pv.CanInterface() {
			if textUnmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](pv); ok {
				return textUnmarshaler.UnmarshalText([]byte(attr.Value))
			}
		}
	}

	if val.Kind() == reflect.Slice && val.Type().Elem().Kind() != reflect.Uint8 {
		// 元素值的切片。
		// 扩展切片。
		n := val.Len()
		val.Grow(1)
		val.SetLen(n + 1)

		// 递归将元素读入切片。
		if err := d.unmarshalAttr(val.Index(n), attr); err != nil {
			val.SetLen(n)
			return err
		}
		return nil
	}

	if val.Type() == attrType {
		val.Set(reflect.ValueOf(attr))
		return nil
	}

	return copyValue(val, []byte(attr.Value))
}

var attrType = reflect.TypeFor[Attr]()

const (
	maxUnmarshalDepth     = 10000
	maxUnmarshalDepthWasm = 5000 // go.dev/issue/56498
)

var errUnmarshalDepth = errors.New("exceeded max depth")

// 将单个 XML 元素反封送到 val。
func (d *Decoder) unmarshal(val reflect.Value, start *StartElement, depth int) error {
	if depth >= maxUnmarshalDepth || runtime.GOARCH == "wasm" && depth >= maxUnmarshalDepthWasm {
		return errUnmarshalDepth
	}
	// 如需要，查找起始元素。
	if start == nil {
		for {
			tok, err := d.Token()
			if err != nil {
				return err
			}
			if t, ok := tok.(StartElement); ok {
				start = &t
				break
			}
		}
	}

	// 从接口加载值，但仅当结果将
	// 可被有效地寻址时。
	if val.Kind() == reflect.Interface && !val.IsNil() {
		e := val.Elem()
		if e.Kind() == reflect.Pointer && !e.IsNil() {
			val = e
		}
	}

	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			val.Set(reflect.New(val.Type().Elem()))
		}
		val = val.Elem()
	}

	if val.CanInterface() {
		// 这是一个接收者为非指针的反封送器，
		// 因此很可能不正确，但我们照做不误。
		if unmarshaler, ok := reflect.TypeAssert[Unmarshaler](val); ok {
			return d.unmarshalInterface(unmarshaler, start)
		}
	}

	if val.CanAddr() {
		pv := val.Addr()
		if pv.CanInterface() {
			if unmarshaler, ok := reflect.TypeAssert[Unmarshaler](pv); ok {
				return d.unmarshalInterface(unmarshaler, start)
			}
		}
	}

	if val.CanInterface() {
		if textUnmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](val); ok {
			return d.unmarshalTextInterface(textUnmarshaler)
		}
	}

	if val.CanAddr() {
		pv := val.Addr()
		if pv.CanInterface() {
			if textUnmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](pv); ok {
				return d.unmarshalTextInterface(textUnmarshaler)
			}
		}
	}

	var (
		data         []byte
		saveData     reflect.Value
		comment      []byte
		saveComment  reflect.Value
		saveXML      reflect.Value
		saveXMLIndex int
		saveXMLData  []byte
		saveAny      reflect.Value
		sv           reflect.Value
		tinfo        *typeInfo
		err          error
	)

	switch v := val; v.Kind() {
	default:
		return errors.New("unknown type " + v.Type().String())

	case reflect.Interface:
		// TODO：目前先简单地忽略该字段。在不久的
		//       将来，我们可能会选择在其上（若非 nil）反封送
		//       起始元素。
		return d.Skip()

	case reflect.Slice:
		typ := v.Type()
		if typ.Elem().Kind() == reflect.Uint8 {
			// []byte
			saveData = v
			break
		}

		// 元素值的切片。
		// 扩展切片。
		n := v.Len()
		v.Grow(1)
		v.SetLen(n + 1)

		// 递归将元素读入切片。
		if err := d.unmarshal(v.Index(n), start, depth+1); err != nil {
			v.SetLen(n)
			return err
		}
		return nil

	case reflect.Bool, reflect.Float32, reflect.Float64, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.String:
		saveData = v

	case reflect.Struct:
		typ := v.Type()
		if typ == nameType {
			v.Set(reflect.ValueOf(start.Name))
			break
		}

		sv = v
		tinfo, err = getTypeInfo(typ)
		if err != nil {
			return err
		}

		// 校验并赋值元素名称。
		if tinfo.xmlname != nil {
			finfo := tinfo.xmlname
			if finfo.name != "" && finfo.name != start.Name.Local {
				return UnmarshalError("expected element type <" + finfo.name + "> but have <" + start.Name.Local + ">")
			}
			if finfo.xmlns != "" && finfo.xmlns != start.Name.Space {
				e := "expected element <" + finfo.name + "> in name space " + finfo.xmlns + " but have "
				if start.Name.Space == "" {
					e += "no name space"
				} else {
					e += start.Name.Space
				}
				return UnmarshalError(e)
			}
			fv := finfo.value(sv, initNilPointers)
			if _, ok := reflect.TypeAssert[Name](fv); ok {
				fv.Set(reflect.ValueOf(start.Name))
			}
		}

		// 赋值属性。
		for _, a := range start.Attr {
			handled := false
			any := -1
			for i := range tinfo.fields {
				finfo := &tinfo.fields[i]
				switch finfo.flags & fMode {
				case fAttr:
					strv := finfo.value(sv, initNilPointers)
					if a.Name.Local == finfo.name && (finfo.xmlns == "" || finfo.xmlns == a.Name.Space) {
						if err := d.unmarshalAttr(strv, a); err != nil {
							return err
						}
						handled = true
					}

				case fAny | fAttr:
					if any == -1 {
						any = i
					}
				}
			}
			if !handled && any >= 0 {
				finfo := &tinfo.fields[any]
				strv := finfo.value(sv, initNilPointers)
				if err := d.unmarshalAttr(strv, a); err != nil {
					return err
				}
			}
		}

		// 确定是否需要保存字符数据或注释。
		for i := range tinfo.fields {
			finfo := &tinfo.fields[i]
			switch finfo.flags & fMode {
			case fCDATA, fCharData:
				if !saveData.IsValid() {
					saveData = finfo.value(sv, initNilPointers)
				}

			case fComment:
				if !saveComment.IsValid() {
					saveComment = finfo.value(sv, initNilPointers)
				}

			case fAny, fAny | fElement:
				if !saveAny.IsValid() {
					saveAny = finfo.value(sv, initNilPointers)
				}

			case fInnerXML:
				if !saveXML.IsValid() {
					saveXML = finfo.value(sv, initNilPointers)
					if d.saved == nil {
						saveXMLIndex = 0
						d.saved = new(bytes.Buffer)
					} else {
						saveXMLIndex = d.savedOffset()
					}
				}
			}
		}
	}

	// 查找结束元素。
	// 同时处理子元素。
Loop:
	for {
		var savedOffset int
		if saveXML.IsValid() {
			savedOffset = d.savedOffset()
		}
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case StartElement:
			consumed := false
			if sv.IsValid() {
				// unmarshalPath 可能会调用 unmarshal，因此我们需要将 depth 传递下去，
				// 以便继续强制执行最大递归限制。
				consumed, err = d.unmarshalPath(tinfo, sv, nil, &t, depth)
				if err != nil {
					return err
				}
				if !consumed && saveAny.IsValid() {
					consumed = true
					if err := d.unmarshal(saveAny, &t, depth+1); err != nil {
						return err
					}
				}
			}
			if !consumed {
				if err := d.Skip(); err != nil {
					return err
				}
			}

		case EndElement:
			if saveXML.IsValid() {
				saveXMLData = d.saved.Bytes()[saveXMLIndex:savedOffset]
				if saveXMLIndex == 0 {
					d.saved = nil
				}
			}
			break Loop

		case CharData:
			if saveData.IsValid() {
				data = append(data, t...)
			}

		case Comment:
			if saveComment.IsValid() {
				comment = append(comment, t...)
			}
		}
	}

	if saveData.IsValid() && saveData.CanInterface() {
		if textUnmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](saveData); ok {
			if err := textUnmarshaler.UnmarshalText(data); err != nil {
				return err
			}
			saveData = reflect.Value{}
		}
	}

	if saveData.IsValid() && saveData.CanAddr() {
		pv := saveData.Addr()
		if pv.CanInterface() {
			if textUnmarshaler, ok := reflect.TypeAssert[encoding.TextUnmarshaler](pv); ok {
				if err := textUnmarshaler.UnmarshalText(data); err != nil {
					return err
				}
				saveData = reflect.Value{}
			}
		}
	}

	if err := copyValue(saveData, data); err != nil {
		return err
	}

	switch t := saveComment; t.Kind() {
	case reflect.String:
		t.SetString(string(comment))
	case reflect.Slice:
		t.Set(reflect.ValueOf(comment))
	}

	switch t := saveXML; t.Kind() {
	case reflect.String:
		t.SetString(string(saveXMLData))
	case reflect.Slice:
		if t.Type().Elem().Kind() == reflect.Uint8 {
			t.Set(reflect.ValueOf(saveXMLData))
		}
	}

	return nil
}

func copyValue(dst reflect.Value, src []byte) (err error) {
	dst0 := dst

	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		dst = dst.Elem()
	}

	// 保存累积的数据。
	switch dst.Kind() {
	case reflect.Invalid:
		// 可能是一条注释。
	default:
		return errors.New("cannot unmarshal into " + dst0.Type().String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if len(src) == 0 {
			dst.SetInt(0)
			return nil
		}
		itmp, err := strconv.ParseInt(strings.TrimSpace(string(src)), 10, dst.Type().Bits())
		if err != nil {
			return err
		}
		dst.SetInt(itmp)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if len(src) == 0 {
			dst.SetUint(0)
			return nil
		}
		utmp, err := strconv.ParseUint(strings.TrimSpace(string(src)), 10, dst.Type().Bits())
		if err != nil {
			return err
		}
		dst.SetUint(utmp)
	case reflect.Float32, reflect.Float64:
		if len(src) == 0 {
			dst.SetFloat(0)
			return nil
		}
		ftmp, err := strconv.ParseFloat(strings.TrimSpace(string(src)), dst.Type().Bits())
		if err != nil {
			return err
		}
		dst.SetFloat(ftmp)
	case reflect.Bool:
		if len(src) == 0 {
			dst.SetBool(false)
			return nil
		}
		value, err := strconv.ParseBool(strings.TrimSpace(string(src)))
		if err != nil {
			return err
		}
		dst.SetBool(value)
	case reflect.String:
		dst.SetString(string(src))
	case reflect.Slice:
		if len(src) == 0 {
			// 非 nil 以标记其存在
			src = []byte{}
		}
		dst.SetBytes(src)
	}
	return nil
}

// unmarshalPath 沿 XML 结构向下查找所需的路径，
// 并对它们调用 unmarshal。
// consumed 结果表示是否已从 Decoder 消费 XML 元素直到 start 的匹配结束元素，
// 或者由于 start 对 sv 的字段而言不重要而仍然未被触及。
func (d *Decoder) unmarshalPath(tinfo *typeInfo, sv reflect.Value, parents []string, start *StartElement, depth int) (consumed bool, err error) {
	recurse := false
Loop:
	for i := range tinfo.fields {
		finfo := &tinfo.fields[i]
		if finfo.flags&fElement == 0 || len(finfo.parents) < len(parents) || finfo.xmlns != "" && finfo.xmlns != start.Name.Space {
			continue
		}
		for j := range parents {
			if parents[j] != finfo.parents[j] {
				continue Loop
			}
		}
		if len(finfo.parents) == len(parents) && finfo.name == start.Name.Local {
			// 完全匹配，反封送该字段。
			return true, d.unmarshal(finfo.value(sv, initNilPointers), start, depth+1)
		}
		if len(finfo.parents) > len(parents) && finfo.parents[len(parents)] == start.Name.Local {
			// 它是该字段的前缀。中断并递归，
			// 因为不允许一条字段路径本身
			// 成为另一条字段路径的前缀。
			recurse = true

			// 只要我们不尝试向它追加内容，
			// 就可以复用同一个切片。
			parents = finfo.parents[:len(parents)+1]
			break
		}
	}
	if !recurse {
		// 我们与这个元素无关。
		return false, nil
	}
	// 该元素与任何字段都不完全匹配，但一个
	// 或多个字段以到达该元素的路径作为父级
	// 前缀。进行递归并尝试匹配这些字段。
	for {
		var tok Token
		tok, err = d.Token()
		if err != nil {
			return true, err
		}
		switch t := tok.(type) {
		case StartElement:
			// unmarshalPath 的递归深度受结构体字段标签所指定的
			// 路径长度限制，因此这里不增加 depth。
			consumed2, err := d.unmarshalPath(tinfo, sv, parents, &t, depth)
			if err != nil {
				return true, err
			}
			if !consumed2 {
				if err := d.Skip(); err != nil {
					return true, err
				}
			}
		case EndElement:
			return true, nil
		}
	}
}

// Skip 读取 token，直到它消费了与最近已消费的起始元素相匹配的
// 结束元素，跳过嵌套结构。
// 如果它找到与起始元素匹配的结束元素，则返回 nil；
// 否则返回一个描述问题的错误。
func (d *Decoder) Skip() error {
	var depth int64
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok.(type) {
		case StartElement:
			depth++
		case EndElement:
			if depth == 0 {
				return nil
			}
			depth--
		}
	}
}
