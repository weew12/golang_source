// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1

package os

// supportsCloseOnExec 报告该平台是否支持
// O_CLOEXEC 标志。
const supportsCloseOnExec = false
