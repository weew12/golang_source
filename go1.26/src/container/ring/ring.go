// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ring 实现了循环链表的操作。
package ring

// Ring 是循环链表（或环）中的元素。
// 环没有开头或结尾；指向任何环元素的指针都可以作为整个环的引用。
// 空环表示为 nil Ring 指针。
// Ring 的零值是一个 Value 为 nil 的单元素环。
type Ring struct {
	next, prev *Ring
	Value      any // 供客户端使用；本库不会修改此字段
}

func (r *Ring) init() *Ring {
	r.next = r
	r.prev = r
	return r
}

// Next 返回环中的下一个元素。r 不能为空。
func (r *Ring) Next() *Ring {
	if r.next == nil {
		return r.init()
	}
	return r.next
}

// Prev 返回环中的前一个元素。r 不能为空。
func (r *Ring) Prev() *Ring {
	if r.next == nil {
		return r.init()
	}
	return r.prev
}

// Move 在环中向后（n < 0）或向前（n >= 0）移动 n % r.Len() 个元素，
// 并返回该环元素。r 不能为空。
func (r *Ring) Move(n int) *Ring {
	if r.next == nil {
		return r.init()
	}
	switch {
	case n < 0:
		for ; n < 0; n++ {
			r = r.prev
		}
	case n > 0:
		for ; n > 0; n-- {
			r = r.next
		}
	}
	return r
}

// New 创建一个包含 n 个元素的环。
func New(n int) *Ring {
	if n <= 0 {
		return nil
	}
	r := new(Ring)
	p := r
	for i := 1; i < n; i++ {
		p.next = &Ring{prev: p}
		p = p.next
	}
	p.next = r
	r.prev = p
	return r
}

// Link 将环 r 与环 s 连接，使 r.Next() 变为 s，并返回 r.Next() 的原始值。
// r 不能为空。
//
// 如果 r 和 s 指向同一个环，连接它们会从环中移除 r 和 s 之间的元素。
// 被移除的元素形成一个子环，结果是对该子环的引用
//（如果没有移除元素，结果仍然是 r.Next() 的原始值，而不是 nil）。
//
// 如果 r 和 s 指向不同的环，连接它们会创建一个单一的环，
// 其中 s 的元素被插入到 r 之后。结果指向插入后 s 的最后一个元素之后的元素。
func (r *Ring) Link(s *Ring) *Ring {
	n := r.Next()
	if s != nil {
		p := s.Prev()
		// 注意：不能使用多重赋值，因为 LHS 的求值顺序未指定。
		r.next = s
		s.prev = r
		n.prev = p
		p.next = n
	}
	return n
}

// Unlink 从环 r 中移除 n % r.Len() 个元素，从 r.Next() 开始。
// 如果 n % r.Len() == 0，r 保持不变。
// 结果是已被移除的子环。r 不能为空。
func (r *Ring) Unlink(n int) *Ring {
	if n <= 0 {
		return nil
	}
	return r.Link(r.Move(n + 1))
}

// Len 计算环 r 中元素的数量。
// 其执行时间与元素数量成正比。
func (r *Ring) Len() int {
	n := 0
	if r != nil {
		n = 1
		for p := r.Next(); p != r; p = p.next {
			n++
		}
	}
	return n
}

// Do 对环中的每个元素按正序调用函数 f。
// 如果 f 改变了 *r，则 Do 的行为是未定义的。
func (r *Ring) Do(f func(any)) {
	if r != nil {
		f(r.Value)
		for p := r.Next(); p != r; p = p.next {
			f(p.Value)
		}
	}
}
