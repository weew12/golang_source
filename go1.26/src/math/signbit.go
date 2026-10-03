// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Signbit 报告 x 是否为负数或负零。
func Signbit(x float64) bool {
	return int64(Float64bits(x)) < 0
}
