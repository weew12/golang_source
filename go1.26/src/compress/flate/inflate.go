// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package flate 实现了 RFC 1951 中描述的 DEFLATE 压缩数据格式。
// [compress/gzip] 和 [compress/zlib] 包实现了对基于 DEFLATE 的文件格式的访问。
package flate

import (
	"bufio"
	"io"
	"math/bits"
	"strconv"
	"sync"
)

const (
	maxCodeLen = 16 // 霍夫曼码的最大长度
	// 接下来的三个数字来自 RFC 第 3.2.7 节，并带有第 3.2.5 节中的
	// 附加条件，即距离码 30 和 31 永远不会出现在压缩数据中。
	maxNumLit  = 286
	maxNumDist = 30
	numCodes   = 19 // 霍夫曼元码中码的数量
)

// 仅在首次使用时初始化 fixedHuffmanDecoder 一次。
var fixedOnce sync.Once
var fixedHuffmanDecoder huffmanDecoder

// CorruptInputError 报告在给定偏移处存在损坏的输入。
type CorruptInputError int64

func (e CorruptInputError) Error() string {
	return "flate: corrupt input before offset " + strconv.FormatInt(int64(e), 10)
}

// InternalError 报告 flate 代码自身中的错误。
type InternalError string

func (e InternalError) Error() string { return "flate: internal error: " + string(e) }

// ReadError 报告读取输入时遇到的错误。
//
// Deprecated: 不再返回。
type ReadError struct {
	Offset int64 // 错误发生处的字节偏移量
	Err    error // 底层 Read 返回的错误
}

func (e *ReadError) Error() string {
	return "flate: read error at offset " + strconv.FormatInt(e.Offset, 10) + ": " + e.Err.Error()
}

// WriteError 报告写入输出时遇到的错误。
//
// Deprecated: 不再返回。
type WriteError struct {
	Offset int64 // 错误发生处的字节偏移量
	Err    error // 底层 Write 返回的错误
}

func (e *WriteError) Error() string {
	return "flate: write error at offset " + strconv.FormatInt(e.Offset, 10) + ": " + e.Err.Error()
}

// Resetter 重置由 [NewReader] 或 [NewReaderDict] 返回的 ReadCloser，
// 以切换到新的底层 [Reader]。这样可以复用 ReadCloser，而无需分配新的。
type Resetter interface {
	// Reset 丢弃所有缓冲数据，并将 Resetter 重置为
	// 如同用给定 reader 新初始化一样。
	Reset(r io.Reader, dict []byte) error
}

// 用于解码霍夫曼表的数据结构基于 zlib 的数据结构。
// 存在一个固定位宽（huffmanChunkBits）的查找表。
// 对于小于表宽的码，有多个条目
// （尾部位的所有组合都具有相同的值）。对于大于表宽的码，
// 表中包含指向溢出表的链接。链接表中每个条目的宽度
// 为最大码长减去块宽度。
//
// 请注意，即使并非所有位都已填充，也可以在该表中进行查找。
// 由于额外的位为零，且 DEFLATE 霍夫曼码具有短码排在长码之前的特性，
// 结果中估计的位长是实际位数的下界。
//
// 参见以下内容：
//	https://github.com/madler/zlib/raw/master/doc/algorithm.txt

// chunk & 15 是位数
// chunk >> 4 是值，包括表链接

const (
	huffmanChunkBits  = 9
	huffmanNumChunks  = 1 << huffmanChunkBits
	huffmanCountMask  = 15
	huffmanValueShift = 4
)

type huffmanDecoder struct {
	min      int                      // 最小码长
	chunks   [huffmanNumChunks]uint32 // 如上所述的块
	links    [][]uint32               // 溢出链接
	linkMask uint32                   // 链接表宽度的掩码
}

// 从码长数组初始化霍夫曼解码表。
// 在此函数之后，h 保证被初始化为完整的树
// （即，既不过度订阅也不订阅不足）。例外是
// 退化情况，即树仅有一个长度为 1 的符号。允许空树。
func (h *huffmanDecoder) init(lengths []int) bool {
	// Sanity 在霍夫曼表构建期间启用额外的运行时测试。
	// 它旨在开发期间使用，以补充当前临时的单元测试。
	const sanity = false

	if h.min != 0 {
		*h = huffmanDecoder{}
	}

	// 统计每种长度的码的数量，
	// 计算最小和最大长度。
	var count [maxCodeLen]int
	var min, max int
	for _, n := range lengths {
		if n == 0 {
			continue
		}
		if min == 0 || n < min {
			min = n
		}
		if n > max {
			max = n
		}
		count[n]++
	}

	// 空树。如果使用了该树，decompressor.huffSym 函数稍后将失败。
	// 严格来说，空树仅对 HDIST 树有效，而对 HCLEN 和 HLIT 树无效。
	// 然而，具有空 HCLEN 树的流必定会失败，因为它会尝试使用该树
	// 来解码 HLIT 和 HDIST 树的码。同样，空 HLIT 树
	// 必定会在稍后失败，因为压缩数据段必须
	// 至少由一个符号（块结束标记）组成。
	if max == 0 {
		return true
	}

	code := 0
	var nextcode [maxCodeLen]int
	for i := min; i <= max; i++ {
		code <<= 1
		nextcode[i] = code
		code += count[i]
	}

	// 检查编码是否完整（即，我们是否已
	// 分配了所有 2 的 max 次方种可能的位序列）。
	// 例外：为了与 zlib 兼容，我们还需要
	// 接受退化的单码编码。另请参见
	// TestDegenerateHuffmanCoding。
	if code != 1<<uint(max) && !(code == 1 && max == 1) {
		return false
	}

	h.min = min
	if max > huffmanChunkBits {
		numLinks := 1 << (uint(max) - huffmanChunkBits)
		h.linkMask = uint32(numLinks - 1)

		// 创建链接表
		link := nextcode[huffmanChunkBits+1] >> 1
		h.links = make([][]uint32, huffmanNumChunks-link)
		for j := uint(link); j < huffmanNumChunks; j++ {
			reverse := int(bits.Reverse16(uint16(j)))
			reverse >>= uint(16 - huffmanChunkBits)
			off := j - uint(link)
			if sanity && h.chunks[reverse] != 0 {
				panic("impossible: overwriting existing chunk")
			}
			h.chunks[reverse] = uint32(off<<huffmanValueShift | (huffmanChunkBits + 1))
			h.links[off] = make([]uint32, numLinks)
		}
	}

	for i, n := range lengths {
		if n == 0 {
			continue
		}
		code := nextcode[n]
		nextcode[n]++
		chunk := uint32(i<<huffmanValueShift | n)
		reverse := int(bits.Reverse16(uint16(code)))
		reverse >>= uint(16 - n)
		if n <= huffmanChunkBits {
			for off := reverse; off < len(h.chunks); off += 1 << uint(n) {
				// 我们永远不应需要覆盖
				// 现有的块。此外，0
				// 永远不是有效的块，因为
				// 低 4 位“count”位应
				// 介于 1 和 15 之间。
				if sanity && h.chunks[off] != 0 {
					panic("impossible: overwriting existing chunk")
				}
				h.chunks[off] = chunk
			}
		} else {
			j := reverse & (huffmanNumChunks - 1)
			if sanity && h.chunks[j]&huffmanCountMask != huffmanChunkBits+1 {
				// 更长的码本应已在
				// 上方关联到链接表。
				panic("impossible: not an indirect chunk")
			}
			value := h.chunks[j] >> huffmanValueShift
			linktab := h.links[value]
			reverse >>= huffmanChunkBits
			for off := reverse; off < len(linktab); off += 1 << uint(n-huffmanChunkBits) {
				if sanity && linktab[off] != 0 {
					panic("impossible: overwriting existing chunk")
				}
				linktab[off] = chunk
			}
		}
	}

	if sanity {
		// 上面我们已经检查过，确保从未覆盖
		// 现有条目。这里我们额外检查
		// 是否完全填满了表。
		for i, chunk := range h.chunks {
			if chunk == 0 {
				// 作为例外，在退化的
				// 单码情况下，我们允许缺少
				// 奇数编号的块。
				if code == 1 && i%2 == 1 {
					continue
				}
				panic("impossible: missing chunk")
			}
		}
		for _, linktab := range h.links {
			for _, chunk := range linktab {
				if chunk == 0 {
					panic("impossible: missing chunk")
				}
			}
		}
	}

	return true
}

// [NewReader] 所需的实际读取接口。
// 如果传入的 [io.Reader] 没有 ReadByte，
// [NewReader] 将引入自己的缓冲。
type Reader interface {
	io.Reader
	io.ByteReader
}

// 解压状态。
type decompressor struct {
	// 输入源。
	r       Reader
	rBuf    *bufio.Reader // 如果提供的 io.Reader 未实现 io.ByteReader 则创建
	roffset int64

	// 输入位，位于 b 的高位。
	b  uint32
	nb uint

	// 用于字面量/长度、距离的霍夫曼解码器。
	h1, h2 huffmanDecoder

	// 用于定义霍夫曼码的长度数组。
	bits     *[maxNumLit + maxNumDist]int
	codebits *[numCodes]int

	// 输出历史、缓冲。
	dict dictDecoder

	// 临时缓冲（避免重复分配）。
	buf [4]byte

	// 解压中的下一步，
	// 以及解压状态。
	step      func(*decompressor)
	stepState int
	final     bool
	err       error
	toRead    []byte
	hl, hd    *huffmanDecoder
	copyLen   int
	copyDist  int
}

func (f *decompressor) nextBlock() {
	for f.nb < 1+2 {
		if f.err = f.moreBits(); f.err != nil {
			return
		}
	}
	f.final = f.b&1 == 1
	f.b >>= 1
	typ := f.b & 3
	f.b >>= 2
	f.nb -= 1 + 2
	switch typ {
	case 0:
		f.dataBlock()
	case 1:
		// 压缩的、固定霍夫曼表
		f.hl = &fixedHuffmanDecoder
		f.hd = nil
		f.huffmanBlock()
	case 2:
		// 压缩的、动态霍夫曼表
		if f.err = f.readHuffman(); f.err != nil {
			break
		}
		f.hl = &f.h1
		f.hd = &f.h2
		f.huffmanBlock()
	default:
		// 3 是保留的。
		f.err = CorruptInputError(f.roffset)
	}
}

func (f *decompressor) Read(b []byte) (int, error) {
	for {
		if len(f.toRead) > 0 {
			n := copy(b, f.toRead)
			f.toRead = f.toRead[n:]
			if len(f.toRead) == 0 {
				return n, f.err
			}
			return n, nil
		}
		if f.err != nil {
			return 0, f.err
		}
		f.step(f)
		if f.err != nil && len(f.toRead) == 0 {
			f.toRead = f.dict.readFlush() // 在出错时刷新剩余内容
		}
	}
}

func (f *decompressor) Close() error {
	if f.err == io.EOF {
		return nil
	}
	return f.err
}

// RFC 1951 第 3.2.7 节。
// 使用动态霍夫曼码的压缩

var codeOrder = [...]int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

func (f *decompressor) readHuffman() error {
	// HLIT[5], HDIST[5], HCLEN[4].
	for f.nb < 5+5+4 {
		if err := f.moreBits(); err != nil {
			return err
		}
	}
	nlit := int(f.b&0x1F) + 257
	if nlit > maxNumLit {
		return CorruptInputError(f.roffset)
	}
	f.b >>= 5
	ndist := int(f.b&0x1F) + 1
	if ndist > maxNumDist {
		return CorruptInputError(f.roffset)
	}
	f.b >>= 5
	nclen := int(f.b&0xF) + 4
	// numCodes 为 19，因此 nclen 始终有效。
	f.b >>= 4
	f.nb -= 5 + 5 + 4

	// (HCLEN+4)*3 位：按魔法 codeOrder 顺序排列的码长。
	for i := 0; i < nclen; i++ {
		for f.nb < 3 {
			if err := f.moreBits(); err != nil {
				return err
			}
		}
		f.codebits[codeOrder[i]] = int(f.b & 0x7)
		f.b >>= 3
		f.nb -= 3
	}
	for i := nclen; i < len(codeOrder); i++ {
		f.codebits[codeOrder[i]] = 0
	}
	if !f.h1.init(f.codebits[0:]) {
		return CorruptInputError(f.roffset)
	}

	// HLIT + 257 个码长，HDIST + 1 个码长，
	// 使用码长霍夫曼码。
	for i, n := 0, nlit+ndist; i < n; {
		x, err := f.huffSym(&f.h1)
		if err != nil {
			return err
		}
		if x < 16 {
			// 实际长度。
			f.bits[i] = x
			i++
			continue
		}
		// 重复前一个长度或零。
		var rep int
		var nb uint
		var b int
		switch x {
		default:
			return InternalError("unexpected length code")
		case 16:
			rep = 3
			nb = 2
			if i == 0 {
				return CorruptInputError(f.roffset)
			}
			b = f.bits[i-1]
		case 17:
			rep = 3
			nb = 3
			b = 0
		case 18:
			rep = 11
			nb = 7
			b = 0
		}
		for f.nb < nb {
			if err := f.moreBits(); err != nil {
				return err
			}
		}
		rep += int(f.b & uint32(1<<nb-1))
		f.b >>= nb
		f.nb -= nb
		if i+rep > n {
			return CorruptInputError(f.roffset)
		}
		for j := 0; j < rep; j++ {
			f.bits[i] = b
			i++
		}
	}

	if !f.h1.init(f.bits[0:nlit]) || !f.h2.init(f.bits[nlit:nlit+ndist]) {
		return CorruptInputError(f.roffset)
	}

	// 作为优化，我们可以将 HLIT 树一次读取的最小位数
	// 初始化为 EOB 标记的长度，因为我们知道
	// 每个块都必须以它终止。这保持了
	// 在 DEFLATE 流结束后绝不读取任何多余字节的特性。
	if f.h1.min < f.bits[endBlockMarker] {
		f.h1.min = f.bits[endBlockMarker]
	}

	return nil
}

// 从 f 解码单个霍夫曼块。
// hl 和 hd 分别是字面量/长度值和距离值的霍夫曼状态。
// 如果 hd == nil，则使用与固定霍夫曼块
// 关联的固定距离编码。
func (f *decompressor) huffmanBlock() {
	const (
		stateInit = iota // 零值必须是 stateInit
		stateDict
	)

	switch f.stepState {
	case stateInit:
		goto readLiteral
	case stateDict:
		goto copyHistory
	}

readLiteral:
	// 根据 RFC 第 3.2.3 节读取字面量和/或（长度、距离）。
	{
		v, err := f.huffSym(f.hl)
		if err != nil {
			f.err = err
			return
		}
		var n uint // 额外位数
		var length int
		switch {
		case v < 256:
			f.dict.writeByte(byte(v))
			if f.dict.availWrite() == 0 {
				f.toRead = f.dict.readFlush()
				f.step = (*decompressor).huffmanBlock
				f.stepState = stateInit
				return
			}
			goto readLiteral
		case v == 256:
			f.finishBlock()
			return
		// 否则，对更早数据的引用
		case v < 265:
			length = v - (257 - 3)
			n = 0
		case v < 269:
			length = v*2 - (265*2 - 11)
			n = 1
		case v < 273:
			length = v*4 - (269*4 - 19)
			n = 2
		case v < 277:
			length = v*8 - (273*8 - 35)
			n = 3
		case v < 281:
			length = v*16 - (277*16 - 67)
			n = 4
		case v < 285:
			length = v*32 - (281*32 - 131)
			n = 5
		case v < maxNumLit:
			length = 258
			n = 0
		default:
			f.err = CorruptInputError(f.roffset)
			return
		}
		if n > 0 {
			for f.nb < n {
				if err = f.moreBits(); err != nil {
					f.err = err
					return
				}
			}
			length += int(f.b & uint32(1<<n-1))
			f.b >>= n
			f.nb -= n
		}

		var dist int
		if f.hd == nil {
			for f.nb < 5 {
				if err = f.moreBits(); err != nil {
					f.err = err
					return
				}
			}
			dist = int(bits.Reverse8(uint8(f.b & 0x1F << 3)))
			f.b >>= 5
			f.nb -= 5
		} else {
			if dist, err = f.huffSym(f.hd); err != nil {
				f.err = err
				return
			}
		}

		switch {
		case dist < 4:
			dist++
		case dist < maxNumDist:
			nb := uint(dist-2) >> 1
			// dist 的底部有 1 位，还需要 nb 位。
			extra := (dist & 1) << nb
			for f.nb < nb {
				if err = f.moreBits(); err != nil {
					f.err = err
					return
				}
			}
			extra |= int(f.b & uint32(1<<nb-1))
			f.b >>= nb
			f.nb -= nb
			dist = 1<<(nb+1) + 1 + extra
		default:
			f.err = CorruptInputError(f.roffset)
			return
		}

		// 不检查长度；编码可以预知。
		if dist > f.dict.histSize() {
			f.err = CorruptInputError(f.roffset)
			return
		}

		f.copyLen, f.copyDist = length, dist
		goto copyHistory
	}

copyHistory:
	// 根据 RFC 第 3.2.3 节执行向后复制。
	{
		cnt := f.dict.tryWriteCopy(f.copyDist, f.copyLen)
		if cnt == 0 {
			cnt = f.dict.writeCopy(f.copyDist, f.copyLen)
		}
		f.copyLen -= cnt

		if f.dict.availWrite() == 0 || f.copyLen > 0 {
			f.toRead = f.dict.readFlush()
			f.step = (*decompressor).huffmanBlock // 我们需要继续此工作
			f.stepState = stateDict
			return
		}
		goto readLiteral
	}
}

// 从输入向输出复制单个未压缩数据块。
func (f *decompressor) dataBlock() {
	// 未压缩。
	// 丢弃当前半个字节。
	f.nb = 0
	f.b = 0

	// 长度，然后是长度的反码。
	nr, err := io.ReadFull(f.r, f.buf[0:4])
	f.roffset += int64(nr)
	if err != nil {
		f.err = noEOF(err)
		return
	}
	n := int(f.buf[0]) | int(f.buf[1])<<8
	nn := int(f.buf[2]) | int(f.buf[3])<<8
	if uint16(nn) != uint16(^n) {
		f.err = CorruptInputError(f.roffset)
		return
	}

	if n == 0 {
		f.toRead = f.dict.readFlush()
		f.finishBlock()
		return
	}

	f.copyLen = n
	f.copyData()
}

// copyData 从底层 reader 复制 f.copyLen 个字节到 f.hist。
// 当 f.hist 已满时，它会暂停读取。
func (f *decompressor) copyData() {
	buf := f.dict.writeSlice()
	if len(buf) > f.copyLen {
		buf = buf[:f.copyLen]
	}

	cnt, err := io.ReadFull(f.r, buf)
	f.roffset += int64(cnt)
	f.copyLen -= cnt
	f.dict.writeMark(cnt)
	if err != nil {
		f.err = noEOF(err)
		return
	}

	if f.dict.availWrite() == 0 || f.copyLen > 0 {
		f.toRead = f.dict.readFlush()
		f.step = (*decompressor).copyData
		return
	}
	f.finishBlock()
}

func (f *decompressor) finishBlock() {
	if f.final {
		if f.dict.availRead() > 0 {
			f.toRead = f.dict.readFlush()
		}
		f.err = io.EOF
	}
	f.step = (*decompressor).nextBlock
}

// noEOF 返回 err，除非 err == io.EOF，此时它返回 io.ErrUnexpectedEOF。
func noEOF(e error) error {
	if e == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return e
}

func (f *decompressor) moreBits() error {
	c, err := f.r.ReadByte()
	if err != nil {
		return noEOF(err)
	}
	f.roffset++
	f.b |= uint32(c) << f.nb
	f.nb += 8
	return nil
}

// 根据 h 从 f 读取下一个霍夫曼编码的符号。
func (f *decompressor) huffSym(h *huffmanDecoder) (int, error) {
	// 由于 huffmanDecoder 可能为空，或由单元素的退化树组成，
	// huffSym 必须在这两种边界情况下报错。在这两种
	// 情况下，对于无效序列，chunks 切片将为 0，使其
	// 满足下面的 n == 0 检查。
	n := uint(h.min)
	// 优化。编译器不够智能，无法将 f.b、f.nb 保存在寄存器中，
	// 但足够智能，可以将局部变量保存在寄存器中，因此使用 nb 和 b，
	// 内联调用 moreBits，并在返回时将 b、nb 重新赋给 f。
	nb, b := f.nb, f.b
	for {
		for nb < n {
			c, err := f.r.ReadByte()
			if err != nil {
				f.b = b
				f.nb = nb
				return 0, noEOF(err)
			}
			f.roffset++
			b |= uint32(c) << (nb & 31)
			nb += 8
		}
		chunk := h.chunks[b&(huffmanNumChunks-1)]
		n = uint(chunk & huffmanCountMask)
		if n > huffmanChunkBits {
			chunk = h.links[chunk>>huffmanValueShift][(b>>huffmanChunkBits)&h.linkMask]
			n = uint(chunk & huffmanCountMask)
		}
		if n <= nb {
			if n == 0 {
				f.b = b
				f.nb = nb
				f.err = CorruptInputError(f.roffset)
				return 0, f.err
			}
			f.b = b >> (n & 31)
			f.nb = nb - n
			return int(chunk >> huffmanValueShift), nil
		}
	}
}

func (f *decompressor) makeReader(r io.Reader) {
	if rr, ok := r.(Reader); ok {
		f.rBuf = nil
		f.r = rr
		return
	}
	// 尽可能复用 rBuf。不变式：rBuf 始终由 decompressor 创建（并拥有）。
	if f.rBuf != nil {
		f.rBuf.Reset(r)
	} else {
		// bufio.NewReader 不会返回 r，因为 r 未实现 flate.Reader，所以它不是 bufio.Reader。
		f.rBuf = bufio.NewReader(r)
	}
	f.r = f.rBuf
}

func fixedHuffmanDecoderInit() {
	fixedOnce.Do(func() {
		// 这些来自 RFC 第 3.2.6 节。
		var bits [288]int
		for i := 0; i < 144; i++ {
			bits[i] = 8
		}
		for i := 144; i < 256; i++ {
			bits[i] = 9
		}
		for i := 256; i < 280; i++ {
			bits[i] = 7
		}
		for i := 280; i < 288; i++ {
			bits[i] = 8
		}
		fixedHuffmanDecoder.init(bits[:])
	})
}

func (f *decompressor) Reset(r io.Reader, dict []byte) error {
	*f = decompressor{
		rBuf:     f.rBuf,
		bits:     f.bits,
		codebits: f.codebits,
		dict:     f.dict,
		step:     (*decompressor).nextBlock,
	}
	f.makeReader(r)
	f.dict.init(maxMatchOffset, dict)
	return nil
}

// NewReader 返回一个新的 ReadCloser，可用于
// 读取 r 的解压版本。
// 如果 r 没有同时实现 [io.ByteReader]，
// 解压器可能会从 r 读取比所需更多的数据。
// 在遇到 DEFLATE 流中的最后一个块之后，
// reader 返回 [io.EOF]。最后一个块之后的任何尾随数据都会被忽略。
//
// NewReader 返回的 [io.ReadCloser] 还实现了 [Resetter]。
func NewReader(r io.Reader) io.ReadCloser {
	fixedHuffmanDecoderInit()

	var f decompressor
	f.makeReader(r)
	f.bits = new([maxNumLit + maxNumDist]int)
	f.codebits = new([numCodes]int)
	f.step = (*decompressor).nextBlock
	f.dict.init(maxMatchOffset, nil)
	return &f
}

// NewReaderDict 类似于 [NewReader]，但使用预设字典
// 初始化 reader。返回的 reader 的行为就像
// 未压缩数据流以给定的字典开头，
// 且该字典已被读取一样。NewReaderDict 通常用于
// 读取由 [NewWriterDict] 压缩的数据。
//
// NewReaderDict 返回的 ReadCloser 还实现了 [Resetter]。
func NewReaderDict(r io.Reader, dict []byte) io.ReadCloser {
	fixedHuffmanDecoderInit()

	var f decompressor
	f.makeReader(r)
	f.bits = new([maxNumLit + maxNumDist]int)
	f.codebits = new([numCodes]int)
	f.step = (*decompressor).nextBlock
	f.dict.init(maxMatchOffset, dict)
	return &f
}
