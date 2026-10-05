// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

// 迁移到 v2
//
// 本包（即 [encoding/json]）现正式称为 v1 包，
// 因为 v2 包现已存在于 [encoding/json/v2]。
// v1 包的所有行为都是通过 v2 包实现的，
// 并指定了一组适当的选项来保留 v1 的历史行为。
//
// [jsonv2.Marshal] 函数是 v1 [Marshal] 的较新等效项。
// [jsonv2.Unmarshal] 函数是 v1 [Unmarshal] 的较新等效项。
// v2 函数与 v1 等效项具有相同的调用签名，
// 但它们接收可变参数 [Options]，可通过指定这些参数
// 来改变 marshal 或 unmarshal 的行为。v1 和 v2 通常
// 行为相似，但存在一些显著差异。
//
// 以下是 v1 和 v2 之间的差异列表：
//
//   - 在 v1 中，JSON 对象成员使用不区分大小写的名称匹配
//     （与字段的 JSON 名称匹配）来反序列化为 Go 结构体。
//     相比之下，v2 使用精确的、区分大小写的匹配来匹配字段。
//     [jsonv2.MatchCaseInsensitiveNames] 和 [MatchCaseSensitiveDelimiter]
//     选项控制此行为差异。要显式指定某个 Go 结构体字段
//     使用特定的名称匹配方案，可以指定 `case:ignore` 或
//     `case:strict` 字段选项。
//     字段指定的选项优先于调用者指定的选项。
//
//   - 在 v1 中，对 Go 结构体进行 marshal 时，标记为 `omitempty`
//     的字段在字段值为"空"的 Go 值时会被省略，"空"的 Go 值定义为
//     false、0、nil 指针、nil 接口值，
//     以及任何空数组、切片、map 或字符串。相比之下，v2 重新定义了
//     `omitempty`：如果字段编码为"空"的 JSON 值，则省略该字段，
//     "空"的 JSON 值定义为 JSON null，或空的 JSON 字符串、对象或数组。
//     [OmitEmptyWithLegacySemantics] 选项控制此行为差异。
//     请注意，对于 Go 数组、切片、map 或字符串，
//     `omitempty` 在 v1 和 v2 中行为完全相同
//     （假设没有用户定义的 MarshalJSON 方法覆盖默认表示）。
//     现有对 Go bool、数字、指针或接口值使用 `omitempty` 的地方
//     应迁移为指定 `omitzero`（在 v1 和 v2 中均相同地支持）。
//
//   - 在 v1 中，标记为 `string` 的 Go 结构体字段可用于将
//     Go 字符串、bool 或数字加引号为 JSON 字符串。它不会递归地
//     对复合 Go 类型生效。相比之下，v2 将 `string` 选项限制为
//     仅将 Go 数字加引号为 JSON 字符串。
//     它会递归地对复合 Go 类型内的 Go 数字生效。
//     [StringifyWithLegacySemantics] 选项控制此行为差异。
//
//   - 在 v1 中，nil 的 Go 切片或 Go map 被 marshal 为 JSON null。
//     相比之下，v2 将 nil 的 Go 切片或 Go map 分别
//     marshal 为空 JSON 数组或 JSON 对象。
//     [jsonv2.FormatNilSliceAsNull] 和 [jsonv2.FormatNilMapAsNull] 选项
//     控制此行为差异。要显式指定某个 Go 结构体字段
//     对 nil 使用特定的表示，可以指定 `format:emitempty` 或
//     `format:emitnull` 字段选项。
//     字段指定的选项优先于调用者指定的选项。
//
//   - 在 v1 中，Go 数组可以从任意长度的 JSON 数组反序列化。
//     相比之下，在 v2 中，Go 数组必须从相同长度的 JSON 数组
//     反序列化，否则会导致错误。
//     [UnmarshalArrayFromAnyLength] 选项控制此行为差异。
//
//   - 在 v1 中，Go 字节数组被表示为 JSON 数字数组。
//     相比之下，在 v2 中，Go 字节数组被表示为 Base64 编码的 JSON 字符串。
//     [FormatByteArrayAsArray] 选项控制此行为差异。
//     要显式指定某个 Go 结构体字段使用特定的表示，
//     可以指定 `format:array` 或 `format:base64` 字段选项。
//     字段指定的选项优先于调用者指定的选项。
//
//   - 在 v1 中，声明在指针接收者上的 MarshalJSON 方法仅在
//     Go 值可寻址时才会被调用。相比之下，在 v2 中，MarshalJSON 方法
//     无论是否可寻址都始终可调用。
//     [CallMethodsWithLegacySemantics] 选项控制此行为差异。
//
//   - 在 v1 中，MarshalJSON 和 UnmarshalJSON 方法永远不会为 Go map 键调用。
//     相比之下，在 v2 中，MarshalJSON 或 UnmarshalJSON 方法有资格
//     为 Go map 键调用。
//     [CallMethodsWithLegacySemantics] 选项控制此行为差异。
//
//   - 在 v1 中，Go map 以确定性的顺序 marshal。
//     相比之下，在 v2 中，Go map 以非确定性的顺序 marshal。
//     [jsonv2.Deterministic] 选项控制此行为差异。
//
//   - 在 v1 中，JSON 字符串会被编码，其中特定于 HTML 或 JavaScript 的
//     字符会被转义。相比之下，在 v2 中，JSON 字符串使用最小化编码，
//     仅在 JSON 语法要求时才转义。
//     [jsontext.EscapeForHTML] 和 [jsontext.EscapeForJS] 选项
//     控制此行为差异。
//
//   - 在 v1 中，字符串内无效 UTF-8 的字节会被静默替换为
//     Unicode 替换字符。相比之下，在 v2 中，无效 UTF-8 的存在
//     会导致错误。[jsontext.AllowInvalidUTF8] 选项
//     控制此行为差异。
//
//   - 在 v1 中，允许具有重复名称的 JSON 对象。
//     相比之下，在 v2 中，具有重复名称的 JSON 对象会导致错误。
//     [jsontext.AllowDuplicateNames] 选项控制此行为差异。
//
//   - 在 v1 中，将 JSON null 反序列化为非空 Go 值时，会
//     不一致地要么清零该值，要么什么都不做。
//     相比之下，在 v2 中，反序列化 JSON null 会一致且始终
//     清零底层的 Go 值。[MergeWithLegacySemantics] 选项
//     控制此行为差异。
//
//   - 在 v1 中，将 JSON 值反序列化为非零 Go 值时，
//     会对数组元素、切片元素、结构体字段（但不包括 map 值）、
//     指针值和接口值（仅当为非 nil 指针时）合并到原始 Go 值中。
//     相比之下，在 v2 中，反序列化会对结构体字段、map 值、
//     指针值和接口值合并到 Go 值中。
//     一般来说，v2 语义在反序列化 JSON 对象时进行合并，
//     否则会替换该值。[MergeWithLegacySemantics] 选项
//     控制此行为差异。
//
//   - 在 v1 中，[time.Duration] 被表示为一个 JSON 数字，
//     包含以纳秒为单位的十进制数。相比之下，在 v2 中，[time.Duration]
//     没有默认表示，并会导致运行时错误。
//     [FormatDurationAsNano] 选项控制此行为差异。
//     要显式指定某个 Go 结构体字段使用特定的表示，
//     可以指定 `format:nano` 或 `format:units` 字段选项。
//     字段指定的选项优先于调用者指定的选项。
//
//   - 在 v1 中，对于存在某种形式结构错误（例如格式错误的标签选项）
//     的 Go 结构体类型，永远不会在运行时报告错误。
//     相比之下，v2 对与 JSON 序列化相关的无效 Go 类型
//     会报告运行时错误。例如，仅包含未导出字段的 Go 结构体
//     无法被序列化。
//     [ReportErrorsWithLegacySemantics] 选项控制此行为差异。
//
// 如前所述，整个 v1 包都是通过 v2 实现的，
// 其中会隐式指定选项以启用旧版行为。
// 例如，[Marshal] 直接使用 [DefaultOptionsV1] 调用 [jsonv2.Marshal]。
// 类似地，[Unmarshal] 直接使用 [DefaultOptionsV1] 调用 [jsonv2.Unmarshal]。
// [DefaultOptionsV1] 选项代表了所有指定默认 v1 行为的选项的集合。
//
// 对于许多行为差异，存在一些 Go 结构体字段选项，
// Go 类型的作者可以指定它们来控制行为，
// 使得该类型在 v1 或 v2 语义下在 JSON 中的表示完全相同。
//
// [DefaultOptionsV1] 和 [jsonv2.DefaultOptionsV2] 的可用性，
// 配合"后者选项优先于前者选项"的规则，允许从 v1 到 v2 的逐步迁移。
// 例如：
//
//   - jsonv1.Marshal(v)
//     使用默认 v1 语义。
//
//   - jsonv2.Marshal(v, jsonv1.DefaultOptionsV1())
//     在语义上等同于 jsonv1.Marshal，
//     因此使用默认 v1 语义。
//
//   - jsonv2.Marshal(v, jsonv1.DefaultOptionsV1(), jsontext.AllowDuplicateNames(false))
//     大多使用 v1 语义，但启用了一种特定的 v2 行为。
//
//   - jsonv2.Marshal(v, jsonv1.CallMethodsWithLegacySemantics(true))
//     大多使用 v2 语义，但启用了一种特定的 v1 行为。
//
//   - jsonv2.Marshal(v, ..., jsonv2.DefaultOptionsV2())
//     在语义上等同于 jsonv2.Marshal，因为
//     jsonv2.DefaultOptionsV2 会覆盖之前指定的任何选项，
//     因此使用默认 v2 语义。
//
//   - jsonv2.Marshal(v)
//     使用默认 v2 语义。
//
// Go 中所有新的 "json" 用法都应使用 v2 包，
// 但 v1 包将永远得到支持。
package json

// TODO(https://go.dev/issue/71631): 更新 "Migrating to v2" 文档，
// 补充 [time.Duration] 的 v2 默认行为。

import (
	"encoding"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// 引用 encoding、jsonv2 和 jsontext 包，以帮助 pkgsite
// 能够对这些包的引用进行热链接。
var (
	_ encoding.TextMarshaler
	_ encoding.TextUnmarshaler
	_ jsonv2.Options
	_ jsontext.Options
)

// Options 是一组选项，用于配置 v2 "json" 包
// 使其对特定功能以 v1 语义运行。
// 此类型的值可以传递给 v2 函数，
// 如 [jsonv2.Marshal] 或 [jsonv2.Unmarshal]。
// 请使用 [jsonv2.Options]，而不是引用此类型。
//
// 有关如何将 "json" 的用法从使用 v1 迁移到使用 v2 的指导，
// 请参见"迁移到 v2"一节。
type Options = jsonopts.Options

// DefaultOptionsV1 是定义 v1 语义的所有选项的完整集合。
// 它等同于将以下布尔选项设置为 true：
//
//   - [CallMethodsWithLegacySemantics]
//   - [FormatByteArrayAsArray]
//   - [FormatBytesWithLegacySemantics]
//   - [FormatDurationAsNano]
//   - [MatchCaseSensitiveDelimiter]
//   - [MergeWithLegacySemantics]
//   - [OmitEmptyWithLegacySemantics]
//   - [ParseBytesWithLooseRFC4648]
//   - [ParseTimeWithLooseRFC3339]
//   - [ReportErrorsWithLegacySemantics]
//   - [StringifyWithLegacySemantics]
//   - [UnmarshalArrayFromAnyLength]
//   - [jsonv2.Deterministic]
//   - [jsonv2.FormatNilMapAsNull]
//   - [jsonv2.FormatNilSliceAsNull]
//   - [jsonv2.MatchCaseInsensitiveNames]
//   - [jsontext.AllowDuplicateNames]
//   - [jsontext.AllowInvalidUTF8]
//   - [jsontext.EscapeForHTML]
//   - [jsontext.EscapeForJS]
//   - [jsontext.PreserveRawStrings]
//
// 所有其他选项均不存在。
//
// 本包中的 [Marshal] 和 [Unmarshal] 函数
// 在语义上等同于使用此选项调用 v2 的等效项：
//
//	jsonv2.Marshal(v, jsonv1.DefaultOptionsV1())
//	jsonv2.Unmarshal(b, v, jsonv1.DefaultOptionsV1())
func DefaultOptionsV1() Options {
	return &jsonopts.DefaultOptionsV1
}

// CallMethodsWithLegacySemantics 指定对类型提供的
// marshal 和 unmarshal 方法的调用遵循旧版语义：
//
//   - 在 marshal 时，声明在指针接收者上的 marshal 方法
//     仅在 Go 值可寻址时才被调用。
//     从接口或 map 元素获得的值不可寻址。
//     从指针或切片元素获得的值可寻址。
//     从数组元素或结构体字段获得的值继承
//     父级的可寻址性。相比之下，v2 语义是
//     无论是否可寻址都始终调用 marshal 方法。
//
//   - 在 marshal 或 unmarshal 时，对 map 键忽略 [Marshaler] 或 [Unmarshaler]
//     方法。然而，[encoding.TextMarshaler] 或 [encoding.TextUnmarshaler]
//     仍然可调用。
//     相比之下，v2 语义是像对待任何其他值一样序列化 map 键
//     （就调用方法而言），
//     这可能包括调用 [Marshaler] 或 [Unmarshaler] 方法，
//     此时由实现负责将 Go 值表示为 JSON 字符串
//     （JSON 对象名称所要求的）。
//
//   - 在 marshal 时，如果 map 键值实现了 marshal 方法
//     且为 nil 指针，则将其序列化为空 JSON 字符串。
//     相比之下，v2 语义是报告错误。
//
//   - 在 marshal 时，如果接口类型实现了 marshal 方法
//     且接口值是指向具体类型的 nil 指针，
//     则始终调用该 marshal 方法。
//     相比之下，v2 语义是永远不直接在接口值上调用方法，
//     而是根据底层具体值推迟求值。
//     与非接口值类似，marshal 方法不会在 nil 指针上调用，
//     而是被序列化为 JSON null。
//
// 这会影响 marshal 或 unmarshal。
// v1 默认值为 true。
func CallMethodsWithLegacySemantics(v bool) Options {
	if v {
		return jsonflags.CallMethodsWithLegacySemantics | 1
	} else {
		return jsonflags.CallMethodsWithLegacySemantics | 0
	}
}

// FormatByteArrayAsArray 指定 Go [N]byte 被格式化为
// 普通的 Go 数组，这与 v2 默认将 [N]byte 格式化为
// 使用二进制数据编码（RFC 4648）不同。
// 如果结构体字段具有 `format` 标签选项，
// 则指定的格式化优先。
//
// 这会影响 marshal 或 unmarshal。
// v1 默认值为 true。
func FormatByteArrayAsArray(v bool) Options {
	if v {
		return jsonflags.FormatByteArrayAsArray | 1
	} else {
		return jsonflags.FormatByteArrayAsArray | 0
	}
}

// FormatBytesWithLegacySemantics 指定对
// []~byte 和 [N]~byte 类型的处理遵循旧版语义：
//
//   - Go []~byte 应被视为使用某种形式的
//     二进制数据编码（RFC 4648），这与 v2 默认
//     仅将 []byte 视为如此不同。特别是，v2 不会
//     将命名字节类型的切片视为表示二进制数据。
//
//   - 在 marshal 时，如果命名字节实现了 marshal 方法，
//     则切片被序列化为元素的 JSON 数组，
//     每个元素都调用该 marshal 方法。
//
//   - 在 unmarshal 时，如果输入是 JSON 数组，
//     则将其反序列化到 []~byte 中，就像它是普通的 Go 切片一样。
//     相比之下，v2 默认在期望某种形式的二进制数据编码时
//     对 JSON 数组进行 unmarshal 会报告错误。
//
// 这会影响 marshal 或 unmarshal。
// v1 默认值为 true。
func FormatBytesWithLegacySemantics(v bool) Options {
	if v {
		return jsonflags.FormatBytesWithLegacySemantics | 1
	} else {
		return jsonflags.FormatBytesWithLegacySemantics | 0
	}
}

// FormatDurationAsNano 指定 [time.Duration] 被格式化为
// 表示纳秒数的 JSON 数字，这与 v2 默认报告错误不同。
// 如果 duration 字段具有 `format` 标签选项，
// 则指定的格式化优先。
//
// 这会影响 marshal 或 unmarshal。
// v1 默认值为 true。
func FormatDurationAsNano(v bool) Options {
	// TODO(https://go.dev/issue/71631): 更新文档以说明 v2 行为。
	if v {
		return jsonflags.FormatDurationAsNano | 1
	} else {
		return jsonflags.FormatDurationAsNano | 0
	}
}

// MatchCaseSensitiveDelimiter 指定在执行不区分大小写的名称匹配时
// 不忽略下划线和短横线，该匹配在 [jsonv2.MatchCaseInsensitiveNames]
// 或 `case:ignore` 标签选项下进行。
// 因此，不区分大小写的名称匹配等同于 [strings.EqualFold]。
// 使用此选项会削弱不区分大小写匹配
// 匹配常见大小写变体的能力（例如，"foo_bar" 与 "fooBar"）。
//
// 这会影响 marshal 或 unmarshal。
// v1 默认值为 true。
func MatchCaseSensitiveDelimiter(v bool) Options {
	if v {
		return jsonflags.MatchCaseSensitiveDelimiter | 1
	} else {
		return jsonflags.MatchCaseSensitiveDelimiter | 0
	}
}

// MergeWithLegacySemantics 指定反序列化到非零 Go 值时
// 遵循旧版语义：
//
//   - 在反序列化 JSON null 时，如果 Go 值的 kind 为
//     bool、int、uint、float、string、array 或 struct，
//     则保留原始 Go 值。
//     否则，清零该 Go 值。
//     相比之下，默认 v2 行为是在将 JSON null 反序列化到 Go 值时
//     一致且始终清零该 Go 值。
//
//   - 在反序列化非 null 的 JSON 值时，会对数组元素、切片元素、
//     结构体字段（但不包括 map 值）、
//     指针值和接口值（仅当为非 nil 指针时）合并到原始 Go 值中。
//     相比之下，默认 v2 行为是对结构体字段、map 值、
//     指针值和接口值合并到 Go 值中。
//     一般来说，v2 语义在反序列化 JSON 对象时进行合并，
//     否则会替换原始值。
//
// 这仅影响 unmarshal，在 marshal 时被忽略。
// v1 默认值为 true。
func MergeWithLegacySemantics(v bool) Options {
	if v {
		return jsonflags.MergeWithLegacySemantics | 1
	} else {
		return jsonflags.MergeWithLegacySemantics | 0
	}
}

// OmitEmptyWithLegacySemantics 指定 `omitempty` 标签选项
// 遵循"空"的定义：如果 Go 值为 false、0、nil 指针、nil 接口值，
// 或任何空数组、切片、map 或字符串，则省略该字段。
// 这会覆盖 v2 语义：如果值 marshal 为 JSON null
// 或空的 JSON 字符串、对象或数组，则该字段为空。
//
// `omitempty` 的 v1 和 v2 定义对于 Go 字符串、切片、数组和 map
// 实际上是相同的。对 Go bool、int、uint、float、指针和接口
// 使用 `omitempty` 的地方应迁移为使用 `omitzero` 标签选项，
// 该选项在字段为零 Go 值时省略该字段。
//
// 这仅影响 marshal，在 unmarshal 时被忽略。
// v1 默认值为 true。
func OmitEmptyWithLegacySemantics(v bool) Options {
	if v {
		return jsonflags.OmitEmptyWithLegacySemantics | 1
	} else {
		return jsonflags.OmitEmptyWithLegacySemantics | 0
	}
}

// ParseBytesWithLooseRFC4648 指定在解析编码为
// "base32" 或 "base64" 的二进制数据时，
// 忽略 '\r' 和 '\n' 字符的存在。
// 相比之下，v2 默认报告错误，以便严格遵守
// RFC 4648 第 3.3 节，
// 该节规定必须拒绝非字母表字符。
//
// 这仅影响 unmarshal，在 marshal 时被忽略。
// v1 默认值为 true。
func ParseBytesWithLooseRFC4648(v bool) Options {
	if v {
		return jsonflags.ParseBytesWithLooseRFC4648 | 1
	} else {
		return jsonflags.ParseBytesWithLooseRFC4648 | 0
	}
}

// ParseTimeWithLooseRFC3339 指定 [time.Time]
// 按照对 RFC 3339 的松散遵循进行解析。
// 特别是，它允许历史上不正确的表示，
// 允许在小时格式、亚秒分隔符和时区表示方面有所偏差。
// 相比之下，默认 v2 行为是严格遵守 RFC 3339 中指定的语法。
//
// 这仅影响 unmarshal，在 marshal 时被忽略。
// v1 默认值为 true。
func ParseTimeWithLooseRFC3339(v bool) Options {
	if v {
		return jsonflags.ParseTimeWithLooseRFC3339 | 1
	} else {
		return jsonflags.ParseTimeWithLooseRFC3339 | 0
	}
}

// ReportErrorsWithLegacySemantics 指定 Marshal 和 Unmarshal
// 应以旧版语义报告错误：
//
//   - 在 marshal 或 unmarshal 时，返回的错误值通常是
//     [SyntaxError]、[MarshalerError]、[UnsupportedTypeError]、
//     [UnsupportedValueError]、[InvalidUnmarshalError] 或
//     [UnmarshalTypeError] 等类型。
//     相比之下，v2 语义是始终将错误返回为
//     [jsonv2.SemanticError] 或 [jsontext.SyntacticError]。
//
//   - 在 marshal 时，如果用户定义的 marshal 方法报告错误，
//     则该错误始终被包装在 [MarshalerError] 中，
//     即使该错误本身已经是 [MarshalerError]，
//     这可能导致多层冗余的包装。
//     相比之下，v2 语义是始终将错误包装在 [jsonv2.SemanticError] 中，
//     除非它已经是语义错误。
//
//   - 在 unmarshal 时，如果用户定义的 unmarshal 方法报告错误，
//     则该错误永远不会被包装，而是原样报告。
//     相比之下，v2 语义是始终将错误包装在 [jsonv2.SemanticError] 中，
//     除非它已经是语义错误。
//
//   - 在 marshal 或 unmarshal 时，如果 Go 结构体包含类型错误
//     （例如，冲突的名称或格式错误的字段标签），
//     则此类错误会被忽略，Go 结构体使用尽力而为的表示。
//     相比之下，v2 语义是报告运行时错误。
//
//   - 在 unmarshal 时，JSON 输入的语法结构在执行
//     将 JSON 数据语义反序列化为 Go 值之前被完全验证。
//     实际上，这意味着具有语法错误的 JSON 输入不会导致
//     目标 Go 值发生任何突变。
//     相比之下，v2 语义是执行流式解码并逐步将 JSON 输入
//     反序列化到目标 Go 值中，这意味着在遇到语法错误时
//     Go 值可能已被部分突变。
//
//   - 在 unmarshal 时，语义错误不会立即终止 unmarshal 过程，
//     而是继续求值。
//     当 unmarshal 返回时，仅报告第一个语义错误。
//     相比之下，v2 语义是在遇到错误的那一刻终止 unmarshal。
//
// 这会影响 marshal 或 unmarshal。
// v1 默认值为 true。
func ReportErrorsWithLegacySemantics(v bool) Options {
	if v {
		return jsonflags.ReportErrorsWithLegacySemantics | 1
	} else {
		return jsonflags.ReportErrorsWithLegacySemantics | 0
	}
}

// StringifyWithLegacySemantics 指定 `string` 标签选项
// 可以将 bool 和 string 值字符串化。它仅在顶层类型为
// bool、string、数字 kind 或指向此类 kind 的指针的字段上生效。
// 具体而言，`string` 不会对复合数据类型
// （例如，array、slice、struct、map 或 interface）内的
// bool、string 或数字 kind 进行字符串化。
//
// 在 marshal 时，此类 Go 值被序列化为它们通常的 JSON 表示，
// 但被加引号放在 JSON 字符串内。
// 在 unmarshal 时，此类 Go 值必须从包含其通常 JSON 表示
// 或该数字 kind 的 Go 数字表示的 JSON 字符串中反序列化。
// 请注意，Go 数字语法是 JSON 数字语法的超集。
// 在 JSON 字符串中加引号的 JSON null 是 JSON null 的有效替代，
// 当反序列化到 `string` 生效的 Go 值时。
//
// 这会影响 marshal 或 unmarshal。
// v1 默认值为 true。
func StringifyWithLegacySemantics(v bool) Options {
	if v {
		return jsonflags.StringifyWithLegacySemantics | 1
	} else {
		return jsonflags.StringifyWithLegacySemantics | 0
	}
}

// UnmarshalArrayFromAnyLength 指定 Go 数组可以从
// 任意长度的输入 JSON 数组反序列化。如果 JSON 数组太短，
// 则剩余的 Go 数组元素被清零。如果 JSON 数组太长，
// 则多余的 JSON 数组元素被跳过。
//
// 这仅影响 unmarshal，在 marshal 时被忽略。
// v1 默认值为 true。
func UnmarshalArrayFromAnyLength(v bool) Options {
	if v {
		return jsonflags.UnmarshalArrayFromAnyLength | 1
	} else {
		return jsonflags.UnmarshalArrayFromAnyLength | 0
	}
}

// unmarshalAnyWithRawNumber 指定将 JSON 数字反序列化到
// 空的 Go 接口时应使用 Number 类型而不是 float64。
func unmarshalAnyWithRawNumber(v bool) Options {
	if v {
		return jsonflags.UnmarshalAnyWithRawNumber | 1
	} else {
		return jsonflags.UnmarshalAnyWithRawNumber | 0
	}
}
