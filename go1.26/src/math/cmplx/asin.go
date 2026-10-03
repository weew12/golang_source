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

// 复数反正弦
//
// 描述：
//
// 复数正弦的反函数：
//                               2
// w = -i clog( iz + csqrt( 1 - z ) ).
//
// casin(z) = -i casinh(iz)
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10     10100       2.1e-15     3.4e-16
//    IEEE      -10,+10     30000       2.2e-14     2.7e-15
// 当 z 接近零时可以观察到更大的相对误差。
// 也通过 csin(casin(z)) = z 进行了测试。

// Asin 返回 x 的反正弦。
func Asin(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case im == 0 && math.Abs(re) <= 1:
		return complex(math.Asin(re), im)
	case re == 0 && math.Abs(im) <= 1:
		return complex(re, math.Asinh(im))
	case math.IsNaN(im):
		switch {
		case re == 0:
			return complex(re, math.NaN())
		case math.IsInf(re, 0):
			return complex(math.NaN(), re)
		default:
			return NaN()
		}
	case math.IsInf(im, 0):
		switch {
		case math.IsNaN(re):
			return x
		case math.IsInf(re, 0):
			return complex(math.Copysign(math.Pi/4, re), im)
		default:
			return complex(math.Copysign(0, re), im)
		}
	case math.IsInf(re, 0):
		return complex(math.Copysign(math.Pi/2, re), math.Copysign(re, im))
	}
	ct := complex(-imag(x), real(x)) // i * x
	xx := x * x
	x1 := complex(1-real(xx), -imag(xx)) // 1 - x*x
	x2 := Sqrt(x1)                       // x2 = sqrt(1 - x*x)
	w := Log(ct + x2)
	return complex(imag(w), -real(w)) // -i * w
}

// Asinh 返回 x 的反双曲正弦。
func Asinh(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case im == 0 && math.Abs(re) <= 1:
		return complex(math.Asinh(re), im)
	case re == 0 && math.Abs(im) <= 1:
		return complex(re, math.Asin(im))
	case math.IsInf(re, 0):
		switch {
		case math.IsInf(im, 0):
			return complex(re, math.Copysign(math.Pi/4, im))
		case math.IsNaN(im):
			return x
		default:
			return complex(re, math.Copysign(0.0, im))
		}
	case math.IsNaN(re):
		switch {
		case im == 0:
			return x
		case math.IsInf(im, 0):
			return complex(im, re)
		default:
			return NaN()
		}
	case math.IsInf(im, 0):
		return complex(math.Copysign(im, re), math.Copysign(math.Pi/2, im))
	}
	xx := x * x
	x1 := complex(1+real(xx), imag(xx)) // 1 + x*x
	return Log(x + Sqrt(x1))            // log(x + sqrt(1 + x*x))
}

// 复数反余弦
//
// 描述：
//
// w = arccos z  =  PI/2 - arcsin z.
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10      5200      1.6e-15      2.8e-16
//    IEEE      -10,+10     30000      1.8e-14      2.2e-15

// Acos 返回 x 的反余弦。
func Acos(x complex128) complex128 {
	w := Asin(x)
	return complex(math.Pi/2-real(w), -imag(w))
}

// Acosh 返回 x 的反双曲余弦。
func Acosh(x complex128) complex128 {
	if x == 0 {
		return complex(0, math.Copysign(math.Pi/2, imag(x)))
	}
	w := Acos(x)
	if imag(w) <= 0 {
		return complex(-imag(w), real(w)) // i * w
	}
	return complex(imag(w), -real(w)) // -i * w
}

// 复数反正切
//
// 描述：
//
// 如果
//     z = x + iy,
//
// 那么
//          1       (    2x     )
// Re w  =  - arctan(-----------)  +  k PI
//          2       (     2    2)
//                  (1 - x  - y )
//
//               ( 2         2)
//          1    (x  +  (y+1) )
// Im w  =  - log(------------)
//          4    ( 2         2)
//               (x  +  (y-1) )
//
// 其中 k 是任意整数。
//
// catan(z) = -i catanh(iz).
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10      5900       1.3e-16     7.8e-18
//    IEEE      -10,+10     30000       2.3e-15     8.5e-17
// 在 |x| 和 |y| < PI/2 的条件下，检验 catan( ctan(z) )  =  z，
// 峰值相对误差为 1.5e-16，均方根相对误差
// 为 2.9e-17。另见 clog()。

// Atan 返回 x 的反正切。
func Atan(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case im == 0:
		return complex(math.Atan(re), im)
	case re == 0 && math.Abs(im) <= 1:
		return complex(re, math.Atanh(im))
	case math.IsInf(im, 0) || math.IsInf(re, 0):
		if math.IsNaN(re) {
			return complex(math.NaN(), math.Copysign(0, im))
		}
		return complex(math.Copysign(math.Pi/2, re), math.Copysign(0, im))
	case math.IsNaN(re) || math.IsNaN(im):
		return NaN()
	}
	x2 := real(x) * real(x)
	a := 1 - x2 - imag(x)*imag(x)
	if a == 0 {
		return NaN()
	}
	t := 0.5 * math.Atan2(2*real(x), a)
	w := reducePi(t)

	t = imag(x) - 1
	b := x2 + t*t
	if b == 0 {
		return NaN()
	}
	t = imag(x) + 1
	c := (x2 + t*t) / b
	return complex(w, 0.25*math.Log(c))
}

// Atanh 返回 x 的反双曲正切。
func Atanh(x complex128) complex128 {
	z := complex(-imag(x), real(x)) // z = i * x
	z = Atan(z)
	return complex(imag(z), -real(z)) // z = -i * z
}
