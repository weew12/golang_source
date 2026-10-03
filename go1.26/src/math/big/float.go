// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现多精度浮点数。
// 与 GNU MPFR 库 (https://www.mpfr.org/) 类似，操作数
// 可以具有混合精度。与 MPFR 不同的是，舍入模式不是
// 随每个操作指定，而是随每个操作数指定。结果操作数的
// 舍入模式决定了一个操作的舍入模式。这是一个从零开始
// 的实现。

package big

import (
	"fmt"
	"math"
	"math/bits"
)

const debugFloat = false // 启用用于调试

// 非零有限 Float 表示一个多精度浮点数
//
//	sign × mantissa × 2**exponent
//
// 满足 0.5 <= mantissa < 1.0，且 MinExp <= exponent <= MaxExp。
// Float 也可以为零 (+0, -0) 或无穷 (+Inf, -Inf)。
// 所有 Float 都是有序的，两个 Float x 和 y 的顺序
// 由 x.Cmp(y) 定义。
//
// 每个 Float 值还带有一个精度、舍入模式和准确性。
// 精度是可用于表示该值的尾数位的最大数量。
// 舍入模式规定了结果应如何舍入以适配尾数位，
// 准确性则描述相对于精确结果的舍入误差。
//
// 除非另有说明，所有为结果指定了 *Float 变量（通常通过
// 接收者，[Float.MantExp] 除外）的操作（包括 setter），
// 都会根据结果变量的精度和舍入模式对数值结果进行舍入。
//
// 如果提供的结果精度为 0（见下文），则在进行任何舍入
// 之前，它会被设为精度值最大的那个参数的精度，且舍入
// 模式保持不变。因此，作为结果参数提供的未初始化 Float
// 会将其精度设为由操作数决定的合理值，其模式为
// RoundingMode 的零值 (ToNearestEven)。
//
// 通过将所需精度设为 24 或 53 并使用匹配的舍入模式
// （通常是 [ToNearestEven]），对于对应普通（即非非规格化）
// float32 或 float64 数的操作数，Float 运算会产生与相应的
// float32 或 float64 IEEE 754 算术相同的结果。
// 指数下溢和上溢会导致与 IEEE 754 不同的值（0 或无穷），
// 因为 Float 的指数范围要大得多。
//
// Float 的零（未初始化）值可直接使用，它精确表示数 +0.0，
// 精度为 0 且舍入模式为 [ToNearestEven]。
//
// 操作始终接受指针参数 (*Float) 而非 Float 值，每个
// 唯一的 Float 值都需要其自己唯一的 *Float 指针。
// 要“复制”一个 Float 值，必须使用 [Float.Set] 方法将现有
// 的（或新分配的）Float 设为新值；不支持 Float 的浅拷贝，
// 且浅拷贝可能导致错误。
type Float struct {
	prec uint32
	mode RoundingMode
	acc  Accuracy
	form form
	neg  bool
	mant nat
	exp  int32
}

// [Float] 操作若在 IEEE 754 规则下会产生 NaN，则会引发
// ErrNaN panic。ErrNaN 实现了 error 接口。
type ErrNaN struct {
	msg string
}

func (err ErrNaN) Error() string {
	return err.msg
}

// NewFloat 分配并返回一个设为 x 的新 [Float]，
// 其精度为 53，舍入模式为 [ToNearestEven]。
// 若 x 为 NaN，NewFloat 会 panic 并给出 [ErrNaN]。
func NewFloat(x float64) *Float {
	if math.IsNaN(x) {
		panic(ErrNaN{"NewFloat(NaN)"})
	}
	return new(Float).SetFloat64(x)
}

// 指数和精度限制。
const (
	MaxExp  = math.MaxInt32  // 支持的最大指数
	MinExp  = math.MinInt32  // 支持的最小指数
	MaxPrec = math.MaxUint32 // （理论上）支持的最大精度；可能受内存限制
)

// 内部表示：非零有限 Float x 的尾数位 x.mant 存储在一个
// nat 切片中，其长度足以容纳最多 x.prec 位；
// 若尾数包含末尾 0 位，该切片可以（但不一定）更短。
// 若 x.mant 的最高有效位 (msb) == 1（即 msb 一直“向左”移），
// 则 x.mant 是规范化的。因此，若尾数带有末尾 0 位，
// 或 x.prec 不是字长 _W 的倍数，
// 则 x.mant[0] 带有末尾零位。尾数的 msb 对应
// 值 0.5；指数 x.exp 按需平移二进制小数点。
//
// 零或非有限的 Float x 会忽略 x.mant 和 x.exp。
//
// x                 form      neg      mant         exp
// ----------------------------------------------------------
// ±0                zero      sign     -            -
// 0 < |x| < +Inf    finite    sign     mantissa     exponent
// ±Inf              inf       sign     -            -

// form 值描述内部表示。
type form byte

// form 值的顺序很重要——请勿更改！
const (
	zero form = iota
	finite
	inf
)

// RoundingMode 决定 [Float] 值如何舍入到所需精度。
// 舍入可能会改变 [Float] 值；舍入误差由
// [Float] 的 [Accuracy] 描述。
type RoundingMode byte

// 这些常量定义支持的舍入模式。
const (
	ToNearestEven RoundingMode = iota // == IEEE 754-2008 roundTiesToEven
	ToNearestAway                     // == IEEE 754-2008 roundTiesToAway
	ToZero                            // == IEEE 754-2008 roundTowardZero
	AwayFromZero                      // 无 IEEE 754-2008 等价项
	ToNegativeInf                     // == IEEE 754-2008 roundTowardNegative
	ToPositiveInf                     // == IEEE 754-2008 roundTowardPositive
)

//go:generate stringer -type=RoundingMode

// Accuracy 描述生成 [Float] 值的最近一次操作所产生的、
// 相对于精确值的舍入误差。
type Accuracy int8

// 描述 [Float] 的 [Accuracy] 的常量。
const (
	Below Accuracy = -1
	Exact Accuracy = 0
	Above Accuracy = +1
)

//go:generate stringer -type=Accuracy

// SetPrec 将 z 的精度设为 prec，并返回 z 的（可能已舍入的）值。
// 若尾数无法在不损失精度的情况下用 prec 位表示，
// 则根据 z 的舍入模式进行舍入。
// SetPrec(0) 将所有有限值映射为 ±0；无穷值保持不变。
// 若 prec > [MaxPrec]，则将其设为 [MaxPrec]。
func (z *Float) SetPrec(prec uint) *Float {
	z.acc = Exact // 乐观地假设无需舍入

	// 特殊情况
	if prec == 0 {
		z.prec = 0
		if z.form == finite {
			// 将 z 截断为 0
			z.acc = makeAcc(z.neg)
			z.form = zero
		}
		return z
	}

	// 一般情况
	if prec > MaxPrec {
		prec = MaxPrec
	}
	old := z.prec
	z.prec = uint32(prec)
	if z.prec < old {
		z.round(0)
	}
	return z
}

func makeAcc(above bool) Accuracy {
	if above {
		return Above
	}
	return Below
}

// SetMode 将 z 的舍入模式设为 mode，并返回精确的 z。
// 除此之外 z 保持不变。
// z.SetMode(z.Mode()) 是将 z 的准确性设为 [Exact] 的廉价方式。
func (z *Float) SetMode(mode RoundingMode) *Float {
	z.mode = mode
	z.acc = Exact
	return z
}

// Prec 返回 x 的尾数精度（以位为单位）。
// 对于 |x| == 0 和 |x| == Inf，结果可能为 0。
func (x *Float) Prec() uint {
	return uint(x.prec)
}

// MinPrec 返回精确表示 x 所需的最小精度
// （即 x.SetPrec(prec) 开始对 x 舍入之前的最小 prec）。
// 对于 |x| == 0 和 |x| == Inf，结果为 0。
func (x *Float) MinPrec() uint {
	if x.form != finite {
		return 0
	}
	return uint(len(x.mant))*_W - x.mant.trailingZeroBits()
}

// Mode 返回 x 的舍入模式。
func (x *Float) Mode() RoundingMode {
	return x.mode
}

// Acc 返回最近一次操作所产生的 x 的准确性，
// 除非该操作另有明确说明。
func (x *Float) Acc() Accuracy {
	return x.acc
}

// Sign 返回：
//   - 若 x < 0，返回 -1；
//   - 若 x 为 ±0，返回 0；
//   - 若 x > 0，返回 +1。
func (x *Float) Sign() int {
	if debugFloat {
		x.validate()
	}
	if x.form == zero {
		return 0
	}
	if x.neg {
		return -1
	}
	return 1
}

// MantExp 将 x 分解为尾数和指数两部分，并返回指数。
// 若提供了非 nil 的 mant 参数，则将其值设为 x 的尾数，
// 精度和舍入模式与 x 相同。这些部分满足
// x == mant × 2**exp，且 0.5 <= |mant| < 1.0。
// 使用 nil 参数调用 MantExp 是获取接收者指数的
// 高效方式。
//
// 特殊情况有：
//
//	(  ±0).MantExp(mant) = 0, with mant set to   ±0
//	(±Inf).MantExp(mant) = 0, with mant set to ±Inf
//
// x 和 mant 可以相同，此时 x 被设为其尾数值。
func (x *Float) MantExp(mant *Float) (exp int) {
	if debugFloat {
		x.validate()
	}
	if x.form == finite {
		exp = int(x.exp)
	}
	if mant != nil {
		mant.Copy(x)
		if mant.form == finite {
			mant.exp = 0
		}
	}
	return
}

func (z *Float) setExpAndRound(exp int64, sbit uint) {
	if exp < MinExp {
		// 下溢
		z.acc = makeAcc(z.neg)
		z.form = zero
		return
	}

	if exp > MaxExp {
		// 上溢
		z.acc = makeAcc(!z.neg)
		z.form = inf
		return
	}

	z.form = finite
	z.exp = int32(exp)
	z.round(sbit)
}

// SetMantExp 将 z 设为 mant × 2**exp 并返回 z。
// 结果 z 的精度和舍入模式与 mant 相同。
// SetMantExp 是 [Float.MantExp] 的逆操作，但不要求
// 0.5 <= |mant| < 1.0。具体而言，对于给定的 *[Float] 类型
// 的 x，SetMantExp 与 [Float.MantExp] 的关系如下：
//
//	mant := new(Float)
//	new(Float).SetMantExp(mant, x.MantExp(mant)).Cmp(x) == 0
//
// 特殊情况有：
//
//	z.SetMantExp(  ±0, exp) =   ±0
//	z.SetMantExp(±Inf, exp) = ±Inf
//
// z 和 mant 可以相同，此时 z 的指数被设为 exp。
func (z *Float) SetMantExp(mant *Float, exp int) *Float {
	if debugFloat {
		z.validate()
		mant.validate()
	}
	z.Copy(mant)

	if z.form == finite {
		// 0 < |mant| < +Inf
		z.setExpAndRound(int64(z.exp)+int64(exp), 0)
	}
	return z
}

// Signbit 报告 x 是否为负数或负零。
func (x *Float) Signbit() bool {
	return x.neg
}

// IsInf 报告 x 是否为 +Inf 或 -Inf。
func (x *Float) IsInf() bool {
	return x.form == inf
}

// IsInt 报告 x 是否为整数。
// ±Inf 值不是整数。
func (x *Float) IsInt() bool {
	if debugFloat {
		x.validate()
	}
	// 特殊情况
	if x.form != finite {
		return x.form == zero
	}
	// x.form == finite
	if x.exp <= 0 {
		return false
	}
	// x.exp > 0
	return x.prec <= uint32(x.exp) || x.MinPrec() <= uint(x.exp) // 用于小数尾数的位数不足
}

// 调试支持
func (x *Float) validate() {
	if !debugFloat {
		// 避免性能问题
		panic("validate called but debugFloat is not set")
	}
	if msg := x.validate0(); msg != "" {
		panic(msg)
	}
}

func (x *Float) validate0() string {
	if x.form != finite {
		return ""
	}
	m := len(x.mant)
	if m == 0 {
		return "nonzero finite number with empty mantissa"
	}
	const msb = 1 << (_W - 1)
	if x.mant[m-1]&msb == 0 {
		return fmt.Sprintf("msb not set in last word %#x of %s", x.mant[m-1], x.Text('p', 0))
	}
	if x.prec == 0 {
		return "zero precision finite number"
	}
	return ""
}

// round 根据 z.mode 将 z 舍入到 z.prec 位，并相应地设置 z.acc。
// sbit 必须为 0 或 1，它汇总了调用 round 之前可能已有的任何
// “粘滞位”信息。z 的尾数必须是规范化的（msb 已设置）或为空的。
//
// 注意：舍入模式 [ToNegativeInf]、[ToPositiveInf] 受 z 的符号影响。
// 为实现正确的舍入，必须在调用 round 之前正确设置 z 的符号。
func (z *Float) round(sbit uint) {
	if debugFloat {
		z.validate()
	}

	z.acc = Exact
	if z.form != finite {
		// ±0 或 ±Inf => 无事可做
		return
	}
	// z.form == finite && len(z.mant) > 0
	// m > 0 意味着 z.prec > 0（由 validate 检查）

	m := uint32(len(z.mant)) // 当前尾数长度（以字为单位）
	bits := m * _W           // 当前尾数位数；bits > 0
	if bits <= z.prec {
		// 尾数可容纳 => 无事可做
		return
	}
	// bits > z.prec

	// 舍入基于两个位：舍入位 (rbit) 和粘滞位 (sbit)。
	// rbit 是紧挨在 z.prec 个前导尾数位之前的位（即 “0.5”）。
	// 若 rbit 之前的任何位被置位，则 sbit 被置位
	// （即 “0.25”、“0.125” 等）：
	//
	//   rbit  sbit  => "小数部分"
	//
	//   0     0        == 0
	//   0     1        >  0  , < 0.5
	//   1     0        == 0.5
	//   1     1        >  0.5, < 1.0

	// bits > z.prec：尾数过大 => 舍入
	r := uint(bits - z.prec - 1) // 舍入位位置；r >= 0
	rbit := z.mant.bit(r) & 1    // 舍入位；稳妥起见确保它是单个位
	// 只有在舍入模式为 ToNearestEven 或舍入位为零时才需要粘滞位。
	// 其他情况下避免计算它。
	if sbit == 0 && (rbit == 0 || z.mode == ToNearestEven) {
		sbit = z.mant.sticky(r)
	}
	sbit &= 1 // 稳妥起见确保它是单个位

	// 截去多余的字
	n := (z.prec + (_W - 1)) / _W // 所需精度的尾数长度（以字为单位）
	if m > n {
		copy(z.mant, z.mant[m-n:]) // 将最后 n 个字移到前面
		z.mant = z.mant[:n]
	}

	// 确定末尾零位的数量 (ntz) 并计算尾数最低有效字的 lsb 掩码
	ntz := n*_W - z.prec // 0 <= ntz < _W
	lsb := Word(1) << ntz

	// 若结果不精确则舍入
	if rbit|sbit != 0 {
		// 做出舍入决定：默认情况下结果尾数被截断（“向下舍入”）。
		// 决定是否需要将（无符号）尾数加一，即“向上舍入”。
		inc := false
		switch z.mode {
		case ToNegativeInf:
			inc = z.neg
		case ToZero:
			// 无事可做
		case ToNearestEven:
			inc = rbit != 0 && (sbit != 0 || z.mant[0]&lsb != 0)
		case ToNearestAway:
			inc = rbit != 0
		case AwayFromZero:
			inc = true
		case ToPositiveInf:
			inc = !z.neg
		default:
			panic("unreachable")
		}

		// 若我们加一，则正结果（!z.neg）高于精确结果；若我们截断，
		// 则低于精确结果（精确结果无需舍入）。
		// 对于负结果（z.neg）则恰好相反。
		z.acc = makeAcc(inc != z.neg)

		if inc {
			// 尾数加 1
			if addVW(z.mant, z.mant, lsb) != 0 {
				// 尾数溢出 => 调整指数
				if z.exp >= MaxExp {
					// 指数溢出
					z.form = inf
					return
				}
				z.exp++
				// 调整尾数：除以 2 以补偿指数调整
				rshVU(z.mant, z.mant, 1)
				// 根据上面的尾数溢出设置 msb == carry == 1
				const msb = 1 << (_W - 1)
				z.mant[n-1] |= msb
			}
		}
	}

	// 将最低有效字中的末尾位清零
	z.mant[0] &^= lsb - 1

	if debugFloat {
		z.validate()
	}
}

func (z *Float) setBits64(neg bool, x uint64) *Float {
	if z.prec == 0 {
		z.prec = 64
	}
	z.acc = Exact
	z.neg = neg
	if x == 0 {
		z.form = zero
		return z
	}
	// x != 0
	z.form = finite
	s := bits.LeadingZeros64(x)
	z.mant = z.mant.setUint64(x << uint(s))
	z.exp = int32(64 - s) // 总能容纳
	if z.prec < 64 {
		z.round(0)
	}
	return z
}

// SetUint64 将 z 设为 x 的（可能已舍入的）值并返回 z。
// 若 z 的精度为 0，则将其改为 64（此时舍入没有任何
// 效果）。
func (z *Float) SetUint64(x uint64) *Float {
	return z.setBits64(false, x)
}

// SetInt64 将 z 设为 x 的（可能已舍入的）值并返回 z。
// 若 z 的精度为 0，则将其改为 64（此时舍入没有任何
// 效果）。
func (z *Float) SetInt64(x int64) *Float {
	u := x
	if u < 0 {
		u = -u
	}
	// 我们不能简单地调用 z.SetUint64(uint64(u)) 然后更改
	// 符号，因为符号会影响舍入。
	return z.setBits64(x < 0, uint64(u))
}

// SetFloat64 将 z 设为 x 的（可能已舍入的）值并返回 z。
// 若 z 的精度为 0，则将其改为 53（此时舍入没有任何效果）。
// 若 x 为 NaN，SetFloat64 会 panic 并给出 [ErrNaN]。
func (z *Float) SetFloat64(x float64) *Float {
	if z.prec == 0 {
		z.prec = 53
	}
	if math.IsNaN(x) {
		panic(ErrNaN{"Float.SetFloat64(NaN)"})
	}
	z.acc = Exact
	z.neg = math.Signbit(x) // 正确处理 -0、-Inf
	if x == 0 {
		z.form = zero
		return z
	}
	if math.IsInf(x, 0) {
		z.form = inf
		return z
	}
	// 规范化后的 x != 0
	z.form = finite
	fmant, exp := math.Frexp(x) // 获取规范化后的尾数
	z.mant = z.mant.setUint64(1<<63 | math.Float64bits(fmant)<<11)
	z.exp = int32(exp) // 总能容纳
	if z.prec < 53 {
		z.round(0)
	}
	return z
}

// fnorm 通过将尾数 m 向左移位使其规范化，
// 使最高有效字 (msw) 的 msb 为 1。
// 它返回移位量。它假定 len(m) != 0。
func fnorm(m nat) int64 {
	if debugFloat && (len(m) == 0 || m[len(m)-1] == 0) {
		panic("msw of mantissa is 0")
	}
	s := nlz(m[len(m)-1])
	if s > 0 {
		c := lshVU(m, m, s)
		if debugFloat && c != 0 {
			panic("nlz or lshVU incorrect")
		}
	}
	return int64(s)
}

// SetInt 将 z 设为 x 的（可能已舍入的）值并返回 z。
// 若 z 的精度为 0，则将其改为 x.BitLen() 与 64 中的
// 较大者（此时舍入没有任何效果）。
func (z *Float) SetInt(x *Int) *Float {
	// TODO(gri) 若 z.prec > 0 但相对于 x 的大小较小，
	// 或者存在许多末尾 0，则可以更高效。
	bits := uint32(x.BitLen())
	if z.prec == 0 {
		z.prec = max(bits, 64)
	}
	z.acc = Exact
	z.neg = x.neg
	if len(x.abs) == 0 {
		z.form = zero
		return z
	}
	// x != 0
	z.mant = z.mant.set(x.abs)
	fnorm(z.mant)
	z.setExpAndRound(int64(bits), 0)
	return z
}

// SetRat 将 z 设为 x 的（可能已舍入的）值并返回 z。
// 若 z 的精度为 0，则将其改为 a.BitLen()、b.BitLen()
// 与 64 中的最大者；其中 x = a/b。
func (z *Float) SetRat(x *Rat) *Float {
	if x.IsInt() {
		return z.SetInt(x.Num())
	}
	var a, b Float
	a.SetInt(x.Num())
	b.SetInt(x.Denom())
	if z.prec == 0 {
		z.prec = max(a.prec, b.prec)
	}
	return z.Quo(&a, &b)
}

// 若 signbit 被置位，SetInf 将 z 设为无穷 Float -Inf，
// 否则设为 +Inf，并返回 z。z 的精度保持不变，
// 且结果始终为 [Exact]。
func (z *Float) SetInf(signbit bool) *Float {
	z.acc = Exact
	z.form = inf
	z.neg = signbit
	return z
}

// Set 将 z 设为 x 的（可能已舍入的）值并返回 z。
// 若 z 的精度为 0，则在设置 z 之前将其改为 x 的精度
// （此时舍入没有任何效果）。
// 舍入根据 z 的精度和舍入模式执行；z 的准确性报告
// 结果相对于精确（未舍入）结果的误差。
func (z *Float) Set(x *Float) *Float {
	if debugFloat {
		x.validate()
	}
	z.acc = Exact
	if z != x {
		z.form = x.form
		z.neg = x.neg
		if x.form == finite {
			z.exp = x.exp
			z.mant = z.mant.set(x.mant)
		}
		if z.prec == 0 {
			z.prec = x.prec
		} else if z.prec < x.prec {
			z.round(0)
		}
	}
	return z
}

// Copy 将 z 设为 x，其精度、舍入模式和准确性与 x 相同。
// Copy 返回 z。若 x 和 z 相同，Copy 是空操作。
func (z *Float) Copy(x *Float) *Float {
	if debugFloat {
		x.validate()
	}
	if z != x {
		z.prec = x.prec
		z.mode = x.mode
		z.acc = x.acc
		z.form = x.form
		z.neg = x.neg
		if z.form == finite {
			z.mant = z.mant.set(x.mant)
			z.exp = x.exp
		}
	}
	return z
}

// msb32 返回 x 的 32 个最高有效位。
func msb32(x nat) uint32 {
	i := len(x) - 1
	if i < 0 {
		return 0
	}
	if debugFloat && x[i]&(1<<(_W-1)) == 0 {
		panic("x not normalized")
	}
	switch _W {
	case 32:
		return uint32(x[i])
	case 64:
		return uint32(x[i] >> 32)
	}
	panic("unreachable")
}

// msb64 返回 x 的 64 个最高有效位。
func msb64(x nat) uint64 {
	i := len(x) - 1
	if i < 0 {
		return 0
	}
	if debugFloat && x[i]&(1<<(_W-1)) == 0 {
		panic("x not normalized")
	}
	switch _W {
	case 32:
		v := uint64(x[i]) << 32
		if i > 0 {
			v |= uint64(x[i-1])
		}
		return v
	case 64:
		return uint64(x[i])
	}
	panic("unreachable")
}

// Uint64 返回将 x 向零截断得到的无符号整数。
// 若 0 <= x <= [math.MaxUint64]，则当 x 为整数时结果为 [Exact]，
// 否则为 [Below]。
// 对于 x < 0，结果为 (0, [Above])；对于 x > [math.MaxUint64]，
// 结果为 ([math.MaxUint64], [Below])。
func (x *Float) Uint64() (uint64, Accuracy) {
	if debugFloat {
		x.validate()
	}

	switch x.form {
	case finite:
		if x.neg {
			return 0, Above
		}
		// 0 < x < +Inf
		if x.exp <= 0 {
			// 0 < x < 1
			return 0, Below
		}
		// 1 <= x < Inf
		if x.exp <= 64 {
			// u = trunc(x) 可容纳于 uint64
			u := msb64(x.mant) >> (64 - uint32(x.exp))
			if x.MinPrec() <= 64 {
				return u, Exact
			}
			return u, Below // x 被截断
		}
		// x 过大
		return math.MaxUint64, Below

	case zero:
		return 0, Exact

	case inf:
		if x.neg {
			return 0, Above
		}
		return math.MaxUint64, Below
	}

	panic("unreachable")
}

// Int64 返回将 x 向零截断得到的整数。
// 若 [math.MinInt64] <= x <= [math.MaxInt64]，则当 x 为整数时
// 结果为 [Exact]，否则为 [Above]（x < 0）或 [Below]（x > 0）。
// 对于 x < [math.MinInt64]，结果为 ([math.MinInt64], [Above])；
// 对于 x > [math.MaxInt64]，结果为 ([math.MaxInt64], [Below])。
func (x *Float) Int64() (int64, Accuracy) {
	if debugFloat {
		x.validate()
	}

	switch x.form {
	case finite:
		// 0 < |x| < +Inf
		acc := makeAcc(x.neg)
		if x.exp <= 0 {
			// 0 < |x| < 1
			return 0, acc
		}
		// x.exp > 0

		// 1 <= |x| < +Inf
		if x.exp <= 63 {
			// i = trunc(x) 可容纳于 int64（不含 math.MinInt64）
			i := int64(msb64(x.mant) >> (64 - uint32(x.exp)))
			if x.neg {
				i = -i
			}
			if x.MinPrec() <= uint(x.exp) {
				return i, Exact
			}
			return i, acc // x 被截断
		}
		if x.neg {
			// 检查特殊情况 x == math.MinInt64（即 x == -(0.5 << 64)）
			if x.exp == 64 && x.MinPrec() == 1 {
				acc = Exact
			}
			return math.MinInt64, acc
		}
		// x 过大
		return math.MaxInt64, Below

	case zero:
		return 0, Exact

	case inf:
		if x.neg {
			return math.MinInt64, Above
		}
		return math.MaxInt64, Below
	}

	panic("unreachable")
}

// Float32 返回最接近 x 的 float32 值。若 x 太小以致无法用
// float32 表示（|x| < [math.SmallestNonzeroFloat32]），则结果
// 根据 x 的符号分别为 (0, [Below]) 或 (-0, [Above])。
// 若 x 太大以致无法用 float32 表示（|x| > [math.MaxFloat32]），
// 则结果根据 x 的符号为 (+Inf, [Above]) 或 (-Inf, [Below])。
func (x *Float) Float32() (float32, Accuracy) {
	if debugFloat {
		x.validate()
	}

	switch x.form {
	case finite:
		// 0 < |x| < +Inf

		const (
			fbits = 32                //        float 大小
			mbits = 23                //        尾数大小（不含隐式 msb）
			ebits = fbits - mbits - 1 //     8  指数大小
			bias  = 1<<(ebits-1) - 1  //   127  指数偏置
			dmin  = 1 - bias - mbits  //  -149  最小无偏指数（非规格化）
			emin  = 1 - bias          //  -126  最小无偏指数（规格化）
			emax  = bias              //   127  最大无偏指数（规格化）
		)

		// Float 尾数 m 满足 0.5 <= m < 1.0；为 float32 尾数计算指数 e。
		e := x.exp - 1 // 满足 1.0 <= m < 2.0 的规格化尾数 m 的指数

		// 计算 float32 尾数的精度 p。
		// 若指数太小，则在舍入前我们会得到一个非规格化数，
		// 可用的尾数精度少于 p 位
		// （指数保持固定，但尾数向右移位）。
		p := mbits + 1 // 规格化 float 的精度
		if e < emin {
			// 重新计算精度
			p = mbits + 1 - emin + int(e)
			// 若 p == 0，则 x 的尾数向右移动过多，
			// 以致其 msb 正好落在 float32 尾数空间右侧。
			// 换言之，若将最小的非规格化数视为“1.0”，
			// 则对于 p == 0，尾数值 m >= 0.5。
			// 若 m > 0.5，则向上舍入为 1.0，即最小的非规格化数。
			// 若 m == 0.5，则向下舍入到偶数，即 0.0。
			// 若 p < 0，则尾数值 m <= “0.25”，绝不会向上舍入。
			if p < 0 /* m <= 0.25 */ || p == 0 && x.mant.sticky(uint(len(x.mant))*_W-1) == 0 /* m == 0.5 */ {
				// 下溢为 ±0
				if x.neg {
					var z float32
					return -z, Above
				}
				return 0.0, Below
			}
			// 否则向上舍入
			// 我们显式处理 p == 0，因为这很简单，且
			// Float.round 不支持舍入到 0 位精度。
			if p == 0 {
				if x.neg {
					return -math.SmallestNonzeroFloat32, Below
				}
				return math.SmallestNonzeroFloat32, Above
			}
		}
		// p > 0

		// 舍入
		var r Float
		r.prec = uint32(p)
		r.Set(x)
		e = r.exp - 1

		// 舍入可能已导致 r 上溢为 ±Inf
		// （舍入绝不会导致下溢为 0）。
		// 若指数太大，也会上溢为 ±Inf。
		if r.form == inf || e > emax {
			// 上溢
			if x.neg {
				return float32(math.Inf(-1)), Below
			}
			return float32(math.Inf(+1)), Above
		}
		// e <= emax

		// 确定符号、带偏置的指数和尾数。
		var sign, bexp, mant uint32
		if x.neg {
			sign = 1 << (fbits - 1)
		}

		// 舍入可能已使一个非规格化数变为规格化数。
		// 再次检查。
		if e < emin {
			// 非规格化数：重新计算精度
			// 由于舍入最多只会提高精度，且我们已提前排除了 p <= 0，
			// 因此可知 p > 0。
			// 非规格化数的 bexp == 0
			p = mbits + 1 - emin + int(e)
			mant = msb32(r.mant) >> uint(fbits-p)
		} else {
			// 规格化数：emin <= e <= emax
			bexp = uint32(e+bias) << mbits
			mant = msb32(r.mant) >> ebits & (1<<mbits - 1) // 截去 msb（隐式 1 位）
		}

		return math.Float32frombits(sign | bexp | mant), r.acc

	case zero:
		if x.neg {
			var z float32
			return -z, Exact
		}
		return 0.0, Exact

	case inf:
		if x.neg {
			return float32(math.Inf(-1)), Exact
		}
		return float32(math.Inf(+1)), Exact
	}

	panic("unreachable")
}

// Float64 返回最接近 x 的 float64 值。若 x 太小以致无法用
// float64 表示（|x| < [math.SmallestNonzeroFloat64]），则结果
// 根据 x 的符号分别为 (0, [Below]) 或 (-0, [Above])。
// 若 x 太大以致无法用 float64 表示（|x| > [math.MaxFloat64]），
// 则结果根据 x 的符号为 (+Inf, [Above]) 或 (-Inf, [Below])。
func (x *Float) Float64() (float64, Accuracy) {
	if debugFloat {
		x.validate()
	}

	switch x.form {
	case finite:
		// 0 < |x| < +Inf

		const (
			fbits = 64                //        float 大小
			mbits = 52                //        尾数大小（不含隐式 msb）
			ebits = fbits - mbits - 1 //    11  指数大小
			bias  = 1<<(ebits-1) - 1  //  1023  指数偏置
			dmin  = 1 - bias - mbits  // -1074  最小无偏指数（非规格化）
			emin  = 1 - bias          // -1022  最小无偏指数（规格化）
			emax  = bias              //  1023  最大无偏指数（规格化）
		)

		// Float 尾数 m 满足 0.5 <= m < 1.0；为 float64 尾数计算指数 e。
		e := x.exp - 1 // 满足 1.0 <= m < 2.0 的规格化尾数 m 的指数

		// 计算 float64 尾数的精度 p。
		// 若指数太小，则在舍入前我们会得到一个非规格化数，
		// 可用的尾数精度少于 p 位
		// （指数保持固定，但尾数向右移位）。
		p := mbits + 1 // 规格化 float 的精度
		if e < emin {
			// 重新计算精度
			p = mbits + 1 - emin + int(e)
			// 若 p == 0，则 x 的尾数向右移动过多，
			// 以致其 msb 正好落在 float64 尾数空间右侧。
			// 换言之，若将最小的非规格化数视为“1.0”，
			// 则对于 p == 0，尾数值 m >= 0.5。
			// 若 m > 0.5，则向上舍入为 1.0，即最小的非规格化数。
			// 若 m == 0.5，则向下舍入到偶数，即 0.0。
			// 若 p < 0，则尾数值 m <= “0.25”，绝不会向上舍入。
			if p < 0 /* m <= 0.25 */ || p == 0 && x.mant.sticky(uint(len(x.mant))*_W-1) == 0 /* m == 0.5 */ {
				// 下溢为 ±0
				if x.neg {
					var z float64
					return -z, Above
				}
				return 0.0, Below
			}
			// 否则向上舍入
			// 我们显式处理 p == 0，因为这很简单，且
			// Float.round 不支持舍入到 0 位精度。
			if p == 0 {
				if x.neg {
					return -math.SmallestNonzeroFloat64, Below
				}
				return math.SmallestNonzeroFloat64, Above
			}
		}
		// p > 0

		// 舍入
		var r Float
		r.prec = uint32(p)
		r.Set(x)
		e = r.exp - 1

		// 舍入可能已导致 r 上溢为 ±Inf
		// （舍入绝不会导致下溢为 0）。
		// 若指数太大，也会上溢为 ±Inf。
		if r.form == inf || e > emax {
			// 上溢
			if x.neg {
				return math.Inf(-1), Below
			}
			return math.Inf(+1), Above
		}
		// e <= emax

		// 确定符号、带偏置的指数和尾数。
		var sign, bexp, mant uint64
		if x.neg {
			sign = 1 << (fbits - 1)
		}

		// 舍入可能已使一个非规格化数变为规格化数。
		// 再次检查。
		if e < emin {
			// 非规格化数：重新计算精度
			// 由于舍入最多只会提高精度，且我们已提前排除了 p <= 0，
			// 因此可知 p > 0。
			// 非规格化数的 bexp == 0
			p = mbits + 1 - emin + int(e)
			mant = msb64(r.mant) >> uint(fbits-p)
		} else {
			// 规格化数：emin <= e <= emax
			bexp = uint64(e+bias) << mbits
			mant = msb64(r.mant) >> ebits & (1<<mbits - 1) // 截去 msb（隐式 1 位）
		}

		return math.Float64frombits(sign | bexp | mant), r.acc

	case zero:
		if x.neg {
			var z float64
			return -z, Exact
		}
		return 0.0, Exact

	case inf:
		if x.neg {
			return math.Inf(-1), Exact
		}
		return math.Inf(+1), Exact
	}

	panic("unreachable")
}

// Int 返回将 x 向零截断的结果；
// 若 x 为无穷则返回 nil。
// 若 x.IsInt() 则结果为 [Exact]；否则对于 x > 0 为 [Below]，
// 对于 x < 0 为 [Above]。
// 若提供了非 nil 的 *[Int] 参数 z，[Int] 会将结果存储在 z 中，
// 而不是分配新的 [Int]。
func (x *Float) Int(z *Int) (*Int, Accuracy) {
	if debugFloat {
		x.validate()
	}

	if z == nil && x.form <= finite {
		z = new(Int)
	}

	switch x.form {
	case finite:
		// 0 < |x| < +Inf
		acc := makeAcc(x.neg)
		if x.exp <= 0 {
			// 0 < |x| < 1
			return z.SetInt64(0), acc
		}
		// x.exp > 0

		// 1 <= |x| < +Inf
		// 确定 x 所需的最小精度
		allBits := uint(len(x.mant)) * _W
		exp := uint(x.exp)
		if x.MinPrec() <= exp {
			acc = Exact
		}
		// 按需移位尾数
		if z == nil {
			z = new(Int)
		}
		z.neg = x.neg
		switch {
		case exp > allBits:
			z.abs = z.abs.lsh(x.mant, exp-allBits)
		default:
			z.abs = z.abs.set(x.mant)
		case exp < allBits:
			z.abs = z.abs.rsh(x.mant, allBits-exp)
		}
		return z, acc

	case zero:
		return z.SetInt64(0), Exact

	case inf:
		return nil, makeAcc(x.neg)
	}

	panic("unreachable")
}

// Rat 返回与 x 对应的有理数；若 x 为无穷则返回 nil。
// 若 x 不是 Inf，则结果为 [Exact]。
// 若提供了非 nil 的 *[Rat] 参数 z，[Rat] 会将结果存储在 z 中，
// 而不是分配新的 [Rat]。
func (x *Float) Rat(z *Rat) (*Rat, Accuracy) {
	if debugFloat {
		x.validate()
	}

	if z == nil && x.form <= finite {
		z = new(Rat)
	}

	switch x.form {
	case finite:
		// 0 < |x| < +Inf
		allBits := int32(len(x.mant)) * _W
		// 构建分子和分母
		z.a.neg = x.neg
		switch {
		case x.exp > allBits:
			z.a.abs = z.a.abs.lsh(x.mant, uint(x.exp-allBits))
			z.b.abs = z.b.abs[:0] // == 1（见 Rat）
			// z 已经是规范形式
		default:
			z.a.abs = z.a.abs.set(x.mant)
			z.b.abs = z.b.abs[:0] // == 1（见 Rat）
			// z 已经是规范形式
		case x.exp < allBits:
			z.a.abs = z.a.abs.set(x.mant)
			t := z.b.abs.setUint64(1)
			z.b.abs = t.lsh(t, uint(allBits-x.exp))
			z.norm()
		}
		return z, Exact

	case zero:
		return z.SetInt64(0), Exact

	case inf:
		return nil, makeAcc(x.neg)
	}

	panic("unreachable")
}

// Abs 将 z 设为 |x| 的（可能已舍入的）值（x 的绝对值）
// 并返回 z。
func (z *Float) Abs(x *Float) *Float {
	z.Set(x)
	z.neg = false
	return z
}

// Neg 将 z 设为 x 的（可能已舍入的）值并取反其符号，
// 并返回 z。
func (z *Float) Neg(x *Float) *Float {
	z.Set(x)
	z.neg = !z.neg
	return z
}

func validateBinaryOperands(x, y *Float) {
	if !debugFloat {
		// 避免性能问题
		panic("validateBinaryOperands called but debugFloat is not set")
	}
	if len(x.mant) == 0 {
		panic("empty mantissa for x")
	}
	if len(y.mant) == 0 {
		panic("empty mantissa for y")
	}
}

// z = x + y，加法时忽略 x 和 y 的符号，
// 但使用 z 的符号对结果进行舍入。
// x 和 y 必须具有非空尾数和有效指数。
func (z *Float) uadd(x, y *Float) {
	// 注意：此实现大多数情况下需要 2 次移位。
	// 若指数或精度相差悬殊，它效率也不高。
	// 以下文章描述了一种与此处所用内部表示兼容的
	// 高效（但复杂得多）的实现：
	//
	// Vincent Lefèvre: "The Generic Multiple-Precision Floating-
	// Point Addition With Exact Rounding (as in the MPFR Library)"
	// http://www.vinc17.net/research/papers/rnc6.pdf

	if debugFloat {
		validateBinaryOperands(x, y)
	}

	// 为“二进制小数点”在右侧（mantissa.0）的尾数
	// 计算指数 ex、ey - 使用 int64 避免溢出
	ex := int64(x.exp) - int64(len(x.mant))*_W
	ey := int64(y.exp) - int64(len(y.mant))*_W

	al := alias(z.mant, x.mant) || alias(z.mant, y.mant)

	// TODO(gri) 提供一个组合的加法-移位原语
	//           可以显著加快此代码
	switch {
	case ex < ey:
		if al {
			t := nat(nil).lsh(y.mant, uint(ey-ex))
			z.mant = z.mant.add(x.mant, t)
		} else {
			z.mant = z.mant.lsh(y.mant, uint(ey-ex))
			z.mant = z.mant.add(x.mant, z.mant)
		}
	default:
		// ex == ey，无需移位
		z.mant = z.mant.add(x.mant, y.mant)
	case ex > ey:
		if al {
			t := nat(nil).lsh(x.mant, uint(ex-ey))
			z.mant = z.mant.add(t, y.mant)
		} else {
			z.mant = z.mant.lsh(x.mant, uint(ex-ey))
			z.mant = z.mant.add(z.mant, y.mant)
		}
		ex = ey
	}
	// len(z.mant) > 0

	z.setExpAndRound(ex+int64(len(z.mant))*_W-fnorm(z.mant), 0)
}

// 对于 |x| > |y|，z = x - y，减法时忽略 x 和 y 的符号，
// 但使用 z 的符号对结果进行舍入。
// x 和 y 必须具有非空尾数和有效指数。
func (z *Float) usub(x, y *Float) {
	// 此代码与 uadd 对称。
	// 我们没有把公共代码抽取出来，因为最终 uadd（和 usub）
	// 应通过特殊化处理进行优化，届时代码将会分化。

	if debugFloat {
		validateBinaryOperands(x, y)
	}

	ex := int64(x.exp) - int64(len(x.mant))*_W
	ey := int64(y.exp) - int64(len(y.mant))*_W

	al := alias(z.mant, x.mant) || alias(z.mant, y.mant)

	switch {
	case ex < ey:
		if al {
			t := nat(nil).lsh(y.mant, uint(ey-ex))
			z.mant = t.sub(x.mant, t)
		} else {
			z.mant = z.mant.lsh(y.mant, uint(ey-ex))
			z.mant = z.mant.sub(x.mant, z.mant)
		}
	default:
		// ex == ey，无需移位
		z.mant = z.mant.sub(x.mant, y.mant)
	case ex > ey:
		if al {
			t := nat(nil).lsh(x.mant, uint(ex-ey))
			z.mant = t.sub(t, y.mant)
		} else {
			z.mant = z.mant.lsh(x.mant, uint(ex-ey))
			z.mant = z.mant.sub(z.mant, y.mant)
		}
		ex = ey
	}

	// 操作数可能相互抵消
	if len(z.mant) == 0 {
		z.acc = Exact
		z.form = zero
		z.neg = false
		return
	}
	// len(z.mant) > 0

	z.setExpAndRound(ex+int64(len(z.mant))*_W-fnorm(z.mant), 0)
}

// z = x * y，乘法时忽略 x 和 y 的符号，
// 但使用 z 的符号对结果进行舍入。
// x 和 y 必须具有非空尾数和有效指数。
func (z *Float) umul(x, y *Float) {
	if debugFloat {
		validateBinaryOperands(x, y)
	}

	// 注意：若 z 的精度小于 x 与 y 的精度之和，
	// 则会做过多的工作，而这通常是常见情况
	// （例如，若所有 float 具有相同精度）。
	// TODO(gri) 针对常见情况对此进行优化。

	e := int64(x.exp) + int64(y.exp)
	if x == y {
		z.mant = z.mant.sqr(nil, x.mant)
	} else {
		z.mant = z.mant.mul(nil, x.mant, y.mant)
	}
	z.setExpAndRound(e-fnorm(z.mant), 0)
}

// z = x / y，除法时忽略 x 和 y 的符号，
// 但使用 z 的符号对结果进行舍入。
// x 和 y 必须具有非空尾数和有效指数。
func (z *Float) uquo(x, y *Float) {
	if debugFloat {
		validateBinaryOperands(x, y)
	}

	// 所需结果精度的尾数长度（以字为单位）+ 1
	// （至少多一位，以便除法后能得到舍入位）
	n := int(z.prec/_W) + 1

	// 计算调整后的 x.mant，以获得足够的结果精度
	xadj := x.mant
	if d := n - len(x.mant) + len(y.mant); d > 0 {
		// 需要 d 个额外的字 => 在 x 后添加 d 个“0 位”
		xadj = make(nat, len(x.mant)+d)
		copy(xadj[d:], x.mant)
	}
	// TODO(gri): 若位数太多（d < 0），我们应该能够
	// 缩短 x 以加快除法。但在那种情况下必须格外小心
	// 舍入。

	// 在除法之前计算 d，因为 x.mant（经由 xadj）或 y.mant
	// 可能与 z.mant 存在别名。
	d := len(xadj) - len(y.mant)

	// 相除
	stk := getStack()
	defer stk.free()
	var r nat
	z.mant, r = z.mant.div(stk, nil, xadj, y.mant)
	e := int64(x.exp) - int64(y.exp) - int64(d-len(z.mant))*_W

	// 结果的长度足以包含（至少）舍入位。
	// 若存在非零余数，则相应的（若计算出来的话）
	// 小数部分会有非零的粘滞位（若它为零，
	// 就不会有非零余数）。
	var sbit uint
	if len(r) > 0 {
		sbit = 1
	}

	z.setExpAndRound(e-fnorm(z.mant), sbit)
}

// ucmp 返回 -1、0 或 +1，取决于
// |x| < |y|、|x| == |y| 还是 |x| > |y|。
// x 和 y 必须具有非空尾数和有效指数。
func (x *Float) ucmp(y *Float) int {
	if debugFloat {
		validateBinaryOperands(x, y)
	}

	switch {
	case x.exp < y.exp:
		return -1
	case x.exp > y.exp:
		return +1
	}
	// x.exp == y.exp

	// 比较尾数
	i := len(x.mant)
	j := len(y.mant)
	for i > 0 || j > 0 {
		var xm, ym Word
		if i > 0 {
			i--
			xm = x.mant[i]
		}
		if j > 0 {
			j--
			ym = y.mant[j]
		}
		switch {
		case xm < ym:
			return -1
		case xm > ym:
			return +1
		}
	}

	return 0
}

// 按 IEEE 754-2008 第 6.3 节的定义处理符号位：
//
// 当输入和结果均非 NaN 时，乘积或商的符号是操作数符号的
// 异或；和（或被视为和 x+(−y) 的差 x−y）的符号与至多
// 一个加数的符号不同；而转换、quantize 操作、
// roundToIntegral 操作以及 roundToIntegralExact（见 5.3.1）
// 的结果符号是第一个或唯一操作数的符号。
// 即使操作数或结果为零或无穷，这些规则也应适用。
//
// 当两个符号相反的操作数之和（或两个符号相同的操作数之差）
// 恰好为零时，该和（或差）的符号在所有舍入方向属性下
// 都应为 +0，但 roundTowardNegative 除外；在该属性下，
// 精确零和（或差）的符号应为 −0。不过，即使 x 为零，
// x+x = x−(−x) 也保持与 x 相同的符号。
//
// 另见：https://play.golang.org/p/RtH3UCt5IH

// Add 将 z 设为舍入后的和 x+y 并返回 z。若 z 的精度为 0，
// 则在操作前将其改为 x 与 y 中较大的精度。
// 舍入根据 z 的精度和舍入模式执行；z 的准确性报告
// 结果相对于精确（未舍入）结果的误差。
// 若 x 和 y 是符号相反的无穷，Add 会 panic 并给出 [ErrNaN]。
// 此时 z 的值未定义。
func (z *Float) Add(x, y *Float) *Float {
	if debugFloat {
		x.validate()
		y.validate()
	}

	if z.prec == 0 {
		z.prec = max(x.prec, y.prec)
	}

	if x.form == finite && y.form == finite {
		// x + y（常见情况）

		// 下面我们设置 z.neg = x.neg，当 z 与 y 别名时，
		// 这会改变 y 操作数的符号。这没问题，因为若某个
		// 操作数与接收者别名，它将被覆盖，但在求值
		// x.neg != y.neg 时我们仍然需要原始的 x.neg 和 y.neg 值，
		// 因此需要在设置 z.neg 之前保存 y.neg。
		yneg := y.neg

		z.neg = x.neg
		if x.neg == yneg {
			// x + y == x + y
			// (-x) + (-y) == -(x + y)
			z.uadd(x, y)
		} else {
			// x + (-y) == x - y == -(y - x)
			// (-x) + y == y - x == -(x - y)
			if x.ucmp(y) > 0 {
				z.usub(x, y)
			} else {
				z.neg = !z.neg
				z.usub(y, x)
			}
		}
		if z.form == zero && z.mode == ToNegativeInf && z.acc == Exact {
			z.neg = true
		}
		return z
	}

	if x.form == inf && y.form == inf && x.neg != y.neg {
		// +Inf + -Inf
		// -Inf + +Inf
		// z 的值未定义，但要确保它是有效的
		z.acc = Exact
		z.form = zero
		z.neg = false
		panic(ErrNaN{"addition of infinities with opposite signs"})
	}

	if x.form == zero && y.form == zero {
		// ±0 + ±0
		z.acc = Exact
		z.form = zero
		z.neg = x.neg && y.neg // -0 + -0 == -0
		return z
	}

	if x.form == inf || y.form == zero {
		// ±Inf + y
		// x + ±0
		return z.Set(x)
	}

	// ±0 + y
	// x + ±Inf
	return z.Set(y)
}

// Sub 将 z 设为舍入后的差 x-y 并返回 z。
// 精度、舍入和准确性报告与 [Float.Add] 相同。
// 若 x 和 y 是符号相同的无穷，Sub 会 panic 并给出 [ErrNaN]。
// 此时 z 的值未定义。
func (z *Float) Sub(x, y *Float) *Float {
	if debugFloat {
		x.validate()
		y.validate()
	}

	if z.prec == 0 {
		z.prec = max(x.prec, y.prec)
	}

	if x.form == finite && y.form == finite {
		// x - y（常见情况）
		yneg := y.neg
		z.neg = x.neg
		if x.neg != yneg {
			// x - (-y) == x + y
			// (-x) - y == -(x + y)
			z.uadd(x, y)
		} else {
			// x - y == x - y == -(y - x)
			// (-x) - (-y) == y - x == -(x - y)
			if x.ucmp(y) > 0 {
				z.usub(x, y)
			} else {
				z.neg = !z.neg
				z.usub(y, x)
			}
		}
		if z.form == zero && z.mode == ToNegativeInf && z.acc == Exact {
			z.neg = true
		}
		return z
	}

	if x.form == inf && y.form == inf && x.neg == y.neg {
		// +Inf - +Inf
		// -Inf - -Inf
		// z 的值未定义，但要确保它是有效的
		z.acc = Exact
		z.form = zero
		z.neg = false
		panic(ErrNaN{"subtraction of infinities with equal signs"})
	}

	if x.form == zero && y.form == zero {
		// ±0 - ±0
		z.acc = Exact
		z.form = zero
		z.neg = x.neg && !y.neg // -0 - +0 == -0
		return z
	}

	if x.form == inf || y.form == zero {
		// ±Inf - y
		// x - ±0
		return z.Set(x)
	}

	// ±0 - y
	// x - ±Inf
	return z.Neg(y)
}

// Mul 将 z 设为舍入后的积 x*y 并返回 z。
// 精度、舍入和准确性报告与 [Float.Add] 相同。
// 若一个操作数为零而另一个操作数为无穷，Mul 会 panic
// 并给出 [ErrNaN]。此时 z 的值未定义。
func (z *Float) Mul(x, y *Float) *Float {
	if debugFloat {
		x.validate()
		y.validate()
	}

	if z.prec == 0 {
		z.prec = max(x.prec, y.prec)
	}

	z.neg = x.neg != y.neg

	if x.form == finite && y.form == finite {
		// x * y（常见情况）
		z.umul(x, y)
		return z
	}

	z.acc = Exact
	if x.form == zero && y.form == inf || x.form == inf && y.form == zero {
		// ±0 * ±Inf
		// ±Inf * ±0
		// z 的值未定义，但要确保它是有效的
		z.form = zero
		z.neg = false
		panic(ErrNaN{"multiplication of zero with infinity"})
	}

	if x.form == inf || y.form == inf {
		// ±Inf * y
		// x * ±Inf
		z.form = inf
		return z
	}

	// ±0 * y
	// x * ±0
	z.form = zero
	return z
}

// Quo 将 z 设为舍入后的商 x/y 并返回 z。
// 精度、舍入和准确性报告与 [Float.Add] 相同。
// 若两个操作数都为零或都为无穷，Quo 会 panic 并给出 [ErrNaN]。
// 此时 z 的值未定义。
func (z *Float) Quo(x, y *Float) *Float {
	if debugFloat {
		x.validate()
		y.validate()
	}

	if z.prec == 0 {
		z.prec = max(x.prec, y.prec)
	}

	z.neg = x.neg != y.neg

	if x.form == finite && y.form == finite {
		// x / y（常见情况）
		z.uquo(x, y)
		return z
	}

	z.acc = Exact
	if x.form == zero && y.form == zero || x.form == inf && y.form == inf {
		// ±0 / ±0
		// ±Inf / ±Inf
		// z 的值未定义，但要确保它是有效的
		z.form = zero
		z.neg = false
		panic(ErrNaN{"division of zero by zero or infinity by infinity"})
	}

	if x.form == zero || y.form == inf {
		// ±0 / y
		// x / ±Inf
		z.form = zero
		return z
	}

	// x / ±0
	// ±Inf / y
	z.form = inf
	return z
}

// Cmp 比较 x 和 y 并返回：
//   - 若 x < y，返回 -1；
//   - 若 x == y（包括 -0 == 0、-Inf == -Inf 和 +Inf == +Inf），返回 0；
//   - 若 x > y，返回 +1。
func (x *Float) Cmp(y *Float) int {
	if debugFloat {
		x.validate()
		y.validate()
	}

	mx := x.ord()
	my := y.ord()
	switch {
	case mx < my:
		return -1
	case mx > my:
		return +1
	}
	// mx == my

	// 只有当 |mx| == 1 时我们才需要比较尾数
	switch mx {
	case -1:
		return y.ucmp(x)
	case +1:
		return x.ucmp(y)
	}

	return 0
}

// ord 对 x 进行分类并返回：
//
//	-2 若 -Inf == x
//	-1 若 -Inf < x < 0
//	 0 若 x == 0（有符号或无符号）
//	+1 若 0 < x < +Inf
//	+2 若 x == +Inf
func (x *Float) ord() int {
	var m int
	switch x.form {
	case finite:
		m = 1
	case zero:
		return 0
	case inf:
		m = 2
	}
	if x.neg {
		m = -m
	}
	return m
}
