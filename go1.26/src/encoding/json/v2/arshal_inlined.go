// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"slices"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
	"encoding/json/jsontext"
)

// 本包支持对 Go 结构体字段进行“内联（inlining）”，
// 即将序列化字段（必须是一个 JSON 对象）的内容
// 视为父 Go 结构体（表示一个 JSON 对象）的一部分。
//
// 通常，内联字段是 Go 结构体类型，嵌套结构体的字段会
// 使用类似于 Go 嵌入（embedding）的规则（但在 JSON 命名空间内运作）
// 虚拟地提升到父结构体中。
//
// 然而，内联字段也可以是带有字符串键的 Go map 类型，
// 或 jsontext.Value。这类内联字段被称为“回退（fallback）”字段，
// 因为它们表示任意的 JSON 对象成员。显式命名的字段
// 优先于内联回退字段。只允许一个内联回退字段。

var errRawInlinedNotObject = errors.New("inlined raw value must be a JSON object")

var jsontextValueType = reflect.TypeFor[jsontext.Value]()

// marshalInlinedFallbackAll 序列化内联回退中的所有成员。
func marshalInlinedFallbackAll(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct, f *structField, insertUnquotedName func([]byte) bool) error {
	v := addressableValue{va.Field(f.index0), va.forcedAddr} // 如果结构体值可寻址，则此值可寻址
	if len(f.index) > 0 {
		v = v.fieldByIndex(f.index, false)
		if !v.IsValid() {
			return nil // 意味着内联字段为 nil
		}
	}
	v = v.indirect(false)
	if !v.IsValid() {
		return nil
	}

	if v.Type() == jsontextValueType {
		b, _ := reflect.TypeAssert[jsontext.Value](v.Value)
		if len(b) == 0 { // TODO: 这里应该是 nil 吗？如果全是空白字符呢？
			return nil
		}

		dec := export.GetBufferedDecoder(b)
		defer export.PutBufferedDecoder(dec)
		xd := export.Decoder(dec)
		xd.Flags.Set(jsonflags.AllowDuplicateNames | jsonflags.AllowInvalidUTF8 | 1)

		tok, err := dec.ReadToken()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return newMarshalErrorBefore(enc, v.Type(), err)
		}
		if tok.Kind() != '{' {
			return newMarshalErrorBefore(enc, v.Type(), errRawInlinedNotObject)
		}
		for dec.PeekKind() != '}' {
			// 解析 JSON 对象名称。
			var flags jsonwire.ValueFlags
			val, err := xd.ReadValue(&flags)
			if err != nil {
				return newMarshalErrorBefore(enc, v.Type(), err)
			}
			if insertUnquotedName != nil {
				name := jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
				if !insertUnquotedName(name) {
					return newDuplicateNameError(enc.StackPointer().Parent(), val, enc.OutputOffset())
				}
			}
			if err := enc.WriteValue(val); err != nil {
				return err
			}

			// 解析 JSON 对象值。
			val, err = xd.ReadValue(&flags)
			if err != nil {
				return newMarshalErrorBefore(enc, v.Type(), err)
			}
			if err := enc.WriteValue(val); err != nil {
				return err
			}
		}
		if _, err := dec.ReadToken(); err != nil {
			return newMarshalErrorBefore(enc, v.Type(), err)
		}
		if err := xd.CheckEOF(); err != nil {
			return newMarshalErrorBefore(enc, v.Type(), err)
		}
		return nil
	} else {
		m := v // 必须是 map[~string]V
		n := m.Len()
		if n == 0 {
			return nil
		}
		mk := newAddressableValue(m.Type().Key())
		mv := newAddressableValue(m.Type().Elem())
		marshalKey := func(mk addressableValue) error {
			b, err := jsonwire.AppendQuote(enc.AvailableBuffer(), mk.String(), &mo.Flags)
			if err != nil {
				return newMarshalErrorBefore(enc, m.Type().Key(), err)
			}
			if insertUnquotedName != nil {
				isVerbatim := bytes.IndexByte(b, '\\') < 0
				name := jsonwire.UnquoteMayCopy(b, isVerbatim)
				if !insertUnquotedName(name) {
					return newDuplicateNameError(enc.StackPointer().Parent(), b, enc.OutputOffset())
				}
			}
			return enc.WriteValue(b)
		}
		marshalVal := f.fncs.marshal
		if mo.Marshalers != nil {
			marshalVal, _ = mo.Marshalers.(*Marshalers).lookup(marshalVal, mv.Type())
		}
		if !mo.Flags.Get(jsonflags.Deterministic) || n <= 1 {
			for iter := m.MapRange(); iter.Next(); {
				mk.SetIterKey(iter)
				if err := marshalKey(mk); err != nil {
					return err
				}
				mv.Set(iter.Value())
				if err := marshalVal(enc, mv, mo); err != nil {
					return err
				}
			}
		} else {
			names := getStrings(n)
			for i, iter := 0, m.Value.MapRange(); i < n && iter.Next(); i++ {
				mk.SetIterKey(iter)
				(*names)[i] = mk.String()
			}
			slices.Sort(*names)
			for _, name := range *names {
				mk.SetString(name)
				if err := marshalKey(mk); err != nil {
					return err
				}
				// TODO(https://go.dev/issue/57061): 使用 mv.SetMapIndexOf。
				mv.Set(m.MapIndex(mk.Value))
				if err := marshalVal(enc, mv, mo); err != nil {
					return err
				}
			}
			putStrings(names)
		}
		return nil
	}
}

// unmarshalInlinedFallbackNext 只反序列化内联回退中的下一个成员。
func unmarshalInlinedFallbackNext(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct, f *structField, quotedName, unquotedName []byte) error {
	v := addressableValue{va.Field(f.index0), va.forcedAddr} // 如果结构体值可寻址，则此值可寻址
	if len(f.index) > 0 {
		v = v.fieldByIndex(f.index, true)
	}
	v = v.indirect(true)

	if v.Type() == jsontextValueType {
		b, _ := reflect.TypeAssert[*jsontext.Value](v.Addr())
		if len(*b) == 0 { // TODO: 这里应该是 nil 吗？如果全是空白字符呢？
			*b = append(*b, '{')
		} else {
			*b = jsonwire.TrimSuffixWhitespace(*b)
			if jsonwire.HasSuffixByte(*b, '}') {
				// TODO: 首次合并到对象中时，
				// 是否应该验证其有效性？
				*b = jsonwire.TrimSuffixByte(*b, '}')
				*b = jsonwire.TrimSuffixWhitespace(*b)
				if !jsonwire.HasSuffixByte(*b, ',') && !jsonwire.HasSuffixByte(*b, '{') {
					*b = append(*b, ',')
				}
			} else {
				return newUnmarshalErrorAfterWithSkipping(dec, v.Type(), errRawInlinedNotObject)
			}
		}
		*b = append(*b, quotedName...)
		*b = append(*b, ':')
		val, err := dec.ReadValue()
		if err != nil {
			return err
		}
		*b = append(*b, val...)
		*b = append(*b, '}')
		return nil
	} else {
		name := string(unquotedName) // TODO: 是否应该对此做驻留（intern）？

		m := v // 必须是 map[~string]V
		if m.IsNil() {
			m.Set(reflect.MakeMap(m.Type()))
		}
		mk := reflect.ValueOf(name)
		if mkt := m.Type().Key(); mkt != stringType {
			mk = mk.Convert(mkt)
		}
		mv := newAddressableValue(m.Type().Elem()) // TODO: 跨调用缓存？
		if v2 := m.MapIndex(mk); v2.IsValid() {
			mv.Set(v2)
		}

		unmarshal := f.fncs.unmarshal
		if uo.Unmarshalers != nil {
			unmarshal, _ = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshal, mv.Type())
		}
		err := unmarshal(dec, mv, uo)
		m.SetMapIndex(mk, mv.Value)
		if err != nil {
			return err
		}
		return nil
	}
}
