// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gzip 包实现了 gzip 格式压缩文件的读写，
// 格式规范见 RFC 1952。
package gzip

import (
	"bufio"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"time"
)

const (
	gzipID1     = 0x1f
	gzipID2     = 0x8b
	gzipDeflate = 8
	flagText    = 1 << 0
	flagHdrCrc  = 1 << 1
	flagExtra   = 1 << 2
	flagName    = 1 << 3
	flagComment = 1 << 4
)

var (
	// 当读取的 GZIP 数据校验和无效时，返回 ErrChecksum。
	ErrChecksum = errors.New("gzip: invalid checksum")
	// 当读取的 GZIP 数据头部无效时，返回 ErrHeader。
	ErrHeader = errors.New("gzip: invalid header")
)

var le = binary.LittleEndian

// noEOF 将 io.EOF 转换为 io.ErrUnexpectedEOF。
func noEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// gzip 文件存储一个头部，提供关于压缩文件的元数据。
// 该头部以 [Writer] 和 [Reader] 结构体的字段形式暴露。
//
// 由于 GZIP 文件格式的限制，字符串必须采用 UTF-8 编码，
// 且只能包含 U+0001 到 U+00FF 的 Unicode 码点。
type Header struct {
	Comment string    // 注释
	Extra   []byte    // “额外数据”
	ModTime time.Time // 修改时间
	Name    string    // 文件名
	OS      byte      // 操作系统类型
}

// Reader 是一个 [io.Reader]，可以从中读取以获取
// gzip 格式压缩文件中的未压缩数据。
//
// 一般而言，一个 gzip 文件可以是多个 gzip 文件的拼接，
// 每个文件都有自己的头部。从 Reader 读取会
// 返回各文件未压缩数据的拼接。
// 只有第一个头部会记录在 Reader 字段中。
//
// Gzip 文件存储未压缩数据的长度和校验和。
// 如果 [Reader.Read] 到达未压缩数据的末尾时
// 数据不具有预期的长度或校验和，
// Reader 将返回 [ErrChecksum]。客户端应将
// [Reader.Read] 返回的数据视为暂定的，直到收到标志着
// 数据末尾的 [io.EOF]。
type Reader struct {
	Header       // 在 NewReader 或 Reader.Reset 之后有效
	r            flate.Reader
	decompressor io.ReadCloser
	digest       uint32 // CRC-32，IEEE 多项式（第 8 节）
	size         uint32 // 未压缩大小（第 2.3.1 节）
	buf          [512]byte
	err          error
	multistream  bool
}

// NewReader 创建一个新的 [Reader]，读取给定的 reader。
// 如果 r 未同时实现 [io.ByteReader]，
// 解压器可能会从 r 读取超出必要的数据。
//
// 调用方有责任在使用完毕后调用 [Reader.Close]。
//
// 返回的 [Reader] 中 Reader.[Header] 字段将是有效的。
func NewReader(r io.Reader) (*Reader, error) {
	z := new(Reader)
	if err := z.Reset(r); err != nil {
		return nil, err
	}
	return z, nil
}

// Reset 丢弃 [Reader] z 的状态，使其等同于
// 从 [NewReader] 得到的初始状态，但改为从 r 读取。
// 这样可以复用 [Reader]，而不必分配一个新的。
func (z *Reader) Reset(r io.Reader) error {
	*z = Reader{
		decompressor: z.decompressor,
		multistream:  true,
	}
	if rr, ok := r.(flate.Reader); ok {
		z.r = rr
	} else {
		z.r = bufio.NewReader(r)
	}
	z.Header, z.err = z.readHeader()
	return z.err
}

// Multistream 控制 reader 是否支持多流文件。
//
// 如果启用（默认），[Reader] 期望输入是一系列
// 各自独立的 gzip 数据流，每个都有自己的头部和
// 尾部，以 EOF 结束。其效果是，一系列
// gzip 文件的拼接被视为等同于
// 该序列拼接后的 gzip。这是 gzip reader 的标准行为。
//
// 调用 Multistream(false) 会禁用此行为；当读取区分
// 单个 gzip 数据流、或将 gzip 数据流与其他数据流混合的文件格式时，
// 禁用该行为会很有用。
// 在此模式下，当 [Reader] 到达数据流末尾时，
// [Reader.Read] 返回 [io.EOF]。底层 reader 必须实现 [io.ByteReader]，
// 以便使其恰好停留在 gzip 流之后。
// 要开始下一个流，调用 z.Reset(r)，然后调用 z.Multistream(false)。
// 如果没有下一个流，z.Reset(r) 将返回 [io.EOF]。
func (z *Reader) Multistream(ok bool) {
	z.multistream = ok
}

// readString 从 z.r 读取一个以 NUL 结尾的字符串。
// 它将读取到的字节视为 ISO 8859-1（Latin-1）编码，
// 并输出使用 UTF-8 编码的字符串。
// 此方法总是用读取到的数据更新 z.digest。
func (z *Reader) readString() (string, error) {
	var err error
	needConv := false
	for i := 0; ; i++ {
		if i >= len(z.buf) {
			return "", ErrHeader
		}
		z.buf[i], err = z.r.ReadByte()
		if err != nil {
			return "", err
		}
		if z.buf[i] > 0x7f {
			needConv = true
		}
		if z.buf[i] == 0 {
			// Digest 涵盖 NUL 终止符。
			z.digest = crc32.Update(z.digest, crc32.IEEETable, z.buf[:i+1])

			// 字符串为 ISO 8859-1、Latin-1（RFC 1952，第 2.3.1 节）。
			if needConv {
				s := make([]rune, 0, i)
				for _, v := range z.buf[:i] {
					s = append(s, rune(v))
				}
				return string(s), nil
			}
			return string(z.buf[:i]), nil
		}
	}
}

// readHeader 根据第 2.3.1 节读取 GZIP 头部。
// 此方法不设置 z.err。
func (z *Reader) readHeader() (hdr Header, err error) {
	if _, err = io.ReadFull(z.r, z.buf[:10]); err != nil {
		// RFC 1952 第 2.2 节写道：
		//	一个 gzip 文件由一系列“成员”（压缩数据集）组成。
		//
		// 除此之外，规范并未阐明“一系列”被定义为
		// “一个或多个”还是“零个或多个”。为谨慎起见，
		// Go 将其解释为“零个或多个”。
		// 因此，这里返回 io.EOF 是可以的。
		return hdr, err
	}
	if z.buf[0] != gzipID1 || z.buf[1] != gzipID2 || z.buf[2] != gzipDeflate {
		return hdr, ErrHeader
	}
	flg := z.buf[3]
	if t := int64(le.Uint32(z.buf[4:8])); t > 0 {
		// 第 2.3.1 节，MTIME 的零值表示
		// 未设置修改时间。
		hdr.ModTime = time.Unix(t, 0)
	}
	// z.buf[8] 是 XFL，目前被忽略。
	hdr.OS = z.buf[9]
	z.digest = crc32.ChecksumIEEE(z.buf[:10])

	if flg&flagExtra != 0 {
		if _, err = io.ReadFull(z.r, z.buf[:2]); err != nil {
			return hdr, noEOF(err)
		}
		z.digest = crc32.Update(z.digest, crc32.IEEETable, z.buf[:2])
		data := make([]byte, le.Uint16(z.buf[:2]))
		if _, err = io.ReadFull(z.r, data); err != nil {
			return hdr, noEOF(err)
		}
		z.digest = crc32.Update(z.digest, crc32.IEEETable, data)
		hdr.Extra = data
	}

	var s string
	if flg&flagName != 0 {
		if s, err = z.readString(); err != nil {
			return hdr, noEOF(err)
		}
		hdr.Name = s
	}

	if flg&flagComment != 0 {
		if s, err = z.readString(); err != nil {
			return hdr, noEOF(err)
		}
		hdr.Comment = s
	}

	if flg&flagHdrCrc != 0 {
		if _, err = io.ReadFull(z.r, z.buf[:2]); err != nil {
			return hdr, noEOF(err)
		}
		digest := le.Uint16(z.buf[:2])
		if digest != uint16(z.digest) {
			return hdr, ErrHeader
		}
	}

	z.digest = 0
	if z.decompressor == nil {
		z.decompressor = flate.NewReader(z.r)
	} else {
		z.decompressor.(flate.Resetter).Reset(z.r, nil)
	}
	return hdr, nil
}

// Read 实现 [io.Reader]，从其底层 reader 读取未压缩字节。
func (z *Reader) Read(p []byte) (n int, err error) {
	if z.err != nil {
		return 0, z.err
	}

	for n == 0 {
		n, z.err = z.decompressor.Read(p)
		z.digest = crc32.Update(z.digest, crc32.IEEETable, p[:n])
		z.size += uint32(n)
		if z.err != io.EOF {
			// 正常情况下我们在这里返回。
			return n, z.err
		}

		// 文件已结束；检查校验和与大小。
		if _, err := io.ReadFull(z.r, z.buf[:8]); err != nil {
			z.err = noEOF(err)
			return n, z.err
		}
		digest := le.Uint32(z.buf[:4])
		size := le.Uint32(z.buf[4:8])
		if digest != z.digest || size != z.size {
			z.err = ErrChecksum
			return n, z.err
		}
		z.digest, z.size = 0, 0

		// 文件正常；检查是否还有另一个。
		if !z.multistream {
			return n, io.EOF
		}
		z.err = nil // 移除 io.EOF

		if _, z.err = z.readHeader(); z.err != nil {
			return n, z.err
		}
	}

	return n, nil
}

// Close 关闭 [Reader]。它不会关闭底层 reader。
// 为了验证 GZIP 校验和，必须将 reader
// 完全消费直到 [io.EOF]。
func (z *Reader) Close() error { return z.decompressor.Close() }
