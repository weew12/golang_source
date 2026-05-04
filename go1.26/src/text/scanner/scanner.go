// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package scanner 提供 UTF-8 编码文本的扫描器和分词器。
// 它接受一个 io.Reader 作为输入源，然后可以通过反复调用 Scan 函数对其进行分词。
// 为了与现有工具兼容，不允许使用 NUL 字符。如果源代码的第一个字符是 UTF-8 编码的
// 字节顺序标记（BOM），它将被丢弃。
//
// 默认情况下，[Scanner] 会跳过空白字符和 Go 注释，并识别 Go 语言规范中定义的所有字面量。
// 可以对其进行自定义，以仅识别这些字面量的子集，以及识别不同的标识符和空白字符。
package scanner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"unicode"
	"unicode/utf8"
)

// Position 表示源代码位置的值。
// 当 Line > 0 时，位置有效。
type Position struct {
	Filename string // filename, if any
	Offset   int    // byte offset, starting at 0
	Line     int    // line number, starting at 1
	Column   int    // column number, starting at 1 (character count per line)
}

// IsValid 报告位置是否有效。
func (pos *Position) IsValid() bool { return pos.Line > 0 }

func (pos Position) String() string {
	s := pos.Filename
	if s == "" {
		s = "<input>"
	}
	if pos.IsValid() {
		s += fmt.Sprintf(":%d:%d", pos.Line, pos.Column)
	}
	return s
}

// 预定义模式位，用于控制词法记号的识别。例如，要配置 [Scanner] 仅识别 Go 标识符、
// 整数并跳过注释，请将 Scanner 的 Mode 字段设置为：
//
//	ScanIdents | ScanInts | ScanComments | SkipComments
//
// 除了注释（在设置 SkipComments 时会被跳过）之外，未识别的词法记号不会被忽略。
// 相反，扫描器会简单地返回各个单独的字符（或者可能是子记号）。例如，如果模式为
// ScanIdents（非 ScanStrings），则字符串 "foo" 会被扫描为词法记号序列
// '"' [Ident] '"'。
//
// 使用 GoTokens 配置 Scanner，使其接受所有 Go 字面量词法记号，包括 Go 标识符。
// 注释将被跳过。
const (
	ScanIdents     = 1 << -Ident
	ScanInts       = 1 << -Int
	ScanFloats     = 1 << -Float // includes Ints and hexadecimal floats
	ScanChars      = 1 << -Char
	ScanStrings    = 1 << -String
	ScanRawStrings = 1 << -RawString
	ScanComments   = 1 << -Comment
	SkipComments   = 1 << -skipComment // if set with ScanComments, comments become white space
	GoTokens       = ScanIdents | ScanFloats | ScanChars | ScanStrings | ScanRawStrings | ScanComments | SkipComments
)

// Scan 的结果是以下词法记号之一或一个 Unicode 字符。
const (
	EOF = -(iota + 1)
	Ident
	Int
	Float
	Char
	String
	RawString
	Comment

	// 仅供内部使用
	skipComment
)

var tokenString = map[rune]string{
	EOF:       "EOF",
	Ident:     "Ident",
	Int:       "Int",
	Float:     "Float",
	Char:      "Char",
	String:    "String",
	RawString: "RawString",
	Comment:   "Comment",
}

// TokenString 返回词法记号或 Unicode 字符的可打印字符串。
func TokenString(tok rune) string {
	if s, found := tokenString[tok]; found {
		return s
	}
	return fmt.Sprintf("%q", string(tok))
}

// GoWhitespace 是 [Scanner] 的 Whitespace 字段的默认值。
// 它的值选择 Go 的空白字符。
const GoWhitespace = 1<<'\t' | 1<<'\n' | 1<<'\r' | 1<<' '

const bufLen = 1024 // at least utf8.UTFMax

// Scanner 实现了从 [io.Reader] 读取 Unicode 字符和词法记号。
type Scanner struct {
	// 输入
	src io.Reader

	// 源缓冲区
	srcBuf [bufLen + 1]byte // +1 用于常见情况 s.next() 的哨兵
	srcPos int              // 读取位置（srcBuf 索引）
	srcEnd int              // 源结束位置（srcBuf 索引）

	// 源位置
	srcBufOffset int // srcBuf[0] 在源中的字节偏移量
	line         int // 行计数
	column       int // 字符计数
	lastLineLen  int // 最后一行的字符长度（用于正确的列报告）
	lastCharLen  int // 最后一个字符的字节长度

	// 词法记号文本缓冲区
	// 通常，词法记号文本完全存储在 srcBuf 中，但一般来说，词法记号文本的头部可能
	// 被缓冲在 tokBuf 中，而尾部存储在 srcBuf 中。
	tokBuf bytes.Buffer // 不再在 srcBuf 中的词法记号文本头部
	tokPos int          // 词法记号文本尾部位置（srcBuf 索引）；>= 0 时有效
	tokEnd int          // 词法记号文本尾部结束位置（srcBuf 索引）

	// 一个字符的预读
	ch rune // 当前 srcPos 之前的字符

	// Error 在遇到每个错误时被调用。如果没有设置 Error 函数，
	// 则错误会被报告到 os.Stderr。
	Error func(s *Scanner, msg string)

	// ErrorCount 在遇到每个错误时递增一。
	ErrorCount int

	// Mode 字段控制识别哪些词法记号。例如，要识别整数，
	// 请在 Mode 中设置 ScanInts 位。该字段可随时更改。
	Mode uint

	// Whitespace 字段控制哪些字符被识别为空白字符。要将字符 ch <= ' ' 识别为空白字符，
	// 请在 Whitespace 中设置第 ch 位（Scanner 对于 ch > ' ' 的值的行为是未定义的）。
	// 该字段可随时更改。
	Whitespace uint64

	// IsIdentRune 是一个谓词，控制哪些字符被接受为标识符中第 i 个字符。
	// 有效字符集不得与空白字符集相交。如果未设置 IsIdentRune 函数，
	// 则会接受常规的 Go 标识符。该字段可随时更改。
	IsIdentRune func(ch rune, i int) bool

	// 最近扫描的词法记号的起始位置；由 Scan 设置。
	// 调用 Init 或 Next 会使该位置失效（Line == 0）。
	// Scanner 始终不会修改 Filename 字段。
	// 如果报告了错误（通过 Error）且 Position 无效，则扫描器不在词法记号内。
	// 在这种情况下，调用 Pos 获取错误位置，或者获取最近扫描的词法记号之后的位置。
	Position
}

// Init 使用新的源初始化 [Scanner] 并返回 s。
// [Scanner.Error] 被设置为 nil，[Scanner.ErrorCount] 被设置为 0，
// [Scanner.Mode] 被设置为 [GoTokens]，[Scanner.Whitespace] 被设置为 [GoWhitespace]。
func (s *Scanner) Init(src io.Reader) *Scanner {
	s.src = src

	// 初始化源缓冲区
	//（第一次调用 next() 将通过调用 src.Read 来填充它）
	s.srcBuf[0] = utf8.RuneSelf // 哨兵
	s.srcPos = 0
	s.srcEnd = 0

	// 初始化源位置
	s.srcBufOffset = 0
	s.line = 1
	s.column = 0
	s.lastLineLen = 0
	s.lastCharLen = 0

	// 初始化词法记号文本缓冲区
	//（第一次调用 next() 时需要）。
	s.tokPos = -1

	// 初始化一个字符的预读
	s.ch = -2 // 尚未读取字符，不是 EOF

	// 初始化公共字段
	s.Error = nil
	s.ErrorCount = 0
	s.Mode = GoTokens
	s.Whitespace = GoWhitespace
	s.Line = 0 // 使词法记号位置失效

	return s
}

// next 读取并返回下一个 Unicode 字符。它的设计使得在常见的 ASCII
// 情况下只需要做最少的工作（一次测试同时检查 ASCII 和缓冲区结束，一次测试检查换行符）。
func (s *Scanner) next() rune {
	ch, width := rune(s.srcBuf[s.srcPos]), 1

	if ch >= utf8.RuneSelf {
		// 不常见的情况：非 ASCII 或字节不足
		for s.srcPos+utf8.UTFMax > s.srcEnd && !utf8.FullRune(s.srcBuf[s.srcPos:s.srcEnd]) {
			// 字节不足：读取更多，但首先
			// 保存词法记号文本（如果有的话）
			if s.tokPos >= 0 {
				s.tokBuf.Write(s.srcBuf[s.tokPos:s.srcPos])
				s.tokPos = 0
				// s.tokEnd 由 Scan() 设置
			}
			// 将未读字节移动到缓冲区开头
			copy(s.srcBuf[0:], s.srcBuf[s.srcPos:s.srcEnd])
			s.srcBufOffset += s.srcPos
			// 读取更多字节
			//（io.Reader 在读取完内容时必须返回 io.EOF——如果只是返回
			// n == 0，将导致此循环永远重试；在这种情况下错误在读取器的实现中）
			i := s.srcEnd - s.srcPos
			n, err := s.src.Read(s.srcBuf[i:bufLen])
			s.srcPos = 0
			s.srcEnd = i + n
			s.srcBuf[s.srcEnd] = utf8.RuneSelf // 哨兵
			if err != nil {
				if err != io.EOF {
					s.error(err.Error())
				}
				if s.srcEnd == 0 {
					if s.lastCharLen > 0 {
						// 前一个字符不是 EOF
						s.column++
					}
					s.lastCharLen = 0
					return EOF
				}
				// 如果 err == EOF，我们将不会再获得更多字节；break 以避免无限循环。
				// 如果 err 是其他错误，我们不知道是否能获得更多字节；因此也要 break。
				break
			}
		}
		// 至少一个字节
		ch = rune(s.srcBuf[s.srcPos])
		if ch >= utf8.RuneSelf {
			// 不常见的情况：非 ASCII
			ch, width = utf8.DecodeRune(s.srcBuf[s.srcPos:s.srcEnd])
			if ch == utf8.RuneError && width == 1 {
				// 前进以获得正确的错误位置
				s.srcPos += width
				s.lastCharLen = width
				s.column++
				s.error("invalid UTF-8 encoding")
				return ch
			}
		}
	}

	// 前进
	s.srcPos += width
	s.lastCharLen = width
	s.column++

	// 特殊情况
	switch ch {
	case 0:
		// 为了与其他工具兼容
		s.error("invalid character NUL")
	case '\n':
		s.line++
		s.lastLineLen = s.column
		s.column = 0
	}

	return ch
}

// Next 读取并返回下一个 Unicode 字符。
// 在源结束时返回 [EOF]。如果 s.Error 不为 nil，它会通过调用 s.Error 报告读取错误；
// 否则打印错误消息到 [os.Stderr]。Next 不更新 [Scanner.Position] 字段；
// 使用 [Scanner.Pos]() 获取当前位置。
func (s *Scanner) Next() rune {
	s.tokPos = -1 // don't collect token text
	s.Line = 0    // invalidate token position
	ch := s.Peek()
	if ch != EOF {
		s.ch = s.next()
	}
	return ch
}

// Peek 返回源中的下一个 Unicode 字符，但不推进扫描器。
// 如果扫描器位置在源的最后一个字符处，则返回 [EOF]。
func (s *Scanner) Peek() rune {
	if s.ch == -2 {
		// this code is only run for the very first character
		s.ch = s.next()
		if s.ch == '\uFEFF' {
			s.ch = s.next() // ignore BOM
		}
	}
	return s.ch
}

func (s *Scanner) error(msg string) {
	s.tokEnd = s.srcPos - s.lastCharLen // make sure token text is terminated
	s.ErrorCount++
	if s.Error != nil {
		s.Error(s, msg)
		return
	}
	pos := s.Position
	if !pos.IsValid() {
		pos = s.Pos()
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", pos, msg)
}

func (s *Scanner) errorf(format string, args ...any) {
	s.error(fmt.Sprintf(format, args...))
}

func (s *Scanner) isIdentRune(ch rune, i int) bool {
	if s.IsIdentRune != nil {
		return ch != EOF && s.IsIdentRune(ch, i)
	}
	return ch == '_' || unicode.IsLetter(ch) || unicode.IsDigit(ch) && i > 0
}

func (s *Scanner) scanIdentifier() rune {
	// 我们知道第零个字符是好的；从下一个开始扫描
	ch := s.next()
	for i := 1; s.isIdentRune(ch, i); i++ {
		ch = s.next()
	}
	return ch
}

func lower(ch rune) rune     { return ('a' - 'A') | ch } // returns lower-case ch iff ch is ASCII letter
func isDecimal(ch rune) bool { return '0' <= ch && ch <= '9' }
func isHex(ch rune) bool     { return '0' <= ch && ch <= '9' || 'a' <= lower(ch) && lower(ch) <= 'f' }

// digits 接受以 ch0 开头的序列 { digit | '_' }。
// 如果 base <= 10，digits 接受任何十进制数字，但如果 *invalid == 0，
// 会在 *invalid 中记录第一个 >= base 的无效数字。
// digits 返回不再是序列一部分的第一个字符，以及一个描述序列包含
// 数字（设置了第 0 位）或分隔符 '_'（设置了第 1 位）的位集。
func (s *Scanner) digits(ch0 rune, base int, invalid *rune) (ch rune, digsep int) {
	ch = ch0
	if base <= 10 {
		max := rune('0' + base)
		for isDecimal(ch) || ch == '_' {
			ds := 1
			if ch == '_' {
				ds = 2
			} else if ch >= max && *invalid == 0 {
				*invalid = ch
			}
			digsep |= ds
			ch = s.next()
		}
	} else {
		for isHex(ch) || ch == '_' {
			ds := 1
			if ch == '_' {
				ds = 2
			}
			digsep |= ds
			ch = s.next()
		}
	}
	return
}

func (s *Scanner) scanNumber(ch rune, seenDot bool) (rune, rune) {
	base := 10         // number base
	prefix := rune(0)  // one of 0 (decimal), '0' (0-octal), 'x', 'o', or 'b'
	digsep := 0        // bit 0: digit present, bit 1: '_' present
	invalid := rune(0) // invalid digit in literal, or 0

	// 整数部分
	var tok rune
	var ds int
	if !seenDot {
		tok = Int
		if ch == '0' {
			ch = s.next()
			switch lower(ch) {
			case 'x':
				ch = s.next()
				base, prefix = 16, 'x'
			case 'o':
				ch = s.next()
				base, prefix = 8, 'o'
			case 'b':
				ch = s.next()
				base, prefix = 2, 'b'
			default:
				base, prefix = 8, '0'
				digsep = 1 // leading 0
			}
		}
		ch, ds = s.digits(ch, base, &invalid)
		digsep |= ds
		if ch == '.' && s.Mode&ScanFloats != 0 {
			ch = s.next()
			seenDot = true
		}
	}

	// 小数部分
	if seenDot {
		tok = Float
		if prefix == 'o' || prefix == 'b' {
			s.error("invalid radix point in " + litname(prefix))
		}
		ch, ds = s.digits(ch, base, &invalid)
		digsep |= ds
	}

	if digsep&1 == 0 {
		s.error(litname(prefix) + " has no digits")
	}

	// 指数部分
	if e := lower(ch); (e == 'e' || e == 'p') && s.Mode&ScanFloats != 0 {
		switch {
		case e == 'e' && prefix != 0 && prefix != '0':
			s.errorf("%q exponent requires decimal mantissa", ch)
		case e == 'p' && prefix != 'x':
			s.errorf("%q exponent requires hexadecimal mantissa", ch)
		}
		ch = s.next()
		tok = Float
		if ch == '+' || ch == '-' {
			ch = s.next()
		}
		ch, ds = s.digits(ch, 10, nil)
		digsep |= ds
		if ds&1 == 0 {
			s.error("exponent has no digits")
		}
	} else if prefix == 'x' && tok == Float {
		s.error("hexadecimal mantissa requires a 'p' exponent")
	}

	if tok == Int && invalid != 0 {
		s.errorf("invalid digit %q in %s", invalid, litname(prefix))
	}

	if digsep&2 != 0 {
		s.tokEnd = s.srcPos - s.lastCharLen // make sure token text is terminated
		if i := invalidSep(s.TokenText()); i >= 0 {
			s.error("'_' must separate successive digits")
		}
	}

	return tok, ch
}

func litname(prefix rune) string {
	switch prefix {
	default:
		return "decimal literal"
	case 'x':
		return "hexadecimal literal"
	case 'o', '0':
		return "octal literal"
	case 'b':
		return "binary literal"
	}
}

// invalidSep 返回 x 中第一个无效分隔符的索引，或 -1。
func invalidSep(x string) int {
	x1 := ' ' // 前缀字符，我们只关心它是否是 'x'
	d := '.'  // 数字，为 '_'、'0'（一个数字）或 '.'（其他任何字符）之一
	i := 0

	// 前缀算作一个数字
	if len(x) >= 2 && x[0] == '0' {
		x1 = lower(rune(x[1]))
		if x1 == 'x' || x1 == 'o' || x1 == 'b' {
			d = '0'
			i = 2
		}
	}

	// 尾数和指数
	for ; i < len(x); i++ {
		p := d // previous digit
		d = rune(x[i])
		switch {
		case d == '_':
			if p != '0' {
				return i
			}
		case isDecimal(d) || x1 == 'x' && isHex(d):
			d = '0'
		default:
			if p == '_' {
				return i - 1
			}
			d = '.'
		}
	}
	if d == '_' {
		return len(x) - 1
	}

	return -1
}

func digitVal(ch rune) int {
	switch {
	case '0' <= ch && ch <= '9':
		return int(ch - '0')
	case 'a' <= lower(ch) && lower(ch) <= 'f':
		return int(lower(ch) - 'a' + 10)
	}
	return 16 // larger than any legal digit val
}

func (s *Scanner) scanDigits(ch rune, base, n int) rune {
	for n > 0 && digitVal(ch) < base {
		ch = s.next()
		n--
	}
	if n > 0 {
		s.error("invalid char escape")
	}
	return ch
}

func (s *Scanner) scanEscape(quote rune) rune {
	ch := s.next() // 读取 '/' 之后的字符
	switch ch {
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', quote:
		// 无需操作
		ch = s.next()
	case '0', '1', '2', '3', '4', '5', '6', '7':
		ch = s.scanDigits(ch, 8, 3)
	case 'x':
		ch = s.scanDigits(s.next(), 16, 2)
	case 'u':
		ch = s.scanDigits(s.next(), 16, 4)
	case 'U':
		ch = s.scanDigits(s.next(), 16, 8)
	default:
		s.error("invalid char escape")
	}
	return ch
}

func (s *Scanner) scanString(quote rune) (n int) {
	ch := s.next() // 读取引号之后的字符
	for ch != quote {
		if ch == '\n' || ch < 0 {
			s.error("literal not terminated")
			return
		}
		if ch == '\\' {
			ch = s.scanEscape(quote)
		} else {
			ch = s.next()
		}
		n++
	}
	return
}

func (s *Scanner) scanRawString() {
	ch := s.next() // 读取 '`' 之后的字符
	for ch != '`' {
		if ch < 0 {
			s.error("literal not terminated")
			return
		}
		ch = s.next()
	}
}

func (s *Scanner) scanChar() {
	if s.scanString('\'') != 1 {
		s.error("invalid char literal")
	}
}

func (s *Scanner) scanComment(ch rune) rune {
	// ch == '/' || ch == '*'
	if ch == '/' {
		// 行注释
		ch = s.next() // 读取 "//" 之后的字符
		for ch != '\n' && ch >= 0 {
			ch = s.next()
		}
		return ch
	}

	// 块注释
	ch = s.next() // 读取 "/*" 之后的字符
	for {
		if ch < 0 {
			s.error("comment not terminated")
			break
		}
		ch0 := ch
		ch = s.next()
		if ch0 == '*' && ch == '/' {
			ch = s.next()
			break
		}
	}
	return ch
}

// Scan 从源中读取下一个词法记号或 Unicode 字符并返回它。
// 它只识别设置了相应 [Scanner.Mode] 位 (1<<-t) 的词法记号 t。
// 在源结束时返回 [EOF]。如果 s.Error 不为 nil，它通过调用 s.Error 报告
// 扫描器错误（读取错误和词法记号错误）；否则打印错误消息到 [os.Stderr]。
func (s *Scanner) Scan() rune {
	ch := s.Peek()

	// 重置词法记号文本位置
	s.tokPos = -1
	s.Line = 0

redo:
	// 跳过空白字符
	for s.Whitespace&(1<<uint(ch)) != 0 {
		ch = s.next()
	}

	// 开始收集词法记号文本
	s.tokBuf.Reset()
	s.tokPos = s.srcPos - s.lastCharLen

	// 设置词法记号位置
	//（这是 Pos() 中代码的稍微优化版本）
	s.Offset = s.srcBufOffset + s.tokPos
	if s.column > 0 {
		// 常见情况：最后一个字符不是 '\n'
		s.Line = s.line
		s.Column = s.column
	} else {
		// 最后一个字符是 '\n'
		//（由于我们至少调用过一次 next()，我们不可能在源的开头）
		s.Line = s.line - 1
		s.Column = s.lastLineLen
	}

	// 确定词法记号值
	tok := ch
	switch {
	case s.isIdentRune(ch, 0):
		if s.Mode&ScanIdents != 0 {
			tok = Ident
			ch = s.scanIdentifier()
		} else {
			ch = s.next()
		}
	case isDecimal(ch):
		if s.Mode&(ScanInts|ScanFloats) != 0 {
			tok, ch = s.scanNumber(ch, false)
		} else {
			ch = s.next()
		}
	default:
		switch ch {
		case EOF:
			break
		case '"':
			if s.Mode&ScanStrings != 0 {
				s.scanString('"')
				tok = String
			}
			ch = s.next()
		case '\'':
			if s.Mode&ScanChars != 0 {
				s.scanChar()
				tok = Char
			}
			ch = s.next()
		case '.':
			ch = s.next()
			if isDecimal(ch) && s.Mode&ScanFloats != 0 {
				tok, ch = s.scanNumber(ch, true)
			}
	case '/':
		ch = s.next()
		if (ch == '/' || ch == '*') && s.Mode&ScanComments != 0 {
			if s.Mode&SkipComments != 0 {
				s.tokPos = -1 // 不收集词法记号文本
				ch = s.scanComment(ch)
				goto redo
			}
			ch = s.scanComment(ch)
			tok = Comment
		}
		ch = s.next()
		_ = tok
	}

	// 词法记号文本结束
	s.tokEnd = s.srcPos - s.lastCharLen

	s.ch = ch
	return tok
}

// Pos 返回上一次调用 [Scanner.Next] 或 [Scanner.Scan] 所返回的字符或词法记号
// 之后那个字符的位置。使用 [Scanner.Position] 字段获取最近扫描的词法记号的起始位置。
func (s *Scanner) Pos() (pos Position) {
	pos.Filename = s.Filename
	pos.Offset = s.srcBufOffset + s.srcPos - s.lastCharLen
	switch {
	case s.column > 0:
		// 常见情况：最后一个字符不是 '\n'
		pos.Line = s.line
		pos.Column = s.column
	case s.lastLineLen > 0:
		// 最后一个字符是 '\n'
		pos.Line = s.line - 1
		pos.Column = s.lastLineLen
	default:
		// 在源的开头
		pos.Line = 1
		pos.Column = 1
	}
	return
}

// TokenText 返回最近扫描的词法记号对应的字符串。
// 在调用 [Scanner.Scan] 之后以及在 [Scanner.Error] 调用中有效。
func (s *Scanner) TokenText() string {
	if s.tokPos < 0 {
		// 没有词法记号文本
		return ""
	}

	if s.tokEnd < s.tokPos {
		// 如果到达了 EOF，s.tokEnd 被设置为 -1（s.srcPos == 0）
		s.tokEnd = s.tokPos
	}
	// s.tokEnd >= s.tokPos

	if s.tokBuf.Len() == 0 {
		// 常见情况：整个词法记号文本仍在 srcBuf 中
		return string(s.srcBuf[s.tokPos:s.tokEnd])
	}

	// 部分词法记号文本被保存在 tokBuf 中：也将剩余部分保存到
	// tokBuf 并返回其内容
	s.tokBuf.Write(s.srcBuf[s.tokPos:s.tokEnd])
	s.tokPos = s.tokEnd // 确保 TokenText() 调用的幂等性
	return s.tokBuf.String()
}
