// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bufio"
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

const (
	// Header 是一个通用的 XML 头部，适合与 [Marshal] 的输出一起使用。
	// 它不会被自动添加到本包的任何输出中，
	// 此处只是作为一个便利提供。
	Header = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"
)

// Marshal 返回 v 的 XML 编码。
//
// Marshal 处理数组或切片的方式是封送其中的每个元素。
// Marshal 处理指针的方式是封送它所指向的值，或者如果
// 指针为 nil，则不写入任何内容。Marshal 处理接口值的方式是
// 封送其中包含的值，或者如果接口值为 nil，则不写入任何内容。
// Marshal 处理所有其他数据的方式是写入一个或多个包含该数据的 XML
// 元素。
//
// XML 元素的名称按优先顺序取自：
//   - 若数据是结构体，则取自 XMLName 字段上的标签
//   - 类型为 [Name] 的 XMLName 字段的值
//   - 用于获取数据的结构体字段的标签
//   - 用于获取数据的结构体字段的名称
//   - 被封送类型的名称
//
// 结构体的 XML 元素包含该结构体每个导出字段的封送元素，
// 但有以下例外：
//   - 上述的 XMLName 字段会被省略。
//   - 标签为 "-" 的字段会被省略。
//   - 标签为 "name,attr" 的字段在 XML 元素中成为具有
//     给定名称的属性。
//   - 标签为 ",attr" 的字段在 XML 元素中成为具有
//     字段名的属性。
//   - 标签为 ",chardata" 的字段被写为字符数据，
//     而不是 XML 元素。
//   - 标签为 ",cdata" 的字段被写为包裹在一个或多个
//     <![CDATA[ ... ]]> 标签中的字符数据，而不是 XML 元素。
//   - 标签为 ",innerxml" 的字段被原样写入，不经过
//     通常的封送过程。
//   - 标签为 ",comment" 的字段被写为 XML 注释，不经过
//     通常的封送过程。它内部不得包含
//     "--" 字符串。
//   - 标签中包含 "omitempty" 选项的字段，若字段值为空则
//     被省略。空值包括 false、0、任何
//     nil 指针或接口值，以及任何长度为零的数组、切片、map 或
//     字符串。
//   - 匿名结构体字段的处理方式如同其值的字段是外层结构体的一部分。
//   - 接口类型的匿名结构体字段被视为以该类型作为其名称，
//     而不是匿名。
//   - 实现了 [Marshaler] 的字段通过调用其 MarshalXML
//     方法来写入。
//   - 实现了 [encoding.TextMarshaler] 的字段通过将其 MarshalText 方法
//     的结果编码为文本来写入。
//
// 如果字段使用标签 "a>b>c"，则元素 c 将嵌套在
// 父元素 a 和 b 中。相邻出现且名称相同的父元素
// 字段会被包含在同一个 XML 元素中。
//
// 如果结构体字段的 XML 名称同时由字段标签和
// 结构体的 XMLName 字段定义，则名称必须匹配。
//
// 示例参见 [MarshalIndent]。
//
// 如果要求 Marshal 封送 channel、函数或 map，它会返回一个错误。
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := NewEncoder(&b)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Marshaler 是那些能够将自身封送为有效 XML 元素的对象所实现的
// 接口。
//
// MarshalXML 将接收者编码为零个或多个 XML 元素。
// 按照惯例，数组或切片通常被编码为一个元素序列，
// 每个条目对应一个元素。
// 使用 start 作为元素标签并非必须，但这样做
// 将使 [Unmarshal] 能够将 XML 元素匹配到正确的
// 结构体字段。
// 一种常见的实现策略是构造一个布局与所需 XML 相对应的独立
// 值，然后使用 e.EncodeElement 对它进行编码。
// 另一种常见策略是重复调用 e.EncodeToken
// 来逐个 token 地生成 XML 输出。
// 编码后的 token 序列必须构成零个或多个有效的
// XML 元素。
type Marshaler interface {
	MarshalXML(e *Encoder, start StartElement) error
}

// MarshalerAttr 是那些能够将自身封送为有效 XML 属性的对象所实现的
// 接口。
//
// MarshalXMLAttr 返回一个 XML 属性，其值为接收者的编码值。
// 使用 name 作为属性名称并非必须，但这样做
// 将使 [Unmarshal] 能够将该属性匹配到正确的
// 结构体字段。
// 如果 MarshalXMLAttr 返回零值属性 [Attr]{}，则输出中
// 不会生成任何属性。
// MarshalXMLAttr 仅用于字段标签中带有
// "attr" 选项的结构体字段。
type MarshalerAttr interface {
	MarshalXMLAttr(name Name) (Attr, error)
}

// MarshalIndent 的工作方式与 [Marshal] 类似，但每个 XML 元素都从
// 新的一行开始，该行以 prefix 开头，并根据嵌套深度
// 后跟一个或多个 indent 的副本。
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := NewEncoder(&b)
	enc.Indent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Encoder 将 XML 数据写入输出流。
type Encoder struct {
	p printer
}

// NewEncoder 返回一个写入 w 的新编码器。
func NewEncoder(w io.Writer) *Encoder {
	e := &Encoder{printer{w: bufio.NewWriter(w)}}
	e.p.encoder = e
	return e
}

// Indent 设置编码器生成 XML 时，每个元素都从
// 新的一行开始，该行以 prefix 开头，并根据嵌套深度
// 后跟一个或多个 indent 的副本。
func (enc *Encoder) Indent(prefix, indent string) {
	enc.p.prefix = prefix
	enc.p.indent = indent
}

// Encode 将 v 的 XML 编码写入流。
//
// 关于 Go 值到 XML 的转换细节，参见 [Marshal] 的文档。
//
// Encode 在返回前会调用 [Encoder.Flush]。
func (enc *Encoder) Encode(v any) error {
	err := enc.p.marshalValue(reflect.ValueOf(v), nil, nil)
	if err != nil {
		return err
	}
	return enc.p.w.Flush()
}

// EncodeElement 将 v 的 XML 编码写入流，
// 使用 start 作为编码中最外层的标签。
//
// 关于 Go 值到 XML 的转换细节，参见 [Marshal] 的文档。
//
// EncodeElement 在返回前会调用 [Encoder.Flush]。
func (enc *Encoder) EncodeElement(v any, start StartElement) error {
	err := enc.p.marshalValue(reflect.ValueOf(v), nil, &start)
	if err != nil {
		return err
	}
	return enc.p.w.Flush()
}

var (
	begComment  = []byte("<!--")
	endComment  = []byte("-->")
	endProcInst = []byte("?>")
)

// EncodeToken 将给定的 XML token 写入流。
// 如果 [StartElement] 和 [EndElement] token 未正确配对，它会返回一个错误。
//
// EncodeToken 不调用 [Encoder.Flush]，因为它通常是更大操作的一部分，
// 例如 [Encoder.Encode] 或 [Encoder.EncodeElement]（或在这些操作期间调用的
// 自定义 [Marshaler] 的 MarshalXML），这些操作会在完成时调用 Flush。
// 创建 Encoder 后直接调用 EncodeToken、而不使用
// Encode 或 EncodeElement 的调用者，需要在完成时调用 Flush，以确保
// XML 被写入底层写入器。
//
// EncodeToken 只允许将 Target 设为 "xml" 的 [ProcInst] 作为流中的
// 第一个 token 写入。
func (enc *Encoder) EncodeToken(t Token) error {

	p := &enc.p
	switch t := t.(type) {
	case StartElement:
		if err := p.writeStart(&t); err != nil {
			return err
		}
	case EndElement:
		if err := p.writeEnd(t.Name); err != nil {
			return err
		}
	case CharData:
		escapeText(p, t, false)
	case Comment:
		if bytes.Contains(t, endComment) {
			return fmt.Errorf("xml: EncodeToken of Comment containing --> marker")
		}
		p.WriteString("<!--")
		p.Write(t)
		p.WriteString("-->")
		return p.cachedWriteError()
	case ProcInst:
		// 要编码的第一个 token，同时又是目标为 xml 的 ProcInst，
		// 即为 xml 声明。这是唯一允许目标为 xml 的 ProcInst。
		if t.Target == "xml" && p.w.Buffered() != 0 {
			return fmt.Errorf("xml: EncodeToken of ProcInst xml target only valid for xml declaration, first token encoded")
		}
		if !isNameString(t.Target) {
			return fmt.Errorf("xml: EncodeToken of ProcInst with invalid Target")
		}
		if bytes.Contains(t.Inst, endProcInst) {
			return fmt.Errorf("xml: EncodeToken of ProcInst containing ?> marker")
		}
		p.WriteString("<?")
		p.WriteString(t.Target)
		if len(t.Inst) > 0 {
			p.WriteByte(' ')
			p.Write(t.Inst)
		}
		p.WriteString("?>")
	case Directive:
		if !isValidDirective(t) {
			return fmt.Errorf("xml: EncodeToken of Directive containing wrong < or > markers")
		}
		p.WriteString("<!")
		p.Write(t)
		p.WriteString(">")
	default:
		return fmt.Errorf("xml: EncodeToken of invalid token type")

	}
	return p.cachedWriteError()
}

// isValidDirective 报告 dir 是否是有效的指令文本，
// 即尖括号成对匹配，忽略注释和字符串。
func isValidDirective(dir Directive) bool {
	var (
		depth     int
		inquote   uint8
		incomment bool
	)
	for i, c := range dir {
		switch {
		case incomment:
			if c == '>' {
				if n := 1 + i - len(endComment); n >= 0 && bytes.Equal(dir[n:i+1], endComment) {
					incomment = false
				}
			}
			// 忽略注释中的任何内容
		case inquote != 0:
			if c == inquote {
				inquote = 0
			}
			// 忽略引号内的任何内容
		case c == '\'' || c == '"':
			inquote = c
		case c == '<':
			if i+len(begComment) < len(dir) && bytes.Equal(dir[i:i+len(begComment)], begComment) {
				incomment = true
			} else {
				depth++
			}
		case c == '>':
			if depth == 0 {
				return false
			}
			depth--
		}
	}
	return depth == 0 && inquote == 0 && !incomment
}

// Flush 将任何已缓冲的 XML 刷新到底层写入器。
// 关于何时需要调用它的细节，参见 [Encoder.EncodeToken] 文档。
func (enc *Encoder) Flush() error {
	return enc.p.w.Flush()
}

// Close 关闭 Encoder，表明不再写入任何数据。它会将
// 任何已缓冲的 XML 刷新到底层写入器，并在
// 写入的 XML 无效（例如包含未闭合的元素）时返回错误。
func (enc *Encoder) Close() error {
	return enc.p.Close()
}

type printer struct {
	w          *bufio.Writer
	encoder    *Encoder
	seq        int
	indent     string
	prefix     string
	depth      int
	indentedIn bool
	putNewline bool
	attrNS     map[string]string // map 前缀 -> 命名空间
	attrPrefix map[string]string // map 命名空间 -> 前缀
	prefixes   []string
	tags       []Name
	closed     bool
	err        error
}

// createAttrPrefix 查找用于给定命名空间的命名空间前缀属性，
// 必要时定义一个新的前缀。它返回该前缀。
func (p *printer) createAttrPrefix(url string) string {
	if prefix := p.attrPrefix[url]; prefix != "" {
		return prefix
	}

	// "http://www.w3.org/XML/1998/namespace" 命名空间被预定义为 "xml"，
	// 必须以这种方式引用它。
	// （"http://www.w3.org/2000/xmlns/" 命名空间也被预定义为 "xmlns"，
	// 但用户不应尝试直接使用它 —— 那是我们的工作。）
	if url == xmlURL {
		return xmlPrefix
	}

	// 需要定义一个新的命名空间。
	if p.attrPrefix == nil {
		p.attrPrefix = make(map[string]string)
		p.attrNS = make(map[string]string)
	}

	// 挑一个名称。我们尝试使用路径的最后一个元素，
	// 但会回退到 _。
	prefix := strings.TrimRight(url, "/")
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		prefix = prefix[i+1:]
	}
	if prefix == "" || !isName([]byte(prefix)) || strings.Contains(prefix, ":") {
		prefix = "_"
	}
	// xmlanything 是保留的，无论大小写，它的任何变体
	// 都应被匹配，因此：
	//    (('X'|'x') ('M'|'m') ('L'|'l'))
	// 参见 https://www.w3.org/TR/REC-xml/ 第 2.3 节。
	if len(prefix) >= 3 && strings.EqualFold(prefix[:3], "xml") {
		prefix = "_" + prefix
	}
	if p.attrNS[prefix] != "" {
		// 名称已被占用。找一个更好的。
		for p.seq++; ; p.seq++ {
			if id := prefix + "_" + strconv.Itoa(p.seq); p.attrNS[id] == "" {
				prefix = id
				break
			}
		}
	}

	p.attrPrefix[url] = prefix
	p.attrNS[prefix] = url

	p.WriteString(`xmlns:`)
	p.WriteString(prefix)
	p.WriteString(`="`)
	EscapeText(p, []byte(url))
	p.WriteString(`" `)

	p.prefixes = append(p.prefixes, prefix)

	return prefix
}

// deleteAttrPrefix 移除一个属性命名空间前缀。
func (p *printer) deleteAttrPrefix(prefix string) {
	delete(p.attrPrefix, p.attrNS[prefix])
	delete(p.attrNS, prefix)
}

func (p *printer) markPrefix() {
	p.prefixes = append(p.prefixes, "")
}

func (p *printer) popPrefix() {
	for len(p.prefixes) > 0 {
		prefix := p.prefixes[len(p.prefixes)-1]
		p.prefixes = p.prefixes[:len(p.prefixes)-1]
		if prefix == "" {
			break
		}
		p.deleteAttrPrefix(prefix)
	}
}

// marshalValue 写入表示 val 的一个或多个 XML 元素。
// 如果 val 来自结构体字段，finfo 必须包含其详细信息。
func (p *printer) marshalValue(val reflect.Value, finfo *fieldInfo, startTemplate *StartElement) error {
	if startTemplate != nil && startTemplate.Name.Local == "" {
		return fmt.Errorf("xml: EncodeElement of StartElement with missing name")
	}

	if !val.IsValid() {
		return nil
	}
	if finfo != nil && finfo.flags&fOmitEmpty != 0 && isEmptyValue(val) {
		return nil
	}

	// 深入接口和指针。
	// 面对循环引用链时，这可能导致无限循环，
	// 但它与 Go 1 的行为一致。
	for val.Kind() == reflect.Interface || val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	kind := val.Kind()
	typ := val.Type()

	// 检查 marshaler。
	if val.CanInterface() {
		if marshaler, ok := reflect.TypeAssert[Marshaler](val); ok {
			return p.marshalInterface(marshaler, defaultStart(typ, finfo, startTemplate))
		}
	}
	if val.CanAddr() {
		pv := val.Addr()
		if pv.CanInterface() {
			if marshaler, ok := reflect.TypeAssert[Marshaler](pv); ok {
				return p.marshalInterface(marshaler, defaultStart(pv.Type(), finfo, startTemplate))
			}
		}
	}

	// 检查文本 marshaler。
	if val.CanInterface() {
		if textMarshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](val); ok {
			return p.marshalTextInterface(textMarshaler, defaultStart(typ, finfo, startTemplate))
		}
	}
	if val.CanAddr() {
		pv := val.Addr()
		if pv.CanInterface() {
			if textMarshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](pv); ok {
				return p.marshalTextInterface(textMarshaler, defaultStart(pv.Type(), finfo, startTemplate))
			}
		}
	}

	// 切片和数组会遍历其元素。它们没有外层标签。
	if (kind == reflect.Slice || kind == reflect.Array) && typ.Elem().Kind() != reflect.Uint8 {
		for i, n := 0, val.Len(); i < n; i++ {
			if err := p.marshalValue(val.Index(i), finfo, startTemplate); err != nil {
				return err
			}
		}
		return nil
	}

	tinfo, err := getTypeInfo(typ)
	if err != nil {
		return err
	}

	// 创建起始元素。
	// XML 元素名称的优先级为：
	// 0. startTemplate
	// 1. 底层结构体中的 XMLName 字段；
	// 2. 结构体字段中的字段名/标签；以及
	// 3. 类型名
	var start StartElement

	if startTemplate != nil {
		start.Name = startTemplate.Name
		start.Attr = append(start.Attr, startTemplate.Attr...)
	} else if tinfo.xmlname != nil {
		xmlname := tinfo.xmlname
		if xmlname.name != "" {
			start.Name.Space, start.Name.Local = xmlname.xmlns, xmlname.name
		} else {
			fv := xmlname.value(val, dontInitNilPointers)
			if v, ok := reflect.TypeAssert[Name](fv); ok && v.Local != "" {
				start.Name = v
			}
		}
	}
	if start.Name.Local == "" && finfo != nil {
		start.Name.Space, start.Name.Local = finfo.xmlns, finfo.name
	}
	if start.Name.Local == "" {
		name := typ.Name()
		if i := strings.IndexByte(name, '['); i >= 0 {
			// 截断泛型实例化名称。参见 issue 48318。
			name = name[:i]
		}
		if name == "" {
			return &UnsupportedTypeError{typ}
		}
		start.Name.Local = name
	}

	// 属性
	for i := range tinfo.fields {
		finfo := &tinfo.fields[i]
		if finfo.flags&fAttr == 0 {
			continue
		}
		fv := finfo.value(val, dontInitNilPointers)

		if finfo.flags&fOmitEmpty != 0 && (!fv.IsValid() || isEmptyValue(fv)) {
			continue
		}

		if fv.Kind() == reflect.Interface && fv.IsNil() {
			continue
		}

		name := Name{Space: finfo.xmlns, Local: finfo.name}
		if err := p.marshalAttr(&start, name, fv); err != nil {
			return err
		}
	}

	// 如果发现空名称，命名空间会被覆盖为空空间
	if tinfo.xmlname != nil && start.Name.Space == "" &&
		tinfo.xmlname.xmlns == "" && tinfo.xmlname.name == "" &&
		len(p.tags) != 0 && p.tags[len(p.tags)-1].Space != "" {
		start.Attr = append(start.Attr, Attr{Name{"", xmlnsPrefix}, ""})
	}
	if err := p.writeStart(&start); err != nil {
		return err
	}

	if val.Kind() == reflect.Struct {
		err = p.marshalStruct(tinfo, val)
	} else {
		s, b, err1 := p.marshalSimple(typ, val)
		if err1 != nil {
			err = err1
		} else if b != nil {
			EscapeText(p, b)
		} else {
			p.EscapeString(s)
		}
	}
	if err != nil {
		return err
	}

	if err := p.writeEnd(start.Name); err != nil {
		return err
	}

	return p.cachedWriteError()
}

// marshalAttr 封送具有给定名称和值的属性，并添加到 start.Attr 中。
func (p *printer) marshalAttr(start *StartElement, name Name, val reflect.Value) error {
	if val.CanInterface() {
		if marshaler, ok := reflect.TypeAssert[MarshalerAttr](val); ok {
			attr, err := marshaler.MarshalXMLAttr(name)
			if err != nil {
				return err
			}
			if attr.Name.Local != "" {
				start.Attr = append(start.Attr, attr)
			}
			return nil
		}
	}

	if val.CanAddr() {
		pv := val.Addr()
		if pv.CanInterface() {
			if marshaler, ok := reflect.TypeAssert[MarshalerAttr](pv); ok {
				attr, err := marshaler.MarshalXMLAttr(name)
				if err != nil {
					return err
				}
				if attr.Name.Local != "" {
					start.Attr = append(start.Attr, attr)
				}
				return nil
			}
		}
	}

	if val.CanInterface() {
		if textMarshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](val); ok {
			text, err := textMarshaler.MarshalText()
			if err != nil {
				return err
			}
			start.Attr = append(start.Attr, Attr{name, string(text)})
			return nil
		}
	}

	if val.CanAddr() {
		pv := val.Addr()
		if pv.CanInterface() {
			if textMarshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](pv); ok {
				text, err := textMarshaler.MarshalText()
				if err != nil {
					return err
				}
				start.Attr = append(start.Attr, Attr{name, string(text)})
				return nil
			}
		}
	}

	// 解引用或跳过 nil 指针、接口值。
	switch val.Kind() {
	case reflect.Pointer, reflect.Interface:
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	// 遍历切片。
	if val.Kind() == reflect.Slice && val.Type().Elem().Kind() != reflect.Uint8 {
		n := val.Len()
		for i := 0; i < n; i++ {
			if err := p.marshalAttr(start, name, val.Index(i)); err != nil {
				return err
			}
		}
		return nil
	}

	if val.Type() == attrType {
		attr, _ := reflect.TypeAssert[Attr](val)
		start.Attr = append(start.Attr, attr)
		return nil
	}

	s, b, err := p.marshalSimple(val.Type(), val)
	if err != nil {
		return err
	}
	if b != nil {
		s = string(b)
	}
	start.Attr = append(start.Attr, Attr{name, s})
	return nil
}

// defaultStart 返回在给定 reflect 类型、字段信息和起始模板时
// 使用的默认起始元素。
func defaultStart(typ reflect.Type, finfo *fieldInfo, startTemplate *StartElement) StartElement {
	var start StartElement
	// XML 元素名称的优先级同上，
	// 只是我们不会为了第一个字段而深入结构体内部查找。
	if startTemplate != nil {
		start.Name = startTemplate.Name
		start.Attr = append(start.Attr, startTemplate.Attr...)
	} else if finfo != nil && finfo.name != "" {
		start.Name.Local = finfo.name
		start.Name.Space = finfo.xmlns
	} else if typ.Name() != "" {
		start.Name.Local = typ.Name()
	} else {
		// 它必定是指向具名类型的指针，
		// 因为它具有 Marshaler 方法。
		start.Name.Local = typ.Elem().Name()
	}
	return start
}

// marshalInterface 封送一个 Marshaler 接口值。
func (p *printer) marshalInterface(val Marshaler, start StartElement) error {
	// 向标签栈压入一个标记，这样 MarshalXML
	// 就无法关闭它自己未打开的 XML 标签。
	p.tags = append(p.tags, Name{})
	n := len(p.tags)

	err := val.MarshalXML(p.encoder, start)
	if err != nil {
		return err
	}

	// 确保 MarshalXML 关闭了它所有的标签。p.tags[n-1] 是标记。
	if len(p.tags) > n {
		return fmt.Errorf("xml: %s.MarshalXML wrote invalid XML: <%s> not closed", receiverType(val), p.tags[len(p.tags)-1].Local)
	}
	p.tags = p.tags[:n-1]
	return nil
}

// marshalTextInterface 封送一个 TextMarshaler 接口值。
func (p *printer) marshalTextInterface(val encoding.TextMarshaler, start StartElement) error {
	if err := p.writeStart(&start); err != nil {
		return err
	}
	text, err := val.MarshalText()
	if err != nil {
		return err
	}
	EscapeText(p, text)
	return p.writeEnd(start.Name)
}

// writeStart 写入给定的起始元素。
func (p *printer) writeStart(start *StartElement) error {
	if start.Name.Local == "" {
		return fmt.Errorf("xml: start tag with no name")
	}

	p.tags = append(p.tags, start.Name)
	p.markPrefix()

	p.writeIndent(1)
	p.WriteByte('<')
	p.WriteString(start.Name.Local)

	if start.Name.Space != "" {
		p.WriteString(` xmlns="`)
		p.EscapeString(start.Name.Space)
		p.WriteByte('"')
	}

	// 属性
	for _, attr := range start.Attr {
		name := attr.Name
		if name.Local == "" {
			continue
		}
		p.WriteByte(' ')
		if name.Space != "" {
			p.WriteString(p.createAttrPrefix(name.Space))
			p.WriteByte(':')
		}
		p.WriteString(name.Local)
		p.WriteString(`="`)
		p.EscapeString(attr.Value)
		p.WriteByte('"')
	}
	p.WriteByte('>')
	return nil
}

func (p *printer) writeEnd(name Name) error {
	if name.Local == "" {
		return fmt.Errorf("xml: end tag with no name")
	}
	if len(p.tags) == 0 || p.tags[len(p.tags)-1].Local == "" {
		return fmt.Errorf("xml: end tag </%s> without start tag", name.Local)
	}
	if top := p.tags[len(p.tags)-1]; top != name {
		if top.Local != name.Local {
			return fmt.Errorf("xml: end tag </%s> does not match start tag <%s>", name.Local, top.Local)
		}
		return fmt.Errorf("xml: end tag </%s> in namespace %s does not match start tag <%s> in namespace %s", name.Local, name.Space, top.Local, top.Space)
	}
	p.tags = p.tags[:len(p.tags)-1]

	p.writeIndent(-1)
	p.WriteByte('<')
	p.WriteByte('/')
	p.WriteString(name.Local)
	p.WriteByte('>')
	p.popPrefix()
	return nil
}

func (p *printer) marshalSimple(typ reflect.Type, val reflect.Value) (string, []byte, error) {
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(val.Int(), 10), nil, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(val.Uint(), 10), nil, nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(val.Float(), 'g', -1, val.Type().Bits()), nil, nil
	case reflect.String:
		return val.String(), nil, nil
	case reflect.Bool:
		return strconv.FormatBool(val.Bool()), nil, nil
	case reflect.Array:
		if typ.Elem().Kind() != reflect.Uint8 {
			break
		}
		// [...]byte
		var bytes []byte
		if val.CanAddr() {
			bytes = val.Bytes()
		} else {
			bytes = make([]byte, val.Len())
			reflect.Copy(reflect.ValueOf(bytes), val)
		}
		return "", bytes, nil
	case reflect.Slice:
		if typ.Elem().Kind() != reflect.Uint8 {
			break
		}
		// []byte
		return "", val.Bytes(), nil
	}
	return "", nil, &UnsupportedTypeError{typ}
}

var ddBytes = []byte("--")

// indirect 深入接口和指针，并返回所指向的值。
// 如果遇到 nil 接口或指针，indirect 返回该 nil 值。
// 在存在循环链的情况下，这可能变成无限循环，
// 但它与 Go 1 的行为一致。
func indirect(vf reflect.Value) reflect.Value {
	for vf.Kind() == reflect.Interface || vf.Kind() == reflect.Pointer {
		if vf.IsNil() {
			return vf
		}
		vf = vf.Elem()
	}
	return vf
}

func (p *printer) marshalStruct(tinfo *typeInfo, val reflect.Value) error {
	s := parentStack{p: p}
	for i := range tinfo.fields {
		finfo := &tinfo.fields[i]
		if finfo.flags&fAttr != 0 {
			continue
		}
		vf := finfo.value(val, dontInitNilPointers)
		if !vf.IsValid() {
			// 该字段位于一个为 nil 的匿名结构体字段之后。
			// 跳过它。
			continue
		}

		switch finfo.flags & fMode {
		case fCDATA, fCharData:
			emit := EscapeText
			if finfo.flags&fMode == fCDATA {
				emit = emitCDATA
			}
			if err := s.trim(finfo.parents); err != nil {
				return err
			}
			if vf.CanInterface() {
				if textMarshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](vf); ok {
					data, err := textMarshaler.MarshalText()
					if err != nil {
						return err
					}
					if err := emit(p, data); err != nil {
						return err
					}
					continue
				}
			}
			if vf.CanAddr() {
				pv := vf.Addr()
				if pv.CanInterface() {
					if textMarshaler, ok := reflect.TypeAssert[encoding.TextMarshaler](pv); ok {
						data, err := textMarshaler.MarshalText()
						if err != nil {
							return err
						}
						if err := emit(p, data); err != nil {
							return err
						}
						continue
					}
				}
			}

			var scratch [64]byte
			vf = indirect(vf)
			switch vf.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				if err := emit(p, strconv.AppendInt(scratch[:0], vf.Int(), 10)); err != nil {
					return err
				}
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				if err := emit(p, strconv.AppendUint(scratch[:0], vf.Uint(), 10)); err != nil {
					return err
				}
			case reflect.Float32, reflect.Float64:
				if err := emit(p, strconv.AppendFloat(scratch[:0], vf.Float(), 'g', -1, vf.Type().Bits())); err != nil {
					return err
				}
			case reflect.Bool:
				if err := emit(p, strconv.AppendBool(scratch[:0], vf.Bool())); err != nil {
					return err
				}
			case reflect.String:
				if err := emit(p, []byte(vf.String())); err != nil {
					return err
				}
			case reflect.Slice:
				if elem, ok := reflect.TypeAssert[[]byte](vf); ok {
					if err := emit(p, elem); err != nil {
						return err
					}
				}
			}
			continue

		case fComment:
			if err := s.trim(finfo.parents); err != nil {
				return err
			}
			vf = indirect(vf)
			k := vf.Kind()
			if !(k == reflect.String || k == reflect.Slice && vf.Type().Elem().Kind() == reflect.Uint8) {
				return fmt.Errorf("xml: bad type for comment field of %s", val.Type())
			}
			if vf.Len() == 0 {
				continue
			}
			p.writeIndent(0)
			p.WriteString("<!--")
			dashDash := false
			dashLast := false
			switch k {
			case reflect.String:
				s := vf.String()
				dashDash = strings.Contains(s, "--")
				dashLast = s[len(s)-1] == '-'
				if !dashDash {
					p.WriteString(s)
				}
			case reflect.Slice:
				b := vf.Bytes()
				dashDash = bytes.Contains(b, ddBytes)
				dashLast = b[len(b)-1] == '-'
				if !dashDash {
					p.Write(b)
				}
			default:
				panic("can't happen")
			}
			if dashDash {
				return fmt.Errorf(`xml: comments must not contain "--"`)
			}
			if dashLast {
				// "--->" 是无效语法。将其改为 "- -->"
				p.WriteByte(' ')
			}
			p.WriteString("-->")
			continue

		case fInnerXML:
			vf = indirect(vf)
			iface := vf.Interface()
			switch raw := iface.(type) {
			case []byte:
				p.Write(raw)
				continue
			case string:
				p.WriteString(raw)
				continue
			}

		case fElement, fElement | fAny:
			if err := s.trim(finfo.parents); err != nil {
				return err
			}
			if len(finfo.parents) > len(s.stack) {
				if vf.Kind() != reflect.Pointer && vf.Kind() != reflect.Interface || !vf.IsNil() {
					if err := s.push(finfo.parents[len(s.stack):]); err != nil {
						return err
					}
				}
			}
		}
		if err := p.marshalValue(vf, finfo, nil); err != nil {
			return err
		}
	}
	s.trim(nil)
	return p.cachedWriteError()
}

// Write 实现了 io.Writer
func (p *printer) Write(b []byte) (n int, err error) {
	if p.closed && p.err == nil {
		p.err = errors.New("use of closed Encoder")
	}
	if p.err == nil {
		n, p.err = p.w.Write(b)
	}
	return n, p.err
}

// WriteString 实现了 io.StringWriter
func (p *printer) WriteString(s string) (n int, err error) {
	if p.closed && p.err == nil {
		p.err = errors.New("use of closed Encoder")
	}
	if p.err == nil {
		n, p.err = p.w.WriteString(s)
	}
	return n, p.err
}

// WriteByte 实现了 io.ByteWriter
func (p *printer) WriteByte(c byte) error {
	if p.closed && p.err == nil {
		p.err = errors.New("use of closed Encoder")
	}
	if p.err == nil {
		p.err = p.w.WriteByte(c)
	}
	return p.err
}

// Close 关闭 Encoder，表明不再写入任何数据。它会将
// 任何已缓冲的 XML 刷新到底层写入器，并在
// 写入的 XML 无效（例如包含未闭合的元素）时返回错误。
func (p *printer) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if err := p.w.Flush(); err != nil {
		return err
	}
	if len(p.tags) > 0 {
		return fmt.Errorf("unclosed tag <%s>", p.tags[len(p.tags)-1].Local)
	}
	return nil
}

// 返回 bufio Writer 缓存的写入错误
func (p *printer) cachedWriteError() error {
	_, err := p.Write(nil)
	return err
}

func (p *printer) writeIndent(depthDelta int) {
	if len(p.prefix) == 0 && len(p.indent) == 0 {
		return
	}
	if depthDelta < 0 {
		p.depth--
		if p.indentedIn {
			p.indentedIn = false
			return
		}
		p.indentedIn = false
	}
	if p.putNewline {
		p.WriteByte('\n')
	} else {
		p.putNewline = true
	}
	if len(p.prefix) > 0 {
		p.WriteString(p.prefix)
	}
	if len(p.indent) > 0 {
		for i := 0; i < p.depth; i++ {
			p.WriteString(p.indent)
		}
	}
	if depthDelta > 0 {
		p.depth++
		p.indentedIn = true
	}
}

type parentStack struct {
	p     *printer
	stack []string
}

// trim 更新 XML 上下文，使其与栈和给定 parents 的最长公共前缀相匹配。
// 每弹出一个父元素，都会写入一个闭合标签。
// 传入零值切片或 nil 将关闭所有元素。
func (s *parentStack) trim(parents []string) error {
	split := 0
	for ; split < len(parents) && split < len(s.stack); split++ {
		if parents[split] != s.stack[split] {
			break
		}
	}
	for i := len(s.stack) - 1; i >= split; i-- {
		if err := s.p.writeEnd(Name{Local: s.stack[i]}); err != nil {
			return err
		}
	}
	s.stack = s.stack[:split]
	return nil
}

// push 将父元素添加到栈中并写入打开标签。
func (s *parentStack) push(parents []string) error {
	for i := 0; i < len(parents); i++ {
		if err := s.p.writeStart(&StartElement{Name: Name{Local: parents[i]}}); err != nil {
			return err
		}
	}
	s.stack = append(s.stack, parents...)
	return nil
}

// 当 [Marshal] 遇到无法转换为 XML 的类型时，
// 会返回 UnsupportedTypeError。
type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return "xml: unsupported type: " + e.Type.String()
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}
