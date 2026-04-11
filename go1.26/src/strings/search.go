// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strings

// stringFinder 用于在源文本中高效查找字符串。它采用 Boyer-Moore 字符串搜索算法实现：
// https://en.wikipedia.org/wiki/Boyer-Moore_string_search_algorithm
// https://www.cs.utexas.edu/~moore/publications/fstrpos.pdf（注意：这份早期文档使用的是从 1 开始的索引）
type stringFinder struct {
	// pattern 是我们要在文本中查找的字符串。
	pattern string

	// badCharSkip[b] 存储 pattern 最后一个字节与 pattern 中 b 最右侧出现位置之间的距离。若 b 不在 pattern 中，
	// badCharSkip[b] 的值为 len(pattern)。
	//
	// 当在文本中发现字节 b 不匹配时，我们可以安全地将匹配窗口至少移动 badCharSkip[b]，直到下一次可能匹配的字符对齐。
	badCharSkip [256]int

	// goodSuffixSkip[i] 定义了当后缀 pattern[i+1:] 匹配但字节 pattern[i] 不匹配时，我们可以将匹配窗口移动的距离。需考虑两种情况：
	//
	// 1. 匹配的后缀在 pattern 中其他位置出现（其前导字节不同，我们可能匹配该字节）。此时，我们可以移动匹配窗口以对齐下一个后缀块。
	// 例如，pattern "mississi" 的后缀 "issi" 下一次出现（从右到左顺序）在索引 1 处，因此 goodSuffixSkip[3] == 偏移量+len(后缀) == 3+4 == 7。
	//
	// 2. 若匹配的后缀未在 pattern 中其他位置出现，则匹配窗口的前缀可能与匹配后缀的末尾部分重叠。此时，goodSuffixSkip[i] 存储将窗口移动以使此前缀部分与后缀对齐所需的距离。
	// 例如，在 pattern "abcxxxabc" 中，当从后往前首次在位置 3 发现不匹配时，匹配的后缀 "xxabc" 未在 pattern 中其他位置出现。
	// 不过，其最右侧的 "abc"（位于位置 6）是整个 pattern 的前缀，因此 goodSuffixSkip[3] == 偏移量+len(后缀) == 6+5 == 11。
	goodSuffixSkip []int
}

func makeStringFinder(pattern string) *stringFinder {
	f := &stringFinder{
		pattern:        pattern,
		goodSuffixSkip: make([]int, len(pattern)),
	}
	// last 是 pattern 中最后一个字符的索引。
	last := len(pattern) - 1

	// 构建坏字符表。
	// 不在 pattern 中的字节可以跳过一个 pattern 的长度。
	for i := range f.badCharSkip {
		f.badCharSkip[i] = len(pattern)
	}
	// 循环条件使用 < 而非 <=，以确保最后一个字节到自身的距离不为零。发现该字节位置不匹配意味着它不在最后一个位置。
	for i := 0; i < last; i++ {
		f.badCharSkip[pattern[i]] = last - i
	}

	// 构建好后缀表。
	// 第一轮：将每个值设置为 pattern 前缀起始的下一个索引。
	lastPrefix := last
	for i := last; i >= 0; i-- {
		if HasPrefix(pattern, pattern[i+1:]) {
			lastPrefix = i + 1
		}
		// lastPrefix 是偏移量，(last-i) 是后缀长度。
		f.goodSuffixSkip[i] = lastPrefix + last - i
	}
	// 第二轮：从前往后查找 pattern 后缀的重复出现。
	for i := 0; i < last; i++ {
		lenSuffix := longestCommonSuffix(pattern, pattern[1:i+1])
		if pattern[i-lenSuffix] != pattern[last-lenSuffix] {
			// (last-i) 是偏移量，lenSuffix 是后缀长度。
			f.goodSuffixSkip[last-lenSuffix] = lenSuffix + last - i
		}
	}

	return f
}

func longestCommonSuffix(a, b string) (i int) {
	for ; i < len(a) && i < len(b); i++ {
		if a[len(a)-1-i] != b[len(b)-1-i] {
			break
		}
	}
	return
}

// next 返回 pattern 在 text 中首次出现的索引。若未找到 pattern，则返回 -1。
func (f *stringFinder) next(text string) int {
	i := len(f.pattern) - 1
	for i < len(text) {
		// 从末尾开始反向比较，直到第一个不匹配的字符。
		j := len(f.pattern) - 1
		for j >= 0 && text[i] == f.pattern[j] {
			i--
			j--
		}
		if j < 0 {
			return i + 1 // 匹配
		}
		i += max(f.badCharSkip[text[i]], f.goodSuffixSkip[j])
	}
	return -1
}
