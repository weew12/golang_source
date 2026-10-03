// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

import "unsafe"

// 尽管是一个导出符号，
// Float32bits 仍被广泛使用的包通过 linkname 引用。
// 其中臭名昭著的成员包括：
//   - gitee.com/quant1x/num
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
// 注意，此注释不属于文档注释。
//
//go:linkname Float32bits

// Float32bits 返回 f 的 IEEE 754 二进制表示，
// 其中 f 的符号位与结果位于相同的位位置。
// Float32bits(Float32frombits(x)) == x.
func Float32bits(f float32) uint32 { return *(*uint32)(unsafe.Pointer(&f)) }

// Float32frombits 返回对应于 IEEE 754 二进制表示 b 的浮点数，
// 其中 b 的符号位与结果位于相同的位位置。
// Float32frombits(Float32bits(x)) == x.
func Float32frombits(b uint32) float32 { return *(*float32)(unsafe.Pointer(&b)) }

// Float64bits 返回 f 的 IEEE 754 二进制表示，
// 其中 f 的符号位与结果位于相同的位位置，
// 且 Float64bits(Float64frombits(x)) == x。
func Float64bits(f float64) uint64 { return *(*uint64)(unsafe.Pointer(&f)) }

// Float64frombits 返回对应于 IEEE 754 二进制表示 b 的浮点数，
// 其中 b 的符号位与结果位于相同的位位置。
// Float64frombits(Float64bits(x)) == x.
func Float64frombits(b uint64) float64 { return *(*float64)(unsafe.Pointer(&b)) }
