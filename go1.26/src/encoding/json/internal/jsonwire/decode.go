// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsonwire

import (
	"io"
	"math"
	"slices"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

type ValueFlags uint

const (
	_ ValueFlags = (1 << iota) / 2 // 从零开始的二的幂

	stringNonVerbatim  // 字符串不能简单地被视为有效的 UTF-8
	stringNonCanonical // 字符串未按照 RFC 8785 第 3.2.2.2 节格式化。
	// TODO: 跟踪一个数字是否是非整数？
)

func (f *ValueFlags) Join(f2 ValueFlags) { *f |= f2 }
func (f ValueFlags) IsVerbatim() bool    { return f&stringNonVerbatim == 0 }
func (f ValueFlags) IsCanonical() bool   { return f&stringNonCanonical == 0 }

// ConsumeWhitespace 按 RFC 7159 第 2 节消费前导的 JSON 空白。
func ConsumeWhitespace(b []byte) (n int) {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	for len(b) > n && (b[n] == ' ' || b[n] == '\t' || b[n] == '\r' || b[n] == '\n') {
		n++
	}
	return n
}

// ConsumeNull 按 RFC 7159 第 3 节消费下一个 JSON null 字面量。
// 如果无效则返回 0，此时应改用 consumeLiteral。
func ConsumeNull(b []byte) int {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	const literal = "null"
	if len(b) >= len(literal) && string(b[:len(literal)]) == literal {
		return len(literal)
	}
	return 0
}

// ConsumeFalse 按 RFC 7159 第 3 节消费下一个 JSON false 字面量。
// 如果无效则返回 0，此时应改用 consumeLiteral。
func ConsumeFalse(b []byte) int {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	const literal = "false"
	if len(b) >= len(literal) && string(b[:len(literal)]) == literal {
		return len(literal)
	}
	return 0
}

// ConsumeTrue 按 RFC 7159 第 3 节消费下一个 JSON true 字面量。
// 如果无效则返回 0，此时应改用 consumeLiteral。
func ConsumeTrue(b []byte) int {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	const literal = "true"
	if len(b) >= len(literal) && string(b[:len(literal)]) == literal {
		return len(literal)
	}
	return 0
}

// ConsumeLiteral 按 RFC 7159 第 3 节消费下一个 JSON 字面量。
// 如果输入看起来被截断，则返回 io.ErrUnexpectedEOF。
func ConsumeLiteral(b []byte, lit string) (n int, err error) {
	for i := 0; i < len(b) && i < len(lit); i++ {
		if b[i] != lit[i] {
			return i, NewInvalidCharacterError(b[i:], "in literal "+lit+" (expecting "+strconv.QuoteRune(rune(lit[i]))+")")
		}
	}
	if len(b) < len(lit) {
		return len(b), io.ErrUnexpectedEOF
	}
	return len(lit), nil
}

// ConsumeSimpleString 按 RFC 7159 第 7 节消费下一个 JSON 字符串，
// 但仅限于不含转义序列的 ASCII 字符串文法。
// 如果无效或比简单字符串更复杂，则返回 0，
// 此时应调用 consumeString。
//
// 出于兼容性原因，它拒绝 '<'、'>' 和 '&'，因为
// 这些字符在 v1 实现中总是被转义。因此，如果此函数
// 返回非零值，我们就知道该字符串在 v1 或 v2 转义语义下
// 都会被以相同方式编码。
func ConsumeSimpleString(b []byte) (n int) {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	if len(b) > 0 && b[0] == '"' {
		n++
		for len(b) > n && b[n] < utf8.RuneSelf && escapeASCII[b[n]] == 0 {
			n++
		}
		if uint(len(b)) > uint(n) && b[n] == '"' {
			n++
			return n
		}
	}
	return 0
}

// ConsumeString 按 RFC 7159 第 7 节消费下一个 JSON 字符串。
// 如果 validateUTF8 为 false，则允许字符串本身中存在
// 无效的 UTF-8 字符。
// 它报告已消费的字节数以及是否遇到错误。
// 如果输入看起来被截断，则返回 io.ErrUnexpectedEOF。
func ConsumeString(flags *ValueFlags, b []byte, validateUTF8 bool) (n int, err error) {
	return ConsumeStringResumable(flags, b, 0, validateUTF8)
}

// ConsumeStringResumable 与 consumeString 相同，但支持从
// 之前返回了 io.ErrUnexpectedEOF 的调用处恢复。
func ConsumeStringResumable(flags *ValueFlags, b []byte, resumeOffset int, validateUTF8 bool) (n int, err error) {
	// 消费前导的双引号。
	switch {
	case resumeOffset > 0:
		n = resumeOffset // 已处理前导引号
	case uint(len(b)) == 0:
		return n, io.ErrUnexpectedEOF
	case b[0] == '"':
		n++
	default:
		return n, NewInvalidCharacterError(b[n:], `at start of string (expecting '"')`)
	}

	// 消费字符串中的每一个字符。
	for uint(len(b)) > uint(n) {
		// 针对长的未转义字符序列进行优化。
		noEscape := func(c byte) bool {
			return c < utf8.RuneSelf && ' ' <= c && c != '\\' && c != '"'
		}
		for uint(len(b)) > uint(n) && noEscape(b[n]) {
			n++
		}
		if uint(len(b)) <= uint(n) {
			return n, io.ErrUnexpectedEOF
		}

		// 检查作为终结的双引号。
		if b[n] == '"' {
			n++
			return n, nil
		}

		switch r, rn := utf8.DecodeRune(b[n:]); {
		// 处理 UTF-8 编码的字节序列。
		// 由于上面已对 ASCII 做了专门处理，我们知道
		// 此时所有普通序列都必须是 2 个字节或更长。
		case rn > 1:
			n += rn
		// 处理转义序列。
		case r == '\\':
			flags.Join(stringNonVerbatim)
			resumeOffset = n
			if uint(len(b)) < uint(n+2) {
				return resumeOffset, io.ErrUnexpectedEOF
			}
			switch r := b[n+1]; r {
			case '/':
				// 正斜杠是唯一具有 3 种表示形式的字符。
				// 根据 RFC 8785 第 3.2.2.2 节，它不得被转义。
				flags.Join(stringNonCanonical)
				n += 2
			case '"', '\\', 'b', 'f', 'n', 'r', 't':
				n += 2
			case 'u':
				if uint(len(b)) < uint(n+6) {
					if hasEscapedUTF16Prefix(b[n:], false) {
						return resumeOffset, io.ErrUnexpectedEOF
					}
					flags.Join(stringNonCanonical)
					return n, NewInvalidEscapeSequenceError(b[n:])
				}
				v1, ok := parseHexUint16(b[n+2 : n+6])
				if !ok {
					flags.Join(stringNonCanonical)
					return n, NewInvalidEscapeSequenceError(b[n : n+6])
				}
				// 只有某些控制字符可以为了规范格式化而使用 \uFFFF
				// 表示法（根据 RFC 8785 第 3.2.2.2 节）。
				switch v1 {
				// 这些字符不允许使用 \uFFFF 表示法。
				case '\b', '\f', '\n', '\r', '\t':
					flags.Join(stringNonCanonical)
				default:
					// \uFFFF 表示法仅允许用于控制字符。
					if v1 >= ' ' {
						flags.Join(stringNonCanonical)
					} else {
						// \uFFFF 表示法必须使用小写。
						for _, c := range b[n+2 : n+6] {
							if 'A' <= c && c <= 'F' {
								flags.Join(stringNonCanonical)
							}
						}
					}
				}
				n += 6

				r := rune(v1)
				if validateUTF8 && utf16.IsSurrogate(r) {
					if uint(len(b)) < uint(n+6) {
						if hasEscapedUTF16Prefix(b[n:], true) {
							return resumeOffset, io.ErrUnexpectedEOF
						}
						flags.Join(stringNonCanonical)
						return n - 6, NewInvalidEscapeSequenceError(b[n-6:])
					} else if v2, ok := parseHexUint16(b[n+2 : n+6]); b[n] != '\\' || b[n+1] != 'u' || !ok {
						flags.Join(stringNonCanonical)
						return n - 6, NewInvalidEscapeSequenceError(b[n-6 : n+6])
					} else if r = utf16.DecodeRune(rune(v1), rune(v2)); r == utf8.RuneError {
						flags.Join(stringNonCanonical)
						return n - 6, NewInvalidEscapeSequenceError(b[n-6 : n+6])
					} else {
						n += 6
					}
				}
			default:
				flags.Join(stringNonCanonical)
				return n, NewInvalidEscapeSequenceError(b[n : n+2])
			}
		// 处理无效的 UTF-8。
		case r == utf8.RuneError:
			if !utf8.FullRune(b[n:]) {
				return n, io.ErrUnexpectedEOF
			}
			flags.Join(stringNonVerbatim | stringNonCanonical)
			if validateUTF8 {
				return n, ErrInvalidUTF8
			}
			n++
		// 处理无效的控制字符。
		case r < ' ':
			flags.Join(stringNonVerbatim | stringNonCanonical)
			return n, NewInvalidCharacterError(b[n:], "in string (expecting non-control character)")
		default:
			panic("BUG: unhandled character " + QuoteRune(b[n:]))
		}
	}
	return n, io.ErrUnexpectedEOF
}

// AppendUnquote 将 src 中 JSON 字符串的去转义形式追加到 dst。
// 字符串中任何无效的 UTF-8 都会被替换为 utf8.RuneError，
// 但错误会被标记为遇到了此类错误。
// 输入必须是完整的 JSON 字符串，且周围没有空白。
func AppendUnquote[Bytes ~[]byte | ~string](dst []byte, src Bytes) (v []byte, err error) {
	dst = slices.Grow(dst, len(src))

	// 消费前导的双引号。
	var i, n int
	switch {
	case uint(len(src)) == 0:
		return dst, io.ErrUnexpectedEOF
	case src[0] == '"':
		i, n = 1, 1
	default:
		return dst, NewInvalidCharacterError(src, `at start of string (expecting '"')`)
	}

	// 消费字符串中的每一个字符。
	for uint(len(src)) > uint(n) {
		// 针对长的未转义字符序列进行优化。
		noEscape := func(c byte) bool {
			return c < utf8.RuneSelf && ' ' <= c && c != '\\' && c != '"'
		}
		for uint(len(src)) > uint(n) && noEscape(src[n]) {
			n++
		}
		if uint(len(src)) <= uint(n) {
			dst = append(dst, src[i:n]...)
			return dst, io.ErrUnexpectedEOF
		}

		// 检查作为终结的双引号。
		if src[n] == '"' {
			dst = append(dst, src[i:n]...)
			n++
			if n < len(src) {
				err = NewInvalidCharacterError(src[n:], "after string value")
			}
			return dst, err
		}

		switch r, rn := utf8.DecodeRuneInString(string(truncateMaxUTF8(src[n:]))); {
		// 处理 UTF-8 编码的字节序列。
		// 由于上面已对 ASCII 做了专门处理，我们知道
		// 此时所有普通序列都必须是 2 个字节或更长。
		case rn > 1:
			n += rn
		// 处理转义序列。
		case r == '\\':
			dst = append(dst, src[i:n]...)

			// 处理转义序列。
			if uint(len(src)) < uint(n+2) {
				return dst, io.ErrUnexpectedEOF
			}
			switch r := src[n+1]; r {
			case '"', '\\', '/':
				dst = append(dst, r)
				n += 2
			case 'b':
				dst = append(dst, '\b')
				n += 2
			case 'f':
				dst = append(dst, '\f')
				n += 2
			case 'n':
				dst = append(dst, '\n')
				n += 2
			case 'r':
				dst = append(dst, '\r')
				n += 2
			case 't':
				dst = append(dst, '\t')
				n += 2
			case 'u':
				if uint(len(src)) < uint(n+6) {
					if hasEscapedUTF16Prefix(src[n:], false) {
						return dst, io.ErrUnexpectedEOF
					}
					return dst, NewInvalidEscapeSequenceError(src[n:])
				}
				v1, ok := parseHexUint16(src[n+2 : n+6])
				if !ok {
					return dst, NewInvalidEscapeSequenceError(src[n : n+6])
				}
				n += 6

				// 检查这是否是一个代理项的一半。
				r := rune(v1)
				if utf16.IsSurrogate(r) {
					r = utf8.RuneError // 除非后续成功，否则假定失败
					if uint(len(src)) < uint(n+6) {
						if hasEscapedUTF16Prefix(src[n:], true) {
							return utf8.AppendRune(dst, r), io.ErrUnexpectedEOF
						}
						err = NewInvalidEscapeSequenceError(src[n-6:])
					} else if v2, ok := parseHexUint16(src[n+2 : n+6]); src[n] != '\\' || src[n+1] != 'u' || !ok {
						err = NewInvalidEscapeSequenceError(src[n-6 : n+6])
					} else if r = utf16.DecodeRune(rune(v1), rune(v2)); r == utf8.RuneError {
						err = NewInvalidEscapeSequenceError(src[n-6 : n+6])
					} else {
						n += 6
					}
				}

				dst = utf8.AppendRune(dst, r)
			default:
				return dst, NewInvalidEscapeSequenceError(src[n : n+2])
			}
			i = n
		// 处理无效的 UTF-8。
		case r == utf8.RuneError:
			dst = append(dst, src[i:n]...)
			if !utf8.FullRuneInString(string(truncateMaxUTF8(src[n:]))) {
				return dst, io.ErrUnexpectedEOF
			}
			// NOTE: 未转义的字符串可能比转义后的字符串更长，
			// 因为无效的 UTF-8 字节正在被替换。
			dst = append(dst, "\uFFFD"...)
			n += rn
			i = n
			err = ErrInvalidUTF8
		// 处理无效的控制字符。
		case r < ' ':
			dst = append(dst, src[i:n]...)
			return dst, NewInvalidCharacterError(src[n:], "in string (expecting non-control character)")
		default:
			panic("BUG: unhandled character " + QuoteRune(src[n:]))
		}
	}
	dst = append(dst, src[i:n]...)
	return dst, io.ErrUnexpectedEOF
}

// hasEscapedUTF16Prefix 报告 b 是否可能是
// 一个 \uFFFF 转义序列被截断后的前缀。
func hasEscapedUTF16Prefix[Bytes ~[]byte | ~string](b Bytes, lowerSurrogateHalf bool) bool {
	for i := range len(b) {
		switch c := b[i]; {
		case i == 0 && c != '\\':
			return false
		case i == 1 && c != 'u':
			return false
		case i == 2 && lowerSurrogateHalf && c != 'd' && c != 'D':
			return false // 不在 ['\uDC00':'\uDFFF'] 范围内
		case i == 3 && lowerSurrogateHalf && !('c' <= c && c <= 'f') && !('C' <= c && c <= 'F'):
			return false // 不在 ['\uDC00':'\uDFFF'] 范围内
		case i >= 2 && i < 6 && !('0' <= c && c <= '9') && !('a' <= c && c <= 'f') && !('A' <= c && c <= 'F'):
			return false
		}
	}
	return true
}

// UnquoteMayCopy 返回 b 的去转义形式。
// 如果没有转义字符，则输出只是去掉了两端引号的
// 输入的子切片。
// 否则，会为输出分配一个新的缓冲区。
// 它假定输入是有效的。
func UnquoteMayCopy(b []byte, isVerbatim bool) []byte {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	if isVerbatim {
		return b[len(`"`) : len(b)-len(`"`)]
	}
	b, _ = AppendUnquote(nil, b)
	return b
}

// ConsumeSimpleNumber 按 RFC 7159 第 6 节消费下一个 JSON 数字，
// 但仅限于正整数的文法。
// 如果无效或比简单整数更复杂，则返回 0，
// 此时应调用 consumeNumber。
func ConsumeSimpleNumber(b []byte) (n int) {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	if len(b) > 0 {
		if b[0] == '0' {
			n++
		} else if '1' <= b[0] && b[0] <= '9' {
			n++
			for len(b) > n && ('0' <= b[n] && b[n] <= '9') {
				n++
			}
		} else {
			return 0
		}
		if uint(len(b)) <= uint(n) || (b[n] != '.' && b[n] != 'e' && b[n] != 'E') {
			return n
		}
	}
	return 0
}

type ConsumeNumberState uint

const (
	consumeNumberInit ConsumeNumberState = iota
	beforeIntegerDigits
	withinIntegerDigits
	beforeFractionalDigits
	withinFractionalDigits
	beforeExponentDigits
	withinExponentDigits
)

// ConsumeNumber 按 RFC 7159 第 6 节消费下一个 JSON 数字。
// 它报告已消费的字节数以及是否遇到错误。
// 如果输入看起来被截断，则返回 io.ErrUnexpectedEOF。
//
// 注意 JSON 数字不是自终止的。
// 如果整个输入都被消费，那么调用方需要考虑
// 是否可能存在后续未读取的数据，它们仍可能是该数字的一部分。
func ConsumeNumber(b []byte) (n int, err error) {
	n, _, err = ConsumeNumberResumable(b, 0, consumeNumberInit)
	return n, err
}

// ConsumeNumberResumable 与 consumeNumber 相同，但支持从
// 之前返回了 io.ErrUnexpectedEOF 的调用处恢复。
func ConsumeNumberResumable(b []byte, resumeOffset int, state ConsumeNumberState) (n int, _ ConsumeNumberState, err error) {
	// 从部分消费处恢复时，跳转到正确的状态。
	n = resumeOffset
	if state > consumeNumberInit {
		switch state {
		case withinIntegerDigits, withinFractionalDigits, withinExponentDigits:
			// 消费前导数字。
			for uint(len(b)) > uint(n) && ('0' <= b[n] && b[n] <= '9') {
				n++
			}
			if uint(len(b)) <= uint(n) {
				return n, state, nil // 仍处于同一状态
			}
			state++ // 将 "withinX" 切换为 "beforeY"，其中 Y 是 X 之后的状态
		}
		switch state {
		case beforeIntegerDigits:
			goto beforeInteger
		case beforeFractionalDigits:
			goto beforeFractional
		case beforeExponentDigits:
			goto beforeExponent
		default:
			return n, state, nil
		}
	}

	// 消费必需的整数部分（可带负号）。
beforeInteger:
	resumeOffset = n
	if uint(len(b)) > 0 && b[0] == '-' {
		n++
	}
	switch {
	case uint(len(b)) <= uint(n):
		return resumeOffset, beforeIntegerDigits, io.ErrUnexpectedEOF
	case b[n] == '0':
		n++
		state = beforeFractionalDigits
	case '1' <= b[n] && b[n] <= '9':
		n++
		for uint(len(b)) > uint(n) && ('0' <= b[n] && b[n] <= '9') {
			n++
		}
		state = withinIntegerDigits
	default:
		return n, state, NewInvalidCharacterError(b[n:], "in number (expecting digit)")
	}

	// 消费可选的小数部分。
beforeFractional:
	if uint(len(b)) > uint(n) && b[n] == '.' {
		resumeOffset = n
		n++
		switch {
		case uint(len(b)) <= uint(n):
			return resumeOffset, beforeFractionalDigits, io.ErrUnexpectedEOF
		case '0' <= b[n] && b[n] <= '9':
			n++
		default:
			return n, state, NewInvalidCharacterError(b[n:], "in number (expecting digit)")
		}
		for uint(len(b)) > uint(n) && ('0' <= b[n] && b[n] <= '9') {
			n++
		}
		state = withinFractionalDigits
	}

	// 消费可选的指数部分。
beforeExponent:
	if uint(len(b)) > uint(n) && (b[n] == 'e' || b[n] == 'E') {
		resumeOffset = n
		n++
		if uint(len(b)) > uint(n) && (b[n] == '-' || b[n] == '+') {
			n++
		}
		switch {
		case uint(len(b)) <= uint(n):
			return resumeOffset, beforeExponentDigits, io.ErrUnexpectedEOF
		case '0' <= b[n] && b[n] <= '9':
			n++
		default:
			return n, state, NewInvalidCharacterError(b[n:], "in number (expecting digit)")
		}
		for uint(len(b)) > uint(n) && ('0' <= b[n] && b[n] <= '9') {
			n++
		}
		state = withinExponentDigits
	}

	return n, state, nil
}

// parseHexUint16 类似于 strconv.ParseUint，
// 但直接作用于 []byte，并针对 16 进制进行了优化。
// 参见 https://go.dev/issue/42429。
func parseHexUint16[Bytes ~[]byte | ~string](b Bytes) (v uint16, ok bool) {
	if len(b) != 4 {
		return 0, false
	}
	for i := range 4 {
		c := b[i]
		switch {
		case '0' <= c && c <= '9':
			c = c - '0'
		case 'a' <= c && c <= 'f':
			c = 10 + c - 'a'
		case 'A' <= c && c <= 'F':
			c = 10 + c - 'A'
		default:
			return 0, false
		}
		v = v*16 + uint16(c)
	}
	return v, true
}

// ParseUint 按照 JSON 数字文法的一个严格子集
// 将 b 解析为十进制无符号整数，如果有效则返回其值。
// 如果有语法错误则返回 (0, false)，
// 如果溢出则返回 (math.MaxUint64, false)。
func ParseUint(b []byte) (v uint64, ok bool) {
	const unsafeWidth = 20 // len(fmt.Sprint(uint64(math.MaxUint64)))
	var n int
	for ; len(b) > n && ('0' <= b[n] && b[n] <= '9'); n++ {
		v = 10*v + uint64(b[n]-'0')
	}
	switch {
	case n == 0 || len(b) != n || (b[0] == '0' && string(b) != "0"):
		return 0, false
	case n >= unsafeWidth && (b[0] != '1' || v < 1e19 || n > unsafeWidth):
		return math.MaxUint64, false
	}
	return v, true
}

// ParseFloat 按照 Go 浮点文法解析一个浮点数。
// 注意 JSON 数字文法是它的一个严格子集。
//
// 如果该数字超出了浮点数的有限表示范围，
// 则我们返回 MaxFloat，因为任何有限值在表示另一个
// 有限值时，其精度总是无限优于无穷值。
func ParseFloat(b []byte, bits int) (v float64, ok bool) {
	fv, err := strconv.ParseFloat(string(b), bits)
	if math.IsInf(fv, 0) {
		switch {
		case bits == 32 && math.IsInf(fv, +1):
			fv = +math.MaxFloat32
		case bits == 64 && math.IsInf(fv, +1):
			fv = +math.MaxFloat64
		case bits == 32 && math.IsInf(fv, -1):
			fv = -math.MaxFloat32
		case bits == 64 && math.IsInf(fv, -1):
			fv = -math.MaxFloat64
		}
	}
	return fv, err == nil
}
