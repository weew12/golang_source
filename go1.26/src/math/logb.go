// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Logb 返回 x 的二进制指数。
//
// 特殊情况为：
//
//	Logb(±Inf) = +Inf
//	Logb(0) = -Inf
//	Logb(NaN) = NaN
func Logb(x float64) float64 {
	// 特殊情况
	switch {
	case x == 0:
		return Inf(-1)
	case IsInf(x, 0):
		return Inf(1)
	case IsNaN(x):
		return x
	}
	return float64(ilogb(x))
}

// Ilogb 以整数形式返回 x 的二进制指数。
//
// 特殊情况为：
//
//	Ilogb(±Inf) = MaxInt32
//	Ilogb(0) = MinInt32
//	Ilogb(NaN) = MaxInt32
func Ilogb(x float64) int {
	// 特殊情况
	switch {
	case x == 0:
		return MinInt32
	case IsNaN(x):
		return MaxInt32
	case IsInf(x, 0):
		return MaxInt32
	}
	return ilogb(x)
}

// ilogb 返回 x 的二进制指数。它假定 x 是有限且
// 非零的。
func ilogb(x float64) int {
	x, exp := normalize(x)
	return int((Float64bits(x)>>shift)&mask) - bias + exp
}
