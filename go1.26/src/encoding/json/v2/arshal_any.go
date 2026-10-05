// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"cmp"
	"math"
	"reflect"
	"slices"
	"strconv"

	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
	"encoding/json/jsontext"
)

// 此文件包含针对 any 类型的优化版 marshal 和 unmarshal
// 实现。当 Go 程序对 JSON schema
// 一无所知时，经常会使用此类型。这种情况足够常见，
// 以至于值得为它增加逻辑的复杂性。

// marshalValueAny 将 Go any 序列化为 JSON 值。
// 它假定对于任何可能存在的嵌套值
// 都没有特殊的格式化指令。
func marshalValueAny(enc *jsontext.Encoder, val any, mo *jsonopts.Struct) error {
	switch val := val.(type) {
	case nil:
		return enc.WriteToken(jsontext.Null)
	case bool:
		return enc.WriteToken(jsontext.Bool(val))
	case string:
		return enc.WriteToken(jsontext.String(val))
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			break // 使用下面的默认逻辑
		}
		return enc.WriteToken(jsontext.Float(val))
	case map[string]any:
		return marshalObjectAny(enc, val, mo)
	case []any:
		return marshalArrayAny(enc, val, mo)
	}

	v := newAddressableValue(reflect.TypeOf(val))
	v.Set(reflect.ValueOf(val))
	marshal := lookupArshaler(v.Type()).marshal
	if mo.Marshalers != nil {
		marshal, _ = mo.Marshalers.(*Marshalers).lookup(marshal, v.Type())
	}
	return marshal(enc, v, mo)
}

// unmarshalValueAny 将 JSON 值反序列化为 Go any。
// 它假定对于任何可能存在的嵌套值
// 都没有特殊的格式化指令。
// 由于它不实现合并，因此必须拒绝重复名称。
func unmarshalValueAny(dec *jsontext.Decoder, uo *jsonopts.Struct) (any, error) {
	switch k := dec.PeekKind(); k {
	case '{':
		return unmarshalObjectAny(dec, uo)
	case '[':
		return unmarshalArrayAny(dec, uo)
	default:
		xd := export.Decoder(dec)
		var flags jsonwire.ValueFlags
		val, err := xd.ReadValue(&flags)
		if err != nil {
			return nil, err
		}
		switch val.Kind() {
		case 'n':
			return nil, nil
		case 'f':
			return false, nil
		case 't':
			return true, nil
		case '"':
			val = jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
			if xd.StringCache == nil {
				xd.StringCache = new(stringCache)
			}
			return makeString(xd.StringCache, val), nil
		case '0':
			if uo.Flags.Get(jsonflags.UnmarshalAnyWithRawNumber) {
				return internal.RawNumberOf(val), nil
			}
			fv, ok := jsonwire.ParseFloat(val, 64)
			if !ok {
				return fv, newUnmarshalErrorAfterWithValue(dec, float64Type, strconv.ErrRange)
			}
			return fv, nil
		default:
			panic("BUG: invalid kind: " + k.String())
		}
	}
}

// marshalObjectAny 将 Go map[string]any 序列化为 JSON 对象
// （或者如果为 nil 且指定了 [jsonflags.FormatNilMapAsNull]，则序列化为 JSON null）。
func marshalObjectAny(enc *jsontext.Encoder, obj map[string]any, mo *jsonopts.Struct) error {
	// 检查循环。
	xe := export.Encoder(enc)
	if xe.Tokens.Depth() > startDetectingCyclesAfter {
		v := reflect.ValueOf(obj)
		if err := visitPointer(&xe.SeenPointers, v); err != nil {
			return newMarshalErrorBefore(enc, mapStringAnyType, err)
		}
		defer leavePointer(&xe.SeenPointers, v)
	}

	// 处理空 map。
	if len(obj) == 0 {
		if mo.Flags.Get(jsonflags.FormatNilMapAsNull) && obj == nil {
			return enc.WriteToken(jsontext.Null)
		}
		// 优化在前面没有任何空白的情况下序列化空 map。
		if !mo.Flags.Get(jsonflags.AnyWhitespace) && !xe.Tokens.Last.NeedObjectName() {
			xe.Buf = append(xe.Tokens.MayAppendDelim(xe.Buf, '{'), "{}"...)
			xe.Tokens.Last.Increment()
			if xe.NeedFlush() {
				return xe.Flush()
			}
			return nil
		}
	}

	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	// Go map 保证每个条目的键都是唯一的
	// 唯一可能出现重复的情况是由于无效的 UTF-8。
	if !mo.Flags.Get(jsonflags.AllowInvalidUTF8) {
		xe.Tokens.Last.DisableNamespace()
	}
	if !mo.Flags.Get(jsonflags.Deterministic) || len(obj) <= 1 {
		for name, val := range obj {
			if err := enc.WriteToken(jsontext.String(name)); err != nil {
				return err
			}
			if err := marshalValueAny(enc, val, mo); err != nil {
				return err
			}
		}
	} else {
		names := getStrings(len(obj))
		var i int
		for name := range obj {
			(*names)[i] = name
			i++
		}
		slices.Sort(*names)
		for _, name := range *names {
			if err := enc.WriteToken(jsontext.String(name)); err != nil {
				return err
			}
			if err := marshalValueAny(enc, obj[name], mo); err != nil {
				return err
			}
		}
		putStrings(names)
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return err
	}
	return nil
}

// unmarshalObjectAny 将 JSON 对象反序列化为 Go map[string]any。
// 如果解码的不是 JSON 对象，它会 panic。
func unmarshalObjectAny(dec *jsontext.Decoder, uo *jsonopts.Struct) (map[string]any, error) {
	switch tok, err := dec.ReadToken(); {
	case err != nil:
		return nil, err
	case tok.Kind() != '{':
		panic("BUG: invalid kind: " + tok.Kind().String())
	}
	obj := make(map[string]any)
	// Go map 保证每个条目的键都是唯一的
	// 唯一可能出现重复的情况是由于无效的 UTF-8。
	if !uo.Flags.Get(jsonflags.AllowInvalidUTF8) {
		export.Decoder(dec).Tokens.Last.DisableNamespace()
	}
	var errUnmarshal error
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return obj, err
		}
		name := tok.String()

		// 手动检查重复名称。
		if _, ok := obj[name]; ok {
			// TODO: 回退对象名称。
			name := export.Decoder(dec).PreviousTokenOrValue()
			err := newDuplicateNameError(dec.StackPointer(), nil, dec.InputOffset()-len64(name))
			return obj, err
		}

		val, err := unmarshalValueAny(dec, uo)
		obj[name] = val
		if err != nil {
			if isFatalError(err, uo.Flags) {
				return obj, err
			}
			errUnmarshal = cmp.Or(err, errUnmarshal)
		}
	}
	if _, err := dec.ReadToken(); err != nil {
		return obj, err
	}
	return obj, errUnmarshal
}

// marshalArrayAny 将 Go []any 序列化为 JSON 数组
// （或者如果为 nil 且指定了 [jsonflags.FormatNilSliceAsNull]，则序列化为 JSON null）。
func marshalArrayAny(enc *jsontext.Encoder, arr []any, mo *jsonopts.Struct) error {
	// 检查循环。
	xe := export.Encoder(enc)
	if xe.Tokens.Depth() > startDetectingCyclesAfter {
		v := reflect.ValueOf(arr)
		if err := visitPointer(&xe.SeenPointers, v); err != nil {
			return newMarshalErrorBefore(enc, sliceAnyType, err)
		}
		defer leavePointer(&xe.SeenPointers, v)
	}

	// 处理空切片。
	if len(arr) == 0 {
		if mo.Flags.Get(jsonflags.FormatNilSliceAsNull) && arr == nil {
			return enc.WriteToken(jsontext.Null)
		}
		// 优化在前面没有任何空白的情况下序列化空切片。
		if !mo.Flags.Get(jsonflags.AnyWhitespace) && !xe.Tokens.Last.NeedObjectName() {
			xe.Buf = append(xe.Tokens.MayAppendDelim(xe.Buf, '['), "[]"...)
			xe.Tokens.Last.Increment()
			if xe.NeedFlush() {
				return xe.Flush()
			}
			return nil
		}
	}

	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for _, val := range arr {
		if err := marshalValueAny(enc, val, mo); err != nil {
			return err
		}
	}
	if err := enc.WriteToken(jsontext.EndArray); err != nil {
		return err
	}
	return nil
}

// unmarshalArrayAny 将 JSON 数组反序列化为 Go []any。
// 如果解码的不是 JSON 数组，它会 panic。
func unmarshalArrayAny(dec *jsontext.Decoder, uo *jsonopts.Struct) ([]any, error) {
	switch tok, err := dec.ReadToken(); {
	case err != nil:
		return nil, err
	case tok.Kind() != '[':
		panic("BUG: invalid kind: " + tok.Kind().String())
	}
	arr := []any{}
	var errUnmarshal error
	for dec.PeekKind() != ']' {
		val, err := unmarshalValueAny(dec, uo)
		arr = append(arr, val)
		if err != nil {
			if isFatalError(err, uo.Flags) {
				return arr, err
			}
			errUnmarshal = cmp.Or(errUnmarshal, err)
		}
	}
	if _, err := dec.ReadToken(); err != nil {
		return arr, err
	}
	return arr, errUnmarshal
}
