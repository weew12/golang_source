// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 以下原始 C 代码、长篇注释及常量来自 FreeBSD 的
// /usr/src/lib/msun/src/s_asinh.c，并附带下述声明。
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
// asinh(x)
// 方法：
//	基于
//	        asinh(x) = sign(x) * log [ |x| + sqrt(x*x+1) ]
//	我们有
//	asinh(x) := x  if  1+x*x=1,
//	         := sign(x)*(log(x)+ln2) for large |x|, else
//	         := sign(x)*log(2|x|+1/(|x|+sqrt(x*x+1))) if|x|>2, else
//	         := sign(x)*log1p(|x| + x**2/(1 + sqrt(1+x**2)))
//

// Asinh 返回 x 的反双曲正弦。
//
// 特殊情况为：
//
//	Asinh(±0) = ±0
//	Asinh(±Inf) = ±Inf
//	Asinh(NaN) = NaN
func Asinh(x float64) float64 {
	if haveArchAsinh {
		return archAsinh(x)
	}
	return asinh(x)
}

func asinh(x float64) float64 {
	const (
		Ln2      = 6.93147180559945286227e-01 // 0x3FE62E42FEFA39EF
		NearZero = 1.0 / (1 << 28)            // 2**-28
		Large    = 1 << 28                    // 2**28
	)
	// 特殊情况
	if IsNaN(x) || IsInf(x, 0) {
		return x
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	var temp float64
	switch {
	case x > Large:
		temp = Log(x) + Ln2 // |x| > 2**28
	case x > 2:
		temp = Log(2*x + 1/(Sqrt(x*x+1)+x)) // 2**28 > |x| > 2.0
	case x < NearZero:
		temp = x // |x| < 2**-28
	default:
		temp = Log1p(x + x*x/(1+Sqrt(1+x*x))) // 2.0 > |x| > 2**-28
	}
	if sign {
		temp = -temp
	}
	return temp
}
