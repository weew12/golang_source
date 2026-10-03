// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

var ArchPPC64x = &Arch{
	Name:          "ppc64x",
	Build:         "ppc64 || ppc64le",
	WordBits:      64,
	WordBytes:     8,
	CarrySafeLoop: true,

	// 注意：旧的、手写的 ppc64x 汇编在少数例程中使用 MOVDU
	// 来避免显式的指针更新，但新生成的代码运行速度一样快，
	// 所以我们就没费心再把它加回来。（这并不简单；
	// 你得让指针偏移一个字才能让语义正常工作。）
	//
	// 旧汇编还使用了一些复杂的向量指令
	// 来实现 lshVU 和 rshVU，但使用
	// 普通整数指令的生成代码比向量代码快得多，
	// 至少从 power10 gomote 上看是这样。

	regs: []string{
		// 按约定 R0 是 0。
		// R1 是 SP。
		// R2 是 TOC。
		// R30 是 g。
		// R31 是汇编器/链接器临时寄存器（我们也使用它）。
		"R3", "R4", "R5", "R6", "R7", "R8", "R9",
		"R10", "R11", "R12" /*R13 是 TLS*/, "R14", "R15", "R16", "R17", "R18", "R19",
		"R20", "R21", "R22", "R23", "R24", "R25", "R26", "R27", "R28", "R29",
	},
	reg0:   "R0",
	regTmp: "R31",

	// 注意：可以编写 addF 和 subF 来使用 ADDZE 和 SUBZE，
	// 但我们有 R0，所以似乎关系不大。

	mov:   "MOVD",
	add:   "ADD",
	adds:  "ADDC",
	adcs:  "ADDE",
	sub:   "SUB",
	subs:  "SUBC",
	sbcs:  "SUBE",
	mul:   "MULLD",
	mulhi: "MULHDU",
	lsh:   "SLD",
	rsh:   "SRD",
	and:   "ANDCC", // 常规 AND 不接受立即数
	or:    "OR",
	xor:   "XOR",

	jmpZero:    "CMP %[1]s, $0; BEQ %[2]s",
	jmpNonZero: "CMP %s, $0; BNE %s",

	// 注意：使用 CTR 意味着我们可以在循环体期间
	// 释放计数寄存器，但可移植的逻辑并不知道这一点，
	// 而且我们并不缺寄存器。
	loopTop:    "CMP %[1]s, $0; BEQ %[2]s; MOVD %[1]s, CTR",
	loopBottom: "BDNZ %[2]s",
}
