// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现多精度十进制数。
// 该实现仅用于浮点数到十进制的转换；
// 并非通用用途。
// 唯一支持的操作是二进制到十进制的精确
// 转换以及舍入。
//
// 关键观察以及部分代码（shr）借鉴自
// strconv/decimal.go：二进制小数值的转换可以在多精度十进制中
// 精确完成，因为 2 整除 10（尾数的 >> 运算需要此性质）；
// 但十进制浮点值无法在二进制表示中精确转换。
//
// 与 strconv/decimal.go 不同，这里只用十进制格式实现右移——
// 左移可以在二进制格式中精确完成。

package big

// 一个 decimal 表示十进制表示下的无符号浮点数。
// 非零 decimal d 的值为 d.mant * 10**d.exp，其中 0.1 <= d.mant < 1，
// 最高有效尾数位位于下标 0 处。对于零 decimal，其尾数长度和指数均为 0。
// decimal 的零值表示可直接使用的 0.0。
type decimal struct {
	mant []byte // 尾数 ASCII 数字，大端序
	exp  int    // 指数
}

// at 返回第 i 个尾数位，从下标 0 处的最高有效位开始。
func (d *decimal) at(i int) byte {
	if 0 <= i && i < len(d.mant) {
		return d.mant[i]
	}
	return '0'
}

// 一次遍历中可以不溢出完成的最大移位量。
// 一个 Word 有 _W 位，且 (1<<maxShift - 1)*10 + 9 必须能放入 Word。
const maxShift = _W - 4

// TODO(gri) 由于在转换浮点数时我们知道所需的十进制精度，
// 我们或许可以通过额外提供一个精度参数并跟踪数字何时被提前截断
// （相当于二进制舍入中的“粘滞位”）来限制 init 需要计算的十进制
// 位数。

// TODO(gri) 同理，对移位量施加某种限制，
// 以避免“无限”漫长的转换（直到耗尽空间）。

// Init 将 x 初始化为 m << shift（当 shift >= 0 时）或
// m >> -shift（当 shift < 0 时）的十进制表示。
func (x *decimal) init(m nat, shift int) {
	// 特殊情况 0
	if len(m) == 0 {
		x.mant = x.mant[:0]
		x.exp = 0
		return
	}

	// 优化：如果需要进行右移，首先移除 m 中任何末尾的
	// 零比特，以减少需要在十进制格式中完成的移位量
	// （因为那可能更慢）。
	if shift < 0 {
		ntz := m.trailingZeroBits()
		s := uint(-shift)
		if s >= ntz {
			s = ntz // 最多移位 ntz 位
		}
		m = nat(nil).rsh(m, s)
		shift += int(s)
	}

	// 在二进制表示中完成任何左移。
	if shift > 0 {
		m = nat(nil).lsh(m, uint(shift))
		shift = 0
	}

	// 将尾数转换为十进制表示。
	s := m.utoa(10)
	n := len(s)
	x.exp = n
	// 去除末尾的零；改为由指数独立于数字位数
	// 跟踪小数点的位置。
	for n > 0 && s[n-1] == '0' {
		n--
	}
	x.mant = append(x.mant[:0], s[:n]...)

	// 在十进制表示中完成任何（剩余的）右移。
	if shift < 0 {
		for shift < -maxShift {
			rsh(x, maxShift)
			shift += maxShift
		}
		rsh(x, uint(-shift))
	}
}

// rsh 实现 x >> s，其中 s <= maxShift。
func rsh(x *decimal, s uint) {
	// 使用移位相减算法做除以 1<<s 的运算。

	// 取足够多的前导数字以覆盖第一次移位
	r := 0 // 读取下标
	var n Word
	for n>>s == 0 && r < len(x.mant) {
		ch := Word(x.mant[r])
		r++
		n = n*10 + ch - '0'
	}
	if n == 0 {
		// x == 0；不应到达这里，但仍作处理
		x.mant = x.mant[:0]
		return
	}
	for n>>s == 0 {
		r++
		n *= 10
	}
	x.exp += 1 - r

	// 读一个数字，写一个数字
	w := 0 // 写入下标
	mask := Word(1)<<s - 1
	for r < len(x.mant) {
		ch := Word(x.mant[r])
		r++
		d := n >> s
		n &= mask // n -= d << s
		x.mant[w] = byte(d + '0')
		w++
		n = n*10 + ch - '0'
	}

	// 写出仍然能放下的额外数字
	for n > 0 && w < len(x.mant) {
		d := n >> s
		n &= mask
		x.mant[w] = byte(d + '0')
		w++
		n = n * 10
	}
	x.mant = x.mant[:w] // 数字可能会更短（例如 1024 >> 10）

	// 追加放不下的额外数字
	for n > 0 {
		d := n >> s
		n &= mask
		x.mant = append(x.mant, byte(d+'0'))
		n = n * 10
	}

	trim(x)
}

func (x *decimal) String() string {
	if len(x.mant) == 0 {
		return "0"
	}

	var buf []byte
	switch {
	case x.exp <= 0:
		// 0.00ddd
		buf = make([]byte, 0, 2+(-x.exp)+len(x.mant))
		buf = append(buf, "0."...)
		buf = appendZeros(buf, -x.exp)
		buf = append(buf, x.mant...)

	case /* 0 < */ x.exp < len(x.mant):
		// dd.ddd
		buf = make([]byte, 0, 1+len(x.mant))
		buf = append(buf, x.mant[:x.exp]...)
		buf = append(buf, '.')
		buf = append(buf, x.mant[x.exp:]...)

	default: // len(x.mant) <= x.exp
		// ddd00
		buf = make([]byte, 0, x.exp)
		buf = append(buf, x.mant...)
		buf = appendZeros(buf, x.exp-len(x.mant))
	}

	return string(buf)
}

// appendZeros 向 buf 追加 n 个 0 数字并返回 buf。
func appendZeros(buf []byte, n int) []byte {
	for ; n > 0; n-- {
		buf = append(buf, '0')
	}
	return buf
}

// shouldRoundUp 报告当 x 被缩短到 n 位数字时是否应向上舍入。
// n 必须是 x.mant 的有效下标。
func shouldRoundUp(x *decimal, n int) bool {
	if x.mant[n] == '5' && n+1 == len(x.mant) {
		// 正好处于中点——向偶数舍入
		return n > 0 && (x.mant[n-1]-'0')&1 != 0
	}
	// 不在中点——该位数字决定一切（x.mant 没有末尾零）
	return x.mant[n] >= '5'
}

// round 通过将 x 舍入到具有 n（或更少）个尾数位的最接近偶数值，
// 把 x 设为至多 n 个尾数位。
// 如果 n < 0，则 x 保持不变。
func (x *decimal) round(n int) {
	if n < 0 || n >= len(x.mant) {
		return // 无需操作
	}

	if shouldRoundUp(x, n) {
		x.roundUp(n)
	} else {
		x.roundDown(n)
	}
}

func (x *decimal) roundUp(n int) {
	if n < 0 || n >= len(x.mant) {
		return // 无需操作
	}
	// 0 <= n < len(x.mant)

	// 找到第一个 < '9' 的数字
	for n > 0 && x.mant[n-1] >= '9' {
		n--
	}

	if n == 0 {
		// 所有数字都是 '9' => 向上舍入为 '1' 并更新指数
		x.mant[0] = '1' // 可以，因为 len(x.mant) > n
		x.mant = x.mant[:1]
		x.exp++
		return
	}

	// n > 0 && x.mant[n-1] < '9'
	x.mant[n-1]++
	x.mant = x.mant[:n]
	// x 已经过裁剪
}

func (x *decimal) roundDown(n int) {
	if n < 0 || n >= len(x.mant) {
		return // 无需操作
	}
	x.mant = x.mant[:n]
	trim(x)
}

// trim 去除 x 尾数中任何末尾的零；
// 它们对 x 的值没有意义。
func trim(x *decimal) {
	i := len(x.mant)
	for i > 0 && x.mant[i-1] == '0' {
		i--
	}
	x.mant = x.mant[:i]
	if i == 0 {
		x.exp = 0
	}
}
