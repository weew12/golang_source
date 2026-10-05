// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"bytes"
	"io"
	"math/bits"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
)

// Encoder 是一个从原始 JSON token 和值进行编码的流式编码器。
// 它用于写入一系列顶层 JSON 值，
// 每个值以换行符结尾。
//
// [Encoder.WriteToken] 和 [Encoder.WriteValue] 调用可以交错进行。
// 例如，以下 JSON 值：
//
//	{"name":"value","array":[null,false,true,3.14159],"object":{"k":"v"}}
//
// 可以通过以下调用组合而成（为简洁起见忽略错误）：
//
//	e.WriteToken(BeginObject)        // {
//	e.WriteToken(String("name"))     // "name"
//	e.WriteToken(String("value"))    // "value"
//	e.WriteValue(Value(`"array"`))   // "array"
//	e.WriteToken(BeginArray)         // [
//	e.WriteToken(Null)               // null
//	e.WriteToken(False)              // false
//	e.WriteValue(Value("true"))      // true
//	e.WriteToken(Float(3.14159))     // 3.14159
//	e.WriteToken(EndArray)           // ]
//	e.WriteValue(Value(`"object"`))  // "object"
//	e.WriteValue(Value(`{"k":"v"}`)) // {"k":"v"}
//	e.WriteToken(EndObject)          // }
//
// 以上是众多可能调用序列之一，
// 对于任何给定的 token/值，它可能并非最合理的调用方法。
// 例如，更常见的做法可能是用字符串调用 [Encoder.WriteToken]
// 来表示对象名。
type Encoder struct {
	s encoderState
}

// encoderState 是 Encoder 的底层状态。
// 它暴露了一些字段和方法供 "json" 包使用。
type encoderState struct {
	state
	encodeBuffer
	jsonopts.Struct

	SeenPointers map[any]struct{} // 仅在 marshaling 时使用；与 json.seenPointers 相同
}

// encodeBuffer 是一个被分为 2 段的缓冲区：
//
//   - buf[0:len(buf)]        // 缓冲区已写入（但未 flush）的部分
//   - buf[len(buf):cap(buf)] // 缓冲区未使用的部分
type encodeBuffer struct {
	Buf []byte // 若是 bytes.Buffer，则可能与之共享底层存储

	// baseOffset 加到 len(buf) 上可得到相对于
	// io.Writer 流起点的绝对偏移量。
	baseOffset int64

	wr io.Writer

	// maxValue 是传给 WriteValue 的 Value 近似最大大小。
	maxValue int
	// availBuffer 是 AvailableBuffer 方法返回的缓冲区。
	availBuffer []byte // 长度始终为零
	// bufStats 是关于缓冲区利用率的统计信息。
	// 它仅用于 pools.go 中的池化编码器。
	bufStats bufferStatistics
}

// NewEncoder 构造一个新的流式编码器，写入到 w，
// 并使用提供的选项进行配置。
// 当缓冲区足够满或已写入一个顶层值时，
// 它会 flush 内部缓冲区。
//
// 如果 w 是 [bytes.Buffer]，则编码器会直接追加到该缓冲区中，
// 而不从中间缓冲区复制内容。
func NewEncoder(w io.Writer, opts ...Options) *Encoder {
	e := new(Encoder)
	e.Reset(w, opts...)
	return e
}

// Reset 重置编码器，使其重新向 w 写入，
// 并使用提供的选项进行配置。不得对已传给
// [encoding/json/v2.MarshalerTo.MarshalJSONTo] 方法
// 或 [encoding/json/v2.MarshalToFunc] 函数的 Encoder 调用 Reset。
func (e *Encoder) Reset(w io.Writer, opts ...Options) {
	switch {
	case e == nil:
		panic("jsontext: invalid nil Encoder")
	case w == nil:
		panic("jsontext: invalid nil io.Writer")
	case e.s.Flags.Get(jsonflags.WithinArshalCall):
		panic("jsontext: cannot reset Encoder passed to json.MarshalerTo")
	}
	// 如果缓冲区不与先前的 [bytes.Buffer] 共享底层存储，则复用它。
	b := e.s.Buf[:0]
	if _, ok := e.s.wr.(*bytes.Buffer); ok {
		b = nil
	}
	e.s.reset(b, w, opts...)
}

func (e *encoderState) reset(b []byte, w io.Writer, opts ...Options) {
	e.state.reset()
	e.encodeBuffer = encodeBuffer{Buf: b, wr: w, availBuffer: e.availBuffer, bufStats: e.bufStats}
	if bb, ok := w.(*bytes.Buffer); ok && bb != nil {
		e.Buf = bb.AvailableBuffer() // 为 bb 的未使用缓冲区建立别名
	}
	opts2 := jsonopts.Struct{} // 避免修改 e.Struct，以防它是 opts 的一部分
	opts2.Join(opts...)
	e.Struct = opts2
	if e.Flags.Get(jsonflags.Multiline) {
		if !e.Flags.Has(jsonflags.SpaceAfterColon) {
			e.Flags.Set(jsonflags.SpaceAfterColon | 1)
		}
		if !e.Flags.Has(jsonflags.SpaceAfterComma) {
			e.Flags.Set(jsonflags.SpaceAfterComma | 0)
		}
		if !e.Flags.Has(jsonflags.Indent) {
			e.Flags.Set(jsonflags.Indent | 1)
			e.Indent = "\t"
		}
	}
}

// Options 返回用于构造解码器的选项，
// 还可能包含传给 [encoding/json/v2.MarshalEncode] 调用的语义选项。
//
// 如果在 [encoding/json/v2.MarshalerTo.MarshalJSONTo] 方法调用
// 或 [encoding/json/v2.MarshalToFunc] 函数调用内操作，
// 则返回的选项仅在该调用内有效。
func (e *Encoder) Options() Options {
	return &e.s.Struct
}

// NeedFlush 判断此刻是否应该 flush。
func (e *encoderState) NeedFlush() bool {
	// NOTE: 此函数经过精心编写以保证可内联。

	// 如果 e.wr 为 nil 则避免 flush，因为没有底层 writer。
	// 如果剩余容量少于 25% 则 flush。
	// 以某个固定比例进行 flush 可确保只要最大的 Token 或 Value
	// 能放入未使用的容量中，缓冲区就会停止增长。
	return e.wr != nil && (e.Tokens.Depth() == 1 || len(e.Buf) > 3*cap(e.Buf)/4)
}

// Flush 将缓冲区 flush 到底层 io.Writer。
// 它可能会在顶层值之后追加一个结尾换行符。
func (e *encoderState) Flush() error {
	if e.wr == nil || e.avoidFlush() {
		return nil
	}

	// 在流式模式下，始终在顶层值之后输出一个换行符。
	if e.Tokens.Depth() == 1 && !e.Flags.Get(jsonflags.OmitTopLevelNewline) {
		e.Buf = append(e.Buf, '\n')
	}

	// 通知 objectNameStack 我们即将 flush 缓冲区内容。
	e.Names.copyQuotedBuffer(e.Buf)

	// 针对 bytes.Buffer 做特殊处理以获得更好的性能。
	if bb, ok := e.wr.(*bytes.Buffer); ok {
		// 如果 e.buf 已经与 bb 的内部缓冲区共享底层存储，
		// 那么 Write 调用只是递增内部偏移量，
		// 否则 Write 按预期工作。
		// 参见 https://go.dev/issue/42986。
		n, _ := bb.Write(e.Buf) // 除非 bb 为 nil，否则绝不会失败
		e.baseOffset += int64(n)

		// 如果 bytes.Buffer 的内部缓冲区太小，
		// Encoder 中其他地方的 append 操作可能会使缓冲区增长。
		// 这在语义上是正确的，但会损害性能。
		// 因此，确保当前长度的 25% 始终可用，
		// 以降低其他 append 必须分配内存的概率。
		if avail := bb.Available(); avail < bb.Len()/4 {
			bb.Grow(avail + 1)
		}

		e.Buf = bb.AvailableBuffer()
		return nil
	}

	// 将内部缓冲区 flush 到底层 io.Writer。
	n, err := e.wr.Write(e.Buf)
	e.baseOffset += int64(n)
	if err != nil {
		// 在发生错误的情况下，保留未 flush 的部分。
		// 因此，只要 io.Writer 在错误之后保持一致的状态，
		// 写入错误就不是致命的。
		if n > 0 {
			e.Buf = e.Buf[:copy(e.Buf, e.Buf[n:])]
		}
		return &ioError{action: "write", err: err}
	}
	e.Buf = e.Buf[:0]

	// 检查是否要增长缓冲区。
	// 注意 cap(e.buf) 可能已经超过 maxBufferSize，因为
	// 其他地方的 append 已经将其增长以存储一个大的 token。
	const maxBufferSize = 4 << 10
	const growthSizeFactor = 2 // 值越大越快
	const growthRateFactor = 2 // 值越大越慢
	// 默认情况下，如果低于最大缓冲区大小就增长。
	grow := cap(e.Buf) <= maxBufferSize/growthSizeFactor
	// 增长可能很昂贵，因此仅当已处理的字节数足够多时才增长。
	grow = grow && int64(cap(e.Buf)) < e.previousOffsetEnd()/growthRateFactor
	if grow {
		e.Buf = make([]byte, 0, cap(e.Buf)*growthSizeFactor)
	}

	return nil
}
func (d *encodeBuffer) offsetAt(pos int) int64   { return d.baseOffset + int64(pos) }
func (e *encodeBuffer) previousOffsetEnd() int64 { return e.baseOffset + int64(len(e.Buf)) }
func (e *encodeBuffer) unflushedBuffer() []byte  { return e.Buf }

// avoidFlush 指示是否避免 flush，以确保缓冲区中始终有
// 足够的空间，从而在最后一个对象成员为空时能够撤销写入。
func (e *encoderState) avoidFlush() bool {
	switch {
	case e.Tokens.Last.Length() == 0:
		// 绝不在 BeginObject 或 BeginArray 之后 flush，因为我们还不知道
		// 该对象或数组最终是否为空。
		return true
	case e.Tokens.Last.needObjectValue():
		// 绝不在对象值之前 flush，因为我们还不知道
		// 该对象值最终是否为空。
		return true
	case e.Tokens.Last.NeedObjectName() && len(e.Buf) >= 2:
		// 绝不在对象值之后 flush，以防它最终确实为空。
		switch string(e.Buf[len(e.Buf)-2:]) {
		case `ll`, `""`, `{}`, `[]`: // 每个空值的最后两个字节
			return true
		}
	}
	return false
}

// UnwriteEmptyObjectMember 在最后一个对象成员为空时撤销其写入，
// 并报告是否执行了撤销操作。
func (e *encoderState) UnwriteEmptyObjectMember(prevName *string) bool {
	if last := e.Tokens.Last; !last.isObject() || !last.NeedObjectName() || last.Length() == 0 {
		panic("BUG: must be called on an object after writing a value")
	}

	// flush 逻辑经过修改，绝不 flush 尾部的空值。
	// 编码器从不急切地写入尾部空白。
	b := e.unflushedBuffer()

	// 检测最后一个值是否为空。
	var n int
	if len(b) >= 3 {
		switch string(b[len(b)-2:]) {
		case "ll": // `null` 的最后两个字节
			n = len(`null`)
		case `""`:
			// 如果倒数第二个引号被转义，非空字符串也可能以 `""` 作为后缀。
			if b[len(b)-3] == '\\' {
				return false // 例如，`"\""` 并非空值
			}
			n = len(`""`)
		case `{}`:
			n = len(`{}`)
		case `[]`:
			n = len(`[]`)
		}
	}
	if n == 0 {
		return false
	}

	// 撤销值、空白、冒号、名称、空白和逗号的写入。
	b = b[:len(b)-n]
	b = jsonwire.TrimSuffixWhitespace(b)
	b = jsonwire.TrimSuffixByte(b, ':')
	b = jsonwire.TrimSuffixString(b)
	b = jsonwire.TrimSuffixWhitespace(b)
	b = jsonwire.TrimSuffixByte(b, ',')
	e.Buf = b // 将截断后的未 flush 缓冲区存回

	// 撤销状态变更。
	e.Tokens.Last.decrement() // 针对对象成员值
	e.Tokens.Last.decrement() // 针对对象成员名
	if !e.Flags.Get(jsonflags.AllowDuplicateNames) {
		if e.Tokens.Last.isActiveNamespace() {
			e.Namespaces.Last().removeLast()
		}
	}
	e.Names.clearLast()
	if prevName != nil {
		e.Names.copyQuotedBuffer(e.Buf) // objectNameStack.replaceLastUnquotedName 所需
		e.Names.replaceLastUnquotedName(*prevName)
	}
	return true
}

// UnwriteOnlyObjectMemberName 撤销唯一的对象成员名的写入
// 并返回去引号后的名称。
func (e *encoderState) UnwriteOnlyObjectMemberName() string {
	if last := e.Tokens.Last; !last.isObject() || last.Length() != 1 {
		panic("BUG: must be called on an object after writing first name")
	}

	// 撤销名称和空白的写入。
	b := jsonwire.TrimSuffixString(e.Buf)
	isVerbatim := bytes.IndexByte(e.Buf[len(b):], '\\') < 0
	name := string(jsonwire.UnquoteMayCopy(e.Buf[len(b):], isVerbatim))
	e.Buf = jsonwire.TrimSuffixWhitespace(b)

	// 撤销状态变更。
	e.Tokens.Last.decrement()
	if !e.Flags.Get(jsonflags.AllowDuplicateNames) {
		if e.Tokens.Last.isActiveNamespace() {
			e.Namespaces.Last().removeLast()
		}
	}
	e.Names.clearLast()
	return name
}

// WriteToken 写入下一个 token 并推进内部写入偏移量。
//
// 提供的 token 种类必须与 JSON 语法一致。
// 例如，当编码器期望一个对象名（它总是字符串）时提供数字，
// 或者当编码器正在结束数组时提供结束对象分隔符，
// 都是错误。
// 如果提供的 token 无效，则它报告一个 [SyntacticError]，
// 且内部状态保持不变。[SyntacticError] 中报告的偏移量
// 将相对于 [Encoder.OutputOffset]。
func (e *Encoder) WriteToken(t Token) error {
	return e.s.WriteToken(t)
}
func (e *encoderState) WriteToken(t Token) error {
	k := t.Kind()
	b := e.Buf // 使用局部变量以避免在出错时改动 e

	// 追加任何分隔符或可选空白。
	b = e.Tokens.MayAppendDelim(b, k)
	if e.Flags.Get(jsonflags.AnyWhitespace) {
		b = e.appendWhitespace(b, k)
	}
	pos := len(b) // token 之前的偏移量

	// 将 token 追加到输出和状态机。
	var err error
	switch k {
	case 'n':
		b = append(b, "null"...)
		err = e.Tokens.appendLiteral()
	case 'f':
		b = append(b, "false"...)
		err = e.Tokens.appendLiteral()
	case 't':
		b = append(b, "true"...)
		err = e.Tokens.appendLiteral()
	case '"':
		if b, err = t.appendString(b, &e.Flags); err != nil {
			break
		}
		if e.Tokens.Last.NeedObjectName() {
			if !e.Flags.Get(jsonflags.AllowDuplicateNames) {
				if !e.Tokens.Last.isValidNamespace() {
					err = errInvalidNamespace
					break
				}
				if e.Tokens.Last.isActiveNamespace() && !e.Namespaces.Last().insertQuoted(b[pos:], false) {
					err = wrapWithObjectName(ErrDuplicateName, b[pos:])
					break
				}
			}
			e.Names.ReplaceLastQuotedOffset(pos) // 仅在 insertQuoted 成功时替换
		}
		err = e.Tokens.appendString()
	case '0':
		if b, err = t.appendNumber(b, &e.Flags); err != nil {
			break
		}
		err = e.Tokens.appendNumber()
	case '{':
		b = append(b, '{')
		if err = e.Tokens.pushObject(); err != nil {
			break
		}
		e.Names.push()
		if !e.Flags.Get(jsonflags.AllowDuplicateNames) {
			e.Namespaces.push()
		}
	case '}':
		b = append(b, '}')
		if err = e.Tokens.popObject(); err != nil {
			break
		}
		e.Names.pop()
		if !e.Flags.Get(jsonflags.AllowDuplicateNames) {
			e.Namespaces.pop()
		}
	case '[':
		b = append(b, '[')
		err = e.Tokens.pushArray()
	case ']':
		b = append(b, ']')
		err = e.Tokens.popArray()
	default:
		err = errInvalidToken
	}
	if err != nil {
		return wrapSyntacticError(e, err, pos, +1)
	}

	// 收尾缓冲区并将其存回 e。
	e.Buf = b
	if e.NeedFlush() {
		return e.Flush()
	}
	return nil
}

// AppendRaw 追加一个原始字符串（不带双引号）或数字。
// 如果字符串输出保证为 ASCII，且不包含任何需要转义的字符
// （包括 '<'、'>' 和 '&'），则指定 safeASCII，
// 否则将校验该字符串是否需要转义。
// 为 JSON 数字追加的字节必须有效。
//
// 这是 Encoder.WriteValue 的一种专用实现，
// 允许直接追加到缓冲区中。
// 它仅由 "json" 包中的 marshal 逻辑调用。
func (e *encoderState) AppendRaw(k Kind, safeASCII bool, appendFn func([]byte) ([]byte, error)) error {
	b := e.Buf // 使用局部变量以避免在出错时改动 e

	// 追加任何分隔符或可选空白。
	b = e.Tokens.MayAppendDelim(b, k)
	if e.Flags.Get(jsonflags.AnyWhitespace) {
		b = e.appendWhitespace(b, k)
	}
	pos := len(b) // token 之前的偏移量

	var err error
	switch k {
	case '"':
		// 直接追加到编码器缓冲区中，假设
		// 大多数时候没有任何字符需要转义。
		b = append(b, '"')
		if b, err = appendFn(b); err != nil {
			return err
		}
		b = append(b, '"')

		// 检查是否需要转义该字符串，如果需要，
		// 则将其复制到暂存缓冲区，然后再转义回写。
		isVerbatim := safeASCII || !jsonwire.NeedEscape(b[pos+len(`"`):len(b)-len(`"`)])
		if !isVerbatim {
			var err error
			b2 := append(e.availBuffer, b[pos+len(`"`):len(b)-len(`"`)]...)
			b, err = jsonwire.AppendQuote(b[:pos], string(b2), &e.Flags)
			e.availBuffer = b2[:0]
			if err != nil {
				return wrapSyntacticError(e, err, pos, +1)
			}
		}

		// 更新状态机。
		if e.Tokens.Last.NeedObjectName() {
			if !e.Flags.Get(jsonflags.AllowDuplicateNames) {
				if !e.Tokens.Last.isValidNamespace() {
					return wrapSyntacticError(e, err, pos, +1)
				}
				if e.Tokens.Last.isActiveNamespace() && !e.Namespaces.Last().insertQuoted(b[pos:], isVerbatim) {
					err = wrapWithObjectName(ErrDuplicateName, b[pos:])
					return wrapSyntacticError(e, err, pos, +1)
				}
			}
			e.Names.ReplaceLastQuotedOffset(pos) // 仅在 insertQuoted 成功时替换
		}
		if err := e.Tokens.appendString(); err != nil {
			return wrapSyntacticError(e, err, pos, +1)
		}
	case '0':
		if b, err = appendFn(b); err != nil {
			return err
		}
		if err := e.Tokens.appendNumber(); err != nil {
			return wrapSyntacticError(e, err, pos, +1)
		}
	default:
		panic("BUG: invalid kind")
	}

	// 收尾缓冲区并将其存回 e。
	e.Buf = b
	if e.NeedFlush() {
		return e.Flush()
	}
	return nil
}

// WriteValue 写入下一个原始值并推进内部写入偏移量。
// Encoder 不会简单地逐字复制提供的值，而是
// 解析它以确认其在语法上有效，并
// 根据 Encoder 配置的空白和字符串格式重新格式化它。
// 如果指定了 [AllowInvalidUTF8]，则任何无效的 UTF-8 都会被改写
// 为 Unicode 替换字符 U+FFFD。
//
// 提供的值种类必须与 JSON 语法一致
// （参见 [Encoder.WriteToken] 上的示例）。如果提供的值无效，
// 则它报告一个 [SyntacticError]，且内部状态保持不变。
// [SyntacticError] 中报告的偏移量将相对于
// [Encoder.OutputOffset] 再加上遇到任何语法错误时在 v 中的偏移量。
func (e *Encoder) WriteValue(v Value) error {
	return e.s.WriteValue(v)
}
func (e *encoderState) WriteValue(v Value) error {
	e.maxValue |= len(v) // 按位或是对最大值的一种快速近似

	k := v.Kind()
	b := e.Buf // 使用局部变量以避免在出错时改动 e

	// 追加任何分隔符或可选空白。
	b = e.Tokens.MayAppendDelim(b, k)
	if e.Flags.Get(jsonflags.AnyWhitespace) {
		b = e.appendWhitespace(b, k)
	}
	pos := len(b) // value 之前的偏移量

	// 将值追加到输出。
	var n int
	n += jsonwire.ConsumeWhitespace(v[n:])
	b, m, err := e.reformatValue(b, v[n:], e.Tokens.Depth())
	if err != nil {
		return wrapSyntacticError(e, err, pos+n+m, +1)
	}
	n += m
	n += jsonwire.ConsumeWhitespace(v[n:])
	if len(v) > n {
		err = jsonwire.NewInvalidCharacterError(v[n:], "after top-level value")
		return wrapSyntacticError(e, err, pos+n, 0)
	}

	// 将种类追加到状态机。
	switch k {
	case 'n', 'f', 't':
		err = e.Tokens.appendLiteral()
	case '"':
		if e.Tokens.Last.NeedObjectName() {
			if !e.Flags.Get(jsonflags.AllowDuplicateNames) {
				if !e.Tokens.Last.isValidNamespace() {
					err = errInvalidNamespace
					break
				}
				if e.Tokens.Last.isActiveNamespace() && !e.Namespaces.Last().insertQuoted(b[pos:], false) {
					err = wrapWithObjectName(ErrDuplicateName, b[pos:])
					break
				}
			}
			e.Names.ReplaceLastQuotedOffset(pos) // 仅在 insertQuoted 成功时替换
		}
		err = e.Tokens.appendString()
	case '0':
		err = e.Tokens.appendNumber()
	case '{':
		if err = e.Tokens.pushObject(); err != nil {
			break
		}
		if err = e.Tokens.popObject(); err != nil {
			panic("BUG: popObject should never fail immediately after pushObject: " + err.Error())
		}
		if e.Flags.Get(jsonflags.ReorderRawObjects) {
			mustReorderObjects(b[pos:])
		}
	case '[':
		if err = e.Tokens.pushArray(); err != nil {
			break
		}
		if err = e.Tokens.popArray(); err != nil {
			panic("BUG: popArray should never fail immediately after pushArray: " + err.Error())
		}
		if e.Flags.Get(jsonflags.ReorderRawObjects) {
			mustReorderObjects(b[pos:])
		}
	}
	if err != nil {
		return wrapSyntacticError(e, err, pos, +1)
	}

	// 收尾缓冲区并将其存回 e。
	e.Buf = b
	if e.NeedFlush() {
		return e.Flush()
	}
	return nil
}

// CountNextDelimWhitespace 假设即将到来的是 JSON 值，
// 统计分隔符和空白字节的数量。
// 此方法用于语义层的错误报告。
func (e *encoderState) CountNextDelimWhitespace() (n int) {
	const next = Kind('"') // 作为下一个 JSON 值的任意种类
	delim := e.Tokens.needDelim(next)
	if delim > 0 {
		n += len(",") | len(":")
	}
	if delim == ':' {
		if e.Flags.Get(jsonflags.SpaceAfterColon) {
			n += len(" ")
		}
	} else {
		if delim == ',' && e.Flags.Get(jsonflags.SpaceAfterComma) {
			n += len(" ")
		}
		if e.Flags.Get(jsonflags.Multiline) {
			if m := e.Tokens.NeedIndent(next); m > 0 {
				n += len("\n") + len(e.IndentPrefix) + (m-1)*len(e.Indent)
			}
		}
	}
	return n
}

// appendWhitespace 追加紧挨在下一个 token 之前的空白。
func (e *encoderState) appendWhitespace(b []byte, next Kind) []byte {
	if delim := e.Tokens.needDelim(next); delim == ':' {
		if e.Flags.Get(jsonflags.SpaceAfterColon) {
			b = append(b, ' ')
		}
	} else {
		if delim == ',' && e.Flags.Get(jsonflags.SpaceAfterComma) {
			b = append(b, ' ')
		}
		if e.Flags.Get(jsonflags.Multiline) {
			b = e.AppendIndent(b, e.Tokens.NeedIndent(next))
		}
	}
	return b
}

// AppendIndent 为当前嵌套层级 n 追加适当数量的缩进字符。
func (e *encoderState) AppendIndent(b []byte, n int) []byte {
	if n == 0 {
		return b
	}
	b = append(b, '\n')
	b = append(b, e.IndentPrefix...)
	for ; n > 1; n-- {
		b = append(b, e.Indent...)
	}
	return b
}

// reformatValue 从 src 的开头解析一个 JSON 值，
// 并将其追加到 dst 的末尾，按需重新格式化空白和字符串。
// 它返回扩展后的 dst 缓冲区以及消耗的输入字节数。
func (e *encoderState) reformatValue(dst []byte, src Value, depth int) ([]byte, int, error) {
	// TODO: 这里是否应将 ValueFlags 作为输入更新？
	if len(src) == 0 {
		return dst, 0, io.ErrUnexpectedEOF
	}
	switch k := Kind(src[0]).normalize(); k {
	case 'n':
		if jsonwire.ConsumeNull(src) == 0 {
			n, err := jsonwire.ConsumeLiteral(src, "null")
			return dst, n, err
		}
		return append(dst, "null"...), len("null"), nil
	case 'f':
		if jsonwire.ConsumeFalse(src) == 0 {
			n, err := jsonwire.ConsumeLiteral(src, "false")
			return dst, n, err
		}
		return append(dst, "false"...), len("false"), nil
	case 't':
		if jsonwire.ConsumeTrue(src) == 0 {
			n, err := jsonwire.ConsumeLiteral(src, "true")
			return dst, n, err
		}
		return append(dst, "true"...), len("true"), nil
	case '"':
		if n := jsonwire.ConsumeSimpleString(src); n > 0 {
			dst = append(dst, src[:n]...) // 逐字复制简单字符串
			return dst, n, nil
		}
		return jsonwire.ReformatString(dst, src, &e.Flags)
	case '0':
		if n := jsonwire.ConsumeSimpleNumber(src); n > 0 && !e.Flags.Get(jsonflags.CanonicalizeNumbers) {
			dst = append(dst, src[:n]...) // 逐字复制简单数字
			return dst, n, nil
		}
		return jsonwire.ReformatNumber(dst, src, &e.Flags)
	case '{':
		return e.reformatObject(dst, src, depth)
	case '[':
		return e.reformatArray(dst, src, depth)
	default:
		return dst, 0, jsonwire.NewInvalidCharacterError(src, "at start of value")
	}
}

// reformatObject 从 src 的开头解析一个 JSON 对象，
// 并将其追加到 src 的末尾，按需重新格式化空白和字符串。
// 它返回扩展后的 dst 缓冲区以及消耗的输入字节数。
func (e *encoderState) reformatObject(dst []byte, src Value, depth int) ([]byte, int, error) {
	// 追加对象开始。
	if len(src) == 0 || src[0] != '{' {
		panic("BUG: reformatObject must be called with a buffer that starts with '{'")
	} else if depth == maxNestingDepth+1 {
		return dst, 0, errMaxDepth
	}
	dst = append(dst, '{')
	n := len("{")

	// 追加（可能的）对象结束。
	n += jsonwire.ConsumeWhitespace(src[n:])
	if uint(len(src)) <= uint(n) {
		return dst, n, io.ErrUnexpectedEOF
	}
	if src[n] == '}' {
		dst = append(dst, '}')
		n += len("}")
		return dst, n, nil
	}

	var err error
	var names *objectNamespace
	if !e.Flags.Get(jsonflags.AllowDuplicateNames) {
		e.Namespaces.push()
		defer e.Namespaces.pop()
		names = e.Namespaces.Last()
	}
	depth++
	for {
		// 追加可选的换行和缩进。
		if e.Flags.Get(jsonflags.Multiline) {
			dst = e.AppendIndent(dst, depth)
		}

		// 追加对象名。
		n += jsonwire.ConsumeWhitespace(src[n:])
		if uint(len(src)) <= uint(n) {
			return dst, n, io.ErrUnexpectedEOF
		}
		m := jsonwire.ConsumeSimpleString(src[n:])
		isVerbatim := m > 0
		if isVerbatim {
			dst = append(dst, src[n:n+m]...)
		} else {
			dst, m, err = jsonwire.ReformatString(dst, src[n:], &e.Flags)
			if err != nil {
				return dst, n + m, err
			}
		}
		quotedName := src[n : n+m]
		if !e.Flags.Get(jsonflags.AllowDuplicateNames) && !names.insertQuoted(quotedName, isVerbatim) {
			return dst, n, wrapWithObjectName(ErrDuplicateName, quotedName)
		}
		n += m

		// 追加冒号。
		n += jsonwire.ConsumeWhitespace(src[n:])
		if uint(len(src)) <= uint(n) {
			return dst, n, wrapWithObjectName(io.ErrUnexpectedEOF, quotedName)
		}
		if src[n] != ':' {
			err = jsonwire.NewInvalidCharacterError(src[n:], "after object name (expecting ':')")
			return dst, n, wrapWithObjectName(err, quotedName)
		}
		dst = append(dst, ':')
		n += len(":")
		if e.Flags.Get(jsonflags.SpaceAfterColon) {
			dst = append(dst, ' ')
		}

		// 追加对象值。
		n += jsonwire.ConsumeWhitespace(src[n:])
		if uint(len(src)) <= uint(n) {
			return dst, n, wrapWithObjectName(io.ErrUnexpectedEOF, quotedName)
		}
		dst, m, err = e.reformatValue(dst, src[n:], depth)
		if err != nil {
			return dst, n + m, wrapWithObjectName(err, quotedName)
		}
		n += m

		// 追加逗号或对象结束。
		n += jsonwire.ConsumeWhitespace(src[n:])
		if uint(len(src)) <= uint(n) {
			return dst, n, io.ErrUnexpectedEOF
		}
		switch src[n] {
		case ',':
			dst = append(dst, ',')
			if e.Flags.Get(jsonflags.SpaceAfterComma) {
				dst = append(dst, ' ')
			}
			n += len(",")
			continue
		case '}':
			if e.Flags.Get(jsonflags.Multiline) {
				dst = e.AppendIndent(dst, depth-1)
			}
			dst = append(dst, '}')
			n += len("}")
			return dst, n, nil
		default:
			return dst, n, jsonwire.NewInvalidCharacterError(src[n:], "after object value (expecting ',' or '}')")
		}
	}
}

// reformatArray 从 src 的开头解析一个 JSON 数组，
// 并将其追加到 dst 的末尾，按需重新格式化空白和字符串。
// 它返回扩展后的 dst 缓冲区以及消耗的输入字节数。
func (e *encoderState) reformatArray(dst []byte, src Value, depth int) ([]byte, int, error) {
	// 追加数组开始。
	if len(src) == 0 || src[0] != '[' {
		panic("BUG: reformatArray must be called with a buffer that starts with '['")
	} else if depth == maxNestingDepth+1 {
		return dst, 0, errMaxDepth
	}
	dst = append(dst, '[')
	n := len("[")

	// 追加（可能的）数组结束。
	n += jsonwire.ConsumeWhitespace(src[n:])
	if uint(len(src)) <= uint(n) {
		return dst, n, io.ErrUnexpectedEOF
	}
	if src[n] == ']' {
		dst = append(dst, ']')
		n += len("]")
		return dst, n, nil
	}

	var idx int64
	var err error
	depth++
	for {
		// 追加可选的换行和缩进。
		if e.Flags.Get(jsonflags.Multiline) {
			dst = e.AppendIndent(dst, depth)
		}

		// 追加数组值。
		n += jsonwire.ConsumeWhitespace(src[n:])
		if uint(len(src)) <= uint(n) {
			return dst, n, io.ErrUnexpectedEOF
		}
		var m int
		dst, m, err = e.reformatValue(dst, src[n:], depth)
		if err != nil {
			return dst, n + m, wrapWithArrayIndex(err, idx)
		}
		n += m

		// 追加逗号或数组结束。
		n += jsonwire.ConsumeWhitespace(src[n:])
		if uint(len(src)) <= uint(n) {
			return dst, n, io.ErrUnexpectedEOF
		}
		switch src[n] {
		case ',':
			dst = append(dst, ',')
			if e.Flags.Get(jsonflags.SpaceAfterComma) {
				dst = append(dst, ' ')
			}
			n += len(",")
			idx++
			continue
		case ']':
			if e.Flags.Get(jsonflags.Multiline) {
				dst = e.AppendIndent(dst, depth-1)
			}
			dst = append(dst, ']')
			n += len("]")
			return dst, n, nil
		default:
			return dst, n, jsonwire.NewInvalidCharacterError(src[n:], "after array value (expecting ',' or ']')")
		}
	}
}

// OutputOffset 返回当前的输出字节偏移量。它给出紧跟在
// 最近写入的 token 或值之后的那个字节的位置。
// 由于内部缓冲的影响，实际写入底层 [io.Writer] 的字节数
// 可能少于该偏移量。
func (e *Encoder) OutputOffset() int64 {
	return e.s.previousOffsetEnd()
}

// AvailableBuffer 返回一个长度为零但容量可能非零的缓冲区。
// 该缓冲区旨在用于填充一个 [Value]，以便
// 传给紧接着的 [Encoder.WriteValue] 调用。
//
// 用法示例：
//
//	b := d.AvailableBuffer()
//	b = append(b, '"')
//	b = appendString(b, v) // 追加 v 的字符串格式
//	b = append(b, '"')
//	... := d.WriteValue(b)
//
// 用户有责任确保该值是有效的 JSON。
func (e *Encoder) AvailableBuffer() []byte {
	// NOTE: 我们不会返回 e.buf[len(e.buf):cap(e.buf)]，因为 WriteValue
	// 需要特别小心以避免在重新格式化时改写数据。
	// 不使用 unsafe.Pointer 的情况下，WriteValue 无法轻易判断
	// 输入的 Value 是否与 e.buf 共享底层存储。因此，我们只返回一个不同的缓冲区。
	// 如果它竟然与 e.buf 共享底层存储，我们需要考虑它如何与
	// 针对 bytes.Buffer 的专用性能优化协同工作。
	n := 1 << bits.Len(uint(e.s.maxValue|63)) // 对最大长度的快速近似
	if cap(e.s.availBuffer) < n {
		e.s.availBuffer = make([]byte, 0, n)
	}
	return e.s.availBuffer
}

// StackDepth 返回已写入 JSON 数据的状态机深度。
// 栈上的每一层表示一个嵌套的 JSON 对象或数组。
// 每当遇到 [BeginObject] 或 [BeginArray] token 时递增，
// 每当遇到 [EndObject] 或 [EndArray] token 时递减。
// 深度从零开始索引，零表示顶层 JSON 值。
func (e *Encoder) StackDepth() int {
	// NOTE: 与 Decoder.StackDepth 保持同步。
	return e.s.Tokens.Depth() - 1
}

// StackIndex 返回指定栈层级的信息。
// 它必须是 0 到 [Encoder.StackDepth]（含）之间的数字。
// 对于每一层，它报告其种类：
//
//   - [KindInvalid] 表示层级零，
//   - [KindBeginObject] 表示代表 JSON 对象的层级，以及
//   - [KindBeginArray] 表示代表 JSON 数组的层级。
//
// 它还报告该 JSON 对象或数组的长度。
// JSON 对象中的每个名称和值分别计数，
// 因此成员的有效数量是长度的一半。
// 一个完整的 JSON 对象的长度必须为偶数。
func (e *Encoder) StackIndex(i int) (Kind, int64) {
	// NOTE: 与 Decoder.StackIndex 保持同步。
	switch s := e.s.Tokens.index(i); {
	case i > 0 && s.isObject():
		return '{', s.Length()
	case i > 0 && s.isArray():
		return '[', s.Length()
	default:
		return 0, s.Length()
	}
}

// StackPointer 返回指向最近写入值的 JSON Pointer（RFC 6901）。
func (e *Encoder) StackPointer() Pointer {
	return Pointer(e.s.AppendStackPointer(nil, -1))
}

func (e *encoderState) AppendStackPointer(b []byte, where int) []byte {
	e.Names.copyQuotedBuffer(e.Buf)
	return e.state.appendStackPointer(b, where)
}
