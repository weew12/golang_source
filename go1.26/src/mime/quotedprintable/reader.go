// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package quotedprintable implements quoted-printable encoding as specified by
// RFC 2045.
package quotedprintable

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// Reader 是 quoted-printable 解码器。
type Reader struct {
	br   *bufio.Reader
	rerr error  // last read error
	line []byte // to be consumed before more of br
}

// NewReader 返回一个 quoted-printable 读取器，从 r 解码。
func NewReader(r io.Reader) *Reader {
	return &Reader{
		br: bufio.NewReader(r),
	}
}

func fromHex(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, nil
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	}
	return 0, fmt.Errorf("quotedprintable: invalid hex byte 0x%02x", b)
}

func readHexByte(v []byte) (b byte, err error) {
	if len(v) < 2 {
		return 0, io.ErrUnexpectedEOF
	}
	var hb, lb byte
	if hb, err = fromHex(v[0]); err != nil {
		return 0, err
	}
	if lb, err = fromHex(v[1]); err != nil {
		return 0, err
	}
	return hb<<4 | lb, nil
}

func isQPDiscardWhitespace(r rune) bool {
	switch r {
	case '\n', '\r', ' ', '\t':
		return true
	}
	return false
}

var (
	crlf       = []byte("\r\n")
	lf         = []byte("\n")
	softSuffix = []byte("=")
	lwspChar   = " \t"
)

// Read 从底层读取器读取并解码 quoted-printable 数据。
func (r *Reader) Read(p []byte) (n int, err error) {
	// 与 RFC 2045 的偏差：
	// 1. 除 "=\r\n" 外，"=\n" 也被视为软换行。
	// 2. 它会透传未以 '=' 为前导的 '\r' 或 '\n'，与其它有问题的 QP 编码器/解码器保持一致。
	// 3. 它接受消息末尾的软换行 (=)（issue 15486）；
	//    即允许从底层读取器读取的最后一个字节为 '='，它将被静默忽略。
	// 4. 若 '=' 后未跟两个十六进制数字（但不在行尾），则将其作为字面 '='（issue 13219）。
	for len(p) > 0 {
		if len(r.line) == 0 {
			if r.rerr != nil {
				return n, r.rerr
			}
			r.line, r.rerr = r.br.ReadSlice('\n')

			// 行是以 CRLF 结尾还是仅以 LF 结尾？
			hasLF := bytes.HasSuffix(r.line, lf)
			hasCR := bytes.HasSuffix(r.line, crlf)
			wholeLine := r.line
			r.line = bytes.TrimRightFunc(wholeLine, isQPDiscardWhitespace)
			if bytes.HasSuffix(r.line, softSuffix) {
				rightStripped := bytes.TrimLeft(wholeLine[len(r.line):], lwspChar)
				r.line = r.line[:len(r.line)-1]
				if !bytes.HasPrefix(rightStripped, lf) && !bytes.HasPrefix(rightStripped, crlf) &&
					!(len(rightStripped) == 0 && len(r.line) > 0 && r.rerr == io.EOF) {
					r.rerr = fmt.Errorf("quotedprintable: invalid bytes after =: %q", rightStripped)
				}
			} else if hasLF {
				if hasCR {
					r.line = append(r.line, '\r', '\n')
				} else {
					r.line = append(r.line, '\n')
				}
			}
			continue
		}
		b := r.line[0]

		switch {
		case b == '=':
			b, err = readHexByte(r.line[1:])
			if err != nil {
				if len(r.line) >= 2 && r.line[1] != '\r' && r.line[1] != '\n' {
					// 将 '=' 作为字面 '='。
					b = '='
					break
				}
				return n, err
			}
			r.line = r.line[2:] // 2 of the 3; other 1 is done below
		case b == '\t' || b == '\r' || b == '\n':
			break
		case b >= 0x80:
			// As an extension to RFC 2045, we accept
			// values >= 0x80 without complaint. Issue 22597.
			break
		case b < ' ' || b > '~':
			return n, fmt.Errorf("quotedprintable: invalid unescaped byte 0x%02x in body", b)
		}
		p[0] = b
		p = p[1:]
		r.line = r.line[1:]
		n++
	}
	return n, nil
}
