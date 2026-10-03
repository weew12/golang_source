// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现字符串到 Float 的转换函数。

package big

import (
	"fmt"
	"io"
	"strings"
)

var floatZero Float

// SetString 将 z 设置为 s 的值，并返回 z 和一个表示成功与否的布尔值。
// s 必须是格式与 [Float.Parse] 所接受格式相同的浮点数，且 base 参数为 0。
// 成功时整个字符串（不只是前缀）都必须有效。如果操作失败，
// z 的值未定义，但返回的值为 nil。
func (z *Float) SetString(s string) (*Float, bool) {
	if f, _, err := z.Parse(s, 0); err == nil {
		return f, true
	}
	return nil, false
}

// scan 类似于 Parse，但它从 io.ByteScanner（而非字符串）读取表示有效
// 浮点数的最长可能前缀。它作为 Parse 的实现。它不识别 ±Inf，
// 也不期望结尾出现 EOF。
func (z *Float) scan(r io.ByteScanner, base int) (f *Float, b int, err error) {
	prec := z.prec
	if prec == 0 {
		prec = 64
	}

	// 出错时使用的一个合理值。
	z.form = zero

	// 符号
	z.neg, err = scanSign(r)
	if err != nil {
		return
	}

	// 尾数
	var fcount int // 小数位数；如果 <= 0 则有效
	z.mant, b, fcount, err = z.mant.scan(r, base, true)
	if err != nil {
		return
	}

	// 指数
	var exp int64
	var ebase int
	exp, ebase, err = scanExponent(r, true, base == 0)
	if err != nil {
		return
	}

	// 特殊情况 0
	if len(z.mant) == 0 {
		z.prec = prec
		z.acc = Exact
		z.form = zero
		f = z
		return
	}
	// len(z.mant) > 0

	// 尾数可能带有一个小数点（fcount <= 0），并且可能存在
	// 非零指数 exp。小数点相当于除以 b**(-fcount)。指数表示
	// 乘以 ebase**exp。最后，尾数规范化（左移）需要乘以
	// 2**(-shiftcount) 进行修正。乘法是可交换的，因此只要不损失
	// 精度，我们可以按任意顺序应用它们。我们只涉及 2 的幂和 10 的幂，
	// 并将 10 的幂拆分为相同幂次的 2 和 5 的乘积。这减小了
	// 以 10 为底指数所需的乘法因子的大小。

	// 规范化尾数并确定初始指数贡献
	exp2 := int64(len(z.mant))*_W - fnorm(z.mant)
	exp5 := int64(0)

	// 确定小数点引入的二进制或十进制指数贡献
	if fcount < 0 {
		// 尾数带有小数点 ddd.dddd；
		// -fcount 是 '.' 右侧的数字个数。
		// 相应地调整相关指数。
		d := int64(fcount)
		switch b {
		case 10:
			exp5 = d
			fallthrough // 10**e == 5**e * 2**e
		case 2:
			exp2 += d
		case 8:
			exp2 += d * 3 // 八进制数字每位占 3 位
		case 16:
			exp2 += d * 4 // 十六进制数字每位占 4 位
		default:
			panic("unexpected mantissa base")
		}
		// fcount 已消费 - 不再需要
	}

	// 将实际指数考虑进来
	switch ebase {
	case 10:
		exp5 += exp
		fallthrough // 参见上面的 fallthrough
	case 2:
		exp2 += exp
	default:
		panic("unexpected exponent base")
	}
	// exp 已消费 - 不再需要

	// 应用 2**exp2
	if MinExp <= exp2 && exp2 <= MaxExp {
		z.prec = prec
		z.form = finite
		z.exp = int32(exp2)
		f = z
	} else {
		err = fmt.Errorf("exponent overflow")
		return
	}

	if exp5 == 0 {
		// 没有十进制指数贡献
		z.round(0)
		return
	}
	// exp5 != 0

	// 应用 5**exp5
	p := new(Float).SetPrec(z.Prec() + 64) // 为 p 使用更多位 -- TODO(gri) 正确的数值是多少？
	if exp5 < 0 {
		z.Quo(z, p.pow5(uint64(-exp5)))
	} else {
		z.Mul(z, p.pow5(uint64(exp5)))
	}

	return
}

// 这些 5 的幂可以放入 uint64。
//
//	for p, q := uint64(0), uint64(1); p < q; p, q = q, q*5 {
//		fmt.Println(q)
//	}
var pow5tab = [...]uint64{
	1,
	5,
	25,
	125,
	625,
	3125,
	15625,
	78125,
	390625,
	1953125,
	9765625,
	48828125,
	244140625,
	1220703125,
	6103515625,
	30517578125,
	152587890625,
	762939453125,
	3814697265625,
	19073486328125,
	95367431640625,
	476837158203125,
	2384185791015625,
	11920928955078125,
	59604644775390625,
	298023223876953125,
	1490116119384765625,
	7450580596923828125,
}

// pow5 将 z 设置为 5**n 并返回 z。
// n 不能为负数。
func (z *Float) pow5(n uint64) *Float {
	const m = uint64(len(pow5tab) - 1)
	if n <= m {
		return z.SetUint64(pow5tab[n])
	}
	// n > m

	z.SetUint64(pow5tab[m])
	n -= m

	// 为 f 使用比 z 更多的位
	// TODO(gri) 正确的数值是多少？
	f := new(Float).SetPrec(z.Prec() + 64).SetUint64(5)

	for n > 0 {
		if n&1 != 0 {
			z.Mul(z, f)
		}
		f.Mul(f, f)
		n >>= 1
	}

	return z
}

// Parse 解析 s，s 必须包含给定转换进制下带尾数的浮点数文本表示
// （指数始终是十进制数），或者是表示无穷值的字符串。
//
// 对于进制 0，下划线字符 “_” 可以出现在进制前缀与相邻数字之间，
// 以及连续数字之间；这样的下划线不会改变数字的值，也不会改变
// 返回的数字个数。如果没有其他错误，下划线位置不正确会报告为错误。
// 如果 base != 0，则不识别下划线，因此会像任何其他不是有效小数点
// 或数字的字符一样终止扫描。
//
// 它将 z 设置为对应浮点值的（可能经过舍入的）值，并返回 z、
// 实际使用的进制 b，以及错误 err（如果有）。成功时必须消费整个
// 字符串（不只是前缀）。如果 z 的精度为 0，则在舍入生效之前
// 会被改为 64。数字必须具有以下形式：
//
//	number    = [ sign ] ( float | "inf" | "Inf" ) .
//	sign      = "+" | "-" .
//	float     = ( mantissa | prefix pmantissa ) [ exponent ] .
//	prefix    = "0" [ "b" | "B" | "o" | "O" | "x" | "X" ] .
//	mantissa  = digits "." [ digits ] | digits | "." digits .
//	pmantissa = [ "_" ] digits "." [ digits ] | [ "_" ] digits | "." digits .
//	exponent  = ( "e" | "E" | "p" | "P" ) [ sign ] digits .
//	digits    = digit { [ "_" ] digit } .
//	digit     = "0" ... "9" | "a" ... "z" | "A" ... "Z" .
//
// base 参数必须是 0、2、8、10 或 16。提供无效的 base 参数
// 会导致运行时 panic。
//
// 对于进制 0，数字前缀决定实际使用的进制：“0b” 或 “0B” 选择进制 2，
// “0o” 或 “0O” 选择进制 8，“0x” 或 “0X” 选择进制 16。否则，实际
// 进制为 10，且不接受前缀。不支持八进制前缀 "0"（前导的 "0" 会被
// 直接视为 "0"）。
//
// "p" 或 "P" 指数表示以 2 为底（而非以 10 为底）的指数；例如，
// "0x1.fffffffffffffp1023"（使用进制 0）表示最大的 float64 值。
// 对于十六进制尾数，如果存在指数字符，则必须是 'p' 或 'P' 之一
// （"e" 或 "E" 指数指示符无法与尾数数字区分开）。
//
// 如果报告了错误，返回的 *Float f 为 nil，z 的值有效但未定义。
func (z *Float) Parse(s string, base int) (f *Float, b int, err error) {
	// scan 不处理 ±Inf
	if len(s) == 3 && (s == "Inf" || s == "inf") {
		f = z.SetInf(false)
		return
	}
	if len(s) == 4 && (s[0] == '+' || s[0] == '-') && (s[1:] == "Inf" || s[1:] == "inf") {
		f = z.SetInf(s[0] == '-')
		return
	}

	r := strings.NewReader(s)
	if f, b, err = z.scan(r, base); err != nil {
		return
	}

	// 必须已消费整个字符串
	if ch, err2 := r.ReadByte(); err2 == nil {
		err = fmt.Errorf("expected end of string, found %q", ch)
	} else if err2 != io.EOF {
		err = err2
	}

	return
}

// ParseFloat 类似于 f.Parse(s, base)，其中 f 被设置为给定的精度和舍入模式。
func ParseFloat(s string, base int, prec uint, mode RoundingMode) (f *Float, b int, err error) {
	return new(Float).SetPrec(prec).SetMode(mode).Parse(s, base)
}

var _ fmt.Scanner = (*Float)(nil) // *Float 必须实现 fmt.Scanner

// Scan 是 [fmt.Scanner] 的辅助例程；它将 z 设置为所扫描数字的值。
// 它接受 [fmt.Scan] 对浮点值所支持的动词格式，即：
// 'b'（二进制）、'e'、'E'、'f'、'F'、'g' 和 'G'。
// Scan 不处理 ±Inf。
func (z *Float) Scan(s fmt.ScanState, ch rune) error {
	s.SkipSpace()
	_, _, err := z.scan(byteReader{s}, 0)
	return err
}
