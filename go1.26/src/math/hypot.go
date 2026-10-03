// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	Hypot —— sqrt(p*p + q*q)，但仅当结果溢出时才溢出。
*/

// Hypot 返回 [Sqrt](p*p + q*q)，并注意避免
// 不必要的溢出和下溢。
//
// 特殊情况为：
//
//	Hypot(±Inf, q) = +Inf
//	Hypot(p, ±Inf) = +Inf
//	Hypot(NaN, q) = NaN
//	Hypot(p, NaN) = NaN
func Hypot(p, q float64) float64 {
	if haveArchHypot {
		return archHypot(p, q)
	}
	return hypot(p, q)
}

func hypot(p, q float64) float64 {
	p, q = Abs(p), Abs(q)
	// 特殊情况
	switch {
	case IsInf(p, 1) || IsInf(q, 1):
		return Inf(1)
	case IsNaN(p) || IsNaN(q):
		return NaN()
	}
	if p < q {
		p, q = q, p
	}
	if p == 0 {
		return 0
	}
	q = q / p
	return p * Sqrt(1+q*q)
}
