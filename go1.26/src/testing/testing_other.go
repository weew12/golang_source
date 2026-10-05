// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows

package testing

import "time"

// isWindowsRetryable 报告 err 是否是一个 Windows 错误码，
// 该错误码可能通过重试失败的文件系统操作来修复。
func isWindowsRetryable(err error) bool {
	return false
}

// highPrecisionTime 表示一个单一的时间点。
// 在除 Windows 之外的所有系统上，使用 time.Time 即可。
type highPrecisionTime struct {
	now time.Time
}

// highPrecisionTimeNow 返回用于基准测试的高精度时间。
func highPrecisionTimeNow() highPrecisionTime {
	return highPrecisionTime{now: time.Now()}
}

// highPrecisionTimeSince 返回从 b 起经过的时长。
func highPrecisionTimeSince(b highPrecisionTime) time.Duration {
	return time.Since(b.now)
}
