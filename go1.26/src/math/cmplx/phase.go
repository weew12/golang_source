// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmplx

import "math"

// Phase 返回 x 的相位（也称为辐角）。
// 返回值在区间 [-Pi, Pi] 内。
func Phase(x complex128) float64 { return math.Atan2(imag(x), real(x)) }
