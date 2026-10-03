// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

var ArchAMD64 = &Arch{
	Name:      "amd64",
	WordBits:  64,
	WordBytes: 8,

	regs: []string{
		"BX", "SI", "DI",
		"R8", "R9", "R10", "R11", "R12", "R13", "R14", "R15",
		"AX", "DX", "CX", // 放在最后，以留作带提示的分配使用
	},
	op3:              x86Op3,
	hint:             x86Hint,
	memOK:            true,
	subCarryIsBorrow: true,

	// 注意：不设置 memIndex，因为如果避免使用
	// 比例索引内存引用，代码通常运行得更快，
	// 尤其是在 ADX 指令中。

	options: map[Option]func(*Asm, string){
		OptionAltCarry: amd64JmpADX,
	},

	mov:      "MOVQ",
	adds:     "ADDQ",
	adcs:     "ADCQ",
	subs:     "SUBQ",
	sbcs:     "SBBQ",
	lsh:      "SHLQ",
	lshd:     "SHLQ",
	rsh:      "SHRQ",
	rshd:     "SHRQ",
	and:      "ANDQ",
	or:       "ORQ",
	xor:      "XORQ",
	neg:      "NEGQ",
	lea:      "LEAQ",
	addF:     amd64Add,
	mulWideF: x86MulWide,

	addWords: "LEAQ (%[2]s)(%[1]s*8), %[3]s",

	jmpZero:       "TESTQ %[1]s, %[1]s; JZ %[2]s",
	jmpNonZero:    "TESTQ %[1]s, %[1]s; JNZ %[2]s",
	loopBottom:    "SUBQ $1, %[1]s; JNZ %[2]s",
	loopBottomNeg: "ADDQ $1, %[1]s; JNZ %[2]s",
}

func amd64JmpADX(a *Asm, label string) {
	a.Printf("\tCMPB ·hasADX(SB), $0; JNZ %s\n", label)
}

func amd64Add(a *Asm, src1, src2 Reg, dst Reg, carry Carry) bool {
	if a.Enabled(OptionAltCarry) {
		// 如果启用了 OptionAltCarry，生成器会同时发出
		// 设置了和未设置 AltCarry 标志的 ADD 指令；AltCarry 标志表示
		// 使用 ADOX。否则我们必须使用 ADCX。
		// 使用常规的 ADD/ADC 会破坏两个进位标志，
		// 因此我们拒绝任何无法用 ADCX/ADOX 处理的情况。
		if carry&UseCarry != 0 && carry&(SetCarry|SmashCarry) != 0 {
			if carry&AltCarry != 0 {
				a.op3("ADOXQ", src1, src2, dst)
			} else {
				a.op3("ADCXQ", src1, src2, dst)
			}
			return true
		}
		if carry&(SetCarry|UseCarry) == SetCarry && a.IsZero(src1) && src2 == dst {
			// 清除进位标志。调用者将添加 EOL 注释。
			a.Printf("\tTESTQ AX, AX\n")
			return true
		}
		if carry != KeepCarry {
			a.Fatalf("unsupported carry")
		}
	}
	return false
}

// 以 x86 为前缀的函数与 386.go 中的 Arch386 共享。

func x86Op3(name string) bool {
	// 就 a.op3 而言，不存在三操作数指令。
	// （像 MULX 这样的指令由我们自己打印。）
	return false
}

func x86Hint(a *Asm, h Hint) string {
	switch h {
	case HintShiftCount:
		return "CX"
	case HintMulSrc:
		if a.Enabled(OptionAltCarry) { // 使用 MULX
			return "DX"
		}
		return "AX"
	case HintMulHi:
		if a.Enabled(OptionAltCarry) { // 使用 MULX
			return ""
		}
		return "DX"
	}
	return ""
}

func x86Suffix(a *Asm) string {
	// 注意：不使用 a.Arch == Arch386，以避免初始化循环。
	if a.Arch.Name == "386" {
		return "L"
	}
	return "Q"
}

func x86MulWide(a *Asm, src1, src2, dstlo, dsthi Reg) {
	if a.Enabled(OptionAltCarry) {
		// 使用 ADCX/ADOX；用 MULX 以避免清除进位标志。
		if src1.name != "DX" {
			if src2.name != "DX" {
				a.Fatalf("mul src1 or src2 must be DX")
			}
			src2 = src1
		}
		a.Printf("\tMULXQ %s, %s, %s\n", src2, dstlo, dsthi)
		return
	}

	if src1.name != "AX" {
		if src2.name != "AX" {
			a.Fatalf("mulwide src1 or src2 must be AX")
		}
		src2 = src1
	}
	if dstlo.name != "AX" {
		a.Fatalf("mulwide dstlo must be AX")
	}
	if dsthi.name != "DX" {
		a.Fatalf("mulwide dsthi must be DX")
	}
	a.Printf("\tMUL%s %s\n", x86Suffix(a), src2)
}
