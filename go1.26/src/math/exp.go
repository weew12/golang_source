// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Exp 返回 e**x，即 x 的以 e 为底的指数。
//
// 特殊情况为：
//
//	Exp(+Inf) = +Inf
//	Exp(NaN) = NaN
//
// 非常大的值会溢出为 0 或 +Inf。
// 非常小的值会下溢为 1。
func Exp(x float64) float64 {
	if haveArchExp {
		return archExp(x)
	}
	return exp(x)
}

// 以下原始 C 代码、长篇注释及常量来自 FreeBSD 的
// /usr/src/lib/msun/src/e_exp.c，并附带下述声明。
// Go 代码是原始 C 代码的简化版本。
//
// ====================================================
// Copyright (C) 2004 by Sun Microsystems, Inc. All rights reserved.
//
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
//
// exp(x)
// 返回 x 的指数。
//
// 方法
//   1. 参数归约：
//      将 x 归约为 r，使得 |r| <= 0.5*ln2 ~ 0.34658。
//      给定 x，求 r 和整数 k，使得
//
//               x = k*ln2 + r,  |r| <= 0.5*ln2.
//
//      这里 r 将表示为 r = hi-lo，以获得更好的
//      精度。
//
//   2. 用特殊有理函数逼近 exp(r)，
//      区间为 [0,0.34658]：
//      记作
//          R(r**2) = r*(exp(r)+1)/(exp(r)-1) = 2 + r*r/6 - r**4/360 + ...
//      我们在 [0,0.34658] 上使用特殊的 Remez 算法生成
//      一个 5 次多项式来逼近 R。该多项式
//      逼近的最大误差以 2**-59 为界。换
//      言之，
//          R(z) ~ 2.0 + P1*z + P2*z**2 + P3*z**3 + P4*z**4 + P5*z**5
//      （其中 z=r*r，P1 到 P5 的值列出如下）
//      并且
//          |                  5          |     -59
//          | 2.0+P1*z+...+P5*z   -  R(z) | <= 2
//          |                             |
//      exp(r) 的计算因此变为
//                             2*r
//              exp(r) = 1 + -------
//                            R - r
//                                 r*R1(r)
//                     = 1 + r + ----------- （以获得更好的精度）
//                                2 - R1(r)
//      其中
//                               2       4             10
//              R1(r) = r - (P1*r  + P2*r  + ... + P5*r   ).
//
//   3. 缩放回去以得到 exp(x)：
//      由第 1 步，我们有
//         exp(x) = 2**k * exp(r)
//
// 特殊情况：
//      exp(INF) 为 INF，exp(NaN) 为 NaN；
//      exp(-INF) 为 0，并且
//      对于有限参数，只有 exp(0)=1 是精确的。
//
// 精度：
//      根据误差分析，误差始终小于
//      1 ulp（单位末位）。
//
// 其他信息。
//      对于 IEEE double
//          若 x >  7.09782712893383973096e+02 则 exp(x) 溢出
//          若 x < -7.45133219101941108420e+02 则 exp(x) 下溢
//
// 常量：
// 十六进制值是以下常量的预期取值。只要编译器
// 能将十进制准确转换为二进制，从而产生
// 所示的十六进制值，也可以使用十进制值。

func exp(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10
		Log2e = 1.44269504088896338700e+00

		Overflow  = 7.09782712893383973096e+02
		Underflow = -7.45133219101941108420e+02
		NearZero  = 1.0 / (1 << 28) // 2**-28
	)

	// 特殊情况
	switch {
	case IsNaN(x):
		return x
	case x > Overflow: // 处理 x 为 +∞ 的情况
		return Inf(1)
	case x < Underflow: // 处理 x 为 -∞ 的情况
		return 0
	case -NearZero < x && x < NearZero:
		return 1 + x
	}

	// 归约；为获得额外精度按 r = hi - lo 计算。
	var k int
	switch {
	case x < 0:
		k = int(Log2e*x - 0.5)
	case x > 0:
		k = int(Log2e*x + 0.5)
	}
	hi := x - float64(k)*Ln2Hi
	lo := float64(k) * Ln2Lo

	// compute
	return expmulti(hi, lo, k)
}

// Exp2 返回 2**x，即 x 的以 2 为底的指数。
//
// 特殊情况与 [Exp] 相同。
func Exp2(x float64) float64 {
	if haveArchExp2 {
		return archExp2(x)
	}
	return exp2(x)
}

func exp2(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10

		Overflow  = 1.0239999999999999e+03
		Underflow = -1.0740e+03
	)

	// 特殊情况
	switch {
	case IsNaN(x):
		return x
	case x > Overflow: // 处理 x 为 +∞ 的情况
		return Inf(1)
	case x < Underflow: // 处理 x 为 -∞ 的情况
		return 0
	}

	// 参数归约；x = r×lg(e) + k，其中 |r| ≤ ln(2)/2。
	// 为获得额外精度按 r = hi - lo 计算。
	var k int
	switch {
	case x > 0:
		k = int(x + 0.5)
	case x < 0:
		k = int(x - 0.5)
	}
	t := x - float64(k)
	hi := t * Ln2Hi
	lo := -t * Ln2Lo

	// compute
	return expmulti(hi, lo, k)
}

// exp1 返回 e**r × 2**k，其中 r = hi - lo 且 |r| ≤ ln(2)/2。
func expmulti(hi, lo float64, k int) float64 {
	const (
		P1 = 1.66666666666666657415e-01  /* 0x3FC55555; 0x55555555 */
		P2 = -2.77777777770155933842e-03 /* 0xBF66C16C; 0x16BEBD93 */
		P3 = 6.61375632143793436117e-05  /* 0x3F11566A; 0xAF25DE2C */
		P4 = -1.65339022054652515390e-06 /* 0xBEBBBD41; 0xC5D26BF1 */
		P5 = 4.13813679705723846039e-08  /* 0x3E663769; 0x72BEA4D0 */
	)

	r := hi - lo
	t := r * r
	c := r - t*(P1+t*(P2+t*(P3+t*(P4+t*P5))))
	y := 1 - ((lo - (r*c)/(2-c)) - hi)
	// TODO(rsc)：确保 Ldexp 能处理边界 k
	return Ldexp(y, k)
}
