// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package maps 定义了对任何类型的 map 都有用的各种函数。
//
// 此包对非自反键（keys k where k != k），如浮点 NaN，没有特殊处理。
package maps

import (
	_ "unsafe"
)

// Equal 报告两个 map 是否包含相同的键/值对。
// 值使用 == 进行比较。
func Equal[M1, M2 ~map[K]V, K, V comparable](m1 M1, m2 M2) bool {
	if len(m1) != len(m2) {
		return false
	}
	for k, v1 := range m1 {
		if v2, ok := m2[k]; !ok || v1 != v2 {
			return false
		}
	}
	return true
}

// EqualFunc 类似于 Equal，但使用 eq 比较值。
// 键仍然使用 == 比较。
func EqualFunc[M1 ~map[K]V1, M2 ~map[K]V2, K comparable, V1, V2 any](m1 M1, m2 M2, eq func(V1, V2) bool) bool {
	if len(m1) != len(m2) {
		return false
	}
	for k, v1 := range m1 {
		if v2, ok := m2[k]; !ok || !eq(v1, v2) {
			return false
		}
	}
	return true
}

// clone is implemented in the runtime package.
//
//go:linkname clone maps.clone
func clone(m any) any

// Clone 返回 m 的副本。这是一个浅拷贝：
// 新的键和值使用普通赋值进行设置。
func Clone[M ~map[K]V, K comparable, V any](m M) M {
	// 保留 nil 以防重要。
	if m == nil {
		return nil
	}
	return clone(m).(M)
}

// Copy 将 src 中的所有键/值对复制到 dst。
// 当 src 中的键已存在于 dst 时，
// dst 中的值将被 src 中与该键关联的值覆盖。
func Copy[M1 ~map[K]V, M2 ~map[K]V, K comparable, V any](dst M1, src M2) {
	for k, v := range src {
		dst[k] = v
	}
}

// DeleteFunc 从 m 中删除所有使 del 返回 true 的键/值对。
func DeleteFunc[M ~map[K]V, K comparable, V any](m M, del func(K, V) bool) {
	for k, v := range m {
		if del(k, v) {
			delete(m, k)
		}
	}
}
