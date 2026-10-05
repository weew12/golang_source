// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package binary

// 本文件实现了 64 位整数的 "varint" 编码。
// 编码方式如下：
// - 无符号整数每次序列化 7 位，从最低有效位开始
// - 每个输出字节的最高位（msb）指示后面是否还有后续字节（msb = 1）
// - 有符号整数使用 "zig-zag" 编码映射为无符号整数：
//   正值 x 写为 2*x + 0，负值写为 2*(^x) + 1；
//   也就是说，负数取反，是否取反由第 0 位编码。
//
// 设计说明：
// 64 位值最多需要 10 个字节。该编码本可以更紧凑：
// 一个完整的 64 位值需要额外一个字节来存放第 63 位。
// 相反，由于我们知道不会超过 64 位，可以用前一个字节的 msb 来存放第 63 位。
// 这是一个微不足道的改进，会将最大编码长度减少到 9 个字节。然而，
// 它打破了 msb 始终是 "后续位" 这一不变式，从而使该格式
// 与针对更大数值（比如 128 位）的 varint 编码不兼容。

import (
	"errors"
	"io"
)

// MaxVarintLenN 是 varint 编码的 N 位整数的最大长度。
const (
	MaxVarintLen16 = 3
	MaxVarintLen32 = 5
	MaxVarintLen64 = 10
)

// AppendUvarint 将 x 的 varint 编码形式（由 [PutUvarint] 生成）
// 追加到 buf 并返回扩展后的缓冲区。
func AppendUvarint(buf []byte, x uint64) []byte {
	for x >= 0x80 {
		buf = append(buf, byte(x)|0x80)
		x >>= 7
	}
	return append(buf, byte(x))
}

// PutUvarint 将 uint64 编码到 buf 并返回写入的字节数。
// 如果缓冲区太小，PutUvarint 会 panic。
func PutUvarint(buf []byte, x uint64) int {
	i := 0
	for x >= 0x80 {
		buf[i] = byte(x) | 0x80
		x >>= 7
		i++
	}
	buf[i] = byte(x)
	return i + 1
}

// Uvarint 从 buf 解码一个 uint64 并返回该值以及读取的字节数（> 0）。
// 如果发生错误，值为 0，且字节数 n <= 0，含义为：
//   - n == 0：buf 太小；
//   - n < 0：值大于 64 位（溢出），且 -n 为读取的字节数。
func Uvarint(buf []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, b := range buf {
		if i == MaxVarintLen64 {
			// 防止读取超过 MaxVarintLen64 的字节。
			// 参见 issue https://golang.org/issues/41185
			return 0, -(i + 1) // 溢出
		}
		if b < 0x80 {
			if i == MaxVarintLen64-1 && b > 1 {
				return 0, -(i + 1) // 溢出
			}
			return x | uint64(b)<<s, i + 1
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, 0
}

// AppendVarint 将 x 的 varint 编码形式（由 [PutVarint] 生成）
// 追加到 buf 并返回扩展后的缓冲区。
func AppendVarint(buf []byte, x int64) []byte {
	ux := uint64(x) << 1
	if x < 0 {
		ux = ^ux
	}
	return AppendUvarint(buf, ux)
}

// PutVarint 将 int64 编码到 buf 并返回写入的字节数。
// 如果缓冲区太小，PutVarint 会 panic。
func PutVarint(buf []byte, x int64) int {
	ux := uint64(x) << 1
	if x < 0 {
		ux = ^ux
	}
	return PutUvarint(buf, ux)
}

// Varint 从 buf 解码一个 int64 并返回该值以及读取的字节数（> 0）。
// 如果发生错误，值为 0，且字节数 n <= 0，含义如下：
//   - n == 0：buf 太小；
//   - n < 0：值大于 64 位（溢出），
//     且 -n 为读取的字节数。
func Varint(buf []byte) (int64, int) {
	ux, n := Uvarint(buf) // 存在错误时仍可继续
	x := int64(ux >> 1)
	if ux&1 != 0 {
		x = ^x
	}
	return x, n
}

var errOverflow = errors.New("binary: varint overflows a 64-bit integer")

// ReadUvarint 从 r 读取一个编码的无符号整数并以 uint64 返回。
// 只有在未读取任何字节时，错误才为 [io.EOF]。
// 如果在读取了一些但并非全部字节之后发生 [io.EOF]，
// ReadUvarint 会返回 [io.ErrUnexpectedEOF]。
func ReadUvarint(r io.ByteReader) (uint64, error) {
	var x uint64
	var s uint
	for i := 0; i < MaxVarintLen64; i++ {
		b, err := r.ReadByte()
		if err != nil {
			if i > 0 && err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return x, err
		}
		if b < 0x80 {
			if i == MaxVarintLen64-1 && b > 1 {
				return x, errOverflow
			}
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return x, errOverflow
}

// ReadVarint 从 r 读取一个编码的有符号整数并以 int64 返回。
// 只有在未读取任何字节时，错误才为 [io.EOF]。
// 如果在读取了一些但并非全部字节之后发生 [io.EOF]，
// ReadVarint 会返回 [io.ErrUnexpectedEOF]。
func ReadVarint(r io.ByteReader) (int64, error) {
	ux, err := ReadUvarint(r) // 存在错误时仍可继续
	x := int64(ux >> 1)
	if ux&1 != 0 {
		x = ^x
	}
	return x, err
}
