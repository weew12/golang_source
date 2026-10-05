// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package slogtest 支持测试 log/slog.Handler 的实现。
package slogtest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"testing"
	"time"
)

type testCase struct {
	// 子测试名称。
	name string
	// 若非空，explanation 解释所违反的约束。
	explanation string
	// f 使用其参数 logger 执行单个日志事件。
	// 为了让 mkdescs.sh 生成正确的描述，
	// f 的函数体必须出现在单行中，且其第一个
	// 非空白字符为 "l."。
	f func(*slog.Logger)
	// 若 mod 非 nil，则在 Record 传给 Handler 之前
	// 调用它来修改 Logger 生成的 Record。
	mod func(*slog.Record)
	// checks 是要对结果运行的检查列表。
	checks []check
}

var cases = []testCase{
	{
		name:        "built-ins",
		explanation: withSource("this test expects slog.TimeKey, slog.LevelKey and slog.MessageKey"),
		f: func(l *slog.Logger) {
			l.Info("message")
		},
		checks: []check{
			hasKey(slog.TimeKey),
			hasKey(slog.LevelKey),
			hasAttr(slog.MessageKey, "message"),
		},
	},
	{
		name:        "attrs",
		explanation: withSource("a Handler should output attributes passed to the logging function"),
		f: func(l *slog.Logger) {
			l.Info("message", "k", "v")
		},
		checks: []check{
			hasAttr("k", "v"),
		},
	},
	{
		name:        "empty-attr",
		explanation: withSource("a Handler should ignore an empty Attr"),
		f: func(l *slog.Logger) {
			l.Info("msg", "a", "b", "", nil, "c", "d")
		},
		checks: []check{
			hasAttr("a", "b"),
			missingKey(""),
			hasAttr("c", "d"),
		},
	},
	{
		name:        "zero-time",
		explanation: withSource("a Handler should ignore a zero Record.Time"),
		f: func(l *slog.Logger) {
			l.Info("msg", "k", "v")
		},
		mod: func(r *slog.Record) { r.Time = time.Time{} },
		checks: []check{
			missingKey(slog.TimeKey),
		},
	},
	{
		name:        "WithAttrs",
		explanation: withSource("a Handler should include the attributes from the WithAttrs method"),
		f: func(l *slog.Logger) {
			l.With("a", "b").Info("msg", "k", "v")
		},
		checks: []check{
			hasAttr("a", "b"),
			hasAttr("k", "v"),
		},
	},
	{
		name:        "groups",
		explanation: withSource("a Handler should handle Group attributes"),
		f: func(l *slog.Logger) {
			l.Info("msg", "a", "b", slog.Group("G", slog.String("c", "d")), "e", "f")
		},
		checks: []check{
			hasAttr("a", "b"),
			inGroup("G", hasAttr("c", "d")),
			hasAttr("e", "f"),
		},
	},
	{
		name:        "empty-group",
		explanation: withSource("a Handler should ignore an empty group"),
		f: func(l *slog.Logger) {
			l.Info("msg", "a", "b", slog.Group("G"), "e", "f")
		},
		checks: []check{
			hasAttr("a", "b"),
			missingKey("G"),
			hasAttr("e", "f"),
		},
	},
	{
		name:        "inline-group",
		explanation: withSource("a Handler should inline the Attrs of a group with an empty key"),
		f: func(l *slog.Logger) {
			l.Info("msg", "a", "b", slog.Group("", slog.String("c", "d")), "e", "f")

		},
		checks: []check{
			hasAttr("a", "b"),
			hasAttr("c", "d"),
			hasAttr("e", "f"),
		},
	},
	{
		name:        "WithGroup",
		explanation: withSource("a Handler should handle the WithGroup method"),
		f: func(l *slog.Logger) {
			l.WithGroup("G").Info("msg", "a", "b")
		},
		checks: []check{
			hasKey(slog.TimeKey),
			hasKey(slog.LevelKey),
			hasAttr(slog.MessageKey, "msg"),
			missingKey("a"),
			inGroup("G", hasAttr("a", "b")),
		},
	},
	{
		name:        "multi-With",
		explanation: withSource("a Handler should handle multiple WithGroup and WithAttr calls"),
		f: func(l *slog.Logger) {
			l.With("a", "b").WithGroup("G").With("c", "d").WithGroup("H").Info("msg", "e", "f")
		},
		checks: []check{
			hasKey(slog.TimeKey),
			hasKey(slog.LevelKey),
			hasAttr(slog.MessageKey, "msg"),
			hasAttr("a", "b"),
			inGroup("G", hasAttr("c", "d")),
			inGroup("G", inGroup("H", hasAttr("e", "f"))),
		},
	},
	{
		name:        "empty-group-record",
		explanation: withSource("a Handler should not output groups if there are no attributes"),
		f: func(l *slog.Logger) {
			l.With("a", "b").WithGroup("G").With("c", "d").WithGroup("H").Info("msg")
		},
		checks: []check{
			hasKey(slog.TimeKey),
			hasKey(slog.LevelKey),
			hasAttr(slog.MessageKey, "msg"),
			hasAttr("a", "b"),
			inGroup("G", hasAttr("c", "d")),
			inGroup("G", missingKey("H")),
		},
	},
	{
		name:        "nested-empty-group-record",
		explanation: withSource("a Handler should not output nested groups if there are no attributes"),
		f: func(l *slog.Logger) {
			l.With("a", "b").WithGroup("G").With("c", "d").WithGroup("H").WithGroup("I").Info("msg")
		},
		checks: []check{
			hasKey(slog.TimeKey),
			hasKey(slog.LevelKey),
			hasAttr(slog.MessageKey, "msg"),
			hasAttr("a", "b"),
			inGroup("G", hasAttr("c", "d")),
			inGroup("G", missingKey("H")),
			inGroup("G", missingKey("I")),
		},
	},
	{
		name:        "resolve",
		explanation: withSource("a Handler should call Resolve on attribute values"),
		f: func(l *slog.Logger) {
			l.Info("msg", "k", &replace{"replaced"})
		},
		checks: []check{hasAttr("k", "replaced")},
	},
	{
		name:        "resolve-groups",
		explanation: withSource("a Handler should call Resolve on attribute values in groups"),
		f: func(l *slog.Logger) {
			l.Info("msg",
				slog.Group("G",
					slog.String("a", "v1"),
					slog.Any("b", &replace{"v2"})))
		},
		checks: []check{
			inGroup("G", hasAttr("a", "v1")),
			inGroup("G", hasAttr("b", "v2")),
		},
	},
	{
		name:        "resolve-WithAttrs",
		explanation: withSource("a Handler should call Resolve on attribute values from WithAttrs"),
		f: func(l *slog.Logger) {
			l = l.With("k", &replace{"replaced"})
			l.Info("msg")
		},
		checks: []check{hasAttr("k", "replaced")},
	},
	{
		name:        "resolve-WithAttrs-groups",
		explanation: withSource("a Handler should call Resolve on attribute values in groups from WithAttrs"),
		f: func(l *slog.Logger) {
			l = l.With(slog.Group("G",
				slog.String("a", "v1"),
				slog.Any("b", &replace{"v2"})))
			l.Info("msg")
		},
		checks: []check{
			inGroup("G", hasAttr("a", "v1")),
			inGroup("G", hasAttr("b", "v2")),
		},
	},
	{
		name:        "empty-PC",
		explanation: withSource("a Handler should not output SourceKey if the PC is zero"),
		f: func(l *slog.Logger) {
			l.Info("message")
		},
		mod: func(r *slog.Record) { r.PC = 0 },
		checks: []check{
			missingKey(slog.SourceKey),
		},
	},
}

// TestHandler 测试一个 [slog.Handler]。
// 如果 TestHandler 发现任何不当行为，它会为每个问题返回一个错误，
// 并用 [errors.Join] 合并为单个错误。
//
// TestHandler 将给定的 Handler 安装到 [slog.Logger] 中，
// 并对 Logger 的输出方法进行若干次调用。
// Handler 应针对 Info 及更高级别启用。
//
// results 函数在所有这些调用之后被调用。
// 它应返回一个 map[string]any 切片，对应每次对 Logger 输出方法的调用。
// 该 map 的键和值应与 Handler 输出的键和值相对应。
// 输出中的每个组应表示为其自身的嵌套 map[string]any。
// 应使用标准键 [slog.TimeKey]、[slog.LevelKey] 和 [slog.MessageKey]。
//
// 如果 Handler 输出 JSON，则使用 `map[string]any` 调用 [encoding/json.Unmarshal]
// 会创建正确的数据结构。
//
// 如果 Handler 有意丢弃某个被测试检查的属性，
// 那么 results 函数应检查其不存在，并将其添加到返回的 map 中。
func TestHandler(h slog.Handler, results func() []map[string]any) error {
	// 在测试用例上运行 handler。
	for _, c := range cases {
		ht := h
		if c.mod != nil {
			ht = &wrapper{h, c.mod}
		}
		l := slog.New(ht)
		c.f(l)
	}

	// 收集并检查结果。
	var errs []error
	res := results()
	if g, w := len(res), len(cases); g != w {
		return fmt.Errorf("got %d results, want %d", g, w)
	}
	for i, got := range res {
		c := cases[i]
		for _, check := range c.checks {
			if problem := check(got); problem != "" {
				errs = append(errs, fmt.Errorf("%s: %s", problem, c.explanation))
			}
		}
	}
	return errors.Join(errs...)
}

// Run 在与 [TestHandler] 相同的测试用例上演练 [slog.Handler]，但
// 会在子测试中运行每个用例。对于每个测试用例，它首先调用 newHandler
// 以获取被测 handler 的实例，然后运行测试用例，接着
// 调用 result 获取结果。如果测试用例失败，它会调用 t.Error。
func Run(t *testing.T, newHandler func(*testing.T) slog.Handler, result func(*testing.T) map[string]any) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHandler(t)
			if c.mod != nil {
				h = &wrapper{h, c.mod}
			}
			l := slog.New(h)
			c.f(l)
			got := result(t)
			for _, check := range c.checks {
				if p := check(got); p != "" {
					t.Errorf("%s: %s", p, c.explanation)
				}
			}
		})
	}
}

type check func(map[string]any) string

func hasKey(key string) check {
	return func(m map[string]any) string {
		if _, ok := m[key]; !ok {
			return fmt.Sprintf("missing key %q", key)
		}
		return ""
	}
}

func missingKey(key string) check {
	return func(m map[string]any) string {
		if _, ok := m[key]; ok {
			return fmt.Sprintf("unexpected key %q", key)
		}
		return ""
	}
}

func hasAttr(key string, wantVal any) check {
	return func(m map[string]any) string {
		if s := hasKey(key)(m); s != "" {
			return s
		}
		gotVal := m[key]
		if !reflect.DeepEqual(gotVal, wantVal) {
			return fmt.Sprintf("%q: got %#v, want %#v", key, gotVal, wantVal)
		}
		return ""
	}
}

func inGroup(name string, c check) check {
	return func(m map[string]any) string {
		v, ok := m[name]
		if !ok {
			return fmt.Sprintf("missing group %q", name)
		}
		g, ok := v.(map[string]any)
		if !ok {
			return fmt.Sprintf("value for group %q is not map[string]any", name)
		}
		return c(g)
	}
}

type wrapper struct {
	slog.Handler
	mod func(*slog.Record)
}

func (h *wrapper) Handle(ctx context.Context, r slog.Record) error {
	h.mod(&r)
	return h.Handler.Handle(ctx, r)
}

func withSource(s string) string {
	_, file, line, ok := runtime.Caller(1)
	if !ok {
		panic("runtime.Caller failed")
	}
	return fmt.Sprintf("%s (%s:%d)", s, file, line)
}

type replace struct {
	v any
}

func (r *replace) LogValue() slog.Value { return slog.AnyValue(r.v) }

func (r *replace) String() string {
	return fmt.Sprintf("<replace(%v)>", r.v)
}
