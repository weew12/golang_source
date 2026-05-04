// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slices

import (
	"cmp"
	"iter"
)

// All 返回切片中索引-值对的迭代器，按通常的顺序。
func All[Slice ~[]E, E any](s Slice) iter.Seq2[int, E] {
	return func(yield func(int, E) bool) {
		for i, v := range s {
			if !yield(i, v) {
				return
			}
		}
	}
}

// Backward 返回切片中索引-值对的迭代器，
// 以降序索引反向遍历。
func Backward[Slice ~[]E, E any](s Slice) iter.Seq2[int, E] {
	return func(yield func(int, E) bool) {
		for i := len(s) - 1; i >= 0; i-- {
			if !yield(i, s[i]) {
				return
			}
		}
	}
}

// Values 返回一个迭代器，按顺序产生切片元素。
func Values[Slice ~[]E, E any](s Slice) iter.Seq[E] {
	return func(yield func(E) bool) {
		for _, v := range s {
			if !yield(v) {
				return
			}
		}
	}
}

// AppendSeq 将 seq 中的值追加到切片并返回扩展后的切片。
// 如果 seq 为空，结果保留 s 的 nil 性质。
func AppendSeq[Slice ~[]E, E any](s Slice, seq iter.Seq[E]) Slice {
	for v := range seq {
		s = append(s, v)
	}
	return s
}

// Collect 将 seq 中的值收集到一个新切片中并返回。
// 如果 seq 为空，结果为 nil。
func Collect[E any](seq iter.Seq[E]) []E {
	return AppendSeq([]E(nil), seq)
}

// Sorted 将 seq 中的值收集到一个新切片中，对切片进行排序，然后返回。
// 如果 seq 为空，结果为 nil。
func Sorted[E cmp.Ordered](seq iter.Seq[E]) []E {
	s := Collect(seq)
	Sort(s)
	return s
}

// SortedFunc 将 seq 中的值收集到一个新切片中，使用比较函数对切片进行排序，然后返回。
// 如果 seq 为空，结果为 nil。
func SortedFunc[E any](seq iter.Seq[E], cmp func(E, E) int) []E {
	s := Collect(seq)
	SortFunc(s, cmp)
	return s
}

// SortedStableFunc 将 seq 中的值收集到一个新切片中。
// 然后使用比较函数对切片进行排序，同时保持相等元素的原始顺序。
// 它返回新的切片。
// 如果 seq 为空，结果为 nil。
func SortedStableFunc[E any](seq iter.Seq[E], cmp func(E, E) int) []E {
	s := Collect(seq)
	SortStableFunc(s, cmp)
	return s
}

// Chunk 返回 s 的最多 n 个元素的连续子切片的迭代器。
// 除最后一个子切片外，所有子切片的大小都为 n。
// 所有子切片都被裁剪为没有超出长度的容量。
// 如果 s 为空，则序列为空：序列中没有空切片。
// 如果 n 小于 1，Chunk 会 panic。
func Chunk[Slice ~[]E, E any](s Slice, n int) iter.Seq[Slice] {
	if n < 1 {
		panic("cannot be less than 1")
	}

	return func(yield func(Slice) bool) {
		for i := 0; i < len(s); i += n {
			// 根据需要限制最后一个 chunk 到切片边界。
			end := min(n, len(s[i:]))

			// 设置每个 chunk 的容量，以便向 chunk 追加不会修改原始切片。
			if !yield(s[i : i+end : i+end]) {
				return
			}
		}
	}
}
