// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Nextafter32 返回朝 y 方向 x 之后的下一个可表示的 float32 值。
//
// 特殊情况为：
//
//	Nextafter32(x, x)   = x
//	Nextafter32(NaN, y) = NaN
//	Nextafter32(x, NaN) = NaN
func Nextafter32(x, y float32) (r float32) {
	switch {
	case IsNaN(float64(x)) || IsNaN(float64(y)): // 特殊情况
		r = float32(NaN())
	case x == y:
		r = x
	case x == 0:
		r = float32(Copysign(float64(Float32frombits(1)), float64(y)))
	case (y > x) == (x > 0):
		r = Float32frombits(Float32bits(x) + 1)
	default:
		r = Float32frombits(Float32bits(x) - 1)
	}
	return
}

// Nextafter 返回朝 y 方向 x 之后的下一个可表示的 float64 值。
//
// 特殊情况为：
//
//	Nextafter(x, x)   = x
//	Nextafter(NaN, y) = NaN
//	Nextafter(x, NaN) = NaN
func Nextafter(x, y float64) (r float64) {
	switch {
	case IsNaN(x) || IsNaN(y): // 特殊情况
		r = NaN()
	case x == y:
		r = x
	case x == 0:
		r = Copysign(Float64frombits(1), y)
	case (y > x) == (x > 0):
		r = Float64frombits(Float64bits(x) + 1)
	default:
		r = Float64frombits(Float64bits(x) - 1)
	}
	return
}
