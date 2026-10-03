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
// crypto/rand 包。
package rand

import (
	"internal/godebug"
	"sync"
	"sync/atomic"
	_ "unsafe" // 用于 go:linkname
)

// Source 表示一个在范围 [0, 1<<63) 内的均匀分布
// 伪随机 int64 值的来源。
//
// Source 不能安全地由多个 goroutine 并发使用。
type Source interface {
	Int63() int64
	Seed(seed int64)
}

// Source64 是一种 [Source]，它还可以直接在
// 范围 [0, 1<<64) 内生成均匀分布的伪随机 uint64 值。
// 如果 [Rand] r 的底层 [Source] s 实现了 Source64，
// 那么 r.Uint64 会返回一次调用 s.Uint64 的结果，
// 而不是调用两次 s.Int63。
type Source64 interface {
	Source
	Uint64() uint64
}

// NewSource 返回一个用给定值播种的新的伪随机 [Source]。
// 与顶层函数使用的默认 [Source] 不同，此 source 不能安全地
// 由多个 goroutine 并发使用。
// 返回的 [Source] 实现了 [Source64]。
func NewSource(seed int64) Source {
	return newSource(seed)
}

func newSource(seed int64) *rngSource {
	var rng rngSource
	rng.Seed(seed)
	return &rng
}

// Rand 是一个随机数来源。
type Rand struct {
	src Source
	s64 Source64 // 如果 src 是 source64 则非 nil

	// readVal 包含最近一次 Read 调用期间用于生成字节的
	// 63 位整数的剩余部分。
	// 它被保存下来，以便下一次 Read 调用可以从上一次
	// 结束的地方继续。
	readVal int64
	// readPos 表示 readVal 中仍然有效的低位字节数。
	readPos int8
}

// New 返回一个新的 [Rand]，它使用来自 src 的随机值
// 来生成其他随机值。
func New(src Source) *Rand {
	s64, _ := src.(Source64)
	return &Rand{src: src, s64: s64}
}

// Seed 使用提供的种子值将生成器初始化为确定性的状态。
// Seed 不应与任何其他 [Rand] 方法并发调用。
func (r *Rand) Seed(seed int64) {
	if lk, ok := r.src.(*lockedSource); ok {
		lk.seedPos(seed, &r.readPos)
		return
	}

	r.src.Seed(seed)
	r.readPos = 0
}

// Int63 以 int64 返回一个非负的伪随机 63 位整数。
func (r *Rand) Int63() int64 { return r.src.Int63() }

// Uint32 以 uint32 返回一个伪随机 32 位值。
func (r *Rand) Uint32() uint32 { return uint32(r.Int63() >> 31) }

// Uint64 以 uint64 返回一个伪随机 64 位值。
func (r *Rand) Uint64() uint64 {
	if r.s64 != nil {
		return r.s64.Uint64()
	}
	return uint64(r.Int63())>>31 | uint64(r.Int63())<<32
}

// Int31 以 int32 返回一个非负的伪随机 31 位整数。
func (r *Rand) Int31() int32 { return int32(r.Int63() >> 32) }

// Int 返回一个非负的伪随机 int。
func (r *Rand) Int() int {
	u := uint(r.Int63())
	return int(u << 1 >> 1) // 如果 int == int32，则清除符号位
}

// Int63n 以 int64 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func (r *Rand) Int63n(n int64) int64 {
	if n <= 0 {
		panic("invalid argument to Int63n")
	}
	if n&(n-1) == 0 { // n 是 2 的幂，可以掩码
		return r.Int63() & (n - 1)
	}
	max := int64((1 << 63) - 1 - (1<<63)%uint64(n))
	v := r.Int63()
	for v > max {
		v = r.Int63()
	}
	return v % n
}

// Int31n 以 int32 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func (r *Rand) Int31n(n int32) int32 {
	if n <= 0 {
		panic("invalid argument to Int31n")
	}
	if n&(n-1) == 0 { // n 是 2 的幂，可以掩码
		return r.Int31() & (n - 1)
	}
	max := int32((1 << 31) - 1 - (1<<31)%uint32(n))
	v := r.Int31()
	for v > max {
		v = r.Int31()
	}
	return v % n
}

// int31n 以 int32 返回半开区间 [0,n) 内的一个非负伪随机数。
// n 必须 > 0，但 int31n 不检查这一点；调用者必须确保这一点。
// 存在 int31n 是因为 Int31n 效率低下，但 Go 1 兼容性
// 要求 math/rand 产生的值流保持不变。
// 因此 int31n 只能在内部被新引入的 API 使用。
//
// 有关实现细节，请参见：
// https://lemire.me/blog/2016/06/27/a-fast-alternative-to-the-modulo-reduction
// https://lemire.me/blog/2016/06/30/fast-random-shuffling
func (r *Rand) int31n(n int32) int32 {
	v := r.Uint32()
	prod := uint64(v) * uint64(n)
	low := uint32(prod)
	if low < uint32(n) {
		thresh := uint32(-n) % uint32(n)
		for low < thresh {
			v = r.Uint32()
			prod = uint64(v) * uint64(n)
			low = uint32(prod)
		}
	}
	return int32(prod >> 32)
}

// Intn 以 int 返回半开区间 [0,n) 内的一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func (r *Rand) Intn(n int) int {
	if n <= 0 {
		panic("invalid argument to Intn")
	}
	if n <= 1<<31-1 {
		return int(r.Int31n(int32(n)))
	}
	return int(r.Int63n(int64(n)))
}

// Float64 以 float64 返回半开区间 [0.0,1.0) 内的一个伪随机数。
func (r *Rand) Float64() float64 {
	// 一个更清晰、更简单的实现会是：
	//	return float64(r.Int63n(1<<53)) / (1<<53)
	// 然而，Go 1 发布时采用的是
	//	return float64(r.Int63()) / (1 << 63)
	// 而我们要保留那个值流。
	//
	// 值流中存在一个 bug：r.Int63() 可能非常接近
	// 1<<63，以至于除法会向上舍入到 1.0，而我们已保证
	// 结果始终小于 1.0。
	//
	// 我们曾尝试通过将 1.0 映射回 0.0 来修复这个问题，但由于 float64
	// 值在 0 附近比在 1 附近密集得多，将 1 映射为 0 会导致
	// 返回 0 的概率在理论上显著偏高。
	// 与其如此，如果向上舍入到 1，就重试。
	// 得到 1 的情况只会在 1/2⁵³ 的时候发生，所以大多数客户端
	// 无论如何都不会观察到它。
again:
	f := float64(r.Int63()) / (1 << 63)
	if f == 1 {
		goto again // 重新采样；几乎不会走到这个分支
	}
	return f
}

// Float32 以 float32 返回半开区间 [0.0,1.0) 内的一个伪随机数。
func (r *Rand) Float32() float32 {
	// 与 Float64 中的理由相同：我们希望保留 Go 1 的值
	// 流，只是要修复它使其不返回 1.0
	// 这种情况只会以 1/2²⁴ 的概率发生（再加上 Float64 中 1/2⁵³ 的概率）。
again:
	f := float32(r.Float64())
	if f == 1 {
		goto again // 重新采样；极少会走到这个分支
	}
	return f
}

// Perm 以 n 个 int 的切片形式返回半开区间 [0,n) 内
// 整数的一个伪随机排列。
func (r *Rand) Perm(n int) []int {
	m := make([]int, n)
	// 在下面的循环中，当 i=0 时的迭代总是将 m[0] 与 m[0] 交换。
	// 移除这个无用迭代的一种改动是在初始化语句中
	// 将 1 赋给 i。但 Perm 也会影响 r。做出这个改动会影响
	// r 的最终状态。所以出于 Go 1 的兼容性
	// 原因，不能做这个改动。
	for i := 0; i < n; i++ {
		j := r.Intn(i + 1)
		m[i] = m[j]
		m[j] = i
	}
	return m
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
	i := n - 1
	for ; i > 1<<31-1-1; i-- {
		j := int(r.Int63n(int64(i + 1)))
		swap(i, j)
	}
	for ; i > 0; i-- {
		j := int(r.int31n(int32(i + 1)))
		swap(i, j)
	}
}

// Read 生成 len(p) 个随机字节并将它们写入 p。它
// 总是返回 len(p) 和一个 nil 错误。
// Read 不应与任何其他 Rand 方法并发调用。
func (r *Rand) Read(p []byte) (n int, err error) {
	switch src := r.src.(type) {
	case *lockedSource:
		return src.read(p, &r.readVal, &r.readPos)
	case *runtimeSource:
		return src.read(p, &r.readVal, &r.readPos)
	}
	return read(p, r.src, &r.readVal, &r.readPos)
}

func read(p []byte, src Source, readVal *int64, readPos *int8) (n int, err error) {
	pos := *readPos
	val := *readVal
	rng, _ := src.(*rngSource)
	for n = 0; n < len(p); n++ {
		if pos == 0 {
			if rng != nil {
				val = rng.Int63()
			} else {
				val = src.Int63()
			}
			pos = 7
		}
		p[n] = byte(val)
		val >>= 8
		pos--
	}
	*readPos = pos
	*readVal = val
	return
}

/*
 * 顶层便捷函数
 */

// globalRandGenerator 是顶层便捷函数的随机数来源。
// 在可能的情况下，它使用运行时 fastrand64
// 函数来避免加锁。如果用户调用了 Seed（无论是显式调用，
// 还是通过 GODEBUG=randautoseed=0 隐式调用），则无法这样做。
var globalRandGenerator atomic.Pointer[Rand]

var randautoseed = godebug.New("randautoseed")

// randseednop 控制全局 Seed 是否为无操作。
var randseednop = godebug.New("randseednop")

// globalRand 返回供顶层便捷函数使用的
// 生成器。
func globalRand() *Rand {
	if r := globalRandGenerator.Load(); r != nil {
		return r
	}

	// 这是第一次调用。基于 GODEBUG 进行初始化。
	var r *Rand
	if randautoseed.Value() == "0" {
		randautoseed.IncNonDefault()
		r = New(new(lockedSource))
		r.Seed(1)
	} else {
		r = &Rand{
			src: &runtimeSource{},
			s64: &runtimeSource{},
		}
	}

	if !globalRandGenerator.CompareAndSwap(nil, r) {
		// 两个不同的 goroutine 同时调用了某个顶层
		// 函数。虽然这种情况下的结果
		// 不可预测，但如果我们这里只使用 r，
		// 并且我们正在使用种子，那么两次调用很可能
		// 会返回相同的值。这似乎并不理想。
		// 就使用第一个进来的那个吧。
		return globalRandGenerator.Load()
	}

	return r
}

//go:linkname runtime_rand runtime.rand
func runtime_rand() uint64

// runtimeSource 是 Source64 的一个实现，它使用运行时的
// fastrand 函数。
type runtimeSource struct {
	// 该互斥锁用于避免 Read 中的竞态条件。
	mu sync.Mutex
}

func (*runtimeSource) Int63() int64 {
	return int64(runtime_rand() & rngMask)
}

func (*runtimeSource) Seed(int64) {
	panic("internal error: call to runtimeSource.Seed")
}

func (*runtimeSource) Uint64() uint64 {
	return runtime_rand()
}

func (fs *runtimeSource) read(p []byte, readVal *int64, readPos *int8) (n int, err error) {
	fs.mu.Lock()
	n, err = read(p, fs, readVal, readPos)
	fs.mu.Unlock()
	return
}

// Seed 使用提供的种子值将默认 Source 初始化为
// 确定性的状态。除以 2³¹-1 后余数相同的种子值
// 会生成相同的伪随机序列。
// 与 [Rand.Seed] 方法不同，Seed 可安全地并发使用。
//
// 如果未调用 Seed，生成器会在程序启动时随机播种。
//
// 在 Go 1.20 之前，生成器在程序启动时会像 Seed(1) 那样播种。
// 要强制使用旧行为，请在程序启动时调用 Seed(1)。
// 或者，在调用本包中的任何函数之前，
// 在环境中设置 GODEBUG=randautoseed=0。
//
// 已弃用：从 Go 1.20 起，没有理由用随机值调用 Seed。
// 那些用已知值调用 Seed 以获得特定结果序列的程序
// 应使用 New(NewSource(seed)) 来获得
// 一个本地随机生成器。
//
// 从 Go 1.24 起 [Seed] 是无操作。要恢复先前的行为，
// 请设置 GODEBUG=randseednop=0。
func Seed(seed int64) {
	if randseednop.Value() != "0" {
		return
	}
	randseednop.IncNonDefault()

	orig := globalRandGenerator.Load()

	// 如果我们已经在使用 lockedSource，只需重新播种即可。
	if orig != nil {
		if _, ok := orig.src.(*lockedSource); ok {
			orig.Seed(seed)
			return
		}
	}

	// 否则要么
	// 1) orig == nil，这是 Seed 是第一个
	// 被调用的顶层函数时的常见情况，要么
	// 2) orig 已经是一个 runtimeSource，此时我们需要改成
	// 一个 lockedSource。
	// 无论哪种情况，我们都做同样的事。

	r := New(new(lockedSource))
	r.Seed(seed)

	if !globalRandGenerator.CompareAndSwap(orig, r) {
		// 情况发生了变化。为安全起见重试。
		Seed(seed)
	}
}

// Int63 从默认 [Source] 以 int64 返回一个
// 非负的伪随机 63 位整数。
func Int63() int64 { return globalRand().Int63() }

// Uint32 从默认 [Source] 以 uint32 返回一个
// 伪随机 32 位值。
func Uint32() uint32 { return globalRand().Uint32() }

// Uint64 从默认 [Source] 以 uint64 返回一个
// 伪随机 64 位值。
func Uint64() uint64 { return globalRand().Uint64() }

// Int31 从默认 [Source] 以 int32 返回一个
// 非负的伪随机 31 位整数。
func Int31() int32 { return globalRand().Int31() }

// Int 从默认 [Source] 返回一个非负的伪随机 int。
func Int() int { return globalRand().Int() }

// Int63n 从默认 [Source] 以 int64 返回半开区间 [0,n) 内的
// 一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func Int63n(n int64) int64 { return globalRand().Int63n(n) }

// Int31n 从默认 [Source] 以 int32 返回半开区间 [0,n) 内的
// 一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func Int31n(n int32) int32 { return globalRand().Int31n(n) }

// Intn 从默认 [Source] 以 int 返回半开区间 [0,n) 内的
// 一个非负伪随机数。
// 如果 n <= 0，它会 panic。
func Intn(n int) int { return globalRand().Intn(n) }

// Float64 从默认 [Source] 以 float64 返回半开区间 [0.0,1.0) 内的
// 一个伪随机数。
func Float64() float64 { return globalRand().Float64() }

// Float32 从默认 [Source] 以 float32 返回半开区间 [0.0,1.0) 内的
// 一个伪随机数。
func Float32() float32 { return globalRand().Float32() }

// Perm 从默认 [Source] 以 n 个 int 的切片形式返回半开区间 [0,n) 内
// 整数的一个伪随机排列。
func Perm(n int) []int { return globalRand().Perm(n) }

// Shuffle 使用默认 [Source] 对元素的顺序进行伪随机化。
// n 是元素的数量。如果 n < 0，Shuffle 会 panic。
// swap 交换索引为 i 和 j 的元素。
func Shuffle(n int, swap func(i, j int)) { globalRand().Shuffle(n, swap) }

// Read 从默认 [Source] 生成 len(p) 个随机字节，并将它们
// 写入 p。它总是返回 len(p) 和一个 nil 错误。
// 与 [Rand.Read] 方法不同，Read 可安全地并发使用。
//
// 已弃用：对于几乎所有用例，[crypto/rand.Read] 都更合适。
// 如果需要确定性的 source，请使用 [math/rand/v2.ChaCha8.Read]。
func Read(p []byte) (n int, err error) { return globalRand().Read(p) }

// NormFloat64 从默认 [Source] 返回一个在范围
// [-[math.MaxFloat64], +[math.MaxFloat64]] 内服从
// 标准正态分布（均值 = 0，标准差 = 1）的 float64。
// 要产生不同的正态分布，调用者可以使用以下方式
// 调整输出：
//
//	sample = NormFloat64() * desiredStdDev + desiredMean
func NormFloat64() float64 { return globalRand().NormFloat64() }

// ExpFloat64 从默认 [Source] 返回一个在范围
// (0, +[math.MaxFloat64]] 内服从指数分布的 float64，其速率参数
// (lambda) 为 1，均值为 1/lambda (1)。
// 要产生具有不同速率参数的分布，
// 调用者可以使用以下方式调整输出：
//
//	sample = ExpFloat64() / desiredRateParameter
func ExpFloat64() float64 { return globalRand().ExpFloat64() }

type lockedSource struct {
	lk sync.Mutex
	s  *rngSource
}

func (r *lockedSource) Int63() (n int64) {
	r.lk.Lock()
	n = r.s.Int63()
	r.lk.Unlock()
	return
}

func (r *lockedSource) Uint64() (n uint64) {
	r.lk.Lock()
	n = r.s.Uint64()
	r.lk.Unlock()
	return
}

func (r *lockedSource) Seed(seed int64) {
	r.lk.Lock()
	r.seed(seed)
	r.lk.Unlock()
}

// seedPos 为 lockedSource 实现 Seed，且不存在竞态条件。
func (r *lockedSource) seedPos(seed int64, readPos *int8) {
	r.lk.Lock()
	r.seed(seed)
	*readPos = 0
	r.lk.Unlock()
}

// seed 为底层 source 播种。
// 调用者必须已经锁定了 r.lk。
func (r *lockedSource) seed(seed int64) {
	if r.s == nil {
		r.s = newSource(seed)
	} else {
		r.s.Seed(seed)
	}
}

// read 为 lockedSource 实现 Read，且不存在竞态条件。
func (r *lockedSource) read(p []byte, readVal *int64, readPos *int8) (n int, err error) {
	r.lk.Lock()
	n, err = read(p, r.s, readVal, readPos)
	r.lk.Unlock()
	return
}
