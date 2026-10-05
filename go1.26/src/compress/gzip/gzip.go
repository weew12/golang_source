// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gzip

import (
	"compress/flate"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"time"
)

// 这些常量复制自 [flate] 包，这样导入 [compress/gzip] 的代码
// 就无需再导入 [compress/flate]。
const (
	NoCompression      = flate.NoCompression
	BestSpeed          = flate.BestSpeed
	BestCompression    = flate.BestCompression
	DefaultCompression = flate.DefaultCompression
	HuffmanOnly        = flate.HuffmanOnly
)

// Writer 是一个 [io.WriteCloser]。
// 写入 Writer 的数据会被压缩并写入 w。
type Writer struct {
	Header      // 在首次调用 Write、Flush 或 Close 时写入
	w           io.Writer
	level       int
	wroteHeader bool
	closed      bool
	buf         [10]byte
	compressor  *flate.Writer
	digest      uint32 // CRC-32，IEEE 多项式（第 8 节）
	size        uint32 // 未压缩大小（第 2.3.1 节）
	err         error
}

// NewWriter 返回一个新的 [Writer]。
// 写入所返回 writer 的数据会被压缩并写入 w。
//
// 调用者有责任在使用完毕后调用 [Writer] 的 Close。
// 写入的数据可能会被缓冲，直到 Close 时才刷新。
//
// 希望设置 Writer.[Header] 中字段的调用者，必须在首次调用 Write、Flush 或 Close
// 之前完成设置。
func NewWriter(w io.Writer) *Writer {
	z, _ := NewWriterLevel(w, DefaultCompression)
	return z
}

// NewWriterLevel 类似 [NewWriter]，但指定压缩级别，而不是假定使用 [DefaultCompression]。
//
// 压缩级别可以是 [DefaultCompression]、[NoCompression]、[HuffmanOnly]，
// 或者是 [BestSpeed] 与 [BestCompression] 之间（含两端）的任意整数值。
// 如果级别有效，则返回的错误为 nil。
func NewWriterLevel(w io.Writer, level int) (*Writer, error) {
	if level < HuffmanOnly || level > BestCompression {
		return nil, fmt.Errorf("gzip: invalid compression level: %d", level)
	}
	z := new(Writer)
	z.init(w, level)
	return z, nil
}

func (z *Writer) init(w io.Writer, level int) {
	compressor := z.compressor
	if compressor != nil {
		compressor.Reset(w)
	}
	*z = Writer{
		Header: Header{
			OS: 255, // 未知
		},
		w:          w,
		level:      level,
		compressor: compressor,
	}
}

// Reset 丢弃 [Writer] z 的状态，使其等同于其原始状态（来自 [NewWriter] 或
// [NewWriterLevel]）的结果，但改为写入 w。这样可以复用 [Writer]，
// 而不必分配一个新的。
func (z *Writer) Reset(w io.Writer) {
	z.init(w, z.level)
}

// writeBytes 将一个带长度前缀的字节切片写入 z.w。
func (z *Writer) writeBytes(b []byte) error {
	if len(b) > 0xffff {
		return errors.New("gzip.Write: Extra data is too large")
	}
	le.PutUint16(z.buf[:2], uint16(len(b)))
	_, err := z.w.Write(z.buf[:2])
	if err != nil {
		return err
	}
	_, err = z.w.Write(b)
	return err
}

// writeString 将 UTF-8 字符串 s 以 GZIP 的格式写入 z.w。
// GZIP（RFC 1952）规定字符串为以 NUL 结尾的 ISO 8859-1（Latin-1）。
func (z *Writer) writeString(s string) (err error) {
	// GZIP 存储 Latin-1 字符串；非 Latin-1 时报错；非 ASCII 时进行转换。
	needconv := false
	for _, v := range s {
		if v == 0 || v > 0xff {
			return errors.New("gzip.Write: non-Latin-1 header string")
		}
		if v > 0x7f {
			needconv = true
		}
	}
	if needconv {
		b := make([]byte, 0, len(s))
		for _, v := range s {
			b = append(b, byte(v))
		}
		_, err = z.w.Write(b)
	} else {
		_, err = io.WriteString(z.w, s)
	}
	if err != nil {
		return err
	}
	// GZIP 字符串以 NUL 结尾。
	z.buf[0] = 0
	_, err = z.w.Write(z.buf[:1])
	return err
}

// Write 将 p 的压缩形式写入底层 [io.Writer]。压缩后的字节在 [Writer]
// 关闭之前不一定被刷新。
func (z *Writer) Write(p []byte) (int, error) {
	if z.err != nil {
		return 0, z.err
	}
	var n int
	// 延迟写入 GZIP 头。
	if !z.wroteHeader {
		z.wroteHeader = true
		z.buf = [10]byte{0: gzipID1, 1: gzipID2, 2: gzipDeflate}
		if z.Extra != nil {
			z.buf[3] |= 0x04
		}
		if z.Name != "" {
			z.buf[3] |= 0x08
		}
		if z.Comment != "" {
			z.buf[3] |= 0x10
		}
		if z.ModTime.After(time.Unix(0, 0)) {
			// 第 2.3.1 节，MTIME 的零值表示未设置修改时间。
			le.PutUint32(z.buf[4:8], uint32(z.ModTime.Unix()))
		}
		if z.level == BestCompression {
			z.buf[8] = 2
		} else if z.level == BestSpeed {
			z.buf[8] = 4
		}
		z.buf[9] = z.OS
		_, z.err = z.w.Write(z.buf[:10])
		if z.err != nil {
			return 0, z.err
		}
		if z.Extra != nil {
			z.err = z.writeBytes(z.Extra)
			if z.err != nil {
				return 0, z.err
			}
		}
		if z.Name != "" {
			z.err = z.writeString(z.Name)
			if z.err != nil {
				return 0, z.err
			}
		}
		if z.Comment != "" {
			z.err = z.writeString(z.Comment)
			if z.err != nil {
				return 0, z.err
			}
		}
		if z.compressor == nil {
			z.compressor, _ = flate.NewWriter(z.w, z.level)
		}
	}
	z.size += uint32(len(p))
	z.digest = crc32.Update(z.digest, crc32.IEEETable, p)
	n, z.err = z.compressor.Write(p)
	return n, z.err
}

// Flush 将所有待处理的压缩数据刷新到底层 writer。
//
// 它主要用于压缩网络协议，以确保远程读取方有足够的数据来重建数据包。
// Flush 在数据写入完成之前不会返回。如果底层 writer 返回错误，Flush 会返回该错误。
//
// 在 zlib 库的术语中，Flush 等价于 Z_SYNC_FLUSH。
func (z *Writer) Flush() error {
	if z.err != nil {
		return z.err
	}
	if z.closed {
		return nil
	}
	if !z.wroteHeader {
		z.Write(nil)
		if z.err != nil {
			return z.err
		}
	}
	z.err = z.compressor.Flush()
	return z.err
}

// Close 通过将所有未写入的数据刷新到底层 [io.Writer] 并写入 GZIP 尾部，
// 从而关闭 [Writer]。
// 它不会关闭底层的 [io.Writer]。
func (z *Writer) Close() error {
	if z.err != nil {
		return z.err
	}
	if z.closed {
		return nil
	}
	z.closed = true
	if !z.wroteHeader {
		z.Write(nil)
		if z.err != nil {
			return z.err
		}
	}
	z.err = z.compressor.Close()
	if z.err != nil {
		return z.err
	}
	le.PutUint32(z.buf[:4], z.digest)
	le.PutUint32(z.buf[4:8], z.size)
	_, z.err = z.w.Write(z.buf[:8])
	return z.err
}
