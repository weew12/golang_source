// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"errors"
	"io"
	"strings"

	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
	"encoding/json/jsontext"
)

// export 暴露 "jsontext" 包的内部功能。
var export = jsontext.Internal.Export(&internal.AllowInternalUse)

// Valid 报告 data 是否是有效的 JSON 编码。
func Valid(data []byte) bool {
	return checkValid(data) == nil
}

func checkValid(data []byte) error {
	d := export.GetBufferedDecoder(data)
	defer export.PutBufferedDecoder(d)
	xd := export.Decoder(d)
	xd.Struct.Flags.Set(jsonflags.AllowDuplicateNames | jsonflags.AllowInvalidUTF8 | 1)
	if _, err := d.ReadValue(); err != nil {
		if err == io.EOF {
			offset := d.InputOffset() + int64(len(d.UnreadBuffer()))
			err = &jsontext.SyntacticError{ByteOffset: offset, Err: io.ErrUnexpectedEOF}
		}
		return transformSyntacticError(err)
	}
	if err := xd.CheckEOF(); err != nil {
		return transformSyntacticError(err)
	}
	return nil
}

// SyntaxError 是对 JSON 语法错误的描述。
// 如果 JSON 无法被解析，[Unmarshal] 将返回 SyntaxError。
type SyntaxError struct {
	msg    string // 错误描述
	Offset int64  // 错误发生在读取 Offset 个字节之后
}

func (e *SyntaxError) Error() string { return e.msg }

var errUnexpectedEnd = errors.New("unexpected end of JSON input")

func transformSyntacticError(err error) error {
	switch serr, ok := err.(*jsontext.SyntacticError); {
	case serr != nil:
		if serr.Err == io.ErrUnexpectedEOF {
			serr.Err = errUnexpectedEnd
		}
		msg := serr.Err.Error()
		if i := strings.Index(msg, " (expecting"); i >= 0 && !strings.Contains(msg, " in literal") {
			msg = msg[:i]
		}
		return &SyntaxError{Offset: serr.ByteOffset, msg: syntaxErrorReplacer.Replace(msg)}
	case ok:
		return (*SyntaxError)(nil)
	case export.IsIOError(err):
		return errors.Unwrap(err) // v1 历史上不包装 IO 错误
	default:
		return err
	}
}

// syntaxErrorReplacer 替换 v2 错误中的某些字符串字面量，
// 以更好地匹配语法错误的历史字符串呈现。
// 特别是，v2 使用术语 "object name" 以匹配 RFC 8259，
// 而 v1 使用 "object key"，这不是 JSON 文献中的术语。
var syntaxErrorReplacer = strings.NewReplacer(
	"object name", "object key",
	"at start of value", "looking for beginning of value",
	"at start of string", "looking for beginning of object key string",
	"after object value", "after object key:value pair",
	"in number", "in numeric literal",
)
