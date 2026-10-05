// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal

// 如果 IgnorePC 为 true，则不调用 runtime.Callers 来获取 pc。
// 这仅用于对 runtime.Callers 带来的性能下降进行基准测试。
var IgnorePC = false
