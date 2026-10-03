// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 系数 _sin[] 和 _cos[] 位于 pkg/math/sin.go 中。

// Sincos 返回 Sin(x), Cos(x)。
//
// 特殊情况为：
//
//	Sincos(±0) = ±0, 1
//	Sincos(±Inf) = NaN, NaN
//	Sincos(NaN) = NaN, NaN
func Sincos(x float64) (sin, cos float64) {
	const (
		PI4A = 7.85398125648498535156e-1  // 0x3fe921fb40000000, Pi/4 拆分为三部分
		PI4B = 3.77489470793079817668e-8  // 0x3e64442d00000000,
		PI4C = 2.69515142907905952645e-15 // 0x3ce8469898cc5170,
	)
	// 特殊情况
	switch {
	case x == 0:
		return x, 1 // 返回 ±0.0, 1.0
	case IsNaN(x) || IsInf(x, 0):
		return NaN(), NaN()
	}

	// 使参数为正
	sinSign, cosSign := false, false
	if x < 0 {
		x = -x
		sinSign = true
	}

	var j uint64
	var y, z float64
	if x >= reduceThreshold {
		j, z = trigReduce(x)
	} else {
		j = uint64(x * (4 / Pi)) // x/(Pi/4) 的整数部分，作为整数用于相位角测试
		y = float64(j)           // x/(Pi/4) 的整数部分，作为浮点数

		if j&1 == 1 { // 将零点映射到原点
			j++
			y++
		}
		j &= 7                               // 对 2Pi 弧度（360 度）取模的八分圆
		z = ((x - y*PI4A) - y*PI4B) - y*PI4C // 扩展精度模运算
	}
	if j > 3 { // 关于 x 轴反射
		j -= 4
		sinSign, cosSign = !sinSign, !cosSign
	}
	if j > 1 {
		cosSign = !cosSign
	}

	zz := z * z
	cos = 1.0 - 0.5*zz + zz*zz*((((((_cos[0]*zz)+_cos[1])*zz+_cos[2])*zz+_cos[3])*zz+_cos[4])*zz+_cos[5])
	sin = z + z*zz*((((((_sin[0]*zz)+_sin[1])*zz+_sin[2])*zz+_sin[3])*zz+_sin[4])*zz+_sin[5])
	if j == 1 || j == 2 {
		sin, cos = cos, sin
	}
	if cosSign {
		cos = -cos
	}
	if sinSign {
		sin = -sin
	}
	return
}
