// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"bytes"
	"errors"
	"io"

	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// Decoder 从输入流中读取并解码 JSON 值。
type Decoder struct {
	dec  *jsontext.Decoder
	opts jsonv2.Options
	err  error

	// hadPeeked 报告是否调用了 [Decoder.More]。
	// 它会被 [Decoder.Decode] 和 [Decoder.Token] 重置。
	hadPeeked bool
}

// NewDecoder 返回一个从 r 读取数据的新解码器。
//
// 该解码器会引入自己的缓冲，并可能从 r 读取
// 超出所请求 JSON 值的数据。
func NewDecoder(r io.Reader) *Decoder {
	// 向 jsontext 隐藏 bytes.Buffer，因为它实现了某些优化，
	// 但这些优化也限制了它的某些使用方式。
	// 例如，当 bytes.Buffer 正被 jsontext.Decoder 使用时，
	// 无法向其写入。
	if _, ok := r.(*bytes.Buffer); ok {
		r = struct{ io.Reader }{r}
	}

	dec := new(Decoder)
	dec.opts = DefaultOptionsV1()
	dec.dec = jsontext.NewDecoder(r, dec.opts)
	return dec
}

// UseNumber 使 Decoder 将数字反序列化为 interface 值时
// 使用 [Number] 而不是 float64。
func (dec *Decoder) UseNumber() {
	if useNumber, _ := jsonv2.GetOption(dec.opts, unmarshalAnyWithRawNumber); !useNumber {
		dec.opts = jsonv2.JoinOptions(dec.opts, unmarshalAnyWithRawNumber(true))
	}
}

// DisallowUnknownFields 使 Decoder 在目标是结构体且输入包含
// 与目标中任何非忽略、导出字段都不匹配的对象键时返回错误。
func (dec *Decoder) DisallowUnknownFields() {
	if reject, _ := jsonv2.GetOption(dec.opts, jsonv2.RejectUnknownMembers); !reject {
		dec.opts = jsonv2.JoinOptions(dec.opts, jsonv2.RejectUnknownMembers(true))
	}
}

// Decode 从其输入中读取下一个经过 JSON 编码的值，
// 并将其存储到 v 指向的值中。
//
// 有关将 JSON 转换为 Go 值的详细信息，
// 请参见 [Unmarshal] 的文档。
func (dec *Decoder) Decode(v any) error {
	if dec.err != nil {
		return dec.err
	}
	b, err := dec.dec.ReadValue()
	if err != nil {
		dec.err = transformSyntacticError(err)
		if dec.err.Error() == errUnexpectedEnd.Error() {
			// 注意：对于截断输入的确切错误值，
			// Decode 一直与 Unmarshal 不一致。
			dec.err = io.ErrUnexpectedEOF
		}
		return dec.err
	}
	dec.hadPeeked = false
	return jsonv2.Unmarshal(b, v, dec.opts)
}

// Buffered 返回 Decoder 缓冲区中剩余数据的读取器。
// 该读取器在下次调用 [Decoder.Decode] 之前有效。
func (dec *Decoder) Buffered() io.Reader {
	return bytes.NewReader(dec.dec.UnreadBuffer())
}

// Encoder 将 JSON 值写入输出流。
type Encoder struct {
	w    io.Writer
	opts jsonv2.Options
	err  error

	buf       bytes.Buffer
	indentBuf bytes.Buffer

	indentPrefix string
	indentValue  string
}

// NewEncoder 返回一个写入 w 的新编码器。
func NewEncoder(w io.Writer) *Encoder {
	enc := new(Encoder)
	enc.w = w
	enc.opts = DefaultOptionsV1()
	return enc
}

// Encode 将 v 的 JSON 编码写入流，
// 并在末尾追加一个换行符。
//
// 有关将 Go 值转换为 JSON 的详细信息，
// 请参见 [Marshal] 的文档。
func (enc *Encoder) Encode(v any) error {
	if enc.err != nil {
		return enc.err
	}

	buf := &enc.buf
	buf.Reset()
	if err := jsonv2.MarshalWrite(buf, v, enc.opts); err != nil {
		return err
	}
	if len(enc.indentPrefix)+len(enc.indentValue) > 0 {
		enc.indentBuf.Reset()
		if err := Indent(&enc.indentBuf, buf.Bytes(), enc.indentPrefix, enc.indentValue); err != nil {
			return err
		}
		buf = &enc.indentBuf
	}
	buf.WriteByte('\n')

	if _, err := enc.w.Write(buf.Bytes()); err != nil {
		enc.err = err
		return err
	}
	return nil
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
	if escape, _ := jsonv2.GetOption(enc.opts, jsontext.EscapeForHTML); escape != on {
		enc.opts = jsonv2.JoinOptions(enc.opts, jsontext.EscapeForHTML(on))
	}
}

// RawMessage 是一个原始的编码 JSON 值。
// 它实现了 [Marshaler] 和 [Unmarshaler]，
// 可用于延迟 JSON 解码或预计算 JSON 编码。
type RawMessage = jsontext.Value

// Token 持有以下类型之一的值：
//
//   - [Delim]，用于四个 JSON 分隔符 [ ] { }
//   - bool，用于 JSON 布尔值
//   - float64，用于 JSON 数字
//   - [Number]，用于 JSON 数字
//   - string，用于 JSON 字符串字面量
//   - nil，用于 JSON null
type Token any

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
	if dec.err != nil {
		return nil, dec.err
	}
	tok, err := dec.dec.ReadToken()
	if err != nil {
		// 历史上，如果流是有效 JSON 值的前缀，
		// v1 只会报告 [io.EOF]。
		// 如果在 JSON token（如字面量、数字或字符串）内被截断，
		// 它会报告未包装的 [io.ErrUnexpectedEOF]。
		if errors.Is(err, io.ErrUnexpectedEOF) {
			if len(bytes.Trim(dec.dec.UnreadBuffer(), " \r\n\t,:")) == 0 {
				return nil, io.EOF
			}
			return nil, io.ErrUnexpectedEOF
		}
		return nil, transformSyntacticError(err)
	}
	dec.hadPeeked = false
	switch k := tok.Kind(); k {
	case 'n':
		return nil, nil
	case 'f':
		return false, nil
	case 't':
		return true, nil
	case '"':
		return tok.String(), nil
	case '0':
		if useNumber, _ := jsonv2.GetOption(dec.opts, unmarshalAnyWithRawNumber); useNumber {
			return Number(tok.String()), nil
		}
		return tok.Float(), nil
	case '{', '}', '[', ']':
		return Delim(k), nil
	default:
		panic("unreachable")
	}
}

// More 报告当前正在解析的数组或对象中是否还有另一个元素。
func (dec *Decoder) More() bool {
	dec.hadPeeked = true
	k := dec.dec.PeekKind()
	if k == 0 {
		if dec.err == nil {
			// PeekKind 不区分 EOF 和错误，
			// 因此读取下一个 token 以查看得到哪种情况。
			_, err := dec.dec.ReadToken()
			if err == nil {
				// 只有当 jsontext 违反其文档时才可能发生这种情况。
				err = errors.New("json: successful read after failed peek")
			}
			dec.err = transformSyntacticError(err)
		}
		return dec.err != io.EOF
	}
	return k != ']' && k != '}'
}

// InputOffset 返回当前解码器位置的输入流字节偏移量。
// 该偏移量给出了最近返回的 token 的末尾和下一个 token 的开头的位置。
func (dec *Decoder) InputOffset() int64 {
	offset := dec.dec.InputOffset()
	if dec.hadPeeked {
		// 历史上，InputOffset 报告最近返回的 token 的末尾位置，
		// 除非调用了 [Decoder.More]，在这种情况下，
		// 它报告下一个 token 的开头位置。
		unreadBuffer := dec.dec.UnreadBuffer()
		trailingTokens := bytes.TrimLeft(unreadBuffer, " \n\r\t")
		if len(trailingTokens) > 0 {
			leadingWhitespace := len(unreadBuffer) - len(trailingTokens)
			offset += int64(leadingWhitespace)
		}
	}
	return offset
}
