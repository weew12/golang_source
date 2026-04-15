// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syntax

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 编译后的程序。
// 可能不属于此包，但目前放在这里很方便。

// Prog 是一个编译后的正则表达式程序。
type Prog struct {
	Inst   []Inst
	Start  int // 起始指令的索引
	NumCap int // re 中 InstCapture 指令的数量
}

// InstOp 是指令操作码。
type InstOp uint8

const (
	InstAlt InstOp = iota
	InstAltMatch
	InstCapture
	InstEmptyWidth
	InstMatch
	InstFail
	InstNop
	InstRune
	InstRune1
	InstRuneAny
	InstRuneAnyNotNL
)

var instOpNames = []string{
	"InstAlt",
	"InstAltMatch",
	"InstCapture",
	"InstEmptyWidth",
	"InstMatch",
	"InstFail",
	"InstNop",
	"InstRune",
	"InstRune1",
	"InstRuneAny",
	"InstRuneAnyNotNL",
}

func (i InstOp) String() string {
	if uint(i) >= uint(len(instOpNames)) {
		return ""
	}
	return instOpNames[i]
}

// EmptyOp 指定一种或多种零宽度断言。
type EmptyOp uint8

const (
	EmptyBeginLine EmptyOp = 1 << iota
	EmptyEndLine
	EmptyBeginText
	EmptyEndText
	EmptyWordBoundary
	EmptyNoWordBoundary
)

// EmptyOpContext 返回在 rune r1 和 r2 之间的位置
// 满足的零宽度断言。
// 传入 r1 == -1 表示该位置在文本开头。
// 传入 r2 == -1 表示该位置在文本结尾。
func EmptyOpContext(r1, r2 rune) EmptyOp {
	var op EmptyOp = EmptyNoWordBoundary
	var boundary byte
	switch {
	case IsWordChar(r1):
		boundary = 1
	case r1 == '\n':
		op |= EmptyBeginLine
	case r1 < 0:
		op |= EmptyBeginText | EmptyBeginLine
	}
	switch {
	case IsWordChar(r2):
		boundary ^= 1
	case r2 == '\n':
		op |= EmptyEndLine
	case r2 < 0:
		op |= EmptyEndText | EmptyEndLine
	}
	if boundary != 0 { // IsWordChar(r1) != IsWordChar(r2)
		op ^= (EmptyWordBoundary | EmptyNoWordBoundary)
	}
	return op
}

// IsWordChar 报告 r 是否被视为"单词字符"，
// 用于 \b 和 \B 零宽度断言的求值过程中。
// 这些断言仅限 ASCII：单词字符为 [A-Za-z0-9_]。
func IsWordChar(r rune) bool {
	// 优先测试小写字母，因为在常见情况下
	// 小写字母出现的频率高于大写字母。
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '_'
}

// Inst 是正则表达式程序中的单条指令。
type Inst struct {
	Op   InstOp
	Out  uint32 // 除 InstMatch、InstFail 外的所有指令
	Arg  uint32 // InstAlt、InstAltMatch、InstCapture、InstEmptyWidth
	Rune []rune
}

func (p *Prog) String() string {
	var b strings.Builder
	dumpProg(&b, p)
	return b.String()
}

// skipNop 跟随任何空操作或捕获指令。
func (p *Prog) skipNop(pc uint32) *Inst {
	i := &p.Inst[pc]
	for i.Op == InstNop || i.Op == InstCapture {
		i = &p.Inst[i.Out]
	}
	return i
}

// op 返回 i.Op，但将所有 Rune 特殊情况合并为 InstRune
func (i *Inst) op() InstOp {
	op := i.Op
	switch op {
	case InstRune1, InstRuneAny, InstRuneAnyNotNL:
		op = InstRune
	}
	return op
}

// Prefix 返回一个字面字符串，正则表达式的所有匹配
// 都必须以此开头。如果前缀就是整个匹配，则 Complete 为 true。
func (p *Prog) Prefix() (prefix string, complete bool) {
	i := p.skipNop(uint32(p.Start))

	// 如果前缀为空，避免分配缓冲区。
	if i.op() != InstRune || len(i.Rune) != 1 {
		return "", i.Op == InstMatch
	}

	// 有前缀；收集字符。
	var buf strings.Builder
	for i.op() == InstRune && len(i.Rune) == 1 && Flags(i.Arg)&FoldCase == 0 && i.Rune[0] != utf8.RuneError {
		buf.WriteRune(i.Rune[0])
		i = p.skipNop(i.Out)
	}
	return buf.String(), i.Op == InstMatch
}

// StartCond 返回在任何匹配中必须为真的前导零宽度条件。
// 如果不可能有匹配，则返回 ^EmptyOp(0)。
func (p *Prog) StartCond() EmptyOp {
	var flag EmptyOp
	pc := uint32(p.Start)
	i := &p.Inst[pc]
Loop:
	for {
		switch i.Op {
		case InstEmptyWidth:
			flag |= EmptyOp(i.Arg)
		case InstFail:
			return ^EmptyOp(0)
		case InstCapture, InstNop:
			// skip
		default:
			break Loop
		}
		pc = i.Out
		i = &p.Inst[pc]
	}
	return flag
}

const noMatch = -1

// MatchRune 报告该指令是否匹配（并消耗）r。
// 只应在 i.Op == [InstRune] 时调用。
func (i *Inst) MatchRune(r rune) bool {
	return i.MatchRunePos(r) != noMatch
}

// MatchRunePos 检查该指令是否匹配（并消耗）r。
// 如果匹配，MatchRunePos 返回匹配的 rune 对的索引
// （或者当 len(i.Rune) == 1 时，返回单个 rune 的索引）。
// 如果不匹配，MatchRunePos 返回 -1。
// MatchRunePos 只应在 i.Op == [InstRune] 时调用。
func (i *Inst) MatchRunePos(r rune) int {
	rune := i.Rune

	switch len(rune) {
	case 0:
		return noMatch

	case 1:
		// 特殊情况：单 rune 切片来自字面字符串，而非字符类。
		r0 := rune[0]
		if r == r0 {
			return 0
		}
		if Flags(i.Arg)&FoldCase != 0 {
			for r1 := unicode.SimpleFold(r0); r1 != r0; r1 = unicode.SimpleFold(r1) {
				if r == r1 {
					return 0
				}
			}
		}
		return noMatch

	case 2:
		if r >= rune[0] && r <= rune[1] {
			return 0
		}
		return noMatch

	case 4, 6, 8:
		// 对少量对进行线性搜索。
		// 应该能很好地处理 ASCII。
		for j := 0; j < len(rune); j += 2 {
			if r < rune[j] {
				return noMatch
			}
			if r <= rune[j+1] {
				return j / 2
			}
		}
		return noMatch
	}

	// 否则使用二分搜索。
	lo := 0
	hi := len(rune) / 2
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if c := rune[2*m]; c <= r {
			if r <= rune[2*m+1] {
				return m
			}
			lo = m + 1
		} else {
			hi = m
		}
	}
	return noMatch
}

// MatchEmptyWidth 报告该指令是否匹配
// rune before 和 after 之间的空字符串。
// 只应在 i.Op == [InstEmptyWidth] 时调用。
func (i *Inst) MatchEmptyWidth(before rune, after rune) bool {
	switch EmptyOp(i.Arg) {
	case EmptyBeginLine:
		return before == '\n' || before == -1
	case EmptyEndLine:
		return after == '\n' || after == -1
	case EmptyBeginText:
		return before == -1
	case EmptyEndText:
		return after == -1
	case EmptyWordBoundary:
		return IsWordChar(before) != IsWordChar(after)
	case EmptyNoWordBoundary:
		return IsWordChar(before) == IsWordChar(after)
	}
	panic("unknown empty width arg")
}

func (i *Inst) String() string {
	var b strings.Builder
	dumpInst(&b, i)
	return b.String()
}

func bw(b *strings.Builder, args ...string) {
	for _, s := range args {
		b.WriteString(s)
	}
}

func dumpProg(b *strings.Builder, p *Prog) {
	for j := range p.Inst {
		i := &p.Inst[j]
		pc := strconv.Itoa(j)
		if len(pc) < 3 {
			b.WriteString("   "[len(pc):])
		}
		if j == p.Start {
			pc += "*"
		}
		bw(b, pc, "\t")
		dumpInst(b, i)
		bw(b, "\n")
	}
}

func u32(i uint32) string {
	return strconv.FormatUint(uint64(i), 10)
}

func dumpInst(b *strings.Builder, i *Inst) {
	switch i.Op {
	case InstAlt:
		bw(b, "alt -> ", u32(i.Out), ", ", u32(i.Arg))
	case InstAltMatch:
		bw(b, "altmatch -> ", u32(i.Out), ", ", u32(i.Arg))
	case InstCapture:
		bw(b, "cap ", u32(i.Arg), " -> ", u32(i.Out))
	case InstEmptyWidth:
		bw(b, "empty ", u32(i.Arg), " -> ", u32(i.Out))
	case InstMatch:
		bw(b, "match")
	case InstFail:
		bw(b, "fail")
	case InstNop:
		bw(b, "nop -> ", u32(i.Out))
	case InstRune:
		if i.Rune == nil {
			// shouldn't happen
			bw(b, "rune <nil>")
		}
		bw(b, "rune ", strconv.QuoteToASCII(string(i.Rune)))
		if Flags(i.Arg)&FoldCase != 0 {
			bw(b, "/i")
		}
		bw(b, " -> ", u32(i.Out))
	case InstRune1:
		bw(b, "rune1 ", strconv.QuoteToASCII(string(i.Rune)), " -> ", u32(i.Out))
	case InstRuneAny:
		bw(b, "any -> ", u32(i.Out))
	case InstRuneAnyNotNL:
		bw(b, "anynotnl -> ", u32(i.Out))
	}
}
