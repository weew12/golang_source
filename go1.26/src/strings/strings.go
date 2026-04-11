// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package strings 实现了用于操作 UTF-8 编码字符串的简单函数。
//
// 有关 Go 中 UTF-8 字符串的更多信息，请参阅 https://blog.golang.org/strings。
package strings

import (
	"internal/bytealg"
	"internal/stringslite"
	"math/bits"
	"unicode"
	"unicode/utf8"
)

const maxInt = int(^uint(0) >> 1)

// explode 将 s 切分为 UTF-8 字符串切片，
// 每个 Unicode 字符对应一个字符串，最多切分 n 个（n < 0 表示无限制）。
// 无效的 UTF-8 字节会被单独切分。
func explode(s string, n int) []string {
	l := utf8.RuneCountInString(s)
	if n < 0 || n > l {
		n = l
	}
	a := make([]string, n)
	for i := 0; i < n-1; i++ {
		_, size := utf8.DecodeRuneInString(s)
		a[i] = s[:size]
		s = s[size:]
	}
	if n > 0 {
		a[n-1] = s
	}
	return a
}

// Count 统计 substr 在 s 中非重叠出现的次数。
// 若 substr 为空字符串，Count 返回 1 + s 中 Unicode 码点的数量。
func Count(s, substr string) int {
	// 特殊情况
	if len(substr) == 0 {
		return utf8.RuneCountInString(s) + 1
	}
	if len(substr) == 1 {
		return bytealg.CountString(s, substr[0])
	}
	n := 0
	for {
		i := Index(s, substr)
		if i == -1 {
			return n
		}
		n++
		s = s[i+len(substr):]
	}
}

// Contains 判断 substr 是否存在于 s 中。
func Contains(s, substr string) bool {
	return Index(s, substr) >= 0
}

// ContainsAny 判断 chars 中任意 Unicode 码点是否存在于 s 中。
func ContainsAny(s, chars string) bool {
	return IndexAny(s, chars) >= 0
}

// ContainsRune 判断 Unicode 码点 r 是否存在于 s 中。
func ContainsRune(s string, r rune) bool {
	return IndexRune(s, r) >= 0
}

// ContainsFunc 判断 s 中是否存在任意 Unicode 码点 r 满足 f(r)。
func ContainsFunc(s string, f func(rune) bool) bool {
	return IndexFunc(s, f) >= 0
}

// LastIndex 返回 substr 在 s 中最后一次出现的索引，若不存在则返回 -1。
func LastIndex(s, substr string) int {
	n := len(substr)
	switch {
	case n == 0:
		return len(s)
	case n == 1:
		return bytealg.LastIndexByteString(s, substr[0])
	case n == len(s):
		if substr == s {
			return 0
		}
		return -1
	case n > len(s):
		return -1
	}
	// 从字符串末尾执行 Rabin-Karp 搜索
	hashss, pow := bytealg.HashStrRev(substr)
	last := len(s) - n
	var h uint32
	for i := len(s) - 1; i >= last; i-- {
		h = h*bytealg.PrimeRK + uint32(s[i])
	}
	if h == hashss && s[last:] == substr {
		return last
	}
	for i := last - 1; i >= 0; i-- {
		h *= bytealg.PrimeRK
		h += uint32(s[i])
		h -= pow * uint32(s[i+n])
		if h == hashss && s[i:i+n] == substr {
			return i
		}
	}
	return -1
}

// IndexByte 返回 c 在 s 中第一次出现的索引，若不存在则返回 -1。
func IndexByte(s string, c byte) int {
	return stringslite.IndexByte(s, c)
}

// IndexRune 返回 Unicode 码点 r 在 s 中第一次出现的索引，
// 若不存在则返回 -1。
// 若 r 为 [utf8.RuneError]，则返回任意无效 UTF-8 字节序列的首次出现位置。
func IndexRune(s string, r rune) int {
	const haveFastIndex = bytealg.MaxBruteForce > 0
	switch {
	case 0 <= r && r < utf8.RuneSelf:
		return IndexByte(s, byte(r))
	case r == utf8.RuneError:
		for i, r := range s {
			if r == utf8.RuneError {
				return i
			}
		}
		return -1
	case !utf8.ValidRune(r):
		return -1
	default:
		// 使用 rune r 的 UTF-8 编码的最后一个字节进行搜索
		// 最后一个字节的分布比首字节更均匀，首字节有 78% 的概率为 [240, 243, 244]
		rs := string(r)
		last := len(rs) - 1
		i := last
		fails := 0
		for i < len(s) {
			if s[i] != rs[last] {
				o := IndexByte(s[i+1:], rs[last])
				if o < 0 {
					return -1
				}
				i += o + 1
			}
			// 向前逐字节对比
			for j := 1; j < len(rs); j++ {
				if s[i-j] != rs[last-j] {
					goto next
				}
			}
			return i - last
		next:
			fails++
			i++
			if (haveFastIndex && fails > bytealg.Cutover(i)) && i < len(s) ||
				(!haveFastIndex && fails >= 4+i>>4 && i < len(s)) {
				goto fallback
			}
		}
		return -1

	fallback:
		// 参见 ../bytes/bytes.go 中的注释
		if haveFastIndex {
			if j := bytealg.IndexString(s[i-last:], string(r)); j >= 0 {
				return i + j - last
			}
		} else {
			c0 := rs[last]
			c1 := rs[last-1]
		loop:
			for ; i < len(s); i++ {
				if s[i] == c0 && s[i-1] == c1 {
					for k := 2; k < len(rs); k++ {
						if s[i-k] != rs[last-k] {
							continue loop
						}
					}
					return i - last
				}
			}
		}
		return -1
	}
}

// IndexAny 返回 chars 中任意 Unicode 码点在 s 中第一次出现的索引，
// 若不存在则返回 -1。
func IndexAny(s, chars string) int {
	if chars == "" {
		// 避免扫描整个 s
		return -1
	}
	if len(chars) == 1 {
		// 避免扫描整个 s
		r := rune(chars[0])
		if r >= utf8.RuneSelf {
			r = utf8.RuneError
		}
		return IndexRune(s, r)
	}
	if len(s) > 8 {
		if as, isASCII := makeASCIISet(chars); isASCII {
			for i := 0; i < len(s); i++ {
				if as.contains(s[i]) {
					return i
				}
			}
			return -1
		}
	}
	for i, c := range s {
		if IndexRune(chars, c) >= 0 {
			return i
		}
	}
	return -1
}

// LastIndexAny 返回 chars 中任意 Unicode 码点在 s 中最后一次出现的索引，
// 若不存在则返回 -1。
func LastIndexAny(s, chars string) int {
	if chars == "" {
		// 避免扫描整个 s
		return -1
	}
	if len(s) == 1 {
		rc := rune(s[0])
		if rc >= utf8.RuneSelf {
			rc = utf8.RuneError
		}
		if IndexRune(chars, rc) >= 0 {
			return 0
		}
		return -1
	}
	if len(s) > 8 {
		if as, isASCII := makeASCIISet(chars); isASCII {
			for i := len(s) - 1; i >= 0; i-- {
				if as.contains(s[i]) {
					return i
				}
			}
			return -1
		}
	}
	if len(chars) == 1 {
		rc := rune(chars[0])
		if rc >= utf8.RuneSelf {
			rc = utf8.RuneError
		}
		for i := len(s); i > 0; {
			r, size := utf8.DecodeLastRuneInString(s[:i])
			i -= size
			if rc == r {
				return i
			}
		}
		return -1
	}
	for i := len(s); i > 0; {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
		if IndexRune(chars, r) >= 0 {
			return i
		}
	}
	return -1
}

// LastIndexByte 返回 c 在 s 中最后一次出现的索引，若不存在则返回 -1。
func LastIndexByte(s string, c byte) int {
	return bytealg.LastIndexByteString(s, c)
}

// Generic split: 在 sep 的每次出现位置后进行切分，
// 在子数组中保留 sep 的 sepSave 个字节。
func genSplit(s, sep string, sepSave, n int) []string {
	if n == 0 {
		return nil
	}
	if sep == "" {
		return explode(s, n)
	}
	if n < 0 {
		n = Count(s, sep) + 1
	}

	if n > len(s)+1 {
		n = len(s) + 1
	}
	a := make([]string, n)
	n--
	i := 0
	for i < n {
		m := Index(s, sep)
		if m < 0 {
			break
		}
		a[i] = s[:m+sepSave]
		s = s[m+len(sep):]
		i++
	}
	a[i] = s
	return a[:i+1]
}

// SplitN 将 s 按 sep 切分为子字符串，并返回分隔符之间的子字符串切片。
//
// count 决定返回的子字符串数量：
//   - n > 0: 最多返回 n 个子字符串，最后一个子字符串为未切分的剩余部分；
//   - n == 0: 返回 nil（零个子字符串）；
//   - n < 0: 返回所有子字符串。
//
// s 和 sep 的边界情况（例如空字符串）处理方式
// 与 [Split] 文档中描述一致。
//
// 如需围绕分隔符的首次出现进行切分，请参阅 [Cut]。
func SplitN(s, sep string, n int) []string { return genSplit(s, sep, 0, n) }

// SplitAfterN 在 sep 的每次出现位置后切分 s，
// 并返回这些子字符串的切片。
//
// count 决定返回的子字符串数量：
//   - n > 0: 最多返回 n 个子字符串，最后一个子字符串为未切分的剩余部分；
//   - n == 0: 返回 nil（零个子字符串）；
//   - n < 0: 返回所有子字符串。
//
// s 和 sep 的边界情况（例如空字符串）处理方式
// 与 [SplitAfter] 文档中描述一致。
func SplitAfterN(s, sep string, n int) []string {
	return genSplit(s, sep, len(sep), n)
}

// Split 将 s 按 sep 全量切分为子字符串，并返回分隔符之间的子字符串切片。
//
// 若 s 不包含 sep 且 sep 非空，Split 返回长度为 1 的切片，唯一元素为 s。
//
// 若 sep 为空，Split 在每个 UTF-8 序列后切分。
// 若 s 和 sep 均为空，Split 返回空切片。
//
// 该函数等价于 count 为 -1 的 [SplitN]。
//
// 如需围绕分隔符的首次出现进行切分，请参阅 [Cut]。
func Split(s, sep string) []string { return genSplit(s, sep, 0, -1) }

// SplitAfter 在 sep 的每次出现位置后全量切分 s，
// 并返回这些子字符串的切片。
//
// 若 s 不包含 sep 且 sep 非空，SplitAfter 返回
// 长度为 1 的切片，唯一元素为 s。
//
// 若 sep 为空，SplitAfter 在每个 UTF-8 序列后切分。
// 若 s 和 sep 均为空，SplitAfter 返回空切片。
//
// 该函数等价于 count 为 -1 的 [SplitAfterN]。
func SplitAfter(s, sep string) []string {
	return genSplit(s, sep, len(sep), -1)
}

var asciiSpace = [256]uint8{'\t': 1, '\n': 1, '\v': 1, '\f': 1, '\r': 1, ' ': 1}

// Fields 以一个或多个连续空白字符（由 [unicode.IsSpace] 定义）为分隔符拆分字符串 s，
// 返回 s 的子字符串切片；若 s 仅包含空白字符则返回空切片。
// 返回切片的所有元素均非空。与 [Split] 不同，首尾连续的空白字符会被丢弃。
func Fields(s string) []string {
	// 首先统计字段数量
	// 若 s 为 ASCII 则为精确计数，否则为近似计数
	n := 0
	wasSpace := 1
	// setBits 用于追踪 s 字节中置位的比特位
	setBits := uint8(0)
	for i := 0; i < len(s); i++ {
		r := s[i]
		setBits |= r
		isSpace := int(asciiSpace[r])
		n += wasSpace & ^isSpace
		wasSpace = isSpace
	}

	if setBits >= utf8.RuneSelf {
		// 输入字符串中存在非 ASCII 码点
		return FieldsFunc(s, unicode.IsSpace)
	}
	// ASCII 快速路径
	a := make([]string, n)
	na := 0
	fieldStart := 0
	i := 0
	// 跳过输入开头的空白字符
	for i < len(s) && asciiSpace[s[i]] != 0 {
		i++
	}
	fieldStart = i
	for i < len(s) {
		if asciiSpace[s[i]] == 0 {
			i++
			continue
		}
		a[na] = s[fieldStart:i]
		na++
		i++
		// 跳过字段间的空白字符
		for i < len(s) && asciiSpace[s[i]] != 0 {
			i++
		}
		fieldStart = i
	}
	if fieldStart < len(s) { // 最后一个字段可能在文件末尾结束
		a[na] = s[fieldStart:]
	}
	return a
}

// FieldsFunc 以满足 f(c) 的连续 Unicode 码点为分隔符拆分字符串 s，
// 返回 s 的子切片数组。若 s 中所有码点均满足 f(c) 或字符串为空，则返回空切片。
// 返回切片的所有元素均非空。与 [Split] 不同，首尾满足 f(c) 的连续码点会被丢弃。
//
// FieldsFunc 不保证调用 f(c) 的顺序，
// 并假定对于给定的 c，f 始终返回相同值。
func FieldsFunc(s string, f func(rune) bool) []string {
	// span 用于记录 s 的切片，格式为 s[start:end]
	// start 索引为闭区间，end 索引为开区间
	type span struct {
		start int
		end   int
	}
	spans := make([]span, 0, 32)

	// 查找字段的起始和结束索引
	// 分两次处理（而非直接切分字符串并收集结果）
	// 效率显著更高，可能与缓存效应有关
	start := -1 // 若 >= 0 则为有效区间起始
	for end, rune := range s {
		if f(rune) {
			if start >= 0 {
				spans = append(spans, span{start, end})
				// 将 start 设为负值
				// 注意：在 amd64 架构上统一使用 -1 会使代码性能下降数个百分点
				start = ^start
			}
		} else {
			if start < 0 {
				start = end
			}
		}
	}

	// 最后一个字段可能在文件末尾结束
	if start >= 0 {
		spans = append(spans, span{start, len(s)})
	}

	// 根据记录的索引创建字符串
	a := make([]string, len(spans))
	for i, span := range spans {
		a[i] = s[span.start:span.end]
	}

	return a
}

// Join 将第一个参数的元素拼接为单个字符串。
// 分隔符 sep 会被放置在结果字符串的元素之间。
func Join(elems []string, sep string) string {
	switch len(elems) {
	case 0:
		return ""
	case 1:
		return elems[0]
	}

	var n int
	if len(sep) > 0 {
		if len(sep) >= maxInt/(len(elems)-1) {
			panic("strings: Join output length overflow")
		}
		n += len(sep) * (len(elems) - 1)
	}
	for _, elem := range elems {
		if len(elem) > maxInt-n {
			panic("strings: Join output length overflow")
		}
		n += len(elem)
	}

	var b Builder
	b.Grow(n)
	b.WriteString(elems[0])
	for _, s := range elems[1:] {
		b.WriteString(sep)
		b.WriteString(s)
	}
	return b.String()
}

// HasPrefix 判断字符串 s 是否以 prefix 开头。
func HasPrefix(s, prefix string) bool {
	return stringslite.HasPrefix(s, prefix)
}

// HasSuffix 判断字符串 s 是否以 suffix 结尾。
func HasSuffix(s, suffix string) bool {
	return stringslite.HasSuffix(s, suffix)
}

// Map 返回字符串 s 的副本，其中所有字符根据映射函数进行修改。
// 若映射函数返回负值，则该字符会被从字符串中移除且无替换。
func Map(mapping func(rune) rune, s string) string {
	// 最坏情况下，映射后的字符串可能变长，导致处理开销增加
	// 但这种情况极少发生，因此默认按正常情况处理
	// 字符串也可能变短，这会自然适配

	// 输出缓冲区 b 按需初始化，在第一个字符发生变化时创建
	var b Builder

	for i, c := range s {
		r := mapping(c)
		if r == c && c != utf8.RuneError {
			continue
		}

		var width int
		if c == utf8.RuneError {
			c, width = utf8.DecodeRuneInString(s[i:])
			if width != 1 && r == c {
				continue
			}
		} else {
			width = utf8.RuneLen(c)
		}

		b.Grow(len(s) + utf8.UTFMax)
		b.WriteString(s[:i])
		if r >= 0 {
			b.WriteRune(r)
		}

		s = s[i+width:]
		break
	}

	// 输入无变化的快速路径
	if b.Cap() == 0 { // 未调用上述 b.Grow
		return s
	}

	for _, c := range s {
		r := mapping(c)

		if r >= 0 {
			// 通用场景
			// 得益于内联，判断是否调用 WriteByte
			// 比直接调用 WriteRune 性能更高
			if r < utf8.RuneSelf {
				b.WriteByte(byte(r))
			} else {
				// r 为非 ASCII 码点
				b.WriteRune(r)
			}
		}
	}

	return b.String()
}

// 根据静态分析，空格、短横线、零、等号和制表符
// 是最常用的重复字符串字面量，
// 常用于固定宽度终端窗口的显示。
// 为这些字符预声明常量，实现通用场景下的 O(1) 重复生成。
const (
	repeatedSpaces = "" +
		"                                                                " +
		"                                                                "
	repeatedDashes = "" +
		"----------------------------------------------------------------" +
		"----------------------------------------------------------------"
	repeatedZeroes = "" +
		"0000000000000000000000000000000000000000000000000000000000000000"
	repeatedEquals = "" +
		"================================================================" +
		"================================================================"
	repeatedTabs = "" +
		"\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t" +
		"\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t"
)

// Repeat 返回由字符串 s 重复 count 次组成的新字符串。
//
// 若 count 为负数，或 (len(s) * count) 结果溢出，该函数会触发 panic。
func Repeat(s string, count int) string {
	switch count {
	case 0:
		return ""
	case 1:
		return s
	}

	// 由于溢出时无法返回错误，
	// 若重复操作会导致溢出则必须触发 panic
	// 参见 golang.org/issue/16237
	if count < 0 {
		panic("strings: negative Repeat count")
	}
	hi, lo := bits.Mul(uint(len(s)), uint(count))
	if hi > 0 || lo > uint(maxInt) {
		panic("strings: Repeat output length overflow")
	}
	n := int(lo) // lo = len(s) * count

	if len(s) == 0 {
		return ""
	}

	// 优化较短长度的常用重复字符串
	switch s[0] {
	case ' ', '-', '0', '=', '\t':
		switch {
		case n <= len(repeatedSpaces) && HasPrefix(repeatedSpaces, s):
			return repeatedSpaces[:n]
		case n <= len(repeatedDashes) && HasPrefix(repeatedDashes, s):
			return repeatedDashes[:n]
		case n <= len(repeatedZeroes) && HasPrefix(repeatedZeroes, s):
			return repeatedZeroes[:n]
		case n <= len(repeatedEquals) && HasPrefix(repeatedEquals, s):
			return repeatedEquals[:n]
		case n <= len(repeatedTabs) && HasPrefix(repeatedTabs, s):
			return repeatedTabs[:n]
		}
	}

	// 当块大小超过特定值时，使用更大的块作为写入源会适得其反，
	// 因为源过大时会频繁刷新 CPU 数据缓存。
	// 因此，若结果长度超过经验值上限（8KB），
	// 则在达到上限后停止增长源字符串，复用该源字符串（常驻 L1 缓存）
	// 直至完成结果构建。
	// 这在结果长度较大（约超过 L2 缓存大小）的场景下
	// 可带来显著性能提升（最高 +100%）。
	const chunkLimit = 8 * 1024
	chunkMax := n
	if n > chunkLimit {
		chunkMax = chunkLimit / len(s) * len(s)
		if chunkMax == 0 {
			chunkMax = len(s)
		}
	}

	var b Builder
	b.Grow(n)
	b.WriteString(s)
	for b.Len() < n {
		chunk := min(n-b.Len(), b.Len(), chunkMax)
		b.WriteString(b.String()[:chunk])
	}
	return b.String()
}

// ToUpper 返回 s 的副本，其中所有 Unicode 字母均转换为大写。
func ToUpper(s string) string {
	isASCII, hasLower := true, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			isASCII = false
			break
		}
		hasLower = hasLower || ('a' <= c && c <= 'z')
	}

	if isASCII { // 优化纯 ASCII 字符串
		if !hasLower {
			return s
		}
		var (
			b   Builder
			pos int
		)
		b.Grow(len(s))
		for i := 0; i < len(s); i++ {
			c := s[i]
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
				if pos < i {
					b.WriteString(s[pos:i])
				}
				b.WriteByte(c)
				pos = i + 1
			}
		}
		if pos < len(s) {
			b.WriteString(s[pos:])
		}
		return b.String()
	}
	return Map(unicode.ToUpper, s)
}

// ToLower 返回 s 的副本，其中所有 Unicode 字母均转换为小写。
func ToLower(s string) string {
	isASCII, hasUpper := true, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			isASCII = false
			break
		}
		hasUpper = hasUpper || ('A' <= c && c <= 'Z')
	}

	if isASCII { // 优化纯 ASCII 字符串
		if !hasUpper {
			return s
		}
		var (
			b   Builder
			pos int
		)
		b.Grow(len(s))
		for i := 0; i < len(s); i++ {
			c := s[i]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
				if pos < i {
					b.WriteString(s[pos:i])
				}
				b.WriteByte(c)
				pos = i + 1
			}
		}
		if pos < len(s) {
			b.WriteString(s[pos:])
		}
		return b.String()
	}
	return Map(unicode.ToLower, s)
}

// ToTitle 返回 s 的副本，其中所有 Unicode 字母均转换为 Unicode 标题大小写。
func ToTitle(s string) string { return Map(unicode.ToTitle, s) }

// ToUpperSpecial 返回 s 的副本，使用 c 指定的大小写映射规则
// 将所有 Unicode 字母转换为大写。
func ToUpperSpecial(c unicode.SpecialCase, s string) string {
	return Map(c.ToUpper, s)
}

// ToLowerSpecial 返回 s 的副本，使用 c 指定的大小写映射规则
// 将所有 Unicode 字母转换为小写。
func ToLowerSpecial(c unicode.SpecialCase, s string) string {
	return Map(c.ToLower, s)
}

// ToTitleSpecial 返回 s 的副本，将所有 Unicode 字母转换为 Unicode 标题大小写，
// 优先应用特殊大小写规则。
func ToTitleSpecial(c unicode.SpecialCase, s string) string {
	return Map(c.ToTitle, s)
}

// ToValidUTF8 返回 s 的副本，其中每段无效 UTF-8 字节序列
// 均被替换为指定的替换字符串（替换字符串可为空）。
func ToValidUTF8(s, replacement string) string {
	var b Builder

	for i, c := range s {
		if c != utf8.RuneError {
			continue
		}

		_, wid := utf8.DecodeRuneInString(s[i:])
		if wid == 1 {
			b.Grow(len(s) + len(replacement))
			b.WriteString(s[:i])
			s = s[i:]
			break
		}
	}

	// 输入无变化的快速路径
	if b.Cap() == 0 { // 未调用上述 b.Grow
		return s
	}

	invalid := false // 前一个字节来自无效 UTF-8 序列
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			i++
			invalid = false
			b.WriteByte(c)
			continue
		}
		_, wid := utf8.DecodeRuneInString(s[i:])
		if wid == 1 {
			i++
			if !invalid {
				invalid = true
				b.WriteString(replacement)
			}
			continue
		}
		invalid = false
		b.WriteString(s[i : i+wid])
		i += wid
	}

	return b.String()
}

// isSeparator 判断该码点是否可作为单词边界。
// TODO: 当 unicode 包支持更多属性时进行更新。
func isSeparator(r rune) bool {
	// ASCII 字母数字和下划线不是分隔符
	if r <= 0x7F {
		switch {
		case '0' <= r && r <= '9':
			return false
		case 'a' <= r && r <= 'z':
			return false
		case 'A' <= r && r <= 'Z':
			return false
		case r == '_':
			return false
		}
		return true
	}
	// 字母和数字不是分隔符
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	// 目前仅能将空白字符视为分隔符
	return unicode.IsSpace(r)
}

// Title 返回 s 的副本，其中所有单词开头的 Unicode 字母
// 均转换为 Unicode 标题大小写。
//
// 已弃用：Title 使用的单词边界规则无法正确处理 Unicode 标点符号。
// 请改用 golang.org/x/text/cases。
func Title(s string) string {
	// 使用闭包保存状态
	// 实现巧妙且高效，依赖 Map 按顺序扫描并为每个码点调用一次闭包
	prev := ' '
	return Map(
		func(r rune) rune {
			if isSeparator(prev) {
				prev = r
				return unicode.ToTitle(r)
			}
			prev = r
			return r
		},
		s)
}

// TrimLeftFunc 返回字符串 s 的切片，移除所有开头满足 f(c) 的 Unicode 码点。
func TrimLeftFunc(s string, f func(rune) bool) string {
	i := indexFunc(s, f, false)
	if i == -1 {
		return ""
	}
	return s[i:]
}

// TrimRightFunc 返回字符串 s 的切片，移除所有末尾满足 f(c) 的 Unicode 码点。
func TrimRightFunc(s string, f func(rune) bool) string {
	i := lastIndexFunc(s, f, false)
	if i >= 0 {
		_, wid := utf8.DecodeRuneInString(s[i:])
		i += wid
	} else {
		i++
	}
	return s[0:i]
}

// TrimFunc 返回字符串 s 的切片，移除所有开头和末尾满足 f(c) 的 Unicode 码点。
func TrimFunc(s string, f func(rune) bool) string {
	return TrimRightFunc(TrimLeftFunc(s, f), f)
}

// IndexFunc 返回 s 中第一个满足 f(c) 的 Unicode 码点的索引，
// 若无则返回 -1。
func IndexFunc(s string, f func(rune) bool) int {
	return indexFunc(s, f, true)
}

// LastIndexFunc 返回 s 中最后一个满足 f(c) 的 Unicode 码点的索引，
// 若无则返回 -1。
func LastIndexFunc(s string, f func(rune) bool) int {
	return lastIndexFunc(s, f, true)
}

// indexFunc 与 IndexFunc 功能一致，
// 当 truth==false 时，断言函数的判断逻辑取反。
func indexFunc(s string, f func(rune) bool, truth bool) int {
	for i, r := range s {
		if f(r) == truth {
			return i
		}
	}
	return -1
}

// lastIndexFunc 与 LastIndexFunc 功能一致，
// 当 truth==false 时，断言函数的判断逻辑取反。
func lastIndexFunc(s string, f func(rune) bool, truth bool) int {
	for i := len(s); i > 0; {
		r, size := utf8.DecodeLastRuneInString(s[0:i])
		i -= size
		if f(r) == truth {
			return i
		}
	}
	return -1
}

// asciiSet 是一个 32 字节的值，每个比特位表示集合中是否存在对应 ASCII 字符。
// 低 16 字节的 128 个比特位（从最低字的最低有效位到最高字的最高有效位）
// 映射全部 128 个 ASCII 字符。高 16 字节的 128 个比特位置零，
// 确保所有非 ASCII 字符均被判定为不在集合中。
// 尽管上半部分未使用，仍分配 32 字节以避免 asciiSet.contains 中的边界检查。
type asciiSet [8]uint32

// makeASCIISet 创建 ASCII 字符集合，并报告 chars 中所有字符是否均为 ASCII。
func makeASCIISet(chars string) (as asciiSet, ok bool) {
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		if c >= utf8.RuneSelf {
			return as, false
		}
		as[c/32] |= 1 << (c % 32)
	}
	return as, true
}

// contains 判断 c 是否存在于集合中。
func (as *asciiSet) contains(c byte) bool {
	return (as[c/32] & (1 << (c % 32))) != 0
}

// Trim 返回字符串 s 的切片，移除所有开头和末尾包含在 cutset 中的 Unicode 码点。
func Trim(s, cutset string) string {
	if s == "" || cutset == "" {
		return s
	}
	if len(cutset) == 1 && cutset[0] < utf8.RuneSelf {
		return trimLeftByte(trimRightByte(s, cutset[0]), cutset[0])
	}
	if as, ok := makeASCIISet(cutset); ok {
		return trimLeftASCII(trimRightASCII(s, &as), &as)
	}
	return trimLeftUnicode(trimRightUnicode(s, cutset), cutset)
}

// TrimLeft 返回字符串 s 的切片，移除所有开头包含在 cutset 中的 Unicode 码点。
//
// 如需移除前缀，请改用 [TrimPrefix]。
func TrimLeft(s, cutset string) string {
	if s == "" || cutset == "" {
		return s
	}
	if len(cutset) == 1 && cutset[0] < utf8.RuneSelf {
		return trimLeftByte(s, cutset[0])
	}
	if as, ok := makeASCIISet(cutset); ok {
		return trimLeftASCII(s, &as)
	}
	return trimLeftUnicode(s, cutset)
}

func trimLeftByte(s string, c byte) string {
	for len(s) > 0 && s[0] == c {
		s = s[1:]
	}
	return s
}

func trimLeftASCII(s string, as *asciiSet) string {
	for len(s) > 0 {
		if !as.contains(s[0]) {
			break
		}
		s = s[1:]
	}
	return s
}

func trimLeftUnicode(s, cutset string) string {
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if !ContainsRune(cutset, r) {
			break
		}
		s = s[n:]
	}
	return s
}

// TrimRight 返回字符串 s 的切片，移除所有末尾包含在 cutset 中的 Unicode 码点。
//
// 如需移除后缀，请改用 [TrimSuffix]。
func TrimRight(s, cutset string) string {
	if s == "" || cutset == "" {
		return s
	}
	if len(cutset) == 1 && cutset[0] < utf8.RuneSelf {
		return trimRightByte(s, cutset[0])
	}
	if as, ok := makeASCIISet(cutset); ok {
		return trimRightASCII(s, &as)
	}
	return trimRightUnicode(s, cutset)
}

func trimRightByte(s string, c byte) string {
	for len(s) > 0 && s[len(s)-1] == c {
		s = s[:len(s)-1]
	}
	return s
}

func trimRightASCII(s string, as *asciiSet) string {
	for len(s) > 0 {
		if !as.contains(s[len(s)-1]) {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

func trimRightUnicode(s, cutset string) string {
	for len(s) > 0 {
		r, n := rune(s[len(s)-1]), 1
		if r >= utf8.RuneSelf {
			r, n = utf8.DecodeLastRuneInString(s)
		}
		if !ContainsRune(cutset, r) {
			break
		}
		s = s[:len(s)-n]
	}
	return s
}

// TrimSpace 返回字符串 s 的切片（子字符串），
// 移除所有 Unicode 定义的首尾空白字符。
func TrimSpace(s string) string {
	// ASCII 快速路径：查找第一个非 ASCII 空白字节
	for lo, c := range []byte(s) {
		if c >= utf8.RuneSelf {
			// 若遇到非 ASCII 字节，回退到兼容 Unicode 的慢速方法处理剩余字节
			return TrimFunc(s[lo:], unicode.IsSpace)
		}
		if asciiSpace[c] != 0 {
			continue
		}
		s = s[lo:]
		// 从末尾查找第一个非 ASCII 空白字节
		for hi := len(s) - 1; hi >= 0; hi-- {
			c := s[hi]
			if c >= utf8.RuneSelf {
				return TrimRightFunc(s[:hi+1], unicode.IsSpace)
			}
			if asciiSpace[c] == 0 {
				// 此时 s[:hi+1] 首尾均为 ASCII 非空白字节，处理完成
				// 非 ASCII 场景已在上方处理
				return s[:hi+1]
			}
		}
	}
	return ""
}

// TrimPrefix 返回移除了指定前缀字符串的 s。
// 若 s 不以 prefix 开头，则原样返回 s。
func TrimPrefix(s, prefix string) string {
	return stringslite.TrimPrefix(s, prefix)
}

// TrimSuffix 返回移除了指定后缀字符串的 s。
// 若 s 不以 suffix 结尾，则原样返回 s。
func TrimSuffix(s, suffix string) string {
	return stringslite.TrimSuffix(s, suffix)
}

// Replace 返回字符串 s 的副本，将前 n 个非重叠的 old 替换为 new。
// 若 old 为空，匹配字符串开头和每个 UTF-8 序列之后，
// 对于 k 个码点的字符串，最多进行 k+1 次替换。
// 若 n < 0，替换次数无限制。
func Replace(s, old, new string, n int) string {
	if old == new || n == 0 {
		return s // 避免内存分配
	}

	// 计算替换次数
	if m := Count(s, old); m == 0 {
		return s // 避免内存分配
	} else if n < 0 || m < n {
		n = m
	}

	// 向缓冲区应用替换
	var b Builder
	b.Grow(len(s) + n*(len(new)-len(old)))
	start := 0
	if len(old) > 0 {
		for range n {
			j := start + Index(s[start:], old)
			b.WriteString(s[start:j])
			b.WriteString(new)
			start = j + len(old)
		}
	} else { // len(old) == 0
		b.WriteString(new)
		for range n - 1 {
			_, wid := utf8.DecodeRuneInString(s[start:])
			j := start + wid
			b.WriteString(s[start:j])
			b.WriteString(new)
			start = j
		}
	}
	b.WriteString(s[start:])
	return b.String()
}

// ReplaceAll 返回字符串 s 的副本，将所有非重叠的 old 替换为 new。
// 若 old 为空，匹配字符串开头和每个 UTF-8 序列之后，
// 对于 k 个码点的字符串，最多进行 k+1 次替换。
func ReplaceAll(s, old, new string) string {
	return Replace(s, old, new, -1)
}

// EqualFold 判断 s 和 t（作为 UTF-8 字符串解析）
// 在简单 Unicode 大小写折叠规则下是否相等，这是一种更通用的不区分大小写比较方式。
func EqualFold(s, t string) bool {
	// ASCII 快速路径
	i := 0
	for n := min(len(s), len(t)); i < n; i++ {
		sr := s[i]
		tr := t[i]
		if sr|tr >= utf8.RuneSelf {
			goto hasUnicode
		}

		// 简单匹配
		if tr == sr {
			continue
		}

		// 令 sr < tr 简化后续逻辑
		if tr < sr {
			tr, sr = sr, tr
		}
		// 仅 ASCII，sr/tr 必须为大小写字母
		if 'A' <= sr && sr <= 'Z' && tr == sr+'a'-'A' {
			continue
		}
		return false
	}
	// 检查两个字符串是否均已遍历完毕
	return len(s) == len(t)

hasUnicode:
	s = s[i:]
	t = t[i:]
	for _, sr := range s {
		// 若 t 已遍历完毕，字符串不相等
		if len(t) == 0 {
			return false
		}

		// 提取第二个字符串的第一个码点
		tr, size := utf8.DecodeRuneInString(t)
		t = t[size:]

		// 匹配则继续，不匹配则返回 false

		// 简单匹配
		if tr == sr {
			continue
		}

		// 令 sr < tr 简化后续逻辑
		if tr < sr {
			tr, sr = sr, tr
		}
		// ASCII 快速检查
		if tr < utf8.RuneSelf {
			// 仅 ASCII，sr/tr 必须为大小写字母
			if 'A' <= sr && sr <= 'Z' && tr == sr+'a'-'A' {
				continue
			}
			return false
		}

		// 通用场景：SimpleFold(x) 返回下一个大于 x 的等效码点，
		// 或循环返回更小的值
		r := unicode.SimpleFold(sr)
		for r != sr && r < tr {
			r = unicode.SimpleFold(r)
		}
		if r == tr {
			continue
		}
		return false
	}

	// 第一个字符串为空，检查第二个字符串是否也为空
	return len(t) == 0
}

// Index 返回 substr 在 s 中第一次出现的索引，若不存在则返回 -1。
func Index(s, substr string) int {
	return stringslite.Index(s, substr)
}

// Cut 围绕 sep 的首次出现位置切分 s，
// 返回 sep 前后的文本。
// found 结果表示 sep 是否存在于 s 中。
// 若 sep 不存在于 s，cut 返回 s, "", false。
func Cut(s, sep string) (before, after string, found bool) {
	return stringslite.Cut(s, sep)
}

// CutPrefix 返回移除了指定前缀字符串的 s，
// 并报告是否找到该前缀。
// 若 s 不以 prefix 开头，CutPrefix 返回 s, false。
// 若 prefix 为空字符串，CutPrefix 返回 s, true。
func CutPrefix(s, prefix string) (after string, found bool) {
	return stringslite.CutPrefix(s, prefix)
}

// CutSuffix 返回移除了指定后缀字符串的 s，
// 并报告是否找到该后缀。
// 若 s 不以 suffix 结尾，CutSuffix 返回 s, false。
// 若 suffix 为空字符串，CutSuffix 返回 s, true。
func CutSuffix(s, suffix string) (before string, found bool) {
	return stringslite.CutSuffix(s, suffix)
}
