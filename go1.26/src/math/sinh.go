// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	浮点双曲正弦和双曲余弦。

	对于绝对值大于 0.5 的参数，调用指数函数。

	对于绝对值小于 0.5 的参数，使用级数。

	对于所有参数，Cosh(x) 都由指数函数计算。
*/

// Sinh 返回 x 的双曲正弦。
//
// 特殊情况为：
//
//	Sinh(±0) = ±0
//	Sinh(±Inf) = ±Inf
//	Sinh(NaN) = NaN
func Sinh(x float64) float64 {
	if haveArchSinh {
		return archSinh(x)
	}
	return sinh(x)
}

func sinh(x float64) float64 {
	// 这些系数是 Hart & Cheney 的 #2029。（20.36D）
	const (
		P0 = -0.6307673640497716991184787251e+6
		P1 = -0.8991272022039509355398013511e+5
		P2 = -0.2894211355989563807284660366e+4
		P3 = -0.2630563213397497062819489e+2
		Q0 = -0.6307673640497716991212077277e+6
		Q1 = 0.1521517378790019070696485176e+5
		Q2 = -0.173678953558233699533450911e+3
	)

	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	var temp float64
	switch {
	case x > 21:
		temp = Exp(x) * 0.5

	case x > 0.5:
		ex := Exp(x)
		temp = (ex - 1/ex) * 0.5

	default:
		sq := x * x
		temp = (((P3*sq+P2)*sq+P1)*sq + P0) * x
		temp = temp / (((sq+Q2)*sq+Q1)*sq + Q0)
	}

	if sign {
		temp = -temp
	}
	return temp
}

// Cosh 返回 x 的双曲余弦。
//
// 特殊情况为：
//
//	Cosh(±0) = 1
//	Cosh(±Inf) = +Inf
//	Cosh(NaN) = NaN
func Cosh(x float64) float64 {
	if haveArchCosh {
		return archCosh(x)
	}
	return cosh(x)
}

func cosh(x float64) float64 {
	x = Abs(x)
	if x > 21 {
		return Exp(x) * 0.5
	}
	ex := Exp(x)
	return (ex + 1/ex) * 0.5
}
