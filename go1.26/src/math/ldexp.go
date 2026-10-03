// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Ldexp 是 [Frexp] 的逆运算。
// 它返回 frac × 2**exp。
//
// 特殊情况为：
//
//	Ldexp(±0, exp) = ±0
//	Ldexp(±Inf, exp) = ±Inf
//	Ldexp(NaN, exp) = NaN
func Ldexp(frac float64, exp int) float64 {
	if haveArchLdexp {
		return archLdexp(frac, exp)
	}
	return ldexp(frac, exp)
}

func ldexp(frac float64, exp int) float64 {
	// 特殊情况
	switch {
	case frac == 0:
		return frac // 正确返回 -0
	case IsInf(frac, 0) || IsNaN(frac):
		return frac
	}
	frac, e := normalize(frac)
	exp += e
	x := Float64bits(frac)
	exp += int(x>>shift)&mask - bias
	if exp < -1075 {
		return Copysign(0, frac) // 下溢
	}
	if exp > 1023 { // 溢出
		if frac < 0 {
			return Inf(-1)
		}
		return Inf(1)
	}
	var m float64 = 1
	if exp < -1022 { // 非规格化
		exp += 53
		m = 1.0 / (1 << 53) // 2**-53
	}
	x &^= mask << shift
	x |= uint64(exp+bias) << shift
	return m * Float64frombits(x)
}
