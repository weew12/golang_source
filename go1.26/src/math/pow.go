// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

func isOddInt(x float64) bool {
	if Abs(x) >= (1 << 53) {
		// 1 << 53 是 float64 格式中最大的精确整数。
		// 任何超出此范围的数都会在小数点前被截断，因此始终是
		// 偶数。
		// 如果没有此检查，且 x 溢出 int64，则下面的 int64(xi) 转换在某些架构上
		// 可能产生错误结果（在 arm64 上确实如此）。参见 issue #57465。
		return false
	}

	xi, xf := Modf(x)
	return xf == 0 && int64(xi)&1 == 1
}

// 特殊情况取自 FreeBSD 的 /usr/src/lib/msun/src/e_pow.c，
// 并由 IEEE Std. 754-2008 “Section 9.2.1 Special values” 更新。

// Pow 返回 x**y，即 y 的以 x 为底的指数。
//
// 特殊情况（按顺序）为：
//
//	Pow(x, ±0) = 1 for any x
//	Pow(1, y) = 1 for any y
//	Pow(x, 1) = x for any x
//	Pow(NaN, y) = NaN
//	Pow(x, NaN) = NaN
//	Pow(±0, y) = ±Inf for y an odd integer < 0
//	Pow(±0, -Inf) = +Inf
//	Pow(±0, +Inf) = +0
//	Pow(±0, y) = +Inf for finite y < 0 and not an odd integer
//	Pow(±0, y) = ±0 for y an odd integer > 0
//	Pow(±0, y) = +0 for finite y > 0 and not an odd integer
//	Pow(-1, ±Inf) = 1
//	Pow(x, +Inf) = +Inf for |x| > 1
//	Pow(x, -Inf) = +0 for |x| > 1
//	Pow(x, +Inf) = +0 for |x| < 1
//	Pow(x, -Inf) = +Inf for |x| < 1
//	Pow(+Inf, y) = +Inf for y > 0
//	Pow(+Inf, y) = +0 for y < 0
//	Pow(-Inf, y) = Pow(-0, -y)
//	Pow(x, y) = NaN for finite x < 0 and finite non-integer y
func Pow(x, y float64) float64 {
	if haveArchPow {
		return archPow(x, y)
	}
	return pow(x, y)
}

func pow(x, y float64) float64 {
	switch {
	case y == 0 || x == 1:
		return 1
	case y == 1:
		return x
	case IsNaN(x) || IsNaN(y):
		return NaN()
	case x == 0:
		switch {
		case y < 0:
			if Signbit(x) && isOddInt(y) {
				return Inf(-1)
			}
			return Inf(1)
		case y > 0:
			if Signbit(x) && isOddInt(y) {
				return x
			}
			return 0
		}
	case IsInf(y, 0):
		switch {
		case x == -1:
			return 1
		case (Abs(x) < 1) == IsInf(y, 1):
			return 0
		default:
			return Inf(1)
		}
	case IsInf(x, 0):
		if IsInf(x, -1) {
			return Pow(1/x, -y) // Pow(-0, -y)
		}
		switch {
		case y < 0:
			return 0
		case y > 0:
			return Inf(1)
		}
	case y == 0.5:
		return Sqrt(x)
	case y == -0.5:
		return 1 / Sqrt(x)
	}

	yi, yf := Modf(Abs(y))
	if yf != 0 && x < 0 {
		return NaN()
	}
	if yi >= 1<<63 {
		// yi 是一个很大的偶数，对于除 -1 之外的所有 x 都会导致溢出（或下溢到 0）
		// （x == 1 已在前面处理）
		switch {
		case x == -1:
			return 1
		case (Abs(x) < 1) == (y > 0):
			return 0
		default:
			return Inf(1)
		}
	}

	// ans = a1 * 2**ae（目前为 1）。
	a1 := 1.0
	ae := 0

	// ans *= x**yf
	if yf != 0 {
		if yf > 0.5 {
			yf--
			yi++
		}
		a1 = Exp(yf * Log(x))
	}

	// ans *= x**yi
	// 通过对 x 进行逐次平方
	// 根据 yi 的各位来累乘。
	// 将 2 的幂累加到 exp 中。
	x1, xe := Frexp(x)
	for i := int64(yi); i != 0; i >>= 1 {
		if xe < -1<<12 || 1<<12 < xe {
			// 在 xe 溢出下面的左移之前捕获它
			// 由于 i !=0，它至少还有一位被置位，因此 ae 将再累加 xe
			// 至少一次迭代，ae += xe 是 ae 的下界
			// ae 的下界超过了 float64 指数的范围
			// 因此最终调用 Ldexp 将产生下溢/溢出（0/Inf）
			ae += xe
			break
		}
		if i&1 == 1 {
			a1 *= x1
			ae += xe
		}
		x1 *= x1
		xe <<= 1
		if x1 < .5 {
			x1 += x1
			xe--
		}
	}

	// ans = a1*2**ae
	// if y < 0 { ans = 1 / ans }
	// 但顺序相反
	if y < 0 {
		a1 = 1 / a1
		ae = -ae
	}
	return Ldexp(a1, ae)
}
