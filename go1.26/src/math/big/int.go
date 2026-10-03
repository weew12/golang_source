// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现有符号多精度整数。

package big

import (
	"fmt"
	"io"
	"math/rand"
	"strings"
)

// An Int 表示一个有符号多精度整数。
// Int 的零值表示值 0。
//
// 运算总是接受指针参数（*Int）而非 Int 值，并且每个唯一的 Int 值
// 都需要其自己唯一的 *Int 指针。要“复制”一个 Int 值，必须使用
// [Int.Set] 方法将现有（或新分配的）Int 设置为新值；不支持 Int 的
// 浅拷贝，且可能导致错误。
//
// 注意，方法可能会通过时序侧信道泄露 Int 的值。由于这一点，以及
// 实现的规模和复杂性，Int 并不适合实现密码学运算。标准库避免将
// 非平凡的 Int 方法暴露给攻击者可控的输入，并且判定 math/big 中的
// 某个 bug 是否被视为安全漏洞可能取决于其对标准库的影响。
type Int struct {
	neg bool // 符号
	abs nat  // 整数的绝对值
}

var intOne = &Int{false, natOne}

// Sign 返回：
//   - -1 若 x < 0；
//   - 0 若 x == 0；
//   - +1 若 x > 0。
func (x *Int) Sign() int {
	// 此函数用于密码学运算。它除 Int 的符号和位大小外，不得通过
	// 侧信道泄露任何信息。任何修改都必须经过安全专家审查。
	if len(x.abs) == 0 {
		return 0
	}
	if x.neg {
		return -1
	}
	return 1
}

// SetInt64 将 z 设为 x 并返回 z。
func (z *Int) SetInt64(x int64) *Int {
	neg := false
	if x < 0 {
		neg = true
		x = -x
	}
	z.abs = z.abs.setUint64(uint64(x))
	z.neg = neg
	return z
}

// SetUint64 将 z 设为 x 并返回 z。
func (z *Int) SetUint64(x uint64) *Int {
	z.abs = z.abs.setUint64(x)
	z.neg = false
	return z
}

// NewInt 分配并返回一个设为 x 的新 [Int]。
func NewInt(x int64) *Int {
	// 这段代码经过安排，以便可内联，并在内联时产生零分配。参见 issue 29951。
	u := uint64(x)
	if x < 0 {
		u = -u
	}
	var abs []Word
	if x == 0 {
	} else if _W == 32 && u>>32 != 0 {
		abs = []Word{Word(u), Word(u >> 32)}
	} else {
		abs = []Word{Word(u)}
	}
	return &Int{neg: x < 0, abs: abs}
}

// Set 将 z 设为 x 并返回 z。
func (z *Int) Set(x *Int) *Int {
	if z != x {
		z.abs = z.abs.set(x.abs)
		z.neg = x.neg
	}
	return z
}

// Bits 以 little-endian [Word] 切片的形式返回 x 的绝对值，从而提供对 x 的
// 原始（未检查但快速）访问。结果与 x 共享同一底层数组。
// Bits 旨在支持在本包之外实现缺失的底层 [Int] 功能；除此之外应避免使用它。
func (x *Int) Bits() []Word {
	// 此函数用于密码学运算。它除 Int 的符号和位大小外，不得通过
	// 侧信道泄露任何信息。任何修改都必须经过安全专家审查。
	return x.abs
}

// SetBits 将 z 的值设为 abs（解释为 little-endian [Word] 切片）并返回 z，
// 从而提供对 z 的原始（未检查但快速）访问。结果与 abs 共享同一底层数组。
// SetBits 旨在支持在本包之外实现缺失的底层 [Int] 功能；除此之外应避免使用它。
func (z *Int) SetBits(abs []Word) *Int {
	z.abs = nat(abs).norm()
	z.neg = false
	return z
}

// Abs 将 z 设为 |x|（x 的绝对值）并返回 z。
func (z *Int) Abs(x *Int) *Int {
	z.Set(x)
	z.neg = false
	return z
}

// Neg 将 z 设为 -x 并返回 z。
func (z *Int) Neg(x *Int) *Int {
	z.Set(x)
	z.neg = len(z.abs) > 0 && !z.neg // 0 没有符号
	return z
}

// Add 将 z 设为和 x+y 并返回 z。
func (z *Int) Add(x, y *Int) *Int {
	neg := x.neg
	if x.neg == y.neg {
		// x + y == x + y
		// (-x) + (-y) == -(x + y)
		z.abs = z.abs.add(x.abs, y.abs)
	} else {
		// x + (-y) == x - y == -(y - x)
		// (-x) + y == y - x == -(x - y)
		if x.abs.cmp(y.abs) >= 0 {
			z.abs = z.abs.sub(x.abs, y.abs)
		} else {
			neg = !neg
			z.abs = z.abs.sub(y.abs, x.abs)
		}
	}
	z.neg = len(z.abs) > 0 && neg // 0 没有符号
	return z
}

// Sub 将 z 设为差 x-y 并返回 z。
func (z *Int) Sub(x, y *Int) *Int {
	neg := x.neg
	if x.neg != y.neg {
		// x - (-y) == x + y
		// (-x) - y == -(x + y)
		z.abs = z.abs.add(x.abs, y.abs)
	} else {
		// x - y == x - y == -(y - x)
		// (-x) - (-y) == y - x == -(x - y)
		if x.abs.cmp(y.abs) >= 0 {
			z.abs = z.abs.sub(x.abs, y.abs)
		} else {
			neg = !neg
			z.abs = z.abs.sub(y.abs, x.abs)
		}
	}
	z.neg = len(z.abs) > 0 && neg // 0 没有符号
	return z
}

// Mul 将 z 设为乘积 x*y 并返回 z。
func (z *Int) Mul(x, y *Int) *Int {
	z.mul(nil, x, y)
	return z
}

// mul 类似于 Mul，但接受一个显式使用的栈，供内部使用。
// 它不返回 *Int，因为这样做会使 natmul.go 中使用的栈分配 Int
// 逃逸到堆上（即便结果未被使用）。
func (z *Int) mul(stk *stack, x, y *Int) {
	// x * y == x * y
	// x * (-y) == -(x * y)
	// (-x) * y == -(x * y)
	// (-x) * (-y) == x * y
	if x == y {
		z.abs = z.abs.sqr(stk, x.abs)
		z.neg = false
		return
	}
	z.abs = z.abs.mul(stk, x.abs, y.abs)
	z.neg = len(z.abs) > 0 && x.neg != y.neg // 0 没有符号
}

// MulRange 将 z 设为区间 [a, b]（含端点）内所有整数的乘积并返回 z。
// 如果 a > b（空区间），结果为 1。
func (z *Int) MulRange(a, b int64) *Int {
	switch {
	case a > b:
		return z.SetInt64(1) // 空区间
	case a <= 0 && b >= 0:
		return z.SetInt64(0) // 区间包含 0
	}
	// a <= b && (b < 0 || a > 0)

	neg := false
	if a < 0 {
		neg = (b-a)&1 == 0
		a, b = -b, -a
	}

	z.abs = z.abs.mulRange(nil, uint64(a), uint64(b))
	z.neg = neg
	return z
}

// Binomial 将 z 设为二项式系数 C(n, k) 并返回 z。
func (z *Int) Binomial(n, k int64) *Int {
	if k > n {
		return z.SetInt64(0)
	}
	// 通过减小 k 来减少乘法次数
	if k > n-k {
		k = n - k // C(n, k) == C(n, n-k)
	}
	// C(n, k) == n * (n-1) * ... * (n-k+1) / k * (k-1) * ... * 1
	//         == n * (n-1) * ... * (n-k+1) / 1 * (1+1) * ... * k
	//
	// 使用乘积公式在每一步产生的值更小，
	// 所需的分配和计算更少：
	//
	// z = 1
	// for i := 0; i < k; i = i+1 {
	//     z *= n-i
	//     z /= i+1
	// }
	//
	// 最后为了避免在每次循环中计算两次 i+1：
	//
	// z = 1
	// i := 0
	// for i < k {
	//     z *= n-i
	//     i++
	//     z /= i
	// }
	var N, K, i, t Int
	N.SetInt64(n)
	K.SetInt64(k)
	z.Set(intOne)
	for i.Cmp(&K) < 0 {
		z.Mul(z, t.Sub(&N, &i))
		i.Add(&i, intOne)
		z.Quo(z, &i)
	}
	return z
}

// Quo 将 z 设为商 x/y（y != 0）并返回 z。
// 如果 y == 0，会引发除零运行期 panic。
// Quo 实现截断除法（与 Go 相同）；更多细节参见 [Int.QuoRem]。
func (z *Int) Quo(x, y *Int) *Int {
	z.abs, _ = z.abs.div(nil, nil, x.abs, y.abs)
	z.neg = len(z.abs) > 0 && x.neg != y.neg // 0 没有符号
	return z
}

// Rem 将 z 设为余数 x%y（y != 0）并返回 z。
// 如果 y == 0，会引发除零运行期 panic。
// Rem 实现截断取模（与 Go 相同）；更多细节参见 [Int.QuoRem]。
func (z *Int) Rem(x, y *Int) *Int {
	_, z.abs = nat(nil).div(nil, z.abs, x.abs, y.abs)
	z.neg = len(z.abs) > 0 && x.neg // 0 没有符号
	return z
}

// QuoRem 将 z 设为商 x/y，将 r 设为余数 x%y，
// 并对 y != 0 返回对 (z, r)。
// 如果 y == 0，会引发除零运行期 panic。
//
// QuoRem 实现 T-除法和取模（与 Go 相同）：
//
//	q = x/y      结果向零截断
//	r = x - y*q
//
// （参见 Daan Leijen，“Division and Modulus for Computer Scientists”。）
// 欧几里得除法和取模（与 Go 不同）参见 [Int.DivMod]。
func (z *Int) QuoRem(x, y, r *Int) (*Int, *Int) {
	z.abs, r.abs = z.abs.div(nil, r.abs, x.abs, y.abs)
	z.neg, r.neg = len(z.abs) > 0 && x.neg != y.neg, len(r.abs) > 0 && x.neg // 0 没有符号
	return z, r
}

// Div 将 z 设为商 x/y（y != 0）并返回 z。
// 如果 y == 0，会引发除零运行期 panic。
// Div 实现欧几里得除法（与 Go 不同）；更多细节参见 [Int.DivMod]。
func (z *Int) Div(x, y *Int) *Int {
	y_neg := y.neg // z 可能是 y 的别名
	var r Int
	z.QuoRem(x, y, &r)
	if r.neg {
		if y_neg {
			z.Add(z, intOne)
		} else {
			z.Sub(z, intOne)
		}
	}
	return z
}

// Mod 将 z 设为模 x%y（y != 0）并返回 z。
// 如果 y == 0，会引发除零运行期 panic。
// Mod 实现欧几里得取模（与 Go 不同）；更多细节参见 [Int.DivMod]。
func (z *Int) Mod(x, y *Int) *Int {
	y0 := y // 保存 y
	if z == y || alias(z.abs, y.abs) {
		y0 = new(Int).Set(y)
	}
	var q Int
	q.QuoRem(x, y, z)
	if z.neg {
		if y0.neg {
			z.Sub(z, y0)
		} else {
			z.Add(z, y0)
		}
	}
	return z
}

// DivMod 将 z 设为商 x div y，将 m 设为模 x mod y，
// 并对 y != 0 返回对 (z, m)。
// 如果 y == 0，会引发除零运行期 panic。
//
// DivMod 实现欧几里得除法和取模（与 Go 不同）：
//
//	q = x div y  使得
//	m = x - y*q  且 0 <= m < |y|
//
// （参见 Raymond T. Boute，“The Euclidean definition of the functions
// div and mod”。ACM Transactions on Programming Languages and
// Systems (TOPLAS), 14(2):127-144, New York, NY, USA, 4/1992.
// ACM press.）
// T-除法和取模（与 Go 相同）参见 [Int.QuoRem]。
func (z *Int) DivMod(x, y, m *Int) (*Int, *Int) {
	y0 := y // 保存 y
	if z == y || alias(z.abs, y.abs) {
		y0 = new(Int).Set(y)
	}
	z.QuoRem(x, y, m)
	if m.neg {
		if y0.neg {
			z.Add(z, intOne)
			m.Sub(m, y0)
		} else {
			z.Sub(z, intOne)
			m.Add(m, y0)
		}
	}
	return z, m
}

// Cmp 比较 x 和 y 并返回：
//   - -1 若 x < y；
//   - 0 若 x == y；
//   - +1 若 x > y。
func (x *Int) Cmp(y *Int) (r int) {
	// x cmp y == x cmp y
	// x cmp (-y) == x
	// (-x) cmp y == y
	// (-x) cmp (-y) == -(x cmp y)
	switch {
	case x == y:
		// 无事可做
	case x.neg == y.neg:
		r = x.abs.cmp(y.abs)
		if x.neg {
			r = -r
		}
	case x.neg:
		r = -1
	default:
		r = 1
	}
	return
}

// CmpAbs 比较 x 和 y 的绝对值并返回：
//   - -1 若 |x| < |y|；
//   - 0 若 |x| == |y|；
//   - +1 若 |x| > |y|。
func (x *Int) CmpAbs(y *Int) int {
	return x.abs.cmp(y.abs)
}

// low32 返回 x 的最低有效 32 位。
func low32(x nat) uint32 {
	if len(x) == 0 {
		return 0
	}
	return uint32(x[0])
}

// low64 返回 x 的最低有效 64 位。
func low64(x nat) uint64 {
	if len(x) == 0 {
		return 0
	}
	v := uint64(x[0])
	if _W == 32 && len(x) > 1 {
		return uint64(x[1])<<32 | v
	}
	return v
}

// Int64 返回 x 的 int64 表示。
// 如果 x 无法用 int64 表示，结果未定义。
func (x *Int) Int64() int64 {
	v := int64(low64(x.abs))
	if x.neg {
		v = -v
	}
	return v
}

// Uint64 返回 x 的 uint64 表示。
// 如果 x 无法用 uint64 表示，结果未定义。
func (x *Int) Uint64() uint64 {
	return low64(x.abs)
}

// IsInt64 报告 x 能否用 int64 表示。
func (x *Int) IsInt64() bool {
	if len(x.abs) <= 64/_W {
		w := int64(low64(x.abs))
		return w >= 0 || x.neg && w == -w
	}
	return false
}

// IsUint64 报告 x 能否用 uint64 表示。
func (x *Int) IsUint64() bool {
	return !x.neg && len(x.abs) <= 64/_W
}

// Float64 返回最接近 x 的 float64 值，
// 以及发生了何种舍入的指示。
func (x *Int) Float64() (float64, Accuracy) {
	n := x.abs.bitLen() // 注意：仍使用较慢的密码学实现！
	if n == 0 {
		return 0.0, Exact
	}

	// 快速路径：不超过 53 个有效位。
	if n <= 53 || n < 64 && n-int(x.abs.trailingZeroBits()) <= 53 {
		f := float64(low64(x.abs))
		if x.neg {
			f = -f
		}
		return f, Exact
	}

	return new(Float).SetInt(x).Float64()
}

// SetString 将 z 设为按给定 base 解释的 s 的值，并返回 z 以及一个表示是否
// 成功的布尔值。整个字符串（而不只是前缀）必须有效才算成功。如果 SetString
// 失败，z 的值未定义，但返回值为 nil。
//
// base 参数必须是 0 或 2 到 [MaxBase] 之间的值。
// 对于 base 0，数字前缀决定实际的 base：“0b”或“0B”前缀选择 base 2，
// “0”、“0o”或“0O”选择 base 8，“0x”或“0X”选择 base 16。否则，所选
// base 为 10，且不接受前缀。
//
// 对于 base <= 36，小写和大写字母被视为相同：字母 'a' 到 'z' 和 'A' 到 'Z'
// 表示数字值 10 到 35。对于 base > 36，大写字母 'A' 到 'Z' 表示数字值
// 36 到 61。
//
// 对于 base 0，下划线字符“_”可以出现在 base 前缀与相邻数字之间，以及相邻
// 数字之间；此类下划线不改变数字的值。如果没有其他错误，下划线位置不正确
// 会被报告为错误。如果 base != 0，则不识别下划线，其行为与任何其他不是有效
// 数字的字符相同。
func (z *Int) SetString(s string, base int) (*Int, bool) {
	return z.setFromScanner(strings.NewReader(s), base)
}

// setFromScanner 在给定 io.ByteScanner 的情况下实现 SetString。
// 文档参见 SetString 的注释。
func (z *Int) setFromScanner(r io.ByteScanner, base int) (*Int, bool) {
	if _, _, err := z.scan(r, base); err != nil {
		return nil, false
	}
	// 必须已消费全部内容
	if _, err := r.ReadByte(); err != io.EOF {
		return nil, false
	}
	return z, true // err == io.EOF => scan 已消费 r 的全部内容
}

// SetBytes 将 buf 解释为大端无符号整数的字节，将 z 设为该值并返回 z。
func (z *Int) SetBytes(buf []byte) *Int {
	z.abs = z.abs.setBytes(buf)
	z.neg = false
	return z
}

// Bytes 以大端字节切片的形式返回 x 的绝对值。
//
// 要使用固定长度的切片或预分配的切片，请使用 [Int.FillBytes]。
func (x *Int) Bytes() []byte {
	// 此函数用于密码学运算。它除 Int 的符号和位大小外，不得通过
	// 侧信道泄露任何信息。任何修改都必须经过安全专家审查。
	buf := make([]byte, len(x.abs)*_S)
	return buf[x.abs.bytes(buf):]
}

// FillBytes 将 buf 设为 x 的绝对值，以零扩展的大端字节切片形式存储，并返回 buf。
//
// 如果 x 的绝对值无法放入 buf，FillBytes 会 panic。
func (x *Int) FillBytes(buf []byte) []byte {
	// 清空整个缓冲区。
	clear(buf)
	x.abs.bytes(buf)
	return buf
}

// BitLen 返回 x 绝对值的位长度。
// 0 的位长度为 0。
func (x *Int) BitLen() int {
	// 此函数用于密码学运算。它除 Int 的符号和位大小外，不得通过
	// 侧信道泄露任何信息。任何修改都必须经过安全专家审查。
	return x.abs.bitLen()
}

// TrailingZeroBits 返回 |x| 中连续的最低有效零位的数量。
func (x *Int) TrailingZeroBits() uint {
	return x.abs.trailingZeroBits()
}

// Exp 将 z 设为 x**y mod |m|（即忽略 m 的符号），并返回 z。
// 如果 m == nil 或 m == 0，则 z = x**y，除非 y <= 0，此时 z = 1。如果 m != 0、
// y < 0，且 x 与 m 不互质，则 z 保持不变并返回 nil。
//
// 特定大小输入的模幂运算不是密码学常数时间的操作。
func (z *Int) Exp(x, y, m *Int) *Int {
	return z.exp(x, y, m, false)
}

func (z *Int) expSlow(x, y, m *Int) *Int {
	return z.exp(x, y, m, true)
}

func (z *Int) exp(x, y, m *Int, slow bool) *Int {
	// 参见 Knuth，第 2 卷，4.6.3 节。
	xWords := x.abs
	if y.neg {
		if m == nil || len(m.abs) == 0 {
			return z.SetInt64(1)
		}
		// 对于 y < 0：x**y mod m == (x**(-1))**|y| mod m
		inverse := new(Int).ModInverse(x, m)
		if inverse == nil {
			return nil
		}
		xWords = inverse.abs
	}
	yWords := y.abs

	var mWords nat
	if m != nil {
		if z == m || alias(z.abs, m.abs) {
			m = new(Int).Set(m)
		}
		mWords = m.abs // 对于 m == 0，m.abs 可能为 nil
	}

	z.abs = z.abs.expNN(nil, xWords, yWords, mWords, slow)
	z.neg = len(z.abs) > 0 && x.neg && len(yWords) > 0 && yWords[0]&1 == 1 // 0 没有符号
	if z.neg && len(mWords) > 0 {
		// 使模运算结果为正
		z.abs = z.abs.sub(mWords, z.abs) // z == x**y mod |m| && 0 <= z < |m|
		z.neg = false
	}

	return z
}

// GCD 将 z 设为 a 和 b 的最大公约数并返回 z。
// 如果 x 或 y 不为 nil，GCD 会设置它们的值，使得 z = a*x + b*y。
//
// a 和 b 可以为正、零或负。（在 Go 1.14 之前，二者都必须 > 0。）无论 a 和 b
// 的符号如何，z 始终 >= 0。
//
// 如果 a == b == 0，GCD 设置 z = x = y = 0。
//
// 如果 a == 0 且 b != 0，GCD 设置 z = |b|，x = 0，y = sign(b) * 1。
//
// 如果 a != 0 且 b == 0，GCD 设置 z = |a|，x = sign(a) * 1，y = 0。
func (z *Int) GCD(x, y, a, b *Int) *Int {
	if len(a.abs) == 0 || len(b.abs) == 0 {
		lenA, lenB, negA, negB := len(a.abs), len(b.abs), a.neg, b.neg
		if lenA == 0 {
			z.Set(b)
		} else {
			z.Set(a)
		}
		z.neg = false
		if x != nil {
			if lenA == 0 {
				x.SetUint64(0)
			} else {
				x.SetUint64(1)
				x.neg = negA
			}
		}
		if y != nil {
			if lenB == 0 {
				y.SetUint64(0)
			} else {
				y.SetUint64(1)
				y.neg = negB
			}
		}
		return z
	}

	return z.lehmerGCD(x, y, a, b)
}

// lehmerSimulate 尝试使用 A 和 B 的前导数字来模拟若干欧几里得更新步骤。
// 它返回 u0、u1、v0、v1，使得 A 和 B 可以按下式更新：
//
//	A = u0*A + v0*B
//	B = u1*A + v1*B
//
// 要求：A >= B 且 len(B.abs) >= 2
// 由于我们使用完整的字进行计算以避免溢出，
// 我们使用 'even' 来跟踪余序列（cosequence）的符号。
// 对于偶数次迭代：u0, v1 >= 0 && u1, v0 <= 0
// 对于奇数次迭代：u0, v1 <= 0 && u1, v0 >= 0
func lehmerSimulate(A, B *Int) (u0, u1, v0, v1 Word, even bool) {
	// 初始化数字
	var a1, a2, u2, v2 Word

	m := len(B.abs) // m >= 2
	n := len(A.abs) // n >= m >= 2

	// 从 A 和 B 中提取最高位的 Word
	h := nlz(A.abs[n-1])
	a1 = A.abs[n-1]<<h | A.abs[n-2]>>(_W-h)
	// 如果长度不同，B 在高位可能含有隐式的零字
	switch {
	case n == m:
		a2 = B.abs[n-1]<<h | B.abs[n-2]>>(_W-h)
	case n == m+1:
		a2 = B.abs[n-2] >> (_W - h)
	default:
		a2 = 0
	}

	// 由于我们使用完整的字进行计算以避免溢出，
	// 我们使用 'even' 来跟踪余序列（cosequence）的符号。
	// 对于偶数次迭代：u0, v1 >= 0 && u1, v0 <= 0
	// 对于奇数次迭代：u0, v1 <= 0 && u1, v0 >= 0
	// 第一次迭代从 k=1（奇数）开始。
	even = false
	// 用于跟踪余序列的变量
	u0, u1, u2 = 0, 1, 0
	v0, v1, v2 = 0, 0, 1

	// 使用 Collins 的停止条件计算商和余序列。
	// 注意，在计算余数序列和余序列时不可能发生 Word 溢出，
	// 因为余序列的大小受输入大小限制。
	// 详见 Jebelean 的 4.2 节。
	for a2 >= v2 && a1-a2 >= v1+v2 {
		q, r := a1/a2, a1%a2
		a1, a2 = a2, r
		u0, u1, u2 = u1, u2, u1+q*u2
		v0, v1, v2 = v1, v2, v1+q*v2
		even = !even
	}
	return
}

// lehmerUpdate 更新输入 A 和 B，使得：
//
//	A = u0*A + v0*B
//	B = u1*A + v1*B
//
// 其中 u0、u1、v0、v1 的符号由 even 给出
// 当 even == true：u0, v1 >= 0 && u1, v0 <= 0
// 当 even == false：u0, v1 <= 0 && u1, v0 >= 0
// q、r、s、t 是临时变量，以避免乘法中的分配。
func lehmerUpdate(A, B, q, r *Int, u0, u1, v0, v1 Word, even bool) {
	mulW(q, B, even, v0)
	mulW(r, A, even, u1)
	mulW(A, A, !even, u0)
	mulW(B, B, !even, v1)
	A.Add(A, q)
	B.Add(B, r)
}

// mulW 将 z 设为 x * (-?)w
// 其中当 neg 为 true 时带负号。
func mulW(z, x *Int, neg bool, w Word) {
	z.abs = z.abs.mulAddWW(x.abs, w, 0)
	z.neg = x.neg != neg
}

// euclidUpdate 执行欧几里得 GCD 算法的单步。
// 如果 extended 为 true，它还会更新余序列 Ua、Ub。
// q 和 r 用作临时变量；其初始值被忽略。
func euclidUpdate(A, B, Ua, Ub, q, r *Int, extended bool) (nA, nB, nr, nUa, nUb *Int) {
	q.QuoRem(A, B, r)

	if extended {
		// Ua, Ub = Ub, Ua-q*Ub
		q.Mul(q, Ub)
		Ua, Ub = Ub, Ua
		Ub.Sub(Ub, q)
	}

	return B, r, A, Ua, Ub
}

// lehmerGCD 将 z 设为 a 和 b 的最大公约数，
// 二者都必须 != 0，并返回 z。
// 如果 x 或 y 不为 nil，则设置它们的值，使得 z = a*x + b*y。
// 参见 Knuth，《The Art of Computer Programming》，第 2 卷，4.5.2 节，算法 L。
// 此实现使用 Collins 改进后的条件，只需一个商，
// 并避免了单个 Word 溢出的可能性。
// 参见 Jebelean，“Improving the multiprecision Euclidean algorithm”，
// Design and Implementation of Symbolic Computation Systems，第 45-58 页。
// 余序列按 Cohen 等人《Handbook of Elliptic and Hyperelliptic Curve
// Cryptography》第 192 页的算法 10.45 更新。
func (z *Int) lehmerGCD(x, y, a, b *Int) *Int {
	var A, B, Ua, Ub *Int

	A = new(Int).Abs(a)
	B = new(Int).Abs(b)

	extended := x != nil || y != nil

	if extended {
		// Ua (Ub) 跟踪输入 a 被累加到 A (B) 中的次数。
		Ua = new(Int).SetInt64(1)
		Ub = new(Int)
	}

	// 用于多精度更新的临时变量
	q := new(Int)
	r := new(Int)

	// 确保 A >= B
	if A.abs.cmp(B.abs) < 0 {
		A, B = B, A
		Ub, Ua = Ua, Ub
	}

	// 循环不变式 A >= B
	for len(B.abs) > 1 {
		// 尝试使用 A 和 B 的前导字进行单精度计算。
		u0, u1, v0, v1, even := lehmerSimulate(A, B)

		// 多精度步骤
		if v0 != 0 {
			// 使用余序列模拟单精度步骤的效果。
			// A = u0*A + v0*B
			// B = u1*A + v1*B
			lehmerUpdate(A, B, q, r, u0, u1, v0, v1, even)

			if extended {
				// Ua = u0*Ua + v0*Ub
				// Ub = u1*Ua + v1*Ub
				lehmerUpdate(Ua, Ub, q, r, u0, u1, v0, v1, even)
			}

		} else {
			// 单个数字的计算未能模拟出任何商。
			// 执行标准欧几里得步骤。
			A, B, r, Ua, Ub = euclidUpdate(A, B, Ua, Ub, q, r, extended)
		}
	}

	if len(B.abs) > 0 {
		// 当 B 是单个 Word 时，扩展欧几里得算法的基本情况
		if len(A.abs) > 1 {
			// A 长于单个 Word，因此需要一次更新。
			A, B, r, Ua, Ub = euclidUpdate(A, B, Ua, Ub, q, r, extended)
		}
		if len(B.abs) > 0 {
			// A 和 B 都是单个 Word。
			aWord, bWord := A.abs[0], B.abs[0]
			if extended {
				var ua, ub, va, vb Word
				ua, ub = 1, 0
				va, vb = 0, 1
				even := true
				for bWord != 0 {
					q, r := aWord/bWord, aWord%bWord
					aWord, bWord = bWord, r
					ua, ub = ub, ua+q*ub
					va, vb = vb, va+q*vb
					even = !even
				}

				mulW(Ua, Ua, !even, ua)
				mulW(Ub, Ub, even, va)
				Ua.Add(Ua, Ub)
			} else {
				for bWord != 0 {
					aWord, bWord = bWord, aWord%bWord
				}
			}
			A.abs[0] = aWord
		}
	}
	negA := a.neg
	if y != nil {
		// 避免别名化下面除法所需的 b
		if y == b {
			B.Set(b)
		} else {
			B = b
		}
		// y = (z - a*x)/b
		y.Mul(a, Ua) // y 可以安全地别名 a
		if negA {
			y.neg = !y.neg
		}
		y.Sub(A, y)
		y.Div(y, B)
	}

	if x != nil {
		x.Set(Ua)
		if negA {
			x.neg = !x.neg
		}
	}

	z.Set(A)

	return z
}

// Rand 将 z 设为 [0, n) 中的伪随机数并返回 z。
//
// 由于它使用 [math/rand] 包，不得用于安全敏感的工作。请改用 [crypto/rand.Int]。
func (z *Int) Rand(rnd *rand.Rand, n *Int) *Int {
	// 在 if 检查之前不修改 z.neg，因为 z 和 n 可能互为别名。
	if n.neg || len(n.abs) == 0 {
		z.neg = false
		z.abs = nil
		return z
	}
	z.neg = false
	z.abs = z.abs.random(rnd, n.abs, n.abs.bitLen())
	return z
}

// ModInverse 将 z 设为 g 在环 ℤ/nℤ 中的乘法逆元
// 并返回 z。如果 g 和 n 不互质，则 g 在环 ℤ/nℤ 中没有乘法逆元。
// 在这种情况下，z 保持不变，返回值为 nil。如果 n == 0，会引发除零运行期 panic。
func (z *Int) ModInverse(g, n *Int) *Int {
	// GCD 期望参数 a 和 b > 0。
	if n.neg {
		var n2 Int
		n = n2.Neg(n)
	}
	if g.neg {
		var g2 Int
		g = g2.Mod(g, n)
	}
	var d, x Int
	d.GCD(&x, nil, g, n)

	// 当且仅当 d==1 时，g 和 n 互质
	if d.Cmp(intOne) != 0 {
		return nil
	}

	// x 和 y 满足 g*x + n*y = 1，因此 x 是逆元，
	// 但它可能为负，所以转换到范围 0 <= z < |n|
	if x.neg {
		z.Add(&x, n)
	} else {
		z.Set(&x)
	}
	return z
}

func (z nat) modInverse(g, n nat) nat {
	// TODO(rsc): ModInverse 应基于此函数实现。
	return (&Int{abs: z}).ModInverse(&Int{abs: g}, &Int{abs: n}).abs
}

// Jacobi 返回 Jacobi 符号 (x/y)，其值为 +1、-1 或 0。
// y 参数必须是奇整数。
func Jacobi(x, y *Int) int {
	if len(y.abs) == 0 || y.abs[0]&1 == 0 {
		panic(fmt.Sprintf("big: invalid 2nd argument to Int.Jacobi: need odd integer but got %s", y.String()))
	}

	// 我们使用 "The Yacas Book of Algorithms" 第 2 章 2.4 节中
	// 描述的表述：
	// http://yacas.sourceforge.net/Algo.book.pdf

	var a, b, c Int
	a.Set(x)
	b.Set(y)
	j := 1

	if b.neg {
		if a.neg {
			j = -1
		}
		b.neg = false
	}

	for {
		if b.Cmp(intOne) == 0 {
			return j
		}
		if len(a.abs) == 0 {
			return 0
		}
		a.Mod(&a, &b)
		if len(a.abs) == 0 {
			return 0
		}
		// a > 0

		// 处理 'a' 中的因子 2
		s := a.abs.trailingZeroBits()
		if s&1 != 0 {
			bmod8 := b.abs[0] & 7
			if bmod8 == 3 || bmod8 == 5 {
				j = -j
			}
		}
		c.Rsh(&a, s) // a = 2^s*c

		// 交换分子和分母
		if b.abs[0]&3 == 3 && c.abs[0]&3 == 3 {
			j = -j
		}
		a.Set(&b)
		b.Set(&c)
	}
}

// modSqrt3Mod4 使用恒等式
//
//	   (a^((p+1)/4))^2  mod p
//	== u^(p+1)          mod p
//	== u^2              mod p
//
// 为 3 mod 4 的素数快速计算任意二次剩余模 p 的平方根。
func (z *Int) modSqrt3Mod4Prime(x, p *Int) *Int {
	e := new(Int).Add(p, intOne) // e = p + 1
	e.Rsh(e, 2)                  // e = (p + 1) / 4
	z.Exp(x, e, p)               // z = x^e mod p
	return z
}

// modSqrt5Mod8Prime 使用 Atkin 的观察结果，即 2 不是模 p 的平方数
//
//	alpha ==  (2*a)^((p-5)/8)    mod p
//	beta  ==  2*a*alpha^2        mod p  is a square root of -1
//	b     ==  a*alpha*(beta-1)   mod p  is a square root of a
//
// 为 5 mod 8 的素数快速计算任意二次剩余模 p 的平方根。
func (z *Int) modSqrt5Mod8Prime(x, p *Int) *Int {
	// p == 5 mod 8 意味着 p = e*8 + 5
	// e 是商，5 是除以 8 后的余数
	e := new(Int).Rsh(p, 3)  // e = (p - 5) / 8
	tx := new(Int).Lsh(x, 1) // tx = 2*x
	alpha := new(Int).Exp(tx, e, p)
	beta := new(Int).Mul(alpha, alpha)
	beta.Mod(beta, p)
	beta.Mul(beta, tx)
	beta.Mod(beta, p)
	beta.Sub(beta, intOne)
	beta.Mul(beta, x)
	beta.Mod(beta, p)
	beta.Mul(beta, alpha)
	z.Mod(beta, p)
	return z
}

// modSqrtTonelliShanks 使用 Tonelli-Shanks 算法求
// 二次剩余模任意素数的平方根。
func (z *Int) modSqrtTonelliShanks(x, p *Int) *Int {
	// 将 p-1 分解为 s*2^e，使得 s 为奇数。
	var s Int
	s.Sub(p, intOne)
	e := s.abs.trailingZeroBits()
	s.Rsh(&s, e)

	// 寻找某个非平方数 n
	var n Int
	n.SetInt64(2)
	for Jacobi(&n, p) != -1 {
		n.Add(&n, intOne)
	}

	// Tonelli-Shanks 算法的核心。遵循 Ezra Brown 的
	// "Square roots from 1; 24, 51, 10 to Dan Shanks" 第 6 节
	// 中的描述：
	// https://www.maa.org/sites/default/files/pdf/upload_library/22/Polya/07468342.di020786.02p0470a.pdf
	var y, b, g, t Int
	y.Add(&s, intOne)
	y.Rsh(&y, 1)
	y.Exp(x, &y, p)  // y = x^((s+1)/2)
	b.Exp(x, &s, p)  // b = x^s
	g.Exp(&n, &s, p) // g = n^s
	r := e
	for {
		// 寻找最小的 m 使得 ord_p(b) = 2^m
		var m uint
		t.Set(&b)
		for t.Cmp(intOne) != 0 {
			t.Mul(&t, &t).Mod(&t, p)
			m++
		}

		if m == 0 {
			return z.Set(&y)
		}

		t.SetInt64(0).SetBit(&t, int(r-m-1), 1).Exp(&g, &t, p)
		// t = g^(2^(r-m-1)) mod p
		g.Mul(&t, &t).Mod(&g, p) // g = g^(2^(r-m)) mod p
		y.Mul(&y, &t).Mod(&y, p)
		b.Mul(&b, &g).Mod(&b, p)
		r = m
	}
}

// ModSqrt 如果 x mod p 的平方根存在，则将 z 设为它，
// 并返回 z。模数 p 必须是奇素数。如果 x 不是模 p 的平方数，
// ModSqrt 保持 z 不变并返回 nil。如果 p 不是奇整数，此函数会 panic；
// 如果 p 是奇数但不是素数，其行为未定义。
func (z *Int) ModSqrt(x, p *Int) *Int {
	switch Jacobi(x, p) {
	case -1:
		return nil // x 不是模 p 的平方数
	case 0:
		return z.SetInt64(0) // sqrt(0) mod p = 0
	case 1:
		break
	}
	if x.neg || x.Cmp(p) >= 0 { // 确保 0 <= x < p
		x = new(Int).Mod(x, p)
	}

	switch {
	case p.abs[0]%4 == 3:
		// 检查 p 是否为 3 mod 4，若是则使用更快的算法。
		return z.modSqrt3Mod4Prime(x, p)
	case p.abs[0]%8 == 5:
		// 检查 p 是否为 5 mod 8，使用 Atkin 的算法。
		return z.modSqrt5Mod8Prime(x, p)
	default:
		// 否则使用 Tonelli-Shanks。
		return z.modSqrtTonelliShanks(x, p)
	}
}

// Lsh 将 z 设为 x << n 并返回 z。
func (z *Int) Lsh(x *Int, n uint) *Int {
	z.abs = z.abs.lsh(x.abs, n)
	z.neg = x.neg
	return z
}

// Rsh 将 z 设为 x >> n 并返回 z。
func (z *Int) Rsh(x *Int, n uint) *Int {
	if x.neg {
		// (-x) >> s == ^(x-1) >> s == ^((x-1) >> s) == -(((x-1) >> s) + 1)
		t := z.abs.sub(x.abs, natOne) // 不会下溢，因为 |x| > 0
		t = t.rsh(t, n)
		z.abs = t.add(t, natOne)
		z.neg = true // 如果 x 为负，z 不可能为零
		return z
	}

	z.abs = z.abs.rsh(x.abs, n)
	z.neg = false
	return z
}

// Bit 返回 x 的第 i 位的值。也就是说，
// 它返回 (x>>i)&1。位索引 i 必须 >= 0。
func (x *Int) Bit(i int) uint {
	if i == 0 {
		// 常见情形的优化：x 的奇偶测试
		if len(x.abs) > 0 {
			return uint(x.abs[0] & 1) // 第 0 位对于 -x 相同
		}
		return 0
	}
	if i < 0 {
		panic("negative bit index")
	}
	if x.neg {
		t := nat(nil).sub(x.abs, natOne)
		return t.bit(uint(i)) ^ 1
	}

	return x.abs.bit(uint(i))
}

// SetBit 将 z 设为 x，并将 x 的第 i 位设为 b（0 或 1）。
// 也就是说，
//   - 如果 b 为 1，SetBit 设置 z = x | (1 << i)；
//   - 如果 b 为 0，SetBit 设置 z = x &^ (1 << i)；
//   - 如果 b 不是 0 或 1，SetBit 会 panic。
func (z *Int) SetBit(x *Int, i int, b uint) *Int {
	if i < 0 {
		panic("negative bit index")
	}
	if x.neg {
		t := z.abs.sub(x.abs, natOne)
		t = t.setBit(t, uint(i), b^1)
		z.abs = t.add(t, natOne)
		z.neg = len(z.abs) > 0
		return z
	}
	z.abs = z.abs.setBit(x.abs, uint(i), b)
	z.neg = false
	return z
}

// And 将 z 设为 x & y 并返回 z。
func (z *Int) And(x, y *Int) *Int {
	if x.neg == y.neg {
		if x.neg {
			// (-x) & (-y) == ^(x-1) & ^(y-1) == ^((x-1) | (y-1)) == -(((x-1) | (y-1)) + 1)
			x1 := nat(nil).sub(x.abs, natOne)
			y1 := nat(nil).sub(y.abs, natOne)
			z.abs = z.abs.add(z.abs.or(x1, y1), natOne)
			z.neg = true // z 不可能为零，因为 x 和 y 为负
			return z
		}

		// x & y == x & y
		z.abs = z.abs.and(x.abs, y.abs)
		z.neg = false
		return z
	}

	// x.neg != y.neg
	if x.neg {
		x, y = y, x // & 是对称的
	}

	// x & (-y) == x & ^(y-1) == x &^ (y-1)
	y1 := nat(nil).sub(y.abs, natOne)
	z.abs = z.abs.andNot(x.abs, y1)
	z.neg = false
	return z
}

// AndNot 将 z 设为 x &^ y 并返回 z。
func (z *Int) AndNot(x, y *Int) *Int {
	if x.neg == y.neg {
		if x.neg {
			// (-x) &^ (-y) == ^(x-1) &^ ^(y-1) == ^(x-1) & (y-1) == (y-1) &^ (x-1)
			x1 := nat(nil).sub(x.abs, natOne)
			y1 := nat(nil).sub(y.abs, natOne)
			z.abs = z.abs.andNot(y1, x1)
			z.neg = false
			return z
		}

		// x &^ y == x &^ y
		z.abs = z.abs.andNot(x.abs, y.abs)
		z.neg = false
		return z
	}

	if x.neg {
		// (-x) &^ y == ^(x-1) &^ y == ^(x-1) & ^y == ^((x-1) | y) == -(((x-1) | y) + 1)
		x1 := nat(nil).sub(x.abs, natOne)
		z.abs = z.abs.add(z.abs.or(x1, y.abs), natOne)
		z.neg = true // z 不可能为零，因为 x 为负且 y 为正
		return z
	}

	// x &^ (-y) == x &^ ^(y-1) == x & (y-1)
	y1 := nat(nil).sub(y.abs, natOne)
	z.abs = z.abs.and(x.abs, y1)
	z.neg = false
	return z
}

// Or 将 z 设为 x | y 并返回 z。
func (z *Int) Or(x, y *Int) *Int {
	if x.neg == y.neg {
		if x.neg {
			// (-x) | (-y) == ^(x-1) | ^(y-1) == ^((x-1) & (y-1)) == -(((x-1) & (y-1)) + 1)
			x1 := nat(nil).sub(x.abs, natOne)
			y1 := nat(nil).sub(y.abs, natOne)
			z.abs = z.abs.add(z.abs.and(x1, y1), natOne)
			z.neg = true // z 不可能为零，因为 x 和 y 为负
			return z
		}

		// x | y == x | y
		z.abs = z.abs.or(x.abs, y.abs)
		z.neg = false
		return z
	}

	// x.neg != y.neg
	if x.neg {
		x, y = y, x // | 是对称的
	}

	// x | (-y) == x | ^(y-1) == ^((y-1) &^ x) == -(^((y-1) &^ x) + 1)
	y1 := nat(nil).sub(y.abs, natOne)
	z.abs = z.abs.add(z.abs.andNot(y1, x.abs), natOne)
	z.neg = true // z 不可能为零，因为 x 或 y 之一为负
	return z
}

// Xor 将 z 设为 x ^ y 并返回 z。
func (z *Int) Xor(x, y *Int) *Int {
	if x.neg == y.neg {
		if x.neg {
			// (-x) ^ (-y) == ^(x-1) ^ ^(y-1) == (x-1) ^ (y-1)
			x1 := nat(nil).sub(x.abs, natOne)
			y1 := nat(nil).sub(y.abs, natOne)
			z.abs = z.abs.xor(x1, y1)
			z.neg = false
			return z
		}

		// x ^ y == x ^ y
		z.abs = z.abs.xor(x.abs, y.abs)
		z.neg = false
		return z
	}

	// x.neg != y.neg
	if x.neg {
		x, y = y, x // ^ 是对称的
	}

	// x ^ (-y) == x ^ ^(y-1) == ^(x ^ (y-1)) == -((x ^ (y-1)) + 1)
	y1 := nat(nil).sub(y.abs, natOne)
	z.abs = z.abs.add(z.abs.xor(x.abs, y1), natOne)
	z.neg = true // z 不可能为零，因为 x 或 y 中只有一个为负
	return z
}

// Not 将 z 设为 ^x 并返回 z。
func (z *Int) Not(x *Int) *Int {
	if x.neg {
		// ^(-x) == ^(^(x-1)) == x-1
		z.abs = z.abs.sub(x.abs, natOne)
		z.neg = false
		return z
	}

	// ^x == -x-1 == -(x+1)
	z.abs = z.abs.add(x.abs, natOne)
	z.neg = true // z 不可能为零，因为 x 为正
	return z
}

// Sqrt 将 z 设为 ⌊√x⌋，即使得 z² ≤ x 的最大整数，并返回 z。
// 如果 x 为负，它会 panic。
func (z *Int) Sqrt(x *Int) *Int {
	if x.neg {
		panic("square root of negative number")
	}
	z.neg = false
	z.abs = z.abs.sqrt(nil, x.abs)
	return z
}
