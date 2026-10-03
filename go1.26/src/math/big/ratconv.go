// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现有理数到字符串的转换函数。

package big

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func ratTok(ch rune) bool {
	return strings.ContainsRune("+-/0123456789.eE", ch)
}

var ratZero Rat
var _ fmt.Scanner = &ratZero // *Rat 必须实现 fmt.Scanner

// Scan 是 fmt.Scanner 的辅助例程。它接受格式 'e'、'E'、'f'、'F'、'g'、'G'
// 和 'v'。所有格式都是等价的。
func (z *Rat) Scan(s fmt.ScanState, ch rune) error {
	tok, err := s.Token(true, ratTok)
	if err != nil {
		return err
	}
	if !strings.ContainsRune("efgEFGv", ch) {
		return errors.New("Rat.Scan: invalid verb")
	}
	if _, ok := z.SetString(string(tok)); !ok {
		return errors.New("Rat.Scan: invalid syntax")
	}
	return nil
}

// SetString 将 z 设置为 s 的值，并返回 z 和一个表示成功与否的布尔值。
// s 可以表示为（可能带符号的）分数 "a/b"，也可以表示为一个浮点数，
// 其后可选地跟一个指数。
// 如果提供的是分数，则被除数和除数都可以是十进制整数，也可以分别
// 独立使用前缀 “0b”、“0” 或 “0o”，或 “0x”（及其大写变体）来分别
// 表示二进制、八进制或十六进制整数。除数不能带符号。
// 如果提供的是浮点数，则它可以是十进制形式，也可以使用上述任一前缀
// （但 “0” 除外）来表示非十进制尾数。前导 “0” 被视为十进制的
// 前导 0；在这种情况下它不表示八进制表示。
// 还可以提供可选的以 10 为底的 “e” 或以 2 为底的 “p”（及其大写变体）
// 指数，但十六进制浮点数除外，它们只接受（可选的）“p” 指数（因为
// “e” 或 “E” 无法与尾数数字区分开）。如果指数的绝对值过大，
// 操作可能会失败。
// 成功时整个字符串（不只是前缀）都必须有效。如果操作失败，
// z 的值未定义，但返回的值为 nil。
func (z *Rat) SetString(s string) (*Rat, bool) {
	if len(s) == 0 {
		return nil, false
	}
	// len(s) > 0

	// 解析分数 a/b（如果有）
	if sep := strings.Index(s, "/"); sep >= 0 {
		if _, ok := z.a.SetString(s[:sep], 0); !ok {
			return nil, false
		}
		r := strings.NewReader(s[sep+1:])
		var err error
		if z.b.abs, _, _, err = z.b.abs.scan(r, 0, false); err != nil {
			return nil, false
		}
		// 必须已消费整个字符串
		if _, err = r.ReadByte(); err != io.EOF {
			return nil, false
		}
		if len(z.b.abs) == 0 {
			return nil, false
		}
		return z.norm(), true
	}

	// 解析浮点数
	r := strings.NewReader(s)

	// 符号
	neg, err := scanSign(r)
	if err != nil {
		return nil, false
	}

	// 尾数
	var base int
	var fcount int // 小数位数；如果 <= 0 则有效
	z.a.abs, base, fcount, err = z.a.abs.scan(r, 0, true)
	if err != nil {
		return nil, false
	}

	// 指数
	var exp int64
	var ebase int
	exp, ebase, err = scanExponent(r, true, true)
	if err != nil {
		return nil, false
	}

	// 不应剩下未读取的字符
	if _, err = r.ReadByte(); err != io.EOF {
		return nil, false
	}

	// 特殊情况 0（另见 issue #16176）
	if len(z.a.abs) == 0 {
		return z.norm(), true
	}
	// len(z.a.abs) > 0

	// 尾数可能带有一个小数点（fcount <= 0），并且可能存在非零指数 exp。
	// 小数点相当于除以 base**(-fcount)，即乘以 base**fcount。指数表示
	// 乘以 ebase**exp。乘法是可交换的，因此我们可以按任意顺序应用它们。
	// 我们只涉及 2 的幂和 10 的幂，并将 10 的幂拆分为相同幂次的 2 和 5
	// 的乘积。这可能会减小创建最终分数所需的移位/乘法因子或除数的大小，
	// 具体取决于实际的浮点值。

	// 确定小数点引入的二进制或十进制指数贡献
	var exp2, exp5 int64
	if fcount < 0 {
		// 尾数带有小数点 ddd.dddd；
		// -fcount 是 '.' 右侧的数字个数。
		// 相应地调整相关指数。
		d := int64(fcount)
		switch base {
		case 10:
			exp5 = d
			fallthrough // 10**e == 5**e * 2**e
		case 2:
			exp2 = d
		case 8:
			exp2 = d * 3 // 八进制数字每位占 3 位
		case 16:
			exp2 = d * 4 // 十六进制数字每位占 4 位
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

	stk := getStack()
	defer stk.free()

	// 应用 exp5 的贡献
	//（从 exp5 开始，这样要相乘的数更小）
	if exp5 != 0 {
		n := exp5
		if n < 0 {
			n = -n
			if n < 0 {
				// 当 -n 溢出时可能发生这种情况。-(-1 << 63) 会变成
				// -1 << 63，仍然为负。
				return nil, false
			}
		}
		if n > 1e6 {
			return nil, false // 避免过大的指数
		}
		pow5 := z.b.abs.expNN(stk, natFive, nat(nil).setWord(Word(n)), nil, false) // 使用 z.b.abs 的底层数组
		if exp5 > 0 {
			z.a.abs = z.a.abs.mul(stk, z.a.abs, pow5)
			z.b.abs = z.b.abs.setWord(1)
		} else {
			z.b.abs = pow5
		}
	} else {
		z.b.abs = z.b.abs.setWord(1)
	}

	// 应用 exp2 的贡献
	if exp2 < -1e7 || exp2 > 1e7 {
		return nil, false // 避免过大的指数
	}
	if exp2 > 0 {
		z.a.abs = z.a.abs.lsh(z.a.abs, uint(exp2))
	} else if exp2 < 0 {
		z.b.abs = z.b.abs.lsh(z.b.abs, uint(-exp2))
	}

	z.a.neg = neg && len(z.a.abs) > 0 // 0 没有符号

	return z.norm(), true
}

// scanExponent 扫描 r 中表示以 10 为底（“e”、“E”）或以 2 为底
// （“p”、“P”）指数的最长可能前缀（如果有）。它返回指数、指数基数
// （10 或 2），或者读取错误或语法错误（如果有）。
//
// 如果设置了 sepOk，下划线字符 “_” 可以出现在连续指数数字之间；
// 这样的下划线不会改变指数的值。如果没有其他错误，下划线位置不正确
// 会报告为错误。如果未设置 sepOk，则不识别下划线，因此会像任何
// 其他不是有效数字的字符一样终止扫描。
//
//	exponent = ( "e" | "E" | "p" | "P" ) [ sign ] digits .
//	sign     = "+" | "-" .
//	digits   = digit { [ '_' ] digit } .
//	digit    = "0" ... "9" .
//
// 仅当设置了 base2ok 时才允许以 2 为底的指数。
func scanExponent(r io.ByteScanner, base2ok, sepOk bool) (exp int64, base int, err error) {
	// 预读一个字符
	ch, err := r.ReadByte()
	if err != nil {
		if err == io.EOF {
			err = nil
		}
		return 0, 10, err
	}

	// 指数字符
	switch ch {
	case 'e', 'E':
		base = 10
	case 'p', 'P':
		if base2ok {
			base = 2
			break // 可以
		}
		fallthrough // 不允许二进制指数
	default:
		r.UnreadByte() // ch 不再属于指数部分
		return 0, 10, nil
	}

	// 符号
	var digits []byte
	ch, err = r.ReadByte()
	if err == nil && (ch == '+' || ch == '-') {
		if ch == '-' {
			digits = append(digits, '-')
		}
		ch, err = r.ReadByte()
	}

	// prev 记录上一次见到的字符：它是 '_'、'0'（一个数字），
	// 或 '.'（其他任何字符）之一。有效的分隔符 '_' 只能出现在数字之后。
	prev := '.'
	invalSep := false

	// 指数值
	hasDigits := false
	for err == nil {
		if '0' <= ch && ch <= '9' {
			digits = append(digits, ch)
			prev = '0'
			hasDigits = true
		} else if ch == '_' && sepOk {
			if prev != '0' {
				invalSep = true
			}
			prev = '_'
		} else {
			r.UnreadByte() // ch 不再属于数字部分
			break
		}
		ch, err = r.ReadByte()
	}

	if err == io.EOF {
		err = nil
	}
	if err == nil && !hasDigits {
		err = errNoDigits
	}
	if err == nil {
		exp, err = strconv.ParseInt(string(digits), 10, 64)
	}
	// 其他错误优先于无效分隔符
	if err == nil && (invalSep || prev == '_') {
		err = errInvalSep
	}

	return
}

// String 返回 x 的字符串表示，形式为 "a/b"（即使 b == 1）。
func (x *Rat) String() string {
	return string(x.marshal(nil))
}

// marshal 实现 [Rat.String]，返回一个字节切片。
// 它将 x 的字符串表示以 "a/b" 的形式（即使 b == 1）追加到 buf，
// 并返回扩展后的缓冲区。
func (x *Rat) marshal(buf []byte) []byte {
	buf = x.a.Append(buf, 10)
	buf = append(buf, '/')
	if len(x.b.abs) != 0 {
		buf = x.b.Append(buf, 10)
	} else {
		buf = append(buf, '1')
	}
	return buf
}

// RatString 返回 x 的字符串表示：如果 b != 1 则为 "a/b" 形式，
// 如果 b == 1 则为 "a" 形式。
func (x *Rat) RatString() string {
	if x.IsInt() {
		return x.a.String()
	}
	return x.String()
}

// FloatString 返回 x 的十进制字符串表示，小数点后具有 prec 位精度。
// 最后一位按最接近舍入，恰好一半时向远离零的方向舍入。
func (x *Rat) FloatString(prec int) string {
	var buf []byte

	if x.IsInt() {
		buf = x.a.Append(buf, 10)
		if prec > 0 {
			buf = append(buf, '.')
			for i := prec; i > 0; i-- {
				buf = append(buf, '0')
			}
		}
		return string(buf)
	}
	// x.b.abs != 0

	stk := getStack()
	defer stk.free()
	q, r := nat(nil).div(stk, nat(nil), x.a.abs, x.b.abs)

	p := natOne
	if prec > 0 {
		p = nat(nil).expNN(stk, natTen, nat(nil).setUint64(uint64(prec)), nil, false)
	}

	r = r.mul(stk, r, p)
	r, r2 := r.div(stk, nat(nil), r, x.b.abs)

	// 看看是否需要向上舍入
	r2 = r2.add(r2, r2)
	if x.b.abs.cmp(r2) <= 0 {
		r = r.add(r, natOne)
		if r.cmp(p) >= 0 {
			q = nat(nil).add(q, natOne)
			r = nat(nil).sub(r, p)
		}
	}

	if x.a.neg {
		buf = append(buf, '-')
	}
	buf = append(buf, q.utoa(10)...) // 如果 q == 0，itoa 会忽略符号

	if prec > 0 {
		buf = append(buf, '.')
		rs := r.utoa(10)
		for i := prec - len(rs); i > 0; i-- {
			buf = append(buf, '0')
		}
		buf = append(buf, rs...)
	}

	return string(buf)
}

// 注意：FloatPrec（见下）放在本文件而不是 rat.go，是因为
//       它的结果与十进制表示/打印相关。

// FloatPrec 返回 x 的十进制表示中小数点后紧接的、非循环数字的
// 个数 n。布尔结果表示 x 的具有这么多位小数的十进制表示是精确的
// 还是舍入的。
//
// 示例：
//
//	x      n    exact    十进制表示（n 位小数）
//	0      0    true     0
//	1      0    true     1
//	1/2    1    true     0.5
//	1/3    0    false    0       （0.333... 舍入）
//	1/4    2    true     0.25
//	1/6    1    false    0.2     （0.166... 舍入）
func (x *Rat) FloatPrec() (n int, exact bool) {
	stk := getStack()
	defer stk.free()

	// 确定 q 以及最大的 p2、p5，使得 d = q·2^p2·5^p5。
	// 结果 n、exact 为：
	//
	//     n = max(p2, p5)
	//     exact = q == 1
	//
	// 详情参见：
	// https://en.wikipedia.org/wiki/Repeating_decimal#Reciprocals_of_integers_not_coprime_to_10
	d := x.Denom().abs // d >= 1

	// 通过统计因子 2 来确定 p2。
	// p2 对应于 d 中末尾的零位。
	// 先做这一步以尽可能减小 q。
	var q nat
	p2 := d.trailingZeroBits()
	q = q.rsh(d, p2)

	// 通过统计因子 5 来确定 p5。
	// 从一个初始的 5 的幂开始构建一张表，
	// 并重复平方，直到该因子不能再整除 q 为止。
	// 然后使用该表来确定 q 中 5 的幂次。
	const fp = 13        // f == 5^fp
	var tab []nat        // tab[i] == (5^fp)^(2^i) == 5^(fp·2^i)
	f := nat{1220703125} // == 5^fp（必须能放进 uint32 Word）
	var t, r nat         // 临时变量
	for {
		if _, r = t.div(stk, r, q, f); len(r) != 0 {
			break // f 不能整除 q
		}
		tab = append(tab, f)
		f = nat(nil).sqr(stk, f) // 使用 nat(nil) 以确保每个表项都有一个新的 f
	}

	// 使用表中的各项（如果有）对 q 进行因式分解。
	// 我们从能整除 q 的最大因子 f = tab[len(tab)-1] 开始。
	// 它最多只能整除一次，否则 f·f 也会整除 q。而这是不可能的，
	// 因为 f·f 是下一个更高的表项，这与 f 最初被选中的方式相矛盾。
	// 同样的推理也适用于后续的因子。
	var p5 uint
	for i := len(tab) - 1; i >= 0; i-- {
		if t, r = t.div(stk, r, q, tab[i]); len(r) == 0 {
			p5 += fp * (1 << i) // tab[i] == 5^(fp·2^i)
			q = q.set(t)
		}
	}

	// 如果 fp != 1，我们可能还剩下 5 的倍数。
	for {
		if t, r = t.div(stk, r, q, natFive); len(r) != 0 {
			break
		}
		p5++
		q = q.set(t)
	}

	return int(max(p2, p5)), q.cmp(natOne) == 0
}
