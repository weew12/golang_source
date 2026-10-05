// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"bytes"
	"errors"
	"io"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
)

// NOTE: 解码逻辑之所以复杂，是因为从
// io.Reader 读入临时缓冲区意味着缓冲区可能包含
// 某个有效输入被截断的部分，从而需要获取更多数据。
//
// 本文件的结构如下：
//
//   - consumeXXX 函数从 []byte 中解析一个确切的 JSON token。
//     如果缓冲区看似被截断，则返回 io.ErrUnexpectedEOF。
//     consumeSimpleXXX 函数之所以这样命名，是因为它们只处理
//     所解析 JSON token 语法的一个子集。
//     它们不处理完整语法，以保持这些函数可内联。
//
//   - Decoder.consumeXXX 方法从 Decoder.buf 中解析下一个 JSON token，
//     必要时自动获取更多输入。这些方法以一个相对于
//     Decoder.buf 起点的位置作为参数，并
//     返回已消耗 JSON token 的结束位置（同样
//     相对于 Decoder.buf 的起点）。
//
//   - 发生 I/O 错误或状态机违规时，
//     实现会避免改动 Decoder 的状态
//     （除了实现 Decoder.fetch 所需的簿记之外）。
//     因此，只有 Decoder.ReadToken 和 Decoder.ReadValue
//     负责更新 Decoder.prevStart 和 Decoder.prevEnd。
//
//   - 出于性能考虑，实现中大量采用先调用
//     可内联的 consumeXXX 函数的模式，如果需要做更多工作，
//     再调用较慢的 Decoder.consumeXXX 方法。
//     TODO: 如果 Go 编译器能更精细地控制
//     究竟哪些调用被内联，则重新审视此模式。

// Decoder 是一个用于原始 JSON token 和值的流式解码器。
// 它用于读取一系列顶层 JSON 值，
// 每个值之间以可选的空白字符分隔。
//
// [Decoder.ReadToken] 和 [Decoder.ReadValue] 调用可以交错进行。
// 例如，以下 JSON 值：
//
//	{"name":"value","array":[null,false,true,3.14159],"object":{"k":"v"}}
//
// 可以通过以下调用解析（为简洁起见忽略错误）：
//
//	d.ReadToken() // {
//	d.ReadToken() // "name"
//	d.ReadToken() // "value"
//	d.ReadValue() // "array"
//	d.ReadToken() // [
//	d.ReadToken() // null
//	d.ReadToken() // false
//	d.ReadValue() // true
//	d.ReadToken() // 3.14159
//	d.ReadToken() // ]
//	d.ReadValue() // "object"
//	d.ReadValue() // {"k":"v"}
//	d.ReadToken() // }
//
// 以上是众多可能调用序列之一，
// 对于任何给定的 token/值，它可能并非最合理的调用方法。
// 例如，更常见的做法可能是调用 [Decoder.ReadToken] 来获取
// 表示对象名的字符串 token。
type Decoder struct {
	s decoderState
}

// decoderState 是 Decoder 的底层状态。
// 它暴露了一些字段和方法供 "json" 包使用。
type decoderState struct {
	state
	decodeBuffer
	jsonopts.Struct

	StringCache *[256]string // 仅在 unmarshaling 时使用；与 json.stringCache 相同
}

// decodeBuffer 是一个被分为 4 段的缓冲区：
//
//   - buf[0:prevEnd]         // 缓冲区已读取的部分
//   - buf[prevStart:prevEnd] // 先前读取的值
//   - buf[prevEnd:len(buf)]  // 缓冲区未读取的部分
//   - buf[len(buf):cap(buf)] // 缓冲区未使用的部分
//
// 不变量：
//
//	0 ≤ prevStart ≤ prevEnd ≤ len(buf) ≤ cap(buf)
type decodeBuffer struct {
	peekPos int   // 若为下一个 token 起点的有效 buf 偏移量则非零
	peekErr error // 意味着 peekPos 为 -1

	buf       []byte // 若是 bytes.Buffer，则可能与之共享底层存储
	prevStart int
	prevEnd   int

	// baseOffset 加到 prevStart 和 prevEnd 上可得到
	// 相对于 io.Reader 流起点的绝对偏移量。
	baseOffset int64

	rd io.Reader
}

// NewDecoder 构造一个新的流式解码器，从 r 读取。
//
// 如果 r 是 [bytes.Buffer]，则解码器直接从该缓冲区解析，
// 而不先将内容复制到中间缓冲区。
// 解码器使用期间不得对该缓冲区进行额外写入。
func NewDecoder(r io.Reader, opts ...Options) *Decoder {
	d := new(Decoder)
	d.Reset(r, opts...)
	return d
}

// Reset 重置解码器，使其重新从 r 读取，
// 并使用提供的选项进行配置。不得对已传给
// [encoding/json/v2.UnmarshalerFrom.UnmarshalJSONFrom] 方法
// 或 [encoding/json/v2.UnmarshalFromFunc] 函数的 Decoder 调用 Reset。
func (d *Decoder) Reset(r io.Reader, opts ...Options) {
	switch {
	case d == nil:
		panic("jsontext: invalid nil Decoder")
	case r == nil:
		panic("jsontext: invalid nil io.Reader")
	case d.s.Flags.Get(jsonflags.WithinArshalCall):
		panic("jsontext: cannot reset Decoder passed to json.UnmarshalerFrom")
	}
	// 如果缓冲区不与先前的 [bytes.Buffer] 共享底层存储，则复用它。
	b := d.s.buf[:0]
	if _, ok := d.s.rd.(*bytes.Buffer); ok {
		b = nil
	}
	d.s.reset(b, r, opts...)
}

func (d *decoderState) reset(b []byte, r io.Reader, opts ...Options) {
	d.state.reset()
	d.decodeBuffer = decodeBuffer{buf: b, rd: r}
	opts2 := jsonopts.Struct{} // 避免修改 d.Struct，以防它是 opts 的一部分
	opts2.Join(opts...)
	d.Struct = opts2
}

// Options 返回用于构造编码器的选项，
// 还可能包含传给 [encoding/json/v2.UnmarshalDecode] 调用的语义选项。
//
// 如果在 [encoding/json/v2.UnmarshalerFrom.UnmarshalJSONFrom] 方法调用
// 或 [encoding/json/v2.UnmarshalFromFunc] 函数调用内操作，
// 则返回的选项仅在该调用内有效。
func (d *Decoder) Options() Options {
	return &d.s.Struct
}

var errBufferWriteAfterNext = errors.New("invalid bytes.Buffer.Write call after calling bytes.Buffer.Next")

// fetch 从底层 io.Reader 读取至少 1 个字节。
// 如果读取到零个字节且遇到 io.EOF，则返回 io.ErrUnexpectedEOF。
func (d *decoderState) fetch() error {
	if d.rd == nil {
		return io.ErrUnexpectedEOF
	}

	// 通知 objectNameStack 我们即将获取新的缓冲区内容。
	d.Names.copyQuotedBuffer(d.buf)

	// 针对 bytes.Buffer 做特殊处理以获得更好的性能。
	if bb, ok := d.rd.(*bytes.Buffer); ok {
		switch {
		case bb.Len() == 0:
			return io.ErrUnexpectedEOF
		case len(d.buf) == 0:
			d.buf = bb.Next(bb.Len()) // "读取" 缓冲区中的所有数据
			return nil
		default:
			// 这只会在提供了部分填充的 bytes.Buffer，
			// 且在 Decoder 从中读取时又向其写入更多数据的情况下发生。
			// 这种做法会导致数据损坏，因为后续写入
			// 可能会覆盖当前缓冲区的内容。
			//
			// 用户试图将 bytes.Buffer 当作管道使用，
			// 但 bytes.Buffer 是管道的糟糕实现，
			// 应改用专门为此设计的 io.Pipe。
			return &ioError{action: "read", err: errBufferWriteAfterNext}
		}
	}

	// 如果为空则分配初始缓冲区。
	if cap(d.buf) == 0 {
		d.buf = make([]byte, 0, 64)
	}

	// 检查是否需要增长缓冲区。
	const maxBufferSize = 4 << 10
	const growthSizeFactor = 2 // 值越大越快
	const growthRateFactor = 2 // 值越大越慢
	// 默认情况下，如果低于最大缓冲区大小则增长。
	grow := cap(d.buf) <= maxBufferSize/growthSizeFactor
	// 增长可能代价高昂，因此只有在
	// 已处理足够数量的字节时才增长。
	grow = grow && int64(cap(d.buf)) < d.previousOffsetEnd()/growthRateFactor
	// 如果 prevStart==0，则调用 fetch 是为了获取更多数据，
	// 以连续地完成消费一个大型 JSON 值。
	// 如果剩余容量少于 25% 可用则增长。
	// 注意这可能导致输入缓冲区超过 maxBufferSize。
	grow = grow || (d.prevStart == 0 && len(d.buf) >= 3*cap(d.buf)/4)

	if grow {
		// 分配一个新缓冲区，并将旧缓冲区的内容复制过来。
		// TODO: 是否对最大内部缓冲区大小提供硬性限制？
		buf := make([]byte, 0, cap(d.buf)*growthSizeFactor)
		d.buf = append(buf, d.buf[d.prevStart:]...)
	} else {
		// 将未读取的数据部分移到前面。
		n := copy(d.buf[:cap(d.buf)], d.buf[d.prevStart:])
		d.buf = d.buf[:n]
	}
	d.baseOffset += int64(d.prevStart)
	d.prevEnd -= d.prevStart
	d.prevStart = 0

	// 向内部缓冲区读取更多数据。
	for {
		n, err := d.rd.Read(d.buf[len(d.buf):cap(d.buf)])
		switch {
		case n > 0:
			d.buf = d.buf[:len(d.buf)+n]
			return nil // 如果读取到任何字节则忽略错误
		case err == io.EOF:
			return io.ErrUnexpectedEOF
		case err != nil:
			return &ioError{action: "read", err: err}
		default:
			continue // Read 返回了 (0, nil)
		}
	}
}

const invalidateBufferByte = '#' // JSON 语法中无效的起始字符

// invalidatePreviousRead 使 Peek 和 Read 调用返回的缓冲区失效，
// 使其第一个字节为无效字符。
// 这为 API 提供了 Hyrum 防护，以应对那些错误地假定
// ReadValue 返回的值在后续 Read 调用之后仍然有效的应用代码。
func (d *decodeBuffer) invalidatePreviousRead() {
	// 如果 d.rd 为 nil 则避免改动缓冲区，这意味着 d.buf
	// 由用户代码提供，可能不希望被改动。
	isBytesBuffer := func(r io.Reader) bool {
		_, ok := r.(*bytes.Buffer)
		return ok
	}
	if d.rd != nil && !isBytesBuffer(d.rd) && d.prevStart < d.prevEnd && uint(d.prevStart) < uint(len(d.buf)) {
		d.buf[d.prevStart] = invalidateBufferByte
		d.prevStart = d.prevEnd
	}
}

// needMore 报告是否没有更多未读取的字节。
func (d *decodeBuffer) needMore(pos int) bool {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	return pos == len(d.buf)
}

func (d *decodeBuffer) offsetAt(pos int) int64     { return d.baseOffset + int64(pos) }
func (d *decodeBuffer) previousOffsetStart() int64 { return d.baseOffset + int64(d.prevStart) }
func (d *decodeBuffer) previousOffsetEnd() int64   { return d.baseOffset + int64(d.prevEnd) }
func (d *decodeBuffer) previousBuffer() []byte     { return d.buf[d.prevStart:d.prevEnd] }
func (d *decodeBuffer) unreadBuffer() []byte       { return d.buf[d.prevEnd:len(d.buf)] }

// PreviousTokenOrValue 返回先前读取的 token 或值，
// 除非它已被 PeekKind 调用失效。
// 如果 token 只是一个分隔符，则返回一个 1 字节的缓冲区。
// 此方法用于语义层的错误报告。
func (d *decodeBuffer) PreviousTokenOrValue() []byte {
	b := d.previousBuffer()
	// 如果调用了 peek，则先前的 token 或缓冲区已失效。
	if d.peekPos > 0 || len(b) > 0 && b[0] == invalidateBufferByte {
		return nil
	}
	// ReadToken 不会保留 null、布尔值或分隔符的缓冲区。
	// 手动重建该缓冲区。
	if len(b) == 0 {
		b = d.buf[:d.prevEnd] // 先前缓冲区的全部内容
		for _, tok := range []string{"null", "false", "true", "{", "}", "[", "]"} {
			if len(b) >= len(tok) && string(b[len(b)-len(tok):]) == tok {
				return b[len(b)-len(tok):]
			}
		}
	}
	return b
}

// PeekKind 获取下一个 token 的种类，但不推进读取偏移量。
//
// 如果发生错误，它返回 [KindInvalid]。任何此类错误都会被缓存，
// 直到下一次读取调用，调用者有责任最终
// 在一次 PeekKind 调用之后跟上一个读取调用。
func (d *Decoder) PeekKind() Kind {
	return d.s.PeekKind()
}
func (d *decoderState) PeekKind() Kind {
	// 检查是否有缓存的 peek 结果。
	if d.peekPos > 0 {
		return Kind(d.buf[d.peekPos]).normalize()
	}

	var err error
	d.invalidatePreviousRead()
	pos := d.prevEnd

	// 消费前导空白。
	pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
	if d.needMore(pos) {
		if pos, err = d.consumeWhitespace(pos); err != nil {
			if err == io.ErrUnexpectedEOF && d.Tokens.Depth() == 1 {
				err = io.EOF // 如果在顶层值之后没有 Token，则可能是 EOF
			}
			d.peekPos, d.peekErr = -1, wrapSyntacticError(d, err, pos, 0)
			return invalidKind
		}
	}

	// 消费冒号或逗号。
	var delim byte
	if c := d.buf[pos]; c == ':' || c == ',' {
		delim = c
		pos += 1
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				err = wrapSyntacticError(d, err, pos, 0)
				d.peekPos, d.peekErr = -1, d.checkDelimBeforeIOError(delim, err)
				return invalidKind
			}
		}
	}
	next := Kind(d.buf[pos]).normalize()
	if d.Tokens.needDelim(next) != delim {
		d.peekPos, d.peekErr = -1, d.checkDelim(delim, next)
		return invalidKind
	}

	// 这可能将 peekPos 设为 0，这与未初始化状态无法区分。
	// 虽然对性能有轻微影响，但这是正确的，
	// 因为 ReadValue 和 ReadToken 会忽略缓存结果并
	// 重新计算下一个种类。
	d.peekPos, d.peekErr = pos, nil
	return next
}

// checkDelimBeforeIOError 在返回发生在分隔符之后的 IO 错误之前，
// 检查该分隔符是否至少有效。
func (d *decoderState) checkDelimBeforeIOError(delim byte, err error) error {
	// 由于发生了 IO 错误，我们不知道下一个种类是什么。
	// 然而，要校验当前分隔符是否至少可能有效，
	// 必须知道下一个种类。
	// 由于 JSON 字符串作为下一个 token 总是有效的，
	// 为保守起见，假定它就是用于校验的下一个种类。
	const next = Kind('"')
	if d.Tokens.needDelim(next) != delim {
		err = d.checkDelim(delim, next)
	}
	return err
}

// CountNextDelimWhitespace 统计即将到来的
// 分隔符或空白字符的字节数。
// 此方法用于语义层的错误报告。
func (d *decoderState) CountNextDelimWhitespace() int {
	d.PeekKind() // 填充 unreadBuffer
	return len(d.unreadBuffer()) - len(bytes.TrimLeft(d.unreadBuffer(), ",: \n\r\t"))
}

// checkDelim 检查 delim 对于给定的下一个种类是否有效。
func (d *decoderState) checkDelim(delim byte, next Kind) error {
	where := "at start of value"
	switch d.Tokens.needDelim(next) {
	case delim:
		return nil
	case ':':
		where = "after object name (expecting ':')"
	case ',':
		if d.Tokens.Last.isObject() {
			where = "after object value (expecting ',' or '}')"
		} else {
			where = "after array element (expecting ',' or ']')"
		}
	}
	pos := d.prevEnd // 将位置恢复到前导空白之后
	pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
	err := jsonwire.NewInvalidCharacterError(d.buf[pos:], where)
	return wrapSyntacticError(d, err, pos, 0)
}

// SkipValue 在语义上等同于调用 [Decoder.ReadValue] 并丢弃
// 结果，只是不会浪费内存去试图保存整个结果。
func (d *Decoder) SkipValue() error {
	return d.s.SkipValue()
}
func (d *decoderState) SkipValue() error {
	switch d.PeekKind() {
	case '{', '[':
		// 对于 JSON 对象和数组，持续跳过所有 token
		// 直到深度与起始深度一致。
		depth := d.Tokens.Depth()
		for {
			if _, err := d.ReadToken(); err != nil {
				return err
			}
			if depth >= d.Tokens.Depth() {
				return nil
			}
		}
	default:
		// 当下一个 token 是 '}' 或 ']' 时试图跳过一个值，
		// 会在此处返回错误。
		var flags jsonwire.ValueFlags
		if _, err := d.ReadValue(&flags); err != nil {
			return err
		}
		return nil
	}
}

// SkipValueRemainder 在读取 '{' 或 '[' token 之后
// 跳过某个值的剩余部分。
func (d *decoderState) SkipValueRemainder() error {
	if d.Tokens.Depth()-1 > 0 && d.Tokens.Last.Length() == 0 {
		for n := d.Tokens.Depth(); d.Tokens.Depth() >= n; {
			if _, err := d.ReadToken(); err != nil {
				return err
			}
		}
	}
	return nil
}

// SkipUntil 跳过所有 token，直到状态机
// 达到或超过指定的深度和长度。
func (d *decoderState) SkipUntil(depth int, length int64) error {
	for d.Tokens.Depth() > depth || (d.Tokens.Depth() == depth && d.Tokens.Last.Length() < length) {
		if _, err := d.ReadToken(); err != nil {
			return err
		}
	}
	return nil
}

// ReadToken 读取下一个 [Token]，并推进读取偏移量。
// 返回的 token 仅在下一次 Peek、Read 或 Skip 调用之前有效。
// 如果没有更多 token，则返回 [io.EOF]。
func (d *Decoder) ReadToken() (Token, error) {
	return d.s.ReadToken()
}
func (d *decoderState) ReadToken() (Token, error) {
	// 确定下一个种类。
	var err error
	var next Kind
	pos := d.peekPos
	if pos != 0 {
		// 使用缓存的 peek 结果。
		if d.peekErr != nil {
			err := d.peekErr
			d.peekPos, d.peekErr = 0, nil // 可能是一个暂时性的 I/O 错误
			return Token{}, err
		}
		next = Kind(d.buf[pos]).normalize()
		d.peekPos = 0 // 重置缓存
	} else {
		d.invalidatePreviousRead()
		pos = d.prevEnd

		// 消费前导空白。
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				if err == io.ErrUnexpectedEOF && d.Tokens.Depth() == 1 {
					err = io.EOF // 如果在顶层值之后没有 Token，则可能是 EOF
				}
				return Token{}, wrapSyntacticError(d, err, pos, 0)
			}
		}

		// 消费冒号或逗号。
		var delim byte
		if c := d.buf[pos]; c == ':' || c == ',' {
			delim = c
			pos += 1
			pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
			if d.needMore(pos) {
				if pos, err = d.consumeWhitespace(pos); err != nil {
					err = wrapSyntacticError(d, err, pos, 0)
					return Token{}, d.checkDelimBeforeIOError(delim, err)
				}
			}
		}
		next = Kind(d.buf[pos]).normalize()
		if d.Tokens.needDelim(next) != delim {
			return Token{}, d.checkDelim(delim, next)
		}
	}

	// 处理下一个 token。
	var n int
	switch next {
	case 'n':
		if jsonwire.ConsumeNull(d.buf[pos:]) == 0 {
			pos, err = d.consumeLiteral(pos, "null")
			if err != nil {
				return Token{}, wrapSyntacticError(d, err, pos, +1)
			}
		} else {
			pos += len("null")
		}
		if err = d.Tokens.appendLiteral(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos-len("null"), +1) // 报告位置在字面量的开头
		}
		d.prevStart, d.prevEnd = pos, pos
		return Null, nil

	case 'f':
		if jsonwire.ConsumeFalse(d.buf[pos:]) == 0 {
			pos, err = d.consumeLiteral(pos, "false")
			if err != nil {
				return Token{}, wrapSyntacticError(d, err, pos, +1)
			}
		} else {
			pos += len("false")
		}
		if err = d.Tokens.appendLiteral(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos-len("false"), +1) // 报告位置在字面量的开头
		}
		d.prevStart, d.prevEnd = pos, pos
		return False, nil

	case 't':
		if jsonwire.ConsumeTrue(d.buf[pos:]) == 0 {
			pos, err = d.consumeLiteral(pos, "true")
			if err != nil {
				return Token{}, wrapSyntacticError(d, err, pos, +1)
			}
		} else {
			pos += len("true")
		}
		if err = d.Tokens.appendLiteral(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos-len("true"), +1) // 报告位置在字面量的开头
		}
		d.prevStart, d.prevEnd = pos, pos
		return True, nil

	case '"':
		var flags jsonwire.ValueFlags // TODO: 在 Token 中保留它？
		if n = jsonwire.ConsumeSimpleString(d.buf[pos:]); n == 0 {
			oldAbsPos := d.baseOffset + int64(pos)
			pos, err = d.consumeString(&flags, pos)
			newAbsPos := d.baseOffset + int64(pos)
			n = int(newAbsPos - oldAbsPos)
			if err != nil {
				return Token{}, wrapSyntacticError(d, err, pos, +1)
			}
		} else {
			pos += n
		}
		if d.Tokens.Last.NeedObjectName() {
			if !d.Flags.Get(jsonflags.AllowDuplicateNames) {
				if !d.Tokens.Last.isValidNamespace() {
					return Token{}, wrapSyntacticError(d, errInvalidNamespace, pos-n, +1)
				}
				if d.Tokens.Last.isActiveNamespace() && !d.Namespaces.Last().insertQuoted(d.buf[pos-n:pos], flags.IsVerbatim()) {
					err = wrapWithObjectName(ErrDuplicateName, d.buf[pos-n:pos])
					return Token{}, wrapSyntacticError(d, err, pos-n, +1) // 报告位置在字符串的开头
				}
			}
			d.Names.ReplaceLastQuotedOffset(pos - n) // 仅在 insertQuoted 成功时才替换
		}
		if err = d.Tokens.appendString(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos-n, +1) // 报告位置在字符串的开头
		}
		d.prevStart, d.prevEnd = pos-n, pos
		return Token{raw: &d.decodeBuffer, num: uint64(d.previousOffsetStart())}, nil

	case '0':
		// NOTE: 由于 JSON 数字不是自终止的，
		// 我们需要确保下一个字节不属于该数字。
		if n = jsonwire.ConsumeSimpleNumber(d.buf[pos:]); n == 0 || d.needMore(pos+n) {
			oldAbsPos := d.baseOffset + int64(pos)
			pos, err = d.consumeNumber(pos)
			newAbsPos := d.baseOffset + int64(pos)
			n = int(newAbsPos - oldAbsPos)
			if err != nil {
				return Token{}, wrapSyntacticError(d, err, pos, +1)
			}
		} else {
			pos += n
		}
		if err = d.Tokens.appendNumber(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos-n, +1) // 报告位置在数字的开头
		}
		d.prevStart, d.prevEnd = pos-n, pos
		return Token{raw: &d.decodeBuffer, num: uint64(d.previousOffsetStart())}, nil

	case '{':
		if err = d.Tokens.pushObject(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos, +1)
		}
		d.Names.push()
		if !d.Flags.Get(jsonflags.AllowDuplicateNames) {
			d.Namespaces.push()
		}
		pos += 1
		d.prevStart, d.prevEnd = pos, pos
		return BeginObject, nil

	case '}':
		if err = d.Tokens.popObject(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos, +1)
		}
		d.Names.pop()
		if !d.Flags.Get(jsonflags.AllowDuplicateNames) {
			d.Namespaces.pop()
		}
		pos += 1
		d.prevStart, d.prevEnd = pos, pos
		return EndObject, nil

	case '[':
		if err = d.Tokens.pushArray(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos, +1)
		}
		pos += 1
		d.prevStart, d.prevEnd = pos, pos
		return BeginArray, nil

	case ']':
		if err = d.Tokens.popArray(); err != nil {
			return Token{}, wrapSyntacticError(d, err, pos, +1)
		}
		pos += 1
		d.prevStart, d.prevEnd = pos, pos
		return EndArray, nil

	default:
		err = jsonwire.NewInvalidCharacterError(d.buf[pos:], "at start of value")
		return Token{}, wrapSyntacticError(d, err, pos, +1)
	}
}

// ReadValue 返回下一个原始 JSON 值，并推进读取偏移量。
// 该值会被去除任何前导或尾部空白，并且
// 包含输入的精确字节；如果指定了 [AllowInvalidUTF8]，
// 其中可能包含无效的 UTF-8。
//
// 返回的值仅在下一次 Peek、Read 或 Skip 调用之前有效，
// 且在 Decoder 仍在使用期间不得改动。
// 如果解码器当前正处于对象或数组的结束 token，
// 则它报告一个 [SyntacticError]，且内部状态保持不变。
// 如果没有更多值，则返回 [io.EOF]。
func (d *Decoder) ReadValue() (Value, error) {
	var flags jsonwire.ValueFlags
	return d.s.ReadValue(&flags)
}
func (d *decoderState) ReadValue(flags *jsonwire.ValueFlags) (Value, error) {
	// 确定下一个种类。
	var err error
	var next Kind
	pos := d.peekPos
	if pos != 0 {
		// 使用缓存的 peek 结果。
		if d.peekErr != nil {
			err := d.peekErr
			d.peekPos, d.peekErr = 0, nil // 可能是一个暂时性的 I/O 错误
			return nil, err
		}
		next = Kind(d.buf[pos]).normalize()
		d.peekPos = 0 // 重置缓存
	} else {
		d.invalidatePreviousRead()
		pos = d.prevEnd

		// 消费前导空白。
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				if err == io.ErrUnexpectedEOF && d.Tokens.Depth() == 1 {
					err = io.EOF // 如果在顶层值之后没有 Token，则可能是 EOF
				}
				return nil, wrapSyntacticError(d, err, pos, 0)
			}
		}

		// 消费冒号或逗号。
		var delim byte
		if c := d.buf[pos]; c == ':' || c == ',' {
			delim = c
			pos += 1
			pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
			if d.needMore(pos) {
				if pos, err = d.consumeWhitespace(pos); err != nil {
					err = wrapSyntacticError(d, err, pos, 0)
					return nil, d.checkDelimBeforeIOError(delim, err)
				}
			}
		}
		next = Kind(d.buf[pos]).normalize()
		if d.Tokens.needDelim(next) != delim {
			return nil, d.checkDelim(delim, next)
		}
	}

	// 处理下一个值。
	oldAbsPos := d.baseOffset + int64(pos)
	pos, err = d.consumeValue(flags, pos, d.Tokens.Depth())
	newAbsPos := d.baseOffset + int64(pos)
	n := int(newAbsPos - oldAbsPos)
	if err != nil {
		return nil, wrapSyntacticError(d, err, pos, +1)
	}
	switch next {
	case 'n', 't', 'f':
		err = d.Tokens.appendLiteral()
	case '"':
		if d.Tokens.Last.NeedObjectName() {
			if !d.Flags.Get(jsonflags.AllowDuplicateNames) {
				if !d.Tokens.Last.isValidNamespace() {
					err = errInvalidNamespace
					break
				}
				if d.Tokens.Last.isActiveNamespace() && !d.Namespaces.Last().insertQuoted(d.buf[pos-n:pos], flags.IsVerbatim()) {
					err = wrapWithObjectName(ErrDuplicateName, d.buf[pos-n:pos])
					break
				}
			}
			d.Names.ReplaceLastQuotedOffset(pos - n) // 仅在 insertQuoted 成功时才替换
		}
		err = d.Tokens.appendString()
	case '0':
		err = d.Tokens.appendNumber()
	case '{':
		if err = d.Tokens.pushObject(); err != nil {
			break
		}
		if err = d.Tokens.popObject(); err != nil {
			panic("BUG: popObject should never fail immediately after pushObject: " + err.Error())
		}
	case '[':
		if err = d.Tokens.pushArray(); err != nil {
			break
		}
		if err = d.Tokens.popArray(); err != nil {
			panic("BUG: popArray should never fail immediately after pushArray: " + err.Error())
		}
	}
	if err != nil {
		return nil, wrapSyntacticError(d, err, pos-n, +1) // 报告值开头的位置
	}
	d.prevEnd = pos
	d.prevStart = pos - n
	return d.buf[pos-n : pos : pos], nil
}

// CheckNextValue 检查下一个值在语法上是否有效，
// 但不推进读取偏移量。
// 如果 last 为真，则它验证流是否以 [io.EOF] 干净地终止。
func (d *decoderState) CheckNextValue(last bool) error {
	d.PeekKind() // 填充 d.peekPos 和 d.peekErr
	pos, err := d.peekPos, d.peekErr
	d.peekPos, d.peekErr = 0, nil
	if err != nil {
		return err
	}

	var flags jsonwire.ValueFlags
	if pos, err := d.consumeValue(&flags, pos, d.Tokens.Depth()); err != nil {
		return wrapSyntacticError(d, err, pos, +1)
	} else if last {
		return d.checkEOF(pos)
	}
	return nil
}

// AtEOF 报告解码器是否已到达 EOF。
func (d *decoderState) AtEOF() bool {
	_, err := d.consumeWhitespace(d.prevEnd)
	return err == io.ErrUnexpectedEOF
}

// CheckEOF 验证输入已没有更多数据。
func (d *decoderState) CheckEOF() error {
	return d.checkEOF(d.prevEnd)
}
func (d *decoderState) checkEOF(pos int) error {
	switch pos, err := d.consumeWhitespace(pos); err {
	case nil:
		err := jsonwire.NewInvalidCharacterError(d.buf[pos:], "after top-level value")
		return wrapSyntacticError(d, err, pos, 0)
	case io.ErrUnexpectedEOF:
		return nil
	default:
		return err
	}
}

// consumeWhitespace 消费从 d.buf[pos:] 开始的所有空白。
// 它返回 d.buf 中紧接在最后一个空白之后的新位置。
// 如果返回 nil，则保证至少还有一个未读字节。
//
// 以下模式在本实现中很常见：
//
//	pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
//	if d.needMore(pos) {
//		if pos, err = d.consumeWhitespace(pos); err != nil {
//			return ...
//		}
//	}
//
// 若要在不牺牲性能的情况下简化这段代码是很困难的，因为
// consumeWhitespace 必须能够内联。if 语句的主体仅在
// 需要获取更多数据的少数情况下才会执行。
// 由于获取操作可能返回错误，我们还需要检查该错误。
func (d *decoderState) consumeWhitespace(pos int) (newPos int, err error) {
	for {
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			absPos := d.baseOffset + int64(pos)
			err = d.fetch() // 会修改 d.buf 并使 pos 失效
			pos = int(absPos - d.baseOffset)
			if err != nil {
				return pos, err
			}
			continue
		}
		return pos, nil
	}
}

// consumeValue 消费从 d.buf[pos:] 开始的一个 JSON 值。
// 它返回 d.buf 中紧接在该值之后的新位置。
func (d *decoderState) consumeValue(flags *jsonwire.ValueFlags, pos, depth int) (newPos int, err error) {
	for {
		var n int
		var err error
		switch next := Kind(d.buf[pos]).normalize(); next {
		case 'n':
			if n = jsonwire.ConsumeNull(d.buf[pos:]); n == 0 {
				n, err = jsonwire.ConsumeLiteral(d.buf[pos:], "null")
			}
		case 'f':
			if n = jsonwire.ConsumeFalse(d.buf[pos:]); n == 0 {
				n, err = jsonwire.ConsumeLiteral(d.buf[pos:], "false")
			}
		case 't':
			if n = jsonwire.ConsumeTrue(d.buf[pos:]); n == 0 {
				n, err = jsonwire.ConsumeLiteral(d.buf[pos:], "true")
			}
		case '"':
			if n = jsonwire.ConsumeSimpleString(d.buf[pos:]); n == 0 {
				return d.consumeString(flags, pos)
			}
		case '0':
			// NOTE: 由于 JSON 数字不是自终结的，
			// 我们需要确保下一个字节不属于该数字。
			if n = jsonwire.ConsumeSimpleNumber(d.buf[pos:]); n == 0 || d.needMore(pos+n) {
				return d.consumeNumber(pos)
			}
		case '{':
			return d.consumeObject(flags, pos, depth)
		case '[':
			return d.consumeArray(flags, pos, depth)
		default:
			if (d.Tokens.Last.isObject() && next == ']') || (d.Tokens.Last.isArray() && next == '}') {
				return pos, errMismatchDelim
			}
			return pos, jsonwire.NewInvalidCharacterError(d.buf[pos:], "at start of value")
		}
		if err == io.ErrUnexpectedEOF {
			absPos := d.baseOffset + int64(pos)
			err = d.fetch() // 会修改 d.buf 并使 pos 失效
			pos = int(absPos - d.baseOffset)
			if err != nil {
				return pos + n, err
			}
			continue
		}
		return pos + n, err
	}
}

// consumeLiteral 消费从 d.buf[pos:] 开始的一个 JSON 字面量。
// 它返回 d.buf 中紧接在该字面量之后的新位置。
func (d *decoderState) consumeLiteral(pos int, lit string) (newPos int, err error) {
	for {
		n, err := jsonwire.ConsumeLiteral(d.buf[pos:], lit)
		if err == io.ErrUnexpectedEOF {
			absPos := d.baseOffset + int64(pos)
			err = d.fetch() // 会修改 d.buf 并使 pos 失效
			pos = int(absPos - d.baseOffset)
			if err != nil {
				return pos + n, err
			}
			continue
		}
		return pos + n, err
	}
}

// consumeString 消费从 d.buf[pos:] 开始的一个 JSON 字符串。
// 它返回 d.buf 中紧接在该字符串之后的新位置。
func (d *decoderState) consumeString(flags *jsonwire.ValueFlags, pos int) (newPos int, err error) {
	var n int
	for {
		n, err = jsonwire.ConsumeStringResumable(flags, d.buf[pos:], n, !d.Flags.Get(jsonflags.AllowInvalidUTF8))
		if err == io.ErrUnexpectedEOF {
			absPos := d.baseOffset + int64(pos)
			err = d.fetch() // 会修改 d.buf 并使 pos 失效
			pos = int(absPos - d.baseOffset)
			if err != nil {
				return pos + n, err
			}
			continue
		}
		return pos + n, err
	}
}

// consumeNumber 消费从 d.buf[pos:] 开始的一个 JSON 数字。
// 它返回 d.buf 中紧接在该数字之后的新位置。
func (d *decoderState) consumeNumber(pos int) (newPos int, err error) {
	var n int
	var state jsonwire.ConsumeNumberState
	for {
		n, state, err = jsonwire.ConsumeNumberResumable(d.buf[pos:], n, state)
		// NOTE: 由于 JSON 数字不是自终结的，
		// 我们需要确保下一个字节不属于该数字。
		if err == io.ErrUnexpectedEOF || d.needMore(pos+n) {
			mayTerminate := err == nil
			absPos := d.baseOffset + int64(pos)
			err = d.fetch() // 会修改 d.buf 并使 pos 失效
			pos = int(absPos - d.baseOffset)
			if err != nil {
				if mayTerminate && err == io.ErrUnexpectedEOF {
					return pos + n, nil
				}
				return pos, err
			}
			continue
		}
		return pos + n, err
	}
}

// consumeObject 消费从 d.buf[pos:] 开始的一个 JSON 对象。
// 它返回 d.buf 中紧接在该对象之后的新位置。
func (d *decoderState) consumeObject(flags *jsonwire.ValueFlags, pos, depth int) (newPos int, err error) {
	var n int
	var names *objectNamespace
	if !d.Flags.Get(jsonflags.AllowDuplicateNames) {
		d.Namespaces.push()
		defer d.Namespaces.pop()
		names = d.Namespaces.Last()
	}

	// 处理开始之前。
	if uint(pos) >= uint(len(d.buf)) || d.buf[pos] != '{' {
		panic("BUG: consumeObject must be called with a buffer that starts with '{'")
	} else if depth == maxNestingDepth+1 {
		return pos, errMaxDepth
	}
	pos++

	// 处理开始之后。
	pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
	if d.needMore(pos) {
		if pos, err = d.consumeWhitespace(pos); err != nil {
			return pos, err
		}
	}
	if d.buf[pos] == '}' {
		pos++
		return pos, nil
	}

	depth++
	for {
		// 处理名称之前。
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				return pos, err
			}
		}
		var flags2 jsonwire.ValueFlags
		if n = jsonwire.ConsumeSimpleString(d.buf[pos:]); n == 0 {
			oldAbsPos := d.baseOffset + int64(pos)
			pos, err = d.consumeString(&flags2, pos)
			newAbsPos := d.baseOffset + int64(pos)
			n = int(newAbsPos - oldAbsPos)
			flags.Join(flags2)
			if err != nil {
				return pos, err
			}
		} else {
			pos += n
		}
		quotedName := d.buf[pos-n : pos]
		if !d.Flags.Get(jsonflags.AllowDuplicateNames) && !names.insertQuoted(quotedName, flags2.IsVerbatim()) {
			return pos - n, wrapWithObjectName(ErrDuplicateName, quotedName)
		}

		// 处理名称之后。
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				return pos, wrapWithObjectName(err, quotedName)
			}
		}
		if d.buf[pos] != ':' {
			err := jsonwire.NewInvalidCharacterError(d.buf[pos:], "after object name (expecting ':')")
			return pos, wrapWithObjectName(err, quotedName)
		}
		pos++

		// 处理值之前。
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				return pos, wrapWithObjectName(err, quotedName)
			}
		}
		pos, err = d.consumeValue(flags, pos, depth)
		if err != nil {
			return pos, wrapWithObjectName(err, quotedName)
		}

		// 处理值之后。
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				return pos, err
			}
		}
		switch d.buf[pos] {
		case ',':
			pos++
			continue
		case '}':
			pos++
			return pos, nil
		default:
			return pos, jsonwire.NewInvalidCharacterError(d.buf[pos:], "after object value (expecting ',' or '}')")
		}
	}
}

// consumeArray 消费从 d.buf[pos:] 开始的一个 JSON 数组。
// 它返回 d.buf 中紧接在该数组之后的新位置。
func (d *decoderState) consumeArray(flags *jsonwire.ValueFlags, pos, depth int) (newPos int, err error) {
	// 处理开始之前。
	if uint(pos) >= uint(len(d.buf)) || d.buf[pos] != '[' {
		panic("BUG: consumeArray must be called with a buffer that starts with '['")
	} else if depth == maxNestingDepth+1 {
		return pos, errMaxDepth
	}
	pos++

	// 处理开始之后。
	pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
	if d.needMore(pos) {
		if pos, err = d.consumeWhitespace(pos); err != nil {
			return pos, err
		}
	}
	if d.buf[pos] == ']' {
		pos++
		return pos, nil
	}

	var idx int64
	depth++
	for {
		// 处理值之前。
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				return pos, err
			}
		}
		pos, err = d.consumeValue(flags, pos, depth)
		if err != nil {
			return pos, wrapWithArrayIndex(err, idx)
		}

		// 处理值之后。
		pos += jsonwire.ConsumeWhitespace(d.buf[pos:])
		if d.needMore(pos) {
			if pos, err = d.consumeWhitespace(pos); err != nil {
				return pos, err
			}
		}
		switch d.buf[pos] {
		case ',':
			pos++
			idx++
			continue
		case ']':
			pos++
			return pos, nil
		default:
			return pos, jsonwire.NewInvalidCharacterError(d.buf[pos:], "after array element (expecting ',' or ']')")
		}
	}
}

// InputOffset 返回当前输入字节偏移量。它给出
// 紧接在最近返回的 token 或值之后的那个字节的位置。
// 由于内部缓冲的影响，从底层 [io.Reader] 实际读取的字节数
// 可能大于此偏移量。
func (d *Decoder) InputOffset() int64 {
	return d.s.previousOffsetEnd()
}

// UnreadBuffer 返回未读缓冲区中剩余的数据，
// 其中可能包含零个或多个字节。
// 在 Decoder 继续被使用期间，不得修改返回的缓冲区。
// 缓冲区内容在下一次 Peek、Read 或 Skip 调用之前有效。
func (d *Decoder) UnreadBuffer() []byte {
	return d.s.unreadBuffer()
}

// StackDepth 返回读取 JSON 数据的状态机的深度。
// 栈上的每一层代表一个嵌套的 JSON 对象或数组。
// 每当遇到 [BeginObject] 或 [BeginArray] token 时它会递增，
// 每当遇到 [EndObject] 或 [EndArray] token 时它会递减。
// 深度从零开始索引，其中零表示顶层 JSON 值。
func (d *Decoder) StackDepth() int {
	// NOTE: 与 Encoder.StackDepth 保持同步。
	return d.s.Tokens.Depth() - 1
}

// StackIndex 返回关于指定栈层级的信息。
// 它必须是介于 0 和 [Decoder.StackDepth] 之间（含两端）的数字。
// 对于每一层，它报告其种类：
//
//   - 层级为零时为 [KindInvalid]，
//   - 表示 JSON 对象的层级为 [KindBeginObject]，
//   - 表示 JSON 数组的层级为 [KindBeginArray]。
//
// 它还报告该 JSON 对象或数组的长度。
// JSON 对象中的每个名称和值分别计数，
// 因此成员的有效数量将是长度的一半。
// 一个完整的 JSON 对象必须具有偶数长度。
func (d *Decoder) StackIndex(i int) (Kind, int64) {
	// NOTE: 与 Encoder.StackIndex 保持同步。
	switch s := d.s.Tokens.index(i); {
	case i > 0 && s.isObject():
		return '{', s.Length()
	case i > 0 && s.isArray():
		return '[', s.Length()
	default:
		return 0, s.Length()
	}
}

// StackPointer 返回指向最近读取的值的 JSON Pointer（RFC 6901）。
func (d *Decoder) StackPointer() Pointer {
	return Pointer(d.s.AppendStackPointer(nil, -1))
}

func (d *decoderState) AppendStackPointer(b []byte, where int) []byte {
	d.Names.copyQuotedBuffer(d.buf)
	return d.state.appendStackPointer(b, where)
}
