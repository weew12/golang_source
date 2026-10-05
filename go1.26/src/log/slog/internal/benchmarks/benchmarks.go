// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package benchmarks 包含 slog 的基准测试。
//
// 这些基准测试大致基于 github.com/uber-go/zap/benchmarks。
// 它们具有以下期望的特性：
//
//   - 它们测试一个完整的日志事件，从用户的调用到其返回。
//
//   - 被基准测试的代码在多个 goroutine 中并发运行，以
//     更好地模拟真实服务器（结构化日志最常见的运行环境）。
//
//   - 一些 handler 是真实 handler 的乐观版本，尽可能快地完成真实世界
//     的任务（有时更快，因为其实现可能不是并发安全的）。这为我们提供了
//     handler 性能的上界，因此我们可以在端到端环境中评估包中
//     （与 handler 无关的）核心活动，而不必担心缓慢的 handler 实现
//     会使结果产生偏差。
//
//   - 我们还测试了内置的 handler，以便进行比较。
package benchmarks

import (
	"errors"
	"log/slog"
	"time"
)

const testMessage = "Test logging, but use a somewhat realistic message length."

type event struct {
	ID    string
	Index int
	Flag  bool
}

var (
	testTime     = time.Date(2022, time.May, 1, 0, 0, 0, 0, time.UTC)
	testString   = "7e3b3b2aaeff56a7108fe11e154200dd/7819479873059528190"
	testInt      = 32768
	testDuration = 23 * time.Second
	testError    = errors.New("fail")
	testEvent    = event{"abcdefgh", 65536, true}
)

var testAttrs = []slog.Attr{
	slog.String("string", testString),
	slog.Int("status", testInt),
	slog.Duration("duration", testDuration),
	slog.Time("time", testTime),
	slog.Any("error", testError),
}

const wantText = "time=1651363200 level=0 msg=Test logging, but use a somewhat realistic message length. string=7e3b3b2aaeff56a7108fe11e154200dd/7819479873059528190 status=32768 duration=23000000000 time=1651363200 error=fail\n"
