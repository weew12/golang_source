// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flate

import (
	"math"
	"math/bits"
	"sort"
)

// hcode 是一个带有位编码和位长度的霍夫曼编码。
type hcode struct {
	code, len uint16
}

type huffmanEncoder struct {
	codes     []hcode
	freqcache []literalNode
	bitCount  [17]int32
	lns       byLiteral // 存储以避免在 generate 中重复分配
	lfs       byFreq    // 存储以避免在 generate 中重复分配
}

type literalNode struct {
	literal uint16
	freq    int32
}

// levelInfo 描述在给定深度处已构造树的状态。
type levelInfo struct {
	// 当前层级。为了打印更清晰
	level int32

	// 该层级上最后一个节点的频率
	lastFreq int32

	// 要添加到该层级的下一个字符的频率
	nextCharFreq int32

	// 要添加到该层级的下一个对（来自下一层级）的频率。
	// 仅当下一较低层级的 "needed" 值为 0 时有效。
	nextPairFreq int32

	// 该层级在上升到下一层级之前还需生成的链的数量
	needed int32
}

// set 设置 hcode 的编码和长度。
func (h *hcode) set(code uint16, length uint16) {
	h.len = length
	h.code = code
}

func maxNode() literalNode { return literalNode{math.MaxUint16, math.MaxInt32} }

func newHuffmanEncoder(size int) *huffmanEncoder {
	return &huffmanEncoder{codes: make([]hcode, size)}
}

// 生成与固定字面量表对应的 HuffmanCode。
func generateFixedLiteralEncoding() *huffmanEncoder {
	h := newHuffmanEncoder(maxNumLit)
	codes := h.codes
	var ch uint16
	for ch = 0; ch < maxNumLit; ch++ {
		var bits uint16
		var size uint16
		switch {
		case ch < 144:
			// size 8, 000110000  .. 10111111
			bits = ch + 48
			size = 8
		case ch < 256:
			// size 9, 110010000 .. 111111111
			bits = ch + 400 - 144
			size = 9
		case ch < 280:
			// size 7, 0000000 .. 0010111
			bits = ch - 256
			size = 7
		default:
			// size 8, 11000000 .. 11000111
			bits = ch + 192 - 280
			size = 8
		}
		codes[ch] = hcode{code: reverseBits(bits, byte(size)), len: size}
	}
	return h
}

func generateFixedOffsetEncoding() *huffmanEncoder {
	h := newHuffmanEncoder(30)
	codes := h.codes
	for ch := range codes {
		codes[ch] = hcode{code: reverseBits(uint16(ch), 5), len: 5}
	}
	return h
}

var fixedLiteralEncoding *huffmanEncoder = generateFixedLiteralEncoding()
var fixedOffsetEncoding *huffmanEncoder = generateFixedOffsetEncoding()

func (h *huffmanEncoder) bitLength(freq []int32) int {
	var total int
	for i, f := range freq {
		if f != 0 {
			total += int(f) * int(h.codes[i].len)
		}
	}
	return total
}

const maxBitsLimit = 16

// bitCounts 计算霍夫曼编码中分配给每个位大小的字面量数量。
// 它仅在 list.length >= 3 时被调用。
// 字面量为 0、1 和 2 的情况由特殊用例代码处理。
//
// list 是非零频率字面量及其关联频率的数组。该数组按频率
// 递增排序，其最后一个元素是一个频率为 MaxInt32
// 的特殊元素。
//
// maxBits 是用于编码任何字面量的最大位数。
// 它必须小于 16。
//
// bitCounts 返回一个整数切片，其中 slice[i] 表示应以 i 位编码的
// 字面量数量。
func (h *huffmanEncoder) bitCounts(list []literalNode, maxBits int32) []int32 {
	if maxBits >= maxBitsLimit {
		panic("flate: maxBits too large")
	}
	n := int32(len(list))
	list = list[0 : n+1]
	list[n] = maxNode()

	// 无论如何，树的深度都不会超过 n - 1。这
	// 在某些小规模情况下可以节省一点工作量
	if maxBits > n-1 {
		maxBits = n - 1
	}

	// 创建关于每个层级的信息。
	// 一个虚假的 "Level 0"，其唯一目的是让
	// level1.prev.needed==0。这使得 level1.nextPairFreq
	// 成为一个永远不会被选中的合法值。
	var levels [maxBitsLimit]levelInfo
	// leafCounts[i] 统计层级 i 最右节点的祖先左侧的
	// 字面量数量。
	// leafCounts[i][j] 是层级 j 祖先左侧的
	// 字面量数量。
	var leafCounts [maxBitsLimit][maxBitsLimit]int32

	for level := int32(1); level <= maxBits; level++ {
		// 对于每个层级，前两项是前两个字符。
		// 我们初始化各层级，就如同已经弄清楚了这一点。
		levels[level] = levelInfo{
			level:        level,
			lastFreq:     list[1].freq,
			nextCharFreq: list[2].freq,
			nextPairFreq: list[0].freq + list[1].freq,
		}
		leafCounts[level][level] = 2
		if level == 1 {
			levels[level].nextPairFreq = math.MaxInt32
		}
	}

	// 顶层总共需要 2*n - 2 项，现在已经生成了 2 项。
	levels[maxBits].needed = 2*n - 4

	level := maxBits
	for {
		l := &levels[level]
		if l.nextPairFreq == math.MaxInt32 && l.nextCharFreq == math.MaxInt32 {
			// 叶节点和对都已耗尽。
			// 结束该层级的所有计算。
			// 为了确保我们永远不会回到该层级或任何更低层级，
			// 将 nextPairFreq 设置为不可能达到的大值。
			l.needed = 0
			levels[level+1].nextPairFreq = math.MaxInt32
			level++
			continue
		}

		prevFreq := l.lastFreq
		if l.nextCharFreq < l.nextPairFreq {
			// 该行上的下一项是一个叶节点。
			n := leafCounts[level][level] + 1
			l.lastFreq = l.nextCharFreq
			// 较低的 leafCounts 与前一个节点相同。
			leafCounts[level][level] = n
			l.nextCharFreq = list[n].freq
		} else {
			// 该行上的下一项是来自上一行的一个对。
			// 在我们于下方层级生成两个
			// 更多的值之前，nextPairFreq 是无效的
			l.lastFreq = l.nextPairFreq
			// 从较低层级获取叶计数，但 counts[level] 保持不变。
			copy(leafCounts[level][:level], leafCounts[level-1][:level])
			levels[l.level-1].needed = 2
		}

		if l.needed--; l.needed == 0 {
			// 我们已经完成了该层级需要做的所有事情。
			// 继续向上计算一个层级。用我们刚刚在该层级上计算出的
			// 两个节点之和填充上一层的 nextPairFreq。
			if l.level == maxBits {
				// 全部完成！
				break
			}
			levels[l.level+1].nextPairFreq = prevFreq + l.lastFreq
			level++
		} else {
			// 如果我们从下方借用，则临时下移以补充它。
			for levels[level-1].needed > 0 {
				level--
			}
		}
	}

	// 如果到最后顶层为空或没有使用所有叶节点，
	// 则说明出了问题。
	if leafCounts[maxBits][maxBits] != n {
		panic("leafCounts[maxBits][maxBits] != n")
	}

	bitCount := h.bitCount[:maxBits+1]
	bits := 1
	counts := &leafCounts[maxBits]
	for level := maxBits; level > 0; level-- {
		// chain.leafCount 给出至少需要 "bits"
		// 位来编码的字面量数量。
		bitCount[bits] = counts[level] - counts[level-1]
		bits++
	}
	return bitCount
}

// 查看叶节点，并按照 RFC 1951 3.2.2 中的规定
// 为它们分配位计数和编码
func (h *huffmanEncoder) assignEncodingAndSize(bitCount []int32, list []literalNode) {
	code := uint16(0)
	for n, bits := range bitCount {
		code <<= 1
		if n == 0 || bits == 0 {
			continue
		}
		// 字面量 list[len(list)-bits] .. list[len(list)-bits]
		// 使用 "bits" 位进行编码，并获得值
		// code、code + 1、……。这些编码值
		// 按字面量顺序（而非频率顺序）分配。
		chunk := list[len(list)-int(bits):]

		h.lns.sort(chunk)
		for _, node := range chunk {
			h.codes[node.literal] = hcode{code: reverseBits(code, uint8(n)), len: uint16(n)}
			code++
		}
		list = list[0 : len(list)-int(bits)]
	}
}

// 更新此霍夫曼编码对象，使其成为指定频率计数下的最小编码。
//
// freq 是频率数组，其中 freq[i] 给出字面量 i 的频率。
// maxBits  用于任何字面量的最大位数。
func (h *huffmanEncoder) generate(freq []int32, maxBits int32) {
	if h.freqcache == nil {
		// 分配一个可复用缓冲区，使用可能最长的频率表。
		// 可能的长度为 codegenCodeCount、offsetCodeCount 和 maxNumLit。
		// 其中最大的是 maxNumLit，因此我们按该情况分配。
		h.freqcache = make([]literalNode, maxNumLit+1)
	}
	list := h.freqcache[:len(freq)+1]
	// 非零字面量的数量
	count := 0
	// 将 list 设置为所有非零字面量及其频率的集合
	for i, f := range freq {
		if f != 0 {
			list[count] = literalNode{uint16(i), f}
			count++
		} else {
			h.codes[i].len = 0
		}
	}

	list = list[:count]
	if count <= 2 {
		// 在此处理小规模情况，因为它们对通用情况代码来说很麻烦。对于
		// 两个或更少的字面量，所有内容的位长度均为 1。
		for i, node := range list {
			// "list" 按字面量值递增排序。
			h.codes[node.literal].set(uint16(i), 1)
		}
		return
	}
	h.lfs.sort(list)

	// 获取每个位计数的字面量数量
	bitCount := h.bitCounts(list, maxBits)
	// 然后进行分配
	h.assignEncodingAndSize(bitCount, list)
}

type byLiteral []literalNode

func (s *byLiteral) sort(a []literalNode) {
	*s = byLiteral(a)
	sort.Sort(s)
}

func (s byLiteral) Len() int { return len(s) }

func (s byLiteral) Less(i, j int) bool {
	return s[i].literal < s[j].literal
}

func (s byLiteral) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

type byFreq []literalNode

func (s *byFreq) sort(a []literalNode) {
	*s = byFreq(a)
	sort.Sort(s)
}

func (s byFreq) Len() int { return len(s) }

func (s byFreq) Less(i, j int) bool {
	if s[i].freq == s[j].freq {
		return s[i].literal < s[j].literal
	}
	return s[i].freq < s[j].freq
}

func (s byFreq) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func reverseBits(number uint16, bitLength byte) uint16 {
	return bits.Reverse16(number << (16 - bitLength))
}
