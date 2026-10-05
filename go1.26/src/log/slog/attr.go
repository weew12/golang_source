// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"time"
)

// Attr 是一个键值对。
type Attr struct {
	Key   string
	Value Value
}

// String 返回一个用于字符串值的 Attr。
func String(key, value string) Attr {
	return Attr{key, StringValue(value)}
}

// Int64 返回一个用于 int64 的 Attr。
func Int64(key string, value int64) Attr {
	return Attr{key, Int64Value(value)}
}

// Int 将 int 转换为 int64，并返回
// 具有该值的 Attr。
func Int(key string, value int) Attr {
	return Int64(key, int64(value))
}

// Uint64 返回一个用于 uint64 的 Attr。
func Uint64(key string, v uint64) Attr {
	return Attr{key, Uint64Value(v)}
}

// Float64 返回一个用于浮点数的 Attr。
func Float64(key string, v float64) Attr {
	return Attr{key, Float64Value(v)}
}

// Bool 返回一个用于 bool 的 Attr。
func Bool(key string, v bool) Attr {
	return Attr{key, BoolValue(v)}
}

// Time 返回一个用于 [time.Time] 的 Attr。
// 它会丢弃单调时钟部分。
func Time(key string, v time.Time) Attr {
	return Attr{key, TimeValue(v)}
}

// Duration 返回一个用于 [time.Duration] 的 Attr。
func Duration(key string, v time.Duration) Attr {
	return Attr{key, DurationValue(v)}
}

// Group 返回一个用于 Group [Value] 的 Attr。
// 第一个参数是键；其余参数
// 会像 [Logger.Log] 中那样被转换为 Attr。
//
// 使用 Group 可以在日志行中将多个键值对收集到
// 同一个键下，或作为 LogValue 的结果，
// 以便将单个值记录为多个 Attr。
func Group(key string, args ...any) Attr {
	return Attr{key, GroupValue(argsToAttrSlice(args)...)}
}

// GroupAttrs 返回一个由给定 Attr 组成的 Group [Value] 的 Attr。
//
// GroupAttrs 是 [Group] 的更高效版本，
// 它只接受 [Attr] 值。
func GroupAttrs(key string, attrs ...Attr) Attr {
	return Attr{key, GroupValue(attrs...)}
}

func argsToAttrSlice(args []any) []Attr {
	var (
		attr  Attr
		attrs []Attr
	)
	for len(args) > 0 {
		attr, args = argsToAttr(args)
		attrs = append(attrs, attr)
	}
	return attrs
}

// Any 返回一个用于所提供值的 Attr。
// 关于值的处理方式，参见 [AnyValue]。
func Any(key string, value any) Attr {
	return Attr{key, AnyValue(value)}
}

// Equal 报告 a 和 b 是否具有相等的键和值。
func (a Attr) Equal(b Attr) bool {
	return a.Key == b.Key && a.Value.Equal(b.Value)
}

func (a Attr) String() string {
	return a.Key + "=" + a.Value.String()
}

// isEmpty 报告 a 是否具有空键和 nil 值。
// 它可以写成 Attr{} 或 Any("", nil)。
func (a Attr) isEmpty() bool {
	return a.Key == "" && a.Value.num == 0 && a.Value.any == nil
}
