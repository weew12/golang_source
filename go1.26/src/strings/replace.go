// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strings

import (
	"io"
	"sync"
)

// Replacer 将一组字符串替换为对应的替换字符串。
// 它支持多个 goroutine 并发安全使用。
type Replacer struct {
	once   sync.Once // 保护 buildOnce 方法
	r      replacer
	oldnew []string
}

// replacer 是替换算法需要实现的接口。
type replacer interface {
	Replace(s string) string
	WriteString(w io.Writer, s string) (n int, err error)
}

// NewReplacer 从一组新旧字符串对返回一个新的 [Replacer]。
// 替换按照它们在目标字符串中出现的顺序执行，不进行重叠匹配。
// 旧字符串的比较按照参数顺序进行。
//
// 若传入奇数个参数，NewReplacer 会触发 panic。
func NewReplacer(oldnew ...string) *Replacer {
	if len(oldnew)%2 == 1 {
		panic("strings.NewReplacer: odd argument count")
	}
	return &Replacer{oldnew: append([]string(nil), oldnew...)}
}

func (r *Replacer) buildOnce() {
	r.r = r.build()
	r.oldnew = nil
}

func (b *Replacer) build() replacer {
	oldnew := b.oldnew
	if len(oldnew) == 2 && len(oldnew[0]) > 1 {
		return makeSingleStringReplacer(oldnew[0], oldnew[1])
	}

	allNewBytes := true
	for i := 0; i < len(oldnew); i += 2 {
		if len(oldnew[i]) != 1 {
			return makeGenericReplacer(oldnew)
		}
		if len(oldnew[i+1]) != 1 {
			allNewBytes = false
		}
	}

	if allNewBytes {
		r := byteReplacer{}
		for i := range r {
			r[i] = byte(i)
		}
		// 旧->新映射的第一次出现优先于
		// 其他具有相同旧字符串的映射。
		for i := len(oldnew) - 2; i >= 0; i -= 2 {
			o := oldnew[i][0]
			n := oldnew[i+1][0]
			r[o] = n
		}
		return &r
	}

	r := byteStringReplacer{toReplace: make([]string, 0, len(oldnew)/2)}
	// 旧->新映射的第一次出现优先于
	// 其他具有相同旧字符串的映射。
	for i := len(oldnew) - 2; i >= 0; i -= 2 {
		o := oldnew[i][0]
		n := oldnew[i+1]
		// 避免多次计数重复项。
		if r.replacements[o] == nil {
			// 需使用 string([]byte{o}) 而非 string(o)，
			// 以避免 o 的 utf8 编码。
			// 例如，byte(150) 会生成长度为 2 的字符串。
			r.toReplace = append(r.toReplace, string([]byte{o}))
		}
		r.replacements[o] = []byte(n)

	}
	return &r
}

// Replace 返回 s 的副本，其中所有替换均已执行。
func (r *Replacer) Replace(s string) string {
	r.once.Do(r.buildOnce)
	return r.r.Replace(s)
}

// WriteString 将 s 写入 w，其中所有替换均已执行。
func (r *Replacer) WriteString(w io.Writer, s string) (n int, err error) {
	r.once.Do(r.buildOnce)
	return r.r.WriteString(w, s)
}

// trieNode 是用于优先级键/值对的查找字典树中的节点。
// 键和值可以为空。例如，包含键 "ax"、"ay"、"bcbc"、"x" 和 "xy" 的字典树可能有八个节点：
//
//	n0  -
//	n1  a-
//	n2  .x+
//	n3  .y+
//	n4  b-
//	n5  .cbc+
//	n6  x+
//	n7  .y+
//
// n0 是根节点，其子节点为 n1、n4 和 n6；n1 的子节点为 n2 和 n3；
// n4 的子节点为 n5；n6 的子节点为 n7。节点 n0、n1 和 n4（标记为后缀 "-"）是部分键，
// 节点 n2、n3、n5、n6 和 n7（标记为后缀 "+"）是完整键。
type trieNode struct {
	// value 是字典树节点键/值对的值。若该节点不是完整键，则为空。
	value string
	// priority 是字典树节点键/值对的优先级（越高越重要）；
	// 键不一定按最短或最长优先匹配。若该节点是完整键，优先级为正，否则为零。
	// 在上述示例中，正/零优先级分别用后缀 "+" 或 "-" 标记。
	priority int

	// 一个字典树节点可以有零个、一个或多个子节点：
	//  * 若剩余字段为零，则没有子节点。
	//  * 若 prefix 和 next 非零，则 next 中有一个子节点。
	//  * 若 table 非零，则它定义了所有子节点。
	//
	// 当只有一个子节点时，前缀优先于表，但根节点始终使用表以提高查找效率。

	// prefix 是该字典树节点与下一个节点之间的键差异。
	// 在上述示例中，节点 n4 的 prefix 为 "cbc"，n4 的下一个节点为 n5。
	// 节点 n5 没有子节点，因此 prefix、next 和 table 字段均为零。
	prefix string
	next   *trieNode

	// table 是一个查找表，通过键中的下一个字节索引，
	// 该字节需先通过 genericReplacer.mapping 重映射以创建密集索引。
	// 在上述示例中，键仅使用 'a'、'b'、'c'、'x' 和 'y'，它们分别重映射为 0、1、2、3 和 4。
	// 所有其他字节重映射为 5，genericReplacer.tableSize 将为 5。
	// 节点 n0 的 table 将为 []*trieNode{ 0:n1, 1:n4, 3:n6 }，其中 0、1 和 3 是重映射后的 'a'、'b' 和 'x'。
	table []*trieNode
}

func (t *trieNode) add(key, val string, priority int, r *genericReplacer) {
	if key == "" {
		if t.priority == 0 {
			t.value = val
			t.priority = priority
		}
		return
	}

	if t.prefix != "" {
		// 需要在多个节点间拆分前缀。
		var n int // 最长公共前缀的长度
		for ; n < len(t.prefix) && n < len(key); n++ {
			if t.prefix[n] != key[n] {
				break
			}
		}
		if n == len(t.prefix) {
			t.next.add(key[n:], val, priority, r)
		} else if n == 0 {
			// 第一个字节不同，在此处启动新的查找表。
			// 查找当前 t.prefix[0] 将指向 prefixNode，
			// 查找 key[0] 将指向 keyNode。
			var prefixNode *trieNode
			if len(t.prefix) == 1 {
				prefixNode = t.next
			} else {
				prefixNode = &trieNode{
					prefix: t.prefix[1:],
					next:   t.next,
				}
			}
			keyNode := new(trieNode)
			t.table = make([]*trieNode, r.tableSize)
			t.table[r.mapping[t.prefix[0]]] = prefixNode
			t.table[r.mapping[key[0]]] = keyNode
			t.prefix = ""
			t.next = nil
			keyNode.add(key[1:], val, priority, r)
		} else {
			// 在前缀的公共部分后插入新节点。
			next := &trieNode{
				prefix: t.prefix[n:],
				next:   t.next,
			}
			t.prefix = t.prefix[:n]
			t.next = next
			next.add(key[n:], val, priority, r)
		}
	} else if t.table != nil {
		// 插入现有表。
		m := r.mapping[key[0]]
		if t.table[m] == nil {
			t.table[m] = new(trieNode)
		}
		t.table[m].add(key[1:], val, priority, r)
	} else {
		t.prefix = key
		t.next = new(trieNode)
		t.next.add("", val, priority, r)
	}
}

func (r *genericReplacer) lookup(s string, ignoreRoot bool) (val string, keylen int, found bool) {
	// 沿字典树向下迭代到末尾，获取优先级最高的值和键长。
	bestPriority := 0
	node := &r.root
	n := 0
	for node != nil {
		if node.priority > bestPriority && !(ignoreRoot && node == &r.root) {
			bestPriority = node.priority
			val = node.value
			keylen = n
			found = true
		}

		if s == "" {
			break
		}
		if node.table != nil {
			index := r.mapping[s[0]]
			if int(index) == r.tableSize {
				break
			}
			node = node.table[index]
			s = s[1:]
			n++
		} else if node.prefix != "" && HasPrefix(s, node.prefix) {
			n += len(node.prefix)
			s = s[len(node.prefix):]
			node = node.next
		} else {
			break
		}
	}
	return
}

// genericReplacer 是完全通用的算法。
// 当无法使用更快的算法时，它作为后备方案。
type genericReplacer struct {
	root trieNode
	// tableSize 是字典树节点查找表的大小。它是唯一键字节的数量。
	tableSize int
	// mapping 将键字节映射到 trieNode.table 的密集索引。
	mapping [256]byte
}

func makeGenericReplacer(oldnew []string) *genericReplacer {
	r := new(genericReplacer)
	// 查找每个使用的字节，然后为它们分配索引。
	for i := 0; i < len(oldnew); i += 2 {
		key := oldnew[i]
		for j := 0; j < len(key); j++ {
			r.mapping[key[j]] = 1
		}
	}

	for _, b := range r.mapping {
		r.tableSize += int(b)
	}

	var index byte
	for i, b := range r.mapping {
		if b == 0 {
			r.mapping[i] = byte(r.tableSize)
		} else {
			r.mapping[i] = index
			index++
		}
	}
	// 确保根节点使用查找表（为了性能）。
	r.root.table = make([]*trieNode, r.tableSize)

	for i := 0; i < len(oldnew); i += 2 {
		r.root.add(oldnew[i], oldnew[i+1], len(oldnew)-i, r)
	}
	return r
}

type appendSliceWriter []byte

// Write 写入缓冲区以满足 [io.Writer] 接口。
func (w *appendSliceWriter) Write(p []byte) (int, error) {
	*w = append(*w, p...)
	return len(p), nil
}

// WriteString 写入缓冲区，无需 string->[]byte->string 的内存分配。
func (w *appendSliceWriter) WriteString(s string) (int, error) {
	*w = append(*w, s...)
	return len(s), nil
}

type stringWriter struct {
	w io.Writer
}

func (w stringWriter) WriteString(s string) (int, error) {
	return w.w.Write([]byte(s))
}

func getStringWriter(w io.Writer) io.StringWriter {
	sw, ok := w.(io.StringWriter)
	if !ok {
		sw = stringWriter{w}
	}
	return sw
}

func (r *genericReplacer) Replace(s string) string {
	buf := make(appendSliceWriter, 0, len(s))
	r.WriteString(&buf, s)
	return string(buf)
}

func (r *genericReplacer) WriteString(w io.Writer, s string) (n int, err error) {
	sw := getStringWriter(w)
	var last, wn int
	var prevMatchEmpty bool
	for i := 0; i <= len(s); {
		// 快速路径：s[i] 不是任何模式的前缀。
		if i != len(s) && r.root.priority == 0 {
			index := int(r.mapping[s[i]])
			if index == r.tableSize || r.root.table[index] == nil {
				i++
				continue
			}
		}

		// 仅当上一次循环找到空匹配时，才忽略空匹配。
		val, keylen, match := r.lookup(s[i:], prevMatchEmpty)
		prevMatchEmpty = match && keylen == 0
		if match {
			wn, err = sw.WriteString(s[last:i])
			n += wn
			if err != nil {
				return
			}
			wn, err = sw.WriteString(val)
			n += wn
			if err != nil {
				return
			}
			i += keylen
			last = i
			continue
		}
		i++
	}
	if last != len(s) {
		wn, err = sw.WriteString(s[last:])
		n += wn
	}
	return
}

// singleStringReplacer 是当只有一个字符串需要替换（且该字符串长度超过一个字节）时使用的实现。
type singleStringReplacer struct {
	finder *stringFinder
	// value 是找到模式时替换它的新字符串。
	value string
}

func makeSingleStringReplacer(pattern string, value string) *singleStringReplacer {
	return &singleStringReplacer{finder: makeStringFinder(pattern), value: value}
}

func (r *singleStringReplacer) Replace(s string) string {
	var buf Builder
	i, matched := 0, false
	for {
		match := r.finder.next(s[i:])
		if match == -1 {
			break
		}
		matched = true
		buf.Grow(match + len(r.value))
		buf.WriteString(s[i : i+match])
		buf.WriteString(r.value)
		i += match + len(r.finder.pattern)
	}
	if !matched {
		return s
	}
	buf.WriteString(s[i:])
	return buf.String()
}

func (r *singleStringReplacer) WriteString(w io.Writer, s string) (n int, err error) {
	sw := getStringWriter(w)
	var i, wn int
	for {
		match := r.finder.next(s[i:])
		if match == -1 {
			break
		}
		wn, err = sw.WriteString(s[i : i+match])
		n += wn
		if err != nil {
			return
		}
		wn, err = sw.WriteString(r.value)
		n += wn
		if err != nil {
			return
		}
		i += match + len(r.finder.pattern)
	}
	wn, err = sw.WriteString(s[i:])
	n += wn
	return
}

// byteReplacer 是当所有"旧"和"新"值均为单个 ASCII 字节时使用的实现。
// 该数组包含按旧字节索引的替换字节。
type byteReplacer [256]byte

func (r *byteReplacer) Replace(s string) string {
	var buf []byte // 延迟分配
	for i := 0; i < len(s); i++ {
		b := s[i]
		if r[b] != b {
			if buf == nil {
				buf = []byte(s)
			}
			buf[i] = r[b]
		}
	}
	if buf == nil {
		return s
	}
	return string(buf)
}

func (r *byteReplacer) WriteString(w io.Writer, s string) (n int, err error) {
	sw := getStringWriter(w)
	last := 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		if r[b] == b {
			continue
		}
		if last != i {
			wn, err := sw.WriteString(s[last:i])
			n += wn
			if err != nil {
				return n, err
			}
		}
		last = i + 1
		nw, err := w.Write(r[b : int(b)+1])
		n += nw
		if err != nil {
			return n, err
		}
	}
	if last != len(s) {
		nw, err := sw.WriteString(s[last:])
		n += nw
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// byteStringReplacer 是当所有"旧"值均为单个 ASCII 字节但"新"值大小不同时使用的实现。
type byteStringReplacer struct {
	// replacements 包含按旧字节索引的替换字节切片。
	// nil []byte 表示不应替换该旧字节。
	replacements [256][]byte
	// toReplace 保存要替换的字节列表。根据 toReplace 的长度
	// 和目标字符串的长度，使用 Count 或简单循环可能更快。
	// 我们将单个字节存储为字符串，因为 Count 接受字符串。
	toReplace []string
}

// countCutOff 控制字符串长度与替换数量的比率，
// 当达到该比率时，(*byteStringReplacer).Replace 会切换算法。
// 对于字符串长度与替换数量比率高于该值的字符串，
// 我们对 toReplace 中的每个替换调用 Count。
// 对于比率较低的字符串，由于 Count 的开销，我们使用简单循环。
// countCutOff 是一个经验确定的开销乘数。
// TODO(tocarip) 一旦我们有基于寄存器的 abi/中栈内联，重新审视此问题。
const countCutOff = 8

func (r *byteStringReplacer) Replace(s string) string {
	newSize := len(s)
	anyChanges := false
	// 使用 Count 更快吗？
	if len(r.toReplace)*countCutOff <= len(s) {
		for _, x := range r.toReplace {
			if c := Count(s, x); c != 0 {
				// -1 是因为我们用 len(replacements[b]) 个字节替换 1 个字节。
				newSize += c * (len(r.replacements[x[0]]) - 1)
				anyChanges = true
			}

		}
	} else {
		for i := 0; i < len(s); i++ {
			b := s[i]
			if r.replacements[b] != nil {
				// 有关 -1 的解释见上文
				newSize += len(r.replacements[b]) - 1
				anyChanges = true
			}
		}
	}
	if !anyChanges {
		return s
	}
	buf := make([]byte, newSize)
	j := 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		if r.replacements[b] != nil {
			j += copy(buf[j:], r.replacements[b])
		} else {
			buf[j] = b
			j++
		}
	}
	return string(buf)
}

func (r *byteStringReplacer) WriteString(w io.Writer, s string) (n int, err error) {
	sw := getStringWriter(w)
	last := 0
	for i := 0; i < len(s); i++ {
		b := s[i]
		if r.replacements[b] == nil {
			continue
		}
		if last != i {
			nw, err := sw.WriteString(s[last:i])
			n += nw
			if err != nil {
				return n, err
			}
		}
		last = i + 1
		nw, err := w.Write(r.replacements[b])
		n += nw
		if err != nil {
			return n, err
		}
	}
	if last != len(s) {
		var nw int
		nw, err = sw.WriteString(s[last:])
		n += nw
	}
	return
}
