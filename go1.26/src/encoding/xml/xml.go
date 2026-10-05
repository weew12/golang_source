// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package xml 实现了一个简单的 XML 1.0 解析器，
// 它能理解 XML 命名空间。
package xml

// 参考资料：
//    带注解的 XML 规范：https://www.xml.com/axml/testaxml.htm
//    XML 命名空间：https://www.w3.org/TR/REC-xml-names/

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SyntaxError 表示 XML 输入流中的一个语法错误。
type SyntaxError struct {
	Msg  string
	Line int
}

func (e *SyntaxError) Error() string {
	return "XML syntax error on line " + strconv.Itoa(e.Line) + ": " + e.Msg
}

// Name 表示一个 XML 名称（Local），
// 并标注了命名空间标识符（Space）。
// 在 [Decoder.Token] 返回的 token 中，Space 标识符
// 以规范化 URL 的形式给出，而不是被解析文档中
// 使用的短前缀。
type Name struct {
	Space, Local string
}

// Attr 表示 XML 元素中的一个属性（Name=Value）。
type Attr struct {
	Name  Name
	Value string
}

// Token 是一个接口，持有以下 token 类型之一：
// [StartElement]、[EndElement]、[CharData]、[Comment]、[ProcInst] 或 [Directive]。
type Token any

// StartElement 表示一个 XML 起始元素。
type StartElement struct {
	Name Name
	Attr []Attr
}

// Copy 创建 StartElement 的新副本。
func (e StartElement) Copy() StartElement {
	attrs := make([]Attr, len(e.Attr))
	copy(attrs, e.Attr)
	e.Attr = attrs
	return e
}

// End 返回对应的 XML 结束元素。
func (e StartElement) End() EndElement {
	return EndElement{e.Name}
}

// EndElement 表示一个 XML 结束元素。
type EndElement struct {
	Name Name
}

// CharData 表示 XML 字符数据（原始文本），
// 其中的 XML 转义序列已被替换为
// 它们所代表的字符。
type CharData []byte

// Copy 创建 CharData 的新副本。
func (c CharData) Copy() CharData { return CharData(bytes.Clone(c)) }

// Comment 表示形如 <!--comment--> 的 XML 注释。
// 其字节内容不包含 <!-- 和 --> 注释标记。
type Comment []byte

// Copy 创建 Comment 的新副本。
func (c Comment) Copy() Comment { return Comment(bytes.Clone(c)) }

// ProcInst 表示形如 <?target inst?> 的 XML 处理指令。
type ProcInst struct {
	Target string
	Inst   []byte
}

// Copy 创建 ProcInst 的新副本。
func (p ProcInst) Copy() ProcInst {
	p.Inst = bytes.Clone(p.Inst)
	return p
}

// Directive 表示形如 <!text> 的 XML 指令。
// 其字节内容不包含 <! 和 > 标记。
type Directive []byte

// Copy 创建 Directive 的新副本。
func (d Directive) Copy() Directive { return Directive(bytes.Clone(d)) }

// CopyToken 返回 Token 的副本。
func CopyToken(t Token) Token {
	switch v := t.(type) {
	case CharData:
		return v.Copy()
	case Comment:
		return v.Copy()
	case Directive:
		return v.Copy()
	case ProcInst:
		return v.Copy()
	case StartElement:
		return v.Copy()
	}
	return t
}

// TokenReader 是任何能够解码 XML token 流的东西，包括
// [Decoder]。
//
// 当 Token 在成功读取一个 token 之后遇到错误或文件结束（EOF）条件时，
// 它会返回该 token。它既可能在同一次调用中返回（非 nil 的）错误，
// 也可能在后续调用中返回该错误（以及一个 nil token）。
// 这种一般情况的一个例子是：在 token 流末尾返回非 nil token 的
// TokenReader 可以返回 io.EOF 或 nil 错误。
// 下一次 Read 应返回 nil、[io.EOF]。
//
// 不建议 Token 的实现返回 nil token 搭配 nil 错误。调用者应将
// 返回 nil、nil 视为未发生任何事；尤其是它并不表示 EOF。
type TokenReader interface {
	Token() (Token, error)
}

// Decoder 表示一个读取特定输入流的 XML 解析器。
// 该解析器假定其输入以 UTF-8 编码。
type Decoder struct {
	// Strict 默认为 true，强制遵守 XML 规范的要求。
	// 如果设为 false，解析器允许包含常见错误的输入：
	//	* 如果某个元素缺少结束标签，解析器会按需虚构
	//	  结束标签，以使 Token 的返回值保持正确配对。
	//	* 在属性值和字符数据中，未知或格式错误的
	//	  字符实体（以 & 开头的序列）将保持原样。
	//
	// 设置：
	//
	//	d.Strict = false
	//	d.AutoClose = xml.HTMLAutoClose
	//	d.Entity = xml.HTMLEntity
	//
	// 可创建能处理典型 HTML 的解析器。
	//
	// Strict 模式不强制遵守 XML 命名空间 TR 的要求。
	// 尤其是它不会拒绝使用未定义前缀的命名空间标签。
	// 这类标签会以未知前缀作为命名空间 URL 记录下来。
	Strict bool

	// 当 Strict == false 时，AutoClose 指示一组元素，
	// 它们在打开之后立即被视为已关闭，无论
	// 是否存在结束元素。
	AutoClose []string

	// Entity 可用于将非标准实体名称映射到字符串替换。
	// 无论该 map 的实际内容如何，解析器的行为都如同
	// 这些标准映射存在于 map 中：
	//
	//	"lt": "<",
	//	"gt": ">",
	//	"amp": "&",
	//	"apos": "'",
	//	"quot": `"`,
	Entity map[string]string

	// CharsetReader 若为非 nil，则定义一个函数来生成
	// 字符集转换读取器，将提供的非 UTF-8 字符集
	// 转换为 UTF-8。如果 CharsetReader 为 nil 或
	// 返回错误，解析会以错误停止。CharsetReader 的
	// 结果值之一必须为非 nil。
	CharsetReader func(charset string, input io.Reader) (io.Reader, error)

	// DefaultSpace 设置用于无修饰标签的默认命名空间，
	// 就如同整个 XML 流被包裹在一个包含
	// 属性 xmlns="DefaultSpace" 的元素中一样。
	DefaultSpace string

	r              io.ByteReader
	t              TokenReader
	buf            bytes.Buffer
	saved          *bytes.Buffer
	stk            *stack
	free           *stack
	needClose      bool
	toClose        Name
	nextToken      Token
	nextByte       int
	ns             map[string]string
	err            error
	line           int
	linestart      int64
	offset         int64
	unmarshalDepth int
}

// NewDecoder 创建一个从 r 读取的新的 XML 解析器。
// 如果 r 未实现 [io.ByteReader]，NewDecoder 将
// 自行进行缓冲。
func NewDecoder(r io.Reader) *Decoder {
	d := &Decoder{
		ns:       make(map[string]string),
		nextByte: -1,
		line:     1,
		Strict:   true,
	}
	d.switchToReader(r)
	return d
}

// NewTokenDecoder 使用底层 token 流创建一个新的 XML 解析器。
func NewTokenDecoder(t TokenReader) *Decoder {
	// 它已经是 Decoder 了吗？
	if d, ok := t.(*Decoder); ok {
		return d
	}
	d := &Decoder{
		ns:       make(map[string]string),
		t:        t,
		nextByte: -1,
		line:     1,
		Strict:   true,
	}
	return d
}

// Token 返回输入流中的下一个 XML token。
// 在输入流末尾，Token 返回 nil、[io.EOF]。
//
// 返回的 token 数据中的字节切片引用解析器的
// 内部缓冲区，仅在下一次调用 Token 之前有效。
// 若要获取字节的副本，请调用 [CopyToken]
// 或该 token 的 Copy 方法。
//
// Token 会将诸如 <br> 这样的自闭合元素
// 展开为通过连续调用返回的独立起始和结束元素。
//
// Token 保证它返回的 [StartElement] 和 [EndElement]
// token 正确嵌套且配对：如果 Token 在所有预期的结束元素
// 之前遇到意外的结束元素或 EOF，它将返回一个错误。
//
// 如果调用了 [Decoder.CharsetReader] 并返回错误，
// 该错误会被包装并返回。
//
// Token 按 https://www.w3.org/TR/REC-xml-names/ 所述
// 实现 XML 命名空间。Token 中包含的每个 [Name]
// 结构在已知时都会将 Space 设置为标识其命名空间的 URL。
// 如果 Token 遇到无法识别的命名空间前缀，
// 它会使用该前缀作为 Space，而不是报告错误。
func (d *Decoder) Token() (Token, error) {
	var t Token
	var err error
	if d.stk != nil && d.stk.kind == stkEOF {
		return nil, io.EOF
	}
	if d.nextToken != nil {
		t = d.nextToken
		d.nextToken = nil
	} else {
		if t, err = d.rawToken(); t == nil && err != nil {
			if err == io.EOF && d.stk != nil && d.stk.kind != stkEOF {
				err = d.syntaxError("unexpected EOF")
			}
			return nil, err
		}
		// 我们还有一个 token 需要处理，因此清除
		// 任何错误（例如 EOF）并继续。
		err = nil
	}
	if !d.Strict {
		if t1, ok := d.autoClose(t); ok {
			d.nextToken = t
			t = t1
		}
	}
	switch t1 := t.(type) {
	case StartElement:
		// 在 XML 命名空间中，属性中列出的翻译
		// 会应用于元素名称和其他属性名称，
		// 因此先处理这些翻译。
		for _, a := range t1.Attr {
			if a.Name.Space == xmlnsPrefix {
				v, ok := d.ns[a.Name.Local]
				d.pushNs(a.Name.Local, v, ok)
				d.ns[a.Name.Local] = a.Value
			}
			if a.Name.Space == "" && a.Name.Local == xmlnsPrefix {
				// 无标签名称的默认命名空间
				v, ok := d.ns[""]
				d.pushNs("", v, ok)
				d.ns[""] = a.Value
			}
		}

		d.pushElement(t1.Name)
		d.translate(&t1.Name, true)
		for i := range t1.Attr {
			d.translate(&t1.Attr[i].Name, false)
		}
		t = t1

	case EndElement:
		if !d.popElement(&t1) {
			return nil, d.err
		}
		t = t1
	}
	return t, err
}

const (
	xmlURL      = "http://www.w3.org/XML/1998/namespace"
	xmlnsPrefix = "xmlns"
	xmlPrefix   = "xml"
)

// 对名称 n 应用命名空间翻译。
// 默认命名空间（Space==""）仅应用于
// 元素名称，而不应用于属性名称。
func (d *Decoder) translate(n *Name, isElementName bool) {
	switch {
	case n.Space == xmlnsPrefix:
		return
	case n.Space == "" && !isElementName:
		return
	case n.Space == xmlPrefix:
		n.Space = xmlURL
	case n.Space == "" && n.Local == xmlnsPrefix:
		return
	}
	if v, ok := d.ns[n.Space]; ok {
		n.Space = v
	} else if n.Space == "" {
		n.Space = d.DefaultSpace
	}
}

func (d *Decoder) switchToReader(r io.Reader) {
	// 获取高效的逐字节读取器。
	// 假定如果 reader 有自己的
	// ReadByte，它就足够高效。
	// 否则使用 bufio。
	if rb, ok := r.(io.ByteReader); ok {
		d.r = rb
	} else {
		d.r = bufio.NewReader(r)
	}
}

// 解析状态 —— 栈保存旧的命名空间翻译
// 以及当前打开的元素集合。结束给定标签时需要弹出的翻译
// 位于栈中该标签的*下方*，这更费事，但这是 XML 强加给我们的。
type stack struct {
	next *stack
	kind int
	name Name
	ok   bool
}

const (
	stkStart = iota
	stkNs
	stkEOF
)

func (d *Decoder) push(kind int) *stack {
	s := d.free
	if s != nil {
		d.free = s.next
	} else {
		s = new(stack)
	}
	s.next = d.stk
	s.kind = kind
	d.stk = s
	return s
}

func (d *Decoder) pop() *stack {
	s := d.stk
	if s != nil {
		d.stk = s.next
		s.next = d.free
		d.free = s
	}
	return s
}

// 记录：在当前元素完成之后（该元素已经压入栈），
// 在调用 popEOF 之前，Token 应返回 EOF。
func (d *Decoder) pushEOF() {
	// 沿栈向下查找 Start。
	// 它可能不在栈顶，因为其上方可能有
	// stkNs 条目。
	start := d.stk
	for start.kind != stkStart {
		start = start.next
	}
	// start 下方的 stkNs 条目也与该元素相关联；
	// 跳过它们。
	for start.next != nil && start.next.kind == stkNs {
		start = start.next
	}
	s := d.free
	if s != nil {
		d.free = s.next
	} else {
		s = new(stack)
	}
	s.kind = stkEOF
	s.next = start.next
	start.next = s
}

// 撤销一次 pushEOF。
// 该元素必定已经完成，因此 EOF 应位于栈顶。
func (d *Decoder) popEOF() bool {
	if d.stk == nil || d.stk.kind != stkEOF {
		return false
	}
	d.pop()
	return true
}

// 记录我们正在开始一个具有给定名称的元素。
func (d *Decoder) pushElement(name Name) {
	s := d.push(stkStart)
	s.name = name
}

// 记录我们正在更改 ns[local] 的值。
// 旧值为 url、ok。
func (d *Decoder) pushNs(local string, url string, ok bool) {
	s := d.push(stkNs)
	s.name.Local = local
	s.name.Space = url
	s.ok = ok
}

// 使用当前行号创建一个 SyntaxError。
func (d *Decoder) syntaxError(msg string) error {
	return &SyntaxError{Msg: msg, Line: d.line}
}

// 记录我们正在结束一个具有给定名称的元素。
// 该名称必须与栈顶的记录匹配，
// 该记录必须是 pushElement 记录。
// 弹出该元素后，应用栈中的任何撤销记录，
// 以恢复在我们看到此元素之前
// 存在的名称翻译。
func (d *Decoder) popElement(t *EndElement) bool {
	s := d.pop()
	name := t.Name
	switch {
	case s == nil || s.kind != stkStart:
		d.err = d.syntaxError("unexpected end element </" + name.Local + ">")
		return false
	case s.name.Local != name.Local:
		if !d.Strict {
			d.needClose = true
			d.toClose = t.Name
			t.Name = s.name
			return true
		}
		d.err = d.syntaxError("element <" + s.name.Local + "> closed by </" + name.Local + ">")
		return false
	case s.name.Space != name.Space:
		ns := name.Space
		if name.Space == "" {
			ns = `""`
		}
		d.err = d.syntaxError("element <" + s.name.Local + "> in space " + s.name.Space +
			" closed by </" + name.Local + "> in space " + ns)
		return false
	}

	d.translate(&t.Name, true)

	// 弹出栈直到 Start 或 EOF 位于栈顶，同时撤销
	// 与我们刚刚关闭的元素相关联的翻译。
	for d.stk != nil && d.stk.kind != stkStart && d.stk.kind != stkEOF {
		s := d.pop()
		if s.ok {
			d.ns[s.name.Local] = s.name.Space
		} else {
			delete(d.ns, s.name.Local)
		}
	}

	return true
}

// 如果栈顶元素正在自动关闭，
// 且 t 不是其结束标签，则虚构结束标签。
func (d *Decoder) autoClose(t Token) (Token, bool) {
	if d.stk == nil || d.stk.kind != stkStart {
		return nil, false
	}
	for _, s := range d.AutoClose {
		if strings.EqualFold(s, d.stk.name.Local) {
			// 如果 t 没有关闭它，则这一个应被自动关闭。
			et, ok := t.(EndElement)
			if !ok || !strings.EqualFold(et.Name.Local, d.stk.name.Local) {
				return EndElement{d.stk.name}, true
			}
			break
		}
	}
	return nil, false
}

var errRawToken = errors.New("xml: cannot use RawToken from UnmarshalXML method")

// RawToken 类似 [Decoder.Token]，但不验证
// 起始和结束元素是否匹配，也不将
// 命名空间前缀翻译为它们对应的 URL。
func (d *Decoder) RawToken() (Token, error) {
	if d.unmarshalDepth > 0 {
		return nil, errRawToken
	}
	return d.rawToken()
}

func (d *Decoder) rawToken() (Token, error) {
	if d.t != nil {
		return d.t.Token()
	}
	if d.err != nil {
		return nil, d.err
	}
	if d.needClose {
		// 我们读取的最后一个元素是自闭合的，
		// 我们只返回了 StartElement 那一半。
		// 现在返回 EndElement 那一半。
		d.needClose = false
		return EndElement{d.toClose}, nil
	}

	b, ok := d.getc()
	if !ok {
		return nil, d.err
	}

	if b != '<' {
		// 文本部分。
		d.ungetc(b)
		data := d.text(-1, false)
		if data == nil {
			return nil, d.err
		}
		return CharData(data), nil
	}

	if b, ok = d.mustgetc(); !ok {
		return nil, d.err
	}
	switch b {
	case '/':
		// </：结束元素
		var name Name
		if name, ok = d.nsname(); !ok {
			if d.err == nil {
				d.err = d.syntaxError("expected element name after </")
			}
			return nil, d.err
		}
		d.space()
		if b, ok = d.mustgetc(); !ok {
			return nil, d.err
		}
		if b != '>' {
			d.err = d.syntaxError("invalid characters between </" + name.Local + " and >")
			return nil, d.err
		}
		return EndElement{name}, nil

	case '?':
		// <?：处理指令。
		var target string
		if target, ok = d.name(); !ok {
			if d.err == nil {
				d.err = d.syntaxError("expected target name after <?")
			}
			return nil, d.err
		}
		d.space()
		d.buf.Reset()
		var b0 byte
		for {
			if b, ok = d.mustgetc(); !ok {
				return nil, d.err
			}
			d.buf.WriteByte(b)
			if b0 == '?' && b == '>' {
				break
			}
			b0 = b
		}
		data := d.buf.Bytes()
		data = data[0 : len(data)-2] // 截掉 ?>

		if target == "xml" {
			content := string(data)
			ver := procInst("version", content)
			if ver != "" && ver != "1.0" {
				d.err = fmt.Errorf("xml: unsupported version %q; only version 1.0 is supported", ver)
				return nil, d.err
			}
			enc := procInst("encoding", content)
			if enc != "" && enc != "utf-8" && enc != "UTF-8" && !strings.EqualFold(enc, "utf-8") {
				if d.CharsetReader == nil {
					d.err = fmt.Errorf("xml: encoding %q declared but Decoder.CharsetReader is nil", enc)
					return nil, d.err
				}
				newr, err := d.CharsetReader(enc, d.r.(io.Reader))
				if err != nil {
					d.err = fmt.Errorf("xml: opening charset %q: %w", enc, err)
					return nil, d.err
				}
				if newr == nil {
					panic("CharsetReader returned a nil Reader for charset " + enc)
				}
				d.switchToReader(newr)
			}
		}
		return ProcInst{target, data}, nil

	case '!':
		// <!：可能是注释，也可能是 CDATA。
		if b, ok = d.mustgetc(); !ok {
			return nil, d.err
		}
		switch b {
		case '-': // <!-
			// 可能是 <!--，即注释。
			if b, ok = d.mustgetc(); !ok {
				return nil, d.err
			}
			if b != '-' {
				d.err = d.syntaxError("invalid sequence <!- not part of <!--")
				return nil, d.err
			}
			// 查找终止符。
			d.buf.Reset()
			var b0, b1 byte
			for {
				if b, ok = d.mustgetc(); !ok {
					return nil, d.err
				}
				d.buf.WriteByte(b)
				if b0 == '-' && b1 == '-' {
					if b != '>' {
						d.err = d.syntaxError(
							`invalid sequence "--" not allowed in comments`)
						return nil, d.err
					}
					break
				}
				b0, b1 = b1, b
			}
			data := d.buf.Bytes()
			data = data[0 : len(data)-3] // 截掉 -->
			return Comment(data), nil

		case '[': // <![
			// 可能是 <![CDATA[。
			for i := 0; i < 6; i++ {
				if b, ok = d.mustgetc(); !ok {
					return nil, d.err
				}
				if b != "CDATA["[i] {
					d.err = d.syntaxError("invalid <![ sequence")
					return nil, d.err
				}
			}
			// 已遇到 <![CDATA[。读取文本直到 ]]>。
			data := d.text(-1, true)
			if data == nil {
				return nil, d.err
			}
			return CharData(data), nil
		}

		// 可能是一条指令：<!DOCTYPE ...>、<!ENTITY ...> 等。
		// 我们不关心其内容，但会为调用者累积。引号内的尖括号
		// 不计入嵌套。
		d.buf.Reset()
		d.buf.WriteByte(b)
		inquote := uint8(0)
		depth := 0
		for {
			if b, ok = d.mustgetc(); !ok {
				return nil, d.err
			}
			if inquote == 0 && b == '>' && depth == 0 {
				break
			}
		HandleB:
			d.buf.WriteByte(b)
			switch {
			case b == inquote:
				inquote = 0

			case inquote != 0:
				// 在引号内，无特殊操作

			case b == '\'' || b == '"':
				inquote = b

			case b == '>' && inquote == 0:
				depth--

			case b == '<' && inquote == 0:
				// 查找 <!-- 以开始注释。
				s := "!--"
				for i := 0; i < len(s); i++ {
					if b, ok = d.mustgetc(); !ok {
						return nil, d.err
					}
					if b != s[i] {
						for j := 0; j < i; j++ {
							d.buf.WriteByte(s[j])
						}
						depth++
						goto HandleB
					}
				}

				// 移除上面写入的 <。
				d.buf.Truncate(d.buf.Len() - 1)

				// 查找终止符。
				var b0, b1 byte
				for {
					if b, ok = d.mustgetc(); !ok {
						return nil, d.err
					}
					if b0 == '-' && b1 == '-' && b == '>' {
						break
					}
					b0, b1 = b1, b
				}

				// 在返回的 Directive 主体中用空格替换注释，
				// 这样被注释分隔的标记部分（例如 "<" 和 "!"）
				// 在重新编码 Directive 时不会被连接起来
				// 而产生新的语义。
				d.buf.WriteByte(' ')
			}
		}
		return Directive(d.buf.Bytes()), nil
	}

	// 必定是一个打开的元素，如 <a href="foo">
	d.ungetc(b)

	var (
		name  Name
		empty bool
		attr  []Attr
	)
	if name, ok = d.nsname(); !ok {
		if d.err == nil {
			d.err = d.syntaxError("expected element name after <")
		}
		return nil, d.err
	}

	attr = []Attr{}
	for {
		d.space()
		if b, ok = d.mustgetc(); !ok {
			return nil, d.err
		}
		if b == '/' {
			empty = true
			if b, ok = d.mustgetc(); !ok {
				return nil, d.err
			}
			if b != '>' {
				d.err = d.syntaxError("expected /> in element")
				return nil, d.err
			}
			break
		}
		if b == '>' {
			break
		}
		d.ungetc(b)

		a := Attr{}
		if a.Name, ok = d.nsname(); !ok {
			if d.err == nil {
				d.err = d.syntaxError("expected attribute name in element")
			}
			return nil, d.err
		}
		d.space()
		if b, ok = d.mustgetc(); !ok {
			return nil, d.err
		}
		if b != '=' {
			if d.Strict {
				d.err = d.syntaxError("attribute name without = in element")
				return nil, d.err
			}
			d.ungetc(b)
			a.Value = a.Name.Local
		} else {
			d.space()
			data := d.attrval()
			if data == nil {
				return nil, d.err
			}
			a.Value = string(data)
		}
		attr = append(attr, a)
	}
	if empty {
		d.needClose = true
		d.toClose = name
	}
	return StartElement{name, attr}, nil
}

func (d *Decoder) attrval() []byte {
	b, ok := d.mustgetc()
	if !ok {
		return nil
	}
	// 处理带引号的属性值
	if b == '"' || b == '\'' {
		return d.text(int(b), false)
	}
	// 为严格解析器处理不带引号的属性值
	if d.Strict {
		d.err = d.syntaxError("unquoted or missing attribute value in element")
		return nil
	}
	// 为非严格解析器处理不带引号的属性值
	d.ungetc(b)
	d.buf.Reset()
	for {
		b, ok = d.mustgetc()
		if !ok {
			return nil
		}
		// https://www.w3.org/TR/REC-html40/intro/sgmltut.html#h-3.2.2
		if 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' ||
			'0' <= b && b <= '9' || b == '_' || b == ':' || b == '-' {
			d.buf.WriteByte(b)
		} else {
			d.ungetc(b)
			break
		}
	}
	return d.buf.Bytes()
}

// 跳过空白（如有）
func (d *Decoder) space() {
	for {
		b, ok := d.getc()
		if !ok {
			return
		}
		switch b {
		case ' ', '\r', '\n', '\t':
		default:
			d.ungetc(b)
			return
		}
	}
}

// 读取单个字节。
// 如果没有可读取的字节，返回 ok==false
// 并将错误留在 d.err 中。
// 维护行号。
func (d *Decoder) getc() (b byte, ok bool) {
	if d.err != nil {
		return 0, false
	}
	if d.nextByte >= 0 {
		b = byte(d.nextByte)
		d.nextByte = -1
	} else {
		b, d.err = d.r.ReadByte()
		if d.err != nil {
			return 0, false
		}
		if d.saved != nil {
			d.saved.WriteByte(b)
		}
	}
	if b == '\n' {
		d.line++
		d.linestart = d.offset + 1
	}
	d.offset++
	return b, true
}

// InputOffset 返回当前解码器位置在输入流中的字节偏移量。
// 该偏移量给出最近返回的 token 的结尾
// 以及下一个 token 的开头的位置。
func (d *Decoder) InputOffset() int64 {
	return d.offset
}

// InputPos 返回当前解码器位置所在的行以及该行内从 1 开始的
// 输入位置。该位置给出最近返回的 token 的结尾位置。
func (d *Decoder) InputPos() (line, column int) {
	return d.line, int(d.offset-d.linestart) + 1
}

// 返回已保存的偏移量。
// 如果我们执行过 ungetc（nextByte >= 0），则必须回退一个。
func (d *Decoder) savedOffset() int {
	n := d.saved.Len()
	if d.nextByte >= 0 {
		n--
	}
	return n
}

// 必须读取单个字节。
// 如果没有可读取的字节，
// 将 d.err 设为 SyntaxError("unexpected EOF")
// 并返回 ok==false
func (d *Decoder) mustgetc() (b byte, ok bool) {
	if b, ok = d.getc(); !ok {
		if d.err == io.EOF {
			d.err = d.syntaxError("unexpected EOF")
		}
	}
	return
}

// 撤销读取一个字节。
func (d *Decoder) ungetc(b byte) {
	if b == '\n' {
		d.line--
	}
	d.nextByte = int(b)
	d.offset--
}

var entity = map[string]rune{
	"lt":   '<',
	"gt":   '>',
	"amp":  '&',
	"apos": '\'',
	"quot": '"',
}

// 读取纯文本部分（XML 称之为字符数据）。
// 如果 quote >= 0，我们处于带引号的字符串中，需要找到匹配的引号。
// 如果 cdata == true，我们处于 <![CDATA[ 区段中，需要找到 ]]>。
// 失败时返回 nil 并将错误留在 d.err 中。
func (d *Decoder) text(quote int, cdata bool) []byte {
	var b0, b1 byte
	var trunc int
	d.buf.Reset()
Input:
	for {
		b, ok := d.getc()
		if !ok {
			if cdata {
				if d.err == io.EOF {
					d.err = d.syntaxError("unexpected EOF in CDATA section")
				}
				return nil
			}
			break Input
		}

		// <![CDATA[ 区段以 ]]> 结束。
		// ]]> 出现在普通文本中是错误，
		// 但在带引号的字符串中是允许的。
		if quote < 0 && b0 == ']' && b1 == ']' && b == '>' {
			if cdata {
				trunc = 2
				break Input
			}
			d.err = d.syntaxError("unescaped ]]> not in CDATA section")
			return nil
		}

		// 如果看到 <，则停止读取文本。
		if b == '<' && !cdata {
			if quote >= 0 {
				d.err = d.syntaxError("unescaped < inside quoted string")
				return nil
			}
			d.ungetc('<')
			break Input
		}
		if quote >= 0 && b == byte(quote) {
			break Input
		}
		if b == '&' && !cdata {
			// 读取转义字符表达式直到分号。
			// XML 的“全部荣耀”在于允许文档通过 <!ENTITY ...> 指令
			// 定义并使用自己的字符名称。
			// 解析器必须能识别 lt、gt、amp、apos 和 quot，
			// 即使它们未被声明。
			before := d.buf.Len()
			d.buf.WriteByte('&')
			var ok bool
			var text string
			var haveText bool
			if b, ok = d.mustgetc(); !ok {
				return nil
			}
			if b == '#' {
				d.buf.WriteByte(b)
				if b, ok = d.mustgetc(); !ok {
					return nil
				}
				base := 10
				if b == 'x' {
					base = 16
					d.buf.WriteByte(b)
					if b, ok = d.mustgetc(); !ok {
						return nil
					}
				}
				start := d.buf.Len()
				for '0' <= b && b <= '9' ||
					base == 16 && 'a' <= b && b <= 'f' ||
					base == 16 && 'A' <= b && b <= 'F' {
					d.buf.WriteByte(b)
					if b, ok = d.mustgetc(); !ok {
						return nil
					}
				}
				if b != ';' {
					d.ungetc(b)
				} else {
					s := string(d.buf.Bytes()[start:])
					d.buf.WriteByte(';')
					n, err := strconv.ParseUint(s, base, 64)
					if err == nil && n <= unicode.MaxRune {
						text = string(rune(n))
						haveText = true
					}
				}
			} else {
				d.ungetc(b)
				if !d.readName() {
					if d.err != nil {
						return nil
					}
				}
				if b, ok = d.mustgetc(); !ok {
					return nil
				}
				if b != ';' {
					d.ungetc(b)
				} else {
					name := d.buf.Bytes()[before+1:]
					d.buf.WriteByte(';')
					if isName(name) {
						s := string(name)
						if r, ok := entity[s]; ok {
							text = string(r)
							haveText = true
						} else if d.Entity != nil {
							text, haveText = d.Entity[s]
						}
					}
				}
			}

			if haveText {
				d.buf.Truncate(before)
				d.buf.WriteString(text)
				b0, b1 = 0, 0
				continue Input
			}
			if !d.Strict {
				b0, b1 = 0, 0
				continue Input
			}
			ent := string(d.buf.Bytes()[before:])
			if ent[len(ent)-1] != ';' {
				ent += " (no semicolon)"
			}
			d.err = d.syntaxError("invalid character entity " + ent)
			return nil
		}

		// 我们必须将未转义的 \r 和 \r\n 重写为 \n。
		if b == '\r' {
			d.buf.WriteByte('\n')
		} else if b1 == '\r' && b == '\n' {
			// 跳过 \r\n —— 我们已经写入了 \n。
		} else {
			d.buf.WriteByte(b)
		}

		b0, b1 = b1, b
	}
	data := d.buf.Bytes()
	data = data[0 : len(data)-trunc]

	// 检查每个 rune 是否为不允许的字符。
	buf := data
	for len(buf) > 0 {
		r, size := utf8.DecodeRune(buf)
		if r == utf8.RuneError && size == 1 {
			d.err = d.syntaxError("invalid UTF-8")
			return nil
		}
		buf = buf[size:]
		if !isInCharacterRange(r) {
			d.err = d.syntaxError(fmt.Sprintf("illegal character code %U", r))
			return nil
		}
	}

	return data
}

// 根据 https://www.xml.com/axml/testaxml.htm 中
// 第 2.2 节 Characters 的 Char 产生式，
// 判断给定 rune 是否位于 XML 字符范围内。
func isInCharacterRange(r rune) (inrange bool) {
	return r == 0x09 ||
		r == 0x0A ||
		r == 0x0D ||
		r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r <= 0xFFFD ||
		r >= 0x10000 && r <= 0x10FFFF
}

// 获取命名空间名称：中间带有 : 的名称。
// : 之前的部分是命名空间标识符。
func (d *Decoder) nsname() (name Name, ok bool) {
	s, ok := d.name()
	if !ok {
		return
	}
	if strings.Count(s, ":") > 1 {
		return name, false
	} else if space, local, ok := strings.Cut(s, ":"); !ok || space == "" || local == "" {
		name.Local = s
	} else {
		name.Space = space
		name.Local = local
	}
	return name, true
}

// 获取名称：/first(first|second)*/
// 如果名称缺失，不要设置 d.err（除非收到意外的 EOF）：
// 让调用者提供更好的上下文。
func (d *Decoder) name() (s string, ok bool) {
	d.buf.Reset()
	if !d.readName() {
		return "", false
	}

	// 现在我们检查这些字符。
	b := d.buf.Bytes()
	if !isName(b) {
		d.err = d.syntaxError("invalid XML name: " + string(b))
		return "", false
	}
	return string(b), true
}

// 读取一个名称并将其字节追加到 d.buf。
// 该名称由任何在名称中无效的单字节字符分隔。
// 所有多字节字符都会被接受；调用者必须检查它们的有效性。
func (d *Decoder) readName() (ok bool) {
	var b byte
	if b, ok = d.mustgetc(); !ok {
		return
	}
	if b < utf8.RuneSelf && !isNameByte(b) {
		d.ungetc(b)
		return false
	}
	d.buf.WriteByte(b)

	for {
		if b, ok = d.mustgetc(); !ok {
			return
		}
		if b < utf8.RuneSelf && !isNameByte(b) {
			d.ungetc(b)
			break
		}
		d.buf.WriteByte(b)
	}
	return true
}

func isNameByte(c byte) bool {
	return 'A' <= c && c <= 'Z' ||
		'a' <= c && c <= 'z' ||
		'0' <= c && c <= '9' ||
		c == '_' || c == ':' || c == '.' || c == '-'
}

func isName(s []byte) bool {
	if len(s) == 0 {
		return false
	}
	c, n := utf8.DecodeRune(s)
	if c == utf8.RuneError && n == 1 {
		return false
	}
	if !unicode.Is(first, c) {
		return false
	}
	for n < len(s) {
		s = s[n:]
		c, n = utf8.DecodeRune(s)
		if c == utf8.RuneError && n == 1 {
			return false
		}
		if !unicode.Is(first, c) && !unicode.Is(second, c) {
			return false
		}
	}
	return true
}

func isNameString(s string) bool {
	if len(s) == 0 {
		return false
	}
	c, n := utf8.DecodeRuneInString(s)
	if c == utf8.RuneError && n == 1 {
		return false
	}
	if !unicode.Is(first, c) {
		return false
	}
	for n < len(s) {
		s = s[n:]
		c, n = utf8.DecodeRuneInString(s)
		if c == utf8.RuneError && n == 1 {
			return false
		}
		if !unicode.Is(first, c) && !unicode.Is(second, c) {
			return false
		}
	}
	return true
}

// 这些表是通过从 https://www.xml.com/axml/testaxml.htm 处
// XML 规范附录 B 剪切粘贴并重新格式化生成的。
// first 对应 (Letter | '_' | ':')，
// second 对应 NameChar。

var first = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x003A, 0x003A, 1},
		{0x0041, 0x005A, 1},
		{0x005F, 0x005F, 1},
		{0x0061, 0x007A, 1},
		{0x00C0, 0x00D6, 1},
		{0x00D8, 0x00F6, 1},
		{0x00F8, 0x00FF, 1},
		{0x0100, 0x0131, 1},
		{0x0134, 0x013E, 1},
		{0x0141, 0x0148, 1},
		{0x014A, 0x017E, 1},
		{0x0180, 0x01C3, 1},
		{0x01CD, 0x01F0, 1},
		{0x01F4, 0x01F5, 1},
		{0x01FA, 0x0217, 1},
		{0x0250, 0x02A8, 1},
		{0x02BB, 0x02C1, 1},
		{0x0386, 0x0386, 1},
		{0x0388, 0x038A, 1},
		{0x038C, 0x038C, 1},
		{0x038E, 0x03A1, 1},
		{0x03A3, 0x03CE, 1},
		{0x03D0, 0x03D6, 1},
		{0x03DA, 0x03E0, 2},
		{0x03E2, 0x03F3, 1},
		{0x0401, 0x040C, 1},
		{0x040E, 0x044F, 1},
		{0x0451, 0x045C, 1},
		{0x045E, 0x0481, 1},
		{0x0490, 0x04C4, 1},
		{0x04C7, 0x04C8, 1},
		{0x04CB, 0x04CC, 1},
		{0x04D0, 0x04EB, 1},
		{0x04EE, 0x04F5, 1},
		{0x04F8, 0x04F9, 1},
		{0x0531, 0x0556, 1},
		{0x0559, 0x0559, 1},
		{0x0561, 0x0586, 1},
		{0x05D0, 0x05EA, 1},
		{0x05F0, 0x05F2, 1},
		{0x0621, 0x063A, 1},
		{0x0641, 0x064A, 1},
		{0x0671, 0x06B7, 1},
		{0x06BA, 0x06BE, 1},
		{0x06C0, 0x06CE, 1},
		{0x06D0, 0x06D3, 1},
		{0x06D5, 0x06D5, 1},
		{0x06E5, 0x06E6, 1},
		{0x0905, 0x0939, 1},
		{0x093D, 0x093D, 1},
		{0x0958, 0x0961, 1},
		{0x0985, 0x098C, 1},
		{0x098F, 0x0990, 1},
		{0x0993, 0x09A8, 1},
		{0x09AA, 0x09B0, 1},
		{0x09B2, 0x09B2, 1},
		{0x09B6, 0x09B9, 1},
		{0x09DC, 0x09DD, 1},
		{0x09DF, 0x09E1, 1},
		{0x09F0, 0x09F1, 1},
		{0x0A05, 0x0A0A, 1},
		{0x0A0F, 0x0A10, 1},
		{0x0A13, 0x0A28, 1},
		{0x0A2A, 0x0A30, 1},
		{0x0A32, 0x0A33, 1},
		{0x0A35, 0x0A36, 1},
		{0x0A38, 0x0A39, 1},
		{0x0A59, 0x0A5C, 1},
		{0x0A5E, 0x0A5E, 1},
		{0x0A72, 0x0A74, 1},
		{0x0A85, 0x0A8B, 1},
		{0x0A8D, 0x0A8D, 1},
		{0x0A8F, 0x0A91, 1},
		{0x0A93, 0x0AA8, 1},
		{0x0AAA, 0x0AB0, 1},
		{0x0AB2, 0x0AB3, 1},
		{0x0AB5, 0x0AB9, 1},
		{0x0ABD, 0x0AE0, 0x23},
		{0x0B05, 0x0B0C, 1},
		{0x0B0F, 0x0B10, 1},
		{0x0B13, 0x0B28, 1},
		{0x0B2A, 0x0B30, 1},
		{0x0B32, 0x0B33, 1},
		{0x0B36, 0x0B39, 1},
		{0x0B3D, 0x0B3D, 1},
		{0x0B5C, 0x0B5D, 1},
		{0x0B5F, 0x0B61, 1},
		{0x0B85, 0x0B8A, 1},
		{0x0B8E, 0x0B90, 1},
		{0x0B92, 0x0B95, 1},
		{0x0B99, 0x0B9A, 1},
		{0x0B9C, 0x0B9C, 1},
		{0x0B9E, 0x0B9F, 1},
		{0x0BA3, 0x0BA4, 1},
		{0x0BA8, 0x0BAA, 1},
		{0x0BAE, 0x0BB5, 1},
		{0x0BB7, 0x0BB9, 1},
		{0x0C05, 0x0C0C, 1},
		{0x0C0E, 0x0C10, 1},
		{0x0C12, 0x0C28, 1},
		{0x0C2A, 0x0C33, 1},
		{0x0C35, 0x0C39, 1},
		{0x0C60, 0x0C61, 1},
		{0x0C85, 0x0C8C, 1},
		{0x0C8E, 0x0C90, 1},
		{0x0C92, 0x0CA8, 1},
		{0x0CAA, 0x0CB3, 1},
		{0x0CB5, 0x0CB9, 1},
		{0x0CDE, 0x0CDE, 1},
		{0x0CE0, 0x0CE1, 1},
		{0x0D05, 0x0D0C, 1},
		{0x0D0E, 0x0D10, 1},
		{0x0D12, 0x0D28, 1},
		{0x0D2A, 0x0D39, 1},
		{0x0D60, 0x0D61, 1},
		{0x0E01, 0x0E2E, 1},
		{0x0E30, 0x0E30, 1},
		{0x0E32, 0x0E33, 1},
		{0x0E40, 0x0E45, 1},
		{0x0E81, 0x0E82, 1},
		{0x0E84, 0x0E84, 1},
		{0x0E87, 0x0E88, 1},
		{0x0E8A, 0x0E8D, 3},
		{0x0E94, 0x0E97, 1},
		{0x0E99, 0x0E9F, 1},
		{0x0EA1, 0x0EA3, 1},
		{0x0EA5, 0x0EA7, 2},
		{0x0EAA, 0x0EAB, 1},
		{0x0EAD, 0x0EAE, 1},
		{0x0EB0, 0x0EB0, 1},
		{0x0EB2, 0x0EB3, 1},
		{0x0EBD, 0x0EBD, 1},
		{0x0EC0, 0x0EC4, 1},
		{0x0F40, 0x0F47, 1},
		{0x0F49, 0x0F69, 1},
		{0x10A0, 0x10C5, 1},
		{0x10D0, 0x10F6, 1},
		{0x1100, 0x1100, 1},
		{0x1102, 0x1103, 1},
		{0x1105, 0x1107, 1},
		{0x1109, 0x1109, 1},
		{0x110B, 0x110C, 1},
		{0x110E, 0x1112, 1},
		{0x113C, 0x1140, 2},
		{0x114C, 0x1150, 2},
		{0x1154, 0x1155, 1},
		{0x1159, 0x1159, 1},
		{0x115F, 0x1161, 1},
		{0x1163, 0x1169, 2},
		{0x116D, 0x116E, 1},
		{0x1172, 0x1173, 1},
		{0x1175, 0x119E, 0x119E - 0x1175},
		{0x11A8, 0x11AB, 0x11AB - 0x11A8},
		{0x11AE, 0x11AF, 1},
		{0x11B7, 0x11B8, 1},
		{0x11BA, 0x11BA, 1},
		{0x11BC, 0x11C2, 1},
		{0x11EB, 0x11F0, 0x11F0 - 0x11EB},
		{0x11F9, 0x11F9, 1},
		{0x1E00, 0x1E9B, 1},
		{0x1EA0, 0x1EF9, 1},
		{0x1F00, 0x1F15, 1},
		{0x1F18, 0x1F1D, 1},
		{0x1F20, 0x1F45, 1},
		{0x1F48, 0x1F4D, 1},
		{0x1F50, 0x1F57, 1},
		{0x1F59, 0x1F5B, 0x1F5B - 0x1F59},
		{0x1F5D, 0x1F5D, 1},
		{0x1F5F, 0x1F7D, 1},
		{0x1F80, 0x1FB4, 1},
		{0x1FB6, 0x1FBC, 1},
		{0x1FBE, 0x1FBE, 1},
		{0x1FC2, 0x1FC4, 1},
		{0x1FC6, 0x1FCC, 1},
		{0x1FD0, 0x1FD3, 1},
		{0x1FD6, 0x1FDB, 1},
		{0x1FE0, 0x1FEC, 1},
		{0x1FF2, 0x1FF4, 1},
		{0x1FF6, 0x1FFC, 1},
		{0x2126, 0x2126, 1},
		{0x212A, 0x212B, 1},
		{0x212E, 0x212E, 1},
		{0x2180, 0x2182, 1},
		{0x3007, 0x3007, 1},
		{0x3021, 0x3029, 1},
		{0x3041, 0x3094, 1},
		{0x30A1, 0x30FA, 1},
		{0x3105, 0x312C, 1},
		{0x4E00, 0x9FA5, 1},
		{0xAC00, 0xD7A3, 1},
	},
}

var second = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x002D, 0x002E, 1},
		{0x0030, 0x0039, 1},
		{0x00B7, 0x00B7, 1},
		{0x02D0, 0x02D1, 1},
		{0x0300, 0x0345, 1},
		{0x0360, 0x0361, 1},
		{0x0387, 0x0387, 1},
		{0x0483, 0x0486, 1},
		{0x0591, 0x05A1, 1},
		{0x05A3, 0x05B9, 1},
		{0x05BB, 0x05BD, 1},
		{0x05BF, 0x05BF, 1},
		{0x05C1, 0x05C2, 1},
		{0x05C4, 0x0640, 0x0640 - 0x05C4},
		{0x064B, 0x0652, 1},
		{0x0660, 0x0669, 1},
		{0x0670, 0x0670, 1},
		{0x06D6, 0x06DC, 1},
		{0x06DD, 0x06DF, 1},
		{0x06E0, 0x06E4, 1},
		{0x06E7, 0x06E8, 1},
		{0x06EA, 0x06ED, 1},
		{0x06F0, 0x06F9, 1},
		{0x0901, 0x0903, 1},
		{0x093C, 0x093C, 1},
		{0x093E, 0x094C, 1},
		{0x094D, 0x094D, 1},
		{0x0951, 0x0954, 1},
		{0x0962, 0x0963, 1},
		{0x0966, 0x096F, 1},
		{0x0981, 0x0983, 1},
		{0x09BC, 0x09BC, 1},
		{0x09BE, 0x09BF, 1},
		{0x09C0, 0x09C4, 1},
		{0x09C7, 0x09C8, 1},
		{0x09CB, 0x09CD, 1},
		{0x09D7, 0x09D7, 1},
		{0x09E2, 0x09E3, 1},
		{0x09E6, 0x09EF, 1},
		{0x0A02, 0x0A3C, 0x3A},
		{0x0A3E, 0x0A3F, 1},
		{0x0A40, 0x0A42, 1},
		{0x0A47, 0x0A48, 1},
		{0x0A4B, 0x0A4D, 1},
		{0x0A66, 0x0A6F, 1},
		{0x0A70, 0x0A71, 1},
		{0x0A81, 0x0A83, 1},
		{0x0ABC, 0x0ABC, 1},
		{0x0ABE, 0x0AC5, 1},
		{0x0AC7, 0x0AC9, 1},
		{0x0ACB, 0x0ACD, 1},
		{0x0AE6, 0x0AEF, 1},
		{0x0B01, 0x0B03, 1},
		{0x0B3C, 0x0B3C, 1},
		{0x0B3E, 0x0B43, 1},
		{0x0B47, 0x0B48, 1},
		{0x0B4B, 0x0B4D, 1},
		{0x0B56, 0x0B57, 1},
		{0x0B66, 0x0B6F, 1},
		{0x0B82, 0x0B83, 1},
		{0x0BBE, 0x0BC2, 1},
		{0x0BC6, 0x0BC8, 1},
		{0x0BCA, 0x0BCD, 1},
		{0x0BD7, 0x0BD7, 1},
		{0x0BE7, 0x0BEF, 1},
		{0x0C01, 0x0C03, 1},
		{0x0C3E, 0x0C44, 1},
		{0x0C46, 0x0C48, 1},
		{0x0C4A, 0x0C4D, 1},
		{0x0C55, 0x0C56, 1},
		{0x0C66, 0x0C6F, 1},
		{0x0C82, 0x0C83, 1},
		{0x0CBE, 0x0CC4, 1},
		{0x0CC6, 0x0CC8, 1},
		{0x0CCA, 0x0CCD, 1},
		{0x0CD5, 0x0CD6, 1},
		{0x0CE6, 0x0CEF, 1},
		{0x0D02, 0x0D03, 1},
		{0x0D3E, 0x0D43, 1},
		{0x0D46, 0x0D48, 1},
		{0x0D4A, 0x0D4D, 1},
		{0x0D57, 0x0D57, 1},
		{0x0D66, 0x0D6F, 1},
		{0x0E31, 0x0E31, 1},
		{0x0E34, 0x0E3A, 1},
		{0x0E46, 0x0E46, 1},
		{0x0E47, 0x0E4E, 1},
		{0x0E50, 0x0E59, 1},
		{0x0EB1, 0x0EB1, 1},
		{0x0EB4, 0x0EB9, 1},
		{0x0EBB, 0x0EBC, 1},
		{0x0EC6, 0x0EC6, 1},
		{0x0EC8, 0x0ECD, 1},
		{0x0ED0, 0x0ED9, 1},
		{0x0F18, 0x0F19, 1},
		{0x0F20, 0x0F29, 1},
		{0x0F35, 0x0F39, 2},
		{0x0F3E, 0x0F3F, 1},
		{0x0F71, 0x0F84, 1},
		{0x0F86, 0x0F8B, 1},
		{0x0F90, 0x0F95, 1},
		{0x0F97, 0x0F97, 1},
		{0x0F99, 0x0FAD, 1},
		{0x0FB1, 0x0FB7, 1},
		{0x0FB9, 0x0FB9, 1},
		{0x20D0, 0x20DC, 1},
		{0x20E1, 0x3005, 0x3005 - 0x20E1},
		{0x302A, 0x302F, 1},
		{0x3031, 0x3035, 1},
		{0x3099, 0x309A, 1},
		{0x309D, 0x309E, 1},
		{0x30FC, 0x30FE, 1},
	},
}

// HTMLEntity 是一个实体映射，包含标准 HTML 实体字符的
// 翻译。
//
// 参见 [Decoder.Strict] 和 [Decoder.Entity] 字段的文档。
var HTMLEntity map[string]string = htmlEntity

var htmlEntity = map[string]string{
	/*
		hget http://www.w3.org/TR/html4/sgml/entities.html |
		ssam '
			,y /\&gt;/ x/\&lt;(.|\n)+/ s/\n/ /g
			,x v/^\&lt;!ENTITY/d
			,s/\&lt;!ENTITY ([^ ]+) .*U\+([0-9A-F][0-9A-F][0-9A-F][0-9A-F]) .+/	"\1": "\\u\2",/g
		'
	*/
	"nbsp":     "\u00A0",
	"iexcl":    "\u00A1",
	"cent":     "\u00A2",
	"pound":    "\u00A3",
	"curren":   "\u00A4",
	"yen":      "\u00A5",
	"brvbar":   "\u00A6",
	"sect":     "\u00A7",
	"uml":      "\u00A8",
	"copy":     "\u00A9",
	"ordf":     "\u00AA",
	"laquo":    "\u00AB",
	"not":      "\u00AC",
	"shy":      "\u00AD",
	"reg":      "\u00AE",
	"macr":     "\u00AF",
	"deg":      "\u00B0",
	"plusmn":   "\u00B1",
	"sup2":     "\u00B2",
	"sup3":     "\u00B3",
	"acute":    "\u00B4",
	"micro":    "\u00B5",
	"para":     "\u00B6",
	"middot":   "\u00B7",
	"cedil":    "\u00B8",
	"sup1":     "\u00B9",
	"ordm":     "\u00BA",
	"raquo":    "\u00BB",
	"frac14":   "\u00BC",
	"frac12":   "\u00BD",
	"frac34":   "\u00BE",
	"iquest":   "\u00BF",
	"Agrave":   "\u00C0",
	"Aacute":   "\u00C1",
	"Acirc":    "\u00C2",
	"Atilde":   "\u00C3",
	"Auml":     "\u00C4",
	"Aring":    "\u00C5",
	"AElig":    "\u00C6",
	"Ccedil":   "\u00C7",
	"Egrave":   "\u00C8",
	"Eacute":   "\u00C9",
	"Ecirc":    "\u00CA",
	"Euml":     "\u00CB",
	"Igrave":   "\u00CC",
	"Iacute":   "\u00CD",
	"Icirc":    "\u00CE",
	"Iuml":     "\u00CF",
	"ETH":      "\u00D0",
	"Ntilde":   "\u00D1",
	"Ograve":   "\u00D2",
	"Oacute":   "\u00D3",
	"Ocirc":    "\u00D4",
	"Otilde":   "\u00D5",
	"Ouml":     "\u00D6",
	"times":    "\u00D7",
	"Oslash":   "\u00D8",
	"Ugrave":   "\u00D9",
	"Uacute":   "\u00DA",
	"Ucirc":    "\u00DB",
	"Uuml":     "\u00DC",
	"Yacute":   "\u00DD",
	"THORN":    "\u00DE",
	"szlig":    "\u00DF",
	"agrave":   "\u00E0",
	"aacute":   "\u00E1",
	"acirc":    "\u00E2",
	"atilde":   "\u00E3",
	"auml":     "\u00E4",
	"aring":    "\u00E5",
	"aelig":    "\u00E6",
	"ccedil":   "\u00E7",
	"egrave":   "\u00E8",
	"eacute":   "\u00E9",
	"ecirc":    "\u00EA",
	"euml":     "\u00EB",
	"igrave":   "\u00EC",
	"iacute":   "\u00ED",
	"icirc":    "\u00EE",
	"iuml":     "\u00EF",
	"eth":      "\u00F0",
	"ntilde":   "\u00F1",
	"ograve":   "\u00F2",
	"oacute":   "\u00F3",
	"ocirc":    "\u00F4",
	"otilde":   "\u00F5",
	"ouml":     "\u00F6",
	"divide":   "\u00F7",
	"oslash":   "\u00F8",
	"ugrave":   "\u00F9",
	"uacute":   "\u00FA",
	"ucirc":    "\u00FB",
	"uuml":     "\u00FC",
	"yacute":   "\u00FD",
	"thorn":    "\u00FE",
	"yuml":     "\u00FF",
	"fnof":     "\u0192",
	"Alpha":    "\u0391",
	"Beta":     "\u0392",
	"Gamma":    "\u0393",
	"Delta":    "\u0394",
	"Epsilon":  "\u0395",
	"Zeta":     "\u0396",
	"Eta":      "\u0397",
	"Theta":    "\u0398",
	"Iota":     "\u0399",
	"Kappa":    "\u039A",
	"Lambda":   "\u039B",
	"Mu":       "\u039C",
	"Nu":       "\u039D",
	"Xi":       "\u039E",
	"Omicron":  "\u039F",
	"Pi":       "\u03A0",
	"Rho":      "\u03A1",
	"Sigma":    "\u03A3",
	"Tau":      "\u03A4",
	"Upsilon":  "\u03A5",
	"Phi":      "\u03A6",
	"Chi":      "\u03A7",
	"Psi":      "\u03A8",
	"Omega":    "\u03A9",
	"alpha":    "\u03B1",
	"beta":     "\u03B2",
	"gamma":    "\u03B3",
	"delta":    "\u03B4",
	"epsilon":  "\u03B5",
	"zeta":     "\u03B6",
	"eta":      "\u03B7",
	"theta":    "\u03B8",
	"iota":     "\u03B9",
	"kappa":    "\u03BA",
	"lambda":   "\u03BB",
	"mu":       "\u03BC",
	"nu":       "\u03BD",
	"xi":       "\u03BE",
	"omicron":  "\u03BF",
	"pi":       "\u03C0",
	"rho":      "\u03C1",
	"sigmaf":   "\u03C2",
	"sigma":    "\u03C3",
	"tau":      "\u03C4",
	"upsilon":  "\u03C5",
	"phi":      "\u03C6",
	"chi":      "\u03C7",
	"psi":      "\u03C8",
	"omega":    "\u03C9",
	"thetasym": "\u03D1",
	"upsih":    "\u03D2",
	"piv":      "\u03D6",
	"bull":     "\u2022",
	"hellip":   "\u2026",
	"prime":    "\u2032",
	"Prime":    "\u2033",
	"oline":    "\u203E",
	"frasl":    "\u2044",
	"weierp":   "\u2118",
	"image":    "\u2111",
	"real":     "\u211C",
	"trade":    "\u2122",
	"alefsym":  "\u2135",
	"larr":     "\u2190",
	"uarr":     "\u2191",
	"rarr":     "\u2192",
	"darr":     "\u2193",
	"harr":     "\u2194",
	"crarr":    "\u21B5",
	"lArr":     "\u21D0",
	"uArr":     "\u21D1",
	"rArr":     "\u21D2",
	"dArr":     "\u21D3",
	"hArr":     "\u21D4",
	"forall":   "\u2200",
	"part":     "\u2202",
	"exist":    "\u2203",
	"empty":    "\u2205",
	"nabla":    "\u2207",
	"isin":     "\u2208",
	"notin":    "\u2209",
	"ni":       "\u220B",
	"prod":     "\u220F",
	"sum":      "\u2211",
	"minus":    "\u2212",
	"lowast":   "\u2217",
	"radic":    "\u221A",
	"prop":     "\u221D",
	"infin":    "\u221E",
	"ang":      "\u2220",
	"and":      "\u2227",
	"or":       "\u2228",
	"cap":      "\u2229",
	"cup":      "\u222A",
	"int":      "\u222B",
	"there4":   "\u2234",
	"sim":      "\u223C",
	"cong":     "\u2245",
	"asymp":    "\u2248",
	"ne":       "\u2260",
	"equiv":    "\u2261",
	"le":       "\u2264",
	"ge":       "\u2265",
	"sub":      "\u2282",
	"sup":      "\u2283",
	"nsub":     "\u2284",
	"sube":     "\u2286",
	"supe":     "\u2287",
	"oplus":    "\u2295",
	"otimes":   "\u2297",
	"perp":     "\u22A5",
	"sdot":     "\u22C5",
	"lceil":    "\u2308",
	"rceil":    "\u2309",
	"lfloor":   "\u230A",
	"rfloor":   "\u230B",
	"lang":     "\u2329",
	"rang":     "\u232A",
	"loz":      "\u25CA",
	"spades":   "\u2660",
	"clubs":    "\u2663",
	"hearts":   "\u2665",
	"diams":    "\u2666",
	"quot":     "\u0022",
	"amp":      "\u0026",
	"lt":       "\u003C",
	"gt":       "\u003E",
	"OElig":    "\u0152",
	"oelig":    "\u0153",
	"Scaron":   "\u0160",
	"scaron":   "\u0161",
	"Yuml":     "\u0178",
	"circ":     "\u02C6",
	"tilde":    "\u02DC",
	"ensp":     "\u2002",
	"emsp":     "\u2003",
	"thinsp":   "\u2009",
	"zwnj":     "\u200C",
	"zwj":      "\u200D",
	"lrm":      "\u200E",
	"rlm":      "\u200F",
	"ndash":    "\u2013",
	"mdash":    "\u2014",
	"lsquo":    "\u2018",
	"rsquo":    "\u2019",
	"sbquo":    "\u201A",
	"ldquo":    "\u201C",
	"rdquo":    "\u201D",
	"bdquo":    "\u201E",
	"dagger":   "\u2020",
	"Dagger":   "\u2021",
	"permil":   "\u2030",
	"lsaquo":   "\u2039",
	"rsaquo":   "\u203A",
	"euro":     "\u20AC",
}

// HTMLAutoClose 是一组 HTML 元素，
// 它们应被视为自动关闭。
//
// 参见 [Decoder.Strict] 和 [Decoder.Entity] 字段的文档。
var HTMLAutoClose []string = htmlAutoClose

var htmlAutoClose = []string{
	/*
		hget http://www.w3.org/TR/html4/loose.dtd |
		9 sed -n 's/<!ELEMENT ([^ ]*) +- O EMPTY.+/	"\1",/p' | tr A-Z a-z
	*/
	"basefont",
	"br",
	"area",
	"link",
	"img",
	"param",
	"hr",
	"input",
	"col",
	"frame",
	"isindex",
	"base",
	"meta",
}

var (
	escQuot = []byte("&#34;") // 比 "&quot;" 短
	escApos = []byte("&#39;") // 比 "&apos;" 短
	escAmp  = []byte("&amp;")
	escLT   = []byte("&lt;")
	escGT   = []byte("&gt;")
	escTab  = []byte("&#x9;")
	escNL   = []byte("&#xA;")
	escCR   = []byte("&#xD;")
	escFFFD = []byte("\uFFFD") // Unicode 替换字符
)

// EscapeText 将纯文本数据 s 的正确转义后的 XML 等价形式
// 写入 w。
func EscapeText(w io.Writer, s []byte) error {
	return escapeText(w, s, true)
}

// escapeText 将纯文本数据 s 的正确转义后的 XML 等价形式
// 写入 w。如果 escapeNewline 为 true，换行
// 字符将被转义。
func escapeText(w io.Writer, s []byte, escapeNewline bool) error {
	var esc []byte
	last := 0
	for i := 0; i < len(s); {
		r, width := utf8.DecodeRune(s[i:])
		i += width
		switch r {
		case '"':
			esc = escQuot
		case '\'':
			esc = escApos
		case '&':
			esc = escAmp
		case '<':
			esc = escLT
		case '>':
			esc = escGT
		case '\t':
			esc = escTab
		case '\n':
			if !escapeNewline {
				continue
			}
			esc = escNL
		case '\r':
			esc = escCR
		default:
			if !isInCharacterRange(r) || (r == 0xFFFD && width == 1) {
				esc = escFFFD
				break
			}
			continue
		}
		if _, err := w.Write(s[last : i-width]); err != nil {
			return err
		}
		if _, err := w.Write(esc); err != nil {
			return err
		}
		last = i
	}
	_, err := w.Write(s[last:])
	return err
}

// EscapeString 将纯文本数据 s 的正确转义后的 XML 等价形式
// 写入 p。
func (p *printer) EscapeString(s string) {
	var esc []byte
	last := 0
	for i := 0; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		i += width
		switch r {
		case '"':
			esc = escQuot
		case '\'':
			esc = escApos
		case '&':
			esc = escAmp
		case '<':
			esc = escLT
		case '>':
			esc = escGT
		case '\t':
			esc = escTab
		case '\n':
			esc = escNL
		case '\r':
			esc = escCR
		default:
			if !isInCharacterRange(r) || (r == 0xFFFD && width == 1) {
				esc = escFFFD
				break
			}
			continue
		}
		p.WriteString(s[last : i-width])
		p.Write(esc)
		last = i
	}
	p.WriteString(s[last:])
}

// Escape 类似 [EscapeText]，但省略错误返回值。
// 提供它是为了与 Go 1.0 向后兼容。
// 面向 Go 1.1 及更高版本的代码应使用 [EscapeText]。
func Escape(w io.Writer, s []byte) {
	EscapeText(w, s)
}

var (
	cdataStart  = []byte("<![CDATA[")
	cdataEnd    = []byte("]]>")
	cdataEscape = []byte("]]]]><![CDATA[>")
)

// emitCDATA 将以 CDATA 包裹的纯文本数据 s 写入 w。
// 它会转义嵌套在 s 中的 CDATA 指令。
func emitCDATA(w io.Writer, s []byte) error {
	if len(s) == 0 {
		return nil
	}
	if _, err := w.Write(cdataStart); err != nil {
		return err
	}

	for {
		before, after, ok := bytes.Cut(s, cdataEnd)
		if !ok {
			break
		}
		// 找到嵌套的 CDATA 指令结尾。
		if _, err := w.Write(before); err != nil {
			return err
		}
		if _, err := w.Write(cdataEscape); err != nil {
			return err
		}
		s = after
	}

	if _, err := w.Write(s); err != nil {
		return err
	}

	_, err := w.Write(cdataEnd)
	return err
}

// procInst 从提供的字符串中解析出 `param="..."` 或 `param='...'`
// 的值，如果未找到则返回 ""。
func procInst(param, s string) string {
	// TODO：这种解析有些简陋且不精确。
	// 不过它对所有实际情况都有效。
	param = param + "="
	lenp := len(param)
	i := 0
	var sep byte
	for i < len(s) {
		sub := s[i:]
		k := strings.Index(sub, param)
		if k < 0 || lenp+k >= len(sub) {
			return ""
		}
		i += lenp + k + 1
		if c := sub[lenp+k]; c == '\'' || c == '"' {
			sep = c
			break
		}
	}
	if sep == 0 {
		return ""
	}
	j := strings.IndexByte(s[i:], sep)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}
