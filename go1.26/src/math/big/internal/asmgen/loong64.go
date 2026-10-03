// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

var ArchLoong64 = &Arch{
	Name:          "loong64",
	WordBits:      64,
	WordBytes:     8,
	CarrySafeLoop: true,

	regs: []string{
		// R0 被设置为 0。
		// R1 是 LR。
		// R2 是 ???
		// R3 是 SP。
		// R22 是 g。
		// R28 和 R29 是我们的虚拟进位标志。
		// R30 是链接器/汇编器临时寄存器，我们也使用它。
		"R4", "R5", "R6", "R7", "R8", "R9",
		"R10", "R11", "R12", "R13", "R14", "R15", "R16", "R17", "R18", "R19",
		"R20", "R21", "R23", "R24", "R25", "R26", "R27",
		"R31",
	},
	reg0:        "R0",
	regCarry:    "R28",
	regAltCarry: "R29",
	regTmp:      "R30",

	mov:   "MOVV",
	add:   "ADDVU",
	sub:   "SUBVU",
	sltu:  "SGTU",
	mul:   "MULV",
	mulhi: "MULHVU",
	lsh:   "SLLV",
	rsh:   "SRLV",
	and:   "AND",
	or:    "OR",
	xor:   "XOR",

	jmpZero:    "BEQ %s, %s",
	jmpNonZero: "BNE %s, %s",
}
