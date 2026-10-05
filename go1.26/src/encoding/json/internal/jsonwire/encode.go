// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsonwire

import (
	"math"
	"slices"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"encoding/json/internal/jsonflags"
)

// escapeASCII 报告该 ASCII 字符是否需要转义。
// 它保守地假定启用了 EscapeForHTML。
var escapeASCII = [...]uint8{
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 转义控制字符
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, // 转义控制字符
	0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, // 转义 '"' 和 '&'
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0, // 转义 '<' 和 '>'
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, // 转义 '\\'
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
}

// NeedEscape 报告 src 是否有任何字符需要转义。
// 它保守地假定启用了 EscapeForHTML 和 EscapeForJS。
// 对于包含无效 UTF-8 的输入，它报告 true。
func NeedEscape[Bytes ~[]byte | ~string](src Bytes) bool {
	var i int
	for uint(len(src)) > uint(i) {
		if c := src[i]; c < utf8.RuneSelf {
			if escapeASCII[c] > 0 {
				return true
			}
			i++
		} else {
			r, rn := utf8.DecodeRuneInString(string(truncateMaxUTF8(src[i:])))
			if r == utf8.RuneError || r == '\u2028' || r == '\u2029' {
				return true
			}
			i += rn
		}
	}
	return false
}

// AppendQuote 按 RFC 7159 第 7 节将 src 作为 JSON 字符串追加到 dst。
//
// 它接收 flags 并遵循以下规则：
//   - EscapeForHTML 转义 '<'、'>' 和 '&'。
//   - EscapeForJS 转义 '\u2028' 和 '\u2029'。
//   - AllowInvalidUTF8 避免对无效 UTF-8 报告错误。
//
// 无论是否指定 AllowInvalidUTF8，
// 无效字节都会被替换为 Unicode 替换字符（'\ufffd'）。
// 如果未设置任何转义标志，则使用最短的可表示形式，
// 这也正是字符串的规范形式（RFC 8785 第 3.2.2.2 节）。
func AppendQuote[Bytes ~[]byte | ~string](dst []byte, src Bytes, flags *jsonflags.Flags) ([]byte, error) {
	var i, n int
	var hasInvalidUTF8 bool
	dst = slices.Grow(dst, len(`"`)+len(src)+len(`"`))
	dst = append(dst, '"')
	for uint(len(src)) > uint(n) {
		if c := src[n]; c < utf8.RuneSelf {
			// 处理单字节 ASCII。
			n++
			if escapeASCII[c] == 0 {
				continue // 可能不需要转义
			}
			// 处理单字节 ASCII 的转义。
			if !(c == '<' || c == '>' || c == '&') || flags.Get(jsonflags.EscapeForHTML) {
				dst = append(dst, src[i:n-1]...)
				dst = appendEscapedASCII(dst, c)
				i = n
			}
		} else {
			// 处理多字节 Unicode。
			r, rn := utf8.DecodeRuneInString(string(truncateMaxUTF8(src[n:])))
			n += rn
			if r != utf8.RuneError && r != '\u2028' && r != '\u2029' {
				continue // 可能不需要转义
			}
			// 处理多字节 Unicode 的转义。
			switch {
			case isInvalidUTF8(r, rn):
				hasInvalidUTF8 = true
				dst = append(dst, src[i:n-rn]...)
				dst = append(dst, "\ufffd"...)
				i = n
			case (r == '\u2028' || r == '\u2029') && flags.Get(jsonflags.EscapeForJS):
				dst = append(dst, src[i:n-rn]...)
				dst = appendEscapedUnicode(dst, r)
				i = n
			}
		}
	}
	dst = append(dst, src[i:n]...)
	dst = append(dst, '"')
	if hasInvalidUTF8 && !flags.Get(jsonflags.AllowInvalidUTF8) {
		return dst, ErrInvalidUTF8
	}
	return dst, nil
}

func appendEscapedASCII(dst []byte, c byte) []byte {
	switch c {
	case '"', '\\':
		dst = append(dst, '\\', c)
	case '\b':
		dst = append(dst, "\\b"...)
	case '\f':
		dst = append(dst, "\\f"...)
	case '\n':
		dst = append(dst, "\\n"...)
	case '\r':
		dst = append(dst, "\\r"...)
	case '\t':
		dst = append(dst, "\\t"...)
	default:
		dst = appendEscapedUTF16(dst, uint16(c))
	}
	return dst
}

func appendEscapedUnicode(dst []byte, r rune) []byte {
	if r1, r2 := utf16.EncodeRune(r); r1 != '\ufffd' && r2 != '\ufffd' {
		dst = appendEscapedUTF16(dst, uint16(r1))
		dst = appendEscapedUTF16(dst, uint16(r2))
	} else {
		dst = appendEscapedUTF16(dst, uint16(r))
	}
	return dst
}

func appendEscapedUTF16(dst []byte, x uint16) []byte {
	const hex = "0123456789abcdef"
	return append(dst, '\\', 'u', hex[(x>>12)&0xf], hex[(x>>8)&0xf], hex[(x>>4)&0xf], hex[(x>>0)&0xf])
}

// ReformatString 从 src 消费一个 JSON 字符串并将其追加到 dst，
// 必要时根据指定的 flags 对其进行重新格式化。
// 它返回追加后的输出以及已消费的输入字节数。
func ReformatString(dst, src []byte, flags *jsonflags.Flags) ([]byte, int, error) {
	// TODO: 这里是否应该更新 ValueFlags 作为输入？
	var valFlags ValueFlags
	n, err := ConsumeString(&valFlags, src, !flags.Get(jsonflags.AllowInvalidUTF8))
	if err != nil {
		return dst, n, err
	}

	// 如果输出不需要特殊的转义，并且输入
	// 已经处于规范形式或应被逐字保留，
	// 那么就直接把输入复制到输出。
	if !flags.Get(jsonflags.AnyEscape) &&
		(valFlags.IsCanonical() || flags.Get(jsonflags.PreserveRawStrings)) {
		dst = append(dst, src[:n]...) // 逐字复制该字符串
		return dst, n, nil
	}

	// 在 [jsonflags.PreserveRawStrings] 下，任何预先转义的序列
	// 保持转义状态，但我们仍然需要遵循
	// [jsonflags.EscapeForHTML] 和 [jsonflags.EscapeForJS] 选项。
	if flags.Get(jsonflags.PreserveRawStrings) {
		var i, lastAppendIndex int
		for i < n {
			if c := src[i]; c < utf8.RuneSelf {
				if (c == '<' || c == '>' || c == '&') && flags.Get(jsonflags.EscapeForHTML) {
					dst = append(dst, src[lastAppendIndex:i]...)
					dst = appendEscapedASCII(dst, c)
					lastAppendIndex = i + 1
				}
				i++
			} else {
				r, rn := utf8.DecodeRune(truncateMaxUTF8(src[i:]))
				if (r == '\u2028' || r == '\u2029') && flags.Get(jsonflags.EscapeForJS) {
					dst = append(dst, src[lastAppendIndex:i]...)
					dst = appendEscapedUnicode(dst, r)
					lastAppendIndex = i + rn
				}
				i += rn
			}
		}
		return append(dst, src[lastAppendIndex:n]...), n, nil
	}

	// 输入包含可能需要转义的字符、
	// 不必要的转义序列，或无效的 UTF-8。
	// 执行一次往返的去引号和加引号，以根据当前 flags
	// 正确重新格式化这些序列。
	b, _ := AppendUnquote(nil, src[:n])
	dst, _ = AppendQuote(dst, b, flags)
	return dst, n, nil
}

// AppendFloat 按 RFC 7159 第 6 节将 src 作为 JSON 数字追加到 dst。
// 它格式化数字的方式类似于 ES6 的数字到字符串转换。
// 参见 https://go.dev/issue/14135。
//
// 对于 64 位浮点数，输出与 ECMA-262 第 6 版 7.1.12.1 节以及
// RFC 8785 第 3.2.2.3 节一致，但 -0 除外，
// 它被格式化为 -0 而不是仅仅 0。
//
// 对于 32 位浮点数，
// 输出是该算法对应的 32 位版本。
// 注意 ECMA-262 并未为 32 位数字指定任何算法。
func AppendFloat(dst []byte, src float64, bits int) []byte {
	if bits == 32 {
		src = float64(float32(src))
	}

	abs := math.Abs(src)
	fmt := byte('f')
	if abs != 0 {
		if bits == 64 && (float64(abs) < 1e-6 || float64(abs) >= 1e21) ||
			bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			fmt = 'e'
		}
	}
	dst = strconv.AppendFloat(dst, src, fmt, -1, bits)
	if fmt == 'e' {
		// 将 e-09 清理为 e-9。
		n := len(dst)
		if n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst
}

// ReformatNumber 从 src 消费一个 JSON 数字并将其追加到 dst，
// 如果指定了则对其进行规范化。
// 它返回追加后的输出以及已消费的输入字节数。
func ReformatNumber(dst, src []byte, flags *jsonflags.Flags) ([]byte, int, error) {
	n, err := ConsumeNumber(src)
	if err != nil {
		return dst, n, err
	}
	if !flags.Get(jsonflags.CanonicalizeNumbers) {
		dst = append(dst, src[:n]...) // 逐字复制该数字
		return dst, n, nil
	}

	// 识别数字的种类。
	var isFloat bool
	for _, c := range src[:n] {
		if c == '.' || c == 'e' || c == 'E' {
			isFloat = true // 含小数部分或指数
			break
		}
	}

	// 检查是否需要对此类数字进行规范化。
	switch {
	case string(src[:n]) == "-0":
		break // 无论何种类型，都将 -0 规范化为 0
	case isFloat:
		if !flags.Get(jsonflags.CanonicalizeRawFloats) {
			dst = append(dst, src[:n]...) // 逐字复制该数字
			return dst, n, nil
		}
	default:
		// 作为优化，我们可以逐字复制小于 2⁵³ 的整数，
		// 因为其规范形式始终相同。
		const maxExactIntegerDigits = 16 // len(strconv.AppendUint(nil, 1<<53, 10))
		if !flags.Get(jsonflags.CanonicalizeRawInts) || n < maxExactIntegerDigits {
			dst = append(dst, src[:n]...) // 逐字复制该数字
			return dst, n, nil
		}
	}

	// 解析并重新格式化该数字（使用规范格式）。
	fv, _ := strconv.ParseFloat(string(src[:n]), 64)
	switch {
	case fv == 0:
		fv = 0 // 将负零规范化为零
	case math.IsInf(fv, +1):
		fv = +math.MaxFloat64
	case math.IsInf(fv, -1):
		fv = -math.MaxFloat64
	}
	return AppendFloat(dst, fv, 64), n, nil
}
