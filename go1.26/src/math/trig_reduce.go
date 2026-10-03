// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

import (
	"math/bits"
)

// reduceThreshold 是使用 Pi/4 分三部分进行归约仍能给出
// 准确结果时 x 的最大值。该阈值
// 由 y*C 能够无误差地表示为 float64 决定，
// 其中 y 由 y = floor(x * (4 / Pi)) 给出，而 C 是 4/Pi 的前导部分
// 项。由于前导项（sin.go 中的 PI4A 和 PI4B）有 30
// 和 32 个尾随零位，因此 y 的有效位数应少于 30 位。
//
//	y < 1<<30  -> floor(x*4/Pi) < 1<<30 -> x < (1<<30 - 1) * Pi/4
//
// 因此，保守地可以取 x < 1<<29。
// 超过此阈值则必须使用 Payne-Hanek 参数归约。
const reduceThreshold = 1 << 29

// trigReduce 对 x > 0 实现按 Pi/4 的 Payne-Hanek 参数归约。
// 它返回 x / (Pi/4) 的整数部分模 8 (j) 和
// 小数部分 (z)。
// 实现基于：
// "ARGUMENT REDUCTION FOR HUGE ARGUMENTS: Good to the Last Bit"
// K. C. Ng et al, March 24, 1992
// x*B 的模拟多精度计算使用 64 位整数运算。
func trigReduce(x float64) (j uint64, z float64) {
	const PI4 = Pi / 4
	if x < PI4 {
		return 0, x
	}
	// 提取出整数和指数，使得，
	// x = ix * 2 ** exp.
	ix := Float64bits(x)
	exp := int(ix>>shift&mask) - bias - shift
	ix &^= mask << shift
	ix |= 1 << shift
	// 使用该指数从 mPi4 中提取 3 个合适的 uint64 数字，
	// B ~ (z0, z1, z2)，使得乘积的前导数字具有指数 -61。
	// 注意，由于 x >= PI4 且对于最大 float64 有 exp < 971，故 exp >= -53。
	digit, bitshift := uint(exp+61)/64, uint(exp+61)%64
	z0 := (mPi4[digit] << bitshift) | (mPi4[digit+1] >> (64 - bitshift))
	z1 := (mPi4[digit+1] << bitshift) | (mPi4[digit+2] >> (64 - bitshift))
	z2 := (mPi4[digit+2] << bitshift) | (mPi4[digit+3] >> (64 - bitshift))
	// 将尾数乘以这些数字并提取高两位数字 (hi, lo)。
	z2hi, _ := bits.Mul64(z2, ix)
	z1hi, z1lo := bits.Mul64(z1, ix)
	z0lo := z0 * ix
	lo, c := bits.Add64(z1lo, z2hi, 0)
	hi, _ := bits.Add64(z0lo, z1hi, c)
	// 最高 3 位是 j。
	j = hi >> 61
	// 提取小数部分并求其量级。
	hi = hi<<3 | lo>>61
	lz := uint(bits.LeadingZeros64(hi))
	e := uint64(bias - (lz + 1))
	// 清除隐含的尾数位并移位到位。
	hi = (hi << (lz + 1)) | (lo >> (64 - (lz + 1)))
	hi >>= 64 - shift
	// 包含指数并转换为浮点数。
	hi |= e << shift
	z = Float64frombits(hi)
	// 将零点映射到原点。
	if j&1 == 1 {
		j++
		j &= 7
		z--
	}
	// 将小数部分乘以 pi/4。
	return j, z * PI4
}

// mPi4 是 4/pi 的二进制数字，表示为 uint64 数组，
// 即 4/pi = Sum mPi4[i]*2^(-64*i)
// 19 个 64 位数字加上前导一位共给出 1217 位
// 精度，以处理最大的 float64 指数。
var mPi4 = [...]uint64{
	0x0000000000000001,
	0x45f306dc9c882a53,
	0xf84eafa3ea69bb81,
	0xb6c52b3278872083,
	0xfca2c757bd778ac3,
	0x6e48dc74849ba5c0,
	0x0c925dd413a32439,
	0xfc3bd63962534e7d,
	0xd1046bea5d768909,
	0xd338e04d68befc82,
	0x7323ac7306a673e9,
	0x3908bf177bf25076,
	0x3ff12fffbc0b301f,
	0xde5e2316b414da3e,
	0xda6cfd9e4f96136e,
	0x9e8c7ecd3cbfd45a,
	0xea4f758fd7cbe2f6,
	0x7a0e73ef14a525d4,
	0xd7f6bf623f1aba10,
	0xac06608df8f6d757,
}
