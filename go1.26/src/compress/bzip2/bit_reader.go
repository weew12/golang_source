// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bzip2

import (
	"bufio"
	"io"
)

// bitReader 包装了一个 io.Reader，并提供了从其中逐位读取值的
// 能力。它的 Read* 方法不返回通常的错误，
// 因为错误处理太冗长了。相反，任何错误都会被保留下来，
// 之后可以检查。
type bitReader struct {
	r    io.ByteReader
	n    uint64
	bits uint
	err  error
}

// newBitReader 返回一个从 r 读取的新 bitReader。如果 r
// 还不是 io.ByteReader，它将通过 bufio.Reader 进行转换。
func newBitReader(r io.Reader) bitReader {
	byter, ok := r.(io.ByteReader)
	if !ok {
		byter = bufio.NewReader(r)
	}
	return bitReader{r: byter}
}

// ReadBits64 读取给定位数的比特，并将其返回在
// uint64 的最低有效部分中。如果发生错误，它返回 0，
// 且可以通过调用 bitReader.Err() 获取该错误。
func (br *bitReader) ReadBits64(bits uint) (n uint64) {
	for bits > br.bits {
		b, err := br.r.ReadByte()
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			br.err = err
			return 0
		}
		br.n <<= 8
		br.n |= uint64(b)
		br.bits += 8
	}

	// br.n 看起来像这样（假设 br.bits = 14 且 bits = 6）：
	// Bit: 111111
	//      5432109876543210
	//
	//         (6 bits, the desired output)
	//        |-----|
	//        V     V
	//      0101101101001110
	//        ^            ^
	//        |------------|
	//           br.bits (num valid bits)
	//
	// 下一行把所需的比特右移到
	// 最低有效位，并屏蔽掉上面的一切。
	n = (br.n >> (br.bits - bits)) & ((1 << bits) - 1)
	br.bits -= bits
	return
}

func (br *bitReader) ReadBits(bits uint) (n int) {
	n64 := br.ReadBits64(bits)
	return int(n64)
}

func (br *bitReader) ReadBit() bool {
	n := br.ReadBits(1)
	return n != 0
}

func (br *bitReader) Err() error {
	return br.err
}
