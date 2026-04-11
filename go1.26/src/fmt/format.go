// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmt

import (
	"strconv"
	"unicode/utf8"
)

const (
	ldigits = "0123456789abcdefx"
	udigits = "0123456789ABCDEFX"
)

const (
	signed   = true
	unsigned = false
)

// 标志位单独放在一个结构体中以便于清除。
type fmtFlags struct {
	widPresent  bool
	precPresent bool
	minus       bool
	plus        bool
	sharp       bool
	space       bool
	zero        bool

	// 对于 %+v %#v 格式，我们设置 plusV/sharpV 标志
	// 并清除 plus/sharp 标志，因为 %+v 和 %#v 实际上是
	// 在顶层设置的不同的、无标志的格式。
	plusV  bool
	sharpV bool
}

// fmt 是 Printf 等函数使用的原始格式化器。
// 它打印到必须单独设置的缓冲区中。
type fmt struct {
	buf *buffer

	fmtFlags

	wid  int // 宽度
	prec int // 精度

	// intbuf 足够大，可存储带符号的 int64 的 %b 格式，
	// 并避免在 32 位架构上结构体末尾的填充。
	intbuf [68]byte
}

func (f *fmt) clearflags() {
	f.fmtFlags = fmtFlags{}
	f.wid = 0
	f.prec = 0
}

func (f *fmt) init(buf *buffer) {
	f.buf = buf
	f.clearflags()
}

// writePadding 生成 n 字节的填充。
func (f *fmt) writePadding(n int) {
	if n <= 0 { // 无需填充字节。
		return
	}
	buf := *f.buf
	oldLen := len(buf)
	newLen := oldLen + n
	// 为填充腾出足够空间。
	if newLen > cap(buf) {
		buf = make(buffer, cap(buf)*2+n)
		copy(buf, *f.buf)
	}
	// 决定填充应使用的字节。
	padByte := byte(' ')
	// 仅允许左侧补零。
	if f.zero && !f.minus {
		padByte = byte('0')
	}
	// 用 padByte 填充。
	padding := buf[oldLen:newLen]
	for i := range padding {
		padding[i] = padByte
	}
	*f.buf = buf[:newLen]
}

// pad 将 b 追加到 f.buf，在左侧（!f.minus）或右侧（f.minus）填充。
func (f *fmt) pad(b []byte) {
	if !f.widPresent || f.wid == 0 {
		f.buf.write(b)
		return
	}
	width := f.wid - utf8.RuneCount(b)
	if !f.minus {
		// 左侧填充
		f.writePadding(width)
		f.buf.write(b)
	} else {
		// 右侧填充
		f.buf.write(b)
		f.writePadding(width)
	}
}

// padString 将 s 追加到 f.buf，在左侧（!f.minus）或右侧（f.minus）填充。
func (f *fmt) padString(s string) {
	if !f.widPresent || f.wid == 0 {
		f.buf.writeString(s)
		return
	}
	width := f.wid - utf8.RuneCountInString(s)
	if !f.minus {
		// 左侧填充
		f.writePadding(width)
		f.buf.writeString(s)
	} else {
		// 右侧填充
		f.buf.writeString(s)
		f.writePadding(width)
	}
}

// fmtBoolean 格式化布尔值。
func (f *fmt) fmtBoolean(v bool) {
	if v {
		f.padString("true")
	} else {
		f.padString("false")
	}
}

// fmtUnicode 将 uint64 格式化为 "U+0078"，或在设置 f.sharp 时格式化为 "U+0078 'x'"。
func (f *fmt) fmtUnicode(u uint64) {
	buf := f.intbuf[0:]

	// 设置默认精度时，所需的最大 buf 长度为 18，
	// 用于用 %#U 格式化 -1（"U+FFFFFFFFFFFFFFFF"），这适合
	// 已分配的容量为 68 字节的 intbuf。
	prec := 4
	if f.precPresent && f.prec > 4 {
		prec = f.prec
		// 计算 "U+"、数字、" '"、字符、"'" 所需的空间。
		width := 2 + prec + 2 + utf8.UTFMax + 1
		if width > len(buf) {
			buf = make([]byte, width)
		}
	}

	// 格式化为 buf，结束于 buf[i]。从右到左格式化数字更容易。
	i := len(buf)

	// 对于 %#U，我们要在缓冲区末尾添加一个空格和一个带引号的字符。
	if f.sharp && u <= utf8.MaxRune && strconv.IsPrint(rune(u)) {
		i--
		buf[i] = '\''
		i -= utf8.RuneLen(rune(u))
		utf8.EncodeRune(buf[i:], rune(u))
		i--
		buf[i] = '\''
		i--
		buf[i] = ' '
	}
	// 将 Unicode 码点 u 格式化为十六进制数。
	for u >= 16 {
		i--
		buf[i] = udigits[u&0xF]
		prec--
		u >>= 4
	}
	i--
	buf[i] = udigits[u]
	prec--
	// 在数字前添加零，直到达到所需的精度。
	for prec > 0 {
		i--
		buf[i] = '0'
		prec--
	}
	// 添加前导 "U+"。
	i--
	buf[i] = '+'
	i--
	buf[i] = 'U'

	oldZero := f.zero
	f.zero = false
	f.pad(buf[i:])
	f.zero = oldZero
}

// fmtInteger 格式化有符号和无符号整数。
func (f *fmt) fmtInteger(u uint64, base int, isSigned bool, verb rune, digits string) {
	negative := isSigned && int64(u) < 0
	if negative {
		u = -u
	}

	buf := f.intbuf[0:]
	// 已分配的容量为 68 字节的 f.intbuf
	// 在未设置精度或宽度时足够用于整数格式化。
	if f.widPresent || f.precPresent {
		// 考虑到可能添加的符号和 "0x"，额外预留 3 字节。
		width := 3 + f.wid + f.prec // wid 和 prec 始终为正。
		if width > len(buf) {
			// 我们需要一艘更大的船。
			buf = make([]byte, width)
		}
	}

	// 两种请求额外前导零的方式：%.3d 或 %03d。
	// 如果两者都指定，则忽略 f.zero 标志，
	// 改用空格填充。
	prec := 0
	if f.precPresent {
		prec = f.prec
		// 精度为 0 且值为 0 表示“不打印任何内容”，只打印填充。
		if prec == 0 && u == 0 {
			oldZero := f.zero
			f.zero = false
			f.writePadding(f.wid)
			f.zero = oldZero
			return
		}
	} else if f.zero && !f.minus && f.widPresent { // 仅允许左侧补零。
		prec = f.wid
		if negative || f.plus || f.space {
			prec-- // 为符号预留空间
		}
	}

	// 因为从右到左打印更容易：将 u 格式化为 buf，结束于 buf[i]。
	// 我们可以通过将 32 位情况拆分到单独的块中来稍微提高速度，
	// 但这不值得重复，所以 u 是 64 位的。
	i := len(buf)
	// 使用常量进行除法和取模以获得更高效的代码。
	// Switch 分支按使用频率排序。
	switch base {
	case 10:
		for u >= 10 {
			i--
			next := u / 10
			buf[i] = byte('0' + u - next*10)
			u = next
		}
	case 16:
		for u >= 16 {
			i--
			buf[i] = digits[u&0xF]
			u >>= 4
		}
	case 8:
		for u >= 8 {
			i--
			buf[i] = byte('0' + u&7)
			u >>= 3
		}
	case 2:
		for u >= 2 {
			i--
			buf[i] = byte('0' + u&1)
			u >>= 1
		}
	default:
		panic("fmt: unknown base; can't happen")
	}
	i--
	buf[i] = digits[u]
	for i > 0 && prec > len(buf)-i {
		i--
		buf[i] = '0'
	}

	// 各种前缀：0x、- 等。
	if f.sharp {
		switch base {
		case 2:
			// 添加前导 0b。
			i--
			buf[i] = 'b'
			i--
			buf[i] = '0'
		case 8:
			if buf[i] != '0' {
				i--
				buf[i] = '0'
			}
		case 16:
			// 添加前导 0x 或 0X。
			i--
			buf[i] = digits[16]
			i--
			buf[i] = '0'
		}
	}
	if verb == 'O' {
		i--
		buf[i] = 'o'
		i--
		buf[i] = '0'
	}

	if negative {
		i--
		buf[i] = '-'
	} else if f.plus {
		i--
		buf[i] = '+'
	} else if f.space {
		i--
		buf[i] = ' '
	}

	// 左侧补零已在之前像精度一样处理，
	// 或者由于显式设置了精度而忽略 f.zero 标志。
	oldZero := f.zero
	f.zero = false
	f.pad(buf[i:])
	f.zero = oldZero
}

// truncateString 将字符串 s 截断到指定的精度（如果存在）。
func (f *fmt) truncateString(s string) string {
	if f.precPresent {
		n := f.prec
		for i := range s {
			n--
			if n < 0 {
				return s[:i]
			}
		}
	}
	return s
}

// truncate 将字节切片 b 作为字符串截断到指定的精度（如果存在）。
func (f *fmt) truncate(b []byte) []byte {
	if f.precPresent {
		n := f.prec
		for i := 0; i < len(b); {
			n--
			if n < 0 {
				return b[:i]
			}
			_, wid := utf8.DecodeRune(b[i:])
			i += wid
		}
	}
	return b
}

// fmtS 格式化字符串。
func (f *fmt) fmtS(s string) {
	s = f.truncateString(s)
	f.padString(s)
}

// fmtBs 将字节切片 b 格式化为如同用 fmtS 格式化的字符串。
func (f *fmt) fmtBs(b []byte) {
	b = f.truncate(b)
	f.pad(b)
}

// fmtSbx 将字符串或字节切片格式化为其字节的十六进制编码。
func (f *fmt) fmtSbx(s string, b []byte, digits string) {
	length := len(b)
	if b == nil {
		// 不存在字节切片。假设字符串 s 应被编码。
		length = len(s)
	}
	// 设置长度，不处理超过精度要求的字节。
	if f.precPresent && f.prec < length {
		length = f.prec
	}
	// 考虑 f.sharp 和 f.space 标志，计算编码的宽度。
	width := 2 * length
	if width > 0 {
		if f.space {
			// 每个由两个十六进制数编码的元素将获得前导 0x 或 0X。
			if f.sharp {
				width *= 2
			}
			// 元素之间用空格分隔。
			width += length - 1
		} else if f.sharp {
			// 仅为整个字符串添加前导 0x 或 0X。
			width += 2
		}
	} else { // 应编码的字节切片或字符串为空。
		if f.widPresent {
			f.writePadding(f.wid)
		}
		return
	}
	// 处理左侧填充。
	if f.widPresent && f.wid > width && !f.minus {
		f.writePadding(f.wid - width)
	}
	// 将编码直接写入输出缓冲区。
	buf := *f.buf
	if f.sharp {
		// 添加前导 0x 或 0X。
		buf = append(buf, '0', digits[16])
	}
	var c byte
	for i := 0; i < length; i++ {
		if f.space && i > 0 {
			// 用空格分隔元素。
			buf = append(buf, ' ')
			if f.sharp {
				// 为每个元素添加前导 0x 或 0X。
				buf = append(buf, '0', digits[16])
			}
		}
		if b != nil {
			c = b[i] // 从输入字节切片中取一个字节。
		} else {
			c = s[i] // 从输入字符串中取一个字节。
		}
		// 将每个字节编码为两个十六进制数字。
		buf = append(buf, digits[c>>4], digits[c&0xF])
	}
	*f.buf = buf
	// 处理右侧填充。
	if f.widPresent && f.wid > width && f.minus {
		f.writePadding(f.wid - width)
	}
}

// fmtSx 将字符串格式化为其字节的十六进制编码。
func (f *fmt) fmtSx(s, digits string) {
	f.fmtSbx(s, nil, digits)
}

// fmtBx 将字节切片格式化为其字节的十六进制编码。
func (f *fmt) fmtBx(b []byte, digits string) {
	f.fmtSbx("", b, digits)
}

// fmtQ 将字符串格式化为双引号、转义的 Go 字符串常量。
// 如果设置了 f.sharp，且字符串不包含除制表符外的任何控制字符，
// 则可能返回原始（反引号）字符串。
func (f *fmt) fmtQ(s string) {
	s = f.truncateString(s)
	if f.sharp && strconv.CanBackquote(s) {
		f.padString("`" + s + "`")
		return
	}
	buf := f.intbuf[:0]
	if f.plus {
		f.pad(strconv.AppendQuoteToASCII(buf, s))
	} else {
		f.pad(strconv.AppendQuote(buf, s))
	}
}

// fmtC 将整数格式化为 Unicode 字符。
// 如果该字符不是有效的 Unicode，将打印 '\ufffd'。
func (f *fmt) fmtC(c uint64) {
	// 显式检查 c 是否超过 utf8.MaxRune，因为将 uint64 转换为 rune
	// 可能会丢失表示溢出的精度。
	r := rune(c)
	if c > utf8.MaxRune {
		r = utf8.RuneError
	}
	buf := f.intbuf[:0]
	f.pad(utf8.AppendRune(buf, r))
}

// fmtQc 将整数格式化为单引号、转义的 Go 字符常量。
// 如果该字符不是有效的 Unicode，将打印 '\ufffd'。
func (f *fmt) fmtQc(c uint64) {
	r := rune(c)
	if c > utf8.MaxRune {
		r = utf8.RuneError
	}
	buf := f.intbuf[:0]
	if f.plus {
		f.pad(strconv.AppendQuoteRuneToASCII(buf, r))
	} else {
		f.pad(strconv.AppendQuoteRune(buf, r))
	}
}

// fmtFloat 格式化 float64。它假设 verb 是 strconv.AppendFloat 的有效格式说明符，
// 因此可以放入一个字节中。
func (f *fmt) fmtFloat(v float64, size int, verb rune, prec int) {
	// 格式说明符中的显式精度覆盖默认精度。
	if f.precPresent {
		prec = f.prec
	}
	// 格式化数字，必要时为前导 + 号预留空间。
	num := strconv.AppendFloat(f.intbuf[:1], v, byte(verb), prec, size)
	if num[1] == '-' || num[1] == '+' {
		num = num[1:]
	} else {
		num[0] = '+'
	}
	// f.space 表示添加前导空格而不是 "+" 号，除非 f.plus 显式要求符号。
	if f.space && num[0] == '+' && !f.plus {
		num[0] = ' '
	}
	// 对无穷大和 NaN 的特殊处理，
	// 它们看起来不像数字，因此不应用零填充。
	if num[1] == 'I' || num[1] == 'N' {
		oldZero := f.zero
		f.zero = false
		// 如果未要求，移除 NaN 前的符号。
		if num[1] == 'N' && !f.space && !f.plus {
			num = num[1:]
		}
		f.pad(num)
		f.zero = oldZero
		return
	}
	// sharp 标志强制为非二进制格式打印小数点，
	// 并保留末尾零，我们可能需要恢复这些零。
	if f.sharp && verb != 'b' {
		digits := 0
		switch verb {
		case 'v', 'g', 'G', 'x':
			digits = prec
			// 如果未显式设置精度，则使用精度 6。
			if digits == -1 {
				digits = 6
			}
		}

		// 预分配的缓冲区，有足够空间容纳
		// "e+123" 或 "p-1023" 形式的指数表示法。
		var tailBuf [6]byte
		tail := tailBuf[:0]

		hasDecimalPoint := false
		sawNonzeroDigit := false
		// 从 i = 1 开始，跳过 num[0] 处的符号。
		for i := 1; i < len(num); i++ {
			switch num[i] {
			case '.':
				hasDecimalPoint = true
			case 'p', 'P':
				tail = append(tail, num[i:]...)
				num = num[:i]
			case 'e', 'E':
				if verb != 'x' && verb != 'X' {
					tail = append(tail, num[i:]...)
					num = num[:i]
					break
				}
				fallthrough
			default:
				if num[i] != '0' {
					sawNonzeroDigit = true
				}
				// 计算第一个非零数字后的有效数字。
				if sawNonzeroDigit {
					digits--
				}
			}
		}
		if !hasDecimalPoint {
			// 前导数字 0 应贡献一次到 digits。
			if len(num) == 2 && num[1] == '0' {
				digits--
			}
			num = append(num, '.')
		}
		for digits > 0 {
			num = append(num, '0')
			digits--
		}
		num = append(num, tail...)
	}
	// 如果要求符号且符号不是正的，我们需要符号。
	if f.plus || num[0] != '+' {
		// 如果我们在左侧补零，我们希望符号在前导零之前。
		// 通过先写出符号，然后填充无符号数字来实现这一点。
		// 仅允许左侧补零。
		if f.zero && !f.minus && f.widPresent && f.wid > len(num) {
			f.buf.writeByte(num[0])
			f.writePadding(f.wid - len(num))
			f.buf.write(num[1:])
			return
		}
		f.pad(num)
		return
	}
	// 没有符号要显示且数字为正；只需打印无符号数字。
	f.pad(num[1:])
}
