// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maps

import "iter"

// All 返回对 m 中键值对的迭代器。
// 迭代顺序未指定，不保证与下一次调用相同。
func All[Map ~map[K]V, K comparable, V any](m Map) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for k, v := range m {
			if !yield(k, v) {
				return
			}
		}
	}
}

// Keys 返回对 m 中键的迭代器。
// 迭代顺序未指定，不保证与下一次调用相同。
func Keys[Map ~map[K]V, K comparable, V any](m Map) iter.Seq[K] {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// Values 返回对 m 中值的迭代器。
// 迭代顺序未指定，不保证与下一次调用相同。
func Values[Map ~map[K]V, K comparable, V any](m Map) iter.Seq[V] {
	return func(yield func(V) bool) {
		for _, v := range m {
			if !yield(v) {
				return
			}
		}
	}
}

// Insert 将 seq 中的键值对添加到 m。
// 如果 seq 中的键已存在于 m 中，其值将被覆盖。
func Insert[Map ~map[K]V, K comparable, V any](m Map, seq iter.Seq2[K, V]) {
	for k, v := range seq {
		m[k] = v
	}
}

// Collect 将 seq 中的键值对收集到一个新 map 中并返回。
func Collect[K comparable, V any](seq iter.Seq2[K, V]) map[K]V {
	m := make(map[K]V)
	Insert(m, seq)
	return m
}
