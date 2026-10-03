// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// 原始 C 代码及下面的长注释
// 来自 FreeBSD 的 /usr/src/lib/msun/src/e_sqrt.c，
// 并随附此声明。Go 代码是原始 C 的简化版本。
//
// ====================================================
// Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
//
// Developed at SunPro, a Sun Microsystems, Inc. business.
// Permission to use, copy, modify, and distribute this
// software is freely granted, provided that this notice
// is preserved.
// ====================================================
//
// __ieee754_sqrt(x)
// 返回正确舍入的 sqrt。
//           -----------------------------------------
//           | Use the hardware sqrt if you have one |
//           -----------------------------------------
// 方法：
//   使用整数运算的逐位方法。（较慢，但可移植）
//   1. 规格化
//      用 2 的偶数次幂将 x 缩放到 [1,4) 内的 y：
//      找到整数 k 使得  1 <= (y=x*2**(2k)) < 4，则
//              sqrt(x) = 2**k * sqrt(y)
//   2. 逐位计算
//      令 q  = sqrt(y) 截断到二进制小数点后 i 位 (q = 1),
//           i                                                   0
//                                     i+1         2
//          s  = 2*q , and      y  =  2   * ( y - q  ).          (1)
//           i      i            i                 i
//
//      为了从 q 计算 q ，需要检查是否
//                  i+1       i
//
//                            -(i+1) 2
//                      (q + 2      )  <= y.                     (2)
//                        i
//                                                            -(i+1)
//      若 (2) 不成立，则 q   = q ; 否则 q   = q  + 2      .
//                             i+1   i             i+1   i
//
//      经过一些代数变换，不难看出
//      (2) 等价于
//                             -(i+1)
//                      s  +  2       <= y                       (3)
//                       i                i
//
//      (3) 的优点是 s 和 y 可以通过以下递推公式计算：
//                                    i      i
//      以下递推公式：
//          若 (3) 不成立
//
//          s     =  s  ,       y    = y   ;                     (4)
//           i+1      i          i+1    i
//
//      否则，
//                         -i                      -(i+1)
//          s     =  s  + 2  ,  y    = y  -  s  - 2              (5)
//           i+1      i          i+1    i     i
//
//      可以很容易地用归纳法证明 (4) 和 (5)。
//      注意：由于 (3) 的左侧仅包含 i+2 位，
//            没有必要在 (3) 中
//            进行完整的（53 位）比较。
//   3. 最终舍入
//      在生成 53 位结果后，我们再计算一位。
//      结合余数，我们可以判断
//      结果是精确的、大于 1/2ulp，还是小于 1/2ulp
//      （它永远不会等于 1/2ulp）。
//      舍入模式可以通过检查以下情况来检测：
//      对于某个浮点数 "huge" 和 "tiny"，huge + tiny 是否等于 huge，
//      以及 huge - tiny 是否等于 huge。
//
//
// 注意：省略了舍入模式检测。常量 "mask"、"shift"、
// 和 "bias" 位于 src/math/bits.go 中

// Sqrt 返回 x 的平方根。
//
// 特殊情况为：
//
//	Sqrt(+Inf) = +Inf
//	Sqrt(±0) = ±0
//	Sqrt(x < 0) = NaN
//	Sqrt(NaN) = NaN
func Sqrt(x float64) float64 {
	return sqrt(x)
}

// 注意：在 Sqrt 是单条指令的系统上，
// 编译器可能会将直接调用改为直接使用该指令。

func sqrt(x float64) float64 {
	// 特殊情况
	switch {
	case x == 0 || IsNaN(x) || IsInf(x, 1):
		return x
	case x < 0:
		return NaN()
	}
	ix := Float64bits(x)
	// 规格化 x
	exp := int((ix >> shift) & mask)
	if exp == 0 { // 非规格化 x
		for ix&(1<<shift) == 0 {
			ix <<= 1
			exp--
		}
		exp++
	}
	exp -= bias // 去偏置指数
	ix &^= mask << shift
	ix |= 1 << shift
	if exp&1 == 1 { // 奇数指数，将 x 加倍使其为偶数
		ix <<= 1
	}
	exp >>= 1 // exp = exp/2，平方根的指数
	// 逐位生成 sqrt(x)
	ix <<= 1
	var q, s uint64               // q = sqrt(x)
	r := uint64(1 << (shift + 1)) // r = 从最高位到最低位移动的位
	for r != 0 {
		t := s + r
		if t <= ix {
			s = t + r
			ix -= t
			q += r
		}
		ix <<= 1
		r >>= 1
	}
	// 最终舍入
	if ix != 0 { // 余数，结果不精确
		q += q & 1 // 根据额外位进行舍入
	}
	ix = q>>1 + uint64(exp-1+bias)<<shift // 有效数字 + 偏置指数
	return Float64frombits(ix)
}
