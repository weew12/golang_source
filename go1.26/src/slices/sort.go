// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run $GOROOT/src/sort/gen_sort_variants.go -generic

package slices

import (
	"cmp"
	"math/bits"
)

// Sort 按升序对任何可排序类型的切片进行排序。
// 在对浮点数进行排序时，NaN 排在其他值之前。
func Sort[S ~[]E, E cmp.Ordered](x S) {
	n := len(x)
	pdqsortOrdered(x, 0, n, bits.Len(uint(n)))
}

// SortFunc 根据 cmp 函数确定的方式对切片 x 按升序排序。
// 此排序不保证是稳定的。
// cmp(a, b) 应该在 a < b 时返回负数，在 a > b 时返回正数，
// 在 a == b 或 a 和 b 以严格弱序而言不可比较时返回零。
//
// SortFunc 要求 cmp 是严格弱序。
// 参见 https://en.wikipedia.org/wiki/Weak_ordering#Strict_weak_orderings。
// 对于不可比较的项，函数应返回 0。
func SortFunc[S ~[]E, E any](x S, cmp func(a, b E) int) {
	n := len(x)
	pdqsortCmpFunc(x, 0, n, bits.Len(uint(n)), cmp)
}

// SortStableFunc 对切片 x 进行排序，同时保持相等元素的原始顺序，
// 使用 cmp 以与 [SortFunc] 相同的方式比较元素。
func SortStableFunc[S ~[]E, E any](x S, cmp func(a, b E) int) {
	stableCmpFunc(x, len(x), cmp)
}

// IsSorted 报告 x 是否按升序排序。
func IsSorted[S ~[]E, E cmp.Ordered](x S) bool {
	for i := len(x) - 1; i > 0; i-- {
		if cmp.Less(x[i], x[i-1]) {
			return false
		}
	}
	return true
}

// IsSortedFunc 报告 x 是否按升序排序，cmp 作为 [SortFunc] 所定义的比较函数。
func IsSortedFunc[S ~[]E, E any](x S, cmp func(a, b E) int) bool {
	for i := len(x) - 1; i > 0; i-- {
		if cmp(x[i], x[i-1]) < 0 {
			return false
		}
	}
	return true
}

// Min 返回 x 中的最小值。如果 x 为空，它会 panic。
// 对于浮点数，Min 传播 NaN（x 中的任何 NaN 值都会使输出为 NaN）。
func Min[S ~[]E, E cmp.Ordered](x S) E {
	if len(x) < 1 {
		panic("slices.Min: empty list")
	}
	m := x[0]
	for i := 1; i < len(x); i++ {
		m = min(m, x[i])
	}
	return m
}

// MinFunc 返回 x 中的最小值，使用 cmp 比较元素。
// 如果 x 为空，它会 panic。如果根据 cmp 函数有多个最小元素，
// MinFunc 返回第一个。
func MinFunc[S ~[]E, E any](x S, cmp func(a, b E) int) E {
	if len(x) < 1 {
		panic("slices.MinFunc: empty list")
	}
	m := x[0]
	for i := 1; i < len(x); i++ {
		if cmp(x[i], m) < 0 {
			m = x[i]
		}
	}
	return m
}

// Max 返回 x 中的最大值。如果 x 为空，它会 panic。
// 对于浮点数 E，Max 传播 NaN（x 中的任何 NaN 值都会使输出为 NaN）。
func Max[S ~[]E, E cmp.Ordered](x S) E {
	if len(x) < 1 {
		panic("slices.Max: empty list")
	}
	m := x[0]
	for i := 1; i < len(x); i++ {
		m = max(m, x[i])
	}
	return m
}

// MaxFunc 返回 x 中的最大值，使用 cmp 比较元素。
// 如果 x 为空，它会 panic。如果根据 cmp 函数有多个最大元素，
// MaxFunc 返回第一个。
func MaxFunc[S ~[]E, E any](x S, cmp func(a, b E) int) E {
	if len(x) < 1 {
		panic("slices.MaxFunc: empty list")
	}
	m := x[0]
	for i := 1; i < len(x); i++ {
		if cmp(x[i], m) > 0 {
			m = x[i]
		}
	}
	return m
}

// BinarySearch 在已排序的切片中搜索 target，并返回找到 target 的最早位置，
// 或者 target 在排序顺序中应该出现的位置；它还返回一个布尔值，
// 说明 target 是否真的在切片中找到。切片必须按升序排序。
func BinarySearch[S ~[]E, E cmp.Ordered](x S, target E) (int, bool) {
	// 内联比用 lambda 调用 BinarySearchFunc 更快。
	n := len(x)
	// 定义 x[-1] < target 且 x[n] >= target。
	// 不变量：x[i-1] < target, x[j] >= target。
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1) // 计算 h 时避免溢出
		// i ≤ h < j
		if cmp.Less(x[h], target) {
			i = h + 1 // 保持 x[i-1] < target
		} else {
			j = h // 保持 x[j] >= target
		}
	}
	// i == j, x[i-1] < target, and x[j] (= x[i]) >= target  => 答案为 i。
	return i, i < n && (x[i] == target || (isNaN(x[i]) && isNaN(target)))
}

// BinarySearchFunc 的工作方式与 [BinarySearch] 类似，但使用自定义比较函数。
// 切片必须按升序排序，其中"升序"由 cmp 定义。
// cmp 应该在切片元素与 target 匹配时返回 0，
// 如果切片元素在 target 之前则返回负数，
// 如果切片元素在 target 之后则返回正数。
// cmp 必须实现与切片相同的排序，使得如果 cmp(a, t) < 0 且 cmp(b, t) >= 0，
// 则 a 必须在切片中排在 b 之前。
func BinarySearchFunc[S ~[]E, E, T any](x S, target T, cmp func(E, T) int) (int, bool) {
	n := len(x)
	// 定义 cmp(x[-1], target) < 0 且 cmp(x[n], target) >= 0 。
	// 不变量：cmp(x[i - 1], target) < 0, cmp(x[j], target) >= 0。
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1) // 计算 h 时避免溢出
		// i ≤ h < j
		if cmp(x[h], target) < 0 {
			i = h + 1 // 保持 cmp(x[i - 1], target) < 0
		} else {
			j = h // 保持 cmp(x[j], target) >= 0
		}
	}
	// i == j, cmp(x[i-1], target) < 0, and cmp(x[j], target) (= cmp(x[i], target)) >= 0  => 答案为 i。
	return i, i < n && cmp(x[i], target) == 0
}

type sortedHint int // hint for pdqsort when choosing the pivot

const (
	unknownHint sortedHint = iota
	increasingHint
	decreasingHint
)

// xorshift paper: https://www.jstatsoft.org/article/view/v008i14/xorshift.pdf
type xorshift uint64

func (r *xorshift) Next() uint64 {
	*r ^= *r << 13
	*r ^= *r >> 7
	*r ^= *r << 17
	return uint64(*r)
}

func nextPowerOfTwo(length int) uint {
	return 1 << bits.Len(uint(length))
}

// isNaN 报告 x 是否为 NaN，无需引入 math 包。
// 如果 T 不是浮点类型，这将始终返回 false。
func isNaN[T cmp.Ordered](x T) bool {
	return x != x
}
