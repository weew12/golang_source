// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

const (
	uvnan    = 0x7FF8000000000001
	uvinf    = 0x7FF0000000000000
	uvneginf = 0xFFF0000000000000
	uvone    = 0x3FF0000000000000
	mask     = 0x7FF
	shift    = 64 - 11 - 1
	bias     = 1023
	signMask = 1 << 63
	fracMask = 1<<shift - 1
)

// Inf 返回正无穷（若 sign >= 0）、负无穷（若 sign < 0）。
func Inf(sign int) float64 {
	var v uint64
	if sign >= 0 {
		v = uvinf
	} else {
		v = uvneginf
	}
	return Float64frombits(v)
}

// NaN 返回一个 IEEE 754 “非数”值。
func NaN() float64 { return Float64frombits(uvnan) }

// IsNaN 报告 f 是否为 IEEE 754 “非数”值。
func IsNaN(f float64) (is bool) {
	// IEEE 754 规定只有 NaN 满足 f != f。
	// 为避开浮点硬件，可以使用：
	//	x := Float64bits(f);
	//	return uint32(x>>shift)&mask == mask && x != uvinf && x != uvneginf
	return f != f
}

// IsInf 根据 sign 报告 f 是否为无穷。
// 若 sign > 0，IsInf 报告 f 是否为正无穷。
// 若 sign < 0，IsInf 报告 f 是否为负无穷。
// 若 sign == 0，IsInf 报告 f 是否为任一无穷。
func IsInf(f float64, sign int) bool {
	// 通过与大浮点数比较来测试无穷。
	// 为避开浮点硬件，可以使用：
	//	x := Float64bits(f);
	//	return sign >= 0 && x == uvinf || sign <= 0 && x == uvneginf;
	if sign == 0 {
		f = Abs(f)
	} else if sign < 0 {
		f = -f
	}
	return f > MaxFloat64
}

// normalize 返回一个规格化数 y 和指数 exp，
// 满足 x == y × 2**exp。它假定 x 是有限且非零的。
func normalize(x float64) (y float64, exp int) {
	const SmallestNormal = 2.2250738585072014e-308 // 2**-1022
	if Abs(x) < SmallestNormal {
		return x * (1 << 52), -52
	}
	return x, 0
}
