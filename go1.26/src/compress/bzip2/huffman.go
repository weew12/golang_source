// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bzip2

import (
	"cmp"
	"slices"
)

// huffmanTree 是一棵二叉树，逐位导航以到达某个符号。
type huffmanTree struct {
	// nodes 包含树中所有非叶节点。nodes[0] 是树的根，
	// 而 nextNode 包含构建树时要使用的下一个 nodes 元素的索引。
	nodes    []huffmanNode
	nextNode int
}

// huffmanNode 是树中的一个节点。left 和 right 包含树中 nodes 切片的
// 索引。如果 left 或 right 为 invalidNodeValue，则该子节点为左节点，
// 其值位于 leftValue/rightValue 中。
//
// 符号为 uint16 类型，因为 bzip2 不仅将 MTF 索引编码进树中，还编码了
// 用于游程编码的两个魔术值和一个 EOF 符号。因此可能的符号超过 256 个。
type huffmanNode struct {
	left, right           uint16
	leftValue, rightValue uint16
}

// invalidNodeValue 是一个无效索引，用于标记树中的叶节点。
const invalidNodeValue = 0xffff

// Decode 从给定的 bitReader 读取位并导航树，直到找到某个符号。
func (t *huffmanTree) Decode(br *bitReader) (v uint16) {
	nodeIndex := uint16(0) // 节点 0 是树的根。

	for {
		node := &t.nodes[nodeIndex]

		var bit uint16
		if br.bits > 0 {
			// 获取下一位 - 快速路径。
			br.bits--
			bit = uint16(br.n>>(br.bits&63)) & 1
		} else {
			// 获取下一位 - 慢速路径。
			// 使用 ReadBits 从底层的 io.ByteReader
			// 中获取单个位。
			bit = uint16(br.ReadBits(1))
		}

		// 通过使两次加载都无条件执行，诱使编译器
		// 生成条件传送而非分支。
		l, r := node.left, node.right

		if bit == 1 {
			nodeIndex = l
		} else {
			nodeIndex = r
		}

		if nodeIndex == invalidNodeValue {
			// 我们找到了一个叶节点。使用 bit 的值来决定
			// 是左值还是右值。
			l, r := node.leftValue, node.rightValue
			if bit == 1 {
				v = l
			} else {
				v = r
			}
			return
		}
	}
}

// newHuffmanTree 根据包含每个符号码长的切片构建一棵 Huffman 树。
// 最大码长为 32 位。
func newHuffmanTree(lengths []uint8) (huffmanTree, error) {
	// 存在许多可能的树为每个符号分配相同的码长（例如，考虑
	// 将一棵树沿中间镜像翻转）。由于码长分配决定了树的
	// 效率，这些树都同样好。为了最小化构建一棵树所需的
	// 信息量，bzip2 使用规范树（canonical tree），
	// 使得仅凭码长分配就能重建它。

	if len(lengths) < 2 {
		panic("newHuffmanTree: too few symbols")
	}

	var t huffmanTree

	// 首先我们按码长升序对码长分配进行排序，
	// 并使用符号值来打破平局。
	pairs := make([]huffmanSymbolLengthPair, len(lengths))
	for i, length := range lengths {
		pairs[i].value = uint16(i)
		pairs[i].length = length
	}

	slices.SortFunc(pairs, func(a, b huffmanSymbolLengthPair) int {
		if c := cmp.Compare(a.length, b.length); c != 0 {
			return c
		}
		return cmp.Compare(a.value, b.value)
	})

	// 现在我们从最长的码开始为符号分配码。
	// 我们将码打包进一个 uint32，靠最高有效位存放。
	// 因此分支是从 MSB 向下取的。这使得后续
	// 对它们排序变得容易。
	code := uint32(0)
	length := uint8(32)

	codes := make([]huffmanCode, len(lengths))
	for i := len(pairs) - 1; i >= 0; i-- {
		if length > pairs[i].length {
			length = pairs[i].length
		}
		codes[i].code = code
		codes[i].codeLen = length
		codes[i].value = pairs[i].value
		// 我们需要"递增"该码，这意味着将 |code|
		// 视作一个 |length| 位的数。
		code += 1 << (32 - length)
	}

	// 现在我们可以按码排序，从而将每个分支的左半部分
	// 递归地归组在一起。
	slices.SortFunc(codes, func(a, b huffmanCode) int {
		return cmp.Compare(a.code, b.code)
	})

	t.nodes = make([]huffmanNode, len(codes))
	_, err := buildHuffmanNode(&t, codes, 0)
	return t, err
}

// huffmanSymbolLengthPair 包含一个符号及其码长。
type huffmanSymbolLengthPair struct {
	value  uint16
	length uint8
}

// huffmanCode 包含一个符号、它的码以及码长。
type huffmanCode struct {
	code    uint32
	codeLen uint8
	value   uint16
}

// buildHuffmanNode 接收一个已排序的 huffmanCode 切片，并在
// 给定层级上构建 Huffman 树中的一个节点。它返回新建
// 节点的索引。
func buildHuffmanNode(t *huffmanTree, codes []huffmanCode, level uint32) (nodeIndex uint16, err error) {
	test := uint32(1) << (31 - level)

	// 我们必须搜索码列表，以找出左右两侧之间的分界。
	firstRightIndex := len(codes)
	for i, code := range codes {
		if code.code&test != 0 {
			firstRightIndex = i
			break
		}
	}

	left := codes[:firstRightIndex]
	right := codes[firstRightIndex:]

	if len(left) == 0 || len(right) == 0 {
		// Huffman 树中存在一个多余的层级，这表明编码器
		// 有 bug。然而，这个 bug 已在现实中观察到，
		// 因此我们处理它。

		// 如果本函数是被递归调用的，那么我们知道
		// len(codes) >= 2，因为否则我们就会命中下面的
		// "leaf node"（叶节点）情形，而不会递归。
		//
		// 然而，对于初始调用而言，len(codes) 可能
		// 为零或一。这两种情形都无效，因为长度为 0 的
		// 树无法编码任何内容，而长度为 1 的树只能
		// 编码 EOF，因而多余。我们拒绝这两种情形。
		if len(codes) < 2 {
			return 0, StructuralError("empty Huffman tree")
		}

		// 在这种情况下，递归并不总能缩短 codes 的
		// 长度，因此我们需要通过另一种机制来
		// 确保终止。
		if level == 31 {
			// 由于 len(codes) >= 2，这些值在所有 32 位上
			// 都匹配的唯一可能就是它们相等，而这是无效的。
			// 这确保我们永远不会进入无限递归。
			return 0, StructuralError("equal symbols in Huffman tree")
		}

		if len(left) == 0 {
			return buildHuffmanNode(t, right, level+1)
		}
		return buildHuffmanNode(t, left, level+1)
	}

	nodeIndex = uint16(t.nextNode)
	node := &t.nodes[t.nextNode]
	t.nextNode++

	if len(left) == 1 {
		// 叶节点
		node.left = invalidNodeValue
		node.leftValue = left[0].value
	} else {
		node.left, err = buildHuffmanNode(t, left, level+1)
	}

	if err != nil {
		return
	}

	if len(right) == 1 {
		// 叶节点
		node.right = invalidNodeValue
		node.rightValue = right[0].value
	} else {
		node.right, err = buildHuffmanNode(t, right, level+1)
	}

	return
}
