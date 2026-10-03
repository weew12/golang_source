// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	浮点反正切。
*/

// 以下原始 C 代码、长篇注释及常量来自
// http://netlib.sandia.gov/cephes/cmath/atan.c，可从
// http://www.netlib.org/cephes/cmath.tgz 获取。
// Go 代码是原始 C 代码的一个版本。
//
// atan.c
// 反正切（arctangent）
//
// 概要：
// double x, y, atan();
// y = atan( x );
//
// 描述：
// 返回正切值为 x 的弧度角，其范围在 -pi/2 与 +pi/2 之间。
//
// 范围缩减将三个区间归约到从 0 到 0.66 的区间。
// 近似式使用形式为 x + x**3 P(x)/Q(x) 的 4/5 次有理函数。
//
// 精度：
//                      相对误差：
// arithmetic   domain    # trials  peak     rms
//    DEC       -10, 10   50000     2.4e-17  8.3e-18
//    IEEE      -10, 10   10^6      1.8e-16  5.0e-17
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

// xatan 计算在区间 [0, 0.66] 内有效的级数。
func xatan(x float64) float64 {
	const (
		P0 = -8.750608600031904122785e-01
		P1 = -1.615753718733365076637e+01
		P2 = -7.500855792314704667340e+01
		P3 = -1.228866684490136173410e+02
		P4 = -6.485021904942025371773e+01
		Q0 = +2.485846490142306297962e+01
		Q1 = +1.650270098316988542046e+02
		Q2 = +4.328810604912902668951e+02
		Q3 = +4.853903996359136964868e+02
		Q4 = +1.945506571482613964425e+02
	)
	z := x * x
	z = z * ((((P0*z+P1)*z+P2)*z+P3)*z + P4) / (((((z+Q0)*z+Q1)*z+Q2)*z+Q3)*z + Q4)
	z = x*z + x
	return z
}

// satan 将其参数（已知为正）归约到区间
// [0, 0.66]，并调用 xatan。
func satan(x float64) float64 {
	const (
		Morebits = 6.123233995736765886130e-17 // pi/2 = PIO2 + Morebits
		Tan3pio8 = 2.41421356237309504880      // tan(3*pi/8)
	)
	if x <= 0.66 {
		return xatan(x)
	}
	if x > Tan3pio8 {
		return Pi/2 - xatan(1/x) + Morebits
	}
	return Pi/4 + xatan((x-1)/(x+1)) + 0.5*Morebits
}

// Atan 返回 x 的反正切（以弧度为单位）。
//
// 特殊情况为：
//
//	Atan(±0) = ±0
//	Atan(±Inf) = ±Pi/2
func Atan(x float64) float64 {
	if haveArchAtan {
		return archAtan(x)
	}
	return atan(x)
}

func atan(x float64) float64 {
	if x == 0 {
		return x
	}
	if x > 0 {
		return satan(x)
	}
	return -satan(-x)
}
