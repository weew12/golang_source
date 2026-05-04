// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package template 实现了用于生成文本输出的数据驱动模板。

要生成 HTML 输出，请参阅 [html/template]，它具有与此包相同的接口，
但会自动保护 HTML 输出免受某些攻击。

模板通过将其应用于数据结构来执行。模板中的注解引用数据结构的元素
（通常是结构体的字段或 map 的键）来控制执行并导出要显示的值。
模板的执行遍历结构体并设置游标，用句点 '.' 表示，称为"点"(dot)，
随着执行进行，将其设置为结构体中当前位置的值。

此包使用的安全模型假定模板作者是可信的。该包不会自动转义输出，
因此如果将代码注入模板并由不受信任的源执行，可能导致任意代码执行。

模板的输入文本是任意格式的 UTF-8 编码文本。"动作"--数据求值或控制结构--
由 "{{" 和 "}}" 定界；动作外的所有文本原样复制到输出。

解析后，模板可以安全地并行执行，尽管如果并行执行共享一个 Writer，
输出可能会交错。

下面是一个打印 "17 items are made of wool" 的简单示例。

	type Inventory struct {
		Material string
		Count    uint
	}
	sweaters := Inventory{"wool", 17}
	tmpl, err := template.New("test").Parse("{{.Count}} items are made of {{.Material}}")
	if err != nil { panic(err) }
	err = tmpl.Execute(os.Stdout, sweaters)
	if err != nil { panic(err) }

更多复杂的示例如下。

文本和空格

默认情况下，执行模板时，动作之间的所有文本都会被原样复制。
例如，上面示例中的字符串 " items are made of " 在程序运行时出现在标准输出中。

但是，为了帮助格式化模板源代码，如果动作的左分隔符（默认 "{{"）
后紧跟减号和空白，则会从紧邻的前一个文本中修剪所有尾随空白。
类似地，如果右分隔符（"}}"）前有空白和减号，则会从紧接的后续文本中
修剪所有前导空白。在这些修剪标记中，空白必须存在：
"{{- 3}}" 类似于 "{{3}}"，但会修剪紧邻的前一个文本，而
"{{-3}}" 解析为包含数字 -3 的动作。

例如，执行源为

	"{{23 -}} < {{- 45}}"

的模板时，生成的输出为

	"23<45"

对于此修剪，空白字符的定义与 Go 中相同：空格、水平制表符、回车符和换行符。

动作

以下是动作列表。"参数"和"管道"是对数据的求值，详情见相应章节。

*/
//	{{/* a comment */}}
//	{{- /* a comment with white space trimmed from preceding and following text */ -}}
//		注释；被丢弃。可能包含换行符。
//		注释不嵌套，必须像这里所示的那样在分隔符处开始和结束。
/*

	{{pipeline}}
		管道值的默认文本表示（与 fmt.Print 打印的相同）被复制到输出。

	{{if pipeline}} T1 {{end}}
		如果管道的值为空，则不生成输出；否则执行 T1。
		空值包括 false、0、任何 nil 指针或接口值，以及任何长度为零的数组、切片、映射或字符串。
		点不受影响。

	{{if pipeline}} T1 {{else}} T0 {{end}}
		如果管道的值为空，则执行 T0；否则执行 T1。点不受影响。

	{{if pipeline}} T1 {{else if pipeline}} T0 {{end}}
		为了简化 if-else 链的外观，if 的 else 动作可以直接包含另一个 if；
		效果与编写
			{{if pipeline}} T1 {{else}}{{if pipeline}} T0 {{end}}{{end}}
		完全相同。

	{{range pipeline}} T1 {{end}}
		管道的值必须是数组、切片、映射、iter.Seq、iter.Seq2、整数或通道。
		如果管道的值为零长度，则不产生输出；否则，点被设置为数组、切片或映射的连续元素，
		并执行 T1。如果值是映射且键是具有定义顺序的基本类型，则元素将按排序的键顺序遍历。

	{{range pipeline}} T1 {{else}} T0 {{end}}
		管道的值必须是数组、切片、映射、iter.Seq、iter.Seq2、整数或通道。
		如果管道的值为零长度，则点不受影响并执行 T0；
		否则，点被设置为数组、切片或映射的连续元素并执行 T1。

	{{break}}
		提前结束最内层的 {{range pipeline}} 循环，停止当前迭代并绕过所有剩余迭代。

	{{continue}}
		停止最内层 {{range pipeline}} 循环的当前迭代，并开始下一次迭代。

	{{template "name"}}
		执行具有指定名称的模板，数据为 nil。

	{{template "name" pipeline}}
		执行具有指定名称的模板，点设置为管道的值。

	{{block "name" pipeline}} T1 {{end}}
		block 是定义模板的简写
			{{define "name"}} T1 {{end}}
		然后在原地执行它
			{{template "name" pipeline}}
		典型用途是定义一组根模板，然后通过在内部重新定义 block 模板来自定义。

	{{with pipeline}} T1 {{end}}
		如果管道的值为空，则不生成输出；否则将点设置为管道的值并执行 T1。

	{{with pipeline}} T1 {{else}} T0 {{end}}
		如果管道的值为空，则点不受影响并执行 T0；
		否则将点设置为管道的值并执行 T1。

	{{with pipeline}} T1 {{else with pipeline}} T0 {{end}}
		为了简化 with-else 链的外观，with 的 else 动作可以直接包含另一个 with；
		效果与编写
			{{with pipeline}} T1 {{else}}{{with pipeline}} T0 {{end}}{{end}}
		完全相同。


参数

参数是一个简单值，由以下之一表示。

	- Go 语法中的布尔值、字符串、字符、整数、浮点数、虚数或复数常量。
	  这些行为类似于 Go 的无类型常量。注意，与 Go 中一样，
	  大整数常量在赋值或传递给函数时是否溢出取决于宿主机的 ints 是 32 位还是 64 位。
	- 关键字 nil，表示无类型的 Go nil。
	- 字符 '.'（句点）：

		.

	  结果是点的值。
	- 变量名，即前面带美元符号的（可能为空的）字母数字字符串，例如

		$piOver2

	  或

		$

	  结果是变量的值。变量在下面描述。
	- 数据字段的名称，必须是结构体，前面带句点，例如

		.Field

	  结果是字段的值。字段调用可以链式调用：

	    .Field1.Field2

	  字段也可以在变量上求值，包括链式调用：

	    $x.Field1.Field2
	- 数据的键的名称，必须是映射，前面带句点，例如

		.Key

	  结果是按键索引的映射元素值。
	  键调用可以链式调用并与字段组合到任意深度：

	    .Field1.Key1.Field2.Key2

	  虽然键必须是字母数字标识符，但与字段名不同，它们不需要以大写字母开头。
	  键也可以在变量上求值，包括链式调用：

	    $x.key1.key2
	- 数据的无参数方法的名称，前面带句点，例如

		.Method

	  结果是使用点作为接收器调用方法的结果，dot.Method()。
	  这样的方法必须有一个返回值（任何类型）或两个返回值，
	  第二个返回值是错误。如果有两个返回值且返回的错误非空，
	  执行终止并将错误作为 Execute 的返回值返回。
	  方法调用可以链式调用并与字段和键组合到任意深度：

	    .Field1.Key1.Method1.Field2.Key2.Method2

	  方法也可以在变量上求值，包括链式调用：

	    $x.Method1.Field
	- 无参数函数的名称，例如

		fun

	  结果是调用函数 fun() 的值。返回类型和值与方法中相同。
	  函数和函数名在下面描述。
	- 上述之一的带括号实例，用于分组。结果可以通过字段或映射键调用访问。

		print (.F1 arg1) (.F2 arg2)
		(.StructValuedMethod "arg").Field

参数可以求值为任何类型；如果它们是指针，实现会在需要时自动间接到基类型。
如果求值产生函数值，例如结构体的函数值字段，该函数不会自动调用，
但可以用作 if 动作等的真值。要调用它，请使用下面定义的 call 函数。

管道

管道是一个可能链接的"命令"序列。命令是一个简单值（参数）或函数或方法调用，
可能有多个参数：

	Argument
		结果是求值参数的值。
	.Method [Argument...]
		该方法可以单独使用或作为链的最后一个元素，
		但与链中间的方法不同，它可以接受参数。
		结果是用参数调用方法的值：
			dot.Method(Argument1, etc.)
	functionName [Argument...]
		结果是使用名称调用关联函数的值：
			function(Argument1, etc.)
		函数和函数名在下面描述。

管道可以通过管道字符 '|' 分隔一系列命令来"链接"。
在链接管道中，每个命令的结果作为最后一个参数传递给下一个命令。
管道中最后一个命令的输出是管道的值。

命令的输出可以是一个值或两个值，第二个值的类型为 error。
如果第二个值存在且求值非空，执行终止并将错误返回给 Execute 的调用者。

变量

动作中的管道可以初始化一个变量来捕获结果。
初始化语法为

	$variable := pipeline

其中 $variable 是变量名。声明变量的动作不产生输出。

先前声明的变量也可以使用语法赋值

	$variable = pipeline

如果"range"动作初始化了一个变量，该变量被设置为迭代的连续元素。
此外，"range"可以声明两个变量，用逗号分隔：

	range $index, $element := pipeline

在这种情况下，$index 和 $element 分别被设置为数组/切片索引或映射键和元素的连续值。
注意，如果只有一个变量，它被分配元素；这与 Go range 子句中的约定相反。

变量的作用域扩展到控制结构（"if"、"with"或"range"）的"end"动作，
或者如果没有这样的控制结构，则扩展到模板的末尾。
模板调用不会从其调用点继承变量。

当执行开始时，$ 被设置为传递给 Execute 的数据参数，即点的起始值。

示例

这里有一些演示管道和变量的单行模板示例。
所有都产生引用的单词 "output"：

	{{"\"output\""}}
		字符串常量。
	{{`"output"`}}
		原始字符串常量。
	{{printf "%q" "output"}}
		函数调用。
	{{"output" | printf "%q"}}
		函数调用，其最终参数来自上一个命令。
	{{printf "%q" (print "out" "put")}}
		带括号的参数。
	{{"put" | printf "%s%s" "out" | printf "%q"}}
		更复杂的调用。
	{{"output" | printf "%s" | printf "%q"}}
		更长的链。
	{{with "output"}}{{printf "%q" .}}{{end}}
		使用点的 with 动作。
	{{with $x := "output" | printf "%q"}}{{$x}}{{end}}
		创建和使用变量的 with 动作。
	{{with $x := "output"}}{{printf "%q" $x}}{{end}}
		在另一个动作中使用变量的 with 动作。
	{{with $x := "output"}}{{$x | printf "%q"}}{{end}}
		相同的，但使用管道。

函数

在执行期间，函数在两个函数映射中找到：首先在模板中，然后在全局函数映射中。
默认情况下，模板中没有定义函数，但可以使用 Funcs 方法添加它们。

预定义的全局函数如下命名。

	and
		通过返回第一个空参数或最后一个参数来返回其参数的布尔 AND。
		也就是说，"and x y" 的行为类似于 "if x then y else x"。
		求值从左到右进行，并在确定结果时返回。
	call
		返回调用第一个参数（必须是函数）的结果，其余参数作为参数。
		因此 "call .X.Y 1 2" 在 Go 表示法中是 dot.X.Y(1, 2)，
		其中 Y 是函数值字段、映射条目或类似的东西。
		第一个参数必须是求值产生函数类型值的结果（与 print 之类的预定义函数不同）。
		函数必须返回一个或两个结果值，第二个结果的类型为 error。
		如果参数不匹配或返回的错误值非空，执行停止。
	html
		返回其参数文本表示的转义 HTML 等效值。
		此函数在 html/template 中不可用，有几个例外。
	index
		返回通过以下参数索引其第一个参数的结果。
		因此 "index x 1 2 3" 在 Go 语法中是 x[1][2][3]。
		每个索引项必须是映射、切片或数组。
	slice
		slice 返回用其余参数切片其第一个参数的结果。
		因此 "slice x 1 2" 在 Go 语法中是 x[1:2]，
		而 "slice x" 是 x[:]，"slice x 1" 是 x[1:]，
		"slice x 1 2 3" 是 x[1:2:3]。第一个参数必须是字符串、切片或数组。
	js
		返回其参数文本表示的转义 JavaScript 等效值。
	len
		返回其参数的整数长度。
	not
		返回其单个参数的布尔否定。
	or
		通过返回第一个非空参数或最后一个参数来返回其参数的布尔 OR，
		也就是说，"or x y" 的行为类似于 "if x then x else y"。
		求值从左到右进行，并在确定结果时返回。
	print
		fmt.Sprint 的别名。
	printf
		fmt.Sprintf 的别名。
	println
		fmt.Sprintln 的别名。
	urlquery
		返回其参数文本表示的转义值，适用于嵌入 URL 查询。
		此函数在 html/template 中不可用，有几个例外。

布尔函数将任何零值视为 false，非零值视为 true。

还有一组定义为函数的二元比较运算符：

	eq
		返回 arg1 == arg2 的布尔真值。
	ne
		返回 arg1 != arg2 的布尔真值。
	lt
		返回 arg1 < arg2 的布尔真值。
	le
		返回 arg1 <= arg2 的布尔真值。
	gt
		返回 arg1 > arg2 的布尔真值。
	ge
		返回 arg1 >= arg2 的布尔真值。

为了更简单的多路相等测试，eq（仅）接受两个或更多参数，
并将后续参数与第一个参数比较，实际上返回

	arg1==arg2 || arg1==arg3 || arg1==arg4 ...

（然而，与 Go 中的 || 不同，eq 是函数调用，所有参数都将被求值。）

比较函数适用于 Go 定义为可比较的任何值。
对于基本类型（如整数），规则放宽：忽略大小和确切类型，
因此任何整数值（有符号或无符号）都可以与任何其他整数值进行比较。
（比较的是算术值，而不是位模式，因此所有负整数都小于所有无符号整数。）
但是，与往常一样，不能将 int 与 float32 等进行比较。

关联模板

每个模板都有一个创建时指定的字符串名称。此外，每个模板都与零个或多个其他模板关联，
它可以通过名称调用这些模板；这种关联是传递的，形成模板的名称空间。

模板可以使用模板调用来实例化另一个关联模板；请参阅上面"template"动作的说明。
名称必须是包含调用的模板关联的模板之一。

嵌套模板定义

解析模板时，可以定义并关联正在解析的模板。
模板定义必须出现在模板的顶层，类似于 Go 程序中的全局变量。

此类定义的语法是用"define"和"end"动作包围每个模板声明。

define 动作通过提供字符串常量来命名正在创建的模板。
这是一个简单的例子：

	{{define "T1"}}ONE{{end}}
	{{define "T2"}}TWO{{end}}
	{{define "T3"}}{{template "T1"}} {{template "T2"}}{{end}}
	{{template "T3"}}

这定义了两个模板 T1 和 T2，以及一个在执行时调用其他两个的 T3。
最后它调用 T3。如果执行此模板将产生文本

	ONE TWO

根据构造，模板只能存在于一个关联中。如果需要让模板可从多个关联寻址，
则必须多次解析模板定义以创建不同的 *Template 值，
或者必须使用 [Template.Clone] 或 [Template.AddParseTree] 复制。

可以多次调用 Parse 来组装各种关联模板；
请参阅 [ParseFiles]、[ParseGlob]、[Template.ParseFiles] 和 [Template.ParseGlob]
获取解析存储在文件中的相关模板的简单方法。

模板可以直接执行，也可以通过 [Template.ExecuteTemplate] 执行，
后者执行按名称识别的关联模板。
要调用上面的示例，我们可以写

	err := tmpl.Execute(os.Stdout, "no data needed")
	if err != nil {
		log.Fatalf("execution failed: %s", err)
	}

或者通过名称显式调用特定模板

	err := tmpl.ExecuteTemplate(os.Stdout, "T2", "no data needed")
	if err != nil {
		log.Fatalf("execution failed: %s", err)
	}

*/
package template
