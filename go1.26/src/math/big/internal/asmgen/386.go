// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

import "fmt"

var Arch386 = &Arch{
	Name:      "386",
	WordBits:  32,
	WordBytes: 4,

	regs: []string{
		"BX", "SI", "DI", "BP",
		"CX", "DX", "AX", // 放在最后，以留作带提示的分配使用
	},
	op3:              x86Op3,
	hint:             x86Hint,
	memOK:            true,
	subCarryIsBorrow: true,
	maxColumns:       1, // 寄存器不足，无法再多

	// 注意：如果不设置 memIndex，然后删除 pipe.go 中
	// 支持它的所有代码，那会是件好事。
	// 但有几个例程，尤其是 lshVU 和 mulAddVWW，
	// 从索引寄存器的使用中获益巨大。
	// 也许某天我们会认定 386 的性能
	// 已不重要，不值得保留这些代码。
	memIndex: _386MemIndex,

	mov:      "MOVL",
	adds:     "ADDL",
	adcs:     "ADCL",
	subs:     "SUBL",
	sbcs:     "SBBL",
	lsh:      "SHLL",
	lshd:     "SHLL",
	rsh:      "SHRL",
	rshd:     "SHRL",
	and:      "ANDL",
	or:       "ORL",
	xor:      "XORL",
	neg:      "NEGL",
	lea:      "LEAL",
	mulWideF: x86MulWide,

	addWords: "LEAL (%[2]s)(%[1]s*4), %[3]s",

	jmpZero:       "TESTL %[1]s, %[1]s; JZ %[2]s",
	jmpNonZero:    "TESTL %[1]s, %[1]s; JNZ %[2]s",
	loopBottom:    "SUBL $1, %[1]s; JNZ %[2]s",
	loopBottomNeg: "ADDL $1, %[1]s; JNZ %[2]s",
}

func _386MemIndex(a *Asm, off int, ix Reg, p RegPtr) Reg {
	return Reg{fmt.Sprintf("%d(%s)(%s*%d)", off, p, ix, a.Arch.WordBytes)}
}
