// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package internal 包含 log 和 log/slog 两者都会使用的定义。
package internal

// DefaultOutput 保存一个函数，该函数会调用默认 log.Logger 的
// 输出函数。
// 它允许 slog.defaultHandler 调用 log 包中未导出的函数。
var DefaultOutput func(pc uintptr, data []byte) error
