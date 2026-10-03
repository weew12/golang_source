// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

import (
	"fmt"
	"slices"
	"strings"
)

// 注意：导出的字段和方法预期由函数生成器
// （如 add.go 等文件中的那些）使用。
// 未导出的字段和方法则不应被使用。

// Func 表示单个汇编函数。
type Func struct {
	Name    string
	Asm     *Asm
	inputs  []string       // 输入切片名称（不以 z 开头）
	outputs []string       // 输出切片名称（以 z 开头）
	args    map[string]int // 参数、结果在栈上的偏移量
}

// Func 在汇编输出中开始一个新函数。
func (a *Asm) Func(decl string) *Func {
	d, ok := strings.CutPrefix(decl, "func ")
	if !ok {
		a.Fatalf("func decl does not begin with 'func '")
	}
	name, d, ok := strings.Cut(d, "(")
	if !ok {
		a.Fatalf("func decl does not have func arg list")
	}
	f := &Func{
		Name: name,
		Asm:  a,
		args: make(map[string]int),
	}
	a.FreeAll()

	// 解析参数名称和类型。简单粗暴。
	// 将 (args) (results) 转换为 args, results。
	d = strings.ReplaceAll(d, ") (", ", ")
	d = strings.TrimSuffix(d, ")")
	args := strings.Split(d, ",")

	// 为所有参数分配隐式类型（x, y int -> x int, y int）。
	typ := ""
	for i, arg := range slices.Backward(args) {
		arg = strings.TrimSpace(arg)
		if !strings.Contains(arg, " ") {
			if typ == "" {
				a.Fatalf("missing argument type")
			}
			arg += " " + typ
		} else {
			_, typ, _ = strings.Cut(arg, " ")
		}
		args[i] = arg
	}

	// 记录从名称到偏移量的映射。
	off := 0
	for _, arg := range args {
		name, typ, _ := strings.Cut(arg, " ")
		switch typ {
		default:
			a.Fatalf("unknown type %s", typ)
		case "Word", "uint", "int":
			f.args[name] = off
			off += a.Arch.WordBytes
		case "[]Word":
			if strings.HasPrefix(name, "z") {
				f.outputs = append(f.outputs, name)
			} else {
				f.inputs = append(f.inputs, name)
			}
			f.args[name+"_base"] = off
			f.args[name+"_len"] = off + a.Arch.WordBytes
			f.args[name+"_cap"] = off + 2*a.Arch.WordBytes
			off += 3 * a.Arch.WordBytes
		}
	}

	a.Printf("\n")
	a.Printf("// %s\n", decl)
	a.Printf("TEXT ·%s(SB), NOSPLIT, $0\n", name)
	if a.Arch.setup != nil {
		a.Arch.setup(f)
	}
	return f
}

// Arg 分配一个新寄存器，将命名参数（或结果）复制到其中，
// 并返回该寄存器。
func (f *Func) Arg(name string) Reg {
	return f.ArgHint(name, HintNone)
}

// ArgHint 类似 Arg，但使用寄存器分配提示。
func (f *Func) ArgHint(name string, hint Hint) Reg {
	off, ok := f.args[name]
	if !ok {
		f.Asm.Fatalf("unknown argument %s", name)
	}
	mem := Reg{fmt.Sprintf("%s+%d(FP)", name, off)}
	if hint == HintMemOK && f.Asm.Arch.memOK {
		return mem
	}
	r := f.Asm.RegHint(hint)
	f.Asm.Mov(mem, r)
	return r
}

// ArgPtr 类似 Arg，但返回 RegPtr。
func (f *Func) ArgPtr(name string) RegPtr {
	return RegPtr(f.Arg(name))
}

// StoreArg 将 src 存储到命名参数（或结果）中。
func (f *Func) StoreArg(src Reg, name string) {
	off, ok := f.args[name]
	if !ok {
		f.Asm.Fatalf("unknown argument %s", name)
	}
	a := f.Asm
	mem := Reg{fmt.Sprintf("%s+%d(FP)", name, off)}
	if src.IsImm() && !a.Arch.memOK {
		r := a.Reg()
		a.Mov(src, r)
		a.Mov(r, mem)
		a.Free(r)
		return
	}
	a.Mov(src, mem)
}
