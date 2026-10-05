// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package slog 提供结构化日志，
其中日志记录包含一条消息、
一个严重级别，以及以键值对形式
表达的各种其他属性。

它定义了一个类型 [Logger]，
该类型提供了若干方法（例如 [Logger.Info] 和 [Logger.Error]）
用于报告关注的事件。

每个 Logger 都与一个 [Handler] 相关联。
Logger 的输出方法会根据方法参数创建一个 [Record]，
并将其传递给 Handler，由后者决定如何处理它。
通过顶层函数（例如 [Info] 和 [Error]）可以访问一个默认的 Logger，
这些顶层函数会调用相应的 Logger 方法。

一条日志记录由时间、级别、消息以及一组键值对组成，
其中键是字符串，值可以是任意类型。
例如，

	slog.Info("hello", "count", 3)

会创建一条记录，其中包含调用时的时间、
Info 级别、消息 "hello"，以及一个
键为 "count"、值为 3 的键值对。

顶层函数 [Info] 会调用默认 Logger 上的 [Logger.Info] 方法。
除了 [Logger.Info] 之外，还有用于 Debug、Warn 和 Error 级别的方法。
除了这些用于常见级别的便捷方法之外，
还有一个 [Logger.Log] 方法，它将级别作为参数。
上述每个方法都有一个对应的、使用默认 logger 的顶层函数。

默认 handler 会将日志记录的消息、时间、级别和属性格式化为
一个字符串，并将其传递给 [log] 包。

	2022/11/08 15:28:26 INFO hello count=3

若要对输出格式进行更多控制，可以创建一个使用不同 handler 的 logger。
下面的语句使用 [New] 创建一个带有 [TextHandler] 的新 logger，
该 handler 会将结构化记录以文本形式写入标准错误：

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

[TextHandler] 的输出是一系列 key=value 键值对，便于机器轻松且无歧义地
解析。下面的语句：

	logger.Info("hello", "count", 3)

会产生如下输出：

	time=2022-11-08T15:28:26.000-05:00 level=INFO msg=hello count=3

该包还提供了 [JSONHandler]，其输出是以行分隔的 JSON：

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("hello", "count", 3)

会产生如下输出：

	{"time":"2022-11-08T15:28:26.000000000-05:00","level":"INFO","msg":"hello","count":3}

[TextHandler] 和 [JSONHandler] 都可以使用 [HandlerOptions] 进行配置。
其中包含用于设置最低级别（参见下文“级别”一节）、
显示日志调用的源文件和行号，以及
在记录属性前修改属性的选项。

使用以下方式将某个 logger 设为默认：

	slog.SetDefault(logger)

会使诸如 [Info] 之类的顶层函数使用它。
[SetDefault] 还会更新 [log] 包所使用的默认 logger，
这样，使用 [log.Printf] 及相关函数的现有应用程序
无需重写即可将日志记录发送到该 logger 的 handler。

有些属性在许多日志调用中都很常见。
例如，你可能希望将某个服务器请求的 URL 或跟踪标识符
包含在由该请求产生的所有日志事件中。
与其在每次日志调用中重复该属性，你可以使用 [Logger.With]
构造一个包含这些属性的新 Logger：

	logger2 := logger.With("url", r.URL)

With 的参数与 [Logger.Info] 中使用的键值对相同。
其结果是一个新 Logger，它与原 Logger 具有相同的 handler，但会额外
包含将出现在每次调用输出中的属性。

# 级别

[Level] 是一个整数，表示日志事件的重要性或严重程度。
级别越高，事件越严重。
该包为最常见的级别定义了常量，
但任何 int 都可以用作级别。

在应用程序中，你可能希望只记录达到某个级别或更高级别的消息。
一种常见的配置是记录 Info 或更高级别的消息，
在需要之前抑制 debug 日志。
内置 handler 可以通过设置 [HandlerOptions.Level] 来配置要输出的最低级别。
程序的 `main` 函数通常会这样做。
默认值是 LevelInfo。

将 [HandlerOptions.Level] 字段设置为一个 [Level] 值，
会在 handler 的整个生命周期内固定其最低级别。
将其设置为一个 [LevelVar] 则允许动态地改变级别。
LevelVar 持有一个 Level，并且可以安全地被多个 goroutine
读写。
若要为整个程序动态改变级别，首先初始化
一个全局 LevelVar：

	var programLevel = new(slog.LevelVar) // Info by default

然后使用该 LevelVar 构造一个 handler，并将其设为默认：

	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: programLevel})
	slog.SetDefault(slog.New(h))

现在，程序只需一条语句即可更改其日志级别：

	programLevel.Set(slog.LevelDebug)

# 分组

属性可以被收集到分组中。
分组有一个名称，用于限定其属性名称。
这种限定如何显示取决于 handler。
[TextHandler] 用一个点分隔分组名称和属性名称。
[JSONHandler] 将每个分组视为一个单独的 JSON 对象，以分组名称作为键。

使用 [Group] 根据名称和一组键值对创建一个 Group 属性：

	slog.Group("request",
	    "method", r.Method,
	    "url", r.URL)

TextHandler 会将该分组显示为

	request.method=GET request.url=http://example.com

JSONHandler 会将其显示为

	"request":{"method":"GET","url":"http://example.com"}

使用 [Logger.WithGroup] 可以用一个分组名称限定某个 Logger 的所有输出。
对 Logger 调用 WithGroup 会得到一个新的 Logger，它与原 Logger 具有相同的 Handler，但
其所有属性都由该分组名称限定。

在大型系统中，这有助于防止属性键重复，
因为各个子系统可能会使用相同的键。
为每个子系统传入一个带有各自分组名称的不同 Logger，以便
潜在的重复项得到限定：

	logger := slog.Default().With("id", systemID)
	parserLogger := logger.WithGroup("parser")
	parseInput(input, parserLogger)

当 parseInput 使用 parserLogger 记录日志时，其键将由 "parser" 限定，
因此即使它使用了常见的键 "id"，日志行也会具有不同的键。

# 上下文

有些 handler 可能希望包含调用点处可用的 [context.Context] 中的信息。
这类信息的一个例子
是在启用跟踪时当前 span 的标识符。

[Logger.Log] 和 [Logger.LogAttrs] 方法将 context 作为第一个
参数，它们对应的顶层函数也是如此。

尽管 Logger 上的便捷方法（Info 等）以及
对应的顶层函数不接受 context，但以 "Context" 结尾的替代方法接受。
例如，

	slog.InfoContext(ctx, "message")

如果可用，建议向输出方法传入一个 context。

# Attr 与 Value

[Attr] 是一个键值对。Logger 的输出方法既接受 Attr，也接受
交替出现的键和值。语句

	slog.Info("hello", slog.Int("count", 3))

的行为与

	slog.Info("hello", "count", 3)

相同。

对于常见类型，有 [Attr] 的便捷构造函数，例如 [Int]、[String] 和 [Bool]，
还有用于构造任意类型 Attr 的函数 [Any]。

Attr 的值部分是一个称为 [Value] 的类型。
与 [any] 类似，Value 可以保存任意 Go 值，
但它可以表示典型值（包括所有数字和字符串），
而无需分配内存。

为了获得最高效的日志输出，请使用 [Logger.LogAttrs]。
它类似于 [Logger.Log]，但只接受 Attr，而不接受交替的
键和值；这使它也能避免分配内存。

调用

	logger.LogAttrs(ctx, slog.LevelInfo, "hello", slog.Int("count", 3))

是实现与

	slog.InfoContext(ctx, "hello", "count", 3)

相同输出的最高效方式。

# 自定义类型的日志行为

如果某个类型实现了 [LogValuer] 接口，则其 LogValue
方法返回的 [Value] 会被用于日志记录。你可以借此控制该类型的值
在日志中的呈现方式。例如，你可以对密码等机密信息进行脱敏，
或将结构体的字段收集到一个 Group 中。有关详细信息，
请参见 [LogValuer] 下的示例。

LogValue 方法可能返回一个本身实现了 [LogValuer] 的 Value。[Value.Resolve]
方法会谨慎处理这些情况，避免无限循环和无界递归。
Handler 作者及其他人员可能希望使用 [Value.Resolve]，而不是直接调用 LogValue。

# 包装输出方法

logger 函数会对调用栈进行反射，以查找应用程序内日志调用的
文件名和行号。对于包装了 slog 的函数，这可能会产生
错误的源信息。例如，如果你
在文件 mylog.go 中定义了如下函数：

	func Infof(logger *slog.Logger, format string, args ...any) {
	    logger.Info(fmt.Sprintf(format, args...))
	}

并在 main.go 中像这样调用它：

	Infof(slog.Default(), "hello, %s", "world")

那么 slog 会将源文件报告为 mylog.go，而不是 main.go。

Infof 的正确实现会获取源位置
(pc) 并将其传递给 NewRecord。
包级示例中名为 "wrapping" 的
Infof 函数演示了如何做到这一点。

# 使用 Record

有时 Handler 需要在将 Record 传递给另一个 Handler 或后端
之前对其进行修改。
Record 包含简单公有字段（例如 Time、Level、Message）和
间接引用状态（例如属性）的隐藏字段的混合体。这
意味着修改 Record 的一个简单副本（例如通过调用
[Record.Add] 或 [Record.AddAttrs] 来添加属性）
可能会对原始对象产生意外影响。
在修改 Record 之前，请使用 [Record.Clone]
创建一个与原对象不共享状态的副本，
或使用 [NewRecord] 创建一个新的 Record，
并通过 [Record.Attrs] 遍历旧的 Attrs 来构建其 Attrs。

# 性能注意事项

如果对应用程序进行性能分析表明日志记录占用了大量时间，
以下建议可能会有所帮助。

如果许多日志行都有一个共同的属性，请使用 [Logger.With] 创建一个带有
该属性的 Logger。内置 handler 只会在调用 [Logger.With] 时
对该属性格式化一次。[Handler] 接口的设计允许进行这种优化，
编写良好的 Handler 应当利用它。

日志调用的参数总是会被求值，即使该日志事件被丢弃。
如果可能，请推迟计算，使其仅在该值确实被记录时才发生。
例如，考虑以下调用

	slog.Info("starting request", "url", r.URL.String())  // may compute String unnecessarily

即使 logger 丢弃 Info 级别的事件，URL.String 方法仍会被调用。
相反，请直接传入 URL：

	slog.Info("starting request", "url", &r.URL) // calls URL.String only if needed

内置的 [TextHandler] 会调用其 String 方法，但仅
在该日志事件启用时才会调用。
避免调用 String 还能保留底层值的结构。
例如 [JSONHandler] 会将解析后 URL 的各组成部分作为 JSON 对象输出。
如果你想避免过早承担 String 调用的开销，
而又不让 handler 可能检查值的结构，
请将该值包装在一个隐藏其 Marshal 方法的 fmt.Stringer 实现中。

你还可以使用 [LogValuer] 接口来避免在未启用的日志
调用中进行不必要的工作。假设你需要记录某个开销很大的值：

	slog.Debug("frobbing", "value", computeExpensiveValue(arg))

即使这一行被禁用，computeExpensiveValue 仍会被调用。
为避免这种情况，请定义一个实现 LogValuer 的类型：

	type expensive struct { arg int }

	func (e expensive) LogValue() slog.Value {
	    return slog.AnyValue(computeExpensiveValue(e.arg))
	}

然后在日志调用中使用该类型的值：

	slog.Debug("frobbing", "value", expensive{arg})

现在，computeExpensiveValue 只会在该行启用时才被调用。

内置 handler 在调用 [io.Writer.Write] 之前会先获取锁，
以确保一次恰好完整地写入一个 [Record]。
尽管每条日志记录都有时间戳，
但内置 handler 不会使用该时间对写入的记录进行排序。
用户自定义的 handler 需自行负责加锁和排序。

# 编写 handler

有关编写自定义 handler 的指南，请参见 https://golang.org/s/slog-handler-guide。
*/
package slog
