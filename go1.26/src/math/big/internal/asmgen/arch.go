// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

import (
	"fmt"
	"strings"
)

// 注意：导出的字段和方法预期由函数生成器
// （如 add.go 等文件中的那些）使用。
// 未导出的字段和方法则不应被使用。

// Arch 定义了如何为特定架构生成汇编代码。
type Arch struct {
	Name          string // 架构名称
	Build         string // 构建标签
	WordBits      int    // 字的长度（以位为单位）（32 或 64）
	WordBytes     int    // 字的长度（以字节为单位）（4 或 8）
	CarrySafeLoop bool   // 循环是否在迭代之间保留进位标志

	// 寄存器。
	regs        []string // 可用的通用寄存器，按分配顺序排列
	reg0        string   // 专用零寄存器
	regCarry    string   // 专用进位寄存器，用于没有硬件进位位的系统
	regAltCarry string   // 专用次级进位寄存器，用于没有硬件进位位的系统
	regTmp      string   // 专用临时寄存器

	// regShift 表示该架构支持将 REG1>>REG2 和 REG1<<REG2
	// 用作算术指令的第一个源操作数。（32 位 ARM 支持这一点。）
	regShift bool

	// setup 被调用以发出特定于架构的函数序言，
	// 紧跟在 TEXT 行发出之后。
	// 如果 setup 为 nil，则视为空操作。
	setup func(*Func)

	// hint 针对给定提示返回要使用的寄存器。
	// 返回空字符串表示没有偏好。
	// 如果 hint 为 nil，则视为返回空字符串。
	hint func(*Asm, Hint) string

	// op3 报告指定操作码是否接受 3 个操作数
	// （在大多数系统的大多数指令上为 true，但对 x86 指令不成立）。
	// 汇编器会无条件地将 op x,z,z 转换为 op x,z。
	// 如果 op3 返回 false，那么汇编器会将 op x,y,z 转换为 mov y,z; op x,z。
	// 如果 op3 为 nil，则假定所有操作码都接受 3 个操作数。
	op3 func(name string) bool

	// memOK 表示算术指令可以使用内存引用（如 x86 上那样）
	memOK bool

	// maxColumns 是在单个 [Pipe.Loop] 块中处理的
	// 默认最大向量列数。
	// 0 表示无限制。
	// [Pipe.SetMaxColumns] 会覆盖此值。
	maxColumns int

	// 指令名称。
	mov   string // 移动（字大小）
	add   string // 不涉及进位的加法
	adds  string // 加法，设置进位但不使用进位
	adc   string // 加法，使用进位但不设置进位
	adcs  string // 加法，设置并使用进位
	sub   string // 不涉及进位的减法
	subs  string // 减法，设置进位但不使用进位
	sbc   string // 减法，使用进位但不设置进位
	sbcs  string // 减法，设置并使用进位
	mul   string // 乘法
	mulhi string // 产生高位的乘法
	lsh   string // 左移
	lshd  string // 双倍宽度左移
	rsh   string // 右移
	rshd  string // 双倍宽度右移
	and   string // 按位与
	or    string // 按位或
	xor   string // 按位异或
	neg   string // 取负
	rsb   string // 反向减法
	sltu  string // 无符号设定小于（dst = src2 < src1），用于无进位系统
	sgtu  string // 无符号设定大于（dst = src2 > src1），用于无进位系统
	lea   string // 加载有效地址

	// addF 和 subF 在情况比六种基本指令
	// （add、adds、adcs、sub、subs、sbcs）更复杂的系统上
	// 实现 a.Add 和 a.Sub。
	// 它们返回一个布尔值，指示该操作是否已被处理。
	addF func(a *Asm, src1, src2, dst Reg, carry Carry) bool
	subF func(a *Asm, src1, src2, dst Reg, carry Carry) bool

	// mulF 和 mulWideF 实现 Mul 和 MulWide。
	// 如果操作不受支持，它们会调用 Fatalf。
	// 架构可以设置 mul 字段而不是 mulF。
	// mulWide 是可选的，但否则应设置 mulhi。
	mulWideF func(a *Asm, src1, src2, dstlo, dsthi Reg)

	// addWords 是一个 printf 格式，接受 src1、src2、dst
	// 并设置 dst = WordBytes*src1+src2。
	// 它可能修改进位标志。
	addWords string

	// 当减法中实际使用的处理器进位位实际上是“借位”位时，
	// subCarryIsBorrow 为 true，这意味着 1 表示借位，0 表示无借位。
	// 相比之下，大多数系统（x86 除外）使用的进位位含义相反：
	// 0 表示发生了借位，1 表示没有借位。
	subCarryIsBorrow bool

	// 跳转指令的 printf 格式。
	// jmpZero 和 jmpNonZero 是接受 src、label 的 printf 格式，
	// 若 src 为零/非零则跳转到 label。
	jmpZero    string
	jmpNonZero string

	// loopTop 是一个 printf 格式，接受 src、label，若 src 为零
	// 则应跳转到 label，否则为循环做准备。
	// 如果未设置 loopTop，则使用 jmpZero。
	loopTop string

	// loopBottom 是一个 printf 格式，接受 dst、label，应递减 dst，
	// 然后若 src 非零则跳转到 label。
	// 如果未设置 loopBottom，则使用减法后再用 jmpNonZero。
	loopBottom string

	// loopBottomNeg 类似 loopBottom，但用于负索引循环，
	// 这种循环只在同时设置了 memIndex 时出现（仅在 386 上）。
	// 它递增 dst 而不是递减 dst。
	loopBottomNeg string

	// 索引内存访问。
	// 如果设置，memIndex 为寻址 off(ptr)(ix*WordBytes) 的 mov 指令
	// 返回一个内存引用。
	// 使用 memIndex 会多消耗一个寄存器，但允许循环末尾
	// 执行单次递增/递减，而不必推进两三个指针。
	// 这在 386 上尤为重要。
	memIndex func(a *Asm, off int, ix Reg, ptr RegPtr) Reg

	// 递增/递减内存访问。
	// loadIncN 将 ptr 处的内存加载到 regs 中，每加载一个 reg 后将 ptr 递增 WordBytes。
	// loadDecN 将 ptr 处的内存加载到 regs 中，每加载一个 reg 前将 ptr 递减 WordBytes。
	// storeIncN 和 storeDecN 相同，但从 regs 存储而非加载到 regs。
	// 如果缺失，汇编器会使用单独的指令访问内存并推进指针。
	loadIncN  func(a *Asm, ptr RegPtr, regs []Reg)
	loadDecN  func(a *Asm, ptr RegPtr, regs []Reg)
	storeIncN func(a *Asm, ptr RegPtr, regs []Reg)
	storeDecN func(a *Asm, ptr RegPtr, regs []Reg)

	// options 是从可选 CPU 特性到测试它们的函数的映射。
	// 如果该特性可用，测试函数应跳转到 label。
	options map[Option]func(a *Asm, label string)
}

// HasShiftWide 报告该 Arch 是否具有可用的 LshWide/RshWide 指令。
// 如果没有，调用它们会 panic。
func (a *Arch) HasShiftWide() bool {
	return a.lshd != ""
}

// Hint 是关于寄存器将用于何种用途的提示，
// 以便能选择合适的一个。
type Hint uint

const (
	HintNone       Hint = iota
	HintShiftCount      // 移位计数（x86 上为 CX）
	HintMulSrc          // 乘法源操作数（x86 上为 AX）
	HintMulHi           // 宽乘法高位输出（x86 上为 DX）
	HintMemOK           // 内存引用是可以的
	HintCarry           // 进位标志
	HintAltCarry        // 次级进位标志
)

// Reg 是一个已分配的寄存器或其他汇编操作数。
// （例如，常量可能具有名称 "$123"，
// 内存引用可能具有名称 "0(R8)"。）
type Reg struct{ name string }

// IsImm 报告 r 是否为立即数值。
func (r Reg) IsImm() bool { return strings.HasPrefix(r.name, "$") }

// IsMem 报告 r 是否为内存值。
func (r Reg) IsMem() bool { return strings.HasSuffix(r.name, ")") }

// String 返回 r 的汇编语法。
func (r Reg) String() string { return r.name }

// Valid 报告 r 是否有效，即 r 不是 Reg 的零值（没有名称的寄存器）。
func (r Reg) Valid() bool { return r.name != "" }

// RegPtr 类似 Reg，但预期保存一个指针。
// 独立的 Go 类型有助于将指针与标量分开并避免错误；
// 在需要使用特定例程时，按需转换为 Reg 是可以的。
type RegPtr struct{ name string }

// String 返回 r 的汇编语法。
func (r RegPtr) String() string { return r.name }

// Valid 报告 r 是否有效，即 r 不是 RegPtr 的零值（没有名称的寄存器）。
func (r RegPtr) Valid() bool { return r.name != "" }

// mem 返回指向指针 r 偏移 off 字节处的内存引用。
func (r *RegPtr) mem(off int) Reg { return Reg{fmt.Sprintf("%d(%s)", off, r)} }

// Carry 是一个标志字段，说明指令如何设置和使用进位标志。
// 不同的操作期望不同的位集合。
// Add 和 Sub 期望：UseCarry 或 0、SetCarry、KeepCarry 或 SmashCarry；以及 AltCarry 或 0。
// ClearCarry、SaveCarry 和 ConvertCarry 期望：AddCarry 或 SubCarry；以及 AltCarry 或 0。
type Carry uint

const (
	SetCarry   Carry = 1 << iota // 设置进位
	UseCarry                     // 使用进位
	KeepCarry                    // 必须保留进位
	SmashCarry                   // 可以修改进位也可以不修改，以最简单为准

	AltCarry // 使用次级进位标志
	AddCarry // 使用加法进位标志语义（用于 ClearCarry、ConvertCarry）
	SubCarry // 使用减法进位标志语义（用于 ClearCarry、ConvertCarry）
)

// Option 表示可在运行时测试的可选 CPU 特性。
type Option int

const (
	_ Option = iota

	// OptionAltCarry 检查是否存在使用次级进位标志的加法指令，
	// 以便可以用独立的进位标志并行累加两个不同的和。
	// 某些架构（MIPS、Loong64、RISC-V）原生提供此功能，
	// 由 asm.Carry().Valid() 为 true 表示。
	OptionAltCarry
)
