// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"encoding"
	"errors"
	"io"
	"reflect"

	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
	"encoding/json/jsontext"
)

var errNonStringValue = errors.New("JSON value must be string type")

// 用于自定义序列化的接口。
var (
	jsonMarshalerType       = reflect.TypeFor[Marshaler]()
	jsonMarshalerToType     = reflect.TypeFor[MarshalerTo]()
	jsonUnmarshalerType     = reflect.TypeFor[Unmarshaler]()
	jsonUnmarshalerFromType = reflect.TypeFor[UnmarshalerFrom]()
	textAppenderType        = reflect.TypeFor[encoding.TextAppender]()
	textMarshalerType       = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshalerType     = reflect.TypeFor[encoding.TextUnmarshaler]()

	allMarshalerTypes   = []reflect.Type{jsonMarshalerToType, jsonMarshalerType, textAppenderType, textMarshalerType}
	allUnmarshalerTypes = []reflect.Type{jsonUnmarshalerFromType, jsonUnmarshalerType, textUnmarshalerType}
	allMethodTypes      = append(allMarshalerTypes, allUnmarshalerTypes...)
)

// Marshaler 由能够 marshal 自身的类型实现。
// 除非实现方试图避免对 "jsontext" 包的硬依赖，
// 否则建议类型实现 [MarshalerTo]。
//
// 建议实现方返回一个缓冲区，调用者可以安全地保留并可能修改它。
//
// 如果返回的错误是 [SemanticError]，那么
// 该错误中未填充的字段可能会被 [json] 用额外的上下文填充。
// 其他类型的错误会被包装在 [SemanticError] 中。
type Marshaler interface {
	MarshalJSON() ([]byte, error)
}

// MarshalerTo 由能够 marshal 自身的类型实现。
// 建议类型实现 MarshalerTo 而非 [Marshaler]，
// 因为这既更高效也更灵活。
// 如果一个类型同时实现了 Marshaler 和 MarshalerTo，
// 那么 MarshalerTo 优先。在这种情况下，两种实现
// 都应力求在默认 marshal 选项下具有等效的行为。
//
// 实现必须向 Encoder 写入恰好一个 JSON 值，
// 且不得保留指向 [jsontext.Encoder] 的指针。
//
// 如果返回的错误是 [SemanticError]，那么
// 该错误中未填充的字段可能会被 [json] 用额外的上下文填充。
// 其他类型的错误会被包装在 [SemanticError] 中，
// 除非它是 IO 错误。
type MarshalerTo interface {
	MarshalJSONTo(*jsontext.Encoder) error

	// TODO: 用户应该调用 MarshalEncode 函数，
	// 还是应该/能够直接调用此方法？这有关系吗？
}

// Unmarshaler 由能够 unmarshal 自身的类型实现。
// 除非实现方试图避免对 "jsontext" 包的硬依赖，
// 否则建议类型实现 [UnmarshalerFrom]。
//
// 如果从本包中的 unmarshal 功能调用，
// 可以假定输入是 JSON 值的有效编码。
// 如果 UnmarshalJSON 在返回后仍保留 JSON 数据，则必须复制它。
// 建议 UnmarshalJSON 在 unmarshal 到已预先填充的值时
// 实现合并语义。
//
// 实现不得保留或修改输入的 []byte。
//
// 如果返回的错误是 [SemanticError]，那么
// 该错误中未填充的字段可能会被 [json] 用额外的上下文填充。
// 其他类型的错误会被包装在 [SemanticError] 中。
type Unmarshaler interface {
	UnmarshalJSON([]byte) error
}

// UnmarshalerFrom 由能够 unmarshal 自身的类型实现。
// 建议类型实现 UnmarshalerFrom 而非 [Unmarshaler]，
// 因为这既更高效也更灵活。
// 如果一个类型同时实现了 Unmarshaler 和 UnmarshalerFrom，
// 那么 UnmarshalerFrom 优先。在这种情况下，两种实现
// 都应力求在默认 unmarshal 选项下具有等效的行为。
//
// 实现必须从 Decoder 读取恰好一个 JSON 值。
// 建议 UnmarshalJSONFrom 在 unmarshal 到已预先填充的值时
// 实现合并语义。
//
// 实现不得保留指向 [jsontext.Decoder] 的指针。
//
// 如果返回的错误是 [SemanticError]，那么
// 该错误中未填充的字段可能会被 [json] 用额外的上下文填充。
// 其他类型的错误会被包装在 [SemanticError] 中，
// 除非它是 [jsontext.SyntacticError] 或 IO 错误。
type UnmarshalerFrom interface {
	UnmarshalJSONFrom(*jsontext.Decoder) error

	// TODO: 用户应该调用 UnmarshalDecode 函数，
	// 还是应该/能够直接调用此方法？这有关系吗？
}

func makeMethodArshaler(fncs *arshaler, t reflect.Type) *arshaler {
	// 避免在指针或接口版本上注入方法 arshaler，
	// 以避免在 nil 指针或接口接收者上调用该方法。
	// 让它注入到值接收者上（值接收者总是可寻址的）。
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface {
		return fncs
	}

	if needAddr, ok := implements(t, textMarshalerType); ok {
		fncs.nonDefault = true
		prevMarshal := fncs.marshal
		fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
			if mo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) &&
				(needAddr && va.forcedAddr) {
				return prevMarshal(enc, va, mo)
			}
			marshaler, _ := reflect.TypeAssert[encoding.TextMarshaler](va.Addr())
			if err := export.Encoder(enc).AppendRaw('"', false, func(b []byte) ([]byte, error) {
				b2, err := marshaler.MarshalText()
				return append(b, b2...), err
			}); err != nil {
				err = wrapSkipFunc(err, "marshal method")
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "MarshalText") // 与 unmarshal 不同，总是被包装
				}
				if !isSemanticError(err) && !export.IsIOError(err) {
					err = newMarshalErrorBefore(enc, t, err)
				}
				return err
			}
			return nil
		}
	}

	if needAddr, ok := implements(t, textAppenderType); ok {
		fncs.nonDefault = true
		prevMarshal := fncs.marshal
		fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) (err error) {
			if mo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) &&
				(needAddr && va.forcedAddr) {
				return prevMarshal(enc, va, mo)
			}
			appender, _ := reflect.TypeAssert[encoding.TextAppender](va.Addr())
			if err := export.Encoder(enc).AppendRaw('"', false, appender.AppendText); err != nil {
				err = wrapSkipFunc(err, "append method")
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "AppendText") // 与 unmarshal 不同，总是被包装
				}
				if !isSemanticError(err) && !export.IsIOError(err) {
					err = newMarshalErrorBefore(enc, t, err)
				}
				return err
			}
			return nil
		}
	}

	if needAddr, ok := implements(t, jsonMarshalerType); ok {
		fncs.nonDefault = true
		prevMarshal := fncs.marshal
		fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
			if mo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) &&
				((needAddr && va.forcedAddr) || export.Encoder(enc).Tokens.Last.NeedObjectName()) {
				return prevMarshal(enc, va, mo)
			}
			marshaler, _ := reflect.TypeAssert[Marshaler](va.Addr())
			val, err := marshaler.MarshalJSON()
			if err != nil {
				err = wrapSkipFunc(err, "marshal method")
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "MarshalJSON") // 与 unmarshal 不同，总是被包装
				}
				err = newMarshalErrorBefore(enc, t, err)
				return collapseSemanticErrors(err)
			}
			if err := enc.WriteValue(val); err != nil {
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "MarshalJSON") // 与 unmarshal 不同，总是被包装
				}
				if isSyntacticError(err) {
					err = newMarshalErrorBefore(enc, t, err)
				}
				return err
			}
			return nil
		}
	}

	if needAddr, ok := implements(t, jsonMarshalerToType); ok {
		fncs.nonDefault = true
		prevMarshal := fncs.marshal
		fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
			if mo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) &&
				((needAddr && va.forcedAddr) || export.Encoder(enc).Tokens.Last.NeedObjectName()) {
				return prevMarshal(enc, va, mo)
			}
			xe := export.Encoder(enc)
			prevDepth, prevLength := xe.Tokens.DepthLength()
			xe.Flags.Set(jsonflags.WithinArshalCall | 1)
			marshaler, _ := reflect.TypeAssert[MarshalerTo](va.Addr())
			err := marshaler.MarshalJSONTo(enc)
			xe.Flags.Set(jsonflags.WithinArshalCall | 0)
			currDepth, currLength := xe.Tokens.DepthLength()
			if (prevDepth != currDepth || prevLength+1 != currLength) && err == nil {
				err = errNonSingularValue
			}
			if err != nil {
				err = wrapSkipFunc(err, "marshal method")
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "MarshalJSONTo") // 与 unmarshal 不同，总是被包装
				}
				if !export.IsIOError(err) {
					err = newSemanticErrorWithPosition(enc, t, prevDepth, prevLength, err)
				}
				return err
			}
			return nil
		}
	}

	if _, ok := implements(t, textUnmarshalerType); ok {
		fncs.nonDefault = true
		fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
			xd := export.Decoder(dec)
			var flags jsonwire.ValueFlags
			val, err := xd.ReadValue(&flags)
			if err != nil {
				return err // 必须是语法错误或 I/O 错误
			}
			if val.Kind() == 'n' {
				if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
					va.SetZero()
				}
				return nil
			}
			if val.Kind() != '"' {
				return newUnmarshalErrorAfter(dec, t, errNonStringValue)
			}
			s := jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
			unmarshaler, _ := reflect.TypeAssert[encoding.TextUnmarshaler](va.Addr())
			if err := unmarshaler.UnmarshalText(s); err != nil {
				err = wrapSkipFunc(err, "unmarshal method")
				if uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return err // 与 marshal 不同，从不被包装
				}
				if !isSemanticError(err) && !isSyntacticError(err) && !export.IsIOError(err) {
					err = newUnmarshalErrorAfter(dec, t, err)
				}
				return err
			}
			return nil
		}
	}

	if _, ok := implements(t, jsonUnmarshalerType); ok {
		fncs.nonDefault = true
		prevUnmarshal := fncs.unmarshal
		fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
			if uo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) &&
				export.Decoder(dec).Tokens.Last.NeedObjectName() {
				return prevUnmarshal(dec, va, uo)
			}
			val, err := dec.ReadValue()
			if err != nil {
				return err // 必须是语法错误或 I/O 错误
			}
			unmarshaler, _ := reflect.TypeAssert[Unmarshaler](va.Addr())
			if err := unmarshaler.UnmarshalJSON(val); err != nil {
				err = wrapSkipFunc(err, "unmarshal method")
				if uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return err // 与 marshal 不同，从不被包装
				}
				err = newUnmarshalErrorAfter(dec, t, err)
				return collapseSemanticErrors(err)
			}
			return nil
		}
	}

	if _, ok := implements(t, jsonUnmarshalerFromType); ok {
		fncs.nonDefault = true
		prevUnmarshal := fncs.unmarshal
		fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
			if uo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) &&
				export.Decoder(dec).Tokens.Last.NeedObjectName() {
				return prevUnmarshal(dec, va, uo)
			}
			xd := export.Decoder(dec)
			prevDepth, prevLength := xd.Tokens.DepthLength()
			if prevDepth == 1 && xd.AtEOF() {
				return io.EOF // 尽早检查 EOF，以避免 fn 报告 EOF
			}
			xd.Flags.Set(jsonflags.WithinArshalCall | 1)
			unmarshaler, _ := reflect.TypeAssert[UnmarshalerFrom](va.Addr())
			err := unmarshaler.UnmarshalJSONFrom(dec)
			xd.Flags.Set(jsonflags.WithinArshalCall | 0)
			currDepth, currLength := xd.Tokens.DepthLength()
			if (prevDepth != currDepth || prevLength+1 != currLength) && err == nil {
				err = errNonSingularValue
			}
			if err != nil {
				err = wrapSkipFunc(err, "unmarshal method")
				if uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					if err2 := xd.SkipUntil(prevDepth, prevLength+1); err2 != nil {
						return err2
					}
					return err // 与 marshal 不同，从不被包装
				}
				if !isSyntacticError(err) && !export.IsIOError(err) {
					err = newSemanticErrorWithPosition(dec, t, prevDepth, prevLength, err)
				}
				return err
			}
			return nil
		}
	}

	return fncs
}

// implementsAny 类似于针对一组接口的 t.Implements(ifaceType)，
// 但检查的是 t 或 reflect.PointerTo(t) 是否实现了该接口。
func implementsAny(t reflect.Type, ifaceTypes ...reflect.Type) bool {
	for _, ifaceType := range ifaceTypes {
		if _, ok := implements(t, ifaceType); ok {
			return true
		}
	}
	return false
}

// implements 类似于 t.Implements(ifaceType)，
// 但检查的是 t 或 reflect.PointerTo(t) 是否实现了该接口。
// 它还报告是否需要对该值取地址
// 才能满足该接口。
func implements(t, ifaceType reflect.Type) (needAddr, ok bool) {
	switch {
	case t.Implements(ifaceType):
		return false, true
	case reflect.PointerTo(t).Implements(ifaceType):
		return true, true
	default:
		return false, false
	}
}
