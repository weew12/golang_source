// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package internal

import "errors"

// NotForPublicUse 是一个标记类型，表示某个 API 仅供内部使用。
// 它并不能完全阻止对该 API 的使用，但有助于限制使用。
// 任何带有此标记的内容都不受 Go 兼容性承诺的约束。
type NotForPublicUse struct{}

// AllowInternalUse 从 "json" 传递给 "jsontext"，
// 用于认证调用方可以访问内部功能。
var AllowInternalUse NotForPublicUse

// 在 jsonv1 和 jsonv2 之间内部共享的哨兵错误值。
var (
	ErrCycle           = errors.New("encountered a cycle")
	ErrNonNilReference = errors.New("value must be passed as a non-nil pointer reference")
	ErrNilInterface    = errors.New("cannot derive concrete type for nil interface with finite type set")
)

var (
	// TransformMarshalError 将 v2 错误转换为 v1 错误。
	// 它只在 Marshal 函数的顶层被调用。
	TransformMarshalError func(any, error) error
	// NewMarshalerError 构造一个 jsonv1.MarshalerError。
	// 它在用户定义的 Marshal 方法/函数失败后被调用。
	NewMarshalerError func(any, error, string) error
	// TransformUnmarshalError 将 v2 错误转换为 v1 错误。
	// 它只在 Unmarshal 函数的顶层被调用。
	TransformUnmarshalError func(any, error) error

	// NewRawNumber 返回 new(jsonv1.Number)。
	NewRawNumber func() any
	// RawNumberOf 返回 jsonv1.Number(b)。
	RawNumberOf func(b []byte) any
)
