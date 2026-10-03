// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

/*
	伽玛函数的浮点对数。
*/

// 原始 C 代码及下面的长注释
// 来自 FreeBSD 的 /usr/src/lib/msun/src/e_lgamma_r.c，
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
// __ieee754_lgamma_r(x, signgamp)
// 伽玛函数对数的可重入版本，
// 用户提供指向 Gamma(x) 符号的指针。
//
// 方法：
//   1. 对 0 < x <= 8 的参数归约
//      由于 gamma(1+s)=s*gamma(s)，对于 x 在 [0,8] 内，我们可以
//      通过以下方式将 x 归约到 [1.5,2.5] 内的一个数
//              lgamma(1+s) = log(s) + lgamma(s)
//      例如，
//              lgamma(7.3) = log(6.3) + lgamma(6.3)
//                          = log(6.3*5.3) + lgamma(5.3)
//                          = log(6.3*5.3*4.3*3.3*2.3) + lgamma(2.3)
//   2. 在 lgamma 的最小值附近作多项式逼近
//      （ymin=1.461632144968362245）以保持单调性。
//      在 [ymin-0.23, ymin+0.27]（即 [1.23164,1.73163]）上，使用
//              Let z = x-ymin;
//              lgamma(x) = -1.214862905358496078218 + z**2*poly(z)
//              poly(z) 是一个 14 次多项式。
//   2. 在主区间 [2,3] 上有理逼近
//      我们使用以下逼近：
//              s = x-2.0;
//              lgamma(x) = 0.5*s + s*P(s)/Q(s)
//      精度为
//              |P/Q - (lgamma(x)-0.5s)| < 2**-61.71
//      我们的算法基于以下观察
//
//                             zeta(2)-1    2    zeta(3)-1    3
// lgamma(2+s) = s*(1-Euler) + --------- * s  -  --------- * s  + ...
//                                 2                 3
//
//      其中 Euler = 0.5772156649... 是欧拉常数，它
//      非常接近 0.5。
//
//   3. 对于 x>=8，我们有
//      lgamma(x)~(x-0.5)log(x)-x+0.5*log(2pi)+1/(12x)-1/(360x**3)+....
//      （更好的公式：
//         lgamma(x)~(x-0.5)*(log(x)-1)-.5*(log(2pi)-1) + ...)
//      令 z = 1/x，然后我们逼近
//              f(z) = lgamma(x) - (x-0.5)(log(x)-1)
//      通过
//                                  3       5             11
//              w = w0 + w1*z + w2*z  + w3*z  + ... + w6*z
//      其中
//              |w - f(z)| < 2**-58.74
//
//   4. 对于负 x，由于（G 是伽玛函数）
//              -x*G(-x)*G(x) = pi/sin(pi*x),
//      我们有
//              G(x) = pi/(sin(pi*x)*(-x)*G(-x))
//      由于 G(-x) 为正，对于 x<0 有 sign(G(x)) = sign(sin(pi*x))
//      因此，对于 x<0，signgam = sign(sin(pi*x)) 且
//              lgamma(x) = log(|Gamma(x)|)
//                        = log(pi/(|x*sin(pi*x)|)) - lgamma(-x);
//      注意：在计算 sin(pi*(-x)) 时，
//            应避免直接计算 pi*(-x)。
//
//   5. 特殊情况
//              lgamma(2+s) ~ s*(1-Euler) for tiny s
//              lgamma(1)=lgamma(2)=0
//              lgamma(x) ~ -log(x) for tiny x
//              lgamma(0) = lgamma(inf) = inf
//              lgamma(-integer) = +-inf
//
//

var _lgamA = [...]float64{
	7.72156649015328655494e-02, // 0x3FB3C467E37DB0C8
	3.22467033424113591611e-01, // 0x3FD4A34CC4A60FAD
	6.73523010531292681824e-02, // 0x3FB13E001A5562A7
	2.05808084325167332806e-02, // 0x3F951322AC92547B
	7.38555086081402883957e-03, // 0x3F7E404FB68FEFE8
	2.89051383673415629091e-03, // 0x3F67ADD8CCB7926B
	1.19270763183362067845e-03, // 0x3F538A94116F3F5D
	5.10069792153511336608e-04, // 0x3F40B6C689B99C00
	2.20862790713908385557e-04, // 0x3F2CF2ECED10E54D
	1.08011567247583939954e-04, // 0x3F1C5088987DFB07
	2.52144565451257326939e-05, // 0x3EFA7074428CFA52
	4.48640949618915160150e-05, // 0x3F07858E90A45837
}
var _lgamR = [...]float64{
	1.0,                        // placeholder
	1.39200533467621045958e+00, // 0x3FF645A762C4AB74
	7.21935547567138069525e-01, // 0x3FE71A1893D3DCDC
	1.71933865632803078993e-01, // 0x3FC601EDCCFBDF27
	1.86459191715652901344e-02, // 0x3F9317EA742ED475
	7.77942496381893596434e-04, // 0x3F497DDACA41A95B
	7.32668430744625636189e-06, // 0x3EDEBAF7A5B38140
}
var _lgamS = [...]float64{
	-7.72156649015328655494e-02, // 0xBFB3C467E37DB0C8
	2.14982415960608852501e-01,  // 0x3FCB848B36E20878
	3.25778796408930981787e-01,  // 0x3FD4D98F4F139F59
	1.46350472652464452805e-01,  // 0x3FC2BB9CBEE5F2F7
	2.66422703033638609560e-02,  // 0x3F9B481C7E939961
	1.84028451407337715652e-03,  // 0x3F5E26B67368F239
	3.19475326584100867617e-05,  // 0x3F00BFECDD17E945
}
var _lgamT = [...]float64{
	4.83836122723810047042e-01,  // 0x3FDEF72BC8EE38A2
	-1.47587722994593911752e-01, // 0xBFC2E4278DC6C509
	6.46249402391333854778e-02,  // 0x3FB08B4294D5419B
	-3.27885410759859649565e-02, // 0xBFA0C9A8DF35B713
	1.79706750811820387126e-02,  // 0x3F9266E7970AF9EC
	-1.03142241298341437450e-02, // 0xBF851F9FBA91EC6A
	6.10053870246291332635e-03,  // 0x3F78FCE0E370E344
	-3.68452016781138256760e-03, // 0xBF6E2EFFB3E914D7
	2.25964780900612472250e-03,  // 0x3F6282D32E15C915
	-1.40346469989232843813e-03, // 0xBF56FE8EBF2D1AF1
	8.81081882437654011382e-04,  // 0x3F4CDF0CEF61A8E9
	-5.38595305356740546715e-04, // 0xBF41A6109C73E0EC
	3.15632070903625950361e-04,  // 0x3F34AF6D6C0EBBF7
	-3.12754168375120860518e-04, // 0xBF347F24ECC38C38
	3.35529192635519073543e-04,  // 0x3F35FD3EE8C2D3F4
}
var _lgamU = [...]float64{
	-7.72156649015328655494e-02, // 0xBFB3C467E37DB0C8
	6.32827064025093366517e-01,  // 0x3FE4401E8B005DFF
	1.45492250137234768737e+00,  // 0x3FF7475CD119BD6F
	9.77717527963372745603e-01,  // 0x3FEF497644EA8450
	2.28963728064692451092e-01,  // 0x3FCD4EAEF6010924
	1.33810918536787660377e-02,  // 0x3F8B678BBF2BAB09
}
var _lgamV = [...]float64{
	1.0,
	2.45597793713041134822e+00, // 0x4003A5D7C2BD619C
	2.12848976379893395361e+00, // 0x40010725A42B18F5
	7.69285150456672783825e-01, // 0x3FE89DFBE45050AF
	1.04222645593369134254e-01, // 0x3FBAAE55D6537C88
	3.21709242282423911810e-03, // 0x3F6A5ABB57D0CF61
}
var _lgamW = [...]float64{
	4.18938533204672725052e-01,  // 0x3FDACFE390C97D69
	8.33333333333329678849e-02,  // 0x3FB555555555553B
	-2.77777777728775536470e-03, // 0xBF66C16C16B02E5C
	7.93650558643019558500e-04,  // 0x3F4A019F98CF38B6
	-5.95187557450339963135e-04, // 0xBF4380CB8C0FE741
	8.36339918996282139126e-04,  // 0x3F4B67BA4CDAD5D1
	-1.63092934096575273989e-03, // 0xBF5AB89D0B9E43E4
}

// Lgamma 返回 [Gamma](x) 的自然对数和符号（-1 或 +1）。
//
// 特殊情况为：
//
//	Lgamma(+Inf) = +Inf
//	Lgamma(0) = +Inf
//	Lgamma(-integer) = +Inf
//	Lgamma(-Inf) = -Inf
//	Lgamma(NaN) = NaN
func Lgamma(x float64) (lgamma float64, sign int) {
	const (
		Ymin  = 1.461632144968362245
		Two52 = 1 << 52                     // 0x4330000000000000 ~4.5036e+15
		Two53 = 1 << 53                     // 0x4340000000000000 ~9.0072e+15
		Two58 = 1 << 58                     // 0x4390000000000000 ~2.8823e+17
		Tiny  = 1.0 / (1 << 70)             // 0x3b90000000000000 ~8.47033e-22
		Tc    = 1.46163214496836224576e+00  // 0x3FF762D86356BE3F
		Tf    = -1.21486290535849611461e-01 // 0xBFBF19B9BCC38A42
		// Tt = -(Tf 的尾部)
		Tt = -3.63867699703950536541e-18 // 0xBC50C7CAA48A971F
	)
	// 特殊情况
	sign = 1
	switch {
	case IsNaN(x):
		lgamma = x
		return
	case IsInf(x, 0):
		lgamma = x
		return
	case x == 0:
		lgamma = Inf(1)
		return
	}

	neg := false
	if x < 0 {
		x = -x
		neg = true
	}

	if x < Tiny { // 若 |x| < 2**-70，返回 -log(|x|)
		if neg {
			sign = -1
		}
		lgamma = -Log(x)
		return
	}
	var nadj float64
	if neg {
		if x >= Two52 { // |x| >= 2**52，必为负整数
			lgamma = Inf(1)
			return
		}
		t := sinPi(x)
		if t == 0 {
			lgamma = Inf(1) // 负整数
			return
		}
		nadj = Log(Pi / Abs(t*x))
		if t < 0 {
			sign = -1
		}
	}

	switch {
	case x == 1 || x == 2: // 剔除 1 和 2
		lgamma = 0
		return
	case x < 2: // 使用 lgamma(x) = lgamma(x+1) - log(x)
		var y float64
		var i int
		if x <= 0.9 {
			lgamma = -Log(x)
			switch {
			case x >= (Ymin - 1 + 0.27): // 0.7316 <= x <=  0.9
				y = 1 - x
				i = 0
			case x >= (Ymin - 1 - 0.27): // 0.2316 <= x < 0.7316
				y = x - (Tc - 1)
				i = 1
			default: // 0 < x < 0.2316
				y = x
				i = 2
			}
		} else {
			lgamma = 0
			switch {
			case x >= (Ymin + 0.27): // 1.7316 <= x < 2
				y = 2 - x
				i = 0
			case x >= (Ymin - 0.27): // 1.2316 <= x < 1.7316
				y = x - Tc
				i = 1
			default: // 0.9 < x < 1.2316
				y = x - 1
				i = 2
			}
		}
		switch i {
		case 0:
			z := y * y
			p1 := _lgamA[0] + z*(_lgamA[2]+z*(_lgamA[4]+z*(_lgamA[6]+z*(_lgamA[8]+z*_lgamA[10]))))
			p2 := z * (_lgamA[1] + z*(+_lgamA[3]+z*(_lgamA[5]+z*(_lgamA[7]+z*(_lgamA[9]+z*_lgamA[11])))))
			p := y*p1 + p2
			lgamma += (p - 0.5*y)
		case 1:
			z := y * y
			w := z * y
			p1 := _lgamT[0] + w*(_lgamT[3]+w*(_lgamT[6]+w*(_lgamT[9]+w*_lgamT[12]))) // 并行计算
			p2 := _lgamT[1] + w*(_lgamT[4]+w*(_lgamT[7]+w*(_lgamT[10]+w*_lgamT[13])))
			p3 := _lgamT[2] + w*(_lgamT[5]+w*(_lgamT[8]+w*(_lgamT[11]+w*_lgamT[14])))
			p := z*p1 - (Tt - w*(p2+y*p3))
			lgamma += (Tf + p)
		case 2:
			p1 := y * (_lgamU[0] + y*(_lgamU[1]+y*(_lgamU[2]+y*(_lgamU[3]+y*(_lgamU[4]+y*_lgamU[5])))))
			p2 := 1 + y*(_lgamV[1]+y*(_lgamV[2]+y*(_lgamV[3]+y*(_lgamV[4]+y*_lgamV[5]))))
			lgamma += (-0.5*y + p1/p2)
		}
	case x < 8: // 2 <= x < 8
		i := int(x)
		y := x - float64(i)
		p := y * (_lgamS[0] + y*(_lgamS[1]+y*(_lgamS[2]+y*(_lgamS[3]+y*(_lgamS[4]+y*(_lgamS[5]+y*_lgamS[6]))))))
		q := 1 + y*(_lgamR[1]+y*(_lgamR[2]+y*(_lgamR[3]+y*(_lgamR[4]+y*(_lgamR[5]+y*_lgamR[6])))))
		lgamma = 0.5*y + p/q
		z := 1.0 // Lgamma(1+s) = Log(s) + Lgamma(s)
		switch i {
		case 7:
			z *= (y + 6)
			fallthrough
		case 6:
			z *= (y + 5)
			fallthrough
		case 5:
			z *= (y + 4)
			fallthrough
		case 4:
			z *= (y + 3)
			fallthrough
		case 3:
			z *= (y + 2)
			lgamma += Log(z)
		}
	case x < Two58: // 8 <= x < 2**58
		t := Log(x)
		z := 1 / x
		y := z * z
		w := _lgamW[0] + z*(_lgamW[1]+y*(_lgamW[2]+y*(_lgamW[3]+y*(_lgamW[4]+y*(_lgamW[5]+y*_lgamW[6])))))
		lgamma = (x-0.5)*(t-1) + w
	default: // 2**58 <= x <= Inf
		lgamma = x * (Log(x) - 1)
	}
	if neg {
		lgamma = nadj - lgamma
	}
	return
}

// sinPi(x) 是用于负 x 的辅助函数
func sinPi(x float64) float64 {
	const (
		Two52 = 1 << 52 // 0x4330000000000000 ~4.5036e+15
		Two53 = 1 << 53 // 0x4340000000000000 ~9.0072e+15
	)
	if x < 0.25 {
		return -Sin(Pi * x)
	}

	// 参数归约
	z := Floor(x)
	var n int
	if z != x { // 非精确
		x = Mod(x, 2)
		n = int(x * 4)
	} else {
		if x >= Two53 { // x 必为偶数
			x = 0
			n = 0
		} else {
			if x < Two52 {
				z = x + Two52 // 精确
			}
			n = int(1 & Float64bits(z))
			x = float64(n)
			n <<= 2
		}
	}
	switch n {
	case 0:
		x = Sin(Pi * x)
	case 1, 2:
		x = Cos(Pi * (0.5 - x))
	case 3, 4:
		x = Sin(Pi * (1 - x))
	case 5, 6:
		x = -Cos(Pi * (x - 1.5))
	default:
		x = Sin(Pi * (x - 2))
	}
	return -x
}
