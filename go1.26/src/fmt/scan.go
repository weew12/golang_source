// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmt

import (
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strconv"
	"sync"
	"unicode/utf8"
)

// ScanState 表示传递给自定义扫描器的扫描状态。
// 扫描器可以逐符文扫描，也可以请求 ScanState 获取下一个以空格分隔的令牌。
type ScanState interface {
	// ReadRune 从输入中读取下一个符文（Unicode 码点）。
	// 若在 Scanln、Fscanln 或 Sscanln 期间调用，ReadRune() 在返回第一个 '\n' 后
	// 或读取超出指定宽度时，将返回 EOF。
	ReadRune() (r rune, size int, err error)
	// UnreadRune 使下一次调用 ReadRune 时返回同一个符文。
	UnreadRune() error
	// SkipSpace 跳过输入中的空白字符。换行符会根据当前执行的操作做适配处理；
	// 更多信息参见包文档。
	SkipSpace()
	// Token 若 skipSpace 为 true 则跳过输入中的空白字符，随后返回满足 f(c) 条件的
	// Unicode 码点序列。若 f 为 nil，则使用 !unicode.IsSpace(c)，即令牌将包含非空白字符。
	// 换行符会根据当前执行的操作做适配处理；更多信息参见包文档。
	// 返回的切片指向共享数据，该数据可能在下次调用 Token、使用该 ScanState 作为输入调用 Scan 函数
	// 或调用方的 Scan 方法返回时被覆盖。
	Token(skipSpace bool, f func(rune) bool) (token []byte, err error)
	// Width 返回宽度选项的值以及该选项是否已设置。
	// 单位为 Unicode 码点。
	Width() (wid int, ok bool)
	// 由于 ReadRune 由该接口实现，扫描例程永远不应调用 Read，
	// 且 ScanState 的有效实现可选择始终从 Read 返回错误。
	Read(buf []byte) (n int, err error)
}

// Scanner 由任何实现了 Scan 方法的值实现，该方法扫描输入中值的表示形式
// 并将结果存储到接收者中，接收者必须为指针才能生效。
// 对于实现了该接口的 [Scan]、[Scanf] 或 [Scanln] 参数，都会调用其 Scan 方法。
type Scanner interface {
	Scan(state ScanState, verb rune) error
}

// Scan 扫描从标准输入读取的文本，将连续的以空格分隔的值依次存储到各个参数中。
// 换行符视为空格。返回成功扫描的项数。
// 若该项数小于参数数量，err 将说明原因。
func Scan(a ...any) (n int, err error) {
	return Fscan(os.Stdin, a...)
}

// Scanln 类似于 [Scan]，但在换行符处停止扫描，
// 且最后一项之后必须为换行符或 EOF。
func Scanln(a ...any) (n int, err error) {
	return Fscanln(os.Stdin, a...)
}

// Scanf 扫描从标准输入读取的文本，按照格式指定的规则将连续的以空格分隔的值
// 依次存储到各个参数中。返回成功扫描的项数。
// 若该项数小于参数数量，err 将说明原因。
// 输入中的换行符必须与格式中的换行符匹配。
// 唯一例外：动词 %c 始终扫描输入中的下一个符文，
// 即使该符文是空格（或制表符等）或换行符。
func Scanf(format string, a ...any) (n int, err error) {
	return Fscanf(os.Stdin, format, a...)
}

type stringReader string

func (r *stringReader) Read(b []byte) (n int, err error) {
	n = copy(b, *r)
	*r = (*r)[n:]
	if n == 0 {
		err = io.EOF
	}
	return
}

// Sscan 扫描参数字符串，将连续的以空格分隔的值依次存储到各个参数中。
// 换行符视为空格。返回成功扫描的项数。
// 若该项数小于参数数量，err 将说明原因。
func Sscan(str string, a ...any) (n int, err error) {
	return Fscan((*stringReader)(&str), a...)
}

// Sscanln 类似于 [Sscan]，但在换行符处停止扫描，
// 且最后一项之后必须为换行符或 EOF。
func Sscanln(str string, a ...any) (n int, err error) {
	return Fscanln((*stringReader)(&str), a...)
}

// Sscanf 扫描参数字符串，按照格式指定的规则将连续的以空格分隔的值
// 依次存储到各个参数中。返回成功解析的项数。
// 输入中的换行符必须与格式中的换行符匹配。
func Sscanf(str string, format string, a ...any) (n int, err error) {
	return Fscanf((*stringReader)(&str), format, a...)
}

// Fscan 扫描从 r 读取的文本，将连续的以空格分隔的值依次存储到各个参数中。
// 换行符视为空格。返回成功扫描的项数。
// 若该项数小于参数数量，err 将说明原因。
func Fscan(r io.Reader, a ...any) (n int, err error) {
	s, old := newScanState(r, true, false)
	n, err = s.doScan(a)
	s.free(old)
	return
}

// Fscanln 类似于 [Fscan]，但在换行符处停止扫描，
// 且最后一项之后必须为换行符或 EOF。
func Fscanln(r io.Reader, a ...any) (n int, err error) {
	s, old := newScanState(r, false, true)
	n, err = s.doScan(a)
	s.free(old)
	return
}

// Fscanf 扫描从 r 读取的文本，按照格式指定的规则将连续的以空格分隔的值
// 依次存储到各个参数中。返回成功解析的项数。
// 输入中的换行符必须与格式中的换行符匹配。
func Fscanf(r io.Reader, format string, a ...any) (n int, err error) {
	s, old := newScanState(r, false, false)
	n, err = s.doScanf(format, a)
	s.free(old)
	return
}

// scanError 表示扫描程序生成的错误。
// 它用作唯一标识，在恢复时识别此类错误。
type scanError struct {
	err error
}

const eof = -1

// ss 是 ScanState 的内部实现。
type ss struct {
	rs    io.RuneScanner // 输入读取源
	buf   buffer         // 令牌累加器
	count int            // 已消耗的符文数量
	atEOF bool           // 已读取到 EOF
	ssave
}

// ssave 保存递归扫描时需要保存和恢复的 ss 部分数据。
type ssave struct {
	validSave bool // 是否为实际 ss 的一部分
	nlIsEnd   bool // 换行符是否终止扫描
	nlIsSpace bool // 换行符是否视为空白符
	argLimit  int  // 该参数的 ss.count 最大值；argLimit <= limit
	limit     int  // ss.count 的最大值
	maxWid    int  // 该参数的宽度
}

// Read 方法仅存在于 ScanState 中，以使其满足 io.Reader 接口。
// 按预期使用时永远不会被调用，因此无需实现实际功能。
func (s *ss) Read(buf []byte) (n int, err error) {
	return 0, errors.New("ScanState's Read should not be called. Use ReadRune")
}

func (s *ss) ReadRune() (r rune, size int, err error) {
	if s.atEOF || s.count >= s.argLimit {
		err = io.EOF
		return
	}

	r, size, err = s.rs.ReadRune()
	if err == nil {
		s.count++
		if s.nlIsEnd && r == '\n' {
			s.atEOF = true
		}
	} else if err == io.EOF {
		s.atEOF = true
	}
	return
}

func (s *ss) Width() (wid int, ok bool) {
	if s.maxWid == hugeWid {
		return 0, false
	}
	return s.maxWid, true
}

// 公有方法返回错误；此私有方法会触发 panic。
// 若 getRune 读取到 EOF，返回值为 EOF (-1)。
func (s *ss) getRune() (r rune) {
	r, _, err := s.ReadRune()
	if err != nil {
		if err == io.EOF {
			return eof
		}
		s.error(err)
	}
	return
}

// mustReadRune 将 io.EOF 转换为 panic(io.ErrUnexpectedEOF)。
// 用于字符串扫描等 EOF 属于语法错误的场景。
func (s *ss) mustReadRune() (r rune) {
	r = s.getRune()
	if r == eof {
		s.error(io.ErrUnexpectedEOF)
	}
	return
}

func (s *ss) UnreadRune() error {
	s.rs.UnreadRune()
	s.atEOF = false
	s.count--
	return nil
}

func (s *ss) error(err error) {
	panic(scanError{err})
}

func (s *ss) errorString(err string) {
	panic(scanError{errors.New(err)})
}

func (s *ss) Token(skipSpace bool, f func(rune) bool) (tok []byte, err error) {
	defer func() {
		if e := recover(); e != nil {
			if se, ok := e.(scanError); ok {
				err = se.err
			} else {
				panic(e)
			}
		}
	}()
	if f == nil {
		f = notSpace
	}
	s.buf = s.buf[:0]
	tok = s.token(skipSpace, f)
	return
}

// space 是 unicode.White_Space 范围的副本，
// 避免依赖 unicode 包。
var space = [][2]uint16{
	{0x0009, 0x000d},
	{0x0020, 0x0020},
	{0x0085, 0x0085},
	{0x00a0, 0x00a0},
	{0x1680, 0x1680},
	{0x2000, 0x200a},
	{0x2028, 0x2029},
	{0x202f, 0x202f},
	{0x205f, 0x205f},
	{0x3000, 0x3000},
}

func isSpace(r rune) bool {
	if r >= 1<<16 {
		return false
	}
	rx := uint16(r)
	for _, rng := range space {
		if rx < rng[0] {
			return false
		}
		if rx <= rng[1] {
			return true
		}
	}
	return false
}

// notSpace 是 Token 中使用的默认扫描函数。
func notSpace(r rune) bool {
	return !isSpace(r)
}

// readRune 是用于从 io.Reader 读取 UTF-8 编码码点的结构体。
// 当传递给扫描器的 Reader 未实现 io.RuneScanner 时使用该结构体。
type readRune struct {
	reader   io.Reader
	buf      [utf8.UTFMax]byte // 仅在 ReadRune 内部使用
	pending  int               // pendBuf 中的字节数；仅 UTF-8 无效时大于 0
	pendBuf  [utf8.UTFMax]byte // 剩余字节
	peekRune rune              // >=0 表示下一个符文；<0 时为 ^(上一个符文)
}

// readByte 从输入读取下一个字节，若 UTF-8 格式错误，
// 该字节可能是上一次读取的剩余数据。
func (r *readRune) readByte() (b byte, err error) {
	if r.pending > 0 {
		b = r.pendBuf[0]
		copy(r.pendBuf[0:], r.pendBuf[1:])
		r.pending--
		return
	}
	n, err := io.ReadFull(r.reader, r.pendBuf[:1])
	if n != 1 {
		return 0, err
	}
	return r.pendBuf[0], err
}

// ReadRune 从 r 内部的 io.Reader 返回下一个 UTF-8 编码码点。
func (r *readRune) ReadRune() (rr rune, size int, err error) {
	if r.peekRune >= 0 {
		rr = r.peekRune
		r.peekRune = ^r.peekRune
		size = utf8.RuneLen(rr)
		return
	}
	r.buf[0], err = r.readByte()
	if err != nil {
		return
	}
	if r.buf[0] < utf8.RuneSelf { // 快速检查常见 ASCII 场景
		rr = rune(r.buf[0])
		size = 1 // 确定为 1
		// 对符文按位取反，以供 UnreadRune 使用
		r.peekRune = ^rr
		return
	}
	var n int
	for n = 1; !utf8.FullRune(r.buf[:n]); n++ {
		r.buf[n], err = r.readByte()
		if err != nil {
			if err == io.EOF {
				err = nil
				break
			}
			return
		}
	}
	rr, size = utf8.DecodeRune(r.buf[:n])
	if size < n { // 出现错误，保存剩余字节供下次读取
		copy(r.pendBuf[r.pending:], r.buf[size:n])
		r.pending += n - size
	}
	// 对符文按位取反，以供 UnreadRune 使用
	r.peekRune = ^rr
	return
}

func (r *readRune) UnreadRune() error {
	if r.peekRune >= 0 {
		return errors.New("fmt: scanning called UnreadRune with no rune available")
	}
	// 反转已读取符文的位翻转，恢复 >=0 的有效状态
	r.peekRune = ^r.peekRune
	return nil
}

var ssFree = sync.Pool{
	New: func() any { return new(ss) },
}

// newScanState 分配新的 ss 结构体或获取缓存的结构体。
func newScanState(r io.Reader, nlIsSpace, nlIsEnd bool) (s *ss, old ssave) {
	s = ssFree.Get().(*ss)
	if rs, ok := r.(io.RuneScanner); ok {
		s.rs = rs
	} else {
		s.rs = &readRune{reader: r, peekRune: -1}
	}
	s.nlIsSpace = nlIsSpace
	s.nlIsEnd = nlIsEnd
	s.atEOF = false
	s.limit = hugeWid
	s.argLimit = hugeWid
	s.maxWid = hugeWid
	s.validSave = true
	s.count = 0
	return
}

// free 将已使用的 ss 结构体存入 ssFree；避免每次调用都分配内存。
func (s *ss) free(old ssave) {
	// 若为递归使用，仅恢复旧状态
	if old.validSave {
		s.ssave = old
		return
	}
	// 不保留带有大缓冲区的 ss 结构体
	if cap(s.buf) > 1024 {
		return
	}
	s.buf = s.buf[:0]
	s.rs = nil
	ssFree.Put(s)
}

// SkipSpace 为 Scan 方法提供跳过空白符和换行符的能力，
// 遵循格式字符串与 [Scan]/[Scanln] 设置的当前扫描模式。
func (s *ss) SkipSpace() {
	for {
		r := s.getRune()
		if r == eof {
			return
		}
		if r == '\r' && s.peek("\n") {
			continue
		}
		if r == '\n' {
			if s.nlIsSpace {
				continue
			}
			s.errorString("unexpected newline")
			return
		}
		if !isSpace(r) {
			s.UnreadRune()
			break
		}
	}
}

// token 从输入返回下一个以空格分隔的字符串。
// 该方法会跳过空白符。对于 Scanln，在换行符处停止；
// 对于 Scan，换行符视为空格。
func (s *ss) token(skipSpace bool, f func(rune) bool) []byte {
	if skipSpace {
		s.SkipSpace()
	}
	// 读取至空白符或换行符
	for {
		r := s.getRune()
		if r == eof {
			break
		}
		if !f(r) {
			s.UnreadRune()
			break
		}
		s.buf.writeRune(r)
	}
	return s.buf
}

var errComplex = errors.New("syntax error scanning complex number")
var errBool = errors.New("syntax error scanning boolean")

func indexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

// consume 读取输入中的下一个符文，并报告该符文是否在 ok 字符串中。
// 若 accept 为 true，将该字符写入输入令牌。
func (s *ss) consume(ok string, accept bool) bool {
	r := s.getRune()
	if r == eof {
		return false
	}
	if indexRune(ok, r) >= 0 {
		if accept {
			s.buf.writeRune(r)
		}
		return true
	}
	if r != eof && accept {
		s.UnreadRune()
	}
	return false
}

// peek 报告下一个字符是否在 ok 字符串中，不消耗该字符。
func (s *ss) peek(ok string) bool {
	r := s.getRune()
	if r != eof {
		s.UnreadRune()
	}
	return indexRune(ok, r) >= 0
}

func (s *ss) notEOF() {
	// 确保存在可读取的数据
	if r := s.getRune(); r == eof {
		panic(io.EOF)
	}
	s.UnreadRune()
}

// accept 检查输入中的下一个符文。若该字节在指定字符串中，
// 将其写入缓冲区并返回 true，否则返回 false。
func (s *ss) accept(ok string) bool {
	return s.consume(ok, true)
}

// okVerb 验证动词是否存在于列表中，若不存在则设置 s.err。
func (s *ss) okVerb(verb rune, okVerbs, typ string) bool {
	for _, v := range okVerbs {
		if v == verb {
			return true
		}
	}
	s.errorString("bad verb '%" + string(verb) + "' for " + typ)
	return false
}

// scanBool 返回下一个令牌表示的布尔值。
func (s *ss) scanBool(verb rune) bool {
	s.SkipSpace()
	s.notEOF()
	if !s.okVerb(verb, "tv", "boolean") {
		return false
	}
	// 布尔值语法校验较为繁琐，此处不严格区分大小写
	switch s.getRune() {
	case '0':
		return false
	case '1':
		return true
	case 't', 'T':
		if s.accept("rR") && (!s.accept("uU") || !s.accept("eE")) {
			s.error(errBool)
		}
		return true
	case 'f', 'F':
		if s.accept("aA") && (!s.accept("lL") || !s.accept("sS") || !s.accept("eE")) {
			s.error(errBool)
		}
		return false
	}
	return false
}

// 数值相关字符集
const (
	binaryDigits      = "01"
	octalDigits       = "01234567"
	decimalDigits     = "0123456789"
	hexadecimalDigits = "0123456789aAbBcCdDeEfF"
	sign              = "+-"
	period            = "."
	exponent          = "eEpP"
)

// getBase 返回动词表示的数字进制及其对应的数字符集。
func (s *ss) getBase(verb rune) (base int, digits string) {
	s.okVerb(verb, "bdoUxXv", "integer") // 设置 s.err
	base = 10
	digits = decimalDigits
	switch verb {
	case 'b':
		base = 2
		digits = binaryDigits
	case 'o':
		base = 8
		digits = octalDigits
	case 'x', 'X', 'U':
		base = 16
		digits = hexadecimalDigits
	}
	return
}

// scanNumber 返回从当前位置开始、由指定数字符组成的数字字符串。
func (s *ss) scanNumber(digits string, haveDigits bool) string {
	if !haveDigits {
		s.notEOF()
		if !s.accept(digits) {
			s.errorString("expected integer")
		}
	}
	for s.accept(digits) {
	}
	return string(s.buf)
}

// scanRune 返回输入中的下一个符文值。
func (s *ss) scanRune(bitSize int) int64 {
	s.notEOF()
	r := s.getRune()
	n := uint(bitSize)
	x := (int64(r) << (64 - n)) >> (64 - n)
	if x != int64(r) {
		s.errorString("overflow on character value " + string(r))
	}
	return int64(r)
}

// scanBasePrefix 报告整数是否以进制前缀开头，
// 并返回进制、数字符集以及是否找到数字 0。
// 仅当动词为 %v 时调用该方法。
func (s *ss) scanBasePrefix() (base int, digits string, zeroFound bool) {
	if !s.peek("0") {
		return 0, decimalDigits + "_", false
	}
	s.accept("0")
	// 0、0b、0o、0x 的特殊处理
	switch {
	case s.peek("bB"):
		s.consume("bB", true)
		return 0, binaryDigits + "_", true
	case s.peek("oO"):
		s.consume("oO", true)
		return 0, octalDigits + "_", true
	case s.peek("xX"):
		s.consume("xX", true)
		return 0, hexadecimalDigits + "_", true
	default:
		return 0, octalDigits + "_", true
	}
}

// scanInt 返回下一个令牌表示的整数值，并检查溢出。
// 所有错误均存储在 s.err 中。
func (s *ss) scanInt(verb rune, bitSize int) int64 {
	if verb == 'c' {
		return s.scanRune(bitSize)
	}
	s.SkipSpace()
	s.notEOF()
	base, digits := s.getBase(verb)
	haveDigits := false
	if verb == 'U' {
		if !s.consume("U", false) || !s.consume("+", false) {
			s.errorString("bad unicode format ")
		}
	} else {
		s.accept(sign) // 若存在符号，将保留在令牌缓冲区中
		if verb == 'v' {
			base, digits, haveDigits = s.scanBasePrefix()
		}
	}
	tok := s.scanNumber(digits, haveDigits)
	i, err := strconv.ParseInt(tok, base, 64)
	if err != nil {
		s.error(err)
	}
	n := uint(bitSize)
	x := (i << (64 - n)) >> (64 - n)
	if x != i {
		s.errorString("integer overflow on token " + tok)
	}
	return i
}

// scanUint 返回下一个令牌表示的无符号整数值，并检查溢出。
// 所有错误均存储在 s.err 中。
func (s *ss) scanUint(verb rune, bitSize int) uint64 {
	if verb == 'c' {
		return uint64(s.scanRune(bitSize))
	}
	s.SkipSpace()
	s.notEOF()
	base, digits := s.getBase(verb)
	haveDigits := false
	if verb == 'U' {
		if !s.consume("U", false) || !s.consume("+", false) {
			s.errorString("bad unicode format ")
		}
	} else if verb == 'v' {
		base, digits, haveDigits = s.scanBasePrefix()
	}
	tok := s.scanNumber(digits, haveDigits)
	i, err := strconv.ParseUint(tok, base, 64)
	if err != nil {
		s.error(err)
	}
	n := uint(bitSize)
	x := (i << (64 - n)) >> (64 - n)
	if x != i {
		s.errorString("unsigned integer overflow on token " + tok)
	}
	return i
}

// floatToken 返回从当前位置开始的浮点数，若指定宽度则不超过该宽度。
// 该方法不严格校验语法（不检查是否包含数字），由 Atof 完成语法校验。
func (s *ss) floatToken() string {
	s.buf = s.buf[:0]
	// NaN?
	if s.accept("nN") && s.accept("aA") && s.accept("nN") {
		return string(s.buf)
	}
	// 前置符号?
	s.accept(sign)
	// Inf?
	if s.accept("iI") && s.accept("nN") && s.accept("fF") {
		return string(s.buf)
	}
	digits := decimalDigits + "_"
	exp := exponent
	if s.accept("0") && s.accept("xX") {
		digits = hexadecimalDigits + "_"
		exp = "pP"
	}
	// 数字?
	for s.accept(digits) {
	}
	// 小数点?
	if s.accept(period) {
		// 小数部分?
		for s.accept(digits) {
		}
	}
	// 指数?
	if s.accept(exp) {
		// 前置符号?
		s.accept(sign)
		// 数字?
		for s.accept(decimalDigits + "_") {
		}
	}
	return string(s.buf)
}

// complexTokens 返回从当前位置开始的复数的实部和虚部。
// 该数字可带括号，格式为 (N+Ni)，其中 N 为浮点数且内部无空格。
func (s *ss) complexTokens() (real, imag string) {
	// TODO: 单独支持 N 和 Ni?
	parens := s.accept("(")
	real = s.floatToken()
	s.buf = s.buf[:0]
	// 必须存在符号
	if !s.accept("+-") {
		s.error(errComplex)
	}
	// 符号已存入缓冲区
	imagSign := string(s.buf)
	imag = s.floatToken()
	if !s.accept("i") {
		s.error(errComplex)
	}
	if parens && !s.accept(")") {
		s.error(errComplex)
	}
	return real, imagSign + imag
}

func hasX(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 'x' || s[i] == 'X' {
			return true
		}
	}
	return false
}

// convertFloat 将字符串转换为 float64 值。
func (s *ss) convertFloat(str string, n int) float64 {
	// strconv.ParseFloat 可处理 "+0x1.fp+2"，
	// 但需自行实现非标准的十进制+二进制指数混合格式 (1.2p4)
	if p := indexRune(str, 'p'); p >= 0 && !hasX(str) {
		// Atof 不处理 2 的幂指数，可简易计算
		f, err := strconv.ParseFloat(str[:p], n)
		if err != nil {
			// 将完整字符串写入错误信息
			if e, ok := err.(*strconv.NumError); ok {
				e.Num = str
			}
			s.error(err)
		}
		m, err := strconv.Atoi(str[p+1:])
		if err != nil {
			// 将完整字符串写入错误信息
			if e, ok := err.(*strconv.NumError); ok {
				e.Num = str
			}
			s.error(err)
		}
		return math.Ldexp(f, m)
	}
	f, err := strconv.ParseFloat(str, n)
	if err != nil {
		s.error(err)
	}
	return f
}

// scanComplex 将下一个令牌转换为 complex128 值。
// atof 参数是底层类型的专用读取函数。
// 若读取 complex64，atof 会解析 float32 并转换为 float64，
// 避免为每种复数类型重复编写代码。
func (s *ss) scanComplex(verb rune, n int) complex128 {
	if !s.okVerb(verb, floatVerbs, "complex") {
		return 0
	}
	s.SkipSpace()
	s.notEOF()
	sreal, simag := s.complexTokens()
	real := s.convertFloat(sreal, n/2)
	imag := s.convertFloat(simag, n/2)
	return complex(real, imag)
}

// convertString 返回输入中后续字符表示的字符串。
// 输入格式由动词决定。
func (s *ss) convertString(verb rune) (str string) {
	if !s.okVerb(verb, "svqxX", "string") {
		return ""
	}
	s.SkipSpace()
	s.notEOF()
	switch verb {
	case 'q':
		str = s.quotedString()
	case 'x', 'X':
		str = s.hexString()
	default:
		str = string(s.token(true, notSpace)) // %s 和 %v 仅返回下一个单词
	}
	return
}

// quotedString 返回输入中后续字符表示的双引号或反引号包裹的字符串。
func (s *ss) quotedString() string {
	s.notEOF()
	quote := s.getRune()
	switch quote {
	case '`':
		// 反引号包裹：读取至 EOF 或反引号，内容原样保留
		for {
			r := s.mustReadRune()
			if r == quote {
				break
			}
			s.buf.writeRune(r)
		}
		return string(s.buf)
	case '"':
		// 双引号包裹：保留引号，由 strconv.Unquote 处理反斜杠转义
		s.buf.writeByte('"')
		for {
			r := s.mustReadRune()
			s.buf.writeRune(r)
			if r == '\\' {
				// 合法反斜杠转义中，仅转义后第一个字符可为反斜杠或引号
				// 因此仅需保护反斜杠后的第一个字符
				s.buf.writeRune(s.mustReadRune())
			} else if r == '"' {
				break
			}
		}
		result, err := strconv.Unquote(string(s.buf))
		if err != nil {
			s.error(err)
		}
		return result
	default:
		s.errorString("expected quoted string")
	}
	return ""
}

// hexDigit 返回十六进制数字对应的值。
func hexDigit(d rune) (int, bool) {
	digit := int(d)
	switch digit {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return digit - '0', true
	case 'a', 'b', 'c', 'd', 'e', 'f':
		return 10 + digit - 'a', true
	case 'A', 'B', 'C', 'D', 'E', 'F':
		return 10 + digit - 'A', true
	}
	return -1, false
}

// hexByte 从输入返回下一个十六进制编码（双字符）字节。
// 若输入后续字节未编码为十六进制字节，返回 ok==false。
// 若第一个字节为十六进制、第二个不是，则停止处理。
func (s *ss) hexByte() (b byte, ok bool) {
	rune1 := s.getRune()
	if rune1 == eof {
		return
	}
	value1, ok := hexDigit(rune1)
	if !ok {
		s.UnreadRune()
		return
	}
	value2, ok := hexDigit(s.mustReadRune())
	if !ok {
		s.errorString("illegal hex digit")
		return
	}
	return byte(value1<<4 | value2), true
}

// hexString 返回以空格分隔的十六进制对编码的字符串。
func (s *ss) hexString() string {
	s.notEOF()
	for {
		b, ok := s.hexByte()
		if !ok {
			break
		}
		s.buf.writeByte(b)
	}
	if len(s.buf) == 0 {
		s.errorString("no hex data for %x string")
		return ""
	}
	return string(s.buf)
}

const (
	floatVerbs = "beEfFgGv"

	hugeWid = 1 << 30

	intBits     = 32 << (^uint(0) >> 63)
	uintptrBits = 32 << (^uintptr(0) >> 63)
)

// scanPercent 扫描字面量百分号字符。
func (s *ss) scanPercent() {
	s.SkipSpace()
	s.notEOF()
	if !s.accept("%") {
		s.errorString("missing literal %")
	}
}

// scanOne 扫描单个值，根据参数类型获取对应的扫描器。
func (s *ss) scanOne(verb rune, arg any) {
	s.buf = s.buf[:0]
	var err error
	// 若参数自带 Scan 方法，优先使用该方法
	if v, ok := arg.(Scanner); ok {
		err = v.Scan(s, verb)
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			s.error(err)
		}
		return
	}

	switch v := arg.(type) {
	case *bool:
		*v = s.scanBool(verb)
	case *complex64:
		*v = complex64(s.scanComplex(verb, 64))
	case *complex128:
		*v = s.scanComplex(verb, 128)
	case *int:
		*v = int(s.scanInt(verb, intBits))
	case *int8:
		*v = int8(s.scanInt(verb, 8))
	case *int16:
		*v = int16(s.scanInt(verb, 16))
	case *int32:
		*v = int32(s.scanInt(verb, 32))
	case *int64:
		*v = s.scanInt(verb, 64)
	case *uint:
		*v = uint(s.scanUint(verb, intBits))
	case *uint8:
		*v = uint8(s.scanUint(verb, 8))
	case *uint16:
		*v = uint16(s.scanUint(verb, 16))
	case *uint32:
		*v = uint32(s.scanUint(verb, 32))
	case *uint64:
		*v = s.scanUint(verb, 64)
	case *uintptr:
		*v = uintptr(s.scanUint(verb, uintptrBits))
	// 浮点数处理需注意：按结果精度扫描，而非高精度扫描后转换，以保留正确错误条件
	case *float32:
		if s.okVerb(verb, floatVerbs, "float32") {
			s.SkipSpace()
			s.notEOF()
			*v = float32(s.convertFloat(s.floatToken(), 32))
		}
	case *float64:
		if s.okVerb(verb, floatVerbs, "float64") {
			s.SkipSpace()
			s.notEOF()
			*v = s.convertFloat(s.floatToken(), 64)
		}
	case *string:
		*v = s.convertString(verb)
	case *[]byte:
		// 扫描为字符串后转换，获取数据副本
		// 若直接扫描为字节，切片将指向缓冲区
		*v = []byte(s.convertString(verb))
	default:
		val := reflect.ValueOf(v)
		ptr := val
		if ptr.Kind() != reflect.Pointer {
			s.errorString("type not a pointer: " + val.Type().String())
			return
		}
		switch v := ptr.Elem(); v.Kind() {
		case reflect.Bool:
			v.SetBool(s.scanBool(verb))
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			v.SetInt(s.scanInt(verb, v.Type().Bits()))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			v.SetUint(s.scanUint(verb, v.Type().Bits()))
		case reflect.String:
			v.SetString(s.convertString(verb))
		case reflect.Slice:
			// 目前仅支持（重命名的）[]byte
			typ := v.Type()
			if typ.Elem().Kind() != reflect.Uint8 {
				s.errorString("can't scan type: " + val.Type().String())
			}
			str := s.convertString(verb)
			v.Set(reflect.MakeSlice(typ, len(str), len(str)))
			for i := 0; i < len(str); i++ {
				v.Index(i).SetUint(uint64(str[i]))
			}
		case reflect.Float32, reflect.Float64:
			s.SkipSpace()
			s.notEOF()
			v.SetFloat(s.convertFloat(s.floatToken(), v.Type().Bits()))
		case reflect.Complex64, reflect.Complex128:
			v.SetComplex(s.scanComplex(verb, v.Type().Bits()))
		default:
			s.errorString("can't scan type: " + val.Type().String())
		}
	}
}

// errorHandler 将本地 panic 转换为错误返回值。
func errorHandler(errp *error) {
	if e := recover(); e != nil {
		if se, ok := e.(scanError); ok { // 捕获本地错误
			*errp = se.err
		} else if eof, ok := e.(error); ok && eof == io.EOF { // 输入耗尽
			*errp = eof
		} else {
			panic(e)
		}
	}
}

// doScan 执行无格式字符串扫描的核心逻辑。
func (s *ss) doScan(a []any) (numProcessed int, err error) {
	defer errorHandler(&err)
	for _, arg := range a {
		s.scanOne('v', arg)
		numProcessed++
	}
	// 若需要（如 Scanln 等），检查换行符（或 EOF）
	if s.nlIsEnd {
		for {
			r := s.getRune()
			if r == '\n' || r == eof {
				break
			}
			if !isSpace(r) {
				s.errorString("expected newline")
				break
			}
		}
	}
	return
}

// advance 判断输入中的后续字符是否与格式字符匹配。
// 返回格式中消耗的字节数。
// 输入或格式中的所有空白符序列均视为单个空格。
// 换行符为特殊字符：格式中的换行符必须与输入中的换行符匹配，反之亦然。
// 该方法同时处理 %% 场景。若返回值为 0，
// 表示格式以 % 开头（后无 %）或输入为空；
// 若返回值为负数，表示输入与字符串不匹配。
func (s *ss) advance(format string) (i int) {
	for i < len(format) {
		fmtc, w := utf8.DecodeRuneInString(format[i:])

		// 空白符处理
		// 本注释剩余部分中，"空白符" 指换行符以外的空格
		// 格式中的换行符匹配输入中零个或多个空白符后接换行符或输入结束
		// 换行符前的格式空白符合并至换行符
		// 换行符后的格式空白符匹配输入对应换行符后的零个或多个空白符
		// 其他格式空白符匹配输入中一个或多个空白符或输入结束
		if isSpace(fmtc) {
			newlines := 0
			trailingSpace := false
			for isSpace(fmtc) && i < len(format) {
				if fmtc == '\n' {
					newlines++
					trailingSpace = false
				} else {
					trailingSpace = true
				}
				i += w
				fmtc, w = utf8.DecodeRuneInString(format[i:])
			}
			for j := 0; j < newlines; j++ {
				inputc := s.getRune()
				for isSpace(inputc) && inputc != '\n' {
					inputc = s.getRune()
				}
				if inputc != '\n' && inputc != eof {
					s.errorString("newline in format does not match input")
				}
			}
			if trailingSpace {
				inputc := s.getRune()
				if newlines == 0 {
					// 若尾随空白符独立存在（未跟随换行符），
					// 必须至少消耗一个空白符
					if !isSpace(inputc) && inputc != eof {
						s.errorString("expected space in input to match format")
					}
					if inputc == '\n' {
						s.errorString("newline in input does not match format")
					}
				}
				for isSpace(inputc) && inputc != '\n' {
					inputc = s.getRune()
				}
				if inputc != eof {
					s.UnreadRune()
				}
			}
			continue
		}

		// 动词处理
		if fmtc == '%' {
			// 字符串末尾的 % 为错误
			if i+w == len(format) {
				s.errorString("missing verb: % at end of format string")
			}
			// %% 视为字面量百分号
			nextc, _ := utf8.DecodeRuneInString(format[i+w:]) // 字符串为空时不匹配 %
			if nextc != '%' {
				return
			}
			i += w // 跳过第一个 %
		}

		// 字面量匹配
		inputc := s.mustReadRune()
		if fmtc != inputc {
			s.UnreadRune()
			return -1
		}
		i += w
	}
	return
}

// doScanf 执行带格式字符串扫描的核心逻辑。
// 目前仅支持基础类型的指针。
func (s *ss) doScanf(format string, a []any) (numProcessed int, err error) {
	defer errorHandler(&err)
	end := len(format) - 1
	// 每个非空格式处理一个参数
	for i := 0; i <= end; {
		w := s.advance(format[i:])
		if w > 0 {
			i += w
			continue
		}
		// 匹配失败、存在百分号或输入耗尽
		if format[i] != '%' {
			// 无法推进格式，判断原因
			if w < 0 {
				s.errorString("input does not match format")
			}
			// 否则到达 EOF；"参数过多" 错误在下方处理
			break
		}
		i++ // % 占一个字节

		// 读取宽度（如 20）
		var widPresent bool
		s.maxWid, widPresent, i = parsenum(format, i, end)
		if !widPresent {
			s.maxWid = hugeWid
		}

		c, w := utf8.DecodeRuneInString(format[i:])
		i += w

		if c != 'c' {
			s.SkipSpace()
		}
		if c == '%' {
			s.scanPercent()
			continue // 不消耗参数
		}
		s.argLimit = s.limit
		if f := s.count + s.maxWid; f < s.argLimit {
			s.argLimit = f
		}

		if numProcessed >= len(a) { // 参数耗尽
			s.errorString("too few operands for format '%" + format[i-w:] + "'")
			break
		}
		arg := a[numProcessed]

		s.scanOne(c, arg)
		numProcessed++
		s.argLimit = s.limit
	}
	if numProcessed < len(a) {
		s.errorString("too many operands")
	}
	return
}
