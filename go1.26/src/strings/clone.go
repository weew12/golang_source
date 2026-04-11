// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strings

import (
	"internal/stringslite"
)

// Clone 返回 s 的一个新副本。
// 它保证将 s 复制到一个新的内存分配中，这在仅保留一个大字符串的小子串时可能很重要。
// 使用 Clone 可以帮助这类程序减少内存使用。当然，由于使用 Clone 会进行复制，
// 过度使用 Clone 会导致程序使用更多内存。
// Clone 通常应仅在极少数情况下使用，并且仅当性能分析表明需要它时才使用。
// 对于长度为零的字符串，将返回字符串 "" 且不会进行内存分配。
func Clone(s string) string {
	return stringslite.Clone(s)
}
