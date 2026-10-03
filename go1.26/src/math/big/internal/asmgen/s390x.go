// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

var ArchS390X = &Arch{
	Name:          "s390x",
	WordBits:      64,
	WordBytes:     8,
	CarrySafeLoop: true,

	regs: []string{
		// 在此代码中按约定 R0 是 0（见 setup）。
		// R10 是汇编器/链接器临时寄存器。
		// R11 是第二个汇编器/链接器临时寄存器，用于宽乘法。
		// 我们允许分配 R10 和 R11，这样就能在跟踪它们是否
		// 正在使用的同时，将它们用作直接的乘法目标。
		// R13 是 g。
		// R14 是 LR。
		// R15 是 SP。
		"R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8", "R9",
		"R10", "R11", "R12",
	},
	reg0:       "R0",
	regTmp:     "R10",
	setup:      s390xSetup,
	maxColumns: 2,
	op3:        s390xOp3,
	hint:       s390xHint,

	// 指令参考：第 7 章
	// https://www.ibm.com/docs/en/SSQ2R2_15.0.0/com.ibm.tpf.toolkit.hlasm.doc/dz9zr006.pdf

	mov:      "MOVD",
	adds:     "ADDC", // ADD 是 ADDC 的别名，设置进位
	adcs:     "ADDE",
	subs:     "SUBC", // SUB 是 SUBC 的别名，设置进位
	sbcs:     "SUBE",
	mulWideF: s390MulWide,
	lsh:      "SLD",
	rsh:      "SRD",
	and:      "AND",
	or:       "OR",
	xor:      "XOR",
	neg:      "NEG",
	lea:      "LAY", // 用 LAY 是因为 LA 只接受正偏移量

	jmpZero:    "CMPBEQ %s, $0, %s",
	jmpNonZero: "CMPBNE %s, $0, %s",
}

func s390xSetup(f *Func) {
	a := f.Asm
	if f.Name == "addVV" || f.Name == "subVV" {
		// S390x 与其他所有系统不同，它有向量指令
		// 可以在并行加法期间传播进位位（VACC）。
		// 与其为这一个系统尝试生成那部分代码，
		// 不如跳转到 arithvec_s390x.s 中手写的代码。
		a.Printf("\tMOVB ·hasVX(SB), R1\n")
		a.Printf("\tCMPBEQ R1, $0, novec\n")
		a.Printf("\tJMP ·%svec(SB)\n", f.Name)
		a.Printf("novec:\n")
	}
	a.Printf("\tMOVD $0, R0\n")
}

func s390xOp3(name string) bool {
	if name == "AND" { // 带立即数的 AND 只接受 imm, reg；不接受 imm, reg, reg。
		return false
	}
	return true
}

func s390xHint(_ *Asm, h Hint) string {
	switch h {
	case HintMulSrc:
		return "R11"
	case HintMulHi:
		return "R10"
	}
	return ""
}

func s390MulWide(a *Asm, src1, src2, dstlo, dsthi Reg) {
	if src1.name != "R11" && src2.name != "R11" {
		a.Fatalf("mulWide src1 or src2 must be R11")
	}
	if dstlo.name != "R11" {
		a.Fatalf("mulWide dstlo must be R11")
	}
	if dsthi.name != "R10" {
		a.Fatalf("mulWide dsthi must be R10")
	}
	src := src1
	if src.name == "R11" {
		src = src2
	}
	a.Printf("\tMLGR %s, R10\n", src)
}
