// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cmplx 为复数提供基本常量和数学函数。
// 特殊情况的处理遵循 C99 标准附录 G 的 IEC 60559 兼容复数运算。
package cmplx

import "math"

// Abs 返回 x 的绝对值（也称为模）。
func Abs(x complex128) float64 { return math.Hypot(real(x), imag(x)) }
