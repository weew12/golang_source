// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package multipart

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/textproto"
	"slices"
	"strings"
)

// Writer 生成多部分消息。
type Writer struct {
	w        io.Writer
	boundary string
	lastpart *part
}

// NewWriter 返回一个新的 multipart [Writer]，带有随机边界，写入到 w。
func NewWriter(w io.Writer) *Writer {
	return &Writer{
		w:        w,
		boundary: randomBoundary(),
	}
}

// Boundary 返回 [Writer] 的边界。
func (w *Writer) Boundary() string {
	return w.boundary
}

// SetBoundary 用显式值覆盖 [Writer] 默认随机生成的边界分隔符。
//
// SetBoundary 必须在创建任何部分之前调用，只能包含某些 ASCII 字符，
// 且必须非空且最多 70 字节长。
func (w *Writer) SetBoundary(boundary string) error {
	if w.lastpart != nil {
		return errors.New("mime: SetBoundary called after write")
	}
	// rfc2046#section-5.1.1
	if len(boundary) < 1 || len(boundary) > 70 {
		return errors.New("mime: invalid boundary length")
	}
	end := len(boundary) - 1
	for i, b := range boundary {
		if 'A' <= b && b <= 'Z' || 'a' <= b && b <= 'z' || '0' <= b && b <= '9' {
			continue
		}
		switch b {
		case '\'', '(', ')', '+', '_', ',', '-', '.', '/', ':', '=', '?':
			continue
		case ' ':
			if i != end {
				continue
			}
		}
		return errors.New("mime: invalid boundary character")
	}
	w.boundary = boundary
	return nil
}

// FormDataContentType 返回带有此 [Writer] 边界的 HTTP multipart/form-data 的 Content-Type。
func (w *Writer) FormDataContentType() string {
	b := w.boundary
	// 若边界包含任何 RFC 2045 定义的 tspecials 字符或空格，我们必须为其加引号。
	if strings.ContainsAny(b, `()<>@,;:\"/[]?= `) {
		b = `"` + b + `"`
	}
	return "multipart/form-data; boundary=" + b
}

func randomBoundary() string {
	var buf [30]byte
	_, err := io.ReadFull(rand.Reader, buf[:])
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", buf[:])
}

// CreatePart 使用提供的标题创建一个新的多部分节。
// 部分的正文应写入返回的 [Writer]。调用 CreatePart 后，任何先前的部分可能无法再写入。
func (w *Writer) CreatePart(header textproto.MIMEHeader) (io.Writer, error) {
	if w.lastpart != nil {
		if err := w.lastpart.close(); err != nil {
			return nil, err
		}
	}
	var b bytes.Buffer
	if w.lastpart != nil {
		fmt.Fprintf(&b, "\r\n--%s\r\n", w.boundary)
	} else {
		fmt.Fprintf(&b, "--%s\r\n", w.boundary)
	}

	for _, k := range slices.Sorted(maps.Keys(header)) {
		for _, v := range header[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&b, "\r\n")
	_, err := io.Copy(w.w, &b)
	if err != nil {
		return nil, err
	}
	p := &part{
		mw: w,
	}
	w.lastpart = p
	return p, nil
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "%0D", "\n", "%0A")

// escapeQuotes 转义字段参数值中的特殊字符。
//
// 由于历史原因，这对 " 和 \ 字符使用 \ 转义，对 CR 和 LF 使用百分号编码。
//
// WhatWG 表单数据编码规范建议我们使用百分号编码 " (%22)，不应转义 \。
// https://html.spec.whatwg.org/multipage/form-control-infrastructure.html#multipart/form-data-encoding-algorithm
//
// 经验性地，在此注释撰写时，有必要转义 \ 字符，
// 否则 Chrome（可能还有其他浏览器）会将未转义的 \ 解释为转义。
func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

// CreateFormFile 是 [Writer.CreatePart] 的便捷包装。它使用提供的字段名和文件名创建一个新的 form-data 标题。
func (w *Writer) CreateFormFile(fieldname, filename string) (io.Writer, error) {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", FileContentDisposition(fieldname, filename))
	h.Set("Content-Type", "application/octet-stream")
	return w.CreatePart(h)
}

// CreateFormField 使用给定字段名调用 [Writer.CreatePart] 创建标题。
func (w *Writer) CreateFormField(fieldname string) (io.Writer, error) {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="%s"`, escapeQuotes(fieldname)))
	return w.CreatePart(h)
}

// FileContentDisposition 返回具有给定字段名和文件名的 Content-Disposition 头部的值。
func FileContentDisposition(fieldname, filename string) string {
	return fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
		escapeQuotes(fieldname), escapeQuotes(filename))
}

// WriteField 调用 [Writer.CreateFormField] 然后写入给定的值。
func (w *Writer) WriteField(fieldname, value string) error {
	p, err := w.CreateFormField(fieldname)
	if err != nil {
		return err
	}
	_, err = p.Write([]byte(value))
	return err
}

// Close 完成多部分消息并将尾部边界结束行写入输出。
func (w *Writer) Close() error {
	if w.lastpart != nil {
		if err := w.lastpart.close(); err != nil {
			return err
		}
		w.lastpart = nil
	}
	_, err := fmt.Fprintf(w.w, "\r\n--%s--\r\n", w.boundary)
	return err
}

type part struct {
	mw     *Writer
	closed bool
	we     error // last error that occurred writing
}

func (p *part) close() error {
	p.closed = true
	return p.we
}

func (p *part) Write(d []byte) (n int, err error) {
	if p.closed {
		return 0, errors.New("multipart: can't write to finished part")
	}
	n, err = p.mw.w.Write(d)
	if err != nil {
		p.we = err
	}
	return
}
