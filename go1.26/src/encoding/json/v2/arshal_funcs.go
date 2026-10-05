// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/jsontext"
)

// SkipFunc 可由 [MarshalToFunc] 和 [UnmarshalFromFunc] 函数返回。
//
// 任何返回 SkipFunc 的函数都不得对所提供的 [jsontext.Encoder] 或
// [jsontext.Decoder] 产生可观察的副作用。
// 例如，允许调用 [jsontext.Decoder.PeekKind]，
// 但不允许调用 [jsontext.Decoder.ReadToken] 或
// [jsontext.Encoder.WriteToken]，因为这些方法会改变状态。
var SkipFunc = errors.New("json: skip function")

var errSkipMutation = errors.New("must not read or write any tokens when skipping")
var errNonSingularValue = errors.New("must read or write exactly one value")

// Marshalers 是可能覆盖特定类型的 marshal 行为的函数列表。
// 通过填充 [WithMarshalers] 以便与
// [Marshal]、[MarshalWrite] 或 [MarshalEncode] 一起使用。
// 为 nil 的 *Marshalers 等价于空列表。
// Marshalers 上没有导出的字段或方法。
type Marshalers = typedMarshalers

// JoinMarshalers 构造一个扁平化的 marshal 函数列表。
// 如果列表中有多个函数适用于给定类型的值，
// 那么列表中靠前的函数优先于靠后的函数。
// 如果某个函数返回 [SkipFunc]，则调用下一个适用的函数，
// 否则使用默认的 marshal 行为。
//
// 例如：
//
//	m1 := JoinMarshalers(f1, f2)
//	m2 := JoinMarshalers(f0, m1, f3)     // equivalent to m3
//	m3 := JoinMarshalers(f0, f1, f2, f3) // equivalent to m2
func JoinMarshalers(ms ...*Marshalers) *Marshalers {
	return newMarshalers(ms...)
}

// Unmarshalers 是可能覆盖特定类型的 unmarshal 行为的函数列表。
// 通过填充 [WithUnmarshalers] 以便与
// [Unmarshal]、[UnmarshalRead] 或 [UnmarshalDecode] 一起使用。
// 为 nil 的 *Unmarshalers 等价于空列表。
// Unmarshalers 上没有导出的字段或方法。
type Unmarshalers = typedUnmarshalers

// JoinUnmarshalers 构造一个扁平化的 unmarshal 函数列表。
// 如果列表中有多个函数适用于给定类型的值，
// 那么列表中靠前的函数优先于靠后的函数。
// 如果某个函数返回 [SkipFunc]，则调用下一个适用的函数，
// 否则使用默认的 unmarshal 行为。
//
// 例如：
//
//	u1 := JoinUnmarshalers(f1, f2)
//	u2 := JoinUnmarshalers(f0, u1, f3)     // equivalent to u3
//	u3 := JoinUnmarshalers(f0, f1, f2, f3) // equivalent to u2
func JoinUnmarshalers(us ...*Unmarshalers) *Unmarshalers {
	return newUnmarshalers(us...)
}

type typedMarshalers = typedArshalers[jsontext.Encoder]
type typedUnmarshalers = typedArshalers[jsontext.Decoder]
type typedArshalers[Coder any] struct {
	nonComparable

	fncVals  []typedArshaler[Coder]
	fncCache sync.Map // map[reflect.Type]arshaler

	// fromAny 报告用于表示任意 JSON 的任一 Go 类型
	// （即 any、bool、string、float64、map[string]any 或 []any）是否
	// 匹配所提供的任一类型专用的 arshaler。
	//
	// arshal_default.go 需要这一信息来确定
	// 是否使用 arshal_any.go 中的专用逻辑来处理
	// any 接口类型。arshal_any.go 中的逻辑不支持
	// 类型专用的 arshal 函数，因此如果此值为 true，
	// 我们就必须避免使用该逻辑。
	fromAny bool
}
type typedMarshaler = typedArshaler[jsontext.Encoder]
type typedUnmarshaler = typedArshaler[jsontext.Decoder]
type typedArshaler[Coder any] struct {
	typ     reflect.Type
	fnc     func(*Coder, addressableValue, *jsonopts.Struct) error
	maySkip bool
}

func newMarshalers(ms ...*Marshalers) *Marshalers       { return newTypedArshalers(ms...) }
func newUnmarshalers(us ...*Unmarshalers) *Unmarshalers { return newTypedArshalers(us...) }
func newTypedArshalers[Coder any](as ...*typedArshalers[Coder]) *typedArshalers[Coder] {
	var a typedArshalers[Coder]
	for _, a2 := range as {
		if a2 != nil {
			a.fncVals = append(a.fncVals, a2.fncVals...)
			a.fromAny = a.fromAny || a2.fromAny
		}
	}
	if len(a.fncVals) == 0 {
		return nil
	}
	return &a
}

func (a *typedArshalers[Coder]) lookup(fnc func(*Coder, addressableValue, *jsonopts.Struct) error, t reflect.Type) (func(*Coder, addressableValue, *jsonopts.Struct) error, bool) {
	if a == nil {
		return fnc, false
	}
	if v, ok := a.fncCache.Load(t); ok {
		if v == nil {
			return fnc, false
		}
		return v.(func(*Coder, addressableValue, *jsonopts.Struct) error), true
	}

	// 收集可为此类型调用的 arshaler 列表。
	// 此列表可能长于 1，因为某些 arshaler 可以被跳过。
	var fncs []func(*Coder, addressableValue, *jsonopts.Struct) error
	for _, fncVal := range a.fncVals {
		if !castableTo(t, fncVal.typ) {
			continue
		}
		fncs = append(fncs, fncVal.fnc)
		if !fncVal.maySkip {
			break // 后续的 arshaler 将永远不会被调用
		}
	}

	if len(fncs) == 0 {
		a.fncCache.Store(t, nil) // 用 nil 表示没有找到任何函数
		return fnc, false
	}

	// 构造一个可以调用每个适用 arshaler 的 arshaler。
	fncDefault := fnc
	fnc = func(c *Coder, v addressableValue, o *jsonopts.Struct) error {
		for _, fnc := range fncs {
			if err := fnc(c, v, o); err != SkipFunc {
				return err // 可以为 nil 或非 nil
			}
		}
		return fncDefault(c, v, o)
	}

	// 使用最先存储的那个，以便重复的工作可以被垃圾回收。
	v, _ := a.fncCache.LoadOrStore(t, fnc)
	return v.(func(*Coder, addressableValue, *jsonopts.Struct) error), true
}

// MarshalFunc 构造一个类型专用的 marshaler，
// 它指定如何 marshal 类型 T 的值。
// T 可以是除命名指针之外的任意类型。
// 如果 T 是接口或指针类型，函数总是会被提供一个非 nil 的指针值。
//
// 该函数必须恰好 marshal 一个 JSON 值。
// 不得在函数调用之外保留 T 的值。
// 它不得返回 [SkipFunc]。
func MarshalFunc[T any](fn func(T) ([]byte, error)) *Marshalers {
	t := reflect.TypeFor[T]()
	assertCastableTo(t, true)
	typFnc := typedMarshaler{
		typ: t,
		fnc: func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
			v, _ := reflect.TypeAssert[T](va.castTo(t))
			val, err := fn(v)
			if err != nil {
				err = wrapSkipFunc(err, "marshal function of type func(T) ([]byte, error)")
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "MarshalFunc") // 与 unmarshal 不同，总是被包装
				}
				err = newMarshalErrorBefore(enc, t, err)
				return collapseSemanticErrors(err)
			}
			if err := enc.WriteValue(val); err != nil {
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "MarshalFunc") // 与 unmarshal 不同，总是被包装
				}
				if isSyntacticError(err) {
					err = newMarshalErrorBefore(enc, t, err)
				}
				return err
			}
			return nil
		},
	}
	return &Marshalers{fncVals: []typedMarshaler{typFnc}, fromAny: castableToFromAny(t)}
}

// MarshalToFunc 构造一个类型专用的 marshaler，
// 它指定如何 marshal 类型 T 的值。
// T 可以是除命名指针之外的任意类型。
// 如果 T 是接口或指针类型，函数总是会被提供一个非 nil 的指针值。
//
// 该函数必须通过对所提供的 encoder 调用写入方法
// 恰好 marshal 一个 JSON 值。它可以返回 [SkipFunc]，
// 以便 marshal 可以继续移动到下一个 marshal 函数。
// 然而，如果返回了 [SkipFunc]，则不得对 encoder
// 调用任何会改变状态的方法。
// 不得在函数调用之外保留指向 [jsontext.Encoder] 的指针
// 和 T 的值。
func MarshalToFunc[T any](fn func(*jsontext.Encoder, T) error) *Marshalers {
	t := reflect.TypeFor[T]()
	assertCastableTo(t, true)
	typFnc := typedMarshaler{
		typ: t,
		fnc: func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
			xe := export.Encoder(enc)
			prevDepth, prevLength := xe.Tokens.DepthLength()
			xe.Flags.Set(jsonflags.WithinArshalCall | 1)
			v, _ := reflect.TypeAssert[T](va.castTo(t))
			err := fn(enc, v)
			xe.Flags.Set(jsonflags.WithinArshalCall | 0)
			currDepth, currLength := xe.Tokens.DepthLength()
			if err == nil && (prevDepth != currDepth || prevLength+1 != currLength) {
				err = errNonSingularValue
			}
			if err != nil {
				if err == SkipFunc {
					if prevDepth == currDepth && prevLength == currLength {
						return SkipFunc
					}
					err = errSkipMutation
				}
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "MarshalToFunc") // 与 unmarshal 不同，总是被包装
				}
				if !export.IsIOError(err) {
					err = newSemanticErrorWithPosition(enc, t, prevDepth, prevLength, err)
				}
				return err
			}
			return nil
		},
		maySkip: true,
	}
	return &Marshalers{fncVals: []typedMarshaler{typFnc}, fromAny: castableToFromAny(t)}
}

// UnmarshalFunc 构造一个类型专用的 unmarshaler，
// 它指定如何 unmarshal 类型 T 的值。
// T 必须是未命名指针或接口类型。
// 函数总是会被提供一个非 nil 的指针值。
//
// 该函数必须恰好 unmarshal 一个 JSON 值。
// 不得修改输入的 []byte。
// 不得在函数调用之外保留输入的 []byte 和 T 的值。
// 它不得返回 [SkipFunc]。
func UnmarshalFunc[T any](fn func([]byte, T) error) *Unmarshalers {
	t := reflect.TypeFor[T]()
	assertCastableTo(t, false)
	typFnc := typedUnmarshaler{
		typ: t,
		fnc: func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
			val, err := dec.ReadValue()
			if err != nil {
				return err // 必定是语法错误或 I/O 错误
			}
			v, _ := reflect.TypeAssert[T](va.castTo(t))
			err = fn(val, v)
			if err != nil {
				err = wrapSkipFunc(err, "unmarshal function of type func([]byte, T) error")
				if uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return err // 与 marshal 不同，从不被包装
				}
				err = newUnmarshalErrorAfter(dec, t, err)
				return collapseSemanticErrors(err)
			}
			return nil
		},
	}
	return &Unmarshalers{fncVals: []typedUnmarshaler{typFnc}, fromAny: castableToFromAny(t)}
}

// UnmarshalFromFunc 构造一个类型专用的 unmarshaler，
// 它指定如何 unmarshal 类型 T 的值。
// T 必须是未命名指针或接口类型。
// 函数总是会被提供一个非 nil 的指针值。
//
// 该函数必须通过对所提供的 decoder 调用读取方法
// 恰好 unmarshal 一个 JSON 值。它可以返回 [SkipFunc]，
// 以便 unmarshal 可以继续移动到下一个 unmarshal 函数。
// 然而，如果返回了 [SkipFunc]，则不得对 decoder
// 调用任何会改变状态的方法。
// 不得在函数调用之外保留指向 [jsontext.Decoder] 的指针
// 和 T 的值。
func UnmarshalFromFunc[T any](fn func(*jsontext.Decoder, T) error) *Unmarshalers {
	t := reflect.TypeFor[T]()
	assertCastableTo(t, false)
	typFnc := typedUnmarshaler{
		typ: t,
		fnc: func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
			xd := export.Decoder(dec)
			prevDepth, prevLength := xd.Tokens.DepthLength()
			if prevDepth == 1 && xd.AtEOF() {
				return io.EOF // check EOF early to avoid fn reporting an EOF
			}
			xd.Flags.Set(jsonflags.WithinArshalCall | 1)
			v, _ := reflect.TypeAssert[T](va.castTo(t))
			err := fn(dec, v)
			xd.Flags.Set(jsonflags.WithinArshalCall | 0)
			currDepth, currLength := xd.Tokens.DepthLength()
			if err == nil && (prevDepth != currDepth || prevLength+1 != currLength) {
				err = errNonSingularValue
			}
			if err != nil {
				if err == SkipFunc {
					if prevDepth == currDepth && prevLength == currLength {
						return SkipFunc
					}
					err = errSkipMutation
				}
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
		},
		maySkip: true,
	}
	return &Unmarshalers{fncVals: []typedUnmarshaler{typFnc}, fromAny: castableToFromAny(t)}
}

// assertCastableTo 断言 "to" 是可被转换到的有效类型。
// 这些是类型专用的 arshaler 可以操作的 Go 类型。
//
// 令 AllTypes 为所有可能 Go 类型的全集。
// 此函数通常断言：
//
//	len([from for from in AllTypes if castableTo(from, to)]) > 0
//
// 否则它会 panic。
//
// 作为特例，如果 marshal 为 false，那么我们禁止任何
// 非指针或非接口类型，因为在尝试 unmarshal 到
// 最终用户调用者未传入可寻址值的地方时，
// 这几乎总是 bug，因为他们将观察不到这些变更。
func assertCastableTo(to reflect.Type, marshal bool) {
	switch to.Kind() {
	case reflect.Interface:
		return
	case reflect.Pointer:
		// 只允许未命名指针，以与以下事实保持一致：
		// 取某个值的地址会产生未命名指针类型。
		if to.Name() == "" {
			return
		}
	default:
		// 从技术上讲，非指针类型对于 unmarshal 是允许的。
		// 然而，它们通常是 bug，因为接收者将是不可变的。
		// 因此，只允许它们用于 marshaling。
		if marshal {
			return
		}
	}
	if marshal {
		panic(fmt.Sprintf("input type %v must be an interface type, an unnamed pointer type, or a non-pointer type", to))
	} else {
		panic(fmt.Sprintf("input type %v must be an interface type or an unnamed pointer type", to))
	}
}

// castableTo 检查类型 "from" 的值是否能转换为类型 "to"。
// 为 nil 的指针或接口 "from" 值永远不会被视为可转换。
//
// 此函数必须与 addressableValue.castTo 保持同步。
func castableTo(from, to reflect.Type) bool {
	switch to.Kind() {
	case reflect.Interface:
		// TODO: 当普通接口可以拥有类型集时，这会失效，
		// 因为现在存在这样的接口：只有类型 (T) 的值形式
		// 实现了该接口，而指针变体 (*T) 没有。
		// 参见 https://go.dev/issue/45346。
		return reflect.PointerTo(from).Implements(to)
	case reflect.Pointer:
		// unmarshaling 的常见情况。
		// From 必须是具体类型或接口类型。
		return reflect.PointerTo(from) == to
	default:
		// marshaling 的常见情况。
		// From 必须是具体类型。
		return from == to
	}
}

// castTo 将 va 转换为指定类型。
// 如果类型是接口，那么底层类型将始终是
// 指向具体类型的非 nil 指针。
//
// 要求：castableTo(va.Type(), to) 必须成立。
func (va addressableValue) castTo(to reflect.Type) reflect.Value {
	switch to.Kind() {
	case reflect.Interface:
		return va.Addr().Convert(to)
	case reflect.Pointer:
		return va.Addr()
	default:
		return va.Value
	}
}

// castableToFromAny 报告 "to" 是否能从用于表示
// 任意 JSON 的任一动态类型转换而来。
func castableToFromAny(to reflect.Type) bool {
	for _, from := range []reflect.Type{anyType, boolType, stringType, float64Type, mapStringAnyType, sliceAnyType} {
		if castableTo(from, to) {
			return true
		}
	}
	return false
}

func wrapSkipFunc(err error, what string) error {
	if err == SkipFunc {
		return errors.New(what + " cannot be skipped")
	}
	return err
}
