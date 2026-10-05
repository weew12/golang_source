// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"bytes"
	"errors"
	"math"
	"strconv"

	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonwire"
)

// 注意：Token 类似于 v1 的 json.Token。

const (
	maxInt64  = math.MaxInt64
	minInt64  = math.MinInt64
	maxUint64 = math.MaxUint64
	minUint64 = 0 // 为了保持一致性和可读性

	invalidTokenPanic = "invalid jsontext.Token; it has been voided by a subsequent json.Decoder call"
)

var errInvalidToken = errors.New("invalid jsontext.Token")

// Token 表示一个词法 JSON 令牌，它可以是以下之一：
//   - JSON 字面量（即 null、true 或 false）
//   - JSON 字符串（例如 "hello, world!"）
//   - JSON 数字（例如 123.456）
//   - JSON 对象的起始或结束定界符（即 { 或 } ）
//   - JSON 数组的起始或结束定界符（即 [ 或 ] ）
//
// Token 不能表示整个数组或对象值，而 [Value] 可以。
// 没有用于表示逗号和冒号的 Token，因为
// 这些结构令牌可以从上下文中推断出来。
type Token struct {
	nonComparable

	// 令牌可以以“raw”或“exact”形式存在。
	// Decoder 产生的令牌为“raw”形式。
	// 构造函数返回的令牌通常为“exact”形式。
	// Encoder 接受“raw”或“exact”形式的令牌。
	//
	// 下表显示了每种 Token 类型可能的值：
	//	╔═════════════════╦════════════╤════════════╤════════════╗
	//	║ Token type      ║ raw field  │ str field  │ num field  ║
	//	╠═════════════════╬════════════╪════════════╪════════════╣
	//	║ null   (raw)    ║ "null"     │ ""         │ 0          ║
	//	║ false  (raw)    ║ "false"    │ ""         │ 0          ║
	//	║ true   (raw)    ║ "true"     │ ""         │ 0          ║
	//	║ string (raw)    ║ non-empty  │ ""         │ offset     ║
	//	║ string (string) ║ nil        │ non-empty  │ 0          ║
	//	║ number (raw)    ║ non-empty  │ ""         │ offset     ║
	//	║ number (float)  ║ nil        │ "f"        │ non-zero   ║
	//	║ number (int64)  ║ nil        │ "i"        │ non-zero   ║
	//	║ number (uint64) ║ nil        │ "u"        │ non-zero   ║
	//	║ object (delim)  ║ "{" or "}" │ ""         │ 0          ║
	//	║ array  (delim)  ║ "[" or "]" │ ""         │ 0          ║
	//	╚═════════════════╩════════════╧════════════╧════════════╝
	//
	// 说明：
	//   - 对于以“raw”形式存储的令牌，num 字段包含由
	//     raw.previousOffsetStart() 确定的绝对偏移量。
	//     缓冲区本身存储在 raw.previousBuffer() 中。
	//   - JSON 字面量和结构字符始终为“raw”形式。
	//   - JSON 字符串和数字可以是“raw”或“exact”形式。
	//   - “exact”形式中 JSON 字符串和数字的精确零值
	//     表示存在歧义。因此，它们始终以“raw”形式表示。

	// raw 包含对原始解码缓冲区的引用。
	// 如果非 nil，则其值优先于 str 和 num。
	// 仅当 num == raw.previousOffsetStart() 时它才有效。
	raw *decodeBuffer

	// 如果 num 为零，则 str 是未转义的 JSON 字符串。
	// 否则，如果 num 应被解释为 float64、int64 或 uint64，
	// 则它是 "f"、"i" 或 "u"。
	str string

	// num 是以 uint64 值存储的 float64、int64 或 uint64。
	// 对于“exact”形式的任何 JSON 数字，它都非零。
	num uint64
}

// TODO: 将 1 字节定界符表示为 *decodeBuffer 是否会导致性能问题？

var (
	Null  Token = rawToken("null")
	False Token = rawToken("false")
	True  Token = rawToken("true")

	BeginObject Token = rawToken("{")
	EndObject   Token = rawToken("}")
	BeginArray  Token = rawToken("[")
	EndArray    Token = rawToken("]")

	zeroString Token = rawToken(`""`)
	zeroNumber Token = rawToken(`0`)

	nanString  Token = String("NaN")
	pinfString Token = String("Infinity")
	ninfString Token = String("-Infinity")
)

func rawToken(s string) Token {
	return Token{raw: &decodeBuffer{buf: []byte(s), prevStart: 0, prevEnd: len(s)}}
}

// Bool 构造一个表示 JSON 布尔值的 Token。
func Bool(b bool) Token {
	if b {
		return True
	}
	return False
}

// String 构造一个表示 JSON 字符串的 Token。
// 所提供的字符串应包含有效的 UTF-8，否则无效字符
// 可能会被替换为 Unicode 替换字符。
func String(s string) Token {
	if len(s) == 0 {
		return zeroString
	}
	return Token{str: s}
}

// Float 构造一个表示 JSON 数字的 Token。
// 值 NaN、+Inf 和 -Inf 将被表示为
// 值为 "NaN"、"Infinity" 和 "-Infinity" 的 JSON 字符串。
func Float(n float64) Token {
	switch {
	case math.Float64bits(n) == 0:
		return zeroNumber
	case math.IsNaN(n):
		return nanString
	case math.IsInf(n, +1):
		return pinfString
	case math.IsInf(n, -1):
		return ninfString
	}
	return Token{str: "f", num: math.Float64bits(n)}
}

// Int 从 int64 构造一个表示 JSON 数字的 Token。
func Int(n int64) Token {
	if n == 0 {
		return zeroNumber
	}
	return Token{str: "i", num: uint64(n)}
}

// Uint 从 uint64 构造一个表示 JSON 数字的 Token。
func Uint(n uint64) Token {
	if n == 0 {
		return zeroNumber
	}
	return Token{str: "u", num: uint64(n)}
}

// Clone 复制 Token，使其值在后续的 [Decoder.Read] 调用之后仍然有效。
func (t Token) Clone() Token {
	// TODO: 允许调用方避免任何分配？
	if raw := t.raw; raw != nil {
		// 避免复制全局变量。
		if t.raw.prevStart == 0 {
			switch t.raw {
			case Null.raw:
				return Null
			case False.raw:
				return False
			case True.raw:
				return True
			case BeginObject.raw:
				return BeginObject
			case EndObject.raw:
				return EndObject
			case BeginArray.raw:
				return BeginArray
			case EndArray.raw:
				return EndArray
			}
		}

		if uint64(raw.previousOffsetStart()) != t.num {
			panic(invalidTokenPanic)
		}
		buf := bytes.Clone(raw.previousBuffer())
		return Token{raw: &decodeBuffer{buf: buf, prevStart: 0, prevEnd: len(buf)}}
	}
	return t
}

// Bool 返回 JSON 布尔值的值。
// 如果令牌种类不是 JSON 布尔值，它会 panic。
func (t Token) Bool() bool {
	switch t.raw {
	case True.raw:
		return true
	case False.raw:
		return false
	default:
		panic("invalid JSON token kind: " + t.Kind().String())
	}
}

// appendString 将 JSON 字符串追加到 dst 并返回它。
// 如果 t 不是 JSON 字符串，它会 panic。
func (t Token) appendString(dst []byte, flags *jsonflags.Flags) ([]byte, error) {
	if raw := t.raw; raw != nil {
		// 处理原始字符串值。
		buf := raw.previousBuffer()
		if Kind(buf[0]) == '"' {
			if jsonwire.ConsumeSimpleString(buf) == len(buf) {
				return append(dst, buf...), nil
			}
			dst, _, err := jsonwire.ReformatString(dst, buf, flags)
			return dst, err
		}
	} else if len(t.str) != 0 && t.num == 0 {
		// 处理精确字符串值。
		return jsonwire.AppendQuote(dst, t.str, flags)
	}

	panic("invalid JSON token kind: " + t.Kind().String())
}

// String 返回 JSON 字符串的未转义字符串值。
// 对于其他 JSON 种类，它返回原始 JSON 表示。
func (t Token) String() string {
	// 这是可内联的，以便利用“函数外联”。
	// 如果调用方不以转义方式使用该字符串，这可以避免
	// 为 string(b) 转换分配内存。
	// 参见 https://blog.filippo.io/efficient-go-apis-with-the-inliner/
	s, b := t.string()
	if len(b) > 0 {
		return string(b)
	}
	return s
}
func (t Token) string() (string, []byte) {
	if raw := t.raw; raw != nil {
		if uint64(raw.previousOffsetStart()) != t.num {
			panic(invalidTokenPanic)
		}
		buf := raw.previousBuffer()
		if buf[0] == '"' {
			// TODO: 在 Token 中保留 ValueFlags？
			isVerbatim := jsonwire.ConsumeSimpleString(buf) == len(buf)
			return "", jsonwire.UnquoteMayCopy(buf, isVerbatim)
		}
		// 为 fmt.Stringer 处理非 JSON 字符串的令牌。
		return "", buf
	}
	if len(t.str) != 0 && t.num == 0 {
		return t.str, nil
	}
	// 为 fmt.Stringer 处理非 JSON 字符串的令牌。
	if t.num > 0 {
		switch t.str[0] {
		case 'f':
			return string(jsonwire.AppendFloat(nil, math.Float64frombits(t.num), 64)), nil
		case 'i':
			return strconv.FormatInt(int64(t.num), 10), nil
		case 'u':
			return strconv.FormatUint(uint64(t.num), 10), nil
		}
	}
	return "<invalid jsontext.Token>", nil
}

// appendNumber 将 JSON 数字追加到 dst 并返回它。
// 如果 t 不是 JSON 数字，它会 panic。
func (t Token) appendNumber(dst []byte, flags *jsonflags.Flags) ([]byte, error) {
	if raw := t.raw; raw != nil {
		// 处理原始数字值。
		buf := raw.previousBuffer()
		if Kind(buf[0]).normalize() == '0' {
			dst, _, err := jsonwire.ReformatNumber(dst, buf, flags)
			return dst, err
		}
	} else if t.num != 0 {
		// 处理精确数字值。
		switch t.str[0] {
		case 'f':
			return jsonwire.AppendFloat(dst, math.Float64frombits(t.num), 64), nil
		case 'i':
			return strconv.AppendInt(dst, int64(t.num), 10), nil
		case 'u':
			return strconv.AppendUint(dst, uint64(t.num), 10), nil
		}
	}

	panic("invalid JSON token kind: " + t.Kind().String())
}

// Float 返回 JSON 数字的浮点值。
// 对于值为 "NaN"、"Infinity" 或 "-Infinity" 的任何 JSON 字符串，
// 它分别返回 NaN、+Inf 或 -Inf 值。
// 对于所有其他情况，它会 panic。
func (t Token) Float() float64 {
	if raw := t.raw; raw != nil {
		// 处理原始数字值。
		if uint64(raw.previousOffsetStart()) != t.num {
			panic(invalidTokenPanic)
		}
		buf := raw.previousBuffer()
		if Kind(buf[0]).normalize() == '0' {
			fv, _ := jsonwire.ParseFloat(buf, 64)
			return fv
		}
	} else if t.num != 0 {
		// 处理精确数字值。
		switch t.str[0] {
		case 'f':
			return math.Float64frombits(t.num)
		case 'i':
			return float64(int64(t.num))
		case 'u':
			return float64(uint64(t.num))
		}
	}

	// 处理值为 "NaN"、"Infinity" 或 "-Infinity" 的字符串值。
	if t.Kind() == '"' {
		switch t.String() {
		case "NaN":
			return math.NaN()
		case "Infinity":
			return math.Inf(+1)
		case "-Infinity":
			return math.Inf(-1)
		}
	}

	panic("invalid JSON token kind: " + t.Kind().String())
}

// Int 返回 JSON 数字的有符号整数值。
// 任何数字的小数部分都会被忽略（向零截断）。
// 任何超出 int64 表示范围的数字都将被饱和
// 到最接近的可表示值。
// 如果令牌种类不是 JSON 数字，它会 panic。
func (t Token) Int() int64 {
	if raw := t.raw; raw != nil {
		// 处理原始整数值。
		if uint64(raw.previousOffsetStart()) != t.num {
			panic(invalidTokenPanic)
		}
		neg := false
		buf := raw.previousBuffer()
		if len(buf) > 0 && buf[0] == '-' {
			neg, buf = true, buf[1:]
		}
		if numAbs, ok := jsonwire.ParseUint(buf); ok {
			if neg {
				if numAbs > -minInt64 {
					return minInt64
				}
				return -1 * int64(numAbs)
			} else {
				if numAbs > +maxInt64 {
					return maxInt64
				}
				return +1 * int64(numAbs)
			}
		}
	} else if t.num != 0 {
		// 处理精确整数值。
		switch t.str[0] {
		case 'i':
			return int64(t.num)
		case 'u':
			if t.num > maxInt64 {
				return maxInt64
			}
			return int64(t.num)
		}
	}

	// 处理作为浮点值的 JSON 数字。
	if t.Kind() == '0' {
		switch fv := t.Float(); {
		case fv >= maxInt64:
			return maxInt64
		case fv <= minInt64:
			return minInt64
		default:
			return int64(fv) // 向零截断
		}
	}

	panic("invalid JSON token kind: " + t.Kind().String())
}

// Uint 返回 JSON 数字的无符号整数值。
// 任何数字的小数部分都会被忽略（向零截断）。
// 任何超出 uint64 表示范围的数字都将被饱和
// 到最接近的可表示值。
// 如果令牌种类不是 JSON 数字，它会 panic。
func (t Token) Uint() uint64 {
	// 注意：此访问器对任何负数 JSON 数字返回 0，
	// 这可能令人意外，但至少与将越界数字
	// 饱和到最接近的可表示数字的行为一致。

	if raw := t.raw; raw != nil {
		// 处理原始整数值。
		if uint64(raw.previousOffsetStart()) != t.num {
			panic(invalidTokenPanic)
		}
		neg := false
		buf := raw.previousBuffer()
		if len(buf) > 0 && buf[0] == '-' {
			neg, buf = true, buf[1:]
		}
		if num, ok := jsonwire.ParseUint(buf); ok {
			if neg {
				return minUint64
			}
			return num
		}
	} else if t.num != 0 {
		// 处理精确整数值。
		switch t.str[0] {
		case 'u':
			return t.num
		case 'i':
			if int64(t.num) < minUint64 {
				return minUint64
			}
			return uint64(int64(t.num))
		}
	}

	// 处理作为浮点值的 JSON 数字。
	if t.Kind() == '0' {
		switch fv := t.Float(); {
		case fv >= maxUint64:
			return maxUint64
		case fv <= minUint64:
			return minUint64
		default:
			return uint64(fv) // 向零截断
		}
	}

	panic("invalid JSON token kind: " + t.Kind().String())
}

// Kind 返回令牌种类。
func (t Token) Kind() Kind {
	switch {
	case t.raw != nil:
		raw := t.raw
		if uint64(raw.previousOffsetStart()) != t.num {
			panic(invalidTokenPanic)
		}
		return Kind(t.raw.buf[raw.prevStart]).normalize()
	case t.num != 0:
		return '0'
	case len(t.str) != 0:
		return '"'
	default:
		return invalidKind
	}
}

// Kind 表示 JSON 令牌的种类。
//
// Kind 用一个字节表示每种可能的 JSON 令牌种类，
// 该字节方便地是该种类文法的第一个字节，
// 但数字始终用 '0' 表示。
type Kind byte

const (
	KindInvalid     Kind = 0   // 无效种类
	KindNull        Kind = 'n' // null
	KindFalse       Kind = 'f' // false
	KindTrue        Kind = 't' // true
	KindString      Kind = '"' // 字符串
	KindNumber      Kind = '0' // 数字
	KindBeginObject Kind = '{' // 对象起始
	KindEndObject   Kind = '}' // 对象结束
	KindBeginArray  Kind = '[' // 数组起始
	KindEndArray    Kind = ']' // 数组结束
)

const invalidKind Kind = 0

// String 以人类可读的方式打印该种类。
func (k Kind) String() string {
	switch k {
	case 0:
		return "invalid"
	case 'n':
		return "null"
	case 'f':
		return "false"
	case 't':
		return "true"
	case '"':
		return "string"
	case '0':
		return "number"
	case '{':
		return "{"
	case '}':
		return "}"
	case '[':
		return "["
	case ']':
		return "]"
	default:
		return "<invalid jsontext.Kind: " + jsonwire.QuoteRune(string(k)) + ">"
	}
}

var normKind = [256]Kind{
	'n': 'n',
	'f': 'f',
	't': 't',
	'"': '"',
	'{': '{',
	'}': '}',
	'[': '[',
	']': ']',
	'-': '0',
	'0': '0',
	'1': '0',
	'2': '0',
	'3': '0',
	'4': '0',
	'5': '0',
	'6': '0',
	'7': '0',
	'8': '0',
	'9': '0',
}

// normalize 将所有可能的数字起始字符合并为 '0'，
// 并将所有无效种类转换为 0。
func (k Kind) normalize() Kind {
	// 使用查找表可将内联开销保持在尽可能低的水平。
	return normKind[k]
}
