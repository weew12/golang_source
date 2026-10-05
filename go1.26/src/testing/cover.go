// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 测试覆盖率支持。

package testing

// CoverBlock 记录单个基本块的覆盖率数据。
// 各字段采用 1 起始索引，如同编辑器中的行号：例如，
// 文件的开头一行编号为 1。列以字节为单位度量。
// 注意：该结构体属于测试基础设施内部实现，可能会发生变化。
// 它（尚）不受 Go 1 兼容性准则的保护。
type CoverBlock struct {
	Line0 uint32 // 块起始的行号。
	Col0  uint16 // 块起始的列号。
	Line1 uint32 // 块结束的行号。
	Col1  uint16 // 块结束的列号。
	Stmts uint16 // 此块中包含的语句数量。
}

// Cover 记录有关测试覆盖率检查的信息。
// 注意：该结构体属于测试基础设施内部实现，可能会发生变化。
// 它（尚）不受 Go 1 兼容性准则的保护。
type Cover struct {
	Mode            string
	Counters        map[string][]uint32
	Blocks          map[string][]CoverBlock
	CoveredPackages string
}

// RegisterCover 记录测试的覆盖率数据累加器。
// 注意：该函数属于测试基础设施内部实现，可能会发生变化。
// 它（尚）不受 Go 1 兼容性准则的保护。
func RegisterCover(c Cover) {
}
