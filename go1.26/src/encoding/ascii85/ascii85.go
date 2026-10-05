// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ascii85 实现 ascii85 数据编码，
// 该编码用于 btoa 工具以及 Adobe 的 PostScript 和 PDF 文档格式。
package ascii85

import (
	"io"
	"strconv"
)

/*
 * 编码器
 */

// Encode 将 src 编码到 dst 的至多 [MaxEncodedLen](len(src))
// 个字节中，并返回实际写入的字节数。
//
// 该编码以 4 字节为块进行处理，对最后一个片段使用特殊编码，
// 因此 Encode 不适合用于大型数据流的单独块。请改用 [NewEncoder]。
//
// ascii85 编码的数据通常被包裹在 <~ 和 ~> 符号中。
// Encode 不会添加这些符号。
func Encode(dst, src []byte) int {
	if len(src) == 0 {
		return 0
	}

	n := 0
	for len(src) > 0 {
		dst[0] = 0
		dst[1] = 0
		dst[2] = 0
		dst[3] = 0
		dst[4] = 0

		// 将 4 个字节解包为 uint32，以便重新打包为 base 85 的 5 个字节。
		var v uint32
		switch len(src) {
		default:
			v |= uint32(src[3])
			fallthrough
		case 3:
			v |= uint32(src[2]) << 8
			fallthrough
		case 2:
			v |= uint32(src[1]) << 16
			fallthrough
		case 1:
			v |= uint32(src[0]) << 24
		}

		// 特殊情况：零（!!!!!）缩短为 z。
		if v == 0 && len(src) >= 4 {
			dst[0] = 'z'
			dst = dst[1:]
			src = src[4:]
			n++
			continue
		}

		// 否则，生成从 ! 开始的 5 个 base 85 数字。
		for i := 4; i >= 0; i-- {
			dst[i] = '!' + byte(v%85)
			v /= 85
		}

		// 如果 src 较短，则丢弃低位目标字节。
		m := 5
		if len(src) < 4 {
			m -= 4 - len(src)
			src = nil
		} else {
			src = src[4:]
		}
		dst = dst[m:]
		n += m
	}
	return n
}

// MaxEncodedLen 返回 n 个源字节编码后的最大长度。
func MaxEncodedLen(n int) int { return (n + 3) / 4 * 5 }

// NewEncoder 返回一个新的 ascii85 流编码器。写入所返回 writer 的数据
// 将被编码，然后写入 w。
// Ascii85 编码以 32 位块为操作单位；写入完成后，
// 调用方必须对所返回的编码器调用 Close 以刷新任何尾部残缺块。
func NewEncoder(w io.Writer) io.WriteCloser { return &encoder{w: w} }

type encoder struct {
	err  error
	w    io.Writer
	buf  [4]byte    // 等待编码的缓冲数据
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
		for i = 0; i < len(p) && e.nbuf < 4; i++ {
			e.buf[e.nbuf] = p[i]
			e.nbuf++
		}
		n += i
		p = p[i:]
		if e.nbuf < 4 {
			return
		}
		nout := Encode(e.out[0:], e.buf[0:])
		if _, e.err = e.w.Write(e.out[0:nout]); e.err != nil {
			return n, e.err
		}
		e.nbuf = 0
	}

	// 大型内部块。
	for len(p) >= 4 {
		nn := len(e.out) / 5 * 4
		if nn > len(p) {
			nn = len(p)
		}
		nn -= nn % 4
		if nn > 0 {
			nout := Encode(e.out[0:], p[0:nn])
			if _, e.err = e.w.Write(e.out[0:nout]); e.err != nil {
				return n, e.err
			}
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

// Close 刷新编码器中任何待处理的输出。
// 在调用 Close 之后再调用 Write 是错误的。
func (e *encoder) Close() error {
	// 如果缓冲区中还有任何内容，就将其刷新出去
	if e.err == nil && e.nbuf > 0 {
		nout := Encode(e.out[0:], e.buf[0:e.nbuf])
		e.nbuf = 0
		_, e.err = e.w.Write(e.out[0:nout])
	}
	return e.err
}

/*
 * 解码器
 */

type CorruptInputError int64

func (e CorruptInputError) Error() string {
	return "illegal ascii85 data at input byte " + strconv.FormatInt(int64(e), 10)
}

// Decode 将 src 解码到 dst，同时返回写入 dst 的字节数
// 和从 src 消耗的字节数。
// 如果 src 包含无效的 ascii85 数据，Decode 将返回成功写入的
// 字节数和一个 [CorruptInputError]。
// Decode 会忽略 src 中的空格和控制字符。
// ascii85 编码的数据通常被包裹在 <~ 和 ~> 符号中。
// Decode 假定调用方已去除这些符号。
//
// 如果 flush 为 true，Decode 假定 src 表示输入流的结尾，
// 并完整地处理它，而不是等待另一个 32 位块完成。
//
// [NewDecoder] 在 Decode 之上封装了一个 [io.Reader] 接口。
func Decode(dst, src []byte, flush bool) (ndst, nsrc int, err error) {
	var v uint32
	var nb int
	for i, b := range src {
		if len(dst)-ndst < 4 {
			return
		}
		switch {
		case b <= ' ':
			continue
		case b == 'z' && nb == 0:
			nb = 5
			v = 0
		case '!' <= b && b <= 'u':
			v = v*85 + uint32(b-'!')
			nb++
		default:
			return 0, 0, CorruptInputError(i)
		}
		if nb == 5 {
			nsrc = i + 1
			dst[ndst] = byte(v >> 24)
			dst[ndst+1] = byte(v >> 16)
			dst[ndst+2] = byte(v >> 8)
			dst[ndst+3] = byte(v)
			ndst += 4
			nb = 0
			v = 0
		}
	}
	if flush {
		nsrc = len(src)
		if nb > 0 {
			// 最后一个片段中的输出字节数是剩余输入字节数减 1：
			// 多出的那个字节提供了足够的位来弥补
			// 该块编码的效率损失。
			if nb == 1 {
				return 0, 0, CorruptInputError(len(src))
			}
			for i := nb; i < 5; i++ {
				// 短编码截断了输出值。
				// 我们必须假定最坏情况下的值（数字 84），
				// 以确保高位的正确性。
				v = v*85 + 84
			}
			for i := 0; i < nb-1; i++ {
				dst[ndst] = byte(v >> 24)
				v <<= 8
				ndst++
			}
		}
	}
	return
}

// NewDecoder 构造一个新的 ascii85 流解码器。
func NewDecoder(r io.Reader) io.Reader { return &decoder{r: r} }

type decoder struct {
	err     error
	readErr error
	r       io.Reader
	buf     [1024]byte // 剩余的输入
	nbuf    int
	out     []byte // 剩余的解码输出
	outbuf  [1024]byte
}

func (d *decoder) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.err != nil {
		return 0, d.err
	}

	for {
		// 复制上一次解码剩余的输出。
		if len(d.out) > 0 {
			n = copy(p, d.out)
			d.out = d.out[n:]
			return
		}

		// 解码上一次读取剩余的输入。
		var nn, nsrc, ndst int
		if d.nbuf > 0 {
			ndst, nsrc, d.err = Decode(d.outbuf[0:], d.buf[0:d.nbuf], d.readErr != nil)
			if ndst > 0 {
				d.out = d.outbuf[0:ndst]
				d.nbuf = copy(d.buf[0:], d.buf[nsrc:d.nbuf])
				continue // 复制输出并返回
			}
			if ndst == 0 && d.err == nil {
				// 特殊情况：输入缓冲区大部分被非数据字节填充。
				// 过滤掉这些字节，为更多输入腾出空间。
				off := 0
				for i := 0; i < d.nbuf; i++ {
					if d.buf[i] > ' ' {
						d.buf[off] = d.buf[i]
						off++
					}
				}
				d.nbuf = off
			}
		}

		// 输入耗尽，解码输出耗尽。检查错误。
		if d.err != nil {
			return 0, d.err
		}
		if d.readErr != nil {
			d.err = d.readErr
			return 0, d.err
		}

		// 读取更多数据。
		nn, d.readErr = d.r.Read(d.buf[d.nbuf:])
		d.nbuf += nn
	}
}
