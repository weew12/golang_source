// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package strconv 实现了基本数据类型与字符串表示之间的相互转换。
//
// # 数值转换
//
// 最常见的数值转换是 [Atoi]（字符串转 int）和 [Itoa]（int 转字符串）。
//
//	i, err := strconv.Atoi("-42")
//	s := strconv.Itoa(-42)
//
// 这些函数假定使用十进制和 Go 的 int 类型。
//
// [ParseBool]、[ParseFloat]、[ParseInt] 和 [ParseUint] 将字符串转换为值：
//
//	b, err := strconv.ParseBool("true")
//	f, err := strconv.ParseFloat("3.1415", 64)
//	i, err := strconv.ParseInt("-42", 10, 64)
//	u, err := strconv.ParseUint("42", 10, 64)
//
// 解析函数返回最宽的类型（float64、int64 和 uint64），但如果 size 参数指定了更窄的宽度，结果可以转换为该窄类型而不会丢失数据：
//
//	s := "2147483647" // biggest int32
//	i64, err := strconv.ParseInt(s, 10, 32)
//	...
//	i := int32(i64)
//
// [FormatBool]、[FormatFloat]、[FormatInt] 和 [FormatUint] 将值转换为字符串：
//
//	s := strconv.FormatBool(true)
//	s := strconv.FormatFloat(3.1415, 'E', -1, 64)
//	s := strconv.FormatInt(-42, 16)
//	s := strconv.FormatUint(42, 16)
//
// [AppendBool]、[AppendFloat]、[AppendInt] 和 [AppendUint] 类似，但会将格式化后的值追加到目标切片中。
//
// # 字符串转换
//
// [Quote] 和 [QuoteToASCII] 将字符串转换为带引号的 Go 字符串字面量。后者通过使用 \u 转义任何非 ASCII Unicode 字符，确保结果是 ASCII 字符串：
//
//	q := strconv.Quote("Hello, 世界")
//	q := strconv.QuoteToASCII("Hello, 世界")
//
// [QuoteRune] 和 [QuoteRuneToASCII] 类似，但接受 rune 并返回带引号的 Go rune 字面量。
//
// [Unquote] 和 [UnquoteChar] 对 Go 字符串和 rune 字面量进行解引用。
package strconv
