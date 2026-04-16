// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// utf16 包实现了 UTF-16 序列的编码和解码。
package utf16

// replacementChar==unicode.ReplacementChar 和 maxRune==unicode.MaxRune
// 这些条件在测试中已验证。
// 在本地定义它们可以避免此包依赖 unicode 包。

const (
	replacementChar = '\uFFFD'     // Unicode 替换字符
	maxRune         = '\U0010FFFF' // 最大有效 Unicode 码点。
)

const (
	// 0xd800-0xdc00 编码一个代理对的高 10 位。
	// 0xdc00-0xe000 编码一个代理对的低 10 位。
	// 值为这 20 位加上 0x10000。
	surr1 = 0xd800
	surr2 = 0xdc00
	surr3 = 0xe000

	surrSelf = 0x10000
)

// IsSurrogate 报告指定的 Unicode 码点是否可以出现在代理对中。
func IsSurrogate(r rune) bool {
	return surr1 <= r && r < surr3
}

// DecodeRune 返回代理对的 UTF-16 解码结果。
// 如果该对不是有效的 UTF-16 代理对，DecodeRune 返回
// Unicode 替换码点 U+FFFD。
func DecodeRune(r1, r2 rune) rune {
	if surr1 <= r1 && r1 < surr2 && surr2 <= r2 && r2 < surr3 {
		return (r1-surr1)<<10 | (r2 - surr2) + surrSelf
	}
	return replacementChar
}

// EncodeRune 返回给定 rune 的 UTF-16 代理对 r1、r2。
// 如果该 rune 不是有效的 Unicode 码点或不需要编码，
// EncodeRune 返回 U+FFFD, U+FFFD。
func EncodeRune(r rune) (r1, r2 rune) {
	if r < surrSelf || r > maxRune {
		return replacementChar, replacementChar
	}
	r -= surrSelf
	return surr1 + (r>>10)&0x3ff, surr2 + r&0x3ff
}

// RuneLen 返回该 rune 的 UTF-16 编码中 16 位字的数量。
// 如果该 rune 不是可用 UTF-16 编码的有效值，则返回 -1。
func RuneLen(r rune) int {
	switch {
	case 0 <= r && r < surr1, surr3 <= r && r < surrSelf:
		return 1
	case surrSelf <= r && r <= maxRune:
		return 2
	default:
		return -1
	}
}

// Encode 返回 Unicode 码点序列 s 的 UTF-16 编码。
func Encode(s []rune) []uint16 {
	n := len(s)
	for _, v := range s {
		if v >= surrSelf {
			n++
		}
	}

	a := make([]uint16, n)
	n = 0
	for _, v := range s {
		switch RuneLen(v) {
		case 1: // 普通 rune
			a[n] = uint16(v)
			n++
		case 2: // 需要代理序列
			r1, r2 := EncodeRune(v)
			a[n] = uint16(r1)
			a[n+1] = uint16(r2)
			n += 2
		default:
			a[n] = uint16(replacementChar)
			n++
		}
	}
	return a[:n]
}

// AppendRune 将 Unicode 码点 r 的 UTF-16 编码追加到 p 的末尾并返回
// 扩展后的缓冲区。如果该 rune 不是有效的 Unicode 码点，
// 则追加 U+FFFD 的编码。
func AppendRune(a []uint16, r rune) []uint16 {
	// 此函数可内联以快速处理 ASCII。
	switch {
	case 0 <= r && r < surr1, surr3 <= r && r < surrSelf:
		// 普通 rune
		return append(a, uint16(r))
	case surrSelf <= r && r <= maxRune:
		// 需要代理序列
		r1, r2 := EncodeRune(r)
		return append(a, uint16(r1), uint16(r2))
	}
	return append(a, replacementChar)
}

// Decode 返回 UTF-16 编码 s 所表示的 Unicode 码点序列。
func Decode(s []uint16) []rune {
	// 预分配最多可容纳 64 个 rune 的容量。
	// Decode 可内联，因此分配可以在栈上进行。
	buf := make([]rune, 0, 64)
	return decode(s, buf)
}

// decode 将 UTF-16 编码 s 所表示的 Unicode 码点序列追加到 buf 并返回扩展后的缓冲区。
func decode(s []uint16, buf []rune) []rune {
	for i := 0; i < len(s); i++ {
		var ar rune
		switch r := s[i]; {
		case r < surr1, surr3 <= r:
			// 普通 rune
			ar = rune(r)
		case surr1 <= r && r < surr2 && i+1 < len(s) &&
			surr2 <= s[i+1] && s[i+1] < surr3:
			// 有效的代理序列
			ar = DecodeRune(rune(r), rune(s[i+1]))
			i++
		default:
			// 无效的代理序列
			ar = replacementChar
		}
		buf = append(buf, ar)
	}
	return buf
}
