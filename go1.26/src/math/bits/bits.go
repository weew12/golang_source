// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run make_tables.go

// Package bits 实现了针对预声明无符号整数类型的位计数和位操作函数。
//
// 本包中的函数可能由编译器直接实现，以获得更好的性能。对于这些函数，
// 本包中的代码将不会被使用。哪些函数由编译器实现取决于架构和 Go 版本。
package bits

const uintSize = 32 << (^uint(0) >> 63) // 32 or 64

// UintSize 是 uint 的大小（以位为单位）。
const UintSize = uintSize

// --- LeadingZeros ---

// LeadingZeros 返回 x 中前导零位的个数；当 x == 0 时结果为 [UintSize]。
func LeadingZeros(x uint) int { return UintSize - Len(x) }

// LeadingZeros8 返回 x 中前导零位的个数；当 x == 0 时结果为 8。
func LeadingZeros8(x uint8) int { return 8 - Len8(x) }

// LeadingZeros16 返回 x 中前导零位的个数；当 x == 0 时结果为 16。
func LeadingZeros16(x uint16) int { return 16 - Len16(x) }

// LeadingZeros32 返回 x 中前导零位的个数；当 x == 0 时结果为 32。
func LeadingZeros32(x uint32) int { return 32 - Len32(x) }

// LeadingZeros64 返回 x 中前导零位的个数；当 x == 0 时结果为 64。
func LeadingZeros64(x uint64) int { return 64 - Len64(x) }

// --- TrailingZeros ---

// 参见 http://keithandkatie.com/keith/papers/debruijn.html
const deBruijn32 = 0x077CB531

var deBruijn32tab = [32]byte{
	0, 1, 28, 2, 29, 14, 24, 3, 30, 22, 20, 15, 25, 17, 4, 8,
	31, 27, 13, 23, 21, 19, 16, 7, 26, 12, 18, 6, 11, 5, 10, 9,
}

const deBruijn64 = 0x03f79d71b4ca8b09

var deBruijn64tab = [64]byte{
	0, 1, 56, 2, 57, 49, 28, 3, 61, 58, 42, 50, 38, 29, 17, 4,
	62, 47, 59, 36, 45, 43, 51, 22, 53, 39, 33, 30, 24, 18, 12, 5,
	63, 55, 48, 27, 60, 41, 37, 16, 46, 35, 44, 21, 52, 32, 23, 11,
	54, 26, 40, 15, 34, 20, 31, 10, 25, 14, 19, 9, 13, 8, 7, 6,
}

// TrailingZeros 返回 x 中末尾零位的个数；当 x == 0 时结果为 [UintSize]。
func TrailingZeros(x uint) int {
	if UintSize == 32 {
		return TrailingZeros32(uint32(x))
	}
	return TrailingZeros64(uint64(x))
}

// TrailingZeros8 返回 x 中末尾零位的个数；当 x == 0 时结果为 8。
func TrailingZeros8(x uint8) int {
	return int(ntz8tab[x])
}

// TrailingZeros16 返回 x 中末尾零位的个数；当 x == 0 时结果为 16。
func TrailingZeros16(x uint16) int {
	if x == 0 {
		return 16
	}
	// 参见 TrailingZeros64 中的注释
	return int(deBruijn32tab[uint32(x&-x)*deBruijn32>>(32-5)])
}

// TrailingZeros32 返回 x 中末尾零位的个数；当 x == 0 时结果为 32。
func TrailingZeros32(x uint32) int {
	if x == 0 {
		return 32
	}
	// 参见 TrailingZeros64 中的注释
	return int(deBruijn32tab[(x&-x)*deBruijn32>>(32-5)])
}

// TrailingZeros64 返回 x 中末尾零位的个数；当 x == 0 时结果为 64。
func TrailingZeros64(x uint64) int {
	if x == 0 {
		return 64
	}
	// 如果 popcount 很快，将下面的代码替换为 return popcount(^x & (x - 1))。
	//
	// x & -x 只保留字中最右边的置位。设 k 为该位的索引。
	// 由于只设置了一个位，该值即 2 的 k 次幂。乘以 2 的幂
	// 等价于左移，在此情况下是左移 k 位。de Bruijn（64 位）常量
	// 的选取使得所有六个位的连续子串都互不相同。
	// 因此，如果我们将该常量左移，就可以通过观察最终位于字顶部的
	// 那个六位子串来确定它被移动了多少位。
	// (Knuth，第 4 卷，7.3.1 节)
	return int(deBruijn64tab[(x&-x)*deBruijn64>>(64-6)])
}

// --- OnesCount ---

const m0 = 0x5555555555555555 // 01010101 ...
const m1 = 0x3333333333333333 // 00110011 ...
const m2 = 0x0f0f0f0f0f0f0f0f // 00001111 ...
const m3 = 0x00ff00ff00ff00ff // etc.
const m4 = 0x0000ffff0000ffff

// OnesCount 返回 x 中 1 位的个数（"population count"）。
func OnesCount(x uint) int {
	if UintSize == 32 {
		return OnesCount32(uint32(x))
	}
	return OnesCount64(uint64(x))
}

// OnesCount8 返回 x 中 1 位的个数（"population count"）。
func OnesCount8(x uint8) int {
	return int(pop8tab[x])
}

// OnesCount16 返回 x 中 1 位的个数（"population count"）。
func OnesCount16(x uint16) int {
	return int(pop8tab[x>>8] + pop8tab[x&0xff])
}

// OnesCount32 返回 x 中 1 位的个数（"population count"）。
func OnesCount32(x uint32) int {
	return int(pop8tab[x>>24] + pop8tab[x>>16&0xff] + pop8tab[x>>8&0xff] + pop8tab[x&0xff])
}

// OnesCount64 返回 x 中 1 位的个数（"population count"）。
func OnesCount64(x uint64) int {
	// 实现：对相邻位进行并行求和。
	// 参见《Hacker's Delight》第 5 章：Counting Bits。
	// 以下模式展示了总体思路：
	//
	//   x = x>>1&(m0&m) + x&(m0&m)
	//   x = x>>2&(m1&m) + x&(m1&m)
	//   x = x>>4&(m2&m) + x&(m2&m)
	//   x = x>>8&(m3&m) + x&(m3&m)
	//   x = x>>16&(m4&m) + x&(m4&m)
	//   x = x>>32&(m5&m) + x&(m5&m)
	//   return int(x)
	//
	// 当字段之和没有进位到下一个字段的风险时，可以省略掩码（& 运算）：
	// 由于结果不可能 > 64，8 位就足够了，因此对于移位 8 及以上的操作
	// 我们可以忽略掩码。
	// 根据《Hacker's Delight》，第一行还可以进一步简化，但最多只能节省
	// 一条指令，因此为了清晰起见我们保持原样。
	const m = 1<<64 - 1
	x = x>>1&(m0&m) + x&(m0&m)
	x = x>>2&(m1&m) + x&(m1&m)
	x = (x>>4 + x) & (m2 & m)
	x += x >> 8
	x += x >> 16
	x += x >> 32
	return int(x) & (1<<7 - 1)
}

// --- RotateLeft ---

// RotateLeft 返回 x 循环左移 (k mod [UintSize]) 位后的值。
// 要将 x 循环右移 k 位，请调用 RotateLeft(x, -k)。
//
// 此函数的执行时间不依赖于输入。
func RotateLeft(x uint, k int) uint {
	if UintSize == 32 {
		return uint(RotateLeft32(uint32(x), k))
	}
	return uint(RotateLeft64(uint64(x), k))
}

// RotateLeft8 返回 x 循环左移 (k mod 8) 位后的值。
// 要将 x 循环右移 k 位，请调用 RotateLeft8(x, -k)。
//
// 此函数的执行时间不依赖于输入。
func RotateLeft8(x uint8, k int) uint8 {
	const n = 8
	s := uint(k) & (n - 1)
	return x<<s | x>>(n-s)
}

// RotateLeft16 返回 x 循环左移 (k mod 16) 位后的值。
// 要将 x 循环右移 k 位，请调用 RotateLeft16(x, -k)。
//
// 此函数的执行时间不依赖于输入。
func RotateLeft16(x uint16, k int) uint16 {
	const n = 16
	s := uint(k) & (n - 1)
	return x<<s | x>>(n-s)
}

// RotateLeft32 返回 x 循环左移 (k mod 32) 位后的值。
// 要将 x 循环右移 k 位，请调用 RotateLeft32(x, -k)。
//
// 此函数的执行时间不依赖于输入。
func RotateLeft32(x uint32, k int) uint32 {
	const n = 32
	s := uint(k) & (n - 1)
	return x<<s | x>>(n-s)
}

// RotateLeft64 返回 x 循环左移 (k mod 64) 位后的值。
// 要将 x 循环右移 k 位，请调用 RotateLeft64(x, -k)。
//
// 此函数的执行时间不依赖于输入。
func RotateLeft64(x uint64, k int) uint64 {
	const n = 64
	s := uint(k) & (n - 1)
	return x<<s | x>>(n-s)
}

// --- Reverse ---

// Reverse 返回将 x 的位按相反顺序排列后的值。
func Reverse(x uint) uint {
	if UintSize == 32 {
		return uint(Reverse32(uint32(x)))
	}
	return uint(Reverse64(uint64(x)))
}

// Reverse8 返回将 x 的位按相反顺序排列后的值。
func Reverse8(x uint8) uint8 {
	return rev8tab[x]
}

// Reverse16 返回将 x 的位按相反顺序排列后的值。
func Reverse16(x uint16) uint16 {
	return uint16(rev8tab[x>>8]) | uint16(rev8tab[x&0xff])<<8
}

// Reverse32 返回将 x 的位按相反顺序排列后的值。
func Reverse32(x uint32) uint32 {
	const m = 1<<32 - 1
	x = x>>1&(m0&m) | x&(m0&m)<<1
	x = x>>2&(m1&m) | x&(m1&m)<<2
	x = x>>4&(m2&m) | x&(m2&m)<<4
	return ReverseBytes32(x)
}

// Reverse64 返回将 x 的位按相反顺序排列后的值。
func Reverse64(x uint64) uint64 {
	const m = 1<<64 - 1
	x = x>>1&(m0&m) | x&(m0&m)<<1
	x = x>>2&(m1&m) | x&(m1&m)<<2
	x = x>>4&(m2&m) | x&(m2&m)<<4
	return ReverseBytes64(x)
}

// --- ReverseBytes ---

// ReverseBytes 返回将 x 的字节按相反顺序排列后的值。
//
// 此函数的执行时间不依赖于输入。
func ReverseBytes(x uint) uint {
	if UintSize == 32 {
		return uint(ReverseBytes32(uint32(x)))
	}
	return uint(ReverseBytes64(uint64(x)))
}

// ReverseBytes16 返回将 x 的字节按相反顺序排列后的值。
//
// 此函数的执行时间不依赖于输入。
func ReverseBytes16(x uint16) uint16 {
	return x>>8 | x<<8
}

// ReverseBytes32 返回将 x 的字节按相反顺序排列后的值。
//
// 此函数的执行时间不依赖于输入。
func ReverseBytes32(x uint32) uint32 {
	const m = 1<<32 - 1
	x = x>>8&(m3&m) | x&(m3&m)<<8
	return x>>16 | x<<16
}

// ReverseBytes64 返回将 x 的字节按相反顺序排列后的值。
//
// 此函数的执行时间不依赖于输入。
func ReverseBytes64(x uint64) uint64 {
	const m = 1<<64 - 1
	x = x>>8&(m3&m) | x&(m3&m)<<8
	x = x>>16&(m4&m) | x&(m4&m)<<16
	return x>>32 | x<<32
}

// --- Len ---

// Len 返回表示 x 所需的最小位数；当 x == 0 时结果为 0。
func Len(x uint) int {
	if UintSize == 32 {
		return Len32(uint32(x))
	}
	return Len64(uint64(x))
}

// Len8 返回表示 x 所需的最小位数；当 x == 0 时结果为 0。
func Len8(x uint8) int {
	return int(len8tab[x])
}

// Len16 返回表示 x 所需的最小位数；当 x == 0 时结果为 0。
func Len16(x uint16) (n int) {
	if x >= 1<<8 {
		x >>= 8
		n = 8
	}
	return n + int(len8tab[uint8(x)])
}

// Len32 返回表示 x 所需的最小位数；当 x == 0 时结果为 0。
func Len32(x uint32) (n int) {
	if x >= 1<<16 {
		x >>= 16
		n = 16
	}
	if x >= 1<<8 {
		x >>= 8
		n += 8
	}
	return n + int(len8tab[uint8(x)])
}

// Len64 返回表示 x 所需的最小位数；当 x == 0 时结果为 0。
func Len64(x uint64) (n int) {
	if x >= 1<<32 {
		x >>= 32
		n = 32
	}
	if x >= 1<<16 {
		x >>= 16
		n += 16
	}
	if x >= 1<<8 {
		x >>= 8
		n += 8
	}
	return n + int(len8tab[uint8(x)])
}

// --- 带进位的加法 ---

// Add 返回 x、y 与 carry 的带进位和：sum = x + y + carry。
// carry 输入必须为 0 或 1；否则行为未定义。
// carryOut 输出保证为 0 或 1。
//
// 此函数的执行时间不依赖于输入。
func Add(x, y, carry uint) (sum, carryOut uint) {
	if UintSize == 32 {
		s32, c32 := Add32(uint32(x), uint32(y), uint32(carry))
		return uint(s32), uint(c32)
	}
	s64, c64 := Add64(uint64(x), uint64(y), uint64(carry))
	return uint(s64), uint(c64)
}

// Add32 返回 x、y 与 carry 的带进位和：sum = x + y + carry。
// carry 输入必须为 0 或 1；否则行为未定义。
// carryOut 输出保证为 0 或 1。
//
// 此函数的执行时间不依赖于输入。
func Add32(x, y, carry uint32) (sum, carryOut uint32) {
	sum64 := uint64(x) + uint64(y) + uint64(carry)
	sum = uint32(sum64)
	carryOut = uint32(sum64 >> 32)
	return
}

// Add64 返回 x、y 与 carry 的带进位和：sum = x + y + carry。
// carry 输入必须为 0 或 1；否则行为未定义。
// carryOut 输出保证为 0 或 1。
//
// 此函数的执行时间不依赖于输入。
func Add64(x, y, carry uint64) (sum, carryOut uint64) {
	sum = x + y + carry
	// 如果两个最高位都置位 (x & y)，或者其中之一置位 (x | y) 且来自低位的
	// 进位发生，则和将会溢出。如果这样的进位发生，最高位将是
	// 1 + 0 + 1 = 0 (&^ sum)。
	carryOut = ((x & y) | ((x | y) &^ sum)) >> 63
	return
}

// --- 带借位的减法 ---

// Sub 返回 x、y 与 borrow 的带借位差：diff = x - y - borrow。
// borrow 输入必须为 0 或 1；否则行为未定义。
// borrowOut 输出保证为 0 或 1。
//
// 此函数的执行时间不依赖于输入。
func Sub(x, y, borrow uint) (diff, borrowOut uint) {
	if UintSize == 32 {
		d32, b32 := Sub32(uint32(x), uint32(y), uint32(borrow))
		return uint(d32), uint(b32)
	}
	d64, b64 := Sub64(uint64(x), uint64(y), uint64(borrow))
	return uint(d64), uint(b64)
}

// Sub32 返回 x、y 与 borrow 的带借位差，diff = x - y - borrow。
// borrow 输入必须为 0 或 1；否则行为未定义。
// borrowOut 输出保证为 0 或 1。
//
// 此函数的执行时间不依赖于输入。
func Sub32(x, y, borrow uint32) (diff, borrowOut uint32) {
	diff = x - y - borrow
	// 如果 x 的最高位未置位而 y 的最高位置位 (^x & y)，或者它们相同
	// (^(x ^ y)) 且来自低位的借位发生，则差将会下溢。如果该借位发生，
	// 结果将是 1 - 1 - 1 = 0 - 0 - 1 = 1 (& diff)。
	borrowOut = ((^x & y) | (^(x ^ y) & diff)) >> 31
	return
}

// Sub64 返回 x、y 与 borrow 的带借位差：diff = x - y - borrow。
// borrow 输入必须为 0 或 1；否则行为未定义。
// borrowOut 输出保证为 0 或 1。
//
// 此函数的执行时间不依赖于输入。
func Sub64(x, y, borrow uint64) (diff, borrowOut uint64) {
	diff = x - y - borrow
	// 位运算逻辑参见 Sub32。
	borrowOut = ((^x & y) | (^(x ^ y) & diff)) >> 63
	return
}

// --- 全宽乘法 ---

// Mul 返回 x 与 y 的全宽乘积：(hi, lo) = x * y，
// 乘积位的高半部分在 hi 中返回，低半部分在 lo 中返回。
//
// 此函数的执行时间不依赖于输入。
func Mul(x, y uint) (hi, lo uint) {
	if UintSize == 32 {
		h, l := Mul32(uint32(x), uint32(y))
		return uint(h), uint(l)
	}
	h, l := Mul64(uint64(x), uint64(y))
	return uint(h), uint(l)
}

// Mul32 返回 x 与 y 的 64 位乘积：(hi, lo) = x * y，
// 乘积位的高半部分在 hi 中返回，低半部分在 lo 中返回。
//
// 此函数的执行时间不依赖于输入。
func Mul32(x, y uint32) (hi, lo uint32) {
	tmp := uint64(x) * uint64(y)
	hi, lo = uint32(tmp>>32), uint32(tmp)
	return
}

// Mul64 返回 x 与 y 的 128 位乘积：(hi, lo) = x * y，
// 乘积位的高半部分在 hi 中返回，低半部分在 lo 中返回。
//
// 此函数的执行时间不依赖于输入。
func Mul64(x, y uint64) (hi, lo uint64) {
	const mask32 = 1<<32 - 1
	x0 := x & mask32
	x1 := x >> 32
	y0 := y & mask32
	y1 := y >> 32
	w0 := x0 * y0
	t := x1*y0 + w0>>32
	w1 := t & mask32
	w2 := t >> 32
	w1 += x0 * y1
	hi = x1*y1 + w2 + w1>>32
	lo = x * y
	return
}

// --- 全宽除法 ---

// Div 返回 (hi, lo) 除以 y 的商和余数：
// quo = (hi, lo)/y，rem = (hi, lo)%y，其中被除数位的高半部分
// 位于参数 hi 中，低半部分位于参数 lo 中。
// 当 y == 0（除以零）或 y <= hi（商溢出）时，Div 会 panic。
func Div(hi, lo, y uint) (quo, rem uint) {
	if UintSize == 32 {
		q, r := Div32(uint32(hi), uint32(lo), uint32(y))
		return uint(q), uint(r)
	}
	q, r := Div64(uint64(hi), uint64(lo), uint64(y))
	return uint(q), uint(r)
}

// Div32 返回 (hi, lo) 除以 y 的商和余数：
// quo = (hi, lo)/y，rem = (hi, lo)%y，其中被除数位的高半部分
// 位于参数 hi 中，低半部分位于参数 lo 中。
// 当 y == 0（除以零）或 y <= hi（商溢出）时，Div32 会 panic。
func Div32(hi, lo, y uint32) (quo, rem uint32) {
	if y != 0 && y <= hi {
		panic(overflowError)
	}
	z := uint64(hi)<<32 | uint64(lo)
	quo, rem = uint32(z/uint64(y)), uint32(z%uint64(y))
	return
}

// Div64 返回 (hi, lo) 除以 y 的商和余数：
// quo = (hi, lo)/y，rem = (hi, lo)%y，其中被除数位的高半部分
// 位于参数 hi 中，低半部分位于参数 lo 中。
// 当 y == 0（除以零）或 y <= hi（商溢出）时，Div64 会 panic。
func Div64(hi, lo, y uint64) (quo, rem uint64) {
	if y == 0 {
		panic(divideError)
	}
	if y <= hi {
		panic(overflowError)
	}

	// 如果高位部分为零，我们可以直接返回结果。
	if hi == 0 {
		return lo / y, lo % y
	}

	s := uint(LeadingZeros64(y))
	y <<= s

	const (
		two32  = 1 << 32
		mask32 = two32 - 1
	)
	yn1 := y >> 32
	yn0 := y & mask32
	un32 := hi<<s | lo>>(64-s)
	un10 := lo << s
	un1 := un10 >> 32
	un0 := un10 & mask32
	q1 := un32 / yn1
	rhat := un32 - q1*yn1

	for q1 >= two32 || q1*yn0 > two32*rhat+un1 {
		q1--
		rhat += yn1
		if rhat >= two32 {
			break
		}
	}

	un21 := un32*two32 + un1 - q1*y
	q0 := un21 / yn1
	rhat = un21 - q0*yn1

	for q0 >= two32 || q0*yn0 > two32*rhat+un0 {
		q0--
		rhat += yn1
		if rhat >= two32 {
			break
		}
	}

	return q1*two32 + q0, (un21*two32 + un0 - q0*y) >> s
}

// Rem 返回 (hi, lo) 除以 y 的余数。当 y == 0（除以零）时 Rem 会 panic，
// 但与 Div 不同，它在商溢出时不会 panic。
func Rem(hi, lo, y uint) uint {
	if UintSize == 32 {
		return uint(Rem32(uint32(hi), uint32(lo), uint32(y)))
	}
	return uint(Rem64(uint64(hi), uint64(lo), uint64(y)))
}

// Rem32 返回 (hi, lo) 除以 y 的余数。当 y == 0（除以零）时 Rem32 会 panic，
// 但与 [Div32] 不同，它在商溢出时不会 panic。
func Rem32(hi, lo, y uint32) uint32 {
	return uint32((uint64(hi)<<32 | uint64(lo)) % uint64(y))
}

// Rem64 返回 (hi, lo) 除以 y 的余数。当 y == 0（除以零）时 Rem64 会 panic，
// 但与 [Div64] 不同，它在商溢出时不会 panic。
func Rem64(hi, lo, y uint64) uint64 {
	// 我们将 hi 缩小以使 hi < y，然后使用 Div64 计算余数，
	// 并保证它不会因商溢出而 panic。
	// 鉴于
	//   hi ≡ hi%y    (mod y)
	// 我们有
	//   hi<<64 + lo ≡ (hi%y)<<64 + lo    (mod y)
	_, rem := Div64(hi%y, lo, y)
	return rem
}
