// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package suffixarray

import (
	"bytes"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

type testCase struct {
	name     string   // 测试用例名称
	source   string   // 要索引的源数据
	patterns []string // 要查找的模式
}

var testCases = []testCase{
	{
		"empty string",
		"",
		[]string{
			"",
			"foo",
			"(foo)",
			".*",
			"a*",
		},
	},

	{
		"all a's",
		"aaaaaaaaaa", // 10 个 a
		[]string{
			"",
			"a",
			"aa",
			"aaa",
			"aaaa",
			"aaaaa",
			"aaaaaa",
			"aaaaaaa",
			"aaaaaaaa",
			"aaaaaaaaa",
			"aaaaaaaaaa",
			"aaaaaaaaaaa", // 11 个 a
			".",
			".*",
			"a+",
			"aa+",
			"aaaa[b]?",
			"aaa*",
		},
	},

	{
		"abc",
		"abc",
		[]string{
			"a",
			"b",
			"c",
			"ab",
			"bc",
			"abc",
			"a.c",
			"a(b|c)",
			"abc?",
		},
	},

	{
		"barbara*3",
		"barbarabarbarabarbara",
		[]string{
			"a",
			"bar",
			"rab",
			"arab",
			"barbar",
			"bara?bar",
		},
	},

	{
		"typing drill",
		"Now is the time for all good men to come to the aid of their country.",
		[]string{
			"Now",
			"the time",
			"to come the aid",
			"is the time for all good men to come to the aid of their",
			"to (come|the)?",
		},
	},

	{
		"godoc simulation",
		"package main\n\nimport(\n    \"rand\"\n    ",
		[]string{},
	},
}

// find 查找 source 中 s 的所有出现位置；最多报告 n 个出现位置
func find(src, s string, n int) []int {
	var res []int
	if s != "" && n != 0 {
		// 最多查找 src 中 s 的 n 个出现位置
		for i := -1; n < 0 || len(res) < n; {
			j := strings.Index(src[i+1:], s)
			if j < 0 {
				break
			}
			i += j + 1
			res = append(res, i)
		}
	}
	return res
}

func testLookup(t *testing.T, tc *testCase, x *Index, s string, n int) {
	res := x.Lookup([]byte(s), n)
	exp := find(tc.source, s, n)

	// 检查长度是否匹配
	if len(res) != len(exp) {
		t.Errorf("test %q, lookup %q (n = %d): expected %d results; got %d", tc.name, s, n, len(exp), len(res))
	}

	// 如果 n >= 0，结果数量是受限的——除非 n >= 所有结果，
	// 我们可能从 Index 和 find 获得不同的位置（因为
	// Index 可能不会以与 find 相同的顺序找到结果）=> 一般来说
	// 我们不能简单地检查 res 和 exp 列表是否相等

	// 检查每个结果是否确实是正确的匹配，并且没有重复
	slices.Sort(res)
	for i, r := range res {
		if r < 0 || len(tc.source) <= r {
			t.Errorf("test %q, lookup %q, result %d (n = %d): index %d out of range [0, %d[", tc.name, s, i, n, r, len(tc.source))
		} else if !strings.HasPrefix(tc.source[r:], s) {
			t.Errorf("test %q, lookup %q, result %d (n = %d): index %d not a match", tc.name, s, i, n, r)
		}
		if i > 0 && res[i-1] == r {
			t.Errorf("test %q, lookup %q, result %d (n = %d): found duplicate index %d", tc.name, s, i, n, r)
		}
	}

	if n < 0 {
		// all results computed - sorted res and exp must be equal
		for i, r := range res {
			e := exp[i]
			if r != e {
				t.Errorf("test %q, lookup %q, result %d: expected index %d; got %d", tc.name, s, i, e, r)
			}
		}
	}
}

func testFindAllIndex(t *testing.T, tc *testCase, x *Index, rx *regexp.Regexp, n int) {
	res := x.FindAllIndex(rx, n)
	exp := rx.FindAllStringIndex(tc.source, n)

	// 检查长度是否匹配
	if len(res) != len(exp) {
		t.Errorf("test %q, FindAllIndex %q (n = %d): expected %d results; got %d", tc.name, rx, n, len(exp), len(res))
	}

	// 如果 n >= 0，结果数量是受限的——除非 n >= 所有结果，
	// 我们可能从 Index 和 regexp 获得不同的位置（因为
	// Index 可能不会以与 regexp 相同的顺序找到结果）=> 一般来说
	// 我们不能简单地检查 res 和 exp 列表是否相等

	// 检查每个结果是否确实是正确的匹配，并且结果是有序的
	for i, r := range res {
		if r[0] < 0 || r[0] > r[1] || len(tc.source) < r[1] {
			t.Errorf("test %q, FindAllIndex %q, result %d (n == %d): illegal match [%d, %d]", tc.name, rx, i, n, r[0], r[1])
		} else if !rx.MatchString(tc.source[r[0]:r[1]]) {
			t.Errorf("test %q, FindAllIndex %q, result %d (n = %d): [%d, %d] not a match", tc.name, rx, i, n, r[0], r[1])
		}
	}

	if n < 0 {
		// 所有结果已计算——排序后的 res 和 exp 必须相等
		for i, r := range res {
			e := exp[i]
			if r[0] != e[0] || r[1] != e[1] {
				t.Errorf("test %q, FindAllIndex %q, result %d: expected match [%d, %d]; got [%d, %d]",
					tc.name, rx, i, e[0], e[1], r[0], r[1])
			}
		}
	}
}

func testLookups(t *testing.T, tc *testCase, x *Index, n int) {
	for _, pat := range tc.patterns {
		testLookup(t, tc, x, pat, n)
		if rx, err := regexp.Compile(pat); err == nil {
			testFindAllIndex(t, tc, x, rx, n)
		}
	}
}

// index 用于隐藏 sort.Interface
type index Index

func (x *index) Len() int           { return x.sa.len() }
func (x *index) Less(i, j int) bool { return bytes.Compare(x.at(i), x.at(j)) < 0 }
func (x *index) Swap(i, j int) {
	if x.sa.int32 != nil {
		x.sa.int32[i], x.sa.int32[j] = x.sa.int32[j], x.sa.int32[i]
	} else {
		x.sa.int64[i], x.sa.int64[j] = x.sa.int64[j], x.sa.int64[i]
	}
}

func (x *index) at(i int) []byte {
	return x.data[x.sa.get(i):]
}

func testConstruction(t *testing.T, tc *testCase, x *Index) {
	if !sort.IsSorted((*index)(x)) {
		t.Errorf("failed testConstruction %s", tc.name)
	}
}

func equal(x, y *Index) bool {
	if !bytes.Equal(x.data, y.data) {
		return false
	}
	if x.sa.len() != y.sa.len() {
		return false
	}
	n := x.sa.len()
	for i := 0; i < n; i++ {
		if x.sa.get(i) != y.sa.get(i) {
			return false
		}
	}
	return true
}

// 返回序列化后的索引大小
func testSaveRestore(t *testing.T, tc *testCase, x *Index) int {
	var buf bytes.Buffer
	if err := x.Write(&buf); err != nil {
		t.Errorf("failed writing index %s (%s)", tc.name, err)
	}
	size := buf.Len()
	var y Index
	if err := y.Read(bytes.NewReader(buf.Bytes())); err != nil {
		t.Errorf("failed reading index %s (%s)", tc.name, err)
	}
	if !equal(x, &y) {
		t.Errorf("restored index doesn't match saved index %s", tc.name)
	}

	old := maxData32
	defer func() {
		maxData32 = old
	}()
	// 以强制 32 位模式重新读取。
	y = Index{}
	maxData32 = realMaxData32
	if err := y.Read(bytes.NewReader(buf.Bytes())); err != nil {
		t.Errorf("failed reading index %s (%s)", tc.name, err)
	}
	if !equal(x, &y) {
		t.Errorf("restored index doesn't match saved index %s", tc.name)
	}

	// 以强制 64 位模式重新读取。
	y = Index{}
	maxData32 = -1
	if err := y.Read(bytes.NewReader(buf.Bytes())); err != nil {
		t.Errorf("failed reading index %s (%s)", tc.name, err)
	}
	if !equal(x, &y) {
		t.Errorf("restored index doesn't match saved index %s", tc.name)
	}

	return size
}

func testIndex(t *testing.T) {
	for _, tc := range testCases {
		x := New([]byte(tc.source))
		testConstruction(t, &tc, x)
		testSaveRestore(t, &tc, x)
		testLookups(t, &tc, x, 0)
		testLookups(t, &tc, x, 1)
		testLookups(t, &tc, x, 10)
		testLookups(t, &tc, x, 2e9)
		testLookups(t, &tc, x, -1)
	}
}

func TestIndex32(t *testing.T) {
	testIndex(t)
}

func TestIndex64(t *testing.T) {
	maxData32 = -1
	defer func() {
		maxData32 = realMaxData32
	}()
	testIndex(t)
}

func TestNew32(t *testing.T) {
	test(t, func(x []byte) []int {
		sa := make([]int32, len(x))
		text_32(x, sa)
		out := make([]int, len(sa))
		for i, v := range sa {
			out[i] = int(v)
		}
		return out
	})
}

func TestNew64(t *testing.T) {
	test(t, func(x []byte) []int {
		sa := make([]int64, len(x))
		text_64(x, sa)
		out := make([]int, len(sa))
		for i, v := range sa {
			out[i] = int(v)
		}
		return out
	})
}

// test 测试任意后缀数组构建函数。
// 生成许多输入，构建并检查后缀数组。
func test(t *testing.T, build func([]byte) []int) {
	t.Run("ababab...", func(t *testing.T) {
		// 非常重复的输入在顶层有 numLMS = len(x)/2-1，
		// 这是它能达到的最大值。
		// 但 maxID 只有两个（aba 和 ab$）。
		size := 100000
		if testing.Short() {
			size = 10000
		}
		x := make([]byte, size)
		for i := range x {
			x[i] = "ab"[i%2]
		}
		testSA(t, x, build)
	})

	t.Run("forcealloc", func(t *testing.T) {
		// 构造一个病态输入，强制
		// recurse_32 分配新的临时缓冲区。
		// 输入必须有超过 N/3 个 LMS 子串，
		// 我们通过重复 SLSLSLSLSLSL 模式来安排
		// 像上面的 ababab...，但我们还必须安排
		// 大量不同的 LMS 子串。
		// 我们使用这个模式：
		// 1 255 1 254 1 253 1 ... 1 2 1 255 2 254 2 253 2 252 2 ...
		// 这给出大约 2¹⁵ 个不同的 LMS 子串。
		// 我们需要至少重复一个子串，
		// 否则递归可以被完全绕过。
		x := make([]byte, 100000, 100001)
		lo := byte(1)
		hi := byte(255)
		for i := range x {
			if i%2 == 0 {
				x[i] = lo
			} else {
				x[i] = hi
				hi--
				if hi <= lo {
					lo++
					if lo == 0 {
						lo = 1
					}
					hi = 255
				}
			}
		}
		x[:cap(x)][len(x)] = 0 // for sais.New
		testSA(t, x, build)
	})

	t.Run("exhaustive2", func(t *testing.T) {
		// {0,1} 上所有长度最多 21 的输入。
		// 在我的笔记本上运行大约 10 秒。
		x := make([]byte, 30)
		numFail := 0
		for n := 0; n <= 21; n++ {
			if n > 12 && testing.Short() {
				break
			}
			x[n] = 0 // for sais.New
			testRec(t, x[:n], 0, 2, &numFail, build)
		}
	})

	t.Run("exhaustive3", func(t *testing.T) {
		// {0,1,2} 上所有长度最多 14 的输入。
		// 在我的笔记本上运行大约 10 秒。
		x := make([]byte, 30)
		numFail := 0
		for n := 0; n <= 14; n++ {
			if n > 8 && testing.Short() {
				break
			}
			x[n] = 0 // for sais.New
			testRec(t, x[:n], 0, 3, &numFail, build)
		}
	})
}

// testRec 用 [1,max] 中值的所有可能组合填充 x[i:]，
// 然后对每个组合调用 testSA(t, x, build)。
func testRec(t *testing.T, x []byte, i, max int, numFail *int, build func([]byte) []int) {
	if i < len(x) {
		for x[i] = 1; x[i] <= byte(max); x[i]++ {
			testRec(t, x, i+1, max, numFail, build)
		}
		return
	}

	if !testSA(t, x, build) {
		*numFail++
		if *numFail >= 10 {
			t.Errorf("stopping after %d failures", *numFail)
			t.FailNow()
		}
	}
}

// testSA 在输入 x 上测试后缀数组构建函数。
// 它构建后缀数组，然后检查它是否正确。
func testSA(t *testing.T, x []byte, build func([]byte) []int) bool {
	defer func() {
		if e := recover(); e != nil {
			t.Logf("build %v", x)
			panic(e)
		}
	}()
	sa := build(x)
	if len(sa) != len(x) {
		t.Errorf("build %v: len(sa) = %d, want %d", x, len(sa), len(x))
		return false
	}
	for i := 0; i+1 < len(sa); i++ {
		if sa[i] < 0 || sa[i] >= len(x) || sa[i+1] < 0 || sa[i+1] >= len(x) {
			t.Errorf("build %s: sa out of range: %v\n", x, sa)
			return false
		}
		if bytes.Compare(x[sa[i]:], x[sa[i+1]:]) >= 0 {
			t.Errorf("build %v -> %v\nsa[%d:] = %d,%d out of order", x, sa, i, sa[i], sa[i+1])
			return false
		}
	}

	return true
}

var (
	benchdata = make([]byte, 1e6)
	benchrand = make([]byte, 1e6)
)

// 在所有可能的输入中，随机字节的子字符串重复最少，
// 重复字节的最多。对于大多数算法，
// 每个输入的运行时间将介于这两者之间。
func benchmarkNew(b *testing.B, random bool) {
	b.ReportAllocs()
	b.StopTimer()
	data := benchdata
	if random {
		data = benchrand
		if data[0] == 0 {
			for i := range data {
				data[i] = byte(rand.Intn(256))
			}
		}
	}
	b.StartTimer()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		New(data)
	}
}

func makeText(name string) ([]byte, error) {
	var data []byte
	switch name {
	case "opticks":
		var err error
		data, err = os.ReadFile("../../testdata/Isaac.Newton-Opticks.txt")
		if err != nil {
			return nil, err
		}
	case "go":
		err := filepath.WalkDir("../..", func(path string, info fs.DirEntry, err error) error {
			if err == nil && strings.HasSuffix(path, ".go") && !info.IsDir() {
				file, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				data = append(data, file...)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	case "zero":
		data = make([]byte, 50e6)
	case "rand":
		data = make([]byte, 50e6)
		for i := range data {
			data[i] = byte(rand.Intn(256))
		}
	}
	return data, nil
}

func setBits(bits int) (cleanup func()) {
	if bits == 32 {
		maxData32 = realMaxData32
	} else {
		maxData32 = -1 // 强制使用 64 位代码
	}
	return func() {
		maxData32 = realMaxData32
	}
}

func BenchmarkNew(b *testing.B) {
	for _, text := range []string{"opticks", "go", "zero", "rand"} {
		b.Run("text="+text, func(b *testing.B) {
			data, err := makeText(text)
			if err != nil {
				b.Fatal(err)
			}
			if testing.Short() && len(data) > 5e6 {
				data = data[:5e6]
			}
			for _, size := range []int{100e3, 500e3, 1e6, 5e6, 10e6, 50e6} {
				if len(data) < size {
					continue
				}
				data := data[:size]
				name := fmt.Sprintf("%dK", size/1e3)
				if size >= 1e6 {
					name = fmt.Sprintf("%dM", size/1e6)
				}
				b.Run("size="+name, func(b *testing.B) {
					for _, bits := range []int{32, 64} {
						if ^uint(0) == 0xffffffff && bits == 64 {
							continue
						}
						b.Run(fmt.Sprintf("bits=%d", bits), func(b *testing.B) {
							cleanup := setBits(bits)
							defer cleanup()

							b.SetBytes(int64(len(data)))
							b.ReportAllocs()
							for i := 0; i < b.N; i++ {
								New(data)
							}
						})
					}
				})
			}
		})
	}
}

func BenchmarkSaveRestore(b *testing.B) {
	r := rand.New(rand.NewSource(0x5a77a1)) // 保证始终相同的序列
	data := make([]byte, 1<<20)             // 1MB 要索引的数据
	for i := range data {
		data[i] = byte(r.Intn(256))
	}
	for _, bits := range []int{32, 64} {
		if ^uint(0) == 0xffffffff && bits == 64 {
			continue
		}
		b.Run(fmt.Sprintf("bits=%d", bits), func(b *testing.B) {
			cleanup := setBits(bits)
			defer cleanup()

			b.StopTimer()
			x := New(data)
		size := testSaveRestore(nil, nil, x)       // 验证正确性
		buf := bytes.NewBuffer(make([]byte, size)) // 避免增长
			b.SetBytes(int64(size))
			b.StartTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf.Reset()
				if err := x.Write(buf); err != nil {
					b.Fatal(err)
				}
				var y Index
				if err := y.Read(buf); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
