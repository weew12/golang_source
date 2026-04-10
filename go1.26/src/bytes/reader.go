// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bytes

import (
	"errors"
	"io"
	"unicode/utf8"
)

// Reader 通过读取字节切片实现了 [io.Reader]、[io.ReaderAt]、[io.WriterTo]、[io.Seeker]、
// [io.ByteScanner] 和 [io.RuneScanner] 接口。
// 与 [Buffer] 不同，Reader 是只读的且支持寻址。
// Reader 的零值行为类似于读取空切片的 Reader。
type Reader struct {
	s        []byte
	i        int64 // 当前读取索引
	prevRune int   // 上一个 rune 的索引；或 < 0
}

// Len 返回切片中未读取部分的字节数。
func (r *Reader) Len() int {
	if r.i >= int64(len(r.s)) {
		return 0
	}
	return int(int64(len(r.s)) - r.i)
}

// Size 返回底层字节切片的原始长度。
// Size 是通过 [Reader.ReadAt] 可读取的字节数。
// 除 [Reader.Reset] 外，任何方法调用都不会影响该结果。
func (r *Reader) Size() int64 { return int64(len(r.s)) }

// Read 实现了 [io.Reader] 接口。
func (r *Reader) Read(b []byte) (n int, err error) {
	if r.i >= int64(len(r.s)) {
		return 0, io.EOF
	}
	r.prevRune = -1
	n = copy(b, r.s[r.i:])
	r.i += int64(n)
	return
}

// ReadAt 实现了 [io.ReaderAt] 接口。
func (r *Reader) ReadAt(b []byte, off int64) (n int, err error) {
	// 不能修改状态 - 参见 io.ReaderAt
	if off < 0 {
		return 0, errors.New("bytes.Reader.ReadAt: negative offset")
	}
	if off >= int64(len(r.s)) {
		return 0, io.EOF
	}
	n = copy(b, r.s[off:])
	if n < len(b) {
		err = io.EOF
	}
	return
}

// ReadByte 实现了 [io.ByteReader] 接口。
func (r *Reader) ReadByte() (byte, error) {
	r.prevRune = -1
	if r.i >= int64(len(r.s)) {
		return 0, io.EOF
	}
	b := r.s[r.i]
	r.i++
	return b, nil
}

// UnreadByte 补充 [Reader.ReadByte] 实现了 [io.ByteScanner] 接口。
func (r *Reader) UnreadByte() error {
	if r.i <= 0 {
		return errors.New("bytes.Reader.UnreadByte: at beginning of slice")
	}
	r.prevRune = -1
	r.i--
	return nil
}

// ReadRune 实现了 [io.RuneReader] 接口。
func (r *Reader) ReadRune() (ch rune, size int, err error) {
	if r.i >= int64(len(r.s)) {
		r.prevRune = -1
		return 0, 0, io.EOF
	}
	r.prevRune = int(r.i)
	if c := r.s[r.i]; c < utf8.RuneSelf {
		r.i++
		return rune(c), 1, nil
	}
	ch, size = utf8.DecodeRune(r.s[r.i:])
	r.i += int64(size)
	return
}

// UnreadRune 补充 [Reader.ReadRune] 实现了 [io.RuneScanner] 接口。
func (r *Reader) UnreadRune() error {
	if r.i <= 0 {
		return errors.New("bytes.Reader.UnreadRune: at beginning of slice")
	}
	if r.prevRune < 0 {
		return errors.New("bytes.Reader.UnreadRune: previous operation was not ReadRune")
	}
	r.i = int64(r.prevRune)
	r.prevRune = -1
	return nil
}

// Seek 实现了 [io.Seeker] 接口。
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	r.prevRune = -1
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.i + offset
	case io.SeekEnd:
		abs = int64(len(r.s)) + offset
	default:
		return 0, errors.New("bytes.Reader.Seek: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("bytes.Reader.Seek: negative position")
	}
	r.i = abs
	return abs, nil
}

// WriteTo 实现了 [io.WriterTo] 接口。
func (r *Reader) WriteTo(w io.Writer) (n int64, err error) {
	r.prevRune = -1
	if r.i >= int64(len(r.s)) {
		return 0, nil
	}
	b := r.s[r.i:]
	m, err := w.Write(b)
	if m > len(b) {
		panic("bytes.Reader.WriteTo: invalid Write count")
	}
	r.i += int64(m)
	n = int64(m)
	if m != len(b) && err == nil {
		err = io.ErrShortWrite
	}
	return
}

// Reset 重置 [Reader] 以从 b 读取。
func (r *Reader) Reset(b []byte) { *r = Reader{b, 0, -1} }

// NewReader 返回一个从 b 读取的新 [Reader]。
func NewReader(b []byte) *Reader { return &Reader{b, 0, -1} }
