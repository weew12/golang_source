// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package big

import "math/rand"

// ProbablyPrime 报告 x 是否可能为素数，
// 使用 n 个伪随机选择的基进行 Miller-Rabin 检验，
// 以及一次 Baillie-PSW 检验。
//
// 如果 x 是素数，ProbablyPrime 返回 true。
// 如果 x 是随机选取的且不是素数，ProbablyPrime 很可能返回 false。
// 对于随机选取的非素数，返回 true 的概率至多为 ¼ⁿ。
//
// 对于小于 2⁶⁴ 的输入，ProbablyPrime 是 100% 准确的。
// 关于错误概率的进一步讨论，参见 Menezes 等人，Handbook of Applied
// Cryptography, 1997, pp. 145-149，以及 FIPS 186-4 Appendix F。
//
// ProbablyPrime 不适合用于判断对手可能精心构造
// 以欺骗该检验的素数。
//
// 从 Go 1.8 起，允许 ProbablyPrime(0)，且它只应用一次 Baillie-PSW 检验。
// 在 Go 1.8 之前，ProbablyPrime 只应用 Miller-Rabin 检验，
// 并且 ProbablyPrime(0) 会 panic。
func (x *Int) ProbablyPrime(n int) bool {
	// 关于上面文档注释的说明：
	// 更精确的说法是 Baillie-PSW 检验使用“超强”Lucas 检验作为其 Lucas 检验，
	// 但由于没人知道如何在 Baillie-PSW 检验内部区分这些 Lucas 检验
	// （从经验上看它们的效果都一样），
	// 因此无需记录或隐式保证这一细节。
	// 由于这种普遍的模糊性，上述注释特意避免说“那个”Baillie-PSW 检验。

	if n < 0 {
		panic("negative n for ProbablyPrime")
	}
	if x.neg || len(x.abs) == 0 {
		return false
	}

	// primeBitMask 记录小于 64 的素数。
	const primeBitMask uint64 = 1<<2 | 1<<3 | 1<<5 | 1<<7 |
		1<<11 | 1<<13 | 1<<17 | 1<<19 | 1<<23 | 1<<29 | 1<<31 |
		1<<37 | 1<<41 | 1<<43 | 1<<47 | 1<<53 | 1<<59 | 1<<61

	w := x.abs[0]
	if len(x.abs) == 1 && w < 64 {
		return primeBitMask&(1<<w) != 0
	}

	if w&1 == 0 {
		return false // x 是偶数
	}

	const primesA = 3 * 5 * 7 * 11 * 13 * 17 * 19 * 23 * 37
	const primesB = 29 * 31 * 41 * 43 * 47 * 53

	var rA, rB uint32
	switch _W {
	case 32:
		rA = uint32(x.abs.modW(primesA))
		rB = uint32(x.abs.modW(primesB))
	case 64:
		r := x.abs.modW((primesA * primesB) & _M)
		rA = uint32(r % primesA)
		rB = uint32(r % primesB)
	default:
		panic("math/big: invalid word size")
	}

	if rA%3 == 0 || rA%5 == 0 || rA%7 == 0 || rA%11 == 0 || rA%13 == 0 || rA%17 == 0 || rA%19 == 0 || rA%23 == 0 || rA%37 == 0 ||
		rB%29 == 0 || rB%31 == 0 || rB%41 == 0 || rB%43 == 0 || rB%47 == 0 || rB%53 == 0 {
		return false
	}

	stk := getStack()
	defer stk.free()
	return x.abs.probablyPrimeMillerRabin(stk, n+1, true) && x.abs.probablyPrimeLucas(stk)
}

// probablyPrimeMillerRabin 报告 n 是否通过 reps 轮 Miller-Rabin
// 素性检验，且使用伪随机选择的基。
// 如果 force2 为 true，则其中一轮强制使用基 2。
// 参见 Handbook of Applied Cryptography, p. 139, Algorithm 4.24。
// 已知 n 非零。
func (n nat) probablyPrimeMillerRabin(stk *stack, reps int, force2 bool) bool {
	nm1 := nat(nil).sub(n, natOne)
	// 确定 q、k，使得 nm1 = q << k
	k := nm1.trailingZeroBits()
	q := nat(nil).rsh(nm1, k)

	nm3 := nat(nil).sub(nm1, natTwo)
	rand := rand.New(rand.NewSource(int64(n[0])))

	var x, y, quotient nat
	nm3Len := nm3.bitLen()

NextRandom:
	for i := 0; i < reps; i++ {
		if i == reps-1 && force2 {
			x = x.set(natTwo)
		} else {
			x = x.random(rand, nm3, nm3Len)
			x = x.add(x, natTwo)
		}
		y = y.expNN(stk, x, q, n, false)
		if y.cmp(natOne) == 0 || y.cmp(nm1) == 0 {
			continue
		}
		for j := uint(1); j < k; j++ {
			y = y.sqr(stk, y)
			quotient, y = quotient.div(stk, y, y, n)
			if y.cmp(nm1) == 0 {
				continue NextRandom
			}
			if y.cmp(natOne) == 0 {
				return false
			}
		}
		return false
	}

	return true
}

// probablyPrimeLucas 报告 n 是否通过 "almost extra strong" Lucas 可能素数检验，
// 使用 Baillie-OEIS 参数选择。这对应于 Jacobsen 表中的 "AESLPSP"（链接见下）。
// 该检验与一次基为 2 的 Miller-Rabin/Fermat 检验相结合即构成 Baillie-PSW 检验。
//
// 参考文献：
//
// Baillie and Wagstaff, "Lucas Pseudoprimes", Mathematics of Computation 35(152),
// October 1980, pp. 1391-1417, especially page 1401.
// https://www.ams.org/journals/mcom/1980-35-152/S0025-5718-1980-0583518-6/S0025-5718-1980-0583518-6.pdf
//
// Grantham, "Frobenius Pseudoprimes", Mathematics of Computation 70(234),
// March 2000, pp. 873-891.
// https://www.ams.org/journals/mcom/2001-70-234/S0025-5718-00-01197-2/S0025-5718-00-01197-2.pdf
//
// Baillie, "Extra strong Lucas pseudoprimes", OEIS A217719, https://oeis.org/A217719.
//
// Jacobsen, "Pseudoprime Statistics, Tables, and Data", http://ntheory.org/pseudoprimes.html.
//
// Nicely, "The Baillie-PSW Primality Test", https://web.archive.org/web/20191121062007/http://www.trnicely.net/misc/bpsw.html.
// （注意 Nicely 对"extra strong"检验的定义给出了错误的 Jacobi 条件，
// 正如 Jacobsen 所指出的。）
//
// Crandall and Pomerance, Prime Numbers: A Computational Perspective, 2nd ed.
// Springer, 2005.
func (n nat) probablyPrimeLucas(stk *stack) bool {
	// 丢弃 0、1。
	if len(n) == 0 || n.cmp(natOne) == 0 {
		return false
	}
	// 2 是唯一的偶素数。
	// 调用者已经检查过，但这里检查是为了能独立测试。
	if n[0]&1 == 0 {
		return n.cmp(natTwo) == 0
	}

	// Baillie-OEIS "method C"，用于选择 D、P、Q，
	// 见 https://oeis.org/A217719/a217719.txt：
	// 尝试递增 P ≥ 3，使得 D = P² - 4（所以 Q = 1），
	// 直到 Jacobi(D, n) = -1。
	// 对于非平方数 n，预计只需几次尝试就能成功。
	// 在超过预期次数的失败之后，检查 n 是否为平方数
	// （若是，则对所有不整除 n 的 D 都有 Jacobi(D, n) = 1）。
	p := Word(3)
	d := nat{1}
	t1 := nat(nil) // 临时
	intD := &Int{abs: d}
	intN := &Int{abs: n}
	for ; ; p++ {
		if p > 10000 {
			// 人们普遍认为这是不可能的。
			// 如果我们收到相关报告，我们会想要确切的数 n。
			panic("math/big: internal error: cannot find (D/n) = -1 for " + intN.String())
		}
		d[0] = p*p - 4
		j := Jacobi(intD, intN)
		if j == -1 {
			break
		}
		if j == 0 {
			// d = p²-4 = (p-2)(p+2)。
			// 如果 (d/n) == 0，则 d 与 n 共享一个素因子。
			// 由于循环以递增的 p 进行，且从 p-2==1 开始，
			// 该共享的素因子必然是 p+2。
			// 如果 p+2 == n，则 n 为素数；否则 p+2 是 n 的一个真因子。
			return len(n) == 1 && n[0] == p+2
		}
		if p == 40 {
			// 如果 n 是平方数，我们永远找不到 (d/n) = -1。
			// 如果 n 不是平方数，平均而言我们预计只需几次尝试就能找到 d。
			// 经过 40 次尝试后，花点时间检查 n 是否确实是平方数。
			t1 = t1.sqrt(stk, n)
			t1 = t1.sqr(stk, t1)
			if t1.cmp(n) == 0 {
				return false
			}
		}
	}

	// Grantham 对 "extra strong Lucas pseudoprime" 的定义，见 p. 876 的 Thm 2.3
	// （上面的 D、P、Q 已变成 Δ、b、1）：
	//
	// 令 U_n = U_n(b, 1)，V_n = V_n(b, 1)，Δ = b²-4。
	// 基为 b 的 extra strong Lucas 伪素数是满足如下条件的合数 n = 2^r s + Jacobi(Δ, n)，
	// 其中 s 为奇数且 gcd(n, 2*Δ) = 1，使得或者 (i) U_s ≡ 0 mod n 且 V_s ≡ ±2 mod n，
	// 或者 (ii) 对某个 0 ≤ t < r-1 有 V_{2^t s} ≡ 0 mod n。
	//
	// 我们知道 gcd(n, Δ) = 1，否则上面就会找到 Jacobi(d, n) == 0。
	// 我们知道 gcd(n, 2) = 1，因为 n 为奇数。
	//
	// 安排 s = (n - Jacobi(Δ, n)) / 2^r = (n+1) / 2^r。
	s := nat(nil).add(n, natOne)
	r := int(s.trailingZeroBits())
	s = s.rsh(s, uint(r))
	nm2 := nat(nil).sub(n, natTwo) // n-2

	// 我们应用 "almost extra strong" 检验，它检查上述条件，
	// 但不包括 U_s ≡ 0 mod n，这使我们得以避免计算任何 U_k 值。
	// Jacobsen 指出，也许我们应该直接做完整的 extra strong 检验：
	// "It is also possible to recover U_n using Crandall and Pomerance equation 3.13:
	// U_n = D^-1 (2V_{n+1} - PV_n) allowing us to run the full extra-strong test
	// at the cost of a single modular inversion. This computation is easy and fast in GMP,
	// so we can get the full extra-strong test at essentially the same performance as the
	// almost extra strong test."

	// 计算 Lucas 序列 V_s(b, 1)，其中：
	//
	//	V(0) = 2
	//	V(1) = P
	//	V(k) = P V(k-1) - Q V(k-2).
	//
	// （记住由于上面的 method C，P = b，Q = 1。）
	//
	// 一般来说 V(k) = α^k + β^k，其中 α 和 β 是 x² - Px + Q 的根。
	// Crandall 和 Pomerance（p.147）观察到，对于 0 ≤ j ≤ k，
	//
	//	V(j+k) = V(j)V(k) - V(k-j).
	//
	// 所以特别地，要快速地将下标加倍：
	//
	//	V(2k) = V(k)² - 2
	//	V(2k+1) = V(k) V(k+1) - P
	//
	// 因此我们可以从 k=0 开始，在 log₂(s) 步内构建到 k=s。
	natP := nat(nil).setWord(p)
	vk := nat(nil).setWord(2)
	vk1 := nat(nil).setWord(p)
	t2 := nat(nil) // 临时
	for i := int(s.bitLen()); i >= 0; i-- {
		if s.bit(uint(i)) != 0 {
			// k' = 2k+1
			// V(k') = V(2k+1) = V(k) V(k+1) - P.
			t1 = t1.mul(stk, vk, vk1)
			t1 = t1.add(t1, n)
			t1 = t1.sub(t1, natP)
			t2, vk = t2.div(stk, vk, t1, n)
			// V(k'+1) = V(2k+2) = V(k+1)² - 2.
			t1 = t1.sqr(stk, vk1)
			t1 = t1.add(t1, nm2)
			t2, vk1 = t2.div(stk, vk1, t1, n)
		} else {
			// k' = 2k
			// V(k'+1) = V(2k+1) = V(k) V(k+1) - P.
			t1 = t1.mul(stk, vk, vk1)
			t1 = t1.add(t1, n)
			t1 = t1.sub(t1, natP)
			t2, vk1 = t2.div(stk, vk1, t1, n)
			// V(k') = V(2k) = V(k)² - 2
			t1 = t1.sqr(stk, vk)
			t1 = t1.add(t1, nm2)
			t2, vk = t2.div(stk, vk, t1, n)
		}
	}

	// 现在 k=s，所以 vk = V(s)。检查 V(s) ≡ ±2 (mod n)。
	if vk.cmp(natTwo) == 0 || vk.cmp(nm2) == 0 {
		// 检查 U(s) ≡ 0。
		// 按照 Jacobsen 的建议，应用 Crandall 和 Pomerance 的公式 3.13：
		//
		//	U(k) = D⁻¹ (2 V(k+1) - P V(k))
		//
		// 由于我们检查的是 U(k) == 0，只需检查 2 V(k+1) == P V(k) mod n，
		// 即 P V(k) - 2 V(k+1) == 0 mod n。
		t1 := t1.mul(stk, vk, natP)
		t2 := t2.lsh(vk1, 1)
		if t1.cmp(t2) < 0 {
			t1, t2 = t2, t1
		}
		t1 = t1.sub(t1, t2)
		t3 := vk1 // 复用 vk1，下面不再需要它
		vk1 = nil
		_ = vk1
		t2, t3 = t2.div(stk, t3, t1, n)
		if len(t3) == 0 {
			return true
		}
	}

	// 检查对某个 0 ≤ t < r-1 是否有 V(2^t s) ≡ 0 mod n。
	for t := 0; t < r-1; t++ {
		if len(vk) == 0 { // vk == 0
			return true
		}
		// 优化：V(k) = 2 是 V(k') = V(k)² - 2 的不动点，
		// 所以如果 V(k) = 2，我们可以停止：我们将永远找不到未来某个 V(k) == 0。
		if len(vk) == 1 && vk[0] == 2 { // vk == 2
			return false
		}
		// k' = 2k
		// V(k') = V(2k) = V(k)² - 2
		t1 = t1.sqr(stk, vk)
		t1 = t1.sub(t1, natTwo)
		t2, vk = t2.div(stk, vk, t1, n)
	}
	return false
}
