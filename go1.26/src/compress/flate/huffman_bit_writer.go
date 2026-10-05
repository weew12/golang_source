// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flate

import (
	"io"
)

const (
	// 最大的偏移码。
	offsetCodeCount = 30

	// 用于标记块结束的特殊码。
	endBlockMarker = 256

	// 第一个长度码。
	lengthCodesStart = 257

	// codegen 码的数量。
	codegenCodeCount = 19
	badCode          = 255

	// bufferFlushSize 表示缓冲区大小，
	// 超过该大小后字节会被刷新到 writer。
	// 最好取 6 的倍数，因为我们在每次
	// 写入缓冲区之间累积 6 个字节。
	bufferFlushSize = 240

	// bufferSize 是实际输出字节缓冲区的大小。
	// 它必须有额外的余量用于一次刷新，
	// 刷新最多可包含 8 个字节。
	bufferSize = bufferFlushSize + 8
)

// 长度码 X - LENGTH_CODES_START 所需的额外比特数。
var lengthExtraBits = []int8{
	/* 257 */ 0, 0, 0,
	/* 260 */ 0, 0, 0, 0, 0, 1, 1, 1, 1, 2,
	/* 270 */ 2, 2, 2, 3, 3, 3, 3, 4, 4, 4,
	/* 280 */ 4, 5, 5, 5, 5, 0,
}

// 长度码 X - LENGTH_CODES_START 所指示的长度。
var lengthBase = []uint32{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
	12, 14, 16, 20, 24, 28, 32, 40, 48, 56,
	64, 80, 96, 112, 128, 160, 192, 224, 255,
}

// 偏移码字的额外比特。
var offsetExtraBits = []int8{
	0, 0, 0, 0, 1, 1, 2, 2, 3, 3,
	4, 4, 5, 5, 6, 6, 7, 7, 8, 8,
	9, 9, 10, 10, 11, 11, 12, 12, 13, 13,
}

var offsetBase = []uint32{
	0x000000, 0x000001, 0x000002, 0x000003, 0x000004,
	0x000006, 0x000008, 0x00000c, 0x000010, 0x000018,
	0x000020, 0x000030, 0x000040, 0x000060, 0x000080,
	0x0000c0, 0x000100, 0x000180, 0x000200, 0x000300,
	0x000400, 0x000600, 0x000800, 0x000c00, 0x001000,
	0x001800, 0x002000, 0x003000, 0x004000, 0x006000,
}

// codegen 码大小被写入的奇特顺序。
var codegenOrder = []uint32{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

type huffmanBitWriter struct {
	// writer 是底层 writer。
	// 不要直接使用它；请使用 write 方法，该方法确保
	// Write 错误具有粘性。
	writer io.Writer

	// 等待写入的数据是 bytes[0:nbytes]，
	// 然后是 bits 的低 nbits 位。数据总是
	// 按顺序写入 bytes 数组。
	bits            uint64
	nbits           uint
	bytes           [bufferSize]byte
	codegenFreq     [codegenCodeCount]int32
	nbytes          int
	literalFreq     []int32
	offsetFreq      []int32
	codegen         []uint8
	literalEncoding *huffmanEncoder
	offsetEncoding  *huffmanEncoder
	codegenEncoding *huffmanEncoder
	err             error
}

func newHuffmanBitWriter(w io.Writer) *huffmanBitWriter {
	return &huffmanBitWriter{
		writer:          w,
		literalFreq:     make([]int32, maxNumLit),
		offsetFreq:      make([]int32, offsetCodeCount),
		codegen:         make([]uint8, maxNumLit+offsetCodeCount+1),
		literalEncoding: newHuffmanEncoder(maxNumLit),
		codegenEncoding: newHuffmanEncoder(codegenCodeCount),
		offsetEncoding:  newHuffmanEncoder(offsetCodeCount),
	}
}

func (w *huffmanBitWriter) reset(writer io.Writer) {
	w.writer = writer
	w.bits, w.nbits, w.nbytes, w.err = 0, 0, 0, nil
}

func (w *huffmanBitWriter) flush() {
	if w.err != nil {
		w.nbits = 0
		return
	}
	n := w.nbytes
	for w.nbits != 0 {
		w.bytes[n] = byte(w.bits)
		w.bits >>= 8
		if w.nbits > 8 { // 避免下溢
			w.nbits -= 8
		} else {
			w.nbits = 0
		}
		n++
	}
	w.bits = 0
	w.write(w.bytes[:n])
	w.nbytes = 0
}

func (w *huffmanBitWriter) write(b []byte) {
	if w.err != nil {
		return
	}
	_, w.err = w.writer.Write(b)
}

func (w *huffmanBitWriter) writeBits(b int32, nb uint) {
	if w.err != nil {
		return
	}
	w.bits |= uint64(b) << w.nbits
	w.nbits += nb
	if w.nbits >= 48 {
		bits := w.bits
		w.bits >>= 48
		w.nbits -= 48
		n := w.nbytes
		bytes := w.bytes[n : n+6]
		bytes[0] = byte(bits)
		bytes[1] = byte(bits >> 8)
		bytes[2] = byte(bits >> 16)
		bytes[3] = byte(bits >> 24)
		bytes[4] = byte(bits >> 32)
		bytes[5] = byte(bits >> 40)
		n += 6
		if n >= bufferFlushSize {
			w.write(w.bytes[:n])
			n = 0
		}
		w.nbytes = n
	}
}

func (w *huffmanBitWriter) writeBytes(bytes []byte) {
	if w.err != nil {
		return
	}
	n := w.nbytes
	if w.nbits&7 != 0 {
		w.err = InternalError("writeBytes with unfinished bits")
		return
	}
	for w.nbits != 0 {
		w.bytes[n] = byte(w.bits)
		w.bits >>= 8
		w.nbits -= 8
		n++
	}
	if n != 0 {
		w.write(w.bytes[:n])
	}
	w.nbytes = 0
	w.write(bytes)
}

// RFC 1951 3.2.7 规定了一种特殊的游程编码，用于指定
// 字面量和偏移长度数组（它们被连接成一个单一
// 数组）。该方法生成该游程编码。
//
// 结果被写入 codegen 数组，每个码的频率
// 被写入 codegenFreq 数组。
// 码 0-15 是单字节码。码 16-18 后面跟着额外
// 信息。码 badCode 是结束标记
//
//	numLiterals      literalEncoding 中字面量的数量
//	numOffsets       offsetEncoding 中偏移的数量
//	litenc, offenc   要使用的字面量和偏移编码器
func (w *huffmanBitWriter) generateCodegen(numLiterals int, numOffsets int, litEnc, offEnc *huffmanEncoder) {
	clear(w.codegenFreq[:])
	// 注意，我们既把 codegen 用作保存频率副本的
	// 临时变量，又把它用作存放结果的地方。
	// 这没问题，因为输出总是比到目前为止使用的
	// 输入更短。
	codegen := w.codegen // 缓存
	// 将连接后的码大小复制到 codegen。在末尾放一个标记。
	cgnl := codegen[:numLiterals]
	for i := range cgnl {
		cgnl[i] = uint8(litEnc.codes[i].len)
	}

	cgnl = codegen[numLiterals : numLiterals+numOffsets]
	for i := range cgnl {
		cgnl[i] = uint8(offEnc.codes[i].len)
	}
	codegen[numLiterals+numOffsets] = badCode

	size := codegen[0]
	count := 1
	outIndex := 0
	for inIndex := 1; size != badCode; inIndex++ {
		// 不变式：我们已经看到 "count" 个 size 的副本，
		// 但尚未为它们生成输出。
		nextSize := codegen[inIndex]
		if nextSize == size {
			count++
			continue
		}
		// 我们需要生成表示 "count" 个 size 的 codegen。
		if size != 0 {
			codegen[outIndex] = size
			outIndex++
			w.codegenFreq[size]++
			count--
			for count >= 3 {
				n := 6
				if n > count {
					n = count
				}
				codegen[outIndex] = 16
				outIndex++
				codegen[outIndex] = uint8(n - 3)
				outIndex++
				w.codegenFreq[16]++
				count -= n
			}
		} else {
			for count >= 11 {
				n := 138
				if n > count {
					n = count
				}
				codegen[outIndex] = 18
				outIndex++
				codegen[outIndex] = uint8(n - 11)
				outIndex++
				w.codegenFreq[18]++
				count -= n
			}
			if count >= 3 {
				// count >= 3 && count <= 10
				codegen[outIndex] = 17
				outIndex++
				codegen[outIndex] = uint8(count - 3)
				outIndex++
				w.codegenFreq[17]++
				count = 0
			}
		}
		count--
		for ; count >= 0; count-- {
			codegen[outIndex] = size
			outIndex++
			w.codegenFreq[size]++
		}
		// 为下一次循环设置不变式。
		size = nextSize
		count = 1
	}
	// 表示 codegen 结束的标记。
	codegen[outIndex] = badCode
}

// dynamicSize 返回动态编码数据的大小（以比特为单位）。
func (w *huffmanBitWriter) dynamicSize(litEnc, offEnc *huffmanEncoder, extraBits int) (size, numCodegens int) {
	numCodegens = len(w.codegenFreq)
	for numCodegens > 4 && w.codegenFreq[codegenOrder[numCodegens-1]] == 0 {
		numCodegens--
	}
	header := 3 + 5 + 5 + 4 + (3 * numCodegens) +
		w.codegenEncoding.bitLength(w.codegenFreq[:]) +
		int(w.codegenFreq[16])*2 +
		int(w.codegenFreq[17])*3 +
		int(w.codegenFreq[18])*7
	size = header +
		litEnc.bitLength(w.literalFreq) +
		offEnc.bitLength(w.offsetFreq) +
		extraBits

	return size, numCodegens
}

// fixedSize 返回动态编码数据的大小（以比特为单位）。
func (w *huffmanBitWriter) fixedSize(extraBits int) int {
	return 3 +
		fixedLiteralEncoding.bitLength(w.literalFreq) +
		fixedOffsetEncoding.bitLength(w.offsetFreq) +
		extraBits
}

// storedSize 计算存储大小，包括头部。
// 该函数返回以比特为单位的大小，以及该块
// 是否适合放在单个块内。
func (w *huffmanBitWriter) storedSize(in []byte) (int, bool) {
	if in == nil {
		return 0, false
	}
	if len(in) <= maxStoreBlockSize {
		return (len(in) + 5) * 8, true
	}
	return 0, false
}

func (w *huffmanBitWriter) writeCode(c hcode) {
	if w.err != nil {
		return
	}
	w.bits |= uint64(c.code) << w.nbits
	w.nbits += uint(c.len)
	if w.nbits >= 48 {
		bits := w.bits
		w.bits >>= 48
		w.nbits -= 48
		n := w.nbytes
		bytes := w.bytes[n : n+6]
		bytes[0] = byte(bits)
		bytes[1] = byte(bits >> 8)
		bytes[2] = byte(bits >> 16)
		bytes[3] = byte(bits >> 24)
		bytes[4] = byte(bits >> 32)
		bytes[5] = byte(bits >> 40)
		n += 6
		if n >= bufferFlushSize {
			w.write(w.bytes[:n])
			n = 0
		}
		w.nbytes = n
	}
}

// 将动态 Huffman 块的头部写入输出流。
//
//	numLiterals  codegen 中指定的字面量数量
//	numOffsets   codegen 中指定的偏移数量
//	numCodegens  codegen 中使用的 codegen 数量
func (w *huffmanBitWriter) writeDynamicHeader(numLiterals int, numOffsets int, numCodegens int, isEof bool) {
	if w.err != nil {
		return
	}
	var firstBits int32 = 4
	if isEof {
		firstBits = 5
	}
	w.writeBits(firstBits, 3)
	w.writeBits(int32(numLiterals-257), 5)
	w.writeBits(int32(numOffsets-1), 5)
	w.writeBits(int32(numCodegens-4), 4)

	for i := 0; i < numCodegens; i++ {
		value := uint(w.codegenEncoding.codes[codegenOrder[i]].len)
		w.writeBits(int32(value), 3)
	}

	i := 0
	for {
		var codeWord int = int(w.codegen[i])
		i++
		if codeWord == badCode {
			break
		}
		w.writeCode(w.codegenEncoding.codes[uint32(codeWord)])

		switch codeWord {
		case 16:
			w.writeBits(int32(w.codegen[i]), 2)
			i++
		case 17:
			w.writeBits(int32(w.codegen[i]), 3)
			i++
		case 18:
			w.writeBits(int32(w.codegen[i]), 7)
			i++
		}
	}
}

func (w *huffmanBitWriter) writeStoredHeader(length int, isEof bool) {
	if w.err != nil {
		return
	}
	var flag int32
	if isEof {
		flag = 1
	}
	w.writeBits(flag, 3)
	w.flush()
	w.writeBits(int32(length), 16)
	w.writeBits(int32(^uint16(length)), 16)
}

func (w *huffmanBitWriter) writeFixedHeader(isEof bool) {
	if w.err != nil {
		return
	}
	// 表明我们是一个固定 Huffman 块
	var value int32 = 2
	if isEof {
		value = 3
	}
	w.writeBits(value, 3)
}

// writeBlock 将以最小编码写入一个 token 块。
// 可以提供原始输入，如果 Huffman 编码数据
// 大于原始字节，则数据将被写为
// 存储块。
// 如果输入为 nil，则 token 将始终进行 Huffman 编码。
func (w *huffmanBitWriter) writeBlock(tokens []token, eof bool, input []byte) {
	if w.err != nil {
		return
	}

	tokens = append(tokens, endBlockMarker)
	numLiterals, numOffsets := w.indexTokens(tokens)

	var extraBits int
	storedSize, storable := w.storedSize(input)
	if storable {
		// 只有在需要将这两种编码与存储编码进行比较时，
		// 我们才费力计算偏移字段长度所需的额外比特的
		// 代价（固定编码和动态编码的该代价相同）。
		for lengthCode := lengthCodesStart + 8; lengthCode < numLiterals; lengthCode++ {
			// 前八个长度码的额外大小 = 0。
			extraBits += int(w.literalFreq[lengthCode]) * int(lengthExtraBits[lengthCode-lengthCodesStart])
		}
		for offsetCode := 4; offsetCode < numOffsets; offsetCode++ {
			// 前四个偏移码的额外大小 = 0。
			extraBits += int(w.offsetFreq[offsetCode]) * int(offsetExtraBits[offsetCode])
		}
	}

	// 找出最小的码。
	// 固定 Huffman 基线。
	var literalEncoding = fixedLiteralEncoding
	var offsetEncoding = fixedOffsetEncoding
	var size = w.fixedSize(extraBits)

	// 动态 Huffman？
	var numCodegens int

	// 生成 codegen 和 codegenFrequencies，它们指示如何编码
	// literalEncoding 和 offsetEncoding。
	w.generateCodegen(numLiterals, numOffsets, w.literalEncoding, w.offsetEncoding)
	w.codegenEncoding.generate(w.codegenFreq[:], 7)
	dynamicSize, numCodegens := w.dynamicSize(w.literalEncoding, w.offsetEncoding, extraBits)

	if dynamicSize < size {
		size = dynamicSize
		literalEncoding = w.literalEncoding
		offsetEncoding = w.offsetEncoding
	}

	// 存储字节？
	if storable && storedSize < size {
		w.writeStoredHeader(len(input), eof)
		w.writeBytes(input)
		return
	}

	// Huffman。
	if literalEncoding == fixedLiteralEncoding {
		w.writeFixedHeader(eof)
	} else {
		w.writeDynamicHeader(numLiterals, numOffsets, numCodegens, eof)
	}

	// 写入 token。
	w.writeTokens(tokens, literalEncoding.codes, offsetEncoding.codes)
}

// writeBlockDynamic 使用动态 Huffman 表编码一个块。
// 如果使用的符号具有不成比例的直方图分布，
// 则应使用此方法。
// 如果提供了 input 且压缩节省量低于输入大小的
// 1/16，则该块被存储。
func (w *huffmanBitWriter) writeBlockDynamic(tokens []token, eof bool, input []byte) {
	if w.err != nil {
		return
	}

	tokens = append(tokens, endBlockMarker)
	numLiterals, numOffsets := w.indexTokens(tokens)

	// 生成 codegen 和 codegenFrequencies，它们指示如何编码
	// literalEncoding 和 offsetEncoding。
	w.generateCodegen(numLiterals, numOffsets, w.literalEncoding, w.offsetEncoding)
	w.codegenEncoding.generate(w.codegenFreq[:], 7)
	size, numCodegens := w.dynamicSize(w.literalEncoding, w.offsetEncoding, 0)

	// 存储字节，如果我们没有得到合理的改进。
	if ssize, storable := w.storedSize(input); storable && ssize < (size+size>>4) {
		w.writeStoredHeader(len(input), eof)
		w.writeBytes(input)
		return
	}

	// 写入 Huffman 表。
	w.writeDynamicHeader(numLiterals, numOffsets, numCodegens, eof)

	// 写入 token。
	w.writeTokens(tokens, w.literalEncoding.codes, w.offsetEncoding.codes)
}

// indexTokens 对 token 切片建立索引，并更新
// literalFreq 和 offsetFreq，同时生成 literalEncoding
// 和 offsetEncoding。
// 返回字面量和偏移 token 的数量。
func (w *huffmanBitWriter) indexTokens(tokens []token) (numLiterals, numOffsets int) {
	clear(w.literalFreq)
	clear(w.offsetFreq)

	for _, t := range tokens {
		if t < matchType {
			w.literalFreq[t.literal()]++
			continue
		}
		length := t.length()
		offset := t.offset()
		w.literalFreq[lengthCodesStart+lengthCode(length)]++
		w.offsetFreq[offsetCode(offset)]++
	}

	// 获取字面量的数量
	numLiterals = len(w.literalFreq)
	for w.literalFreq[numLiterals-1] == 0 {
		numLiterals--
	}
	// 获取偏移的数量
	numOffsets = len(w.offsetFreq)
	for numOffsets > 0 && w.offsetFreq[numOffsets-1] == 0 {
		numOffsets--
	}
	if numOffsets == 0 {
		// 我们还没有找到任何匹配。如果我们想采用动态编码，
		// 应该至少计数一个偏移，以确保偏移 huffman 树能够被编码。
		w.offsetFreq[0] = 1
		numOffsets = 1
	}
	w.literalEncoding.generate(w.literalFreq, 15)
	w.offsetEncoding.generate(w.offsetFreq, 15)
	return
}

// writeTokens 将一个 token 切片写入输出。
// 必须提供字面量和偏移编码的码。
func (w *huffmanBitWriter) writeTokens(tokens []token, leCodes, oeCodes []hcode) {
	if w.err != nil {
		return
	}
	for _, t := range tokens {
		if t < matchType {
			w.writeCode(leCodes[t.literal()])
			continue
		}
		// 写入长度
		length := t.length()
		lengthCode := lengthCode(length)
		w.writeCode(leCodes[lengthCode+lengthCodesStart])
		extraLengthBits := uint(lengthExtraBits[lengthCode])
		if extraLengthBits > 0 {
			extraLength := int32(length - lengthBase[lengthCode])
			w.writeBits(extraLength, extraLengthBits)
		}
		// 写入偏移
		offset := t.offset()
		offsetCode := offsetCode(offset)
		w.writeCode(oeCodes[offsetCode])
		extraOffsetBits := uint(offsetExtraBits[offsetCode])
		if extraOffsetBits > 0 {
			extraOffset := int32(offset - offsetBase[offsetCode])
			w.writeBits(extraOffset, extraOffsetBits)
		}
	}
}

// huffOffset 是一个静态偏移编码器，仅用于 huffman 编码。
// 它可以被重用，因为我们不会编码偏移值。
var huffOffset *huffmanEncoder

func init() {
	offsetFreq := make([]int32, offsetCodeCount)
	offsetFreq[0] = 1
	huffOffset = newHuffmanEncoder(offsetCodeCount)
	huffOffset.generate(offsetFreq, 15)
}

// writeBlockHuff 将字节块编码为
// Huffman 编码的字面量，或者如果压缩带来的
// 收益很小，则编码为未压缩字节。
func (w *huffmanBitWriter) writeBlockHuff(eof bool, input []byte) {
	if w.err != nil {
		return
	}

	// 清除直方图
	clear(w.literalFreq)

	// 将所有内容作为字面量添加
	histogram(input, w.literalFreq)

	w.literalFreq[endBlockMarker] = 1

	const numLiterals = endBlockMarker + 1
	w.offsetFreq[0] = 1
	const numOffsets = 1

	w.literalEncoding.generate(w.literalFreq, 15)

	// 找出最小的码。
	// 始终使用动态 Huffman 或 Store
	var numCodegens int

	// 生成 codegen 和 codegenFrequencies，它们指示如何编码
	// literalEncoding 和 offsetEncoding。
	w.generateCodegen(numLiterals, numOffsets, w.literalEncoding, huffOffset)
	w.codegenEncoding.generate(w.codegenFreq[:], 7)
	size, numCodegens := w.dynamicSize(w.literalEncoding, huffOffset, 0)

	// 存储字节，如果我们没有得到合理的改进。
	if ssize, storable := w.storedSize(input); storable && ssize < (size+size>>4) {
		w.writeStoredHeader(len(input), eof)
		w.writeBytes(input)
		return
	}

	// Huffman。
	w.writeDynamicHeader(numLiterals, numOffsets, numCodegens, eof)
	encoding := w.literalEncoding.codes[:257]
	n := w.nbytes
	for _, t := range input {
		// 比特写入内联，约 30% 提速
		c := encoding[t]
		w.bits |= uint64(c.code) << w.nbits
		w.nbits += uint(c.len)
		if w.nbits < 48 {
			continue
		}
		// 存储 6 个字节
		bits := w.bits
		w.bits >>= 48
		w.nbits -= 48
		bytes := w.bytes[n : n+6]
		bytes[0] = byte(bits)
		bytes[1] = byte(bits >> 8)
		bytes[2] = byte(bits >> 16)
		bytes[3] = byte(bits >> 24)
		bytes[4] = byte(bits >> 32)
		bytes[5] = byte(bits >> 40)
		n += 6
		if n < bufferFlushSize {
			continue
		}
		w.write(w.bytes[:n])
		if w.err != nil {
			return // 在写入失败时提前返回
		}
		n = 0
	}
	w.nbytes = n
	w.writeCode(encoding[endBlockMarker])
}

// histogram 在 h 中累积 b 的直方图。
//
// len(h) 必须 >= 256，且 h 的元素必须全为零。
func histogram(b []byte, h []int32) {
	h = h[:256]
	for _, t := range b {
		h[t]++
	}
}
