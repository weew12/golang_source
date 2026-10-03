// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	浮点正切。
*/

// 原始 C 代码、长注释及下面的常量
// 来自 http://netlib.sandia.gov/cephes/cmath/sin.c，
// 可从 http://www.netlib.org/cephes/cmath.tgz 获取。
// Go 代码是原始 C 的简化版本。
//
//      tan.c
//
//      圆正切
//
// 概要：
//
// double x, y, tan();
// y = tan( x );
//
// 描述：
//
// 返回弧度参数 x 的圆正切。
//
// 参数归约为 pi/4 的模。在基本区间 [0, pi/4] 中
//       x + x**3 P(x**2)/Q(x**2)
// 使用有理函数
//
// 精度：
//                      相对误差：
// arithmetic   domain     # trials      peak         rms
//    DEC      +-1.07e9      44000      4.1e-17     1.0e-17
//    IEEE     +-1.07e9      30000      2.9e-16     8.1e-17
//
// 精度的部分损失开始出现在 x = 2**30 = 1.074e9 处。这种损失
// 不是渐进的，而是突然跃升到约 10e7 分之一。对于
// x > 2**49 = 5.6e14，结果可能没有意义。
// [精度损失说明取自 sin.go 的注释。]
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

// tan 系数
var _tanP = [...]float64{
	-1.30936939181383777646e4, // 0xc0c992d8d24f3f38
	1.15351664838587416140e6,  // 0x413199eca5fc9ddd
	-1.79565251976484877988e7, // 0xc1711fead3299176
}
var _tanQ = [...]float64{
	1.00000000000000000000e0,
	1.36812963470692954678e4,  // 0x40cab8a5eeb36572
	-1.32089234440210967447e6, // 0xc13427bc582abc96
	2.50083801823357915839e7,  // 0x4177d98fc2ead8ef
	-5.38695755929454629881e7, // 0xc189afe03cbe5a31
}

// Tan 返回弧度参数 x 的正切。
//
// 特殊情况为：
//
//	Tan(±0) = ±0
//	Tan(±Inf) = NaN
//	Tan(NaN) = NaN
func Tan(x float64) float64 {
	if haveArchTan {
		return archTan(x)
	}
	return tan(x)
}

func tan(x float64) float64 {
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

		/* 将零点和奇点映射到原点 */
		if j&1 == 1 {
			j++
			y++
		}

		z = ((x - y*PI4A) - y*PI4B) - y*PI4C
	}
	zz := z * z

	if zz > 1e-14 {
		y = z + z*(zz*(((_tanP[0]*zz)+_tanP[1])*zz+_tanP[2])/((((zz+_tanQ[1])*zz+_tanQ[2])*zz+_tanQ[3])*zz+_tanQ[4]))
	} else {
		y = z
	}
	if j&2 == 2 {
		y = -1 / y
	}
	if sign {
		y = -y
	}
	return y
}
