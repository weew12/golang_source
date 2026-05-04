// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run gen_sort_variants.go

// Package sort 为切片和用户自定义集合提供排序原语。
package sort

import (
	"math/bits"
	"slices"
)

// Interface 的实现可以被本包中的例程排序。
// 这些方法通过整数索引引用底层集合中的元素。
type Interface interface {
	// Len 是集合中元素的数量。
	Len() int

	// Less 报告索引为 i 的元素是否必须排在索引为 j 的元素之前。
	//
	// 如果 Less(i, j) 和 Less(j, i) 都为 false，
	// 则认为索引 i 和 j 处的元素相等。
	// Sort 可以在最终结果中以任意顺序放置相等的元素，
	// 而 Stable 会保留相等元素的原始输入顺序。
	//
	// Less 必须描述一个 [严格弱序]。例如：
	//  - 如果 Less(i, j) 和 Less(j, k) 都为 true，则 Less(i, k) 也必须为 true。
	//  - 如果 Less(i, j) 和 Less(j, k) 都为 false，则 Less(i, k) 也必须为 false。
	//
	// 注意，当涉及非数字（NaN）值时，浮点数的比较（float32 或 float64 值上的 < 运算符）
	// 不是严格弱序。
	// 有关浮点值的正确实现，请参见 Float64Slice.Less。
	//
	// [Strict Weak Ordering]: https://en.wikipedia.org/wiki/Weak_ordering#Strict_weak_orderings
	Less(i, j int) bool

	// Swap 交换索引为 i 和 j 的元素。
	Swap(i, j int)
}

// Sort 根据 Less 方法确定的方式以升序排序数据。
// 它调用一次 data.Len 以确定 n，调用 O(n*log(n)) 次
// data.Less 和 data.Swap。不保证排序是稳定的。
//
// 注意：在许多情况下，更新的 [slices.SortFunc] 函数更加人性化且运行更快。
func Sort(data Interface) {
	n := data.Len()
	if n <= 1 {
		return
	}
	limit := bits.Len(uint(n))
	pdqsort(data, 0, n, limit)
}

type sortedHint int // hint for pdqsort when choosing the pivot

const (
	unknownHint sortedHint = iota
	increasingHint
	decreasingHint
)

// xorshift paper: https://www.jstatsoft.org/article/view/v008i14/xorshift.pdf
type xorshift uint64

func (r *xorshift) Next() uint64 {
	*r ^= *r << 13
	*r ^= *r >> 7
	*r ^= *r << 17
	return uint64(*r)
}

func nextPowerOfTwo(length int) uint {
	shift := uint(bits.Len(uint(length)))
	return uint(1 << shift)
}

// lessSwap is a pair of Less and Swap function for use with the
// auto-generated func-optimized variant of sort.go in
// zfuncversion.go.
type lessSwap struct {
	Less func(i, j int) bool
	Swap func(i, j int)
}

type reverse struct {
	// 这个嵌入的 Interface 允许 Reverse 使用
	// 另一个 Interface 实现的方法。
	Interface
}

// Less 返回嵌入实现的 Less 方法的反向结果。
func (r reverse) Less(i, j int) bool {
	return r.Interface.Less(j, i)
}

// Reverse 返回数据反序后的顺序。
func Reverse(data Interface) Interface {
	return &reverse{data}
}

// IsSorted 报告数据是否已排序。
//
// 注意：在许多情况下，更新的 [slices.IsSortedFunc] 函数更加人性化且运行更快。
func IsSorted(data Interface) bool {
	n := data.Len()
	for i := n - 1; i > 0; i-- {
		if data.Less(i, i-1) {
			return false
		}
	}
	return true
}

// 常见用例的便捷类型

// IntSlice 将 Interface 的方法附加到 []int，按递增顺序排序。
type IntSlice []int

func (x IntSlice) Len() int           { return len(x) }
func (x IntSlice) Less(i, j int) bool { return x[i] < x[j] }
func (x IntSlice) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }

// Sort 是一个便捷方法：x.Sort() 调用 Sort(x)。
func (x IntSlice) Sort() { Sort(x) }

// Float64Slice 实现 []float64 的 Interface，按递增顺序排序，
// 非数字（NaN）值排在其他值之前。
type Float64Slice []float64

func (x Float64Slice) Len() int { return len(x) }

// Less 报告 x[i] 是否应排在 x[j] 之前，这是 sort Interface 所要求的。
// 注意，浮点数比较本身不是传递关系：它不能为非数字（NaN）值报告一致的排序。
// 这个 Less 的实现通过以下方式将 NaN 值放在其他值之前：
//
//	x[i] < x[j] || (math.IsNaN(x[i]) && !math.IsNaN(x[j]))
func (x Float64Slice) Less(i, j int) bool { return x[i] < x[j] || (isNaN(x[i]) && !isNaN(x[j])) }
func (x Float64Slice) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }

// isNaN 是 math.IsNaN 的副本，以避免对 math 包的依赖。
func isNaN(f float64) bool {
	return f != f
}

// Sort 是一个便捷方法：x.Sort() 调用 Sort(x)。
func (x Float64Slice) Sort() { Sort(x) }

// StringSlice 将 Interface 的方法附加到 []string，按递增顺序排序。
type StringSlice []string

func (x StringSlice) Len() int           { return len(x) }
func (x StringSlice) Less(i, j int) bool { return x[i] < x[j] }
func (x StringSlice) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }

// Sort 是一个便捷方法：x.Sort() 调用 Sort(x)。
func (x StringSlice) Sort() { Sort(x) }

// 常见用例的便捷封装函数

// Ints 按递增顺序对 int 切片进行排序。
//
// 注意：从 Go 1.22 开始，此函数直接调用 [slices.Sort]。
func Ints(x []int) { slices.Sort(x) }

// Float64s 按递增顺序对 float64 切片进行排序。
// 非数字（NaN）值排在其他值之前。
//
// 注意：从 Go 1.22 开始，此函数直接调用 [slices.Sort]。
func Float64s(x []float64) { slices.Sort(x) }

// Strings 按递增顺序对 string 切片进行排序。
//
// 注意：从 Go 1.22 开始，此函数直接调用 [slices.Sort]。
func Strings(x []string) { slices.Sort(x) }

// IntsAreSorted 报告 int 切片 x 是否按递增顺序排序。
//
// 注意：从 Go 1.22 开始，此函数直接调用 [slices.IsSorted]。
func IntsAreSorted(x []int) bool { return slices.IsSorted(x) }

// Float64sAreSorted 报告 float64 切片 x 是否按递增顺序排序，
// 非数字（NaN）值排在其他值之前。
//
// 注意：从 Go 1.22 开始，此函数直接调用 [slices.IsSorted]。
func Float64sAreSorted(x []float64) bool { return slices.IsSorted(x) }

// StringsAreSorted 报告 string 切片 x 是否按递增顺序排序。
//
// 注意：从 Go 1.22 开始，此函数直接调用 [slices.IsSorted]。
func StringsAreSorted(x []string) bool { return slices.IsSorted(x) }

// 稳定排序注意事项：
// 所使用的算法简单且可在所有输入上证明正确，并且仅使用对数额外栈空间。
// 与其他稳定原地排序算法相比，它们的性能表现良好。
//
// 对其他已评估算法的评价：
//  - GCC 4.6.3 的 stable_sort（来自 libstdc++ 的 merge_without_buffer）：
//    不更快。
//  - GCC 的 __rotate 用于块旋转：不更快。
//  - Jyrki Katajainen、Tomí A. Pasanen 和 Jukka Teuhola 的"实用原地归并排序"；
//    Nordic Journal of Computing 3,1 (1996)，27-40：
//    给出的算法是原地的，Swap 和赋值次数增长为 n log n，但该算法不稳定。
//  - J.I. Munro 和 V. Raman 的"具有 O(n) 数据移动的快速稳定原地排序"，
//    发表于 Algorithmica (1996) 16, 115-160：
//    该算法要么需要额外的 2n 位，要么仅在有足够不同元素可用于编码某些排列时才能工作，
//    这些排列稍后必须撤消（因此在任何输入上都不稳定）。
//  - 我发现的所有最佳原地排序/归并算法要么不稳定，
//    要么在每一步都依赖足够不同的元素来编码所执行的块重排。
//    另请参见"In-Place Merging Algorithms"，
//    Denham Coates-Evely, Department of Computer Science, Kings College,
//    2004 年 1 月及其中的参考文献。
//  - 通常"最佳"算法在赋值次数方面是最优的，
//    但 Interface 只有 Swap 作为操作。

// Stable 根据 Less 方法以升序排序数据，
// 同时保持相等元素的原始顺序。
//
// 它调用一次 data.Len 以确定 n，调用 O(n*log(n)) 次 data.Less
// 和 O(n*log(n)*log(n)) 次 data.Swap。
//
// 注意：在许多情况下，更新的 slices.SortStableFunc 函数更加人性化且运行更快。
func Stable(data Interface) {
	stable(data, data.Len())
}

/*
Complexity of Stable Sorting


Complexity of block swapping rotation

Each Swap puts one new element into its correct, final position.
Elements which reach their final position are no longer moved.
Thus block swapping rotation needs |u|+|v| calls to Swaps.
This is best possible as each element might need a move.

Pay attention when comparing to other optimal algorithms which
typically count the number of assignments instead of swaps:
E.g. the optimal algorithm of Dudzinski and Dydek for in-place
rotations uses O(u + v + gcd(u,v)) assignments which is
better than our O(3 * (u+v)) as gcd(u,v) <= u.


Stable sorting by SymMerge and BlockSwap rotations

SymMerg complexity for same size input M = N:
Calls to Less:  O(M*log(N/M+1)) = O(N*log(2)) = O(N)
Calls to Swap:  O((M+N)*log(M)) = O(2*N*log(N)) = O(N*log(N))

(The following argument does not fuzz over a missing -1 or
other stuff which does not impact the final result).

Let n = data.Len(). Assume n = 2^k.

Plain merge sort performs log(n) = k iterations.
On iteration i the algorithm merges 2^(k-i) blocks, each of size 2^i.

Thus iteration i of merge sort performs:
Calls to Less  O(2^(k-i) * 2^i) = O(2^k) = O(2^log(n)) = O(n)
Calls to Swap  O(2^(k-i) * 2^i * log(2^i)) = O(2^k * i) = O(n*i)

In total k = log(n) iterations are performed; so in total:
Calls to Less O(log(n) * n)
Calls to Swap O(n + 2*n + 3*n + ... + (k-1)*n + k*n)
   = O((k/2) * k * n) = O(n * k^2) = O(n * log^2(n))


Above results should generalize to arbitrary n = 2^k + p
and should not be influenced by the initial insertion sort phase:
Insertion sort is O(n^2) on Swap and Less, thus O(bs^2) per block of
size bs at n/bs blocks:  O(bs*n) Swaps and Less during insertion sort.
Merge sort iterations start at i = log(bs). With t = log(bs) constant:
Calls to Less O((log(n)-t) * n + bs*n) = O(log(n)*n + (bs-t)*n)
   = O(n * log(n))
Calls to Swap O(n * log^2(n) - (t^2+t)/2*n) = O(n * log^2(n))

*/
