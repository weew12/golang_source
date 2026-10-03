// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

// shiftVU 生成 lshVU 和 rshVU，它们分别执行
// z, c = x << s 和 z, c = x >> s，其中 0 < s < _W。
func shiftVU(a *Asm, name string) {
	// 由于这些例程可能在 z.Lsh(z, N) 和 z.Rsh(z, N) 中被调用，
	// 输入和输出切片可能以不同偏移量别名重叠。
	// 例如（在 64 位系统上），在 z.Lsh(z, 65) 期间，&z[0] == &x[1]，
	// 而在 z.Rsh(z, 65) 期间，&z[1] == &x[0]。
	// 对于左移，我们必须从 len(z)-1 向下处理到 0，
	// 这样就不会在需要读取某个字之前将其覆盖。
	// 对于右移，我们必须从 0 向上处理到 len(z)-1。
	// 不同的遍历顺序至少让这两种情况更加一致，
	// 因为相对于输入，我们总是将输出延迟一个字。

	f := a.Func("func " + name + "(z, x []Word, s uint) (c Word)")

	// 尽早检查是否没有输入，因为我们需要先读取 1 个字开始。
	n := f.Arg("z_len")
	a.JmpZero(n, "ret0")

	// 通过读取第一个输入字来开始循环。
	s := f.ArgHint("s", HintShiftCount)
	p := f.Pipe()
	if name == "lshVU" {
		p.SetBackward()
	}
	unroll := []int{1, 4}
	if a.Arch == Arch386 {
		unroll = []int{1} // 寄存器太少，无法更多展开
		p.SetUseIndexCounter()
	}
	p.LoadPtrs(n)
	a.Comment("shift first word into carry")
	prev := p.LoadN(1)[0][0]

	// 决定如何进行移位。在具有宽移位（wide shift）的系统（x86）上，使用宽移位。
	// 否则，我们需要按 s 移位，并按 64-s 或 32-s 做反向（负）移位。
	shift := a.Lsh
	shiftWide := a.LshWide
	negShift := a.Rsh
	negShiftReg := a.RshReg
	if name == "rshVU" {
		shift = a.Rsh
		shiftWide = a.RshWide
		negShift = a.Lsh
		negShiftReg = a.LshReg
	}
	if a.Arch.HasShiftWide() {
		// 使用宽移位以避免需要负移位。
		// 不变式是：prev 保存前一个字（完全未移位），
		// 用作宽移位的输入。
		// 循环结束后，prev 保存要写入的最终输出字。
		c := a.Reg()
		shiftWide(s, prev, a.Imm(0), c)
		f.StoreArg(c, "c")
		a.Free(c)
		a.Comment("shift remaining words")
		p.Start(n, unroll...)
		p.Loop(func(in [][]Reg, out [][]Reg) {
			// 我们将输入寄存器复用为输出，延迟一个周期；prev 是第一个输出。
			// 在将输出写入内存后，我们可以把最终的 x 值复制到 prev
			// 以供下一次迭代使用。
			old := prev
			for i, x := range in[0] {
				shiftWide(s, x, old, old)
				out[0][i] = old
				old = x
			}
			p.StoreN(out)
			a.Mov(old, prev)
		})
		a.Comment("store final shifted bits")
		shift(s, prev, prev)
	} else {
		// 由 x << s 和 x >> (64-s) 构造出各个值。
		// 第一个字处理完毕后，不变式是：
		// prev 保存 x << s，用作下一个输出字的高位，
		// 待我们读取下一个输入字后求出低位即可。
		// 循环结束后，prev 保存要写入的最终输出字。
		sNeg := a.Reg()
		a.Mov(a.Imm(a.Arch.WordBits), sNeg)
		a.Sub(s, sNeg, sNeg, SmashCarry)
		c := a.Reg()
		negShift(sNeg, prev, c)
		shift(s, prev, prev)
		f.StoreArg(c, "c")
		a.Free(c)
		a.Comment("shift remaining words")
		p.Start(n, unroll...)
		p.Loop(func(in, out [][]Reg) {
			if a.HasRegShift() {
				// ARM（32 位）允许在大多数算术表达式中使用移位，
				// 包括 OR，这让我们可以合并 negShift 和 a.Or。
				// 管理寄存器的最简单方式是一次为一个输出执行 StoreN，
				// 而由于我们在 ARM 上不使用多寄存器存储，这并不会带来损失。
				out[0] = out[0][:1]
				for _, x := range in[0] {
					a.Or(negShiftReg(sNeg, x), prev, prev)
					out[0][0] = prev
					p.StoreN(out)
					shift(s, x, prev)
				}
				return
			}
			// 我们将输入寄存器复用为输出，延迟一个周期；z0 是第一个输出。
			z0 := a.Reg()
			z := z0
			for i, x := range in[0] {
				negShift(sNeg, x, z)
				a.Or(prev, z, z)
				shift(s, x, prev)
				out[0][i] = z
				z = x
			}
			p.StoreN(out)
		})
		a.Comment("store final shifted bits")
	}
	p.StoreN([][]Reg{{prev}})
	p.Done()
	a.Free(s)
	a.Ret()

	// 返回 0，由上文使用。
	a.Label("ret0")
	f.StoreArg(a.Imm(0), "c")
	a.Ret()
}
