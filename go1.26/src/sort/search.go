// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements binary search.

package sort

// Search 使用二分搜索查找并返回最小的索引 i，
// 位于 [0, n) 中且满足 f(i) 为 true，假设在范围 [0, n) 上，
// f(i) == true 意味着 f(i+1) == true。也就是说，Search 要求
// f 对于输入范围 [0, n) 的某个（可能为空的）前缀为 false，
// 然后对（可能为空的）剩余部分为 true；Search 返回第一个 true 索引。
// 如果没有这样的索引，Search 返回 n。
//（请注意，"未找到"的返回值不是 -1，例如 strings.Index 的情况。）
// Search 仅对 [0, n) 范围内的 i 调用 f(i)。
//
// Search 的一个常见用途是在已排序的、可索引的数据结构（如数组或切片）中
// 查找值 x 的索引 i。在这种情况下，参数 f（通常是闭包）捕获要查找的值，
// 以及数据结构的索引和排序方式。
//
// 例如，给定一个按升序排序的切片 data，
// 调用 Search(len(data), func(i int) bool { return data[i] >= 23 })
// 返回满足 data[i] >= 23 的最小索引 i。如果调用者想查找 23 是否在切片中，
// 必须单独测试 data[i] == 23。
//
// 搜索按降序排序的数据将使用 <= 运算符而不是 >= 运算符。
//
// 完整的示例中，以下代码尝试在按升序排序的整数切片 data 中查找值 x：
//
//	x := 23
//	i := sort.Search(len(data), func(i int) bool { return data[i] >= x })
//	if i < len(data) && data[i] == x {
//		// x 存在于 data[i] 处
//	} else {
//		// x 不存在于 data 中，
//		// 但 i 是它将被插入的索引。
//	}
//
// 作为一个更幽默的例子，这个程序猜你的数字：
//
//	func GuessingGame() {
//		var s string
//		fmt.Printf("Pick an integer from 0 to 100.\n")
//		answer := sort.Search(100, func(i int) bool {
//			fmt.Printf("Is your number <= %d? ", i)
//			fmt.Scanf("%s", &s)
//			return s != "" && s[0] == 'y'
//		})
//		fmt.Printf("Your number is %d.\n", answer)
//	}
func Search(n int, f func(int) bool) int {
	// 定义 f(-1) == false 和 f(n) == true。
	// 不变量：f(i-1) == false, f(j) == true。
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1) // 计算 h 时避免溢出
		// i ≤ h < j
		if !f(h) {
			i = h + 1 // 保持 f(i-1) == false
		} else {
			j = h // 保持 f(j) == true
		}
	}
	// i == j, f(i-1) == false, and f(j) (= f(i)) == true  => 答案为 i。
	return i
}

// Find 使用二分搜索查找并返回 [0, n) 中满足 cmp(i) <= 0 的最小索引 i。
// 如果没有这样的索引 i，Find 返回 i = n。
// 如果 i < n 且 cmp(i) == 0，则 found 结果为 true。
// Find 仅对 [0, n) 范围内的 i 调用 cmp(i)。
//
// 为了允许二分搜索，Find 要求在范围的前导前缀中 cmp(i) > 0，
// 在中间 cmp(i) == 0，在最终后缀中 cmp(i) < 0。（每个子范围可以为空。）
// 建立此条件的常用方法是将 cmp(i) 解释为将所需目标值 t 与
// 底层索引数据结构 x 中的条目 i 进行比较，
// 当 t < x[i]、t == x[i] 和 t > x[i] 时分别返回 <0、0 和 >0。
//
// 例如，在已排序的随机访问字符串列表中查找特定字符串：
//
//	i, found := sort.Find(x.Len(), func(i int) int {
//	    return strings.Compare(target, x.At(i))
//	})
//	if found {
//	    fmt.Printf("found %s at entry %d\n", target, i)
//	} else {
//	    fmt.Printf("%s not found, would insert at %d", target, i)
//	}
func Find(n int, cmp func(int) int) (i int, found bool) {
	// 这里的不变量与 Search 中的类似。
	// 定义 cmp(-1) > 0 和 cmp(n) <= 0
	// 不变量：cmp(i-1) > 0, cmp(j) <= 0
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1) // 计算 h 时避免溢出
		// i ≤ h < j
		if cmp(h) > 0 {
			i = h + 1 // 保持 cmp(i-1) > 0
		} else {
			j = h // 保持 cmp(j) <= 0
		}
	}
	// i == j, cmp(i-1) > 0 and cmp(j) <= 0
	return i, i < n && cmp(i) == 0
}

// 常见用例的便捷封装函数。

// SearchInts 在已排序的 int 切片中搜索 x，并返回如 [Search] 所指定的索引。
// 如果 x 不存在，返回值是插入 x 的索引（可以是 len(a)）。
// 切片必须按升序排序。
func SearchInts(a []int, x int) int {
	return Search(len(a), func(i int) bool { return a[i] >= x })
}

// SearchFloat64s 在已排序的 float64 切片中搜索 x，并返回如 [Search] 所指定的索引。
// 如果 x 不存在，返回值是插入 x 的索引（可以是 len(a)）。
// 切片必须按升序排序。
func SearchFloat64s(a []float64, x float64) int {
	return Search(len(a), func(i int) bool { return a[i] >= x })
}

// SearchStrings 在已排序的 string 切片中搜索 x，并返回如 Search 所指定的索引。
// 如果 x 不存在，返回值是插入 x 的索引（可以是 len(a)）。
// 切片必须按升序排序。
func SearchStrings(a []string, x string) int {
	return Search(len(a), func(i int) bool { return a[i] >= x })
}

// Search 返回将 [SearchInts] 应用于接收者和 x 的结果。
func (p IntSlice) Search(x int) int { return SearchInts(p, x) }

// Search 返回将 [SearchFloat64s] 应用于接收者和 x 的结果。
func (p Float64Slice) Search(x float64) int { return SearchFloat64s(p, x) }

// Search 返回将 [SearchStrings] 应用于接收者和 x 的结果。
func (p StringSlice) Search(x string) int { return SearchStrings(p, x) }
