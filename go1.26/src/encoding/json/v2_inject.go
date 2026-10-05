// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"encoding/json/internal"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// 将功能注入到 v2 中，以正确处理 v1 类型。
func init() {
	internal.TransformMarshalError = transformMarshalError
	internal.TransformUnmarshalError = transformUnmarshalError
	internal.NewMarshalerError = func(val any, err error, funcName string) error {
		return &MarshalerError{reflect.TypeOf(val), err, funcName}
	}

	internal.NewRawNumber = func() any { return new(Number) }
	internal.RawNumberOf = func(b []byte) any { return Number(b) }
}

func transformMarshalError(root any, err error) error {
	// 历史上，从 Marshal 方法返回的错误会被包装在
	// [MarshalerError] 中。这是由 v2 包通过
	// 在 [ReportErrorsWithLegacySemantics] 下运行时注入的
	// [internal.NewMarshalerError] 构造函数直接执行的。
	// 请注意，来自 Marshal 方法的错误总是被包装，
	// 即使被包装了多层。
	if err, ok := err.(*jsonv2.SemanticError); err != nil {
		if err.Err == nil {
			// 历史上，这仅针对不可序列化的类型报告，
			// 如复数、channel、函数和 unsafe.Pointer。
			return &UnsupportedTypeError{Type: err.GoType}
		} else {
			// 历史上，这仅针对 NaN 或 ±Inf 值
			// 以及值中检测到的循环报告。
			// Val 过去会填充为 reflect.Value，
			// 但现在已不再支持。
			errStr := err.Err.Error()
			if err.Err == internal.ErrCycle && err.GoType != nil {
				errStr += " via " + err.GoType.String()
			}
			errStr = strings.TrimPrefix(errStr, "unsupported value: ")
			return &UnsupportedValueError{Str: errStr}
		}
	} else if ok {
		return (*UnsupportedValueError)(nil)
	}
	if err, _ := err.(*MarshalerError); err != nil {
		err.Err = transformSyntacticError(err.Err)
		return err
	}
	return transformSyntacticError(err)
}

func transformUnmarshalError(root any, err error) error {
	// 历史上，在 [ReportErrorsWithLegacySemantics] 下运行时，
	// 来自 Unmarshal 方法的错误永远不会被包装，而是原样返回。
	if err, ok := err.(*jsonv2.SemanticError); err != nil {
		if err.Err == internal.ErrNonNilReference {
			return &InvalidUnmarshalError{err.GoType}
		}
		if err.Err == jsonv2.ErrUnknownName {
			return fmt.Errorf("json: unknown field %q", err.JSONPointer.LastToken())
		}
		if err.Err == internal.ErrNilInterface {
			err.Err = nil // 出于历史原因不具描述性
		}

		// 历史上，UnmarshalTypeError 在如何报告
		// 位置信息方面一直不一致。
		//
		// Struct 字段现在指向根类型，
		// 而不是路径中的某个中间结构体。
		// 这更好地匹配了该字段基于 Error 消息
		// 格式化方式的原始意图。
		//
		// 为了更接近历史表示，
		// 我们将 JSON 指针的 '/' 分隔表示
		// 切换为使用 '.' 分隔表示。这可能有歧义，
		// 但之前的表示也总是有歧义。
		// 关心精确位置的用户应通过禁用
		// [ReportErrorsWithLegacySemantics] 来使用 v2 错误。
		//
		// Err 字段的引入是 v1 到 v2 迁移的新内容，
		// 使我们能够保留可能由 v2 包
		// 暴露的更强错误信息。
		//
		// 参见 https://go.dev/issue/43126
		var value string
		switch err.JSONKind {
		case 'n', '"', '0':
			value = err.JSONKind.String()
		case 'f', 't':
			value = "bool"
		case '[', ']':
			value = "array"
		case '{', '}':
			value = "object"
		}
		if len(err.JSONValue) > 0 {
			isStrconvError := err.Err == strconv.ErrRange || err.Err == strconv.ErrSyntax
			isNumericKind := func(t reflect.Type) bool {
				if t == nil {
					return false
				}
				switch t.Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
					reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
					reflect.Float32, reflect.Float64:
					return true
				}
				return false
			}
			if isStrconvError && isNumericKind(err.GoType) {
				value = "number"
				if err.JSONKind == '"' {
					err.JSONValue, _ = jsontext.AppendUnquote(nil, err.JSONValue)
				}
				err.Err = nil
			}
			value += " " + string(err.JSONValue)
		}
		var rootName string
		if t := reflect.TypeOf(root); t != nil && err.JSONPointer != "" {
			if t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			rootName = t.Name()
		}
		fieldPath := string(err.JSONPointer)
		fieldPath = strings.TrimPrefix(fieldPath, "/")
		fieldPath = strings.ReplaceAll(fieldPath, "/", ".")
		return &UnmarshalTypeError{
			Value:  value,
			Type:   err.GoType,
			Offset: err.ByteOffset,
			Struct: rootName,
			Field:  fieldPath,
			Err:    transformSyntacticError(err.Err),
		}
	} else if ok {
		return (*UnmarshalTypeError)(nil)
	}
	return transformSyntacticError(err)
}
