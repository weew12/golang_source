// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

// mulAddVWW 生成 mulAddVWW，它执行 z, c = x*m + a。
func mulAddVWW(a *Asm) {
	f := a.Func("func mulAddVWW(z, x []Word, m, a Word) (c Word)")

	if a.AltCarry().Valid() {
		addMulVirtualCarry(f, 0)
		return
	}
	addMul(f, "", "x", 0)
}

// addMulVVWW 生成 addMulVVWW，它执行 z, c = x + y*m + a。
// （更严谨的名字应是 addMulAddVVWW。）
func addMulVVWW(a *Asm) {
	f := a.Func("func addMulVVWW(z, x, y []Word, m, a Word) (c Word)")

	// 如果架构具有虚拟进位（virtual carry），则无条件生成该版本。
	if a.AltCarry().Valid() {
		addMulVirtualCarry(f, 1)
		return
	}

	// 如果架构可选地支持两个进位，则进行测试并同时生成两个版本。
	if a.JmpEnable(OptionAltCarry, "altcarry") {
		regs := a.RegsUsed()
		addMul(f, "x", "y", 1)
		a.Label("altcarry")
		a.SetOption(OptionAltCarry, true)
		a.SetRegsUsed(regs)
		addMulAlt(f)
		a.SetOption(OptionAltCarry, false)
		return
	}

	// 否则生成单进位的形式。
	addMul(f, "x", "y", 1)
}

// 计算 z = addsrc + m*mulsrc + a 时，我们需要：
//
//	for i := range z {
//		lo, hi := m * mulsrc[i]
//		lo, carry = bits.Add(lo, a, 0)
//		lo, carryAlt = bits.Add(lo, addsrc[i], 0)
//		z[i] = lo
//		a = hi + carry + carryAlt  // 不会溢出
//	}
//
// 最终加法不会溢出，因为在处理 N 个字之后，
// 最大可能的值是（对于 64 位系统）：
//
//	  (2**64N - 1) + (2**64 - 1)*(2**64N - 1) + (2**64 - 1)
//	= (2**64)*(2**64N - 1) + (2**64 - 1)
//	= 2**64(N+1) - 1,
//
// 它可容纳于 N+1 个字中（最高位那个是 a 的新值）。
//
// （例如，用 3 个十进制字，999 + 9*999 + 9 = 999*10 + 9 = 9999。）
//
// 如果我们把循环展开一点，就可以用两遍处理来串接进位。
// 考虑：
//
//	lo0, hi0 := m * mulsrc[i]
//	lo0, carry = bits.Add(lo0, a, 0)
//	lo0, carryAlt = bits.Add(lo0, addsrc[i], 0)
//	z[i] = lo0
//	a = hi + carry + carryAlt // 不会溢出
//
//	lo1, hi1 := m * mulsrc[i]
//	lo1, carry = bits.Add(lo1, a, 0)
//	lo1, carryAlt = bits.Add(lo1, addsrc[i], 0)
//	z[i] = lo1
//	a = hi + carry + carryAlt // 不会溢出
//
//	lo2, hi2 := m * mulsrc[i]
//	lo2, carry = bits.Add(lo2, a, 0)
//	lo2, carryAlt = bits.Add(lo2, addsrc[i], 0)
//	z[i] = lo2
//	a = hi + carry + carryAlt // 不会溢出
//
//	lo3, hi3 := m * mulsrc[i]
//	lo3, carry = bits.Add(lo3, a, 0)
//	lo3, carryAlt = bits.Add(lo3, addsrc[i], 0)
//	z[i] = lo3
//	a = hi + carry + carryAlt // 不会溢出
//
// 我们可以用三种方式优化这个序列。
//
// （1）通过重排序，我们可以串接进位，从而只用一个硬件进位标志，
// 但在多条指令间分摊保存和恢复它的开销：
//
//	// 乘法
//	lo0, hi0 := m * mulsrc[i]
//	lo1, hi1 := m * mulsrc[i+1]
//	lo2, hi2 := m * mulsrc[i+2]
//	lo3, hi3 := m * mulsrc[i+3]
//
//	lo0, carry = bits.Add(lo0, a, 0)
//	lo1, carry = bits.Add(lo1, hi0, carry)
//	lo2, carry = bits.Add(lo2, hi1, carry)
//	lo3, carry = bits.Add(lo3, hi2, carry)
//	a = hi3 + carry // 不会溢出
//
//	// 加法
//	lo0, carryAlt = bits.Add(lo0, addsrc[i], 0)
//	lo1, carryAlt = bits.Add(lo1, addsrc[i+1], carryAlt)
//	lo2, carryAlt = bits.Add(lo2, addsrc[i+2], carryAlt)
//	lo3, carryAlt = bits.Add(lo3, addrsc[i+3], carryAlt)
//	a = a + carryAlt // 不会溢出
//
//	z[i] = lo0
//	z[i+1] = lo1
//	z[i+2] = lo2
//	z[i+3] = lo3
//
// addMul 采用这种做法，先使用硬件进位标志表示 carry，
// 然后表示 carryAlt。
//
// （2）addMulAlt 假定有两个可用的硬件进位标志。
// 它分别用其中一个表示 carry 和 carryAlt，这样多块展开
// 就能在所有块中让这些标志保留在硬件里。
// 所以即便块大小为 1，代码也可以这样做：
//
//	// 乘法并相加
//	lo0, hi0 := m * mulsrc[i]
//	lo0, carry = bits.Add(lo0, a, 0)
//	lo0, carryAlt = bits.Add(lo0, addsrc[i], 0)
//	z[i] = lo0
//
//	lo1, hi1 := m * mulsrc[i+1]
//	lo1, carry = bits.Add(lo1, hi0, carry)
//	lo1, carryAlt = bits.Add(lo1, addsrc[i+1], carryAlt)
//	z[i+1] = lo1
//
//	lo2, hi2 := m * mulsrc[i+2]
//	lo2, carry = bits.Add(lo2, hi1, carry)
//	lo2, carryAlt = bits.Add(lo2, addsrc[i+2], carryAlt)
//	z[i+2] = lo2
//
//	lo3, hi3 := m * mulsrc[i+3]
//	lo3, carry = bits.Add(lo3, hi2, carry)
//	lo3, carryAlt = bits.Add(lo3, addrsc[i+3], carryAlt)
//	z[i+3] = lo2
//
//	a = hi3 + carry + carryAlt // 不会溢出
//
// （3）addMulVirtualCarry 针对显式计算进位位的系统
// （loong64、mips、riscv64）进行优化，将实际指令数几乎减半。
// 再来看原始的逐字（word-at-a-time）版本：
//
//	lo1, hi1 := m * mulsrc[i]
//	lo1, carry = bits.Add(lo1, a, 0)
//	lo1, carryAlt = bits.Add(lo1, addsrc[i], 0)
//	z[i] = lo1
//	a = hi + carry + carryAlt // 不会溢出
//
// 虽然它每个字用了四次加法，但这些都是廉价的加法：两次 bits.Add 加法
// 各用两条指令（ADD+SLTU），而最终的 + 加法每次只用一条 ADD，
// 合计每个字 6 条指令。相比之下，（2）中间那几段每个字
// 只用了两次“加法”，但这些是 SetCarry|UseCarry 加法，每次会编译成
// 五条指令，合计每个字 10 条指令。所以逐字（word-at-a-time）
// 循环实际上更好。而且我们可以稍微重排，只使用单个进位位：
//
//	lo1, hi1 := m * mulsrc[i]
//	lo1, carry = bits.Add(lo1, a, 0)
//	a = hi + carry
//	lo1, carry = bits.Add(lo1, addsrc[i], 0)
//	a = a + carry
//	z[i] = lo1
func addMul(f *Func, addsrc, mulsrc string, mulIndex int) {
	a := f.Asm
	mh := HintNone
	if a.Arch == Arch386 && addsrc != "" {
		mh = HintMemOK // 否则寄存器太少
	}
	m := f.ArgHint("m", mh)
	c := f.Arg("a")
	n := f.Arg("z_len")

	p := f.Pipe()
	if addsrc != "" {
		p.SetHint(addsrc, HintMemOK)
	}
	p.SetHint(mulsrc, HintMulSrc)
	unroll := []int{1, 4}
	switch a.Arch {
	case Arch386:
		unroll = []int{1} // 寄存器太少
	case ArchARM:
		p.SetMaxColumns(2) // 寄存器太少（但比 386 多）
	case ArchARM64:
		unroll = []int{1, 8} // 在 c4as16 上提速 5%
	}

	// 所生成代码的解释见上文的大段注释。
	// 这是优化策略 1。
	p.Start(n, unroll...)
	p.Loop(func(in, out [][]Reg) {
		a.Comment("multiply")
		prev := c
		flag := SetCarry
		for i, x := range in[mulIndex] {
			hi := a.RegHint(HintMulHi)
			a.MulWide(m, x, x, hi)
			a.Add(prev, x, x, flag)
			flag = UseCarry | SetCarry
			if prev != c {
				a.Free(prev)
			}
			out[0][i] = x
			prev = hi
		}
		a.Add(a.Imm(0), prev, c, UseCarry|SmashCarry)
		if addsrc != "" {
			a.Comment("add")
			flag := SetCarry
			for i, x := range in[0] {
				a.Add(x, out[0][i], out[0][i], flag)
				flag = UseCarry | SetCarry
			}
			a.Add(a.Imm(0), c, c, UseCarry|SmashCarry)
		}
		p.StoreN(out)
	})

	f.StoreArg(c, "c")
	a.Ret()
}

func addMulAlt(f *Func) {
	a := f.Asm
	m := f.ArgHint("m", HintMulSrc)
	c := f.Arg("a")
	n := f.Arg("z_len")

	// 在 amd64 上，AtUnrollEnd 的加法需要一个非立即数。
	r0 := a.ZR()
	if !r0.Valid() {
		r0 = a.Reg()
		a.Mov(a.Imm(0), r0)
	}

	p := f.Pipe()
	p.SetLabel("alt")
	p.SetHint("x", HintMemOK)
	p.SetHint("y", HintMemOK)
	if a.Arch == ArchAMD64 {
		p.SetMaxColumns(2)
	}

	// 所生成代码的解释见上文的大段注释。
	// 这是优化策略（2）。
	var hi Reg
	prev := c
	p.Start(n, 1, 8)
	p.AtUnrollStart(func() {
		a.Comment("multiply and add")
		a.ClearCarry(AddCarry | AltCarry)
		a.ClearCarry(AddCarry)
		hi = a.Reg()
	})
	p.AtUnrollEnd(func() {
		a.Add(r0, prev, c, UseCarry|SmashCarry)
		a.Add(r0, c, c, UseCarry|SmashCarry|AltCarry)
		prev = c
	})
	p.Loop(func(in, out [][]Reg) {
		for i, y := range in[1] {
			x := in[0][i]
			lo := y
			if lo.IsMem() {
				lo = a.Reg()
			}
			a.MulWide(m, y, lo, hi)
			a.Add(prev, lo, lo, UseCarry|SetCarry)
			a.Add(x, lo, lo, UseCarry|SetCarry|AltCarry)
			out[0][i] = lo
			prev, hi = hi, prev
		}
		p.StoreN(out)
	})

	f.StoreArg(c, "c")
	a.Ret()
}

func addMulVirtualCarry(f *Func, mulIndex int) {
	a := f.Asm
	m := f.Arg("m")
	c := f.Arg("a")
	n := f.Arg("z_len")

	// 所生成代码的解释见上文的大段注释。
	// 这是优化策略（3）。
	p := f.Pipe()
	p.Start(n, 1, 4)
	p.Loop(func(in, out [][]Reg) {
		a.Comment("synthetic carry, one column at a time")
		lo, hi := a.Reg(), a.Reg()
		for i, x := range in[mulIndex] {
			a.MulWide(m, x, lo, hi)
			if mulIndex == 1 {
				a.Add(in[0][i], lo, lo, SetCarry)
				a.Add(a.Imm(0), hi, hi, UseCarry|SmashCarry)
			}
			a.Add(c, lo, x, SetCarry)
			a.Add(a.Imm(0), hi, c, UseCarry|SmashCarry)
			out[0][i] = x
		}
		p.StoreN(out)
	})
	f.StoreArg(c, "c")
	a.Ret()
}
