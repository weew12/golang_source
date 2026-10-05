// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"bytes"
	"encoding"
	"io"
	"reflect"
	"sync"
	"time"

	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/jsontext"
)

// 引用 encoding 和 time 包以帮助 pkgsite
// 能够热链接到这些包。
var (
	_ encoding.TextMarshaler
	_ encoding.TextAppender
	_ encoding.TextUnmarshaler
	_ time.Time
	_ time.Duration
)

// export 暴露 "jsontext" 包的内部功能。
var export = jsontext.Internal.Export(&internal.AllowInternalUse)

// Marshal 根据提供的 marshal 和 encode 选项（同时忽略 unmarshal 或 decode 选项）
// 将 Go 值序列化为一个 []byte。
// 它不会以换行符终止输出。
//
// 特定于类型的 marshal 函数和方法优先于
// 值的默认表示形式。
// 作用于 *T 的函数或方法仅在编码
// 类型为 T 的值（通过取其地址）或 *T 的非 nil 值时被调用。
// Marshal 确保值始终可寻址
// （必要时将其装箱到堆上），以便
// 这些函数和方法能够被一致地调用。为了性能，
// 建议向 Marshal 传递该值的非 nil 指针。
//
// 输入值按照以下规则编码为 JSON：
//
//   - 如果 [WithMarshalers] 选项中的任何特定于类型的函数
//     与该值类型匹配，则调用这些函数来编码该值。
//     如果所有适用的函数都返回 [SkipFunc]，
//     则按照后续规则编码该值。
//
//   - 如果值类型实现了 [MarshalerTo]，
//     则调用 MarshalJSONTo 方法来编码该值。
//
//   - 如果值类型实现了 [Marshaler]，
//     则调用 MarshalJSON 方法来编码该值。
//
//   - 如果值类型实现了 [encoding.TextAppender]，
//     则调用 AppendText 方法来编码该值，并
//     随后将其结果编码为 JSON 字符串。
//
//   - 如果值类型实现了 [encoding.TextMarshaler]，
//     则调用 MarshalText 方法来编码该值，并
//     随后将其结果编码为 JSON 字符串。
//
//   - 否则，按照值类型编码该值，
//     详细描述如下。
//
// 大多数 Go 类型都有默认的 JSON 表示形式。
// 某些类型支持根据 Go 结构体标签中
// 为包含当前值的结构体字段可选指定的
// 格式标志进行特殊格式化
// （更多细节参见“Go 结构体的 JSON 表示”一节）。
//
// 各类型的表示形式如下：
//
//   - Go 布尔值被编码为 JSON 布尔值（例如 true 或 false）。
//     它不支持任何自定义格式标志。
//
//   - Go 字符串被编码为 JSON 字符串。
//     它不支持任何自定义格式标志。
//
//   - Go []byte 或 [N]byte 被编码为 JSON 字符串，
//     其中包含使用 RFC 4648 编码的二进制值。
//     如果格式为 "base64" 或未指定，则使用 RFC 4648 第 4 节。
//     如果格式为 "base64url"，则使用 RFC 4648 第 5 节。
//     如果格式为 "base32"，则使用 RFC 4648 第 6 节。
//     如果格式为 "base32hex"，则使用 RFC 4648 第 7 节。
//     如果格式为 "base16" 或 "hex"，则使用 RFC 4648 第 8 节。
//     如果格式为 "array"，则字节值被编码为 JSON 数组，
//     其中每个字节被递归地 JSON 编码为每个 JSON 数组元素。
//
//   - Go 整数被编码为不含小数或指数的 JSON 数字。
//     如果指定了 [StringifyNumbers] 或正在编码 JSON 对象名称，
//     则该 JSON 数字被编码在 JSON 字符串内。
//     它不支持任何自定义格式标志。
//
//   - Go 浮点数被编码为 JSON 数字。
//     如果指定了 [StringifyNumbers] 或正在编码 JSON 对象名称，
//     则该 JSON 数字被编码在 JSON 字符串内。
//     如果格式为 "nonfinite"，则 NaN、+Inf 和 -Inf 分别被编码为
//     JSON 字符串 "NaN"、"Infinity" 和 "-Infinity"。
//     否则，存在非有限数字将导致 [SemanticError]。
//
//   - Go map 被编码为 JSON 对象，其中每个 Go map 键和值
//     被递归地编码为 JSON 对象中的名称和值对。
//     Go map 键必须编码为 JSON 字符串，否则将导致
//     [SemanticError]。Go map 以非确定性顺序遍历。
//     如需确定性编码，请考虑使用 [Deterministic] 选项。
//     如果格式为 "emitnull"，则 nil map 被编码为 JSON null。
//     如果格式为 "emitempty"，则 nil map 被编码为空 JSON 对象，
//     无论是否指定了 [FormatNilMapAsNull]。
//     否则默认情况下，nil map 被编码为空 JSON 对象。
//
//   - Go 结构体被编码为 JSON 对象。
//     更多细节参见包级文档中的“Go 结构体的 JSON 表示”一节。
//
//   - Go 切片被编码为 JSON 数组，其中每个 Go 切片元素
//     被递归地 JSON 编码为 JSON 数组的元素。
//     如果格式为 "emitnull"，则 nil 切片被编码为 JSON null。
//     如果格式为 "emitempty"，则 nil 切片被编码为空 JSON 数组，
//     无论是否指定了 [FormatNilSliceAsNull]。
//     否则默认情况下，nil 切片被编码为空 JSON 数组。
//
//   - Go 数组被编码为 JSON 数组，其中每个 Go 数组元素
//     被递归地 JSON 编码为 JSON 数组的元素。
//     JSON 数组长度始终与 Go 数组长度相同。
//     它不支持任何自定义格式标志。
//
//   - Go 指针如果为 nil 则被编码为 JSON null，否则它就是
//     底层值的递归 JSON 编码表示形式。
//     格式标志被转发到底层值的编码。
//
//   - Go 接口如果为 nil 则被编码为 JSON null，否则它就是
//     底层值的递归 JSON 编码表示形式。
//     它不支持任何自定义格式标志。
//
//   - Go [time.Time] 被编码为 JSON 字符串，其中包含
//     以 RFC 3339 格式、纳秒精度格式化的时间戳。
//     如果格式与 time 包中声明的某个格式常量
//     匹配（例如 RFC1123），则使用该格式。
//     如果格式为 "unix"、"unixmilli"、"unixmicro" 或 "unixnano"，
//     则时间戳被编码为一个可能为小数的 JSON 数字，
//     表示自 Unix 纪元（即 1970 年 1 月 1 日 00:00:00 UTC）
//     以来的秒数（或毫秒、微秒或纳秒数）。
//     为避免出现小数部分，请将时间戳舍入到相关单位。
//     否则，如果格式非空，则按原样与 [time.Time.Format] 一起使用。
//
//   - Go [time.Duration] 目前没有默认表示形式，
//     需要显式指定格式。
//     如果格式为 "sec"、"milli"、"micro" 或 "nano"，
//     则时长被编码为一个可能为小数的 JSON 数字，
//     表示秒数（或毫秒、微秒或纳秒数）。
//     为避免出现小数部分，请将时长舍入到相关单位。
//     如果格式为 "units"，则使用 [time.Duration.String]
//     将其编码为 JSON 字符串（例如 1 小时 30 分钟为 "1h30m"）。
//     如果格式为 "iso8601"，则使用 ISO 8601 时长标准
//     将其编码为 JSON 字符串（例如 1 小时 30 分钟为 "PT1H30M"），
//     仅使用小时、分钟和秒这些精确单位。
//
//   - 所有其他 Go 类型（例如复数、通道和函数）
//     没有默认表示形式，将导致 [SemanticError]。
//
// JSON 无法表示循环数据结构，Marshal 也不处理它们。
// 传入循环结构将导致错误。
func Marshal(in any, opts ...Options) (out []byte, err error) {
	enc := export.GetBufferedEncoder(opts...)
	defer export.PutBufferedEncoder(enc)
	xe := export.Encoder(enc)
	xe.Flags.Set(jsonflags.OmitTopLevelNewline | 1)
	err = marshalEncode(enc, in, &xe.Struct)
	if err != nil && xe.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		return nil, internal.TransformMarshalError(in, err)
	}
	return bytes.Clone(xe.Buf), err
}

// MarshalWrite 根据提供的 marshal 和 encode 选项（同时忽略 unmarshal 或 decode 选项）
// 将 Go 值序列化到 [io.Writer] 中。
// 它不会以换行符终止输出。
// 有关 Go 值转换为 JSON 的细节，参见 [Marshal]。
func MarshalWrite(out io.Writer, in any, opts ...Options) (err error) {
	enc := export.GetStreamingEncoder(out, opts...)
	defer export.PutStreamingEncoder(enc)
	xe := export.Encoder(enc)
	xe.Flags.Set(jsonflags.OmitTopLevelNewline | 1)
	err = marshalEncode(enc, in, &xe.Struct)
	if err != nil && xe.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		return internal.TransformMarshalError(in, err)
	}
	return err
}

// MarshalEncode 根据提供的 marshal 选项（同时忽略 unmarshal、encode 或 decode 选项）
// 将 Go 值序列化到 [jsontext.Encoder] 中。
// 已在 [jsontext.Encoder] 上指定的任何与 marshal 相关的选项，
// 其优先级都低于调用方提供的选项集。
// 与 [Marshal] 和 [MarshalWrite] 不同，encode 选项被忽略，因为
// 它们必须已经在所提供的 [jsontext.Encoder] 上指定。
//
// 有关 Go 值转换为 JSON 的细节，参见 [Marshal]。
func MarshalEncode(out *jsontext.Encoder, in any, opts ...Options) (err error) {
	xe := export.Encoder(out)
	if len(opts) > 0 {
		optsOriginal := xe.Struct
		defer func() { xe.Struct = optsOriginal }()
		xe.Struct.JoinWithoutCoderOptions(opts...)
	}
	err = marshalEncode(out, in, &xe.Struct)
	if err != nil && xe.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		return internal.TransformMarshalError(in, err)
	}
	return err
}

func marshalEncode(out *jsontext.Encoder, in any, mo *jsonopts.Struct) (err error) {
	v := reflect.ValueOf(in)
	if !v.IsValid() || (v.Kind() == reflect.Pointer && v.IsNil()) {
		return out.WriteToken(jsontext.Null)
	}
	// 浅拷贝非指针值以获得可寻址的值。
	// 为性能考虑，始终传递指针以避免此操作是有益的。
	forceAddr := v.Kind() != reflect.Pointer
	if forceAddr {
		v2 := reflect.New(v.Type())
		v2.Elem().Set(v)
		v = v2
	}
	va := addressableValue{v.Elem(), forceAddr} // 解引用后的指针始终可寻址
	t := va.Type()

	// 查找并调用此类型的 marshal 函数。
	marshal := lookupArshaler(t).marshal
	if mo.Marshalers != nil {
		marshal, _ = mo.Marshalers.(*Marshalers).lookup(marshal, t)
	}
	if err := marshal(out, va, mo); err != nil {
		if !mo.Flags.Get(jsonflags.AllowDuplicateNames) {
			export.Encoder(out).Tokens.InvalidateDisabledNamespaces()
		}
		return err
	}
	return nil
}

// Unmarshal 根据提供的 unmarshal 和 decode 选项（同时忽略 marshal 或 encode 选项）
// 将 []byte 输入解码为 Go 值。
// 输入必须是一个单一的 JSON 值，可穿插可选的空白。
// 输出必须是非 nil 指针。
//
// 特定于类型的 unmarshal 函数和方法优先于
// 值的默认表示形式。
// 作用于 *T 的函数或方法仅在解码
// 类型为 T 的值（通过取其地址）或 *T 的非 nil 值时被调用。
// Unmarshal 确保值始终可寻址
// （必要时将其装箱到堆上），以便
// 这些函数和方法能够被一致地调用。
//
// 输入按照以下规则解码到输出中：
//
//   - 如果 [WithUnmarshalers] 选项中的任何特定于类型的函数
//     与该值类型匹配，则调用这些函数来解码该 JSON
//     值。如果所有适用的函数都返回 [SkipFunc]，
//     则按照后续规则解码输入。
//
//   - 如果值类型实现了 [UnmarshalerFrom]，
//     则调用 UnmarshalJSONFrom 方法来解码该 JSON 值。
//
//   - 如果值类型实现了 [Unmarshaler]，
//     则调用 UnmarshalJSON 方法来解码该 JSON 值。
//
//   - 如果值类型实现了 [encoding.TextUnmarshaler]，
//     则输入被解码为 JSON 字符串，并以解码后的字符串值
//     调用 UnmarshalText 方法。
//     如果输入不是 JSON 字符串，则失败并返回 [SemanticError]。
//
//   - 否则，按照值类型解码该 JSON 值，
//     详细描述如下。
//
// 大多数 Go 类型都有默认的 JSON 表示形式。
// 某些类型支持根据 Go 结构体标签中
// 为包含当前值的结构体字段可选指定的
// 格式标志进行特殊格式化
// （更多细节参见“Go 结构体的 JSON 表示”一节）。
// JSON null 可以被解码到每个受支持的 Go 值中，
// 在此处它等价于存储该 Go 值的零值。
// 如果输入 JSON 的种类未被当前 Go 值类型处理，
// 则失败并返回 [SemanticError]。除非另有说明，
// 解码后的值会替换任何预先存在的值。
//
// 各类型的表示形式如下：
//
//   - Go 布尔值从 JSON 布尔值（例如 true 或 false）解码。
//     它不支持任何自定义格式标志。
//
//   - Go 字符串从 JSON 字符串解码。
//     它不支持任何自定义格式标志。
//
//   - Go []byte 或 [N]byte 从 JSON 字符串解码，
//     该字符串包含使用 RFC 4648 编码的二进制值。
//     如果格式为 "base64" 或未指定，则使用 RFC 4648 第 4 节。
//     如果格式为 "base64url"，则使用 RFC 4648 第 5 节。
//     如果格式为 "base32"，则使用 RFC 4648 第 6 节。
//     如果格式为 "base32hex"，则使用 RFC 4648 第 7 节。
//     如果格式为 "base16" 或 "hex"，则使用 RFC 4648 第 8 节。
//     如果格式为 "array"，则 Go 切片或数组从 JSON 数组解码，
//     其中每个 JSON 元素被递归地解码为每个字节。
//     当解码到非 nil 的 []byte 时，切片长度被重置为零，
//     并将解码后的输入追加到它。
//     当解码到 [N]byte 时，输入的字节数必须正好为 N，
//     否则失败并返回 [SemanticError]。
//
//   - Go 整数从 JSON 数字解码。
//     如果指定了 [StringifyNumbers] 或正在解码 JSON 对象名称，
//     则它必须从包含 JSON 数字的 JSON 字符串解码。
//     如果 JSON 数字包含小数或指数部分，
//     则失败并返回 [SemanticError]。
//     如果它溢出 Go 整数类型的表示范围，也会失败。
//     它不支持任何自定义格式标志。
//
//   - Go 浮点数从 JSON 数字解码。
//     如果指定了 [StringifyNumbers] 或正在解码 JSON 对象名称，
//     则它必须从包含 JSON 数字的 JSON 字符串解码。
//     如果它溢出 Go 浮点类型的表示范围，则失败。
//     如果格式为 "nonfinite"，则 JSON 字符串
//     "NaN"、"Infinity" 和 "-Infinity" 被解码为 NaN、+Inf 和 -Inf。
//     否则，出现此类字符串将导致 [SemanticError]。
//
//   - Go map 从 JSON 对象解码，
//     其中每个 JSON 对象名称和值对分别被递归地解码
//     为 Go map 键和值。Map 不会被清空。
//     如果 Go map 为 nil，则分配一个新 map 用于解码。
//     如果解码后的键与现有的 Go map 条目匹配，则该条目值
//     会被复用，将 JSON 对象值解码到其中。
//     格式 "emitnull" 和 "emitempty" 在解码时没有效果。
//
//   - Go 结构体从 JSON 对象解码。
//     更多细节参见包级文档中的“Go 结构体的 JSON 表示”一节。
//
//   - Go 切片从 JSON 数组解码，其中每个 JSON 元素
//     被递归地解码并追加到 Go 切片。
//     在追加到 Go 切片之前，如果切片为 nil 则分配新切片，
//     否则将切片长度重置为零。
//     格式 "emitnull" 和 "emitempty" 在解码时没有效果。
//
//   - Go 数组从 JSON 数组解码，其中每个 JSON 数组元素
//     被递归地解码为对应的 Go 数组元素。
//     每个 Go 数组元素在解码到其中之前被置零。
//     如果 JSON 数组包含的元素数量与 Go 数组不完全相同，
//     则失败并返回 [SemanticError]。
//     它不支持任何自定义格式标志。
//
//   - Go 指针根据 JSON 种类和底层 Go 类型解码。
//     如果输入为 JSON null，则存储 nil 指针。
//     否则，如果指针为 nil 则分配新的底层值，
//     并将 JSON 递归解码到该底层值中。
//     格式标志被转发到底层类型的解码。
//
//   - Go 接口根据 JSON 种类和底层 Go 类型解码。
//     如果输入为 JSON null，则存储 nil 接口值。
//     否则，如果输入分别是 JSON 布尔值、字符串、数字、
//     对象或数组，则空接口类型的 nil 接口值会被初始化为
//     零值 Go bool、string、float64、map[string]any 或 []any。
//     如果接口值仍为 nil，则失败并返回 [SemanticError]，
//     因为解码无法确定要解码到的合适 Go 类型。
//     例如，将数据反序列化到 nil io.Reader 会失败，因为
//     没有具体类型来填充该接口值。
//     否则，存在底层值，它会将 JSON 输入
//     递归解码到其中。它不支持任何自定义格式标志。
//
//   - Go [time.Time] 从 JSON 字符串解码，该字符串包含
//     以 RFC 3339、纳秒精度格式化的时间。
//     如果格式与 time 包中声明的某个格式常量
//     匹配（例如 RFC1123），则使用该格式进行解析。
//     如果格式为 "unix"、"unixmilli"、"unixmicro" 或 "unixnano"，
//     则时间戳从自 Unix 纪元（即 1970 年 1 月 1 日 00:00:00 UTC）
//     以来的秒数（或毫秒、微秒或纳秒数）的、
//     可能为小数的 JSON 数字解码。
//     否则，如果格式非空，则按原样与 [time.Time.Parse] 一起使用。
//
//   - Go [time.Duration] 目前没有默认表示形式，
//     需要显式指定格式。
//     如果格式为 "sec"、"milli"、"micro" 或 "nano"，
//     则时长从秒数（或毫秒、微秒或纳秒数）的、
//     可能为小数的 JSON 数字解码。
//     如果格式为 "units"，则从使用 [time.ParseDuration]
//     解析的 JSON 字符串解码（例如 1 小时 30 分钟为 "1h30m"）。
//     如果格式为 "iso8601"，则从使用 ISO 8601 时长标准
//     的 JSON 字符串解码（例如 1 小时 30 分钟为 "PT1H30M"），
//     仅接受小时、分钟或秒这些精确单位。
//
//   - 所有其他 Go 类型（例如复数、通道和函数）
//     没有默认表示形式，将导致 [SemanticError]。
//
// 通常，反序列化遵循合并语义（类似于 RFC 7396），
// 对于除对象以外的任何 JSON 种类，
// 解码后的 Go 值会替换目标值。
// 对于 JSON 对象，输入对象被合并到目标值中，
// 其中匹配的对象成员会递归地应用合并语义。
func Unmarshal(in []byte, out any, opts ...Options) (err error) {
	dec := export.GetBufferedDecoder(in, opts...)
	defer export.PutBufferedDecoder(dec)
	xd := export.Decoder(dec)
	err = unmarshalDecode(dec, out, &xd.Struct, true)
	if err != nil && xd.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		return internal.TransformUnmarshalError(out, err)
	}
	return err
}

// UnmarshalRead 根据提供的 unmarshal 和 decode 选项（同时忽略 marshal 或 encode 选项）
// 从 [io.Reader] 反序列化一个 Go 值。
// 输入必须是一个单一的 JSON 值，可穿插可选的空白。
// 它消费 [io.Reader] 的全部内容直到遇到 [io.EOF]，
// 且不将 EOF 报告为错误。输出必须是非 nil 指针。
// 有关 JSON 转换为 Go 值的细节，参见 [Unmarshal]。
func UnmarshalRead(in io.Reader, out any, opts ...Options) (err error) {
	dec := export.GetStreamingDecoder(in, opts...)
	defer export.PutStreamingDecoder(dec)
	xd := export.Decoder(dec)
	err = unmarshalDecode(dec, out, &xd.Struct, true)
	if err != nil && xd.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		return internal.TransformUnmarshalError(out, err)
	}
	return err
}

// UnmarshalDecode 根据提供的 unmarshal 选项（同时忽略 marshal、encode 或 decode 选项）
// 从 [jsontext.Decoder] 反序列化一个 Go 值。
// 已在 [jsontext.Decoder] 上指定的任何 unmarshal 选项，
// 其优先级都低于调用方提供的选项集。
// 与 [Unmarshal] 和 [UnmarshalRead] 不同，decode 选项被忽略，因为
// 它们必须已经在所提供的 [jsontext.Decoder] 上指定。
//
// 输入可以是零个或多个 JSON 值的流，
// 此处仅反序列化流中的下一个 JSON 值。
// 如果没有更多顶层 JSON 值，则报告 [io.EOF]。
// 输出必须是非 nil 指针。
// 有关 JSON 转换为 Go 值的细节，参见 [Unmarshal]。
func UnmarshalDecode(in *jsontext.Decoder, out any, opts ...Options) (err error) {
	xd := export.Decoder(in)
	if len(opts) > 0 {
		optsOriginal := xd.Struct
		defer func() { xd.Struct = optsOriginal }()
		xd.Struct.JoinWithoutCoderOptions(opts...)
	}
	err = unmarshalDecode(in, out, &xd.Struct, false)
	if err != nil && xd.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		return internal.TransformUnmarshalError(out, err)
	}
	return err
}

func unmarshalDecode(in *jsontext.Decoder, out any, uo *jsonopts.Struct, last bool) (err error) {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return &SemanticError{action: "unmarshal", GoType: reflect.TypeOf(out), Err: internal.ErrNonNilReference}
	}
	va := addressableValue{v.Elem(), false} // 解引用后的指针始终可寻址
	t := va.Type()

	// 在旧版语义中，下一个 JSON 值的整体
	// 会在尝试反序列化之前被验证。
	if uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
		if err := export.Decoder(in).CheckNextValue(last); err != nil {
			if err == io.EOF && last {
				offset := in.InputOffset() + int64(len(in.UnreadBuffer()))
				return &jsontext.SyntacticError{ByteOffset: offset, Err: io.ErrUnexpectedEOF}
			}
			return err
		}
	}

	// 查找并调用此类型的 unmarshal 函数。
	unmarshal := lookupArshaler(t).unmarshal
	if uo.Unmarshalers != nil {
		unmarshal, _ = uo.Unmarshalers.(*Unmarshalers).lookup(unmarshal, t)
	}
	if err := unmarshal(in, va, uo); err != nil {
		if !uo.Flags.Get(jsonflags.AllowDuplicateNames) {
			export.Decoder(in).Tokens.InvalidateDisabledNamespaces()
		}
		if err == io.EOF && last {
			offset := in.InputOffset() + int64(len(in.UnreadBuffer()))
			return &jsontext.SyntacticError{ByteOffset: offset, Err: io.ErrUnexpectedEOF}
		}
		return err
	}
	if last {
		return export.Decoder(in).CheckEOF()
	}
	return nil
}

// addressableValue 是一个 reflect.Value，它保证可寻址，
// 因此调用 Addr 和 Set 方法不会 panic。
//
// 没有编译魔法来强制实施这一属性，
// 而是构造此类型的必要性使得更容易检查每个
// 构造点，以确保该属性得到维护。
type addressableValue struct {
	reflect.Value

	// forcedAddr 报告此值是否仅通过
	// 使用 [newAddressableValue] 而可寻址。
	// 它仅用于 [jsonflags.CallMethodsWithLegacySemantics]。
	forcedAddr bool
}

// newAddressableValue 构造一个类型为 t 的新可寻址值。
func newAddressableValue(t reflect.Type) addressableValue {
	return addressableValue{reflect.New(t).Elem(), true}
}

// TODO: 从 [marshaler] 和 [unmarshaler] 中移除 *jsonopts.Struct 参数。
// 它可以直接在 encoder 或 decoder 上访问。

// 所有 marshal 和 unmarshal 行为都使用这些签名实现。
// 保证 *jsonopts.Struct 参数与 Encoder.Struct 或 Decoder.Struct 中的选项
// 相同，或至少是其严格超集。
// 对于 Marshal、Unmarshal、MarshalWrite 和 UnmarshalRead，它相同。
// 对于 MarshalEncode 和 UnmarshalDecode，它是超集。
type (
	marshaler   = func(*jsontext.Encoder, addressableValue, *jsonopts.Struct) error
	unmarshaler = func(*jsontext.Decoder, addressableValue, *jsonopts.Struct) error
)

type arshaler struct {
	marshal    marshaler
	unmarshal  unmarshaler
	nonDefault bool
}

var lookupArshalerCache sync.Map // map[reflect.Type]*arshaler

func lookupArshaler(t reflect.Type) *arshaler {
	if v, ok := lookupArshalerCache.Load(t); ok {
		return v.(*arshaler)
	}

	fncs := makeDefaultArshaler(t)
	fncs = makeMethodArshaler(fncs, t)
	fncs = makeTimeArshaler(fncs, t)

	// 使用最后存储的那个，以便重复的 arshaler 可以被垃圾回收。
	v, _ := lookupArshalerCache.LoadOrStore(t, fncs)
	return v.(*arshaler)
}

var stringsPools = &sync.Pool{New: func() any { return new(stringSlice) }}

type stringSlice []string

// getStrings 返回一个指向长度为 n 的切片的非 nil 指针。
func getStrings(n int) *stringSlice {
	s := stringsPools.Get().(*stringSlice)
	if cap(*s) < n {
		*s = make([]string, n)
	}
	*s = (*s)[:n]
	return s
}

func putStrings(s *stringSlice) {
	if cap(*s) > 1<<10 {
		*s = nil // 避免固定任意大的内存量
	}
	clear(*s) // 避免固定对每个字符串的引用
	stringsPools.Put(s)
}
