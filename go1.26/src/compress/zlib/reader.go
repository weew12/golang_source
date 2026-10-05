// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
zlib 包实现了 zlib 格式压缩数据的读写，
该格式由 RFC 1950 规定。

该实现提供了在读取时解压、在写入时压缩的过滤器。
例如，将压缩数据写入缓冲区：

	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write([]byte("hello, world\n"))
	w.Close()

以及将这些数据读回：

	r, err := zlib.NewReader(&b)
	io.Copy(os.Stdout, r)
	r.Close()
*/
package zlib

import (
	"bufio"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash"
	"hash/adler32"
	"io"
)

const (
	zlibDeflate   = 8
	zlibMaxWindow = 7
)

var (
	// ErrChecksum 在读取校验和无效的 ZLIB 数据时返回。
	ErrChecksum = errors.New("zlib: invalid checksum")
	// ErrDictionary 在读取字典无效的 ZLIB 数据时返回。
	ErrDictionary = errors.New("zlib: invalid dictionary")
	// ErrHeader 在读取头部无效的 ZLIB 数据时返回。
	ErrHeader = errors.New("zlib: invalid header")
)

type reader struct {
	r            flate.Reader
	decompressor io.ReadCloser
	digest       hash.Hash32
	err          error
	scratch      [4]byte
}

// Resetter 重置由 [NewReader] 或 [NewReaderDict] 返回的 ReadCloser，
// 以切换到新的底层 Reader。这允许复用 ReadCloser
// 而不是分配一个新的。
type Resetter interface {
	// Reset 丢弃任何已缓冲的数据，并把 Resetter 重置为
	// 如同刚用给定 reader 初始化的状态。
	Reset(r io.Reader, dict []byte) error
}

// NewReader 创建一个新的 ReadCloser。
// 从返回的 ReadCloser 读取时会从 r 读取并解压数据。
// 如果 r 未实现 [io.ByteReader]，解压器可能从 r 读取比所需更多的数据。
// 调用者有责任在完成后对 ReadCloser 调用 Close。
//
// NewReader 返回的 [io.ReadCloser] 也实现了 [Resetter]。
func NewReader(r io.Reader) (io.ReadCloser, error) {
	return NewReaderDict(r, nil)
}

// NewReaderDict 类似于 [NewReader]，但使用预设字典。
// 如果压缩数据没有引用该字典，NewReaderDict 会忽略它。
// 如果压缩数据引用了不同的字典，NewReaderDict 返回 [ErrDictionary]。
//
// NewReaderDict 返回的 ReadCloser 也实现了 [Resetter]。
func NewReaderDict(r io.Reader, dict []byte) (io.ReadCloser, error) {
	z := new(reader)
	err := z.Reset(r, dict)
	if err != nil {
		return nil, err
	}
	return z, nil
}

func (z *reader) Read(p []byte) (int, error) {
	if z.err != nil {
		return 0, z.err
	}

	var n int
	n, z.err = z.decompressor.Read(p)
	z.digest.Write(p[0:n])
	if z.err != io.EOF {
		// 正常情况下我们在这里返回。
		return n, z.err
	}

	// 文件结束；检查校验和。
	if _, err := io.ReadFull(z.r, z.scratch[0:4]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		z.err = err
		return n, z.err
	}
	// 与 GZIP（RFC 1952）不同，ZLIB（RFC 1950）使用大端序。
	checksum := binary.BigEndian.Uint32(z.scratch[:4])
	if checksum != z.digest.Sum32() {
		z.err = ErrChecksum
		return n, z.err
	}
	return n, io.EOF
}

// 调用 Close 不会关闭最初传给 [NewReader] 的包装 [io.Reader]。
// 为了验证 ZLIB 校验和，读取器必须
// 被完全消费直到 [io.EOF]。
func (z *reader) Close() error {
	if z.err != nil && z.err != io.EOF {
		return z.err
	}
	z.err = z.decompressor.Close()
	return z.err
}

func (z *reader) Reset(r io.Reader, dict []byte) error {
	*z = reader{decompressor: z.decompressor}
	if fr, ok := r.(flate.Reader); ok {
		z.r = fr
	} else {
		z.r = bufio.NewReader(r)
	}

	// 读取头部（RFC 1950 第 2.2 节）。
	_, z.err = io.ReadFull(z.r, z.scratch[0:2])
	if z.err != nil {
		if z.err == io.EOF {
			z.err = io.ErrUnexpectedEOF
		}
		return z.err
	}
	h := binary.BigEndian.Uint16(z.scratch[:2])
	if (z.scratch[0]&0x0f != zlibDeflate) || (z.scratch[0]>>4 > zlibMaxWindow) || (h%31 != 0) {
		z.err = ErrHeader
		return z.err
	}
	haveDict := z.scratch[1]&0x20 != 0
	if haveDict {
		_, z.err = io.ReadFull(z.r, z.scratch[0:4])
		if z.err != nil {
			if z.err == io.EOF {
				z.err = io.ErrUnexpectedEOF
			}
			return z.err
		}
		checksum := binary.BigEndian.Uint32(z.scratch[:4])
		if checksum != adler32.Checksum(dict) {
			z.err = ErrDictionary
			return z.err
		}
	}

	if z.decompressor == nil {
		if haveDict {
			z.decompressor = flate.NewReaderDict(z.r, dict)
		} else {
			z.decompressor = flate.NewReader(z.r)
		}
	} else {
		z.decompressor.(flate.Resetter).Reset(z.r, dict)
	}
	z.digest = adler32.New()
	return nil
}
