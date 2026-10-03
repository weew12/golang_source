// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Abs 返回 x 的绝对值。
//
// 特殊情况为：
//
//	Abs(±Inf) = +Inf
//	Abs(NaN) = NaN
func Abs(x float64) float64 {
	return Float64frombits(Float64bits(x) &^ (1 << 63))
}
