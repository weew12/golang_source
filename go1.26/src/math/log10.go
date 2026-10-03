// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Log10 返回 x 的十进制对数。
// 特殊情况与 [Log] 的相同。
func Log10(x float64) float64 {
	if haveArchLog10 {
		return archLog10(x)
	}
	return log10(x)
}

func log10(x float64) float64 {
	return Log(x) * (1 / Ln10)
}

// Log2 返回 x 的二进制对数。
// 特殊情况与 [Log] 的相同。
func Log2(x float64) float64 {
	if haveArchLog2 {
		return archLog2(x)
	}
	return log2(x)
}

func log2(x float64) float64 {
	frac, exp := Frexp(x)
	// 确保 2 的精确幂给出精确答案。
	// 不要依赖 Log(0.5)*(1/Ln2)+exp 恰好等于 exp-1。
	if frac == 0.5 {
		return float64(exp - 1)
	}
	return Log(frac)*(1/Ln2) + float64(exp)
}
