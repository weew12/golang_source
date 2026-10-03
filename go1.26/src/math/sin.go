// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	浮点正弦和余弦。
*/

// 原始 C 代码、长注释及下面的常量
// 来自 http://netlib.sandia.gov/cephes/cmath/sin.c，
// 可从 http://www.netlib.org/cephes/cmath.tgz 获取。
// Go 代码是原始 C 的简化版本。
//
//      sin.c
//
//      圆正弦
//
// 概要：
//
// double x, y, sin();
// y = sin( x );
//
// 描述：
//
// 参数归约到 pi/4 的区间内。几乎
// 通过构造扩展精度的模运算来消除归约误差。
//
// 使用了两个多项式逼近函数。
// 在 0 到 pi/4 之间，正弦由下式逼近
//      x  +  x**3 P(x**2).
// 在 pi/4 到 pi/2 之间，余弦表示为
//      1  -  x**2 Q(x**2).
//
// 精度：
//
//                      相对误差：
// arithmetic   domain      # trials      peak         rms
//    DEC       0, 10       150000       3.0e-17     7.8e-18
//    IEEE -1.07e9,+1.07e9  130000       2.1e-16     5.4e-17
//
// 精度的部分损失开始出现在 x = 2**30 = 1.074e9 处。这种损失
// 不是渐进的，而是突然跃升到约 10e7 分之一。对于
// x > 2**49 = 5.6e14，结果可能没有意义。
//
//      cos.c
//
//      圆余弦
//
// 概要：
//
// double x, y, cos();
// y = cos( x );
//
// 描述：
//
// 参数归约到 pi/4 的区间内。几乎
// 通过构造扩展精度的模运算来消除归约误差。
//
// 使用了两个多项式逼近函数。
// 在 0 到 pi/4 之间，余弦由下式逼近
//      1  -  x**2 Q(x**2).
// 在 pi/4 到 pi/2 之间，正弦表示为
//      x  +  x**3 P(x**2).
//
// 精度：
//
//                      相对误差：
// arithmetic   domain      # trials      peak         rms
//    IEEE -1.07e9,+1.07e9  130000       2.1e-16     5.4e-17
//    DEC        0,+1.07e9   17000       3.0e-17     7.2e-18
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

// sin 系数
var _sin = [...]float64{
	1.58962301576546568060e-10, // 0x3de5d8fd1fd19ccd
	-2.50507477628578072866e-8, // 0xbe5ae5e5a9291f5d
	2.75573136213857245213e-6,  // 0x3ec71de3567d48a1
	-1.98412698295895385996e-4, // 0xbf2a01a019bfdf03
	8.33333333332211858878e-3,  // 0x3f8111111110f7d0
	-1.66666666666666307295e-1, // 0xbfc5555555555548
}

// cos 系数
var _cos = [...]float64{
	-1.13585365213876817300e-11, // 0xbda8fa49a0861a9b
	2.08757008419747316778e-9,   // 0x3e21ee9d7b4e3f05
	-2.75573141792967388112e-7,  // 0xbe927e4f7eac4bc6
	2.48015872888517045348e-5,   // 0x3efa01a019c844f5
	-1.38888888888730564116e-3,  // 0xbf56c16c16c14f91
	4.16666666666665929218e-2,   // 0x3fa555555555554b
}

// Cos 返回弧度参数 x 的余弦。
//
// 特殊情况为：
//
//	Cos(±Inf) = NaN
//	Cos(NaN) = NaN
func Cos(x float64) float64 {
	if haveArchCos {
		return archCos(x)
	}
	return cos(x)
}

func cos(x float64) float64 {
	const (
		PI4A = 7.85398125648498535156e-1  // 0x3fe921fb40000000, Pi/4 拆分为三部分
		PI4B = 3.77489470793079817668e-8  // 0x3e64442d00000000,
		PI4C = 2.69515142907905952645e-15 // 0x3ce8469898cc5170,
	)
	// 特殊情况
	switch {
	case IsNaN(x) || IsInf(x, 0):
		return NaN()
	}

	// 使参数为正
	sign := false
	x = Abs(x)

	var j uint64
	var y, z float64
	if x >= reduceThreshold {
		j, z = trigReduce(x)
	} else {
		j = uint64(x * (4 / Pi)) // x/(Pi/4) 的整数部分，作为整数用于相位角测试
		y = float64(j)           // x/(Pi/4) 的整数部分，作为浮点数

		// 将零点映射到原点
		if j&1 == 1 {
			j++
			y++
		}
		j &= 7                               // 对 2Pi 弧度（360 度）取模的八分圆
		z = ((x - y*PI4A) - y*PI4B) - y*PI4C // 扩展精度模运算
	}

	if j > 3 {
		j -= 4
		sign = !sign
	}
	if j > 1 {
		sign = !sign
	}

	zz := z * z
	if j == 1 || j == 2 {
		y = z + z*zz*((((((_sin[0]*zz)+_sin[1])*zz+_sin[2])*zz+_sin[3])*zz+_sin[4])*zz+_sin[5])
	} else {
		y = 1.0 - 0.5*zz + zz*zz*((((((_cos[0]*zz)+_cos[1])*zz+_cos[2])*zz+_cos[3])*zz+_cos[4])*zz+_cos[5])
	}
	if sign {
		y = -y
	}
	return y
}

// Sin 返回弧度参数 x 的正弦。
//
// 特殊情况为：
//
//	Sin(±0) = ±0
//	Sin(±Inf) = NaN
//	Sin(NaN) = NaN
func Sin(x float64) float64 {
	if haveArchSin {
		return archSin(x)
	}
	return sin(x)
}

func sin(x float64) float64 {
	const (
		PI4A = 7.85398125648498535156e-1  // 0x3fe921fb40000000, Pi/4 拆分为三部分
		PI4B = 3.77489470793079817668e-8  // 0x3e64442d00000000,
		PI4C = 2.69515142907905952645e-15 // 0x3ce8469898cc5170,
	)
	// 特殊情况
	switch {
	case x == 0 || IsNaN(x):
		return x // 返回 ±0 || NaN()
	case IsInf(x, 0):
		return NaN()
	}

	// 使参数为正但保留符号
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	var j uint64
	var y, z float64
	if x >= reduceThreshold {
		j, z = trigReduce(x)
	} else {
		j = uint64(x * (4 / Pi)) // x/(Pi/4) 的整数部分，作为整数用于相位角测试
		y = float64(j)           // x/(Pi/4) 的整数部分，作为浮点数

		// 将零点映射到原点
		if j&1 == 1 {
			j++
			y++
		}
		j &= 7                               // 对 2Pi 弧度（360 度）取模的八分圆
		z = ((x - y*PI4A) - y*PI4B) - y*PI4C // 扩展精度模运算
	}
	// 关于 x 轴反射
	if j > 3 {
		sign = !sign
		j -= 4
	}
	zz := z * z
	if j == 1 || j == 2 {
		y = 1.0 - 0.5*zz + zz*zz*((((((_cos[0]*zz)+_cos[1])*zz+_cos[2])*zz+_cos[3])*zz+_cos[4])*zz+_cos[5])
	} else {
		y = z + z*zz*((((((_sin[0]*zz)+_sin[1])*zz+_sin[2])*zz+_sin[3])*zz+_sin[4])*zz+_sin[5])
	}
	if sign {
		y = -y
	}
	return y
}
