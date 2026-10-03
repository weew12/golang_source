// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现无符号多精度整数（自然数）。它们是
// 有符号整数、有理数和浮点数实现的基础构件。
//
// 注意：此实现依赖函数 "alias"，它假设 (nat) 切片的容量
//       永远不会改变（没有三操作数的切片表达式）。
//       如果这一点发生变化，为保持正确性需要更新 alias。

package big

import (
	"internal/byteorder"
	"math/bits"
	"math/rand"
	"slices"
	"sync"
)

// 形如
//
//	x = x[n-1]*_B^(n-1) + x[n-2]*_B^(n-2) + ... + x[1]*_B + x[0]
//
// 的无符号整数 x，其中 0 <= x[i] < _B 且 0 <= i < n，存储在一个
// 长度为 n 的切片中，以数字 x[i] 作为切片元素。
//
// 如果切片不包含前导 0 数字，则称该数是规范化的。
// 在算术运算过程中可能出现非规范化的值，但在返回最终结果之前
// 总是会进行规范化。0 的规范化表示是空切片或 nil 切片（长度 = 0）。
type nat []Word

var (
	natOne  = nat{1}
	natTwo  = nat{2}
	natFive = nat{5}
	natTen  = nat{10}
)

func (z nat) String() string {
	return "0x" + string(z.itoa(false, 16))
}

func (z nat) norm() nat {
	i := len(z)
	for i > 0 && z[i-1] == 0 {
		i--
	}
	return z[0:i]
}

func (z nat) make(n int) nat {
	if n <= cap(z) {
		return z[:n] // 复用 z
	}
	if n == 1 {
		// 大多数 nat 开始时很小并保持如此；不要过度分配。
		return make(nat, 1)
	}
	// 为 e 选择一个好的值对性能有显著影响，
	// 因为它增加了值可被复用的机会。
	const e = 4 // 额外容量
	return make(nat, n, n+e)
}

func (z nat) setWord(x Word) nat {
	if x == 0 {
		return z[:0]
	}
	z = z.make(1)
	z[0] = x
	return z
}

func (z nat) setUint64(x uint64) nat {
	// 单字值
	if w := Word(x); uint64(w) == x {
		return z.setWord(w)
	}
	// 双字值
	z = z.make(2)
	z[1] = Word(x >> 32)
	z[0] = Word(x)
	return z
}

func (z nat) set(x nat) nat {
	z = z.make(len(x))
	copy(z, x)
	return z
}

func (z nat) add(x, y nat) nat {
	m := len(x)
	n := len(y)

	switch {
	case m < n:
		return z.add(y, x)
	case m == 0:
		// 因为 m >= n，所以 n == 0；结果为 0
		return z[:0]
	case n == 0:
		// 结果为 x
		return z.set(x)
	}
	// m > 0

	z = z.make(m + 1)
	c := addVV(z[:n], x[:n], y[:n])
	if m > n {
		c = addVW(z[n:m], x[n:], c)
	}
	z[m] = c

	return z.norm()
}

func (z nat) sub(x, y nat) nat {
	m := len(x)
	n := len(y)

	switch {
	case m < n:
		panic("underflow")
	case m == 0:
		// 因为 m >= n，所以 n == 0；结果为 0
		return z[:0]
	case n == 0:
		// 结果为 x
		return z.set(x)
	}
	// m > 0

	z = z.make(m)
	c := subVV(z[:n], x[:n], y[:n])
	if m > n {
		c = subVW(z[n:], x[n:], c)
	}
	if c != 0 {
		panic("underflow")
	}

	return z.norm()
}

func (x nat) cmp(y nat) (r int) {
	m := len(x)
	n := len(y)
	if m != n || m == 0 {
		switch {
		case m < n:
			r = -1
		case m > n:
			r = 1
		}
		return
	}

	i := m - 1
	for i > 0 && x[i] == y[i] {
		i--
	}

	switch {
	case x[i] < y[i]:
		r = -1
	case x[i] > y[i]:
		r = 1
	}
	return
}

// montgomery 计算 z mod m = x*y*2**(-n*_W) mod m，
// 假设 k = -1/m mod 2**_W。
// z 用于存储被返回的结果；
// z 不能与 x、y 或 m 互为别名。
// 参见 Gueron, "Efficient Software Implementations of Modular Exponentiation"。
// https://eprint.iacr.org/2011/239.pdf
// 用该论文的术语来说，这是一种 "Almost Montgomery Multiplication"（近似
// Montgomery 乘法）：要求 x 和 y 满足 0 <= z < 2**(n*_W)，此时结果
// z 保证满足 0 <= z < 2**(n*_W)，但不一定 < m。
func (z nat) montgomery(x, y, m nat, k Word, n int) nat {
	// 此代码假设 x、y、m 长度都相同，均为 n。
	// （addMulVVW 和 for 循环都要求如此）。
	// 它还假设 x、y 已经对 m 取模化简，
	// 否则结果将无法正确化简。
	if len(x) != n || len(y) != n || len(m) != n {
		panic("math/big: mismatched montgomery number lengths")
	}
	z = z.make(n * 2)
	clear(z)
	var c Word
	for i := 0; i < n; i++ {
		d := y[i]
		c2 := addMulVVWW(z[i:n+i], z[i:n+i], x, d, 0)
		t := z[i] * k
		c3 := addMulVVWW(z[i:n+i], z[i:n+i], m, t, 0)
		cx := c + c2
		cy := cx + c3
		z[n+i] = cy
		if cx < c2 || cy < c3 {
			c = 1
		} else {
			c = 0
		}
	}
	if c != 0 {
		subVV(z[:n], z[n:], m)
	} else {
		copy(z[:n], z[n:])
	}
	return z[:n]
}

// alias 报告 x 和 y 是否共享同一个底层数组。
//
// 注意：alias 假设底层数组的容量对 nat 值而言永远不会改变；
// 即此代码中不存在三操作数的切片表达式（或更糟的情况，
// 借助 reflect 的等效操作）。
func alias(x, y nat) bool {
	return cap(x) > 0 && cap(y) > 0 && &x[0:cap(x)][cap(x)-1] == &y[0:cap(y)][cap(y)-1]
}

// addTo 实现 z += x；z 必须足够长。
// （我们不使用 nat.add，因为需要 z 保持同一个
// 切片，并且不需要在每次加法后对 z 进行规范化）
func addTo(z, x nat) {
	if n := len(x); n > 0 {
		if c := addVV(z[:n], z[:n], x[:n]); c != 0 {
			if n < len(z) {
				addVW(z[n:], z[n:], c)
			}
		}
	}
}

// mulRange 计算闭区间 [a, b] 中所有无符号整数的乘积。
// 如果 a > b（空区间），结果为 1。
// 调用者可以传入 stk == nil，以请求 mulRange 自行获取并释放一个。
func (z nat) mulRange(stk *stack, a, b uint64) nat {
	switch {
	case a == 0:
		// 缩短长区间（优化）
		return z.setUint64(0)
	case a > b:
		return z.setUint64(1)
	case a == b:
		return z.setUint64(a)
	case a+1 == b:
		return z.mul(stk, nat(nil).setUint64(a), nat(nil).setUint64(b))
	}

	if stk == nil {
		stk = getStack()
		defer stk.free()
	}

	m := a + (b-a)/2 // 避免溢出
	return z.mul(stk, nat(nil).mulRange(stk, a, m), nat(nil).mulRange(stk, m+1, b))
}

// stack 为乘法、除法等复杂计算提供临时存储。
// 该 stack 是一个简单的 Word 切片，按需扩展
// 以容纳某次计算的全部临时存储。
// 通常，如果函数接受 *stack 参数，它期望得到非 nil 的 *stack。
// 但某些函数允许传入 nil *stack，
// 这样它们就能处理无需 stack 的简单情形，而不必强迫
// 调用者获取并释放一个不会被使用的 stack。这些函数
// 会在其文档注释中说明它们接受 nil *stack。
type stack struct {
	w []Word
}

var stackPool sync.Pool

// getStack 返回一个临时 stack。
// 调用者用完后必须调用 [stack.free] 以放弃对该 stack 的使用。
func getStack() *stack {
	s, _ := stackPool.Get().(*stack)
	if s == nil {
		s = new(stack)
	}
	return s
}

// free 将该 stack 归还以供其他计算使用。
func (s *stack) free() {
	s.w = s.w[:0]
	stackPool.Put(s)
}

// save 返回当前栈指针。
// 之后用相同值调用 restore，
// 会释放在调用 save 之后分配在栈上的任何临时对象。
func (s *stack) save() int {
	return len(s.w)
}

// restore 将栈指针恢复到 n。
// 它几乎总是这样调用：
//
//	defer stk.restore(stk.save())
//
// 这确保在返回之前，从栈上弹出当前函数中分配的任何临时对象。
func (s *stack) restore(n int) {
	s.w = s.w[:n]
}

// nat 返回一个包含 n 个 Word 的 nat，分配在栈上。
func (s *stack) nat(n int) nat {
	nr := (n + 3) &^ 3 // 向上取整到 4 的倍数
	off := len(s.w)
	s.w = slices.Grow(s.w, nr)
	s.w = s.w[:off+nr]
	x := s.w[off : off+n : off+n]
	if n > 0 {
		x[0] = 0xfedcb
	}
	return x
}

// bitLen 返回 x 的位长度。
// 与大多数方法不同，即使 x 未规范化它也能正常工作。
func (x nat) bitLen() int {
	// 此函数用于密码学操作。它不能通过边信道泄露
	// 除 Int 的符号和位长之外的任何信息。任何
	// 修改都必须由安全专家审查。
	if i := len(x) - 1; i >= 0 {
		// 在某些架构上，bits.Len 对低位使用查找表。
		// 通过把第一个 1 位之后的所有位都置为 1，
		// 来消除任何依赖于输入的行为。
		top := uint(x[i])
		top |= top >> 1
		top |= top >> 2
		top |= top >> 4
		top |= top >> 8
		top |= top >> 16
		top |= top >> 16 >> 16 // 在 32 位架构上 ">> 32" 无法编译
		return i*_W + bits.Len(top)
	}
	return 0
}

// trailingZeroBits 返回 x 中连续的、最低有效零位的个数。
func (x nat) trailingZeroBits() uint {
	if len(x) == 0 {
		return 0
	}
	var i uint
	for x[i] == 0 {
		i++
	}
	// x[i] != 0
	return i*_W + uint(bits.TrailingZeros(uint(x[i])))
}

// isPow2 在 x == 2**i 时返回 i, true，否则返回 0, false。
func (x nat) isPow2() (uint, bool) {
	var i uint
	for x[i] == 0 {
		i++
	}
	if i == uint(len(x))-1 && x[i]&(x[i]-1) == 0 {
		return i*_W + uint(bits.TrailingZeros(uint(x[i]))), true
	}
	return 0, false
}

func same(x, y nat) bool {
	return len(x) == len(y) && len(x) > 0 && &x[0] == &y[0]
}

// z = x << s
func (z nat) lsh(x nat, s uint) nat {
	if s == 0 {
		if same(z, x) {
			return z
		}
		if !alias(z, x) {
			return z.set(x)
		}
	}

	m := len(x)
	if m == 0 {
		return z[:0]
	}
	// m > 0

	n := m + int(s/_W)
	z = z.make(n + 1)
	if s %= _W; s == 0 {
		copy(z[n-m:n], x)
		z[n] = 0
	} else {
		z[n] = lshVU(z[n-m:n], x, s)
	}
	clear(z[0 : n-m])

	return z.norm()
}

// z = x >> s
func (z nat) rsh(x nat, s uint) nat {
	if s == 0 {
		if same(z, x) {
			return z
		}
		if !alias(z, x) {
			return z.set(x)
		}
	}

	m := len(x)
	n := m - int(s/_W)
	if n <= 0 {
		return z[:0]
	}
	// n > 0

	z = z.make(n)
	if s %= _W; s == 0 {
		copy(z, x[m-n:])
	} else {
		rshVU(z, x[m-n:], s)
	}

	return z.norm()
}

func (z nat) setBit(x nat, i uint, b uint) nat {
	j := int(i / _W)
	m := Word(1) << (i % _W)
	n := len(x)
	switch b {
	case 0:
		z = z.make(n)
		copy(z, x)
		if j >= n {
			// 无需增长
			return z
		}
		z[j] &^= m
		return z.norm()
	case 1:
		if j >= n {
			z = z.make(j + 1)
			clear(z[n:])
		} else {
			z = z.make(n)
		}
		copy(z, x)
		z[j] |= m
		// 无需规范化
		return z
	}
	panic("set bit is not 0 or 1")
}

// bit 返回第 i 位的值，最低有效位为第 0 位。
func (x nat) bit(i uint) uint {
	j := i / _W
	if j >= uint(len(x)) {
		return 0
	}
	// 0 <= j < len(x)
	return uint(x[j] >> (i % _W) & 1)
}

// sticky 如果 i 个最低有效位内存在 1 位，则返回 1，
// 否则返回 0。
func (x nat) sticky(i uint) uint {
	j := i / _W
	if j >= uint(len(x)) {
		if len(x) == 0 {
			return 0
		}
		return 1
	}
	// 0 <= j < len(x)
	for _, x := range x[:j] {
		if x != 0 {
			return 1
		}
	}
	if x[j]<<(_W-i%_W) != 0 {
		return 1
	}
	return 0
}

func (z nat) and(x, y nat) nat {
	m := len(x)
	n := len(y)
	if m > n {
		m = n
	}
	// m <= n

	z = z.make(m)
	for i := 0; i < m; i++ {
		z[i] = x[i] & y[i]
	}

	return z.norm()
}

// trunc 返回 z = x mod 2ⁿ。
func (z nat) trunc(x nat, n uint) nat {
	w := (n + _W - 1) / _W
	if uint(len(x)) < w {
		return z.set(x)
	}
	z = z.make(int(w))
	copy(z, x)
	if n%_W != 0 {
		z[len(z)-1] &= 1<<(n%_W) - 1
	}
	return z.norm()
}

func (z nat) andNot(x, y nat) nat {
	m := len(x)
	n := len(y)
	if n > m {
		n = m
	}
	// m >= n

	z = z.make(m)
	for i := 0; i < n; i++ {
		z[i] = x[i] &^ y[i]
	}
	copy(z[n:m], x[n:m])

	return z.norm()
}

func (z nat) or(x, y nat) nat {
	m := len(x)
	n := len(y)
	s := x
	if m < n {
		n, m = m, n
		s = y
	}
	// m >= n

	z = z.make(m)
	for i := 0; i < n; i++ {
		z[i] = x[i] | y[i]
	}
	copy(z[n:m], s[n:m])

	return z.norm()
}

func (z nat) xor(x, y nat) nat {
	m := len(x)
	n := len(y)
	s := x
	if m < n {
		n, m = m, n
		s = y
	}
	// m >= n

	z = z.make(m)
	for i := 0; i < n; i++ {
		z[i] = x[i] ^ y[i]
	}
	copy(z[n:m], s[n:m])

	return z.norm()
}

// random 在 [0..limit) 中创建一个随机整数，如有可能
// 则使用 z 的空间。n 是 limit 的位长度。
func (z nat) random(rand *rand.Rand, limit nat, n int) nat {
	if alias(z, limit) {
		z = nil // z 是 limit 的别名 - 无法复用
	}
	z = z.make(len(limit))

	bitLengthOfMSW := uint(n % _W)
	if bitLengthOfMSW == 0 {
		bitLengthOfMSW = _W
	}
	mask := Word((1 << bitLengthOfMSW) - 1)

	for {
		switch _W {
		case 32:
			for i := range z {
				z[i] = Word(rand.Uint32())
			}
		case 64:
			for i := range z {
				z[i] = Word(rand.Uint32()) | Word(rand.Uint32())<<32
			}
		default:
			panic("unknown word size")
		}
		z[len(limit)-1] &= mask
		if z.cmp(limit) < 0 {
			break
		}
	}

	return z.norm()
}

// 如果 m != 0（即 len(m) != 0），expNN 将 z 置为 x**y mod m；
// 否则将 z 置为 x**y。结果是 z 的值。
// 调用者可以传入 stk == nil，以请求 expNN 自行获取并释放一个。
func (z nat) expNN(stk *stack, x, y, m nat, slow bool) nat {
	if alias(z, x) || alias(z, y) {
		// 我们不能允许就地修改 x 或 y。
		z = nil
	}

	// x**y mod 1 == 0
	if len(m) == 1 && m[0] == 1 {
		return z.setWord(0)
	}
	// m == 0 || m > 1

	// x**0 == 1
	if len(y) == 0 {
		return z.setWord(1)
	}
	// y > 0

	// 0**y = 0
	if len(x) == 0 {
		return z.setWord(0)
	}
	// x > 0

	// 1**y = 1
	if len(x) == 1 && x[0] == 1 {
		return z.setWord(1)
	}
	// x > 1

	// x**1 == x
	if len(y) == 1 && y[0] == 1 && len(m) == 0 {
		return z.set(x)
	}
	if stk == nil {
		stk = getStack()
		defer stk.free()
	}
	if len(y) == 1 && y[0] == 1 { // len(m) > 0
		return z.rem(stk, x, m)
	}

	// y > 1

	if len(m) != 0 {
		// 我们最终很可能与模数一样长。
		z = z.make(len(m))

		// 如果指数很大，对奇数值我们使用 Montgomery 方法，
		// 对 2 的幂使用 4 位窗口化指数运算，
		// 对剩余的值使用 CRT 分解的 Montgomery 方法
		// （偶数值乘以非平凡的奇数值，可分解为前两种情形
		// 各一个实例）。
		if len(y) > 1 && !slow {
			if m[0]&1 == 1 {
				return z.expNNMontgomery(stk, x, y, m)
			}
			if logM, ok := m.isPow2(); ok {
				return z.expNNWindowed(stk, x, y, logM)
			}
			return z.expNNMontgomeryEven(stk, x, y, m)
		}
	}

	z = z.set(x)
	v := y[len(y)-1] // v > 0，因为 y 已规范化且 y > 0
	shift := nlz(v) + 1
	v <<= shift
	var q nat

	const mask = 1 << (_W - 1)

	// 我们逐个遍历指数的各位。每遇到一位就做一次平方，
	// 从而使幂翻倍。如果该位为 1，
	// 我们还要乘以 x，从而使幂加一。

	w := _W - int(shift)
	// 使用 zz 和 r 以避免在 mul 和 div 中分配内存，
	// 否则参数会互为别名。
	var zz, r nat
	for j := 0; j < w; j++ {
		zz = zz.sqr(stk, z)
		zz, z = z, zz

		if v&mask != 0 {
			zz = zz.mul(stk, z, x)
			zz, z = z, zz
		}

		if len(m) != 0 {
			zz, r = zz.div(stk, r, z, m)
			zz, r, q, z = q, z, zz, r
		}

		v <<= 1
	}

	for i := len(y) - 2; i >= 0; i-- {
		v = y[i]

		for j := 0; j < _W; j++ {
			zz = zz.sqr(stk, z)
			zz, z = z, zz

			if v&mask != 0 {
				zz = zz.mul(stk, z, x)
				zz, z = z, zz
			}

			if len(m) != 0 {
				zz, r = zz.div(stk, r, z, m)
				zz, r, q, z = q, z, zz, r
			}

			v <<= 1
		}
	}

	return z.norm()
}

// expNNMontgomeryEven 计算 x**y mod m，其中 m = m1 × m2，
// m1 = 2ⁿ 且 m2 为奇数。
// 它对 x**y mod m1 和 x**y mod m2 分别递归调用 expNN，
// 然后使用中国剩余定理合并结果。
// 使用 m1 的递归调用将使用 expNNWindowed，
// 而使用 m2 的递归调用将使用 expNNMontgomery。
// 更多细节参见 Ç. K. Koç, “Montgomery Reduction with Even Modulus”,
// IEE Proceedings: Computers and Digital Techniques, 141(5) 314-316, September 1994.
// http://www.people.vcu.edu/~jwang3/CMSC691/j34monex.pdf
func (z nat) expNNMontgomeryEven(stk *stack, x, y, m nat) nat {
	// 将 m 拆分为 m = m₁ × m₂，其中 m₁ = 2ⁿ
	n := m.trailingZeroBits()
	m1 := nat(nil).lsh(natOne, n)
	m2 := nat(nil).rsh(m, n)

	// 我们希望得到 z = x**y mod m。
	// z₁ = x**y mod m1 = (x**y mod m) mod m1 = z mod m1
	// z₂ = x**y mod m2 = (x**y mod m) mod m2 = z mod m2
	// （这里我们采用 math/big 的命名约定，
	// 即计算 z = x**y mod m，所以其各部分为 z1 和 z2。
	// 论文中计算的是 x = a**e mod n；它将这些称为 x2 和 z1。）
	z1 := nat(nil).expNN(stk, x, y, m1, false)
	z2 := nat(nil).expNN(stk, x, y, m2, false)

	// 使用 CRT 从 z₁、z₂ 重建 z，采用论文中的算法，
	// 该算法只使用一次 modInverse（而且是一次简单的求逆）。
	//	p = (z₁ - z₂) × m₂⁻¹ (mod m₁)
	//	z = z₂ + p × m₂
	// 最后一次加法在范围内，因为：
	//	z = z₂ + p × m₂
	//	  ≤ z₂ + (m₁-1) × m₂
	//	  < m₂ + (m₁-1) × m₂
	//	  = m₁ × m₂
	//	  = m.
	z = z.set(z2)

	// 计算 (z₁ - z₂) mod m1 [m1 == 2**n] 并存入 z1。
	z1 = z1.subMod2N(z1, z2, n)

	// 复用 z2 存放 p = (z₁ - z₂) [在 z1 中] * m2⁻¹ (mod m₁ [= 2ⁿ])。
	m2inv := nat(nil).modInverse(m2, m1)
	z2 = z2.mul(stk, z1, m2inv)
	z2 = z2.trunc(z2, n)

	// 复用 z1 存放 p * m2。
	z = z.add(z, z1.mul(stk, z2, m2))

	return z
}

// expNNWindowed 使用固定的 4 位窗口计算 x**y mod m，
// 其中 m = 2**logM。
func (z nat) expNNWindowed(stk *stack, x, y nat, logM uint) nat {
	if len(y) <= 1 {
		panic("big: misuse of expNNWindowed")
	}
	if x[0]&1 == 0 {
		// len(y) > 1，所以 y > logM。
		// x 为偶数，所以 x**y 是 2**y 的倍数，而后者是 2**logM 的倍数。
		return z.setWord(0)
	}
	if logM == 1 {
		return z.setWord(1)
	}

	// 使用 zz 以避免在 mul 中分配内存，否则
	// 参数会互为别名。
	defer stk.restore(stk.save())
	w := int((logM + _W - 1) / _W)
	zz := stk.nat(w)

	const n = 4
	// powers[i] 保存 x^i。
	var powers [1 << n]nat
	for i := range powers {
		powers[i] = stk.nat(w)
	}
	powers[0] = powers[0].set(natOne)
	powers[1] = powers[1].trunc(x, logM)
	for i := 2; i < 1<<n; i += 2 {
		p2, p, p1 := &powers[i/2], &powers[i], &powers[i+1]
		*p = p.sqr(stk, *p2)
		*p = p.trunc(*p, logM)
		*p1 = p1.mul(stk, *p, x)
		*p1 = p1.trunc(*p1, logM)
	}

	// 因为 phi(2**logM) = 2**(logM-1)，所以 x**(2**(logM-1)) = 1，
	// 因此我们可以计算 x**(y mod 2**(logM-1)) 而非 x**y。
	// 也就是说，我们可以丢弃 y 除最低 logM-1 位之外的所有位。
	// 我们不分配新的 y，而是从正确的字开始读取 y，
	// 并在循环开始时适当地截断它。
	i := len(y) - 1
	mtop := int((logM - 2) / _W) // -2 是因为 N 位的最高字是第 (N-1)/W 个字。
	mmask := ^Word(0)
	if mbits := (logM - 1) & (_W - 1); mbits != 0 {
		mmask = (1 << mbits) - 1
	}
	if i > mtop {
		i = mtop
	}
	advance := false
	z = z.setWord(1)
	for ; i >= 0; i-- {
		yi := y[i]
		if i == mtop {
			yi &= mmask
		}
		for j := 0; j < _W; j += n {
			if advance {
				// 处理上一轮迭代中使用的 4 位。
				// 展开循环以获得显著的性能提升。
				// 在做出改动前，使用 crypto/rsa 中的
				// go test -bench=".*" 检查性能。
				zz = zz.sqr(stk, z)
				zz, z = z, zz
				z = z.trunc(z, logM)

				zz = zz.sqr(stk, z)
				zz, z = z, zz
				z = z.trunc(z, logM)

				zz = zz.sqr(stk, z)
				zz, z = z, zz
				z = z.trunc(z, logM)

				zz = zz.sqr(stk, z)
				zz, z = z, zz
				z = z.trunc(z, logM)
			}

			zz = zz.mul(stk, z, powers[yi>>(_W-n)])
			zz, z = z, zz
			z = z.trunc(z, logM)

			yi <<= n
			advance = true
		}
	}

	return z.norm()
}

// expNNMontgomery 使用固定的 4 位窗口计算 x**y mod m。
// 使用 Montgomery 表示。
func (z nat) expNNMontgomery(stk *stack, x, y, m nat) nat {
	numWords := len(m)

	// 我们希望 x 和 m 的长度相等。
	// 只要 len(x) == len(m)，x >= m 也可以。
	if len(x) > numWords {
		_, x = nat(nil).div(stk, nil, x, m)
		// 注意：现在 len(x) <= numWords，不保证 ==。
	}
	if len(x) < numWords {
		rr := make(nat, numWords)
		copy(rr, x)
		x = rr
	}

	// 理想情况下预计算应在外部完成并复用
	// k0 = -m**-1 mod 2**_W。算法来自：Dumas, J.G. "On Newton–Raphson
	// Iteration for Multiplicative Inverses Modulo Prime Powers"。
	k0 := 2 - m[0]
	t := m[0] - 1
	for i := 1; i < _W; i <<= 1 {
		t *= t
		k0 *= (t + 1)
	}
	k0 = -k0

	// RR = 2**(2*_W*len(m)) mod m
	RR := nat(nil).setWord(1)
	zz := nat(nil).lsh(RR, uint(2*numWords*_W))
	_, RR = nat(nil).div(stk, RR, zz, m)
	if len(RR) < numWords {
		zz = zz.make(numWords)
		copy(zz, RR)
		RR = zz
	}
	// one = 1，长度与 m 相同
	one := make(nat, numWords)
	one[0] = 1

	const n = 4
	// powers[i] 保存 x^i
	var powers [1 << n]nat
	powers[0] = powers[0].montgomery(one, RR, m, k0, numWords)
	powers[1] = powers[1].montgomery(x, RR, m, k0, numWords)
	for i := 2; i < 1<<n; i++ {
		powers[i] = powers[i].montgomery(powers[i-1], powers[1], m, k0, numWords)
	}

	// 初始化 z = 1（Montgomery 1）
	z = z.make(numWords)
	copy(z, powers[0])

	zz = zz.make(numWords)

	// 相同的窗口化指数运算，但使用 Montgomery 乘法
	for i := len(y) - 1; i >= 0; i-- {
		yi := y[i]
		for j := 0; j < _W; j += n {
			if i != len(y)-1 || j != 0 {
				zz = zz.montgomery(z, z, m, k0, numWords)
				z = z.montgomery(zz, zz, m, k0, numWords)
				zz = zz.montgomery(z, z, m, k0, numWords)
				z = z.montgomery(zz, zz, m, k0, numWords)
			}
			zz = zz.montgomery(z, powers[yi>>(_W-n)], m, k0, numWords)
			z, zz = zz, z
			yi <<= n
		}
	}
	// 转换为常规数
	zz = zz.montgomery(z, one, m, k0, numWords)

	// 最后一次化简，以防万一。
	// 参见 golang.org/issue/13907。
	if zz.cmp(m) >= 0 {
		// 常见情况是 m 的最高位被置位；这种情况下，
		// 由于 zz 与 m 长度相同，最多只需
		// 移除一个 m 的倍数。直接做减法即可。
		// 我们认为一般情况下减法就足够了，
		// 所以无条件地做减法，但再核对一次，
		// 以防我们的判断有误。
		// 预计不会执行到这里的 div。
		zz = zz.sub(zz, m)
		if zz.cmp(m) >= 0 {
			_, zz = nat(nil).div(stk, nil, zz, m)
		}
	}

	return zz.norm()
}

// bytes 使用大端编码将 z 的值写入 buf。
// z 的值被编码在切片 buf[i:] 中。如果 z 的值
// 无法用 buf 表示，bytes 会 panic。返回 buf 开头
// 未使用字节数 i 作为结果。
func (z nat) bytes(buf []byte) (i int) {
	// 此函数用于密码学操作。它不能通过边信道泄露
	// 除 Int 的符号和位长之外的任何信息。任何
	// 修改都必须由安全专家审查。
	i = len(buf)
	for _, d := range z {
		for j := 0; j < _S; j++ {
			i--
			if i >= 0 {
				buf[i] = byte(d)
			} else if byte(d) != 0 {
				panic("math/big: buffer too small to fit value")
			}
			d >>= 8
		}
	}

	if i < 0 {
		i = 0
	}
	for i < len(buf) && buf[i] == 0 {
		i++
	}

	return
}

// bigEndianWord 将 buf 的内容按大端编码解释为一个 Word 值并返回。
func bigEndianWord(buf []byte) Word {
	if _W == 64 {
		return Word(byteorder.BEUint64(buf))
	}
	return Word(byteorder.BEUint32(buf))
}

// setBytes 将 buf 解释为大端无符号整数的字节，
// 将 z 置为该值并返回 z。
func (z nat) setBytes(buf []byte) nat {
	z = z.make((len(buf) + _S - 1) / _S)

	i := len(buf)
	for k := 0; i >= _S; k++ {
		z[k] = bigEndianWord(buf[i-_S : i])
		i -= _S
	}
	if i > 0 {
		var d Word
		for s := uint(0); i > 0; s += 8 {
			d |= Word(buf[i-1]) << s
			i--
		}
		z[len(z)-1] = d
	}

	return z.norm()
}

// sqrt 设置 z = ⌊√x⌋
// 调用者可以传入 stk == nil，以请求 sqrt 自行获取并释放一个。
func (z nat) sqrt(stk *stack, x nat) nat {
	if x.cmp(natOne) <= 0 {
		return z.set(x)
	}
	if alias(z, x) {
		z = nil
	}

	if stk == nil {
		stk = getStack()
		defer stk.free()
	}

	// 从一个已知过大的值开始，重复 "z = ⌊(z + ⌊x/z⌋)/2⌋" 直到它不再变小。
	// 参见 Brent and Zimmermann, Modern Computer Arithmetic, Algorithm 1.13 (SqrtInt)。
	// https://members.loria.fr/PZimmermann/mca/pub226.html
	// 如果 x 比一个完全平方数小 1，该序列会在正确的 z 与 z+1 之间振荡；
	// 否则它会收敛到正确的 z 并保持不变。
	var z1, z2 nat
	z1 = z
	z1 = z1.setUint64(1)
	z1 = z1.lsh(z1, uint(x.bitLen()+1)/2) // 必须 ≥ √x
	for n := 0; ; n++ {
		z2, _ = z2.div(stk, nil, x, z1)
		z2 = z2.add(z2, z1)
		z2 = z2.rsh(z2, 1)
		if z2.cmp(z1) >= 0 {
			// z1 是答案。
			// 通过查看循环计数来判断当前是 z1 还是 z2 与 z 互为别名。
			if n&1 == 0 {
				return z1
			}
			return z.set(z1)
		}
		z1, z2 = z2, z1
	}
}

// subMod2N 返回 z = (x - y) mod 2ⁿ。
func (z nat) subMod2N(x, y nat, n uint) nat {
	if uint(x.bitLen()) > n {
		if alias(z, x) {
			// 可以就地覆盖 x
			x = x.trunc(x, n)
		} else {
			x = nat(nil).trunc(x, n)
		}
	}
	if uint(y.bitLen()) > n {
		if alias(z, y) {
			// 可以就地覆盖 y
			y = y.trunc(y, n)
		} else {
			y = nat(nil).trunc(y, n)
		}
	}
	if x.cmp(y) >= 0 {
		return z.sub(x, y)
	}
	// x - y < 0; x - y mod 2ⁿ = x - y + 2ⁿ = 2ⁿ - (y - x) = 1 + 2ⁿ-1 - (y - x) = 1 + ^(y - x).
	z = z.sub(y, x)
	for uint(len(z))*_W < n {
		z = append(z, 0)
	}
	for i := range z {
		z[i] = ^z[i]
	}
	z = z.trunc(z, n)
	return z.add(z, natOne)
}
