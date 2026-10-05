// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"sync"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonwire"
)

// 注意：Value 类似于 v1 的 json.RawMessage。

// AppendFormat 根据指定的选项对 src 中的 JSON 值进行格式化，
// 并将其追加到 dst。
// 有关格式化行为的更多细节，请参见 [Value.Format]。
//
// dst 和 src 可能重叠。
// 如果报告了错误，那么 src 的全部内容都会被追加到 dst。
func AppendFormat(dst, src []byte, opts ...Options) ([]byte, error) {
	e := getBufferedEncoder(opts...)
	defer putBufferedEncoder(e)
	e.s.Flags.Set(jsonflags.OmitTopLevelNewline | 1)
	if err := e.s.WriteValue(src); err != nil {
		return append(dst, src...), err
	}
	return append(dst, e.s.Buf...), nil
}

// Value 表示单个原始 JSON 值，它可以是以下之一：
//   - JSON 字面量（即 null、true 或 false）
//   - JSON 字符串（例如 "hello, world!"）
//   - JSON 数字（例如 123.456）
//   - 整个 JSON 对象（例如 {"fizz":"buzz"} ）
//   - 整个 JSON 数组（例如 [1,2,3] ）
//
// Value 可以表示整个数组或对象值，而 [Token] 不能。
// Value 可能包含前导和/或尾随空白。
type Value []byte

// Clone 返回 v 的副本。
func (v Value) Clone() Value {
	return bytes.Clone(v)
}

// String 返回 v 的字符串格式化结果。
func (v Value) String() string {
	if v == nil {
		return "null"
	}
	return string(v)
}

// IsValid 报告原始 JSON 值是否根据指定的选项在语法上有效。
//
// 默认情况下（如果未指定任何选项），它根据 RFC 7493 进行校验。
// 它验证输入是否正确编码为 UTF-8、
// 字符串内的转义序列是否解码为有效的 Unicode 码点，以及
// 每个对象中的所有名称是否唯一。
// 它不验证数字是否可在任何常见数值类型（例如 float64、int64 或 uint64）
// 的范围内表示。
//
// 相关选项包括：
//   - [AllowDuplicateNames]
//   - [AllowInvalidUTF8]
//
// 所有其他选项均被忽略。
func (v Value) IsValid(opts ...Options) bool {
	// TODO: 说明对 [WithByteLimit] 和 [WithDepthLimit] 的支持。
	d := getBufferedDecoder(v, opts...)
	defer putBufferedDecoder(d)
	_, errVal := d.ReadValue()
	_, errEOF := d.ReadToken()
	return errVal == nil && errEOF == io.EOF
}

// Format 就地对原始 JSON 值进行格式化。
//
// 默认情况下（如果未指定任何选项），它根据 RFC 7493 进行校验，
// 并生成最简 JSON 表示，其中
// 所有空白都被省略，JSON 字符串使用最短的编码。
//
// 相关选项包括：
//   - [AllowDuplicateNames]
//   - [AllowInvalidUTF8]
//   - [EscapeForHTML]
//   - [EscapeForJS]
//   - [PreserveRawStrings]
//   - [CanonicalizeRawInts]
//   - [CanonicalizeRawFloats]
//   - [ReorderRawObjects]
//   - [SpaceAfterColon]
//   - [SpaceAfterComma]
//   - [Multiline]
//   - [WithIndent]
//   - [WithIndentPrefix]
//
// 所有其他选项均被忽略。
//
// 如果该值根据相同的选项有效，则保证成功。
// 如果该值已经格式化，则缓冲区不会被修改。
func (v *Value) Format(opts ...Options) error {
	// TODO: 说明对 [WithByteLimit] 和 [WithDepthLimit] 的支持。
	return v.format(opts, nil)
}

// format 接受两个 []Options，以避免将它们拼接时产生分配。
// 它等价于 v.Format(append(opts1, opts2...)...)。
func (v *Value) format(opts1, opts2 []Options) error {
	e := getBufferedEncoder(opts1...)
	defer putBufferedEncoder(e)
	e.s.Join(opts2...)
	e.s.Flags.Set(jsonflags.OmitTopLevelNewline | 1)
	if err := e.s.WriteValue(*v); err != nil {
		return err
	}
	if !bytes.Equal(*v, e.s.Buf) {
		*v = append((*v)[:0], e.s.Buf...)
	}
	return nil
}

// Compact 从原始 JSON 值中移除所有空白。
//
// 它不会重新格式化 JSON 字符串或数字以使用任何其他表示。
// 为了最大化可格式化的 JSON 值集合，
// 它允许包含重复名称和无效 UTF-8 的值。
//
// Compact 等价于使用以下选项调用 [Value.Format]：
//   - [AllowDuplicateNames](true)
//   - [AllowInvalidUTF8](true)
//   - [PreserveRawStrings](true)
//
// 调用方指定的任何选项都会在初始选项集之后应用，
// 并且可以有意覆盖先前的选项。
func (v *Value) Compact(opts ...Options) error {
	return v.format([]Options{
		AllowDuplicateNames(true),
		AllowInvalidUTF8(true),
		PreserveRawStrings(true),
	}, opts)
}

// Indent 重新格式化原始 JSON 值中的空白，使
// JSON 对象或数组中的每个元素根据嵌套层级从缩进的新行开始。
//
// 它不会重新格式化 JSON 字符串或数字以使用任何其他表示。
// 为了最大化可格式化的 JSON 值集合，
// 它允许包含重复名称和无效 UTF-8 的值。
//
// Indent 等价于使用以下选项调用 [Value.Format]：
//   - [AllowDuplicateNames](true)
//   - [AllowInvalidUTF8](true)
//   - [PreserveRawStrings](true)
//   - [Multiline](true)
//
// 调用方指定的任何选项都会在初始选项集之后应用，
// 并且可以有意覆盖先前的选项。
func (v *Value) Indent(opts ...Options) error {
	return v.format([]Options{
		AllowDuplicateNames(true),
		AllowInvalidUTF8(true),
		PreserveRawStrings(true),
		Multiline(true),
	}, opts)
}

// Canonicalize 根据 RFC 8785 定义的
// JSON 规范化方案（JCS）对原始 JSON 值进行规范化，
// 其中它会生成 JSON 值的稳定表示。
//
// JSON 字符串被格式化为使用其最简表示，
// JSON 数字根据某种稳定的序列化算法被格式化为双精度数字。
// JSON 对象成员按名称升序排序。
// 所有空白都被移除。
//
// 输出的稳定性取决于应用数据的稳定性
// （参见 RFC 8785 附录 E）。它无法从
// 本质上不稳定的输入产生稳定的输出。例如，如果 JSON 值
// 包含短暂数据（例如频繁变化的时间戳），
// 那么无论是否调用此方法，该值仍然是不稳定的。
//
// Canonicalize 等价于使用以下选项调用 [Value.Format]：
//   - [CanonicalizeRawInts](true)
//   - [CanonicalizeRawFloats](true)
//   - [ReorderRawObjects](true)
//
// 调用方指定的任何选项都会在初始选项集之后应用，
// 并且可以有意覆盖先前的选项。
//
// 请注意，JCS 将所有 JSON 数字视为 IEEE 754 双精度数字。
// 任何精度超出该形式可表示范围的数字
// 在规范化时都会丢失精度。例如，超出 ±2⁵³ 的
// 整数值将丢失其精度。要保留 JSON 整数的原始表示，
// 可额外将 [CanonicalizeRawInts] 设置为 false：
//
//	v.Canonicalize(jsontext.CanonicalizeRawInts(false))
func (v *Value) Canonicalize(opts ...Options) error {
	return v.format([]Options{
		CanonicalizeRawInts(true),
		CanonicalizeRawFloats(true),
		ReorderRawObjects(true),
	}, opts)
}

// MarshalJSON 返回 v 作为 v 的 JSON 编码。
// 它将存储的值作为原始 JSON 输出返回，不做任何校验。
// 如果 v 为 nil，则返回 JSON null。
func (v Value) MarshalJSON() ([]byte, error) {
	// 注意：这与 v1 的 json.RawMessage.MarshalJSON 行为一致。
	if v == nil {
		return []byte("null"), nil
	}
	return v, nil
}

// UnmarshalJSON 将 v 设置为 b 的 JSON 编码。
// 它存储所提供原始 JSON 输入的副本，不做任何校验。
func (v *Value) UnmarshalJSON(b []byte) error {
	// 注意：这与 v1 的 json.RawMessage.UnmarshalJSON 行为一致。
	if v == nil {
		return errors.New("jsontext.Value: UnmarshalJSON on nil pointer")
	}
	*v = append((*v)[:0], b...)
	return nil
}

// Kind 返回起始令牌的种类。
// 对于有效的值，它绝不会包含 [KindEndObject] 或 [KindEndArray]。
func (v Value) Kind() Kind {
	if v := v[jsonwire.ConsumeWhitespace(v):]; len(v) > 0 {
		return Kind(v[0]).normalize()
	}
	return invalidKind
}

const commaAndWhitespace = ", \n\r\t"

type objectMember struct {
	// name 是未加引号的名称。
	name []byte // 例如 "name"
	// buffer 是原始 JSON 对象成员的全部内容，
	// 从上一个成员之后（或起始 '{' 之后）开始，
	// 直到该成员值之后结束。
	buffer []byte // 例如 `, \n\r\t"name": "value"`
}

func (x objectMember) Compare(y objectMember) int {
	if c := jsonwire.CompareUTF16(x.name, y.name); c != 0 {
		return c
	}
	// 在启用 [AllowDuplicateNames] 或 [AllowInvalidUTF8] 时，
	// 名称可能相同，因此还要使用成员值进行排序。
	return jsonwire.CompareUTF16(
		bytes.TrimLeft(x.buffer, commaAndWhitespace),
		bytes.TrimLeft(y.buffer, commaAndWhitespace))
}

var objectMemberPool = sync.Pool{New: func() any { return new([]objectMember) }}

func getObjectMembers() *[]objectMember {
	ns := objectMemberPool.Get().(*[]objectMember)
	*ns = (*ns)[:0]
	return ns
}
func putObjectMembers(ns *[]objectMember) {
	if cap(*ns) < 1<<10 {
		clear(*ns) // 避免固定 name 和 buffer
		objectMemberPool.Put(ns)
	}
}

// mustReorderObjects 就地对 JSON 值中的所有对象成员重新排序，
// 该值必须有效，否则会 panic。
func mustReorderObjects(b []byte) {
	// 获取一个带缓冲的编码器，仅为了将其内部缓冲区用作
	// 重新排序对象成员的临时缓冲区。
	e2 := getBufferedEncoder()
	defer putBufferedEncoder(e2)

	// 禁用不必要的检查，以便对 JSON 值进行语法解析。
	d := getBufferedDecoder(b)
	defer putBufferedDecoder(d)
	d.s.Flags.Set(jsonflags.AllowDuplicateNames | jsonflags.AllowInvalidUTF8 | 1)
	mustReorderObjectsFromDecoder(d, &e2.s.Buf) // 依据 RFC 8785 第 3.2.3 节
}

// mustReorderObjectsFromDecoder 就地递归地重新排序所有对象成员，
// 依据 RFC 8785 第 3.2.3 节中指定的顺序。
//
// 前置条件：
//   - 该值有效（即绝不应发生解码器错误）。
//   - 初次调用时传入的 Decoder 从 v 的开头读取。
//
// 后置条件：
//   - 从 Decoder 中恰好读取一个 JSON 值。
//   - 所有完全解析的 JSON 对象都通过在值缓冲区中直接移动
//     成员来完成重新排序。
//
// 运行时间约为 O(n·log(n)) + O(m·log(m))，
// 其中 n 是 len(v)，m 是对象成员的总数。
func mustReorderObjectsFromDecoder(d *Decoder, scratch *[]byte) {
	switch tok, err := d.ReadToken(); tok.Kind() {
	case '{':
		// 遍历并收集每个对象成员的名称和偏移量。
		members := getObjectMembers()
		defer putObjectMembers(members)
		var prevMember objectMember
		isSorted := true

		beforeBody := d.InputOffset() // '{' 之后的偏移量
		for d.PeekKind() != '}' {
			beforeName := d.InputOffset()
			var flags jsonwire.ValueFlags
			name, _ := d.s.ReadValue(&flags)
			name = jsonwire.UnquoteMayCopy(name, flags.IsVerbatim())
			mustReorderObjectsFromDecoder(d, scratch)
			afterValue := d.InputOffset()

			currMember := objectMember{name, d.s.buf[beforeName:afterValue]}
			if isSorted && len(*members) > 0 {
				isSorted = objectMember.Compare(prevMember, currMember) < 0
			}
			*members = append(*members, currMember)
			prevMember = currMember
		}
		afterBody := d.InputOffset() // '}' 之前的偏移量
		d.ReadToken()

		// 对成员排序；如果已经有序则提前返回。
		if isSorted {
			return
		}
		firstBufferBeforeSorting := (*members)[0].buffer
		slices.SortFunc(*members, objectMember.Compare)
		firstBufferAfterSorting := (*members)[0].buffer

		// 将重新排序后的成员追加到新缓冲区中，
		// 然后将重新排序后的成员复制回原成员之上。
		// 避免就地交换，因为每个成员的大小可能不同，
		// 将成员移动到较小成员之上时，可能会在后续成员
		// 被移动之前破坏它们的数据。
		//
		// 以下不变式必须成立：
		//	sum([m.after-m.before for m in members]) == afterBody-beforeBody
		commaAndWhitespacePrefix := func(b []byte) []byte {
			return b[:len(b)-len(bytes.TrimLeft(b, commaAndWhitespace))]
		}
		sorted := (*scratch)[:0]
		for i, member := range *members {
			switch {
			case i == 0 && &member.buffer[0] != &firstBufferBeforeSorting[0]:
				// 排序后的第一个成员不是排序前的第一个成员，
				// 因此使用排序前第一个成员的前缀。
				sorted = append(sorted, commaAndWhitespacePrefix(firstBufferBeforeSorting)...)
				sorted = append(sorted, bytes.TrimLeft(member.buffer, commaAndWhitespace)...)
			case i != 0 && &member.buffer[0] == &firstBufferBeforeSorting[0]:
				// 排序后的后续成员是排序前的第一个成员，
				// 因此使用排序后第一个成员的前缀。
				sorted = append(sorted, commaAndWhitespacePrefix(firstBufferAfterSorting)...)
				sorted = append(sorted, bytes.TrimLeft(member.buffer, commaAndWhitespace)...)
			default:
				sorted = append(sorted, member.buffer...)
			}
		}
		if int(afterBody-beforeBody) != len(sorted) {
			panic("BUG: length invariant violated")
		}
		copy(d.s.buf[beforeBody:afterBody], sorted)

		// 将临时缓冲区更新为有史以来使用的最大量。
		if len(sorted) > len(*scratch) {
			*scratch = sorted
		}
	case '[':
		for d.PeekKind() != ']' {
			mustReorderObjectsFromDecoder(d, scratch)
		}
		d.ReadToken()
	default:
		if err != nil {
			panic("BUG: " + err.Error())
		}
	}
}
