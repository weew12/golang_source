// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package heap 为任何实现了 heap.Interface 的类型提供堆操作。
// 堆是一棵树，其特点是每个节点都是其子树中的最小值节点。
//
// 树中的最小元素是根节点，位于索引 0 处。
//
// 堆是实现优先级队列的常用方式。要构建优先级队列，
// 需要用（负）优先级作为 Less 方法的排序来实现 Heap 接口，
// 这样 Push 添加元素而 Pop 从队列中移除最高优先级的元素。
// 示例中包含了这样的实现；example_pq_test.go 文件中有完整的源代码。
package heap

import "sort"

// Interface 类型描述了使用本包中例程的类型所需满足的要求。
// 任何实现该接口的类型都可以作为最小堆使用，并满足以下不变量
//（在调用 [Init] 后，或数据为空或已排序时建立）：
//
//	!h.Less(j, i) for 0 <= i < h.Len() and 2*i+1 <= j <= 2*i+2 and j < h.Len()
//
// 注意，本接口中的 [Push] 和 [Pop] 供堆包实现调用。
// 要向堆中添加和移除元素，请使用 [heap.Push] 和 [heap.Pop]。
type Interface interface {
	sort.Interface
	Push(x any) // 将 x 添加为元素 Len()
	Pop() any   // 移除并返回元素 Len() - 1。
}

// Init 建立本包中其他例程所需的堆不变量。
// Init 对于堆不变量是幂等的，
// 可在堆不变量可能已失效的任何时候调用。
// 时间复杂度为 O(n)，其中 n = h.Len()。
func Init(h Interface) {
	// 堆化
	n := h.Len()
	for i := n/2 - 1; i >= 0; i-- {
		down(h, i, n)
	}
}

// Push 将元素 x 推入堆中。
// 时间复杂度为 O(log n)，其中 n = h.Len()。
func Push(h Interface, x any) {
	h.Push(x)
	up(h, h.Len()-1)
}

// Pop 移除并返回堆中的最小元素（根据 Less 方法）。
// 时间复杂度为 O(log n)，其中 n = h.Len()。
// Pop 等价于 [Remove](h, 0)。
func Pop(h Interface) any {
	n := h.Len() - 1
	h.Swap(0, n)
	down(h, 0, n)
	return h.Pop()
}

// Remove 移除并返回堆中索引 i 处的元素。
// 时间复杂度为 O(log n)，其中 n = h.Len()。
func Remove(h Interface, i int) any {
	n := h.Len() - 1
	if n != i {
		h.Swap(i, n)
		if !down(h, i, n) {
			up(h, i)
		}
	}
	return h.Pop()
}

// Fix 在索引 i 处的元素值改变后，重新建立堆的排序。
// 改变索引 i 处元素的值然后调用 Fix，等价于
// 但比调用 [Remove](h, i) 后再 Push 新值更廉价。
// 时间复杂度为 O(log n)，其中 n = h.Len()。
func Fix(h Interface, i int) {
	if !down(h, i, h.Len()) {
		up(h, i)
	}
}

func up(h Interface, j int) {
	for {
		i := (j - 1) / 2 // 父节点
		if i == j || !h.Less(j, i) {
			break
		}
		h.Swap(i, j)
		j = i
	}
}

func down(h Interface, i0, n int) bool {
	i := i0
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 { // j1 < 0 after int overflow
			break
		}
		j := j1 // 左子节点
		if j2 := j1 + 1; j2 < n && h.Less(j2, j1) {
			j = j2 // = 2*i + 2  // 右子节点
		}
		if !h.Less(j, i) {
			break
		}
		h.Swap(i, j)
		i = j
	}
	return i > i0
}
