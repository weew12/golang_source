// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package list 实现了双向链表。
//
// 遍历链表的代码（其中 l 是 *List）：
//
//	for e := l.Front(); e != nil; e = e.Next() {
//		// 对 e.Value 做某些操作
//	}
package list

// Element 是链表中的元素。
type Element struct {
	// 双向链表中的前向和后向指针。
	// 为简化实现，链表 l 在内部被实现为一个环，
	// 使得 &l.root 既是最后一个链表元素 (l.Back()) 的后向元素，
	// 也是第一个链表元素 (l.Front()) 的前向元素。
	next, prev *Element

	// 该元素所属的链表。
	list *List

	// 存储在该元素中的值。
	Value any
}

// Next 返回下一个链表元素；如果不存在则返回 nil。
func (e *Element) Next() *Element {
	if p := e.next; e.list != nil && p != &e.list.root {
		return p
	}
	return nil
}

// Prev 返回前一个链表元素；如果不存在则返回 nil。
func (e *Element) Prev() *Element {
	if p := e.prev; e.list != nil && p != &e.list.root {
		return p
	}
	return nil
}

// List 表示一个双向链表。
// List 的零值是一个空的、即可使用的链表。
type List struct {
	root Element // 哨兵链表元素，仅使用 &root、root.prev 和 root.next
	len  int     // 当前链表长度，不包含（此）哨兵元素
}

// Init 初始化或清空链表 l。
func (l *List) Init() *List {
	l.root.next = &l.root
	l.root.prev = &l.root
	l.len = 0
	return l
}

// New 返回一个已初始化的链表。
func New() *List { return new(List).Init() }

// Len 返回链表 l 中元素的数量。
// 时间复杂度为 O(1)。
func (l *List) Len() int { return l.len }

// Front 返回链表 l 的第一个元素；如果链表为空则返回 nil。
func (l *List) Front() *Element {
	if l.len == 0 {
		return nil
	}
	return l.root.next
}

// Back 返回链表 l 的最后一个元素；如果链表为空则返回 nil。
func (l *List) Back() *Element {
	if l.len == 0 {
		return nil
	}
	return l.root.prev
}

// lazyInit 延迟初始化零值的 List。
func (l *List) lazyInit() {
	if l.root.next == nil {
		l.Init()
	}
}

// insert 在 at 之后插入 e，增加 l.len，并返回 e。
func (l *List) insert(e, at *Element) *Element {
	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
	e.list = l
	l.len++
	return e
}

// insertValue 是 insert(&Element{Value: v}, at) 的便捷封装。
func (l *List) insertValue(v any, at *Element) *Element {
	return l.insert(&Element{Value: v}, at)
}

// remove 将 e 从其链表中移除，减少 l.len
func (l *List) remove(e *Element) {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = nil // 避免内存泄漏
	e.prev = nil // 避免内存泄漏
	e.list = nil
	l.len--
}

// move 将 e 移动到 at 之后。
func (l *List) move(e, at *Element) {
	if e == at {
		return
	}
	e.prev.next = e.next
	e.next.prev = e.prev

	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
}

// Remove 从链表 l 中移除元素 e（如果 e 是链表 l 的元素）。
// 返回元素的值 e.Value。
// e 不能为 nil。
func (l *List) Remove(e *Element) any {
	if e.list == l {
		// 如果 e.list == l，那么 l 在 e 被插入时一定是已初始化的，
		// 或者 l == nil（e 是零值 Element），此时 l.remove 会崩溃
		l.remove(e)
	}
	return e.Value
}

// PushFront 在链表 l 的前端插入一个值为 v 的新元素 e，并返回 e。
func (l *List) PushFront(v any) *Element {
	l.lazyInit()
	return l.insertValue(v, &l.root)
}

// PushBack 在链表 l 的末尾插入一个值为 v 的新元素 e，并返回 e。
func (l *List) PushBack(v any) *Element {
	l.lazyInit()
	return l.insertValue(v, l.root.prev)
}

// InsertBefore 在 mark 之前立即插入一个值为 v 的新元素 e，并返回 e。
// 如果 mark 不是 l 的元素，链表不会被修改。
// mark 不能为 nil。
func (l *List) InsertBefore(v any, mark *Element) *Element {
	if mark.list != l {
		return nil
	}
	// 参见 List.Remove 中关于 l 初始化的注释
	return l.insertValue(v, mark.prev)
}

// InsertAfter 在 mark 之后立即插入一个值为 v 的新元素 e，并返回 e。
// 如果 mark 不是 l 的元素，链表不会被修改。
// mark 不能为 nil。
func (l *List) InsertAfter(v any, mark *Element) *Element {
	if mark.list != l {
		return nil
	}
	// 参见 List.Remove 中关于 l 初始化的注释
	return l.insertValue(v, mark)
}

// MoveToFront 将元素 e 移动到链表 l 的前端。
// 如果 e 不是 l 的元素，链表不会被修改。
// e 不能为 nil。
func (l *List) MoveToFront(e *Element) {
	if e.list != l || l.root.next == e {
		return
	}
	// 参见 List.Remove 中关于 l 初始化的注释
	l.move(e, &l.root)
}

// MoveToBack 将元素 e 移动到链表 l 的末尾。
// 如果 e 不是 l 的元素，链表不会被修改。
// e 不能为 nil。
func (l *List) MoveToBack(e *Element) {
	if e.list != l || l.root.prev == e {
		return
	}
	// 参见 List.Remove 中关于 l 初始化的注释
	l.move(e, l.root.prev)
}

// MoveBefore 将元素 e 移动到 mark 之前的新位置。
// 如果 e 或 mark 不是 l 的元素，或者 e == mark，链表不会被修改。
// e 和 mark 不能为 nil。
func (l *List) MoveBefore(e, mark *Element) {
	if e.list != l || e == mark || mark.list != l {
		return
	}
	l.move(e, mark.prev)
}

// MoveAfter 将元素 e 移动到 mark 之后的新位置。
// 如果 e 或 mark 不是 l 的元素，或者 e == mark，链表不会被修改。
// e 和 mark 不能为 nil。
func (l *List) MoveAfter(e, mark *Element) {
	if e.list != l || e == mark || mark.list != l {
		return
	}
	l.move(e, mark)
}

// PushBackList 将另一个链表的副本插入到链表 l 的末尾。
// l 和 other 可以是同一个链表。other 不能为 nil。
func (l *List) PushBackList(other *List) {
	l.lazyInit()
	for i, e := other.Len(), other.Front(); i > 0; i, e = i-1, e.Next() {
		l.insertValue(e.Value, l.root.prev)
	}
}

// PushFrontList 将另一个链表的副本插入到链表 l 的前端。
// l 和 other 可以是同一个链表。other 不能为 nil。
func (l *List) PushFrontList(other *List) {
	l.lazyInit()
	for i, e := other.Len(), other.Back(); i > 0; i, e = i-1, e.Prev() {
		l.insertValue(e.Value, &l.root)
	}
}
