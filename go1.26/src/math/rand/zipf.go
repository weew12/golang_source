// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// W.Hormann, G.Derflinger：
// "Rejection-Inversion to Generate Variates
// from Monotone Discrete Distributions"（
// 从单调离散分布生成变量的拒绝-反演法）
// http://eeyore.wu-wien.ac.at/papers/96-04-04.wh-der.ps.gz

package rand

import "math"

// Zipf 生成服从 Zipf 分布的变量。
type Zipf struct {
	r            *Rand
	imax         float64
	v            float64
	q            float64
	s            float64
	oneminusQ    float64
	oneminusQinv float64
	hxm          float64
	hx0minusHxm  float64
}

func (z *Zipf) h(x float64) float64 {
	return math.Exp(z.oneminusQ*math.Log(z.v+x)) * z.oneminusQinv
}

func (z *Zipf) hinv(x float64) float64 {
	return math.Exp(z.oneminusQinv*math.Log(z.oneminusQ*x)) - z.v
}

// NewZipf 返回一个 [Zipf] 变量生成器。
// 该生成器生成值 k ∈ [0, imax]，
// 使得 P(k) 与 (v + k) ** (-s) 成正比。
// 要求：s > 1 且 v >= 1。
func NewZipf(r *Rand, s float64, v float64, imax uint64) *Zipf {
	z := new(Zipf)
	if s <= 1.0 || v < 1 {
		return nil
	}
	z.r = r
	z.imax = float64(imax)
	z.v = v
	z.q = s
	z.oneminusQ = 1.0 - z.q
	z.oneminusQinv = 1.0 / z.oneminusQ
	z.hxm = z.h(z.imax + 0.5)
	z.hx0minusHxm = z.h(0.5) - math.Exp(math.Log(z.v)*(-z.q)) - z.hxm
	z.s = 1 - z.hinv(z.h(1.5)-math.Exp(-z.q*math.Log(z.v+1.0)))
	return z
}

// Uint64 返回一个从 [Zipf] 对象所描述的
// [Zipf] 分布中抽取的值。
func (z *Zipf) Uint64() uint64 {
	if z == nil {
		panic("rand: nil Zipf")
	}
	k := 0.0

	for {
		r := z.r.Float64() // r 在 [0,1] 上
		ur := z.hxm + r*z.hx0minusHxm
		x := z.hinv(ur)
		k = math.Floor(x + 0.5)
		if k-x <= z.s {
			break
		}
		if ur >= z.h(k+0.5)-math.Exp(-math.Log(k+z.v)*z.q) {
			break
		}
	}
	return uint64(k)
}
