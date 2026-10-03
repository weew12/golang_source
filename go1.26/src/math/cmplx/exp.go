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

// 复数指数函数
//
// 描述：
//
// 返回复数参数 z 的复指数。
//
// 如果
//     z = x + iy,
//     r = exp(x),
// 那么
//     w = r cos y + i r sin y.
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10      8700       3.7e-17     1.1e-17
//    IEEE      -10,+10     30000       3.0e-16     8.7e-17

// Exp 返回 e**x，即 x 的以 e 为底的指数。
func Exp(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case math.IsInf(re, 0):
		switch {
		case re > 0 && im == 0:
			return x
		case math.IsInf(im, 0) || math.IsNaN(im):
			if re < 0 {
				return complex(0, math.Copysign(0, im))
			} else {
				return complex(math.Inf(1.0), math.NaN())
			}
		}
	case math.IsNaN(re):
		if im == 0 {
			return complex(math.NaN(), im)
		}
	}
	r := math.Exp(real(x))
	s, c := math.Sincos(imag(x))
	return complex(r*c, r*s)
}
