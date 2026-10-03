// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 原始 C 代码、长注释以及下面的常量
// 来自 http://netlib.sandia.gov/cephes/cprob/gamma.c。
// Go 代码是原始 C 代码的简化版本。
//
//      tgamma.c
//
//      伽玛函数
//
// 概要：
//
// double x, y, tgamma();
// extern int signgam;
//
// y = tgamma( x );
//
// 描述：

// 返回参数的伽玛函数。结果带有正确的符号，
// 且符号（+1 或 -1）也会通过一个名为 signgam
// 的全局（extern）变量返回。
// 该变量也会由对数伽玛函数 lgamma() 填充。
//
// |x| <= 34 的参数通过递推进行归约，函数在
// 区间 (2,3) 上用 6/7 次有理函数逼近。
// 较大的参数由 Stirling 公式处理。
// 较大的负参数利用反射公式转换为正数。
//
// 精度：
//
//                      Relative error:
// arithmetic   domain     # trials      peak         rms
//    DEC      -34, 34      10000       1.3e-16     2.5e-17
//    IEEE    -170,-33      20000       2.3e-15     3.3e-16
//    IEEE     -33,  33     20000       9.4e-16     2.2e-16
//    IEEE      33, 171.6   20000       2.3e-15     3.2e-16
//
// 测试范围之外的参数误差会更大，
// 这是由于指数函数对误差的放大。
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// The readme file at http://netlib.sandia.gov/cephes/ says:
//    Some software in this archive may be from the book _Methods and
// Programs for Mathematical Functions_ (Prentice-Hall or Simon & Schuster
// International, 1989) or from the Cephes Mathematical Library, a
// commercial product. In either event, it is copyrighted by the author.
// What you see here may be used freely but it comes with no support or
// guarantee.
//
//   The two known misprints in the book are repaired here in the
// source listings for the gamma function and the incomplete beta
// integral.
//
//   Stephen L. Moshier
//   moshier@na-net.ornl.gov

var _gamP = [...]float64{
	1.60119522476751861407e-04,
	1.19135147006586384913e-03,
	1.04213797561761569935e-02,
	4.76367800457137231464e-02,
	2.07448227648435975150e-01,
	4.94214826801497100753e-01,
	9.99999999999999996796e-01,
}
var _gamQ = [...]float64{
	-2.31581873324120129819e-05,
	5.39605580493303397842e-04,
	-4.45641913851797240494e-03,
	1.18139785222060435552e-02,
	3.58236398605498653373e-02,
	-2.34591795718243348568e-01,
	7.14304917030273074085e-02,
	1.00000000000000000320e+00,
}
var _gamS = [...]float64{
	7.87311395793093628397e-04,
	-2.29549961613378126380e-04,
	-2.68132617805781232825e-03,
	3.47222221605458667310e-03,
	8.33333333333482257126e-02,
}

// 由 Stirling 公式计算的伽玛函数。
// 两个结果必须相乘才能得到实际答案。
// 乘法留给调用者，这样如果调用者足够小心，就可以避免
// 在 172 <= x <= 180 时出现无穷。
// 该多项式对 33 <= x <= 172 有效；更大的值仅用于
// 倒数，并产生非规格化浮点数。那里的较低精度
// 掩盖了多项式中的任何不精确。
func stirling(x float64) (float64, float64) {
	if x > 200 {
		return Inf(1), 1
	}
	const (
		SqrtTwoPi   = 2.506628274631000502417
		MaxStirling = 143.01608
	)
	w := 1 / x
	w = 1 + w*((((_gamS[0]*w+_gamS[1])*w+_gamS[2])*w+_gamS[3])*w+_gamS[4])
	y1 := Exp(x)
	y2 := 1.0
	if x > MaxStirling { // 避免 Pow() 溢出
		v := Pow(x, 0.5*x-0.25)
		y1, y2 = v, v/y1
	} else {
		y1 = Pow(x, x-0.5) / y1
	}
	return y1, SqrtTwoPi * w * y2
}

// Gamma 返回 x 的 Gamma 函数。
//
// 特殊情况为：
//
//	Gamma(+Inf) = +Inf
//	Gamma(+0) = +Inf
//	Gamma(-0) = -Inf
//	Gamma(x) = NaN for integer x < 0
//	Gamma(-Inf) = NaN
//	Gamma(NaN) = NaN
func Gamma(x float64) float64 {
	const Euler = 0.57721566490153286060651209008240243104215933593992 // A001620
	// 特殊情况
	switch {
	case isNegInt(x) || IsInf(x, -1) || IsNaN(x):
		return NaN()
	case IsInf(x, 1):
		return Inf(1)
	case x == 0:
		if Signbit(x) {
			return Inf(-1)
		}
		return Inf(1)
	}
	q := Abs(x)
	p := Floor(q)
	if q > 33 {
		if x >= 0 {
			y1, y2 := stirling(x)
			return y1 * y2
		}
		// 注意：x 为负数，但（上面已检查）不是负整数，
		// 因此 x 必须足够小，以便能转换为 int64。
		// 如果 |x| >= 2⁶³，则它必定是整数。
		signgam := 1
		if ip := int64(p); ip&1 == 0 {
			signgam = -1
		}
		z := q - p
		if z > 0.5 {
			p = p + 1
			z = q - p
		}
		z = q * Sin(Pi*z)
		if z == 0 {
			return Inf(signgam)
		}
		sq1, sq2 := stirling(q)
		absz := Abs(z)
		d := absz * sq1 * sq2
		if IsInf(d, 0) {
			z = Pi / absz / sq1 / sq2
		} else {
			z = Pi / d
		}
		return float64(signgam) * z
	}

	// 归约参数
	z := 1.0
	for x >= 3 {
		x = x - 1
		z = z * x
	}
	for x < 0 {
		if x > -1e-09 {
			goto small
		}
		z = z / x
		x = x + 1
	}
	for x < 2 {
		if x < 1e-09 {
			goto small
		}
		z = z / x
		x = x + 1
	}

	if x == 2 {
		return z
	}

	x = x - 2
	p = (((((x*_gamP[0]+_gamP[1])*x+_gamP[2])*x+_gamP[3])*x+_gamP[4])*x+_gamP[5])*x + _gamP[6]
	q = ((((((x*_gamQ[0]+_gamQ[1])*x+_gamQ[2])*x+_gamQ[3])*x+_gamQ[4])*x+_gamQ[5])*x+_gamQ[6])*x + _gamQ[7]
	return z * p / q

small:
	if x == 0 {
		return Inf(1)
	}
	return z / ((1 + Euler*x) * x)
}

func isNegInt(x float64) bool {
	if x < 0 {
		_, xf := Modf(x)
		return xf == 0
	}
	return false
}
