// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flate

import (
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	NoCompression      = 0
	BestSpeed          = 1
	BestCompression    = 9
	DefaultCompression = -1

	// HuffmanOnly 禁用 Lempel-Ziv 匹配搜索，仅执行 Huffman
	// 熵编码。该模式适用于压缩那些已经用
	// LZ 风格算法（例如 Snappy 或 LZ4）压缩过、
	// 但没有熵编码器的数据。当输入流中某些字节
	// 出现的频率高于其他字节时，可以获得压缩收益。
	//
	// 注意，HuffmanOnly 产生的压缩输出符合
	// RFC 1951 规范。也就是说，任何有效的 DEFLATE 解压器
	// 都能继续解压该输出。
	HuffmanOnly = -2
)

const (
	logWindowSize = 15
	windowSize    = 1 << logWindowSize
	windowMask    = windowSize - 1

	// LZ77 步骤产生一系列字面量 token 和 <长度, 偏移量>
	// 对 token。偏移量也称为距离。底层线路
	// 格式限制了长度和偏移量的范围。例如，共有
	// 256 种合法的长度：即 [3, 258] 范围内的那些。本包的
	// 压缩器使用更高的最小匹配长度，从而支持诸如
	// 通过 32 位加载和比较来查找匹配的优化。
	baseMatchLength = 3       // 按 RFC 第 3.2.5 节规定的最小匹配长度
	minMatchLength  = 4       // 压缩器实际输出的最小匹配长度
	maxMatchLength  = 258     // 最大匹配长度
	baseMatchOffset = 1       // 最小匹配偏移量
	maxMatchOffset  = 1 << 15 // 最大匹配偏移量

	// 单个 flate 块中允许放入的最大 token 数量，仅用于
	// 防止块变得过大。
	maxFlateBlockTokens = 1 << 14
	maxStoreBlockSize   = 65535
	hashBits            = 17 // 超过 17 后性能会下降
	hashSize            = 1 << hashBits
	hashMask            = (1 << hashBits) - 1
	maxHashOffset       = 1 << 24

	skipNever = math.MaxInt32
)

type compressionLevel struct {
	level, good, lazy, nice, chain, fastSkipHashing int
}

var levels = []compressionLevel{
	{0, 0, 0, 0, 0, 0}, // NoCompression。
	{1, 0, 0, 0, 0, 0}, // BestSpeed 使用自定义算法；参见 deflatefast.go。
	// 对于等级 2-3，我们不尝试使用惰性匹配。
	{2, 4, 0, 16, 8, 5},
	{3, 4, 0, 32, 32, 6},
	// 等级 4-9 使用越来越多的惰性匹配
	// 以及越来越严格的“足够好”条件。
	{4, 4, 4, 16, 16, skipNever},
	{5, 8, 16, 32, 32, skipNever},
	{6, 8, 16, 128, 128, skipNever},
	{7, 8, 32, 128, 256, skipNever},
	{8, 32, 128, 258, 1024, skipNever},
	{9, 32, 258, 258, 4096, skipNever},
}

type compressor struct {
	compressionLevel

	w          *huffmanBitWriter
	bulkHasher func([]byte, []uint32)

	// 压缩算法
	fill      func(*compressor, []byte) int // 将数据复制到窗口
	step      func(*compressor)             // 处理窗口
	bestSpeed *deflateFast                  // BestSpeed 的编码器

	// 输入窗口：未处理的数据为 window[index:windowEnd]
	index         int
	window        []byte
	windowEnd     int
	blockStart    int  // 当前 token 开始的窗口索引
	byteAvailable bool // 若为 true，仍需处理 window[index-1]。

	sync bool // 请求 flush

	// 已排队的输出 token
	tokens []token

	// deflate 状态
	length         int
	offset         int
	maxInsertIndex int
	err            error

	// 输入哈希链
	// hashHead[hashValue] 包含具有指定哈希值的最大 inputIndex
	// 若 hashHead[hashValue] 位于当前窗口内，则
	// hashPrev[hashHead[hashValue] & windowMask] 包含具有相同哈希值的
	// 前一个索引。
	// 这些数组较大且不含指针，因此将它们放在
	// 结构体末尾附近，以减少 GC 需要扫描的范围。
	chainHead  int
	hashHead   [hashSize]uint32
	hashPrev   [windowSize]uint32
	hashOffset int

	// hashMatch 必须能够容纳最大匹配长度的哈希值。
	hashMatch [maxMatchLength - 1]uint32
}

func (d *compressor) fillDeflate(b []byte) int {
	if d.index >= 2*windowSize-(minMatchLength+maxMatchLength) {
		// 将窗口按 windowSize 平移
		copy(d.window, d.window[windowSize:2*windowSize])
		d.index -= windowSize
		d.windowEnd -= windowSize
		if d.blockStart >= windowSize {
			d.blockStart -= windowSize
		} else {
			d.blockStart = math.MaxInt32
		}
		d.hashOffset += windowSize
		if d.hashOffset > maxHashOffset {
			delta := d.hashOffset - 1
			d.hashOffset -= delta
			d.chainHead -= delta

			// 遍历切片而不是数组，以避免将整个
			// 表复制到栈上（Issue #18625）。
			for i, v := range d.hashPrev[:] {
				if int(v) > delta {
					d.hashPrev[i] = uint32(int(v) - delta)
				} else {
					d.hashPrev[i] = 0
				}
			}
			for i, v := range d.hashHead[:] {
				if int(v) > delta {
					d.hashHead[i] = uint32(int(v) - delta)
				} else {
					d.hashHead[i] = 0
				}
			}
		}
	}
	n := copy(d.window[d.windowEnd:], b)
	d.windowEnd += n
	return n
}

func (d *compressor) writeBlock(tokens []token, index int) error {
	if index > 0 {
		var window []byte
		if d.blockStart <= index {
			window = d.window[d.blockStart:index]
		}
		d.blockStart = index
		d.w.writeBlock(tokens, false, window)
		return d.w.err
	}
	return nil
}

// fillWindow 使用提供的字典填充当前窗口
// 并计算所有哈希值。
// 这比执行完整编码要快得多。
// 仅应在 reset 之后使用。
func (d *compressor) fillWindow(b []byte) {
	// 如果处于仅存储模式，则不填充窗口。
	if d.compressionLevel.level < 2 {
		return
	}
	if d.index != 0 || d.windowEnd != 0 {
		panic("internal error: fillWindow called with stale data")
	}

	// 如果给定的数据过多，将其截断。
	if len(b) > windowSize {
		b = b[len(b)-windowSize:]
	}
	// 全部加入窗口。
	n := copy(d.window, b)

	// 每次计算 256 个哈希值（获得更多 L1 缓存命中）
	loops := (n + 256 - minMatchLength) / 256
	for j := 0; j < loops; j++ {
		index := j * 256
		end := index + 256 + minMatchLength - 1
		if end > n {
			end = n
		}
		toCheck := d.window[index:end]
		dstSize := len(toCheck) - minMatchLength + 1

		if dstSize <= 0 {
			continue
		}

		dst := d.hashMatch[:dstSize]
		d.bulkHasher(toCheck, dst)
		for i, val := range dst {
			di := i + index
			hh := &d.hashHead[val&hashMask]
			// 获取具有相同哈希值的前一个值。
			// 我们的链应指向前一个值。
			d.hashPrev[di&windowMask] = *hh
			// 将哈希链的头设为我们。
			*hh = uint32(di + d.hashOffset)
		}
	}
	// 更新窗口信息。
	d.windowEnd = n
	d.index = n
}

// 尝试查找一个从 index 开始、长度大于 prevSize 的匹配。
// 在放弃之前，我们只查看 chainCount 种可能性。
func (d *compressor) findMatch(pos int, prevHead int, prevLength int, lookahead int) (length, offset int, ok bool) {
	minMatchLook := maxMatchLength
	if lookahead < minMatchLook {
		minMatchLook = lookahead
	}

	win := d.window[0 : pos+minMatchLook]

	// 当找到一个至少达到 nice 长度的匹配时就停止
	nice := len(win) - pos
	if d.nice < nice {
		nice = d.nice
	}

	// 如果已经找到一个足够好的匹配，则只在 1/4 的链中查找。
	tries := d.chain
	length = prevLength
	if length >= d.good {
		tries >>= 2
	}

	wEnd := win[pos+length]
	wPos := win[pos:]
	minIndex := pos - windowSize

	for i := prevHead; tries > 0; tries-- {
		if wEnd == win[i+length] {
			n := matchLen(win[i:], wPos, minMatchLook)

			if n > length && (n > minMatchLength || pos-i <= 4096) {
				length = n
				offset = pos - i
				ok = true
				if n >= nice {
					// 该匹配已经足够好，不再尝试寻找更好的。
					break
				}
				wEnd = win[pos+n]
			}
		}
		if i == minIndex {
			// hashPrev[i & windowMask] 已被覆盖，因此现在停止。
			break
		}
		i = int(d.hashPrev[i&windowMask]) - d.hashOffset
		if i < minIndex || i < 0 {
			break
		}
	}
	return
}

func (d *compressor) writeStoredBlock(buf []byte) error {
	if d.w.writeStoredHeader(len(buf), false); d.w.err != nil {
		return d.w.err
	}
	d.w.writeBytes(buf)
	return d.w.err
}

const hashmul = 0x1e35a7bd

// hash4 返回所提供切片前 4 个字节的
// 哈希表示。
// 调用方必须确保 len(b) >= 4。
func hash4(b []byte) uint32 {
	return ((uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24) * hashmul) >> (32 - hashBits)
}

// bulkHash4 将使用与 hash4 相同的
// 算法计算哈希值。
func bulkHash4(b []byte, dst []uint32) {
	if len(b) < minMatchLength {
		return
	}
	hb := uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
	dst[0] = (hb * hashmul) >> (32 - hashBits)
	end := len(b) - minMatchLength + 1
	for i := 1; i < end; i++ {
		hb = (hb << 8) | uint32(b[i+3])
		dst[i] = (hb * hashmul) >> (32 - hashBits)
	}
}

// matchLen 返回 a 和 b 中匹配的字节数，
// 最多为 'max' 个。两个切片的大小必须至少为 'max'
// 字节。
func matchLen(a, b []byte, max int) int {
	a = a[:max]
	b = b[:len(a)]
	for i, av := range a {
		if b[i] != av {
			return i
		}
	}
	return max
}

// encSpeed 将压缩并存储当前已添加的数据，
// 前提是已累积足够的数据或我们已到达流的末尾。
// 发生的任何错误都会保存在 d.err 中
func (d *compressor) encSpeed() {
	// 仅当达到 maxStoreBlockSize 时才进行压缩。
	if d.windowEnd < maxStoreBlockSize {
		if !d.sync {
			return
		}

		// 处理较小的大小。
		if d.windowEnd < 128 {
			switch {
			case d.windowEnd == 0:
				return
			case d.windowEnd <= 16:
				d.err = d.writeStoredBlock(d.window[:d.windowEnd])
			default:
				d.w.writeBlockHuff(false, d.window[:d.windowEnd])
				d.err = d.w.err
			}
			d.windowEnd = 0
			d.bestSpeed.reset()
			return
		}

	}
	// 编码该块。
	d.tokens = d.bestSpeed.encode(d.tokens[:0], d.window[:d.windowEnd])

	// 如果移除的量少于 1/16，则对该块进行 Huffman 压缩。
	if len(d.tokens) > d.windowEnd-(d.windowEnd>>4) {
		d.w.writeBlockHuff(false, d.window[:d.windowEnd])
	} else {
		d.w.writeBlockDynamic(d.tokens, false, d.window[:d.windowEnd])
	}
	d.err = d.w.err
	d.windowEnd = 0
}

func (d *compressor) initDeflate() {
	d.window = make([]byte, 2*windowSize)
	d.hashOffset = 1
	d.tokens = make([]token, 0, maxFlateBlockTokens+1)
	d.length = minMatchLength - 1
	d.offset = 0
	d.byteAvailable = false
	d.index = 0
	d.chainHead = -1
	d.bulkHasher = bulkHash4
}

func (d *compressor) deflate() {
	if d.windowEnd-d.index < minMatchLength+maxMatchLength && !d.sync {
		return
	}

	d.maxInsertIndex = d.windowEnd - (minMatchLength - 1)

Loop:
	for {
		if d.index > d.windowEnd {
			panic("index > windowEnd")
		}
		lookahead := d.windowEnd - d.index
		if lookahead < minMatchLength+maxMatchLength {
			if !d.sync {
				break Loop
			}
			if d.index > d.windowEnd {
				panic("index > windowEnd")
			}
			if lookahead == 0 {
				// 如果有当前输出块，则将其刷新。
				if d.byteAvailable {
					// 仍有一个待处理的 token 需要刷新
					d.tokens = append(d.tokens, literalToken(uint32(d.window[d.index-1])))
					d.byteAvailable = false
				}
				if len(d.tokens) > 0 {
					if d.err = d.writeBlock(d.tokens, d.index); d.err != nil {
						return
					}
					d.tokens = d.tokens[:0]
				}
				break Loop
			}
		}
		if d.index < d.maxInsertIndex {
			// 更新哈希
			hash := hash4(d.window[d.index : d.index+minMatchLength])
			hh := &d.hashHead[hash&hashMask]
			d.chainHead = int(*hh)
			d.hashPrev[d.index&windowMask] = uint32(d.chainHead)
			*hh = uint32(d.index + d.hashOffset)
		}
		prevLength := d.length
		prevOffset := d.offset
		d.length = minMatchLength - 1
		d.offset = 0
		minIndex := d.index - windowSize
		if minIndex < 0 {
			minIndex = 0
		}

		if d.chainHead-d.hashOffset >= minIndex &&
			(d.fastSkipHashing != skipNever && lookahead > minMatchLength-1 ||
				d.fastSkipHashing == skipNever && lookahead > prevLength && prevLength < d.lazy) {
			if newLength, newOffset, ok := d.findMatch(d.index, d.chainHead-d.hashOffset, minMatchLength-1, lookahead); ok {
				d.length = newLength
				d.offset = newOffset
			}
		}
		if d.fastSkipHashing != skipNever && d.length >= minMatchLength ||
			d.fastSkipHashing == skipNever && prevLength >= minMatchLength && d.length <= prevLength {
			// 上一步存在一个匹配，而当前匹配
			// 并不更好。输出上一个匹配。
			if d.fastSkipHashing != skipNever {
				d.tokens = append(d.tokens, matchToken(uint32(d.length-baseMatchLength), uint32(d.offset-baseMatchOffset)))
			} else {
				d.tokens = append(d.tokens, matchToken(uint32(prevLength-baseMatchLength), uint32(prevOffset-baseMatchOffset)))
			}
			// 将与匹配末尾之间的所有字符串插入哈希表。
			// index 和 index-1 已经插入。如果 lookahead 不足，
			// 最后两个字符串不会被插入哈希
			// 表。
			if d.length <= d.fastSkipHashing {
				var newIndex int
				if d.fastSkipHashing != skipNever {
					newIndex = d.index + d.length
				} else {
					newIndex = d.index + prevLength - 1
				}
				index := d.index
				for index++; index < newIndex; index++ {
					if index < d.maxInsertIndex {
						hash := hash4(d.window[index : index+minMatchLength])
						// 获取具有相同哈希值的前一个值。
						// 我们的链应指向前一个值。
						hh := &d.hashHead[hash&hashMask]
						d.hashPrev[index&windowMask] = *hh
						// 将哈希链的头设为我们。
						*hh = uint32(index + d.hashOffset)
					}
				}
				d.index = index

				if d.fastSkipHashing == skipNever {
					d.byteAvailable = false
					d.length = minMatchLength - 1
				}
			} else {
				// 对于这么长的匹配，我们不必将每个单独的
				// 项都插入表中。
				d.index += d.length
			}
			if len(d.tokens) == maxFlateBlockTokens {
				// 该块包含当前字符
				if d.err = d.writeBlock(d.tokens, d.index); d.err != nil {
					return
				}
				d.tokens = d.tokens[:0]
			}
		} else {
			if d.fastSkipHashing != skipNever || d.byteAvailable {
				i := d.index - 1
				if d.fastSkipHashing != skipNever {
					i = d.index
				}
				d.tokens = append(d.tokens, literalToken(uint32(d.window[i])))
				if len(d.tokens) == maxFlateBlockTokens {
					if d.err = d.writeBlock(d.tokens, i+1); d.err != nil {
						return
					}
					d.tokens = d.tokens[:0]
				}
			}
			d.index++
			if d.fastSkipHashing == skipNever {
				d.byteAvailable = true
			}
		}
	}
}

func (d *compressor) fillStore(b []byte) int {
	n := copy(d.window[d.windowEnd:], b)
	d.windowEnd += n
	return n
}

func (d *compressor) store() {
	if d.windowEnd > 0 && (d.windowEnd == maxStoreBlockSize || d.sync) {
		d.err = d.writeStoredBlock(d.window[:d.windowEnd])
		d.windowEnd = 0
	}
}

// storeHuff 在当前已添加的数据填满 d.window 时
// 或我们已到达流的末尾时压缩并存储这些数据。
// 发生的任何错误都会保存在 d.err 中
func (d *compressor) storeHuff() {
	if d.windowEnd < len(d.window) && !d.sync || d.windowEnd == 0 {
		return
	}
	d.w.writeBlockHuff(false, d.window[:d.windowEnd])
	d.err = d.w.err
	d.windowEnd = 0
}

func (d *compressor) write(b []byte) (n int, err error) {
	if d.err != nil {
		return 0, d.err
	}
	n = len(b)
	for len(b) > 0 {
		d.step(d)
		b = b[d.fill(d, b):]
		if d.err != nil {
			return 0, d.err
		}
	}
	return n, nil
}

func (d *compressor) syncFlush() error {
	if d.err != nil {
		return d.err
	}
	d.sync = true
	d.step(d)
	if d.err == nil {
		d.w.writeStoredHeader(0, false)
		d.w.flush()
		d.err = d.w.err
	}
	d.sync = false
	return d.err
}

func (d *compressor) init(w io.Writer, level int) (err error) {
	d.w = newHuffmanBitWriter(w)

	switch {
	case level == NoCompression:
		d.window = make([]byte, maxStoreBlockSize)
		d.fill = (*compressor).fillStore
		d.step = (*compressor).store
	case level == HuffmanOnly:
		d.window = make([]byte, maxStoreBlockSize)
		d.fill = (*compressor).fillStore
		d.step = (*compressor).storeHuff
	case level == BestSpeed:
		d.compressionLevel = levels[level]
		d.window = make([]byte, maxStoreBlockSize)
		d.fill = (*compressor).fillStore
		d.step = (*compressor).encSpeed
		d.bestSpeed = newDeflateFast()
		d.tokens = make([]token, maxStoreBlockSize)
	case level == DefaultCompression:
		level = 6
		fallthrough
	case 2 <= level && level <= 9:
		d.compressionLevel = levels[level]
		d.initDeflate()
		d.fill = (*compressor).fillDeflate
		d.step = (*compressor).deflate
	default:
		return fmt.Errorf("flate: invalid compression level %d: want value in range [-2, 9]", level)
	}
	return nil
}

func (d *compressor) reset(w io.Writer) {
	d.w.reset(w)
	d.sync = false
	d.err = nil
	switch d.compressionLevel.level {
	case NoCompression:
		d.windowEnd = 0
	case BestSpeed:
		d.windowEnd = 0
		d.tokens = d.tokens[:0]
		d.bestSpeed.reset()
	default:
		d.chainHead = -1
		clear(d.hashHead[:])
		clear(d.hashPrev[:])
		d.hashOffset = 1
		d.index, d.windowEnd = 0, 0
		d.blockStart, d.byteAvailable = 0, false
		d.tokens = d.tokens[:0]
		d.length = minMatchLength - 1
		d.offset = 0
		d.maxInsertIndex = 0
	}
}

func (d *compressor) close() error {
	if d.err == errWriterClosed {
		return nil
	}
	if d.err != nil {
		return d.err
	}
	d.sync = true
	d.step(d)
	if d.err != nil {
		return d.err
	}
	if d.w.writeStoredHeader(0, true); d.w.err != nil {
		return d.w.err
	}
	d.w.flush()
	if d.w.err != nil {
		return d.w.err
	}
	d.err = errWriterClosed
	return nil
}

// NewWriter 返回一个新的 [Writer]，它按给定等级压缩数据。
// 遵循 zlib 的规范，等级范围从 1（[BestSpeed]）到 9（[BestCompression]）；
// 等级越高通常运行越慢，但压缩率越高。等级 0
// （[NoCompression]）不尝试任何压缩；它只添加
// 必要的 DEFLATE 帧。
// 等级 -1（[DefaultCompression]）使用默认压缩等级。
// 等级 -2（[HuffmanOnly]）将仅使用 Huffman 压缩，从而
// 对所有类型的输入都提供非常快的压缩，但会牺牲相当多的
// 压缩效率。
//
// 若 level 在范围 [-2, 9] 内，返回的错误将为 nil。
// 否则返回的错误将非 nil。
func NewWriter(w io.Writer, level int) (*Writer, error) {
	var dw Writer
	if err := dw.d.init(w, level); err != nil {
		return nil, err
	}
	return &dw, nil
}

// NewWriterDict 类似于 [NewWriter]，但会用预设的字典
// 初始化新的 [Writer]。返回的 [Writer] 的行为就如同
// 该字典已被写入其中且未产生任何压缩输出。
// 写入 w 的压缩数据只能由一个用相同字典
// 初始化的读取器解压（参见 [NewReaderDict]）。
func NewWriterDict(w io.Writer, level int, dict []byte) (*Writer, error) {
	dw := &dictWriter{w}
	zw, err := NewWriter(dw, level)
	if err != nil {
		return nil, err
	}
	zw.d.fillWindow(dict)
	zw.dict = append(zw.dict, dict...) // 复制字典以供 Reset 方法使用。
	return zw, nil
}

type dictWriter struct {
	w io.Writer
}

func (w *dictWriter) Write(b []byte) (n int, err error) {
	return w.w.Write(b)
}

var errWriterClosed = errors.New("flate: closed writer")

// Writer 接收写入其中的数据，并将该数据的压缩
// 形式写入一个底层 writer（参见 [NewWriter]）。
type Writer struct {
	d    compressor
	dict []byte
}

// Write 将数据写入 w，w 最终会将数据的压缩
// 形式写入其底层 writer。
func (w *Writer) Write(data []byte) (n int, err error) {
	return w.d.write(data)
}

// Flush 将所有待处理的数据刷新到底层 writer。
// 它主要用于压缩网络协议中，以确保
// 远程读取器有足够的数据来重建一个数据包。
// Flush 直到数据被写入后才返回。
// 在没有待处理数据时调用 Flush 仍会使 [Writer]
// 发出至少 4 个字节的同步标记。
// 如果底层 writer 返回错误，Flush 会返回该错误。
//
// 在 zlib 库的术语中，Flush 等价于 Z_SYNC_FLUSH。
func (w *Writer) Flush() error {
	// 关于刷新（flushing）的更多信息：
	// https://www.bolet.org/~pornin/deflate-flush.html
	return w.d.syncFlush()
}

// Close 刷新并关闭该 writer。
func (w *Writer) Close() error {
	return w.d.close()
}

// Reset 丢弃 writer 的状态，使其等同于用 dst
// 以及 w 的等级和字典调用 [NewWriter] 或 [NewWriterDict]
// 所得到的结果。
func (w *Writer) Reset(dst io.Writer) {
	if dw, ok := w.d.w.writer.(*dictWriter); ok {
		// w 是用 NewWriterDict 创建的
		dw.w = dst
		w.d.reset(dw)
		w.d.fillWindow(w.dict)
	} else {
		// w 是用 NewWriter 创建的
		w.d.reset(dst)
	}
}
