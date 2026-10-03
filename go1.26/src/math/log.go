// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	浮点对数。
*/

// 原始 C 代码、长注释及下面的常量
// 来自 FreeBSD 的 /usr/src/lib/msun/src/e_log.c，
// 并随附此声明。Go 代码是原始 C 的更简单版本。
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
// __ieee754_log(x)
// 返回 x 的对数
//
// 方法：
//   1. 参数归约：找到 k 和 f 使得
//			x = 2**k * (1+f),
//	   其中  sqrt(2)/2 < 1+f < sqrt(2) 。
//
//   2. log(1+f) 的逼近。
//	令 s = f/(2+f) ; 基于 log(1+f) = log(1+s) - log(1-s)
//		 = 2s + 2/3 s**3 + 2/5 s**5 + .....,
//	     	 = 2s + s*R
//      我们使用一种特殊的 Reme 算法在 [0,0.1716] 上生成
//	一个 14 次多项式来逼近 R。该多项式逼近的最大误差
//	以 2**-58.45 为界。换
//	句话说，
//		        2      4      6      8      10      12      14
//	    R(z) ~ L1*s +L2*s +L3*s +L4*s +L5*s  +L6*s  +L7*s
//	（L1 到 L7 的值列在程序中）且
//	    |      2          14          |     -58.45
//	    | L1*s +...+L7*s    -  R(z) | <= 2
//	    |                             |
//	注意 2s = f - s*f = f - hfsq + s*hfsq，其中 hfsq = f*f/2。
//	为了保证 log 的误差低于 1ulp，我们按如下方式计算 log
//		log(1+f) = f - s*(f - R)		(if f is not too large)
//		log(1+f) = f - (hfsq - s*(hfsq+R)).	(better accuracy)
//
//	3. 最后，  log(x) = k*Ln2 + log(1+f).
//			    = k*Ln2_hi+(f-(hfsq-(s*(hfsq+R)+k*Ln2_lo)))
//	   这里 Ln2 被拆分为两个浮点数：
//			Ln2_hi + Ln2_lo,
//	   其中当 |n| < 2000 时 n*Ln2_hi 总是精确的。
//
// 特殊情况：
//	若 x < 0（包括 -INF），log(x) 为带信号的 NaN ;
//	log(+INF) 为 +INF; log(0) 为带信号的 -INF;
//	log(NaN) 为该 NaN 且无信号。
//
// 精度：
//	根据误差分析，误差始终小于
//	1 ulp（最后一位的单位）。
//
// 常量：
// 十六进制值是下面这些常量的预期值。
// 可以使用十进制值，前提是
// 编译器从十进制到二进制的转换足够精确，
// 以产生所示出的十六进制值。

// Log 返回 x 的自然对数。
//
// 特殊情况为：
//
//	Log(+Inf) = +Inf
//	Log(0) = -Inf
//	Log(x < 0) = NaN
//	Log(NaN) = NaN
func Log(x float64) float64 {
	if haveArchLog {
		return archLog(x)
	}
	return log(x)
}

func log(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01 /* 3fe62e42 fee00000 */
		Ln2Lo = 1.90821492927058770002e-10 /* 3dea39ef 35793c76 */
		L1    = 6.666666666666735130e-01   /* 3FE55555 55555593 */
		L2    = 3.999999999940941908e-01   /* 3FD99999 9997FA04 */
		L3    = 2.857142874366239149e-01   /* 3FD24924 94229359 */
		L4    = 2.222219843214978396e-01   /* 3FCC71C5 1D8E78AF */
		L5    = 1.818357216161805012e-01   /* 3FC74664 96CB03DE */
		L6    = 1.531383769920937332e-01   /* 3FC39A09 D078C69F */
		L7    = 1.479819860511658591e-01   /* 3FC2F112 DF3E5244 */
	)

	// 特殊情况
	switch {
	case IsNaN(x) || IsInf(x, 1):
		return x
	case x < 0:
		return NaN()
	case x == 0:
		return Inf(-1)
	}

	// 归约
	f1, ki := Frexp(x)
	if f1 < Sqrt2/2 {
		f1 *= 2
		ki--
	}
	f := f1 - 1
	k := float64(ki)

	// 计算
	s := f / (2 + f)
	s2 := s * s
	s4 := s2 * s2
	t1 := s2 * (L1 + s4*(L3+s4*(L5+s4*L7)))
	t2 := s4 * (L2 + s4*(L4+s4*L6))
	R := t1 + t2
	hfsq := 0.5 * f * f
	return k*Ln2Hi - ((hfsq - (s*(hfsq+R) + k*Ln2Lo)) - f)
}
