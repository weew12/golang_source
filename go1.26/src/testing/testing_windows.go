// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows

package testing

import (
	"errors"
	"internal/syscall/windows"
	"math/bits"
	"syscall"
	"time"
)

// isWindowsRetryable 报告 err 是否是一个 Windows 错误码，
// 该错误码可能通过重试失败的文件系统操作来修复。
func isWindowsRetryable(err error) bool {
	for {
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			break
		}
		err = unwrapped
	}
	if err == syscall.ERROR_ACCESS_DENIED {
		return true // 在 https://go.dev/issue/50051 中观察到。
	}
	if err == windows.ERROR_SHARING_VIOLATION {
		return true // 在 https://go.dev/issue/51442 中观察到。
	}
	return false
}

// highPrecisionTime 使用查询性能计数器表示一个单一的时间点。
// Windows 上 time.Time 的系统粒度较低，不适合
// 测量短时间间隔。
//
// TODO: 如果 Windows 运行时实现了高分辨率计时，那么 highPrecisionTime
// 就可以被移除。
type highPrecisionTime struct {
	now int64
}

// highPrecisionTimeNow 返回用于基准测试的高精度时间。
func highPrecisionTimeNow() highPrecisionTime {
	var t highPrecisionTime
	// 对于 Windows XP 及以上版本，这应当总是成功。
	t.now = windows.QueryPerformanceCounter()
	return t
}

func (a highPrecisionTime) sub(b highPrecisionTime) time.Duration {
	delta := a.now - b.now

	if queryPerformanceFrequency == 0 {
		queryPerformanceFrequency = windows.QueryPerformanceFrequency()
	}
	hi, lo := bits.Mul64(uint64(delta), uint64(time.Second)/uint64(time.Nanosecond))
	quo, _ := bits.Div64(hi, lo, uint64(queryPerformanceFrequency))
	return time.Duration(quo)
}

var queryPerformanceFrequency int64

// highPrecisionTimeSince 返回从 a 起经过的时长。
func highPrecisionTimeSince(a highPrecisionTime) time.Duration {
	return highPrecisionTimeNow().sub(a)
}
