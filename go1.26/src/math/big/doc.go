// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package big 实现任意精度算术（大数）。
支持以下数值类型：

	Int    有符号整数
	Rat    有理数
	Float  浮点数

[Int]、[Rat] 或 [Float] 的零值对应于 0。因此，可以按通常方式声明新值，
它们无需进一步初始化即可表示 0：

	var x Int        // &x is an *Int of value 0
	var r = &Rat{}   // r is a *Rat of value 0
	y := new(Float)  // y is a *Float of value 0

此外，也可以使用如下形式的工厂函数来分配并初始化新值：

	func NewT(v V) *T

例如，[NewInt](x) 返回一个 *[Int]，其被设为 int64 参数 x 的值；[NewRat](a, b)
返回一个 *[Rat]，其被设为分数 a/b，其中 a 和 b 是 int64 值；[NewFloat](f)
返回一个 *[Float]，其被初始化为 float64 参数 f。通过显式的 setter 可以
提供更大的灵活性，例如：

	var z1 Int
	z1.SetUint64(123)                 // z1 := 123
	z2 := new(Rat).SetFloat64(1.25)   // z2 := 5/4
	z3 := new(Float).SetInt(z1)       // z3 := 123.0

Setter、数值运算和谓词表示为如下形式的方法：

	func (z *T) SetV(v V) *T          // z = v
	func (z *T) Unary(x *T) *T        // z = unary x
	func (z *T) Binary(x, y *T) *T    // z = x binary y
	func (x *T) Pred() P              // p = pred(x)

其中 T 是 [Int]、[Rat] 或 [Float] 之一。对于一元和二元运算，结果是接收者
（在那种情况下通常命名为 z；见下文）；如果它是操作数 x 或 y 之一，则可以
安全地覆盖它（并重用其内存）。

算术表达式通常写成一连串单独的方法调用，每次调用对应一个运算。接收者
表示结果，方法参数是运算的操作数。例如，给定三个 *Int 值 a、b 和 c，
调用

	c.Add(a, b)

计算和 a + b 并将结果存储在 c 中，覆盖 c 之前保存的任何值。除非另有
说明，运算允许参数重叠（别名），因此完全可以这样写

	sum.Add(sum, x)

将值 x 累加到 sum 中。

（通过始终经由接收者传入结果值，可以更好地控制内存使用。运算无需为每个
结果分配新内存，而是可以重用为结果值分配的空间，并在过程中用新结果覆盖
该值。）

命名约定：传入的方法参数（包括接收者）在 API 中的命名保持一致，以澄清
其用途。传入的操作数通常命名为 x、y、a、b 等等，但绝不命名为 z。指定
结果的参数命名为 z（通常是接收者）。

例如，(*Int).Add 的参数命名为 x 和 y，而由于接收者指定了结果的目标位置，
它被称为 z：

	func (z *Int) Add(x, y *Int) *Int

这种形式的方法通常也会返回传入的接收者，以便进行简单的调用链式组合。

不需要传入结果值的方法（例如 [Int.Sign]）直接返回结果。在这种情况下，
接收者通常是第一个操作数，命名为 x：

	func (x *Int) Sign() int

各种方法支持字符串与相应数值之间的相互转换：*[Int]、*[Rat] 和 *[Float]
值实现了 Stringer 接口以提供值的（默认）字符串表示，但也提供了 SetString
方法以从多种受支持格式的字符串初始化值（参见各自的 SetString 文档）。

最后，*[Int]、*[Rat] 和 *[Float] 满足 [fmt.Scanner] 以进行扫描，并且
（除 *[Rat] 外）满足 Formatter 接口以进行格式化打印。
*/
package big
