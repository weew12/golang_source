// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lzw

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// writer 是一个带缓冲、可刷新的 writer。
type writer interface {
	io.ByteWriter
	Flush() error
}

const (
	// code 是一个 12 位的值，编码时以 uint32 存储，以避免移位时发生
	// 类型转换。
	maxCode     = 1<<12 - 1
	invalidCode = 1<<32 - 1
	// 一共有 1<<12 个可能的 code，这是任意时刻有效哈希表表项数量的上界。
	// tableSize 是它的 4 倍。
	tableSize = 4 * 1 << 12
	tableMask = tableSize - 1
	// 哈希表表项是一个 uint32。零是无效表项，因为有效表项的低 12 位
	// 必须是一个非字面量（non-literal）code。
	invalidEntry = 0
)

// Writer 是一个 LZW 压缩器。它把数据的压缩形式写入底层 writer
// （参见 [NewWriter]）。
type Writer struct {
	// w 是压缩后的字节被写入的 writer。
	w writer
	// litWidth 是字面量 code 的位宽（以比特为单位）。
	litWidth uint
	// order、write、bits、nBits 和 width 是把 code 流转换为字节流所需的状态。
	order Order
	write func(*Writer, uint32) error
	nBits uint
	width uint
	bits  uint32
	// hi 是下一次发出 code 时所隐含的 code。
	// overflow 是 hi 溢出 code 宽度时的 code。
	hi, overflow uint32
	// savedCode 是最近一次 Write 调用结束时累积的 code。如果没有这样的调用，
	// 它等于 invalidCode。
	savedCode uint32
	// err 是写入过程中遇到的第一个错误。关闭 writer 会使之后所有的 Write
	// 调用返回 errClosed。
	err error
	// table 是从 20 位键到 12 位值的哈希表。每个表项包含 key<<12|val，
	// 冲突通过线性探测解决。键由一个 12 位的 code 前缀和一个 8 位的字节后缀组成。
	// 值是 12 位的 code。
	table [tableSize]uint32
}

// writeLSB 为“最低有效位优先”的数据写入 code c。
func (w *Writer) writeLSB(c uint32) error {
	w.bits |= c << w.nBits
	w.nBits += w.width
	for w.nBits >= 8 {
		if err := w.w.WriteByte(uint8(w.bits)); err != nil {
			return err
		}
		w.bits >>= 8
		w.nBits -= 8
	}
	return nil
}

// writeMSB 为“最高有效位优先”的数据写入 code c。
func (w *Writer) writeMSB(c uint32) error {
	w.bits |= c << (32 - w.width - w.nBits)
	w.nBits += w.width
	for w.nBits >= 8 {
		if err := w.w.WriteByte(uint8(w.bits >> 24)); err != nil {
			return err
		}
		w.bits <<= 8
		w.nBits -= 8
	}
	return nil
}

// errOutOfCodes 是一个内部错误，表示 writer 已用尽未使用的 code，
// 接下来需要发送一个 clear code。
var errOutOfCodes = errors.New("lzw: out of codes")

// incHi 递增 e.hi，并检查是否溢出以及是否用尽未使用的 code。在后一种情况下，
// incHi 会发送一个 clear code，重置 writer 状态并返回 errOutOfCodes。
func (w *Writer) incHi() error {
	w.hi++
	if w.hi == w.overflow {
		w.width++
		w.overflow <<= 1
	}
	if w.hi == maxCode {
		clear := uint32(1) << w.litWidth
		if err := w.write(w, clear); err != nil {
			return err
		}
		w.width = w.litWidth + 1
		w.hi = clear + 1
		w.overflow = clear << 1
		for i := range w.table {
			w.table[i] = invalidEntry
		}
		return errOutOfCodes
	}
	return nil
}

// Write 把 p 的压缩表示写入 w 的底层 writer。
func (w *Writer) Write(p []byte) (n int, err error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if maxLit := uint8(1<<w.litWidth - 1); maxLit != 0xff {
		for _, x := range p {
			if x > maxLit {
				w.err = errors.New("lzw: input byte too large for the litWidth")
				return 0, w.err
			}
		}
	}
	n = len(p)
	code := w.savedCode
	if code == invalidCode {
		// 这是第一次写入；发送一个 clear code。
		// https://www.w3.org/Graphics/GIF/spec-gif89a.txt 附录 F
		// “Variable-Length-Code LZW Compression” 指出 “Encoders should
		// output a Clear code as the first code of each image data stream”。
		//
		// LZW 压缩并非只用于 GIF，但无条件遵循该指令的成本很低。
		clear := uint32(1) << w.litWidth
		if err := w.write(w, clear); err != nil {
			return 0, err
		}
		// 在起始 clear code 之后，对于非空输入，下一个发送的 code 总是
		// 一个字面量 code。
		code, p = uint32(p[0]), p[1:]
	}
loop:
	for _, x := range p {
		literal := uint32(x)
		key := code<<8 | literal
		// 如果该 key 在哈希表中命中，则继续循环，暂不发出 code。
		hash := (key>>12 ^ key) & tableMask
		for h, t := hash, w.table[hash]; t != invalidEntry; {
			if key == t>>12 {
				code = t & maxCode
				continue loop
			}
			h = (h + 1) & tableMask
			t = w.table[h]
		}
		// 否则，写入当前 code，literal 成为下一个发出 code 的起始。
		if w.err = w.write(w, code); w.err != nil {
			return 0, w.err
		}
		code = literal
		// 递增 e.hi，即下一个隐含的 code。如果 code 用尽，重置 writer 状态
		// （包括清空哈希表）并继续。
		if err1 := w.incHi(); err1 != nil {
			if err1 == errOutOfCodes {
				continue
			}
			w.err = err1
			return 0, w.err
		}
		// 否则，把 key -> e.hi 插入 e.table 所表示的映射中。
		for {
			if w.table[hash] == invalidEntry {
				w.table[hash] = (key << 12) | w.hi
				break
			}
			hash = (hash + 1) & tableMask
		}
	}
	w.savedCode = code
	return n, nil
}

// Close 关闭 [Writer]，刷新所有待处理的输出。它不会关闭 w 的底层 writer。
func (w *Writer) Close() error {
	if w.err != nil {
		if w.err == errClosed {
			return nil
		}
		return w.err
	}
	// 使之后所有对 Write 的调用返回 errClosed。
	w.err = errClosed
	// 如果 savedCode 有效则写入它。
	if w.savedCode != invalidCode {
		if err := w.write(w, w.savedCode); err != nil {
			return err
		}
		if err := w.incHi(); err != nil && err != errOutOfCodes {
			return err
		}
	} else {
		// 写入起始 clear code，因为 w.Write 没有写。
		clear := uint32(1) << w.litWidth
		if err := w.write(w, clear); err != nil {
			return err
		}
	}
	// 写入 eof code。
	eof := uint32(1)<<w.litWidth + 1
	if err := w.write(w, eof); err != nil {
		return err
	}
	// 写入最后的比特位。
	if w.nBits > 0 {
		if w.order == MSB {
			w.bits >>= 24
		}
		if err := w.w.WriteByte(uint8(w.bits)); err != nil {
			return err
		}
	}
	return w.w.Flush()
}

// Reset 清除 [Writer] 的状态，使其可以作为一个新的 [Writer] 再次复用。
func (w *Writer) Reset(dst io.Writer, order Order, litWidth int) {
	*w = Writer{}
	w.init(dst, order, litWidth)
}

// NewWriter 创建一个新的 [io.WriteCloser]。
// 写入返回的 [io.WriteCloser] 的数据会被压缩并写入 w。
// 调用者负责在写入完成后对 WriteCloser 调用 Close。
// 用于字面量 code 的比特数 litWidth 必须在 [2,8] 范围内，通常为 8。
// 输入字节必须小于 1<<litWidth。
//
// 保证返回的 [io.WriteCloser] 的底层类型是 *[Writer]。
func NewWriter(w io.Writer, order Order, litWidth int) io.WriteCloser {
	return newWriter(w, order, litWidth)
}

func newWriter(dst io.Writer, order Order, litWidth int) *Writer {
	w := new(Writer)
	w.init(dst, order, litWidth)
	return w
}

func (w *Writer) init(dst io.Writer, order Order, litWidth int) {
	switch order {
	case LSB:
		w.write = (*Writer).writeLSB
	case MSB:
		w.write = (*Writer).writeMSB
	default:
		w.err = errors.New("lzw: unknown order")
		return
	}
	if litWidth < 2 || 8 < litWidth {
		w.err = fmt.Errorf("lzw: litWidth %d out of range", litWidth)
		return
	}
	bw, ok := dst.(writer)
	if !ok && dst != nil {
		bw = bufio.NewWriter(dst)
	}
	w.w = bw
	lw := uint(litWidth)
	w.order = order
	w.width = 1 + lw
	w.litWidth = lw
	w.hi = 1<<lw + 1
	w.overflow = 1 << (lw + 1)
	w.savedCode = invalidCode
}
