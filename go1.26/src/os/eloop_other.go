// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build aix || darwin || dragonfly || freebsd || linux || openbsd || solaris || wasip1

package os

import (
	"runtime"
	"syscall"
)

// isNoFollowErr 报告 err 是否可能由 O_NOFOLLOW 阻止打开操作而导致。
func isNoFollowErr(err error) bool {
	switch err {
	case syscall.ELOOP, syscall.EMLINK:
		return true
	}
	if runtime.GOOS == "dragonfly" {
		// 在这种情况下，Dragonfly 似乎会从 openat 返回 EINVAL。
		if err == syscall.EINVAL {
			return true
		}
	}
	return false
}
