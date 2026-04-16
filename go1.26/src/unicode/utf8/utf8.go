// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// utf8 包实现了支持 UTF-8 编码文本的函数和常量。
// 它包含在 rune 和 UTF-8 字节序列之间进行转换的函数。
// 参见 https://en.wikipedia.org/wiki/UTF-8
package utf8

// RuneError==unicode.ReplacementChar 和 MaxRune==unicode.MaxRune
// 这些条件在测试中已验证。
// 在本地定义它们可以避免此包依赖 unicode 包。

// 编码的基本数值。
const (
	RuneError = '\uFFFD'     // "错误" Rune 或 "Unicode 替换字符"
	RuneSelf  = 0x80         // 低于 RuneSelf 的字符在单个字节中以自身表示。
	MaxRune   = '\U0010FFFF' // 最大有效 Unicode 码点。
	UTFMax    = 4            // UTF-8 编码的 Unicode 字符的最大字节数。
)

// 代理项范围内的码点对 UTF-8 无效。
const (
	surrogateMin = 0xD800
	surrogateMax = 0xDFFF
)

const (
	t1 = 0b00000000
	tx = 0b10000000
	t2 = 0b11000000
	t3 = 0b11100000
	t4 = 0b11110000
	t5 = 0b11111000

	maskx = 0b00111111
	mask2 = 0b00011111
	mask3 = 0b00001111
	mask4 = 0b00000111

	rune1Max = 1<<7 - 1
	rune2Max = 1<<11 - 1
	rune3Max = 1<<16 - 1

	// 默认的最低和最高延续字节。
	locb = 0b10000000
	hicb = 0b10111111

	// 这些常量的名称经过选择以在下方的表格中提供良好的对齐。
	// 第一个半字节是 acceptRanges 的索引，或者 F 表示特殊的单字节情况。
	// 第二个半字节是 Rune 长度，或者是特殊单字节情况的状态。
	xx = 0xF1 // 无效：大小 1
	as = 0xF0 // ASCII：大小 1
	s1 = 0x02 // accept 0, size 2
	s2 = 0x13 // accept 1, size 3
	s3 = 0x03 // accept 0, size 3
	s4 = 0x23 // accept 2, size 3
	s5 = 0x34 // accept 3, size 4
	s6 = 0x04 // accept 0, size 4
	s7 = 0x44 // accept 4, size 4
)

const (
	runeErrorByte0 = t3 | (RuneError >> 12)
	runeErrorByte1 = tx | (RuneError>>6)&maskx
	runeErrorByte2 = tx | RuneError&maskx
)

// first 是关于 UTF-8 序列中第一个字节的信息。
var first = [256]uint8{
	//   1   2   3   4   5   6   7   8   9   A   B   C   D   E   F
	as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, // 0x00-0x0F
	as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, // 0x10-0x1F
	as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, // 0x20-0x2F
	as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, // 0x30-0x3F
	as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, // 0x40-0x4F
	as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, // 0x50-0x5F
	as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, // 0x60-0x6F
	as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, as, // 0x70-0x7F
	//   1   2   3   4   5   6   7   8   9   A   B   C   D   E   F
	xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, // 0x80-0x8F
	xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, // 0x90-0x9F
	xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, // 0xA0-0xAF
	xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, // 0xB0-0xBF
	xx, xx, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, // 0xC0-0xCF
	s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, s1, // 0xD0-0xDF
	s2, s3, s3, s3, s3, s3, s3, s3, s3, s3, s3, s3, s3, s4, s3, s3, // 0xE0-0xEF
	s5, s6, s6, s6, s7, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, xx, // 0xF0-0xFF
}

// acceptRange 给出 UTF-8 序列中第二个字节的有效值范围。
type acceptRange struct {
	lo uint8 // 第二个字节的最低值。
	hi uint8 // 第二个字节的最高值。
}

// acceptRanges 的大小为 16，以避免使用它的代码中的边界检查。
var acceptRanges = [16]acceptRange{
	0: {locb, hicb},
	1: {0xA0, hicb},
	2: {locb, 0x9F},
	3: {0x90, hicb},
	4: {locb, 0x8F},
}

// FullRune 报告 p 中的字节是否以一个完整的 UTF-8 编码的 rune 开头。
// 无效编码被视为完整的 Rune，因为它将作为宽度为 1 的错误 rune 进行转换。
func FullRune(p []byte) bool {
	n := len(p)
	if n == 0 {
		return false
	}
	x := first[p[0]]
	if n >= int(x&7) {
		return true // ASCII、无效或有效。
	}
	// 必定是短的或无效的。
	accept := acceptRanges[x>>4]
	if n > 1 && (p[1] < accept.lo || accept.hi < p[1]) {
		return true
	} else if n > 2 && (p[2] < locb || hicb < p[2]) {
		return true
	}
	return false
}

// FullRuneInString 类似于 [FullRune]，但其输入为字符串。
func FullRuneInString(s string) bool {
	n := len(s)
	if n == 0 {
		return false
	}
	x := first[s[0]]
	if n >= int(x&7) {
		return true // ASCII、无效或有效。
	}
	// 必定是短的或无效的。
	accept := acceptRanges[x>>4]
	if n > 1 && (s[1] < accept.lo || accept.hi < s[1]) {
		return true
	} else if n > 2 && (s[2] < locb || hicb < s[2]) {
		return true
	}
	return false
}

// DecodeRune 解包 p 中的第一个 UTF-8 编码并返回该 rune 及其字节宽度。
// 如果 p 为空，则返回 ([RuneError], 0)。否则，如果编码无效，
// 则返回 (RuneError, 1)。对于正确的非空 UTF-8，这两种结果都是不可能的。
//
// 如果编码不是正确的 UTF-8、编码的 rune 超出范围、
// 或不是该值最短的 UTF-8 编码，则该编码无效。不执行其他验证。
func DecodeRune(p []byte) (r rune, size int) {
	// ASCII 字符的可内联快速路径；参见 #48195。
	// 此实现方式看起来奇怪，但能有效地使函数可内联。
	for _, b := range p {
		if b < RuneSelf {
			return rune(b), 1
		}
		break
	}
	r, size = decodeRuneSlow(p)
	return
}

func decodeRuneSlow(p []byte) (r rune, size int) {
	n := len(p)
	if n < 1 {
		return RuneError, 0
	}
	p0 := p[0]
	x := first[p0]
	if x >= as {
		// 以下代码模拟了对 x == xx 的额外检查，
		// 并相应地处理 ASCII 和无效情况。这种掩码与或的方法避免了额外的分支。
		mask := rune(x) << 31 >> 31 // 创建 0x0000 或 0xFFFF。
		return rune(p[0])&^mask | RuneError&mask, 1
	}
	sz := int(x & 7)
	accept := acceptRanges[x>>4]
	if n < sz {
		return RuneError, 1
	}
	b1 := p[1]
	if b1 < accept.lo || accept.hi < b1 {
		return RuneError, 1
	}
	if sz <= 2 { // 使用 <= 而非 == 以帮助编译器消除一些边界检查
		return rune(p0&mask2)<<6 | rune(b1&maskx), 2
	}
	b2 := p[2]
	if b2 < locb || hicb < b2 {
		return RuneError, 1
	}
	if sz <= 3 {
		return rune(p0&mask3)<<12 | rune(b1&maskx)<<6 | rune(b2&maskx), 3
	}
	b3 := p[3]
	if b3 < locb || hicb < b3 {
		return RuneError, 1
	}
	return rune(p0&mask4)<<18 | rune(b1&maskx)<<12 | rune(b2&maskx)<<6 | rune(b3&maskx), 4
}

// DecodeRuneInString 类似于 [DecodeRune]，但其输入为字符串。如果 s 为空，
// 则返回 ([RuneError], 0)。否则，如果编码无效，则返回 (RuneError, 1)。
// 对于正确的非空 UTF-8，这两种结果都是不可能的。
//
// 如果编码不是正确的 UTF-8、编码的 rune 超出范围、
// 或不是该值最短的 UTF-8 编码，则该编码无效。不执行其他验证。
func DecodeRuneInString(s string) (r rune, size int) {
	// ASCII 字符的可内联快速路径；参见 #48195。
	// 此实现方式看起来有点奇怪，但能有效地使函数可内联。
	if s != "" && s[0] < RuneSelf {
		return rune(s[0]), 1
	} else {
		r, size = decodeRuneInStringSlow(s)
	}
	return
}

func decodeRuneInStringSlow(s string) (rune, int) {
	n := len(s)
	if n < 1 {
		return RuneError, 0
	}
	s0 := s[0]
	x := first[s0]
	if x >= as {
		// 以下代码模拟了对 x == xx 的额外检查，
		// 并相应地处理 ASCII 和无效情况。这种掩码与或的方法避免了额外的分支。
		mask := rune(x) << 31 >> 31 // 创建 0x0000 或 0xFFFF。
		return rune(s[0])&^mask | RuneError&mask, 1
	}
	sz := int(x & 7)
	accept := acceptRanges[x>>4]
	if n < sz {
		return RuneError, 1
	}
	s1 := s[1]
	if s1 < accept.lo || accept.hi < s1 {
		return RuneError, 1
	}
	if sz <= 2 { // 使用 <= 而非 == 以帮助编译器消除一些边界检查
		return rune(s0&mask2)<<6 | rune(s1&maskx), 2
	}
	s2 := s[2]
	if s2 < locb || hicb < s2 {
		return RuneError, 1
	}
	if sz <= 3 {
		return rune(s0&mask3)<<12 | rune(s1&maskx)<<6 | rune(s2&maskx), 3
	}
	s3 := s[3]
	if s3 < locb || hicb < s3 {
		return RuneError, 1
	}
	return rune(s0&mask4)<<18 | rune(s1&maskx)<<12 | rune(s2&maskx)<<6 | rune(s3&maskx), 4
}

// DecodeLastRune 解包 p 中的最后一个 UTF-8 编码并返回该 rune 及其字节宽度。
// 如果 p 为空，则返回 ([RuneError], 0)。否则，如果编码无效，
// 则返回 (RuneError, 1)。对于正确的非空 UTF-8，这两种结果都是不可能的。
//
// 如果编码不是正确的 UTF-8、编码的 rune 超出范围、
// 或不是该值最短的 UTF-8 编码，则该编码无效。不执行其他验证。
func DecodeLastRune(p []byte) (r rune, size int) {
	end := len(p)
	if end == 0 {
		return RuneError, 0
	}
	start := end - 1
	r = rune(p[start])
	if r < RuneSelf {
		return r, 1
	}
	// 防止在反向遍历包含长序列无效 UTF-8 的字符串时出现 O(n^2) 行为。
	lim := max(end-UTFMax, 0)
	for start--; start >= lim; start-- {
		if RuneStart(p[start]) {
			break
		}
	}
	if start < 0 {
		start = 0
	}
	r, size = DecodeRune(p[start:end])
	if start+size != end {
		return RuneError, 1
	}
	return r, size
}

// DecodeLastRuneInString 类似于 [DecodeLastRune]，但其输入为字符串。
// 如果 s 为空，则返回 ([RuneError], 0)。否则，如果编码无效，
// 则返回 (RuneError, 1)。对于正确的非空 UTF-8，这两种结果都是不可能的。
//
// 如果编码不是正确的 UTF-8、编码的 rune 超出范围、
// 或不是该值最短的 UTF-8 编码，则该编码无效。不执行其他验证。
func DecodeLastRuneInString(s string) (r rune, size int) {
	end := len(s)
	if end == 0 {
		return RuneError, 0
	}
	start := end - 1
	r = rune(s[start])
	if r < RuneSelf {
		return r, 1
	}
	// 防止在反向遍历包含长序列无效 UTF-8 的字符串时出现 O(n^2) 行为。
	lim := max(end-UTFMax, 0)
	for start--; start >= lim; start-- {
		if RuneStart(s[start]) {
			break
		}
	}
	if start < 0 {
		start = 0
	}
	r, size = DecodeRuneInString(s[start:end])
	if start+size != end {
		return RuneError, 1
	}
	return r, size
}

// RuneLen 返回该 rune 的 UTF-8 编码的字节数。
// 如果该 rune 不是可用 UTF-8 编码的有效值，则返回 -1。
func RuneLen(r rune) int {
	switch {
	case r < 0:
		return -1
	case r <= rune1Max:
		return 1
	case r <= rune2Max:
		return 2
	case surrogateMin <= r && r <= surrogateMax:
		return -1
	case r <= rune3Max:
		return 3
	case r <= MaxRune:
		return 4
	}
	return -1
}

// EncodeRune 将 rune 的 UTF-8 编码写入 p（必须足够大）。
// 如果 rune 超出范围，则写入 [RuneError] 的编码。
// 返回写入的字节数。
func EncodeRune(p []byte, r rune) int {
	// 此函数可内联以快速处理 ASCII。
	if uint32(r) <= rune1Max {
		p[0] = byte(r)
		return 1
	}
	return encodeRuneNonASCII(p, r)
}

func encodeRuneNonASCII(p []byte, r rune) int {
	// 负值是错误的。将其转换为无符号可以解决此问题。
	switch i := uint32(r); {
	case i <= rune2Max:
		_ = p[1] // 消除边界检查
		p[0] = t2 | byte(r>>6)
		p[1] = tx | byte(r)&maskx
		return 2
	case i < surrogateMin, surrogateMax < i && i <= rune3Max:
		_ = p[2] // 消除边界检查
		p[0] = t3 | byte(r>>12)
		p[1] = tx | byte(r>>6)&maskx
		p[2] = tx | byte(r)&maskx
		return 3
	case i > rune3Max && i <= MaxRune:
		_ = p[3] // 消除边界检查
		p[0] = t4 | byte(r>>18)
		p[1] = tx | byte(r>>12)&maskx
		p[2] = tx | byte(r>>6)&maskx
		p[3] = tx | byte(r)&maskx
		return 4
	default:
		_ = p[2] // 消除边界检查
		p[0] = runeErrorByte0
		p[1] = runeErrorByte1
		p[2] = runeErrorByte2
		return 3
	}
}

// AppendRune 将 r 的 UTF-8 编码追加到 p 的末尾并返回扩展后的缓冲区。
// 如果 rune 超出范围，则追加 [RuneError] 的编码。
func AppendRune(p []byte, r rune) []byte {
	// 此函数可内联以快速处理 ASCII。
	if uint32(r) <= rune1Max {
		return append(p, byte(r))
	}
	return appendRuneNonASCII(p, r)
}

func appendRuneNonASCII(p []byte, r rune) []byte {
	// 负值是错误的。将其转换为无符号可以解决此问题。
	switch i := uint32(r); {
	case i <= rune2Max:
		return append(p, t2|byte(r>>6), tx|byte(r)&maskx)
	case i < surrogateMin, surrogateMax < i && i <= rune3Max:
		return append(p, t3|byte(r>>12), tx|byte(r>>6)&maskx, tx|byte(r)&maskx)
	case i > rune3Max && i <= MaxRune:
		return append(p, t4|byte(r>>18), tx|byte(r>>12)&maskx, tx|byte(r>>6)&maskx, tx|byte(r)&maskx)
	default:
		return append(p, runeErrorByte0, runeErrorByte1, runeErrorByte2)
	}
}

// RuneCount 返回 p 中的 rune 数量。错误和过短的编码被视为
// 宽度为 1 字节的单个 rune。
func RuneCount(p []byte) int {
	np := len(p)
	var n int
	for ; n < np; n++ {
		if c := p[n]; c >= RuneSelf {
			// 非 ASCII 慢速路径
			return n + RuneCountInString(string(p[n:]))
		}
	}
	return n
}

// RuneCountInString 类似于 [RuneCount]，但其输入为字符串。
func RuneCountInString(s string) (n int) {
	for range s {
		n++
	}
	return n
}

// RuneStart 报告该字节是否可能是一个已编码（可能无效）rune 的第一个字节。
// 第二个及后续字节的最高两位始终设置为 10。
func RuneStart(b byte) bool { return b&0xC0 != 0x80 }

const ptrSize = 4 << (^uintptr(0) >> 63)
const hiBits = 0x8080808080808080 >> (64 - 8*ptrSize)

func word[T string | []byte](s T) uintptr {
	if ptrSize == 4 {
		return uintptr(s[0]) | uintptr(s[1])<<8 | uintptr(s[2])<<16 | uintptr(s[3])<<24
	}
	return uintptr(uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 | uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56)
}

// Valid 报告 p 是否完全由有效的 UTF-8 编码的 rune 组成。
func Valid(p []byte) bool {
	// 此优化避免了在生成 p 的切片代码时重新计算容量的需要，
	// 使其与 ValidString 持平，后者在长 ASCII 字符串上快 20%。
	p = p[:len(p):len(p)]

	for len(p) > 0 {
		p0 := p[0]
		if p0 < RuneSelf {
			p = p[1:]
			// 如果有一个 ASCII 字节，可能还有更多。
			// 快速跳过纯 ASCII 数据。
			// 注意：这里有意使用 > 而非 >=。这避免了在切片操作中
			// 需要进行指向末尾之后的修正。
			if len(p) > ptrSize && word(p)&hiBits == 0 {
				p = p[ptrSize:]
				if len(p) > 2*ptrSize && (word(p)|word(p[ptrSize:]))&hiBits == 0 {
					p = p[2*ptrSize:]
					for len(p) > 4*ptrSize && ((word(p)|word(p[ptrSize:]))|(word(p[2*ptrSize:])|word(p[3*ptrSize:])))&hiBits == 0 {
						p = p[4*ptrSize:]
					}
				}
			}
			continue
		}
		x := first[p0]
		size := int(x & 7)
		accept := acceptRanges[x>>4]
		switch size {
		case 2:
			if len(p) < 2 || p[1] < accept.lo || accept.hi < p[1] {
				return false
			}
			p = p[2:]
		case 3:
			if len(p) < 3 || p[1] < accept.lo || accept.hi < p[1] || p[2] < locb || hicb < p[2] {
				return false
			}
			p = p[3:]
		case 4:
			if len(p) < 4 || p[1] < accept.lo || accept.hi < p[1] || p[2] < locb || hicb < p[2] || p[3] < locb || hicb < p[3] {
				return false
			}
			p = p[4:]
		default:
			return false // 非法的起始字节
		}
	}
	return true
}

// ValidString 报告 s 是否完全由有效的 UTF-8 编码的 rune 组成。
func ValidString(s string) bool {
	for len(s) > 0 {
		s0 := s[0]
		if s0 < RuneSelf {
			s = s[1:]
			// 如果有一个 ASCII 字节，可能还有更多。
			// 快速跳过纯 ASCII 数据。
			// 注意：这里有意使用 > 而非 >=。这避免了在切片操作中
			// 需要进行指向末尾之后的修正。
			if len(s) > ptrSize && word(s)&hiBits == 0 {
				s = s[ptrSize:]
				if len(s) > 2*ptrSize && (word(s)|word(s[ptrSize:]))&hiBits == 0 {
					s = s[2*ptrSize:]
					for len(s) > 4*ptrSize && ((word(s)|word(s[ptrSize:]))|(word(s[2*ptrSize:])|word(s[3*ptrSize:])))&hiBits == 0 {
						s = s[4*ptrSize:]
					}
				}
			}
			continue
		}
		x := first[s0]
		size := int(x & 7)
		accept := acceptRanges[x>>4]
		switch size {
		case 2:
			if len(s) < 2 || s[1] < accept.lo || accept.hi < s[1] {
				return false
			}
			s = s[2:]
		case 3:
			if len(s) < 3 || s[1] < accept.lo || accept.hi < s[1] || s[2] < locb || hicb < s[2] {
				return false
			}
			s = s[3:]
		case 4:
			if len(s) < 4 || s[1] < accept.lo || accept.hi < s[1] || s[2] < locb || hicb < s[2] || s[3] < locb || hicb < s[3] {
				return false
			}
			s = s[4:]
		default:
			return false // 非法的起始字节
		}
	}
	return true
}

// ValidRune 报告 r 是否可以合法地编码为 UTF-8。
// 超出范围的码点或代理项半值是非法的。
func ValidRune(r rune) bool {
	switch {
	case 0 <= r && r < surrogateMin:
		return true
	case surrogateMax < r && r <= MaxRune:
		return true
	}
	return false
}
