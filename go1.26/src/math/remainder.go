// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 原始 C 代码及下面的注释来自
// FreeBSD 的 /usr/src/lib/msun/src/e_remainder.c，并随附
// 此声明。Go 代码是原始 C 的简化版本。
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
// __ieee754_remainder(x,y)
// 返回：
//      返回  x REM y  =  x - [x/y]*y，如同在无限
//      精度算术中一样，其中 [x/y] 是最接近 x/y 的（无限位）
//      整数（在恰好居中的情况下，选择偶数）。
// 方法：
//      基于 Mod() 精确返回  x - [x/y]截断 * y 。

// Remainder 返回 x/y 的 IEEE 754 浮点余数。
//
// 特殊情况为：
//
//	Remainder(±Inf, y) = NaN
//	Remainder(NaN, y) = NaN
//	Remainder(x, 0) = NaN
//	Remainder(x, ±Inf) = x
//	Remainder(x, NaN) = NaN
func Remainder(x, y float64) float64 {
	if haveArchRemainder {
		return archRemainder(x, y)
	}
	return remainder(x, y)
}

func remainder(x, y float64) float64 {
	const (
		Tiny    = 4.45014771701440276618e-308 // 0x0020000000000000
		HalfMax = MaxFloat64 / 2
	)
	// 特殊情况
	switch {
	case IsNaN(x) || IsNaN(y) || IsInf(x, 0) || y == 0:
		return NaN()
	case IsInf(y, 0):
		return x
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	if y < 0 {
		y = -y
	}
	if x == y {
		if sign {
			zero := 0.0
			return -zero
		}
		return 0
	}
	if y <= HalfMax {
		x = Mod(x, y+y) // 现在 x < 2y
	}
	if y < Tiny {
		if x+x > y {
			x -= y
			if x+x >= y {
				x -= y
			}
		}
	} else {
		yHalf := 0.5 * y
		if x > yHalf {
			x -= y
			if x >= yHalf {
				x -= y
			}
		}
	}
	if sign {
		x = -x
	}
	return x
}
