// Copyright 2009-2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	浮点取模函数。
*/

// Mod 返回 x/y 的浮点余数。
// 结果的绝对值小于 y，且其
// 符号与 x 的相同。
//
// 特殊情况为：
//
//	Mod(±Inf, y) = NaN
//	Mod(NaN, y) = NaN
//	Mod(x, 0) = NaN
//	Mod(x, ±Inf) = x
//	Mod(x, NaN) = NaN
func Mod(x, y float64) float64 {
	if haveArchMod {
		return archMod(x, y)
	}
	return mod(x, y)
}

func mod(x, y float64) float64 {
	if y == 0 || IsInf(x, 0) || IsNaN(x) || IsNaN(y) {
		return NaN()
	}
	y = Abs(y)

	yfr, yexp := Frexp(y)
	r := x
	if x < 0 {
		r = -x
	}

	for r >= y {
		rfr, rexp := Frexp(r)
		if rfr < yfr {
			rexp = rexp - 1
		}
		r = r - Ldexp(y, rexp-yexp)
	}
	if x < 0 {
		r = -r
	}
	return r
}
