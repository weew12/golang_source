// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	浮点反正弦与反余弦。

	它们通过适当的范围缩减后计算反正切来实现。
*/

// Asin 返回 x 的反正弦（以弧度为单位）。
//
// 特殊情况为：
//
//	Asin(±0) = ±0
//	Asin(x) = NaN 若 x < -1 或 x > 1
func Asin(x float64) float64 {
	if haveArchAsin {
		return archAsin(x)
	}
	return asin(x)
}

func asin(x float64) float64 {
	if x == 0 {
		return x // 特殊情况
	}
	sign := false
	if x < 0 {
		x = -x
		sign = true
	}
	if x > 1 {
		return NaN() // 特殊情况
	}

	temp := Sqrt(1 - x*x)
	if x > 0.7 {
		temp = Pi/2 - satan(temp/x)
	} else {
		temp = satan(x / temp)
	}

	if sign {
		temp = -temp
	}
	return temp
}

// Acos 返回 x 的反余弦（以弧度为单位）。
//
// 特殊情况为：
//
//	Acos(x) = NaN 若 x < -1 或 x > 1
func Acos(x float64) float64 {
	if haveArchAcos {
		return archAcos(x)
	}
	return acos(x)
}

func acos(x float64) float64 {
	return Pi/2 - Asin(x)
}
