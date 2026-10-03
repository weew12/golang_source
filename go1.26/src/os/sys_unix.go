// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package os

// supportsCloseOnExec 报告该平台是否支持
// O_CLOEXEC 标志。
// 在 Darwin 上，O_CLOEXEC 标志是在 OS X 10.7（Darwin 11.0.0）中引入的。
// 参见 https://support.apple.com/kb/HT1633。
// 在 FreeBSD 上，O_CLOEXEC 标志是在 8.3 版本中引入的。
const supportsCloseOnExec = true
