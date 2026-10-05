// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"errors"
	"iter"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"encoding/json/internal/jsonwire"
)

// ErrDuplicateName 表示某个 JSON 令牌无法
// 编码或解码，因为它导致重复的 JSON 对象名称。
// 产生时，该错误会直接包装在 [SyntacticError] 中。
//
// 重复的 JSON 对象成员的名称可以这样提取：
//
//	err := ...
//	serr, ok := errors.AsType[*jsontext.SyntacticError](err)
//	if ok && serr.Err == jsontext.ErrDuplicateName {
//		ptr := serr.JSONPointer // 指向重复名称的 JSON 指针
//		name := ptr.LastToken() // 重复名称本身
//		...
//	}
//
// 仅当 [AllowDuplicateNames] 为 false 时才会返回此错误。
var ErrDuplicateName = errors.New("duplicate object member name")

// ErrNonStringName 表示某个 JSON 令牌无法
// 编码或解码，因为它不是字符串，
// 而根据 RFC 8259 第 4 节，JSON 对象名称要求必须是字符串。
// 产生时，该错误会直接包装在 [SyntacticError] 中。
var ErrNonStringName = errors.New("object member name must be a string")

var (
	errMissingValue  = errors.New("missing value after object name")
	errMismatchDelim = errors.New("mismatching structural token for object or array")
	errMaxDepth      = errors.New("exceeded max depth")

	errInvalidNamespace = errors.New("object namespace is in an invalid state")
)

// 根据 RFC 8259 第 9 节，实现可以强制实施最大深度。
// 这样的限制是必要的，以防止栈溢出。
const maxNestingDepth = 10000

type state struct {
	// Tokens 校验下一个令牌种类是否有效。
	Tokens stateMachine

	// Names 是对象名称的栈。
	Names objectNameStack

	// Namespaces 是对象命名空间的栈。
	// 出于性能原因，如果 Marshal 或 Unmarshal 能以更高效的方式
	// 跟踪名称，Encoder 或 Decoder 可能不会更新它。
	// 参见 makeMapArshaler 和 makeStructArshaler。
	// 如果 AllowDuplicateNames 为 true，则不使用。
	Namespaces objectNamespaceStack
}

// needObjectValue 报告下一个令牌是否应为对象值。
// [wrapSyntacticError] 会使用此方法。
func (s *state) needObjectValue() bool {
	return s.Tokens.Last.needObjectValue()
}

func (s *state) reset() {
	s.Tokens.reset()
	s.Names.reset()
	s.Namespaces.reset()
}

// Pointer 是一个 JSON 指针（RFC 6901），它引用
// 相对于顶层 JSON 值根部的某个特定 JSON 值。
//
// Pointer 是由斜杠分隔的令牌列表，其中每个令牌
// 要么是 JSON 对象名称，要么是编码为十进制整数值的
// JSON 数组元素索引。
// 如果不知道指针所引用的顶层 JSON 值的结构，
// 就无法区分数组索引和（恰好是十进制编码整数的）对象名称。
//
// 指向特定值的指针只有唯一的一种表示，
// 因此比较 Pointer 值等价于检查
// 它们是否都指向完全相同的值。
type Pointer string

// IsValid 报告 p 是否为符合 RFC 6901 的有效 JSON 指针。
// 注意，两个有效指针的拼接结果也是有效指针。
func (p Pointer) IsValid() bool {
	for i, r := range p {
		switch {
		case r == '~' && (i+1 == len(p) || (p[i+1] != '0' && p[i+1] != '1')):
			return false // 无效的转义
		case r == '\ufffd' && !strings.HasPrefix(string(p[i:]), "\ufffd"):
			return false // 无效的 UTF-8
		}
	}
	return len(p) == 0 || p[0] == '/'
}

// Contains 报告 p 所指向的 JSON 值
// 是否等于或包含 pc 所指向的 JSON 值。
func (p Pointer) Contains(pc Pointer) bool {
	// 不变式：如果 p.Contains(pc)，则 len(p) <= len(pc)
	suffix, ok := strings.CutPrefix(string(pc), string(p))
	return ok && (suffix == "" || suffix[0] == '/')
}

// Parent 去掉最后一个令牌并返回剩余的指针。
// 空 p 的父级是空字符串。
func (p Pointer) Parent() Pointer {
	return p[:max(strings.LastIndexByte(string(p), '/'), 0)]
}

// LastToken 返回指针中的最后一个令牌。
// 空 p 的最后一个令牌是空字符串。
func (p Pointer) LastToken() string {
	last := p[max(strings.LastIndexByte(string(p), '/'), 0):]
	return unescapePointerToken(strings.TrimPrefix(string(last), "/"))
}

// AppendToken 向 p 的末尾追加一个令牌，并返回完整的指针。
func (p Pointer) AppendToken(tok string) Pointer {
	return Pointer(appendEscapePointerName([]byte(p+"/"), tok))
}

// TODO: 添加 Pointer.AppendTokens，
// 但它应该接受 ...string 还是 iter.Seq[string]？

// Tokens 返回一个迭代器，用于遍历 JSON 指针中的引用令牌，
// 从第一个令牌开始直到最后一个令牌（除非提前停止）。
func (p Pointer) Tokens() iter.Seq[string] {
	return func(yield func(string) bool) {
		for len(p) > 0 {
			p = Pointer(strings.TrimPrefix(string(p), "/"))
			i := min(uint(strings.IndexByte(string(p), '/')), uint(len(p)))
			if !yield(unescapePointerToken(string(p)[:i])) {
				return
			}
			p = p[i:]
		}
	}
}

func unescapePointerToken(token string) string {
	if strings.Contains(token, "~") {
		// 根据 RFC 6901 第 3 节，对 '~' 和 '/' 字符进行反转义。
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
	}
	return token
}

// appendStackPointer 向当前值追加一个 JSON 指针（RFC 6901）。
//
//   - 如果 where 为 -1，则它指向先前处理过的令牌。
//
//   - 如果 where 为 0，则它指向父 JSON 对象或数组，
//     或者如果处于对象成员键和值之间，则指向对象成员。
//     当位置有歧义、无法确定我们关注的是前一个还是后一个令牌时，
//     或者当无法确定下一个令牌是继续还是终止当前对象或数组时，
//     这很有用。
//
//   - 如果 where 为 +1，则它指向下一个预期值，
//     假定下一个值延续当前 JSON 对象或数组。
//     作为一种特殊情况，如果下一个令牌是 JSON 对象名称，
//     则它指向父 JSON 对象。
//
// 不变式：必须事先调用 s.names.copyQuotedBuffer。
func (s state) appendStackPointer(b []byte, where int) []byte {
	var objectDepth int
	for i := 1; i < s.Tokens.Depth(); i++ {
		e := s.Tokens.index(i)
		arrayDelta := -1 // 默认指向前一个数组元素
		if isLast := i == s.Tokens.Depth()-1; isLast {
			switch {
			case where < 0 && e.Length() == 0 || where == 0 && !e.needObjectValue() || where > 0 && e.NeedObjectName():
				return b
			case where > 0 && e.isArray():
				arrayDelta = 0 // 指向下一个数组元素
			}
		}
		switch {
		case e.isObject():
			b = appendEscapePointerName(append(b, '/'), s.Names.getUnquoted(objectDepth))
			objectDepth++
		case e.isArray():
			b = strconv.AppendUint(append(b, '/'), uint64(e.Length()+int64(arrayDelta)), 10)
		}
	}
	return b
}

func appendEscapePointerName[Bytes ~[]byte | ~string](b []byte, name Bytes) []byte {
	for _, r := range string(name) {
		// 根据 RFC 6901 第 3 节，对 '~' 和 '/' 字符进行转义。
		switch r {
		case '~':
			b = append(b, "~0"...)
		case '/':
			b = append(b, "~1"...)
		default:
			b = utf8.AppendRune(b, r)
		}
	}
	return b
}

// stateMachine 是一个下推自动机，用于根据 JSON 文法校验
// 令牌序列是否有效。
// 它同时适用于编码和解码。
//
// 它是一个栈，其中每个条目表示一个嵌套的 JSON 对象或数组。
// 该栈的最小深度为 1，其中第一层是一个
// 虚拟 JSON 数组，用于处理顶层 JSON 值的流。
// 顶层虚拟 JSON 数组的特殊之处在于，它不要求
// 各个 JSON 值之间有逗号。
//
// 出于性能考虑，大多数方法都被精心编写为可内联。
// 零值是一个可用的有效状态机。
type stateMachine struct {
	Stack []stateEntry
	Last  stateEntry
}

// reset 重置状态机。
// 状态机总是以最小深度 1 开始。
func (m *stateMachine) reset() {
	m.Stack = m.Stack[:0]
	if cap(m.Stack) > 1<<10 {
		m.Stack = nil
	}
	m.Last = stateTypeArray
}

// Depth 是 JSON 对象和数组当前的嵌套深度。
// 它从 1 开始计数（即顶层值的深度为 1）。
func (m stateMachine) Depth() int {
	return len(m.Stack) + 1
}

// index 返回对第 i 个条目的引用。
// 它仅在下次调用 push 方法之前有效。
func (m *stateMachine) index(i int) *stateEntry {
	if i == len(m.Stack) {
		return &m.Last
	}
	return &m.Stack[i]
}

// DepthLength 报告当前的嵌套深度
// 以及最后一个 JSON 对象或数组的长度。
func (m stateMachine) DepthLength() (int, int64) {
	return m.Depth(), m.Last.Length()
}

// appendLiteral 追加一个 JSON 字面量作为序列中的下一个令牌。
// 如果返回错误，则状态不会被修改。
func (m *stateMachine) appendLiteral() error {
	switch {
	case m.Last.NeedObjectName():
		return ErrNonStringName
	case !m.Last.isValidNamespace():
		return errInvalidNamespace
	default:
		m.Last.Increment()
		return nil
	}
}

// appendString 追加一个 JSON 字符串作为序列中的下一个令牌。
// 如果返回错误，则状态不会被修改。
func (m *stateMachine) appendString() error {
	switch {
	case !m.Last.isValidNamespace():
		return errInvalidNamespace
	default:
		m.Last.Increment()
		return nil
	}
}

// appendNumber 追加一个 JSON 数字作为序列中的下一个令牌。
// 如果返回错误，则状态不会被修改。
func (m *stateMachine) appendNumber() error {
	return m.appendLiteral()
}

// pushObject 追加一个 JSON 起始对象令牌作为序列中的下一个令牌。
// 如果返回错误，则状态不会被修改。
func (m *stateMachine) pushObject() error {
	switch {
	case m.Last.NeedObjectName():
		return ErrNonStringName
	case !m.Last.isValidNamespace():
		return errInvalidNamespace
	case len(m.Stack) == maxNestingDepth:
		return errMaxDepth
	default:
		m.Last.Increment()
		m.Stack = append(m.Stack, m.Last)
		m.Last = stateTypeObject
		return nil
	}
}

// popObject 追加一个 JSON 结束对象令牌作为序列中的下一个令牌。
// 如果返回错误，则状态不会被修改。
func (m *stateMachine) popObject() error {
	switch {
	case !m.Last.isObject():
		return errMismatchDelim
	case m.Last.needObjectValue():
		return errMissingValue
	case !m.Last.isValidNamespace():
		return errInvalidNamespace
	default:
		m.Last = m.Stack[len(m.Stack)-1]
		m.Stack = m.Stack[:len(m.Stack)-1]
		return nil
	}
}

// pushArray 追加一个 JSON 起始数组令牌作为序列中的下一个令牌。
// 如果返回错误，则状态不会被修改。
func (m *stateMachine) pushArray() error {
	switch {
	case m.Last.NeedObjectName():
		return ErrNonStringName
	case !m.Last.isValidNamespace():
		return errInvalidNamespace
	case len(m.Stack) == maxNestingDepth:
		return errMaxDepth
	default:
		m.Last.Increment()
		m.Stack = append(m.Stack, m.Last)
		m.Last = stateTypeArray
		return nil
	}
}

// popArray 追加一个 JSON 结束数组令牌作为序列中的下一个令牌。
// 如果返回错误，则状态不会被修改。
func (m *stateMachine) popArray() error {
	switch {
	case !m.Last.isArray() || len(m.Stack) == 0: // 禁止弹出顶层虚拟 JSON 数组
		return errMismatchDelim
	case !m.Last.isValidNamespace():
		return errInvalidNamespace
	default:
		m.Last = m.Stack[len(m.Stack)-1]
		m.Stack = m.Stack[:len(m.Stack)-1]
		return nil
	}
}

// NeedIndent 报告是否应注入缩进空白。
// 零值表示不应注入任何空白。
// 正值表示应紧接在下一个令牌之前向输出追加
// '\n'、indentPrefix 以及 (n-1) 份 indentBody。
func (m stateMachine) NeedIndent(next Kind) (n int) {
	willEnd := next == '}' || next == ']'
	switch {
	case m.Depth() == 1:
		return 0 // 顶层值从不缩进
	case m.Last.Length() == 0 && willEnd:
		return 0 // 空对象或数组从不缩进
	case m.Last.Length() == 0 || m.Last.needImplicitComma(next):
		return m.Depth()
	case willEnd:
		return m.Depth() - 1
	default:
		return 0
	}
}

// MayAppendDelim 追加可能位于下一个令牌之前的冒号或逗号。
func (m stateMachine) MayAppendDelim(b []byte, next Kind) []byte {
	switch {
	case m.Last.needImplicitColon():
		return append(b, ':')
	case m.Last.needImplicitComma(next) && len(m.Stack) != 0: // 顶层值不需要逗号
		return append(b, ',')
	default:
		return b
	}
}

// needDelim 报告是否应在指定种类的下一个令牌之前
// 隐式输出冒号或逗号令牌。
// 零值表示不应输出定界符。
func (m stateMachine) needDelim(next Kind) (delim byte) {
	switch {
	case m.Last.needImplicitColon():
		return ':'
	case m.Last.needImplicitComma(next) && len(m.Stack) != 0: // 顶层值不需要逗号
		return ','
	default:
		return 0
	}
}

// InvalidateDisabledNamespaces 将所有已禁用的命名空间标记为无效。
//
// 为提高效率，Marshal 和 Unmarshal 可能会禁用命名空间，因为有
// 更高效的方式来跟踪重复名称。然而，如果发生错误，
// Encoder 或 Decoder 中的命名空间将处于不一致状态。
// 将这些命名空间标记为无效，以便将来对
// Encoder 或 Decoder 的方法调用会返回错误。
func (m *stateMachine) InvalidateDisabledNamespaces() {
	for i := range m.Depth() {
		e := m.index(i)
		if !e.isActiveNamespace() {
			e.invalidateNamespace()
		}
	}
}

// stateEntry 将若干信息编码进一个无符号整数中：
//   - 它表示的是 JSON 对象还是数组，
//   - 该对象是否应检查重复名称，以及
//   - 该 JSON 对象或数组中有多少个元素。
type stateEntry uint64

const (
	// 类型掩码（1 位）记录这是一个 JSON 对象还是数组。
	stateTypeMask   stateEntry = 0x8000_0000_0000_0000
	stateTypeObject stateEntry = 0x8000_0000_0000_0000
	stateTypeArray  stateEntry = 0x0000_0000_0000_0000

	// 名称检查掩码（2 位）记录是否更新
	// 当前 JSON 对象的命名空间，以及
	// 该命名空间是否有效。
	stateNamespaceMask    stateEntry = 0x6000_0000_0000_0000
	stateDisableNamespace stateEntry = 0x4000_0000_0000_0000
	stateInvalidNamespace stateEntry = 0x2000_0000_0000_0000

	// 计数掩码（61 位）记录元素的数量。
	stateCountMask    stateEntry = 0x1fff_ffff_ffff_ffff
	stateCountLSBMask stateEntry = 0x0000_0000_0000_0001
	stateCountOdd     stateEntry = 0x0000_0000_0000_0001
	stateCountEven    stateEntry = 0x0000_0000_0000_0000
)

// Length 报告 JSON 对象或数组中的元素数量。
// 对象条目中的每个名称和值都被视为一个单独的元素。
func (e stateEntry) Length() int64 {
	return int64(e & stateCountMask)
}

// isObject 报告这是否为 JSON 对象。
func (e stateEntry) isObject() bool {
	return e&stateTypeMask == stateTypeObject
}

// isArray 报告这是否为 JSON 数组。
func (e stateEntry) isArray() bool {
	return e&stateTypeMask == stateTypeArray
}

// NeedObjectName 报告下一个令牌是否必须是 JSON 字符串，
// 这是 JSON 对象名称所必需的。
func (e stateEntry) NeedObjectName() bool {
	return e&(stateTypeMask|stateCountLSBMask) == stateTypeObject|stateCountEven
}

// needImplicitColon 报告下一个是否应为冒号，
// 冒号总是出现在 JSON 对象名称之后。
func (e stateEntry) needImplicitColon() bool {
	return e.needObjectValue()
}

// needObjectValue 报告下一个令牌是否必须是 JSON 值，
// 这是每个 JSON 对象名称之后所必需的。
func (e stateEntry) needObjectValue() bool {
	return e&(stateTypeMask|stateCountLSBMask) == stateTypeObject|stateCountOdd
}

// needImplicitComma 报告下一个是否应为逗号，
// 逗号总是出现在 JSON 对象或数组中的值之后、
// 下一个值（或名称）之前。
func (e stateEntry) needImplicitComma(next Kind) bool {
	return !e.needObjectValue() && e.Length() > 0 && next != '}' && next != ']'
}

// Increment 增加当前对象或数组的元素数量。
// 这里假定溢出在实践中不会是问题，因为
// 1<<bits.OnesCount(stateCountMask) 足够大。
func (e *stateEntry) Increment() {
	(*e)++
}

// decrement 减少当前对象或数组的元素数量。
// 调用方负责确保 e.length > 0。
func (e *stateEntry) decrement() {
	(*e)--
}

// DisableNamespace 禁用 JSON 对象命名空间，使得
// Encoder 或 Decoder 不再更新该命名空间。
func (e *stateEntry) DisableNamespace() {
	*e |= stateDisableNamespace
}

// isActiveNamespace 报告 JSON 对象命名空间是否正在
// 被主动更新并用于重复名称检查。
func (e stateEntry) isActiveNamespace() bool {
	return e&(stateDisableNamespace) == 0
}

// invalidateNamespace 将 JSON 对象命名空间标记为无效。
func (e *stateEntry) invalidateNamespace() {
	*e |= stateInvalidNamespace
}

// isValidNamespace 报告 JSON 对象命名空间是否有效。
func (e stateEntry) isValidNamespace() bool {
	return e&(stateInvalidNamespace) == 0
}

// objectNameStack 是进入 JSON 对象时使用的名称栈。
// 与 objectNamespaceStack 不同，它每个 JSON 对象
// 只需记住一个名称。
//
// 此数据结构可能包含指向 encodeBuffer 或 decodeBuffer 的偏移量。
// 这违反了清晰的分层抽象，但效率显著更高。
// 这确保了在常见情况下，出栈和入栈只是
// 对一个偏移量整数进行简单的 push/pop。
//
// 零值是一个可用的空名称栈。
type objectNameStack struct {
	// offsets 是每个名称的偏移量栈。
	// 非负偏移量是本地名称缓冲区中的结束偏移量。
	// 负偏移量是远程缓冲区（例如 encodeBuffer 或 decodeBuffer）中
	// 起始偏移量的按位取反。
	// 末尾为 math.MinInt 偏移量意味着最后一个对象为空。
	// 不变式：正偏移量总是出现在负偏移量之前。
	offsets []int
	// unquotedNames 是名称的首尾相接的拼接。
	unquotedNames []byte
}

func (ns *objectNameStack) reset() {
	ns.offsets = ns.offsets[:0]
	ns.unquotedNames = ns.unquotedNames[:0]
	if cap(ns.offsets) > 1<<6 {
		ns.offsets = nil // 避免固定任意大量的内存
	}
	if cap(ns.unquotedNames) > 1<<10 {
		ns.unquotedNames = nil // 避免固定任意大量的内存
	}
}

func (ns *objectNameStack) length() int {
	return len(ns.offsets)
}

// getUnquoted 获取栈中第 i 个未加引号的名称。
// 如果最后一个对象为空，则返回空字符串。
//
// 不变式：必须事先调用 copyQuotedBuffer。
func (ns *objectNameStack) getUnquoted(i int) []byte {
	ns.ensureCopiedBuffer()
	if i == 0 {
		return ns.unquotedNames[:ns.offsets[0]]
	} else {
		return ns.unquotedNames[ns.offsets[i-1]:ns.offsets[i-0]]
	}
}

// invalidOffset 表示最后一个 JSON 对象当前没有名称。
const invalidOffset = math.MinInt

// push 进入一个嵌套的 JSON 对象。
func (ns *objectNameStack) push() {
	ns.offsets = append(ns.offsets, invalidOffset)
}

// ReplaceLastQuotedOffset 将最后一个名称替换为
// 某个远程缓冲区中带引号名称的起始偏移量。在调用 copyQuotedBuffer 之前，
// 所提供的所有偏移量都必须相对于同一个缓冲区。
func (ns *objectNameStack) ReplaceLastQuotedOffset(i int) {
	// 使用按位取反而非简单地乘以 -1，以避免
	// 关于零的歧义（零是 names 字段的有效偏移量）。
	// 按位取反在数学上等价于 -i-1，
	// 因此 0 变为 -1，1 变为 -2，依此类推。
	// 这确保了远程偏移量始终为负。
	ns.offsets[len(ns.offsets)-1] = ^i
}

// replaceLastUnquotedName 用所提供的名称替换最后一个名称。
//
// 不变式：必须事先调用 copyQuotedBuffer。
func (ns *objectNameStack) replaceLastUnquotedName(s string) {
	ns.ensureCopiedBuffer()
	var startOffset int
	if len(ns.offsets) > 1 {
		startOffset = ns.offsets[len(ns.offsets)-2]
	}
	ns.unquotedNames = append(ns.unquotedNames[:startOffset], s...)
	ns.offsets[len(ns.offsets)-1] = len(ns.unquotedNames)
}

// clearLast 移除最后一个 JSON 对象中的任何名称。
// 它在语义上等价于先 ns.push 再 ns.pop。
func (ns *objectNameStack) clearLast() {
	ns.offsets[len(ns.offsets)-1] = invalidOffset
}

// pop 从嵌套的 JSON 对象中退出。
func (ns *objectNameStack) pop() {
	ns.offsets = ns.offsets[:len(ns.offsets)-1]
}

// copyQuotedBuffer 将名称从远程缓冲区复制到本地名称
// 缓冲区，使得不再有指向远程缓冲区的偏移量引用。
// 这允许远程缓冲区更改内容，而不影响
// 此数据结构试图记住的名称。
func (ns *objectNameStack) copyQuotedBuffer(b []byte) {
	// 查找第一个负偏移量。
	var i int
	for i = len(ns.offsets) - 1; i >= 0 && ns.offsets[i] < 0; i-- {
		continue
	}

	// 将每个名称从远程缓冲区复制到本地缓冲区。
	for i = i + 1; i < len(ns.offsets); i++ {
		if i == len(ns.offsets)-1 && ns.offsets[i] == invalidOffset {
			if i == 0 {
				ns.offsets[i] = 0
			} else {
				ns.offsets[i] = ns.offsets[i-1]
			}
			break // 最后一个 JSON 对象执行了 push 但没有名称
		}

		// 作为一种 Hyrum 防御措施，我们向缓冲区写入一个无效字符，
		// 以使对 Decoder.ReadToken 的误用更加明显。
		// 我们需要在此处撤销该修改。
		quotedName := b[^ns.offsets[i]:]
		if quotedName[0] == invalidateBufferByte {
			quotedName[0] = '"'
		}

		// 将未加引号的名称追加到本地缓冲区。
		var startOffset int
		if i > 0 {
			startOffset = ns.offsets[i-1]
		}
		if n := jsonwire.ConsumeSimpleString(quotedName); n > 0 {
			ns.unquotedNames = append(ns.unquotedNames[:startOffset], quotedName[len(`"`):n-len(`"`)]...)
		} else {
			ns.unquotedNames, _ = jsonwire.AppendUnquote(ns.unquotedNames[:startOffset], quotedName)
		}
		ns.offsets[i] = len(ns.unquotedNames)
	}
}

func (ns *objectNameStack) ensureCopiedBuffer() {
	if len(ns.offsets) > 0 && ns.offsets[len(ns.offsets)-1] < 0 {
		panic("BUG: copyQuotedBuffer not called beforehand")
	}
}

// objectNamespaceStack 是对象命名空间的栈。
// 此数据结构有助于检测重复名称。
type objectNamespaceStack []objectNamespace

// reset 重置对象命名空间栈。
func (nss *objectNamespaceStack) reset() {
	if cap(*nss) > 1<<10 {
		*nss = nil
	}
	*nss = (*nss)[:0]
}

// push 为嵌套的 JSON 对象启动一个新的命名空间。
func (nss *objectNamespaceStack) push() {
	if cap(*nss) > len(*nss) {
		*nss = (*nss)[:len(*nss)+1]
		nss.Last().reset()
	} else {
		*nss = append(*nss, objectNamespace{})
	}
}

// Last 返回指向最后一个 JSON 对象命名空间的指针。
func (nss objectNamespaceStack) Last() *objectNamespace {
	return &nss[len(nss)-1]
}

// pop 终止嵌套 JSON 对象的命名空间。
func (nss *objectNamespaceStack) pop() {
	*nss = (*nss)[:len(*nss)-1]
}

// objectNamespace 是 JSON 对象的命名空间。
// 与 objectNameStack 不同，它需要记住
// 每个 JSON 对象的所有名称。
//
// 零值是一个可用的空命名空间。
type objectNamespace struct {
	// 它先对所有名称进行线性搜索，然后才切换为
	// 使用 Go map 进行直接查找。

	// endOffsets 是缓冲区中每个名称末尾的偏移量列表。
	// offsets 的长度是命名空间中名称的数量。
	endOffsets []uint
	// allUnquotedNames 是命名空间中每个名称的首尾相接的拼接。
	allUnquotedNames []byte
	// mapNames 是包含命名空间中每个名称的 Go map。
	// 仅当非 nil 时有效。
	mapNames map[string]struct{}
}

// reset 将命名空间重置为空。
func (ns *objectNamespace) reset() {
	ns.endOffsets = ns.endOffsets[:0]
	ns.allUnquotedNames = ns.allUnquotedNames[:0]
	ns.mapNames = nil
	if cap(ns.endOffsets) > 1<<6 {
		ns.endOffsets = nil // 避免固定任意大量的内存
	}
	if cap(ns.allUnquotedNames) > 1<<10 {
		ns.allUnquotedNames = nil // 避免固定任意大量的内存
	}
}

// length 报告命名空间中名称的数量。
func (ns *objectNamespace) length() int {
	return len(ns.endOffsets)
}

// getUnquoted 获取命名空间中第 i 个未加引号的名称。
func (ns *objectNamespace) getUnquoted(i int) []byte {
	if i == 0 {
		return ns.allUnquotedNames[:ns.endOffsets[0]]
	} else {
		return ns.allUnquotedNames[ns.endOffsets[i-1]:ns.endOffsets[i-0]]
	}
}

// lastUnquoted 获取命名空间中的最后一个名称。
func (ns *objectNamespace) lastUnquoted() []byte {
	return ns.getUnquoted(ns.length() - 1)
}

// insertQuoted 插入一个名称并报告是否插入了它，
// 仅当名称尚不在命名空间中时才会插入。
// 所提供的名称必须是有效的 JSON 字符串。
func (ns *objectNamespace) insertQuoted(name []byte, isVerbatim bool) bool {
	if isVerbatim {
		name = name[len(`"`) : len(name)-len(`"`)]
	}
	return ns.insert(name, !isVerbatim)
}
func (ns *objectNamespace) InsertUnquoted(name []byte) bool {
	return ns.insert(name, false)
}
func (ns *objectNamespace) insert(name []byte, quoted bool) bool {
	var allNames []byte
	if quoted {
		allNames, _ = jsonwire.AppendUnquote(ns.allUnquotedNames, name)
	} else {
		allNames = append(ns.allUnquotedNames, name...)
	}
	name = allNames[len(ns.allUnquotedNames):]

	// 如果缓冲区过大，无法进行线性搜索，则切换为使用 map。
	// 这不会将当前名称添加到 map 中。
	if ns.mapNames == nil && (ns.length() > 64 || len(ns.allUnquotedNames) > 1024) {
		ns.mapNames = make(map[string]struct{})
		var startOffset uint
		for _, endOffset := range ns.endOffsets {
			name := ns.allUnquotedNames[startOffset:endOffset]
			ns.mapNames[string(name)] = struct{}{} // 分配一个新的字符串
			startOffset = endOffset
		}
	}

	if ns.mapNames == nil {
		// 对缓冲区执行线性搜索以查找匹配的名称。
		// 它提供 O(n) 查找，但不需要任何分配。
		var startOffset uint
		for _, endOffset := range ns.endOffsets {
			if string(ns.allUnquotedNames[startOffset:endOffset]) == string(name) {
				return false
			}
			startOffset = endOffset
		}
	} else {
		// 如果 map 已填充则使用它。
		// 它提供 O(1) 查找，但每个名称都需要一次字符串分配。
		if _, ok := ns.mapNames[string(name)]; ok {
			return false
		}
		ns.mapNames[string(name)] = struct{}{} // 分配一个新的字符串
	}

	ns.allUnquotedNames = allNames
	ns.endOffsets = append(ns.endOffsets, uint(len(ns.allUnquotedNames)))
	return true
}

// removeLast 移除命名空间中的最后一个名称。
func (ns *objectNamespace) removeLast() {
	if ns.mapNames != nil {
		delete(ns.mapNames, string(ns.lastUnquoted()))
	}
	if ns.length()-1 == 0 {
		ns.endOffsets = ns.endOffsets[:0]
		ns.allUnquotedNames = ns.allUnquotedNames[:0]
	} else {
		ns.endOffsets = ns.endOffsets[:ns.length()-1]
		ns.allUnquotedNames = ns.allUnquotedNames[:ns.endOffsets[ns.length()-1]]
	}
}
