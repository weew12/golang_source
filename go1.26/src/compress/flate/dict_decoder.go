// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flate

// dictDecoder 实现了解压缩中使用的 LZ77 滑动字典。
// LZ77 通过两种形式的命令序列来解压缩数据：
//
//   - 字面量插入：将一个或多个符号的连续序列按原样插入数据
//     流中。这通过针对单个符号的 writeByte 方法，或针对多个符号的
//     writeSlice/writeMark 组合来实现。
//     如果未使用预设字典，任何有效流都必须以字面量插入开头。
//
//   - 向后复制：将一个或多个符号的连续序列从先前
//     已输出的数据中复制。向后复制以元组 (dist, length) 的形式出现，其中 dist
//     决定从流中往回复制多远，length 决定要复制
//     多少字节。注意，length 大于距离也是有效的。
//     由于 LZ77 使用向前复制，该情形被用来
//     对重复的符号序列执行一种形式的游程编码。
//     writeCopy 和 tryWriteCopy 用于实现此命令。
//
// 出于性能原因，此实现几乎不对参数进行合理性
// 检查。因此，必须遵守为每个方法调用记录的不变量。
type dictDecoder struct {
	hist []byte // 滑动窗口历史

	// 不变量：0 <= rdPos <= wrPos <= len(hist)
	wrPos int  // 缓冲区中当前的输出位置
	rdPos int  // 已输出 hist[:rdPos]
	full  bool // 是否已写入完整的窗口长度？
}

// init 将 dictDecoder 初始化为具有给定大小的滑动窗口
// 字典。如果提供了预设 dict，它会用
// dict 的内容初始化该字典。
func (dd *dictDecoder) init(size int, dict []byte) {
	*dd = dictDecoder{hist: dd.hist}

	if cap(dd.hist) < size {
		dd.hist = make([]byte, size)
	}
	dd.hist = dd.hist[:size]

	if len(dict) > len(dd.hist) {
		dict = dict[len(dict)-len(dd.hist):]
	}
	dd.wrPos = copy(dd.hist, dict)
	if dd.wrPos == len(dd.hist) {
		dd.wrPos = 0
		dd.full = true
	}
	dd.rdPos = dd.wrPos
}

// histSize 报告字典中历史数据的总量。
func (dd *dictDecoder) histSize() int {
	if dd.full {
		return len(dd.hist)
	}
	return dd.wrPos
}

// availRead 报告 readFlush 可以刷出的字节数。
func (dd *dictDecoder) availRead() int {
	return dd.wrPos - dd.rdPos
}

// availWrite 报告可用的输出缓冲区空间量。
func (dd *dictDecoder) availWrite() int {
	return len(dd.hist) - dd.wrPos
}

// writeSlice 返回可写入数据的可用缓冲区切片。
//
// 将保持此不变量：len(s) <= availWrite()
func (dd *dictDecoder) writeSlice() []byte {
	return dd.hist[dd.wrPos:]
}

// writeMark 将写指针前进 cnt。
//
// 必须保持此不变量：0 <= cnt <= availWrite()
func (dd *dictDecoder) writeMark(cnt int) {
	dd.wrPos += cnt
}

// writeByte 向字典写入单个字节。
//
// 必须保持此不变量：0 < availWrite()
func (dd *dictDecoder) writeByte(c byte) {
	dd.hist[dd.wrPos] = c
	dd.wrPos++
}

// writeCopy 将给定 (dist, length) 处的字符串复制到输出。
// 它返回复制的字节数，如果输出缓冲区中的可用空间太小，
// 返回值可能小于请求的 length。
//
// 必须保持此不变量：0 < dist <= histSize()
func (dd *dictDecoder) writeCopy(dist, length int) int {
	dstBase := dd.wrPos
	dstPos := dstBase
	srcPos := dstPos - dist
	endPos := dstPos + length
	if endPos > len(dd.hist) {
		endPos = len(dd.hist)
	}

	// 复制目标位置之后的不重叠区段。
	//
	// 该区段是不重叠的，因为此区段的复制长度
	// 始终小于或等于向后距离。当某个距离指向
	// 缓冲区中环绕的数据时，就可能发生这种情况。
	// 因此，这里执行的是向后复制；也就是说，复制前
	// 源中的确切字节被放入目标。
	if srcPos < 0 {
		srcPos += len(dd.hist)
		dstPos += copy(dd.hist[dstPos:endPos], dd.hist[srcPos:])
		srcPos = 0
	}

	// 复制目标位置之前可能重叠的区段。
	//
	// 如果此区段的复制长度大于向后距离，则该区段
	// 可能重叠。LZ77 允许这样做，以便可以用 (dist, length) 对
	// 简洁地表示重复的字符串。
	// 因此，这里执行的是向前复制；也就是说，随着复制
	// 推进，复制的字节可能依赖于目标中生成的字节。
	// 这在功能上等价于以下代码：
	//
	//	for i := 0; i < endPos-dstPos; i++ {
	//		dd.hist[dstPos+i] = dd.hist[srcPos+i]
	//	}
	//	dstPos = endPos
	//
	for dstPos < endPos {
		dstPos += copy(dd.hist[dstPos:endPos], dd.hist[srcPos:dstPos])
	}

	dd.wrPos = dstPos
	return dstPos - dstBase
}

// tryWriteCopy 尝试将给定 (distance, length) 处的字符串复制到
// 输出。这个专用版本针对短距离进行了优化。
//
// 出于性能原因，此方法被设计为可内联。
//
// 必须保持此不变量：0 < dist <= histSize()
func (dd *dictDecoder) tryWriteCopy(dist, length int) int {
	dstPos := dd.wrPos
	endPos := dstPos + length
	if dstPos < dist || endPos > len(dd.hist) {
		return 0
	}
	dstBase := dstPos
	srcPos := dstPos - dist

	// 复制目标位置之前可能重叠的区段。
	for dstPos < endPos {
		dstPos += copy(dd.hist[dstPos:endPos], dd.hist[srcPos:dstPos])
	}

	dd.wrPos = dstPos
	return dstPos - dstBase
}

// readFlush 返回历史缓冲区中已准备好输出给用户的
// 切片。在调用任何其他 dictDecoder 方法之前，必须完全
// 消费 readFlush 返回的数据。
func (dd *dictDecoder) readFlush() []byte {
	toRead := dd.hist[dd.rdPos:dd.wrPos]
	dd.rdPos = dd.wrPos
	if dd.wrPos == len(dd.hist) {
		dd.wrPos, dd.rdPos = 0, 0
		dd.full = true
	}
	return toRead
}
