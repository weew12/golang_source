// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

// Package jsontext 实现了 JSON 的语法处理，
// 遵循 RFC 4627、RFC 7159、RFC 7493、RFC 8259 和 RFC 8785 的规定。
// JSON 是一种简单的数据交换格式，除了能表示对象和数组等结构化数据类型外，
// 还能表示布尔值、字符串和数字等原始数据类型。
//
// 本包（encoding/json/jsontext）是实验性的，
// 不受 Go 1 兼容性承诺的约束。
// 它仅在设置 GOEXPERIMENT=jsonv2 环境变量进行构建时才存在。
// 大多数用户应使用 [encoding/json]。
//
// [Encoder] 和 [Decoder] 类型用于编码或解码
// 一串 JSON 令牌或值。
//
// # 令牌与值
//
// JSON 令牌指的是 JSON 的基本结构元素：
//
//   - JSON 字面量（即 null、true 或 false）
//   - JSON 字符串（例如 "hello, world!"）
//   - JSON 数字（例如 123.456）
//   - JSON 对象的起始或结束定界符（即 '{' 或 '}'）
//   - JSON 数组的起始或结束定界符（即 '[' 或 ']'）
//
// JSON 令牌在 Go 中由 [Token] 类型表示。严格来说，
// 还有两个额外的结构字符（即 ':' 和 ','），
// 但它们没有 [Token] 表示，因为根据 JSON 文法结构本身
// 就可以推断出它们的存在。
// 例如，JSON 对象成员的名称和值之间总是有一个隐式的冒号。
//
// JSON 值指的是 JSON 数据的完整单元：
//
//   - JSON 字面量、字符串或数字
//   - JSON 对象（例如 `{"name":"value"}`）
//   - JSON 数组（例如 `[1,2,3,]`）
//
// JSON 值在 Go 中由 [Value] 类型表示，是一个 []byte，
// 包含该值的原始文本表示。令牌和值之间存在一些重叠，
// 因为两者都包含字面量、字符串和数字。
// 然而，只有值才能表示整个 JSON 对象或数组。
//
// [Encoder] 和 [Decoder] 类型包含用于读取或写入序列中下一个
// [Token] 或 [Value] 的方法。它们维护一个状态机，以校验
// JSON 令牌和/或值的序列是否生成有效的 JSON。
// 可以向 [NewEncoder] 或 [NewDecoder] 构造函数传递 [Options]，
// 以配置编码和解码的语法行为。
//
// # 术语
//
// “encode”和“decode”这两个术语用于指代基于文法处理 JSON 的
// 语法功能，而“marshal”和“unmarshal”这两个术语用于指代
// 确定 JSON 值作为 Go 值的含义及其反向转换的语义功能。
// 本包（即 [jsontext]）在语法层处理 JSON，
// 而 [encoding/json/v2] 在语义层处理 JSON。
// 目标是清晰区分纯粹与编码相关的功能与编组功能。
// 例如，可以直接编码一串 JSON 令牌，而无需
// 编组表示它们的具体 Go 值。
// 类似地，可以解码一串 JSON 令牌，而无需
// 将其解组为具体的 Go 值。
//
// 本包在讨论 JSON 时使用 JSON 术语，这些术语可能与
// Go 或计算文献中其他相关概念有所不同。
//
//   - JSON“对象”指的是无序的名称/值成员集合。
//   - JSON“数组”指的是元素的有序序列。
//   - JSON“值”指的是字面量（即 null、false 或 true）、
//     字符串、数字、对象或数组。
//
// 更多信息请参见 RFC 8259。
//
// # 规范
//
// 相关规范包括 RFC 4627、RFC 7159、RFC 7493、RFC 8259
// 和 RFC 8785。每个 RFC 通常都是另一个 RFC 的严格子集。
// 按严格程度递增的顺序：
//
//   - RFC 4627 和 RFC 7159 不要求（但建议）使用 UTF-8，
//     也不要求（但建议）对象名称唯一。
//   - RFC 8259 要求使用 UTF-8，
//     但不要求（但建议）对象名称唯一。
//   - RFC 7493 要求使用 UTF-8，
//     并且要求对象名称唯一。
//   - RFC 8785 定义了规范表示。它要求使用 UTF-8，
//     并且要求对象名称唯一且按特定顺序排列。
//     它精确规定了字符串和数字的格式化方式。
//
// RFC 4627 与 RFC 7159 的主要区别在于，前者
// 将顶层值限制为仅 JSON 对象和数组，而
// RFC 7159 及其后续 RFC 允许顶层值额外为
// JSON null、布尔值、字符串或数字。
//
// 默认情况下，本包按 RFC 7493 运行，但可以配置为
// 按照其他 RFC 规范运行。
// RFC 7493 是 RFC 8259 的严格子集，并完全兼容它。
// 特别是，它对 RFC 8259 留作未定义的行为做出了具体选择，
// 以确保更好的互操作性。
//
// # 安全注意事项
//
// 参见 [encoding/json/v2] 中的 "Security Considerations" 部分。
package jsontext

// requireKeyedLiterals 可以嵌入到结构体中，以要求使用带键的字面量。
type requireKeyedLiterals struct{}

// nonComparable 可以嵌入到结构体中，以阻止可比较性。
type nonComparable [0]func()
