// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
	"encoding/json/jsontext"
)

// optimizeCommon 指定是否使用针对某些常见模式优化的逻辑，
// 而非使用较慢但更通用的逻辑。
// 无论此常量是否为 true，所有测试都应通过。
const optimizeCommon = true

var (
	// 与每种 JSON 类型对应的最自然的 Go 类型
	anyType          = reflect.TypeFor[any]()            // JSON 值
	boolType         = reflect.TypeFor[bool]()           // JSON 布尔值
	stringType       = reflect.TypeFor[string]()         // JSON 字符串
	float64Type      = reflect.TypeFor[float64]()        // JSON 数字
	mapStringAnyType = reflect.TypeFor[map[string]any]() // JSON 对象
	sliceAnyType     = reflect.TypeFor[[]any]()          // JSON 数组

	bytesType       = reflect.TypeFor[[]byte]()
	emptyStructType = reflect.TypeFor[struct{}]()
)

const startDetectingCyclesAfter = 1000

type seenPointers = map[any]struct{}

type typedPointer struct {
	typ reflect.Type
	ptr any // 始终存储 unsafe.Pointer，但避免依赖 unsafe
	len int // 记住切片长度以避免误报
}

// visitPointer 访问类型为 t 的指针 p，如果之前已经访问过则报告错误。
// 如果访问成功，则调用方最终必须调用 leave。
func visitPointer(m *seenPointers, v reflect.Value) error {
	p := typedPointer{v.Type(), v.UnsafePointer(), sliceLen(v)}
	if _, ok := (*m)[p]; ok {
		return internal.ErrCycle
	}
	if *m == nil {
		*m = make(seenPointers)
	}
	(*m)[p] = struct{}{}
	return nil
}
func leavePointer(m *seenPointers, v reflect.Value) {
	p := typedPointer{v.Type(), v.UnsafePointer(), sliceLen(v)}
	delete(*m, p)
}

func sliceLen(v reflect.Value) int {
	if v.Kind() == reflect.Slice {
		return v.Len()
	}
	return 0
}

func len64[Bytes ~[]byte | ~string](in Bytes) int64 {
	return int64(len(in))
}

func makeDefaultArshaler(t reflect.Type) *arshaler {
	switch t.Kind() {
	case reflect.Bool:
		return makeBoolArshaler(t)
	case reflect.String:
		return makeStringArshaler(t)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return makeIntArshaler(t)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return makeUintArshaler(t)
	case reflect.Float32, reflect.Float64:
		return makeFloatArshaler(t)
	case reflect.Map:
		return makeMapArshaler(t)
	case reflect.Struct:
		return makeStructArshaler(t)
	case reflect.Slice:
		fncs := makeSliceArshaler(t)
		if t.Elem().Kind() == reflect.Uint8 {
			return makeBytesArshaler(t, fncs)
		}
		return fncs
	case reflect.Array:
		fncs := makeArrayArshaler(t)
		if t.Elem().Kind() == reflect.Uint8 {
			return makeBytesArshaler(t, fncs)
		}
		return fncs
	case reflect.Pointer:
		return makePointerArshaler(t)
	case reflect.Interface:
		return makeInterfaceArshaler(t)
	default:
		return makeInvalidArshaler(t)
	}
}

func makeBoolArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		xe := export.Encoder(enc)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			return newInvalidFormatError(enc, t)
		}

		// 针对没有前置空白的情况优化序列化。
		if optimizeCommon && !mo.Flags.Get(jsonflags.AnyWhitespace|jsonflags.StringifyBoolsAndStrings) && !xe.Tokens.Last.NeedObjectName() {
			xe.Buf = strconv.AppendBool(xe.Tokens.MayAppendDelim(xe.Buf, 't'), va.Bool())
			xe.Tokens.Last.Increment()
			if xe.NeedFlush() {
				return xe.Flush()
			}
			return nil
		}

		if mo.Flags.Get(jsonflags.StringifyBoolsAndStrings) {
			if va.Bool() {
				return enc.WriteToken(jsontext.String("true"))
			} else {
				return enc.WriteToken(jsontext.String("false"))
			}
		}
		return enc.WriteToken(jsontext.Bool(va.Bool()))
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			return newInvalidFormatError(dec, t)
		}
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		k := tok.Kind()
		switch k {
		case 'n':
			if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
				va.SetBool(false)
			}
			return nil
		case 't', 'f':
			if !uo.Flags.Get(jsonflags.StringifyBoolsAndStrings) {
				va.SetBool(tok.Bool())
				return nil
			}
		case '"':
			if uo.Flags.Get(jsonflags.StringifyBoolsAndStrings) {
				switch tok.String() {
				case "true":
					va.SetBool(true)
				case "false":
					va.SetBool(false)
				default:
					if uo.Flags.Get(jsonflags.StringifyWithLegacySemantics) && tok.String() == "null" {
						if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
							va.SetBool(false)
						}
						return nil
					}
					return newUnmarshalErrorAfterWithValue(dec, t, strconv.ErrSyntax)
				}
				return nil
			}
		}
		return newUnmarshalErrorAfterWithSkipping(dec, t, nil)
	}
	return &fncs
}

func makeStringArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		xe := export.Encoder(enc)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			return newInvalidFormatError(enc, t)
		}

		// 针对没有前置空白的情况优化序列化。
		s := va.String()
		if optimizeCommon && !mo.Flags.Get(jsonflags.AnyWhitespace|jsonflags.StringifyBoolsAndStrings) && !xe.Tokens.Last.NeedObjectName() {
			b := xe.Buf
			b = xe.Tokens.MayAppendDelim(b, '"')
			b, err := jsonwire.AppendQuote(b, s, &mo.Flags)
			if err == nil {
				xe.Buf = b
				xe.Tokens.Last.Increment()
				if xe.NeedFlush() {
					return xe.Flush()
				}
				return nil
			}
			// 否则，该字符串包含无效的 UTF-8，
			// 因此让下面的逻辑构造适当的错误。
		}

		if mo.Flags.Get(jsonflags.StringifyBoolsAndStrings) {
			b, err := jsonwire.AppendQuote(nil, s, &mo.Flags)
			if err != nil {
				return newMarshalErrorBefore(enc, t, &jsontext.SyntacticError{Err: err})
			}
			q, err := jsontext.AppendQuote(nil, b)
			if err != nil {
				panic("BUG: second AppendQuote should never fail: " + err.Error())
			}
			return enc.WriteValue(q)
		}
		return enc.WriteToken(jsontext.String(s))
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			return newInvalidFormatError(dec, t)
		}
		var flags jsonwire.ValueFlags
		val, err := xd.ReadValue(&flags)
		if err != nil {
			return err
		}
		k := val.Kind()
		switch k {
		case 'n':
			if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
				va.SetString("")
			}
			return nil
		case '"':
			val = jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
			if uo.Flags.Get(jsonflags.StringifyBoolsAndStrings) {
				val, err = jsontext.AppendUnquote(nil, val)
				if err != nil {
					return newUnmarshalErrorAfter(dec, t, err)
				}
				if uo.Flags.Get(jsonflags.StringifyWithLegacySemantics) && string(val) == "null" {
					if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
						va.SetString("")
					}
					return nil
				}
			}
			if xd.StringCache == nil {
				xd.StringCache = new(stringCache)
			}
			str := makeString(xd.StringCache, val)
			va.SetString(str)
			return nil
		}
		return newUnmarshalErrorAfter(dec, t, nil)
	}
	return &fncs
}

var (
	appendEncodeBase16    = hex.AppendEncode
	appendEncodeBase32    = base32.StdEncoding.AppendEncode
	appendEncodeBase32Hex = base32.HexEncoding.AppendEncode
	appendEncodeBase64    = base64.StdEncoding.AppendEncode
	appendEncodeBase64URL = base64.URLEncoding.AppendEncode
	encodedLenBase16      = hex.EncodedLen
	encodedLenBase32      = base32.StdEncoding.EncodedLen
	encodedLenBase32Hex   = base32.HexEncoding.EncodedLen
	encodedLenBase64      = base64.StdEncoding.EncodedLen
	encodedLenBase64URL   = base64.URLEncoding.EncodedLen
	appendDecodeBase16    = hex.AppendDecode
	appendDecodeBase32    = base32.StdEncoding.AppendDecode
	appendDecodeBase32Hex = base32.HexEncoding.AppendDecode
	appendDecodeBase64    = base64.StdEncoding.AppendDecode
	appendDecodeBase64URL = base64.URLEncoding.AppendDecode
)

func makeBytesArshaler(t reflect.Type, fncs *arshaler) *arshaler {
	// 注意：这同时处理 []~byte 和 [N]~byte。
	// v2 的默认行为是将 []namedByte 视为等价于 []T，
	// 因为能够将 []namedByte 转换为 []byte 依赖
	// 可疑的 Go 反射行为（参见 https://go.dev/issue/24746）。
	// 为了模拟 v1，我们使用 jsonflags.FormatBytesWithLegacySemantics
	// 强制将 []namedByte 视为 []byte。
	marshalArray := fncs.marshal
	isNamedByte := t.Elem().PkgPath() != ""
	hasMarshaler := implementsAny(t.Elem(), allMarshalerTypes...)
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		if !mo.Flags.Get(jsonflags.FormatBytesWithLegacySemantics) && isNamedByte {
			return marshalArray(enc, va, mo) // 视为 []T 或 [N]T
		}
		xe := export.Encoder(enc)
		appendEncode := appendEncodeBase64
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			switch mo.Format {
			case "base64":
				appendEncode = appendEncodeBase64
			case "base64url":
				appendEncode = appendEncodeBase64URL
			case "base32":
				appendEncode = appendEncodeBase32
			case "base32hex":
				appendEncode = appendEncodeBase32Hex
			case "base16", "hex":
				appendEncode = appendEncodeBase16
			case "array":
				mo.Format = ""
				return marshalArray(enc, va, mo)
			default:
				return newInvalidFormatError(enc, t)
			}
		} else if mo.Flags.Get(jsonflags.FormatByteArrayAsArray) && va.Kind() == reflect.Array {
			return marshalArray(enc, va, mo)
		} else if mo.Flags.Get(jsonflags.FormatBytesWithLegacySemantics) && hasMarshaler {
			return marshalArray(enc, va, mo)
		}
		if mo.Flags.Get(jsonflags.FormatNilSliceAsNull) && va.Kind() == reflect.Slice && va.IsNil() {
			// TODO: 提供 "emitempty" 格式覆盖？
			return enc.WriteToken(jsontext.Null)
		}
		return xe.AppendRaw('"', true, func(b []byte) ([]byte, error) {
			return appendEncode(b, va.Bytes()), nil
		})
	}
	unmarshalArray := fncs.unmarshal
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		if !uo.Flags.Get(jsonflags.FormatBytesWithLegacySemantics) && isNamedByte {
			return unmarshalArray(dec, va, uo) // 视为 []T 或 [N]T
		}
		xd := export.Decoder(dec)
		appendDecode, encodedLen := appendDecodeBase64, encodedLenBase64
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			switch uo.Format {
			case "base64":
				appendDecode, encodedLen = appendDecodeBase64, encodedLenBase64
			case "base64url":
				appendDecode, encodedLen = appendDecodeBase64URL, encodedLenBase64URL
			case "base32":
				appendDecode, encodedLen = appendDecodeBase32, encodedLenBase32
			case "base32hex":
				appendDecode, encodedLen = appendDecodeBase32Hex, encodedLenBase32Hex
			case "base16", "hex":
				appendDecode, encodedLen = appendDecodeBase16, encodedLenBase16
			case "array":
				uo.Format = ""
				return unmarshalArray(dec, va, uo)
			default:
				return newInvalidFormatError(dec, t)
			}
		} else if uo.Flags.Get(jsonflags.FormatByteArrayAsArray) && va.Kind() == reflect.Array {
			return unmarshalArray(dec, va, uo)
		} else if uo.Flags.Get(jsonflags.FormatBytesWithLegacySemantics) && dec.PeekKind() == '[' {
			return unmarshalArray(dec, va, uo)
		}
		var flags jsonwire.ValueFlags
		val, err := xd.ReadValue(&flags)
		if err != nil {
			return err
		}
		k := val.Kind()
		switch k {
		case 'n':
			if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) || va.Kind() != reflect.Array {
				va.SetZero()
			}
			return nil
		case '"':
			// 注意：v2 的默认行为是严格遵循 RFC 4648。
			// 第 3.2 节规定填充是必需的。
			// 第 3.3 节规定必须拒绝非字母表字符
			// （例如 '\r' 或 '\n'）。
			// 第 3.5 节规定最后一个量子中不必要的非零位
			// 可以被拒绝。由于这是可选的，
			// 我们不拒绝此类输入。
			val = jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
			b, err := appendDecode(va.Bytes()[:0], val)
			if err != nil {
				return newUnmarshalErrorAfter(dec, t, err)
			}
			if len(val) != encodedLen(len(b)) && !uo.Flags.Get(jsonflags.ParseBytesWithLooseRFC4648) {
				// TODO(https://go.dev/issue/53845): RFC 4648 第 3.3 节
				// 规定必须拒绝非字母表字符。
				// 遗憾的是，"base32" 和 "base64" 包默认允许
				// '\r' 和 '\n' 字符。
				i := bytes.IndexAny(val, "\r\n")
				err := fmt.Errorf("illegal character %s at offset %d", jsonwire.QuoteRune(val[i:]), i)
				return newUnmarshalErrorAfter(dec, t, err)
			}

			if va.Kind() == reflect.Array {
				dst := va.Bytes()
				clear(dst[copy(dst, b):]) // 若 len(b) <= len(dst) 则为空操作
				if len(b) != len(dst) && !uo.Flags.Get(jsonflags.UnmarshalArrayFromAnyLength) {
					err := fmt.Errorf("decoded length of %d mismatches array length of %d", len(b), len(dst))
					return newUnmarshalErrorAfter(dec, t, err)
				}
			} else {
				if b == nil {
					b = []byte{}
				}
				va.SetBytes(b)
			}
			return nil
		}
		return newUnmarshalErrorAfter(dec, t, nil)
	}
	return fncs
}

func makeIntArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	bits := t.Bits()
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		xe := export.Encoder(enc)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			return newInvalidFormatError(enc, t)
		}

		// 针对没有前置空白或字符串转义的情况优化序列化。
		if optimizeCommon && !mo.Flags.Get(jsonflags.AnyWhitespace|jsonflags.StringifyNumbers) && !xe.Tokens.Last.NeedObjectName() {
			xe.Buf = strconv.AppendInt(xe.Tokens.MayAppendDelim(xe.Buf, '0'), va.Int(), 10)
			xe.Tokens.Last.Increment()
			if xe.NeedFlush() {
				return xe.Flush()
			}
			return nil
		}

		k := stringOrNumberKind(xe.Tokens.Last.NeedObjectName() || mo.Flags.Get(jsonflags.StringifyNumbers))
		return xe.AppendRaw(k, true, func(b []byte) ([]byte, error) {
			return strconv.AppendInt(b, va.Int(), 10), nil
		})
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			return newInvalidFormatError(dec, t)
		}
		stringify := xd.Tokens.Last.NeedObjectName() || uo.Flags.Get(jsonflags.StringifyNumbers)
		var flags jsonwire.ValueFlags
		val, err := xd.ReadValue(&flags)
		if err != nil {
			return err
		}
		k := val.Kind()
		switch k {
		case 'n':
			if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
				va.SetInt(0)
			}
			return nil
		case '"':
			if !stringify {
				break
			}
			val = jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
			if uo.Flags.Get(jsonflags.StringifyWithLegacySemantics) {
				// 出于历史原因，v1 按照 Go 语法解析带引号的数字，
				// 并允许带引号的 null。
				// 参见 https://go.dev/issue/75619
				n, err := strconv.ParseInt(string(val), 10, bits)
				if err != nil {
					if string(val) == "null" {
						if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
							va.SetInt(0)
						}
						return nil
					}
					return newUnmarshalErrorAfterWithValue(dec, t, errors.Unwrap(err))
				}
				va.SetInt(n)
				return nil
			}
			fallthrough
		case '0':
			if stringify && k == '0' {
				break
			}
			var negOffset int
			neg := len(val) > 0 && val[0] == '-'
			if neg {
				negOffset = 1
			}
			n, ok := jsonwire.ParseUint(val[negOffset:])
			maxInt := uint64(1) << (bits - 1)
			overflow := (neg && n > maxInt) || (!neg && n > maxInt-1)
			if !ok {
				if n != math.MaxUint64 {
					return newUnmarshalErrorAfterWithValue(dec, t, strconv.ErrSyntax)
				}
				overflow = true
			}
			if overflow {
				return newUnmarshalErrorAfterWithValue(dec, t, strconv.ErrRange)
			}
			if neg {
				va.SetInt(int64(-n))
			} else {
				va.SetInt(int64(+n))
			}
			return nil
		}
		return newUnmarshalErrorAfter(dec, t, nil)
	}
	return &fncs
}

func makeUintArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	bits := t.Bits()
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		xe := export.Encoder(enc)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			return newInvalidFormatError(enc, t)
		}

		// 针对没有前置空白或字符串转义的情况优化序列化。
		if optimizeCommon && !mo.Flags.Get(jsonflags.AnyWhitespace|jsonflags.StringifyNumbers) && !xe.Tokens.Last.NeedObjectName() {
			xe.Buf = strconv.AppendUint(xe.Tokens.MayAppendDelim(xe.Buf, '0'), va.Uint(), 10)
			xe.Tokens.Last.Increment()
			if xe.NeedFlush() {
				return xe.Flush()
			}
			return nil
		}

		k := stringOrNumberKind(xe.Tokens.Last.NeedObjectName() || mo.Flags.Get(jsonflags.StringifyNumbers))
		return xe.AppendRaw(k, true, func(b []byte) ([]byte, error) {
			return strconv.AppendUint(b, va.Uint(), 10), nil
		})
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			return newInvalidFormatError(dec, t)
		}
		stringify := xd.Tokens.Last.NeedObjectName() || uo.Flags.Get(jsonflags.StringifyNumbers)
		var flags jsonwire.ValueFlags
		val, err := xd.ReadValue(&flags)
		if err != nil {
			return err
		}
		k := val.Kind()
		switch k {
		case 'n':
			if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
				va.SetUint(0)
			}
			return nil
		case '"':
			if !stringify {
				break
			}
			val = jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
			if uo.Flags.Get(jsonflags.StringifyWithLegacySemantics) {
				// 出于历史原因，v1 按照 Go 语法解析带引号的数字，
				// 并允许带引号的 null。
				// 参见 https://go.dev/issue/75619
				n, err := strconv.ParseUint(string(val), 10, bits)
				if err != nil {
					if string(val) == "null" {
						if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
							va.SetUint(0)
						}
						return nil
					}
					return newUnmarshalErrorAfterWithValue(dec, t, errors.Unwrap(err))
				}
				va.SetUint(n)
				return nil
			}
			fallthrough
		case '0':
			if stringify && k == '0' {
				break
			}
			n, ok := jsonwire.ParseUint(val)
			maxUint := uint64(1) << bits
			overflow := n > maxUint-1
			if !ok {
				if n != math.MaxUint64 {
					return newUnmarshalErrorAfterWithValue(dec, t, strconv.ErrSyntax)
				}
				overflow = true
			}
			if overflow {
				return newUnmarshalErrorAfterWithValue(dec, t, strconv.ErrRange)
			}
			va.SetUint(n)
			return nil
		}
		return newUnmarshalErrorAfter(dec, t, nil)
	}
	return &fncs
}

func makeFloatArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	bits := t.Bits()
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		xe := export.Encoder(enc)
		var allowNonFinite bool
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			if mo.Format == "nonfinite" {
				allowNonFinite = true
			} else {
				return newInvalidFormatError(enc, t)
			}
		}

		fv := va.Float()
		if math.IsNaN(fv) || math.IsInf(fv, 0) {
			if !allowNonFinite {
				err := fmt.Errorf("unsupported value: %v", fv)
				return newMarshalErrorBefore(enc, t, err)
			}
			return enc.WriteToken(jsontext.Float(fv))
		}

		// 针对没有前置空白或字符串转义的情况优化序列化。
		if optimizeCommon && !mo.Flags.Get(jsonflags.AnyWhitespace|jsonflags.StringifyNumbers) && !xe.Tokens.Last.NeedObjectName() {
			xe.Buf = jsonwire.AppendFloat(xe.Tokens.MayAppendDelim(xe.Buf, '0'), fv, bits)
			xe.Tokens.Last.Increment()
			if xe.NeedFlush() {
				return xe.Flush()
			}
			return nil
		}

		k := stringOrNumberKind(xe.Tokens.Last.NeedObjectName() || mo.Flags.Get(jsonflags.StringifyNumbers))
		return xe.AppendRaw(k, true, func(b []byte) ([]byte, error) {
			return jsonwire.AppendFloat(b, va.Float(), bits), nil
		})
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		var allowNonFinite bool
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			if uo.Format == "nonfinite" {
				allowNonFinite = true
			} else {
				return newInvalidFormatError(dec, t)
			}
		}
		stringify := xd.Tokens.Last.NeedObjectName() || uo.Flags.Get(jsonflags.StringifyNumbers)
		var flags jsonwire.ValueFlags
		val, err := xd.ReadValue(&flags)
		if err != nil {
			return err
		}
		k := val.Kind()
		switch k {
		case 'n':
			if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
				va.SetFloat(0)
			}
			return nil
		case '"':
			val = jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
			if allowNonFinite {
				switch string(val) {
				case "NaN":
					va.SetFloat(math.NaN())
					return nil
				case "Infinity":
					va.SetFloat(math.Inf(+1))
					return nil
				case "-Infinity":
					va.SetFloat(math.Inf(-1))
					return nil
				}
			}
			if !stringify {
				break
			}
			if uo.Flags.Get(jsonflags.StringifyWithLegacySemantics) {
				// 出于历史原因，v1 按照 Go 语法解析带引号的数字，
				// 并允许带引号的 null。
				// 参见 https://go.dev/issue/75619
				n, err := strconv.ParseFloat(string(val), bits)
				if err != nil {
					if string(val) == "null" {
						if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
							va.SetFloat(0)
						}
						return nil
					}
					return newUnmarshalErrorAfterWithValue(dec, t, errors.Unwrap(err))
				}
				va.SetFloat(n)
				return nil
			}
			if n, err := jsonwire.ConsumeNumber(val); n != len(val) || err != nil {
				return newUnmarshalErrorAfterWithValue(dec, t, strconv.ErrSyntax)
			}
			fallthrough
		case '0':
			if stringify && k == '0' {
				break
			}
			fv, ok := jsonwire.ParseFloat(val, bits)
			va.SetFloat(fv)
			if !ok {
				return newUnmarshalErrorAfterWithValue(dec, t, strconv.ErrRange)
			}
			return nil
		}
		return newUnmarshalErrorAfter(dec, t, nil)
	}
	return &fncs
}

func makeMapArshaler(t reflect.Type) *arshaler {
	// 注意：下面的逻辑在处理具有唯一表示的映射键时，
	// 禁用用于跟踪重复名称的命名空间。

	// 注意：从 map 中取出的值是不可寻址的，
	// 因此我们浅拷贝这些值使其可寻址，
	// 之后再将其存回 map。

	var fncs arshaler
	var (
		once    sync.Once
		keyFncs *arshaler
		valFncs *arshaler
	)
	init := func() {
		keyFncs = lookupArshaler(t.Key())
		valFncs = lookupArshaler(t.Elem())
	}
	nillableLegacyKey := t.Key().Kind() == reflect.Pointer &&
		implementsAny(t.Key(), textMarshalerType, textAppenderType)
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		// 检查循环。
		xe := export.Encoder(enc)
		if xe.Tokens.Depth() > startDetectingCyclesAfter {
			if err := visitPointer(&xe.SeenPointers, va.Value); err != nil {
				return newMarshalErrorBefore(enc, t, err)
			}
			defer leavePointer(&xe.SeenPointers, va.Value)
		}

		emitNull := mo.Flags.Get(jsonflags.FormatNilMapAsNull)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			switch mo.Format {
			case "emitnull":
				emitNull = true
				mo.Format = ""
			case "emitempty":
				emitNull = false
				mo.Format = ""
			default:
				return newInvalidFormatError(enc, t)
			}
		}

		// 处理空 map。
		n := va.Len()
		if n == 0 {
			if emitNull && va.IsNil() {
				return enc.WriteToken(jsontext.Null)
			}
			// 针对没有任何前置空白的情况优化空 map 的序列化。
			if optimizeCommon && !mo.Flags.Get(jsonflags.AnyWhitespace) && !xe.Tokens.Last.NeedObjectName() {
				xe.Buf = append(xe.Tokens.MayAppendDelim(xe.Buf, '{'), "{}"...)
				xe.Tokens.Last.Increment()
				if xe.NeedFlush() {
					return xe.Flush()
				}
				return nil
			}
		}

		once.Do(init)
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if n > 0 {
			nonDefaultKey := keyFncs.nonDefault
			marshalKey := keyFncs.marshal
			marshalVal := valFncs.marshal
			if mo.Marshalers != nil {
				var ok bool
				marshalKey, ok = mo.Marshalers.(*Marshalers).lookup(marshalKey, t.Key())
				marshalVal, _ = mo.Marshalers.(*Marshalers).lookup(marshalVal, t.Elem())
				nonDefaultKey = nonDefaultKey || ok
			}
			k := newAddressableValue(t.Key())
			v := newAddressableValue(t.Elem())

			// Go map 保证每个条目都有唯一的键。
			// 因此，如果我们知道每个 Go 键都会序列化为唯一的 JSON 字符串，
			// 就禁用开销较大的重复名称检查。
			if !nonDefaultKey && mapKeyWithUniqueRepresentation(k.Kind(), mo.Flags.Get(jsonflags.AllowInvalidUTF8)) {
				xe.Tokens.Last.DisableNamespace()
			}

			switch {
			case !mo.Flags.Get(jsonflags.Deterministic) || n <= 1:
				for iter := va.Value.MapRange(); iter.Next(); {
					k.SetIterKey(iter)
					err := marshalKey(enc, k, mo)
					if err != nil {
						if mo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) &&
							errors.Is(err, jsontext.ErrNonStringName) && nillableLegacyKey && k.IsNil() {
							err = enc.WriteToken(jsontext.String(""))
						}
						if err != nil {
							if serr, ok := err.(*jsontext.SyntacticError); ok && serr.Err == jsontext.ErrNonStringName {
								err = newMarshalErrorBefore(enc, k.Type(), err)
							}
							return err
						}
					}
					v.SetIterValue(iter)
					if err := marshalVal(enc, v, mo); err != nil {
						return err
					}
				}
			case !nonDefaultKey && t.Key().Kind() == reflect.String:
				names := getStrings(n)
				for i, iter := 0, va.Value.MapRange(); i < n && iter.Next(); i++ {
					k.SetIterKey(iter)
					(*names)[i] = k.String()
				}
				slices.Sort(*names)
				for _, name := range *names {
					if err := enc.WriteToken(jsontext.String(name)); err != nil {
						return err
					}
					// TODO(https://go.dev/issue/57061): 使用 v.SetMapIndexOf。
					k.SetString(name)
					v.Set(va.MapIndex(k.Value))
					if err := marshalVal(enc, v, mo); err != nil {
						return err
					}
				}
				putStrings(names)
			default:
				type member struct {
					name string // 未加引号的名称
					key  addressableValue
					val  addressableValue
				}
				members := make([]member, n)
				keys := reflect.MakeSlice(reflect.SliceOf(t.Key()), n, n)
				vals := reflect.MakeSlice(reflect.SliceOf(t.Elem()), n, n)
				for i, iter := 0, va.Value.MapRange(); i < n && iter.Next(); i++ {
					// 序列化成员名称。
					k := addressableValue{keys.Index(i), true} // 索引切片元素始终可寻址
					k.SetIterKey(iter)
					v := addressableValue{vals.Index(i), true} // 索引切片元素始终可寻址
					v.SetIterValue(iter)
					err := marshalKey(enc, k, mo)
					if err != nil {
						if mo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) &&
							errors.Is(err, jsontext.ErrNonStringName) && nillableLegacyKey && k.IsNil() {
							err = enc.WriteToken(jsontext.String(""))
						}
						if err != nil {
							if serr, ok := err.(*jsontext.SyntacticError); ok && serr.Err == jsontext.ErrNonStringName {
								err = newMarshalErrorBefore(enc, k.Type(), err)
							}
							return err
						}
					}
					name := xe.UnwriteOnlyObjectMemberName()
					members[i] = member{name, k, v}
				}
				// TODO: 如果启用了 AllowDuplicateNames，
				// 那么当名称相等时也根据 reflect.Value 排序。
				// 参见 internal/fmtsort。
				slices.SortFunc(members, func(x, y member) int {
					return strings.Compare(x.name, y.name)
				})
				for _, member := range members {
					if err := enc.WriteToken(jsontext.String(member.name)); err != nil {
						return err
					}
					if err := marshalVal(enc, member.val, mo); err != nil {
						return err
					}
				}
			}
		}
		if err := enc.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
		return nil
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			switch uo.Format {
			case "emitnull", "emitempty":
				uo.Format = "" // 仅在序列化时相关
			default:
				return newInvalidFormatError(dec, t)
			}
		}
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		k := tok.Kind()
		switch k {
		case 'n':
			va.SetZero()
			return nil
		case '{':
			once.Do(init)
			if va.IsNil() {
				va.Set(reflect.MakeMap(t))
			}

			nonDefaultKey := keyFncs.nonDefault
			unmarshalKey := keyFncs.unmarshal
			unmarshalVal := valFncs.unmarshal
			if uo.Unmarshalers != nil {
				var ok bool
				unmarshalKey, ok = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshalKey, t.Key())
				unmarshalVal, _ = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshalVal, t.Elem())
				nonDefaultKey = nonDefaultKey || ok
			}
			k := newAddressableValue(t.Key())
			v := newAddressableValue(t.Elem())

			// 通过判断反序列化后的键是否已存在于目标 Go map 中来手动检查重复条目。
			// 因此，语法上不同的名称（例如 "0" 和 "-0"）
			// 会被作为重复项拒绝，因为它们在语义上指向
			// 同一个 Go 值。这是语法与语义之间一种不寻常的
			// 交互，但更为正确。
			if !nonDefaultKey && mapKeyWithUniqueRepresentation(k.Kind(), uo.Flags.Get(jsonflags.AllowInvalidUTF8)) {
				xd.Tokens.Last.DisableNamespace()
			}

			// 在 map 并非已经为空的罕见情况下，
			// 我们需要手动跟踪已经见到过哪些键，
			// 因为仅凭已存在这一点不足以表明
			// 输入中是否出现了重复名称。
			var seen reflect.Value
			if !uo.Flags.Get(jsonflags.AllowDuplicateNames) && va.Len() > 0 {
				seen = reflect.MakeMap(reflect.MapOf(k.Type(), emptyStructType))
			}

			var errUnmarshal error
			for dec.PeekKind() != '}' {
				// 反序列化 map 条目的键。
				k.SetZero()
				err := unmarshalKey(dec, k, uo)
				if err != nil {
					if isFatalError(err, uo.Flags) {
						return err
					}
					if err := dec.SkipValue(); err != nil {
						return err
					}
					errUnmarshal = cmp.Or(errUnmarshal, err)
					continue
				}
				if k.Kind() == reflect.Interface && !k.IsNil() && !k.Elem().Type().Comparable() {
					err := newUnmarshalErrorAfter(dec, t, fmt.Errorf("invalid incomparable key type %v", k.Elem().Type()))
					if !uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
						return err
					}
					if err2 := dec.SkipValue(); err2 != nil {
						return err2
					}
					errUnmarshal = cmp.Or(errUnmarshal, err)
					continue
				}

				// 检查此键是否已存在预先存在的 map 条目值。
				if v2 := va.MapIndex(k.Value); v2.IsValid() {
					if !uo.Flags.Get(jsonflags.AllowDuplicateNames) && (!seen.IsValid() || seen.MapIndex(k.Value).IsValid()) {
						// TODO: 取消读取对象名称。
						name := xd.PreviousTokenOrValue()
						return newDuplicateNameError(dec.StackPointer(), nil, dec.InputOffset()-len64(name))
					}
					if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
						v.Set(v2)
					} else {
						v.SetZero()
					}
				} else {
					v.SetZero()
				}

				// 反序列化 map 条目的值。
				err = unmarshalVal(dec, v, uo)
				va.SetMapIndex(k.Value, v.Value)
				if seen.IsValid() {
					seen.SetMapIndex(k.Value, reflect.Zero(emptyStructType))
				}
				if err != nil {
					if isFatalError(err, uo.Flags) {
						return err
					}
					errUnmarshal = cmp.Or(errUnmarshal, err)
				}
			}
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			return errUnmarshal
		}
		return newUnmarshalErrorAfterWithSkipping(dec, t, nil)
	}
	return &fncs
}

// mapKeyWithUniqueRepresentation 报告 k 的所有可能值是否都序列化为不同的 JSON 值，
// 以及所有能够反序列化到 k 的 JSON 值是否都反序列化为不同的 Go 值。
// 换句话说，该表示必须是双射的。
func mapKeyWithUniqueRepresentation(k reflect.Kind, allowInvalidUTF8 bool) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	case reflect.String:
		// 对于字符串，必须小心，因为带有无效 UTF-8 的名称
		// 可能会反转义为相同的 Go 字符串值。
		return !allowInvalidUTF8
	default:
		// 上面未列出浮点类型，因为 NaN 可能出现多次，
		// 且都会序列化为 "NaN"。
		return false
	}
}

var errNilField = errors.New("cannot set embedded pointer to unexported struct type")

func makeStructArshaler(t reflect.Type) *arshaler {
	// 注意：下面的逻辑禁用了用于跟踪重复名称的命名空间，
	// 而是根据所见过的 Go 结构体字段，用一个高效的位集
	// 在本地进行跟踪。

	var fncs arshaler
	var (
		once    sync.Once
		fields  structFields
		errInit *SemanticError
	)
	init := func() {
		fields, errInit = makeStructFields(t)
	}
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		xe := export.Encoder(enc)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			return newInvalidFormatError(enc, t)
		}
		once.Do(init)
		if errInit != nil && !mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
			return newMarshalErrorBefore(enc, errInit.GoType, errInit.Err)
		}
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		var seenIdxs uintSet
		prevIdx := -1
		xe.Tokens.Last.DisableNamespace() // 我们在下面手动确保名称唯一
		for i := range fields.flattened {
			f := &fields.flattened[i]
			v := addressableValue{va.Field(f.index0), va.forcedAddr} // 如果结构体值可寻址，则此项可寻址
			if len(f.index) > 0 {
				v = v.fieldByIndex(f.index, false)
				if !v.IsValid() {
					continue // 意味着内联字段为 nil
				}
			}

			// OmitZero 在 Go 值为零时跳过该字段，
			// 我们可以在前面就确定这一点，而无需调用序列化器。
			if (f.omitzero || mo.Flags.Get(jsonflags.OmitZeroStructFields)) &&
				((f.isZero == nil && v.IsZero()) || (f.isZero != nil && f.isZero(v))) {
				continue
			}

			// 检查 omitempty 的遗留定义。
			if f.omitempty && mo.Flags.Get(jsonflags.OmitEmptyWithLegacySemantics) && isLegacyEmpty(v) {
				continue
			}

			marshal := f.fncs.marshal
			nonDefault := f.fncs.nonDefault
			if mo.Marshalers != nil {
				var ok bool
				marshal, ok = mo.Marshalers.(*Marshalers).lookup(marshal, f.typ)
				nonDefault = nonDefault || ok
			}

			// OmitEmpty 在序列化后的 JSON 值为空时跳过该字段，
			// 如果没有自定义序列化器，我们可以在前面就确定这一点，
			// 否则必须先序列化该值，若为空再取消写入。
			if f.omitempty && !mo.Flags.Get(jsonflags.OmitEmptyWithLegacySemantics) &&
				!nonDefault && f.isEmpty != nil && f.isEmpty(v) {
				continue // omitempty 的快速路径
			}

			// 写入对象成员名称。
			//
			// 下面的逻辑在语义上等价于：
			//	enc.WriteToken(String(f.name))
			// 但经过了专门化和简化，因为：
			//	1. Encoder 必定正期待一个对象名称。
			//	2. 对象命名空间保证已禁用。
			//	3. 对象名称保证有效且已预先转义。
			//	4. 无需刷新缓冲区（为了取消写入）。
			//	5. 不可能发生错误。
			if optimizeCommon {
				// 追加任意分隔符或可选空白。
				b := xe.Buf
				if xe.Tokens.Last.Length() > 0 {
					b = append(b, ',')
					if mo.Flags.Get(jsonflags.SpaceAfterComma) {
						b = append(b, ' ')
					}
				}
				if mo.Flags.Get(jsonflags.Multiline) {
					b = xe.AppendIndent(b, xe.Tokens.NeedIndent('"'))
				}

				// 将该 token 追加到输出和状态机。
				n0 := len(b) // 调用 AppendQuote 之前的偏移量
				if !f.nameNeedEscape {
					b = append(b, f.quotedName...)
				} else {
					b, _ = jsonwire.AppendQuote(b, f.name, &mo.Flags)
				}
				xe.Buf = b
				xe.Names.ReplaceLastQuotedOffset(n0)
				xe.Tokens.Last.Increment()
			} else {
				if err := enc.WriteToken(jsontext.String(f.name)); err != nil {
					return err
				}
			}

			// 写入对象成员值。
			flagsOriginal := mo.Flags
			if f.string {
				if !mo.Flags.Get(jsonflags.StringifyWithLegacySemantics) {
					mo.Flags.Set(jsonflags.StringifyNumbers | 1)
				} else if canLegacyStringify(f.typ) {
					mo.Flags.Set(jsonflags.StringifyNumbers | jsonflags.StringifyBoolsAndStrings | 1)
				}
			}
			if f.format != "" {
				mo.FormatDepth = xe.Tokens.Depth()
				mo.Format = f.format
			}
			err := marshal(enc, v, mo)
			mo.Flags = flagsOriginal
			mo.Format = ""
			if err != nil {
				return err
			}

			// 如果成员为空则尝试取消写入（omitempty 的慢速路径）。
			if f.omitempty && !mo.Flags.Get(jsonflags.OmitEmptyWithLegacySemantics) {
				var prevName *string
				if prevIdx >= 0 {
					prevName = &fields.flattened[prevIdx].name
				}
				if xe.UnwriteEmptyObjectMember(prevName) {
					continue
				}
			}

			// 记住上一个写入的对象成员。
			// 只有在需要检测与来自内联回退的名称重复时，
			// 才需要更新已见字段的集合。
			if !mo.Flags.Get(jsonflags.AllowDuplicateNames) && fields.inlinedFallback != nil {
				seenIdxs.insert(uint(f.id))
			}
			prevIdx = f.id
		}
		if fields.inlinedFallback != nil && !(mo.Flags.Get(jsonflags.DiscardUnknownMembers) && fields.inlinedFallback.unknown) {
			var insertUnquotedName func([]byte) bool
			if !mo.Flags.Get(jsonflags.AllowDuplicateNames) {
				insertUnquotedName = func(name []byte) bool {
					// 检查来自内联回退的名称是否不与
					// 之前从已知字段序列化出的某个名称匹配。
					if foldedFields := fields.lookupByFoldedName(name); len(foldedFields) > 0 {
						if f := fields.byActualName[string(name)]; f != nil {
							return seenIdxs.insert(uint(f.id))
						}
						for _, f := range foldedFields {
							if f.matchFoldedName(name, &mo.Flags) {
								return seenIdxs.insert(uint(f.id))
							}
						}
					}

					// 检查该名称是否不与之前从内联回退
					// 序列化出的任何其他名称匹配。
					return xe.Namespaces.Last().InsertUnquoted(name)
				}
			}
			if err := marshalInlinedFallbackAll(enc, va, mo, fields.inlinedFallback, insertUnquotedName); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
		return nil
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			return newInvalidFormatError(dec, t)
		}
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		k := tok.Kind()
		switch k {
		case 'n':
			if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
				va.SetZero()
			}
			return nil
		case '{':
			once.Do(init)
			if errInit != nil && !uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
				return newUnmarshalErrorAfter(dec, errInit.GoType, errInit.Err)
			}
			var seenIdxs uintSet
			xd.Tokens.Last.DisableNamespace()
			var errUnmarshal error
			for dec.PeekKind() != '}' {
				// 处理对象成员名称。
				var flags jsonwire.ValueFlags
				val, err := xd.ReadValue(&flags)
				if err != nil {
					return err
				}
				name := jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
				f := fields.byActualName[string(name)]
				if f == nil {
					for _, f2 := range fields.lookupByFoldedName(name) {
						if f2.matchFoldedName(name, &uo.Flags) {
							f = f2
							break
						}
					}
					if f == nil {
						if uo.Flags.Get(jsonflags.RejectUnknownMembers) && (fields.inlinedFallback == nil || fields.inlinedFallback.unknown) {
							err := newUnmarshalErrorAfter(dec, t, ErrUnknownName)
							if !uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
								return err
							}
							errUnmarshal = cmp.Or(errUnmarshal, err)
						}
						if !uo.Flags.Get(jsonflags.AllowDuplicateNames) && !xd.Namespaces.Last().InsertUnquoted(name) {
							// TODO: 取消读取对象名称。
							return newDuplicateNameError(dec.StackPointer(), nil, dec.InputOffset()-len64(val))
						}

						if fields.inlinedFallback == nil {
							// 跳过未知值，因为我们没有地方存储它。
							if err := dec.SkipValue(); err != nil {
								return err
							}
						} else {
							// 序列化到能够存储任意对象成员的值中。
							if err := unmarshalInlinedFallbackNext(dec, va, uo, fields.inlinedFallback, val, name); err != nil {
								if isFatalError(err, uo.Flags) {
									return err
								}
								errUnmarshal = cmp.Or(errUnmarshal, err)
							}
						}
						continue
					}
				}
				if !uo.Flags.Get(jsonflags.AllowDuplicateNames) && !seenIdxs.insert(uint(f.id)) {
					// TODO: 取消读取对象名称。
					return newDuplicateNameError(dec.StackPointer(), nil, dec.InputOffset()-len64(val))
				}

				// 处理对象成员值。
				unmarshal := f.fncs.unmarshal
				if uo.Unmarshalers != nil {
					unmarshal, _ = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshal, f.typ)
				}
				flagsOriginal := uo.Flags
				if f.string {
					if !uo.Flags.Get(jsonflags.StringifyWithLegacySemantics) {
						uo.Flags.Set(jsonflags.StringifyNumbers | 1)
					} else if canLegacyStringify(f.typ) {
						uo.Flags.Set(jsonflags.StringifyNumbers | jsonflags.StringifyBoolsAndStrings | 1)
					}
				}
				if f.format != "" {
					uo.FormatDepth = xd.Tokens.Depth()
					uo.Format = f.format
				}
				v := addressableValue{va.Field(f.index0), va.forcedAddr} // 如果结构体值可寻址，则此项可寻址
				if len(f.index) > 0 {
					v = v.fieldByIndex(f.index, true)
					if !v.IsValid() {
						err := newUnmarshalErrorBefore(dec, t, errNilField)
						if !uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
							return err
						}
						errUnmarshal = cmp.Or(errUnmarshal, err)
						unmarshal = func(dec *jsontext.Decoder, _ addressableValue, _ *jsonopts.Struct) error {
							return dec.SkipValue()
						}
					}
				}
				err = unmarshal(dec, v, uo)
				uo.Flags = flagsOriginal
				uo.Format = ""
				if err != nil {
					if isFatalError(err, uo.Flags) {
						return err
					}
					errUnmarshal = cmp.Or(errUnmarshal, err)
				}
			}
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			return errUnmarshal
		}
		return newUnmarshalErrorAfterWithSkipping(dec, t, nil)
	}
	return &fncs
}

func (va addressableValue) fieldByIndex(index []int, mayAlloc bool) addressableValue {
	for _, i := range index {
		va = va.indirect(mayAlloc)
		if !va.IsValid() {
			return va
		}
		va = addressableValue{va.Field(i), va.forcedAddr} // 如果结构体值可寻址，则此项可寻址
	}
	return va
}

func (va addressableValue) indirect(mayAlloc bool) addressableValue {
	if va.Kind() == reflect.Pointer {
		if va.IsNil() {
			if !mayAlloc || !va.CanSet() {
				return addressableValue{}
			}
			va.Set(reflect.New(va.Type().Elem()))
		}
		va = addressableValue{va.Elem(), false} // 解引用后的指针始终可寻址
	}
	return va
}

// isLegacyEmpty 报告根据 v1 定义该值是否为空。
func isLegacyEmpty(v addressableValue) bool {
	// 等价于 encoding/json.isEmptyValue@v1.21.0。
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool() == false
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.String, reflect.Map, reflect.Slice, reflect.Array:
		return v.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// canLegacyStringify 报告 t 是否可以根据 v1 被字符串化，
// 其中 t 是布尔值、字符串或数字（或指向此类类型的未命名指针）。
// 在 v1 中，`string` 选项不会递归地应用于
// 复合 Go 类型（例如数组、切片、结构体、map 或接口）内的嵌套类型。
func canLegacyStringify(t reflect.Type) bool {
	// 基于 encoding/json.typeFields#L1126-L1143@v1.23.0
	if t.Name() == "" && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func makeSliceArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	var (
		once    sync.Once
		valFncs *arshaler
	)
	init := func() {
		valFncs = lookupArshaler(t.Elem())
	}
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		// 检查循环。
		xe := export.Encoder(enc)
		if xe.Tokens.Depth() > startDetectingCyclesAfter {
			if err := visitPointer(&xe.SeenPointers, va.Value); err != nil {
				return newMarshalErrorBefore(enc, t, err)
			}
			defer leavePointer(&xe.SeenPointers, va.Value)
		}

		emitNull := mo.Flags.Get(jsonflags.FormatNilSliceAsNull)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			switch mo.Format {
			case "emitnull":
				emitNull = true
				mo.Format = ""
			case "emitempty":
				emitNull = false
				mo.Format = ""
			default:
				return newInvalidFormatError(enc, t)
			}
		}

		// 处理空切片。
		n := va.Len()
		if n == 0 {
			if emitNull && va.IsNil() {
				return enc.WriteToken(jsontext.Null)
			}
			// 针对没有任何前置空白的情况优化空切片的序列化。
			if optimizeCommon && !mo.Flags.Get(jsonflags.AnyWhitespace) && !xe.Tokens.Last.NeedObjectName() {
				xe.Buf = append(xe.Tokens.MayAppendDelim(xe.Buf, '['), "[]"...)
				xe.Tokens.Last.Increment()
				if xe.NeedFlush() {
					return xe.Flush()
				}
				return nil
			}
		}

		once.Do(init)
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		marshal := valFncs.marshal
		if mo.Marshalers != nil {
			marshal, _ = mo.Marshalers.(*Marshalers).lookup(marshal, t.Elem())
		}
		for i := range n {
			v := addressableValue{va.Index(i), false} // 索引切片元素始终可寻址
			if err := marshal(enc, v, mo); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
		return nil
	}
	emptySlice := reflect.MakeSlice(t, 0, 0)
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			switch uo.Format {
			case "emitnull", "emitempty":
				uo.Format = "" // 仅在序列化时相关
			default:
				return newInvalidFormatError(dec, t)
			}
		}

		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		k := tok.Kind()
		switch k {
		case 'n':
			va.SetZero()
			return nil
		case '[':
			once.Do(init)
			unmarshal := valFncs.unmarshal
			if uo.Unmarshalers != nil {
				unmarshal, _ = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshal, t.Elem())
			}
			mustZero := true // 我们不知道未使用容量是否干净
			cap := va.Cap()
			if cap > 0 {
				va.SetLen(cap)
			}
			var i int
			var errUnmarshal error
			for dec.PeekKind() != ']' {
				if i == cap {
					va.Value.Grow(1)
					cap = va.Cap()
					va.SetLen(cap)
					mustZero = false // reflect.Value.Grow 确保新容量被零初始化
				}
				v := addressableValue{va.Index(i), false} // 索引切片元素始终可寻址
				i++
				if mustZero && !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
					v.SetZero()
				}
				if err := unmarshal(dec, v, uo); err != nil {
					if isFatalError(err, uo.Flags) {
						va.SetLen(i)
						return err
					}
					errUnmarshal = cmp.Or(errUnmarshal, err)
				}
			}
			if i == 0 {
				va.Set(emptySlice)
			} else {
				va.SetLen(i)
			}
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			return errUnmarshal
		}
		return newUnmarshalErrorAfterWithSkipping(dec, t, nil)
	}
	return &fncs
}

var errArrayUnderflow = errors.New("too few array elements")
var errArrayOverflow = errors.New("too many array elements")

func makeArrayArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	var (
		once    sync.Once
		valFncs *arshaler
	)
	init := func() {
		valFncs = lookupArshaler(t.Elem())
	}
	n := t.Len()
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		xe := export.Encoder(enc)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			return newInvalidFormatError(enc, t)
		}
		once.Do(init)
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		marshal := valFncs.marshal
		if mo.Marshalers != nil {
			marshal, _ = mo.Marshalers.(*Marshalers).lookup(marshal, t.Elem())
		}
		for i := range n {
			v := addressableValue{va.Index(i), va.forcedAddr} // 如果数组可寻址，则索引数组元素可寻址
			if err := marshal(enc, v, mo); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
		return nil
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			return newInvalidFormatError(dec, t)
		}
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		k := tok.Kind()
		switch k {
		case 'n':
			if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
				va.SetZero()
			}
			return nil
		case '[':
			once.Do(init)
			unmarshal := valFncs.unmarshal
			if uo.Unmarshalers != nil {
				unmarshal, _ = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshal, t.Elem())
			}
			var i int
			var errUnmarshal error
			for dec.PeekKind() != ']' {
				if i >= n {
					if err := dec.SkipValue(); err != nil {
						return err
					}
					err = errArrayOverflow
					continue
				}
				v := addressableValue{va.Index(i), va.forcedAddr} // 如果数组可寻址，则索引数组元素可寻址
				if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
					v.SetZero()
				}
				if err := unmarshal(dec, v, uo); err != nil {
					if isFatalError(err, uo.Flags) {
						return err
					}
					errUnmarshal = cmp.Or(errUnmarshal, err)
				}
				i++
			}
			for ; i < n; i++ {
				va.Index(i).SetZero()
				err = errArrayUnderflow
			}
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			if err != nil && !uo.Flags.Get(jsonflags.UnmarshalArrayFromAnyLength) {
				return newUnmarshalErrorAfter(dec, t, err)
			}
			return errUnmarshal
		}
		return newUnmarshalErrorAfterWithSkipping(dec, t, nil)
	}
	return &fncs
}

func makePointerArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	var (
		once    sync.Once
		valFncs *arshaler
	)
	init := func() {
		valFncs = lookupArshaler(t.Elem())
	}
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		// 检查循环。
		xe := export.Encoder(enc)
		if xe.Tokens.Depth() > startDetectingCyclesAfter {
			if err := visitPointer(&xe.SeenPointers, va.Value); err != nil {
				return newMarshalErrorBefore(enc, t, err)
			}
			defer leavePointer(&xe.SeenPointers, va.Value)
		}

		// 注意：Struct.Format 会被转发到底层的 marshal。
		if va.IsNil() {
			return enc.WriteToken(jsontext.Null)
		}
		once.Do(init)
		marshal := valFncs.marshal
		if mo.Marshalers != nil {
			marshal, _ = mo.Marshalers.(*Marshalers).lookup(marshal, t.Elem())
		}
		v := addressableValue{va.Elem(), false} // 解引用后的指针始终可寻址
		return marshal(enc, v, mo)
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		// 注意：Struct.Format 会被转发到底层的 unmarshal。
		if dec.PeekKind() == 'n' {
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			va.SetZero()
			return nil
		}
		once.Do(init)
		unmarshal := valFncs.unmarshal
		if uo.Unmarshalers != nil {
			unmarshal, _ = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshal, t.Elem())
		}
		if va.IsNil() {
			va.Set(reflect.New(t.Elem()))
		}
		v := addressableValue{va.Elem(), false} // 解引用后的指针始终可寻址
		if err := unmarshal(dec, v, uo); err != nil {
			return err
		}
		if uo.Flags.Get(jsonflags.StringifyWithLegacySemantics) &&
			uo.Flags.Get(jsonflags.StringifyNumbers|jsonflags.StringifyBoolsAndStrings) {
			// 位于 JSON 字符串内被引用的 JSON null 应当在
			// 指针值内生效，而非在间接值内生效。
			//
			// TODO: 这无法正确处理转义的 null
			// （例如 "\u006e\u0075\u006c\u006c"），但对于
			// `string` 选项这种冷门用例已经足够。
			if string(export.Decoder(dec).PreviousTokenOrValue()) == `"null"` {
				va.SetZero()
			}
		}
		return nil
	}
	return &fncs
}

func makeInterfaceArshaler(t reflect.Type) *arshaler {
	// 注意：从接口中取出的值是不可寻址的，
	// 因此我们浅拷贝这些值使其可寻址，
	// 之后再将其存回接口。

	var fncs arshaler
	var whichMarshaler reflect.Type
	for _, iface := range allMarshalerTypes {
		if t.Implements(iface) {
			whichMarshaler = t
			break
		}
	}
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		xe := export.Encoder(enc)
		if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
			return newInvalidFormatError(enc, t)
		}
		if va.IsNil() {
			return enc.WriteToken(jsontext.Null)
		} else if mo.Flags.Get(jsonflags.CallMethodsWithLegacySemantics) && whichMarshaler != nil {
			// 指针的序列化器从不会在 nil 接收者上调用方法。
			// 将该 nil 指针包装在一个结构体类型中，使 marshal
			// 改为出现在值接收者上从而可以被调用。
			if va.Elem().Kind() == reflect.Pointer && va.Elem().IsNil() {
				v2 := newAddressableValue(whichMarshaler)
				switch whichMarshaler {
				case jsonMarshalerToType:
					v2.Set(reflect.ValueOf(struct{ MarshalerTo }{va.Elem().Interface().(MarshalerTo)}))
				case jsonMarshalerType:
					v2.Set(reflect.ValueOf(struct{ Marshaler }{va.Elem().Interface().(Marshaler)}))
				case textAppenderType:
					v2.Set(reflect.ValueOf(struct{ encoding.TextAppender }{va.Elem().Interface().(encoding.TextAppender)}))
				case textMarshalerType:
					v2.Set(reflect.ValueOf(struct{ encoding.TextMarshaler }{va.Elem().Interface().(encoding.TextMarshaler)}))
				}
				va = v2
			}
		}
		v := newAddressableValue(va.Elem().Type())
		v.Set(va.Elem())
		marshal := lookupArshaler(v.Type()).marshal
		if mo.Marshalers != nil {
			marshal, _ = mo.Marshalers.(*Marshalers).lookup(marshal, v.Type())
		}
		// 如果没有特殊选项，则针对 any 类型进行优化。
		if optimizeCommon &&
			t == anyType && !mo.Flags.Get(jsonflags.StringifyNumbers|jsonflags.StringifyBoolsAndStrings) && mo.Format == "" &&
			(mo.Marshalers == nil || !mo.Marshalers.(*Marshalers).fromAny) {
			return marshalValueAny(enc, va.Elem().Interface(), mo)
		}
		return marshal(enc, v, mo)
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		xd := export.Decoder(dec)
		if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
			return newInvalidFormatError(dec, t)
		}
		if uo.Flags.Get(jsonflags.MergeWithLegacySemantics) && !va.IsNil() {
			// 遗留的合并行为很难解释。
			// 一般来说，它只对非 nil 指针类型进行合并。
			// 作为一种特殊情况，将 JSON null 反序列化到指针
			// 会设置底层类型的具体 nil 指针
			// （而不是将接口值本身设为 nil）。
			e := va.Elem()
			if e.Kind() == reflect.Pointer && !e.IsNil() {
				if dec.PeekKind() == 'n' && e.Elem().Kind() == reflect.Pointer {
					if _, err := dec.ReadToken(); err != nil {
						return err
					}
					va.Elem().Elem().SetZero()
					return nil
				}
			} else {
				va.SetZero()
			}
		}
		if dec.PeekKind() == 'n' {
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			va.SetZero()
			return nil
		}
		var v addressableValue
		if va.IsNil() {
			// 如果没有特殊选项，则针对 any 类型进行优化。
			// 我们不关心字符串化的数字，因为 JSON 字符串
			// 总是会作为 Go 字符串反序列化到 any 值中。
			// 必须强制执行重复名称检查，因为 unmarshalValueAny
			// 并未实现合并语义。
			if optimizeCommon &&
				t == anyType && !uo.Flags.Get(jsonflags.AllowDuplicateNames) && uo.Format == "" &&
				(uo.Unmarshalers == nil || !uo.Unmarshalers.(*Unmarshalers).fromAny) {
				v, err := unmarshalValueAny(dec, uo)
				// 我们必须在前面检查 nil 接口值。
				// 参见 https://go.dev/issue/52310。
				if v != nil {
					va.Set(reflect.ValueOf(v))
				}
				return err
			}

			k := dec.PeekKind()
			if !isAnyType(t) {
				return newUnmarshalErrorBeforeWithSkipping(dec, t, internal.ErrNilInterface)
			}
			switch k {
			case 'f', 't':
				v = newAddressableValue(boolType)
			case '"':
				v = newAddressableValue(stringType)
			case '0':
				if uo.Flags.Get(jsonflags.UnmarshalAnyWithRawNumber) {
					v = addressableValue{reflect.ValueOf(internal.NewRawNumber()).Elem(), true}
				} else {
					v = newAddressableValue(float64Type)
				}
			case '{':
				v = newAddressableValue(mapStringAnyType)
			case '[':
				v = newAddressableValue(sliceAnyType)
			default:
				// 如果 k 无效（例如由于 I/O 或语法错误），
				// 那么它会被 PeekKind 缓存并由 ReadValue 返回。
				// 如果 k 是 '}' 或 ']'，则 ReadValue 必定出错，因为
				// 这些是 JSON 值开头的非法类型。
				_, err := dec.ReadValue()
				return err
			}
		} else {
			// 浅拷贝现有值以使其保持可寻址。
			// 该值顶层的任何修改都是可观察的，
			// 因为我们总是会将该值存回接口值中。
			v = newAddressableValue(va.Elem().Type())
			v.Set(va.Elem())
		}
		unmarshal := lookupArshaler(v.Type()).unmarshal
		if uo.Unmarshalers != nil {
			unmarshal, _ = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshal, v.Type())
		}
		err := unmarshal(dec, v, uo)
		va.Set(v.Value)
		return err
	}
	return &fncs
}

// isAnyType 报告 t 是否等价于 any 接口类型。
func isAnyType(t reflect.Type) bool {
	// 如果 Go 语言允许在普通接口中使用类型集，那么这是向前兼容的，
	// 在这种情况下，零方法的接口并不
	// 一定意味着它可以容纳所有可能的 Go 类型。
	// 参见 https://go.dev/issue/45346。
	return t == anyType || anyType.Implements(t)
}

func makeInvalidArshaler(t reflect.Type) *arshaler {
	var fncs arshaler
	fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
		return newMarshalErrorBefore(enc, t, nil)
	}
	fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
		return newUnmarshalErrorBefore(dec, t, nil)
	}
	return &fncs
}

func stringOrNumberKind(isString bool) jsontext.Kind {
	if isString {
		return '"'
	} else {
		return '0'
	}
}

type uintSet64 uint64

func (s uintSet64) has(i uint) bool { return s&(1<<i) > 0 }
func (s *uintSet64) set(i uint)     { *s |= 1 << i }

// uintSet 是一个无符号整数集合。
// 它针对大多数整数接近零的情况进行了优化。
type uintSet struct {
	lo uintSet64
	hi []uintSet64
}

// has 报告 i 是否在集合中。
func (s *uintSet) has(i uint) bool {
	if i < 64 {
		return s.lo.has(i)
	} else {
		i -= 64
		iHi, iLo := int(i/64), i%64
		return iHi < len(s.hi) && s.hi[iHi].has(iLo)
	}
}

// insert 将 i 插入集合，并报告它是否为首次插入。
func (s *uintSet) insert(i uint) bool {
	// TODO: 至少对低 64 位的情况使其可内联。
	if i < 64 {
		has := s.lo.has(i)
		s.lo.set(i)
		return !has
	} else {
		i -= 64
		iHi, iLo := int(i/64), i%64
		if iHi >= len(s.hi) {
			s.hi = append(s.hi, make([]uintSet64, iHi+1-len(s.hi))...)
			s.hi = s.hi[:cap(s.hi)]
		}
		has := s.hi[iHi].has(iLo)
		s.hi[iHi].set(iLo)
		return !has
	}
}
