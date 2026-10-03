// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Asmgen 生成 math/big 汇编。
//
// 用法：
//
//	cd go/src/math/big
//	go test ./internal/asmgen -generate
//
// 或者：
//
//	go generate math/big
package asmgen

var arches = []*Arch{
	Arch386,
	ArchAMD64,
	ArchARM,
	ArchARM64,
	ArchLoong64,
	ArchMIPS,
	ArchMIPS64x,
	ArchPPC64x,
	ArchRISCV64,
	ArchS390X,
}

// generate 返回给定架构所生成汇编的文件名和内容。
func generate(arch *Arch) (file string, data []byte) {
	file = "arith_" + arch.Name + ".s"
	a := NewAsm(arch)
	addOrSubVV(a, "addVV")
	addOrSubVV(a, "subVV")
	shiftVU(a, "lshVU")
	shiftVU(a, "rshVU")
	mulAddVWW(a)
	addMulVVWW(a)
	return file, a.out.Bytes()
}
