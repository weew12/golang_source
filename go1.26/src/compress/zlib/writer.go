// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zlib

import (
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/adler32"
	"io"
)

// 这些常量复制自 [flate] 包，因此导入 [compress/zlib] 的代码
// 无需再导入 [compress/flate]。
const (
	NoCompression      = flate.NoCompression
	BestSpeed          = flate.BestSpeed
	BestCompression    = flate.BestCompression
	DefaultCompression = flate.DefaultCompression
	HuffmanOnly        = flate.HuffmanOnly
)

// Writer 接收写入其中的数据，并将该数据的压缩形式
// 写入底层 writer（参见 [NewWriter]）。
type Writer struct {
	w           io.Writer
	level       int
	dict        []byte
	compressor  *flate.Writer
	digest      hash.Hash32
	err         error
	scratch     [4]byte
	wroteHeader bool
}

// NewWriter 创建一个新的 [Writer]。
// 写入返回的 Writer 的数据会被压缩并写入 w。
//
// 调用者有责任在使用完毕后调用 Writer 的 Close。
// 写入的数据可能会被缓冲，直到 Close 时才刷新。
func NewWriter(w io.Writer) *Writer {
	z, _ := NewWriterLevelDict(w, DefaultCompression, nil)
	return z
}

// NewWriterLevel 与 [NewWriter] 类似，但会指定压缩级别，
// 而不是假定为 [DefaultCompression]。
//
// 压缩级别可以是 [DefaultCompression]、[NoCompression]、[HuffmanOnly]，
// 或 [BestSpeed] 到 [BestCompression] 之间（含两端）的任意整数值。
// 如果该级别有效，返回的错误为 nil。
func NewWriterLevel(w io.Writer, level int) (*Writer, error) {
	return NewWriterLevelDict(w, level, nil)
}

// NewWriterLevelDict 与 [NewWriterLevel] 类似，但会指定用于压缩的字典。
//
// 字典可以为 nil。如果不为 nil，在 Writer 关闭之前不应修改其内容。
func NewWriterLevelDict(w io.Writer, level int, dict []byte) (*Writer, error) {
	if level < HuffmanOnly || level > BestCompression {
		return nil, fmt.Errorf("zlib: invalid compression level: %d", level)
	}
	return &Writer{
		w:     w,
		level: level,
		dict:  dict,
	}, nil
}

// Reset 清除 [Writer] z 的状态，使其等同于来自 [NewWriterLevel] 或
// [NewWriterLevelDict] 的初始状态，但改为写入 w。
func (z *Writer) Reset(w io.Writer) {
	z.w = w
	// z.level 和 z.dict 保持不变。
	if z.compressor != nil {
		z.compressor.Reset(w)
	}
	if z.digest != nil {
		z.digest.Reset()
	}
	z.err = nil
	z.scratch = [4]byte{}
	z.wroteHeader = false
}

// writeHeader 写入 ZLIB 头部。
func (z *Writer) writeHeader() (err error) {
	z.wroteHeader = true
	// ZLIB 有一个两字节的头部（如 RFC 1950 中所述）。
	// 前四位是 CINFO（压缩信息），对于默认的 deflate 窗口大小，其值为 7。
	// 接下来四位是 CM（压缩方法），对于 deflate，其值为 8。
	z.scratch[0] = 0x78
	// 接下来两位是 FLEVEL（压缩级别）。这四个取值为：
	// 0=最快，1=快，2=默认，3=最佳。
	// 接下来一位是 FDICT，如果给定了字典则置位。
	// 最后的五位 FCHECK 构成一个 mod-31 校验和。
	switch z.level {
	case -2, 0, 1:
		z.scratch[1] = 0 << 6
	case 2, 3, 4, 5:
		z.scratch[1] = 1 << 6
	case 6, -1:
		z.scratch[1] = 2 << 6
	case 7, 8, 9:
		z.scratch[1] = 3 << 6
	default:
		panic("unreachable")
	}
	if z.dict != nil {
		z.scratch[1] |= 1 << 5
	}
	z.scratch[1] += uint8(31 - binary.BigEndian.Uint16(z.scratch[:2])%31)
	if _, err = z.w.Write(z.scratch[0:2]); err != nil {
		return err
	}
	if z.dict != nil {
		// 接下来四个字节是字典的 Adler-32 校验和。
		binary.BigEndian.PutUint32(z.scratch[:], adler32.Checksum(z.dict))
		if _, err = z.w.Write(z.scratch[0:4]); err != nil {
			return err
		}
	}
	if z.compressor == nil {
		// 初始化 deflater，除非 Writer 是在一次 Reset
		// 调用之后被复用的。
		z.compressor, err = flate.NewWriterDict(z.w, z.level, z.dict)
		if err != nil {
			return err
		}
		z.digest = adler32.New()
	}
	return nil
}

// Write 将 p 的压缩形式写入底层 [io.Writer]。在 [Writer] 被关闭或
// 显式刷新之前，压缩后的字节不一定会被刷新。
func (z *Writer) Write(p []byte) (n int, err error) {
	if !z.wroteHeader {
		z.err = z.writeHeader()
	}
	if z.err != nil {
		return 0, z.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	n, err = z.compressor.Write(p)
	if err != nil {
		z.err = err
		return
	}
	z.digest.Write(p)
	return
}

// Flush 将 Writer 刷新到底层 [io.Writer]。
func (z *Writer) Flush() error {
	if !z.wroteHeader {
		z.err = z.writeHeader()
	}
	if z.err != nil {
		return z.err
	}
	z.err = z.compressor.Flush()
	return z.err
}

// Close 关闭 Writer，将任何未写入的数据刷新到底层 [io.Writer]，
// 但不会关闭底层的 io.Writer。
func (z *Writer) Close() error {
	if !z.wroteHeader {
		z.err = z.writeHeader()
	}
	if z.err != nil {
		return z.err
	}
	z.err = z.compressor.Close()
	if z.err != nil {
		return z.err
	}
	checksum := z.digest.Sum32()
	// ZLIB（RFC 1950）是大端序，与 GZIP（RFC 1952）不同。
	binary.BigEndian.PutUint32(z.scratch[:], checksum)
	_, z.err = z.w.Write(z.scratch[0:4])
	return z.err
}
