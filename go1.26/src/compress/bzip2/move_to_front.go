// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bzip2

// moveToFrontDecoder 实现了一个移动到前端（move-to-front）列表。
// 这种列表是一种高效的方式，可将含有重复元素的字符串转换为
// 含有许多小数值的字符串，从而适合熵编码。它的工作方式是：
// 从一份初始符号列表开始，并通过符号在该列表中的索引来引用
// 符号。当一个符号被引用时，它会被移动到列表的前端。因此，
// 重复的符号最终会以许多零来编码，因为该符号在第一次访问后
// 就会位于列表的前端。
type moveToFrontDecoder []byte

// newMTFDecoder 创建一个带有显式初始符号列表的
// move-to-front 解码器。
func newMTFDecoder(symbols []byte) moveToFrontDecoder {
	if len(symbols) > 256 {
		panic("too many symbols")
	}
	return moveToFrontDecoder(symbols)
}

// newMTFDecoderWithRange 创建一个 move-to-front 解码器，
// 其初始符号列表为 0...n-1。
func newMTFDecoderWithRange(n int) moveToFrontDecoder {
	if n > 256 {
		panic("newMTFDecoderWithRange: cannot have > 256 symbols")
	}

	m := make([]byte, n)
	for i := 0; i < n; i++ {
		m[i] = byte(i)
	}
	return moveToFrontDecoder(m)
}

func (m moveToFrontDecoder) Decode(n int) (b byte) {
	// 用一个简单的复制来实现 move-to-front。在基准测试中，
	// 这种方法胜过更复杂的方法，可能是因为它在单个缓存行
	// 内具有很高的引用局部性（大多数 move-to-front 操作的
	// n < 64）。
	b = m[n]
	copy(m[1:], m[:n])
	m[0] = b
	return
}

// First 返回列表前端的符号。
func (m moveToFrontDecoder) First() byte {
	return m[0]
}
