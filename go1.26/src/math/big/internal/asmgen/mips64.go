// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

var ArchMIPS64x = &Arch{
	Name:          "mips64x",
	Build:         "mips64 || mips64le",
	WordBits:      64,
	WordBytes:     8,
	CarrySafeLoop: true,

	regs: []string{
		// R0 是 0
		// R23 是汇编器/链接器临时寄存器（我们也使用它）。
		// R24 和 R25 是我们的虚拟进位标志。
		// R28 是 SB。
		// R29 是 SP。
		// R30 是 g。
		// R31 是 LR。
		"R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8", "R9",
		"R10", "R11", "R12", "R13", "R14", "R15", "R16", "R17", "R18", "R19",
		"R20", "R21", "R22", "R24", "R25",
	},
	reg0:        "R0",
	regTmp:      "R23",
	regCarry:    "R24",
	regAltCarry: "R25",

	mov:      "MOVV",
	add:      "ADDVU",
	sltu:     "SGTU", // SGTU 的参数是交换的，所以它实际上是 SLTU
	sub:      "SUBVU",
	mulWideF: mips64MulWide,
	lsh:      "SLLV",
	rsh:      "SRLV",
	and:      "AND",
	or:       "OR",
	xor:      "XOR",

	jmpZero:    "BEQ %s, %s",
	jmpNonZero: "BNE %s, %s",
}

func mips64MulWide(a *Asm, src1, src2, dstlo, dsthi Reg) {
	a.Printf("\tMULVU %s, %s\n\tMOVV LO, %s\n\tMOVV HI, %s\n", src1, src2, dstlo, dsthi)
}
