// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rand 实现了适用于模拟等任务的伪随机数生成器，
// 但不应用于安全敏感的工作。
//
// 随机数由 [Source] 生成，通常包装在 [Rand] 中。
// 这两种类型同一时间都应由单个 goroutine 使用：在多个
// goroutine 之间共享需要某种形式的同步。
//
// 顶层函数，例如 [Float64] 和 [Int]，
// 可安全地由多个 goroutine 并发使用。
//
// 无论以何种方式设置种子，本包的输出都很容易被预测。
// 对于适用于安全敏感工作的随机数，请参见
// [crypto/rand] 包。
package rand

import (
	"math/bits"
	_ "unsafe" // 用于 go:linkname
)

// Source 是范围 [0, 1<<64) 内均匀分布
// 伪随机 uint64 值的来源。
//
// Source 不能安全地由多个 goroutine 并发使用。
type Source interface {
	Uint64() uint64
}

// Rand 是一个随机数来源。
type Rand struct {
	src Source
}

// New 返回一个新的 Rand，它使用来自 src 的随机值
// 来生成其他随机值。
func New(src Source) *Rand {
	return &Rand{src: src}
}

// Int64 以 int64 返回一个非负的伪随机 63 位整数。
func (r *Rand) Int64() int64 { return int64(r.src.Uint64() &^ (1 << 63)) }

// Uint32 以 uint32 返回一个伪随机 32 位值。
func (r *Rand) Uint32() uint32 { return uint32(r.src.Uint64() >> 32) }

// Uint64 以 uint64 返回一个伪随机 64 位值。
func (r *Rand) Uint64() uint64 { return r.src.Uint64() }

// Int32 以 int32 返回一个非负的伪随机 31 位整数。
func (r *Rand) Int32() int32 { return int32(r.src.Uint64() >> 33) }

// Int 返回一个非负的伪随机 int。
func (r *Rand) Int() int { return int(uint(r.src.Uint64()) << 1 >> 1) }

// Uint 返回一个伪随机 uint。
func (r *Rand) Uint() uint { return uint(r.src.Uint64()) }

// Int64N 以 int64 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func (r *Rand) Int64N(n int64) int64 {
	if n <= 0 {
		panic("invalid argument to Int64N")
	}
	return int64(r.uint64n(uint64(n)))
}

// Uint64N 以 uint64 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n == 0，它会 panic。
func (r *Rand) Uint64N(n uint64) uint64 {
	if n == 0 {
		panic("invalid argument to Uint64N")
	}
	return r.uint64n(n)
}

// uint64n 是不做边界检查的 Uint64N 版本。
func (r *Rand) uint64n(n uint64) uint64 {
	if is32bit && uint64(uint32(n)) == n {
		return uint64(r.uint32n(uint32(n)))
	}
	if n&(n-1) == 0 { // n 是 2 的幂，可以掩码
		return r.Uint64() & (n - 1)
	}

	// 假设我们有一个在范围 [0,2⁶⁴) 内均匀分布的 uint64 x，
	// 并想将其约减到范围 [0,n) 同时保持精确的均匀性。
	// 我们可以通过对 x*n 进行双倍宽乘法得到的高位来
	// 模拟任意精度的缩放 x * (n/2⁶⁴)，即 (x*n)/2⁶⁴。
	// 由于有 2⁶⁴ 种可能的输入 x，而输出只有 n 种，
	// 如果 n 不能整除 2⁶⁴，输出必然是有偏的。
	// 一般而言，当 x*n 在 [k*2⁶⁴,(k+1)*2⁶⁴) 内时 (x*n)/2⁶⁴ = k。
	// 在该范围内有 floor(2⁶⁴/n) 或 ceil(2⁶⁴/n) 种可能的乘积，
	// 取决于 k。
	// 但假设当 x*n 落在 [k*2⁶⁴, k*2⁶⁴+(2⁶⁴%n)) 内时，
	// 我们拒绝该样本并重试，这意味着在 2⁶⁴ 种结果中
	// 拒绝少于 n 种可能的结果。
	// 现在恰好有 floor(2⁶⁴/n) 种可能的方式来产生
	// 每个输出值 k，因此我们恢复了均匀性。
	// 为得到有效的 uint64 运算，2⁶⁴ % n = (2⁶⁴ - n) % n = -n % n，
	// 因此该算法的直接实现会是：
	//
	//	hi, lo := bits.Mul64(r.Uint64(), n)
	//	thresh := -n % n
	//	for lo < thresh {
	//		hi, lo = bits.Mul64(r.Uint64(), n)
	//	}
	//
	// 这仍然留下一次开销高昂的 64 位除法，我们宁愿避免它。
	// 我们知道 thresh < n，而 n 通常远小于 2⁶⁴，因此除非 lo < n，
	// 我们可以避免最后四行。
	//
	// 另请参见：
	// https://lemire.me/blog/2016/06/27/a-fast-alternative-to-the-modulo-reduction
	// https://lemire.me/blog/2016/06/30/fast-random-shuffling
	hi, lo := bits.Mul64(r.Uint64(), n)
	if lo < n {
		thresh := -n % n
		for lo < thresh {
			hi, lo = bits.Mul64(r.Uint64(), n)
		}
	}
	return hi
}

// uint32n 与 uint64n 的计算相同，
// 但针对 32 位系统进行了优化。
func (r *Rand) uint32n(n uint32) uint32 {
	if n&(n-1) == 0 { // n 是 2 的幂，可以掩码
		return uint32(r.Uint64()) & (n - 1)
	}
	// 在 64 位系统上，我们仍然使用下面的 uint64 代码，因为
	// 随机 uint64 lo 小于 uint32 n 的概率接近于零，
	// 这意味着去偏循环几乎从不运行。
	// 在 32 位系统上，这里我们需要用 32 位运算实现同样的逻辑，
	// 既是为了保留在 64 位机器上观察到的确切输出序列，
	// 也是为了保留去偏循环几乎从不运行这一优化。
	//
	// 我们想计算
	// 	hi, lo := bits.Mul64(r.Uint64(), n)
	// 用 32 位半部分表示，即为：
	// 	x1:x0 := r.Uint64()
	// 	0:hi, lo1:lo0 := bits.Mul64(x1:x0, 0:n)
	// 用 bits.Mul32 写出该乘法可以
	// 使用直接的硬件指令，并避免
	// 涉及这些零的计算。
	x := r.Uint64()
	lo1a, lo0 := bits.Mul32(uint32(x), n)
	hi, lo1b := bits.Mul32(uint32(x>>32), n)
	lo1, c := bits.Add32(lo1a, lo1b, 0)
	hi += c
	if lo1 == 0 && lo0 < uint32(n) {
		n64 := uint64(n)
		thresh := uint32(-n64 % n64)
		for lo1 == 0 && lo0 < thresh {
			x := r.Uint64()
			lo1a, lo0 = bits.Mul32(uint32(x), n)
			hi, lo1b = bits.Mul32(uint32(x>>32), n)
			lo1, c = bits.Add32(lo1a, lo1b, 0)
			hi += c
		}
	}
	return hi
}

// Int32N 以 int32 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func (r *Rand) Int32N(n int32) int32 {
	if n <= 0 {
		panic("invalid argument to Int32N")
	}
	return int32(r.uint64n(uint64(n)))
}

// Uint32N 以 uint32 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n == 0，它会 panic。
func (r *Rand) Uint32N(n uint32) uint32 {
	if n == 0 {
		panic("invalid argument to Uint32N")
	}
	return uint32(r.uint64n(uint64(n)))
}

const is32bit = ^uint(0)>>32 == 0

// IntN 以 int 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func (r *Rand) IntN(n int) int {
	if n <= 0 {
		panic("invalid argument to IntN")
	}
	return int(r.uint64n(uint64(n)))
}

// UintN 以 uint 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n == 0，它会 panic。
func (r *Rand) UintN(n uint) uint {
	if n == 0 {
		panic("invalid argument to UintN")
	}
	return uint(r.uint64n(uint64(n)))
}

// Float64 以 float64 返回半开区间 [0.0,1.0) 内的一个伪随机数。
func (r *Rand) Float64() float64 {
	// 在 [0,1) 内恰好有 1<<53 个 float64。使用 Intn(1<<53) / (1<<53)。
	return float64(r.Uint64()<<11>>11) / (1 << 53)
}

// Float32 以 float32 返回半开区间 [0.0,1.0) 内的一个伪随机数。
func (r *Rand) Float32() float32 {
	// 在 [0,1) 内恰好有 1<<24 个 float32。使用 Intn(1<<24) / (1<<24)。
	return float32(r.Uint32()<<8>>8) / (1 << 24)
}

// Perm 以 n 个 int 的切片形式返回半开区间 [0,n) 内
// 整数的一个伪随机排列。
func (r *Rand) Perm(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	r.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
	return p
}

// Shuffle 对元素的顺序进行伪随机化。
// n 是元素的数量。如果 n < 0，Shuffle 会 panic。
// swap 交换索引为 i 和 j 的元素。
func (r *Rand) Shuffle(n int, swap func(i, j int)) {
	if n < 0 {
		panic("invalid argument to Shuffle")
	}

	// Fisher-Yates 洗牌：https://en.wikipedia.org/wiki/Fisher%E2%80%93Yates_shuffle
	// 实在不应该用无法放入 32 位的 n 来调用 Shuffle。
	// 这不仅会花费非常长的时间，而且由于有 2³¹! 种可能的排列，
	// 任何 PRNG 都不可能拥有足够大的内部状态来
	// 生成哪怕极小一部分的可能排列。
	// 尽管如此，正确的 API 签名接受一个 int n，所以尽我们所能处理它。
	for i := n - 1; i > 0; i-- {
		j := int(r.uint64n(uint64(i + 1)))
		swap(i, j)
	}
}

/*
 * 顶层便捷函数
 */

// globalRand 是顶层便捷函数的随机数来源。
var globalRand = &Rand{src: runtimeSource{}}

//go:linkname runtime_rand runtime.rand
func runtime_rand() uint64

// runtimeSource 是一个使用运行时 fastrand 函数的 Source。
type runtimeSource struct{}

func (runtimeSource) Uint64() uint64 {
	return runtime_rand()
}

// Int64 从默认 Source 以 int64 返回一个
// 非负的伪随机 63 位整数。
func Int64() int64 { return globalRand.Int64() }

// Uint32 从默认 Source 以 uint32 返回一个
// 伪随机 32 位值。
func Uint32() uint32 { return globalRand.Uint32() }

// Uint64N 从默认 Source 以 uint64 返回半开区间 [0,n) 内的
// 一个伪随机数。
// 如果 n == 0，它会 panic。
func Uint64N(n uint64) uint64 { return globalRand.Uint64N(n) }

// Uint32N 从默认 Source 以 uint32 返回半开区间 [0,n) 内的
// 一个伪随机数。
// 如果 n == 0，它会 panic。
func Uint32N(n uint32) uint32 { return globalRand.Uint32N(n) }

// Uint64 从默认 Source 以 uint64 返回一个
// 伪随机 64 位值。
func Uint64() uint64 { return globalRand.Uint64() }

// Int32 从默认 Source 以 int32 返回一个
// 非负的伪随机 31 位整数。
func Int32() int32 { return globalRand.Int32() }

// Int 从默认 Source 返回一个非负的伪随机 int。
func Int() int { return globalRand.Int() }

// Uint 从默认 Source 返回一个伪随机 uint。
func Uint() uint { return globalRand.Uint() }

// Int64N 从默认 Source 以 int64 返回半开区间 [0,n) 内的
// 一个伪随机数。
// 如果 n <= 0，它会 panic。
func Int64N(n int64) int64 { return globalRand.Int64N(n) }

// Int32N 从默认 Source 以 int32 返回半开区间 [0,n) 内的
// 一个伪随机数。
// 如果 n <= 0，它会 panic。
func Int32N(n int32) int32 { return globalRand.Int32N(n) }

// IntN 从默认 Source 以 int 返回半开区间 [0,n) 内的
// 一个伪随机数。
// 如果 n <= 0，它会 panic。
func IntN(n int) int { return globalRand.IntN(n) }

// UintN 从默认 Source 以 uint 返回半开区间 [0,n) 内的
// 一个伪随机数。
// 如果 n == 0，它会 panic。
func UintN(n uint) uint { return globalRand.UintN(n) }

// N 从默认 Source 返回半开区间 [0,n) 内的一个伪随机数。
// 类型参数 Int 可以是任意整数类型。
// 如果 n <= 0，它会 panic。
func N[Int intType](n Int) Int {
	if n <= 0 {
		panic("invalid argument to N")
	}
	return Int(globalRand.uint64n(uint64(n)))
}

type intType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// Float64 从默认 Source 以 float64 返回半开区间 [0.0,1.0) 内的
// 一个伪随机数。
func Float64() float64 { return globalRand.Float64() }

// Float32 从默认 Source 以 float32 返回半开区间 [0.0,1.0) 内的
// 一个伪随机数。
func Float32() float32 { return globalRand.Float32() }

// Perm 从默认 Source 以 n 个 int 的切片形式返回半开区间 [0,n) 内
// 整数的一个伪随机排列。
func Perm(n int) []int { return globalRand.Perm(n) }

// Shuffle 使用默认 Source 对元素的顺序进行伪随机化。
// n 是元素的数量。如果 n < 0，Shuffle 会 panic。
// swap 交换索引为 i 和 j 的元素。
func Shuffle(n int, swap func(i, j int)) { globalRand.Shuffle(n, swap) }

// NormFloat64 从默认 Source 返回一个在范围
// [-math.MaxFloat64, +math.MaxFloat64] 内服从
// 标准正态分布（均值 = 0，标准差 = 1）的 float64。
// 要产生不同的正态分布，调用者可以使用以下方式
// 调整输出：
//
//	sample = NormFloat64() * desiredStdDev + desiredMean
func NormFloat64() float64 { return globalRand.NormFloat64() }

// ExpFloat64 从默认 Source 返回一个在范围
// (0, +math.MaxFloat64] 内服从指数分布的 float64，其速率参数
// (lambda) 为 1，均值为 1/lambda (1)。
// 要产生具有不同速率参数的分布，
// 调用者可以使用以下方式调整输出：
//
//	sample = ExpFloat64() / desiredRateParameter
func ExpFloat64() float64 { return globalRand.ExpFloat64() }
