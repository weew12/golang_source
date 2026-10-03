// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unique

import (
	"internal/abi"
	"internal/goarch"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
	"weak"
)

// canonMap 是一个 T -> *T 的 map。该 map 控制规范化 *T 的创建，
// 并且当规范化的 *T 不再被引用时，map 中的元素会被自动
// 删除。
type canonMap[T comparable] struct {
	root atomic.Pointer[indirect[T]]
	hash func(unsafe.Pointer, uintptr) uintptr
	seed uintptr
}

func newCanonMap[T comparable]() *canonMap[T] {
	cm := new(canonMap[T])
	cm.root.Store(newIndirectNode[T](nil))

	var m map[T]struct{}
	mapType := abi.TypeOf(m).MapType()
	cm.hash = mapType.Hasher
	cm.seed = uintptr(runtime_rand())
	return cm
}

func (m *canonMap[T]) Load(key T) *T {
	hash := m.hash(abi.NoEscape(unsafe.Pointer(&key)), m.seed)

	i := m.root.Load()
	hashShift := 8 * goarch.PtrSize
	for hashShift != 0 {
		hashShift -= nChildrenLog2

		n := i.children[(hash>>hashShift)&nChildrenMask].Load()
		if n == nil {
			return nil
		}
		if n.isEntry {
			v, _ := n.entry().lookup(key)
			return v
		}
		i = n.indirect()
	}
	panic("unique.canonMap: ran out of hash bits while iterating")
}

func (m *canonMap[T]) LoadOrStore(key T) *T {
	hash := m.hash(abi.NoEscape(unsafe.Pointer(&key)), m.seed)

	var i *indirect[T]
	var hashShift uint
	var slot *atomic.Pointer[node[T]]
	var n *node[T]
	for {
		// 查找该键或一个可插入的候选位置。
		i = m.root.Load()
		hashShift = 8 * goarch.PtrSize
		haveInsertPoint := false
		for hashShift != 0 {
			hashShift -= nChildrenLog2

			slot = &i.children[(hash>>hashShift)&nChildrenMask]
			n = slot.Load()
			if n == nil {
				// 我们发现了一个值为 nil 的槽位，它是插入的候选位置。
				haveInsertPoint = true
				break
			}
			if n.isEntry {
				// 我们找到了一个已存在的 entry，这是我们能走到的尽头。
				// 如果情况保持不变，我们将不得不把它替换为一个
				// indirect 节点。
				if v, _ := n.entry().lookup(key); v != nil {
					return v
				}
				haveInsertPoint = true
				break
			}
			i = n.indirect()
		}
		if !haveInsertPoint {
			panic("unique.canonMap: ran out of hash bits while iterating")
		}

		// 获取锁并复核我们之前看到的情况。
		i.mu.Lock()
		n = slot.Load()
		if (n == nil || n.isEntry) && !i.dead.Load() {
			// 我们之前看到的情况仍然成立，因此可以继续插入。
			break
		}
		// 我们必须重新开始。
		i.mu.Unlock()
	}
	// 注意：这个锁从我们上面跳出外层循环时就一直持有。
	// 我们特意这样跳出，以便能在此安全地使用 defer。
	// 另一种选择是把它拆分成一个新函数，但
	// 下面用到了太多局部迭代状态，所以这样反而
	// 更加清晰。
	defer i.mu.Unlock()

	var oldEntry *entry[T]
	if n != nil {
		oldEntry = n.entry()
		if v, _ := oldEntry.lookup(key); v != nil {
			// 简单情形：通过再次加载，发现我们想要的正好就在这里！
			return v
		}
	}
	newEntry, canon, wp := newEntryNode(key, hash)
	// 清理失效指针。这是为了避免当我们把完全相同的值
	// 存入集合、但清理由于某种原因被推迟而尚未运行时，
	// 发生 O(n) 的查找。
	oldEntry = oldEntry.prune()
	if oldEntry == nil {
		// 简单情形：创建一个新 entry 并存储它。
		slot.Store(&newEntry.node)
	} else {
		// 我们可能需要将已在那里的 entry 扩展为一个或多个新节点。
		//
		// 最后发布该节点，这将使 oldEntry 和 newEntry 都可见。我们
		// 不希望读者能够观察到 oldEntry 不在树中。
		slot.Store(m.expand(oldEntry, newEntry, hash, hashShift, i))
	}
	runtime.AddCleanup(canon, func(_ struct{}) {
		m.cleanup(hash, wp)
	}, struct{}{})
	return canon
}

// expand 接受哈希从第 64 位到 hashShift 位存在冲突的 oldEntry 和 newEntry，
// 并生成一棵由 indirect 节点组成的子树来容纳这两个新 entry。newHash 是新 entry 中
// 值的哈希。
func (m *canonMap[T]) expand(oldEntry, newEntry *entry[T], newHash uintptr, hashShift uint, parent *indirect[T]) *node[T] {
	// 检查哈希冲突。
	oldHash := oldEntry.hash
	if oldHash == newHash {
		// 将旧 entry 存入新 entry 的溢出列表，然后存储
		// 新 entry。
		newEntry.overflow.Store(oldEntry)
		return &newEntry.node
	}
	// 我们必须添加一个 indirect 节点。更糟的是，我们可能需要添加不止一个。
	newIndirect := newIndirectNode(parent)
	top := newIndirect
	for {
		if hashShift == 0 {
			panic("unique.canonMap: ran out of hash bits while inserting")
		}
		hashShift -= nChildrenLog2 // hashShift 对应 parent 所在的层级。我们需要再深入一层。
		oi := (oldHash >> hashShift) & nChildrenMask
		ni := (newHash >> hashShift) & nChildrenMask
		if oi != ni {
			newIndirect.children[oi].Store(&oldEntry.node)
			newIndirect.children[ni].Store(&newEntry.node)
			break
		}
		nextIndirect := newIndirectNode(newIndirect)
		newIndirect.children[oi].Store(&nextIndirect.node)
		newIndirect = nextIndirect
	}
	return &top.node
}

// cleanup 删除 canon map 中与 wp 对应的 entry（如果它
// 仍在 map 中）。当调用此函数时，wp 的 Value 方法必须
// 返回 nil。hash 必须是 wp 曾指向的值的哈希
// （即 *wp.Value() 的哈希）。
func (m *canonMap[T]) cleanup(hash uintptr, wp weak.Pointer[T]) {
	var i *indirect[T]
	var hashShift uint
	var slot *atomic.Pointer[node[T]]
	var n *node[T]
	for {
		// 沿着 hash 在 map 中查找 wp。
		i = m.root.Load()
		hashShift = 8 * goarch.PtrSize
		haveEntry := false
		for hashShift != 0 {
			hashShift -= nChildrenLog2

			slot = &i.children[(hash>>hashShift)&nChildrenMask]
			n = slot.Load()
			if n == nil {
				// 我们发现了一个值为 nil 的槽位，已被删除。
				return
			}
			if n.isEntry {
				if !n.entry().hasWeakPointer(wp) {
					// 该弱指针已被清理。
					return
				}
				haveEntry = true
				break
			}
			i = n.indirect()
		}
		if !haveEntry {
			panic("unique.canonMap: ran out of hash bits while iterating")
		}

		// 获取锁并复核我们之前看到的情况。
		i.mu.Lock()
		n = slot.Load()
		if n != nil && n.isEntry {
			// 不必过多顾虑地清理该 entry 节点。如果我们做了
			// 别人的工作，比如有人正尝试插入一个具有相同哈希的
			// entry（很可能是相同的值），那很好，
			// 他们会不获取锁就退出。
			newEntry := n.entry().prune()
			if newEntry == nil {
				slot.Store(nil)
			} else {
				slot.Store(&newEntry.node)
			}

			// 沿树向上删除空的内部节点。
			//
			// 在此过程中，我们将交替着锁沿着树向上走，
			// 因为我们需要删除每个空节点在其父节点中的链接，
			// 而这需要父节点的锁。
			for i.parent != nil && i.empty() {
				if hashShift == 8*goarch.PtrSize {
					panic("unique.canonMap: ran out of hash bits while iterating")
				}
				hashShift += nChildrenLog2

				// 在父节点中删除当前节点。
				parent := i.parent
				parent.mu.Lock()
				i.dead.Store(true) // 也可以在父节点的锁之外完成。
				parent.children[(hash>>hashShift)&nChildrenMask].Store(nil)
				i.mu.Unlock()
				i = parent
			}
			i.mu.Unlock()
			return
		}
		// 我们必须重新开始。
		i.mu.Unlock()
	}
}

// node 是节点的头部。它是多态的，
// 实际上要么是 entry，要么是 indirect。
type node[T comparable] struct {
	isEntry bool
}

func (n *node[T]) entry() *entry[T] {
	if !n.isEntry {
		panic("called entry on non-entry node")
	}
	return (*entry[T])(unsafe.Pointer(n))
}

func (n *node[T]) indirect() *indirect[T] {
	if n.isEntry {
		panic("called indirect on entry node")
	}
	return (*indirect[T])(unsafe.Pointer(n))
}

const (
	// 16 个子节点。这似乎是负载性能的最佳点：
	// 再小一些，我们会损失 50% 或更多的 CPU 性能。
	// 再大一些，收益微乎其微（32 个子节点约提升 1%）。
	nChildrenLog2 = 4
	nChildren     = 1 << nChildrenLog2
	nChildrenMask = nChildren - 1
)

// indirect 是 hash-trie 中的内部节点。
type indirect[T comparable] struct {
	node[T]
	dead     atomic.Bool
	parent   *indirect[T]
	mu       sync.Mutex // 保护对 children 以及任何为 entry 节点的子节点的修改。
	children [nChildren]atomic.Pointer[node[T]]
}

func newIndirectNode[T comparable](parent *indirect[T]) *indirect[T] {
	return &indirect[T]{node: node[T]{isEntry: false}, parent: parent}
}

func (i *indirect[T]) empty() bool {
	for j := range i.children {
		if i.children[j].Load() != nil {
			return false
		}
	}
	return true
}

// entry 是 hash-trie 中的叶子节点。
type entry[T comparable] struct {
	node[T]
	overflow atomic.Pointer[entry[T]] // 用于哈希冲突的溢出链。
	key      weak.Pointer[T]
	hash     uintptr
}

func newEntryNode[T comparable](key T, hash uintptr) (*entry[T], *T, weak.Pointer[T]) {
	k := new(T)
	*k = key
	wp := weak.Make(k)
	return &entry[T]{
		node: node[T]{isEntry: true},
		key:  wp,
		hash: hash,
	}, k, wp
}

// lookup 在溢出链中查找具有所提供键的 entry。
//
// 返回该键的规范指针以及该规范指针对应的弱指针。
func (e *entry[T]) lookup(key T) (*T, weak.Pointer[T]) {
	for e != nil {
		s := e.key.Value()
		if s != nil && *s == key {
			return s, e.key
		}
		e = e.overflow.Load()
	}
	return nil, weak.Pointer[T]{}
}

// hasWeakPointer 如果能在溢出链中找到所提供的弱指针则返回 true。
func (e *entry[T]) hasWeakPointer(wp weak.Pointer[T]) bool {
	for e != nil {
		if e.key == wp {
			return true
		}
		e = e.overflow.Load()
	}
	return false
}

// prune 移除溢出链中所有键为 nil 的 entry。
//
// 调用方必须持有 e 的父节点的锁。
func (e *entry[T]) prune() *entry[T] {
	// 清理链表的头部。
	for e != nil {
		if e.key.Value() != nil {
			break
		}
		e = e.overflow.Load()
	}
	if e == nil {
		return nil
	}

	// 清理链表中的各个节点。
	newHead := e
	i := &e.overflow
	e = i.Load()
	for e != nil {
		if e.key.Value() != nil {
			i = &e.overflow
		} else {
			i.Store(e.overflow.Load())
		}
		e = e.overflow.Load()
	}
	return newHead
}

// 引入 runtime.rand，这样我们就无需依赖
// math/rand/v2。
//
//go:linkname runtime_rand runtime.rand
func runtime_rand() uint64
