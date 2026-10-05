// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"strings"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
)

// Options 使用特定功能配置 [NewEncoder]、[Encoder.Reset]、[NewDecoder]
// 和 [Decoder.Reset]。
// 每个函数接收一个可变参数列表的选项，其中较后设置的选项中的属性
// 会覆盖先前设置的属性的值。
//
// 只有一个 Options 类型，它同时用于编码和解码。
// 有些选项同时影响这两种操作，而其他选项只影响其中一种操作：
//
//   - [AllowDuplicateNames] 同时影响编码和解码
//   - [AllowInvalidUTF8] 同时影响编码和解码
//   - [EscapeForHTML] 仅影响编码
//   - [EscapeForJS] 仅影响编码
//   - [PreserveRawStrings] 仅影响编码
//   - [CanonicalizeRawInts] 仅影响编码
//   - [CanonicalizeRawFloats] 仅影响编码
//   - [ReorderRawObjects] 仅影响编码
//   - [SpaceAfterColon] 仅影响编码
//   - [SpaceAfterComma] 仅影响编码
//   - [Multiline] 仅影响编码
//   - [WithIndent] 仅影响编码
//   - [WithIndentPrefix] 仅影响编码
//
// 不影响特定操作的选项会被忽略。
//
// Options 类型与 [encoding/json.Options] 和
// [encoding/json/v2.Options] 完全相同。来自其他包的选项
// 可以传递给本包中的功能，但会被忽略。
// 本包中的选项可以与其他包一起使用。
type Options = jsonopts.Options

// AllowDuplicateNames 规定 JSON 对象可以包含
// 重复的成员名称。禁用重复名称检查可能带来
// 性能上的好处，但会破坏与 RFC 7493 第 2.3 节的兼容性。
// 输入或输出仍然符合 RFC 8259，
// 该标准将重复名称的处理留作未指定的行为。
//
// 这影响编码或解码。
func AllowDuplicateNames(v bool) Options {
	if v {
		return jsonflags.AllowDuplicateNames | 1
	} else {
		return jsonflags.AllowDuplicateNames | 0
	}
}

// AllowInvalidUTF8 规定 JSON 字符串可以包含无效的 UTF-8，
// 这些字节将被篡改为 Unicode 替换字符 U+FFFD。
// 这会导致编码器或解码器破坏与
// RFC 7493 第 2.1 节以及 RFC 8259 第 8.1 节的兼容性。
//
// 这影响编码或解码。
func AllowInvalidUTF8(v bool) Options {
	if v {
		return jsonflags.AllowInvalidUTF8 | 1
	} else {
		return jsonflags.AllowInvalidUTF8 | 0
	}
}

// EscapeForHTML 规定 JSON 字符串中的 '<'、'>' 和 '&' 字符
// 应转义为十六进制的 Unicode 码点（例如 \u003c），以便
// 输出可以安全地嵌入 HTML 中。
//
// 这仅影响编码，在解码时会被忽略。
func EscapeForHTML(v bool) Options {
	if v {
		return jsonflags.EscapeForHTML | 1
	} else {
		return jsonflags.EscapeForHTML | 0
	}
}

// EscapeForJS 规定 JSON 字符串中的 U+2028 和 U+2029 字符
// 应转义为十六进制的 Unicode 码点（例如 \u2028），以便
// 输出可以有效地嵌入 JavaScript 中。参见 RFC 8259 第 12 节。
//
// 这仅影响编码，在解码时会被忽略。
func EscapeForJS(v bool) Options {
	if v {
		return jsonflags.EscapeForJS | 1
	} else {
		return jsonflags.EscapeForJS | 0
	}
}

// PreserveRawStrings 规定在编码 [Token] 或 [Value] 中的原始 JSON
// 字符串时，JSON 字符串中已转义的序列
// 会被保留到输出中。
// 然而，原始字符串仍会遵循 [EscapeForHTML] 和 [EscapeForJS]，
// 从而对相关字符进行转义。
// 如果启用了 [AllowInvalidUTF8]，无效 UTF-8 的字节
// 会被保留到输出中。
//
// 这仅影响编码，在解码时会被忽略。
func PreserveRawStrings(v bool) Options {
	if v {
		return jsonflags.PreserveRawStrings | 1
	} else {
		return jsonflags.PreserveRawStrings | 0
	}
}

// CanonicalizeRawInts 规定在编码 [Token] 或 [Value] 中的原始 JSON
// 整数值（即没有小数部分和指数的数字）时，该数字
// 会根据 RFC 8785 第 3.2.2.3 节被规范化。作为一种特殊情况，
// 数字 -0 被规范化为 0。
//
// JSON 数字被视为 IEEE 754 双精度数字。
// 任何精度超出该形式可表示范围的数字
// 在被规范化时会丢失精度。例如，
// 超出 ±2⁵³ 的整数值会丢失精度。
// 例如，1234567890123456789 会被格式化为 1234567890123456800。
//
// 这仅影响编码，在解码时会被忽略。
func CanonicalizeRawInts(v bool) Options {
	if v {
		return jsonflags.CanonicalizeRawInts | 1
	} else {
		return jsonflags.CanonicalizeRawInts | 0
	}
}

// CanonicalizeRawFloats 规定在编码 [Token] 或 [Value] 中的原始 JSON
// 浮点数值（即带有小数部分或指数的数字）时，该数字
// 会根据 RFC 8785 第 3.2.2.3 节被规范化。作为一种特殊情况，
// 数字 -0 被规范化为 0。
//
// JSON 数字被视为 IEEE 754 双精度数字。
// 将一个序列化的单精度数字规范化，再将其作为单精度数字
// 解析回来并期望得到相同的值是安全的。
// 如果某个数字超出 ±1.7976931348623157e+308，即最大的
// 有限数字，那么它会被饱和到该值并按此格式化。
//
// 这仅影响编码，在解码时会被忽略。
func CanonicalizeRawFloats(v bool) Options {
	if v {
		return jsonflags.CanonicalizeRawFloats | 1
	} else {
		return jsonflags.CanonicalizeRawFloats | 0
	}
}

// ReorderRawObjects 规定在编码 [Value] 中的原始 JSON 对象时，
// 对象成员会根据 RFC 8785 第 3.2.3 节被重新排序。
//
// 这仅影响编码，在解码时会被忽略。
func ReorderRawObjects(v bool) Options {
	if v {
		return jsonflags.ReorderRawObjects | 1
	} else {
		return jsonflags.ReorderRawObjects | 0
	}
}

// SpaceAfterColon 规定 JSON 输出应在 JSON 对象名称之后的
// 每个冒号分隔符之后输出一个空格字符。
// 如果为 false，则冒号分隔符之后不会出现空格字符。
//
// 这仅影响编码，在解码时会被忽略。
func SpaceAfterColon(v bool) Options {
	if v {
		return jsonflags.SpaceAfterColon | 1
	} else {
		return jsonflags.SpaceAfterColon | 0
	}
}

// SpaceAfterComma 规定 JSON 输出应在 JSON 对象值或数组元素之后的
// 每个逗号分隔符之后输出一个空格字符。
// 如果为 false，则逗号分隔符之后不会出现空格字符。
//
// 这仅影响编码，在解码时会被忽略。
func SpaceAfterComma(v bool) Options {
	if v {
		return jsonflags.SpaceAfterComma | 1
	} else {
		return jsonflags.SpaceAfterComma | 0
	}
}

// Multiline 规定 JSON 输出应展开为多行，
// 其中每个 JSON 对象成员或 JSON 数组元素都根据嵌套深度
// 出现在新的缩进行上。
//
// 如果未指定 [SpaceAfterColon]，则默认为 true。
// 如果未指定 [SpaceAfterComma]，则默认为 false。
// 如果未指定 [WithIndent]，则默认为 "\t"。
//
// 如果设置为 false，则输出为单行，
// 其中输出的唯一空白由 [SpaceAfterColon] 和 [SpaceAfterComma]
// 的当前值决定。
//
// 这仅影响编码，在解码时会被忽略。
func Multiline(v bool) Options {
	if v {
		return jsonflags.Multiline | 1
	} else {
		return jsonflags.Multiline | 0
	}
}

// WithIndent 规定编码器应输出多行输出，
// 其中 JSON 对象或数组中的每个元素都从新的缩进行开始，
// 该行以缩进前缀开头（参见 [WithIndentPrefix]），
// 后跟根据嵌套深度的一处或多处缩进副本。
// 缩进必须只由空格或制表符组成。
//
// 如果意图输出缩进，但不偏好
// 特定的缩进字符串，则应改用 [Multiline]。
//
// 这仅影响编码，在解码时会被忽略。
// 使用此选项意味着 [Multiline] 被设置为 true。
func WithIndent(indent string) Options {
	// 快速路径：为常见缩进返回常量，从而避免分配。
	// 这些是通过分析 2023-07-01 的 Go module proxy 得出的。
	switch indent {
	case "\t":
		return jsonopts.Indent("\t") // 约 14k 次使用
	case "    ":
		return jsonopts.Indent("    ") // 约 18k 次使用
	case "   ":
		return jsonopts.Indent("   ") // 约 1.7k 次使用
	case "  ":
		return jsonopts.Indent("  ") // 约 52k 次使用
	case " ":
		return jsonopts.Indent(" ") // 约 12k 次使用
	case "":
		return jsonopts.Indent("") // 约 1.5k 次使用
	}

	// 否则，为这个唯一的值进行分配。
	if s := strings.Trim(indent, " \t"); len(s) > 0 {
		panic("json: invalid character " + jsonwire.QuoteRune(s) + " in indent")
	}
	return jsonopts.Indent(indent)
}

// WithIndentPrefix 规定编码器应输出多行输出，
// 其中 JSON 对象或数组中的每个元素都从新的缩进行开始，
// 该行以缩进前缀开头，后跟根据嵌套深度的一处或多处缩进副本
// （参见 [WithIndent]）。
// 前缀必须只由空格或制表符组成。
//
// 这仅影响编码，在解码时会被忽略。
// 使用此选项意味着 [Multiline] 被设置为 true。
func WithIndentPrefix(prefix string) Options {
	if s := strings.Trim(prefix, " \t"); len(s) > 0 {
		panic("json: invalid character " + jsonwire.QuoteRune(s) + " in indent prefix")
	}
	return jsonopts.IndentPrefix(prefix)
}

/*
// TODO(https://go.dev/issue/56733): 实现 WithByteLimit 和 WithDepthLimit。
// 记得还要更新 "Security Considerations" 一节。

// WithByteLimit 对每个顶层 JSON 值可能被消费或产生的
// 输入或输出字节数设置限制。
// 如果 [Decoder] 或 [Encoder] 的方法调用为了在顶层 JSON 值上取得进展
// 需要消费/产生总共超过 n 个字节，
// 那么该调用将报告一个错误。
// 顶层值之前和内部的空白会计入该限制。
// 顶层值之后的空白会计入下一个顶层值的限制。
//
// 非正的限制等价于完全没有限制。
// 如果未指定，默认限制为完全没有限制。
// 这影响编码或解码。
func WithByteLimit(n int64) Options {
	return jsonopts.ByteLimit(max(n, 0))
}

// WithDepthLimit 对每个顶层 JSON 值可能被消费或产生的
// JSON 嵌套最大深度设置限制。
// 如果 [Decoder] 或 [Encoder] 的方法调用为了在顶层 JSON 值上取得进展
// 需要消费或产生的深度大于 n，
// 那么该调用将报告一个错误。
//
// 非正的限制等价于完全没有限制。
// 如果未指定，默认限制为 10000。
// 这影响编码或解码。
func WithDepthLimit(n int) Options {
	return jsonopts.DepthLimit(max(n, 0))
}
*/
