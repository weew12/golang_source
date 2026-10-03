// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmplx

import (
	"math"
	"math/bits"
)

// 原始 C 代码、长注释以及下面的常量
// 来自 http://netlib.sandia.gov/cephes/c9x-complex/clog.c。
// Go 代码是原始 C 代码的简化版本。
//
// Cephes Math Library Release 2.8:  June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
//
// http://netlib.sandia.gov/cephes/ 处的 readme 文件写道：
//    本档案中的某些软件可能来自 _Methods and
// Programs for Mathematical Functions_ 一书（Prentice-Hall 或 Simon & Schuster
// International，1989），或者来自 Cephes 数学库，一个
// 商业产品。无论哪种情况，其版权均归作者所有。
// 你可以自由使用在此看到的内容，但对此不提供任何支持或
// 担保。
//
//   书中已知的两处印刷错误已在此处修正，
// 涉及 gamma 函数和不完全 beta 积分的源代码清单。
//
//   Stephen L. Moshier
//   moshier@na-net.ornl.gov

// 复数正切
//
// 描述：
//
// 如果
//     z = x + iy,
//
// 那么
//
//           sin 2x  +  i sinh 2y
//     w  =  --------------------.
//            cos 2x  +  cosh 2y
//
// 在实轴上，分母在 PI/2 的奇数倍处为零。
// 在这些点附近，分母由其泰勒级数计算。
//
// ctan(z) = -i ctanh(iz).
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10      5200       7.1e-17     1.6e-17
//    IEEE      -10,+10     30000       7.2e-16     1.2e-16
// 也通过 ctan * ccot = 1 和 catan(ctan(z))  =  z 进行了测试。

// Tan 返回 x 的正切。
func Tan(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case math.IsInf(im, 0):
		switch {
		case math.IsInf(re, 0) || math.IsNaN(re):
			return complex(math.Copysign(0, re), math.Copysign(1, im))
		}
		return complex(math.Copysign(0, math.Sin(2*re)), math.Copysign(1, im))
	case re == 0 && math.IsNaN(im):
		return x
	}
	d := math.Cos(2*real(x)) + math.Cosh(2*imag(x))
	if math.Abs(d) < 0.25 {
		d = tanSeries(x)
	}
	if d == 0 {
		return Inf()
	}
	return complex(math.Sin(2*real(x))/d, math.Sinh(2*imag(x))/d)
}

// 复数双曲正切
//
// 描述：
//
// tanh z = (sinh 2x  +  i sin 2y) / (cosh 2x + cos 2y) .
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    IEEE      -10,+10     30000       1.7e-14     2.4e-16

// Tanh 返回 x 的双曲正切。
func Tanh(x complex128) complex128 {
	switch re, im := real(x), imag(x); {
	case math.IsInf(re, 0):
		switch {
		case math.IsInf(im, 0) || math.IsNaN(im):
			return complex(math.Copysign(1, re), math.Copysign(0, im))
		}
		return complex(math.Copysign(1, re), math.Copysign(0, math.Sin(2*im)))
	case im == 0 && math.IsNaN(re):
		return x
	}
	d := math.Cosh(2*real(x)) + math.Cos(2*imag(x))
	if d == 0 {
		return Inf()
	}
	return complex(math.Sinh(2*real(x))/d, math.Sin(2*imag(x))/d)
}

// reducePi 将输入参数 x 归约到区间 (-Pi/2, Pi/2]。
// x 必须大于或等于 0。对于较小的参数，它
// 使用基于以下文献的 Cody-Waite 3 个 float64 部分归约：
// "Elementary Function Evaluation:  Algorithms and Implementation"
// Jean-Michel Muller, 1997。
// 对于非常大的参数，它使用基于以下文献的 Payne-Hanek 范围归约：
// "ARGUMENT REDUCTION FOR HUGE ARGUMENTS: Good to the Last Bit"
// K. C. Ng et al, March 24, 1992。
func reducePi(x float64) float64 {
	// reduceThreshold 是使用 Cody-Waite 归约仍能给出准确结果的
	// x 的最大值。该阈值由 t*PIn 能够无误差地表示为 float64 决定，
	// 其中 t 由 t = floor(x * (1 / Pi)) 给出，PIn 是 Pi 的前导部分项。
	// 由于下面的前导项 PI1 和 PI2 分别具有 30 和 32 个末尾零位，
	// t 的有效位应少于 30 位。
	//	t < 1<<30  -> floor(x*(1/Pi)+0.5) < 1<<30 -> x < (1<<30-1) * Pi - 0.5
	// 因此，保守地我们可以取 x < 1<<30。
	const reduceThreshold float64 = 1 << 30
	if math.Abs(x) < reduceThreshold {
		// 使用三部分进行 Cody-Waite 归约。
		const (
			// PI1、PI2 和 PI3 组成 PI 的扩展精度值，
			// 使得 PI ~= PI1 + PI2 + PI3。这些部分的选取使得
			// PI1 和 PI2 具有大致相等数量的末尾零位。
			// 这确保了对于较大的整数 t，t*PI1 和 t*PI2 是精确的。
			// 全精度的 PI3 确保 PI 的近似精确到 102 位，以处理
			// 减法过程中的抵消。
			PI1 = 3.141592502593994      // 0x400921fb40000000
			PI2 = 1.5099578831723193e-07 // 0x3e84442d00000000
			PI3 = 1.0780605716316238e-14 // 0x3d08469898cc5170
		)
		t := x / math.Pi
		t += 0.5
		t = float64(int64(t)) // int64(t) = 该倍数
		return ((x - t*PI1) - t*PI2) - t*PI3
	}
	// 必须应用 Payne-Hanek 范围归约
	const (
		mask     = 0x7FF
		shift    = 64 - 11 - 1
		bias     = 1023
		fracMask = 1<<shift - 1
	)
	// 提取整数和指数，使得
	// x = ix * 2 ** exp。
	ix := math.Float64bits(x)
	exp := int(ix>>shift&mask) - bias - shift
	ix &= fracMask
	ix |= 1 << shift

	// mPi 是 1/Pi 的二进制位，表示为 uint64 数组，
	// 即 1/Pi = Sum mPi[i]*2^(-64*i)。
	// 19 个 64 位数字提供 1216 位精度，
	// 以处理可能的最大 float64 指数。
	var mPi = [...]uint64{
		0x0000000000000000,
		0x517cc1b727220a94,
		0xfe13abe8fa9a6ee0,
		0x6db14acc9e21c820,
		0xff28b1d5ef5de2b0,
		0xdb92371d2126e970,
		0x0324977504e8c90e,
		0x7f0ef58e5894d39f,
		0x74411afa975da242,
		0x74ce38135a2fbf20,
		0x9cc8eb1cc1a99cfa,
		0x4e422fc5defc941d,
		0x8ffc4bffef02cc07,
		0xf79788c5ad05368f,
		0xb69b3f6793e584db,
		0xa7a31fb34f2ff516,
		0xba93dd63f5f2f8bd,
		0x9e839cfbc5294975,
		0x35fdafd88fc6ae84,
		0x2b0198237e3db5d5,
	}
	// 使用指数从 mPi 中提取 3 个合适的 uint64 数字，
	// B ~ (z0, z1, z2)，使得乘积的前导数字具有指数 -64。
	// 注意，由于 x >= reduceThreshold，exp >= 50，而对于最大 float64，exp < 971。
	digit, bitshift := uint(exp+64)/64, uint(exp+64)%64
	z0 := (mPi[digit] << bitshift) | (mPi[digit+1] >> (64 - bitshift))
	z1 := (mPi[digit+1] << bitshift) | (mPi[digit+2] >> (64 - bitshift))
	z2 := (mPi[digit+2] << bitshift) | (mPi[digit+3] >> (64 - bitshift))
	// 将尾数乘以这些数字，并提取高两位数字 (hi, lo)。
	z2hi, _ := bits.Mul64(z2, ix)
	z1hi, z1lo := bits.Mul64(z1, ix)
	z0lo := z0 * ix
	lo, c := bits.Add64(z1lo, z2hi, 0)
	hi, _ := bits.Add64(z0lo, z1hi, c)
	// 求该分数的大小。
	lz := uint(bits.LeadingZeros64(hi))
	e := uint64(bias - (lz + 1))
	// 清除隐含的尾数位并移位到位。
	hi = (hi << (lz + 1)) | (lo >> (64 - (lz + 1)))
	hi >>= 64 - shift
	// 包含指数并转换为浮点数。
	hi |= e << shift
	x = math.Float64frombits(hi)
	// 映射到 (-Pi/2, Pi/2]
	if x > 0.5 {
		x--
	}
	return math.Pi * x
}

// cosh(2y) - cos(2x) 的泰勒级数展开
func tanSeries(z complex128) float64 {
	const MACHEP = 1.0 / (1 << 53)
	x := math.Abs(2 * real(z))
	y := math.Abs(2 * imag(z))
	x = reducePi(x)
	x = x * x
	y = y * y
	x2 := 1.0
	y2 := 1.0
	f := 1.0
	rn := 0.0
	d := 0.0
	for {
		rn++
		f *= rn
		rn++
		f *= rn
		x2 *= x
		y2 *= y
		t := y2 + x2
		t /= f
		d += t

		rn++
		f *= rn
		rn++
		f *= rn
		x2 *= x
		y2 *= y
		t = y2 - x2
		t /= f
		d += t
		if !(math.Abs(t/d) > MACHEP) {
			// 注意：如果 t/d 是 NaN，为了行为正确，请使用 ! 和 > 而不是 <=。
			// 参见 issue 17577。
			break
		}
	}
	return d
}

// 复数余切
//
// 描述：
//
// 如果
//     z = x + iy,
//
// 那么
//
//           sin 2x  -  i sinh 2y
//     w  =  --------------------.
//            cosh 2y  -  cos 2x
//
// 在实轴上，分母在 PI/2 的偶数倍处有零点。
// 在这些点附近，它由泰勒级数计算。
//
// 精度：
//
//                      相对误差：
// 算术         定义域     试验次数      峰值         均方根
//    DEC       -10,+10      3000       6.5e-17     1.6e-17
//    IEEE      -10,+10     30000       9.2e-16     1.2e-16
// 也通过 ctan * ccot = 1 + i0 进行了测试。

// Cot 返回 x 的余切。
func Cot(x complex128) complex128 {
	d := math.Cosh(2*imag(x)) - math.Cos(2*real(x))
	if math.Abs(d) < 0.25 {
		d = tanSeries(x)
	}
	if d == 0 {
		return Inf()
	}
	return complex(math.Sin(2*real(x))/d, -math.Sinh(2*imag(x))/d)
}
