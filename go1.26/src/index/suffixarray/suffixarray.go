// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package suffixarray 使用内存中的后缀数组实现对数时间复杂度的子字符串搜索。
//
// 示例用法：
//
//	// 为一些数据创建索引
//	index := suffixarray.New(data)
//
//	// 查找字节切片 s
//	offsets1 := index.Lookup(s, -1) // s 在 data 中出现的所有索引列表
//	offsets2 := index.Lookup(s, 3)  // s 在 data 中最多出现的 3 个索引列表
package suffixarray

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"regexp"
	"slices"
	"sort"
)

// Can change for testing
var maxData32 int = realMaxData32

const realMaxData32 = math.MaxInt32

// Index 实现了一个用于快速子字符串搜索的后缀数组。
type Index struct {
	data []byte
	sa   ints // data 的后缀数组；sa.len() == len(data)
}

// ints 是 []int32 或 []int64 之一。
// 也就是说，其中一个为空，另一个是真实数据。
// 当 len(data) > maxData32 时使用 int64 形式。
type ints struct {
	int32 []int32
	int64 []int64
}

func (a *ints) len() int {
	return len(a.int32) + len(a.int64)
}

func (a *ints) get(i int) int64 {
	if a.int32 != nil {
		return int64(a.int32[i])
	}
	return a.int64[i]
}

func (a *ints) set(i int, v int64) {
	if a.int32 != nil {
		a.int32[i] = int32(v)
	} else {
		a.int64[i] = v
	}
}

func (a *ints) slice(i, j int) ints {
	if a.int32 != nil {
		return ints{a.int32[i:j], nil}
	}
	return ints{nil, a.int64[i:j]}
}

// New 为 data 创建一个新的 [Index]。
// [Index] 的创建时间为 O(N)，其中 N = len(data)。
func New(data []byte) *Index {
	ix := &Index{data: data}
	if len(data) <= maxData32 {
		ix.sa.int32 = make([]int32, len(data))
		text_32(data, ix.sa.int32)
	} else {
		ix.sa.int64 = make([]int64, len(data))
		text_64(data, ix.sa.int64)
	}
	return ix
}

// writeInt 使用 buf 缓冲写入，将 int x 写入 w。
func writeInt(w io.Writer, buf []byte, x int) error {
	binary.PutVarint(buf, int64(x))
	_, err := w.Write(buf[0:binary.MaxVarintLen64])
	return err
}

// readInt 使用 buf 缓冲读取，从 r 读取 int x 并返回 x。
func readInt(r io.Reader, buf []byte) (int64, error) {
	_, err := io.ReadFull(r, buf[0:binary.MaxVarintLen64]) // 出错时继续是可以的
	x, _ := binary.Varint(buf)
	return x, err
}

// writeSlice 将 data[:n] 写入 w 并返回 n。
// 它使用 buf 来缓冲写入。
func writeSlice(w io.Writer, buf []byte, data ints) (n int, err error) {
	// 将尽可能多的元素编码到 buf 中
	p := binary.MaxVarintLen64
	m := data.len()
	for ; n < m && p+binary.MaxVarintLen64 <= len(buf); n++ {
		p += binary.PutUvarint(buf[p:], uint64(data.get(n)))
	}

	// 更新缓冲区大小
	binary.PutVarint(buf, int64(p))

	// 写入缓冲区
	_, err = w.Write(buf[0:p])
	return
}

var errTooBig = errors.New("suffixarray: data too large")

// readSlice 从 r 读取 data[:n] 并返回 n。
// 它使用 buf 来缓冲读取。
func readSlice(r io.Reader, buf []byte, data ints) (n int, err error) {
	// 读取缓冲区大小
	var size64 int64
	size64, err = readInt(r, buf)
	if err != nil {
		return
	}
	if int64(int(size64)) != size64 || int(size64) < 0 {
		// 我们从来不写这么大的块。
		return 0, errTooBig
	}
	size := int(size64)

	// 读取不带大小的缓冲区
	if _, err = io.ReadFull(r, buf[binary.MaxVarintLen64:size]); err != nil {
		return
	}

	// 解码 buf 中存在的尽可能多的元素
	for p := binary.MaxVarintLen64; p < size; n++ {
		x, w := binary.Uvarint(buf[p:])
		data.set(n, int64(x))
		p += w
	}

	return
}

const bufSize = 16 << 10 // 对 BenchmarkSaveRestore 来说是合理的

// Read 将索引从 r 读入 x；x 不能为 nil。
func (x *Index) Read(r io.Reader) error {
	// 所有读取的缓冲区
	buf := make([]byte, bufSize)

	// 读取长度
	n64, err := readInt(r, buf)
	if err != nil {
		return err
	}
	if int64(int(n64)) != n64 || int(n64) < 0 {
		return errTooBig
	}
	n := int(n64)

	// 分配空间
	if 2*n < cap(x.data) || cap(x.data) < n || x.sa.int32 != nil && n > maxData32 || x.sa.int64 != nil && n <= maxData32 {
		// 新数据明显小于或大于现有缓冲区——分配新的
		x.data = make([]byte, n)
		x.sa.int32 = nil
		x.sa.int64 = nil
		if n <= maxData32 {
			x.sa.int32 = make([]int32, n)
		} else {
			x.sa.int64 = make([]int64, n)
		}
	} else {
		// 重用现有缓冲区
		x.data = x.data[0:n]
		x.sa = x.sa.slice(0, n)
	}

	// 读取数据
	if _, err := io.ReadFull(r, x.data); err != nil {
		return err
	}

	// 读取索引
	sa := x.sa
	for sa.len() > 0 {
		n, err := readSlice(r, buf, sa)
		if err != nil {
			return err
		}
		sa = sa.slice(n, sa.len())
	}
	return nil
}

// Write 将索引 x 写入 w。
func (x *Index) Write(w io.Writer) error {
	// 所有写入的缓冲区
	buf := make([]byte, bufSize)

	// 写入长度
	if err := writeInt(w, buf, len(x.data)); err != nil {
		return err
	}

	// 写入数据
	if _, err := w.Write(x.data); err != nil {
		return err
	}

	// 写入索引
	sa := x.sa
	for sa.len() > 0 {
		n, err := writeSlice(w, buf, sa)
		if err != nil {
			return err
		}
		sa = sa.slice(n, sa.len())
	}
	return nil
}

// Bytes 返回创建索引所基于的数据。
// 它不能被修改。
func (x *Index) Bytes() []byte {
	return x.data
}

func (x *Index) at(i int) []byte {
	return x.data[x.sa.get(i):]
}

// lookupAll 返回索引中匹配区域的切片。
// 运行时间为 O(log(N)*len(s))。
func (x *Index) lookupAll(s []byte) ints {
	// 找到匹配的后缀索引范围 [i:j]
	// 找到 s 将作为前缀的第一个索引
	i := sort.Search(x.sa.len(), func(i int) bool { return bytes.Compare(x.at(i), s) >= 0 })
	// 从 i 开始，找到 s 不是前缀的第一个索引
	j := i + sort.Search(x.sa.len()-i, func(j int) bool { return !bytes.HasPrefix(x.at(j+i), s) })
	return x.sa.slice(i, j)
}

// Lookup 返回字节字符串 s 在索引数据中出现最多 n 个索引的无序列表。
// 如果 n < 0，返回所有出现的位置。
// 如果 s 为空、未找到或 n == 0，结果为 nil。
// 查找时间为 O(log(N)*len(s) + len(result))，其中 N 是索引数据的大小。
func (x *Index) Lookup(s []byte, n int) (result []int) {
	if len(s) > 0 && n != 0 {
		matches := x.lookupAll(s)
		count := matches.len()
		if n < 0 || count < n {
			n = count
		}
		// 0 <= n <= count
		if n > 0 {
			result = make([]int, n)
			if matches.int32 != nil {
				for i := range result {
					result[i] = int(matches.int32[i])
				}
			} else {
				for i := range result {
					result[i] = int(matches.int64[i])
				}
			}
		}
	}
	return
}

// FindAllIndex 返回正则表达式 r 的非重叠匹配的有序列表，
// 其中匹配是一对索引，指定匹配到的 x.Bytes() 的切片。
// 如果 n < 0，则按顺序返回所有匹配。
// 否则，最多返回 n 个匹配，它们可能不是连续的。
// 如果没有匹配或 n == 0，结果为 nil。
func (x *Index) FindAllIndex(r *regexp.Regexp, n int) (result [][]int) {
	// 非空字面量前缀用于通过 Lookup 确定可能的匹配起始索引
	prefix, complete := r.LiteralPrefix()
	lit := []byte(prefix)

	// 最坏情况：没有字面量前缀
	if prefix == "" {
		return r.FindAllIndex(x.data, n)
	}

	// 如果正则表达式是字面量，只使用 Lookup 并将其结果转换为匹配对
	if complete {
		// Lookup 返回的索引可能属于重叠匹配。
		// 在消除它们之后，我们可能最终得到少于 n 个匹配。
		// 如果最后没有足够的匹配，使用增加的值 n1 重新搜索，
		// 但仅当 Lookup 首先返回了所有请求的索引时
		//（如果它返回的少于该值，则不可能有更多）。
		for n1 := n; ; n1 += 2 * (n - len(result)) /* 溢出没关系 */ {
			indices := x.Lookup(lit, n1)
			if len(indices) == 0 {
				return
			}
			slices.Sort(indices)
			pairs := make([]int, 2*len(indices))
			result = make([][]int, len(indices))
			count := 0
			prev := 0
			for _, i := range indices {
				if count == n {
					break
				}
				// 忽略导致重叠匹配的索引
				if prev <= i {
					j := 2 * count
					pairs[j+0] = i
					pairs[j+1] = i + len(lit)
					result[count] = pairs[j : j+2]
					count++
					prev = i + len(lit)
				}
			}
			result = result[0:count]
			if len(result) >= n || len(indices) != n1 {
				// 找到了所有匹配或没有机会找到更多
				// (n 和 n1 可以为负)
				break
			}
		}
		if len(result) == 0 {
			result = nil
		}
		return
	}

	// 正则表达式有一个非空字面量前缀；Lookup(lit) 计算
	// 可能完整匹配的索引；使用这些作为锚定搜索的起点
	//（正则表达式 "^" 匹配输入的开头，而不是行的开头）
	r = regexp.MustCompile("^" + r.String()) // 可以编译，因为 r 已编译

	// 与上面循环中相同的关于 Lookup 的评论也适用于此
	for n1 := n; ; n1 += 2 * (n - len(result)) /* 溢出没关系 */ {
		indices := x.Lookup(lit, n1)
		if len(indices) == 0 {
			return
		}
		slices.Sort(indices)
		result = result[0:0]
		prev := 0
		for _, i := range indices {
			if len(result) == n {
				break
			}
			m := r.FindIndex(x.data[i:]) // 锚定搜索——不会跑出去
			// 忽略导致重叠匹配的索引
			if m != nil && prev <= i {
				m[0] = i // 修正 m
				m[1] += i
				result = append(result, m)
				prev = m[1]
			}
		}
		if len(result) >= n || len(indices) != n1 {
			// 找到了所有匹配或没有机会找到更多
			// (n 和 n1 可以为负)
			break
		}
	}
	if len(result) == 0 {
		result = nil
	}
	return
}
