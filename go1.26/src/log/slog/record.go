// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"runtime"
	"slices"
	"time"
)

const nAttrsInline = 5

// Record 保存关于日志事件的信息。
// Record 的副本共享状态。
// 在将 Record 的副本分发出去后，不要修改该 Record。
// 调用 [NewRecord] 创建新的 Record。
// 使用 [Record.Clone] 创建一个不共享状态的副本。
type Record struct {
	// 调用输出方法（Log、Info 等）的时间。
	Time time.Time

	// 日志消息。
	Message string

	// 事件的级别。
	Level Level

	// 构造记录时的程序计数器，由 runtime.Callers 确定。
	// 如果为零，则表示没有可用的程序计数器。
	//
	// 该值的唯一有效用途是作为 [runtime.CallersFrames] 的参数。
	// 特别是，不得将其传递给 [runtime.FuncForPC]。
	PC uintptr

	// 分配优化：一个内联数组，其大小足以容纳
	// 大多数日志调用（基于对开源代码的考察）。
	// 它保存 Attrs 列表的开头部分。
	front [nAttrsInline]Attr

	// front 中 Attr 的数量。
	nFront int

	// 除 front 中那些之外的 Attrs 列表。
	// 不变式：
	//   - len(back) > 0 当且仅当 nFront == len(front)
	//   - 未使用的数组元素为零。用于检测错误。
	back []Attr
}

// NewRecord 根据给定的参数创建一个 [Record]。
// 使用 [Record.AddAttrs] 向 Record 添加属性。
//
// NewRecord 适用于希望将 [Handler] 作为
// 后端支持的日志记录 API。
func NewRecord(t time.Time, level Level, msg string, pc uintptr) Record {
	return Record{
		Time:    t,
		Message: msg,
		Level:   level,
		PC:      pc,
	}
}

// Clone 返回记录的一个不共享状态的副本。
// 原始记录和副本都可以被修改，
// 而不会相互干扰。
func (r Record) Clone() Record {
	r.back = slices.Clip(r.back) // 防止 append 修改共享数组
	return r
}

// NumAttrs 返回 [Record] 中属性的数量。
func (r Record) NumAttrs() int {
	return r.nFront + len(r.back)
}

// Attrs 对 [Record] 中的每个 Attr 调用 f。
// 如果 f 返回 false，则停止迭代。
func (r Record) Attrs(f func(Attr) bool) {
	for i := 0; i < r.nFront; i++ {
		if !f(r.front[i]) {
			return
		}
	}
	for _, a := range r.back {
		if !f(a) {
			return
		}
	}
}

// AddAttrs 将给定的 Attrs 追加到 [Record] 的 Attrs 列表中。
// 它会省略空分组。
func (r *Record) AddAttrs(attrs ...Attr) {
	var i int
	for i = 0; i < len(attrs) && r.nFront < len(r.front); i++ {
		a := attrs[i]
		if a.Value.isEmptyGroup() {
			continue
		}
		r.front[r.nFront] = a
		r.nFront++
	}
	// 通过向后越界切片并查看该处的 Attr 是否非零，
	// 来检查副本是否被修改过。
	if cap(r.back) > len(r.back) {
		end := r.back[:len(r.back)+1][len(r.back)]
		if !end.isEmpty() {
			// 不要 panic；复制并设法应付过去。
			r.back = slices.Clip(r.back)
			r.back = append(r.back, String("!BUG", "AddAttrs unsafely called on copy of Record made without using Record.Clone"))
		}
	}
	ne := countEmptyGroups(attrs[i:])
	r.back = slices.Grow(r.back, len(attrs[i:])-ne)
	for _, a := range attrs[i:] {
		if !a.Value.isEmptyGroup() {
			r.back = append(r.back, a)
		}
	}
}

// Add 按照 [Logger.Log] 中描述的方式将 args 转换为 Attrs，
// 然后将这些 Attrs 追加到 [Record] 的 Attrs 列表中。
// 它会省略空分组。
func (r *Record) Add(args ...any) {
	var a Attr
	for len(args) > 0 {
		a, args = argsToAttr(args)
		if a.Value.isEmptyGroup() {
			continue
		}
		if r.nFront < len(r.front) {
			r.front[r.nFront] = a
			r.nFront++
		} else {
			if r.back == nil {
				r.back = make([]Attr, 0, countAttrs(args)+1)
			}
			r.back = append(r.back, a)
		}
	}
}

// countAttrs 返回根据 args 将创建的 Attr 数量。
func countAttrs(args []any) int {
	n := 0
	for i := 0; i < len(args); i++ {
		n++
		if _, ok := args[i].(string); ok {
			i++
		}
	}
	return n
}

const badKey = "!BADKEY"

// argsToAttr 将非空 args 切片的一个前缀转换为一个 Attr，
// 并返回该切片中未被消费的部分。
// 如果 args[0] 是一个 Attr，则返回它。
// 如果 args[0] 是一个字符串，则将前两个元素视为
// 一个键值对。
// 否则，将 args[0] 视为一个缺少键的值。
func argsToAttr(args []any) (Attr, []any) {
	switch x := args[0].(type) {
	case string:
		if len(args) == 1 {
			return String(badKey, x), nil
		}
		return Any(x, args[1]), args[2:]

	case Attr:
		return x, args[1:]

	default:
		return Any(badKey, x), args[1:]
	}
}

// Source 描述一行源代码的位置。
type Source struct {
	// Function 是包含该源代码行的、以包路径限定的函数名。
	// 如果非空，该字符串唯一标识程序中的单个函数。
	// 如果未知，则可能是空字符串。
	Function string `json:"function"`
	// File 和 Line 是该源代码行的文件名和行号（从 1 开始）。
	// 如果未知，它们可能分别为空字符串和零。
	File string `json:"file"`
	Line int    `json:"line"`
}

// group 将 s 的非零字段作为 attrs 切片返回。
// 它类似于 LogValue 方法，但我们不希望 Source
// 实现 LogValuer，因为那样它会在
// ReplaceAttr 函数被调用之前就被解析。
func (s *Source) group() Value {
	var as []Attr
	if s.Function != "" {
		as = append(as, String("function", s.Function))
	}
	if s.File != "" {
		as = append(as, String("file", s.File))
	}
	if s.Line != 0 {
		as = append(as, Int("line", s.Line))
	}
	return GroupValue(as...)
}

// isEmpty 返回 Source 结构体是否为 nil 或只包含零值字段。
func (s *Source) isEmpty() bool { return s == nil || *s == Source{} }

// Source 使用 r 的 PC 为日志事件返回一个新的 Source。
// 如果 PC 字段为零，意味着创建 Record 时缺少必要信息
// 或位置不可用，则返回 nil。
func (r Record) Source() *Source {
	if r.PC == 0 {
		return nil
	}

	fs := runtime.CallersFrames([]uintptr{r.PC})
	f, _ := fs.Next()
	return &Source{
		Function: f.Function,
		File:     f.File,
		Line:     f.Line,
	}
}
