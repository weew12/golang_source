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

// 复数正弦
//
// 描述：
//
// 如果
//     z = x + iy,
//
// 那么
//
//     w = sin x  cosh y  +  i cos x sinh y.
//
// csin(z) = -i csinh(iz).
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10      8400       5.3e-17     1.3e-17
//    IEEE      -10,+10     30000       3.8e-16     1.0e-16
// 也通过 csin(casin(z)) = z 进行了测试。

// Sin 返回 x 的正弦。
func Sin(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case im == 0 && (math.IsInf(re, 0) || math.IsNaN(re)):
		return complex(math.NaN(), im)
	case math.IsInf(im, 0):
		switch {
		case re == 0:
			return x
		case math.IsInf(re, 0) || math.IsNaN(re):
			return complex(math.NaN(), im)
		}
	case re == 0 && math.IsNaN(im):
		return x
	}
	s, c := math.Sincos(real(x))
	sh, ch := sinhcosh(imag(x))
	return complex(s*ch, c*sh)
}

// 复数双曲正弦
//
// 描述：
//
// csinh z = (cexp(z) - cexp(-z))/2
//         = sinh x * cos y  +  i cosh x * sin y .
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    IEEE      -10,+10     30000       3.1e-16     8.2e-17

// Sinh 返回 x 的双曲正弦。
func Sinh(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case re == 0 && (math.IsInf(im, 0) || math.IsNaN(im)):
		return complex(re, math.NaN())
	case math.IsInf(re, 0):
		switch {
		case im == 0:
			return complex(re, im)
		case math.IsInf(im, 0) || math.IsNaN(im):
			return complex(re, math.NaN())
		}
	case im == 0 && math.IsNaN(re):
		return complex(math.NaN(), im)
	}
	s, c := math.Sincos(imag(x))
	sh, ch := sinhcosh(real(x))
	return complex(c*sh, s*ch)
}

// 复数余弦
//
// 描述：
//
// 如果
//     z = x + iy,
//
// 那么
//
//     w = cos x  cosh y  -  i sin x sinh y.
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10      8400       4.5e-17     1.3e-17
//    IEEE      -10,+10     30000       3.8e-16     1.0e-16

// Cos 返回 x 的余弦。
func Cos(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case im == 0 && (math.IsInf(re, 0) || math.IsNaN(re)):
		return complex(math.NaN(), -im*math.Copysign(0, re))
	case math.IsInf(im, 0):
		switch {
		case re == 0:
			return complex(math.Inf(1), -re*math.Copysign(0, im))
		case math.IsInf(re, 0) || math.IsNaN(re):
			return complex(math.Inf(1), math.NaN())
		}
	case re == 0 && math.IsNaN(im):
		return complex(math.NaN(), 0)
	}
	s, c := math.Sincos(real(x))
	sh, ch := sinhcosh(imag(x))
	return complex(c*ch, -s*sh)
}

// 复数双曲余弦
//
// 描述：
//
// ccosh(z) = cosh x  cos y + i sinh x sin y .
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    IEEE      -10,+10     30000       2.9e-16     8.1e-17

// Cosh 返回 x 的双曲余弦。
func Cosh(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case re == 0 && (math.IsInf(im, 0) || math.IsNaN(im)):
		return complex(math.NaN(), re*math.Copysign(0, im))
	case math.IsInf(re, 0):
		switch {
		case im == 0:
			return complex(math.Inf(1), im*math.Copysign(0, re))
		case math.IsInf(im, 0) || math.IsNaN(im):
			return complex(math.Inf(1), math.NaN())
		}
	case im == 0 && math.IsNaN(re):
		return complex(math.NaN(), im)
	}
	s, c := math.Sincos(imag(x))
	sh, ch := sinhcosh(real(x))
	return complex(c*ch, s*sh)
}

// 计算 sinh 和 cosh。
func sinhcosh(x float64) (sh, ch float64) {
	if math.Abs(x) <= 0.5 {
		return math.Sinh(x), math.Cosh(x)
	}
	e := math.Exp(x)
	ei := 0.5 / e
	e *= 0.5
	return e - ei, e + ei
}
