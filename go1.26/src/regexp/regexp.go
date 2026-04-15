// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package regexp 实现了正则表达式搜索。
//
// 所接受的正则表达式语法与 Perl、Python 和其他语言使用的通用语法相同。
// 更准确地说，它是 RE2 所接受并在 https://golang.org/s/re2syntax 中描述的语法，
// 但不包括 \C。
// 关于语法概览，请参阅 [regexp/syntax] 包。
//
// 本包提供的正则表达式实现保证在输入大小的线性时间内运行。
// （这是大多数开源正则表达式实现所不保证的属性。）
// 关于此属性的更多信息，请参阅 https://swtch.com/~rsc/regexp/regexp1.html
// 或任何关于自动机理论的书籍。
//
// 所有字符都是 UTF-8 编码的码点。
// 遵循 [utf8.DecodeRune] 的规则，无效 UTF-8 序列中的每个字节
// 都被视为编码了 utf8.RuneError (U+FFFD)。
//
// [Regexp] 有 16 个方法用于匹配正则表达式并标识匹配的文本。
// 它们的名称可由以下正则表达式匹配：
//
//	Find(All)?(String)?(Submatch)?(Index)?
//
// 如果存在 'All'，则该函数匹配整个表达式的连续非重叠匹配。
// 紧邻前一个匹配的空匹配将被忽略。返回值是一个切片，
// 包含对应的非 'All' 函数的连续返回值。这些函数接受一个额外的整数参数 n。
// 如果 n >= 0，函数最多返回 n 个匹配/子匹配；否则，返回全部匹配。
//
// 如果存在 'String'，则参数为字符串；否则为字节切片；
// 返回值会相应调整。
//
// 如果存在 'Submatch'，则返回值是一个切片，标识表达式的连续子匹配。
// 子匹配是正则表达式中带括号的子表达式（也称为捕获组）的匹配，
// 按左括号的顺序从左到右编号。子匹配 0 是整个表达式的匹配，
// 子匹配 1 是第一个带括号子表达式的匹配，依此类推。
//
// 如果存在 'Index'，匹配和子匹配通过输入字符串中的字节索引对来标识：
// result[2*n:2*n+2] 标识第 n 个子匹配的索引。n==0 的索引对标识整个表达式的匹配。
// 如果不存在 'Index'，则匹配通过匹配/子匹配的文本来标识。
// 如果索引为负数或文本为 nil，则表示该子表达式未匹配输入中的任何字符串。
// 对于 'String' 版本，空字符串表示无匹配或匹配了空字符串。
//
// 还有一组方法可以应用于从 [io.RuneReader] 读取的文本：
// [Regexp.MatchReader]、[Regexp.FindReaderIndex]、
// [Regexp.FindReaderSubmatchIndex]。
//
// 此集合可能会增长。请注意，正则表达式匹配可能需要检查匹配返回的文本之外的文本，
// 因此从 [io.RuneReader] 匹配文本的方法在返回之前可能会读取输入中任意远的位置。
//
// （还有一些其他方法不符合此模式。）
package regexp

import (
	"bytes"
	"io"
	"regexp/syntax"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Regexp 是已编译正则表达式的表示。
// Regexp 可以安全地被多个 goroutine 并发使用，
// 但配置方法（如 [Regexp.Longest]）除外。
type Regexp struct {
	expr           string       // 传递给 Compile 的原始表达式
	prog           *syntax.Prog // 编译后的程序
	onepass        *onePassProg // 单遍程序或 nil
	numSubexp      int
	maxBitStateLen int
	subexpNames    []string
	prefix         string         // 非锚定匹配中要求的前缀
	prefixBytes    []byte         // 前缀，作为 []byte
	prefixRune     rune           // 前缀中的第一个 rune
	prefixEnd      uint32         // 前缀中最后一个 rune 的 pc
	mpool          int            // 机器池
	matchcap       int            // 记录的匹配长度大小
	prefixComplete bool           // 前缀就是整个正则表达式
	cond           syntax.EmptyOp // 匹配开始时要求的零宽度条件
	minInputLen    int            // 输入的最小字节长度

	// 此字段可被 Longest 方法修改，
	// 但其他情况下是只读的。
	longest bool // 正则表达式是否偏好最左最长匹配
}

// String 返回用于编译该正则表达式的源文本。
func (re *Regexp) String() string {
	return re.expr
}

// Copy 返回一个从 re 复制的新 [Regexp] 对象。
// 对一个副本调用 [Regexp.Longest] 不会影响另一个副本。
//
// Deprecated: 在早期版本中，在多个 goroutine 中使用 [Regexp] 时，
// 为每个 goroutine 提供各自的副本有助于避免锁竞争。
// 从 Go 1.12 开始，不再需要使用 Copy 来避免锁竞争。
// 如果使用 Copy 的原因是创建两个具有不同 [Regexp.Longest] 设置的副本，
// 则 Copy 仍然是合适的。
func (re *Regexp) Copy() *Regexp {
	re2 := *re
	return &re2
}

// Compile 解析正则表达式，如果成功，返回一个可用于匹配文本的 [Regexp] 对象。
//
// 在匹配文本时，该正则表达式返回在输入中尽可能早开始的匹配（最左匹配），
// 并在这些匹配中选择回溯搜索最先找到的那个。
// 这种所谓的最左优先匹配与 Perl、Python 和其他实现使用的语义相同，
// 尽管本包在实现时没有回溯的开销。
// 对于 POSIX 最左最长匹配，请参阅 [CompilePOSIX]。
func Compile(expr string) (*Regexp, error) {
	return compile(expr, syntax.Perl, false)
}

// CompilePOSIX 类似于 [Compile]，但将正则表达式限制为
// POSIX ERE (egrep) 语法，并将匹配语义改为最左最长匹配。
//
// 也就是说，在匹配文本时，该正则表达式返回在输入中尽可能早开始的匹配（最左匹配），
// 并在这些匹配中选择尽可能长的匹配。
// 这种所谓的最左最长匹配与早期正则表达式实现所使用的以及 POSIX
// 所规定的语义相同。
//
// 然而，可能存在多个最左最长匹配，且具有不同的子匹配选择，
// 在这一点上本包与 POSIX 存在差异。
// 在所有可能的最左最长匹配中，本包选择回溯搜索最先找到的那个，
// 而 POSIX 规定应选择使第一个子表达式长度最大化的匹配，
// 然后是第二个，依此类推从左到右。
// POSIX 规则在计算上是不可行的，甚至定义也不完善。
// 详细信息请参阅 https://swtch.com/~rsc/regexp/regexp2.html#posix。
func CompilePOSIX(expr string) (*Regexp, error) {
	return compile(expr, syntax.POSIX, true)
}

// Longest 使后续搜索优先选择最左最长匹配。
// 也就是说，在匹配文本时，该正则表达式返回在输入中尽可能早开始的匹配（最左匹配），
// 并在这些匹配中选择尽可能长的匹配。
// 此方法会修改 [Regexp]，不能与任何其他方法并发调用。
func (re *Regexp) Longest() {
	re.longest = true
}

func compile(expr string, mode syntax.Flags, longest bool) (*Regexp, error) {
	re, err := syntax.Parse(expr, mode)
	if err != nil {
		return nil, err
	}
	maxCap := re.MaxCap()
	capNames := re.CapNames()

	re = re.Simplify()
	prog, err := syntax.Compile(re)
	if err != nil {
		return nil, err
	}
	matchcap := prog.NumCap
	if matchcap < 2 {
		matchcap = 2
	}
	regexp := &Regexp{
		expr:        expr,
		prog:        prog,
		onepass:     compileOnePass(prog),
		numSubexp:   maxCap,
		subexpNames: capNames,
		cond:        prog.StartCond(),
		longest:     longest,
		matchcap:    matchcap,
		minInputLen: minInputLen(re),
	}
	if regexp.onepass == nil {
		regexp.prefix, regexp.prefixComplete = prog.Prefix()
		regexp.maxBitStateLen = maxBitStateLen(prog)
	} else {
		regexp.prefix, regexp.prefixComplete, regexp.prefixEnd = onePassPrefix(prog)
	}
	if regexp.prefix != "" {
		// TODO(rsc): 通过向 bytes 包添加
		// IndexString 来消除此分配。
		regexp.prefixBytes = []byte(regexp.prefix)
		regexp.prefixRune, _ = utf8.DecodeRuneInString(regexp.prefix)
	}

	n := len(prog.Inst)
	i := 0
	for matchSize[i] != 0 && matchSize[i] < n {
		i++
	}
	regexp.mpool = i

	return regexp, nil
}

// 在 (*Regexp).doExecute 期间使用的 *machine 池，
// 按执行队列的大小分隔。
// matchPool[i] 的机器具有 matchSize[i] 的队列大小。
// 在 64 位系统上每个队列条目为 16 字节，
// 因此 matchPool[0] 有 16*2*128 = 4kB 的队列，以此类推。
// 最后一个 matchPool 是用于非常大队列的兜底池。
var (
	matchSize = [...]int{128, 512, 2048, 16384, 0}
	matchPool [len(matchSize)]sync.Pool
)

// get 返回一个用于匹配 re 的机器。
// 如果可能，它使用 re 的机器缓存，以避免
// 不必要的分配。
func (re *Regexp) get() *machine {
	m, ok := matchPool[re.mpool].Get().(*machine)
	if !ok {
		m = new(machine)
	}
	m.re = re
	m.p = re.prog
	if cap(m.matchcap) < re.matchcap {
		m.matchcap = make([]int, re.matchcap)
		for _, t := range m.pool {
			t.cap = make([]int, re.matchcap)
		}
	}

	// 如果需要则分配队列。
	// 或者为"大"匹配池重新分配。
	n := matchSize[re.mpool]
	if n == 0 { // 大池
		n = len(re.prog.Inst)
	}
	if len(m.q0.sparse) < n {
		m.q0 = queue{make([]uint32, n), make([]entry, 0, n)}
		m.q1 = queue{make([]uint32, n), make([]entry, 0, n)}
	}
	return m
}

// put 将机器归还到正确的机器池。
func (re *Regexp) put(m *machine) {
	m.re = nil
	m.p = nil
	m.inputs.clear()
	matchPool[re.mpool].Put(m)
}

// minInputLen 遍历正则表达式以找到任何可匹配输入的最小长度。
func minInputLen(re *syntax.Regexp) int {
	switch re.Op {
	default:
		return 0
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL, syntax.OpCharClass:
		return 1
	case syntax.OpLiteral:
		l := 0
		for _, r := range re.Rune {
			if r == utf8.RuneError {
				l++
			} else {
				l += utf8.RuneLen(r)
			}
		}
		return l
	case syntax.OpCapture, syntax.OpPlus:
		return minInputLen(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min * minInputLen(re.Sub[0])
	case syntax.OpConcat:
		l := 0
		for _, sub := range re.Sub {
			l += minInputLen(sub)
		}
		return l
	case syntax.OpAlternate:
		l := minInputLen(re.Sub[0])
		var lnext int
		for _, sub := range re.Sub[1:] {
			lnext = minInputLen(sub)
			if lnext < l {
				l = lnext
			}
		}
		return l
	}
}

// MustCompile 类似于 [Compile]，但在表达式无法解析时会 panic。
// 它简化了保存已编译正则表达式的全局变量的安全初始化。
func MustCompile(str string) *Regexp {
	regexp, err := Compile(str)
	if err != nil {
		panic(`regexp: Compile(` + quote(str) + `): ` + err.Error())
	}
	return regexp
}

// MustCompilePOSIX 类似于 [CompilePOSIX]，但在表达式无法解析时会 panic。
// 它简化了保存已编译正则表达式的全局变量的安全初始化。
func MustCompilePOSIX(str string) *Regexp {
	regexp, err := CompilePOSIX(str)
	if err != nil {
		panic(`regexp: CompilePOSIX(` + quote(str) + `): ` + err.Error())
	}
	return regexp
}

func quote(s string) string {
	if strconv.CanBackquote(s) {
		return "`" + s + "`"
	}
	return strconv.Quote(s)
}

// NumSubexp 返回此 [Regexp] 中带括号的子表达式的数量。
func (re *Regexp) NumSubexp() int {
	return re.numSubexp
}

// SubexpNames 返回此 [Regexp] 中带括号的子表达式的名称。
// 第一个子表达式的名称是 names[1]，
// 因此如果 m 是一个匹配切片，m[i] 的名称就是 SubexpNames()[i]。
// 由于 Regexp 整体不能命名，names[0] 总是空字符串。
// 返回的切片不应被修改。
func (re *Regexp) SubexpNames() []string {
	return re.subexpNames
}

// SubexpIndex 返回具有给定名称的第一个子表达式的索引，
// 如果没有该名称的子表达式则返回 -1。
//
// 注意，多个子表达式可以使用相同的名称，例如
// (?P<bob>a+)(?P<bob>b+) 声明了两个名为 "bob" 的子表达式。
// 在这种情况下，SubexpIndex 返回正则表达式中最左边的此类子表达式的索引。
func (re *Regexp) SubexpIndex(name string) int {
	if name != "" {
		for i, s := range re.subexpNames {
			if name == s {
				return i
			}
		}
	}
	return -1
}

const endOfText rune = -1

// input 抽象了输入文本的不同表示形式。它提供
// 单字符前瞻。
type input interface {
	step(pos int) (r rune, width int) // 前进一个 rune
	canCheckPrefix() bool             // 是否可以在不丢失信息的情况下前瞻？
	hasPrefix(re *Regexp) bool
	index(re *Regexp, pos int) int
	context(pos int) lazyFlag
}

// inputString 扫描一个字符串。
type inputString struct {
	str string
}

func (i *inputString) step(pos int) (rune, int) {
	if pos < len(i.str) {
		return utf8.DecodeRuneInString(i.str[pos:])
	}
	return endOfText, 0
}

func (i *inputString) canCheckPrefix() bool {
	return true
}

func (i *inputString) hasPrefix(re *Regexp) bool {
	return strings.HasPrefix(i.str, re.prefix)
}

func (i *inputString) index(re *Regexp, pos int) int {
	return strings.Index(i.str[pos:], re.prefix)
}

func (i *inputString) context(pos int) lazyFlag {
	r1, r2 := endOfText, endOfText
	// 0 < pos && pos <= len(i.str)
	if uint(pos-1) < uint(len(i.str)) {
		r1, _ = utf8.DecodeLastRuneInString(i.str[:pos])
	}
	// 0 <= pos && pos < len(i.str)
	if uint(pos) < uint(len(i.str)) {
		r2, _ = utf8.DecodeRuneInString(i.str[pos:])
	}
	return newLazyFlag(r1, r2)
}

// inputBytes 扫描一个字节切片。
type inputBytes struct {
	str []byte
}

func (i *inputBytes) step(pos int) (rune, int) {
	if pos < len(i.str) {
		return utf8.DecodeRune(i.str[pos:])
	}
	return endOfText, 0
}

func (i *inputBytes) canCheckPrefix() bool {
	return true
}

func (i *inputBytes) hasPrefix(re *Regexp) bool {
	return bytes.HasPrefix(i.str, re.prefixBytes)
}

func (i *inputBytes) index(re *Regexp, pos int) int {
	return bytes.Index(i.str[pos:], re.prefixBytes)
}

func (i *inputBytes) context(pos int) lazyFlag {
	r1, r2 := endOfText, endOfText
	// 0 < pos && pos <= len(i.str)
	if uint(pos-1) < uint(len(i.str)) {
		r1, _ = utf8.DecodeLastRune(i.str[:pos])
	}
	// 0 <= pos && pos < len(i.str)
	if uint(pos) < uint(len(i.str)) {
		r2, _ = utf8.DecodeRune(i.str[pos:])
	}
	return newLazyFlag(r1, r2)
}

// inputReader 扫描一个 RuneReader。
type inputReader struct {
	r     io.RuneReader
	atEOT bool
	pos   int
}

func (i *inputReader) step(pos int) (rune, int) {
	if !i.atEOT && pos != i.pos {
		return endOfText, 0

	}
	r, w, err := i.r.ReadRune()
	if err != nil {
		i.atEOT = true
		return endOfText, 0
	}
	i.pos += w
	return r, w
}

func (i *inputReader) canCheckPrefix() bool {
	return false
}

func (i *inputReader) hasPrefix(re *Regexp) bool {
	return false
}

func (i *inputReader) index(re *Regexp, pos int) int {
	return -1
}

func (i *inputReader) context(pos int) lazyFlag {
	return 0 // 未使用
}

// LiteralPrefix 返回正则表达式 re 的任何匹配都必须以其开头的字面字符串。
// 如果该字面字符串构成了整个正则表达式，则返回布尔值 true。
func (re *Regexp) LiteralPrefix() (prefix string, complete bool) {
	return re.prefix, re.prefixComplete
}

// MatchReader 报告 [io.RuneReader] 返回的文本中是否包含正则表达式 re 的任何匹配。
func (re *Regexp) MatchReader(r io.RuneReader) bool {
	return re.doMatch(r, nil, "")
}

// MatchString 报告字符串 s 中是否包含正则表达式 re 的任何匹配。
func (re *Regexp) MatchString(s string) bool {
	return re.doMatch(nil, nil, s)
}

// Match 报告字节切片 b 中是否包含正则表达式 re 的任何匹配。
func (re *Regexp) Match(b []byte) bool {
	return re.doMatch(nil, b, "")
}

// MatchReader 报告 [io.RuneReader] 返回的文本中是否包含正则表达式 pattern 的任何匹配。
// 更复杂的查询需要使用 [Compile] 和完整的 [Regexp] 接口。
func MatchReader(pattern string, r io.RuneReader) (matched bool, err error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchReader(r), nil
}

// MatchString 报告字符串 s 中是否包含正则表达式 pattern 的任何匹配。
// 更复杂的查询需要使用 [Compile] 和完整的 [Regexp] 接口。
func MatchString(pattern string, s string) (matched bool, err error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

// Match 报告字节切片 b 中是否包含正则表达式 pattern 的任何匹配。
// 更复杂的查询需要使用 [Compile] 和完整的 [Regexp] 接口。
func Match(pattern string, b []byte) (matched bool, err error) {
	re, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.Match(b), nil
}

// ReplaceAllString 返回 src 的副本，将 [Regexp] 的匹配替换为替换字符串 repl。
// 在 repl 中，$ 符号按照 [Regexp.Expand] 中的规则进行解释。
func (re *Regexp) ReplaceAllString(src, repl string) string {
	n := 2
	if strings.Contains(repl, "$") {
		n = 2 * (re.numSubexp + 1)
	}
	b := re.replaceAll(nil, src, n, func(dst []byte, match []int) []byte {
		return re.expand(dst, repl, nil, src, match)
	})
	return string(b)
}

// ReplaceAllLiteralString 返回 src 的副本，将 [Regexp] 的匹配替换为替换字符串 repl。
// 替换字符串 repl 被直接替换，不使用 [Regexp.Expand]。
func (re *Regexp) ReplaceAllLiteralString(src, repl string) string {
	return string(re.replaceAll(nil, src, 2, func(dst []byte, match []int) []byte {
		return append(dst, repl...)
	}))
}

// ReplaceAllStringFunc 返回 src 的副本，其中 [Regexp] 的所有匹配都被函数 repl
// 应用于匹配子串后的返回值所替换。repl 返回的替换值被直接替换，
// 不使用 [Regexp.Expand]。
func (re *Regexp) ReplaceAllStringFunc(src string, repl func(string) string) string {
	b := re.replaceAll(nil, src, 2, func(dst []byte, match []int) []byte {
		return append(dst, repl(src[match[0]:match[1]])...)
	})
	return string(b)
}

func (re *Regexp) replaceAll(bsrc []byte, src string, nmatch int, repl func(dst []byte, m []int) []byte) []byte {
	lastMatchEnd := 0 // 最近一次匹配的结束位置
	searchPos := 0    // 下次查找匹配的位置
	var buf []byte
	var endPos int
	if bsrc != nil {
		endPos = len(bsrc)
	} else {
		endPos = len(src)
	}
	if nmatch > re.prog.NumCap {
		nmatch = re.prog.NumCap
	}

	var dstCap [2]int
	for searchPos <= endPos {
		a := re.doExecute(nil, bsrc, src, searchPos, nmatch, dstCap[:0])
		if len(a) == 0 {
			break // 没有更多匹配
		}

		// 复制此匹配之前的未匹配字符。
		if bsrc != nil {
			buf = append(buf, bsrc[lastMatchEnd:a[0]]...)
		} else {
			buf = append(buf, src[lastMatchEnd:a[0]]...)
		}

		// 现在插入替换字符串的副本，但不是针对紧接着
		// 另一个匹配之后的空字符串匹配。
		// （否则，对于同时匹配空字符串和非空字符串的
		// 模式，我们会得到双重替换。）
		if a[1] > lastMatchEnd || a[0] == 0 {
			buf = repl(buf, a)
		}
		lastMatchEnd = a[1]

		// 越过此匹配；始终至少前进一个字符。
		var width int
		if bsrc != nil {
			_, width = utf8.DecodeRune(bsrc[searchPos:])
		} else {
			_, width = utf8.DecodeRuneInString(src[searchPos:])
		}
		if searchPos+width > a[1] {
			searchPos += width
		} else if searchPos+1 > a[1] {
			// 此子句仅在输入字符串末尾时需要。
			// 在这种情况下，DecodeRuneInString 返回 width=0。
			searchPos++
		} else {
			searchPos = a[1]
		}
	}

	// 复制最后一次匹配之后的未匹配字符。
	if bsrc != nil {
		buf = append(buf, bsrc[lastMatchEnd:]...)
	} else {
		buf = append(buf, src[lastMatchEnd:]...)
	}

	return buf
}

// ReplaceAll 返回 src 的副本，将 [Regexp] 的匹配替换为替换文本 repl。
// 在 repl 中，$ 符号按照 [Regexp.Expand] 中的规则进行解释。
func (re *Regexp) ReplaceAll(src, repl []byte) []byte {
	n := 2
	if bytes.IndexByte(repl, '$') >= 0 {
		n = 2 * (re.numSubexp + 1)
	}
	srepl := ""
	b := re.replaceAll(src, "", n, func(dst []byte, match []int) []byte {
		if len(srepl) != len(repl) {
			srepl = string(repl)
		}
		return re.expand(dst, srepl, src, "", match)
	})
	return b
}

// ReplaceAllLiteral 返回 src 的副本，将 [Regexp] 的匹配替换为替换字节 repl。
// 替换字节 repl 被直接替换，不使用 [Regexp.Expand]。
func (re *Regexp) ReplaceAllLiteral(src, repl []byte) []byte {
	return re.replaceAll(src, "", 2, func(dst []byte, match []int) []byte {
		return append(dst, repl...)
	})
}

// ReplaceAllFunc 返回 src 的副本，其中 [Regexp] 的所有匹配都被函数 repl
// 应用于匹配字节切片后的返回值所替换。repl 返回的替换值被直接替换，
// 不使用 [Regexp.Expand]。
func (re *Regexp) ReplaceAllFunc(src []byte, repl func([]byte) []byte) []byte {
	return re.replaceAll(src, "", 2, func(dst []byte, match []int) []byte {
		return append(dst, repl(src[match[0]:match[1]])...)
	})
}

// special 函数使用的位图，用于检查字符是否需要转义。
var specialBytes [16]byte

// special 报告字节 b 是否需要被 QuoteMeta 转义。
func special(b byte) bool {
	return b < utf8.RuneSelf && specialBytes[b%16]&(1<<(b/16)) != 0
}

func init() {
	for _, b := range []byte(`\.+*?()|[]{}^$`) {
		specialBytes[b%16] |= 1 << (b / 16)
	}
}

// QuoteMeta 返回一个字符串，其中转义了参数文本中所有正则表达式元字符；
// 返回的字符串是一个匹配该字面文本的正则表达式。
func QuoteMeta(s string) string {
	// 字节循环是正确的，因为所有元字符都是 ASCII。
	var i int
	for i = 0; i < len(s); i++ {
		if special(s[i]) {
			break
		}
	}
	// 未找到元字符，返回原始字符串。
	if i >= len(s) {
		return s
	}

	b := make([]byte, 2*len(s)-i)
	copy(b, s[:i])
	j := i
	for ; i < len(s); i++ {
		if special(s[i]) {
			b[j] = '\\'
			j++
		}
		b[j] = s[i]
		j++
	}
	return string(b[:j])
}

// 程序中的捕获值数量可能对应比正则表达式中
// 更少的捕获表达式。
// 例如，"(a){0}" 变成一个空程序，所以
// 程序中的最大捕获值为 0，但我们需要为 \1 返回
// 一个表达式。pad 根据需要向切片 a 追加 -1。
func (re *Regexp) pad(a []int) []int {
	if a == nil {
		// 无匹配。
		return nil
	}
	n := (1 + re.numSubexp) * 2
	for len(a) < n {
		a = append(a, -1)
	}
	return a
}

// allMatches 最多调用 deliver n 次，
// 传入输入文本中连续匹配的位置。
// 输入文本为 b（如果非 nil），否则为 s。
func (re *Regexp) allMatches(s string, b []byte, n int, deliver func([]int)) {
	var end int
	if b == nil {
		end = len(s)
	} else {
		end = len(b)
	}

	for pos, i, prevMatchEnd := 0, 0, -1; i < n && pos <= end; {
		matches := re.doExecute(nil, b, s, pos, re.prog.NumCap, nil)
		if len(matches) == 0 {
			break
		}

		accept := true
		if matches[1] == pos {
			// 我们找到了一个空匹配。
			if matches[0] == prevMatchEnd {
				// 我们不允许在前一个匹配之后
				// 紧接着出现空匹配，所以忽略它。
				accept = false
			}
			var width int
			if b == nil {
				is := inputString{str: s}
				_, width = is.step(pos)
			} else {
				ib := inputBytes{str: b}
				_, width = ib.step(pos)
			}
			if width > 0 {
				pos += width
			} else {
				pos = end + 1
			}
		} else {
			pos = matches[1]
		}
		prevMatchEnd = matches[1]

		if accept {
			deliver(re.pad(matches))
			i++
		}
	}
}

// Find 返回一个切片，持有正则表达式在 b 中最左匹配的文本。
// 返回值为 nil 表示无匹配。
func (re *Regexp) Find(b []byte) []byte {
	var dstCap [2]int
	a := re.doExecute(nil, b, "", 0, 2, dstCap[:0])
	if a == nil {
		return nil
	}
	return b[a[0]:a[1]:a[1]]
}

// FindIndex 返回一个包含两个整数的切片，定义正则表达式在 b 中最左匹配的位置。
// 匹配本身位于 b[loc[0]:loc[1]]。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindIndex(b []byte) (loc []int) {
	a := re.doExecute(nil, b, "", 0, 2, nil)
	if a == nil {
		return nil
	}
	return a[0:2]
}

// FindString 返回一个字符串，持有正则表达式在 s 中最左匹配的文本。
// 如果没有匹配，返回值为空字符串，
// 但如果正则表达式成功匹配了空字符串，返回值也为空。
// 如果需要区分这些情况，请使用 [Regexp.FindStringIndex] 或 [Regexp.FindStringSubmatch]。
func (re *Regexp) FindString(s string) string {
	var dstCap [2]int
	a := re.doExecute(nil, nil, s, 0, 2, dstCap[:0])
	if a == nil {
		return ""
	}
	return s[a[0]:a[1]]
}

// FindStringIndex 返回一个包含两个整数的切片，定义正则表达式在 s 中最左匹配的位置。
// 匹配本身位于 s[loc[0]:loc[1]]。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindStringIndex(s string) (loc []int) {
	a := re.doExecute(nil, nil, s, 0, 2, nil)
	if a == nil {
		return nil
	}
	return a[0:2]
}

// FindReaderIndex 返回一个包含两个整数的切片，定义正则表达式在从 [io.RuneReader]
// 读取的文本中最左匹配的位置。匹配文本在输入流中的字节偏移量为
// loc[0] 到 loc[1]-1。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindReaderIndex(r io.RuneReader) (loc []int) {
	a := re.doExecute(r, nil, "", 0, 2, nil)
	if a == nil {
		return nil
	}
	return a[0:2]
}

// FindSubmatch 返回一个切片的切片，持有正则表达式在 b 中最左匹配的文本
// 以及其子表达式的匹配（如果有），如包注释中 'Submatch' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindSubmatch(b []byte) [][]byte {
	var dstCap [4]int
	a := re.doExecute(nil, b, "", 0, re.prog.NumCap, dstCap[:0])
	if a == nil {
		return nil
	}
	ret := make([][]byte, 1+re.numSubexp)
	for i := range ret {
		if 2*i < len(a) && a[2*i] >= 0 {
			ret[i] = b[a[2*i]:a[2*i+1]:a[2*i+1]]
		}
	}
	return ret
}

// Expand 将 template 追加到 dst 并返回结果；在追加过程中，
// Expand 用从 src 中提取的对应匹配替换模板中的变量。
// match 切片应该是由 [Regexp.FindSubmatchIndex] 返回的。
//
// 在模板中，变量由 $name 或 ${name} 形式的子串表示，
// 其中 name 是由字母、数字和下划线组成的非空序列。
// 纯数字名称（如 $1）引用对应索引的子匹配；
// 其他名称引用使用 (?P<name>...) 语法命名的捕获括号。
// 对超出范围或未匹配的索引，或正则表达式中不存在的名称的引用，
// 将被替换为空切片。
//
// 在 $name 形式中，name 取尽可能长的匹配：$1x 等价于 ${1x}
// 而非 ${1}x，$10 等价于 ${10} 而非 ${1}0。
//
// 要在输出中插入字面 $ 符号，请在模板中使用 $$。
func (re *Regexp) Expand(dst []byte, template []byte, src []byte, match []int) []byte {
	return re.expand(dst, string(template), src, "", match)
}

// ExpandString 类似于 [Regexp.Expand]，但模板和源是字符串。
// 它追加到并返回一个字节切片，以便让调用代码控制内存分配。
func (re *Regexp) ExpandString(dst []byte, template string, src string, match []int) []byte {
	return re.expand(dst, template, nil, src, match)
}

func (re *Regexp) expand(dst []byte, template string, bsrc []byte, src string, match []int) []byte {
	for len(template) > 0 {
		before, after, ok := strings.Cut(template, "$")
		if !ok {
			break
		}
		dst = append(dst, before...)
		template = after
		if template != "" && template[0] == '$' {
			// 将 $$ 视为 $。
			dst = append(dst, '$')
			template = template[1:]
			continue
		}
		name, num, rest, ok := extract(template)
		if !ok {
			// 格式错误；将 $ 视为原始文本。
			dst = append(dst, '$')
			continue
		}
		template = rest
		if num >= 0 {
			if 2*num+1 < len(match) && match[2*num] >= 0 {
				if bsrc != nil {
					dst = append(dst, bsrc[match[2*num]:match[2*num+1]]...)
				} else {
					dst = append(dst, src[match[2*num]:match[2*num+1]]...)
				}
			}
		} else {
			for i, namei := range re.subexpNames {
				if name == namei && 2*i+1 < len(match) && match[2*i] >= 0 {
					if bsrc != nil {
						dst = append(dst, bsrc[match[2*i]:match[2*i+1]]...)
					} else {
						dst = append(dst, src[match[2*i]:match[2*i+1]]...)
					}
					break
				}
			}
		}
	}
	dst = append(dst, template...)
	return dst
}

// extract 从 str 中的前导 "name" 或 "{name}" 中返回名称。
// （$ 已被调用者移除。）
// 如果是数字，extract 返回设置为该数字的 num；否则 num = -1。
func extract(str string) (name string, num int, rest string, ok bool) {
	if str == "" {
		return
	}
	brace := false
	if str[0] == '{' {
		brace = true
		str = str[1:]
	}
	i := 0
	for i < len(str) {
		rune, size := utf8.DecodeRuneInString(str[i:])
		if !unicode.IsLetter(rune) && !unicode.IsDigit(rune) && rune != '_' {
			break
		}
		i += size
	}
	if i == 0 {
		// 空名称是不允许的
		return
	}
	name = str[:i]
	if brace {
		if i >= len(str) || str[i] != '}' {
			// 缺少关闭大括号
			return
		}
		i++
	}

	// 解析数字。
	num = 0
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || '9' < name[i] || num >= 1e8 {
			num = -1
			break
		}
		num = num*10 + int(name[i]) - '0'
	}
	// 不允许前导零。
	if name[0] == '0' && len(name) > 1 {
		num = -1
	}

	rest = str[i:]
	ok = true
	return
}

// FindSubmatchIndex 返回一个切片，持有标识正则表达式在 b 中最左匹配
// 以及其子表达式的匹配（如果有）的索引对，
// 如包注释中 'Submatch' 和 'Index' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindSubmatchIndex(b []byte) []int {
	return re.pad(re.doExecute(nil, b, "", 0, re.prog.NumCap, nil))
}

// FindStringSubmatch 返回一个字符串切片，持有正则表达式在 s 中最左匹配的文本
// 以及其子表达式的匹配（如果有），如包注释中 'Submatch' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindStringSubmatch(s string) []string {
	var dstCap [4]int
	a := re.doExecute(nil, nil, s, 0, re.prog.NumCap, dstCap[:0])
	if a == nil {
		return nil
	}
	ret := make([]string, 1+re.numSubexp)
	for i := range ret {
		if 2*i < len(a) && a[2*i] >= 0 {
			ret[i] = s[a[2*i]:a[2*i+1]]
		}
	}
	return ret
}

// FindStringSubmatchIndex 返回一个切片，持有标识正则表达式在 s 中最左匹配
// 以及其子表达式的匹配（如果有）的索引对，
// 如包注释中 'Submatch' 和 'Index' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindStringSubmatchIndex(s string) []int {
	return re.pad(re.doExecute(nil, nil, s, 0, re.prog.NumCap, nil))
}

// FindReaderSubmatchIndex 返回一个切片，持有标识正则表达式在由 [io.RuneReader]
// 读取的文本中最左匹配以及其子表达式的匹配（如果有）的索引对，
// 如包注释中 'Submatch' 和 'Index' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindReaderSubmatchIndex(r io.RuneReader) []int {
	return re.pad(re.doExecute(r, nil, "", 0, re.prog.NumCap, nil))
}

const startSize = 10 // 'All' 系列方法中切片的初始大小。

// FindAll 是 [Regexp.Find] 的 'All' 版本；它返回表达式所有连续匹配的切片，
// 如包注释中 'All' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindAll(b []byte, n int) [][]byte {
	if n < 0 {
		n = len(b) + 1
	}
	var result [][]byte
	re.allMatches("", b, n, func(match []int) {
		if result == nil {
			result = make([][]byte, 0, startSize)
		}
		result = append(result, b[match[0]:match[1]:match[1]])
	})
	return result
}

// FindAllIndex 是 [Regexp.FindIndex] 的 'All' 版本；它返回表达式所有连续匹配的切片，
// 如包注释中 'All' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindAllIndex(b []byte, n int) [][]int {
	if n < 0 {
		n = len(b) + 1
	}
	var result [][]int
	re.allMatches("", b, n, func(match []int) {
		if result == nil {
			result = make([][]int, 0, startSize)
		}
		result = append(result, match[0:2])
	})
	return result
}

// FindAllString 是 [Regexp.FindString] 的 'All' 版本；它返回表达式所有连续匹配的切片，
// 如包注释中 'All' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindAllString(s string, n int) []string {
	if n < 0 {
		n = len(s) + 1
	}
	var result []string
	re.allMatches(s, nil, n, func(match []int) {
		if result == nil {
			result = make([]string, 0, startSize)
		}
		result = append(result, s[match[0]:match[1]])
	})
	return result
}

// FindAllStringIndex 是 [Regexp.FindStringIndex] 的 'All' 版本；
// 它返回表达式所有连续匹配的切片，如包注释中 'All' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindAllStringIndex(s string, n int) [][]int {
	if n < 0 {
		n = len(s) + 1
	}
	var result [][]int
	re.allMatches(s, nil, n, func(match []int) {
		if result == nil {
			result = make([][]int, 0, startSize)
		}
		result = append(result, match[0:2])
	})
	return result
}

// FindAllSubmatch 是 [Regexp.FindSubmatch] 的 'All' 版本；它返回表达式所有连续匹配的切片，
// 如包注释中 'All' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindAllSubmatch(b []byte, n int) [][][]byte {
	if n < 0 {
		n = len(b) + 1
	}
	var result [][][]byte
	re.allMatches("", b, n, func(match []int) {
		if result == nil {
			result = make([][][]byte, 0, startSize)
		}
		slice := make([][]byte, len(match)/2)
		for j := range slice {
			if match[2*j] >= 0 {
				slice[j] = b[match[2*j]:match[2*j+1]:match[2*j+1]]
			}
		}
		result = append(result, slice)
	})
	return result
}

// FindAllSubmatchIndex 是 [Regexp.FindSubmatchIndex] 的 'All' 版本；
// 它返回表达式所有连续匹配的切片，如包注释中 'All' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindAllSubmatchIndex(b []byte, n int) [][]int {
	if n < 0 {
		n = len(b) + 1
	}
	var result [][]int
	re.allMatches("", b, n, func(match []int) {
		if result == nil {
			result = make([][]int, 0, startSize)
		}
		result = append(result, match)
	})
	return result
}

// FindAllStringSubmatch 是 [Regexp.FindStringSubmatch] 的 'All' 版本；
// 它返回表达式所有连续匹配的切片，如包注释中 'All' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	if n < 0 {
		n = len(s) + 1
	}
	var result [][]string
	re.allMatches(s, nil, n, func(match []int) {
		if result == nil {
			result = make([][]string, 0, startSize)
		}
		slice := make([]string, len(match)/2)
		for j := range slice {
			if match[2*j] >= 0 {
				slice[j] = s[match[2*j]:match[2*j+1]]
			}
		}
		result = append(result, slice)
	})
	return result
}

// FindAllStringSubmatchIndex 是 [Regexp.FindStringSubmatchIndex] 的 'All' 版本；
// 它返回表达式所有连续匹配的切片，如包注释中 'All' 描述所定义。
// 返回值为 nil 表示无匹配。
func (re *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	if n < 0 {
		n = len(s) + 1
	}
	var result [][]int
	re.allMatches(s, nil, n, func(match []int) {
		if result == nil {
			result = make([][]int, 0, startSize)
		}
		result = append(result, match)
	})
	return result
}

// Split 将 s 按表达式分隔为子串，并返回这些表达式匹配之间的子串切片。
//
// 此方法返回的切片由 s 中未包含在 [Regexp.FindAllString] 返回的切片中的
// 所有子串组成。当对不含元字符的表达式调用时，它等价于 [strings.SplitN]。
//
// 示例：
//
//	s := regexp.MustCompile("a*").Split("abaabaccadaaae", 5)
//	// s: ["", "b", "b", "c", "cadaaae"]
//
// count 参数决定返回的子串数量：
//   - n > 0：最多 n 个子串；最后一个子串将是未分割的剩余部分；
//   - n == 0：结果为 nil（零个子串）；
//   - n < 0：所有子串。
func (re *Regexp) Split(s string, n int) []string {

	if n == 0 {
		return nil
	}

	if len(re.expr) > 0 && len(s) == 0 {
		return []string{""}
	}

	matches := re.FindAllStringIndex(s, n)
	strings := make([]string, 0, len(matches))

	beg := 0
	end := 0
	for _, match := range matches {
		if n > 0 && len(strings) >= n-1 {
			break
		}

		end = match[0]
		if match[1] != 0 {
			strings = append(strings, s[beg:end])
		}
		beg = match[1]
	}

	if end != len(s) {
		strings = append(strings, s[beg:])
	}

	return strings
}

// AppendText 实现了 [encoding.TextAppender]。输出与调用 [Regexp.String] 方法的结果相同。
//
// 注意在某些情况下输出是有损的：此方法不会标示 POSIX 正则表达式
// （即通过调用 [CompilePOSIX] 编译的表达式），
// 也不会标示已调用 [Regexp.Longest] 方法的表达式。
func (re *Regexp) AppendText(b []byte) ([]byte, error) {
	return append(b, re.String()...), nil
}

// MarshalText 实现了 [encoding.TextMarshaler]。输出与调用 [Regexp.AppendText] 方法的结果相同。
//
// 更多信息请参阅 [Regexp.AppendText]。
func (re *Regexp) MarshalText() ([]byte, error) {
	return re.AppendText(nil)
}

// UnmarshalText 通过对编码值调用 [Compile] 来实现 [encoding.TextUnmarshaler]。
func (re *Regexp) UnmarshalText(text []byte) error {
	newRE, err := Compile(string(text))
	if err != nil {
		return err
	}
	*re = *newRE
	return nil
}
