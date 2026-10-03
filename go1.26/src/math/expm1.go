// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 以下原始 C 代码、长篇注释及常量来自 FreeBSD 的
// /usr/src/lib/msun/src/s_expm1.c，并附带下述声明。
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
// expm1(x)
// 返回 exp(x)-1，即 x 的指数减 1。
//
// 方法
//   1. 参数归约：
//      给定 x，求 r 和整数 k，使得
//
//               x = k*ln2 + r,  |r| <= 0.5*ln2 ~ 0.34658
//
//      这里将计算一个修正项 c，以补偿
//      将 r 舍入为浮点数时的误差。
//
//   2. 用特殊有理函数逼近 expm1(r)，
//      区间为 [0,0.34658]：
//      由于
//          r*(exp(r)+1)/(exp(r)-1) = 2+ r**2/6 - r**4/360 + ...
//      我们定义 R1(r*r) 为
//          r*(exp(r)+1)/(exp(r)-1) = 2+ r**2/6 * R1(r*r)
//      也就是说，
//          R1(r**2) = 6/r *((exp(r)+1)/(exp(r)-1) - 2/r)
//                   = 6/r * ( 1 + 2.0*(1/(exp(r)-1) - 1/r))
//                   = 1 - r**2/60 + r**4/2520 - r**6/100800 + ...
//      我们在 [0,0.347] 上使用特殊的 Reme 算法生成
//      一个关于 r*r 的 5 次多项式来逼近 R1。该
//      多项式逼近的最大误差以
//      2**-61 为界。换言之，
//          R1(z) ~ 1.0 + Q1*z + Q2*z**2 + Q3*z**3 + Q4*z**4 + Q5*z**5
//      其中   Q1  =  -1.6666666666666567384E-2,
//              Q2  =   3.9682539681370365873E-4,
//              Q3  =  -9.9206344733435987357E-6,
//              Q4  =   2.5051361420808517002E-7,
//              Q5  =  -6.2843505682382617102E-9;
//      （其中 z=r*r，Q1 到 Q5 的值列出如下）
//      误差界为
//          |                  5           |     -61
//          | 1.0+Q1*z+...+Q5*z   -  R1(z) | <= 2
//          |                              |
//
//      expm1(r) = exp(r)-1 随后按以下
//      特定方式计算，以最小化累积舍入误差：
//                             2     3
//                            r     r    [ 3 - (R1 + R1*r/2)  ]
//            expm1(r) = r + --- + --- * [--------------------]
//                            2     2    [ 6 - r*(3 - R1*r/2) ]
//
//      为补偿参数归约中的误差，我们使用
//              expm1(r+c) = expm1(r) + c + expm1(r)*c
//                         ~ expm1(r) + c + r*c
//      因此，c+r*c 将作为 expm1(r+c) 的修正项
//      加入。现在重新排列各项以避免优化
//      搞乱：
//                      (      2                                    2 )
//                      ({  ( r    [ R1 -  (3 - R1*r/2) ]  )  }    r  )
//       expm1(r+c)~r - ({r*(--- * [--------------------]-c)-c} - --- )
//                      ({  ( 2    [ 6 - r*(3 - R1*r/2) ]  )  }    2  )
//                      (                                             )
//
//                 = r - E
//   3. 缩放回去以得到 expm1(x)：
//      由第 1 步，我们有
//         expm1(x) = either 2**k*[expm1(r)+1] - 1
//                  = or     2**k*[expm1(r) + (1-2**-k)]
//   4. 实现说明：
//      (A). 为节省一次乘法，我们将系数 Qi
//           缩放为 Qi*2**i，并将 z 替换为 (x**2)/2。
//      (B). 为达到最大精度，我们按以下方式计算 expm1(x)：
//        (i)   if x < -56*ln2, return -1.0, (raise inexact if x!=inf)
//        (ii)  if k=0, return r-E
//        (iii) if k=-1, return 0.5*(r-E)-0.5
//        (iv)  if k=1 if r < -0.25, return 2*((r+0.5)- E)
//                     else          return  1.0+2.0*(r-E);
//        (v)   if (k<-2||k>56) return 2**k(1-(E-r)) - 1 (or exp(x)-1)
//        (vi)  if k <= 20, return 2**k((1-2**-k)-(E-r)), else
//        (vii) return 2**k(1-((E+2**-k)-r))
//
// 特殊情况：
//      expm1(INF) 为 INF，expm1(NaN) 为 NaN；
//      expm1(-INF) 为 -1，并且
//      对于有限参数，只有 expm1(0)=0 是精确的。
//
// 精度：
//      根据误差分析，误差始终小于
//      1 ulp（单位末位）。
//
// 其他信息。
//      对于 IEEE double
//          若 x >  7.09782712893383973096e+02 则 expm1(x) 溢出
//
// 常量：
// 十六进制值是以下常量的预期取值。只要编译器
// 能将十进制准确转换为二进制，从而产生
// 所示的十六进制值，也可以使用十进制值。
//

// Expm1 返回 e**x - 1，即 x 的以 e 为底的指数减 1。
// 当 x 接近零时，它比 [Exp](x) - 1 更精确。
//
// 特殊情况为：
//
//	Expm1(+Inf) = +Inf
//	Expm1(-Inf) = -1
//	Expm1(NaN) = NaN
//
// 非常大的值会溢出为 -1 或 +Inf。
func Expm1(x float64) float64 {
	if haveArchExpm1 {
		return archExpm1(x)
	}
	return expm1(x)
}

func expm1(x float64) float64 {
	const (
		Othreshold = 7.09782712893383973096e+02 // 0x40862E42FEFA39EF
		Ln2X56     = 3.88162421113569373274e+01 // 0x4043687a9f1af2b1
		Ln2HalfX3  = 1.03972077083991796413e+00 // 0x3ff0a2b23f3bab73
		Ln2Half    = 3.46573590279972654709e-01 // 0x3fd62e42fefa39ef
		Ln2Hi      = 6.93147180369123816490e-01 // 0x3fe62e42fee00000
		Ln2Lo      = 1.90821492927058770002e-10 // 0x3dea39ef35793c76
		InvLn2     = 1.44269504088896338700e+00 // 0x3ff71547652b82fe
		Tiny       = 1.0 / (1 << 54)            // 2**-54 = 0x3c90000000000000
		// 与 expm1 相关的缩放系数
		Q1 = -3.33333333333331316428e-02 // 0xBFA11111111110F4
		Q2 = 1.58730158725481460165e-03  // 0x3F5A01A019FE5585
		Q3 = -7.93650757867487942473e-05 // 0xBF14CE199EAADBB7
		Q4 = 4.00821782732936239552e-06  // 0x3ED0CFCA86E65239
		Q5 = -2.01099218183624371326e-07 // 0xBE8AFDB76E09C32D
	)

	// 特殊情况
	switch {
	case IsInf(x, 1) || IsNaN(x):
		return x
	case IsInf(x, -1):
		return -1
	}

	absx := x
	sign := false
	if x < 0 {
		absx = -absx
		sign = true
	}

	// 过滤掉过大的参数
	if absx >= Ln2X56 { // 若 |x| >= 56 * ln2
		if sign {
			return -1 // x < -56*ln2，返回 -1
		}
		if absx >= Othreshold { // 若 |x| >= 709.78...
			return Inf(1)
		}
	}

	// 参数归约
	var c float64
	var k int
	if absx > Ln2Half { // if  |x| > 0.5 * ln2
		var hi, lo float64
		if absx < Ln2HalfX3 { // and |x| < 1.5 * ln2
			if !sign {
				hi = x - Ln2Hi
				lo = Ln2Lo
				k = 1
			} else {
				hi = x + Ln2Hi
				lo = -Ln2Lo
				k = -1
			}
		} else {
			if !sign {
				k = int(InvLn2*x + 0.5)
			} else {
				k = int(InvLn2*x - 0.5)
			}
			t := float64(k)
			hi = x - t*Ln2Hi // 此处 t * Ln2Hi 是精确的
			lo = t * Ln2Lo
		}
		x = hi - lo
		c = (hi - x) - lo
	} else if absx < Tiny { // 当 |x| < 2**-54 时，返回 x
		return x
	} else {
		k = 0
	}

	// x 现在位于主区间内
	hfx := 0.5 * x
	hxs := x * hfx
	r1 := 1 + hxs*(Q1+hxs*(Q2+hxs*(Q3+hxs*(Q4+hxs*Q5))))
	t := 3 - r1*hfx
	e := hxs * ((r1 - t) / (6.0 - x*t))
	if k == 0 {
		return x - (x*e - hxs) // c 为 0
	}
	e = (x*(e-c) - c)
	e -= hxs
	switch {
	case k == -1:
		return 0.5*(x-e) - 0.5
	case k == 1:
		if x < -0.25 {
			return -2 * (e - (x + 0.5))
		}
		return 1 + 2*(x-e)
	case k <= -2 || k > 56: // 返回 exp(x)-1 即可
		y := 1 - (e - x)
		y = Float64frombits(Float64bits(y) + uint64(k)<<52) // 将 k 加到 y 的指数上
		return y - 1
	}
	if k < 20 {
		t := Float64frombits(0x3ff0000000000000 - (0x20000000000000 >> uint(k))) // t=1-2**-k
		y := t - (e - x)
		y = Float64frombits(Float64bits(y) + uint64(k)<<52) // 将 k 加到 y 的指数上
		return y
	}
	t = Float64frombits(uint64(0x3ff-k) << 52) // 2**-k
	y := x - (e + t)
	y++
	y = Float64frombits(Float64bits(y) + uint64(k)<<52) // 将 k 加到 y 的指数上
	return y
}
