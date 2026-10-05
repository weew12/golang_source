// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lzw 实现了 Lempel-Ziv-Welch 压缩数据格式，
// 该格式在 T. A. Welch 的 “A Technique for High-Performance Data
// Compression”, Computer, 17(6) (June 1984), pp 8-19 中有描述。
//
// 具体而言，它实现了 GIF 和 PDF 文件格式所使用的 LZW，
// 这意味着可变宽度编码最多可达 12 位，并且前两个
// 非字面量编码分别是清除码和 EOF 码。
//
// TIFF 文件格式使用了 LZW 算法的一个类似但不兼容的版本。
// 参见 [golang.org/x/image/tiff/lzw] 包中的实现。
package lzw

// TODO(nigeltao): 检查 PDF 是否以与 GIF 相同的方式使用 LZW，
// 只是在 LSB/MSB 打包顺序上有所不同。

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// Order 指定 LZW 数据流中的位顺序。
type Order int

const (
	// LSB 表示最低有效位在前（Least Significant Bits first），GIF 文件格式即使用该顺序。
	LSB Order = iota
	// MSB 表示最高有效位在前（Most Significant Bits first），TIFF 和 PDF
	// 文件格式即使用该顺序。
	MSB
)

const (
	maxWidth           = 12
	decoderInvalidCode = 0xffff
	flushBuffer        = 1 << maxWidth
)

// Reader 是一个 [io.Reader]，可用于读取 LZW 格式的压缩数据。
type Reader struct {
	r        io.ByteReader
	bits     uint32
	nBits    uint
	width    uint
	read     func(*Reader) (uint16, error) // readLSB 或 readMSB
	litWidth int                           // 字面量编码的位宽
	err      error

	// 前 1<<litWidth 个编码是字面量编码。
	// 接下来的两个编码表示 clear 和 EOF。
	// 其他有效编码位于范围 [lo, hi] 内，其中 lo := clear + 2，
	// 上界随着每遇到一个编码而递增。
	//
	// overflow 是 hi 溢出编码宽度的那个编码。它始终
	// 等于 1 << width。
	//
	// last 是最近遇到的编码，或 decoderInvalidCode。
	//
	// 一个不变量是 hi < overflow。
	clear, eof, hi, overflow, last uint16

	// [lo, hi] 中的每个编码 c 展开为两个或更多字节。对于 c != hi：
	//   suffix[c] 是这些字节中的最后一个。
	//   prefix[c] 是除最后一个字节外所有字节对应的编码。
	//   该编码可以是字面量编码，也可以是 [lo, c) 中的另一个编码。
	// c == hi 的情况是一种特殊情况。
	suffix [1 << maxWidth]uint8
	prefix [1 << maxWidth]uint16

	// output 是临时输出缓冲区。
	// 字面量编码从缓冲区开头开始累积。
	// 非字面量编码解码为一系列后缀，这些后缀首先
	// 从缓冲区末尾从右向左写入，然后再复制
	// 到缓冲区开头。
	// 当它包含 >= 1<<maxWidth 字节时会被刷新，
	// 以便始终有空间解码一个完整的编码。
	output [2 * 1 << maxWidth]byte
	o      int    // output 中的写入索引
	toRead []byte // 要从 Read 返回的字节
}

// readLSB 返回 "Least Significant Bits first" 数据的下一个编码。
func (r *Reader) readLSB() (uint16, error) {
	for r.nBits < r.width {
		x, err := r.r.ReadByte()
		if err != nil {
			return 0, err
		}
		r.bits |= uint32(x) << r.nBits
		r.nBits += 8
	}
	code := uint16(r.bits & (1<<r.width - 1))
	r.bits >>= r.width
	r.nBits -= r.width
	return code, nil
}

// readMSB 返回 "Most Significant Bits first" 数据的下一个编码。
func (r *Reader) readMSB() (uint16, error) {
	for r.nBits < r.width {
		x, err := r.r.ReadByte()
		if err != nil {
			return 0, err
		}
		r.bits |= uint32(x) << (24 - r.nBits)
		r.nBits += 8
	}
	code := uint16(r.bits >> (32 - r.width))
	r.bits <<= r.width
	r.nBits -= r.width
	return code, nil
}

// Read 实现 io.Reader，从其底层 reader 读取未压缩的字节。
func (r *Reader) Read(b []byte) (int, error) {
	for {
		if len(r.toRead) > 0 {
			n := copy(b, r.toRead)
			r.toRead = r.toRead[n:]
			return n, nil
		}
		if r.err != nil {
			return 0, r.err
		}
		r.decode()
	}
}

// decode 从 r 解压字节并将它们留在 d.toRead 中。
// read 指定如何将字节解码为编码。
// litWidth 是字面量编码的位宽。
func (r *Reader) decode() {
	// 遍历编码流，将编码转换为解压后的字节。
loop:
	for {
		code, err := r.read(r)
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			r.err = err
			break
		}
		switch {
		case code < r.clear:
			// 我们遇到了一个字面量编码。
			r.output[r.o] = uint8(code)
			r.o++
			if r.last != decoderInvalidCode {
				// 保存 hi 编码展开后的内容。
				r.suffix[r.hi] = uint8(code)
				r.prefix[r.hi] = r.last
			}
		case code == r.clear:
			r.width = 1 + uint(r.litWidth)
			r.hi = r.eof
			r.overflow = 1 << r.width
			r.last = decoderInvalidCode
			continue
		case code == r.eof:
			r.err = io.EOF
			break loop
		case code <= r.hi:
			c, i := code, len(r.output)-1
			if code == r.hi && r.last != decoderInvalidCode {
				// code == hi 是一种特殊情况，它展开为上一次展开
				// 后接上一次展开的头部。要找到头部，我们沿
				// 前缀链回溯，直到找到字面量编码。
				c = r.last
				for c >= r.clear {
					c = r.prefix[c]
				}
				r.output[i] = uint8(c)
				i--
				c = r.last
			}
			// 将后缀链复制到 output，然后将其写入 w。
			for c >= r.clear {
				r.output[i] = r.suffix[c]
				i--
				c = r.prefix[c]
			}
			r.output[i] = uint8(c)
			r.o += copy(r.output[r.o:], r.output[i:])
			if r.last != decoderInvalidCode {
				// 保存 hi 编码展开后的内容。
				r.suffix[r.hi] = uint8(c)
				r.prefix[r.hi] = r.last
			}
		default:
			r.err = errors.New("lzw: invalid code")
			break loop
		}
		r.last, r.hi = code, r.hi+1
		if r.hi >= r.overflow {
			if r.hi > r.overflow {
				panic("unreachable")
			}
			if r.width == maxWidth {
				r.last = decoderInvalidCode
				// 撤销上面几行中的 d.hi++，以便 (1) 维持
				// d.hi < d.overflow 这一不变量，并且 (2) d.hi 最终不会
				// 溢出 uint16。
				r.hi--
			} else {
				r.width++
				r.overflow = 1 << r.width
			}
		}
		if r.o >= flushBuffer {
			break
		}
	}
	// 刷新待处理的输出。
	r.toRead = r.output[:r.o]
	r.o = 0
}

var errClosed = errors.New("lzw: reader/writer is closed")

// Close 关闭 [Reader]，并为之后的任何读取操作返回一个错误。
// 它不会关闭底层的 [io.Reader]。
func (r *Reader) Close() error {
	r.err = errClosed // 以防有 Read 操作发生
	return nil
}

// Reset 清除 [Reader] 的状态，并允许它作为一个新的 [Reader]
// 再次被复用。
func (r *Reader) Reset(src io.Reader, order Order, litWidth int) {
	*r = Reader{}
	r.init(src, order, litWidth)
}

// NewReader 创建一个新的 [io.ReadCloser]。
// 从返回的 [io.ReadCloser] 读取会读取并解压来自 r 的数据。
// 如果 r 没有同时实现 [io.ByteReader]，
// 解压器可能会从 r 读取超出所需的数据。
// 读取完成后，调用者有责任对 ReadCloser 调用 Close。
// 用于字面量编码的位数 litWidth 必须位于
// 范围 [2,8] 内，且通常为 8。它必须等于
// 压缩时使用的 litWidth。
//
// 保证返回的 [io.ReadCloser] 的底层类型
// 是 *[Reader]。
func NewReader(r io.Reader, order Order, litWidth int) io.ReadCloser {
	return newReader(r, order, litWidth)
}

func newReader(src io.Reader, order Order, litWidth int) *Reader {
	r := new(Reader)
	r.init(src, order, litWidth)
	return r
}

func (r *Reader) init(src io.Reader, order Order, litWidth int) {
	switch order {
	case LSB:
		r.read = (*Reader).readLSB
	case MSB:
		r.read = (*Reader).readMSB
	default:
		r.err = errors.New("lzw: unknown order")
		return
	}
	if litWidth < 2 || 8 < litWidth {
		r.err = fmt.Errorf("lzw: litWidth %d out of range", litWidth)
		return
	}

	br, ok := src.(io.ByteReader)
	if !ok && src != nil {
		br = bufio.NewReader(src)
	}
	r.r = br
	r.litWidth = litWidth
	r.width = 1 + uint(litWidth)
	r.clear = uint16(1) << uint(litWidth)
	r.eof, r.hi = r.clear+1, r.clear+1
	r.overflow = uint16(1) << r.width
	r.last = decoderInvalidCode
}
