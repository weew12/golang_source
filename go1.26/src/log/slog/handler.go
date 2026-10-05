// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"context"
	"fmt"
	"io"
	"log/slog/internal/buffer"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Handler 处理由 Logger 产生的日志记录。
//
// 典型的 handler 可能会将日志记录打印到标准错误，
// 或将它们写入文件或数据库，或可能用额外的属性
// 增强它们，并将其传递给另一个 handler。
//
// Handler 的任意方法都可能与自身或其他方法
// 并发调用。管理这种并发性是 Handler 的
// 责任。
//
// slog 包的用户不应直接调用 Handler 方法。
// 而应改用 [Logger] 的方法。
//
// 在实现你自己的 handler 之前，请查阅 https://go.dev/s/slog-handler-guide。
type Handler interface {
	// Enabled 报告 handler 是否处理给定级别的记录。
	// handler 会忽略级别更低的记录。
	// 它会在处理任何参数之前被提前调用，
	// 以便在日志事件应被丢弃时省去开销。
	// 如果从 Logger 方法调用，第一个参数是传递给
	// 该方法的 context，若传入 nil 或该方法不接受 context，
	// 则为 context.Background()。
	// 传入 context 是为了让 Enabled 能够使用其值
	// 做出判断。
	Enabled(context.Context, Level) bool

	// Handle 处理 Record。
	// 它仅在 Enabled 返回 true 时才会被调用。
	// Context 参数与 Enabled 的相同。
	// 它存在的唯一目的是让 Handler 能够访问 context 的值。
	// 取消 context 不应影响记录的处理。
	// （除其他事项外，日志消息可能是调试
	// 与取消相关问题的必要条件。）
	//
	// 产生输出的 Handle 方法应遵守以下规则：
	//   - 如果 r.Time 是零值时间，则忽略时间。
	//   - 如果 r.PC 为零，则忽略它。
	//   - Attr 的值应被解析。
	//   - 如果某个 Attr 的键和值都是零值，则忽略该 Attr。
	//     这可以用 attr.Equal(Attr{}) 来判断。
	//   - 如果某个分组的键为空，则内联该分组的 Attrs。
	//   - 如果某个分组没有 Attrs（即使它的键非空），
	//     则忽略它。
	//
	// [Logger] 会丢弃来自 Handle 的任何错误。包装 Handle 方法以
	// 处理来自 Handler 的任何错误。
	Handle(context.Context, Record) error

	// WithAttrs 返回一个新的 Handler，其属性由
	// 接收者的属性和参数共同组成。
	// Handler 拥有该切片：它可以保留、修改或丢弃它。
	WithAttrs(attrs []Attr) Handler

	// WithGroup 返回一个新的 Handler，其中给定的分组被追加到
	// 接收者已有的分组之后。
	// 所有后续属性的键，无论是由 With 添加还是在
	// Record 中添加，都应由该分组名称序列来限定。
	//
	// 这种限定如何发生由 Handler 决定，只要
	// 该 Handler 的属性键与具有不同分组名称序列的
	// 另一个 Handler 的属性键不同即可。
	//
	// Handler 应将 WithGroup 视为开始一个 Attrs 分组，该分组在
	// 日志事件结束时结束。也就是说，
	//
	//     logger.WithGroup("s").LogAttrs(ctx, level, msg, slog.Int("a", 1), slog.Int("b", 2))
	//
	// 的行为应类似于
	//
	//     logger.LogAttrs(ctx, level, msg, slog.Group("s", slog.Int("a", 1), slog.Int("b", 2)))
	//
	// 如果名称为空，WithGroup 返回接收者。
	WithGroup(name string) Handler
}

type defaultHandler struct {
	ch *commonHandler
	// internal.DefaultOutput，测试时除外
	output func(pc uintptr, data []byte) error
}

func newDefaultHandler(output func(uintptr, []byte) error) *defaultHandler {
	return &defaultHandler{
		ch:     &commonHandler{json: false},
		output: output,
	}
}

func (*defaultHandler) Enabled(_ context.Context, l Level) bool {
	return l >= logLoggerLevel.Level()
}

// 将级别、属性和消息收集到一个字符串中，并用
// 默认的 log.Logger 写出。
// 让 log.Logger 处理时间和文件/行号。
func (h *defaultHandler) Handle(ctx context.Context, r Record) error {
	buf := buffer.New()
	buf.WriteString(r.Level.String())
	buf.WriteByte(' ')
	buf.WriteString(r.Message)
	state := h.ch.newHandleState(buf, true, " ")
	defer state.free()
	state.appendNonBuiltIns(r)
	return h.output(r.PC, *buf)
}

func (h *defaultHandler) WithAttrs(as []Attr) Handler {
	return &defaultHandler{h.ch.withAttrs(as), h.output}
}

func (h *defaultHandler) WithGroup(name string) Handler {
	return &defaultHandler{h.ch.withGroup(name), h.output}
}

// HandlerOptions 是 [TextHandler] 或 [JSONHandler] 的选项。
// 零值 HandlerOptions 完全由默认值组成。
type HandlerOptions struct {
	// AddSource 使 handler 计算日志语句的源代码位置，
	// 并在输出中添加一个 SourceKey 属性。
	AddSource bool

	// Level 报告将被记录的最低记录级别。
	// handler 会丢弃级别更低的记录。
	// 如果 Level 为 nil，则 handler 假定为 LevelInfo。
	// handler 会为处理的每条记录调用 Level.Level；
	// 若要动态调整最低级别，请使用 LevelVar。
	Level Leveler

	// ReplaceAttr 会在每条非分组属性被记录之前调用，以对其进行改写。
	// 属性的值已被解析（参见 [Value.Resolve]）。
	// 如果 ReplaceAttr 返回一个零值 Attr，则该属性会被丢弃。
	//
	// 键为 "time"、"level"、"source" 和 "msg" 的内置属性
	// 会被传递给该函数，但 time 为零时会被省略，
	// 且 AddSource 为 false 时 source 会被省略。
	//
	// 第一个参数是当前打开的、包含该 Attr 的分组列表。
	// 不得保留或修改它。ReplaceAttr 从不会为
	// Group 属性调用，只会为其内容调用。例如，属性
	// 列表
	//
	//     Int("a", 1), Group("g", Int("b", 2)), Int("c", 3)
	//
	// 会导致以以下参数连续调用 ReplaceAttr：
	//
	//     nil, Int("a", 1)
	//     []string{"g"}, Int("b", 2)
	//     nil, Int("c", 3)
	//
	// ReplaceAttr 可用于更改内置属性的默认键、转换类型
	// （例如，将 `time.Time` 替换为自 Unix 纪元以来的整数秒数）、
	// 脱敏个人信息，或从输出中移除属性。
	ReplaceAttr func(groups []string, a Attr) Attr
}

// “内置”属性的键。
const (
	// TimeKey 是内置 handler 用于表示日志方法被调用时间的键。
	// 关联的 Value 是一个 [time.Time]。
	TimeKey = "time"
	// LevelKey 是内置 handler 用于表示日志调用级别的键。
	// 关联的值是一个 [Level]。
	LevelKey = "level"
	// MessageKey 是内置 handler 用于表示日志调用消息的键。
	// 关联的值是一个字符串。
	MessageKey = "msg"
	// SourceKey 是内置 handler 用于表示日志调用源文件和行号的键。
	// 关联的值是一个 *[Source]。
	SourceKey = "source"
)

type commonHandler struct {
	json              bool // true => 输出 JSON；false => 输出文本
	opts              HandlerOptions
	preformattedAttrs []byte
	// groupPrefix 仅用于文本 handler。
	// 它保存已被预先格式化的分组的前缀。
	// 当一次 WithGroup 调用后跟着一次 WithAttrs 调用时，
	// 分组会出现在这里。
	groupPrefix string
	groups      []string // 所有由 WithGroup 启动的分组
	nOpenGroups int      // 在 preformattedAttrs 中打开的分组数量
	mu          *sync.Mutex
	w           io.Writer
}

func (h *commonHandler) clone() *commonHandler {
	// 我们不能使用赋值，因为无法复制互斥锁。
	return &commonHandler{
		json:              h.json,
		opts:              h.opts,
		preformattedAttrs: slices.Clip(h.preformattedAttrs),
		groupPrefix:       h.groupPrefix,
		groups:            slices.Clip(h.groups),
		nOpenGroups:       h.nOpenGroups,
		w:                 h.w,
		mu:                h.mu, // 互斥锁在此 handler 的所有克隆之间共享
	}
}

// enabled 报告 l 是否大于或等于
// 最低级别。
func (h *commonHandler) enabled(l Level) bool {
	minLevel := LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return l >= minLevel
}

func (h *commonHandler) withAttrs(as []Attr) *commonHandler {
	// 我们将忽略空分组，因此如果整个切片都由空分组组成，
	// 就无需做任何事。
	if countEmptyGroups(as) == len(as) {
		return h
	}
	h2 := h.clone()
	// 预先格式化属性作为一种优化。
	state := h2.newHandleState((*buffer.Buffer)(&h2.preformattedAttrs), false, "")
	defer state.free()
	state.prefix.WriteString(h.groupPrefix)
	if pfa := h2.preformattedAttrs; len(pfa) > 0 {
		state.sep = h.attrSep()
		if h2.json && pfa[len(pfa)-1] == '{' {
			state.sep = ""
		}
	}
	// 记住缓冲区中的位置，以防所有 attr 都为空。
	pos := state.buf.Len()
	state.openGroups()
	if !state.appendAttrs(as) {
		state.buf.SetLen(pos)
	} else {
		// 记住供后续键使用的新前缀。
		h2.groupPrefix = state.prefix.String()
		// 记住 preformattedAttrs 中有多少个已打开的分组，
		// 这样我们在处理 Record 时就不会再次打开它们。
		h2.nOpenGroups = len(h2.groups)
	}
	return h2
}

func (h *commonHandler) withGroup(name string) *commonHandler {
	h2 := h.clone()
	h2.groups = append(h2.groups, name)
	return h2
}

// handle 是供 TextHandler 和 JSONHandler 使用的
// Handler.Handle 的内部实现。
func (h *commonHandler) handle(r Record) error {
	state := h.newHandleState(buffer.New(), true, "")
	defer state.free()
	if h.json {
		state.buf.WriteByte('{')
	}
	// 内置属性。它们不在分组中。
	stateGroups := state.groups
	state.groups = nil // 这样 ReplaceAttrs 看到的是没有分组，而不是预先的分组。
	rep := h.opts.ReplaceAttr
	// 时间
	if !r.Time.IsZero() {
		key := TimeKey
		val := r.Time.Round(0) // 去除单调时钟以匹配 Attr 的行为
		if rep == nil {
			state.appendKey(key)
			state.appendTime(val)
		} else {
			state.appendAttr(Time(key, val))
		}
	}
	// 级别
	key := LevelKey
	val := r.Level
	if rep == nil {
		state.appendKey(key)
		state.appendString(val.String())
	} else {
		state.appendAttr(Any(key, val))
	}
	// 源
	if h.opts.AddSource {
		src := r.Source()
		if src == nil {
			src = &Source{}
		}
		state.appendAttr(Any(SourceKey, src))
	}
	key = MessageKey
	msg := r.Message
	if rep == nil {
		state.appendKey(key)
		state.appendString(msg)
	} else {
		state.appendAttr(String(key, msg))
	}
	state.groups = stateGroups // 恢复传递给 ReplaceAttrs 的分组。
	state.appendNonBuiltIns(r)
	state.buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(*state.buf)
	return err
}

func (s *handleState) appendNonBuiltIns(r Record) {
	// 预先格式化的 Attrs
	if pfa := s.h.preformattedAttrs; len(pfa) > 0 {
		s.buf.WriteString(s.sep)
		s.buf.Write(pfa)
		s.sep = s.h.attrSep()
		if s.h.json && pfa[len(pfa)-1] == '{' {
			s.sep = ""
		}
	}
	// Record 中的 Attrs —— 与内置的不同，它们位于由 WithGroup 启动的
	// 分组中。
	// 如果记录没有 Attrs，则不输出任何分组。
	nOpenGroups := s.h.nOpenGroups
	if r.NumAttrs() > 0 {
		s.prefix.WriteString(s.h.groupPrefix)
		// 即使分组有 attrs，它也可能最终为空（例如，
		// ReplaceAttr 可能删除所有 attrs）。
		// 因此记住我们在缓冲区中的位置，以便必要时
		// 稍后恢复该位置。
		pos := s.buf.Len()
		s.openGroups()
		nOpenGroups = len(s.h.groups)
		empty := true
		r.Attrs(func(a Attr) bool {
			if s.appendAttr(a) {
				empty = false
			}
			return true
		})
		if empty {
			s.buf.SetLen(pos)
			nOpenGroups = s.h.nOpenGroups
		}
	}
	if s.h.json {
		// 关闭所有已打开的分组。
		for range s.h.groups[:nOpenGroups] {
			s.buf.WriteByte('}')
		}
		// 关闭顶层对象。
		s.buf.WriteByte('}')
	}
}

// attrSep 返回属性之间的分隔符。
func (h *commonHandler) attrSep() string {
	if h.json {
		return ","
	}
	return " "
}

// handleState 保存单次 commonHandler.handle 调用的状态。
// sep 的初始值决定是否在下一个键之前输出分隔符，
// 此后它将始终保持为真。
type handleState struct {
	h       *commonHandler
	buf     *buffer.Buffer
	freeBuf bool           // buf 是否应被释放？
	sep     string         // 下一个键之前要写入的分隔符
	prefix  *buffer.Buffer // 用于文本：键前缀
	groups  *[]string      // 池分配的、活跃分组的切片，用于 ReplaceAttr
}

var groupPool = sync.Pool{New: func() any {
	s := make([]string, 0, 10)
	return &s
}}

func (h *commonHandler) newHandleState(buf *buffer.Buffer, freeBuf bool, sep string) handleState {
	s := handleState{
		h:       h,
		buf:     buf,
		freeBuf: freeBuf,
		sep:     sep,
		prefix:  buffer.New(),
	}
	if h.opts.ReplaceAttr != nil {
		s.groups = groupPool.Get().(*[]string)
		*s.groups = append(*s.groups, h.groups[:h.nOpenGroups]...)
	}
	return s
}

func (s *handleState) free() {
	if s.freeBuf {
		s.buf.Free()
	}
	if gs := s.groups; gs != nil {
		*gs = (*gs)[:0]
		groupPool.Put(gs)
	}
	s.prefix.Free()
}

func (s *handleState) openGroups() {
	for _, n := range s.h.groups[s.h.nOpenGroups:] {
		s.openGroup(n)
	}
}

// 分组名称和键的分隔符。
const keyComponentSep = '.'

// openGroup 以给定名称启动一个新的属性分组。
func (s *handleState) openGroup(name string) {
	if s.h.json {
		s.appendKey(name)
		s.buf.WriteByte('{')
		s.sep = ""
	} else {
		s.prefix.WriteString(name)
		s.prefix.WriteByte(keyComponentSep)
	}
	// 收集供 ReplaceAttr 使用的分组名称。
	if s.groups != nil {
		*s.groups = append(*s.groups, name)
	}
}

// closeGroup 结束具有给定名称的分组。
func (s *handleState) closeGroup(name string) {
	if s.h.json {
		s.buf.WriteByte('}')
	} else {
		(*s.prefix) = (*s.prefix)[:len(*s.prefix)-len(name)-1 /* 用于 keyComponentSep */]
	}
	s.sep = s.h.attrSep()
	if s.groups != nil {
		*s.groups = (*s.groups)[:len(*s.groups)-1]
	}
}

// appendAttrs 追加 Attrs 切片。
// 它报告是否追加了内容。
func (s *handleState) appendAttrs(as []Attr) bool {
	nonEmpty := false
	for _, a := range as {
		if s.appendAttr(a) {
			nonEmpty = true
		}
	}
	return nonEmpty
}

// appendAttr 追加 Attr 的键和值。
// 它处理替换并检查空键。
// 它报告是否追加了内容。
func (s *handleState) appendAttr(a Attr) bool {
	a.Value = a.Value.Resolve()
	if rep := s.h.opts.ReplaceAttr; rep != nil && a.Value.Kind() != KindGroup {
		var gs []string
		if s.groups != nil {
			gs = *s.groups
		}
		// a.Value 在调用 ReplaceAttr 之前已被解析，因此用户无需这样做。
		a = rep(gs, a)
		// ReplaceAttr 函数可能返回未解析的 Attr。
		a.Value = a.Value.Resolve()
	}
	// 省略空 Attrs。
	if a.isEmpty() {
		return false
	}
	// 特殊情况：Source。
	if v := a.Value; v.Kind() == KindAny {
		if src, ok := v.Any().(*Source); ok {
			if src.isEmpty() {
				return false
			}
			if s.h.json {
				a.Value = src.group()
			} else {
				a.Value = StringValue(fmt.Sprintf("%s:%d", src.File, src.Line))
			}
		}
	}
	if a.Value.Kind() == KindGroup {
		attrs := a.Value.Group()
		// 只输出非空分组。
		if len(attrs) > 0 {
			// 即使分组有 attrs，它也可能最终为空（例如，
			// ReplaceAttr 可能删除所有 attrs）。
			// 因此记住我们在缓冲区中的位置，以便必要时
			// 稍后恢复该位置。
			pos := s.buf.Len()
			// 内联键为空的分组。
			if a.Key != "" {
				s.openGroup(a.Key)
			}
			if !s.appendAttrs(attrs) {
				s.buf.SetLen(pos)
				return false
			}
			if a.Key != "" {
				s.closeGroup(a.Key)
			}
		}
	} else {
		s.appendKey(a.Key)
		s.appendValue(a.Value)
	}
	return true
}

func (s *handleState) appendError(err error) {
	s.appendString(fmt.Sprintf("!ERROR:%v", err))
}

func (s *handleState) appendKey(key string) {
	s.buf.WriteString(s.sep)
	if s.prefix != nil && len(*s.prefix) > 0 {
		s.appendTwoStrings(string(*s.prefix), key)
	} else {
		s.appendString(key)
	}
	if s.h.json {
		s.buf.WriteByte(':')
	} else {
		s.buf.WriteByte('=')
	}
	s.sep = s.h.attrSep()
}

// appendTwoStrings 实现 appendString(prefix + key)，但更快。
func (s *handleState) appendTwoStrings(x, y string) {
	buf := *s.buf
	switch {
	case s.h.json:
		buf.WriteByte('"')
		buf = appendEscapedJSONString(buf, x)
		buf = appendEscapedJSONString(buf, y)
		buf.WriteByte('"')
	case !needsQuoting(x) && !needsQuoting(y):
		buf.WriteString(x)
		buf.WriteString(y)
	default:
		buf = strconv.AppendQuote(buf, x+y)
	}
	*s.buf = buf
}

func (s *handleState) appendString(str string) {
	if s.h.json {
		s.buf.WriteByte('"')
		*s.buf = appendEscapedJSONString(*s.buf, str)
		s.buf.WriteByte('"')
	} else {
		// 文本
		if needsQuoting(str) {
			*s.buf = strconv.AppendQuote(*s.buf, str)
		} else {
			s.buf.WriteString(str)
		}
	}
}

func (s *handleState) appendValue(v Value) {
	defer func() {
		if r := recover(); r != nil {
			// 如果它以 nil 指针 panic，最可能的情况是
			// encoding.TextMarshaler 或 error 未能防范 nil，
			// 此时 "<nil>" 似乎是可行的选择。
			//
			// 改编自 fmt/print.go 中的代码。
			if v := reflect.ValueOf(v.any); v.Kind() == reflect.Pointer && v.IsNil() {
				s.appendString("<nil>")
				return
			}

			// 否则仅打印原始的 panic 消息。
			s.appendString(fmt.Sprintf("!PANIC: %v", r))
		}
	}()

	var err error
	if s.h.json {
		err = appendJSONValue(s, v)
	} else {
		err = appendTextValue(s, v)
	}
	if err != nil {
		s.appendError(err)
	}
}

func (s *handleState) appendTime(t time.Time) {
	if s.h.json {
		appendJSONTime(s, t)
	} else {
		*s.buf = appendRFC3339Millis(*s.buf, t)
	}
}

func appendRFC3339Millis(b []byte, t time.Time) []byte {
	// 按照 time.RFC3339Nano 格式化，因为它经过了高度优化，
	// 但将其截断以使用毫秒精度。
	// 遗憾的是，该格式会去除末尾的 0，因此加上 1/10 毫秒
	// 以保证小数点后恰好有 4 位数字。
	const prefixLen = len("2006-01-02T15:04:05.000")
	n := len(b)
	t = t.Truncate(time.Millisecond).Add(time.Millisecond / 10)
	b = t.AppendFormat(b, time.RFC3339Nano)
	b = append(b[:n+prefixLen], b[n+prefixLen+1:]...) // 去掉第 4 位数字
	return b
}

// DiscardHandler 丢弃所有日志输出。
// DiscardHandler.Enabled 对所有 Level 都返回 false。
var DiscardHandler Handler = discardHandler{}

type discardHandler struct{}

func (dh discardHandler) Enabled(context.Context, Level) bool  { return false }
func (dh discardHandler) Handle(context.Context, Record) error { return nil }
func (dh discardHandler) WithAttrs(attrs []Attr) Handler       { return dh }
func (dh discardHandler) WithGroup(name string) Handler        { return dh }
