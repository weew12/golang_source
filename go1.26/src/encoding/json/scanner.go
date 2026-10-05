// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.jsonv2

package json

// JSON 值解析器状态机。
// 几乎达到了手写代码的合理极限。
// 有些部分有点繁琐，但总体上它很好地分离了
// 本包中多个扫描函数（Compact、Indent、checkValid 等）
// 中原本共有的代码。
//
// 本文件以两个使用 scanner 的简单示例开头，
// 然后才深入 scanner 本身。

import (
	"strconv"
	"sync"
)

// Valid 报告 data 是否是有效的 JSON 编码。
func Valid(data []byte) bool {
	scan := newScanner()
	defer freeScanner(scan)
	return checkValid(data, scan) == nil
}

// checkValid 验证 data 是有效的 JSON 编码数据。
// 传入 scan 以供 checkValid 使用，以避免分配内存。
// checkValid 返回 nil 或 SyntaxError。
func checkValid(data []byte, scan *scanner) error {
	scan.reset()
	for _, c := range data {
		scan.bytes++
		if scan.step(scan, c) == scanError {
			return scan.err
		}
	}
	if scan.eof() == scanError {
		return scan.err
	}
	return nil
}

// SyntaxError 是对 JSON 语法错误的描述。
// 如果 JSON 无法被解析，[Unmarshal] 将返回 SyntaxError。
type SyntaxError struct {
	msg    string // 错误描述
	Offset int64  // 错误发生在读取 Offset 个字节之后
}

func (e *SyntaxError) Error() string { return e.msg }

// scanner 是一个 JSON 扫描状态机。
// 调用者调用 scan.reset，然后通过对每个字节调用
// scan.step(&scan, c) 一次传入一个字节。
// 返回值（称为操作码）告诉调用者一些重要的解析事件，
// 比如字面量、对象和数组的开始与结束，
// 以便调用者可以在需要时跟进。
// 返回值 scanEnd 表示单个顶层 JSON 值已完成，
// *在* 刚传入的字节*之前*。（必须延迟此指示，
// 才能识别数字的结束：123 是一个完整的值，
// 还是 12345e+6 的开头？）
type scanner struct {
	// step 是一个被调用以执行下一次转换的函数。
	// 也曾尝试使用一个整数常量和一个带 switch 的单一函数，
	// 但直接使用函数在 64 位 Mac Mini 上快 10%，
	// 而且更易读。
	step func(*scanner, byte) int

	// 已到达顶层值的末尾。
	endTop bool

	// 当前所处位置的栈——数组值、对象键、对象值。
	parseState []int

	// 发生的错误（如果有）。
	err error

	// 已消耗的总字节数，由 decoder.Decode 更新
	// （并且刻意不被 scan.reset 置零）
	bytes int64
}

var scannerPool = sync.Pool{
	New: func() any {
		return &scanner{}
	},
}

func newScanner() *scanner {
	scan := scannerPool.Get().(*scanner)
	// scan.reset 按设计不会将 bytes 置零
	scan.bytes = 0
	scan.reset()
	return scan
}

func freeScanner(scan *scanner) {
	// 在极端情况下避免占用过多内存。
	if len(scan.parseState) > 1024 {
		scan.parseState = nil
	}
	scannerPool.Put(scan)
}

// 这些值由分配给 scanner.state 的状态转换函数
// 和 scanner.eof 方法返回。
// 它们提供了调用者可能感兴趣的扫描当前状态的详细信息。
// 忽略对 scanner.state 的任何特定调用的返回值是可以的：
// 如果某次调用返回 scanError，则之后的每次调用也会返回 scanError。
const (
	// 继续。
	scanContinue     = iota // 无意义字节
	scanBeginLiteral        // 结束由下一个非 scanContinue 的结果暗示
	scanBeginObject         // 开始对象
	scanObjectKey           // 刚完成对象键（字符串）
	scanObjectValue         // 刚完成非最后一个对象值
	scanEndObject           // 结束对象（如果可能则暗示 scanObjectValue）
	scanBeginArray          // 开始数组
	scanArrayValue          // 刚完成数组值
	scanEndArray            // 结束数组（如果可能则暗示 scanArrayValue）
	scanSkipSpace           // 空格字节；可跳过；已知是最后一个 "continue" 结果

	// 停止。
	scanEnd   // 顶层值在*此字节之前*结束；已知是第一个 "stop" 结果
	scanError // 遇到错误，scanner.err。
)

// 这些值存储在 parseState 栈中。
// 它们给出了正在扫描的复合值的当前状态。
// 如果解析器位于嵌套值内部，
// parseState 描述的是嵌套状态，最外层在条目 0。
const (
	parseObjectKey   = iota // 正在解析对象键（冒号之前）
	parseObjectValue        // 正在解析对象值（冒号之后）
	parseArrayValue         // 正在解析数组值
)

// 这限制了最大嵌套深度以防止栈溢出。
// 这是 https://tools.ietf.org/html/rfc7159#section-9 所允许的。
const maxNestingDepth = 10000

// reset 准备 scanner 以供使用。
// 必须在调用 s.step 之前调用它。
func (s *scanner) reset() {
	s.step = stateBeginValue
	s.parseState = s.parseState[0:0]
	s.err = nil
	s.endTop = false
}

// eof 告知 scanner 已到达输入末尾。
// 与 s.step 一样，它返回扫描状态。
func (s *scanner) eof() int {
	if s.err != nil {
		return scanError
	}
	if s.endTop {
		return scanEnd
	}
	s.step(s, ' ')
	if s.endTop {
		return scanEnd
	}
	if s.err == nil {
		s.err = &SyntaxError{"unexpected end of JSON input", s.bytes}
	}
	return scanError
}

// pushParseState 将一个新的解析状态 newParseState 压入解析栈。
// 如果超出 maxNestingDepth 则返回错误状态，否则返回 successState。
func (s *scanner) pushParseState(c byte, newParseState int, successState int) int {
	s.parseState = append(s.parseState, newParseState)
	if len(s.parseState) <= maxNestingDepth {
		return successState
	}
	return s.error(c, "exceeded max depth")
}

// popParseState 从栈中弹出一个解析状态（已获取），
// 并相应地更新 s.step。
func (s *scanner) popParseState() {
	n := len(s.parseState) - 1
	s.parseState = s.parseState[0:n]
	if n == 0 {
		s.step = stateEndTop
		s.endTop = true
	} else {
		s.step = stateEndValue
	}
}

func isSpace(c byte) bool {
	return c <= ' ' && (c == ' ' || c == '\t' || c == '\r' || c == '\n')
}

// stateBeginValueOrEmpty 是读取 `[` 之后的状态。
func stateBeginValueOrEmpty(s *scanner, c byte) int {
	if isSpace(c) {
		return scanSkipSpace
	}
	if c == ']' {
		return stateEndValue(s, c)
	}
	return stateBeginValue(s, c)
}

// stateBeginValue 是输入开始时的状态。
func stateBeginValue(s *scanner, c byte) int {
	if isSpace(c) {
		return scanSkipSpace
	}
	switch c {
	case '{':
		s.step = stateBeginStringOrEmpty
		return s.pushParseState(c, parseObjectKey, scanBeginObject)
	case '[':
		s.step = stateBeginValueOrEmpty
		return s.pushParseState(c, parseArrayValue, scanBeginArray)
	case '"':
		s.step = stateInString
		return scanBeginLiteral
	case '-':
		s.step = stateNeg
		return scanBeginLiteral
	case '0': // 0.123 的开头
		s.step = state0
		return scanBeginLiteral
	case 't': // true 的开头
		s.step = stateT
		return scanBeginLiteral
	case 'f': // false 的开头
		s.step = stateF
		return scanBeginLiteral
	case 'n': // null 的开头
		s.step = stateN
		return scanBeginLiteral
	}
	if '1' <= c && c <= '9' { // 1234.5 的开头
		s.step = state1
		return scanBeginLiteral
	}
	return s.error(c, "looking for beginning of value")
}

// stateBeginStringOrEmpty 是读取 `{` 之后的状态。
func stateBeginStringOrEmpty(s *scanner, c byte) int {
	if isSpace(c) {
		return scanSkipSpace
	}
	if c == '}' {
		n := len(s.parseState)
		s.parseState[n-1] = parseObjectValue
		return stateEndValue(s, c)
	}
	return stateBeginString(s, c)
}

// stateBeginString 是读取 `{"key": value,` 之后的状态。
func stateBeginString(s *scanner, c byte) int {
	if isSpace(c) {
		return scanSkipSpace
	}
	if c == '"' {
		s.step = stateInString
		return scanBeginLiteral
	}
	return s.error(c, "looking for beginning of object key string")
}

// stateEndValue 是完成一个值之后的状态，
// 例如读取 `{}` 或 `true` 或 `["x"` 之后。
func stateEndValue(s *scanner, c byte) int {
	n := len(s.parseState)
	if n == 0 {
		// 在当前字节之前完成了顶层值。
		s.step = stateEndTop
		s.endTop = true
		return stateEndTop(s, c)
	}
	if isSpace(c) {
		s.step = stateEndValue
		return scanSkipSpace
	}
	ps := s.parseState[n-1]
	switch ps {
	case parseObjectKey:
		if c == ':' {
			s.parseState[n-1] = parseObjectValue
			s.step = stateBeginValue
			return scanObjectKey
		}
		return s.error(c, "after object key")
	case parseObjectValue:
		if c == ',' {
			s.parseState[n-1] = parseObjectKey
			s.step = stateBeginString
			return scanObjectValue
		}
		if c == '}' {
			s.popParseState()
			return scanEndObject
		}
		return s.error(c, "after object key:value pair")
	case parseArrayValue:
		if c == ',' {
			s.step = stateBeginValue
			return scanArrayValue
		}
		if c == ']' {
			s.popParseState()
			return scanEndArray
		}
		return s.error(c, "after array element")
	}
	return s.error(c, "")
}

// stateEndTop 是完成顶层值之后的状态，
// 例如读取 `{}` 或 `[1,2,3]` 之后。
// 此时只能看到空白字符。
func stateEndTop(s *scanner, c byte) int {
	if !isSpace(c) {
		// 在下一次调用时对非空白字节提出异议。
		s.error(c, "after top-level value")
	}
	return scanEnd
}

// stateInString 是读取 `"` 之后的状态。
func stateInString(s *scanner, c byte) int {
	if c == '"' {
		s.step = stateEndValue
		return scanContinue
	}
	if c == '\\' {
		s.step = stateInStringEsc
		return scanContinue
	}
	if c < 0x20 {
		return s.error(c, "in string literal")
	}
	return scanContinue
}

// stateInStringEsc 是在引号字符串中读取 `"\` 之后的状态。
func stateInStringEsc(s *scanner, c byte) int {
	switch c {
	case 'b', 'f', 'n', 'r', 't', '\\', '/', '"':
		s.step = stateInString
		return scanContinue
	case 'u':
		s.step = stateInStringEscU
		return scanContinue
	}
	return s.error(c, "in string escape code")
}

// stateInStringEscU 是在引号字符串中读取 `"\u` 之后的状态。
func stateInStringEscU(s *scanner, c byte) int {
	if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
		s.step = stateInStringEscU1
		return scanContinue
	}
	// numbers（数字）
	return s.error(c, "in \\u hexadecimal character escape")
}

// stateInStringEscU1 是在引号字符串中读取 `"\u1` 之后的状态。
func stateInStringEscU1(s *scanner, c byte) int {
	if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
		s.step = stateInStringEscU12
		return scanContinue
	}
	// numbers（数字）
	return s.error(c, "in \\u hexadecimal character escape")
}

// stateInStringEscU12 是在引号字符串中读取 `"\u12` 之后的状态。
func stateInStringEscU12(s *scanner, c byte) int {
	if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
		s.step = stateInStringEscU123
		return scanContinue
	}
	// numbers（数字）
	return s.error(c, "in \\u hexadecimal character escape")
}

// stateInStringEscU123 是在引号字符串中读取 `"\u123` 之后的状态。
func stateInStringEscU123(s *scanner, c byte) int {
	if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' {
		s.step = stateInString
		return scanContinue
	}
	// numbers（数字）
	return s.error(c, "in \\u hexadecimal character escape")
}

// stateNeg 是在数字中读取 `-` 之后的状态。
func stateNeg(s *scanner, c byte) int {
	if c == '0' {
		s.step = state0
		return scanContinue
	}
	if '1' <= c && c <= '9' {
		s.step = state1
		return scanContinue
	}
	return s.error(c, "in numeric literal")
}

// state1 是在数字中读取一个非零整数之后的状态，
// 例如读取 `1` 或 `100` 之后，但不包括 `0`。
func state1(s *scanner, c byte) int {
	if '0' <= c && c <= '9' {
		s.step = state1
		return scanContinue
	}
	return state0(s, c)
}

// state0 是在数字中读取 `0` 之后的状态。
func state0(s *scanner, c byte) int {
	if c == '.' {
		s.step = stateDot
		return scanContinue
	}
	if c == 'e' || c == 'E' {
		s.step = stateE
		return scanContinue
	}
	return stateEndValue(s, c)
}

// stateDot 是在数字中读取整数部分和小数点之后的状态，
// 例如读取 `1.` 之后。
func stateDot(s *scanner, c byte) int {
	if '0' <= c && c <= '9' {
		s.step = stateDot0
		return scanContinue
	}
	return s.error(c, "after decimal point in numeric literal")
}

// stateDot0 是在数字中读取整数部分、小数点及其后续数字之后的状态，
// 例如读取 `3.14` 之后。
func stateDot0(s *scanner, c byte) int {
	if '0' <= c && c <= '9' {
		return scanContinue
	}
	if c == 'e' || c == 'E' {
		s.step = stateE
		return scanContinue
	}
	return stateEndValue(s, c)
}

// stateE 是在数字中读取尾数和 e 之后的状态，
// 例如读取 `314e` 或 `0.314e` 之后。
func stateE(s *scanner, c byte) int {
	if c == '+' || c == '-' {
		s.step = stateESign
		return scanContinue
	}
	return stateESign(s, c)
}

// stateESign 是在数字中读取尾数、e 和符号之后的状态，
// 例如读取 `314e-` 或 `0.314e+` 之后。
func stateESign(s *scanner, c byte) int {
	if '0' <= c && c <= '9' {
		s.step = stateE0
		return scanContinue
	}
	return s.error(c, "in exponent of numeric literal")
}

// stateE0 是在数字中读取尾数、e、可选符号
// 和至少一个指数数字之后的状态，
// 例如读取 `314e-2` 或 `0.314e+1` 或 `3.14e0` 之后。
func stateE0(s *scanner, c byte) int {
	if '0' <= c && c <= '9' {
		return scanContinue
	}
	return stateEndValue(s, c)
}

// stateT 是读取 `t` 之后的状态。
func stateT(s *scanner, c byte) int {
	if c == 'r' {
		s.step = stateTr
		return scanContinue
	}
	return s.error(c, "in literal true (expecting 'r')")
}

// stateTr 是读取 `tr` 之后的状态。
func stateTr(s *scanner, c byte) int {
	if c == 'u' {
		s.step = stateTru
		return scanContinue
	}
	return s.error(c, "in literal true (expecting 'u')")
}

// stateTru 是读取 `tru` 之后的状态。
func stateTru(s *scanner, c byte) int {
	if c == 'e' {
		s.step = stateEndValue
		return scanContinue
	}
	return s.error(c, "in literal true (expecting 'e')")
}

// stateF 是读取 `f` 之后的状态。
func stateF(s *scanner, c byte) int {
	if c == 'a' {
		s.step = stateFa
		return scanContinue
	}
	return s.error(c, "in literal false (expecting 'a')")
}

// stateFa 是读取 `fa` 之后的状态。
func stateFa(s *scanner, c byte) int {
	if c == 'l' {
		s.step = stateFal
		return scanContinue
	}
	return s.error(c, "in literal false (expecting 'l')")
}

// stateFal 是读取 `fal` 之后的状态。
func stateFal(s *scanner, c byte) int {
	if c == 's' {
		s.step = stateFals
		return scanContinue
	}
	return s.error(c, "in literal false (expecting 's')")
}

// stateFals 是读取 `fals` 之后的状态。
func stateFals(s *scanner, c byte) int {
	if c == 'e' {
		s.step = stateEndValue
		return scanContinue
	}
	return s.error(c, "in literal false (expecting 'e')")
}

// stateN 是读取 `n` 之后的状态。
func stateN(s *scanner, c byte) int {
	if c == 'u' {
		s.step = stateNu
		return scanContinue
	}
	return s.error(c, "in literal null (expecting 'u')")
}

// stateNu 是读取 `nu` 之后的状态。
func stateNu(s *scanner, c byte) int {
	if c == 'l' {
		s.step = stateNul
		return scanContinue
	}
	return s.error(c, "in literal null (expecting 'l')")
}

// stateNul 是读取 `nul` 之后的状态。
func stateNul(s *scanner, c byte) int {
	if c == 'l' {
		s.step = stateEndValue
		return scanContinue
	}
	return s.error(c, "in literal null (expecting 'l')")
}

// stateError 是达到语法错误之后的状态，
// 例如读取 `[1}` 或 `5.1.2` 之后。
func stateError(s *scanner, c byte) int {
	return scanError
}

// error 记录一个错误并切换到错误状态。
func (s *scanner) error(c byte, context string) int {
	s.step = stateError
	s.err = &SyntaxError{"invalid character " + quoteChar(c) + " " + context, s.bytes}
	return scanError
}

// quoteChar 将 c 格式化为带引号的字符字面量。
func quoteChar(c byte) string {
	// 特殊情况——与带引号的字符串不同
	if c == '\'' {
		return `'\''`
	}
	if c == '"' {
		return `'"'`
	}

	// 使用不同引号的带引号字符串
	s := strconv.Quote(string(c))
	return "'" + s[1:len(s)-1] + "'"
}
