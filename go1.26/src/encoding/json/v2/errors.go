// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
	"encoding/json/jsontext"
)

// ErrUnknownName 表示某个 JSON 对象成员无法被 unmarshal，
// 因为目标 Go struct 不认识该名称。
// 该错误在产生时会被直接包装在 [SemanticError] 内。
//
// 可以按如下方式提取未知 JSON 对象成员的名称：
//
//	err := ...
//	serr, ok := errors.AsType[*json.SemanticError](err)
//	if ok && serr.Err == json.ErrUnknownName {
//		ptr := serr.JSONPointer // JSON pointer to unknown name
//		name := ptr.LastToken() // unknown name itself
//		...
//	}
//
// 仅当 [RejectUnknownMembers] 为 true 时才会返回该错误。
var ErrUnknownName = errors.New("unknown object member name")

const errorPrefix = "json: "

func isSemanticError(err error) bool {
	_, ok := err.(*SemanticError)
	return ok
}

func isSyntacticError(err error) bool {
	_, ok := err.(*jsontext.SyntacticError)
	return ok
}

// isFatalError 报告该错误是否必须终止 asharling。
// 所有错误都被视为致命错误，除非处于
// [jsonflags.ReportErrorsWithLegacySemantics] 下，
// 在这种情况下只有语法错误和 I/O 错误被视为致命错误。
func isFatalError(err error, flags jsonflags.Flags) bool {
	return !flags.Get(jsonflags.ReportErrorsWithLegacySemantics) ||
		isSyntacticError(err) || export.IsIOError(err)
}

// SemanticError 描述在将 JSON 数据确定含义为 Go 数据
// （或反之）时发生的错误。
//
// 如果 [Marshaler]、[MarshalerTo]、[Unmarshaler] 或 [UnmarshalerFrom] 方法
// 在被 [json] 包调用时返回 SemanticError，
// 那么当 ByteOffset、JSONPointer 和 GoType 字段为零值时，
// 它们会被调用上下文自动填充。
//
// 本包产生的该错误的内容可能会随时间变化。
type SemanticError struct {
	requireKeyedLiterals
	nonComparable

	action string // 取值为 "marshal" 或 "unmarshal"

	// ByteOffset 表示错误发生在此字节偏移之后。
	ByteOffset int64
	// JSONPointer 表示错误发生在此 JSON 值内部，
	// 使用 JSON Pointer 表示法指示（参见 RFC 6901）。
	JSONPointer jsontext.Pointer

	// JSONKind 是无法处理的 JSON 种类。
	JSONKind jsontext.Kind // 如果未知则可能为零
	// JSONValue 是无法被 unmarshal 的 JSON 数字或字符串。
	// 在 marshaling 期间不会填充该字段。
	JSONValue jsontext.Value // 如果不相关或未知则可能为 nil
	// GoType 是无法处理的 Go 类型。
	GoType reflect.Type // 如果未知则可能为 nil

	// Err 是底层错误。
	Err error // 可能为 nil
}

// coder 由 [jsontext.Encoder] 或 [jsontext.Decoder] 实现。
type coder interface {
	StackPointer() jsontext.Pointer
	Options() Options
}

// newInvalidFormatError 将 err 包装进一个 SemanticError，因为
// 当前类型 t 无法处理所提供的 options 格式。
// 必须在产生或消费下一个值之前调用该错误。
//
// 如果指定了 [jsonflags.ReportErrorsWithLegacySemantics]，
// 那么在 unmarshaling 时这会自动跳过下一个值，
// 以确保该值被完全消费。
func newInvalidFormatError(c coder, t reflect.Type) error {
	err := fmt.Errorf("invalid format flag %q", c.Options().(*jsonopts.Struct).Format)
	switch c := c.(type) {
	case *jsontext.Encoder:
		err = newMarshalErrorBefore(c, t, err)
	case *jsontext.Decoder:
		err = newUnmarshalErrorBeforeWithSkipping(c, t, err)
	}
	return err
}

// newMarshalErrorBefore 将 err 包装进一个 SemanticError，假定 e
// 恰好位于下一个 token 或值之前，从而引发错误。
func newMarshalErrorBefore(e *jsontext.Encoder, t reflect.Type, err error) error {
	return &SemanticError{action: "marshal", GoType: t, Err: toUnexpectedEOF(err),
		ByteOffset:  e.OutputOffset() + int64(export.Encoder(e).CountNextDelimWhitespace()),
		JSONPointer: jsontext.Pointer(export.Encoder(e).AppendStackPointer(nil, +1))}
}

// newUnmarshalErrorBefore 将 err 包装进一个 SemanticError，假定 d
// 恰好位于下一个 token 或值之前，从而引发错误。
// 它不记录下一个 JSON 种类，因为该错误用于表示
// 接收方 Go 值无法被 unmarshal 到其中（而不是 JSON 错误）。
// 然而，如果指定了 [jsonflags.ReportErrorsWithLegacySemantics]，
// 那么出于历史报告的原因，它确实会记录下一个 JSON 种类。
func newUnmarshalErrorBefore(d *jsontext.Decoder, t reflect.Type, err error) error {
	var k jsontext.Kind
	if export.Decoder(d).Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		k = d.PeekKind()
	}
	return &SemanticError{action: "unmarshal", GoType: t, Err: toUnexpectedEOF(err),
		ByteOffset:  d.InputOffset() + int64(export.Decoder(d).CountNextDelimWhitespace()),
		JSONPointer: jsontext.Pointer(export.Decoder(d).AppendStackPointer(nil, +1)),
		JSONKind:    k}
}

// newUnmarshalErrorBeforeWithSkipping 类似 [newUnmarshalErrorBefore]，
// 但如果指定了 [jsonflags.ReportErrorsWithLegacySemantics]，
// 则会自动跳过下一个值。
func newUnmarshalErrorBeforeWithSkipping(d *jsontext.Decoder, t reflect.Type, err error) error {
	err = newUnmarshalErrorBefore(d, t, err)
	if export.Decoder(d).Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		if err2 := export.Decoder(d).SkipValue(); err2 != nil {
			return err2
		}
	}
	return err
}

// newUnmarshalErrorAfter 将 err 包装进一个 SemanticError，假定 d
// 恰好位于前一个 token 或值之后，而正是它引发了错误。
func newUnmarshalErrorAfter(d *jsontext.Decoder, t reflect.Type, err error) error {
	tokOrVal := export.Decoder(d).PreviousTokenOrValue()
	return &SemanticError{action: "unmarshal", GoType: t, Err: toUnexpectedEOF(err),
		ByteOffset:  d.InputOffset() - int64(len(tokOrVal)),
		JSONPointer: jsontext.Pointer(export.Decoder(d).AppendStackPointer(nil, -1)),
		JSONKind:    jsontext.Value(tokOrVal).Kind()}
}

// newUnmarshalErrorAfter 将 err 包装进一个 SemanticError，假定 d
// 恰好位于前一个 token 或值之后，而正是它引发了错误。
// 如果最后一个 JSON 值是字符串或数字，它还会存储该值的一份副本。
func newUnmarshalErrorAfterWithValue(d *jsontext.Decoder, t reflect.Type, err error) error {
	serr := newUnmarshalErrorAfter(d, t, err).(*SemanticError)
	if serr.JSONKind == '"' || serr.JSONKind == '0' {
		serr.JSONValue = jsontext.Value(export.Decoder(d).PreviousTokenOrValue()).Clone()
	}
	return serr
}

// newUnmarshalErrorAfterWithSkipping 类似 [newUnmarshalErrorAfter]，
// 但如果指定了 [jsonflags.ReportErrorsWithLegacySemantics]，
// 则会自动跳过当前值的剩余部分。
func newUnmarshalErrorAfterWithSkipping(d *jsontext.Decoder, t reflect.Type, err error) error {
	err = newUnmarshalErrorAfter(d, t, err)
	if export.Decoder(d).Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		if err2 := export.Decoder(d).SkipValueRemainder(); err2 != nil {
			return err2
		}
	}
	return err
}

// newSemanticErrorWithPosition 将 err 包装进一个 SemanticError，假定
// 错误发生在所提供的 depth 和 length 处。
// 如果 err 已经是一个 SemanticError，那么仅在位置信息
// 当前尚未填充时才会注入位置信息。
//
// 如果位置尚未填充，则错误在用户代码中发生的位置是模糊的，
// 无法确定它是在当前位置之前还是之后发生。
// 对于字节偏移，我们假定在解码时错误发生在最后读取的
// token 或值之前，而在编码时错误发生在下一个值之前。
// 对于 JSON 指针，我们指向父对象或数组，除非
// 我们能确定它发生在某个对象成员处。
//
// 这用于为用户的 v2 MarshalJSON 或 UnmarshalJSON
// 方法或函数返回的错误添加注释。
func newSemanticErrorWithPosition(c coder, t reflect.Type, prevDepth int, prevLength int64, err error) error {
	serr, _ := err.(*SemanticError)
	if serr == nil {
		serr = &SemanticError{Err: err}
	}
	serr.Err = toUnexpectedEOF(serr.Err)
	var currDepth int
	var currLength int64
	var coderState interface{ AppendStackPointer([]byte, int) []byte }
	var offset int64
	switch c := c.(type) {
	case *jsontext.Encoder:
		e := export.Encoder(c)
		serr.action = cmp.Or(serr.action, "marshal")
		currDepth, currLength = e.Tokens.DepthLength()
		offset = c.OutputOffset() + int64(export.Encoder(c).CountNextDelimWhitespace())
		coderState = e
	case *jsontext.Decoder:
		d := export.Decoder(c)
		serr.action = cmp.Or(serr.action, "unmarshal")
		currDepth, currLength = d.Tokens.DepthLength()
		tokOrVal := d.PreviousTokenOrValue()
		offset = c.InputOffset() - int64(len(tokOrVal))
		if (prevDepth == currDepth && prevLength == currLength) || len(tokOrVal) == 0 {
			// 如果在用户定义的方法中未调用 Read 方法，
			// 或调用了 Peek 方法，则使用下一个值的偏移量。
			offset = c.InputOffset() + int64(export.Decoder(c).CountNextDelimWhitespace())
		}
		coderState = d
	}
	serr.ByteOffset = cmp.Or(serr.ByteOffset, offset)
	if serr.JSONPointer == "" {
		where := 0 // 默认为模糊定位
		switch {
		case prevDepth == currDepth && prevLength+0 == currLength:
			where = +1
		case prevDepth == currDepth && prevLength+1 == currLength:
			where = -1
		}
		serr.JSONPointer = jsontext.Pointer(coderState.AppendStackPointer(nil, where))
	}
	serr.GoType = cmp.Or(serr.GoType, t)
	return serr
}

// collapseSemanticErrors 将外层出现的双重 SemanticError
// 折叠为单个 SemanticError，方法是保留内层错误，
// 但在 ByteOffset 和 JSONPointer 前面加上外层错误的值。
//
// 例如：
//
//	collapseSemanticErrors(&SemanticError{
//		ByteOffset:  len64(`[0,{"alpha":[0,1,`),
//		JSONPointer: "/1/alpha/2",
//		GoType:      reflect.TypeFor[outerType](),
//		Err: &SemanticError{
//			ByteOffset:  len64(`{"foo":"bar","fizz":[0,`),
//			JSONPointer: "/fizz/1",
//			GoType:      reflect.TypeFor[innerType](),
//			Err:         ...,
//		},
//	})
//
// 结果为：
//
//	&SemanticError{
//		ByteOffset:  len64(`[0,{"alpha":[0,1,`) + len64(`{"foo":"bar","fizz":[0,`),
//		JSONPointer: "/1/alpha/2" + "/fizz/1",
//		GoType:      reflect.TypeFor[innerType](),
//		Err:         ...,
//	}
//
// 这用于为用户的 v1 MarshalJSON 或 UnmarshalJSON 方法
// 返回的错误添加精确的位置信息，
// 如果它们本身恰好返回了 SemanticError。
// 由于 MarshalJSON 和 UnmarshalJSON 并非操作根 JSON 值，
// 它们的位置必须相对于 UnmarshalJSON 返回的
// 或传递给 MarshalJSON 的嵌套 JSON 值。
// 因此，我们可以通过将外层位置与内层位置
// 连接起来来构造绝对位置。
//
// 注意，对于接收 [jsontext.Encoder] 或 [jsontext.Decoder] 的用户函数，
// 我们不会使用 collapseSemanticErrors，因为它们包含
// 用于报告相对于根 JSON 值的位置的方法。
// 我们假定用户构造的错误在位置上足够精确。
func collapseSemanticErrors(err error) error {
	if serr1, ok := err.(*SemanticError); ok {
		if serr2, ok := serr1.Err.(*SemanticError); ok {
			serr2.ByteOffset = serr1.ByteOffset + serr2.ByteOffset
			serr2.JSONPointer = serr1.JSONPointer + serr2.JSONPointer
			*serr1 = *serr2
		}
	}
	return err
}

// errorModalVerb 是一个情态动词，如 "cannot" 或 "unable to"。
//
// 每个进程一次，通过刻意在同一个错误消息的等价表述之间
// 切换，来对错误消息进行 Hyrum 防护。
// 这种随机化与 Go 中已对 map 迭代应用的
// Hyrum 防护相关联。
var errorModalVerb = sync.OnceValue(func() string {
	for phrase := range map[string]struct{}{"cannot": {}, "unable to": {}} {
		return phrase // 使用第一次迭代中得到的任意一个短语
	}
	return ""
})

func (e *SemanticError) Error() string {
	var sb strings.Builder
	sb.WriteString(errorPrefix)
	sb.WriteString(errorModalVerb())

	// 格式化 action。
	var preposition string
	switch e.action {
	case "marshal":
		sb.WriteString(" marshal")
		preposition = " from"
	case "unmarshal":
		sb.WriteString(" unmarshal")
		preposition = " into"
	default:
		sb.WriteString(" handle")
		preposition = " with"
	}

	// 格式化 JSON 种类。
	switch e.JSONKind {
	case 'n':
		sb.WriteString(" JSON null")
	case 'f', 't':
		sb.WriteString(" JSON boolean")
	case '"':
		sb.WriteString(" JSON string")
	case '0':
		sb.WriteString(" JSON number")
	case '{', '}':
		sb.WriteString(" JSON object")
	case '[', ']':
		sb.WriteString(" JSON array")
	default:
		if e.action == "" {
			preposition = ""
		}
	}
	if len(e.JSONValue) > 0 && len(e.JSONValue) < 100 {
		sb.WriteByte(' ')
		sb.Write(e.JSONValue)
	}

	// 格式化 Go 类型。
	if e.GoType != nil {
		typeString := e.GoType.String()
		if len(typeString) > 100 {
			// 过长的类型字符串最有可能出现在
			// 声明了许多字段的匿名 struct 中。
			// 通过只打印种类来减少噪音，
			// 并在 struct 恰好包含未导出字段时
			// 可选地在前面加上包名。
			typeString = e.GoType.Kind().String()
			if e.GoType.Kind() == reflect.Struct && e.GoType.Name() == "" {
				for i := range e.GoType.NumField() {
					if pkgPath := e.GoType.Field(i).PkgPath; pkgPath != "" {
						typeString = pkgPath[strings.LastIndexByte(pkgPath, '/')+len("/"):] + ".struct"
						break
					}
				}
			}
		}
		sb.WriteString(preposition)
		sb.WriteString(" Go ")
		sb.WriteString(typeString)
	}

	// 对未知名称的特殊处理。
	if e.Err == ErrUnknownName {
		sb.WriteString(": ")
		sb.WriteString(ErrUnknownName.Error())
		sb.WriteString(" ")
		sb.WriteString(strconv.Quote(e.JSONPointer.LastToken()))
		if parent := e.JSONPointer.Parent(); parent != "" {
			sb.WriteString(" within ")
			sb.WriteString(strconv.Quote(jsonwire.TruncatePointer(string(parent), 100)))
		}
		return sb.String()
	}

	// 格式化位置。
	// 如果它与被包装的 SyntacticError 重叠，则避免打印。
	switch serr, _ := e.Err.(*jsontext.SyntacticError); {
	case e.JSONPointer != "":
		if serr == nil || !e.JSONPointer.Contains(serr.JSONPointer) {
			sb.WriteString(" within ")
			sb.WriteString(strconv.Quote(jsonwire.TruncatePointer(string(e.JSONPointer), 100)))
		}
	case e.ByteOffset > 0:
		if serr == nil || !(e.ByteOffset <= serr.ByteOffset) {
			sb.WriteString(" after offset ")
			sb.WriteString(strconv.FormatInt(e.ByteOffset, 10))
		}
	}

	// 格式化底层错误。
	if e.Err != nil {
		errString := e.Err.Error()
		if isSyntacticError(e.Err) {
			errString = strings.TrimPrefix(errString, "jsontext: ")
		}
		sb.WriteString(": ")
		sb.WriteString(errString)
	}

	return sb.String()
}

func (e *SemanticError) Unwrap() error {
	return e.Err
}

func newDuplicateNameError(ptr jsontext.Pointer, quotedName []byte, offset int64) error {
	if quotedName != nil {
		name, _ := jsonwire.AppendUnquote(nil, quotedName)
		ptr = ptr.AppendToken(string(name))
	}
	return &jsontext.SyntacticError{
		ByteOffset:  offset,
		JSONPointer: ptr,
		Err:         jsontext.ErrDuplicateName,
	}
}

// toUnexpectedEOF 将 [io.EOF] 转换为 [io.ErrUnexpectedEOF]。
func toUnexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
