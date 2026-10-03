// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现多精度有理数。

package big

import (
	"fmt"
	"math"
)

// Rat 表示任意精度的商 a/b。
// Rat 的零值表示数值 0。
//
// 所有操作都接受指针参数 (*Rat) 而不是 Rat 值，并且每个唯一的 Rat 值
// 都需要其自身唯一的 *Rat 指针。要“复制”一个 Rat 值，必须使用
// [Rat.Set] 方法将某个已存在（或新分配）的 Rat 设置为新值；
// 不支持对 Rat 进行浅拷贝，否则可能导致错误。
type Rat struct {
	// 为了使 Rat 的零值无需初始化即可工作，
	// b 的零值（len(b) == 0）表现得就像 b == 1。在最早的
	// 时机（当对 Rat 进行赋值时），这样的未初始化分母会被设为 1。
	// a.neg 决定 Rat 的符号，b.neg 会被忽略。
	a, b Int
}

// NewRat 创建一个新的 [Rat]，其分子为 a、分母为 b。
func NewRat(a, b int64) *Rat {
	return new(Rat).SetFrac64(a, b)
}

// SetFloat64 将 z 精确设置为 f 并返回 z。
// 如果 f 不是有限值，SetFloat 返回 nil。
func (z *Rat) SetFloat64(f float64) *Rat {
	const expMask = 1<<11 - 1
	bits := math.Float64bits(f)
	mantissa := bits & (1<<52 - 1)
	exp := int((bits >> 52) & expMask)
	switch exp {
	case expMask: // 非有限
		return nil
	case 0: // 非规格化数
		exp -= 1022
	default: // 规格化数
		mantissa |= 1 << 52
		exp -= 1023
	}

	shift := 52 - exp

	// 优化（？）：部分预规格化。
	for mantissa&1 == 0 && shift > 0 {
		mantissa >>= 1
		shift--
	}

	z.a.SetUint64(mantissa)
	z.a.neg = f < 0
	z.b.Set(intOne)
	if shift > 0 {
		z.b.Lsh(&z.b, uint(shift))
	} else {
		z.a.Lsh(&z.a, uint(-shift))
	}
	return z.norm()
}

// quotToFloat32 返回最接近商 a/b 的非负 float32 值，
// 在恰好一半的情况（halfway）下使用四舍六入五成双（round-to-even）。
// 它不会修改其参数。
// 前置条件：b 非零；a 和 b 没有公因子。
func quotToFloat32(stk *stack, a, b nat) (f float32, exact bool) {
	const (
		// 浮点数的位大小
		Fsize = 32

		// 尾数
		Msize  = 23
		Msize1 = Msize + 1 // 包括隐式的 1
		Msize2 = Msize1 + 1

		// 指数
		Esize = Fsize - Msize1
		Ebias = 1<<(Esize-1) - 1
		Emin  = 1 - Ebias
		Emax  = Ebias
	)

	// TODO(adonovan)：针对常见的退化情况做特化处理：1.0、整数。
	alen := a.bitLen()
	if alen == 0 {
		return 0, true
	}
	blen := b.bitLen()
	if blen == 0 {
		panic("division by zero")
	}

	// 1. 左移 A 或 B，使得商 A/B 落在 [1<<Msize1, 1<<(Msize2+1) 内
	//（当左对齐时，若 A < B 则为 Msize2 位，若 A >= B 则为 Msize2+1 位）。
	// 这比 float32 尾数字段宽度 Msize 多出 2 或 3 位：
	// - 可选的多余位会在下面的步骤 3 中被移走。
	// - 最高位 1 在“规格化”表示中被省略；
	// - 最低位 1 会在舍入期间使用，然后被丢弃。
	exp := alen - blen
	var a2, b2 nat
	a2 = a2.set(a)
	b2 = b2.set(b)
	if shift := Msize2 - exp; shift > 0 {
		a2 = a2.lsh(a2, uint(shift))
	} else if shift < 0 {
		b2 = b2.lsh(b2, uint(-shift))
	}

	// 2. 计算商和余数 (q, r)。注意：由于额外的移位，
	// q 的低位在逻辑上是 r 的高位。
	var q nat
	q, r := q.div(stk, a2, a2, b2) // (回收 a2)
	mantissa := low32(q)
	haveRem := len(r) > 0 // mantissa&1 && !haveRem => remainder is exactly half

	// 3. 如果商不能放进 Msize2 位，则用 b2<<1 重做除法
	//（实际上是增量式完成的）。
	if mantissa>>Msize2 == 1 {
		if mantissa&1 == 1 {
			haveRem = true
		}
		mantissa >>= 1
		exp++
	}
	if mantissa>>Msize1 != 1 {
		panic(fmt.Sprintf("expected exactly %d bits of result", Msize2))
	}

	// 4. 舍入。
	if Emin-Msize <= exp && exp <= Emin {
		// 非规格化数情况；损失 'shift' 位的精度。
		shift := uint(Emin - (exp - 1)) // [1..Esize1)
		lostbits := mantissa & (1<<shift - 1)
		haveRem = haveRem || lostbits != 0
		mantissa >>= shift
		exp = 2 - Ebias // == exp + shift
	}
	// 使用 round-half-to-even 对 q 进行舍入。
	exact = !haveRem
	if mantissa&1 != 0 {
		exact = false
		if haveRem || mantissa&2 != 0 {
			if mantissa++; mantissa >= 1<<Msize2 {
				// 完全进位 11...1 => 100...0，因此移位是安全的
				mantissa >>= 1
				exp++
			}
		}
	}
	mantissa >>= 1 // 丢弃舍入位。尾数现在按 1<<Msize1 缩放了。

	f = float32(math.Ldexp(float64(mantissa), exp-Msize1))
	if math.IsInf(float64(f), 0) {
		exact = false
	}
	return
}

// quotToFloat64 返回最接近商 a/b 的非负 float64 值，
// 在恰好一半的情况（halfway）下使用四舍六入五成双（round-to-even）。
// 它不会修改其参数。
// 前置条件：b 非零；a 和 b 没有公因子。
func quotToFloat64(stk *stack, a, b nat) (f float64, exact bool) {
	const (
		// 浮点数的位大小
		Fsize = 64

		// 尾数
		Msize  = 52
		Msize1 = Msize + 1 // 包括隐式的 1
		Msize2 = Msize1 + 1

		// 指数
		Esize = Fsize - Msize1
		Ebias = 1<<(Esize-1) - 1
		Emin  = 1 - Ebias
		Emax  = Ebias
	)

	// TODO(adonovan)：针对常见的退化情况做特化处理：1.0、整数。
	alen := a.bitLen()
	if alen == 0 {
		return 0, true
	}
	blen := b.bitLen()
	if blen == 0 {
		panic("division by zero")
	}

	// 1. 左移 A 或 B，使得商 A/B 落在 [1<<Msize1, 1<<(Msize2+1) 内
	//（当左对齐时，若 A < B 则为 Msize2 位，若 A >= B 则为 Msize2+1 位）。
	// 这比 float64 尾数字段宽度 Msize 多出 2 或 3 位：
	// - 可选的多余位会在下面的步骤 3 中被移走。
	// - 最高位 1 在“规格化”表示中被省略；
	// - 最低位 1 会在舍入期间使用，然后被丢弃。
	exp := alen - blen
	var a2, b2 nat
	a2 = a2.set(a)
	b2 = b2.set(b)
	if shift := Msize2 - exp; shift > 0 {
		a2 = a2.lsh(a2, uint(shift))
	} else if shift < 0 {
		b2 = b2.lsh(b2, uint(-shift))
	}

	// 2. 计算商和余数 (q, r)。注意：由于额外的移位，
	// q 的低位在逻辑上是 r 的高位。
	var q nat
	q, r := q.div(stk, a2, a2, b2) // (回收 a2)
	mantissa := low64(q)
	haveRem := len(r) > 0 // mantissa&1 && !haveRem => remainder is exactly half

	// 3. 如果商不能放进 Msize2 位，则用 b2<<1 重做除法
	//（实际上是增量式完成的）。
	if mantissa>>Msize2 == 1 {
		if mantissa&1 == 1 {
			haveRem = true
		}
		mantissa >>= 1
		exp++
	}
	if mantissa>>Msize1 != 1 {
		panic(fmt.Sprintf("expected exactly %d bits of result", Msize2))
	}

	// 4. 舍入。
	if Emin-Msize <= exp && exp <= Emin {
		// 非规格化数情况；损失 'shift' 位的精度。
		shift := uint(Emin - (exp - 1)) // [1..Esize1)
		lostbits := mantissa & (1<<shift - 1)
		haveRem = haveRem || lostbits != 0
		mantissa >>= shift
		exp = 2 - Ebias // == exp + shift
	}
	// 使用 round-half-to-even 对 q 进行舍入。
	exact = !haveRem
	if mantissa&1 != 0 {
		exact = false
		if haveRem || mantissa&2 != 0 {
			if mantissa++; mantissa >= 1<<Msize2 {
				// 完全进位 11...1 => 100...0，因此移位是安全的
				mantissa >>= 1
				exp++
			}
		}
	}
	mantissa >>= 1 // 丢弃舍入位。尾数现在按 1<<Msize1 缩放了。

	f = math.Ldexp(float64(mantissa), exp-Msize1)
	if math.IsInf(f, 0) {
		exact = false
	}
	return
}

// Float32 返回 x 最接近的 float32 值，以及一个布尔值，表示 f 是否
// 精确地表示 x。如果 x 的绝对值太大而无法用 float32 表示，则 f 为
// 无穷，exact 为 false。即使 f == 0，f 的符号也始终与 x 的符号一致。
func (x *Rat) Float32() (f float32, exact bool) {
	b := x.b.abs
	if len(b) == 0 {
		b = natOne
	}
	stk := getStack()
	defer stk.free()
	f, exact = quotToFloat32(stk, x.a.abs, b)
	if x.a.neg {
		f = -f
	}
	return
}

// Float64 返回 x 最接近的 float64 值，以及一个布尔值，表示 f 是否
// 精确地表示 x。如果 x 的绝对值太大而无法用 float64 表示，则 f 为
// 无穷，exact 为 false。即使 f == 0，f 的符号也始终与 x 的符号一致。
func (x *Rat) Float64() (f float64, exact bool) {
	b := x.b.abs
	if len(b) == 0 {
		b = natOne
	}
	stk := getStack()
	defer stk.free()
	f, exact = quotToFloat64(stk, x.a.abs, b)
	if x.a.neg {
		f = -f
	}
	return
}

// SetFrac 将 z 设置为 a/b 并返回 z。
// 如果 b == 0，SetFrac 会 panic。
func (z *Rat) SetFrac(a, b *Int) *Rat {
	z.a.neg = a.neg != b.neg
	babs := b.abs
	if len(babs) == 0 {
		panic("division by zero")
	}
	if &z.a == b || alias(z.a.abs, babs) {
		babs = nat(nil).set(babs) // make a copy
	}
	z.a.abs = z.a.abs.set(a.abs)
	z.b.abs = z.b.abs.set(babs)
	return z.norm()
}

// SetFrac64 将 z 设置为 a/b 并返回 z。
// 如果 b == 0，SetFrac64 会 panic。
func (z *Rat) SetFrac64(a, b int64) *Rat {
	if b == 0 {
		panic("division by zero")
	}
	z.a.SetInt64(a)
	if b < 0 {
		b = -b
		z.a.neg = !z.a.neg
	}
	z.b.abs = z.b.abs.setUint64(uint64(b))
	return z.norm()
}

// SetInt 将 z 设置为 x（通过复制 x）并返回 z。
func (z *Rat) SetInt(x *Int) *Rat {
	z.a.Set(x)
	z.b.abs = z.b.abs.setWord(1)
	return z
}

// SetInt64 将 z 设置为 x 并返回 z。
func (z *Rat) SetInt64(x int64) *Rat {
	z.a.SetInt64(x)
	z.b.abs = z.b.abs.setWord(1)
	return z
}

// SetUint64 将 z 设置为 x 并返回 z。
func (z *Rat) SetUint64(x uint64) *Rat {
	z.a.SetUint64(x)
	z.b.abs = z.b.abs.setWord(1)
	return z
}

// Set 将 z 设置为 x（通过复制 x）并返回 z。
func (z *Rat) Set(x *Rat) *Rat {
	if z != x {
		z.a.Set(&x.a)
		z.b.Set(&x.b)
	}
	if len(z.b.abs) == 0 {
		z.b.abs = z.b.abs.setWord(1)
	}
	return z
}

// Abs 将 z 设置为 |x|（x 的绝对值）并返回 z。
func (z *Rat) Abs(x *Rat) *Rat {
	z.Set(x)
	z.a.neg = false
	return z
}

// Neg 将 z 设置为 -x 并返回 z。
func (z *Rat) Neg(x *Rat) *Rat {
	z.Set(x)
	z.a.neg = len(z.a.abs) > 0 && !z.a.neg // 0 没有符号
	return z
}

// Inv 将 z 设置为 1/x 并返回 z。
// 如果 x == 0，Inv 会 panic。
func (z *Rat) Inv(x *Rat) *Rat {
	if len(x.a.abs) == 0 {
		panic("division by zero")
	}
	z.Set(x)
	z.a.abs, z.b.abs = z.b.abs, z.a.abs
	return z
}

// Sign 返回：
//   - 如果 x < 0 则为 -1；
//   - 如果 x == 0 则为 0；
//   - 如果 x > 0 则为 +1。
func (x *Rat) Sign() int {
	return x.a.Sign()
}

// IsInt 报告 x 的分母是否为 1。
func (x *Rat) IsInt() bool {
	return len(x.b.abs) == 0 || x.b.abs.cmp(natOne) == 0
}

// Num 返回 x 的分子；它可能 <= 0。
// 结果是对 x 分子的引用；如果给 x 赋了新值，它可能会改变，
// 反之亦然。分子的符号与 x 的符号一致。
func (x *Rat) Num() *Int {
	return &x.a
}

// Denom 返回 x 的分母；它总是 > 0。
// 结果是对 x 分母的引用，除非 x 是未初始化（零值）的 [Rat]，
// 此时结果是一个值为 1 的新 [Int]。（要初始化 x，任何会设置 x 的
// 操作都可以，包括 x.Set(x)。）如果结果是对 x 分母的引用，
// 那么当给 x 赋了新值时它可能会改变，反之亦然。
func (x *Rat) Denom() *Int {
	// 注意 x.b.neg 保证为 false。
	if len(x.b.abs) == 0 {
		// 注意：如果这被证明有问题，我们可以改为 panic
		//       并要求显式初始化 Rat。
		return &Int{abs: nat{1}}
	}
	return &x.b
}

func (z *Rat) norm() *Rat {
	switch {
	case len(z.a.abs) == 0:
		// z == 0；规范化符号和分母
		z.a.neg = false
		fallthrough
	case len(z.b.abs) == 0:
		// z 是整数；规范化分母
		z.b.abs = z.b.abs.setWord(1)
	default:
		// z 是分数；规范化分子和分母
		stk := getStack()
		defer stk.free()
		neg := z.a.neg
		z.a.neg = false
		z.b.neg = false
		if f := NewInt(0).lehmerGCD(nil, nil, &z.a, &z.b); f.Cmp(intOne) != 0 {
			z.a.abs, _ = z.a.abs.div(stk, nil, z.a.abs, f.abs)
			z.b.abs, _ = z.b.abs.div(stk, nil, z.b.abs, f.abs)
		}
		z.a.neg = neg
	}
	return z
}

// mulDenom 将 z 设置为分母乘积 x*y（考虑到 x 或 y 的 0 值
// 必须被解释为 1）并返回 z。
func mulDenom(stk *stack, z, x, y nat) nat {
	switch {
	case len(x) == 0 && len(y) == 0:
		return z.setWord(1)
	case len(x) == 0:
		return z.set(y)
	case len(y) == 0:
		return z.set(x)
	}
	return z.mul(stk, x, y)
}

// scaleDenom 将 z 设置为乘积 x*f。
// 如果 f == 0（分母的零值），则 z 被设置为（一个 x 的副本）。
func (z *Int) scaleDenom(stk *stack, x *Int, f nat) {
	if len(f) == 0 {
		z.Set(x)
		return
	}
	z.abs = z.abs.mul(stk, x.abs, f)
	z.neg = x.neg
}

// Cmp 比较 x 和 y 并返回：
//   - 如果 x < y 则为 -1；
//   - 如果 x == y 则为 0；
//   - 如果 x > y 则为 +1。
func (x *Rat) Cmp(y *Rat) int {
	var a, b Int
	stk := getStack()
	defer stk.free()
	a.scaleDenom(stk, &x.a, y.b.abs)
	b.scaleDenom(stk, &y.a, x.b.abs)
	return a.Cmp(&b)
}

// Add 将 z 设置为和 x+y 并返回 z。
func (z *Rat) Add(x, y *Rat) *Rat {
	stk := getStack()
	defer stk.free()

	var a1, a2 Int
	a1.scaleDenom(stk, &x.a, y.b.abs)
	a2.scaleDenom(stk, &y.a, x.b.abs)
	z.a.Add(&a1, &a2)
	z.b.abs = mulDenom(stk, z.b.abs, x.b.abs, y.b.abs)
	return z.norm()
}

// Sub 将 z 设置为差 x-y 并返回 z。
func (z *Rat) Sub(x, y *Rat) *Rat {
	stk := getStack()
	defer stk.free()

	var a1, a2 Int
	a1.scaleDenom(stk, &x.a, y.b.abs)
	a2.scaleDenom(stk, &y.a, x.b.abs)
	z.a.Sub(&a1, &a2)
	z.b.abs = mulDenom(stk, z.b.abs, x.b.abs, y.b.abs)
	return z.norm()
}

// Mul 将 z 设置为乘积 x*y 并返回 z。
func (z *Rat) Mul(x, y *Rat) *Rat {
	stk := getStack()
	defer stk.free()

	if x == y {
		// 平方后的 Rat 为正且不可约分（无需调用 norm()）
		z.a.neg = false
		z.a.abs = z.a.abs.sqr(stk, x.a.abs)
		if len(x.b.abs) == 0 {
			z.b.abs = z.b.abs.setWord(1)
		} else {
			z.b.abs = z.b.abs.sqr(stk, x.b.abs)
		}
		return z
	}

	z.a.mul(stk, &x.a, &y.a)
	z.b.abs = mulDenom(stk, z.b.abs, x.b.abs, y.b.abs)
	return z.norm()
}

// Quo 将 z 设置为商 x/y 并返回 z。
// 如果 y == 0，Quo 会 panic。
func (z *Rat) Quo(x, y *Rat) *Rat {
	stk := getStack()
	defer stk.free()

	if len(y.a.abs) == 0 {
		panic("division by zero")
	}
	var a, b Int
	a.scaleDenom(stk, &x.a, y.b.abs)
	b.scaleDenom(stk, &y.a, x.b.abs)
	z.a.abs = a.abs
	z.b.abs = b.abs
	z.a.neg = a.neg != b.neg
	return z.norm()
}
