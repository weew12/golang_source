// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmplx

import "math"

// Rect 返回具有极坐标 r、θ 的复数 x。
func Rect(r, θ float64) complex128 {
	s, c := math.Sincos(θ)
	return complex(r*c, r*s)
}
