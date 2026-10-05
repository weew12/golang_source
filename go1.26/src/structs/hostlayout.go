// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package structs

// HostLayout 将结构体标记为使用宿主内存布局。带有 HostLayout 类型
// 字段的结构体将按照宿主的预期进行内存布局，通常遵循宿主的 C ABI。
//
// HostLayout 不会影响包含结构体中任何其他结构体类型字段内部的布局，
// 也不会影响包含被标记为宿主布局的结构体的那些结构体的布局。
//
// 按照约定，HostLayout 应用作名为 "_" 的字段的类型，
// 并放置在结构体类型定义的起始处。
type HostLayout struct {
	_ hostLayout // 防止与普通 struct{} 发生意外转换
}

// 我们在导出类型内部使用一个未导出类型，是为了让标记类型本身（而非
// 仅仅是其名称）在类型系统中具有可识别的身份。这样做的主要后果是，
// 用户可以给该类型起一个新名字，而它仍将具有相同的属性，例如，
//
//	type HL structs.HostLayout
//
// 这也可以防止将 struct{} 意外转换为具名标记类型。
type hostLayout struct {
}
