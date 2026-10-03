// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现整数到字符串的转换函数。

package big

import (
	"errors"
	"fmt"
	"io"
)

// Text 返回 x 在给定 base 下的字符串表示。
// Base 必须在 2 到 62 之间（含）。结果使用小写字母 'a' 到 'z'
// 表示数字值 10 到 35，使用大写字母 'A' 到 'Z' 表示数字值 36 到 61。
// 字符串中不添加前缀（如 "0x"）。如果 x 是 nil 指针，返回 "<nil>"。
func (x *Int) Text(base int) string {
	if x == nil {
		return "<nil>"
	}
	return string(x.abs.itoa(x.neg, base))
}

// Append 将由 x.Text(base) 生成的 x 的字符串表示追加到 buf，
// 并返回扩展后的缓冲区。
func (x *Int) Append(buf []byte, base int) []byte {
	if x == nil {
		return append(buf, "<nil>"...)
	}
	return append(buf, x.abs.itoa(x.neg, base)...)
}

// String 返回由 x.Text(10) 生成的 x 的十进制表示。
func (x *Int) String() string {
	return x.Text(10)
}

// writeMultiple 将 text 的 count 份副本写入 s。
func writeMultiple(s fmt.State, text string, count int) {
	if len(text) > 0 {
		b := []byte(text)
		for ; count > 0; count-- {
			s.Write(b)
		}
	}
}

var _ fmt.Formatter = intOne // *Int 必须实现 fmt.Formatter

// Format 实现 [fmt.Formatter]。它接受格式
// 'b'（二进制）、'o'（带 0 前缀的八进制）、'O'（带 0o 前缀的八进制）、
// 'd'（十进制）、'x'（小写十六进制）和
// 'X'（大写十六进制）。
// 还支持 fmt 包针对整数类型的全套格式标志，
// 包括用于符号控制的 '+' 和 ' '，用于八进制前导零和十六进制的 '#'，
// 分别用于 "%#x" 和 "%#X" 的前导 "0x" 或 "0X"，
// 最小数字精度、输出字段宽度、空格或零填充，
// 以及用于左对齐或右对齐的 '-'。
func (x *Int) Format(s fmt.State, ch rune) {
	// 确定 base
	var base int
	switch ch {
	case 'b':
		base = 2
	case 'o', 'O':
		base = 8
	case 'd', 's', 'v':
		base = 10
	case 'x', 'X':
		base = 16
	default:
		// 未知格式
		fmt.Fprintf(s, "%%!%c(big.Int=%s)", ch, x.String())
		return
	}

	if x == nil {
		fmt.Fprint(s, "<nil>")
		return
	}

	// 确定符号字符
	sign := ""
	switch {
	case x.neg:
		sign = "-"
	case s.Flag('+'): // 二者都指定时优先于 ' '
		sign = "+"
	case s.Flag(' '):
		sign = " "
	}

	// 确定用于指示输出 base 的前缀字符
	prefix := ""
	if s.Flag('#') {
		switch ch {
		case 'b': // 二进制
			prefix = "0b"
		case 'o': // 八进制
			prefix = "0"
		case 'x': // 十六进制
			prefix = "0x"
		case 'X':
			prefix = "0X"
		}
	}
	if ch == 'O' {
		prefix = "0o"
	}

	digits := x.abs.utoa(base)
	if ch == 'X' {
		// 比 bytes.ToUpper 更快
		for i, d := range digits {
			if 'a' <= d && d <= 'z' {
				digits[i] = 'A' + (d - 'a')
			}
		}
	}

	// 三类数字填充的字符数
	var left int  // 用于右对齐时位于数字左侧的空格字符（"%8d"）
	var zeros int // 作为最左数字的零字符（实际是 cs[0]）（"%.8d"）
	var right int // 用于左对齐时位于数字右侧的空格字符（"%-8d"）

	// 根据精度确定数字填充：要输出的最少数字位数
	precision, precisionSet := s.Precision()
	if precisionSet {
		switch {
		case len(digits) < precision:
			zeros = precision - len(digits) // 零填充的数量
		case len(digits) == 1 && digits[0] == '0' && precision == 0:
			return // 如果为零值（x == 0）且精度为零（"." 或 ".0"），则不打印任何内容
		}
	}

	// 根据宽度确定字段填充：要输出的最少字符数
	length := len(sign) + len(prefix) + zeros + len(digits)
	if width, widthSet := s.Width(); widthSet && length < width { // 按规定填充
		switch d := width - length; {
		case s.Flag('-'):
			// 在右侧用空格填充；二者都指定时优先于 '0'
			right = d
		case s.Flag('0') && !precisionSet:
			// 用零填充，除非同时指定了精度
			zeros = d
		default:
			// 在左侧用空格填充
			left = d
		}
	}

	// 按 [左填充][符号][前缀][零填充][数字][右填充] 打印数字
	writeMultiple(s, " ", left)
	writeMultiple(s, sign, 1)
	writeMultiple(s, prefix, 1)
	writeMultiple(s, "0", zeros)
	s.Write(digits)
	writeMultiple(s, " ", right)
}

// scan 将 z 设为与从 r 读取的最长可能前缀对应的整数值，
// 该前缀表示给定转换 base 下的有符号整数。
// 它返回 z、实际使用的转换 base，以及错误（如果有）。在错误情况下，
// z 的值未定义，但返回值为 nil。语法遵循 Go 中整数字面量的语法。
//
// base 参数必须是 0 或 2 到 MaxBase 之间的值。如果 base 为 0，
// 字符串前缀决定实际的转换 base。“0b”或“0B”前缀选择 base 2；
// “0”、“0o”或“0O”前缀选择 base 8，“0x”或“0X”前缀选择 base 16。
// 否则，所选 base 为 10。
func (z *Int) scan(r io.ByteScanner, base int) (*Int, int, error) {
	// 确定符号
	neg, err := scanSign(r)
	if err != nil {
		return nil, 0, err
	}

	// 确定尾数
	z.abs, base, _, err = z.abs.scan(r, base, false)
	if err != nil {
		return nil, base, err
	}
	z.neg = len(z.abs) > 0 && neg // 0 has no sign

	return z, base, nil
}

func scanSign(r io.ByteScanner) (neg bool, err error) {
	var ch byte
	if ch, err = r.ReadByte(); err != nil {
		return false, err
	}
	switch ch {
	case '-':
		neg = true
	case '+':
		// 无事可做
	default:
		r.UnreadByte()
	}
	return
}

// byteReader 是围绕 fmt.ScanState 的本地包装；
// 它实现 ByteReader 接口。
type byteReader struct {
	fmt.ScanState
}

func (r byteReader) ReadByte() (byte, error) {
	ch, size, err := r.ReadRune()
	if size != 1 && err == nil {
		err = fmt.Errorf("invalid rune %#U", ch)
	}
	return byte(ch), err
}

func (r byteReader) UnreadByte() error {
	return r.UnreadRune()
}

var _ fmt.Scanner = intOne // *Int 必须实现 fmt.Scanner

// Scan 是 [fmt.Scanner] 的辅助例程；它将 z 设为所扫描数字的值。
// 它接受格式 'b'（二进制）、'o'（八进制）、'd'（十进制）、
// 'x'（小写十六进制）和 'X'（大写十六进制）。
func (z *Int) Scan(s fmt.ScanState, ch rune) error {
	s.SkipSpace() // 跳过前导空格字符
	base := 0
	switch ch {
	case 'b':
		base = 2
	case 'o':
		base = 8
	case 'd':
		base = 10
	case 'x', 'X':
		base = 16
	case 's', 'v':
		// 让 scan 确定 base
	default:
		return errors.New("Int.Scan: invalid verb")
	}
	_, _, err := z.scan(byteReader{s}, base)
	return err
}
