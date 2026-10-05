// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package log 实现了一个简单的日志记录包。它定义了一个类型 [Logger]，
// 该类型带有用于格式化输出的方法。它还通过辅助函数 Print[f|ln]、Fatal[f|ln] 和
// Panic[f|ln] 提供了一个预定义的“标准”
// Logger，这些函数比手动创建 Logger 更易于使用。
// 该 logger 写入标准错误，并打印每条日志消息的
// 日期和时间。
// 每条日志消息输出在单独的一行：如果正在打印的消息
// 不以换行符结尾，logger 会添加一个。
// Fatal 系列函数在写入日志消息后调用 [os.Exit](1)。
// Panic 系列函数在写入日志消息后调用 panic。
package log

import (
	"fmt"
	"io"
	"log/internal"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// 这些标志（flag）定义 [Logger] 生成的每条日志条目要添加什么前缀文本。
// 各位通过按位或组合在一起，以控制打印的内容。
// 除 Lmsgprefix 标志外，无法控制它们出现的顺序（即此处列出的顺序）
// 或它们呈现的格式（如注释中所述）。
// 仅当指定了 Llongfile 或 Lshortfile 时，前缀后才会跟一个冒号。
// 例如，标志 Ldate | Ltime（或 LstdFlags）产生：
//
//	2009/01/23 01:23:23 message
//
// 而标志 Ldate | Ltime | Lmicroseconds | Llongfile 产生：
//
//	2009/01/23 01:23:23.123123 /a/b/c/d.go:23: message
const (
	Ldate         = 1 << iota     // 本地时区的日期：2009/01/23
	Ltime                         // 本地时区的时间：01:23:23
	Lmicroseconds                 // 微秒级分辨率：01:23:23.123123。假定已设置 Ltime。
	Llongfile                     // 完整文件名和行号：/a/b/c/d.go:23
	Lshortfile                    // 文件名最后一项和行号：d.go:23。会覆盖 Llongfile
	LUTC                          // 如果设置了 Ldate 或 Ltime，则使用 UTC 而非本地时区
	Lmsgprefix                    // 将“prefix”从行首移到消息之前
	LstdFlags     = Ldate | Ltime // 标准 logger 的初始值
)

// Logger 表示一个活动的日志记录对象，它向 [io.Writer] 生成若干行输出。
// 每次日志记录操作都会对 Writer 的 Write 方法进行一次调用。
// 一个 Logger 可同时被多个 goroutine 使用；
// 它保证对 Writer 的访问是串行化的。
type Logger struct {
	outMu sync.Mutex
	out   io.Writer // 输出的目标

	prefix    atomic.Pointer[string] // 每行的前缀，用于标识 logger（但请参见 Lmsgprefix）
	flag      atomic.Int32           // 属性
	isDiscard atomic.Bool
}

// New 创建一个新的 [Logger]。变量 out 设置日志数据
// 将写入的目标。
// prefix 出现在生成的每条日志行的开头，或者
// 如果提供了 [Lmsgprefix] 标志，则出现在日志头部之后。
// flag 参数定义日志记录属性。
func New(out io.Writer, prefix string, flag int) *Logger {
	l := new(Logger)
	l.SetOutput(out)
	l.SetPrefix(prefix)
	l.SetFlags(flag)
	return l
}

// SetOutput 设置 logger 的输出目标。
func (l *Logger) SetOutput(w io.Writer) {
	l.outMu.Lock()
	defer l.outMu.Unlock()
	l.out = w
	l.isDiscard.Store(w == io.Discard)
}

var std = New(os.Stderr, "", LstdFlags)

// Default 返回包级输出函数所用的标准 logger。
func Default() *Logger { return std }

// 将整数廉价地转换为固定宽度的十进制 ASCII。传入负宽度可避免补零。
func itoa(buf *[]byte, i int, wid int) {
	// 按相反的顺序组装十进制数字。
	var b [20]byte
	bp := len(b) - 1
	for i >= 10 || wid > 1 {
		wid--
		q := i / 10
		b[bp] = byte('0' + i - q*10)
		bp--
		i = q
	}
	// i < 10
	b[bp] = byte('0' + i)
	*buf = append(*buf, b[bp:]...)
}

// formatHeader 按以下顺序将日志头部写入 buf：
//   - l.prefix（如果不为空且未设置 Lmsgprefix），
//   - 日期和/或时间（如果提供了相应标志），
//   - 文件名和行号（如果提供了相应标志），
//   - l.prefix（如果不为空且设置了 Lmsgprefix）。
func formatHeader(buf *[]byte, t time.Time, prefix string, flag int, file string, line int) {
	if flag&Lmsgprefix == 0 {
		*buf = append(*buf, prefix...)
	}
	if flag&(Ldate|Ltime|Lmicroseconds) != 0 {
		if flag&LUTC != 0 {
			t = t.UTC()
		}
		if flag&Ldate != 0 {
			year, month, day := t.Date()
			itoa(buf, year, 4)
			*buf = append(*buf, '/')
			itoa(buf, int(month), 2)
			*buf = append(*buf, '/')
			itoa(buf, day, 2)
			*buf = append(*buf, ' ')
		}
		if flag&(Ltime|Lmicroseconds) != 0 {
			hour, min, sec := t.Clock()
			itoa(buf, hour, 2)
			*buf = append(*buf, ':')
			itoa(buf, min, 2)
			*buf = append(*buf, ':')
			itoa(buf, sec, 2)
			if flag&Lmicroseconds != 0 {
				*buf = append(*buf, '.')
				itoa(buf, t.Nanosecond()/1e3, 6)
			}
			*buf = append(*buf, ' ')
		}
	}
	if flag&(Lshortfile|Llongfile) != 0 {
		if flag&Lshortfile != 0 {
			short := file
			for i := len(file) - 1; i > 0; i-- {
				if file[i] == '/' {
					short = file[i+1:]
					break
				}
			}
			file = short
		}
		*buf = append(*buf, file...)
		*buf = append(*buf, ':')
		itoa(buf, line, -1)
		*buf = append(*buf, ": "...)
	}
	if flag&Lmsgprefix != 0 {
		*buf = append(*buf, prefix...)
	}
}

var bufferPool = sync.Pool{New: func() any { return new([]byte) }}

func getBuffer() *[]byte {
	p := bufferPool.Get().(*[]byte)
	*p = (*p)[:0]
	return p
}

func putBuffer(p *[]byte) {
	// 正确使用 sync.Pool 要求每个条目具有大致相同的内存开销。
	// 当所存储的类型包含大小可变的缓冲区时，为获得这一性质，
	// 我们对放回池中的最大缓冲区施加一个硬性限制。
	//
	// 参见 https://go.dev/issue/23199
	if cap(*p) > 64<<10 {
		*p = nil
	}
	bufferPool.Put(p)
}

// Output 为一次日志事件写入输出。字符串 s 包含要打印在
// Logger 各标志所指定前缀之后的文本。
// 如果 s 的最后一个字符不是换行符，则追加一个换行符。
// Calldepth 用于恢复 PC，提供它是出于通用性考虑，
// 尽管目前在所有预定义路径上它都是 2。
func (l *Logger) Output(calldepth int, s string) error {
	return l.output(0, calldepth+1, func(b []byte) []byte { // +1 表示当前帧。
		return append(b, s...)
	})
}

// output 既可以接受 calldepth，也可以接受 pc 来获取源码行信息。
// 如果 pc 非零，则使用 pc。
func (l *Logger) output(pc uintptr, calldepth int, appendOutput func([]byte) []byte) error {
	if l.isDiscard.Load() {
		return nil
	}

	now := time.Now() // 尽早获取。

	// 只加载一次 prefix 和 flag，这样无论它们是否被并发修改，
	// 其值在本次调用中都保持一致。
	prefix := l.Prefix()
	flag := l.Flags()

	var file string
	var line int
	if flag&(Lshortfile|Llongfile) != 0 {
		if pc == 0 {
			var ok bool
			_, file, line, ok = runtime.Caller(calldepth)
			if !ok {
				file = "???"
				line = 0
			}
		} else {
			fs := runtime.CallersFrames([]uintptr{pc})
			f, _ := fs.Next()
			file = f.File
			if file == "" {
				file = "???"
			}
			line = f.Line
		}
	}

	buf := getBuffer()
	defer putBuffer(buf)
	formatHeader(buf, now, prefix, flag, file, line)
	*buf = appendOutput(*buf)
	if len(*buf) == 0 || (*buf)[len(*buf)-1] != '\n' {
		*buf = append(*buf, '\n')
	}

	l.outMu.Lock()
	defer l.outMu.Unlock()
	_, err := l.out.Write(*buf)
	return err
}

func init() {
	internal.DefaultOutput = func(pc uintptr, data []byte) error {
		return std.output(pc, 0, func(buf []byte) []byte {
			return append(buf, data...)
		})
	}
}

// Print 调用 l.Output 输出到 logger。
// 参数的处理方式与 [fmt.Print] 相同。
func (l *Logger) Print(v ...any) {
	l.output(0, 2, func(b []byte) []byte {
		return fmt.Append(b, v...)
	})
}

// Printf 调用 l.Output 输出到 logger。
// 参数的处理方式与 [fmt.Printf] 相同。
func (l *Logger) Printf(format string, v ...any) {
	l.output(0, 2, func(b []byte) []byte {
		return fmt.Appendf(b, format, v...)
	})
}

// Println 调用 l.Output 输出到 logger。
// 参数的处理方式与 [fmt.Println] 相同。
func (l *Logger) Println(v ...any) {
	l.output(0, 2, func(b []byte) []byte {
		return fmt.Appendln(b, v...)
	})
}

// Fatal 等价于 l.Print() 后接一次 [os.Exit](1) 调用。
func (l *Logger) Fatal(v ...any) {
	l.output(0, 2, func(b []byte) []byte {
		return fmt.Append(b, v...)
	})
	os.Exit(1)
}

// Fatalf 等价于 l.Printf() 后接一次 [os.Exit](1) 调用。
func (l *Logger) Fatalf(format string, v ...any) {
	l.output(0, 2, func(b []byte) []byte {
		return fmt.Appendf(b, format, v...)
	})
	os.Exit(1)
}

// Fatalln 等价于 l.Println() 后接一次 [os.Exit](1) 调用。
func (l *Logger) Fatalln(v ...any) {
	l.output(0, 2, func(b []byte) []byte {
		return fmt.Appendln(b, v...)
	})
	os.Exit(1)
}

// Panic 等价于 l.Print() 后接一次 panic() 调用。
func (l *Logger) Panic(v ...any) {
	s := fmt.Sprint(v...)
	l.output(0, 2, func(b []byte) []byte {
		return append(b, s...)
	})
	panic(s)
}

// Panicf 等价于 l.Printf() 后接一次 panic() 调用。
func (l *Logger) Panicf(format string, v ...any) {
	s := fmt.Sprintf(format, v...)
	l.output(0, 2, func(b []byte) []byte {
		return append(b, s...)
	})
	panic(s)
}

// Panicln 等价于 l.Println() 后接一次 panic() 调用。
func (l *Logger) Panicln(v ...any) {
	s := fmt.Sprintln(v...)
	l.output(0, 2, func(b []byte) []byte {
		return append(b, s...)
	})
	panic(s)
}

// Flags 返回 logger 的输出标志。
// 标志位包括 [Ldate]、[Ltime] 等。
func (l *Logger) Flags() int {
	return int(l.flag.Load())
}

// SetFlags 设置 logger 的输出标志。
// 标志位包括 [Ldate]、[Ltime] 等。
func (l *Logger) SetFlags(flag int) {
	l.flag.Store(int32(flag))
}

// Prefix 返回 logger 的输出前缀。
func (l *Logger) Prefix() string {
	if p := l.prefix.Load(); p != nil {
		return *p
	}
	return ""
}

// SetPrefix 设置 logger 的输出前缀。
func (l *Logger) SetPrefix(prefix string) {
	l.prefix.Store(&prefix)
}

// Writer 返回 logger 的输出目标。
func (l *Logger) Writer() io.Writer {
	l.outMu.Lock()
	defer l.outMu.Unlock()
	return l.out
}

// SetOutput 设置标准 logger 的输出目标。
func SetOutput(w io.Writer) {
	std.SetOutput(w)
}

// Flags 返回标准 logger 的输出标志。
// 标志位包括 [Ldate]、[Ltime] 等。
func Flags() int {
	return std.Flags()
}

// SetFlags 设置标准 logger 的输出标志。
// 标志位包括 [Ldate]、[Ltime] 等。
func SetFlags(flag int) {
	std.SetFlags(flag)
}

// Prefix 返回标准 logger 的输出前缀。
func Prefix() string {
	return std.Prefix()
}

// SetPrefix 设置标准 logger 的输出前缀。
func SetPrefix(prefix string) {
	std.SetPrefix(prefix)
}

// Writer 返回标准 logger 的输出目标。
func Writer() io.Writer {
	return std.Writer()
}

// 以下函数写入标准 logger。

// Print 调用 Output 输出到标准 logger。
// 参数的处理方式与 [fmt.Print] 相同。
func Print(v ...any) {
	std.output(0, 2, func(b []byte) []byte {
		return fmt.Append(b, v...)
	})
}

// Printf 调用 Output 输出到标准 logger。
// 参数的处理方式与 [fmt.Printf] 相同。
func Printf(format string, v ...any) {
	std.output(0, 2, func(b []byte) []byte {
		return fmt.Appendf(b, format, v...)
	})
}

// Println 调用 Output 输出到标准 logger。
// 参数的处理方式与 [fmt.Println] 相同。
func Println(v ...any) {
	std.output(0, 2, func(b []byte) []byte {
		return fmt.Appendln(b, v...)
	})
}

// Fatal 等价于 [Print] 后接一次 [os.Exit](1) 调用。
func Fatal(v ...any) {
	std.output(0, 2, func(b []byte) []byte {
		return fmt.Append(b, v...)
	})
	os.Exit(1)
}

// Fatalf 等价于 [Printf] 后接一次 [os.Exit](1) 调用。
func Fatalf(format string, v ...any) {
	std.output(0, 2, func(b []byte) []byte {
		return fmt.Appendf(b, format, v...)
	})
	os.Exit(1)
}

// Fatalln 等价于 [Println] 后接一次 [os.Exit](1) 调用。
func Fatalln(v ...any) {
	std.output(0, 2, func(b []byte) []byte {
		return fmt.Appendln(b, v...)
	})
	os.Exit(1)
}

// Panic 等价于 [Print] 后接一次 panic() 调用。
func Panic(v ...any) {
	s := fmt.Sprint(v...)
	std.output(0, 2, func(b []byte) []byte {
		return append(b, s...)
	})
	panic(s)
}

// Panicf 等价于 [Printf] 后接一次 panic() 调用。
func Panicf(format string, v ...any) {
	s := fmt.Sprintf(format, v...)
	std.output(0, 2, func(b []byte) []byte {
		return append(b, s...)
	})
	panic(s)
}

// Panicln 等价于 [Println] 后接一次 panic() 调用。
func Panicln(v ...any) {
	s := fmt.Sprintln(v...)
	std.output(0, 2, func(b []byte) []byte {
		return append(b, s...)
	})
	panic(s)
}

// Output 为一次日志事件写入输出。字符串 s 包含要打印在
// Logger 各标志所指定前缀之后的文本。
// 如果 s 的最后一个字符不是换行符，则追加一个换行符。
// 当设置了 [Llongfile] 或 [Lshortfile] 时，Calldepth 是
// 在计算文件名和行号时要跳过的帧数；
// 值为 1 时将打印 Output 调用者的详细信息。
func Output(calldepth int, s string) error {
	return std.output(0, calldepth+1, func(b []byte) []byte { // +1 表示当前帧。
		return append(b, s...)
	})
}
