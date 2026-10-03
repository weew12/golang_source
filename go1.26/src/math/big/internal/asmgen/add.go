// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

// addOrSubVV 生成 addVV 或 subVV，
// 它们执行 z, c = x ± y。
// 调用者保证 len(z) == len(x) == len(y)。
func addOrSubVV(a *Asm, name string) {
	f := a.Func("func " + name + "(z, x, y []Word) (c Word)")

	add := a.Add
	which := AddCarry
	if name == "subVV" {
		add = a.Sub
		which = SubCarry
	}

	n := f.Arg("z_len")
	p := f.Pipe()
	p.SetHint("y", HintMemOK) // 允许在 x86 上从内存使用 y
	p.Start(n, 1, 4)
	var c Reg
	if !a.Arch.CarrySafeLoop {
		// 进位被循环测试破坏；分配一个寄存器并在展开块前后
		// 保存它。
		c = a.Reg()
		a.Mov(a.Imm(0), c)
		a.EOL("clear saved carry")
		p.AtUnrollStart(func() { a.RestoreCarry(c); a.Free(c) })
		p.AtUnrollEnd(func() { a.Unfree(c); a.SaveCarry(c) })
	} else {
		// 进位由循环保留；现在在循环之前清除
		// （但在 Start 之后，Start 可能已修改了它）。
		a.ClearCarry(which)
	}
	p.Loop(func(in, out [][]Reg) {
		for i, x := range in[0] {
			y := in[1][i]
			add(y, x, x, SetCarry|UseCarry)
		}
		p.StoreN(in[:1])
	})
	p.Done()

	// 将进位复制到输出。
	if c.Valid() {
		a.ConvertCarry(which, c)
	} else {
		c = a.RegHint(HintCarry)
		a.SaveConvertCarry(which, c)
	}
	f.StoreArg(c, "c")
	a.Free(c)
	a.Ret()
}
