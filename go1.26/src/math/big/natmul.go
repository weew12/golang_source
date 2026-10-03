// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 乘法。

package big

// 短于 karatsubaThreshold 的操作数使用“小学式”乘法进行相乘；
// 对于更长的操作数则使用 Karatsuba 算法。
var karatsubaThreshold = 40 // 参见 calibrate_test.go

// mul 设置 z = x*y，使用 stk 作为临时存储。
// 调用者可以传入 stk == nil，以请求 mul 自行获取并释放一个。
func (z nat) mul(stk *stack, x, y nat) nat {
	m := len(x)
	n := len(y)

	switch {
	case m < n:
		return z.mul(stk, y, x)
	case m == 0 || n == 0:
		return z[:0]
	case n == 1:
		return z.mulAddWW(x, y[0], 0)
	}
	// m >= n > 1

	// 判断 z 是否可以被复用
	if alias(z, x) || alias(z, y) {
		z = nil // z 是 x 或 y 的别名 - 无法复用
	}
	z = z.make(m + n)

	// 如果数字较小则使用基本乘法
	if n < karatsubaThreshold {
		basicMul(z, x, y)
		return z.norm()
	}

	if stk == nil {
		stk = getStack()
		defer stk.free()
	}

	// 令 x = x1:x0，其中 x0 与 y 长度相同。
	// 计算 z = x0*y，然后按段加入 x1*y
	// （如果需要）。
	karatsuba(stk, z[:2*n], x[:n], y)

	if n < m {
		clear(z[2*n:])
		defer stk.restore(stk.save())
		t := stk.nat(2 * n)
		for i := n; i < m; i += n {
			t = t.mul(stk, x[i:min(i+n, len(x))], y)
			addTo(z[i:], t)
		}
	}

	return z.norm()
}

// 短于 basicSqrThreshold 的操作数使用“小学式”乘法进行平方；
// 对于长于 karatsubaSqrThreshold 的操作数，
// 我们使用针对 x == y 优化的 Karatsuba 算法。
var basicSqrThreshold = 12     // 参见 calibrate_test.go
var karatsubaSqrThreshold = 80 // 参见 calibrate_test.go

// sqr 设置 z = x*x，使用 stk 作为临时存储。
// 调用者可以传入 stk == nil，以请求 sqr 自行获取并释放一个。
func (z nat) sqr(stk *stack, x nat) nat {
	n := len(x)
	switch {
	case n == 0:
		return z[:0]
	case n == 1:
		d := x[0]
		z = z.make(2)
		z[1], z[0] = mulWW(d, d)
		return z.norm()
	}

	if alias(z, x) {
		z = nil // z 是 x 的别名 - 无法复用
	}
	z = z.make(2 * n)

	if n < basicSqrThreshold && n < karatsubaSqrThreshold {
		basicMul(z, x, x)
		return z.norm()
	}

	if stk == nil {
		stk = getStack()
		defer stk.free()
	}

	if n < karatsubaSqrThreshold {
		basicSqr(stk, z, x)
		return z.norm()
	}

	karatsubaSqr(stk, z, x)
	return z.norm()
}

// basicSqr 设置 z = x*x，其渐近速度比 basicMul 快
// 约一个因子 2，但由于开销，对于小参数反而更慢。
// 要求：len(x) > 0，len(z) == 2*len(x)
// （未归一化的）结果放在 z 中。
func basicSqr(stk *stack, z, x nat) {
	n := len(x)
	if n < basicSqrThreshold {
		basicMul(z, x, x)
		return
	}

	defer stk.restore(stk.save())
	t := stk.nat(2 * n)
	clear(t)
	z[1], z[0] = mulWW(x[0], x[0]) // 初始平方
	for i := 1; i < n; i++ {
		d := x[i]
		// z 收集平方 x[i] * x[i]
		z[2*i+1], z[2*i] = mulWW(d, d)
		// t 收集乘积 x[i] * x[j]，其中 j < i
		t[2*i] = addMulVVWW(t[i:2*i], t[i:2*i], x[0:i], d, 0)
	}
	t[2*n-1] = lshVU(t[1:2*n-1], t[1:2*n-1], 1) // 将 j < i 的乘积加倍
	addVV(z, z, t)                              // 合并结果
}

// mulAddWW 返回 z = x*y + r。
func (z nat) mulAddWW(x nat, y, r Word) nat {
	m := len(x)
	if m == 0 || y == 0 {
		return z.setWord(r) // 结果为 r
	}
	// m > 0

	z = z.make(m + 1)
	z[m] = mulAddVWW(z[0:m], x, y, r)

	return z.norm()
}

// basicMul 将 x 与 y 相乘，并将结果留在 z 中。
// （未归一化的）结果放在 z[0 : len(x) + len(y)] 中。
func basicMul(z, x, y nat) {
	clear(z[0 : len(x)+len(y)]) // 初始化 z
	for i, d := range y {
		if d != 0 {
			z[len(x)+i] = addMulVVWW(z[i:i+len(x)], z[i:i+len(x)], x, d, 0)
		}
	}
}

// karatsuba 将 x 与 y 相乘，
// 将（未归一化的）结果写入 z。
// x 和 y 必须长度相同，均为 n，
// 且 z 的长度必须是它的两倍。
func karatsuba(stk *stack, z, x, y nat) {
	n := len(y)
	if len(x) != n || len(z) != 2*n {
		panic("bad karatsuba length")
	}

	// 如果足够小则退回到基本算法。
	if n < karatsubaThreshold || n < 2 {
		basicMul(z, x, y)
		return
	}

	// 记法 x1:x0 表示对于某个 N 的 nat 值 (x1<<N)+x0，
	// 类似地 z2:z1:z0 = (z2<<2N)+(z1<<N)+z0。
	//
	// （注意 z0、z1、z2 可能 ≥ 2**N，此时这一记法表示
	// 比如 z0 的高位被加到 z1 的低位上。）
	//
	// Karatsuba 乘法基于以下观察：
	//
	//	x1:x0 * y1:y0 = x1*y1:(x0*y1+y0*x1):x0*y0
	//	              = x1*y1:((x0-x1)*(y1-y0)+x1*y1+x0*y0):x0*y0
	//
	// 第二种形式只使用三次半宽乘法，
	// 而直接的第一种形式需要四次。
	//
	// 我们把三部分称为 z0、z1、z2：
	//
	//	z0 = x0*y0
	//	z2 = x1*y1
	//	z1 = (x0-x1)*(y1-y0) + z0 + z2

	n2 := (n + 1) / 2
	x0, x1 := &Int{abs: x[:n2].norm()}, &Int{abs: x[n2:].norm()}
	y0, y1 := &Int{abs: y[:n2].norm()}, &Int{abs: y[n2:].norm()}
	z0 := &Int{abs: z[0 : 2*n2]}
	z2 := &Int{abs: z[2*n2:]}

	// 为 z1 分配临时存储；将 z0 改用作存放 tx 和 ty。
	defer stk.restore(stk.save())
	z1 := &Int{abs: stk.nat(2*n2 + 1)}
	tx := &Int{abs: z[0:n2]}
	ty := &Int{abs: z[n2 : 2*n2]}

	tx.Sub(x0, x1)
	ty.Sub(y1, y0)
	z1.mul(stk, tx, ty)

	clear(z)
	z0.mul(stk, x0, y0)
	z2.mul(stk, x1, y1)
	z1.Add(z1, z0)
	z1.Add(z1, z2)
	addTo(z[n2:], z1.abs)

	// 调试模式：复核答案，并在失败时打印跟踪信息。
	const debug = false
	if debug {
		zz := make(nat, len(z))
		basicMul(zz, x, y)
		if z.cmp(zz) != 0 {
			// 所有临时变量都曾被别名到 z，现已消失。重新计算。
			z0 = new(Int)
			z0.mul(stk, x0, y0)
			tx = new(Int).Sub(x1, x0)
			ty = new(Int).Sub(y0, y1)
			z2 = new(Int)
			z2.mul(stk, x1, y1)
			print("karatsuba wrong\n")
			trace("x ", &Int{abs: x})
			trace("y ", &Int{abs: y})
			trace("z ", &Int{abs: z})
			trace("zz", &Int{abs: zz})
			trace("x0", x0)
			trace("x1", x1)
			trace("y0", y0)
			trace("y1", y1)
			trace("tx", tx)
			trace("ty", ty)
			trace("z0", z0)
			trace("z1", z1)
			trace("z2", z2)
			panic("karatsuba")
		}
	}

}

// karatsubaSqr 对 x 求平方，
// 将（未归一化的）结果写入 z。
// z 的长度必须是 2*len(x)。
// 它类似于 [karatsuba]，但由于知道两个乘数
// 是同一个值，因此运行得更快。
func karatsubaSqr(stk *stack, z, x nat) {
	n := len(x)
	if len(z) != 2*n {
		panic("bad karatsubaSqr length")
	}

	if n < karatsubaSqrThreshold || n < 2 {
		basicSqr(stk, z, x)
		return
	}

	// 回忆一下，对于 karatsuba，我们要计算：
	//
	//	x1:x0 * y1:y0 = x1y1:(x0y1+y0x1):x0y0
	//                = x1y1:((x0-x1)*(y1-y0)+x1y1+x0y0):x0y0
	//	              = z2:z1:z0
	// 其中：
	//
	//	z0 = x0y0
	//	z2 = x1y1
	//	z1 = (x0-x1)*(y1-y0) + z0 + z2
	//
	// 当 x = y 时，这些式子简化为：
	//
	//	z0 = x0²
	//	z2 = x1²
	//	z1 = z0 + z2 - (x0-x1)²

	n2 := (n + 1) / 2
	x0, x1 := &Int{abs: x[:n2].norm()}, &Int{abs: x[n2:].norm()}
	z0 := &Int{abs: z[0 : 2*n2]}
	z2 := &Int{abs: z[2*n2:]}

	// 为 z1 分配临时存储；将 z0 改用作存放 tx。
	defer stk.restore(stk.save())
	z1 := &Int{abs: stk.nat(2*n2 + 1)}
	tx := &Int{abs: z[0:n2]}

	tx.Sub(x0, x1)
	z1.abs = z1.abs.sqr(stk, tx.abs)
	z1.neg = true

	clear(z)
	z0.abs = z0.abs.sqr(stk, x0.abs)
	z2.abs = z2.abs.sqr(stk, x1.abs)
	z1.Add(z1, z0)
	z1.Add(z1, z2)
	addTo(z[n2:], z1.abs)

	// 调试模式：复核答案，并在失败时打印跟踪信息。
	const debug = false
	if debug {
		zz := make(nat, len(z))
		basicSqr(stk, zz, x)
		if z.cmp(zz) != 0 {
			// 所有临时变量都曾被别名到 z，现已消失。重新计算。
			tx = new(Int).Sub(x0, x1)
			z0 = new(Int).Mul(x0, x0)
			z2 = new(Int).Mul(x1, x1)
			z1 = new(Int).Mul(tx, tx)
			z1.Neg(z1)
			z1.Add(z1, z0)
			z1.Add(z1, z2)
			print("karatsubaSqr wrong\n")
			trace("x ", &Int{abs: x})
			trace("z ", &Int{abs: z})
			trace("zz", &Int{abs: zz})
			trace("x0", x0)
			trace("x1", x1)
			trace("z0", z0)
			trace("z1", z1)
			trace("z2", z2)
			panic("karatsubaSqr")
		}
	}
}

// ifmt 返回 Int x 的调试格式：0xHEX。
func ifmt(x *Int) string {
	neg, s, t := "", x.Text(16), ""
	if s == "" { // 出现在非归一化的零的情况
		s = "0x0"
	}
	if s[0] == '-' {
		neg, s = "-", s[1:]
	}

	// 在字之间添加 _。
	const D = _W / 4 // 每个块的位数
	for len(s) > D {
		s, t = s[:len(s)-D], s[len(s)-D:]+"_"+t
	}
	return neg + s + t
}

// trace 打印单个调试值。
func trace(name string, x *Int) {
	print(name, "=", ifmt(x), "\n")
}
