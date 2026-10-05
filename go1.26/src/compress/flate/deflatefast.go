// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flate

import "math"

// 这种编码算法优先考虑速度而非输出大小，
// 它基于 Snappy 的 LZ77 风格编码器：github.com/golang/snappy

const (
	tableBits  = 14             // 表中使用的位数。
	tableSize  = 1 << tableBits // 表的大小。
	tableMask  = tableSize - 1  // 表索引的掩码。虽然冗余，但可以消除边界检查。
	tableShift = 32 - tableBits // 右移以获取 uint32 的最高 tableBits 位。

	// 达到此值时重置缓冲区偏移量。
	// 偏移量在块之间以 int32 值存储。
	// 由于我们检查的偏移量位于缓冲区的开头，
	// 我们需要减去当前缓冲区和输入缓冲区，
	// 以避免 int32 溢出的风险。
	bufferReset = math.MaxInt32 - maxStoreBlockSize*2
)

func load32(b []byte, i int32) uint32 {
	b = b[i : i+4 : len(b)] // 帮助编译器消除下一行的边界检查。
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func load64(b []byte, i int32) uint64 {
	b = b[i : i+8 : len(b)] // 帮助编译器消除下一行的边界检查。
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

func hash(u uint32) uint32 {
	return (u * 0x1e35a7bd) >> tableShift
}

// 这些常量由 Snappy 实现定义，以便其汇编实现可以
// 快速处理某些每次 16 字节的复制。它们在纯 Go 实现中
// 并非必需，因为我们不使用相同的优化，
// 但使用相同的阈值并没有什么坏处。
const (
	inputMargin            = 16 - 1
	minNonLiteralBlockSize = 1 + 1 + inputMargin
)

type tableEntry struct {
	val    uint32 // 目标位置的值
	offset int32
}

// deflateFast 维护用于匹配的表，
// 以及用于跨块匹配的前一个字节块。
type deflateFast struct {
	table [tableSize]tableEntry
	prev  []byte // 前一个块，如果未知则长度为零。
	cur   int32  // 当前匹配偏移量。
}

func newDeflateFast() *deflateFast {
	return &deflateFast{cur: maxStoreBlockSize, prev: make([]byte, 0, maxStoreBlockSize)}
}

// encode 对 src 中给定的块进行编码，并将 token 追加到
// dst 并返回结果。
func (e *deflateFast) encode(dst []token, src []byte) []token {
	// 确保 e.cur 不会回绕。
	if e.cur >= bufferReset {
		e.shiftOffsets()
	}

	// 此检查不在 Snappy 实现中，但在那里，是由调用方
	// 而非被调用方处理这种情况。
	if len(src) < minNonLiteralBlockSize {
		e.cur += maxStoreBlockSize
		e.prev = e.prev[:0]
		return emitLiteral(dst, src)
	}

	// sLimit 是停止寻找偏移/长度复制的位置。inputMargin
	// 让我们在寻找复制时，在主循环中为 emitLiteral
	// 使用快速路径。
	sLimit := int32(len(src) - inputMargin)

	// nextEmit 是 src 中下一次 emitLiteral 应该开始的位置。
	nextEmit := int32(0)
	s := int32(0)
	cv := load32(src, s)
	nextHash := hash(cv)

	for {
		// 复制自 C++ snappy 实现：
		//
		// 启发式匹配跳过：如果扫描了 32 个字节都未找到匹配，
		// 则开始只查看每隔一个的字节。如果再扫描（或跳过）
		// 32 个字节，则查看每隔两个的字节，以此类推。当找到匹配时，
		// 立即回到查看每个字节。对于可压缩数据，由于需要更多的
		// 簿记，这会有小幅损失（约 5% 性能，约 0.1% 密度），
		// 但对于不可压缩数据（如 JPEG）则是巨大的胜利，因为压缩器
		// 会很快“意识到”数据不可压缩，从而不再到处寻找匹配。
		//
		// “skip” 变量记录自上次匹配以来有多少字节；将其除以 32
		// （即右移五位）就得到每次迭代要前进的字节数。
		skip := int32(32)

		nextS := s
		var candidate tableEntry
		for {
			s = nextS
			bytesBetweenHashLookups := skip >> 5
			nextS = s + bytesBetweenHashLookups
			skip += bytesBetweenHashLookups
			if nextS > sLimit {
				goto emitRemainder
			}
			candidate = e.table[nextHash&tableMask]
			now := load32(src, nextS)
			e.table[nextHash&tableMask] = tableEntry{offset: s + e.cur, val: cv}
			nextHash = hash(now)

			offset := s - (candidate.offset - e.cur)
			if offset > maxMatchOffset || cv != candidate.val {
				// 超出范围或不匹配。
				cv = now
				continue
			}
			break
		}

		// 已找到一个 4 字节匹配。我们稍后会查看是否有超过 4 字节的
		// 匹配。但是，在匹配之前，src[nextEmit:s] 是不匹配的。将它们
		// 作为字面量字节发出。
		dst = emitLiteral(dst, src[nextEmit:s])

		// 调用 emitCopy，然后查看另一个 emitCopy 是否可能是我们的下一步
		// 操作。重复进行，直到在最后一次 emitCopy 调用所消耗的内容之后
		// 立即找不到输入的匹配为止。
		//
		// 如果我们正常退出此循环，那么接下来需要调用 emitLiteral，
		// 尽管我们尚不知道字面量会有多大。我们通过继续主循环的
		// 下一次迭代来处理这一点。如果接近耗尽输入，我们也可以通过
		// goto 退出此循环。
		for {
			// 不变式：我们在 s 处有一个 4 字节匹配，并且不需要发出
			// s 之前的任何字面量字节。

			// 尽可能延长 4 字节匹配。
			//
			s += 4
			t := candidate.offset - e.cur + 4
			l := e.matchLen(s, t, src)

			// matchToken 是 flate 中与 Snappy 的 emitCopy 等价的东西。(length,offset)
			dst = append(dst, matchToken(uint32(l+4-baseMatchLength), uint32(s-t-baseMatchOffset)))
			s += l
			nextEmit = s
			if s >= sLimit {
				goto emitRemainder
			}

			// 我们现在就可以立即在 s 处开始工作，但为了提高压缩率，
			// 我们首先更新 s-1 和 s 处的哈希表。如果另一个 emitCopy
			// 不是我们的下一步操作，还要在 s+1 处计算 nextHash。
			// 至少在 GOARCH=amd64 上，这三次哈希计算作为一次 load64
			// 调用（带一些移位）比三次 load32 调用更快。
			x := load64(src, s-1)
			prevHash := hash(uint32(x))
			e.table[prevHash&tableMask] = tableEntry{offset: e.cur + s - 1, val: uint32(x)}
			x >>= 8
			currHash := hash(uint32(x))
			candidate = e.table[currHash&tableMask]
			e.table[currHash&tableMask] = tableEntry{offset: e.cur + s, val: uint32(x)}

			offset := s - (candidate.offset - e.cur)
			if offset > maxMatchOffset || uint32(x) != candidate.val {
				cv = uint32(x >> 8)
				nextHash = hash(cv)
				s++
				break
			}
		}
	}

emitRemainder:
	if int(nextEmit) < len(src) {
		dst = emitLiteral(dst, src[nextEmit:])
	}
	e.cur += int32(len(src))
	e.prev = e.prev[:len(src)]
	copy(e.prev, src)
	return dst
}

func emitLiteral(dst []token, lit []byte) []token {
	for _, v := range lit {
		dst = append(dst, literalToken(uint32(v)))
	}
	return dst
}

// matchLen 返回 src[s:] 和 src[t:] 之间的匹配长度。
// t 可以为负数，表示匹配从 e.prev 中开始。
// 我们假设 src[s-4:s] 和 src[t-4:t] 已经匹配。
func (e *deflateFast) matchLen(s, t int32, src []byte) int32 {
	s1 := int(s) + maxMatchLength - 4
	if s1 > len(src) {
		s1 = len(src)
	}

	// 如果我们在当前块内部
	if t >= 0 {
		b := src[t:]
		a := src[s:s1]
		b = b[:len(a)]
		// 尽可能延长匹配。
		for i := range a {
			if a[i] != b[i] {
				return int32(i)
			}
		}
		return int32(len(a))
	}

	// 我们在前一个块中找到匹配。
	tp := int32(len(e.prev)) + t
	if tp < 0 {
		return 0
	}

	// 尽可能延长匹配。
	a := src[s:s1]
	b := e.prev[tp:]
	if len(b) > len(a) {
		b = b[:len(a)]
	}
	a = a[:len(b)]
	for i := range b {
		if a[i] != b[i] {
			return int32(i)
		}
	}

	// 如果我们达到限制，说明我们已在前一个块中匹配了
	// 所有允许匹配的内容，然后返回。
	n := int32(len(b))
	if int(s+n) == s1 {
		return n
	}

	// 继续在当前块中寻找更多匹配。
	a = src[s+n : s1]
	b = src[:len(a)]
	for i := range a {
		if a[i] != b[i] {
			return int32(i) + n
		}
	}
	return int32(len(a)) + n
}

// Reset 重置编码历史记录。
// 这确保不会与前一个块产生匹配。
func (e *deflateFast) reset() {
	e.prev = e.prev[:0]
	// 增加偏移量，使所有匹配都无法通过距离检查。
	// 表中不应有任何值 >= e.cur。
	e.cur += maxMatchOffset

	// 防止 e.cur 回绕。
	if e.cur >= bufferReset {
		e.shiftOffsets()
	}
}

// shiftOffsets 会将所有匹配偏移量向下移动。
// 这仅在极少数情况下调用以防止整数溢出。
//
// 参见 https://golang.org/issue/18636 和 https://github.com/golang/go/issues/34121。
func (e *deflateFast) shiftOffsets() {
	if len(e.prev) == 0 {
		// 我们没有历史记录；只需清空表。
		clear(e.table[:])
		e.cur = maxMatchOffset + 1
		return
	}

	// 将表中尚未距离过远的所有内容向下移动。
	for i := range e.table[:] {
		v := e.table[i].offset - e.cur + maxMatchOffset + 1
		if v < 0 {
			// 我们想将 e.cur 重置为 maxMatchOffset + 1，因此需要将所有
			// 表项向下移动 (e.cur - (maxMatchOffset + 1))。
			// 因为我们忽略大于 maxMatchOffset 的匹配，所以我们可以将
			// 任何负偏移量上限设为 0。
			v = 0
		}
		e.table[i].offset = v
	}
	e.cur = maxMatchOffset + 1
}
