// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 原始 C 代码、长注释及下面的常量
// 来自 FreeBSD 的 /usr/src/lib/msun/src/s_log1p.c，
// 并随附此声明。Go 代码是原始 C 的简化版本。
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
// double log1p(double x)
//
// 方法：
//   1. 参数归约：找到 k 和 f 使得
//                      1+x = 2**k * (1+f),
//         其中  sqrt(2)/2 < 1+f < sqrt(2) 。
//
//      注意：若 k=0，则 f=x 是精确的。然而，若 k!=0，则 f
//      可能无法精确表示。在这种情况下，需要一个修正
//      项。令 u=1+x 的舍入值。令 c = (1+x)-u，则
//      log(1+x) - log(u) ~ c/u。因此，我们继续计算 log(u)，
//      并加回修正项 c/u。
//      （注意：当 x > 2**53 时，直接返回 log(x) 即可）
//
//   2. log1p(f) 的逼近。
//      令 s = f/(2+f) ; 基于 log(1+f) = log(1+s) - log(1-s)
//               = 2s + 2/3 s**3 + 2/5 s**5 + .....,
//               = 2s + s*R
//      我们使用一种特殊的 Reme 算法在 [0,0.1716] 上生成
//      一个 14 次多项式来逼近 R，该多项式逼近的最大误差
//      以 2**-58.45 为界。换
//      句话说，
//                      2      4      6      8      10      12      14
//          R(z) ~ Lp1*s +Lp2*s +Lp3*s +Lp4*s +Lp5*s  +Lp6*s  +Lp7*s
//      （Lp1 到 Lp7 的值列在程序中）
//      且
//          |      2          14          |     -58.45
//          | Lp1*s +...+Lp7*s    -  R(z) | <= 2
//          |                             |
//      注意 2s = f - s*f = f - hfsq + s*hfsq，其中 hfsq = f*f/2。
//      为了保证 log 的误差低于 1ulp，我们计算 log
//      通过
//              log1p(f) = f - (hfsq - s*(hfsq+R)).
//
//   3. 最后， log1p(x) = k*ln2 + log1p(f).
//                        = k*ln2_hi+(f-(hfsq-(s*(hfsq+R)+k*ln2_lo)))
//      这里 ln2 被拆分为两个浮点数：
//                   ln2_hi + ln2_lo,
//      其中当 |n| < 2000 时 n*ln2_hi 总是精确的。
//
// 特殊情况：
//      若 x < -1（包括 -INF），log1p(x) 为带信号的 NaN ;
//      log1p(+INF) 为 +INF; log1p(-1) 为带信号的 -INF;
//      log1p(NaN) 为该 NaN 且无信号。
//
// 精度：
//      根据误差分析，误差始终小于
//      1 ulp（最后一位的单位）。
//
// 常量：
// 十六进制值是下面这些常量的预期值。
// 可以使用十进制值，前提是
// 编译器从十进制到二进制的转换足够精确，
// 以产生所示出的十六进制值。
//
// 注意：假设 log() 返回准确结果，可以使用以下
//       算法将 log1p(x) 计算到几个 ULP 以内：
//
//              u = 1+x;
//              if(u==1.0) return x ; else
//                         return log(u)*(x/(u-1.0));
//
//       参见 HP-15C Advanced Functions Handbook, p.193.

// Log1p 返回 1 加上其参数 x 的自然对数。
// 当 x 接近零时，它比 [Log](1 + x) 更精确。
//
// 特殊情况为：
//
//	Log1p(+Inf) = +Inf
//	Log1p(±0) = ±0
//	Log1p(-1) = -Inf
//	Log1p(x < -1) = NaN
//	Log1p(NaN) = NaN
func Log1p(x float64) float64 {
	if haveArchLog1p {
		return archLog1p(x)
	}
	return log1p(x)
}

func log1p(x float64) float64 {
	const (
		Sqrt2M1     = 4.142135623730950488017e-01  // Sqrt(2)-1 = 0x3fda827999fcef34
		Sqrt2HalfM1 = -2.928932188134524755992e-01 // Sqrt(2)/2-1 = 0xbfd2bec333018866
		Small       = 1.0 / (1 << 29)              // 2**-29 = 0x3e20000000000000
		Tiny        = 1.0 / (1 << 54)              // 2**-54
		Two53       = 1 << 53                      // 2**53
		Ln2Hi       = 6.93147180369123816490e-01   // 3fe62e42fee00000
		Ln2Lo       = 1.90821492927058770002e-10   // 3dea39ef35793c76
		Lp1         = 6.666666666666735130e-01     // 3FE5555555555593
		Lp2         = 3.999999999940941908e-01     // 3FD999999997FA04
		Lp3         = 2.857142874366239149e-01     // 3FD2492494229359
		Lp4         = 2.222219843214978396e-01     // 3FCC71C51D8E78AF
		Lp5         = 1.818357216161805012e-01     // 3FC7466496CB03DE
		Lp6         = 1.531383769920937332e-01     // 3FC39A09D078C69F
		Lp7         = 1.479819860511658591e-01     // 3FC2F112DF3E5244
	)

	// 特殊情况
	switch {
	case x < -1 || IsNaN(x): // includes -Inf
		return NaN()
	case x == -1:
		return Inf(-1)
	case IsInf(x, 1):
		return Inf(1)
	}

	absx := Abs(x)

	var f float64
	var iu uint64
	k := 1
	if absx < Sqrt2M1 { //  |x| < Sqrt(2)-1
		if absx < Small { // |x| < 2**-29
			if absx < Tiny { // |x| < 2**-54
				return x
			}
			return x - x*x*0.5
		}
		if x > Sqrt2HalfM1 { // Sqrt(2)/2-1 < x
			// (Sqrt(2)/2-1) < x < (Sqrt(2)-1)
			k = 0
			f = x
			iu = 1
		}
	}
	var c float64
	if k != 0 {
		var u float64
		if absx < Two53 { // 1<<53
			u = 1.0 + x
			iu = Float64bits(u)
			k = int((iu >> 52) - 1023)
			// 修正项
			if k > 0 {
				c = 1.0 - (u - x)
			} else {
				c = x - (u - 1.0)
			}
			c /= u
		} else {
			u = x
			iu = Float64bits(u)
			k = int((iu >> 52) - 1023)
			c = 0
		}
		iu &= 0x000fffffffffffff
		if iu < 0x0006a09e667f3bcd { // Sqrt(2) 的尾数
			u = Float64frombits(iu | 0x3ff0000000000000) // 规格化 u
		} else {
			k++
			u = Float64frombits(iu | 0x3fe0000000000000) // 规格化 u/2
			iu = (0x0010000000000000 - iu) >> 2
		}
		f = u - 1.0 // Sqrt(2)/2 < u < Sqrt(2)
	}
	hfsq := 0.5 * f * f
	var s, R, z float64
	if iu == 0 { // |f| < 2**-20
		if f == 0 {
			if k == 0 {
				return 0
			}
			c += float64(k) * Ln2Lo
			return float64(k)*Ln2Hi + c
		}
		R = hfsq * (1.0 - 0.66666666666666666*f) // 避免除法
		if k == 0 {
			return f - R
		}
		return float64(k)*Ln2Hi - ((R - (float64(k)*Ln2Lo + c)) - f)
	}
	s = f / (2.0 + f)
	z = s * s
	R = z * (Lp1 + z*(Lp2+z*(Lp3+z*(Lp4+z*(Lp5+z*(Lp6+z*Lp7))))))
	if k == 0 {
		return f - (hfsq - s*(hfsq+R))
	}
	return float64(k)*Ln2Hi - ((hfsq - (s*(hfsq+R) + (float64(k)*Ln2Lo + c))) - f)
}
