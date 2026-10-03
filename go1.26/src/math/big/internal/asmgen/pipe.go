// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

import (
	"fmt"
	"math/bits"
	"slices"
)

// 注意：导出的字段和方法预期由函数生成器
// （如 add.go 等文件中的那些）使用。
// 未导出的字段和方法则不应被使用。

// Pipe 管理函数内存操作的输入和输出数据流水线。
//
// 输入是一个或多个等长的字切片，因此整体上
// 可以把它看作一个矩阵，其中每个切片是一行，每一列
// 是来自不同切片的一组对应字。
// 输出可以同样看待，尽管它通常只有一行。
type Pipe struct {
	f               *Func    // 正在生成的函数
	label           string   // 循环标签的前缀（默认 "loop"）
	backward        bool     // 以相反顺序处理列
	started         bool     // 已调用 Start
	loaded          bool     // 已调用 LoadPtrs
	inPtr           []RegPtr // 输入切片指针
	hints           []Hint   // 对每个 inPtr，其数据要使用的寄存器提示
	outPtr          []RegPtr // 输出切片指针
	index           Reg      // 索引寄存器，如果正在使用
	useIndexCounter bool     // 请求使用索引计数器
	indexCounter    int      // 索引同时作为计数器（386）；0 否，-1 负计数器，+1 正计数器
	readOff         int      // 尚未加到索引上的读偏移量
	writeOff        int      // 尚未加到索引上的写偏移量
	factors         []int    // 展开因子
	counts          []Reg    // 每个因子的迭代次数
	needWrite       bool     // 在 Loop1/LoopN 期间需要一次写调用
	maxColumns      int      // 展开循环期间的最大列数
	unrollStart     func()   // 在展开体开始时发出代码
	unrollEnd       func()   // 在展开体结束时发出代码
}

// Pipe 创建并返回一个供函数 f 使用的新 pipe。
func (f *Func) Pipe() *Pipe {
	a := f.Asm
	p := &Pipe{
		f:          f,
		label:      "loop",
		maxColumns: 10000000,
	}
	if m := a.Arch.maxColumns; m != 0 {
		p.maxColumns = m
	}
	return p
}

// SetBackward 设置 pipe 以相反顺序处理输入和输出列。
// 左移需要这样做，否则可能会覆盖它们稍后要读取的数据。
func (p *Pipe) SetBackward() {
	if p.loaded {
		p.f.Asm.Fatalf("SetBackward after Start/LoadPtrs")
	}
	p.backward = true
}

// SetUseIndexCounter 使 pipe 在可能的情况下使用索引计数器，
// 这意味着循环计数器同时用作访问切片数据的索引。
// 这个巧妙的技巧在现代处理器上较慢，但在 386 上仍然是必需的。
// 在非 386 系统上，SetUseIndexCounter 是空操作。
func (p *Pipe) SetUseIndexCounter() {
	if p.f.Asm.Arch.memIndex == nil { // 需要 memIndex（只有 386 提供它）
		return
	}
	p.useIndexCounter = true
}

// SetLabel 设置 pipe 所发出循环的标签前缀。
// 默认前缀是 "loop"。
func (p *Pipe) SetLabel(label string) {
	p.label = label
}

// SetMaxColumns 设置在单次循环体调用中
// 处理的最大列数。
func (p *Pipe) SetMaxColumns(m int) {
	p.maxColumns = m
}

// SetHint 记录来自命名向量的输入
// 应使用给定的寄存器提示进行分配。
//
// 如果该提示在目标架构上指示单个寄存器，
// 那么 SetHint 会调用 SetMaxColumns(1)，因为被提示的寄存器
// 一次只能用于一个值。
func (p *Pipe) SetHint(name string, hint Hint) {
	if hint == HintMemOK && !p.f.Asm.Arch.memOK {
		return
	}
	i := slices.Index(p.f.inputs, name)
	if i < 0 {
		p.f.Asm.Fatalf("unknown input name %s", name)
	}
	if p.f.Asm.hint(hint) != "" {
		p.SetMaxColumns(1)
	}
	for len(p.hints) <= i {
		p.hints = append(p.hints, HintNone)
	}
	p.hints[i] = hint
}

// LoadPtrs 将切片指针参数加载到寄存器中，
// 假定切片长度 n 已经加载到
// 寄存器 n 中。
//
// 如果尚未调用，Start 会调用 LoadPtrs。
// 只有当代码需要在 Start 之前使用 LoadN 时，
// 才需要显式调用 LoadPtrs，比如 shift.go 的生成器
// 在循环之前读取初始字时。
func (p *Pipe) LoadPtrs(n Reg) {
	a := p.f.Asm
	if p.loaded {
		a.Fatalf("pointers already loaded")
	}

	// 加载实际的指针。
	p.loaded = true
	for _, name := range p.f.inputs {
		p.inPtr = append(p.inPtr, RegPtr(p.f.Arg(name+"_base")))
	}
	for _, name := range p.f.outputs {
		p.outPtr = append(p.outPtr, RegPtr(p.f.Arg(name+"_base")))
	}

	// 决定 LoadN 和 StoreN 的内存访问策略。
	switch {
	case p.backward && p.useIndexCounter:
		// 生成器想要一个索引计数器，这意味着当迭代计数器
		// 为 AX 时，我们将使用 (BX)(AX*WordBytes) 通过指针 BX 访问切片。
		// 循环在切片中向后移动，但计数器
		// 也在向后移动，所以没什么要做的。
		a.Comment("run loop backward, using counter as positive index")
		p.indexCounter = +1
		p.index = n

	case !p.backward && p.useIndexCounter:
		// 生成器想要一个索引计数器，但循环在向前移动。
		// 为了使计数器沿数据访问的方向移动，
		// 我们取反计数器，从 -len(z) 向上计数到 -1。
		// 为了使索引访问到正确的字，我们将 len(z)*WordBytes
		// 加到每个指针上。
		// 关于指向切片边界之外对垃圾回收器的（非）影响，
		// 见下面的注释。
		a.Comment("use counter as negative index")
		p.indexCounter = -1
		p.index = n
		for _, ptr := range p.inPtr {
			a.AddWords(n, ptr, ptr)
		}
		for _, ptr := range p.outPtr {
			a.AddWords(n, ptr, ptr)
		}
		a.Neg(n, n)

	case p.backward:
		// 生成器想要向后运行循环。
		// 我们会在使用指针之前递减它们，
		// 因此将它们定位在切片的最末端。
		// 如果我们有用于汇编的精确指针信息，
		// 这些指针会给垃圾回收器带来问题，
		// 因为它们不再指向已分配的切片，
		// 但垃圾回收器会忽略汇编栈中出乎意料的值，
		// 而实际的切片指针仍然在参数栈槽中，
		// 所以切片不会被提前回收。
		// 如果我们改用寄存器 ABI，可能就得重新考虑这一点。
		// （在向前循环结束时也会发生同样的事，
		// 但那没那么重要，因为在向前循环中一旦指针越出切片，
		// 循环就结束了，切片也不会再被访问。）
		a.Comment("run loop backward")
		for _, ptr := range p.inPtr {
			a.AddWords(n, ptr, ptr)
		}
		for _, ptr := range p.outPtr {
			a.AddWords(n, ptr, ptr)
		}

	case !p.backward:
		// 无需做任何事！
	}
}

// LoadN 以行的切片形式返回接下来的 n 列输入字。
// 使用 p.SetMemOK 标记过的输入的 Reg 将是直接内存引用。
// 其他输入的 Reg 将是新分配的寄存器，必须释放。
func (p *Pipe) LoadN(n int) [][]Reg {
	a := p.f.Asm
	regs := make([][]Reg, len(p.inPtr))
	for i, ptr := range p.inPtr {
		regs[i] = make([]Reg, n)
		switch {
		case a.Arch.loadIncN != nil:
			// 从内存加载并同时推进指针。
			for j := range regs[i] {
				regs[i][j] = p.f.Asm.Reg()
			}
			if p.backward {
				a.Arch.loadDecN(a, ptr, regs[i])
			} else {
				a.Arch.loadIncN(a, ptr, regs[i])
			}

		default:
			// 使用偏移量从内存加载。
			// 我们稍后会推进指针或索引计数器。
			for j := range n {
				off := p.readOff + j
				if p.backward {
					off = -(off + 1)
				}
				var mem Reg
				if p.indexCounter != 0 {
					mem = a.Arch.memIndex(a, off*a.Arch.WordBytes, p.index, ptr)
				} else {
					mem = ptr.mem(off * a.Arch.WordBytes)
				}
				h := HintNone
				if i < len(p.hints) {
					h = p.hints[i]
				}
				if h == HintMemOK {
					regs[i][j] = mem
				} else {
					r := p.f.Asm.RegHint(h)
					a.Mov(mem, r)
					regs[i][j] = r
				}
			}
		}
	}
	p.readOff += n
	return regs
}

// StoreN 将 regs（行的切片）写入接下来的 n 列输出，其中 n = len(regs[0])。
func (p *Pipe) StoreN(regs [][]Reg) {
	p.needWrite = false
	a := p.f.Asm
	if len(regs) != len(p.outPtr) {
		p.f.Asm.Fatalf("wrong number of output rows")
	}
	n := len(regs[0])
	for i, ptr := range p.outPtr {
		switch {
		case a.Arch.storeIncN != nil:
			// 存储到内存并同时推进指针。
			if p.backward {
				a.Arch.storeDecN(a, ptr, regs[i])
			} else {
				a.Arch.storeIncN(a, ptr, regs[i])
			}

		default:
			// 使用偏移量存储到内存。
			// 我们稍后会推进指针或索引计数器。
			for j, r := range regs[i] {
				off := p.writeOff + j
				if p.backward {
					off = -(off + 1)
				}
				var mem Reg
				if p.indexCounter != 0 {
					mem = a.Arch.memIndex(a, off*a.Arch.WordBytes, p.index, ptr)
				} else {
					mem = ptr.mem(off * a.Arch.WordBytes)
				}
				a.Mov(r, mem)
			}
		}
	}
	p.writeOff += n
}

// advancePtrs 将指针推进 step，
// 或者为调用者即将执行的索引推进 step
// 处理簿记工作。
func (p *Pipe) advancePtrs(step int) {
	a := p.f.Asm
	switch {
	case a.Arch.loadIncN != nil:
		// 无需做任何事

	default:
		// 为指针推进（或即将进行的索引推进）调整读/写偏移量。
		p.readOff -= step
		p.writeOff -= step

		if p.indexCounter == 0 {
			// 推进指针。
			if p.backward {
				step = -step
			}
			for _, ptr := range p.inPtr {
				a.Add(a.Imm(step*a.Arch.WordBytes), Reg(ptr), Reg(ptr), KeepCarry)
			}
			for _, ptr := range p.outPtr {
				a.Add(a.Imm(step*a.Arch.WordBytes), Reg(ptr), Reg(ptr), KeepCarry)
			}
		}
	}
}

// DropInput 从 pipe 中删除命名输入，
// 通常是因为它已被耗尽。
// （这尚未使用，但会在未来的生成器中使用。）
func (p *Pipe) DropInput(name string) {
	i := slices.Index(p.f.inputs, name)
	if i < 0 {
		p.f.Asm.Fatalf("unknown input %s", name)
	}
	ptr := p.inPtr[i]
	p.f.Asm.Free(Reg(ptr))
	p.inPtr = slices.Delete(p.inPtr, i, i+1)
	p.f.inputs = slices.Delete(p.f.inputs, i, i+1)
	if len(p.hints) > i {
		p.hints = slices.Delete(p.hints, i, i+1)
	}
}

// Start 准备对 n 列进行循环。
// factors 给出要使用的一系列展开因子，
// 它必须严格递增或严格递减，
// 并且必须包含 1。
// 例如，4, 1 表示一次处理 4 个元素，
// 然后对最后 0-3 个一次处理 1 个；改为指定 1,4
// 则先处理 0-3 个元素，然后一次处理 4 个。
// 类似地，32, 4, 1 表示一次处理 32 个，
// 然后一次 4 个，然后一次 1 个。
//
// 使用 1, 4 而非 4, 1 的一个好处是：一次处理 4 个的
// 主体需要更多寄存器，而如果它是最后一个主体，
// 保存片段计数（0-3）的寄存器已被释放，可供使用。
//
// Start 可能修改进位标志。
//
// Start 之后必须调用 Loop1 或 LoopN，
// 但允许先发出其他指令，
// 例如设置初始进位标志。
func (p *Pipe) Start(n Reg, factors ...int) {
	a := p.f.Asm
	if p.started {
		a.Fatalf("loop already started")
	}
	if p.useIndexCounter && len(factors) > 1 {
		a.Fatalf("cannot call SetUseIndexCounter and then use Start with factors != [1]; have factors = %v", factors)
	}
	p.started = true
	if !p.loaded {
		if len(factors) == 1 {
			p.SetUseIndexCounter()
		}
		p.LoadPtrs(n)
	}

	// 如果在 LoadPtrs 和 Start 之间调用了 LoadN，
	// 则调整循环使其不扫描那些列，假定
	// 代码已经调用了等效的 StoreN，或者
	// 将在循环之后调用。
	if off := p.readOff; off != 0 {
		if p.indexCounter < 0 {
			// 索引被取反了，所以加 off 而不是减。
			a.Add(a.Imm(off), n, n, SmashCarry)
		} else {
			a.Sub(a.Imm(off), n, n, SmashCarry)
		}
		if p.indexCounter != 0 {
			// n 也是我们正在使用的索引，所以调整 readOff 和 writeOff，
			// 使其继续指向我们改变 n 之前相同的位置。
			p.readOff -= off
			p.writeOff -= off
		}
	}

	p.Restart(n, factors...)
}

// Restart 准备在 p.Start/p.Loop 先前运行的循环之外，
// 再对额外的 n 列进行循环。
func (p *Pipe) Restart(n Reg, factors ...int) {
	a := p.f.Asm
	if !p.started {
		a.Fatalf("pipe not started")
	}
	p.factors = factors
	p.counts = make([]Reg, len(factors))
	if len(factors) == 0 {
		factors = []int{1}
	}

	// 将每个展开段的循环长度计算到单独的寄存器中。
	// 我们提前把它们全部计算好，以防该计算会破坏
	// 循环体需要保留的进位标志。
	if len(factors) > 1 {
		a.Comment("compute unrolled loop lengths")
	}
	switch {
	default:
		a.Fatalf("invalid factors %v", factors)

	case factors[0] == 1:
		// 递增的循环因子
		div := 1
		for i, f := range factors[1:] {
			if f <= factors[i] {
				a.Fatalf("non-increasing factors %v", factors)
			}
			if f&(f-1) != 0 {
				a.Fatalf("non-power-of-two factors %v", factors)
			}
			t := p.f.Asm.Reg()
			f /= div
			a.And(a.Imm(f-1), n, t)
			a.Rsh(a.Imm(bits.TrailingZeros(uint(f))), n, n)
			div *= f
			p.counts[i] = t
		}
		p.counts[len(p.counts)-1] = n

	case factors[len(factors)-1] == 1:
		// 递减的循环因子
		for i, f := range factors[:len(factors)-1] {
			if f <= factors[i+1] {
				a.Fatalf("non-decreasing factors %v", factors)
			}
			if f&(f-1) != 0 {
				a.Fatalf("non-power-of-two factors %v", factors)
			}
			t := p.f.Asm.Reg()
			a.Rsh(a.Imm(bits.TrailingZeros(uint(f))), n, t)
			a.And(a.Imm(f-1), n, n)
			p.counts[i] = t
		}
		p.counts[len(p.counts)-1] = n
	}
}

// Done 释放 pipe 分配的所有寄存器。
func (p *Pipe) Done() {
	for _, ptr := range p.inPtr {
		p.f.Asm.Free(Reg(ptr))
	}
	p.inPtr = nil
	for _, ptr := range p.outPtr {
		p.f.Asm.Free(Reg(ptr))
	}
	p.outPtr = nil
	p.index = Reg{}
}

// Loop 发出循环代码，反复调用 block 以发出处理
// N 个输入列（N 为任意的 N = len(in[0])，由 p 选择）的代码。
// block 必须调用 p.StoreN(out) 来写入 N 个输出列。
// out 切片是一个预分配的、未初始化 Reg 值的矩阵。
// 预期 block 在调用 p.StoreN(out) 之前将每个条目
// 设置为应写入的 Reg。
//
// 例如，如果循环要以每块 2 列的方式展开 4 次，
// 发出展开循环体的调用序列是：
//
//	start()  // 由 pAtUnrollStart 设置
//	... 2 列的读取 ...
//	block()
//	... 2 列的写入 ...
//	... 2 列的读取 ...
//	block()
//	... 2 列的写入 ...
//	end()  // 由 p.AtUnrollEnd 设置
//
// block 期间分配的任何寄存器在 block 返回时自动释放。
func (p *Pipe) Loop(block func(in, out [][]Reg)) {
	if p.factors == nil {
		p.f.Asm.Fatalf("Pipe.Start not called")
	}
	for i, factor := range p.factors {
		n := p.counts[i]
		p.unroll(n, factor, block)
		if i < len(p.factors)-1 {
			p.f.Asm.Free(n)
		}
	}
	p.factors = nil
}

// AtUnrollStart 设置在展开序列开始时调用的函数。
// 详情见 [Pipe.Loop]。
func (p *Pipe) AtUnrollStart(start func()) {
	p.unrollStart = start
}

// AtUnrollEnd 设置在展开序列结束时调用的函数。
// 详情见 [Pipe.Loop]。
func (p *Pipe) AtUnrollEnd(end func()) {
	p.unrollEnd = end
}

// unroll 为给定因子发出单个展开循环，迭代 n 次。
func (p *Pipe) unroll(n Reg, factor int, block func(in, out [][]Reg)) {
	a := p.f.Asm
	label := fmt.Sprintf("%s%d", p.label, factor)

	// 循环控制流的顶部。
	a.Label(label)
	if a.Arch.loopTop != "" {
		a.Printf("\t"+a.Arch.loopTop+"\n", n, label+"done")
	} else {
		a.JmpZero(n, label+"done")
	}
	a.Label(label + "cont")

	// 展开的循环体。
	if factor < p.maxColumns {
		a.Comment("unroll %dX", factor)
	} else {
		a.Comment("unroll %dX in batches of %d", factor, p.maxColumns)
	}
	if p.unrollStart != nil {
		p.unrollStart()
	}
	for done := 0; done < factor; {
		batch := min(factor-done, p.maxColumns)
		regs := a.RegsUsed()
		out := make([][]Reg, len(p.outPtr))
		for i := range out {
			out[i] = make([]Reg, batch)
		}
		in := p.LoadN(batch)
		p.needWrite = true
		block(in, out)
		if p.needWrite && len(p.outPtr) > 0 {
			a.Fatalf("missing p.Write1 or p.StoreN")
		}
		a.SetRegsUsed(regs) // 释放 block 分配的任何东西
		done += batch
	}
	if p.unrollEnd != nil {
		p.unrollEnd()
	}
	p.advancePtrs(factor)

	// 循环控制流的底部。
	switch {
	case p.indexCounter >= 0 && a.Arch.loopBottom != "":
		a.Printf("\t"+a.Arch.loopBottom+"\n", n, label+"cont")

	case p.indexCounter >= 0:
		a.Sub(a.Imm(1), n, n, KeepCarry)
		a.JmpNonZero(n, label+"cont")

	case p.indexCounter < 0 && a.Arch.loopBottomNeg != "":
		a.Printf("\t"+a.Arch.loopBottomNeg+"\n", n, label+"cont")

	case p.indexCounter < 0:
		a.Add(a.Imm(1), n, n, KeepCarry)
	}
	a.Label(label + "done")
}
