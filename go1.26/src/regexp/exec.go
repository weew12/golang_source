// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regexp

import (
	"io"
	"regexp/syntax"
	"sync"
)

// queue 是一个持有待执行线程的"稀疏数组"。
// 参见 https://research.swtch.com/2008/03/using-uninitialized-memory-for-fun-and.html
type queue struct {
	sparse []uint32
	dense  []entry
}

// entry 是队列上的一个条目。
// 它同时持有指令计数器 pc 和实际线程。
// 一些队列条目只是占位符，以便机器
// 知道它已经考虑过该 pc。这些条目的 t == nil。
type entry struct {
	pc uint32
	t  *thread
}

// thread 是机器中单条路径的状态：
// 一个指令和一个对应的捕获数组。
// 参见 https://swtch.com/~rsc/regexp/regexp2.html
type thread struct {
	inst *syntax.Inst
	cap  []int
}

// machine 持有 p 的 NFA 模拟期间的所有状态。
type machine struct {
	re       *Regexp      // 对应的 Regexp
	p        *syntax.Prog // 编译后的程序
	q0, q1   queue        // runq 和 nextq 的两个队列
	pool     []*thread    // 可用线程池
	matched  bool         // 是否找到了匹配
	matchcap []int        // 匹配的捕获信息

	inputs inputs
}

type inputs struct {
	// 缓存的输入，以避免分配
	bytes  inputBytes
	string inputString
	reader inputReader
}

func (i *inputs) newBytes(b []byte) input {
	i.bytes.str = b
	return &i.bytes
}

func (i *inputs) newString(s string) input {
	i.string.str = s
	return &i.string
}

func (i *inputs) newReader(r io.RuneReader) input {
	i.reader.r = r
	i.reader.atEOT = false
	i.reader.pos = 0
	return &i.reader
}

func (i *inputs) clear() {
	// 我们需要清除其中 1 个。
	// 避免清除其他项的开销（指针写屏障）。
	if i.bytes.str != nil {
		i.bytes.str = nil
	} else if i.reader.r != nil {
		i.reader.r = nil
	} else {
		i.string.str = ""
	}
}

func (i *inputs) init(r io.RuneReader, b []byte, s string) (input, int) {
	if r != nil {
		return i.newReader(r), 0
	}
	if b != nil {
		return i.newBytes(b), len(b)
	}
	return i.newString(s), len(s)
}

func (m *machine) init(ncap int) {
	for _, t := range m.pool {
		t.cap = t.cap[:ncap]
	}
	m.matchcap = m.matchcap[:ncap]
}

// alloc 用给定的指令分配一个新线程。
// 如果可能，它使用空闲池。
func (m *machine) alloc(i *syntax.Inst) *thread {
	var t *thread
	if n := len(m.pool); n > 0 {
		t = m.pool[n-1]
		m.pool = m.pool[:n-1]
	} else {
		t = new(thread)
		t.cap = make([]int, len(m.matchcap), cap(m.matchcap))
	}
	t.inst = i
	return t
}

// lazyFlag 是一个延迟求值的 syntax.EmptyOp，
// 用于检查零宽度标志，如 ^ $ \A \z \B \b。
// 它记录相关的 rune 对，并且不会在
// 绝对必要之前确定隐含的标志
// （大多数情况下，这意味着永远不会）。
type lazyFlag uint64

func newLazyFlag(r1, r2 rune) lazyFlag {
	return lazyFlag(uint64(r1)<<32 | uint64(uint32(r2)))
}

func (f lazyFlag) match(op syntax.EmptyOp) bool {
	if op == 0 {
		return true
	}
	r1 := rune(f >> 32)
	if op&syntax.EmptyBeginLine != 0 {
		if r1 != '\n' && r1 >= 0 {
			return false
		}
		op &^= syntax.EmptyBeginLine
	}
	if op&syntax.EmptyBeginText != 0 {
		if r1 >= 0 {
			return false
		}
		op &^= syntax.EmptyBeginText
	}
	if op == 0 {
		return true
	}
	r2 := rune(f)
	if op&syntax.EmptyEndLine != 0 {
		if r2 != '\n' && r2 >= 0 {
			return false
		}
		op &^= syntax.EmptyEndLine
	}
	if op&syntax.EmptyEndText != 0 {
		if r2 >= 0 {
			return false
		}
		op &^= syntax.EmptyEndText
	}
	if op == 0 {
		return true
	}
	if syntax.IsWordChar(r1) != syntax.IsWordChar(r2) {
		op &^= syntax.EmptyWordBoundary
	} else {
		op &^= syntax.EmptyNoWordBoundary
	}
	return op == 0
}

// match 从 pos 开始在输入上运行机器。
// 它报告是否找到了匹配。
// 如果找到，m.matchcap 持有子匹配信息。
func (m *machine) match(i input, pos int) bool {
	startCond := m.re.cond
	if startCond == ^syntax.EmptyOp(0) { // 不可能满足
		return false
	}
	m.matched = false
	for i := range m.matchcap {
		m.matchcap[i] = -1
	}
	runq, nextq := &m.q0, &m.q1
	r, r1 := endOfText, endOfText
	width, width1 := 0, 0
	r, width = i.step(pos)
	if r != endOfText {
		r1, width1 = i.step(pos + width)
	}
	var flag lazyFlag
	if pos == 0 {
		flag = newLazyFlag(-1, r)
	} else {
		flag = i.context(pos)
	}
	for {
		if len(runq.dense) == 0 {
			if startCond&syntax.EmptyBeginText != 0 && pos != 0 {
				// 锚定匹配，已超过文本开头。
				break
			}
			if m.matched {
				// 已有匹配；完成了对替代方案的探索。
				break
			}
			if len(m.re.prefix) > 0 && r1 != m.re.prefixRune && i.canCheckPrefix() {
				// 匹配需要字面前缀；快速搜索它。
				advance := i.index(m.re, pos)
				if advance < 0 {
					break
				}
				pos += advance
				r, width = i.step(pos)
				r1, width1 = i.step(pos + width)
			}
		}
		if !m.matched {
			if len(m.matchcap) > 0 {
				m.matchcap[0] = pos
			}
			m.add(runq, uint32(m.p.Start), pos, m.matchcap, &flag, nil)
		}
		flag = newLazyFlag(r, r1)
		m.step(runq, nextq, pos, pos+width, r, &flag)
		if width == 0 {
			break
		}
		if len(m.matchcap) == 0 && m.matched {
			// 找到了匹配但不关心匹配的位置，
			// 所以任何匹配都可以。
			break
		}
		pos += width
		r, width = r1, width1
		if r != endOfText {
			r1, width1 = i.step(pos + width)
		}
		runq, nextq = nextq, runq
	}
	m.clear(nextq)
	return m.matched
}

// clear 释放线程队列上的所有线程。
func (m *machine) clear(q *queue) {
	for _, d := range q.dense {
		if d.t != nil {
			m.pool = append(m.pool, d.t)
		}
	}
	q.dense = q.dense[:0]
}

// step 执行机器的一步，运行 runq 上的每个线程
// 并将新线程追加到 nextq。
// 该步骤处理 rune c（可能是 endOfText），
// 它从位置 pos 开始，到 nextPos 结束。
// nextCond 给出 c 之后的零宽度标志设置。
func (m *machine) step(runq, nextq *queue, pos, nextPos int, c rune, nextCond *lazyFlag) {
	longest := m.re.longest
	for j := 0; j < len(runq.dense); j++ {
		d := &runq.dense[j]
		t := d.t
		if t == nil {
			continue
		}
		if longest && m.matched && len(t.cap) > 0 && m.matchcap[0] < t.cap[0] {
			m.pool = append(m.pool, t)
			continue
		}
		i := t.inst
		add := false
		switch i.Op {
		default:
			panic("bad inst")

		case syntax.InstMatch:
			if len(t.cap) > 0 && (!longest || !m.matched || m.matchcap[1] < pos) {
				t.cap[1] = pos
				copy(m.matchcap, t.cap)
			}
			if !longest {
				// 首次匹配模式：切断所有低优先级线程。
				for _, d := range runq.dense[j+1:] {
					if d.t != nil {
						m.pool = append(m.pool, d.t)
					}
				}
				runq.dense = runq.dense[:0]
			}
			m.matched = true

		case syntax.InstRune:
			add = i.MatchRune(c)
		case syntax.InstRune1:
			add = c == i.Rune[0]
		case syntax.InstRuneAny:
			add = true
		case syntax.InstRuneAnyNotNL:
			add = c != '\n'
		}
		if add {
			t = m.add(nextq, i.Out, nextPos, t.cap, nextCond, t)
		}
		if t != nil {
			m.pool = append(m.pool, t)
		}
	}
	runq.dense = runq.dense[:0]
}

// add 为 pc 向 q 添加一个条目，除非 q 已经有这样的条目。
// 它还递归地为从 pc 出发、通过满足 cond 的零宽度条件
// 可到达的所有指令添加条目。pos 给出输入中的当前位置。
func (m *machine) add(q *queue, pc uint32, pos int, cap []int, cond *lazyFlag, t *thread) *thread {
Again:
	if pc == 0 {
		return t
	}
	if j := q.sparse[pc]; j < uint32(len(q.dense)) && q.dense[j].pc == pc {
		return t
	}

	j := len(q.dense)
	q.dense = q.dense[:j+1]
	d := &q.dense[j]
	d.t = nil
	d.pc = pc
	q.sparse[pc] = uint32(j)

	i := &m.p.Inst[pc]
	switch i.Op {
	default:
		panic("unhandled")
	case syntax.InstFail:
		// nothing
	case syntax.InstAlt, syntax.InstAltMatch:
		t = m.add(q, i.Out, pos, cap, cond, t)
		pc = i.Arg
		goto Again
	case syntax.InstEmptyWidth:
		if cond.match(syntax.EmptyOp(i.Arg)) {
			pc = i.Out
			goto Again
		}
	case syntax.InstNop:
		pc = i.Out
		goto Again
	case syntax.InstCapture:
		if int(i.Arg) < len(cap) {
			opos := cap[i.Arg]
			cap[i.Arg] = pos
			m.add(q, i.Out, pos, cap, cond, nil)
			cap[i.Arg] = opos
		} else {
			pc = i.Out
			goto Again
		}
	case syntax.InstMatch, syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
		if t == nil {
			t = m.alloc(i)
		} else {
			t.inst = i
		}
		if len(cap) > 0 && &t.cap[0] != &cap[0] {
			copy(t.cap, cap)
		}
		d.t = t
		t = nil
	}
	return t
}

type onePassMachine struct {
	inputs   inputs
	matchcap []int
}

var onePassPool sync.Pool

func newOnePassMachine() *onePassMachine {
	m, ok := onePassPool.Get().(*onePassMachine)
	if !ok {
		m = new(onePassMachine)
	}
	return m
}

func freeOnePassMachine(m *onePassMachine) {
	m.inputs.clear()
	onePassPool.Put(m)
}

// doOnePass 使用单遍执行引擎实现 r.doExecute。
func (re *Regexp) doOnePass(ir io.RuneReader, ib []byte, is string, pos, ncap int, dstCap []int) []int {
	startCond := re.cond
	if startCond == ^syntax.EmptyOp(0) { // 不可能满足
		return nil
	}

	m := newOnePassMachine()
	if cap(m.matchcap) < ncap {
		m.matchcap = make([]int, ncap)
	} else {
		m.matchcap = m.matchcap[:ncap]
	}

	matched := false
	for i := range m.matchcap {
		m.matchcap[i] = -1
	}

	i, _ := m.inputs.init(ir, ib, is)

	r, r1 := endOfText, endOfText
	width, width1 := 0, 0
	r, width = i.step(pos)
	if r != endOfText {
		r1, width1 = i.step(pos + width)
	}
	var flag lazyFlag
	if pos == 0 {
		flag = newLazyFlag(-1, r)
	} else {
		flag = i.context(pos)
	}
	pc := re.onepass.Start
	inst := &re.onepass.Inst[pc]
	// 如果有简单的字面前缀，跳过它。
	if pos == 0 && flag.match(syntax.EmptyOp(inst.Arg)) &&
		len(re.prefix) > 0 && i.canCheckPrefix() {
		// 匹配需要字面前缀；快速搜索它。
		if !i.hasPrefix(re) {
			goto Return
		}
		pos += len(re.prefix)
		r, width = i.step(pos)
		r1, width1 = i.step(pos + width)
		flag = i.context(pos)
		pc = int(re.prefixEnd)
	}
	for {
		inst = &re.onepass.Inst[pc]
		pc = int(inst.Out)
		switch inst.Op {
		default:
			panic("bad inst")
		case syntax.InstMatch:
			matched = true
			if len(m.matchcap) > 0 {
				m.matchcap[0] = 0
				m.matchcap[1] = pos
			}
			goto Return
		case syntax.InstRune:
			if !inst.MatchRune(r) {
				goto Return
			}
		case syntax.InstRune1:
			if r != inst.Rune[0] {
				goto Return
			}
		case syntax.InstRuneAny:
			// Nothing
		case syntax.InstRuneAnyNotNL:
			if r == '\n' {
				goto Return
			}
		// 预览输入 rune 以确定 Alt 的哪个分支
		case syntax.InstAlt, syntax.InstAltMatch:
			pc = int(onePassNext(inst, r))
			continue
		case syntax.InstFail:
			goto Return
		case syntax.InstNop:
			continue
		case syntax.InstEmptyWidth:
			if !flag.match(syntax.EmptyOp(inst.Arg)) {
				goto Return
			}
			continue
		case syntax.InstCapture:
			if int(inst.Arg) < len(m.matchcap) {
				m.matchcap[inst.Arg] = pos
			}
			continue
		}
		if width == 0 {
			break
		}
		flag = newLazyFlag(r, r1)
		pos += width
		r, width = r1, width1
		if r != endOfText {
			r1, width1 = i.step(pos + width)
		}
	}

Return:
	if !matched {
		freeOnePassMachine(m)
		return nil
	}

	dstCap = append(dstCap, m.matchcap...)
	freeOnePassMachine(m)
	return dstCap
}

// doMatch 报告 r、b 或 s 是否匹配该正则表达式。
func (re *Regexp) doMatch(r io.RuneReader, b []byte, s string) bool {
	return re.doExecute(r, b, s, 0, 0, nil) != nil
}

// doExecute 在输入中找到最左匹配，将其子表达式的位置
// 追加到 dstCap 并返回 dstCap。
//
// 如果未找到匹配则返回 nil，如果找到匹配则返回非 nil。
func (re *Regexp) doExecute(r io.RuneReader, b []byte, s string, pos int, ncap int, dstCap []int) []int {
	if dstCap == nil {
		// 确保 'return dstCap' 是非 nil 的。
		dstCap = arrayNoInts[:0:0]
	}

	if r == nil && len(b)+len(s) < re.minInputLen {
		return nil
	}

	if re.onepass != nil {
		return re.doOnePass(r, b, s, pos, ncap, dstCap)
	}
	if r == nil && len(b)+len(s) < re.maxBitStateLen {
		return re.backtrack(b, s, pos, ncap, dstCap)
	}

	m := re.get()
	i, _ := m.inputs.init(r, b, s)

	m.init(ncap)
	if !m.match(i, pos) {
		re.put(m)
		return nil
	}

	dstCap = append(dstCap, m.matchcap...)
	re.put(m)
	return dstCap
}

// arrayNoInts 在传入 nil dstCap 且 ncap=0 时由 doExecute 匹配返回。
var arrayNoInts [0]int
