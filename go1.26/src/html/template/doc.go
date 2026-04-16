// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package template (html/template) 实现了数据驱动的模板，用于生成防止代码注入的安全 HTML 输出。
它提供与 [text/template] 相同的接口，在输出为 HTML 时应使用本包代替 [text/template]。

本文档重点介绍本包的安全特性。有关如何编写模板本身的信息，请参阅 [text/template] 的文档。

# 简介

本包封装了 [text/template]，使你可以共享其模板 API 来安全地解析和执行 HTML 模板。

	tmpl, err := template.New("name").Parse(...)
	// 省略错误检查
	err = tmpl.Execute(out, data)

如果成功，tmpl 将是注入安全的。否则，err 是 ErrorCode 文档中定义的错误。

HTML 模板将数据值视为纯文本，这些纯文本会被编码以便安全地嵌入 HTML 文档中。
转义是上下文相关的，因此 action 可以出现在 JavaScript、CSS 和 URI 上下文中。

注释会从输出中被移除，但通过 [HTML]、[CSS] 和 [JS] 类型在各自上下文中传入的除外。

本包使用的安全模型假定模板作者是可信的，而 Execute 的 data 参数是不可信的。
更多细节见下文。

示例

	import "text/template"
	...
	t, err := template.New("foo").Parse(`{{define "T"}}Hello, {{.}}!{{end}}`)
	err = t.ExecuteTemplate(out, "T", "<script>alert('you have been pwned')</script>")

产生

	Hello, <script>alert('you have been pwned')</script>!

但 html/template 中的上下文自动转义

	import "html/template"
	...
	t, err := template.New("foo").Parse(`{{define "T"}}Hello, {{.}}!{{end}}`)
	err = t.ExecuteTemplate(out, "T", "<script>alert('you have been pwned')</script>")

产生安全的、转义后的 HTML 输出

	Hello, &lt;script&gt;alert(&#39;you have been pwned&#39;)&lt;/script&gt;!

# 上下文

本包理解 HTML、CSS、JavaScript 和 URI。它会为每个简单的 action 管道添加
净化函数，因此给定以下片段

	<a href="/search?q={{.}}">{{.}}</a>

在解析时，每个 {{.}} 都会被重写以根据需要添加转义函数。在本例中它变为

	<a href="/search?q={{. | urlescaper | attrescaper}}">{{. | htmlescaper}}</a>

其中 urlescaper、attrescaper 和 htmlescaper 是内部转义函数的别名。

对于这些内部转义函数，如果 action 管道的求值结果是 nil 接口值，
则将其视为空字符串。

# 带命名空间和 data- 属性

带有命名空间的属性被视为没有命名空间。给定以下片段

	<a my:href="{{.}}"></a>

在解析时，该属性将被视为只是 "href"。因此在解析时模板变为：

	<a my:href="{{. | urlescaper | attrescaper}}"></a>

类似于带命名空间的属性，带有 "data-" 前缀的属性被视为没有 "data-" 前缀。因此给定

	<a data-href="{{.}}"></a>

在解析时变为

	<a data-href="{{. | urlescaper | attrescaper}}"></a>

如果属性同时具有命名空间和 "data-" 前缀，在确定上下文时只会移除命名空间。例如

	<a my:data-href="{{.}}"></a>

这会被处理为 "my:data-href" 只是 "data-href" 而不是 "href"，
因为如果 "data-" 前缀也被忽略就会变成 "href"。因此在解析时只变为

	<a my:data-href="{{. | attrescaper}}"></a>

作为特殊情况，具有 "xmlns" 命名空间的属性始终被视为包含 URL。给定以下片段

	<a xmlns:title="{{.}}"></a>
	<a xmlns:href="{{.}}"></a>
	<a xmlns:onclick="{{.}}"></a>

在解析时变为：

	<a xmlns:title="{{. | urlescaper | attrescaper}}"></a>
	<a xmlns:href="{{. | urlescaper | attrescaper}}"></a>
	<a xmlns:onclick="{{. | urlescaper | attrescaper}}"></a>

# 错误

详情请参阅 ErrorCode 的文档。

# 更完整的图景

本包注释的其余部分在首次阅读时可以跳过；它包含理解转义上下文和错误消息
所需的细节。大多数用户不需要了解这些细节。

# 上下文

假设 {{.}} 是 `O'Reilly: How are <i>you</i>?`，下表显示了 {{.}} 在左侧上下文中
使用时的显示方式。

	上下文                            {{.}} 之后
	{{.}}                            O'Reilly: How are &lt;i&gt;you&lt;/i&gt;?
	<a title='{{.}}'>                O&#39;Reilly: How are you?
	<a href="/{{.}}">                O&#39;Reilly: How are %3ci%3eyou%3c/i%3e?
	<a href="?q={{.}}">              O&#39;Reilly%3a%20How%20are%3ci%3e...%3f
	<a onx='f("{{.}}")'>             O\x27Reilly: How are \x3ci\x3eyou...?
	<a onx='f({{.}})'>               "O\x27Reilly: How are \x3ci\x3eyou...?"
	<a onx='pattern = /{{.}}/;'>     O\x27Reilly: How are \x3ci\x3eyou...\x3f

如果在不安全的上下文中使用，则该值可能会被过滤掉：

	上下文                            {{.}} 之后
	<a href="{{.}}">                 #ZgotmplZ

因为 "O'Reilly:" 不是像 "http:" 那样被允许的协议。

如果 {{.}} 是无害的单词 `left`，则它可以更广泛地出现，

	上下文                                {{.}} 之后
	{{.}}                                left
	<a title='{{.}}'>                    left
	<a href='{{.}}'>                     left
	<a href='/{{.}}'>                    left
	<a href='?dir={{.}}'>                left
	<a style="border-{{.}}: 4px">        left
	<a style="align: {{.}}">             left
	<a style="background: '{{.}}'>       left
	<a style="background: url('{{.}}')>  left
	<style>p.{{.}} {color:red}</style>   left

非字符串值可以在 JavaScript 上下文中使用。如果 {{.}} 是

	struct{A,B string}{ "foo", "bar" }

在转义后的模板中

	<script>var pair = {{.}};</script>

则模板输出为

	<script>var pair = {"A": "foo", "B": "bar"};</script>

请参阅 json 包以了解非字符串内容如何被序列化以嵌入 JavaScript 上下文。

# 类型化字符串

默认情况下，本包假定所有管道产生纯文本字符串。
它会添加必要的转义管道阶段，以正确安全地将该纯文本字符串嵌入到适当的上下文中。

当数据值不是纯文本时，你可以通过标记其类型来确保它不会被过度转义。

HTML、JS、URL 以及 content.go 中的其他类型可以承载安全内容，这些内容可豁免转义。

模板

	Hello, {{.}}!

可以通过以下方式调用

	tmpl.Execute(out, template.HTML(`<b>World</b>`))

以产生

	Hello, <b>World</b>!

而不是

	Hello, &lt;b&gt;World&lt;b&gt;!

如果 {{.}} 是普通字符串则会产生后者。

# 安全模型

https://web.archive.org/web/20160501113828/http://js-quasis-libraries-and-repl.googlecode.com/svn/trunk/safetemplate.html#problem_definition 定义了本包所使用的"安全"的含义。

本包假定模板作者是可信的，而 Execute 的 data 参数是不可信的，
并致力于在面对不可信数据时保持以下属性：

结构保持属性：
"……当模板作者在安全的模板语言中编写 HTML 标签时，
浏览器会将输出的相应部分解释为标签，
无论不可信数据的值如何，其他结构（如属性边界、JS 和 CSS 字符串边界）也是如此。"

代码效果属性：
"……只有模板作者指定的代码应该在将模板输出注入页面后运行，
并且模板作者指定的所有代码都应该因此而运行。"

最少意外属性：
"一个熟悉 HTML、CSS 和 JavaScript 的开发者（或代码审查者），
知道会发生上下文自动转义，应该能够查看 {{.}} 并正确推断出会发生什么净化处理。"

之前，ECMAScript 6 模板字面量默认被禁用，可以通过 GODEBUG=jstmpllitinterp=1
环境变量启用。模板字面量现在默认被支持，设置 jstmpllitinterp 不再有任何效果。
*/
package template
