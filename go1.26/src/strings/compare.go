// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strings

import "internal/bytealg"

// Compare 返回一个按字典顺序比较两个字符串的整数。
// 如果 a == b，结果为 0；如果 a < b，结果为 -1；如果 a > b，结果为 +1。
//
// 当你需要执行三向比较时（例如与 [slices.SortFunc] 一起使用），请使用 Compare。
// 使用内置的字符串比较运算符 ==、<、> 等通常更清晰，而且总是更快。
func Compare(a, b string) int {
	return bytealg.CompareString(a, b)
}
