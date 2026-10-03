// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 以下原始 C 代码、长篇注释及常量来自 FreeBSD 的
// /usr/src/lib/msun/src/e_acosh.c，并附带下述声明。
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
// __ieee754_acosh(x)
// 方法：
//	基于
//	        acosh(x) = log [ x + sqrt(x*x-1) ]
//	我们有
//	        acosh(x) := log(x)+ln2,	if x is large; else
//	        acosh(x) := log(2x-1/(sqrt(x*x-1)+x)) if x>2; else
//	        acosh(x) := log1p(t+sqrt(2.0*t+t*t)); where t=x-1.
//
// 特殊情况：
//	若 x<1，acosh(x) 为带信号的 NaN。
//	acosh(NaN) 为不带信号的 NaN。
//

// Acosh 返回 x 的反双曲余弦。
//
// 特殊情况为：
//
//	Acosh(+Inf) = +Inf
//	Acosh(x) = NaN 若 x < 1
//	Acosh(NaN) = NaN
func Acosh(x float64) float64 {
	if haveArchAcosh {
		return archAcosh(x)
	}
	return acosh(x)
}

func acosh(x float64) float64 {
	const Large = 1 << 28 // 2**28
	// 第一种情况是特殊情况
	switch {
	case x < 1 || IsNaN(x):
		return NaN()
	case x == 1:
		return 0
	case x >= Large:
		return Log(x) + Ln2 // x > 2**28
	case x > 2:
		return Log(2*x - 1/(x+Sqrt(x*x-1))) // 2**28 > x > 2
	}
	t := x - 1
	return Log1p(t + Sqrt(2*t+t*t)) // 2 >= x > 1
}
