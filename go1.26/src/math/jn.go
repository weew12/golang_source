// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	n 阶第一类和第二类贝塞尔函数。
*/

// 原始 C 代码及下面的长注释
// 来自 FreeBSD 的 /usr/src/lib/msun/src/e_jn.c，
// 并随附此声明。Go 代码是原始 C
// 的简化版本。
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
// __ieee754_jn(n, x), __ieee754_yn(n, x)
// n 阶第一类和第二类浮点贝塞尔函数
//
// 特殊情况：
//      y0(0)=y1(0)=yn(n,0) = -inf，并产生除零信号；
//      y0(-ve)=y1(-ve)=yn(n,-ve) 为 NaN，并产生无效信号。
// 注 2. 关于 jn(n,x), yn(n,x)
//      当 n=0 时，调用 j0(x)，
//      当 n=1 时，调用 j1(x)，
//      当 n<x 时，使用前向递推，起始于
//      j0(x) 和 j1(x) 的值。
//      当 n>x 时，对以下式子求连分式逼近
//      j(n,x)/j(n-1,x)，然后使用后向
//      递推，起始于一个假定的
//      j(n,x) 值。将得到的 j(0,x) 值与
//      实际值比较，以修正
//      j(n,x) 的假定值。
//
//      yn(n,x) 在所有方面都类似，只是
//      对所有
//      n>1 的值都使用前向递推。

// Jn 返回 n 阶第一类贝塞尔函数。
//
// 特殊情况为：
//
//	Jn(n, ±Inf) = 0
//	Jn(n, NaN) = NaN
func Jn(n int, x float64) float64 {
	const (
		TwoM29 = 1.0 / (1 << 29) // 2**-29 0x3e10000000000000
		Two302 = 1 << 302        // 2**302 0x52D0000000000000
	)
	// 特殊情况
	switch {
	case IsNaN(x):
		return x
	case IsInf(x, 0):
		return 0
	}
	// J(-n, x) = (-1)**n * J(n, x), J(n, -x) = (-1)**n * J(n, x)
	// 因此，J(-n, x) = J(n, -x)

	if n == 0 {
		return J0(x)
	}
	if x == 0 {
		return 0
	}
	if n < 0 {
		n, x = -n, -x
	}
	if n == 1 {
		return J1(x)
	}
	sign := false
	if x < 0 {
		x = -x
		if n&1 == 1 {
			sign = true // n 为奇数且 x 为负
		}
	}
	var b float64
	if float64(n) <= x {
		// 可以安全地使用 J(n+1,x)=2n/x *J(n,x)-J(n-1,x)
		if x >= Two302 { // x > 2**302

			// (x >> n**2)
			//          Jn(x) = cos(x-(2n+1)*pi/4)*sqrt(2/x*pi)
			//          Yn(x) = sin(x-(2n+1)*pi/4)*sqrt(2/x*pi)
			//          令 s=sin(x), c=cos(x)，
			//              xn=x-(2n+1)*pi/4, sqt2 = sqrt(2)，则
			//
			//                 n    sin(xn)*sqt2    cos(xn)*sqt2
			//              ----------------------------------
			//                 0     s-c             c+s
			//                 1    -s-c            -c+s
			//                 2    -s+c            -c-s
			//                 3     s+c             c-s

			var temp float64
			switch s, c := Sincos(x); n & 3 {
			case 0:
				temp = c + s
			case 1:
				temp = -c + s
			case 2:
				temp = -c - s
			case 3:
				temp = c - s
			}
			b = (1 / SqrtPi) * temp / Sqrt(x)
		} else {
			b = J1(x)
			for i, a := 1, J0(x); i < n; i++ {
				a, b = b, b*(float64(i+i)/x)-a // 避免下溢
			}
		}
	} else {
		if x < TwoM29 { // x < 2**-29
			// x 极小，返回 J(n,x) 的一阶泰勒展开
			// J(n,x) = 1/n!*(x/2)**n  - ...

			if n > 33 { // 下溢
				b = 0
			} else {
				temp := x * 0.5
				b = temp
				a := 1.0
				for i := 2; i <= n; i++ {
					a *= float64(i) // a = n!
					b *= temp       // b = (x/2)**n
				}
				b /= a
			}
		} else {
			// 使用后向递推
			//                      x      x**2      x**2
			//  J(n,x)/J(n-1,x) =  ----   ------   ------   .....
			//                      2n  - 2(n+1) - 2(n+2)
			//
			//                      1      1        1
			//  (for large x)   =  ----  ------   ------   .....
			//                      2n   2(n+1)   2(n+2)
			//                      -- - ------ - ------ -
			//                       x     x         x
			//
			// 令 w = 2n/x 且 h=2/x，则上述商
			// 等于连分式：
			//                  1
			//      = -----------------------
			//                     1
			//         w - -----------------
			//                        1
			//              w+h - ---------
			//                     w+2h - ...
			//
			// 为确定需要多少项，令
			// Q(0) = w, Q(1) = w(w+h) - 1,
			// Q(k) = (w+k*h)*Q(k-1) - Q(k-2),
			// 当 Q(k) > 1e4	对单精度足够
			// 当 Q(k) > 1e9	对双精度足够
			// 当 Q(k) > 1e17	对四精度足够

			// 确定 k
			w := float64(n+n) / x
			h := 2 / x
			q0 := w
			z := w + h
			q1 := w*z - 1
			k := 1
			for q1 < 1e9 {
				k++
				z += h
				q0, q1 = q1, z*q1-q0
			}
			m := n + n
			t := 0.0
			for i := 2 * (n + k); i >= m; i -= 2 {
				t = 1 / (float64(i)/x - t)
			}
			a := t
			b = 1
			//  估计 log((2/x)**n*n!) = n*log(2/x)+n*ln(n)
			//  因此，如果 n*(log(2n/x)) > ...
			//  single 8.8722839355e+01
			//  double 7.09782712893383973096e+02
			//  long double 1.1356523406294143949491931077970765006170e+04
			//  则递推值可能溢出，结果
			//  很可能下溢为零

			tmp := float64(n)
			v := 2 / x
			tmp = tmp * Log(Abs(v*tmp))
			if tmp < 7.09782712893383973096e+02 {
				for i := n - 1; i > 0; i-- {
					di := float64(i + i)
					a, b = b, b*di/x-a
				}
			} else {
				for i := n - 1; i > 0; i-- {
					di := float64(i + i)
					a, b = b, b*di/x-a
					// 缩放 b 以避免虚假溢出
					if b > 1e100 {
						a /= b
						t /= b
						b = 1
					}
				}
			}
			b = t * J0(x) / b
		}
	}
	if sign {
		return -b
	}
	return b
}

// Yn 返回 n 阶第二类贝塞尔函数。
//
// 特殊情况为：
//
//	Yn(n, +Inf) = 0
//	Yn(n ≥ 0, 0) = -Inf
//	Yn(n < 0, 0) = +Inf if n is odd, -Inf if n is even
//	Yn(n, x < 0) = NaN
//	Yn(n, NaN) = NaN
func Yn(n int, x float64) float64 {
	const Two302 = 1 << 302 // 2**302 0x52D0000000000000
	// 特殊情况
	switch {
	case x < 0 || IsNaN(x):
		return NaN()
	case IsInf(x, 1):
		return 0
	}

	if n == 0 {
		return Y0(x)
	}
	if x == 0 {
		if n < 0 && n&1 == 1 {
			return Inf(1)
		}
		return Inf(-1)
	}
	sign := false
	if n < 0 {
		n = -n
		if n&1 == 1 {
			sign = true // 若 n < 0 且 |n| 为奇数，则 sign 为 true
		}
	}
	if n == 1 {
		if sign {
			return -Y1(x)
		}
		return Y1(x)
	}
	var b float64
	if x >= Two302 { // x > 2**302
		// (x >> n**2)
		//	    Jn(x) = cos(x-(2n+1)*pi/4)*sqrt(2/x*pi)
		//	    Yn(x) = sin(x-(2n+1)*pi/4)*sqrt(2/x*pi)
		//	    令 s=sin(x), c=cos(x)，
		//		xn=x-(2n+1)*pi/4, sqt2 = sqrt(2)，则
		//
		//		   n	sin(xn)*sqt2	cos(xn)*sqt2
		//		----------------------------------
		//		   0	 s-c		 c+s
		//		   1	-s-c 		-c+s
		//		   2	-s+c		-c-s
		//		   3	 s+c		 c-s

		var temp float64
		switch s, c := Sincos(x); n & 3 {
		case 0:
			temp = s - c
		case 1:
			temp = -s - c
		case 2:
			temp = -s + c
		case 3:
			temp = s + c
		}
		b = (1 / SqrtPi) * temp / Sqrt(x)
	} else {
		a := Y0(x)
		b = Y1(x)
		// 若 b 为 -inf 则退出
		for i := 1; i < n && !IsInf(b, -1); i++ {
			a, b = b, (float64(i+i)/x)*b-a
		}
	}
	if sign {
		return -b
	}
	return b
}
