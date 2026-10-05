// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

// 包 json 实现了 RFC 7159 中定义的 JSON 的编码和解码。
// JSON 与 Go 值之间的映射在 Marshal 和 Unmarshal 函数的
// 文档中描述。
//
// 有关本包的介绍，请参见 "JSON and Go"：
// https://golang.org/doc/articles/json_and_go.html
//
// # 安全注意事项
//
// 请参见 [encoding/json/v2] 中的"安全注意事项"一节。
//
// 出于历史原因，v1 [encoding/json] 的默认行为
// 不幸地采用了安全性较低的默认值。
// 鼓励 Go 中新的 JSON 用法改用 [encoding/json/v2]。
package json

import (
	"reflect"
	"strconv"

	jsonv2 "encoding/json/v2"
)

// Marshal 返回 v 的 JSON 编码。
//
// Marshal 递归地遍历值 v。
// 如果遇到的值实现了 [Marshaler]
// 且不是 nil 指针，Marshal 调用 [Marshaler.MarshalJSON]
// 来生成 JSON。如果不存在 [Marshaler.MarshalJSON] 方法，
// 但该值实现了 [encoding.TextMarshaler]，Marshal 调用
// [encoding.TextMarshaler.MarshalText] 并将结果编码为 JSON 字符串。
// nil 指针的例外并非严格必要，
// 但模仿了 [Unmarshaler.UnmarshalJSON] 行为中类似的必要例外。
//
// 否则，Marshal 使用以下与类型相关的默认编码：
//
// 布尔值编码为 JSON 布尔值。
//
// 浮点、整数和 [Number] 值编码为 JSON 数字。
// NaN 和 +/-Inf 值将返回 [UnsupportedValueError]。
//
// 字符串值编码为被强制转换为有效 UTF-8 的 JSON 字符串，
// 无效字节被替换为 Unicode 替换符文。
// 为了使 JSON 可以安全地嵌入到 HTML <script> 标签内，
// 字符串使用 [HTMLEscape] 进行编码，
// 它将 "<"、">"、"&"、U+2028 和 U+2029 转义为
// "\u003c"、"\u003e"、"\u0026"、"\u2028" 和 "\u2029"。
// 在使用 [Encoder] 时，可以通过调用 [Encoder.SetEscapeHTML](false)
// 来禁用此替换。
//
// 数组和切片值编码为 JSON 数组，
// 但 []byte 编码为 base64 编码的字符串，
// nil 切片编码为 JSON null 值。
//
// 结构体值编码为 JSON 对象。
// 每个导出的结构体字段成为对象的一个成员，
// 使用字段名作为对象键，
// 除非该字段因下述原因之一被省略。
//
// 每个结构体字段的编码可以通过存储在结构体字段标签中
// "json" 键下的格式字符串来自定义。
// 格式字符串给出字段的名称，后面可以跟一个逗号分隔的
// 选项列表。名称可以为空，以便在不覆盖默认字段名的情况下
// 指定选项。
//
// "omitempty" 选项指定：如果字段具有空值
// （定义为 false、0、nil 指针、nil 接口值，
// 以及任何长度为零的数组、切片、map 或字符串），
// 则该字段应从编码中省略。
//
// 作为一种特殊情况，如果字段标签为 "-"，则该字段始终被省略。
// 包含逗号或引号的 JSON 名称，或与 "" 或 "-" 相同的名称，
// 可以使用单引号字符串字面量来指定，
// 其语法与 Go 中双引号字符串字面量的语法相同，
// 但改为使用单引号作为分隔符。
//
// 结构体字段标签及其含义的示例：
//
//	// 字段在 JSON 中以键 "myName" 出现。
//	Field int `json:"myName"`
//
//	// 字段在 JSON 中以键 "myName" 出现，
//	// 且如果字段的值为空（如上定义），
//	// 则从对象中省略该字段。
//	Field int `json:"myName,omitempty"`
//
//	// 字段在 JSON 中以键 "Field"（默认值）出现，
//	// 但如果为空则跳过该字段。
//	// 注意前导逗号。
//	Field int `json:",omitempty"`
//
//	// 字段被本包忽略。
//	Field int `json:"-"`
//
//	// 字段在 JSON 中以键 "-" 出现。
//	Field int `json:"'-'"`
//
// "omitzero" 选项指定：如果字段具有零值，
// 则根据以下规则从编码中省略该字段：
//
//  1. 如果字段类型具有 "IsZero() bool" 方法，
//     则使用该方法来确定值是否为零。
//
// 2) 否则，如果值是其类型的零值，则该值为零。
//
// 如果同时指定了 "omitempty" 和 "omitzero"，
// 则如果值为空或为零（或两者皆是），该字段将被省略。
//
// "string" 选项表示字段以 JSON 编码字符串内的 JSON 形式存储。
// 它仅适用于字符串、浮点、整数或布尔类型的字段。
// 这种额外的编码层级有时用于与 JavaScript 程序通信：
//
//	Int64String int64 `json:",string"`
//
// 如果键名是非空字符串，且仅由 Unicode 字母、数字和
// ASCII 标点（除引号、反斜杠和逗号外）组成，则会使用该键名。
//
// 内嵌结构体字段通常被 marshal，就好像其内部导出字段
// 是外部结构体中的字段一样，这受通常的 Go 可见性规则约束，
// 并按下一段所述进行修正。
// 在其 JSON 标签中给出名称的匿名字段结构体被视为
// 具有该名称，而不是匿名的。
// 接口类型的匿名结构体字段被视为
// 具有该类型作为其名称，而不是匿名的。
//
// 在决定要 marshal 或 unmarshal 哪个字段时，
// Go 对结构体字段的可见性规则针对 JSON 进行了修正。
// 如果在同一层级存在多个字段，且该层级是嵌套最少的
// （因此会是通常 Go 规则选中的嵌套层级），
// 则适用以下额外规则：
//
//  1. 在这些字段中，如果有任何字段带有 JSON 标签，则仅考虑带标签的字段，
//     即使存在多个否则会冲突的不带标签字段。
//
// 2) 如果恰好有一个字段（根据第一条规则带标签或不带标签），则选中该字段。
//
// 3) 否则存在多个字段，全部被忽略；不发生错误。
//
// 匿名结构体字段的处理是 Go 1.1 中新增的。
// 在 Go 1.1 之前，匿名结构体字段会被忽略。
// 要在当前和更早的版本中强制忽略匿名结构体字段，
// 请为该字段指定 JSON 标签 "-"。
//
// Map 值编码为 JSON 对象。map 的键类型必须是
// 字符串、整数类型，或实现 [encoding.TextMarshaler]。
// map 键会被排序，并通过应用以下规则
// （受上述字符串值所描述的 UTF-8 强制转换约束）用作 JSON 对象键：
//   - 任意字符串类型的键直接使用
//   - 实现 [encoding.TextMarshaler] 的键会被 marshal
//   - 整数键会被转换为字符串
//
// 指针值编码为其所指向的值。
// nil 指针编码为 JSON null 值。
//
// 接口值编码为接口中包含的值。
// nil 接口值编码为 JSON null 值。
//
// Channel、复数和函数值无法在 JSON 中编码。
// 尝试编码此类值会导致 Marshal 返回
// [UnsupportedTypeError]。
//
// JSON 无法表示循环数据结构，Marshal 也不会
// 处理它们。将循环结构传递给 Marshal 会导致错误。
func Marshal(v any) ([]byte, error) {
	return jsonv2.Marshal(v, DefaultOptionsV1())
}

// MarshalIndent 类似于 [Marshal]，但会应用 [Indent] 来格式化输出。
// 输出中的每个 JSON 元素都会从以 prefix 开头的新行开始，
// 后跟一个或多个 indent 副本（根据缩进嵌套层级而定）。
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	b, err = appendIndent(nil, b, prefix, indent)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Marshaler 是由能够将自身 marshal 为有效 JSON 的类型
// 实现的接口。
type Marshaler = jsonv2.Marshaler

// UnsupportedTypeError 由 [Marshal] 在尝试
// 编码不支持的值类型时返回。
type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return "json: unsupported type: " + e.Type.String()
}

// UnsupportedValueError 由 [Marshal] 在尝试
// 编码不支持的值时返回。
type UnsupportedValueError struct {
	Value reflect.Value
	Str   string
}

func (e *UnsupportedValueError) Error() string {
	return "json: unsupported value: " + e.Str
}

// 在 Go 1.2 之前，当 [Marshal] 尝试编码
// 具有无效 UTF-8 序列的字符串值时，会返回 InvalidUTF8Error。
// 从 Go 1.2 开始，[Marshal] 改为通过
// 将无效字节替换为 Unicode 替换符文 U+FFFD，
// 将字符串强制转换为有效 UTF-8。
//
// Deprecated: 不再使用；为兼容性保留。
type InvalidUTF8Error struct {
	S string // 导致错误的整个字符串值
}

func (e *InvalidUTF8Error) Error() string {
	return "json: invalid UTF-8 in string: " + strconv.Quote(e.S)
}

// MarshalerError 表示调用 [Marshaler.MarshalJSON] 或
// [encoding.TextMarshaler.MarshalText] 方法时产生的错误。
type MarshalerError struct {
	Type       reflect.Type
	Err        error
	sourceFunc string
}

func (e *MarshalerError) Error() string {
	srcFunc := e.sourceFunc
	if srcFunc == "" {
		srcFunc = "MarshalJSON"
	}
	return "json: error calling " + srcFunc +
		" for type " + e.Type.String() +
		": " + e.Err.Error()
}

// Unwrap 返回底层错误。
func (e *MarshalerError) Unwrap() error { return e.Err }
