// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package big

import (
	"math"
	"sync"
)

var threeOnce struct {
	sync.Once
	v *Float
}

func three() *Float {
	threeOnce.Do(func() {
		threeOnce.v = NewFloat(3.0)
	})
	return threeOnce.v
}

// Sqrt 将 z 设为 x 的舍入后的平方根，并返回它。
//
// 如果 z 的精度为 0，则在操作前将其改为 x 的精度。舍入根据 z 的精度
// 和舍入模式进行，但不会计算 z 的准确度。具体来说，z.Acc() 的结果
// 是未定义的。
//
// 如果 z < 0，该函数会 panic。在这种情况下 z 的值未定义。
func (z *Float) Sqrt(x *Float) *Float {
	if debugFloat {
		x.validate()
	}

	if z.prec == 0 {
		z.prec = x.prec
	}

	if x.Sign() == -1 {
		// 遵循 IEEE754-2008（第 7.2 节）
		panic(ErrNaN{"square root of negative operand"})
	}

	// 处理 ±0 和 +∞
	if x.form != finite {
		z.acc = Exact
		z.form = x.form
		z.neg = x.neg // IEEE754-2008 要求 √±0 = ±0
		return z
	}

	// MantExp 将参数的精度设为接收者的精度，当 z.prec > x.prec 时
	// 这会降低 z.prec。在 MantExp 调用之后恢复它。
	prec := z.prec
	b := x.MantExp(z)
	z.prec = prec

	// 计算 √(z·2**b)：
	//   √( z)·2**(½b)     b 为偶数时
	//   √(2z)·2**(⌊½b⌋)   b > 0 且为奇数时
	//   √(½z)·2**(⌈½b⌉)   b < 0 且为奇数时
	switch b % 2 {
	case 0:
		// 无需操作
	case 1:
		z.exp++
	case -1:
		z.exp--
	}
	// 0.25 <= z < 2.0

	// 求解 1/x² - z = 0 避免了 Quo 调用，而且更快，尤其是在高精度时。
	z.sqrtInverse(z)

	// 重新附上减半的指数
	return z.SetMantExp(z, b/2)
}

// 通过求解
//
//	1/t² - x = 0
//
// 得到 t（使用牛顿法）来计算 √x（精度为 z.prec），然后求逆。
func (z *Float) sqrtInverse(x *Float) {
	// 令
	//   f(t) = 1/t² - x
	// 那么
	//   g(t) = f(t)/f'(t) = -½t(1 - xt²)
	// 下一次猜测由下式给出
	//   t2 = t - g(t) = ½t(3 - xt²)
	u := newFloat(z.prec)
	v := newFloat(z.prec)
	three := three()
	ng := func(t *Float) *Float {
		u.prec = t.prec
		v.prec = t.prec
		u.Mul(t, t)     // u = t²
		u.Mul(x, u)     //   = xt²
		v.Sub(three, u) // v = 3 - xt²
		u.Mul(t, v)     // u = t(3 - xt²)
		u.exp--         //   = ½t(3 - xt²)
		return t.Set(u)
	}

	xf, _ := x.Float64()
	sqi := newFloat(z.prec)
	sqi.SetFloat64(1 / math.Sqrt(xf))
	for prec := z.prec + 32; sqi.prec < prec; {
		sqi.prec *= 2
		sqi = ng(sqi)
	}
	// sqi = 1/√x

	// x/√x = √x
	z.Mul(x, sqi)
}

// newFloat 返回一个新的 *Float，其空间为给定精度的两倍。
func newFloat(prec2 uint32) *Float {
	z := new(Float)
	// nat.make 确保切片长度 > 0
	z.mant = z.mant.make(int(prec2/_W) * 2)
	return z
}
