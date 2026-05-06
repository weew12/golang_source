// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 通过诱导排序（SAIS）构建后缀数组。
// 参见 Ge Nong、Sen Zhang 和 Wai Hong Chen，
// "Two Efficient Algorithms for Linear Time Suffix Array Construction"，
// 特别是第 3 节（https://ieeexplore.ieee.org/document/5582081）。
// 另请参见 http://zork.net/~st/jottings/sais.html。
//
// 受 Yuta Mori 的 sais-lite 启发的优化
// (https://sites.google.com/site/yuta256/sais)。
//
// 以及其他新的优化。

// 这些函数中的许多都按照它们操作的类型大小进行参数化。
// 生成器 gen.go 复制这些函数以用于其他大小。
// 具体来说：
//
// - 以 _8_32 结尾的函数接受 []byte 和 []int32 参数，
//   并被复制为 _32_32、_8_64 和 _64_64 形式。
//   _32_32 和 _64_64_ 后缀被缩短为纯 _32 和 _64。
//   在创建 _32_32 和 _64_64 形式时，
//   函数体中包含 "byte-only" 或 "256" 的行会被剥离。
//   （这些行通常是 8 位特定的优化。）
//
// - 仅以 _32 结尾的函数操作 []int32，
//   并被复制为 _64 形式。（请注意，它仍然可能接受 []byte，
//   但不需要一个将 []byte 扩展为完整整数数组的版本函数。）

// 此代码的整体运行时间与输入大小成线性关系：
// 它运行一系列线性 passes 将问题缩小到最多一半大小的子问题，
// 递归调用自身，然后运行一系列线性 passes 将
// 子问题的答案转换为原始问题的答案。
// 这给出 T(N) = O(N) + T(N/2) = O(N) + O(N/2) + O(N/4) + ... = O(N)。
//
// 代码大纲，标出了通过 O(N) 大小数组的正向和反向扫描：

//	sais_I_N
//	placeLMS_I_B
//		bucketMax_I_B
//			freq_I_B
//				<scan +text> (1)
//			<scan +freq> (2)
//		<scan -text, random bucket> (3)
//	induceSubL_I_B
//		bucketMin_I_B
//			freq_I_B
//				<scan +text, often optimized away> (4)
//			<scan +freq> (5)
//		<scan +sa, random text, random bucket> (6)
//	induceSubS_I_B
//		bucketMax_I_B
//			freq_I_B
//				<scan +text, often optimized away> (7)
//			<scan +freq> (8)
//		<scan -sa, random text, random bucket> (9)
//	assignID_I_B
//		<scan +sa, random text substrings> (10)
//	map_B
//		<scan -sa> (11)
//	recurse_B
//		(递归调用 sais_B_B 处理最多为输入大小 1/2 的子问题，通常更小)
//	unmap_I_B
//		<scan -text> (12)
//		<scan +sa> (13)
//	expand_I_B
//		bucketMax_I_B
//			freq_I_B
//				<scan +text, often optimized away> (14)
//			<scan +freq> (15)
//		<scan -sa, random text, random bucket> (16)
//	induceL_I_B
//		bucketMin_I_B
//			freq_I_B
//				<scan +text, often optimized away> (17)
//			<scan +freq> (18)
//		<scan +sa, random text, random bucket> (19)
//	induceS_I_B
//		bucketMax_I_B
//			freq_I_B
//				<scan +text, often optimized away> (20)
//			<scan +freq> (21)
//		<scan -sa, random text, random bucket> (22)
//
// 这里，_B 表示后缀数组大小（_32 或 _64），_I 表示输入大小（_8 或 _B）。
//
// 大纲显示，对于给定的递归级别，通常有 22 次通过 O(N) 大小数组的扫描。
// 在顶层，操作 8 位输入文本时，
// 六个 freq 扫描是固定大小（256）而不是可能与输入大小相同。
// 此外，只要有空就会对频率进行一次计数并缓存
// （通常几乎总是有空间，在顶层始终有空间），
// 这消除了除第一个 freq_I_B 文本扫描之外的所有扫描（即 6 个中的 5 个）。
// 因此，递归的顶层只进行 22 - 6 - 5 = 11 次输入大小的扫描，
// 典型级别进行 16 次扫描。
//
// 线性扫描的成本远不及在少数扫描中
// 对文本进行的随机访问（特别是上面标记的 #6、#9、#16、#19、#22）。
// 在真实文本中，访问有一些局部性，尽管不多，
// 这是由于文本的重复结构
// （这也是 Burrows-Wheeler 压缩如此有效的原因）。
// 对于随机输入，没有局部性，这使得这些访问更加昂贵，
// 特别是当文本不再适合缓存时。
// 例如，在 50 MB 的 Go 源代码上运行，
// induceSubL_8_32（仅在递归顶层运行一次）需要 0.44 秒，
// 而在 50 MB 的随机输入上需要 2.55 秒。
// 几乎所有的相对减速都可以用文本访问来解释：
//
//		c0, c1 := text[k-1], text[k]
//
// 该行在 Go 文本上运行 0.23 秒，在随机文本上运行 2.02 秒。

//go:generate go run gen.go

package suffixarray

// text_32 返回输入文本的后缀数组。
// 它要求 len(text) 能容纳在 int32 中，
// 并且调用者已将 sa 清零。
func text_32(text []byte, sa []int32) {
	if int(int32(len(text))) != len(text) || len(text) != len(sa) {
		panic("suffixarray: misuse of text_32")
	}
	sais_8_32(text, 256, sa, make([]int32, 2*256))
}

// sais_8_32 计算 text 的后缀数组。
// text 必须只包含 [0, textMax) 范围内的值。
// 后缀数组存储在 sa 中，调用者必须确保已将其清零。
// 调用者还必须提供临时空间 tmp，
// 其中 len(tmp) ≥ textMax。如果 len(tmp) ≥ 2*textMax，
// 则算法运行得更快一些。
// 如果 sais_8_32 修改了 tmp，它会在返回时设置 tmp[0] = -1。
func sais_8_32(text []byte, textMax int, sa, tmp []int32) {
	if len(sa) != len(text) || len(tmp) < textMax {
		panic("suffixarray: misuse of sais_8_32")
	}

	// 简单的基本情况。排序 0 或 1 个元素很容易。
	if len(text) == 0 {
		return
	}
	if len(text) == 1 {
		sa[0] = 0
		return
	}

	// 建立由文本字符索引的切片，
	// 持有字符频率和桶排序偏移量。
	// 如果 tmp 只够一个切片使用，
	// 我们将它作为桶偏移量，并在每次需要时重新计算字符频率。
	var freq, bucket []int32
	if len(tmp) >= 2*textMax {
		freq, bucket = tmp[:textMax], tmp[textMax:2*textMax]
		freq[0] = -1 // 标记为未初始化
	} else {
		freq, bucket = nil, tmp[:textMax]
	}

	// SAIS 算法。
	// 每个调用都通过对 sa 的一次扫描实现。
	// 有关每个函数在算法中的作用，请参见各个函数的文档。
	numLMS := placeLMS_8_32(text, sa, freq, bucket)
	if numLMS <= 1 {
		// 0 或 1 个元素已排序。什么都不做。
	} else {
		induceSubL_8_32(text, sa, freq, bucket)
		induceSubS_8_32(text, sa, freq, bucket)
		length_8_32(text, sa, numLMS)
		maxID := assignID_8_32(text, sa, numLMS)
		if maxID < numLMS {
			map_32(sa, numLMS)
			recurse_32(sa, tmp, numLMS, maxID)
			unmap_8_32(text, sa, numLMS)
		} else {
			// 如果 maxID == numLMS，则每个 LMS 子串都是唯一的，
			// 所以两个 LMS 后缀的相对顺序由前导 LMS 子串决定。
			// 也就是说，LMS 后缀排序顺序与（更简单的）LMS 子串排序顺序匹配。
			// 将原始 LMS 子串顺序复制到后缀数组目标位置。
			copy(sa, sa[len(sa)-numLMS:])
		}
		expand_8_32(text, freq, bucket, sa, numLMS)
	}
	induceL_8_32(text, sa, freq, bucket)
	induceS_8_32(text, sa, freq, bucket)

	// 标记给调用者我们覆盖了 tmp。
	tmp[0] = -1
}

// freq_8_32 返回 text 的字符频率，
	// 作为由字符值索引的切片。
	// 如果 freq 为 nil，freq_8_32 使用并返回 bucket。
	// 如果 freq 非 nil，freq_8_32 假设 freq[0] >= 0
	// 表示频率已经计算过。
	// 如果频率数据被覆盖或未初始化，
	// 调用者必须设置 freq[0] = -1 以强制下次需要时重新计算。
func freq_8_32(text []byte, freq, bucket []int32) []int32 {
	if freq != nil && freq[0] >= 0 {
		return freq // 已计算
	}
	if freq == nil {
		freq = bucket
	}

	freq = freq[:256] // 消除下面 freq[c] 的边界检查
	clear(freq)
	for _, c := range text {
		freq[c]++
	}
	return freq
}

// bucketMin_8_32 将 text 桶排序中字符 c 的桶的最小索引存储到 bucket[c] 中。
func bucketMin_8_32(text []byte, freq, bucket []int32) {
	freq = freq_8_32(text, freq, bucket)
	freq = freq[:256]     // 确定 len(freq) = 256，所以下面 0 ≤ i < 256
	bucket = bucket[:256] // 消除下面 bucket[i] 的边界检查
	total := int32(0)
	for i, n := range freq {
		bucket[i] = total
		total += n
	}
}

// bucketMax_8_32 将 text 桶排序中字符 c 的桶的最大索引存储到 bucket[c] 中。
// c 的桶索引范围为 [min, max)。
// 也就是说，max 是该桶中最后一个索引之后的位置。
func bucketMax_8_32(text []byte, freq, bucket []int32) {
	freq = freq_8_32(text, freq, bucket)
	freq = freq[:256]     // 确定 len(freq) = 256，所以下面 0 ≤ i < 256
	bucket = bucket[:256] // 消除下面 bucket[i] 的边界检查
	total := int32(0)
	for i, n := range freq {
		total += n
		bucket[i] = total
	}
}

// SAIS 算法通过一系列对 sa 的扫描来执行。
// 以下每个函数都实现一次扫描，
// 这些函数在此按算法执行顺序排列。

// placeLMS_8_32 将 text 中 LMS 子串的最终字符的索引放入 sa，
// 按后缀数组中其正确桶的最右端排序。
//
// 文本末尾的虚拟哨兵字符是最终 LMS 子串的最终字符，
// 但虚拟哨兵字符没有桶，
// 因为它的值小于任何真实字符。
// 调用者必须因此假定 sa[-1] == len(text)。
//
// LMS 子串字符的文本索引始终 ≥ 1
// （第一个 LMS 子串前面必须有一个或多个 L 型字符，不属于任何 LMS 子串），
// 因此使用 0 作为"不存在"的后缀数组条目是安全的，
// 在此函数和大多数后续函数中都是如此
//（直到下面的 induceL_8_32）。
func placeLMS_8_32(text []byte, sa, freq, bucket []int32) int {
	bucketMax_8_32(text, freq, bucket)

	numLMS := 0
	lastB := int32(-1)
	bucket = bucket[:256] // eliminate bounds check for bucket[c1] below

	// 接下来的这段代码（直到空行）向后遍历 text，
	// 在每个满足 text[i] 是 L 型字符而 text[i+1] 是 S 型字符的位置 i 处
	// 执行代码体。
	// 也就是说，i+1 是 LMS 子串的起始位置。
	// 这些代码可以提取到一个带有回调函数的函数中，
	// 但会以显著的速度为代价。相反，我们只是在这个源文件中
	// 多次编写这七行代码。下面的副本参考了
	// 这个原始模式，称之为"LMS 子串迭代器"。
	//
	// 在每次遍历 text 的扫描中，c0、c1 是 text 中连续的字符。
	// 在这个后向扫描中，c0 == text[i] 而 c1 == text[i+1]。
	// 通过后向扫描，我们可以根据以下通常的定义
	// 跟踪当前位置是 S 型还是 L 型：
	//
	//	- 位置 len(text) 是 S 型，text[len(text)] == -1（哨兵）
	//	- 如果 text[i] < text[i+1]，或者 text[i] == text[i+1] 且 i+1 是 S 型，则位置 i 是 S 型。
	//	- 如果 text[i] > text[i+1]，或者 text[i] == text[i+1] 且 i+1 是 L 型，则位置 i 是 L 型。
	//
	// 后向扫描让我们能够保持当前的类型，
	// 当看到 c0 != c1 时更新它，否则保持不变。
	// 我们想要识别所有前面有 L 的 S 位置。
	// 根据定义，位置 len(text) 就是这样的位置，但是我们
	// 没有地方来记录它，所以通过在循环开始时不真实地
	// 设置 isTypeS = false 来消除它。
	c0, c1, isTypeS := byte(0), byte(0), false
	for i := len(text) - 1; i >= 0; i-- {
		c0, c1 = text[i], c0
		if c0 < c1 {
			isTypeS = true
		} else if c0 > c1 && isTypeS {
			isTypeS = false

			// Bucket the index i+1 for the start of an LMS-substring.
			b := bucket[c1] - 1
			bucket[c1] = b
			sa[b] = int32(i + 1)
			lastB = b
			numLMS++
		}
	}

	// 我们记录了 LMS 子串的起始索引，但实际上想要的是结束索引。
	// 幸运的是，有两个区别，起始索引和结束索引是相同的。
	// 第一个区别是最右边 LMS 子串的结束索引是 len(text)，
	// 所以调用者必须假设 sa[-1] == len(text)，如上所述。
	// 第二个区别是最左边 LMS 子串的起始索引
	// 不是前一个 LMS 子串的结束索引，
	// 所以作为优化，我们可以省略最左边的 LMS 子串起始索引（我们写入的最后一个）。
	//
	// 例外：如果 numLMS <= 1，调用者根本不会进行递归，
	// 会将结果视为包含 LMS 子串的起始索引。
	// 在这种情况下，我们不会删除最后一个条目。
	if numLMS > 1 {
		sa[lastB] = 0
	}
	return numLMS
}

// induceSubL_8_32 将 LMS 子串的 L 型文本索引插入 sa，
// 假设 LMS 子串的最终字符已按最终字符排序插入 sa，
// 且位于相应字符桶的右端（而非左端）。
// 每个 LMS 子串的形式（作为正则表达式）为 /S+L+S/：
// 一个或多个 S 型，一个或多个 L 型，最终 S 型。
// induceSubL_8_32 只留下每个 LMS 子串最左边的 L 型文本索引。
// 也就是说，它移除存在的最终 S 型索引，
// 也插入然后移除内部的 L 型索引。
//（只有最左边的 L 型索引需要由 induceSubS_8_32 处理。）
func induceSubL_8_32(text []byte, sa, freq, bucket []int32) {
	// 初始化字符桶左侧的位置。
	bucketMin_8_32(text, freq, bucket)
	bucket = bucket[:256] // 消除下面 bucket[cB] 的边界检查

	// 当我们从左到右扫描数组时，每个 sa[i] = j > 0 是一个正确的
	// 排序后缀数组条目（对于 text[j:]），我们知道 j-1 是 L 型。
	// 因为 j-1 是 L 型，现在将它插入 sa 会正确排序。
	// 但我们想要区分 j-1 前面是 L 型还是 S 型的情况。
	// 如果 j-1 前面是 S 型，我们可以通过取反 j-1 来记录区别，
	// 这样调用者就能跳过它。
	// 无论哪种情况，插入（到 text[j-1] 桶中）保证会
	// 发生在 sa[i´] 处，其中 i´ > i，也就是说，在尚未扫描的 sa 部分中。
	// 因此单次扫描会按排序但不一定相邻的顺序看到索引 j、j-1、j-2、j-3，
	// 等等，直到找到一个前面是 S 型索引的索引，此时它必须停止。
	//
	// 当我们遍历数组时，我们清除已处理的条目（sa[i] > 0）为零，
	// 并将 sa[i] < 0 翻转为 -sa[i]，
	// 以便循环结束时 sa 只包含每个 LMS 子串最左边的 L 型索引。
	//
	// 因此，后缀数组 sa 同时充当输入、输出
	// 以及一个精心设计的工作队列。

	// placeLMS_8_32 遗漏了隐含的条目 sa[-1] == len(text)，
	// 对应于识别的 L 型索引 len(text)-1。
	// 在从左到右正式扫描 sa 之前处理它。
	// 参见循环中的正文注释。
	k := len(text) - 1
	c0, c1 := text[k-1], text[k]
	if c0 < c1 {
		k = -k
	}

	// 缓存最近使用的桶索引：
	// 我们按排序顺序处理后缀，
	// 访问由排序顺序前的字节索引的桶，
	// 这仍然具有非常好的局部性。
	// 不变式：b 是 bucket[cB] 的缓存，可能是脏拷贝。
	cB := c1
	b := bucket[cB]
	sa[b] = int32(k)
	b++

	for i := 0; i < len(sa); i++ {
		j := int(sa[i])
		if j == 0 {
			// 跳过空条目。
			continue
		}
		if j < 0 {
			// 保留发现 S 型索引给调用者。
			sa[i] = int32(-j)
			continue
		}
		sa[i] = 0

		// 索引 j 在工作队列中，意味着 k := j-1 是 L 型，
		// 所以我们现在可以将 k 正确地放入 sa。
		// 如果 k-1 是 L 型，将 k 排队以便稍后在此循环中处理。
		// 如果 k-1 是 S 型（text[k-1] < text[k]），将 -k 排队保留给调用者。
		k := j - 1
		c0, c1 := text[k-1], text[k]
		if c0 < c1 {
			k = -k
		}

		if cB != c1 {
			bucket[cB] = b
			cB = c1
			b = bucket[cB]
		}
		sa[b] = int32(k)
		b++
	}
}

// induceSubS_8_32 将 LMS 子串的 S 型文本索引插入 sa，
// 假设 LMS 子串最左边的 L 型文本索引已按 LMS 子串后缀排序插入 sa，
// 且位于相应字符桶的左端。
// 每个 LMS 子串的形式（作为正则表达式）为 /S+L+S/：
// 一个或多个 S 型，一个或多个 L 型，最终 S 型。
// induceSubS_8_32 只为每个 LMS 子串留下最左边的 S 型文本索引，
// 按排序顺序位于 sa 的右端。
// 也就是说，它移除存在的 L 型索引，
// 也插入然后移除内部的 S 型索引，
// 将 LMS 子串起始索引压缩到 sa[len(sa)-numLMS:] 中。
//（只有 LMS 子串起始索引由递归处理。）
func induceSubS_8_32(text []byte, sa, freq, bucket []int32) {
	// 初始化字符桶右侧的位置。
	bucketMax_8_32(text, freq, bucket)
	bucket = bucket[:256] // 消除下面 bucket[cB] 的边界检查

	// 类似于上面的 induceSubL_8_32，
	// 当我们从右到左扫描数组时，每个 sa[i] = j > 0 是一个正确的
	// 排序后缀数组条目（对于 text[j:]），我们知道 j-1 是 S 型。
	// 因为 j-1 是 S 型，现在将它插入 sa 会正确排序。
	// 但我们想要区分 j-1 前面是 S 型还是 L 型的情况。
	// 如果 j-1 前面是 L 型，我们可以通过取反 j-1 来记录区别，
	// 这样调用者就能跳过它。
	// 无论哪种情况，插入（到 text[j-1] 桶中）保证会
	// 发生在 sa[i´] 处，其中 i´ < i，也就是说，在尚未扫描的 sa 部分中。
	// 因此单次扫描会按排序但不一定相邻的顺序看到索引 j、j-1、j-2、j-3，
	// 等等，直到找到一个前面是 L 型索引的索引，此时它必须停止。
	// 那个索引（前面是 L 型）是一个 LMS 子串的起始。
	//
	// 当我们遍历数组时，我们清除已处理的条目（sa[i] > 0）为零，
	// 并将 sa[i] < 0 翻转为 -sa[i] 并压缩到 sa 的顶部，
	// 以便循环结束时 sa 的顶部正好包含
	// LMS 子串起始索引，按 LMS 子串排序。

	// 缓存最近使用的桶索引。
	cB := byte(0)
	b := bucket[cB]

	top := len(sa)
	for i := len(sa) - 1; i >= 0; i-- {
		j := int(sa[i])
		if j == 0 {
			// 跳过空条目。
			continue
		}
		sa[i] = 0
		if j < 0 {
			// 保留发现的 LMS 子串起始索引给调用者。
			top--
			sa[top] = int32(-j)
			continue
		}

		// 索引 j 在工作队列中，意味着 k := j-1 是 S 型，
		// 所以我们现在可以将 k 正确地放入 sa。
		// 如果 k-1 是 S 型，将 k 排队以便稍后在此循环中处理。
		// 如果 k-1 是 L 型（text[k-1] > text[k]），将 -k 排队保留给调用者。
		k := j - 1
		c1 := text[k]
		c0 := text[k-1]
		if c0 > c1 {
			k = -k
		}

		if cB != c1 {
			bucket[cB] = b
			cB = c1
			b = bucket[cB]
		}
		b--
		sa[b] = int32(k)
	}
}

// length_8_32 计算并记录 text 中每个 LMS 子串的长度。
// The length of the LMS-substring at index j is stored at sa[j/2],
// avoiding the LMS-substring indexes already stored in the top half of sa.
// (If index j is an LMS-substring start, then index j-1 is type L and cannot be.)
// There are two exceptions, made for optimizations in name_8_32 below.
//
// 第一个例外是，最终 LMS 子串被记录为长度 0，
// 这在其他情况下是不可能的，
// 而不是给它一个包含隐式哨兵的长度。
// 这确保最终 LMS 子串的长度与所有其他子串不同，
// 因此可以无需文本比较即可检测为不同
//（它不同是因为它是唯一以隐式哨兵结尾的子串，
// 而文本比较是有问题的，因为隐式哨兵
// 实际上并不存在于 text[len(text)]）。
//
// 第二个例外是，为了完全避免文本比较，
// 如果一个 LMS 子串非常短，
// sa[j/2] 记录的是其实际文本而不是长度，
// 这样如果两个这样的子串有匹配的"长度"，就不需要读取文本了。
// "非常短"的定义是文本字节必须能打包到一个 uint32 中，
// 并且无符号编码 e 必须 ≥ len(text)，
// 以便可以与有效长度区分开来。
func length_8_32(text []byte, sa []int32, numLMS int) {
	end := 0 // 当前 LMS 子串结尾的索引（0 表示最后一个 LMS 子串）

	// The encoding of N text bytes into a “length” word
	// adds 1 to each byte, packs them into the bottom
	// N*8 bits of a word, and then bitwise inverts the result.
	// That is, the text sequence A B C (hex 41 42 43)
	// encodes as ^uint32(0x42_43_44).
	// LMS-substrings can never start or end with 0xFF.
	// Adding 1 ensures the encoded byte sequence never
	// starts or ends with 0x00, so that present bytes can be
	// distinguished from zero-padding in the top bits,
	// so the length need not be separately encoded.
	// Inverting the bytes increases the chance that a
	// 4-byte encoding will still be ≥ len(text).
	// In particular, if the first byte is ASCII (<= 0x7E, so +1 <= 0x7F)
	// then the high bit of the inversion will be set,
	// making it clearly not a valid length (it would be a negative one).
	//
	// cx holds the pre-inverted encoding (the packed incremented bytes).
	cx := uint32(0) // byte-only

	// 这一节（直到空行）是"LMS 子串迭代器"，
	// 在上面 placeLMS_8_32 中描述的，
	// 添加了一行来维护 cx。
	c0, c1, isTypeS := byte(0), byte(0), false
	for i := len(text) - 1; i >= 0; i-- {
		c0, c1 = text[i], c0
		cx = cx<<8 | uint32(c1+1) // byte-only
		if c0 < c1 {
			isTypeS = true
		} else if c0 > c1 && isTypeS {
			isTypeS = false

			// 索引 j = i+1 是 LMS 子串的开始。
			// 计算长度或编码文本存储到 sa[j/2] 中。
			j := i + 1
			var code int32
			if end == 0 {
				code = 0
			} else {
				code = int32(end - j)
				if code <= 32/8 && ^cx >= uint32(len(text)) { // byte-only
					code = int32(^cx) // byte-only
				} // byte-only
			}
			sa[j>>1] = code
			end = j + 1
			cx = uint32(c1 + 1) // byte-only
		}
	}
}

// assignID_8_32 为 LMS 子串集合分配紧凑的 ID 编号，
// 遵循字符串排序和相等性，返回最大分配的 ID。
// 例如给定输入 "ababab"，LMS 子串是 "aba"、"aba" 和 "ab"，
// 重新编号为 2 2 1。
// sa[len(sa)-numLMS:] 包含按字符串顺序排序的 LMS 子串索引，
// 因此为了分配编号，我们可以依次处理每个子串，
// 去除相邻的重复项。
// 索引 j 处的 LMS 子串的新 ID 被写入 sa[j/2]，
// 覆盖之前（由上面的 length_8_32）存储的长度。
func assignID_8_32(text []byte, sa []int32, numLMS int) int {
	id := 0
	lastLen := int32(-1) // 不可能值
	lastPos := int32(0)
	for _, j := range sa[len(sa)-numLMS:] {
		// 索引 j 处的 LMS 子串是新的，还是与我们看到的最后一个相同？
		n := sa[j/2]
		if n != lastLen {
			goto New
		}
		if uint32(n) >= uint32(len(text)) {
			// “Length” is really encoded full text, and they match.
			goto Same
		}
		{
			// Compare actual texts.
			n := int(n)
			this := text[j:][:n]
			last := text[lastPos:][:n]
			for i := 0; i < n; i++ {
				if this[i] != last[i] {
					goto New
				}
			}
			goto Same
		}
	New:
		id++
		lastPos = j
		lastLen = n
	Same:
		sa[j/2] = int32(id)
	}
	return id
}

// map_32 将 text 中的 LMS 子串映射到它们的新 ID，
// 生成递归的子问题。
// 映射本身主要由 assignID_8_32 应用：
// sa[i] 要么是 0，要么是索引 2*i 处 LMS 子串的 ID，
// 要么是索引 2*i+1 处 LMS 子串的 ID。
// 为了生成子问题，我们只需要去除零，
// 并将 ID 改为 ID-1（我们的 ID 从 1 开始，但文本字符从 0 开始）。
//
// map_32 将结果（递归的输入）打包到 sa 的顶部，
// 这样递归结果可以存储在 sa 的底部，
// 这为 expand_8_32 做好了准备。
func map_32(sa []int32, numLMS int) {
	w := len(sa)
	for i := len(sa) / 2; i >= 0; i-- {
		j := sa[i]
		if j > 0 {
			w--
			sa[w] = j - 1
		}
	}
}

// recurse_32 调用 sais_32 递归地解决我们构建的子问题。
// 子问题在 sa 的右端，后缀数组结果将写入 sa 的左端，
// sa 的中间部分可用作临时频率和桶存储。
func recurse_32(sa, oldTmp []int32, numLMS, maxID int) {
	dst, saTmp, text := sa[:numLMS], sa[numLMS:len(sa)-numLMS], sa[len(sa)-numLMS:]

	// 为递归调用设置临时空间。
	// 我们必须传递给 sais_32 一个至少具有 maxID 条目的 tmp 缓冲区。
	//
	// 子问题保证长度最多为 len(sa)/2，
	// 这样 sa 可以同时保存子问题和它的后缀数组。
	// 然而，几乎所有时候，子问题长度 < len(sa)/3，
	// 在这种情况下，sa 有一个子问题大小的中间部分
	// 我们可以重用为临时空间（saTmp）。
	// 当 recurse_32 从 sais_8_32 调用时，oldTmp 长度为 512
	//（来自 text_32），saTmp 通常会大得多，所以我们会使用 saTmp。
	// 当更深的递归回到 recurse_32 时，现在 oldTmp 是
	// 最顶层递归的 saTmp，它通常比当前的 saTmp 大
	//（因为随着递归加深，当前 sa 越来越小），
	// 我们继续重用那个最大的 saTmp，而不是提供的较小的。
	//
	// 为什么子问题长度经常刚好在 len(sa)/3 以下？
	// 参见 Nong、Zhang 和 Chen，第 3.6 节的一个合理的解释。
	// 简言之，len(sa)/2 的情况对应于输入中的 SLSLSLSLSLSL 模式，
	// 即输入字节的完美交替。
	// 真实的文本不会这样。如果每个 L 型索引随机跟随
	// 一个 L 型或 S 型索引，那么一半的子串将是 SLS 形式，
	// 但另一半会更长。在那一半中，
	// 一半（四分之一总体）将是 SLLS；八分之一将是 SLLLS，以此类推。
	// 不计算每个中的最后一个 S（与下一个中的第一个 S 重叠），
	// 这相当于平均长度 2×½ + 3×¼ + 4×⅛ + ... = 3。
	// 所需的空间进一步减少，因为许多
	// 像 SLS 这样的短模式通常是整个文本中重复出现的相同字符序列，
	// 相对于 numLMS 减少了 maxID。
	//
	// 对于短输入，平均值可能对我们不利，但那时我们可以
	// 回退到使用顶层调用中可用的长度-512 tmp。
	// （同样，短的分配也不是什么大问题。）
	//
	// 对于病态输入，我们回退到分配一个新的 tmp，长度为
	// max(maxID, numLMS/2)。这一层递归需要 maxID，
	// 所有更深的递归层将需要不超过 numLMS/2，
	// 因此这 one 次分配保证足以满足整个递归调用栈。
	tmp := oldTmp
	if len(tmp) < len(saTmp) {
		tmp = saTmp
	}
	if len(tmp) < numLMS {
		// TestSAIS/forcealloc 到达此代码。
		n := maxID
		if n < numLMS/2 {
			n = numLMS / 2
		}
		tmp = make([]int32, n)
	}

	// sais_32 要求调用者安排清除 dst，
	// 因为通常调用者可能知道 dst 是
	// 新分配的且已经清除。但这一个不是。
	clear(dst)
	sais_32(text, maxID, dst, tmp)
}

// unmap_8_32 将子问题映射回原始问题。
// sa[:numLMS] 是 LMS 子串编号，现在不太重要了。
// sa[len(sa)-numLMS:] 是这些 LMS 子串编号的排序列表。
// 关键部分是如果列表显示 K，那意味着第 K 个子串。
// 我们可以用 LMS 子串的索引替换 sa[:numLMS]。
// 然后如果列表显示 K，它实际上指的是 sa[K]。
// 既然已经将列表映射回 LMS 子串索引，
// 我们可以将它们放入正确的桶中。
func unmap_8_32(text []byte, sa []int32, numLMS int) {
	unmap := sa[len(sa)-numLMS:]
	j := len(unmap)

	// "LMS 子串迭代器"（参见上面的 placeLMS_8_32）。
	c0, c1, isTypeS := byte(0), byte(0), false
	for i := len(text) - 1; i >= 0; i-- {
		c0, c1 = text[i], c0
		if c0 < c1 {
			isTypeS = true
		} else if c0 > c1 && isTypeS {
			isTypeS = false

			// 填充逆映射。
			j--
			unmap[j] = int32(i + 1)
		}
	}

	// 将逆映射应用到子问题后缀数组。
	sa = sa[:numLMS]
	for i := 0; i < len(sa); i++ {
		sa[i] = unmap[sa[i]]
	}
}

// expand_8_32 将紧凑排序的 LMS 后缀索引
// 从 sa[:numLMS] 分布到 sa 中相应桶的顶部，
// 保持排序顺序，并为 L 型索引腾出空间
// 以便通过 induceL_8_32 插入到排序序列中。
func expand_8_32(text []byte, freq, bucket, sa []int32, numLMS int) {
	bucketMax_8_32(text, freq, bucket)
	bucket = bucket[:256] // 消除下面 bucket[c] 的边界检查

	// 向后循环遍历 sa，始终跟踪
	// 接下来要从 sa[:numLMS] 填充的索引。
	// 当我们到达一个时，填充它。
	// 其余槽位置清零；它们里面有死值。
	x := numLMS - 1
	saX := sa[x]
	c := text[saX]
	b := bucket[c] - 1
	bucket[c] = b

	for i := len(sa) - 1; i >= 0; i-- {
		if i != int(b) {
			sa[i] = 0
			continue
		}
		sa[i] = saX

		// 加载下一个要放置的条目（如果有）。
		if x > 0 {
			x--
			saX = sa[x] // TODO bounds check
			c = text[saX]
			b = bucket[c] - 1
			bucket[c] = b
		}
	}
}

// induceL_8_32 将 L 型文本索引插入 sa，
// 假设最左边的 S 型索引已按排序顺序
// 插入到右边的桶半部分中。
// 它将所有 L 型索引留在 sa 中，
// 但最左边的 L 型索引被取反，
// 以便由 induceS_8_32 处理标记它们。
func induceL_8_32(text []byte, sa, freq, bucket []int32) {
	// 初始化字符桶左侧的位置。
	bucketMin_8_32(text, freq, bucket)
	bucket = bucket[:256] // 消除下面 bucket[cB] 的边界检查

	// 此扫描类似于上面的 induceSubL_8_32。
	// 那个扫描安排清除除最左边 L 型索引外的所有索引。
	// 此扫描保留所有 L 型索引和原始 S 型索引，
	// 但它对正的最左边 L 型索引取反
	//（那些 induceS_8_32 需要处理的）。

	// expand_8_32 遗漏了隐含的条目 sa[-1] == len(text)，
	// 对应于识别的 L 型索引 len(text)-1。
	// 在正式从左到右扫描 sa 之前处理它。
	// 参见循环中的正文注释。
	k := len(text) - 1
	c0, c1 := text[k-1], text[k]
	if c0 < c1 {
		k = -k
	}

	// 缓存最近使用的桶索引。
	cB := c1
	b := bucket[cB]
	sa[b] = int32(k)
	b++

	for i := 0; i < len(sa); i++ {
		j := int(sa[i])
		if j <= 0 {
			// 跳过空或取反的条目（包括取反的零）。
			continue
		}

		// 索引 j 在工作队列中，意味着 k := j-1 是 L 型，
		// 所以我们现在可以将 k 正确地放入 sa。
		// 如果 k-1 是 L 型，将 k 排队以便稍后在此循环中处理。
		// 如果 k-1 是 S 型（text[k-1] < text[k]），将 -k 排队保留给调用者。
		// 如果 k 为零，k-1 不存在，所以我们只需要将它留给调用者。
		// 调用者无法区分空槽和非空零，但没有必要区分它们：
		// 最终的后缀数组最终会在某处有一个真正的零，
		// 那将是一个真实的零。
		k := j - 1
		c1 := text[k]
		if k > 0 {
			if c0 := text[k-1]; c0 < c1 {
				k = -k
			}
		}

		if cB != c1 {
			bucket[cB] = b
			cB = c1
			b = bucket[cB]
		}
		sa[b] = int32(k)
		b++
	}
}

func induceS_8_32(text []byte, sa, freq, bucket []int32) {
	// 初始化字符桶右侧的位置。
	bucketMax_8_32(text, freq, bucket)
	bucket = bucket[:256] // 消除下面 bucket[cB] 的边界检查

	cB := byte(0)
	b := bucket[cB]

	for i := len(sa) - 1; i >= 0; i-- {
		j := int(sa[i])
		if j >= 0 {
			// 跳过未标记的条目。
			//（此循环看不到空条目；0 表示真实的零索引。）
			continue
		}

		// 负 j 是工作队列条目；重写为正 j 用于最终后缀数组。
		j = -j
		sa[i] = int32(j)

		// 索引 j 在工作队列中（编码为 -j，但现在已解码），
		// 意味着 k := j-1 是 L 型，
		// 所以我们现在可以将 k 正确地放入 sa。
		// 如果 k-1 是 S 型，将 -k 排队以便稍后在此循环中处理。
		// 如果 k-1 是 L 型（text[k-1] > text[k]），将 k 排队保留给调用者。
		// 如果 k 为零，k-1 不存在，所以我们只需要将它留给调用者。
		k := j - 1
		c1 := text[k]
		if k > 0 {
			if c0 := text[k-1]; c0 <= c1 {
				k = -k
			}
		}

		if cB != c1 {
			bucket[cB] = b
			cB = c1
			b = bucket[cB]
		}
		b--
		sa[b] = int32(k)
	}
}
