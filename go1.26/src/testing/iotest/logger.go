// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package iotest

import (
	"io"
	"log"
)

type writeLogger struct {
	prefix string
	w      io.Writer
}

func (l *writeLogger) Write(p []byte) (n int, err error) {
	n, err = l.w.Write(p)
	if err != nil {
		log.Printf("%s %x: %v", l.prefix, p[0:n], err)
	} else {
		log.Printf("%s %x", l.prefix, p[0:n])
	}
	return
}

// NewWriteLogger 返回一个 writer，其行为与 w 相同，
// 但会在每次写入时通过 [log.Printf] 向标准错误记录日志，
// 打印前缀以及所写入数据的十六进制表示。
func NewWriteLogger(prefix string, w io.Writer) io.Writer {
	return &writeLogger{prefix, w}
}

type readLogger struct {
	prefix string
	r      io.Reader
}

func (l *readLogger) Read(p []byte) (n int, err error) {
	n, err = l.r.Read(p)
	if err != nil {
		log.Printf("%s %x: %v", l.prefix, p[0:n], err)
	} else {
		log.Printf("%s %x", l.prefix, p[0:n])
	}
	return
}

// NewReadLogger 返回一个 reader，其行为与 r 相同，
// 但会在每次读取时通过 [log.Printf] 向标准错误记录日志，
// 打印前缀以及所读取数据的十六进制表示。
func NewReadLogger(prefix string, r io.Reader) io.Reader {
	return &readLogger{prefix, r}
}
