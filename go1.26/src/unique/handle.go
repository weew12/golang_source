// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unique

import (
	"internal/abi"
	isync "internal/sync"
	"unsafe"
)

var zero uintptr

// Handle 是类型 T 的某个值的全局唯一标识。
//
// 两个 Handle 比较相等，当且仅当用于创建这两个 Handle 的两个值
// 也会比较相等。两个 Handle 的比较是平凡的，
// 通常比比较用于创建它们的值高效得多。
type Handle[T comparable] struct {
	value *T
}

// Value 返回产生该 Handle 的 T 值的浅拷贝。
// Value 可安全地被多个 goroutine 并发使用。
func (h Handle[T]) Value() T {
	return *h.value
}

// Make 返回类型 T 的某个值的全局唯一 handle。两个 Handle
// 相等当且仅当用于产生它们的值相等。
// Make 可安全地被多个 goroutine 并发使用。
func Make[T comparable](value T) Handle[T] {
	// 查找类型 T 对应的 map。
	typ := abi.TypeFor[T]()
	if typ.Size() == 0 {
		return Handle[T]{(*T)(unsafe.Pointer(&zero))}
	}
	ma, ok := uniqueMaps.Load(typ)
	if !ok {
		m := &uniqueMap[T]{canonMap: newCanonMap[T](), cloneSeq: makeCloneSeq(typ)}
		ma, _ = uniqueMaps.LoadOrStore(typ, m)
	}
	m := ma.(*uniqueMap[T])

	// 在 map 中查找该值。
	ptr := m.Load(value)
	if ptr == nil {
		// 向 map 中插入一个新值。
		ptr = m.LoadOrStore(clone(value, &m.cloneSeq))
	}
	return Handle[T]{ptr}
}

// uniqueMaps 是供 unique.Make 使用的、按类型区分的并发 map 的索引。
//
// 这种两级 map 乍看可能有些奇怪，因为 HashTrieMap 本可以用 "any"
// 作为键类型，但问题在于逃逸分析。我们不想强制查找操作
// 使参数逃逸，而使用按类型区分的 map 让我们能够在可能的情况下
// 避免这一点（例如，对于字符串和纯数据结构体）。我们还获得了
// 不把所有不同类型都塞进单个 map 的好处，但这当然
// 不足以抵消两次 map 查找的开销。真正值得的是
// 节省那些分配。
var uniqueMaps isync.HashTrieMap[*abi.Type, any] // any 始终是 *uniqueMap[T]。

type uniqueMap[T comparable] struct {
	*canonMap[T]
	cloneSeq
}
