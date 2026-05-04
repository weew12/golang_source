// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sort

import (
	"internal/reflectlite"
	"math/bits"
)

// Slice 使用提供的 less 函数对切片 x 进行排序。
// 如果 x 不是切片，它会 panic。
//
// 不保证排序是稳定的：相等的元素可能与其原始顺序相反。
// 要进行稳定排序，请使用 [SliceStable]。
//
// less 函数必须满足与 Interface 类型的 Less 方法相同的要求。
//
// 注意：在许多情况下，更新的 [slices.SortFunc] 函数更加人性化且运行更快。
func Slice(x any, less func(i, j int) bool) {
	rv := reflectlite.ValueOf(x)
	swap := reflectlite.Swapper(x)
	length := rv.Len()
	limit := bits.Len(uint(length))
	pdqsort_func(lessSwap{less, swap}, 0, length, limit)
}

// SliceStable 使用提供的 less 函数对切片 x 进行排序，
// 保持相等元素在其原始顺序中。
// 如果 x 不是切片，它会 panic。
//
// less 函数必须满足与 Interface 类型的 Less 方法相同的要求。
//
// 注意：在许多情况下，更新的 [slices.SortStableFunc] 函数更加人性化且运行更快。
func SliceStable(x any, less func(i, j int) bool) {
	rv := reflectlite.ValueOf(x)
	swap := reflectlite.Swapper(x)
	stable_func(lessSwap{less, swap}, rv.Len())
}

// SliceIsSorted 报告切片 x 是否根据提供的 less 函数排序。
// 如果 x 不是切片，它会 panic。
//
// 注意：在许多情况下，更新的 [slices.IsSortedFunc] 函数更加人性化且运行更快。
func SliceIsSorted(x any, less func(i, j int) bool) bool {
	rv := reflectlite.ValueOf(x)
	n := rv.Len()
	for i := n - 1; i > 0; i-- {
		if less(i, i-1) {
			return false
		}
	}
	return true
}
