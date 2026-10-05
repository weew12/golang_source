// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"bytes"
	"context"
	"log"
	loginternal "log/internal"
	"log/slog/internal"
	"runtime"
	"sync/atomic"
	"time"
)

var defaultLogger atomic.Pointer[Logger]

var logLoggerLevel LevelVar

// SetLogLoggerLevel 控制通往 [log] 包的桥接的级别。
//
// 在调用 [SetDefault] 之前，slog 顶层日志函数会调用默认的 [log.Logger]。
// 在该模式下，SetLogLoggerLevel 为这些调用设置最低级别。
// 默认情况下，最低级别为 Info，因此对 [Debug] 的调用
// （以及更低级别的顶层日志调用）
// 不会传递给 log.Logger。在调用
//
//	slog.SetLogLoggerLevel(slog.LevelDebug)
//
// 之后，对 [Debug] 的调用将被传递给 log.Logger。
//
// 在调用 [SetDefault] 之后，对默认 [log.Logger] 的调用会被传递给
// slog 默认 handler。在该模式下，
// SetLogLoggerLevel 设置这些调用被记录的级别。
// 也就是说，在调用
//
//	slog.SetLogLoggerLevel(slog.LevelDebug)
//
// 之后，对 [log.Printf] 的调用将导致以 [LevelDebug] 级别输出。
//
// SetLogLoggerLevel 返回之前的值。
func SetLogLoggerLevel(level Level) (oldLevel Level) {
	oldLevel = logLoggerLevel.Level()
	logLoggerLevel.Set(level)
	return
}

func init() {
	defaultLogger.Store(New(newDefaultHandler(loginternal.DefaultOutput)))
}

// Default 返回默认的 [Logger]。
func Default() *Logger { return defaultLogger.Load() }

// SetDefault 使 l 成为默认的 [Logger]，供顶层函数 [Info]、[Debug] 等
// 使用。
// 在此调用之后，log 包默认 Logger 的输出
// （如 [log.Print] 等）将使用 l 的 Handler 记录，
// 其级别由 [SetLogLoggerLevel] 控制。
func SetDefault(l *Logger) {
	defaultLogger.Store(l)
	// 如果默认的 handler 是 defaultHandler，则不要使用 handleWriter，
	// 否则我们会死锁，因为它们都试图获取 log 默认互斥锁。
	// defaultHandler 会使用 log 默认 writer 当前设置的任何内容，
	// 这是正确的。
	// 这可能在调用 SetDefault(Default()) 时发生。
	// 参见 TestSetDefault。
	if _, ok := l.Handler().(*defaultHandler); !ok {
		capturePC := log.Flags()&(log.Lshortfile|log.Llongfile) != 0
		log.SetOutput(&handlerWriter{l.Handler(), &logLoggerLevel, capturePC})
		log.SetFlags(0) // 我们只想要日志消息，不要时间或位置
	}
}

// handlerWriter 是一个调用 Handler 的 io.Writer。
// 它用于将默认的 log.Logger 链接到默认的 slog.Logger。
type handlerWriter struct {
	h         Handler
	level     Leveler
	capturePC bool
}

func (w *handlerWriter) Write(buf []byte) (int, error) {
	level := w.level.Level()
	if !w.h.Enabled(context.Background(), level) {
		return 0, nil
	}
	var pc uintptr
	if !internal.IgnorePC && w.capturePC {
		// 跳过 [runtime.Callers, w.Write, Logger.Output, log.Print]
		var pcs [1]uintptr
		runtime.Callers(4, pcs[:])
		pc = pcs[0]
	}

	// 移除末尾的换行符。
	origLen := len(buf) // 报告整个 buf 都已写入。
	buf = bytes.TrimSuffix(buf, []byte{'\n'})
	r := NewRecord(time.Now(), level, string(buf), pc)
	return origLen, w.h.Handle(context.Background(), r)
}

// Logger 记录对其 Log、Debug、Info、Warn 和 Error 方法的
// 每次调用的结构化信息。
// 对于每次调用，它创建一个 [Record] 并将其传递给一个 [Handler]。
//
// 若要创建新的 Logger，请调用 [New] 或以 "With" 开头的
// Logger 方法。
type Logger struct {
	handler Handler // 用于结构化日志
}

func (l *Logger) clone() *Logger {
	c := *l
	return &c
}

// Handler 返回 l 的 Handler。
func (l *Logger) Handler() Handler { return l.handler }

// With 返回一个 Logger，它在每次输出操作中都包含给定的属性。
// 参数会像 [Logger.Log] 那样被转换为属性。
func (l *Logger) With(args ...any) *Logger {
	if len(args) == 0 {
		return l
	}
	c := l.clone()
	c.handler = l.handler.WithAttrs(argsToAttrSlice(args))
	return c
}

// WithGroup 返回一个启动分组的 Logger，前提是 name 非空。
// 添加到该 Logger 的所有属性的键都将由给定的名称
// 限定。（这种限定如何发生取决于该 Logger 的 Handler 的
// [Handler.WithGroup] 方法。）
//
// 如果 name 为空，WithGroup 返回接收者。
func (l *Logger) WithGroup(name string) *Logger {
	if name == "" {
		return l
	}
	c := l.clone()
	c.handler = l.handler.WithGroup(name)
	return c
}

// New 使用给定的非 nil Handler 创建一个新的 Logger。
func New(h Handler) *Logger {
	if h == nil {
		panic("nil Handler")
	}
	return &Logger{handler: h}
}

// With 在默认 logger 上调用 [Logger.With]。
func With(args ...any) *Logger {
	return Default().With(args...)
}

// Enabled 报告 l 是否在给定的 context 和级别下发出日志记录。
func (l *Logger) Enabled(ctx context.Context, level Level) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	return l.Handler().Enabled(ctx, level)
}

// NewLogLogger 返回一个新的 [log.Logger]，使得对其 Output 方法的每次调用
// 都会将 Record 分派给指定的 handler。该 logger 充当从
// 较旧的 log API 到较新的结构化日志 handler 的桥梁。
func NewLogLogger(h Handler, level Level) *log.Logger {
	return log.New(&handlerWriter{h, level, true}, "", 0)
}

// Log 使用当前时间以及给定的级别和消息发出日志记录。
// Record 的 Attrs 由 Logger 的属性后跟
// args 指定的 Attrs 组成。
//
// 属性参数的处理方式如下：
//   - 如果参数是 Attr，则按原样使用。
//   - 如果参数是字符串且不是最后一个参数，
//     则下一个参数被当作值，两者合并
//     为一个 Attr。
//   - 否则，该参数被当作键为 "!BADKEY" 的值。
func (l *Logger) Log(ctx context.Context, level Level, msg string, args ...any) {
	l.log(ctx, level, msg, args...)
}

// LogAttrs 是 [Logger.Log] 的更高效版本，它只接受 Attrs。
func (l *Logger) LogAttrs(ctx context.Context, level Level, msg string, attrs ...Attr) {
	l.logAttrs(ctx, level, msg, attrs...)
}

// Debug 以 [LevelDebug] 级别记录日志。
func (l *Logger) Debug(msg string, args ...any) {
	l.log(context.Background(), LevelDebug, msg, args...)
}

// DebugContext 使用给定的 context 以 [LevelDebug] 级别记录日志。
func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelDebug, msg, args...)
}

// Info 以 [LevelInfo] 级别记录日志。
func (l *Logger) Info(msg string, args ...any) {
	l.log(context.Background(), LevelInfo, msg, args...)
}

// InfoContext 使用给定的 context 以 [LevelInfo] 级别记录日志。
func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelInfo, msg, args...)
}

// Warn 以 [LevelWarn] 级别记录日志。
func (l *Logger) Warn(msg string, args ...any) {
	l.log(context.Background(), LevelWarn, msg, args...)
}

// WarnContext 使用给定的 context 以 [LevelWarn] 级别记录日志。
func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelWarn, msg, args...)
}

// Error 以 [LevelError] 级别记录日志。
func (l *Logger) Error(msg string, args ...any) {
	l.log(context.Background(), LevelError, msg, args...)
}

// ErrorContext 使用给定的 context 以 [LevelError] 级别记录日志。
func (l *Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelError, msg, args...)
}

// log 是供接收 ...any 的方法使用的底层日志记录方法。
// 它必须总是由导出的日志记录方法
// 或函数直接调用，因为它使用固定的调用深度来获取 pc。
func (l *Logger) log(ctx context.Context, level Level, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !l.Enabled(ctx, level) {
		return
	}
	var pc uintptr
	if !internal.IgnorePC {
		var pcs [1]uintptr
		// 跳过 [runtime.Callers, 此函数, 此函数的调用者]
		runtime.Callers(3, pcs[:])
		pc = pcs[0]
	}
	r := NewRecord(time.Now(), level, msg, pc)
	r.Add(args...)
	_ = l.Handler().Handle(ctx, r)
}

// logAttrs 类似于 [Logger.log]，但用于接收 ...Attr 的方法。
func (l *Logger) logAttrs(ctx context.Context, level Level, msg string, attrs ...Attr) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !l.Enabled(ctx, level) {
		return
	}
	var pc uintptr
	if !internal.IgnorePC {
		var pcs [1]uintptr
		// 跳过 [runtime.Callers, 此函数, 此函数的调用者]
		runtime.Callers(3, pcs[:])
		pc = pcs[0]
	}
	r := NewRecord(time.Now(), level, msg, pc)
	r.AddAttrs(attrs...)
	_ = l.Handler().Handle(ctx, r)
}

// Debug 在默认 logger 上调用 [Logger.Debug]。
func Debug(msg string, args ...any) {
	Default().log(context.Background(), LevelDebug, msg, args...)
}

// DebugContext 在默认 logger 上调用 [Logger.DebugContext]。
func DebugContext(ctx context.Context, msg string, args ...any) {
	Default().log(ctx, LevelDebug, msg, args...)
}

// Info 在默认 logger 上调用 [Logger.Info]。
func Info(msg string, args ...any) {
	Default().log(context.Background(), LevelInfo, msg, args...)
}

// InfoContext 在默认 logger 上调用 [Logger.InfoContext]。
func InfoContext(ctx context.Context, msg string, args ...any) {
	Default().log(ctx, LevelInfo, msg, args...)
}

// Warn 在默认 logger 上调用 [Logger.Warn]。
func Warn(msg string, args ...any) {
	Default().log(context.Background(), LevelWarn, msg, args...)
}

// WarnContext 在默认 logger 上调用 [Logger.WarnContext]。
func WarnContext(ctx context.Context, msg string, args ...any) {
	Default().log(ctx, LevelWarn, msg, args...)
}

// Error 在默认 logger 上调用 [Logger.Error]。
func Error(msg string, args ...any) {
	Default().log(context.Background(), LevelError, msg, args...)
}

// ErrorContext 在默认 logger 上调用 [Logger.ErrorContext]。
func ErrorContext(ctx context.Context, msg string, args ...any) {
	Default().log(ctx, LevelError, msg, args...)
}

// Log 在默认 logger 上调用 [Logger.Log]。
func Log(ctx context.Context, level Level, msg string, args ...any) {
	Default().log(ctx, level, msg, args...)
}

// LogAttrs 在默认 logger 上调用 [Logger.LogAttrs]。
func LogAttrs(ctx context.Context, level Level, msg string, attrs ...Attr) {
	Default().logAttrs(ctx, level, msg, attrs...)
}
