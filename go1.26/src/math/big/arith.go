// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件提供了对字向量进行基本多精度算术运算的 Go 实现。
// 这些实现带有后缀 _g。
// 对于没有这些例程汇编实现的平台，需要它们。
// 本文件还包含一些可以在 Go 中足够高效实现的基本运算。

package big

import (
	"math/bits"
	_ "unsafe" // 用于 go:linkname
)

// A Word 表示多精度无符号整数的一个数位。
type Word uint

const (
	_S = _W / 8 // 字长（字节）

	_W = bits.UintSize // 字长（比特）
	_B = 1 << _W       // 数位基数
	_M = _B - 1        // 数位掩码
)

// 在这些例程中，调用者有责任确保 x、y 和 z 三者长度相同。
// 我们会检查这一点并 panic。
// 这些例程的汇编版本不包含该检查。
//
// 检查+panic 还有教导编译器的作用：即
// “i 在 z 的范围内”蕴含“i 在 x 和 y 的范围内”，从而消除
// 从 0 到 len(z) 的循环中的所有边界检查，反之亦然。

// ----------------------------------------------------------------------------
// 对字的基本运算
//
// 这些运算被下面的向量运算所使用。

// z1<<_W + z0 = x*y
func mulWW(x, y Word) (z1, z0 Word) {
	hi, lo := bits.Mul(uint(x), uint(y))
	return Word(hi), Word(lo)
}

// z1<<_W + z0 = x*y + c
func mulAddWWW_g(x, y, c Word) (z1, z0 Word) {
	hi, lo := bits.Mul(uint(x), uint(y))
	var cc uint
	lo, cc = bits.Add(lo, uint(c), 0)
	return Word(hi + cc), Word(lo)
}

// nlz 返回 x 中前导零的个数。
// 为方便起见，封装了 bits.LeadingZeros 调用。
func nlz(x Word) uint {
	return uint(bits.LeadingZeros(uint(x)))
}

// 产生的进位 c 要么为 0 要么为 1。
func addVV_g(z, x, y []Word) (c Word) {
	if len(x) != len(z) || len(y) != len(z) {
		panic("addVV len")
	}

	for i := range z {
		zi, cc := bits.Add(uint(x[i]), uint(y[i]), uint(c))
		z[i] = Word(zi)
		c = Word(cc)
	}
	return
}

// 产生的进位 c 要么为 0 要么为 1。
func subVV_g(z, x, y []Word) (c Word) {
	if len(x) != len(z) || len(y) != len(z) {
		panic("subVV len")
	}

	for i := range z {
		zi, cc := bits.Sub(uint(x[i]), uint(y[i]), uint(c))
		z[i] = Word(zi)
		c = Word(cc)
	}
	return
}

// addVW 设置 z = x + y，返回最终的进位 c。
// 如果 len(x) != len(z)，行为未定义。
// 如果 len(z) == 0，c = y；否则 c 为 0 或 1。
//
// addVW 应当是内部细节，
// 但广泛使用的包通过 linkname 访问它。
// “耻辱墙”上的著名成员包括：
//   - github.com/remyoudompheng/bigfft
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname addVW
func addVW(z, x []Word, y Word) (c Word) {
	if len(x) != len(z) {
		panic("addVW len")
	}

	if len(z) == 0 {
		return y
	}
	zi, cc := bits.Add(uint(x[0]), uint(y), 0)
	z[0] = Word(zi)
	if cc == 0 {
		if &z[0] != &x[0] {
			copy(z[1:], x[1:])
		}
		return 0
	}
	for i := 1; i < len(z); i++ {
		xi := x[i]
		if xi != ^Word(0) {
			z[i] = xi + 1
			if &z[0] != &x[0] {
				copy(z[i+1:], x[i+1:])
			}
			return 0
		}
		z[i] = 0
	}
	return 1
}

// addVW_ref 是 addVW 的参考实现，仅用于测试。
func addVW_ref(z, x []Word, y Word) (c Word) {
	c = y
	for i := range z {
		zi, cc := bits.Add(uint(x[i]), uint(c), 0)
		z[i] = Word(zi)
		c = Word(cc)
	}
	return
}

// subVW 设置 z = x - y，返回最终的进位 c。
// 如果 len(x) != len(z)，行为未定义。
// 如果 len(z) == 0，c = y；否则 c 为 0 或 1。
//
// subVW 应当是内部细节，
// 但广泛使用的包通过 linkname 访问它。
// “耻辱墙”上的著名成员包括：
//   - github.com/remyoudompheng/bigfft
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname subVW
func subVW(z, x []Word, y Word) (c Word) {
	if len(x) != len(z) {
		panic("subVW len")
	}

	if len(z) == 0 {
		return y
	}
	zi, cc := bits.Sub(uint(x[0]), uint(y), 0)
	z[0] = Word(zi)
	if cc == 0 {
		if &z[0] != &x[0] {
			copy(z[1:], x[1:])
		}
		return 0
	}
	for i := 1; i < len(z); i++ {
		xi := x[i]
		if xi != 0 {
			z[i] = xi - 1
			if &z[0] != &x[0] {
				copy(z[i+1:], x[i+1:])
			}
			return 0
		}
		z[i] = ^Word(0)
	}
	return 1
}

// subVW_ref 是 subVW 的参考实现，仅用于测试。
func subVW_ref(z, x []Word, y Word) (c Word) {
	c = y
	for i := range z {
		zi, cc := bits.Sub(uint(x[i]), uint(c), 0)
		z[i] = Word(zi)
		c = Word(cc)
	}
	return c
}

func lshVU_g(z, x []Word, s uint) (c Word) {
	if len(x) != len(z) {
		panic("lshVU len")
	}

	if s == 0 {
		copy(z, x)
		return
	}
	if len(z) == 0 {
		return
	}
	s &= _W - 1 // 向编译器提示：按 s 移位不需要保护代码
	ŝ := _W - s
	ŝ &= _W - 1 // 同上
	c = x[len(z)-1] >> ŝ
	for i := len(z) - 1; i > 0; i-- {
		z[i] = x[i]<<s | x[i-1]>>ŝ
	}
	z[0] = x[0] << s
	return
}

func rshVU_g(z, x []Word, s uint) (c Word) {
	if len(x) != len(z) {
		panic("rshVU len")
	}

	if s == 0 {
		copy(z, x)
		return
	}
	if len(z) == 0 {
		return
	}
	s &= _W - 1 // 向编译器提示：按 s 移位不需要保护代码
	ŝ := _W - s
	ŝ &= _W - 1 // 同上
	c = x[0] << ŝ
	for i := 1; i < len(z); i++ {
		z[i-1] = x[i-1]>>s | x[i]<<ŝ
	}
	z[len(z)-1] = x[len(z)-1] >> s
	return
}

func mulAddVWW_g(z, x []Word, y, r Word) (c Word) {
	if len(x) != len(z) {
		panic("mulAddVWW len")
	}
	c = r
	for i := range z {
		c, z[i] = mulAddWWW_g(x[i], y, c)
	}
	return
}

func addMulVVWW_g(z, x, y []Word, m, a Word) (c Word) {
	if len(x) != len(z) || len(y) != len(z) {
		panic("rshVU len")
	}

	c = a
	for i := range z {
		z1, z0 := mulAddWWW_g(y[i], m, x[i])
		lo, cc := bits.Add(uint(z0), uint(c), 0)
		c, z[i] = Word(cc), Word(lo)
		c += z1
	}
	return
}

// q = ( x1 << _W + x0 - r)/y. m = floor(( _B^2 - 1 ) / d - _B). 要求 x1<y。
// 近似倒数，参考文献 "Improved Division by Invariant Integers
// (IEEE Transactions on Computers, 11 Jun. 2010)"
func divWW(x1, x0, y, m Word) (q, r Word) {
	s := nlz(y)
	if s != 0 {
		x1 = x1<<s | x0>>(_W-s)
		x0 <<= s
		y <<= s
	}
	d := uint(y)
	// 我们知道
	//   m = ⎣(B^2-1)/d⎦-B
	//   ⎣(B^2-1)/d⎦ = m+B
	//   (B^2-1)/d = m+B+delta1    0 <= delta1 <= (d-1)/d
	//   B^2/d = m+B+delta2        0 <= delta2 <= 1
	// 我们要计算的商是
	//   quotient = ⎣(x1*B+x0)/d⎦
	//            = ⎣(x1*B*(B^2/d)+x0*(B^2/d))/B^2⎦
	//            = ⎣(x1*B*(m+B+delta2)+x0*(m+B+delta2))/B^2⎦
	//            = ⎣(x1*m+x1*B+x0)/B + x0*m/B^2 + delta2*(x1*B+x0)/B^2⎦
	// 这个三项和的后两项介于 0 和 1 之间。
	// 所以我们只需计算第一项，结果至多偏小 2。
	t1, t0 := bits.Mul(uint(m), uint(x1))
	_, c := bits.Add(t0, uint(x0), 0)
	t1, _ = bits.Add(t1, uint(x1), c)
	// 商要么是 t1，要么是 t1+1，要么是 t1+2。
	// 我们尝试 t1 并在需要时进行调整。
	qq := t1
	// 计算余数 r=x-d*q。
	dq1, dq0 := bits.Mul(d, qq)
	r0, b := bits.Sub(uint(x0), dq0, 0)
	r1, _ := bits.Sub(uint(x1), dq1, b)
	// 我们刚计算的余数上界为 B+d：
	// r = x1*B + x0 - d*q.
	//   = x1*B + x0 - d*⎣(x1*m+x1*B+x0)/B⎦
	//   = x1*B + x0 - d*((x1*m+x1*B+x0)/B-alpha)                                   0 <= alpha < 1
	//   = x1*B + x0 - x1*d/B*m                         - x1*d - x0*d/B + d*alpha
	//   = x1*B + x0 - x1*d/B*⎣(B^2-1)/d-B⎦             - x1*d - x0*d/B + d*alpha
	//   = x1*B + x0 - x1*d/B*⎣(B^2-1)/d-B⎦             - x1*d - x0*d/B + d*alpha
	//   = x1*B + x0 - x1*d/B*((B^2-1)/d-B-beta)        - x1*d - x0*d/B + d*alpha   0 <= beta < 1
	//   = x1*B + x0 - x1*B + x1/B + x1*d + x1*d/B*beta - x1*d - x0*d/B + d*alpha
	//   =        x0        + x1/B        + x1*d/B*beta        - x0*d/B + d*alpha
	//   = x0*(1-d/B) + x1*(1+d*beta)/B + d*alpha
	//   <  B*(1-d/B) +  d*B/B          + d          because x0<B (and 1-d/B>0), x1<d, 1+d*beta<=B, alpha<1
	//   =  B - d     +  d              + d
	//   = B+d
	// 所以 r1 只能是 0 或 1。如果 r1 为 1，则我们可知 q 太小了。
	// 给 q 加 1 并从 r 中减去 d。这保证 r <B，因此
	// 我们不再需要跟踪 r1。
	if r1 != 0 {
		qq++
		r0 -= d
	}
	// 如果余数仍然太大，再将 q 增一。
	if r0 >= d {
		qq++
		r0 -= d
	}
	return Word(qq), Word(r0 >> s)
}

// reciprocalWord 返回除数的倒数。rec = floor(( _B^2 - 1 ) / u - _B)。u = d1 << nlz(d1)。
func reciprocalWord(d1 Word) Word {
	u := uint(d1 << nlz(d1))
	x1 := ^u
	x0 := uint(_M)
	rec, _ := bits.Div(x1, x0, u) // (_B^2-1)/U-_B = (_B*(_M-C)+_M)/U
	return Word(rec)
}
