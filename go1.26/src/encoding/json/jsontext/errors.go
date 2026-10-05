// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"bytes"
	"io"
	"strconv"

	"encoding/json/internal/jsonwire"
)

const errorPrefix = "jsontext: "

type ioError struct {
	action string // "read" 或 "write"
	err    error
}

func (e *ioError) Error() string {
	return errorPrefix + e.action + " error: " + e.err.Error()
}
func (e *ioError) Unwrap() error {
	return e.err
}

// SyntacticError 是对在根据语法编码或解码 JSON 时
// 发生的语法错误的描述。
//
// 本包产生的此错误的内容可能会随时间而变化。
type SyntacticError struct {
	requireKeyedLiterals
	nonComparable

	// ByteOffset 表明错误发生在此字节偏移量之后。
	ByteOffset int64
	// JSONPointer 表明错误发生在此 JSON 值内，
	// 使用 JSON Pointer 表示法（参见 RFC 6901）指示。
	JSONPointer Pointer

	// Err 是底层错误。
	Err error
}

// wrapSyntacticError 包装一个错误，并使用所提供的
// [encoderState] 或 [decoderState] 为其标注精确的位置。
// 如果 err 是 [ioError] 或 [io.EOF]，则不会被包装。
//
// 它接收一个相对偏移量 pos，可通过 state.offsetAt
// 解析为绝对偏移量。
//
// 它接收一个 where，用于指定 JSON pointer 是如何推导的。
// 如果底层错误是 [pointerSuffixError]，
// 则将该后缀追加到推导出的 pointer 上。
func wrapSyntacticError(state interface {
	offsetAt(pos int) int64
	AppendStackPointer(b []byte, where int) []byte
}, err error, pos, where int) error {
	if _, ok := err.(*ioError); err == io.EOF || ok {
		return err
	}
	offset := state.offsetAt(pos)
	ptr := state.AppendStackPointer(nil, where)
	if serr, ok := err.(*pointerSuffixError); ok {
		ptr = serr.appendPointer(ptr)
		err = serr.error
	}
	if d, ok := state.(*decoderState); ok && err == errMismatchDelim {
		where := "at start of value"
		if len(d.Tokens.Stack) > 0 && d.Tokens.Last.Length() > 0 {
			switch {
			case d.Tokens.Last.isArray():
				where = "after array element (expecting ',' or ']')"
				ptr = []byte(Pointer(ptr).Parent()) // 问题出在父数组上
			case d.Tokens.Last.isObject():
				where = "after object value (expecting ',' or '}')"
				ptr = []byte(Pointer(ptr).Parent()) // 问题出在父对象上
			}
		}
		err = jsonwire.NewInvalidCharacterError(d.buf[pos:], where)
	}
	return &SyntacticError{ByteOffset: offset, JSONPointer: Pointer(ptr), Err: err}
}

func (e *SyntacticError) Error() string {
	pointer := e.JSONPointer
	offset := e.ByteOffset
	b := []byte(errorPrefix)
	if e.Err != nil {
		b = append(b, e.Err.Error()...)
		if e.Err == ErrDuplicateName {
			b = strconv.AppendQuote(append(b, ' '), pointer.LastToken())
			pointer = pointer.Parent()
			offset = 0 // 为重复名称打印偏移量没有用处
		}
	} else {
		b = append(b, "syntactic error"...)
	}
	if pointer != "" {
		b = strconv.AppendQuote(append(b, " within "...), jsonwire.TruncatePointer(string(pointer), 100))
	}
	if offset > 0 {
		b = strconv.AppendInt(append(b, " after offset "...), offset, 10)
	}
	return string(b)
}

func (e *SyntacticError) Unwrap() error {
	return e.Err
}

// pointerSuffixError 表示要追加到
// [SyntacticError.JSONPointer] 的 JSON pointer 后缀。它是本包内部
// 使用的错误类型，不会出现在公开 API 中。
//
// 此类型主要用于在 Encoder.WriteValue
// 和 Decoder.ReadValue 中为错误标注精确的位置。
// 在调用 WriteValue 或 ReadValue 时，可以使用 Encoder/Decoder 的状态
// 构造指向即将到来的值的 JSON pointer。
// 然而，在正常操作期间跟踪值内部的 pointer
// 会在无错误的情况下带来性能损失。
//
// 为了在不产生这种开销的情况下提供精确的错误位置，
// 当发生错误时，随着调用栈的弹出，
// 错误会被对象名称或数组索引包装。
// 由于这以相反的顺序发生，pointerSuffixError 会以相反的顺序
// 持有 pointer，只有在追加到 pointer 前缀时
// 才会将其反转。
//
// 例如，如果编码器位于 "/alpha/bravo/charlie"，
// 并且在 WriteValue 中 "/xray/yankee/zulu" 处发生错误，那么
// 最终的 pointer 应为 "/alpha/bravo/charlie/xray/yankee/zulu"。
//
// 由于 pointerSuffixError 是在错误返回路径中填充的，
// 它首先包含 "/zulu"，然后是 "/zulu/yankee"，
// 最后是 "/zulu/yankee/xray"。
// 这些 token 会被反转并连接到 "/alpha/bravo/charlie"，
// 从而形成完整的 pointer。
type pointerSuffixError struct {
	error

	// reversePointer 是一个 JSON pointer，但每个 token 的顺序都是反的。
	reversePointer []byte
}

// wrapWithObjectName 用一个 JSON 对象名称访问来包装 err，
// 该名称必须是有效的带引号 JSON 字符串。
func wrapWithObjectName(err error, quotedName []byte) error {
	serr, _ := err.(*pointerSuffixError)
	if serr == nil {
		serr = &pointerSuffixError{error: err}
	}
	name := jsonwire.UnquoteMayCopy(quotedName, false)
	serr.reversePointer = appendEscapePointerName(append(serr.reversePointer, '/'), name)
	return serr
}

// wrapWithArrayIndex 用一个 JSON 数组索引访问来包装 err。
func wrapWithArrayIndex(err error, index int64) error {
	serr, _ := err.(*pointerSuffixError)
	if serr == nil {
		serr = &pointerSuffixError{error: err}
	}
	serr.reversePointer = strconv.AppendUint(append(serr.reversePointer, '/'), uint64(index), 10)
	return serr
}

// appendPointer 将 e 中编码的路径追加到 pointer 的末尾。
func (e *pointerSuffixError) appendPointer(pointer []byte) []byte {
	// 按相反的顺序将 reversePointer 中的每个 token 复制到 pointer 的末尾。
	// 双重反转意味着所追加的后缀现在处于正向顺序。
	bi, bo := e.reversePointer, pointer
	for len(bi) > 0 {
		i := bytes.LastIndexByte(bi, '/')
		bi, bo = bi[:i], append(bo, bi[i:]...)
	}
	return bo
}
