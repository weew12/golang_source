// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package quotedprintable

import "io"

const lineMaxLen = 76

// A Writer is a quoted-printable writer that implements [io.WriteCloser].
type Writer struct {
	// Binary 模式将写入器的输入视为纯二进制，并将行尾字节作为二进制数据处理。
	Binary bool

	w    io.Writer
	i    int
	line [78]byte
	cr   bool
}

// NewWriter 返回一个新的 [Writer]，写入到 w。
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Write 使用 quoted-printable 编码将 p 写入底层 [io.Writer]。
// 它将行长度限制为 76 个字符。编码字节不会自动刷新直到 [Writer] 关闭。
func (w *Writer) Write(p []byte) (n int, err error) {
	for i, b := range p {
		switch {
		// 简单写入批量完成。
		case b >= '!' && b <= '~' && b != '=':
			continue
		case isWhitespace(b) || !w.Binary && (b == '\n' || b == '\r'):
			continue
		}

		if i > n {
			if err := w.write(p[n:i]); err != nil {
				return n, err
			}
			n = i
		}

		if err := w.encode(b); err != nil {
			return n, err
		}
		n++
	}

	if n == len(p) {
		return n, nil
	}

	if err := w.write(p[n:]); err != nil {
		return n, err
	}

	return len(p), nil
}

// Close 关闭 [Writer]，将任何未写入的数据刷新到底层 [io.Writer]，
// 但不关闭底层的 io.Writer。
func (w *Writer) Close() error {
	if err := w.checkLastByte(); err != nil {
		return err
	}

	return w.flush()
}

// write 将以 quoted-printable 编码的文本限制为每行 76 个字符。
func (w *Writer) write(p []byte) error {
	for _, b := range p {
		if b == '\n' || b == '\r' {
			// 若前一个字节是 \r，则 CRLF 已插入。
			if w.cr && b == '\n' {
				w.cr = false
				continue
			}

			if b == '\r' {
				w.cr = true
			}

			if err := w.checkLastByte(); err != nil {
				return err
			}
			if err := w.insertCRLF(); err != nil {
				return err
			}
			continue
		}

		if w.i == lineMaxLen-1 {
			if err := w.insertSoftLineBreak(); err != nil {
				return err
			}
		}

		w.line[w.i] = b
		w.i++
		w.cr = false
	}

	return nil
}

func (w *Writer) encode(b byte) error {
	if lineMaxLen-1-w.i < 3 {
		if err := w.insertSoftLineBreak(); err != nil {
			return err
		}
	}

	w.line[w.i] = '='
	w.line[w.i+1] = upperhex[b>>4]
	w.line[w.i+2] = upperhex[b&0x0f]
	w.i += 3

	return nil
}

const upperhex = "0123456789ABCDEF"

// checkLastByte 若最后一个缓冲字节是空格或制表符，则对其进行编码。
func (w *Writer) checkLastByte() error {
	if w.i == 0 {
		return nil
	}

	b := w.line[w.i-1]
	if isWhitespace(b) {
		w.i--
		if err := w.encode(b); err != nil {
			return err
		}
	}

	return nil
}

func (w *Writer) insertSoftLineBreak() error {
	w.line[w.i] = '='
	w.i++

	return w.insertCRLF()
}

func (w *Writer) insertCRLF() error {
	w.line[w.i] = '\r'
	w.line[w.i+1] = '\n'
	w.i += 2

	return w.flush()
}

func (w *Writer) flush() error {
	if _, err := w.w.Write(w.line[:w.i]); err != nil {
		return err
	}

	w.i = 0
	return nil
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t'
}
