// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

// Package json 按照 RFC 8259 的规定实现 JSON 的语义处理。
// JSON 是一种简单的数据交换格式，除了对象和数组等结构化数据类型之外，
// 还能表示布尔值、字符串和数字等原始数据类型。
//
// 本包（encoding/json/v2）是实验性的，
// 不受 Go 1 兼容性承诺的约束。
// 它仅在设置了 GOEXPERIMENT=jsonv2 环境变量进行构建时才存在。
// 大多数用户应使用 [encoding/json]。
//
// [Marshal] 和 [Unmarshal] 在 []byte 中包含的 JSON 文本
// 与 Go 值之间进行编码和解码。
// [MarshalWrite] 和 [UnmarshalRead] 通过向 [io.Writer] 写入
// 或从 [io.Reader] 读取来操作 JSON 文本。
// [MarshalEncode] 和 [UnmarshalDecode] 通过向 [jsontext.Encoder] 编码
// 或从 [jsontext.Decoder] 解码来操作 JSON 文本。
// 可以将 [Options] 传递给每个 marshal 或 unmarshal 函数，
// 以配置 marshaling 和 unmarshaling 的语义行为
// （即改变 JSON 数据如何被理解为 Go 数据，以及反之）。
// 也可以将 [jsontext.Options] 传递给 marshal 或 unmarshal 函数，
// 以配置编码或解码的语法行为。
//
// JSON 的数据类型与 Go 的数据类型之间的映射，
// 基于两种类型系统之间最接近的逻辑等价关系。例如，
// JSON 布尔值对应于 Go 的 bool，
// JSON 字符串对应于 Go 的 string，
// JSON 数字对应于 Go 的 int、uint 或 float，
// JSON 数组对应于 Go 的 slice 或 array，
// JSON 对象对应于 Go 的 struct 或 map。
// 有关 JSON 与 Go 类型系统如何对应的完整列表，
// 参见 [Marshal] 和 [Unmarshal] 的文档。
//
// 任意 Go 类型都可以通过实现 [Marshaler]、[MarshalerTo]、[Unmarshaler] 或
// [UnmarshalerFrom] 来自定义其 JSON 表示形式。
// 这为 Go 类型的作者提供了对其类型如何序列化为 JSON 的控制权。
// 或者，用户可以实现与 [MarshalFunc]、[MarshalToFunc]、[UnmarshalFunc] 或
// [UnmarshalFromFunc] 匹配的函数，为任意类型指定 JSON 表示形式。
// 这为 JSON 功能的使用者提供了对任意类型如何序列化为 JSON 的控制权。
//
// # Go struct 的 JSON 表示形式
//
// Go struct 自然地表示为 JSON 对象，
// 其中每个 Go struct 字段对应一个 JSON 对象成员。
// 在 marshaling 时，所有 Go struct 字段都会以深度优先顺序
// 递归地编码为 JSON 对象成员，被忽略或省略的字段除外。
// 在 unmarshaling 时，JSON 对象成员会被递归地解码
// 到对应的 Go struct 字段中。
// 不匹配任何 struct 字段的对象成员，
// 也称为“未知成员”，默认会被忽略，
// 如果指定了 [RejectUnknownMembers]，则会被拒绝。
//
// 每个 struct 字段的表示形式可以在
// "json" struct 字段标签中自定义，该标签是一个以逗号分隔的选项列表。
// 作为特殊情况，如果整个标签为 `json:"-"`，
// 则该字段在其 JSON 表示形式中被忽略。
// 某些选项也有由调用方指定的 [Options] 控制的等效行为。
// 字段指定的选项优先于调用方指定的选项。
//
// 第一个选项是 Go struct 字段的 JSON 对象名称覆盖值。
// 如果未指定名称，则使用 Go struct 字段名作为 JSON 对象名称。
// 包含逗号或引号的 JSON 名称，或与 "" 或 "-" 相同的名称，
// 可以使用单引号字符串字面量来指定，其语法
// 与 Go 中双引号字符串字面量的语法相同，
// 只是改用单引号作为定界符。
// 默认情况下，unmarshaling 使用区分大小写的匹配
// 来识别与某个 JSON 对象名称相关联的 Go struct 字段。
//
// 在名称之后，支持以下标签选项：
//
//   - omitzero：在 marshaling 时，"omitzero" 选项指定
//     如果字段值按其 "IsZero() bool" 方法（若存在）判定为零，
//     否则按该字段是否为零值 Go 值来判定，
//     则应省略该 struct 字段。
//     该选项在 unmarshaling 时无效。
//
//   - omitempty：在 marshaling 时，"omitempty" 选项指定
//     如果字段值本来会被编码为 JSON null、空字符串、空对象或空数组，
//     则应省略该 struct 字段。
//     该选项在 unmarshaling 时无效。
//
//   - string："string" 选项指定在对 struct 字段值进行 marshaling 或
//     unmarshaling 时设置 [StringifyNumbers]。
//     这会导致数值类型被编码为 JSON 字符串内的 JSON 数字，
//     并从包含该 JSON 数字且不含任何周围空白的 JSON 字符串中解码。
//     这种额外的编码层级通常是必要的，
//     因为许多 JSON 解析器无法精确表示 64 位整数。
//
//   - case：在 unmarshaling 时，"case" 选项指定
//     JSON 对象名称如何与 Go struct 字段的 JSON 名称匹配。
//     该选项是一个键值对，形式为 "case:value"，其中
//     值必须是 'ignore' 或 'strict'。
//     'ignore' 值指定匹配不区分大小写，
//     其中连字符和下划线也会被忽略。如果有多个字段匹配，
//     则以广度优先顺序声明的第一个字段优先。
//     'strict' 值指定匹配区分大小写。
//     这优先于 [MatchCaseInsensitiveNames] 选项。
//
//   - inline："inline" 选项指定
//     此字段类型中可表示为 JSON 的内容将被提升，
//     如同它们在父 struct 中指定一样。
//     它相当于 Go struct 嵌入的 JSON 等价物。
//     除非显式指定 JSON 名称，否则 Go 嵌入字段会被隐式内联。
//     被内联的字段必须是 Go struct
//     （不实现任何 JSON 方法）、[jsontext.Value]、
//     map[~string]T，或指向这些类型的未命名指针。在 marshaling 时，
//     来自指针类型的内联字段如果为 nil 则会被省略。
//     类型为 [jsontext.Value] 和 map[~string]T 的内联字段被称为
//     “内联回退字段”，因为它们可以表示父 struct 未直接处理的
//     所有可能的 JSON 对象成员。
//     一个 struct 中只能指定一个内联回退字段，
//     但可以指定许多非回退字段。该选项
//     不得与任何其他选项（包括 JSON 名称）一起指定。
//
//   - unknown："unknown" 选项是内联回退的一种特殊变体，
//     用于表明此 Go struct 字段包含任意数量的未知 JSON 对象成员。
//     字段类型必须是 [jsontext.Value]、map[~string]T，
//     或指向这些类型的未命名指针。
//     如果在 marshaling 时指定了 [DiscardUnknownMembers]，
//     则该字段的内容会被忽略。
//     如果在 unmarshaling 时指定了 [RejectUnknownMembers]，
//     则无论是否存在带 "unknown" 选项的内联回退字段，
//     任何未知对象成员都会被拒绝。该选项
//     不得与任何其他选项（包括 JSON 名称）一起指定。
//
//   - format："format" 选项指定一个格式标志，
//     用于特化字段值的格式化。
//     该选项是一个键值对，形式为 "format:value"，其中
//     值必须是由字母和数字组成的字面量
//     （例如 "format:RFC3339"）或单引号字符串字面量
//     （例如 "format:'2006-01-02'"）。格式标志的解释
//     由 struct 字段类型决定。
//
// "omitzero" 和 "omitempty" 选项在语义上大体相同。
// 前者依据 Go 类型系统定义，
// 而后者依据 JSON 类型系统定义。
// 因此它们在某些情况下行为不同。
// 例如，在 "omitzero" 下只有 nil slice 或 map 会被省略，而在
// "omitempty" 下无论是否为 nil，空 slice 或 map 都会被省略。
// "omitzero" 选项适用于具有明确定义零值的类型
// （例如 [net/netip.Addr]）或具有 IsZero 方法的类型（例如 [time.Time.IsZero]）。
//
// 每个 Go struct 都对应一个可表示为 JSON 的字段列表，
// 该列表通过对所有 struct 字段（不包括未导出或被忽略的字段）
// 执行广度优先搜索构建，
// 其中搜索会递归进入被内联的 struct。
// struct 中非内联字段的集合必须具有唯一的 JSON 名称。
// 如果多个字段都具有相同的 JSON 名称，则
// 位于最浅深度的那一个优先，位于更深深度的其他字段
// 会被排除在可表示为 JSON 的字段列表之外。
// 如果最浅深度处有多个字段具有相同的 JSON 名称，
// 但恰好有一个显式标记了 JSON 名称，
// 则该字段优先，所有其他字段都会被排除在该列表之外。
// 这类似于 Go 中带嵌入 struct 类型的 struct 字段选择的可见性规则。
//
// 对没有任何可表示为 JSON 字段的非空 struct
// 进行 marshaling 或 unmarshaling 会导致 [SemanticError]。
// 未导出字段不得有任何 `json` 标签，`json:"-"` 除外。
//
// # 安全注意事项
//
// JSON 经常被用作数据交换格式，以在不同系统之间通信，
// 这些系统可能以不同的语言实现。
// 出于互操作性和安全性考虑，重要的是
// 所有实现都就数据的语义含义达成一致。
//
// [For example, suppose we have two micro-services.]
// 第一个服务负责对 JSON 请求进行身份验证，
// 而第二个服务负责执行该请求
// （假定先前的服务已经对该请求进行了身份验证）。
// 如果攻击者能够恶意构造一个 JSON 请求，使得
// 两个服务都认为同一个请求来自不同的用户，
// 那么它就可以用一个用户的有效凭据绕过身份验证器，
// 却代表另一个用户恶意执行操作。
//
// 根据 RFC 8259，不幸的是存在许多在语法上有效
// 但在语义上存在歧义的 JSON 文本。
// 例如，该标准没有定义如何解释对象中的重复名称。
//
// v1 [encoding/json] 和 [encoding/json/v2] 包
// 会以不同的方式解释某些输入。具体而言：
//
//   - 标准规定 JSON 必须使用 UTF-8 编码。
//     默认情况下，v1 会将 JSON 字符串中无效的 UTF-8 字节
//     替换为 Unicode 替换字符，
//     而 v2 会拒绝包含无效 UTF-8 的输入。
//     要更改默认行为，请指定 [jsontext.AllowInvalidUTF8] 选项。
//     替换无效 UTF-8 是一种数据损坏形式，
//     会改变字符串的精确含义。
//
//   - 标准没有规定在 JSON 对象中遇到重复名称时的
//     特定行为，这意味着不同实现可能行为不同。
//     默认情况下，v1 允许存在重复名称，
//     而 v2 会拒绝重复名称。
//     要更改默认行为，请指定 [jsontext.AllowDuplicateNames] 选项。
//     如果允许，对象成员会按其被观察到的顺序处理，
//     这意味着后面的值会替换或合并到前面的值中，
//     具体取决于 Go 值类型。
//
//   - 标准将 JSON 对象定义为名称/值对的无序集合。
//     虽然可以通过底层 [jsontext] API 观察到顺序，
//     但 v1 和 v2 通常都避免暴露该顺序。
//     任何应用都不应在语义上依赖对象成员的顺序。
//     允许重复名称是成员顺序可能被意外观察和依赖的
//     一条途径。
//
//   - 标准建议 JSON 对象名称通常基于
//     Unicode 码点序列的相等性进行比较，
//     这意味着比较名称通常是区分大小写的。
//     在将 JSON 对象 unmarshal 到 Go struct 时，
//     默认情况下，v1 使用（宽松的）不区分大小写的名称匹配，
//     而 v2 使用（严格的）区分大小写的名称匹配。
//     要更改默认行为，请指定 [MatchCaseInsensitiveNames] 选项。
//     使用不区分大小写的匹配提供了另一条
//     可能产生重复名称的途径。允许不区分大小写的匹配
//     意味着 v1 或 v2 可能以与大多数其他 JSON 实现
//     （通常使用区分大小写的匹配）不同的方式解释 JSON 对象。
//
//   - 标准没有规定在 JSON 对象中遇到
//     未知名称时的特定行为。
//     在将 JSON 对象 unmarshal 到 Go struct 时，默认情况下
//     v1 和 v2 都会忽略未知名称及其对应的值。
//     要更改默认行为，请指定 [RejectUnknownMembers] 选项。
//
//   - 标准建议实现可以使用 float64
//     来表示 JSON 数字。因此，较大的 JSON 整数
//     在存储为浮点类型时可能会丢失精度。
//     v1 和 v2 在对具体整数类型进行 marshaling 和 unmarshaling 时
//     都能正确地保留精度。然而，即使 v1 和 v2
//     对具体类型保留了精度，其他 JSON 实现
//     也可能无法为 v1 或 v2 产生的输出保留精度。
//     可以使用 `string` 标签选项来指定将整数类型
//     在 JSON 字符串中加引号，以避免精度丢失。
//     此外，当 unmarshal 到 any 接口值时，v1 和 v2 仍可能丢失精度，
//     因为 unmarshal 默认使用 float64
//     来表示 JSON 数字。
//     要更改默认行为，请指定 [WithUnmarshalers] 选项
//     并提供一个自定义 unmarshaler，用能够保留精度的
//     具体 Go 类型预填充接口值。
//
// RFC 8785 为任何 JSON 文本指定了一种规范形式，
// 它明确定义了 RFC 8259 未定义的特定行为。
// 理论上，如果一个文本能够成功地执行 [jsontext.Value.Canonicalize]
// 而不改变数据的语义含义，那么它就能提供
// 更高的信心，表明数据更安全、更具互操作性。
//
// v2 API 通常比 v1 选择更安全的默认值，
// 但对于大整数或未知成员仍应谨慎处理。
//
// [For example, suppose we have two micro-services.]: https://www.youtube.com/watch?v=avilmOcHKHE&t=1057s
package json

// requireKeyedLiterals 可以嵌入到 struct 中以要求使用带键字面量。
type requireKeyedLiterals struct{}

// nonComparable 可以嵌入到 struct 中以阻止可比较性。
type nonComparable [0]func()
