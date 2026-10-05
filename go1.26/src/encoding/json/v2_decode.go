// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

// 使用原生 Go 类型表示 JSON 数据结构：布尔值、浮点数、
// 字符串、数组和 map。

package json

import (
	"cmp"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"encoding/json/internal/jsonwire"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// Unmarshal 解析 JSON 编码的数据，并将结果存储到
// v 指向的值中。如果 v 为 nil 或不是指针，
// Unmarshal 返回 [InvalidUnmarshalError]。
//
// Unmarshal 使用 [Marshal] 所使用编码的逆编码，
// 在必要时分配 map、切片和指针，并遵循以下附加规则：
//
// 要将 JSON 反序列化到指针中，Unmarshal 首先处理
// JSON 为 JSON 字面量 null 的情况。在这种情况下，Unmarshal 将
// 该指针设置为 nil。否则，Unmarshal 将 JSON 反序列化到
// 指针所指向的值中。如果指针为 nil，Unmarshal
// 会分配一个新值供其指向。
//
// 要将 JSON 反序列化到实现 [Unmarshaler] 的值中，
// Unmarshal 会调用该值的 [Unmarshaler.UnmarshalJSON] 方法，
// 包括当输入为 JSON null 时。
// 否则，如果该值实现了 [encoding.TextUnmarshaler]
// 且输入是 JSON 引号字符串，Unmarshal 会使用
// 该字符串的去引号形式调用 [encoding.TextUnmarshaler.UnmarshalText]。
//
// 要将 JSON 反序列化到结构体中，Unmarshal 将传入的对象键
// 与 [Marshal] 使用的键（结构体字段名或其标签）进行匹配，
// 优先精确匹配，但也接受不区分大小写的匹配。默认情况下，
// 没有对应结构体字段的对象键会被忽略
// （另一种方式请参见 [Decoder.DisallowUnknownFields]）。
//
// 要将 JSON 反序列化到接口值中，
// Unmarshal 会将以下值之一存储到接口值中：
//
//   - bool，用于 JSON 布尔值
//   - float64，用于 JSON 数字
//   - string，用于 JSON 字符串
//   - []any，用于 JSON 数组
//   - map[string]any，用于 JSON 对象
//   - nil，用于 JSON null
//
// 要将 JSON 数组反序列化到切片中，Unmarshal 将切片长度
// 重置为零，然后将每个元素追加到切片中。
// 作为一种特殊情况，要将空 JSON 数组反序列化到切片中，
// Unmarshal 会用一个新的空切片替换该切片。
//
// 要将 JSON 数组反序列化到 Go 数组中，Unmarshal 将
// JSON 数组元素解码到对应的 Go 数组元素中。
// 如果 Go 数组比 JSON 数组小，
// 多余的 JSON 数组元素会被丢弃。
// 如果 JSON 数组比 Go 数组小，
// 多余的 Go 数组元素会被设置为零值。
//
// 要将 JSON 对象反序列化到 map 中，Unmarshal 首先建立一个
// 要使用的 map。如果 map 为 nil，Unmarshal 分配一个新 map。
// 否则 Unmarshal 重用现有 map，保留现有条目。
// 然后 Unmarshal 将 JSON 对象中的键值对存储到 map 中。
// map 的键类型必须是任意字符串类型、整数，
// 或实现 [encoding.TextUnmarshaler]。
//
// 如果 JSON 编码的数据包含语法错误，Unmarshal 返回 [SyntaxError]。
//
// 如果 JSON 值不适合给定的目标类型，
// 或 JSON 数字溢出目标类型，Unmarshal
// 会跳过该字段并尽其所能完成反序列化。
// 如果没有遇到更严重的错误，Unmarshal 返回
// 描述最早此类错误的 [UnmarshalTypeError]。
// 在任何情况下，都不能保证问题字段之后的
// 所有剩余字段都会被反序列化到目标对象中。
//
// JSON null 值通过将该 Go 值设置为 nil 来
// 反序列化到接口、map、指针或切片中。
// 因为 null 在 JSON 中常用于表示"不存在"，
// 将 JSON null 反序列化到任何其他 Go 类型中
// 对该值没有影响，也不会产生错误。
//
// 在反序列化引号字符串时，无效的 UTF-8 或
// 无效的 UTF-16 代理对不会被视为错误。
// 相反，它们会被替换为 Unicode 替换
// 字符 U+FFFD。
func Unmarshal(data []byte, v any) error {
	return jsonv2.Unmarshal(data, v, DefaultOptionsV1())
}

// Unmarshaler 是由能够反序列化自身 JSON 描述的类型
// 实现的接口。
// 输入可以假定为某个 JSON 值的有效编码。
// 如果 UnmarshalJSON 希望在返回后保留数据，
// 则必须复制该 JSON 数据。
type Unmarshaler = jsonv2.Unmarshaler

// UnmarshalTypeError 描述了一个不适合
// 特定 Go 类型值的 JSON 值。
type UnmarshalTypeError struct {
	Value  string       // JSON 值的描述 - "bool"、"array"、"number -5"
	Type   reflect.Type // 无法赋给的 Go 值的类型
	Offset int64        // 错误发生在读取 Offset 个字节之后
	Struct string       // 包含该字段的根类型的名称
	Field  string       // 从根节点到该值的完整路径
	Err    error        // 可能为 nil
}

func (e *UnmarshalTypeError) Error() string {
	var s string
	if e.Struct != "" || e.Field != "" {
		// UnmarshalTypeError 的设计过度假设了
		// JSON 值基于结构体的 Go 表示。
		// jsontext 中的逻辑使用 JSON Pointer 表示路径，
		// 它与 Go 类型系统无关。
		// 试图将 JSON Pointer 转换为 UnmarshalTypeError.Field
		// 是困难的。作为启发式，如果最后一个路径标记看起来
		// 像是 JSON 数组的索引（例如 ".foo.bar.0"），
		// 则避免使用短语 "Go struct field "。
		intoWhat := "Go struct field "
		i := strings.LastIndexByte(e.Field, '.') + len(".")
		if len(e.Field[i:]) > 0 && strings.TrimRight(e.Field[i:], "0123456789") == "" {
			intoWhat = "" // 可能是 Go 切片或数组
		}
		s = "json: cannot unmarshal " + e.Value + " into " + intoWhat + e.Struct + "." + e.Field + " of type " + e.Type.String()
	} else {
		s = "json: cannot unmarshal " + e.Value + " into Go value of type " + e.Type.String()
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *UnmarshalTypeError) Unwrap() error {
	return e.Err
}

// UnmarshalFieldError 描述了一个导致指向未导出
// （因此不可写）结构体字段的 JSON 对象键。
//
// Deprecated: 不再使用；为兼容性保留。
type UnmarshalFieldError struct {
	Key   string
	Type  reflect.Type
	Field reflect.StructField
}

func (e *UnmarshalFieldError) Error() string {
	return "json: cannot unmarshal object key " + strconv.Quote(e.Key) + " into unexported field " + e.Field.Name + " of type " + e.Type.String()
}

// InvalidUnmarshalError 描述传递给 [Unmarshal] 的无效参数。
// （[Unmarshal] 的参数必须是非 nil 指针。）
type InvalidUnmarshalError struct {
	Type reflect.Type
}

func (e *InvalidUnmarshalError) Error() string {
	if e.Type == nil {
		return "json: Unmarshal(nil)"
	}

	if e.Type.Kind() != reflect.Pointer {
		return "json: Unmarshal(non-pointer " + e.Type.String() + ")"
	}
	return "json: Unmarshal(nil " + e.Type.String() + ")"
}

// Number 表示一个 JSON 数字字面量。
type Number string

// String 返回该数字的字面文本。
func (n Number) String() string { return string(n) }

// Float64 将该数字作为 float64 返回。
func (n Number) Float64() (float64, error) {
	return strconv.ParseFloat(string(n), 64)
}

// Int64 将该数字作为 int64 返回。
func (n Number) Int64() (int64, error) {
	return strconv.ParseInt(string(n), 10, 64)
}

var numberType = reflect.TypeFor[Number]()

// MarshalJSONTo 实现 [jsonv2.MarshalerTo]。
func (n Number) MarshalJSONTo(enc *jsontext.Encoder) error {
	opts := enc.Options()
	stringify, _ := jsonv2.GetOption(opts, jsonv2.StringifyNumbers)
	if k, n := enc.StackIndex(enc.StackDepth()); k == '{' && n%2 == 0 {
		stringify = true // 期望一个 JSON 对象名称
	}
	n = cmp.Or(n, "0")
	var num []byte
	val := enc.AvailableBuffer()
	if stringify {
		val = append(val, '"')
		val = append(val, n...)
		val = append(val, '"')
		num = val[len(`"`) : len(val)-len(`"`)]
	} else {
		val = append(val, n...)
		num = val
	}
	if n, err := jsonwire.ConsumeNumber(num); n != len(num) || err != nil {
		return fmt.Errorf("cannot parse %q as JSON number: %w", val, strconv.ErrSyntax)
	}
	return enc.WriteValue(val)
}

// UnmarshalJSONFrom 实现 [jsonv2.UnmarshalerFrom]。
func (n *Number) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	opts := dec.Options()
	stringify, _ := jsonv2.GetOption(opts, jsonv2.StringifyNumbers)
	if k, n := dec.StackIndex(dec.StackDepth()); k == '{' && n%2 == 0 {
		stringify = true // 期望一个 JSON 对象名称
	}
	val, err := dec.ReadValue()
	if err != nil {
		return err
	}
	val0 := val
	k := val.Kind()
	switch k {
	case 'n':
		if legacy, _ := jsonv2.GetOption(opts, MergeWithLegacySemantics); !legacy {
			*n = ""
		}
		return nil
	case '"':
		verbatim := jsonwire.ConsumeSimpleString(val) == len(val)
		val = jsonwire.UnquoteMayCopy(val, verbatim)
		if n, err := jsonwire.ConsumeNumber(val); n != len(val) || err != nil {
			return &jsonv2.SemanticError{JSONKind: val0.Kind(), JSONValue: val0.Clone(), GoType: numberType, Err: strconv.ErrSyntax}
		}
		*n = Number(val)
		return nil
	case '0':
		if stringify {
			break
		}
		*n = Number(val)
		return nil
	}
	return &jsonv2.SemanticError{JSONKind: k, GoType: numberType}
}
