// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package base32 实现 RFC 4648 所规定的 base32 编码。
package base32

import (
	"io"
	"slices"
	"strconv"
)

/*
 * 编码
 */

// Encoding 是一种基数 32 的编码/解码方案，由 32 个字符的字母表定义。
// 最常见的是为 SASL GSSAPI 引入并在 RFC 4648 中标准化的 "base32" 编码。
// 另一种 "base32hex" 编码用于 DNSSEC。
type Encoding struct {
	encode    [32]byte   // 符号索引到符号字节值的映射
	decodeMap [256]uint8 // 符号字节值到符号索引的映射
	padChar   rune
}

const (
	StdPadding rune = '=' // 标准填充字符
	NoPadding  rune = -1  // 无填充
)

const (
	decodeMapInitialize = "" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff"
	invalidIndex = '\xff'
)

// NewEncoding 返回一个新的带填充的 Encoding，由给定的字母表定义，
// 该字母表必须是包含唯一字节值的 32 字节字符串，
// 且不包含填充字符或 CR / LF（'\r'、'\n'）。
// 字母表被视为字节值序列，对多字节 UTF-8 不做任何特殊处理。
// 生成的 Encoding 使用默认填充字符（'='），
// 可通过 [Encoding.WithPadding] 更改或禁用。
func NewEncoding(encoder string) *Encoding {
	if len(encoder) != 32 {
		panic("encoding alphabet is not 32-bytes long")
	}

	e := new(Encoding)
	e.padChar = StdPadding
	copy(e.encode[:], encoder)
	copy(e.decodeMap[:], decodeMapInitialize)

	for i := 0; i < len(encoder); i++ {
		// 注意：虽然我们规定字母表不能包含填充字符，但我们并不强制执行，
		// 因为我们不知道调用者是否打算之后将填充从 StdPadding 切换。
		switch {
		case encoder[i] == '\n' || encoder[i] == '\r':
			panic("encoding alphabet contains newline character")
		case e.decodeMap[encoder[i]] != invalidIndex:
			panic("encoding alphabet includes duplicate symbols")
		}
		e.decodeMap[encoder[i]] = uint8(i)
	}
	return e
}

// StdEncoding 是 RFC 4648 中定义的标准 base32 编码。
var StdEncoding = NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567")

// HexEncoding 是 RFC 4648 中定义的 "扩展十六进制字母表"。
// 它通常用于 DNS。
var HexEncoding = NewEncoding("0123456789ABCDEFGHIJKLMNOPQRSTUV")

// WithPadding 创建一个新的编码，除使用指定的填充字符外与 enc 相同，
// 或使用 NoPadding 禁用填充。
// 填充字符不能是 '\r' 或 '\n'，
// 不能包含在编码的字母表中，
// 不能为负数，且必须是等于或小于 '\xff' 的 rune。
// 高于 '\x7f' 的填充字符按其准确的字节值编码，
// 而不是使用该码点的 UTF-8 表示。
func (enc Encoding) WithPadding(padding rune) *Encoding {
	switch {
	case padding < NoPadding || padding == '\r' || padding == '\n' || padding > 0xff:
		panic("invalid padding")
	case padding != NoPadding && enc.decodeMap[byte(padding)] != invalidIndex:
		panic("padding contained in alphabet")
	}
	enc.padChar = padding
	return &enc
}

/*
 * 编码器
 */

// Encode 使用编码 enc 对 src 进行编码，
// 向 dst 写入 [Encoding.EncodedLen](len(src)) 个字节。
//
// 该编码将输出填充到 8 字节的倍数，
// 因此 Encode 不适合用于大型数据流的单个块。
// 请改用 [NewEncoder]。
func (enc *Encoding) Encode(dst, src []byte) {
	if len(src) == 0 {
		return
	}
	// enc 是一个指针接收者，因此在下面的热循环中使用 enc.encode
	// 意味着每次操作都要进行 nil 检查。将该 nil 检查提升到
	// 循环之外以加速编码器。
	_ = enc.encode

	di, si := 0, 0
	n := (len(src) / 5) * 5
	for si < n {
		// 合并两次 32 位加载可以让同一份代码
		// 在 32 位和 64 位平台上通用。
		hi := uint32(src[si+0])<<24 | uint32(src[si+1])<<16 | uint32(src[si+2])<<8 | uint32(src[si+3])
		lo := hi<<8 | uint32(src[si+4])

		dst[di+0] = enc.encode[(hi>>27)&0x1F]
		dst[di+1] = enc.encode[(hi>>22)&0x1F]
		dst[di+2] = enc.encode[(hi>>17)&0x1F]
		dst[di+3] = enc.encode[(hi>>12)&0x1F]
		dst[di+4] = enc.encode[(hi>>7)&0x1F]
		dst[di+5] = enc.encode[(hi>>2)&0x1F]
		dst[di+6] = enc.encode[(lo>>5)&0x1F]
		dst[di+7] = enc.encode[(lo)&0x1F]

		si += 5
		di += 8
	}

	// 添加剩余的小块
	remain := len(src) - si
	if remain == 0 {
		return
	}

	// 按逆序编码剩余的字节。
	val := uint32(0)
	switch remain {
	case 4:
		val |= uint32(src[si+3])
		dst[di+6] = enc.encode[val<<3&0x1F]
		dst[di+5] = enc.encode[val>>2&0x1F]
		fallthrough
	case 3:
		val |= uint32(src[si+2]) << 8
		dst[di+4] = enc.encode[val>>7&0x1F]
		fallthrough
	case 2:
		val |= uint32(src[si+1]) << 16
		dst[di+3] = enc.encode[val>>12&0x1F]
		dst[di+2] = enc.encode[val>>17&0x1F]
		fallthrough
	case 1:
		val |= uint32(src[si+0]) << 24
		dst[di+1] = enc.encode[val>>22&0x1F]
		dst[di+0] = enc.encode[val>>27&0x1F]
	}

	// 填充最后一个量子
	if enc.padChar != NoPadding {
		nPad := (remain * 8 / 5) + 1
		for i := nPad; i < 8; i++ {
			dst[di+i] = byte(enc.padChar)
		}
	}
}

// AppendEncode 将 base32 编码后的 src 追加到 dst
// 并返回扩展后的缓冲区。
func (enc *Encoding) AppendEncode(dst, src []byte) []byte {
	n := enc.EncodedLen(len(src))
	dst = slices.Grow(dst, n)
	enc.Encode(dst[len(dst):][:n], src)
	return dst[:len(dst)+n]
}

// EncodeToString 返回 src 的 base32 编码。
func (enc *Encoding) EncodeToString(src []byte) string {
	buf := make([]byte, enc.EncodedLen(len(src)))
	enc.Encode(buf, src)
	return string(buf)
}

type encoder struct {
	err  error
	enc  *Encoding
	w    io.Writer
	buf  [5]byte    // 等待编码的缓冲数据
	nbuf int        // buf 中的字节数
	out  [1024]byte // 输出缓冲区
}

func (e *encoder) Write(p []byte) (n int, err error) {
	if e.err != nil {
		return 0, e.err
	}

	// 前导边缘。
	if e.nbuf > 0 {
		var i int
		for i = 0; i < len(p) && e.nbuf < 5; i++ {
			e.buf[e.nbuf] = p[i]
			e.nbuf++
		}
		n += i
		p = p[i:]
		if e.nbuf < 5 {
			return
		}
		e.enc.Encode(e.out[0:], e.buf[0:])
		if _, e.err = e.w.Write(e.out[0:8]); e.err != nil {
			return n, e.err
		}
		e.nbuf = 0
	}

	// 大的内部块。
	for len(p) >= 5 {
		nn := len(e.out) / 8 * 5
		if nn > len(p) {
			nn = len(p)
			nn -= nn % 5
		}
		e.enc.Encode(e.out[0:], p[0:nn])
		if _, e.err = e.w.Write(e.out[0 : nn/5*8]); e.err != nil {
			return n, e.err
		}
		n += nn
		p = p[nn:]
	}

	// 尾部边缘。
	copy(e.buf[:], p)
	e.nbuf = len(p)
	n += len(p)
	return
}

// Close 刷新编码器中所有待处理的输出。
// 调用 Close 之后再调用 Write 是错误的。
func (e *encoder) Close() error {
	// 如果缓冲区中还有剩余内容，就将其刷新出去
	if e.err == nil && e.nbuf > 0 {
		e.enc.Encode(e.out[0:], e.buf[0:e.nbuf])
		encodedLen := e.enc.EncodedLen(e.nbuf)
		e.nbuf = 0
		_, e.err = e.w.Write(e.out[0:encodedLen])
	}
	return e.err
}

// NewEncoder 返回一个新的 base32 流编码器。写入返回的 writer 的数据
// 将使用 enc 编码，然后写入 w。
// Base32 编码以 5 字节块为操作单位；写入完成后，
// 调用者必须 Close 返回的编码器，以刷新任何
// 部分写入的块。
func NewEncoder(enc *Encoding, w io.Writer) io.WriteCloser {
	return &encoder{enc: enc, w: w}
}

// EncodedLen 返回长度为 n 的输入缓冲区
// 的 base32 编码的字节长度。
func (enc *Encoding) EncodedLen(n int) int {
	if enc.padChar == NoPadding {
		return n/5*8 + (n%5*8+4)/5
	}
	return (n + 4) / 5 * 8
}

/*
 * 解码器
 */

type CorruptInputError int64

func (e CorruptInputError) Error() string {
	return "illegal base32 data at input byte " + strconv.FormatInt(int64(e), 10)
}

// decode 类似 Decode，但会额外返回一个 'end' 值，
// 它指示是否遇到了消息结束填充，从而任何
// 额外数据都是错误。该方法假定 src 已被
// 去除所有受支持的空白字符（'\r' 和 '\n'）。
func (enc *Encoding) decode(dst, src []byte) (n int, end bool, err error) {
	// 将 nil 检查提升到循环之外。
	_ = enc.decodeMap

	dsti := 0
	olen := len(src)

	for len(src) > 0 && !end {
		// 使用 base32 字母表解码量子
		var dbuf [8]byte
		dlen := 8

		for j := 0; j < 8; {

			if len(src) == 0 {
				if enc.padChar != NoPadding {
					// 已到达末尾，且缺少填充
					return n, false, CorruptInputError(olen - len(src) - j)
				}
				// 已到达末尾，且不期望任何填充
				dlen, end = j, true
				break
			}
			in := src[0]
			src = src[1:]
			if in == byte(enc.padChar) && j >= 2 && len(src) < 8 {
				// 已到达末尾，且有填充
				if len(src)+j < 8-1 {
					// 填充不足
					return n, false, CorruptInputError(olen)
				}
				for k := 0; k < 8-1-j; k++ {
					if len(src) > k && src[k] != byte(enc.padChar) {
						// 填充不正确
						return n, false, CorruptInputError(olen - len(src) + k - 1)
					}
				}
				dlen, end = j, true
				// 7、5 和 2 不是有效的填充长度，因此 1、3 和 6 也不是
				// 有效的 dlen 值。请参见 RFC 4648 第 6 节 "Base 32 Encoding" 中
				// 列出的五种有效填充长度，以及第 9 节 "Illustrations and
				// Examples" 中关于第 1、3、6 个 base32 源字节为何
				// 无法提供足够信息来解码一个 dst 字节的图示。
				if dlen == 1 || dlen == 3 || dlen == 6 {
					return n, false, CorruptInputError(olen - len(src) - 1)
				}
				break
			}
			dbuf[j] = enc.decodeMap[in]
			if dbuf[j] == 0xFF {
				return n, false, CorruptInputError(olen - len(src) - 1)
			}
			j++
		}

		// 将 8 个 5 位源块打包成 5 字节的目标
		// 量子
		switch dlen {
		case 8:
			dst[dsti+4] = dbuf[6]<<5 | dbuf[7]
			n++
			fallthrough
		case 7:
			dst[dsti+3] = dbuf[4]<<7 | dbuf[5]<<2 | dbuf[6]>>3
			n++
			fallthrough
		case 5:
			dst[dsti+2] = dbuf[3]<<4 | dbuf[4]>>1
			n++
			fallthrough
		case 4:
			dst[dsti+1] = dbuf[1]<<6 | dbuf[2]<<1 | dbuf[3]>>4
			n++
			fallthrough
		case 2:
			dst[dsti+0] = dbuf[0]<<3 | dbuf[1]>>2
			n++
		}
		dsti += 5
	}
	return n, end, nil
}

// Decode 使用编码 enc 解码 src。它最多向 dst 写入
// [Encoding.DecodedLen](len(src)) 个字节，并返回写入的字节数。
// 调用者必须确保 dst 足够大以容纳所有解码后的数据。
// 如果 src 包含无效的 base32 数据，它会返回成功写入的
// 字节数以及 [CorruptInputError]。
// 换行字符（\r 和 \n）会被忽略。
func (enc *Encoding) Decode(dst, src []byte) (n int, err error) {
	buf := make([]byte, len(src))
	l := stripNewlines(buf, src)
	n, _, err = enc.decode(dst, buf[:l])
	return
}

// AppendDecode 将 base32 解码后的 src 追加到 dst
// 并返回扩展后的缓冲区。
// 如果输入格式错误，它返回部分解码的 src 和一个错误。
// 换行字符（\r 和 \n）会被忽略。
func (enc *Encoding) AppendDecode(dst, src []byte) ([]byte, error) {
	// 计算不带填充的输出大小，以避免过度分配。
	n := len(src)
	for n > 0 && rune(src[n-1]) == enc.padChar {
		n--
	}
	n = decodedLen(n, NoPadding)

	dst = slices.Grow(dst, n)
	n, err := enc.Decode(dst[len(dst):][:n], src)
	return dst[:len(dst)+n], err
}

// DecodeString 返回 base32 字符串 s 所表示的字节。
// 如果输入格式错误，它返回部分解码的数据以及
// [CorruptInputError]。换行字符（\r 和 \n）会被忽略。
func (enc *Encoding) DecodeString(s string) ([]byte, error) {
	buf := []byte(s)
	l := stripNewlines(buf, buf)
	n, _, err := enc.decode(buf, buf[:l])
	return buf[:n], err
}

type decoder struct {
	err    error
	enc    *Encoding
	r      io.Reader
	end    bool       // 已看到消息结束
	buf    [1024]byte // 剩余输入
	nbuf   int
	out    []byte // 剩余的解码输出
	outbuf [1024 / 8 * 5]byte
}

func readEncodedData(r io.Reader, buf []byte, min int, expectsPadding bool) (n int, err error) {
	for n < min && err == nil {
		var nn int
		nn, err = r.Read(buf[n:])
		n += nn
	}
	// 已读取数据，但读取的字节数少于 min
	if n < min && n > 0 && err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	// 没有读取到数据，缓冲区已包含一些数据
	// 当禁用填充时，这不是错误，因为消息可以是
	// 任意长度
	if expectsPadding && min < 8 && n == 0 && err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return
}

func (d *decoder) Read(p []byte) (n int, err error) {
	// 使用上次读取留下的已解码输出。
	if len(d.out) > 0 {
		n = copy(p, d.out)
		d.out = d.out[n:]
		if len(d.out) == 0 {
			return n, d.err
		}
		return n, nil
	}

	if d.err != nil {
		return 0, d.err
	}

	// 读取一个块。
	nn := (len(p) + 4) / 5 * 8
	if nn < 8 {
		nn = 8
	}
	if nn > len(d.buf) {
		nn = len(d.buf)
	}

	// 每个周期需要读取的最小字节数
	var min int
	var expectsPadding bool
	if d.enc.padChar == NoPadding {
		min = 1
		expectsPadding = false
	} else {
		min = 8 - d.nbuf
		expectsPadding = true
	}

	nn, d.err = readEncodedData(d.r, d.buf[d.nbuf:nn], min, expectsPadding)
	d.nbuf += nn
	if d.nbuf < min {
		return 0, d.err
	}
	if nn > 0 && d.end {
		return 0, CorruptInputError(0)
	}

	// 将块解码到 p，或者如果 p 太小，先解码到 d.out 再拷贝到 p。
	var nr int
	if d.enc.padChar == NoPadding {
		nr = d.nbuf
	} else {
		nr = d.nbuf / 8 * 8
	}
	nw := d.enc.DecodedLen(d.nbuf)

	if nw > len(p) {
		nw, d.end, err = d.enc.decode(d.outbuf[0:], d.buf[0:nr])
		d.out = d.outbuf[0:nw]
		n = copy(p, d.out)
		d.out = d.out[n:]
	} else {
		n, d.end, err = d.enc.decode(p, d.buf[0:nr])
	}
	d.nbuf -= nr
	for i := 0; i < d.nbuf; i++ {
		d.buf[i] = d.buf[i+nr]
	}

	if err != nil && (d.err == nil || d.err == io.EOF) {
		d.err = err
	}

	if len(d.out) > 0 {
		// 在这次 Read 调用中我们无法将所有已解码的字节返回给调用者，
		// 因此返回 nil 错误以确保 Read 会被再次调用。
		// d.err 中存储的错误（如果有）将随最后一组已解码字节一起返回。
		return n, nil
	}

	return n, d.err
}

type newlineFilteringReader struct {
	wrapped io.Reader
}

// stripNewlines 移除换行字符，并返回复制到 dst 的
// 非换行字符数。
func stripNewlines(dst, src []byte) int {
	offset := 0
	for _, b := range src {
		if b == '\r' || b == '\n' {
			continue
		}
		dst[offset] = b
		offset++
	}
	return offset
}

func (r *newlineFilteringReader) Read(p []byte) (int, error) {
	n, err := r.wrapped.Read(p)
	for n > 0 {
		s := p[0:n]
		offset := stripNewlines(s, s)
		if err != nil || offset > 0 {
			return offset, err
		}
		// 前一个缓冲区全是空白，再次读取
		n, err = r.wrapped.Read(p)
	}
	return n, err
}

// NewDecoder 构造一个新的 base32 流解码器。
func NewDecoder(enc *Encoding, r io.Reader) io.Reader {
	return &decoder{enc: enc, r: &newlineFilteringReader{r}}
}

// DecodedLen 返回与 n 字节 base32 编码数据相对应的
// 已解码数据的最大字节长度。
func (enc *Encoding) DecodedLen(n int) int {
	return decodedLen(n, enc.padChar)
}

func decodedLen(n int, padChar rune) int {
	if padChar == NoPadding {
		return n/8*5 + n%8*5/8
	}
	return n / 8 * 5
}
