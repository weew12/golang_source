// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parse

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// item 表示从扫描器返回的词法记号或文本字符串。
type item struct {
	typ  itemType // 此 item 的类型。
	pos  Pos      // 此 item 在输入字符串中的起始位置（字节）。
	val  string   // 此 item 的值。
	line int      // 此 item 起始处的行号。
}

func (i item) String() string {
	switch {
	case i.typ == itemEOF:
		return "EOF"
	case i.typ == itemError:
		return i.val
	case i.typ > itemKeyword:
		return fmt.Sprintf("<%s>", i.val)
	case len(i.val) > 10:
		return fmt.Sprintf("%.10q...", i.val)
	}
	return fmt.Sprintf("%q", i.val)
}

// itemType 标识词法项的类型。
type itemType int

const (
	itemError        itemType = iota // 发生错误；值为错误文本
	itemBool                         // 布尔常量
	itemChar                         // 可打印的 ASCII 字符；逗号等的杂项
	itemCharConstant                 // 字符常量
	itemComment                      // 注释文本
	itemComplex                      // 复数常量 (1+2i)；虚数只是一个数字
	itemAssign                       // 等号 ('=') 引入赋值
	itemDeclare                      // 冒号等号 (':=') 引入声明
	itemEOF
	itemField      // 以 '.' 开头的字母数字标识符
	itemIdentifier // 不以 '.' 开头的字母数字标识符
	itemLeftDelim  // 左动作分隔符
	itemLeftParen  // 动作内的 '('
	itemNumber     // 简单数字，包括虚数
	itemPipe       // 管道符号
	itemRawString  // 原始带引号字符串（包括引号）
	itemRightDelim // 右动作分隔符
	itemRightParen // 动作内的 ')'
	itemSpace      // 分隔参数的空格序列
	itemString     // 带引号字符串（包括引号）
	itemText       // 纯文本
	itemVariable   // 以 '$' 开头的变量，如 '$' 或 '$1' 或 '$hello'
	// 关键字出现在所有其余之后。
	itemKeyword  // 仅用于分隔关键字
	itemBlock    // block 关键字
	itemBreak    // break 关键字
	itemContinue // continue 关键字
	itemDot      // 游标，拼写为 '.'
	itemDefine   // define 关键字
	itemElse     // else 关键字
	itemEnd      // end 关键字
	itemIf       // if 关键字
	itemNil      // 无类型的 nil 常量，最容易当作关键字处理
	itemRange    // range 关键字
	itemTemplate // template 关键字
	itemWith     // with 关键字
)

var key = map[string]itemType{
	".":        itemDot,
	"block":    itemBlock,
	"break":    itemBreak,
	"continue": itemContinue,
	"define":   itemDefine,
	"else":     itemElse,
	"end":      itemEnd,
	"if":       itemIf,
	"range":    itemRange,
	"nil":      itemNil,
	"template": itemTemplate,
	"with":     itemWith,
}

const eof = -1

// 修剪空格。
// 如果动作以 "{{- " 而非 "{{" 开始，则所有在动作之前的 space/tab/newlines 都会被修剪；
// 反之如果以 " -}}" 结束，则会修剪后续的前导空格。这完全在词法分析器中完成；
// 解析器永远不会看到它发生。我们需要一个 ASCII 空格 (' ', \t, \r, \n)
// 存在以避免与 "{{-3}}" 之类的东西产生歧义。
// 无论如何，有空格存在时阅读起来更好。为简单起见，只有 ASCII 就行。
const (
	spaceChars    = " \t\r\n"  // 这些是 Go 自己定义的空格字符。
	trimMarker    = '-'        // 附加到左/右分隔符，修剪前/后文本的尾随空格。
	trimMarkerLen = Pos(1 + 1) // 标记加上之前或之后的空间
)

// stateFn 将扫描器的状态表示为返回下一个状态的函数。
type stateFn func(*lexer) stateFn

// lexer 持有扫描器的状态。
type lexer struct {
	name         string // 输入的名称；仅用于错误报告
	input        string // 被扫描的字符串
	leftDelim    string // 动作标记的开始
	rightDelim   string // 动作标记的结束
	pos          Pos    // 输入中的当前位置
	start        Pos    // 此 item 的起始位置
	atEOF        bool   // 我们已到达输入末尾并返回 eof
	parenDepth   int    // ( ) 表达式的嵌套深度
	line         int    // 1+看到的换行符数
	startLine    int    // 此 item 的起始行
	item         item   // 返回给解析器的 item
	insideAction bool   // 我们是否在动作内部？
	options      lexOptions
}

// lexOptions 控制词法分析器的行为。默认都为 false。
type lexOptions struct {
	emitComment bool // 发出 itemComment 词法项。
	breakOK     bool // break 关键字允许
	continueOK  bool // continue 关键字允许
}

// next 返回输入中的下一个 rune。
func (l *lexer) next() rune {
	if int(l.pos) >= len(l.input) {
		l.atEOF = true
		return eof
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos += Pos(w)
	if r == '\n' {
		l.line++
	}
	return r
}

// peek 返回但不消费输入中的下一个 rune。
func (l *lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

// backup 后退一个 rune。
func (l *lexer) backup() {
	if !l.atEOF && l.pos > 0 {
		r, w := utf8.DecodeLastRuneInString(l.input[:l.pos])
		l.pos -= Pos(w)
		// 修正换行计数。
		if r == '\n' {
			l.line--
		}
	}
}

// thisItem 返回当前输入点的 item，具有指定的类型并推进输入。
func (l *lexer) thisItem(t itemType) item {
	i := item{t, l.start, l.input[l.start:l.pos], l.startLine}
	l.start = l.pos
	l.startLine = l.line
	return i
}

// emit 将尾随文本作为 item 传回解析器。
func (l *lexer) emit(t itemType) stateFn {
	return l.emitItem(l.thisItem(t))
}

// emitItem 将指定的 item 传给解析器。
func (l *lexer) emitItem(i item) stateFn {
	l.item = i
	return nil
}

// ignore 跳过此点之前的待处理输入。
// 它跟踪被忽略文本中的换行符，因此仅用于跳过而不调用 l.next 的文本。
func (l *lexer) ignore() {
	l.line += strings.Count(l.input[l.start:l.pos], "\n")
	l.start = l.pos
	l.startLine = l.line
}

// accept 如果下一个 rune 来自有效集合，则消费它。
func (l *lexer) accept(valid string) bool {
	if strings.ContainsRune(valid, l.next()) {
		return true
	}
	l.backup()
	return false
}

// acceptRun 消费来自有效集合的 rune 序列。
func (l *lexer) acceptRun(valid string) {
	for strings.ContainsRune(valid, l.next()) {
	}
	l.backup()
}

// errorf 返回错误词法项并通过返回将终止扫描的 nil 指针来终止扫描，
// 该指针将是下一个状态，终止 l.nextItem。
func (l *lexer) errorf(format string, args ...any) stateFn {
	l.item = item{itemError, l.start, fmt.Sprintf(format, args...), l.startLine}
	l.start = 0
	l.pos = 0
	l.input = l.input[:0]
	return nil
}

// nextItem 从输入返回下一个 item。
// 由解析器调用，不在词法分析 goroutine 中。
func (l *lexer) nextItem() item {
	l.item = item{itemEOF, l.pos, "EOF", l.startLine}
	state := lexText
	if l.insideAction {
		state = lexInsideAction
	}
	for {
		state = state(l)
		if state == nil {
			return l.item
		}
	}
}

// lex 为输入字符串创建新的扫描器。
func lex(name, input, left, right string) *lexer {
	if left == "" {
		left = leftDelim
	}
	if right == "" {
		right = rightDelim
	}
	l := &lexer{
		name:         name,
		input:        input,
		leftDelim:    left,
		rightDelim:   right,
		line:         1,
		startLine:    1,
		insideAction: false,
	}
	return l
}

// 状态函数

const (
	leftDelim    = "{{"
	rightDelim   = "}}"
	leftComment  = "/*"
	rightComment = "*/"
)

// lexText 扫描直到遇到开行动作分隔符 "{{"。
func lexText(l *lexer) stateFn {
	if x := strings.Index(l.input[l.pos:], l.leftDelim); x >= 0 {
		if x > 0 {
			l.pos += Pos(x)
			// 我们修剪尾随空格吗？
			trimLength := Pos(0)
			delimEnd := l.pos + Pos(len(l.leftDelim))
			if hasLeftTrimMarker(l.input[delimEnd:]) {
				trimLength = rightTrimLength(l.input[l.start:l.pos])
			}
			l.pos -= trimLength
			l.line += strings.Count(l.input[l.start:l.pos], "\n")
			i := l.thisItem(itemText)
			l.pos += trimLength
			l.ignore()
			if len(i.val) > 0 {
				return l.emitItem(i)
			}
		}
		return lexLeftDelim
	}
	l.pos = Pos(len(l.input))
	// 正确到达 EOF。
	if l.pos > l.start {
		l.line += strings.Count(l.input[l.start:l.pos], "\n")
		return l.emit(itemText)
	}
	return l.emit(itemEOF)
}

// rightTrimLength 返回字符串末尾空格的长度。
func rightTrimLength(s string) Pos {
	return Pos(len(s) - len(strings.TrimRight(s, spaceChars)))
}

// atRightDelim 报告词法分析器是否在右分隔符处，可能前面有修剪标记。
func (l *lexer) atRightDelim() (delim, trimSpaces bool) {
	if hasRightTrimMarker(l.input[l.pos:]) && strings.HasPrefix(l.input[l.pos+trimMarkerLen:], l.rightDelim) { // 带修剪标记。
		return true, true
	}
	if strings.HasPrefix(l.input[l.pos:], l.rightDelim) { // 不带修剪标记。
		return true, false
	}
	return false, false
}

// leftTrimLength 返回字符串开头空格的长度。
func leftTrimLength(s string) Pos {
	return Pos(len(s) - len(strings.TrimLeft(s, spaceChars)))
}

// lexLeftDelim 扫描左分隔符，已知它存在，可能带有修剪标记。
//（要修剪的文本已经被发出。）
func lexLeftDelim(l *lexer) stateFn {
	l.pos += Pos(len(l.leftDelim))
	trimSpace := hasLeftTrimMarker(l.input[l.pos:])
	afterMarker := Pos(0)
	if trimSpace {
		afterMarker = trimMarkerLen
	}
	if strings.HasPrefix(l.input[l.pos+afterMarker:], leftComment) {
		l.pos += afterMarker
		l.ignore()
		return lexComment
	}
	i := l.thisItem(itemLeftDelim)
	l.insideAction = true
	l.pos += afterMarker
	l.ignore()
	l.parenDepth = 0
	return l.emitItem(i)
}

// lexComment 扫描注释。已知左注释标记存在。
func lexComment(l *lexer) stateFn {
	l.pos += Pos(len(leftComment))
	x := strings.Index(l.input[l.pos:], rightComment)
	if x < 0 {
		return l.errorf("unclosed comment")
	}
	l.pos += Pos(x + len(rightComment))
	delim, trimSpace := l.atRightDelim()
	if !delim {
		return l.errorf("comment ends before closing delimiter")
	}
	l.line += strings.Count(l.input[l.start:l.pos], "\n")
	i := l.thisItem(itemComment)
	if trimSpace {
		l.pos += trimMarkerLen
	}
	l.pos += Pos(len(l.rightDelim))
	if trimSpace {
		l.pos += leftTrimLength(l.input[l.pos:])
	}
	l.ignore()
	if l.options.emitComment {
		return l.emitItem(i)
	}
	return lexText
}

// lexRightDelim 扫描右分隔符，已知它存在，可能带有修剪标记。
func lexRightDelim(l *lexer) stateFn {
	_, trimSpace := l.atRightDelim()
	if trimSpace {
		l.pos += trimMarkerLen
		l.ignore()
	}
	l.pos += Pos(len(l.rightDelim))
	i := l.thisItem(itemRightDelim)
	if trimSpace {
		l.pos += leftTrimLength(l.input[l.pos:])
		l.ignore()
	}
	l.insideAction = false
	return l.emitItem(i)
}

// lexInsideAction 扫描动作分隔符内的元素。
func lexInsideAction(l *lexer) stateFn {
	// Either number, quoted string, or identifier.
	// Spaces separate arguments; runs of spaces turn into itemSpace.
	// Pipe symbols separate and are emitted.
	delim, _ := l.atRightDelim()
	if delim {
		if l.parenDepth == 0 {
			return lexRightDelim
		}
		return l.errorf("unclosed left paren")
	}
	switch r := l.next(); {
	case r == eof:
		return l.errorf("unclosed action")
	case isSpace(r):
		l.backup() // Put space back in case we have " -}}".
		return lexSpace
	case r == '=':
		return l.emit(itemAssign)
	case r == ':':
		if l.next() != '=' {
			return l.errorf("expected :=")
		}
		return l.emit(itemDeclare)
	case r == '|':
		return l.emit(itemPipe)
	case r == '"':
		return lexQuote
	case r == '`':
		return lexRawQuote
	case r == '$':
		return lexVariable
	case r == '\'':
		return lexChar
	case r == '.':
		// special look-ahead for ".field" so we don't break l.backup().
		if l.pos < Pos(len(l.input)) {
			r := l.input[l.pos]
			if r < '0' || '9' < r {
				return lexField
			}
		}
		fallthrough // '.' can start a number.
	case r == '+' || r == '-' || ('0' <= r && r <= '9'):
		l.backup()
		return lexNumber
	case isAlphaNumeric(r):
		l.backup()
		return lexIdentifier
	case r == '(':
		l.parenDepth++
		return l.emit(itemLeftParen)
	case r == ')':
		l.parenDepth--
		if l.parenDepth < 0 {
			return l.errorf("unexpected right paren")
		}
		return l.emit(itemRightParen)
	case r <= unicode.MaxASCII && unicode.IsPrint(r):
		return l.emit(itemChar)
	default:
		return l.errorf("unrecognized character in action: %#U", r)
	}
}

// lexSpace 扫描空格字符序列。
// 我们还没有消费第一个空格，它是已知的。
// 如果有修剪标记的右分隔符（以空格开头）要小心。
func lexSpace(l *lexer) stateFn {
	var r rune
	var numSpaces int
	for {
		r = l.peek()
		if !isSpace(r) {
			break
		}
		l.next()
		numSpaces++
	}
	// 小心带修剪标记的闭合分隔符，它在空格后面有减号。
	// 我们知道有一个空格，所以检查可能跟随的 '-'。
	if hasRightTrimMarker(l.input[l.pos-1:]) && strings.HasPrefix(l.input[l.pos-1+trimMarkerLen:], l.rightDelim) {
		l.backup() // 在空格之前。
		if numSpaces == 1 {
			return lexRightDelim // 在分隔符上，直接进入。
		}
	}
	return l.emit(itemSpace)
}

// lexIdentifier 扫描字母数字。
func lexIdentifier(l *lexer) stateFn {
	for {
		switch r := l.next(); {
		case isAlphaNumeric(r):
			// 吸收。
		default:
			l.backup()
			word := l.input[l.start:l.pos]
			if !l.atTerminator() {
				return l.errorf("bad character %#U", r)
			}
			switch {
			case key[word] > itemKeyword:
				item := key[word]
				if item == itemBreak && !l.options.breakOK || item == itemContinue && !l.options.continueOK {
					return l.emit(itemIdentifier)
				}
				return l.emit(item)
			case word[0] == '.':
				return l.emit(itemField)
			case word == "true", word == "false":
				return l.emit(itemBool)
			default:
				return l.emit(itemIdentifier)
			}
		}
	}
}

// lexField 扫描字段：.Alphanumeric。. 已经被扫描。
func lexField(l *lexer) stateFn {
	return lexFieldOrVariable(l, itemField)
}

// lexVariable 扫描变量：$Alphanumeric。$ 已经被扫描。
func lexVariable(l *lexer) stateFn {
	if l.atTerminator() { // 没有有趣的跟随 -> "$"。
		return l.emit(itemVariable)
	}
	return lexFieldOrVariable(l, itemVariable)
}

// lexFieldOrVariable 扫描字段或变量：[.$]Alphanumeric。
// . 或 $ 已经被扫描。
func lexFieldOrVariable(l *lexer, typ itemType) stateFn {
	if l.atTerminator() { // 没有有趣的跟随 -> "." 或 "$"。
		if typ == itemVariable {
			return l.emit(itemVariable)
		}
		return l.emit(itemDot)
	}
	var r rune
	for {
		r = l.next()
		if !isAlphaNumeric(r) {
			l.backup()
			break
		}
	}
	if !l.atTerminator() {
		return l.errorf("bad character %#U", r)
	}
	return l.emit(typ)
}

// atTerminator 报告输入是否在标识符后有效的终止字符处。
// 将 .X.Y 分成两块。也捕获像 "$x+2" 这样没有空格不可接受的情况，
// 以防我们某天决定实现算术。
func (l *lexer) atTerminator() bool {
	r := l.peek()
	if isSpace(r) {
		return true
	}
	switch r {
	case eof, '.', ',', '|', ':', ')', '(':
		return true
	}
	return strings.HasPrefix(l.input[l.pos:], l.rightDelim)
}

// lexChar 扫描字符常量。初始引号已经被扫描。
// 语法检查由解析器完成。
func lexChar(l *lexer) stateFn {
Loop:
	for {
		switch l.next() {
		case '\\':
			if r := l.next(); r != eof && r != '\n' {
				break
			}
			fallthrough
		case eof, '\n':
			return l.errorf("unterminated character constant")
		case '\'':
			break Loop
		}
	}
	return l.emit(itemCharConstant)
}

// lexNumber 扫描数字：十进制、八进制、十六进制、浮点数或虚数。
// 这不是一个完美的数字扫描器——例如它接受 "." 和 "0x0.2" 和 "089"——
// 但当它出错时输入无效，解析器（通过 strconv）会注意到。
func lexNumber(l *lexer) stateFn {
	if !l.scanNumber() {
		return l.errorf("bad number syntax: %q", l.input[l.start:l.pos])
	}
	if sign := l.peek(); sign == '+' || sign == '-' {
		// 复数：1+2i。没有空格，必须以 'i' 结尾。
		if !l.scanNumber() || l.input[l.pos-1] != 'i' {
			return l.errorf("bad number syntax: %q", l.input[l.start:l.pos])
		}
		return l.emit(itemComplex)
	}
	return l.emit(itemNumber)
}

func (l *lexer) scanNumber() bool {
	// 可选的前导符号。
	l.accept("+-")
	// 是十六进制吗？
	digits := "0123456789_"
	if l.accept("0") {
		// 注意：在浮点数中前导 0 不表示八进制。
		if l.accept("xX") {
			digits = "0123456789abcdefABCDEF_"
		} else if l.accept("oO") {
			digits = "01234567_"
		} else if l.accept("bB") {
			digits = "01_"
		}
	}
	l.acceptRun(digits)
	if l.accept(".") {
		l.acceptRun(digits)
	}
	if len(digits) == 10+1 && l.accept("eE") {
		l.accept("+-")
		l.acceptRun("0123456789_")
	}
	if len(digits) == 16+6+1 && l.accept("pP") {
		l.accept("+-")
		l.acceptRun("0123456789_")
	}
	// 是虚数吗？
	l.accept("i")
	// 下一个东西不能是字母数字。
	if isAlphaNumeric(l.peek()) {
		l.next()
		return false
	}
	return true
}

// lexQuote 扫描带引号的字符串。
func lexQuote(l *lexer) stateFn {
Loop:
	for {
		switch l.next() {
		case '\\':
			if r := l.next(); r != eof && r != '\n' {
				break
			}
			fallthrough
		case eof, '\n':
			return l.errorf("unterminated quoted string")
		case '"':
			break Loop
		}
	}
	return l.emit(itemString)
}

// lexRawQuote 扫描原始带引号字符串。
func lexRawQuote(l *lexer) stateFn {
Loop:
	for {
		switch l.next() {
		case eof:
			return l.errorf("unterminated raw quoted string")
		case '`':
			break Loop
		}
	}
	return l.emit(itemRawString)
}

// isSpace 报告 r 是否为空格字符。
func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

// isAlphaNumeric 报告 r 是否为字母、数字或下划线。
func isAlphaNumeric(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func hasLeftTrimMarker(s string) bool {
	return len(s) >= 2 && s[0] == trimMarker && isSpace(rune(s[1]))
}

func hasRightTrimMarker(s string) bool {
	return len(s) >= 2 && isSpace(rune(s[0])) && s[1] == trimMarker
}
