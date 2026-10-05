// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package gob 管理 gob 流——在 [Encoder]（发送方）与 [Decoder]（接收方）之间交换的
二进制值。一种典型用途是传输远程过程调用（RPC，例如 [net/rpc] 所提供的那些）
的参数和结果。

该实现会为流中的每种数据类型编译一个定制的编解码器，并且当使用单个
[Encoder] 来传输一个值流时效率最高，因为这样可以摊薄编译成本。

# 基础

gob 流是自描述的。流中的每个数据项前面都带有其类型的说明，该说明以一小
组预定义类型来表示。指针不会被传输，但它们所指向的内容会被传输；也就是
说，值会被展平。nil 指针不被允许，因为它们没有值。递归类型工作良好，但
递归值（带环的数据）则存在问题。这一点可能会改变。

要使用 gob，请创建一个 [Encoder]，并向它依次提供一系列数据项，这些数据
项可以是值，也可以是可解引用为值的地址。[Encoder] 会确保所有类型信息在
需要之前就已发送。在接收端，[Decoder] 从编码后的流中取回值，并将它们解包
到局部变量中。

# 类型与值

源和目标的值/类型不必完全对应。对于结构体，源中存在但接收变量中不存在的
字段（按名称识别）将被忽略。接收变量中存在但传输的类型或值中缺失的字段，
在目标中将被忽略。如果两边都存在同名字段，它们的类型必须兼容。接收方和发送
方都会执行所有必要的间接寻址和解引用，以便在 gob 与实际的 Go 值之间进行
转换。例如，一个示意上为

	struct { A, B int }

的 gob 类型，可以从以下任一 Go 类型发送或接收进：

	struct { A, B int }	// the same
	*struct { A, B int }	// extra indirection of the struct
	struct { *A, **B int }	// extra indirection of the fields
	struct { A, B int64 }	// different concrete value type; see below

它也可以被接收进以下任一类型：

	struct { A, B int }	// the same
	struct { B, A int }	// ordering doesn't matter; matching is by name
	struct { A, B, C int }	// extra field (C) ignored
	struct { B int }	// missing field (A) ignored; data will be dropped
	struct { B, C int }	// missing field (A) ignored; extra field (C) ignored.

尝试接收进以下类型将引发解码错误：

	struct { A int; B uint }	// change of signedness for B
	struct { A int; B float }	// change of type for B
	struct { }			// no field names in common
	struct { C, D int }		// no field names in common

整数以两种方式传输：任意精度的有符号整数，或任意精度的无符号整数。gob 格式
中不区分 int8、int16 等；只有有符号整数和无符号整数。如下文所述，发送方以
变长编码发送值；接收方接受该值并将其存入目标变量。浮点数始终使用 IEEE 754
64 位精度发送（见下文）。

有符号整数可以被接收进任何有符号整数变量：int、int16 等；无符号整数可以被
接收进任何无符号整数变量；浮点值可以被接收进任何浮点变量。不过，目标变量
必须能够表示该值，否则解码操作将失败。

结构体、数组和切片也受支持。结构体只编码和解码已导出字段。字符串和字节数组
以一种特殊的、高效的表示形式受支持（见下文）。解码切片时，如果现有切片具有
容量，则该切片会被原地扩展；如果没有，则分配一个新数组。无论如何，结果切片
的长度都会报告已解码的元素个数。

一般而言，如果需要分配，解码器将分配内存。如果不需要，它会用从流中读取的值
更新目标变量。它不会先初始化这些变量，因此如果目标是 map、结构体或切片这类
复合值，解码得到的值将按元素合并进现有变量中。

函数和通道不会被发送到 gob 中。尝试在顶层编码这样的值将失败。chan 或 func
类型的结构体字段会被完全当作未导出字段处理，并被忽略。

Gob 可以通过调用相应方法，对任何实现了 [GobEncoder] 或
[encoding.BinaryMarshaler] 接口的值进行编码，按上述优先级顺序。

Gob 可以通过调用相应方法，对任何实现了 [GobDecoder] 或
[encoding.BinaryUnmarshaler] 接口的值进行解码，同样按上述优先级顺序。

# 编码细节

本节记录编码细节，这些细节对大多数用户并不重要。细节按自底向上的顺序呈现。

无符号整数以两种方式之一发送。如果它小于 128，就作为带有该值的一个字节发送。
否则，就作为以最小长度的大端序（高字节在前）字节流保存该值来发送，其前面是
一个保存字节数取负后的字节。因此 0 被传输为 (00)，7 被传输为 (07)，256 被
传输为 (FE 01 00)。

布尔值编码在一个无符号整数之内：0 表示 false，1 表示 true。

有符号整数 i 编码在一个无符号整数 u 之内。在 u 中，从第 1 位起往上保存该值；
第 0 位表示在接收时它们是否应当被取反。编码算法如下：

	var u uint
	if i < 0 {
		u = (^uint(i) << 1) | 1 // complement i, bit 0 is 1
	} else {
		u = (uint(i) << 1) // do not complement i, bit 0 is 0
	}
	encodeUnsigned(u)

因此，低位类似于符号位，但把它改为取反位则保证了最大的负整数不是特例。例如，
-129=^128=(^256>>1) 编码为 (FE 01 01)。

浮点数始终以 float64 值的表示形式发送。该值使用 [math.Float64bits] 转换为
uint64。然后该 uint64 被字节反转，并作为常规无符号整数发送。字节反转意味着
指数和尾数的高精度部分先出现。由于低位常常为零，这可以节省编码字节。例如，
17.0 只编码为三个字节 (FE 31 40)。

字符串和字节切片作为无符号计数后跟该数量的未经解释的值字节发送。

所有其他切片和数组都作为无符号计数后跟该数量的元素发送，元素使用其类型的
标准 gob 编码递归地进行编码。

Map 作为无符号计数后跟该数量的键、元素对发送。空的但非 nil 的 map 也会被
发送，因此如果接收方尚未分配一个 map，那么在接收时总会分配一个，除非所
传输的 map 为 nil 且不在顶层。

在切片和数组中，以及在 map 中，所有元素——即使是零值元素——都会被传输，即使
所有元素都为零。

结构体作为一系列（字段号，字段值）对发送。字段值使用其类型的标准 gob 编码
递归地发送。如果某个字段具有其类型的零值（数组除外；见上文），或者它是指向
零值的指针，则会从传输中省略。字段号由被编码结构体的类型定义：被编码类型的
第一个字段是字段 0，第二个是字段 1，依此类推。编码某个值时，字段号为效率而
采用增量编码，且字段总是按字段号递增的顺序发送；因此增量是无符号的。增量
编码的初始化将字段号设为 -1，因此一个值为 7 的无符号整数字段 0 被传输为
无符号增量 = 1、无符号值 = 7，即 (01 07)。最后，在所有字段都发送完之后，一个
终止标记表示结构体的结束。该标记是一个 delta=0 的值，其表示为 (00)。

接口类型不进行兼容性检查；在传输时，所有接口类型都被当作单一 "interface"
类型的成员处理，类似于 int 或 []byte ——实际上它们都被当作 interface{}。
接口值作为这样一个字符串发送：它标识正在发送的具体类型（该名称必须通过调用
[Register] 预先定义），后跟随后数据长度的字节计数（以便在无法存储该值时
跳过它），再后跟存储在接口值中的具体（动态）值的常规编码。（nil 接口值由空
字符串标识，且不传输任何值。）接收时，解码器验证解包出的具体项是否满足接收
变量的接口。

如果某个值被传给 [Encoder.Encode]，且其类型不是结构体（或指向结构体的指针等），
为简化处理，它会被表示为一个含单个字段的结构体。唯一可见的效果是在该值之后
编码一个零字节，就像在编码后的结构体的最后一个字段之后那样，以便解码算法
知道顶层值何时结束。

类型的表示如下所述。当某个类型在 [Encoder] 与 [Decoder] 之间的给定连接上被
定义时，它会被分配一个有符号整数类型 id。当调用 [Encoder.Encode](v) 时，它
会确保为 v 的类型及其所有元素都分配了 id，然后发送对 (typeid, encoded-v)，
其中 typeid 是 v 被编码类型的类型 id，encoded-v 是值 v 的 gob 编码。

要定义类型，编码器选择一个未使用的正类型 id，并发送对 (-type id, encoded-type)，
其中 encoded-type 是 wireType 描述的 gob 编码，由以下类型构造而来：

	type wireType struct {
		ArrayT           *arrayType
		SliceT           *sliceType
		StructT          *structType
		MapT             *mapType
		GobEncoderT      *gobEncoderType
		BinaryMarshalerT *gobEncoderType
		TextMarshalerT   *gobEncoderType
	}
	type arrayType struct {
		CommonType
		Elem typeId
		Len  int
	}
	type CommonType struct {
		Name string // the name of the struct type
		Id  int    // the id of the type, repeated so it's inside the type
	}
	type sliceType struct {
		CommonType
		Elem typeId
	}
	type structType struct {
		CommonType
		Field []fieldType // the fields of the struct.
	}
	type fieldType struct {
		Name string // the name of the field.
		Id   int    // the type id of the field, which must be already defined
	}
	type mapType struct {
		CommonType
		Key  typeId
		Elem typeId
	}
	type gobEncoderType struct {
		CommonType
	}

如果存在嵌套的类型 id，那么所有内部类型 id 的类型必须在顶层类型 id 被用于
描述 encoded-v 之前定义好。

为简化设置，连接被定义为先验地理解这些类型，以及基本的 gob 类型 int、uint
等。它们的 id 为：

	bool        1
	int         2
	uint        3
	float       4
	[]byte      5
	string      6
	complex     7
	interface   8
	// 为保留的 id 留出的间隙。
	WireType    16
	ArrayType   17
	CommonType  18
	SliceType   19
	StructType  20
	FieldType   21
	// 22 是 fieldType 的切片。
	MapType     23

最后，Encode 调用所创建的每条消息前面都带有一个经过编码的无符号整数，表示
消息中剩余字节的数量。在初始类型名之后，接口值也以同样的方式包装；实际上，
接口值就像是对 Encode 的递归调用。

总之，一个 gob 流看起来像

	(byteCount (-type id, encoding of a wireType)* (type id, encoding of a value))*

其中 * 表示零次或多次重复，并且某个值的类型 id 必须已预定义，或在流中于该值
之前定义好。

兼容性：本包未来的任何变更都将尽力保持与使用先前版本编码的流的兼容性。也就是
说，本包任何已发布的版本都应能够解码用任何先前已发布版本写入的数据，但受安全
修复之类问题的影响。背景参见 Go 兼容性文档：https://golang.org/doc/go1compat

关于 gob 线格式的设计讨论，参见 "Gobs of data"：
https://go.dev/blog/gob

# 安全性

本包并非设计用来抵御对抗性输入，且不在 https://go.dev/security/policy 的
范围内。尤其是，[Decoder] 对解码所得的输入大小只做基本的健全性检查，且其限制
不可配置。在解码来自不可信来源的 gob 数据时应小心，此类数据可能会消耗大量
资源。
*/
package gob

/*
Grammar:

以小写字母开头的记号是终结符；int(n) 和 uint(n) 表示值 n 的有符号/无符号编码。

GobStream:
	DelimitedMessage*
DelimitedMessage:
	uint(lengthOfMessage) Message
Message:
	TypeSequence TypedValue
TypeSequence
	(TypeDefinition DelimitedTypeDefinition*)?
DelimitedTypeDefinition:
	uint(lengthOfTypeDefinition) TypeDefinition
TypedValue:
	int(typeId) Value
TypeDefinition:
	int(-typeId) encodingOfWireType
Value:
	SingletonValue | StructValue
SingletonValue:
	uint(0) FieldValue
FieldValue:
	builtinValue | ArrayValue | MapValue | SliceValue | StructValue | InterfaceValue
InterfaceValue:
	NilInterfaceValue | NonNilInterfaceValue
NilInterfaceValue:
	uint(0)
NonNilInterfaceValue:
	ConcreteTypeName TypeSequence InterfaceContents
ConcreteTypeName:
	uint(lengthOfName) [already read=n] name
InterfaceContents:
	int(concreteTypeId) DelimitedValue
DelimitedValue:
	uint(length) Value
ArrayValue:
	uint(n) FieldValue*n [n elements]
MapValue:
	uint(n) (FieldValue FieldValue)*n  [n (key, value) pairs]
SliceValue:
	uint(n) FieldValue*n [n elements]
StructValue:
	(uint(fieldDelta) FieldValue)*
*/

/*
供实现者和好奇者参考，这里给出一个编码示例。已知
	type Point struct {X, Y int}
以及值
	p := Point{22, 33}
编码 p 所传输的字节将为：
	1f ff 81 03 01 01 05 50 6f 69 6e 74 01 ff 82 00
	01 02 01 01 58 01 04 00 01 01 59 01 04 00 00 00
	07 ff 82 01 2c 01 42 00
它们按如下方式确定。

由于这是类型 Point 的首次传输，Point 本身的类型描述符必须在值之前发送。
这是我们在该 Encoder 上发送的第一个类型，因此它的类型 id 为 65（0 到 64
被保留）。

	1f	// 本项（一个类型描述符）长 31 字节。
	ff 81	// 我们正在定义的类型的 id 取负，即 -65。
		// 这里是一个字节（由 FF = -1 表示）后跟
		// ^-65<<1 | 1。低位的 1 表示在接收时对
		// 其余部分取反。

	// 现在我们发送一个类型描述符，它本身是一个结构体（wireType）。
	// wireType 本身的类型是已知的（它是内建的，其所有组成部分的类型
	// 也是内建的），因此我们只需发送一个表示类型 "Point" 的 wireType
	// 类型的 *值*。
	// 该值的编码从这里开始。
	// 隐式地将字段号设为 -1；这会在每个结构体（包括嵌套结构体）
	// 的开头进行。
	03	// 给字段号加 3；现在为 2（wireType.structType；这是一个结构体）。
		// structType 以一个内嵌的 CommonType 开头，后者在这里
		// 也表现为一个常规的结构体。
	01	// 给字段号加 1（现在为 0）；内嵌 CommonType 的开始。
	01	// 给字段号加 1（现在为 0，即类型的名称）
	05	// 字符串为（无符号）5 字节长
	50 6f 69 6e 74	// wireType.structType.CommonType.name = "Point"
	01	// 给字段号加 1（现在为 1，即类型的 id）
	ff 82	// wireType.structType.CommonType._id = 65
	00	// 内嵌的 wiretype.structType.CommonType 结构体结束
	01	// 给字段号加 1（现在为 1，即 wireType.structType 中的字段数组）
	02	// 类型中有两个字段（len(structType.field)）
	01	// 第一个字段结构的开始；加 1 得到字段号 0：field[0].name
	01	// 1 字节
	58	// structType.field[0].name = "X"
	01	// 加 1 得到字段号 1：field[0].id
	04	// structType.field[0].typeId 为 2（有符号 int）。
	00	// structType.field[0] 结束；开始 structType.field[1]；将字段号设为 -1。
	01	// 加 1 得到字段号 0：field[1].name
	01	// 1 字节
	59	// structType.field[1].name = "Y"
	01	// 加 1 得到字段号 1：field[1].id
	04	// struct.Type.field[1].typeId 为 2（有符号 int）。
	00	// structType.field[1] 结束；structType.field 结束。
	00	// wireType.structType 结构体结束
	00	// wireType 结构体结束

现在我们可以发送 Point 值了。字段号再次重置为 -1：

	07	// 本值长 7 字节
	ff 82	// 类型编号，65（1 字节（-FF）后跟 65<<1）
	01	// 给字段号加一，得到字段 0
	2c	// 有符号 "22" 的编码（0x2c = 44 = 22<<1）；Point.x = 22
	01	// 给字段号加一，得到字段 1
	42	// 有符号 "33" 的编码（0x42 = 66 = 33<<1）；Point.y = 33
	00	// 结构体结束

类型编码很长且相当复杂，但我们只发送一次。
如果 p 被第二次传输，类型已经已知，因此输出将只是：

	07 ff 82 01 2c 01 42 00

顶层的单个非结构体值像带增量标签 0 的字段一样传输。例如，一个值为 3 的
有符号整数作为 Encode 的参数传入时，将输出：

	03 04 00 06

它表示：

	03	// 本值长 3 字节
	04	// 类型编号 2，表示整数
	00	// 标签增量 0
	06	// 值 3

*/
