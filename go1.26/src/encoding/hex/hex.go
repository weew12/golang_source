// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package hex 实现十六进制编码和解码。
package hex

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	hextable        = "0123456789abcdef"
	reverseHexTable = "" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\xff\xff\xff\xff\xff\xff" +
		"\xff\x0a\x0b\x0c\x0d\x0e\x0f\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\x0a\x0b\x0c\x0d\x0e\x0f\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff" +
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff"
)

// EncodedLen 返回 n 个源字节的编码长度。
// 具体来说，它返回 n * 2。
func EncodedLen(n int) int { return n * 2 }

// Encode 将 src 编码到 [EncodedLen](len(src)) 个字节的 dst 中。
// 为方便起见，它返回写入 dst 的字节数，
// 但该值始终等于 [EncodedLen](len(src))。
// Encode 实现十六进制编码。
func Encode(dst, src []byte) int {
	j := 0
	for _, v := range src {
		dst[j] = hextable[v>>4]
		dst[j+1] = hextable[v&0x0f]
		j += 2
	}
	return len(src) * 2
}

// AppendEncode 将十六进制编码后的 src 追加到 dst
// 并返回扩展后的缓冲区。
func AppendEncode(dst, src []byte) []byte {
	n := EncodedLen(len(src))
	dst = slices.Grow(dst, n)
	Encode(dst[len(dst):][:n], src)
	return dst[:len(dst)+n]
}

// ErrLength 报告尝试使用 [Decode] 或 [DecodeString]
// 解码奇数长度的输入。
// 基于流的 Decoder 返回 [io.ErrUnexpectedEOF] 而不是 ErrLength。
var ErrLength = errors.New("encoding/hex: odd length hex string")

// InvalidByteError 值描述由十六进制字符串中的无效字节导致的错误。
type InvalidByteError byte

func (e InvalidByteError) Error() string {
	return fmt.Sprintf("encoding/hex: invalid byte: %#U", rune(e))
}

// DecodedLen 返回 x 个源字节的解码长度。
// 具体来说，它返回 x / 2。
func DecodedLen(x int) int { return x / 2 }

// Decode 将 src 解码到 [DecodedLen](len(src)) 个字节中，
// 返回实际写入 dst 的字节数。
//
// Decode 要求 src 只包含十六进制字符，且 src 长度为偶数。
// 如果输入格式错误，Decode 返回出错之前
// 已解码的字节数。
func Decode(dst, src []byte) (int, error) {
	i, j := 0, 1
	for ; j < len(src); j += 2 {
		p := src[j-1]
		q := src[j]

		a := reverseHexTable[p]
		b := reverseHexTable[q]
		if a > 0x0f {
			return i, InvalidByteError(p)
		}
		if b > 0x0f {
			return i, InvalidByteError(q)
		}
		dst[i] = (a << 4) | b
		i++
	}
	if len(src)%2 == 1 {
		// 在报告长度错误之前先检查无效字符，
		// 因为无效字符（如果存在）是更早出现的问题。
		if reverseHexTable[src[j-1]] > 0x0f {
			return i, InvalidByteError(src[j-1])
		}
		return i, ErrLength
	}
	return i, nil
}

// AppendDecode 将十六进制解码后的 src 追加到 dst
// 并返回扩展后的缓冲区。
// 如果输入格式错误，它返回部分解码的 src 和一个错误。
func AppendDecode(dst, src []byte) ([]byte, error) {
	n := DecodedLen(len(src))
	dst = slices.Grow(dst, n)
	n, err := Decode(dst[len(dst):][:n], src)
	return dst[:len(dst)+n], err
}

// EncodeToString 返回 src 的十六进制编码。
func EncodeToString(src []byte) string {
	dst := make([]byte, EncodedLen(len(src)))
	Encode(dst, src)
	return string(dst)
}

// DecodeString 返回十六进制字符串 s 所表示的字节。
//
// DecodeString 要求 src 只包含十六进制字符，且 src 长度为偶数。
// 如果输入格式错误，DecodeString 返回出错之前
// 已解码的字节。
func DecodeString(s string) ([]byte, error) {
	dst := make([]byte, DecodedLen(len(s)))
	n, err := Decode(dst, []byte(s))
	return dst[:n], err
}

// Dump 返回一个包含给定数据的十六进制转储的字符串。该十六进制转储的格式
// 与命令行中 `hexdump -C` 的输出一致。
func Dump(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var buf strings.Builder
	// Dumper 每完成一个 16 字节块会写入 79 个字节，剩余部分至少
	// 写入 64 个字节。将分配向上取整，因为最多只会浪费 15 个字节。
	buf.Grow((1 + ((len(data) - 1) / 16)) * 79)

	dumper := Dumper(&buf)
	dumper.Write(data)
	dumper.Close()
	return buf.String()
}

// bufferSize 是编码器和解码器中缓冲的十六进制字符数。
const bufferSize = 1024

type encoder struct {
	w   io.Writer
	err error
	out [bufferSize]byte // 输出缓冲区
}

// NewEncoder 返回一个 [io.Writer]，它将小写十六进制字符写入 w。
func NewEncoder(w io.Writer) io.Writer {
	return &encoder{w: w}
}

func (e *encoder) Write(p []byte) (n int, err error) {
	for len(p) > 0 && e.err == nil {
		chunkSize := bufferSize / 2
		if len(p) < chunkSize {
			chunkSize = len(p)
		}

		var written int
		encoded := Encode(e.out[:], p[:chunkSize])
		written, e.err = e.w.Write(e.out[:encoded])
		n += written / 2
		p = p[chunkSize:]
	}
	return n, e.err
}

type decoder struct {
	r   io.Reader
	err error
	in  []byte           // 输入缓冲区（编码形式）
	arr [bufferSize]byte // in 的底层数组
}

// NewDecoder 返回一个 [io.Reader]，它从 r 解码十六进制字符。
// NewDecoder 要求 r 只包含偶数个十六进制字符。
func NewDecoder(r io.Reader) io.Reader {
	return &decoder{r: r}
}

func (d *decoder) Read(p []byte) (n int, err error) {
	// 用足够的字节填充内部缓冲区以进行解码
	if len(d.in) < 2 && d.err == nil {
		var numCopy, numRead int
		numCopy = copy(d.arr[:], d.in) // 复制 0 或 1 个字节
		numRead, d.err = d.r.Read(d.arr[numCopy:])
		d.in = d.arr[:numCopy+numRead]
		if d.err == io.EOF && len(d.in)%2 != 0 {

			if a := reverseHexTable[d.in[len(d.in)-1]]; a > 0x0f {
				d.err = InvalidByteError(d.in[len(d.in)-1])
			} else {
				d.err = io.ErrUnexpectedEOF
			}
		}
	}

	// 将内部缓冲区解码到输出缓冲区
	if numAvail := len(d.in) / 2; len(p) > numAvail {
		p = p[:numAvail]
	}
	numDec, err := Decode(p, d.in[:len(p)*2])
	d.in = d.in[2*numDec:]
	if err != nil {
		d.in, d.err = nil, err // 解码错误；丢弃输入剩余部分
	}

	if len(d.in) < 2 {
		return numDec, d.err // 仅在缓冲区完全消耗后才暴露错误
	}
	return numDec, nil
}

// Dumper 返回一个 [io.WriteCloser]，它将所有写入数据的十六进制转储写入
// w。转储的格式与命令行中 `hexdump -C` 的输出
// 一致。
func Dumper(w io.Writer) io.WriteCloser {
	return &dumper{w: w}
}

type dumper struct {
	w          io.Writer
	rightChars [18]byte
	buf        [14]byte
	used       int  // 当前行中的字节数
	n          uint // 字节总数
	closed     bool
}

func toChar(b byte) byte {
	if b < 32 || b > 126 {
		return '.'
	}
	return b
}

func (h *dumper) Write(data []byte) (n int, err error) {
	if h.closed {
		return 0, errors.New("encoding/hex: dumper closed")
	}

	// 输出行类似这样：
	// 00000010  2e 2f 30 31 32 33 34 35  36 37 38 39 3a 3b 3c 3d  |./0123456789:;<=|
	// ^ offset                          ^ extra space              ^ ASCII of line.
	for i := range data {
		if h.used == 0 {
			// 在行首，我们用十六进制打印当前
			// 偏移量。
			h.buf[0] = byte(h.n >> 24)
			h.buf[1] = byte(h.n >> 16)
			h.buf[2] = byte(h.n >> 8)
			h.buf[3] = byte(h.n)
			Encode(h.buf[4:], h.buf[:4])
			h.buf[12] = ' '
			h.buf[13] = ' '
			_, err = h.w.Write(h.buf[4:])
			if err != nil {
				return
			}
		}
		Encode(h.buf[:], data[i:i+1])
		h.buf[2] = ' '
		l := 3
		if h.used == 7 {
			// 第 8 个字节之后有一个额外的空格。
			h.buf[3] = ' '
			l = 4
		} else if h.used == 15 {
			// 在行尾有一个额外的空格
			// 以及右列的竖线。
			h.buf[3] = ' '
			h.buf[4] = '|'
			l = 5
		}
		_, err = h.w.Write(h.buf[:l])
		if err != nil {
			return
		}
		n++
		h.rightChars[h.used] = toChar(data[i])
		h.used++
		h.n++
		if h.used == 16 {
			h.rightChars[16] = '|'
			h.rightChars[17] = '\n'
			_, err = h.w.Write(h.rightChars[:])
			if err != nil {
				return
			}
			h.used = 0
		}
	}
	return
}

func (h *dumper) Close() (err error) {
	// 有关此格式的细节，请参见 Write() 中的注释。
	if h.closed {
		return
	}
	h.closed = true
	if h.used == 0 {
		return
	}
	h.buf[0] = ' '
	h.buf[1] = ' '
	h.buf[2] = ' '
	h.buf[3] = ' '
	h.buf[4] = '|'
	nBytes := h.used
	for h.used < 16 {
		l := 3
		if h.used == 7 {
			l = 4
		} else if h.used == 15 {
			l = 5
		}
		_, err = h.w.Write(h.buf[:l])
		if err != nil {
			return
		}
		h.used++
	}
	h.rightChars[nBytes] = '|'
	h.rightChars[nBytes+1] = '\n'
	_, err = h.w.Write(h.rightChars[:nBytes+2])
	return
}
