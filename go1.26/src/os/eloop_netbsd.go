// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build netbsd

package os

import "syscall"

// isNoFollowErr 报告 err 是否可能由 O_NOFOLLOW 阻止打开操作而导致。
func isNoFollowErr(err error) bool {
	// NetBSD 返回 EFTYPE，但也检查其他可能性。
	switch err {
	case syscall.ELOOP, syscall.EMLINK, syscall.EFTYPE:
		return true
	}
	return false
}
