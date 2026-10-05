// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

// jsonflags 实现了所有可选的布尔标志。
// 这些标志在 "json"、"jsontext" 和 "jsonopts" 之间共享。
package jsonflags

import "encoding/json/internal"

// Bools 表示零个或多个布尔标志，它们全部被设为 true 或 false。
// 最低有效位表示该集合中所有标志的布尔值。
// 其余位标识具体是哪些标志。
//
// 常见用法中，它会与 0 或 1 进行 OR 运算。例如：
//   - (AllowInvalidUTF8 | 0) 表示 "AllowInvalidUTF8 为 false"
//   - (Multiline | Indent | 1) 表示 "Multiline 和 Indent 为 true"
type Bools uint64

func (Bools) JSONOptions(internal.NotForPublicUse) {}

const (
	// AllFlags 是所有标志的集合。
	AllFlags = AllCoderFlags | AllArshalV2Flags | AllArshalV1Flags

	// AllCoderFlags 是所有编码器/解码器标志的集合。
	AllCoderFlags = (maxCoderFlag - 1) - initFlag

	// AllArshalV2Flags 是所有 v2 marshal/unmarshal 标志的集合。
	AllArshalV2Flags = (maxArshalV2Flag - 1) - (maxCoderFlag - 1)

	// AllArshalV1Flags 是所有 v1 marshal/unmarshal 标志的集合。
	AllArshalV1Flags = (maxArshalV1Flag - 1) - (maxArshalV2Flag - 1)

	// NonBooleanFlags 是非布尔标志的集合，
	// 其值为某种其他具体的 Go 类型。
	// 标志的值存储在 jsonopts.Struct 中。
	NonBooleanFlags = 0 |
		Indent |
		IndentPrefix |
		ByteLimit |
		DepthLimit |
		Marshalers |
		Unmarshalers

	// DefaultV1Flags 是在 v1 语义下默认值为 true 的布尔标志集合。
	// 没有任何非布尔标志在 v1 和 v2 之间存在差异。
	DefaultV1Flags = 0 |
		AllowDuplicateNames |
		AllowInvalidUTF8 |
		EscapeForHTML |
		EscapeForJS |
		PreserveRawStrings |
		Deterministic |
		FormatNilMapAsNull |
		FormatNilSliceAsNull |
		MatchCaseInsensitiveNames |
		CallMethodsWithLegacySemantics |
		FormatByteArrayAsArray |
		FormatBytesWithLegacySemantics |
		FormatDurationAsNano |
		MatchCaseSensitiveDelimiter |
		MergeWithLegacySemantics |
		OmitEmptyWithLegacySemantics |
		ParseBytesWithLooseRFC4648 |
		ParseTimeWithLooseRFC3339 |
		ReportErrorsWithLegacySemantics |
		StringifyWithLegacySemantics |
		UnmarshalArrayFromAnyLength

	// AnyWhitespace 报告编码后的输出是否可能包含任何空白。
	AnyWhitespace = Multiline | SpaceAfterColon | SpaceAfterComma

	// WhitespaceFlags 是与空白格式化相关的标志集合。
	// 与 AnyWhitespace 相比，它还包括 Indent 和 IndentPrefix，
	// 因为如果 Multiline 为 false，这些设置不会生效。
	WhitespaceFlags = AnyWhitespace | Indent | IndentPrefix

	// AnyEscape 是与 JSON 字符串中转义相关的标志集合。
	AnyEscape = EscapeForHTML | EscapeForJS

	// CanonicalizeNumbers 是与原始数字规范化相关的标志集合。
	CanonicalizeNumbers = CanonicalizeRawInts | CanonicalizeRawFloats
)

// 编码器和解码器标志。
const (
	initFlag Bools = 1 << iota // 保留给布尔值本身

	AllowDuplicateNames   // 编码或解码
	AllowInvalidUTF8      // 编码或解码
	WithinArshalCall      // 编码或解码；供 json.Marshal 和 json.Unmarshal 内部使用
	OmitTopLevelNewline   // 仅编码；供 json.Marshal 和 json.MarshalWrite 内部使用
	PreserveRawStrings    // 仅编码
	CanonicalizeRawInts   // 仅编码
	CanonicalizeRawFloats // 仅编码
	ReorderRawObjects     // 仅编码
	EscapeForHTML         // 仅编码
	EscapeForJS           // 仅编码
	Multiline             // 仅编码
	SpaceAfterColon       // 仅编码
	SpaceAfterComma       // 仅编码
	Indent                // 仅编码；非布尔标志
	IndentPrefix          // 仅编码；非布尔标志
	ByteLimit             // 编码或解码；非布尔标志
	DepthLimit            // 编码或解码；非布尔标志

	maxCoderFlag
)

// Marshal 和 Unmarshal 标志（用于 v2）。
const (
	_ Bools = (maxCoderFlag >> 1) << iota

	StringifyNumbers          // marshal 或 unmarshal
	Deterministic             // 仅 marshal
	FormatNilMapAsNull        // 仅 marshal
	FormatNilSliceAsNull      // 仅 marshal
	OmitZeroStructFields      // 仅 marshal
	MatchCaseInsensitiveNames // marshal 或 unmarshal
	DiscardUnknownMembers     // 仅 marshal
	RejectUnknownMembers      // 仅 unmarshal
	Marshalers                // 仅 marshal；非布尔标志
	Unmarshalers              // 仅 unmarshal；非布尔标志

	maxArshalV2Flag
)

// Marshal 和 Unmarshal 标志（用于 v1）。
const (
	_ Bools = (maxArshalV2Flag >> 1) << iota

	CallMethodsWithLegacySemantics  // marshal 或 unmarshal
	FormatByteArrayAsArray          // marshal 或 unmarshal
	FormatBytesWithLegacySemantics  // marshal 或 unmarshal
	FormatDurationAsNano            // marshal 或 unmarshal
	MatchCaseSensitiveDelimiter     // marshal 或 unmarshal
	MergeWithLegacySemantics        // unmarshal
	OmitEmptyWithLegacySemantics    // marshal
	ParseBytesWithLooseRFC4648      // unmarshal
	ParseTimeWithLooseRFC3339       // unmarshal
	ReportErrorsWithLegacySemantics // marshal 或 unmarshal
	StringifyWithLegacySemantics    // marshal 或 unmarshal
	StringifyBoolsAndStrings        // marshal 或 unmarshal；供 jsonv2.makeStructArshaler 内部使用
	UnmarshalAnyWithRawNumber       // unmarshal；供 jsonv1.Decoder.UseNumber 内部使用
	UnmarshalArrayFromAnyLength     // unmarshal

	maxArshalV1Flag
)

// bitsUsed 是 64 位布尔标志中使用的位数
const bitsUsed = 42

// 静态编译期检查，确保 bitsUsed 与 maxArshalV1Flag 保持同步。
const _ = uint64((1<<bitsUsed)-maxArshalV1Flag) + uint64(maxArshalV1Flag-(1<<bitsUsed))

// Flags 是一组布尔标志。
// 如果存在位为零，那么值位也必须为零。
// 两个字段的最低有效位始终为零。
//
// 与 Bools 不同（Bools 可以表示一组全为 true 或全为 false 的布尔值），
// Flags 表示一组布尔值，其中每个可以分别为 true 或 false。
type Flags struct{ Presence, Values uint64 }

// Join 合并两组标志，后者优先。
func (dst *Flags) Join(src Flags) {
	// 把源的所有存在位复制到目标（使用 OR），
	// 然后取反源的存在位以清除源的值（使用 AND-NOT），
	// 再把源的值位复制到目标（使用 OR）。
	//	e.g., dst := Flags{Presence: 0b_1100_0011, Values: 0b_1000_0011}
	//	e.g., src := Flags{Presence: 0b_0101_1010, Values: 0b_1001_0010}
	dst.Presence |= src.Presence // e.g., 0b_1100_0011 | 0b_0101_1010 -> 0b_110_11011
	dst.Values &= ^src.Presence  // e.g., 0b_1000_0011 & 0b_1010_0101 -> 0b_100_00001
	dst.Values |= src.Values     // e.g., 0b_1000_0001 | 0b_1001_0010 -> 0b_100_10011
}

// Set 为所提供的布尔值（或布尔值集合）同时设置存在位和值。
func (fs *Flags) Set(f Bools) {
	// 选出标志标识符的位（除 LSB 之外的所有位），
	// 然后为所有标识符位设置存在位（使用 OR），
	// 再取反标识符位以清除对应的值（使用 AND-NOT），
	// 最后如果 LSB 为 1，则把所有标识符位复制到值。
	//	e.g., fs := Flags{Presence: 0b_0101_0010, Values: 0b_0001_0010}
	//	e.g., f := 0b_1001_0001
	id := uint64(f) &^ uint64(1)  // e.g., 0b_1001_0001 & 0b_1111_1110 -> 0b_1001_0000
	fs.Presence |= id             // e.g., 0b_0101_0010 | 0b_1001_0000 -> 0b_1101_0011
	fs.Values &= ^id              // e.g., 0b_0001_0010 & 0b_0110_1111 -> 0b_0000_0010
	fs.Values |= uint64(f&1) * id // e.g., 0b_0000_0010 | 0b_1001_0000 -> 0b_1001_0010
}

// Get 报告该布尔值（或其中任一布尔值）是否为 true。
// 它通常只用于单个布尔值。
// f 的值位（即 LSB）会被忽略。
func (fs Flags) Get(f Bools) bool {
	return fs.Values&uint64(f) > 0
}

// Has 报告该布尔值（或其中任一布尔值）是否已设置。
// f 的值位（即 LSB）会被忽略。
func (fs Flags) Has(f Bools) bool {
	return fs.Presence&uint64(f) > 0
}

// Clear 清除所提供的布尔值或布尔值集合的存在位和值。
// f 的值位（即 LSB）会被忽略。
func (fs *Flags) Clear(f Bools) {
	// 取反 f 生成一个掩码，以清除 f 中的所有位（使用 AND）。
	//	e.g., fs := Flags{Presence: 0b_0101_0010, Values: 0b_0001_0010}
	//	e.g., f := 0b_0001_1000
	mask := uint64(^f)  // e.g., 0b_0001_1000 -> 0b_1110_0111
	fs.Presence &= mask // e.g., 0b_0101_0010 &  0b_1110_0111 -> 0b_0100_0010
	fs.Values &= mask   // e.g., 0b_0001_0010 &  0b_1110_0111 -> 0b_0000_0010
}
