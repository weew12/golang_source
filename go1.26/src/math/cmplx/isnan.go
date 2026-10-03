// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmplx

import "math"

// IsNaN 报告 real(x) 或 imag(x) 中是否有一个是 NaN
// 且两者都不是无穷大。
func IsNaN(x complex128) bool {
	switch {
	case math.IsInf(real(x), 0) || math.IsInf(imag(x), 0):
		return false
	case math.IsNaN(real(x)) || math.IsNaN(imag(x)):
		return true
	}
	return false
}

// NaN 返回一个复数“非数”（not-a-number）值。
func NaN() complex128 {
	nan := math.NaN()
	return complex(nan, nan)
}
