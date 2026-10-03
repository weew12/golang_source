// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmplx

import "math"

// 原始 C 代码、长注释以及下面的常量
// 来自 http://netlib.sandia.gov/cephes/c9x-complex/clog.c。
// Go 代码是原始 C 代码的简化版本。
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// http://netlib.sandia.gov/cephes/ 处的 readme 文件写道：
//    本档案中的某些软件可能来自 _Methods and
// Programs for Mathematical Functions_ 一书（Prentice-Hall 或 Simon & Schuster
// International，1989），或者来自 Cephes 数学库，一个
// 商业产品。无论哪种情况，其版权均归作者所有。
// 你可以自由使用在此看到的内容，但对此不提供任何支持或
// 担保。
//
//   书中已知的两处印刷错误已在此处修正，
// 涉及 gamma 函数和不完全 beta 积分的源代码清单。
//
//   Stephen L. Moshier
//   moshier@na-net.ornl.gov

// 复数自然对数
//
// 描述：
//
// 返回复数参数 z 的以 e（2.718...）为底的对数。
//
// 如果
//       z = x + iy, r = sqrt( x**2 + y**2 ),
// 那么
//       w = log(r) + i arctan(y/x).
//
// 反正切的取值范围为 -PI 到 +PI。
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10      7000       8.5e-17     1.9e-17
//    IEEE      -10,+10     30000       5.0e-15     1.1e-16
//
// 当 z 接近 1 +i0 时可以观察到更大的相对误差。
// 在 IEEE 运算中，峰值绝对误差为 5.2e-16，均方根
// 绝对误差为 1.0e-16。

// Log 返回 x 的自然对数。
func Log(x complex128) complex128 {
	return complex(math.Log(Abs(x)), Phase(x))
}

// Log10 返回 x 的十进制对数。
func Log10(x complex128) complex128 {
	z := Log(x)
	return complex(math.Log10E*real(z), math.Log10E*imag(z))
}
