// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.jsonv2

package json

import (
	"bytes"
	"errors"
	"io"
)

// Decoder 从输入流中读取并解码 JSON 值。
type Decoder struct {
	r       io.Reader
	buf     []byte
	d       decodeState
	scanp   int   // buf 中未读数据的起始位置
	scanned int64 // 已扫描的数据量
	scan    scanner
	err     error

	tokenState int
	tokenStack []int
}

// NewDecoder 返回一个从 r 读取数据的新解码器。
//
// 该解码器会引入自己的缓冲，并可能从 r 读取
// 超出所请求 JSON 值的数据。
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

// UseNumber 使 Decoder 将数字反序列化为 interface 值时
// 使用 [Number] 而不是 float64。
func (dec *Decoder) UseNumber() { dec.d.useNumber = true }

// DisallowUnknownFields 使 Decoder 在目标是结构体且输入包含
// 与目标中任何非忽略、导出字段都不匹配的对象键时返回错误。
func (dec *Decoder) DisallowUnknownFields() { dec.d.disallowUnknownFields = true }

// Decode 从其输入中读取下一个经过 JSON 编码的值，
// 并将其存储到 v 指向的值中。
//
// 有关将 JSON 转换为 Go 值的详细信息，
// 请参见 [Unmarshal] 的文档。
func (dec *Decoder) Decode(v any) error {
	if dec.err != nil {
		return dec.err
	}

	if err := dec.tokenPrepareForDecode(); err != nil {
		return err
	}

	if !dec.tokenValueAllowed() {
		return &SyntaxError{msg: "not at beginning of value", Offset: dec.InputOffset()}
	}

	// 将整个值读入缓冲区。
	n, err := dec.readValue()
	if err != nil {
		return err
	}
	dec.d.init(dec.buf[dec.scanp : dec.scanp+n])
	dec.scanp += n

	// 不要将 unmarshal 的错误保存到 dec.err 中：
	// 由于在错误发生之前我们已从中读取了完整的 JSON 对象，
	// 连接仍然可用。
	err = dec.d.unmarshal(v)

	// 修正 token 流式传输状态
	dec.tokenValueEnd()

	return err
}

// Buffered 返回 Decoder 缓冲区中剩余数据的读取器。
// 该读取器在下次调用 [Decoder.Decode] 之前有效。
func (dec *Decoder) Buffered() io.Reader {
	return bytes.NewReader(dec.buf[dec.scanp:])
}

// readValue 将一个 JSON 值读入 dec.buf。
// 它返回该编码的长度。
func (dec *Decoder) readValue() (int, error) {
	dec.scan.reset()

	scanp := dec.scanp
	var err error
Input:
	// 帮助编译器得知 scanp 永远不为负，
	// 以便它能移除下面的一些边界检查。
	for scanp >= 0 {

		// 在缓冲区中寻找一个新的值。
		for ; scanp < len(dec.buf); scanp++ {
			c := dec.buf[scanp]
			dec.scan.bytes++
			switch dec.scan.step(&dec.scan, c) {
			case scanEnd:
				// scanEnd 延迟一个字节，因此我们将
				// scanner 的字节计数减 1，以确保
				// 在下一次调用 Decode 时该值是正确的。
				dec.scan.bytes--
				break Input
			case scanEndObject, scanEndArray:
				// scanEnd 延迟一个字节。
				// 试图从 src 获取该字节时我们可能会阻塞，
				// 因此改为虚构一个空格字节。
				if stateEndValue(&dec.scan, ' ') == scanEnd {
					scanp++
					break Input
				}
			case scanError:
				dec.err = dec.scan.err
				return 0, dec.scan.err
			}
		}

		// 上一次读取是否有错误？
		// 延迟到现在处理，以便允许扫描缓冲区。
		if err != nil {
			if err == io.EOF {
				if dec.scan.step(&dec.scan, ' ') == scanEnd {
					break Input
				}
				if nonSpace(dec.buf) {
					err = io.ErrUnexpectedEOF
				}
			}
			dec.err = err
			return 0, err
		}

		n := scanp - dec.scanp
		err = dec.refill()
		scanp = dec.scanp + n
	}
	return scanp - dec.scanp, nil
}

func (dec *Decoder) refill() error {
	// 腾出空间以便向缓冲区读取更多数据。
	// 首先将已消费的数据向下滑动。
	if dec.scanp > 0 {
		dec.scanned += int64(dec.scanp)
		n := copy(dec.buf, dec.buf[dec.scanp:])
		dec.buf = dec.buf[:n]
		dec.scanp = 0
	}

	// 如果不够大则增长缓冲区。
	const minRead = 512
	if cap(dec.buf)-len(dec.buf) < minRead {
		newBuf := make([]byte, len(dec.buf), 2*cap(dec.buf)+minRead)
		copy(newBuf, dec.buf)
		dec.buf = newBuf
	}

	// 读取。将错误延迟到下一次迭代（扫描之后）处理。
	n, err := dec.r.Read(dec.buf[len(dec.buf):cap(dec.buf)])
	dec.buf = dec.buf[0 : len(dec.buf)+n]

	return err
}

func nonSpace(b []byte) bool {
	for _, c := range b {
		if !isSpace(c) {
			return true
		}
	}
	return false
}

// Encoder 将 JSON 值写入输出流。
type Encoder struct {
	w          io.Writer
	err        error
	escapeHTML bool

	indentBuf    []byte
	indentPrefix string
	indentValue  string
}

// NewEncoder 返回一个写入 w 的新编码器。
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w, escapeHTML: true}
}

// Encode 将 v 的 JSON 编码写入流，
// 去掉无意义的空白字符，
// 并在末尾追加一个换行符。
//
// 有关将 Go 值转换为 JSON 的详细信息，
// 请参见 [Marshal] 的文档。
func (enc *Encoder) Encode(v any) error {
	if enc.err != nil {
		return enc.err
	}

	e := newEncodeState()
	defer encodeStatePool.Put(e)

	err := e.marshal(v, encOpts{escapeHTML: enc.escapeHTML})
	if err != nil {
		return err
	}

	// 用换行符终止每个值。
	// 这使得调试时输出看起来更整洁一些，
	// 并且如果编码后的值是数字，
	// 则需要某种形式的空白，
	// 以便读取器知道不会再有更多数字到来。
	e.WriteByte('\n')

	b := e.Bytes()
	if enc.indentPrefix != "" || enc.indentValue != "" {
		enc.indentBuf, err = appendIndent(enc.indentBuf[:0], b, enc.indentPrefix, enc.indentValue)
		if err != nil {
			return err
		}
		b = enc.indentBuf
	}
	if _, err = enc.w.Write(b); err != nil {
		enc.err = err
	}
	return err
}

// SetIndent 指示编码器将后续每个编码值格式化，
// 就像由包级函数 Indent(dst, src, prefix, indent) 缩进一样。
// 调用 SetIndent("", "") 会禁用缩进。
func (enc *Encoder) SetIndent(prefix, indent string) {
	enc.indentPrefix = prefix
	enc.indentValue = indent
}

// SetEscapeHTML 指定是否应在 JSON 引号字符串内
// 转义有问题的 HTML 字符。
// 默认行为是将 &、< 和 > 转义为 \u0026、\u003c 和 \u003e，
// 以避免在 HTML 中嵌入 JSON 时可能出现的某些安全问题。
//
// 在转义会影响输出可读性的非 HTML 环境中，
// SetEscapeHTML(false) 会禁用此行为。
func (enc *Encoder) SetEscapeHTML(on bool) {
	enc.escapeHTML = on
}

// RawMessage 是一个原始的编码 JSON 值。
// 它实现了 [Marshaler] 和 [Unmarshaler]，
// 可用于延迟 JSON 解码或预计算 JSON 编码。
type RawMessage []byte

// MarshalJSON 返回 m 作为 m 的 JSON 编码。
func (m RawMessage) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("null"), nil
	}
	return m, nil
}

// UnmarshalJSON 将 *m 设置为 data 的副本。
func (m *RawMessage) UnmarshalJSON(data []byte) error {
	if m == nil {
		return errors.New("json.RawMessage: UnmarshalJSON on nil pointer")
	}
	*m = append((*m)[0:0], data...)
	return nil
}

var _ Marshaler = (*RawMessage)(nil)
var _ Unmarshaler = (*RawMessage)(nil)

// Token 持有以下类型之一的值：
//
//   - [Delim]，用于四个 JSON 分隔符 [ ] { }
//   - bool，用于 JSON 布尔值
//   - float64，用于 JSON 数字
//   - [Number]，用于 JSON 数字
//   - string，用于 JSON 字符串字面量
//   - nil，用于 JSON null
type Token any

const (
	tokenTopValue = iota
	tokenArrayStart
	tokenArrayValue
	tokenArrayComma
	tokenObjectStart
	tokenObjectKey
	tokenObjectColon
	tokenObjectValue
	tokenObjectComma
)

// 将 tokenstate 从分隔符状态推进到值状态
func (dec *Decoder) tokenPrepareForDecode() error {
	// 注意：不在 switch 之前调用 peek，以避免
	// 将 peek 放入标准的 Decode 路径中。
	// 只有在使用 Token API 时才会调用 peek。
	switch dec.tokenState {
	case tokenArrayComma:
		c, err := dec.peek()
		if err != nil {
			return err
		}
		if c != ',' {
			return &SyntaxError{"expected comma after array element", dec.InputOffset()}
		}
		dec.scanp++
		dec.tokenState = tokenArrayValue
	case tokenObjectColon:
		c, err := dec.peek()
		if err != nil {
			return err
		}
		if c != ':' {
			return &SyntaxError{"expected colon after object key", dec.InputOffset()}
		}
		dec.scanp++
		dec.tokenState = tokenObjectValue
	}
	return nil
}

func (dec *Decoder) tokenValueAllowed() bool {
	switch dec.tokenState {
	case tokenTopValue, tokenArrayStart, tokenArrayValue, tokenObjectValue:
		return true
	}
	return false
}

func (dec *Decoder) tokenValueEnd() {
	switch dec.tokenState {
	case tokenArrayStart, tokenArrayValue:
		dec.tokenState = tokenArrayComma
	case tokenObjectValue:
		dec.tokenState = tokenObjectComma
	}
}

// Delim 是一个 JSON 数组或对象分隔符，为 [ ] { 或 } 之一。
type Delim rune

func (d Delim) String() string {
	return string(d)
}

// Token 返回输入流中的下一个 JSON token。
// 在输入流末尾，Token 返回 nil, [io.EOF]。
//
// Token 保证其返回的分隔符 [ ] { } 被正确嵌套和匹配：
// 如果 Token 在输入中遇到意外的分隔符，它将返回错误。
//
// 输入流由基本 JSON 值——bool、string、number 和 null——
// 以及标记数组和对象开始与结束的 [Delim] 类型的分隔符 [ ] { } 组成。
// 逗号和冒号会被省略。
func (dec *Decoder) Token() (Token, error) {
	for {
		c, err := dec.peek()
		if err != nil {
			return nil, err
		}
		switch c {
		case '[':
			if !dec.tokenValueAllowed() {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenStack = append(dec.tokenStack, dec.tokenState)
			dec.tokenState = tokenArrayStart
			return Delim('['), nil

		case ']':
			if dec.tokenState != tokenArrayStart && dec.tokenState != tokenArrayComma {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenState = dec.tokenStack[len(dec.tokenStack)-1]
			dec.tokenStack = dec.tokenStack[:len(dec.tokenStack)-1]
			dec.tokenValueEnd()
			return Delim(']'), nil

		case '{':
			if !dec.tokenValueAllowed() {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenStack = append(dec.tokenStack, dec.tokenState)
			dec.tokenState = tokenObjectStart
			return Delim('{'), nil

		case '}':
			if dec.tokenState != tokenObjectStart && dec.tokenState != tokenObjectComma {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenState = dec.tokenStack[len(dec.tokenStack)-1]
			dec.tokenStack = dec.tokenStack[:len(dec.tokenStack)-1]
			dec.tokenValueEnd()
			return Delim('}'), nil

		case ':':
			if dec.tokenState != tokenObjectColon {
				return dec.tokenError(c)
			}
			dec.scanp++
			dec.tokenState = tokenObjectValue
			continue

		case ',':
			if dec.tokenState == tokenArrayComma {
				dec.scanp++
				dec.tokenState = tokenArrayValue
				continue
			}
			if dec.tokenState == tokenObjectComma {
				dec.scanp++
				dec.tokenState = tokenObjectKey
				continue
			}
			return dec.tokenError(c)

		case '"':
			if dec.tokenState == tokenObjectStart || dec.tokenState == tokenObjectKey {
				var x string
				old := dec.tokenState
				dec.tokenState = tokenTopValue
				err := dec.Decode(&x)
				dec.tokenState = old
				if err != nil {
					return nil, err
				}
				dec.tokenState = tokenObjectColon
				return x, nil
			}
			fallthrough

		default:
			if !dec.tokenValueAllowed() {
				return dec.tokenError(c)
			}
			var x any
			if err := dec.Decode(&x); err != nil {
				return nil, err
			}
			return x, nil
		}
	}
}

func (dec *Decoder) tokenError(c byte) (Token, error) {
	var context string
	switch dec.tokenState {
	case tokenTopValue:
		context = " looking for beginning of value"
	case tokenArrayStart, tokenArrayValue, tokenObjectValue:
		context = " looking for beginning of value"
	case tokenArrayComma:
		context = " after array element"
	case tokenObjectKey:
		context = " looking for beginning of object key string"
	case tokenObjectColon:
		context = " after object key"
	case tokenObjectComma:
		context = " after object key:value pair"
	}
	return nil, &SyntaxError{"invalid character " + quoteChar(c) + context, dec.InputOffset()}
}

// More 报告当前正在解析的数组或对象中是否还有另一个元素。
func (dec *Decoder) More() bool {
	c, err := dec.peek()
	return err == nil && c != ']' && c != '}'
}

func (dec *Decoder) peek() (byte, error) {
	var err error
	for {
		for i := dec.scanp; i < len(dec.buf); i++ {
			c := dec.buf[i]
			if isSpace(c) {
				continue
			}
			dec.scanp = i
			return c, nil
		}
		// 缓冲区已扫描完毕，现在报告任何错误
		if err != nil {
			return 0, err
		}
		err = dec.refill()
	}
}

// InputOffset 返回当前解码器位置的输入流字节偏移量。
// 该偏移量给出了最近返回的 token 的末尾和下一个 token 的开头的位置。
func (dec *Decoder) InputOffset() int64 {
	return dec.scanned + int64(dec.scanp)
}
