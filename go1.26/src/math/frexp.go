// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Frexp 将 f 拆分为一个归一化小数
// 和一个 2 的整数次幂。
// 它返回满足 f == frac × 2**exp 的 frac 和 exp，
// 其中 frac 的绝对值位于区间 [½, 1)。
//
// 特殊情况为：
//
//	Frexp(±0) = ±0, 0
//	Frexp(±Inf) = ±Inf, 0
//	Frexp(NaN) = NaN, 0
func Frexp(f float64) (frac float64, exp int) {
	if haveArchFrexp {
		return archFrexp(f)
	}
	return frexp(f)
}

func frexp(f float64) (frac float64, exp int) {
	// 特殊情况
	switch {
	case f == 0:
		return f, 0 // 正确地返回 -0
	case IsInf(f, 0) || IsNaN(f):
		return f, 0
	}
	f, exp = normalize(f)
	x := Float64bits(f)
	exp += int((x>>shift)&mask) - bias + 1
	x &^= mask << shift
	x |= (-1 + bias) << shift
	frac = Float64frombits(x)
	return
}
