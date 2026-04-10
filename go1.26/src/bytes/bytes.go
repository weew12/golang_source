// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bytes 实现了用于操作字节切片的函数。
// 它的功能与 [strings] 包的工具类似。
package bytes

import (
	"internal/bytealg"
	"math/bits"
	"unicode"
	"unicode/utf8"
	_ "unsafe" // 用于 linkname
)

// Equal 判断 a 和 b
// 是否长度相同且包含的字节完全一致。
// nil 参数等价于空切片。
func Equal(a, b []byte) bool {
	// cmd/compile 和 gccgo 都不会为这些字符串转换分配内存。
	return string(a) == string(b)
}

// Compare 按字典序比较两个字节切片并返回一个整数。
// 若 a == b，结果为 0；若 a < b，结果为 -1；若 a > b，结果为 +1。
// nil 参数等价于空切片。
func Compare(a, b []byte) int {
	return bytealg.Compare(a, b)
}

// explode 将 s 拆分为 UTF-8 序列切片，每个 Unicode 码点对应一个切片（仍为字节切片），
// 最多生成 n 个字节切片。无效的 UTF-8 序列会被拆分为单个字节。
func explode(s []byte, n int) [][]byte {
	if n <= 0 || n > len(s) {
		n = len(s)
	}
	a := make([][]byte, n)
	var size int
	na := 0
	for len(s) > 0 {
		if na+1 >= n {
			a[na] = s
			na++
			break
		}
		_, size = utf8.DecodeRune(s)
		a[na] = s[0:size:size]
		s = s[size:]
		na++
	}
	return a[0:na]
}

// Count 统计 sep 在 s 中非重叠出现的次数。
// 若 sep 为空切片，Count 返回 1 + s 中 UTF-8 编码码点的数量。
func Count(s, sep []byte) int {
	// 特殊情况
	if len(sep) == 0 {
		return utf8.RuneCount(s) + 1
	}
	if len(sep) == 1 {
		return bytealg.Count(s, sep[0])
	}
	n := 0
	for {
		i := Index(s, sep)
		if i == -1 {
			return n
		}
		n++
		s = s[i+len(sep):]
	}
}

// Contains 判断子切片 subslice 是否存在于 b 中。
func Contains(b, subslice []byte) bool {
	return Index(b, subslice) != -1
}

// ContainsAny 判断 chars 中任意 UTF-8 编码的码点是否存在于 b 中。
func ContainsAny(b []byte, chars string) bool {
	return IndexAny(b, chars) >= 0
}

// ContainsRune 判断字符 r 是否包含在 UTF-8 编码的字节切片 b 中。
func ContainsRune(b []byte, r rune) bool {
	return IndexRune(b, r) >= 0
}

// ContainsFunc 判断 b 中任意 UTF-8 编码码点 r 是否满足 f(r)。
func ContainsFunc(b []byte, f func(rune) bool) bool {
	return IndexFunc(b, f) >= 0
}

// IndexByte 返回 c 在 b 中首次出现的索引，若 b 中不存在 c 则返回 -1。
func IndexByte(b []byte, c byte) int {
	return bytealg.IndexByte(b, c)
}

func indexBytePortable(s []byte, c byte) int {
	for i, b := range s {
		if b == c {
			return i
		}
	}
	return -1
}

// LastIndex 返回 sep 在 s 中最后一次出现的索引，若 s 中不存在 sep 则返回 -1。
func LastIndex(s, sep []byte) int {
	n := len(sep)
	switch {
	case n == 0:
		return len(s)
	case n == 1:
		return bytealg.LastIndexByte(s, sep[0])
	case n == len(s):
		if Equal(s, sep) {
			return 0
		}
		return -1
	case n > len(s):
		return -1
	}
	return bytealg.LastIndexRabinKarp(s, sep)
}

// LastIndexByte 返回 c 在 s 中最后一次出现的索引，若 s 中不存在 c 则返回 -1。
func LastIndexByte(s []byte, c byte) int {
	return bytealg.LastIndexByte(s, c)
}

// IndexRune 将 s 解析为 UTF-8 编码的码点序列。
// 它返回指定字符在 s 中首次出现的字节索引。
// 若 s 中不存在该字符则返回 -1。
// 若 r 为 [utf8.RuneError]，则返回任意无效 UTF-8 字节序列的首次出现位置。
func IndexRune(s []byte, r rune) int {
	const haveFastIndex = bytealg.MaxBruteForce > 0
	switch {
	case 0 <= r && r < utf8.RuneSelf:
		return IndexByte(s, byte(r))
	case r == utf8.RuneError:
		for i := 0; i < len(s); {
			r1, n := utf8.DecodeRune(s[i:])
			if r1 == utf8.RuneError {
				return i
			}
			i += n
		}
		return -1
	case !utf8.ValidRune(r):
		return -1
	default:
		// 使用字符 r UTF-8 编码形式的最后一个字节进行搜索。
		// 最后一个字节的分布比首字节更均匀，首字节有 78% 的概率为 [240, 243, 244]。
		var b [utf8.UTFMax]byte
		n := utf8.EncodeRune(b[:], r)
		last := n - 1
		i := last
		fails := 0
		for i < len(s) {
			if s[i] != b[last] {
				o := IndexByte(s[i+1:], b[last])
				if o < 0 {
					return -1
				}
				i += o + 1
			}
			// 向后逐字节比较
			for j := 1; j < n; j++ {
				if s[i-j] != b[last-j] {
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
		// 当 IndexByte 返回过多误报时，切换到 bytealg.Index（若可用）或暴力搜索
		if haveFastIndex {
			if j := bytealg.Index(s[i-last:], b[:n]); j >= 0 {
				return i + j - last
			}
		} else {
			// 若 bytealg.Index 不可用，暴力搜索比 Rabin-Karp 快约 1.5-3 倍（因 n 较小）
			c0 := b[last]
			c1 := b[last-1] // 至少有2个字符需要匹配
		loop:
			for ; i < len(s); i++ {
				if s[i] == c0 && s[i-1] == c1 {
					for k := 2; k < n; k++ {
						if s[i-k] != b[last-k] {
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

// IndexAny 将 s 解析为 UTF-8 编码的 Unicode 码点序列。
// 它返回 chars 中任意 Unicode 码点在 s 中首次出现的字节索引。
// 若 chars 为空或无共同码点则返回 -1。
func IndexAny(s []byte, chars string) int {
	if chars == "" {
		// 避免扫描整个 s
		return -1
	}
	if len(s) == 1 {
		r := rune(s[0])
		if r >= utf8.RuneSelf {
			// 搜索 utf8.RuneError
			for _, r = range chars {
				if r == utf8.RuneError {
					return 0
				}
			}
			return -1
		}
		if bytealg.IndexByteString(chars, s[0]) >= 0 {
			return 0
		}
		return -1
	}
	if len(chars) == 1 {
		r := rune(chars[0])
		if r >= utf8.RuneSelf {
			r = utf8.RuneError
		}
		return IndexRune(s, r)
	}
	if len(s) > 8 {
		if as, isASCII := makeASCIISet(chars); isASCII {
			for i, c := range s {
				if as.contains(c) {
					return i
				}
			}
			return -1
		}
	}
	var width int
	for i := 0; i < len(s); i += width {
		r := rune(s[i])
		if r < utf8.RuneSelf {
			if bytealg.IndexByteString(chars, s[i]) >= 0 {
				return i
			}
			width = 1
			continue
		}
		r, width = utf8.DecodeRune(s[i:])
		if r != utf8.RuneError {
			// r 占 2 到 4 个字节
			if len(chars) == width {
				if chars == string(r) {
					return i
				}
				continue
			}
			// 若可用，使用 bytealg.IndexString 提升性能
			if bytealg.MaxLen >= width {
				if bytealg.IndexString(chars, string(r)) >= 0 {
					return i
				}
				continue
			}
		}
		for _, ch := range chars {
			if r == ch {
				return i
			}
		}
	}
	return -1
}

// LastIndexAny 将 s 解析为 UTF-8 编码的 Unicode 码点序列。
// 它返回 chars 中任意 Unicode 码点在 s 中最后一次出现的字节索引。
// 若 chars 为空或无共同码点则返回 -1。
func LastIndexAny(s []byte, chars string) int {
	if chars == "" {
		// 避免扫描整个 s
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
	if len(s) == 1 {
		r := rune(s[0])
		if r >= utf8.RuneSelf {
			for _, r = range chars {
				if r == utf8.RuneError {
					return 0
				}
			}
			return -1
		}
		if bytealg.IndexByteString(chars, s[0]) >= 0 {
			return 0
		}
		return -1
	}
	if len(chars) == 1 {
		cr := rune(chars[0])
		if cr >= utf8.RuneSelf {
			cr = utf8.RuneError
		}
		for i := len(s); i > 0; {
			r, size := utf8.DecodeLastRune(s[:i])
			i -= size
			if r == cr {
				return i
			}
		}
		return -1
	}
	for i := len(s); i > 0; {
		r := rune(s[i-1])
		if r < utf8.RuneSelf {
			if bytealg.IndexByteString(chars, s[i-1]) >= 0 {
				return i - 1
			}
			i--
			continue
		}
		r, size := utf8.DecodeLastRune(s[:i])
		i -= size
		if r != utf8.RuneError {
			// r 占 2 到 4 个字节
			if len(chars) == size {
				if chars == string(r) {
					return i
				}
				continue
			}
			// 若可用，使用 bytealg.IndexString 提升性能
			if bytealg.MaxLen >= size {
				if bytealg.IndexString(chars, string(r)) >= 0 {
					return i
				}
				continue
			}
		}
		for _, ch := range chars {
			if r == ch {
				return i
			}
		}
	}
	return -1
}

// 通用分割：在 sep 每次出现后进行分割，
// 在子切片中保留 sep 的 sepSave 个字节
func genSplit(s, sep []byte, sepSave, n int) [][]byte {
	if n == 0 {
		return nil
	}
	if len(sep) == 0 {
		return explode(s, n)
	}
	if n < 0 {
		n = Count(s, sep) + 1
	}
	if n > len(s)+1 {
		n = len(s) + 1
	}

	a := make([][]byte, n)
	n--
	i := 0
	for i < n {
		m := Index(s, sep)
		if m < 0 {
			break
		}
		a[i] = s[: m+sepSave : m+sepSave]
		s = s[m+len(sep):]
		i++
	}
	a[i] = s
	return a[:i+1]
}

// SplitN 将 s 按 sep 分割为多个子切片，并返回分隔符之间的子切片切片。
// 若 sep 为空，SplitN 会在每个 UTF-8 序列后进行分割。
// 计数参数决定返回的子切片数量：
//   - n > 0: 最多返回 n 个子切片；最后一个子切片为未分割的剩余部分；
//   - n == 0: 返回 nil（零个子切片）；
//   - n < 0: 返回所有子切片。
//
// 如需围绕分隔符的第一个实例进行分割，参见 [Cut]。
func SplitN(s, sep []byte, n int) [][]byte { return genSplit(s, sep, 0, n) }

// SplitAfterN 在 sep 每次出现后将 s 分割为多个子切片，
// 并返回这些子切片的切片。
// 若 sep 为空，SplitAfterN 会在每个 UTF-8 序列后进行分割。
// 计数参数决定返回的子切片数量：
//   - n > 0: 最多返回 n 个子切片；最后一个子切片为未分割的剩余部分；
//   - n == 0: 返回 nil（零个子切片）；
//   - n < 0: 返回所有子切片。
func SplitAfterN(s, sep []byte, n int) [][]byte {
	return genSplit(s, sep, len(sep), n)
}

// Split 将 s 按 sep 全部分割为多个子切片，并返回分隔符之间的子切片切片。
// 若 sep 为空，Split 会在每个 UTF-8 序列后进行分割。
// 等价于计数为 -1 的 SplitN。
//
// 如需围绕分隔符的第一个实例进行分割，参见 [Cut]。
func Split(s, sep []byte) [][]byte { return genSplit(s, sep, 0, -1) }

// SplitAfter 在 sep 每次出现后将 s 全部分割为多个子切片，
// 并返回这些子切片的切片。
// 若 sep 为空，SplitAfter 会在每个 UTF-8 序列后进行分割。
// 等价于计数为 -1 的 SplitAfterN。
func SplitAfter(s, sep []byte) [][]byte {
	return genSplit(s, sep, len(sep), -1)
}

var asciiSpace = [256]uint8{'\t': 1, '\n': 1, '\v': 1, '\f': 1, '\r': 1, ' ': 1}

// Fields 将 s 解析为 UTF-8 编码的码点序列。
// 它以一个或多个连续空白字符（由 [unicode.IsSpace] 定义）为分隔分割切片 s，
// 返回 s 的子切片切片；若 s 仅包含空白字符则返回空切片。
// 返回切片的每个元素均非空。与 [Split] 不同，首尾的连续空白字符会被舍弃。
func Fields(s []byte) [][]byte {
	// 首先统计字段数量
	// 若 s 为 ASCII 则为精确计数，否则为近似值
	n := 0
	wasSpace := 1
	// setBits 用于跟踪 s 字节中置位的比特位
	setBits := uint8(0)
	for i := 0; i < len(s); i++ {
		r := s[i]
		setBits |= r
		isSpace := int(asciiSpace[r])
		n += wasSpace & ^isSpace
		wasSpace = isSpace
	}

	if setBits >= utf8.RuneSelf {
		// 输入切片中存在非 ASCII 字符
		return FieldsFunc(s, unicode.IsSpace)
	}

	// ASCII 快速路径
	a := make([][]byte, n)
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
		a[na] = s[fieldStart:i:i]
		na++
		i++
		// 跳过字段间的空白字符
		for i < len(s) && asciiSpace[s[i]] != 0 {
			i++
		}
		fieldStart = i
	}
	if fieldStart < len(s) { // 最后一个字段可能在文件末尾结束
		a[na] = s[fieldStart:len(s):len(s)]
	}
	return a
}

// FieldsFunc 将 s 解析为 UTF-8 编码的码点序列。
// 它以满足 f(c) 的连续码点为分隔分割切片 s，
// 返回 s 的子切片切片。若 s 中所有码点都满足 f(c) 或 len(s) == 0，返回空切片。
// 返回切片的每个元素均非空。与 [Split] 不同，首尾满足 f(c) 的连续码点会被舍弃。
//
// FieldsFunc 不保证调用 f(c) 的顺序，
// 并假定对于给定的 c，f 始终返回相同的值。
func FieldsFunc(s []byte, f func(rune) bool) [][]byte {
	// span 用于记录 s 的切片，格式为 s[start:end]
	// start 索引包含，end 索引不包含
	type span struct {
		start int
		end   int
	}
	spans := make([]span, 0, 32)

	// 查找字段的起始和结束索引
	// 分两次处理（而非立即切片并收集结果子串）效率显著更高，可能与缓存效应有关
	start := -1 // 若 >=0 则为有效 span 起始
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRune(s[i:])
		if f(r) {
			if start >= 0 {
				spans = append(spans, span{start, i})
				start = -1
			}
		} else {
			if start < 0 {
				start = i
			}
		}
		i += size
	}

	// 最后一个字段可能在文件末尾结束
	if start >= 0 {
		spans = append(spans, span{start, len(s)})
	}

	// 根据记录的字段索引创建子切片
	a := make([][]byte, len(spans))
	for i, span := range spans {
		a[i] = s[span.start:span.end:span.end]
	}

	return a
}

// Join 拼接 s 中的元素创建新的字节切片。分隔符 sep 会放置在结果切片的元素之间。
func Join(s [][]byte, sep []byte) []byte {
	if len(s) == 0 {
		return []byte{}
	}
	if len(s) == 1 {
		// 直接返回副本
		return append([]byte(nil), s[0]...)
	}

	var n int
	if len(sep) > 0 {
		if len(sep) >= maxInt/(len(s)-1) {
			panic("bytes: Join output length overflow")
		}
		n += len(sep) * (len(s) - 1)
	}
	for _, v := range s {
		if len(v) > maxInt-n {
			panic("bytes: Join output length overflow")
		}
		n += len(v)
	}

	b := bytealg.MakeNoZero(n)[:n:n]
	bp := copy(b, s[0])
	for _, v := range s[1:] {
		bp += copy(b[bp:], sep)
		bp += copy(b[bp:], v)
	}
	return b
}

// HasPrefix 判断字节切片 s 是否以 prefix 开头。
func HasPrefix(s, prefix []byte) bool {
	return len(s) >= len(prefix) && Equal(s[:len(prefix)], prefix)
}

// HasSuffix 判断字节切片 s 是否以 suffix 结尾。
func HasSuffix(s, suffix []byte) bool {
	return len(s) >= len(suffix) && Equal(s[len(s)-len(suffix):], suffix)
}

// Map 返回字节切片 s 的副本，其中所有字符根据映射函数进行修改。
// 若映射返回负值，该字符将从字节切片中移除且不替换。
// s 和输出中的字符均被解析为 UTF-8 编码码点。
func Map(mapping func(r rune) rune, s []byte) []byte {
	// 最坏情况下，切片映射后会扩容，处理起来较为麻烦
	// 但这种情况极少，我们直接默认正常处理；切片也可能自然缩小
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, wid := utf8.DecodeRune(s[i:])
		r = mapping(r)
		if r >= 0 {
			b = utf8.AppendRune(b, r)
		}
		i += wid
	}
	return b
}

// 尽管是导出符号，
// Repeat 仍被广泛使用的包通过 linkname 引用
// 典型的不当使用案例包括：
//   - gitee.com/quant1x/num
//
// 请勿删除或修改类型签名
// 参见 go.dev/issue/67401
//
// 注意：此注释不属于文档注释
//
//go:linkname Repeat

// Repeat 返回由 count 个 b 副本组成的新字节切片。
//
// 若 count 为负数或 (len(b) * count) 结果溢出，会触发 panic。
func Repeat(b []byte, count int) []byte {
	if count == 0 {
		return []byte{}
	}

	// 由于溢出时无法返回错误，
	// 若重复操作会导致溢出则必须触发 panic
	// 参见 golang.org/issue/16237
	if count < 0 {
		panic("bytes: negative Repeat count")
	}
	hi, lo := bits.Mul(uint(len(b)), uint(count))
	if hi > 0 || lo > uint(maxInt) {
		panic("bytes: Repeat output length overflow")
	}
	n := int(lo) // lo = len(b) * count

	if len(b) == 0 {
		return []byte{}
	}

	// 超过特定块大小后，使用更大的块作为写入源会适得其反
	// 当源过大时，本质上会频繁刷新 CPU 数据缓存
	// 因此若结果长度超过经验值上限（8KB），
	// 达到上限后停止扩大源字符串，复用同一源字符串
	// （使其常驻 L1 缓存）直至完成结果构建
	// 这在结果长度较大（约超过 L2 缓存大小）时可显著提速（最高 +100%）
	const chunkLimit = 8 * 1024
	chunkMax := n
	if chunkMax > chunkLimit {
		chunkMax = chunkLimit / len(b) * len(b)
		if chunkMax == 0 {
			chunkMax = len(b)
		}
	}
	nb := bytealg.MakeNoZero(n)[:n:n]
	bp := copy(nb, b)
	for bp < n {
		chunk := min(bp, chunkMax)
		bp += copy(nb[bp:], nb[:chunk])
	}
	return nb
}

// ToUpper 返回字节切片 s 的副本，其中所有 Unicode 字母转换为大写。
func ToUpper(s []byte) []byte {
	isASCII, hasLower := true, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			isASCII = false
			break
		}
		hasLower = hasLower || ('a' <= c && c <= 'z')
	}

	if isASCII { // 优化纯 ASCII 字节切片
		if !hasLower {
			// 直接返回副本
			return append([]byte(""), s...)
		}
		b := bytealg.MakeNoZero(len(s))[:len(s):len(s)]
		for i := 0; i < len(s); i++ {
			c := s[i]
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			b[i] = c
		}
		return b
	}
	return Map(unicode.ToUpper, s)
}

// ToLower 返回字节切片 s 的副本，其中所有 Unicode 字母转换为小写。
func ToLower(s []byte) []byte {
	isASCII, hasUpper := true, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			isASCII = false
			break
		}
		hasUpper = hasUpper || ('A' <= c && c <= 'Z')
	}

	if isASCII { // 优化纯 ASCII 字节切片
		if !hasUpper {
			return append([]byte(""), s...)
		}
		b := bytealg.MakeNoZero(len(s))[:len(s):len(s)]
		for i := 0; i < len(s); i++ {
			c := s[i]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			b[i] = c
		}
		return b
	}
	return Map(unicode.ToLower, s)
}

// ToTitle 将 s 视为 UTF-8 编码字节，返回副本并将所有 Unicode 字母转换为标题大小写。
func ToTitle(s []byte) []byte { return Map(unicode.ToTitle, s) }

// ToUpperSpecial 将 s 视为 UTF-8 编码字节，返回副本并将所有 Unicode 字母转换为大写，
// 优先遵循特殊大小写规则。
func ToUpperSpecial(c unicode.SpecialCase, s []byte) []byte {
	return Map(c.ToUpper, s)
}

// ToLowerSpecial 将 s 视为 UTF-8 编码字节，返回副本并将所有 Unicode 字母转换为小写，
// 优先遵循特殊大小写规则。
func ToLowerSpecial(c unicode.SpecialCase, s []byte) []byte {
	return Map(c.ToLower, s)
}

// ToTitleSpecial 将 s 视为 UTF-8 编码字节，返回副本并将所有 Unicode 字母转换为标题大小写，
// 优先遵循特殊大小写规则。
func ToTitleSpecial(c unicode.SpecialCase, s []byte) []byte {
	return Map(c.ToTitle, s)
}

// ToValidUTF8 将 s 视为 UTF-8 编码字节，返回副本并将每组表示无效 UTF-8 的字节替换为 replacement 字节（可为空）。
func ToValidUTF8(s, replacement []byte) []byte {
	b := make([]byte, 0, len(s)+len(replacement))
	invalid := false // 前一个字节来自无效 UTF-8 序列
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			i++
			invalid = false
			b = append(b, c)
			continue
		}
		_, wid := utf8.DecodeRune(s[i:])
		if wid == 1 {
			i++
			if !invalid {
				invalid = true
				b = append(b, replacement...)
			}
			continue
		}
		invalid = false
		b = append(b, s[i:i+wid]...)
		i += wid
	}
	return b
}

// isSeparator 判断字符是否可标记单词边界
// TODO: 当 unicode 包支持更多属性时更新
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

// Title 将 s 视为 UTF-8 编码字节，返回副本并将所有单词开头的 Unicode 字母转换为标题大小写。
//
// 已弃用：Title 使用的单词边界规则无法正确处理 Unicode 标点符号。
// 请改用 golang.org/x/text/cases。
func Title(s []byte) []byte {
	// 使用闭包保存状态
	// 技巧性强但高效；依赖 Map 按顺序扫描并为每个字符调用一次闭包
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

// TrimLeftFunc 将 s 视为 UTF-8 编码字节，切除所有满足 f(c) 的前导 UTF-8 码点，返回子切片。
func TrimLeftFunc(s []byte, f func(rune) bool) []byte {
	i := indexFunc(s, f, false)
	if i == -1 {
		return nil
	}
	return s[i:]
}

// TrimRightFunc 切除所有满足 f(c) 的尾部 UTF-8 码点，返回 s 的子切片。
func TrimRightFunc(s []byte, f func(rune) bool) []byte {
	i := lastIndexFunc(s, f, false)
	if i >= 0 && s[i] >= utf8.RuneSelf {
		_, wid := utf8.DecodeRune(s[i:])
		i += wid
	} else {
		i++
	}
	return s[0:i]
}

// TrimFunc 切除所有满足 f(c) 的前导和尾部 UTF-8 码点，返回 s 的子切片。
func TrimFunc(s []byte, f func(rune) bool) []byte {
	return TrimRightFunc(TrimLeftFunc(s, f), f)
}

// TrimPrefix 返回移除前导前缀后的 s。
// 若 s 不以 prefix 开头，直接返回原 s。
func TrimPrefix(s, prefix []byte) []byte {
	if HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

// TrimSuffix 返回移除尾部后缀后的 s。
// 若 s 不以 suffix 结尾，直接返回原 s。
func TrimSuffix(s, suffix []byte) []byte {
	if HasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// IndexFunc 将 s 解析为 UTF-8 编码的码点序列。
// 它返回 s 中第一个满足 f(c) 的 Unicode 码点的字节索引，若无则返回 -1。
func IndexFunc(s []byte, f func(r rune) bool) int {
	return indexFunc(s, f, true)
}

// LastIndexFunc 将 s 解析为 UTF-8 编码的码点序列。
// 它返回 s 中最后一个满足 f(c) 的 Unicode 码点的字节索引，若无则返回 -1。
func LastIndexFunc(s []byte, f func(rune) bool) int {
	return lastIndexFunc(s, f, true)
}

// indexFunc 与 IndexFunc 功能一致，
// 区别在于当 truth==false 时，断言函数的判断逻辑取反。
func indexFunc(s []byte, f func(r rune) bool, truth bool) int {
	start := 0
	for start < len(s) {
		r, wid := utf8.DecodeRune(s[start:])
		if f(r) == truth {
			return start
		}
		start += wid
	}
	return -1
}

// lastIndexFunc 与 LastIndexFunc 功能一致，
// 区别在于当 truth==false 时，断言函数的判断逻辑取反。
func lastIndexFunc(s []byte, f func(r rune) bool, truth bool) int {
	for i := len(s); i > 0; {
		r, size := rune(s[i-1]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeLastRune(s[0:i])
		}
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
// 确保所有非 ASCII 字符都判定为不在集合中。
// 尽管上半部分未使用，仍分配 32 字节以避免 asciiSet.contains 中的边界检查。
type asciiSet [8]uint32

// makeASCIISet 创建 ASCII 字符集，并报告 chars 中所有字符是否均为 ASCII。
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

// contains 判断 c 是否在集合中。
func (as *asciiSet) contains(c byte) bool {
	return (as[c/32] & (1 << (c % 32))) != 0
}

// containsRune 是 strings.ContainsRune 的简化版本，
// 避免导入 strings 包。
// 避免使用 bytes.ContainsRune 以防止分配 s 的临时副本。
func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// Trim 切除 cutset 中包含的所有前导和尾部 UTF-8 码点，返回 s 的子切片。
func Trim(s []byte, cutset string) []byte {
	if len(s) == 0 {
		// 保持历史行为
		return nil
	}
	if cutset == "" {
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

// TrimLeft 切除 cutset 中包含的所有前导 UTF-8 码点，返回 s 的子切片。
func TrimLeft(s []byte, cutset string) []byte {
	if len(s) == 0 {
		// 保持历史行为
		return nil
	}
	if cutset == "" {
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

func trimLeftByte(s []byte, c byte) []byte {
	for len(s) > 0 && s[0] == c {
		s = s[1:]
	}
	if len(s) == 0 {
		// 保持历史行为
		return nil
	}
	return s
}

func trimLeftASCII(s []byte, as *asciiSet) []byte {
	for len(s) > 0 {
		if !as.contains(s[0]) {
			break
		}
		s = s[1:]
	}
	if len(s) == 0 {
		// 保持历史行为
		return nil
	}
	return s
}

func trimLeftUnicode(s []byte, cutset string) []byte {
	for len(s) > 0 {
		r, n := utf8.DecodeRune(s)
		if !containsRune(cutset, r) {
			break
		}
		s = s[n:]
	}
	if len(s) == 0 {
		// 保持历史行为
		return nil
	}
	return s
}

// TrimRight 切除 cutset 中包含的所有尾部 UTF-8 码点，返回 s 的子切片。
func TrimRight(s []byte, cutset string) []byte {
	if len(s) == 0 || cutset == "" {
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

func trimRightByte(s []byte, c byte) []byte {
	for len(s) > 0 && s[len(s)-1] == c {
		s = s[:len(s)-1]
	}
	return s
}

func trimRightASCII(s []byte, as *asciiSet) []byte {
	for len(s) > 0 {
		if !as.contains(s[len(s)-1]) {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

func trimRightUnicode(s []byte, cutset string) []byte {
	for len(s) > 0 {
		r, n := rune(s[len(s)-1]), 1
		if r >= utf8.RuneSelf {
			r, n = utf8.DecodeLastRune(s)
		}
		if !containsRune(cutset, r) {
			break
		}
		s = s[:len(s)-n]
	}
	return s
}

// TrimSpace 切除所有 Unicode 定义的前导和尾部空白字符，返回 s 的子切片。
func TrimSpace(s []byte) []byte {
	// ASCII 快速路径：查找第一个非空白 ASCII 字节
	for lo, c := range s {
		if c >= utf8.RuneSelf {
			// 遇到非 ASCII 字节，对剩余字节回退到支持 Unicode 的慢速方法
			return TrimFunc(s[lo:], unicode.IsSpace)
		}
		if asciiSpace[c] != 0 {
			continue
		}
		s = s[lo:]
		// 从末尾查找第一个非空白 ASCII 字节
		for hi := len(s) - 1; hi >= 0; hi-- {
			c := s[hi]
			if c >= utf8.RuneSelf {
				return TrimFunc(s[:hi+1], unicode.IsSpace)
			}
			if asciiSpace[c] == 0 {
				// 此时 s[:hi+1] 首尾均为非空白 ASCII 字节，处理完成
				// 非 ASCII 情况已在上方处理
				return s[:hi+1]
			}
		}
	}
	// 特殊情况保持 TrimLeftFunc 历史行为，全空白时返回 nil 而非空切片
	return nil
}

// Runes 将 s 解析为 UTF-8 编码的码点序列。
// 返回与 s 等效的字符（Unicode 码点）切片。
func Runes(s []byte) []rune {
	t := make([]rune, utf8.RuneCount(s))
	i := 0
	for len(s) > 0 {
		r, l := utf8.DecodeRune(s)
		t[i] = r
		i++
		s = s[l:]
	}
	return t
}

// Replace 返回切片 s 的副本，将前 n 个非重叠的 old 替换为 new。
// 若 old 为空，匹配切片开头和每个 UTF-8 序列之后，
// 对于 k 个码点的切片最多进行 k+1 次替换。
// 若 n < 0，替换次数无限制。
func Replace(s, old, new []byte, n int) []byte {
	m := 0
	if n != 0 {
		// 计算替换次数
		m = Count(s, old)
	}
	if m == 0 {
		// 直接返回副本
		return append([]byte(nil), s...)
	}
	if n < 0 || m < n {
		n = m
	}

	// 对缓冲区应用替换
	t := make([]byte, len(s)+n*(len(new)-len(old)))
	w := 0
	start := 0
	if len(old) > 0 {
		for range n {
			j := start + Index(s[start:], old)
			w += copy(t[w:], s[start:j])
			w += copy(t[w:], new)
			start = j + len(old)
		}
	} else { // len(old) == 0
		w += copy(t[w:], new)
		for range n - 1 {
			_, wid := utf8.DecodeRune(s[start:])
			j := start + wid
			w += copy(t[w:], s[start:j])
			w += copy(t[w:], new)
			start = j
		}
	}
	w += copy(t[w:], s[start:])
	return t[0:w]
}

// ReplaceAll 返回切片 s 的副本，将所有非重叠的 old 替换为 new。
// 若 old 为空，匹配切片开头和每个 UTF-8 序列之后，
// 对于 k 个码点的切片最多进行 k+1 次替换。
func ReplaceAll(s, old, new []byte) []byte {
	return Replace(s, old, new, -1)
}

// EqualFold 判断解析为 UTF-8 字符串的 s 和 t，
// 在简单 Unicode 大小写折叠规则下是否相等（更通用的不区分大小写比较）。
func EqualFold(s, t []byte) bool {
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

		// 使 sr < tr 简化后续逻辑
		if tr < sr {
			tr, sr = sr, tr
		}
		// 仅 ASCII，sr/tr 必须为大小写字母
		if 'A' <= sr && sr <= 'Z' && tr == sr+'a'-'A' {
			continue
		}
		return false
	}
	// 检查是否已遍历完两个字符串
	return len(s) == len(t)

hasUnicode:
	s = s[i:]
	t = t[i:]
	for len(s) != 0 && len(t) != 0 {
		// 提取每个字符串的第一个字符
		sr, size := utf8.DecodeRune(s)
		s = s[size:]
		tr, size := utf8.DecodeRune(t)
		t = t[size:]

		// 匹配则继续，不匹配返回 false

		// 简单匹配
		if tr == sr {
			continue
		}

		// 使 sr < tr 简化后续逻辑
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

		// 通用情况：SimpleFold(x) 返回下一个大于 x 的等效字符，或回绕为更小值
		r := unicode.SimpleFold(sr)
		for r != sr && r < tr {
			r = unicode.SimpleFold(r)
		}
		if r == tr {
			continue
		}
		return false
	}

	// 一个字符串为空，检查是否两者均为空
	return len(s) == len(t)
}

// Index 返回 sep 在 s 中首次出现的索引，若 s 中不存在 sep 则返回 -1。
func Index(s, sep []byte) int {
	n := len(sep)
	switch {
	case n == 0:
		return 0
	case n == 1:
		return IndexByte(s, sep[0])
	case n == len(s):
		if Equal(sep, s) {
			return 0
		}
		return -1
	case n > len(s):
		return -1
	case n <= bytealg.MaxLen:
		// s 和 sep 均较小时使用暴力搜索
		if len(s) <= bytealg.MaxBruteForce {
			return bytealg.Index(s, sep)
		}
		c0 := sep[0]
		c1 := sep[1]
		i := 0
		t := len(s) - n + 1
		fails := 0
		for i < t {
			if s[i] != c0 {
				// IndexByte 比 bytealg.Index 更快，无大量误报时优先使用
				o := IndexByte(s[i+1:t], c0)
				if o < 0 {
					return -1
				}
				i += o + 1
			}
			if s[i+1] == c1 && Equal(s[i:i+n], sep) {
				return i
			}
			fails++
			i++
			// IndexByte 产生过多误报时切换到 bytealg.Index
			if fails > bytealg.Cutover(i) {
				r := bytealg.Index(s[i:], sep)
				if r >= 0 {
					return r + i
				}
				return -1
			}
		}
		return -1
	}
	c0 := sep[0]
	c1 := sep[1]
	i := 0
	fails := 0
	t := len(s) - n + 1
	for i < t {
		if s[i] != c0 {
			o := IndexByte(s[i+1:t], c0)
			if o < 0 {
				break
			}
			i += o + 1
		}
		if s[i+1] == c1 && Equal(s[i:i+n], sep) {
			return i
		}
		i++
		fails++
		if fails >= 4+i>>4 && i < t {
			// 放弃 IndexByte，其跳过距离不足，效率低于 Rabin-Karp
			// 实验（IndexPeriodic）表明切换点约为 16 字节跳过距离
			// TODO: 若 sep 大前缀匹配，应在更大平均跳过距离时切换，因 Equal 开销更高
			// 本代码未考虑该影响
			j := bytealg.IndexRabinKarp(s[i:], sep)
			if j < 0 {
				return -1
			}
			return i + j
		}
	}
	return -1
}

// Cut 围绕 sep 的第一个实例分割 s，
// 返回 sep 前后的文本。found 结果表示 sep 是否出现在 s 中。
// 若 s 中无 sep，Cut 返回 s、nil、false。
//
// Cut 返回原切片 s 的子切片，非副本。
func Cut(s, sep []byte) (before, after []byte, found bool) {
	if i := Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, nil, false
}

// Clone 返回 b[:len(b)] 的副本。
// 结果可能包含额外未使用的容量。
// Clone(nil) 返回 nil。
func Clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte{}, b...)
}

// CutPrefix 返回移除前导前缀字节切片后的 s，
// 并报告是否找到前缀。
// 若 s 不以 prefix 开头，CutPrefix 返回 s、false。
// 若 prefix 为空字节切片，CutPrefix 返回 s、true。
//
// CutPrefix 返回原切片 s 的子切片，非副本。
func CutPrefix(s, prefix []byte) (after []byte, found bool) {
	if !HasPrefix(s, prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

// CutSuffix 返回移除尾部后缀字节切片后的 s，
// 并报告是否找到后缀。
// 若 s 不以 suffix 结尾，CutSuffix 返回 s、false。
// 若 suffix 为空字节切片，CutSuffix 返回 s、true。
//
// CutSuffix 返回原切片 s 的子切片，非副本。
func CutSuffix(s, suffix []byte) (before []byte, found bool) {
	if !HasSuffix(s, suffix) {
		return s, false
	}
	return s[:len(s)-len(suffix)], true
}
