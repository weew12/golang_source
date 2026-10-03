// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 以下原始 C 代码、长篇注释及常量来自 FreeBSD 的
// /usr/src/lib/msun/src/e_atanh.c，并附带下述声明。
// Go 代码是原始 C 代码的简化版本。
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
//
// __ieee754_atanh(x)
// 方法：
//	1. 利用 atanh(-x) = -atanh(x) 将 x 化为正数
//	2. 当 x>=0.5 时
//	            1              2x                          x
//	atanh(x) = --- * log(1 + -------) = 0.5 * log1p(2 * --------)
//	            2             1 - x                      1 - x
//
//	当 x<0.5 时
//	atanh(x) = 0.5*log1p(2x+2x*x/(1-x))
//
// 特殊情况：
//	若 |x| > 1，atanh(x) 为带信号的 NaN；
//	atanh(NaN) 为该 NaN，不带信号；
//	atanh(+-1) 为带信号的 +-INF。
//

// Atanh 返回 x 的反双曲正切。
//
// 特殊情况为：
//
//	Atanh(1) = +Inf
//	Atanh(±0) = ±0
//	Atanh(-1) = -Inf
//	Atanh(x) = NaN 若 x < -1 或 x > 1
//	Atanh(NaN) = NaN
func Atanh(x float64) float64 {
	if haveArchAtanh {
		return archAtanh(x)
	}
	return atanh(x)
}

func atanh(x float64) float64 {
	const NearZero = 1.0 / (1 << 28) // 2**-28
	// 特殊情况
	switch {
	case x < -1 || x > 1 || IsNaN(x):
		return NaN()
	case x == 1:
		return Inf(1)
	case x == -1:
		return Inf(-1)
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	var temp float64
	switch {
	case x < NearZero:
		temp = x
	case x < 0.5:
		temp = x + x
		temp = 0.5 * Log1p(temp+temp*x/(1-x))
	default:
		temp = 0.5 * Log1p((x+x)/(1-x))
	}
	if sign {
		temp = -temp
	}
	return temp
}
