// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package base64 实现 RFC 4648 所规定的 base64 编码。
package base64

import (
	"internal/byteorder"
	"io"
	"slices"
	"strconv"
)

/*
 * 编码
 */

// Encoding 是一种基数 64 的编码/解码方案，由 64 个字符的字母表定义。
// 最常见的编码是 RFC 4648 中定义、并用于 MIME（RFC 2045）和 PEM
// （RFC 1421）的 "base64" 编码。RFC 4648 还定义了一种替代编码，
// 即用 - 和 _ 替换 + 和 / 的标准编码。
type Encoding struct {
	encode    [64]byte   // 符号索引到符号字节值的映射
	decodeMap [256]uint8 // 符号字节值到符号索引的映射
	padChar   rune
	strict    bool
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
// 该字母表必须是包含唯一字节值的 64 字节字符串，
// 且不包含填充字符或 CR / LF（'\r'、'\n'）。
// 字母表被视为字节值序列，对多字节 UTF-8 不做任何特殊处理。
// 生成的 Encoding 使用默认填充字符（'='），
// 可通过 [Encoding.WithPadding] 更改或禁用。
func NewEncoding(encoder string) *Encoding {
	if len(encoder) != 64 {
		panic("encoding alphabet is not 64-bytes long")
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

// WithPadding 创建一个新的编码，除使用指定的填充字符外与 enc 相同，
// 或使用 [NoPadding] 禁用填充。
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

// Strict 创建一个新的编码，除启用严格解码外与 enc 相同。
// 在此模式下，解码器要求末尾填充位为零，
// 如 RFC 4648 第 3.5 节所述。
//
// 注意，输入仍然具有可塑性，因为换行字符（CR 和 LF）仍会被忽略。
func (enc Encoding) Strict() *Encoding {
	enc.strict = true
	return &enc
}

// StdEncoding 是 RFC 4648 中定义的标准 base64 编码。
var StdEncoding = NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/")

// URLEncoding 是 RFC 4648 中定义的替代 base64 编码。
// 它通常用于 URL 和文件名。
var URLEncoding = NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_")

// RawStdEncoding 是 RFC 4648 第 3.2 节中定义的标准原始、无填充 base64 编码。
// 它与 [StdEncoding] 相同，但省略了填充字符。
var RawStdEncoding = StdEncoding.WithPadding(NoPadding)

// RawURLEncoding 是 RFC 4648 中定义的无填充替代 base64 编码。
// 它通常用于 URL 和文件名。
// 它与 [URLEncoding] 相同，但省略了填充字符。
var RawURLEncoding = URLEncoding.WithPadding(NoPadding)

/*
 * 编码器
 */

// Encode 使用编码 enc 对 src 进行编码，
// 向 dst 写入 [Encoding.EncodedLen](len(src)) 个字节。
//
// 该编码将输出填充到 4 字节的倍数，
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
	n := (len(src) / 3) * 3
	for si < n {
		// 将 3 个 8 位源字节转换为 4 个字节
		val := uint(src[si+0])<<16 | uint(src[si+1])<<8 | uint(src[si+2])

		dst[di+0] = enc.encode[val>>18&0x3F]
		dst[di+1] = enc.encode[val>>12&0x3F]
		dst[di+2] = enc.encode[val>>6&0x3F]
		dst[di+3] = enc.encode[val&0x3F]

		si += 3
		di += 4
	}

	remain := len(src) - si
	if remain == 0 {
		return
	}
	// 添加剩余的小块
	val := uint(src[si+0]) << 16
	if remain == 2 {
		val |= uint(src[si+1]) << 8
	}

	dst[di+0] = enc.encode[val>>18&0x3F]
	dst[di+1] = enc.encode[val>>12&0x3F]

	switch remain {
	case 2:
		dst[di+2] = enc.encode[val>>6&0x3F]
		if enc.padChar != NoPadding {
			dst[di+3] = byte(enc.padChar)
		}
	case 1:
		if enc.padChar != NoPadding {
			dst[di+2] = byte(enc.padChar)
			dst[di+3] = byte(enc.padChar)
		}
	}
}

// AppendEncode 将 base64 编码后的 src 追加到 dst
// 并返回扩展后的缓冲区。
func (enc *Encoding) AppendEncode(dst, src []byte) []byte {
	n := enc.EncodedLen(len(src))
	dst = slices.Grow(dst, n)
	enc.Encode(dst[len(dst):][:n], src)
	return dst[:len(dst)+n]
}

// EncodeToString 返回 src 的 base64 编码。
func (enc *Encoding) EncodeToString(src []byte) string {
	buf := make([]byte, enc.EncodedLen(len(src)))
	enc.Encode(buf, src)
	return string(buf)
}

type encoder struct {
	err  error
	enc  *Encoding
	w    io.Writer
	buf  [3]byte    // 等待编码的缓冲数据
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
		for i = 0; i < len(p) && e.nbuf < 3; i++ {
			e.buf[e.nbuf] = p[i]
			e.nbuf++
		}
		n += i
		p = p[i:]
		if e.nbuf < 3 {
			return
		}
		e.enc.Encode(e.out[:], e.buf[:])
		if _, e.err = e.w.Write(e.out[:4]); e.err != nil {
			return n, e.err
		}
		e.nbuf = 0
	}

	// 大的内部块。
	for len(p) >= 3 {
		nn := len(e.out) / 4 * 3
		if nn > len(p) {
			nn = len(p)
			nn -= nn % 3
		}
		e.enc.Encode(e.out[:], p[:nn])
		if _, e.err = e.w.Write(e.out[0 : nn/3*4]); e.err != nil {
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
		e.enc.Encode(e.out[:], e.buf[:e.nbuf])
		_, e.err = e.w.Write(e.out[:e.enc.EncodedLen(e.nbuf)])
		e.nbuf = 0
	}
	return e.err
}

// NewEncoder 返回一个新的 base64 流编码器。写入返回的 writer 的数据
// 将使用 enc 编码，然后写入 w。
// Base64 编码以 4 字节块为操作单位；写入完成后，
// 调用者必须 Close 返回的编码器，以刷新任何
// 部分写入的块。
func NewEncoder(enc *Encoding, w io.Writer) io.WriteCloser {
	return &encoder{enc: enc, w: w}
}

// EncodedLen 返回长度为 n 的输入缓冲区
// 的 base64 编码的字节长度。
func (enc *Encoding) EncodedLen(n int) int {
	if enc.padChar == NoPadding {
		return n/3*4 + (n%3*8+5)/6 // 每个字符 6 位时的最少字符数
	}
	return (n + 2) / 3 * 4 // 最少 4 字符量子数，每个 3 字节
}

/*
 * 解码器
 */

type CorruptInputError int64

func (e CorruptInputError) Error() string {
	return "illegal base64 data at input byte " + strconv.FormatInt(int64(e), 10)
}

// decodeQuantum 解码最多 4 个 base64 字节。接收的参数为
// 目标缓冲区 dst、源缓冲区 src 以及源缓冲区中的索引 si。
// 它返回从 src 读取的字节数、写入 dst 的字节数，
// 以及错误（如果有）。
func (enc *Encoding) decodeQuantum(dst, src []byte, si int) (nsi, n int, err error) {
	// 使用 base64 字母表解码量子
	var dbuf [4]byte
	dlen := 4

	// 将 nil 检查提升到循环之外。
	_ = enc.decodeMap

	for j := 0; j < len(dbuf); j++ {
		if len(src) == si {
			switch {
			case j == 0:
				return si, 0, nil
			case j == 1, enc.padChar != NoPadding:
				return si, 0, CorruptInputError(si - j)
			}
			dlen = j
			break
		}
		in := src[si]
		si++

		out := enc.decodeMap[in]
		if out != 0xff {
			dbuf[j] = out
			continue
		}

		if in == '\n' || in == '\r' {
			j--
			continue
		}

		if rune(in) != enc.padChar {
			return si, 0, CorruptInputError(si - 1)
		}

		// 已到达末尾，且有填充
		switch j {
		case 0, 1:
			// 填充不正确
			return si, 0, CorruptInputError(si - 1)
		case 2:
			// 期望 "=="，第一个 "=" 已被消耗。
			// 跳过换行符
			for si < len(src) && (src[si] == '\n' || src[si] == '\r') {
				si++
			}
			if si == len(src) {
				// 填充不足
				return si, 0, CorruptInputError(len(src))
			}
			if rune(src[si]) != enc.padChar {
				// 填充不正确
				return si, 0, CorruptInputError(si - 1)
			}

			si++
		}

		// 跳过换行符
		for si < len(src) && (src[si] == '\n' || src[si] == '\r') {
			si++
		}
		if si < len(src) {
			// 尾部垃圾数据
			err = CorruptInputError(si)
		}
		dlen = j
		break
	}

	// 将 4 个 6 位源字节转换为 3 个字节
	val := uint(dbuf[0])<<18 | uint(dbuf[1])<<12 | uint(dbuf[2])<<6 | uint(dbuf[3])
	dbuf[2], dbuf[1], dbuf[0] = byte(val>>0), byte(val>>8), byte(val>>16)
	switch dlen {
	case 4:
		dst[2] = dbuf[2]
		dbuf[2] = 0
		fallthrough
	case 3:
		dst[1] = dbuf[1]
		if enc.strict && dbuf[2] != 0 {
			return si, 0, CorruptInputError(si - 1)
		}
		dbuf[1] = 0
		fallthrough
	case 2:
		dst[0] = dbuf[0]
		if enc.strict && (dbuf[1] != 0 || dbuf[2] != 0) {
			return si, 0, CorruptInputError(si - 2)
		}
	}

	return si, dlen - 1, err
}

// AppendDecode 将 base64 解码后的 src 追加到 dst
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

// DecodeString 返回 base64 字符串 s 所表示的字节。
// 如果输入格式错误，它返回部分解码的数据以及
// [CorruptInputError]。换行字符（\r 和 \n）会被忽略。
func (enc *Encoding) DecodeString(s string) ([]byte, error) {
	dbuf := make([]byte, enc.DecodedLen(len(s)))
	n, err := enc.Decode(dbuf, []byte(s))
	return dbuf[:n], err
}

type decoder struct {
	err     error
	readErr error // 来自 r.Read 的错误
	enc     *Encoding
	r       io.Reader
	buf     [1024]byte // 剩余输入
	nbuf    int
	out     []byte // 剩余的解码输出
	outbuf  [1024 / 4 * 3]byte
}

func (d *decoder) Read(p []byte) (n int, err error) {
	// 使用上次读取留下的已解码输出。
	if len(d.out) > 0 {
		n = copy(p, d.out)
		d.out = d.out[n:]
		return n, nil
	}

	if d.err != nil {
		return 0, d.err
	}

	// 此代码假定 d.r 会去除受支持的空白字符（'\r' 和 '\n'）。

	// 重新填充缓冲区。
	for d.nbuf < 4 && d.readErr == nil {
		nn := len(p) / 3 * 4
		if nn < 4 {
			nn = 4
		}
		if nn > len(d.buf) {
			nn = len(d.buf)
		}
		nn, d.readErr = d.r.Read(d.buf[d.nbuf:nn])
		d.nbuf += nn
	}

	if d.nbuf < 4 {
		if d.enc.padChar == NoPadding && d.nbuf > 0 {
			// 解码最后一段，不带填充。
			var nw int
			nw, d.err = d.enc.Decode(d.outbuf[:], d.buf[:d.nbuf])
			d.nbuf = 0
			d.out = d.outbuf[:nw]
			n = copy(p, d.out)
			d.out = d.out[n:]
			if n > 0 || len(p) == 0 && len(d.out) > 0 {
				return n, nil
			}
			if d.err != nil {
				return 0, d.err
			}
		}
		d.err = d.readErr
		if d.err == io.EOF && d.nbuf > 0 {
			d.err = io.ErrUnexpectedEOF
		}
		return 0, d.err
	}

	// 将块解码到 p，或者如果 p 太小，先解码到 d.out 再拷贝到 p。
	nr := d.nbuf / 4 * 4
	nw := d.nbuf / 4 * 3
	if nw > len(p) {
		nw, d.err = d.enc.Decode(d.outbuf[:], d.buf[:nr])
		d.out = d.outbuf[:nw]
		n = copy(p, d.out)
		d.out = d.out[n:]
	} else {
		n, d.err = d.enc.Decode(p, d.buf[:nr])
	}
	d.nbuf -= nr
	copy(d.buf[:d.nbuf], d.buf[nr:])
	return n, d.err
}

// Decode 使用编码 enc 解码 src。它最多向 dst 写入
// [Encoding.DecodedLen](len(src)) 个字节，并返回写入的字节数。
// 调用者必须确保 dst 足够大以容纳所有解码后的数据。
// 如果 src 包含无效的 base64 数据，它会返回成功写入的
// 字节数以及 [CorruptInputError]。
// 换行字符（\r 和 \n）会被忽略。
func (enc *Encoding) Decode(dst, src []byte) (n int, err error) {
	if len(src) == 0 {
		return 0, nil
	}

	// 将 nil 检查提升到循环之外。enc.decodeMap 在本函数稍后
	// 会被直接使用，以让编译器知道
	// 接收者不可能是 nil。
	_ = enc.decodeMap

	si := 0
	for strconv.IntSize >= 64 && len(src)-si >= 8 && len(dst)-n >= 8 {
		src2 := src[si : si+8]
		if dn, ok := assemble64(
			enc.decodeMap[src2[0]],
			enc.decodeMap[src2[1]],
			enc.decodeMap[src2[2]],
			enc.decodeMap[src2[3]],
			enc.decodeMap[src2[4]],
			enc.decodeMap[src2[5]],
			enc.decodeMap[src2[6]],
			enc.decodeMap[src2[7]],
		); ok {
			byteorder.BEPutUint64(dst[n:], dn)
			n += 6
			si += 8
		} else {
			var ninc int
			si, ninc, err = enc.decodeQuantum(dst[n:], src, si)
			n += ninc
			if err != nil {
				return n, err
			}
		}
	}

	for len(src)-si >= 4 && len(dst)-n >= 4 {
		src2 := src[si : si+4]
		if dn, ok := assemble32(
			enc.decodeMap[src2[0]],
			enc.decodeMap[src2[1]],
			enc.decodeMap[src2[2]],
			enc.decodeMap[src2[3]],
		); ok {
			byteorder.BEPutUint32(dst[n:], dn)
			n += 3
			si += 4
		} else {
			var ninc int
			si, ninc, err = enc.decodeQuantum(dst[n:], src, si)
			n += ninc
			if err != nil {
				return n, err
			}
		}
	}

	for si < len(src) {
		var ninc int
		si, ninc, err = enc.decodeQuantum(dst[n:], src, si)
		n += ninc
		if err != nil {
			return n, err
		}
	}
	return n, err
}

// assemble32 将 4 个 base64 数字组合成 3 个字节。
// 每个数字来自解码映射，如果它来自无效字符，
// 则为 0xff。
func assemble32(n1, n2, n3, n4 byte) (dn uint32, ok bool) {
	// 检查所有数字都有效。如果其中任何一个是 0xff，它们的
	// 按位 OR 结果将是 0xff。
	if n1|n2|n3|n4 == 0xff {
		return 0, false
	}
	return uint32(n1)<<26 |
			uint32(n2)<<20 |
			uint32(n3)<<14 |
			uint32(n4)<<8,
		true
}

// assemble64 将 8 个 base64 数字组合成 6 个字节。
// 每个数字来自解码映射，如果它来自无效字符，
// 则为 0xff。
func assemble64(n1, n2, n3, n4, n5, n6, n7, n8 byte) (dn uint64, ok bool) {
	// 检查所有数字都有效。如果其中任何一个是 0xff，它们的
	// 按位 OR 结果将是 0xff。
	if n1|n2|n3|n4|n5|n6|n7|n8 == 0xff {
		return 0, false
	}
	return uint64(n1)<<58 |
			uint64(n2)<<52 |
			uint64(n3)<<46 |
			uint64(n4)<<40 |
			uint64(n5)<<34 |
			uint64(n6)<<28 |
			uint64(n7)<<22 |
			uint64(n8)<<16,
		true
}

type newlineFilteringReader struct {
	wrapped io.Reader
}

func (r *newlineFilteringReader) Read(p []byte) (int, error) {
	n, err := r.wrapped.Read(p)
	for n > 0 {
		offset := 0
		for i, b := range p[:n] {
			if b != '\r' && b != '\n' {
				if i != offset {
					p[offset] = b
				}
				offset++
			}
		}
		if offset > 0 {
			return offset, err
		}
		// 前一个缓冲区全是空白，再次读取
		n, err = r.wrapped.Read(p)
	}
	return n, err
}

// NewDecoder 构造一个新的 base64 流解码器。
func NewDecoder(enc *Encoding, r io.Reader) io.Reader {
	return &decoder{enc: enc, r: &newlineFilteringReader{r}}
}

// DecodedLen 返回与 n 字节 base64 编码数据相对应的
// 已解码数据的最大字节长度。
func (enc *Encoding) DecodedLen(n int) int {
	return decodedLen(n, enc.padChar)
}

func decodedLen(n int, padChar rune) int {
	if padChar == NoPadding {
		// 不带填充的数据可能以 2-3 个字符的部分块结尾。
		return n/4*3 + n%4*6/8
	}
	// 带填充的 base64 长度始终应是 4 个字符的倍数。
	return n / 4 * 3
}
