// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonwire"
)

// AppendQuote 将表示 src 的双引号 JSON 字符串字面量追加到 dst，
// 并返回扩展后的缓冲区。
// 它使用 RFC 8785 第 3.2.2.2 节规定的最小字符串表示。
// 无效的 UTF-8 字节会被替换为 Unicode 替换字符，
// 并在末尾返回一个错误，表明存在无效的 UTF-8。
// dst 不得与 src 重叠。
func AppendQuote[Bytes ~[]byte | ~string](dst []byte, src Bytes) ([]byte, error) {
	dst, err := jsonwire.AppendQuote(dst, src, &jsonflags.Flags{})
	if err != nil {
		err = &SyntacticError{Err: err}
	}
	return dst, err
}

// AppendUnquote 将 src 解码后的解释作为双引号 JSON 字符串字面量
// 追加到 dst，并返回扩展后的缓冲区。
// 输入 src 必须是一个不带任何周围空白的 JSON 字符串。
// 无效的 UTF-8 字节会被替换为 Unicode 替换字符，
// 并在末尾返回一个错误，表明存在无效的 UTF-8。
// JSON 字符串字面量之后的任何尾随字节都会导致错误。
// dst 不得与 src 重叠。
func AppendUnquote[Bytes ~[]byte | ~string](dst []byte, src Bytes) ([]byte, error) {
	dst, err := jsonwire.AppendUnquote(dst, src)
	if err != nil {
		err = &SyntacticError{Err: err}
	}
	return dst, err
}
