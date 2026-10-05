// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"fmt"

	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
)

// Options 使用特定功能来配置 [Marshal]、[MarshalWrite]、[MarshalEncode]、
// [Unmarshal]、[UnmarshalRead] 和 [UnmarshalDecode]。
// 每个函数接受一个可变参数列表的选项，其中
// 后续选项中设置的属性会覆盖先前设置的属性值。
//
// Options 类型与 [encoding/json.Options] 和
// [encoding/json/jsontext.Options] 完全相同。其他包中的 Options
// 可以与本包的功能互换使用。
//
// Options 表示单个选项或一组选项。
// 在功能上可以把它看作选项属性的 Go map
// （尽管底层实现出于性能考虑避免使用 Go map）。
//
// 构造函数（例如 [Deterministic]）返回单个选项值：
//
//	opt := Deterministic(true)
//
// 它类似于创建单条目 map：
//
//	opt := Options{"Deterministic": true}
//
// [JoinOptions] 将多个选项值组合在一起：
//
//	out := JoinOptions(opts...)
//
// 它类似于创建一个新 map 并复制选项：
//
//	out := make(Options)
//	for _, m := range opts {
//		for k, v := range m {
//			out[k] = v
//		}
//	}
//
// [GetOption] 查找 options 参数的值：
//
//	v, ok := GetOption(opts, Deterministic)
//
// 它类似于 Go map 查找：
//
//	v, ok := Options["Deterministic"]
//
// 只有单一的 Options 类型，它同时用于 marshal 和 unmarshal。
// 有些选项影响两种操作，而其他选项只影响一种操作：
//
//   - [StringifyNumbers] 影响 marshaling 和 unmarshaling
//   - [Deterministic] 仅影响 marshaling
//   - [FormatNilSliceAsNull] 仅影响 marshaling
//   - [FormatNilMapAsNull] 仅影响 marshaling
//   - [OmitZeroStructFields] 仅影响 marshaling
//   - [MatchCaseInsensitiveNames] 影响 marshaling 和 unmarshaling
//   - [DiscardUnknownMembers] 仅影响 marshaling
//   - [RejectUnknownMembers] 仅影响 unmarshaling
//   - [WithMarshalers] 仅影响 marshaling
//   - [WithUnmarshalers] 仅影响 unmarshaling
//
// 不影响某一特定操作的选项会被忽略。
type Options = jsonopts.Options

// JoinOptions 将提供的选项列表合并为单个 Options。
// 后续选项中设置的属性会覆盖先前设置的属性值。
func JoinOptions(srcs ...Options) Options {
	var dst jsonopts.Struct
	dst.Join(srcs...)
	return &dst
}

// GetOption 返回使用提供的 setter 存储在 opts 中的值，
// 并报告该值是否存在。
//
// 示例用法：
//
//	v, ok := json.GetOption(opts, json.Deterministic)
//
// Options 最常见的用途是通过内省来改变
// [MarshalerTo.MarshalJSONTo] 和 [UnmarshalerFrom.UnmarshalJSONFrom] 方法，以及
// [MarshalToFunc] 和 [UnmarshalFromFunc] 函数的 JSON 表示形式。
// 在这些情况下，通常应忽略存在性标志位。
func GetOption[T any](opts Options, setter func(T) Options) (T, bool) {
	return jsonopts.GetOption(opts, setter)
}

// DefaultOptionsV2 是定义 v2 语义的所有选项的完整集合。
// 它等价于 [encoding/json.DefaultOptionsV1] 中的选项集合
// 全部设置为 false。所有其他选项都不存在。
func DefaultOptionsV2() Options {
	return &jsonopts.DefaultOptionsV2
}

// StringifyNumbers 指定数值 Go 类型应被 marshal 为
// 包含等价 JSON 数字值的 JSON 字符串。
// 在 unmarshaling 时，数值 Go 类型会从包含该 JSON 数字
// 且不含任何周围空白的 JSON 字符串中解析。
//
// 根据 RFC 8259 第 6 节，JSON 实现可以选择
// 将 JSON 数字的表示限制为 IEEE 754 binary64 值。
// 这可能导致解码器对 int64 和 uint64 类型丢失精度。
// 将 JSON 数字作为 JSON 字符串加引号可以保留精确精度。
//
// 这影响 marshaling 或 unmarshaling。
func StringifyNumbers(v bool) Options {
	if v {
		return jsonflags.StringifyNumbers | 1
	} else {
		return jsonflags.StringifyNumbers | 0
	}
}

// Deterministic 指定相同的输入值将被序列化为
// 完全相同的输出字节。同一程序的
// 不同进程会将相等的值序列化为相同的字节，
// 但不保证同一程序的不同版本
// 产生完全相同的字节序列。
//
// 这仅影响 marshaling，在 unmarshaling 时会被忽略。
func Deterministic(v bool) Options {
	if v {
		return jsonflags.Deterministic | 1
	} else {
		return jsonflags.Deterministic | 0
	}
}

// FormatNilSliceAsNull 指定 nil 的 Go slice 应 marshal 为
// JSON null，而不是默认表示为空的 JSON 数组
// （对于 ~[]byte 则为空的 JSON 字符串）。
// 显式标记了 `format:emitempty` 的 slice 字段仍然会 marshal
// 为空的 JSON 数组。
//
// 这仅影响 marshaling，在 unmarshaling 时会被忽略。
func FormatNilSliceAsNull(v bool) Options {
	if v {
		return jsonflags.FormatNilSliceAsNull | 1
	} else {
		return jsonflags.FormatNilSliceAsNull | 0
	}
}

// FormatNilMapAsNull 指定 nil 的 Go map 应 marshal 为
// JSON null，而不是默认表示为空的 JSON 对象。
// 显式标记了 `format:emitempty` 的 map 字段仍然会 marshal
// 为空的 JSON 对象。
//
// 这仅影响 marshaling，在 unmarshaling 时会被忽略。
func FormatNilMapAsNull(v bool) Options {
	if v {
		return jsonflags.FormatNilMapAsNull | 1
	} else {
		return jsonflags.FormatNilMapAsNull | 0
	}
}

// OmitZeroStructFields 指定 Go struct 应以如下方式 marshal：
// 如果字段值按其 "IsZero() bool" 方法（若存在）判定为零，
// 否则按该字段是否为零值 Go 值来判定，
// 则从 marshal 输出中省略所有为零的 struct 字段。
// 这在语义上等价于在 Go struct 的每个字段上
// 指定 `omitzero` 标签选项。
//
// 这仅影响 marshaling，在 unmarshaling 时会被忽略。
func OmitZeroStructFields(v bool) Options {
	if v {
		return jsonflags.OmitZeroStructFields | 1
	} else {
		return jsonflags.OmitZeroStructFields | 0
	}
}

// MatchCaseInsensitiveNames 指定 JSON 对象成员使用名称的
// 不区分大小写匹配来与 Go struct 字段进行匹配。
// 显式标记了 `case:strict` 或 `case:ignore` 的 Go struct 字段
// 始终使用区分大小写（或不区分大小写）的名称匹配，
// 无论该选项的值是什么。
//
// 这影响 marshaling 或 unmarshaling。
// 对于 marshaling，如果内联字段与 Go struct 中声明的某个字段匹配，
// 该选项可能会改变对内联字段重复名称的检测
// （假定 [jsontext.AllowDuplicateNames] 为 false）。
func MatchCaseInsensitiveNames(v bool) Options {
	if v {
		return jsonflags.MatchCaseInsensitiveNames | 1
	} else {
		return jsonflags.MatchCaseInsensitiveNames | 0
	}
}

// DiscardUnknownMembers 指定 marshaling 应忽略任何存储在
// 专门用于存放未知 JSON 对象成员的 Go struct 字段中的
// JSON 对象成员。
//
// 这仅影响 marshaling，在 unmarshaling 时会被忽略。
func DiscardUnknownMembers(v bool) Options {
	if v {
		return jsonflags.DiscardUnknownMembers | 1
	} else {
		return jsonflags.DiscardUnknownMembers | 0
	}
}

// RejectUnknownMembers 指定在 unmarshal 一个 JSON 对象时，
// 应拒绝未知成员，无论是否存在用于存放未知成员的字段。
//
// 这仅影响 unmarshaling，在 marshaling 时会被忽略。
func RejectUnknownMembers(v bool) Options {
	if v {
		return jsonflags.RejectUnknownMembers | 1
	} else {
		return jsonflags.RejectUnknownMembers | 0
	}
}

// WithMarshalers 指定要使用的类型特定 marshaler 列表，
// 可用于覆盖特定类型值的默认 marshal 行为。
//
// 这仅影响 marshaling，在 unmarshaling 时会被忽略。
func WithMarshalers(v *Marshalers) Options {
	return (*marshalersOption)(v)
}

// WithUnmarshalers 指定要使用的类型特定 unmarshaler 列表，
// 可用于覆盖特定类型值的默认 unmarshal 行为。
//
// 这仅影响 unmarshaling，在 marshaling 时会被忽略。
func WithUnmarshalers(v *Unmarshalers) Options {
	return (*unmarshalersOption)(v)
}

// 这些选项类型在此处声明而不是在 "jsonopts" 中声明，
// 以避免 "jsonopts" 依赖 "reflect"。
type (
	marshalersOption   Marshalers
	unmarshalersOption Unmarshalers
)

func (*marshalersOption) JSONOptions(internal.NotForPublicUse)   {}
func (*unmarshalersOption) JSONOptions(internal.NotForPublicUse) {}

// 向 "jsonopts" 注入支持以处理这些类型。
func init() {
	jsonopts.GetUnknownOption = func(src jsonopts.Struct, zero jsonopts.Options) (any, bool) {
		switch zero.(type) {
		case *marshalersOption:
			if !src.Flags.Has(jsonflags.Marshalers) {
				return (*Marshalers)(nil), false
			}
			return src.Marshalers.(*Marshalers), true
		case *unmarshalersOption:
			if !src.Flags.Has(jsonflags.Unmarshalers) {
				return (*Unmarshalers)(nil), false
			}
			return src.Unmarshalers.(*Unmarshalers), true
		default:
			panic(fmt.Sprintf("unknown option %T", zero))
		}
	}
	jsonopts.JoinUnknownOption = func(dst jsonopts.Struct, src jsonopts.Options) jsonopts.Struct {
		switch src := src.(type) {
		case *marshalersOption:
			dst.Flags.Set(jsonflags.Marshalers | 1)
			dst.Marshalers = (*Marshalers)(src)
		case *unmarshalersOption:
			dst.Flags.Set(jsonflags.Unmarshalers | 1)
			dst.Unmarshalers = (*Unmarshalers)(src)
		default:
			panic(fmt.Sprintf("unknown option %T", src))
		}
		return dst
	}
}
