// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现 Float 到字符串的转换函数。
// 它紧密参照 strconv/ftoa.go 中的对应实现，
// 但针对 Float 进行了修改和简化。

package big

import (
	"bytes"
	"fmt"
	"strconv"
)

// Text 按照给定的格式和精度 prec 将浮点数 x 转换为字符串。格式为以下之一：
//
//	'e'	-d.dddde±dd，十进制指数，至少两位（可能为 0）指数数字
//	'E'	-d.ddddE±dd，十进制指数，至少两位（可能为 0）指数数字
//	'f'	-ddddd.dddd，无指数
//	'g'	对大指数像 'e'，否则像 'f'
//	'G'	对大指数像 'E'，否则像 'f'
//	'x'	-0xd.dddddp±dd，十六进制尾数，十进制表示的 2 的幂指数
//	'p'	-0x.dddp±dd，十六进制尾数，十进制表示的 2 的幂指数（非标准）
//	'b'	-ddddddp±dd，十进制尾数，十进制表示的 2 的幂指数（非标准）
//
// 对于 2 的幂指数格式，尾数会以规范化形式打印：
//
//	'x'	[1, 2) 范围内的十六进制尾数，或 0
//	'p'	[½, 1) 范围内的十六进制尾数，或 0
//	'b'	使用 x.Prec() 位的十进制整数尾数，或 0
//
// 注意 'x' 形式是大多数其他语言和库所采用的形式。
//
// 如果 format 是其他字符，Text 会返回 "%" 后跟无法识别的格式字符。
//
// 精度 prec 控制由 'e'、'E'、'f'、'g'、'G' 和 'x' 格式打印的数字
// 个数（不包括指数）。对于 'e'、'E'、'f' 和 'x'，它表示小数点后的
// 数字个数。对于 'g' 和 'G'，它表示数字总个数。负的精度会选择
// 能够使用 x.Prec() 尾数位唯一标识值 x 所需的最小十进制数字个数。
// 对于 'b' 和 'p' 格式，prec 值会被忽略。
func (x *Float) Text(format byte, prec int) string {
	cap := 10 // TODO(gri) 在这里确定一个合适/更好的值
	if prec > 0 {
		cap += prec
	}
	return string(x.Append(make([]byte, 0, cap), format, prec))
}

// String 像 x.Text('g', 10) 一样格式化 x。
// （必须显式调用 String，[Float.Format] 不支持 %s 动词。）
func (x *Float) String() string {
	return x.Text('g', 10)
}

// Append 将由 x.Text 生成的浮点数 x 的字符串形式追加到 buf，
// 并返回扩展后的缓冲区。
func (x *Float) Append(buf []byte, fmt byte, prec int) []byte {
	// 符号
	if x.neg {
		buf = append(buf, '-')
	}

	// Inf
	if x.form == inf {
		if !x.neg {
			buf = append(buf, '+')
		}
		return append(buf, "Inf"...)
	}

	// 挑出简单的格式
	switch fmt {
	case 'b':
		return x.fmtB(buf)
	case 'p':
		return x.fmtP(buf)
	case 'x':
		return x.fmtX(buf, prec)
	}

	// 算法：
	//   1) 将 Float 转换为多精度十进制数
	//   2) 舍入到所需精度
	//   3) 读出数字并格式化

	// 1) 将 Float 转换为多精度十进制数
	var d decimal // == 0.0
	if x.form == finite {
		// x != 0
		d.init(x.mant, int(x.exp)-x.mant.bitLen())
	}

	// 2) 舍入到所需精度
	shortest := false
	if prec < 0 {
		shortest = true
		roundShortest(&d, x)
		// 最短表示模式的精度。
		switch fmt {
		case 'e', 'E':
			prec = len(d.mant) - 1
		case 'f':
			prec = max(len(d.mant)-d.exp, 0)
		case 'g', 'G':
			prec = len(d.mant)
		}
	} else {
		// 进行适当的舍入
		switch fmt {
		case 'e', 'E':
			// 小数点前一位数字，后面是指定位数的数字
			d.round(1 + prec)
		case 'f':
			// 小数点前后数字的个数
			d.round(d.exp + prec)
		case 'g', 'G':
			if prec == 0 {
				prec = 1
			}
			d.round(prec)
		}
	}

	// 3) 读出数字并格式化
	switch fmt {
	case 'e', 'E':
		return fmtE(buf, fmt, prec, d)
	case 'f':
		return fmtF(buf, prec, d)
	case 'g', 'G':
		// 去掉 %e 格式中末尾的小数零
		eprec := prec
		if eprec > len(d.mant) && len(d.mant) >= d.exp {
			eprec = len(d.mant)
		}
		// 如果转换得到的指数小于 -4 或大于等于精度，
		// 则使用 %e 格式。
		// 如果精度已是最短可能，则此决策使用 eprec = 6。
		if shortest {
			eprec = 6
		}
		exp := d.exp - 1
		if exp < -4 || exp >= eprec {
			if prec > len(d.mant) {
				prec = len(d.mant)
			}
			return fmtE(buf, fmt+'e'-'g', prec-1, d)
		}
		if prec > d.exp {
			prec = len(d.mant)
		}
		return fmtF(buf, max(prec-d.exp, 0), d)
	}

	// 未知格式
	if x.neg {
		buf = buf[:len(buf)-1] // 符号被提前添加了 - 再次移除它
	}
	return append(buf, '%', fmt)
}

func roundShortest(d *decimal, x *Float) {
	// 如果尾数为零，则该数为零 - 现在停止
	if len(d.mant) == 0 {
		return
	}

	// 方法：对于给定的 x 精度，区间 [x - 1/2ulp, x + 1/2ulp]（可能为
	// 开区间）内的所有数字都会舍入到 x。以十进制形式计算下界和上界，
	// 并找出满足 lower <= d <= upper 的最短十进制数 d。

	// TODO(gri) strconv/ftoa.do 描述了某些情况下的捷径。
	// 看看我们是否也能在这里（以调整后的形式）使用它。

	// 1) 计算 x 的规范化尾数 mant 和指数 exp，使得 mant 的最低有效位
	// 对应于 x 精度的 1/2 ulp（即对于 mant，我们需要 x.prec + 1 位）。
	mant := nat(nil).set(x.mant)
	exp := int(x.exp) - mant.bitLen()
	s := mant.bitLen() - int(x.prec+1)
	switch {
	case s < 0:
		mant = mant.lsh(mant, uint(-s))
	case s > 0:
		mant = mant.rsh(mant, uint(+s))
	}
	exp += s
	// x = mant * 2**exp with lsb(mant) == 1/2 ulp of x.prec

	// 2) 通过减去 1/2 ulp 计算下界。
	var lower decimal
	var tmp nat
	lower.init(tmp.sub(mant, natOne), exp)

	// 3) 通过加上 1/2 ulp 计算上界。
	var upper decimal
	upper.init(tmp.add(mant, natOne), exp)

	// 只有当原始尾数为偶数时，上界和下界才可能是输出，
	// 这样 ToNearestEven 舍入会舍入到原始尾数而不是相邻值。
	inclusive := mant[0]&2 == 0 // 测试第 1 位，因为原始尾数被左移了 1 位

	// 现在我们可以确定所需的最小数字个数。
	// 逐个检查，直到 d 与上界和下界区分开来。
	for i, m := range d.mant {
		l := lower.at(i)
		u := upper.at(i)

		// 如果 lower 的数字不同，或者 lower 是包含的并且恰好是
		// 向下舍入（截断）的结果（即我们已到达 lower 的最后一位数字），
		// 则可以向下舍入。
		okdown := l != m || inclusive && i+1 == len(lower.mant)

		// 如果 upper 的数字不同，并且 upper 是包含的或 upper 大于
		// 向上舍入的结果，则可以向上舍入。
		okup := m != u && (inclusive || m+1 < u || i+1 < len(upper.mant))

		// 如果两者都可以，则舍入到最接近的一个。
		// 如果只有一种可行，就采用它。
		switch {
		case okdown && okup:
			d.round(i + 1)
			return
		case okdown:
			d.roundDown(i + 1)
			return
		case okup:
			d.roundUp(i + 1)
			return
		}
	}
}

// %e: d.ddddde±dd
func fmtE(buf []byte, fmt byte, prec int, d decimal) []byte {
	// 第一位数字
	ch := byte('0')
	if len(d.mant) > 0 {
		ch = d.mant[0]
	}
	buf = append(buf, ch)

	// .更多数字
	if prec > 0 {
		buf = append(buf, '.')
		i := 1
		m := min(len(d.mant), prec+1)
		if i < m {
			buf = append(buf, d.mant[i:m]...)
			i = m
		}
		for ; i <= prec; i++ {
			buf = append(buf, '0')
		}
	}

	// e±
	buf = append(buf, fmt)
	var exp int64
	if len(d.mant) > 0 {
		exp = int64(d.exp) - 1 // -1，因为第一位数字在 '.' 之前打印
	}
	if exp < 0 {
		ch = '-'
		exp = -exp
	} else {
		ch = '+'
	}
	buf = append(buf, ch)

	// dd...d
	if exp < 10 {
		buf = append(buf, '0') // 至少两位指数数字
	}
	return strconv.AppendInt(buf, exp, 10)
}

// %f: ddddddd.ddddd
func fmtF(buf []byte, prec int, d decimal) []byte {
	// 整数部分，根据需要补零
	if d.exp > 0 {
		m := min(len(d.mant), d.exp)
		buf = append(buf, d.mant[:m]...)
		for ; m < d.exp; m++ {
			buf = append(buf, '0')
		}
	} else {
		buf = append(buf, '0')
	}

	// 小数部分
	if prec > 0 {
		buf = append(buf, '.')
		for i := 0; i < prec; i++ {
			buf = append(buf, d.at(d.exp+i))
		}
	}

	return buf
}

// fmtB 将 x 以格式「尾数 "p" 指数」追加为字符串（十进制尾数和二进制指数），
// 如果 x 为零则追加 "0"，并返回扩展后的缓冲区。
// 尾数被规范化，使其在二进制表示中使用 x.Prec() 位。
// x 的符号被忽略，且 x 不能是 Inf。
// （调用者在调用 fmtB 之前处理 Inf。）
func (x *Float) fmtB(buf []byte) []byte {
	if x.form == zero {
		return append(buf, '0')
	}

	if debugFloat && x.form != finite {
		panic("non-finite float")
	}
	// x != 0

	// 调整尾数以恰好使用 x.prec 位
	m := x.mant
	switch w := uint32(len(x.mant)) * _W; {
	case w < x.prec:
		m = nat(nil).lsh(m, uint(x.prec-w))
	case w > x.prec:
		m = nat(nil).rsh(m, uint(w-x.prec))
	}

	buf = append(buf, m.utoa(10)...)
	buf = append(buf, 'p')
	e := int64(x.exp) - int64(x.prec)
	if e >= 0 {
		buf = append(buf, '+')
	}
	return strconv.AppendInt(buf, e, 10)
}

// fmtX 将 x 以格式 "0x1." 尾数 "p" 指数追加为字符串（十六进制尾数和二进制
// 指数），如果 x 为零则追加 "0x0p0"，并返回扩展后的缓冲区。
// 非零尾数被规范化，使得 1.0 <= 尾数 < 2.0。
// x 的符号被忽略，且 x 不能是 Inf。
// （调用者在调用 fmtX 之前处理 Inf。）
func (x *Float) fmtX(buf []byte, prec int) []byte {
	if x.form == zero {
		buf = append(buf, "0x0"...)
		if prec > 0 {
			buf = append(buf, '.')
			for i := 0; i < prec; i++ {
				buf = append(buf, '0')
			}
		}
		buf = append(buf, "p+00"...)
		return buf
	}

	if debugFloat && x.form != finite {
		panic("non-finite float")
	}

	// 将尾数舍入到 n 位
	var n uint
	if prec < 0 {
		n = 1 + (x.MinPrec()-1+3)/4*4 // 将 MinPrec 向上舍入到 1 mod 4
	} else {
		n = 1 + 4*uint(prec)
	}
	// n%4 == 1
	x = new(Float).SetPrec(n).SetMode(x.mode).Set(x)

	// 调整尾数以恰好使用 n 位
	m := x.mant
	switch w := uint(len(x.mant)) * _W; {
	case w < n:
		m = nat(nil).lsh(m, n-w)
	case w > n:
		m = nat(nil).rsh(m, w-n)
	}
	exp64 := int64(x.exp) - 1 // 避免回绕

	hm := m.utoa(16)
	if debugFloat && hm[0] != '1' {
		panic("incorrect mantissa: " + string(hm))
	}
	buf = append(buf, "0x1"...)
	if len(hm) > 1 {
		buf = append(buf, '.')
		buf = append(buf, hm[1:]...)
	}

	buf = append(buf, 'p')
	if exp64 >= 0 {
		buf = append(buf, '+')
	} else {
		exp64 = -exp64
		buf = append(buf, '-')
	}
	// 强制至少两位指数数字，以与 fmt 保持一致。
	if exp64 < 10 {
		buf = append(buf, '0')
	}
	return strconv.AppendInt(buf, exp64, 10)
}

// fmtP 将 x 以格式 "0x." 尾数 "p" 指数追加为字符串（十六进制尾数和二进制
// 指数），如果 x 为零则追加 "0"，并返回扩展后的缓冲区。
// 尾数被规范化，使得 0.5 <= 0.尾数 < 1.0。
// x 的符号被忽略，且 x 不能是 Inf。
// （调用者在调用 fmtP 之前处理 Inf。）
func (x *Float) fmtP(buf []byte) []byte {
	if x.form == zero {
		return append(buf, '0')
	}

	if debugFloat && x.form != finite {
		panic("non-finite float")
	}
	// x != 0

	// 尽早去掉末尾的 0 字
	//（无需先转换为十六进制 0 再修剪）
	m := x.mant
	i := 0
	for i < len(m) && m[i] == 0 {
		i++
	}
	m = m[i:]

	buf = append(buf, "0x."...)
	buf = append(buf, bytes.TrimRight(m.utoa(16), "0")...)
	buf = append(buf, 'p')
	if x.exp >= 0 {
		buf = append(buf, '+')
	}
	return strconv.AppendInt(buf, int64(x.exp), 10)
}

var _ fmt.Formatter = &floatZero // *Float 必须实现 fmt.Formatter

// Format 实现 [fmt.Formatter]。它接受浮点数的所有常规格式
// （'b'、'e'、'E'、'f'、'F'、'g'、'G'、'x'）以及 'p' 和 'v'。
// 'p' 的含义参见 (*Float).Text。'v' 格式的处理方式与 'g' 相同。
// Format 还支持指定以数字表示的最小精度、输出字段宽度，以及
// 用于符号控制的格式标志 '+' 和 ' '、用于空格或零填充的 '0'，
// 以及用于左对齐或右对齐的 '-'。详情参见 fmt 包。
func (x *Float) Format(s fmt.State, format rune) {
	prec, hasPrec := s.Precision()
	if !hasPrec {
		prec = 6 // 'e'、'f' 的默认精度
	}

	switch format {
	case 'e', 'E', 'f', 'b', 'p', 'x':
		// 无需处理
	case 'F':
		// (*Float).Text 不支持 'F'；像 'f' 一样处理
		format = 'f'
	case 'v':
		// 像 'g' 一样处理
		format = 'g'
		fallthrough
	case 'g', 'G':
		if !hasPrec {
			prec = -1 // 'g'、'G' 的默认精度
		}
	default:
		fmt.Fprintf(s, "%%!%c(*big.Float=%s)", format, x.String())
		return
	}
	var buf []byte
	buf = x.Append(buf, byte(format), prec)
	if len(buf) == 0 {
		buf = []byte("?") // 不应发生，但不要崩溃
	}
	// len(buf) > 0

	var sign string
	switch {
	case buf[0] == '-':
		sign = "-"
		buf = buf[1:]
	case buf[0] == '+':
		// +无穷
		sign = "+"
		if s.Flag(' ') {
			sign = " "
		}
		buf = buf[1:]
	case s.Flag('+'):
		sign = "+"
	case s.Flag(' '):
		sign = " "
	}

	var padding int
	if width, hasWidth := s.Width(); hasWidth && width > len(sign)+len(buf) {
		padding = width - len(sign) - len(buf)
	}

	switch {
	case s.Flag('0') && !x.IsInf():
		// 左侧补零
		writeMultiple(s, sign, 1)
		writeMultiple(s, "0", padding)
		s.Write(buf)
	case s.Flag('-'):
		// 右侧填充
		writeMultiple(s, sign, 1)
		s.Write(buf)
		writeMultiple(s, " ", padding)
	default:
		// 左侧填充
		writeMultiple(s, " ", padding)
		writeMultiple(s, sign, 1)
		s.Write(buf)
	}
}
