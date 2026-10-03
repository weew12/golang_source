// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Dim 返回 x-y 与 0 中的较大值。
//
// 特殊情况为：
//
//	Dim(+Inf, +Inf) = NaN
//	Dim(-Inf, -Inf) = NaN
//	Dim(x, NaN) = Dim(NaN, x) = NaN
func Dim(x, y float64) float64 {
	// 这些特殊情况在减法后得到 NaN：
	//      +Inf - +Inf = NaN
	//      -Inf - -Inf = NaN
	//       NaN - y    = NaN
	//         x - NaN  = NaN
	v := x - y
	if v <= 0 {
		// v 为负数或 0
		return 0
	}
	// v 为正数或 NaN
	return v
}

// Max 返回 x 或 y 中的较大值。
//
// 特殊情况为：
//
//	Max(x, +Inf) = Max(+Inf, x) = +Inf
//	Max(x, NaN) = Max(NaN, x) = NaN
//	Max(+0, ±0) = Max(±0, +0) = +0
//	Max(-0, -0) = -0
//
// 注意，当以 NaN 和 +Inf 调用时，这与内置函数 max 不同。
func Max(x, y float64) float64 {
	if haveArchMax {
		return archMax(x, y)
	}
	return max(x, y)
}

func max(x, y float64) float64 {
	// 特殊情况
	switch {
	case IsInf(x, 1) || IsInf(y, 1):
		return Inf(1)
	case IsNaN(x) || IsNaN(y):
		return NaN()
	case x == 0 && x == y:
		if Signbit(x) {
			return y
		}
		return x
	}
	if x > y {
		return x
	}
	return y
}

// Min 返回 x 或 y 中的较小值。
//
// 特殊情况为：
//
//	Min(x, -Inf) = Min(-Inf, x) = -Inf
//	Min(x, NaN) = Min(NaN, x) = NaN
//	Min(-0, ±0) = Min(±0, -0) = -0
//
// 注意，当以 NaN 和 -Inf 调用时，这与内置函数 min 不同。
func Min(x, y float64) float64 {
	if haveArchMin {
		return archMin(x, y)
	}
	return min(x, y)
}

func min(x, y float64) float64 {
	// 特殊情况
	switch {
	case IsInf(x, -1) || IsInf(y, -1):
		return Inf(-1)
	case IsNaN(x) || IsNaN(y):
		return NaN()
	case x == 0 && x == y:
		if Signbit(x) {
			return x
		}
		return y
	}
	if x < y {
		return x
	}
	return y
}
