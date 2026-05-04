// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package slices 定义了对任何类型的切片都有用的各种函数。
package slices

import (
	"cmp"
	"math/bits"
	"unsafe"
)

// Equal 报告两个切片是否相等：相同的长度且所有元素都相等。
// 如果长度不同，Equal 返回 false。
// 否则，按递增索引顺序比较元素，并在第一对不相等的元素处停止比较。
// 空切片和 nil 切片被认为是相等的。
// 浮点数 NaN 不被认为是相等的。
func Equal[S ~[]E, E comparable](s1, s2 S) bool {
	if len(s1) != len(s2) {
		return false
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			return false
		}
	}
	return true
}

// EqualFunc 使用等值函数对每对元素进行比较，报告两个切片是否相等。
// 如果长度不同，EqualFunc 返回 false。
// 否则，按递增索引顺序比较元素，并在第一个使 eq 返回 false 的索引处停止比较。
func EqualFunc[S1 ~[]E1, S2 ~[]E2, E1, E2 any](s1 S1, s2 S2, eq func(E1, E2) bool) bool {
	if len(s1) != len(s2) {
		return false
	}
	for i, v1 := range s1 {
		v2 := s2[i]
		if !eq(v1, v2) {
			return false
		}
	}
	return true
}

// Compare 使用 [cmp.Compare] 对每对元素进行比较。
// 元素按顺序比较，从索引 0 开始，直到有一个元素与另一个不相等。
// 返回第一个不匹配元素的比较结果。
// 如果两个切片在其中一个结束时之前都相等，则认为较短的切片比较长的切片小。
// 结果为 0 表示 s1 == s2，-1 表示 s1 < s2，+1 表示 s1 > s2。
func Compare[S ~[]E, E cmp.Ordered](s1, s2 S) int {
	for i, v1 := range s1 {
		if i >= len(s2) {
			return +1
		}
		v2 := s2[i]
		if c := cmp.Compare(v1, v2); c != 0 {
			return c
		}
	}
	if len(s1) < len(s2) {
		return -1
	}
	return 0
}

// CompareFunc 类似于 [Compare]，但对每对元素使用自定义比较函数。
// 结果是 cmp 的第一个非零结果；如果 cmp 始终返回 0，
// 则结果为 0（如果 len(s1) == len(s2)），-1（如果 len(s1) < len(s2)），
// +1（如果 len(s1) > len(s2)）。
func CompareFunc[S1 ~[]E1, S2 ~[]E2, E1, E2 any](s1 S1, s2 S2, cmp func(E1, E2) int) int {
	for i, v1 := range s1 {
		if i >= len(s2) {
			return +1
		}
		v2 := s2[i]
		if c := cmp(v1, v2); c != 0 {
			return c
		}
	}
	if len(s1) < len(s2) {
		return -1
	}
	return 0
}

// Index 返回 v 在 s 中第一次出现的索引，
// 如果不存在则返回 -1。
func Index[S ~[]E, E comparable](s S, v E) int {
	for i := range s {
		if v == s[i] {
			return i
		}
	}
	return -1
}

// IndexFunc 返回满足 f(s[i]) 的第一个索引 i，
// 如果没有满足条件的则返回 -1。
func IndexFunc[S ~[]E, E any](s S, f func(E) bool) int {
	for i := range s {
		if f(s[i]) {
			return i
		}
	}
	return -1
}

// Contains 报告 v 是否存在于 s 中。
func Contains[S ~[]E, E comparable](s S, v E) bool {
	return Index(s, v) >= 0
}

// ContainsFunc 报告是否存在至少一个 s 中的元素 e 满足 f(e)。
func ContainsFunc[S ~[]E, E any](s S, f func(E) bool) bool {
	return IndexFunc(s, f) >= 0
}

// Insert 将值 v... 插入到 s 的索引 i 处，
// 返回修改后的切片。
// s[i:] 中的元素向上移动以腾出空间。
// 在返回的切片 r 中，r[i] == v[0]，
// 并且如果 i < len(s)，则 r[i+len(v)] == 原本在 s[i] 处的值。
// 如果 i > len(s)，Insert 会 panic。
// 此函数的时间复杂度为 O(len(s) + len(v))。
// 如果结果为空，它与 s 具有相同的 nil 性质。
func Insert[S ~[]E, E any](s S, i int, v ...E) S {
	_ = s[i:] // bounds check

	m := len(v)
	if m == 0 {
		return s
	}
	n := len(s)
	if i == n {
		return append(s, v...)
	}
	if n+m > cap(s) {
		// Use append rather than make so that we bump the size of
		// the slice up to the next storage class.
		// This is what Grow does but we don't call Grow because
		// that might copy the values twice.
		s2 := append(s[:i], make(S, n+m-i)...)
		copy(s2[i:], v)
		copy(s2[i+m:], s[i:])
		return s2
	}
	s = s[:n+m]

	// before:
	// s: aaaaaaaabbbbccccccccdddd
	//            ^   ^       ^   ^
	//            i  i+m      n  n+m
	// after:
	// s: aaaaaaaavvvvbbbbcccccccc
	//            ^   ^       ^   ^
	//            i  i+m      n  n+m
	//
	// a are the values that don't move in s.
	// v are the values copied in from v.
	// b and c are the values from s that are shifted up in index.
	// d are the values that get overwritten, never to be seen again.

	if !overlaps(v, s[i+m:]) {
		// Easy case - v does not overlap either the c or d regions.
		// (It might be in some of a or b, or elsewhere entirely.)
		// The data we copy up doesn't write to v at all, so just do it.

		copy(s[i+m:], s[i:])

		// Now we have
		// s: aaaaaaaabbbbbbbbcccccccc
		//            ^   ^       ^   ^
		//            i  i+m      n  n+m
		// Note the b values are duplicated.

		copy(s[i:], v)

		// Now we have
		// s: aaaaaaaavvvvbbbbcccccccc
		//            ^   ^       ^   ^
		//            i  i+m      n  n+m
		// That's the result we want.
		return s
	}

	// The hard case - v overlaps c or d. We can't just shift up
	// the data because we'd move or clobber the values we're trying
	// to insert.
	// So instead, write v on top of d, then rotate.
	copy(s[n:], v)

	// Now we have
	// s: aaaaaaaabbbbccccccccvvvv
	//            ^   ^       ^   ^
	//            i  i+m      n  n+m

	rotateRight(s[i:], m)

	// Now we have
	// s: aaaaaaaavvvvbbbbcccccccc
	//            ^   ^       ^   ^
	//            i  i+m      n  n+m
	// That's the result we want.
	return s
}

// Delete 从 s 中移除元素 s[i:j]，返回修改后的切片。
// 如果 j > len(s) 或 s[i:j] 不是 s 的有效切片，Delete 会 panic。
// Delete 的时间复杂度为 O(len(s)-i)，因此如果需要删除多个元素，
// 最好一次性调用删除所有元素，而不是一次删除一个。
// Delete 会将元素 s[len(s)-(j-i):len(s)] 置零。
// 如果结果为空，它与 s 具有相同的 nil 性质。
func Delete[S ~[]E, E any](s S, i, j int) S {
	_ = s[i:j:len(s)] // 边界检查

	if i == j {
		return s
	}

	oldlen := len(s)
	s = append(s[:i], s[j:]...)
	clear(s[len(s):oldlen]) // 将废弃元素置零/nil，以供 GC
	return s
}

// DeleteFunc 从 s 中移除任何使 del 返回 true 的元素，返回修改后的切片。
// DeleteFunc 会将新长度与原始长度之间的元素置零。
// 如果结果为空，它与 s 具有相同的 nil 性质。
func DeleteFunc[S ~[]E, E any](s S, del func(E) bool) S {
	i := IndexFunc(s, del)
	if i == -1 {
		return s
	}
	// 在找到要删除的元素之前不开始复制元素。
	for j := i + 1; j < len(s); j++ {
		if v := s[j]; !del(v) {
			s[i] = v
			i++
		}
	}
	clear(s[i:]) // 将废弃元素置零/nil，以供 GC
	return s[:i]
}

// Replace 用给定的 v 替换元素 s[i:j]，并返回修改后的切片。
// 如果 j > len(s) 或 s[i:j] 不是 s 的有效切片，Replace 会 panic。
// 当 len(v) < (j-i) 时，Replace 会将新长度与原始长度之间的元素置零。
// 如果结果为空，它与 s 具有相同的 nil 性质。
func Replace[S ~[]E, E any](s S, i, j int, v ...E) S {
	_ = s[i:j] // bounds check

	if i == j {
		return Insert(s, i, v...)
	}
	if j == len(s) {
		s2 := append(s[:i], v...)
		if len(s2) < len(s) {
			clear(s[len(s2):]) // zero/nil out the obsolete elements, for GC
		}
		return s2
	}

	tot := len(s[:i]) + len(v) + len(s[j:])
	if tot > cap(s) {
		// Too big to fit, allocate and copy over.
		s2 := append(s[:i], make(S, tot-i)...) // See Insert
		copy(s2[i:], v)
		copy(s2[i+len(v):], s[j:])
		return s2
	}

	r := s[:tot]

	if i+len(v) <= j {
		// Easy, as v fits in the deleted portion.
		copy(r[i:], v)
		copy(r[i+len(v):], s[j:])
		clear(s[tot:]) // zero/nil out the obsolete elements, for GC
		return r
	}

	// We are expanding (v is bigger than j-i).
	// The situation is something like this:
	// (example has i=4,j=8,len(s)=16,len(v)=6)
	// s: aaaaxxxxbbbbbbbbyy
	//        ^   ^       ^ ^
	//        i   j  len(s) tot
	// a: prefix of s
	// x: deleted range
	// b: more of s
	// y: area to expand into

	if !overlaps(r[i+len(v):], v) {
		// Easy, as v is not clobbered by the first copy.
		copy(r[i+len(v):], s[j:])
		copy(r[i:], v)
		return r
	}

	// This is a situation where we don't have a single place to which
	// we can copy v. Parts of it need to go to two different places.
	// We want to copy the prefix of v into y and the suffix into x, then
	// rotate |y| spots to the right.
	//
	//        v[2:]      v[:2]
	//         |           |
	// s: aaaavvvvbbbbbbbbvv
	//        ^   ^       ^ ^
	//        i   j  len(s) tot
	//
	// If either of those two destinations don't alias v, then we're good.
	y := len(v) - (j - i) // length of y portion

	if !overlaps(r[i:j], v) {
		copy(r[i:j], v[y:])
		copy(r[len(s):], v[:y])
		rotateRight(r[i:], y)
		return r
	}
	if !overlaps(r[len(s):], v) {
		copy(r[len(s):], v[:y])
		copy(r[i:j], v[y:])
		rotateRight(r[i:], y)
		return r
	}

	// Now we know that v overlaps both x and y.
	// That means that the entirety of b is *inside* v.
	// So we don't need to preserve b at all; instead we
	// can copy v first, then copy the b part of v out of
	// v to the right destination.
	k := startIdx(v, s[j:])
	copy(r[i:], v)
	copy(r[i+len(v):], r[i+k:])
	return r
}

// Clone 返回切片的副本。
// 元素通过赋值复制，因此这是浅拷贝。
// 结果可能有额外的未使用容量。
// 结果保留 s 的 nil 性质。
func Clone[S ~[]E, E any](s S) S {
	// 保留 nil 性质以防重要。
	if s == nil {
		return nil
	}
	// 避免 s[:0:0]，因为在克隆大型数组的零长度切片时会导致不必要的存活；
	// 参见 https://go.dev/issue/68488。
	return append(S{}, s...)
}

// Compact 用单个副本替换连续相等的元素运行。
// 这类似于 Unix 上的 uniq 命令。
// Compact 修改切片 s 的内容并返回修改后的切片，
// 其长度可能较小。
// Compact 将新长度与原始长度之间的元素置零。
// 结果保留 s 的 nil 性质。
func Compact[S ~[]E, E comparable](s S) S {
	if len(s) < 2 {
		return s
	}
	for k := 1; k < len(s); k++ {
		if s[k] == s[k-1] {
			s2 := s[k:]
			for k2 := 1; k2 < len(s2); k2++ {
				if s2[k2] != s2[k2-1] {
					s[k] = s2[k2]
					k++
				}
			}

			clear(s[k:]) // 将废弃元素置零/nil，以供 GC
			return s[:k]
		}
	}
	return s
}

// CompactFunc 类似于 [Compact]，但使用等值函数比较元素。
// 对于比较相等的元素运行，CompactFunc 保留第一个。
// CompactFunc 将新长度与原始长度之间的元素置零。
// 结果保留 s 的 nil 性质。
func CompactFunc[S ~[]E, E any](s S, eq func(E, E) bool) S {
	if len(s) < 2 {
		return s
	}
	for k := 1; k < len(s); k++ {
		if eq(s[k], s[k-1]) {
			s2 := s[k:]
			for k2 := 1; k2 < len(s2); k2++ {
				if !eq(s2[k2], s2[k2-1]) {
					s[k] = s2[k2]
					k++
				}
			}

			clear(s[k:]) // 将废弃元素置零/nil，以供 GC
			return s[:k]
		}
	}
	return s
}

// Grow 在必要时增加切片的容量，以保证另外 n 个元素的空间。
// 在 Grow(n) 之后，至少可以追加 n 个元素到切片而无需再次分配。
// 如果 n 为负数或太大无法分配内存，Grow 会 panic。
// 结果保留 s 的 nil 性质。
func Grow[S ~[]E, E any](s S, n int) S {
	if n < 0 {
		panic("cannot be negative")
	}
	if n -= cap(s) - len(s); n > 0 {
		// 此表达式仅分配一次（参见测试）。
		s = append(s[:cap(s)], make([]E, n)...)[:len(s)]
	}
	return s
}

// Clip 从切片中移除未使用的容量，返回 s[:len(s):len(s)]。
// 结果保留 s 的 nil 性质。
func Clip[S ~[]E, E any](s S) S {
	return s[:len(s):len(s)]
}

// TODO：还有其他旋转算法。
// 此算法具有理想的特性，即每个元素最多移动两次。
// follow-cycles 算法可以是 1 次写入，但它不太适合缓存。

// rotateLeft 将 s 左旋 r 个位置。
// s_final[i] = s_orig[i+r]，环绕。
func rotateLeft[E any](s []E, r int) {
	Reverse(s[:r])
	Reverse(s[r:])
	Reverse(s)
}
func rotateRight[E any](s []E, r int) {
	rotateLeft(s, len(s)-r)
}

// overlaps 报告内存范围 a[:len(a)] 和 b[:len(b)] 是否重叠。
func overlaps[E any](a, b []E) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	elemSize := unsafe.Sizeof(a[0])
	if elemSize == 0 {
		return false
	}
	// TODO：一旦有 runtime/unsafe 工具可用就使用它。参见 issue 12445。
	// 另请参见 crypto/internal/fips140/alias/alias.go:AnyOverlap
	return uintptr(unsafe.Pointer(&a[0])) <= uintptr(unsafe.Pointer(&b[len(b)-1]))+(elemSize-1) &&
		uintptr(unsafe.Pointer(&b[0])) <= uintptr(unsafe.Pointer(&a[len(a)-1]))+(elemSize-1)
}

// startIdx 返回 needle 在 haystack 中开始的索引。
// 前置条件：needle 必须完全嵌套在 haystack 中。
func startIdx[E any](haystack, needle []E) int {
	p := &needle[0]
	for i := range haystack {
		if p == &haystack[i] {
			return i
		}
	}
	// TODO：如果重叠的 Es 数量不是整数怎么办？
	panic("needle not found")
}

// Reverse 就地反转切片的元素。
func Reverse[S ~[]E, E any](s S) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// Concat 返回一个新切片，连接传入的切片。
// 如果连接为空，结果为 nil。
func Concat[S ~[]E, E any](slices ...S) S {
	size := 0
	for _, s := range slices {
		size += len(s)
		if size < 0 {
			panic("len out of range")
		}
	}
	// 使用 Grow 而不是 make，以四舍五入到大小类：
	// 否则多余的空间未使用，且有助于调用者向结果追加一些元素。
	newslice := Grow[S](nil, size)
	for _, s := range slices {
		newslice = append(newslice, s...)
	}
	return newslice
}

// Repeat 返回一个新切片，将提供的切片重复指定次数。
// 结果的长度和容量为 (len(x) * count)。
// 结果永远不为 nil。
// 如果 count 为负数，或 (len(x) * count) 的结果溢出，Repeat 会 panic。
func Repeat[S ~[]E, E any](x S, count int) S {
	if count < 0 {
		panic("cannot be negative")
	}

	const maxInt = ^uint(0) >> 1
	hi, lo := bits.Mul(uint(len(x)), uint(count))
	if hi > 0 || lo > maxInt {
		panic("the result of (len(x) * count) overflows")
	}

	newslice := make(S, int(lo)) // lo = len(x) * count
	n := copy(newslice, x)
	for n < len(newslice) {
		n += copy(newslice[n:], newslice[:n])
	}
	return newslice
}
