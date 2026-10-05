// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

// Package jsonwire 实现了处理 JSON 文本的无状态功能。
package jsonwire

import (
	"cmp"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// TrimSuffixWhitespace 从 b 的末尾修剪 JSON。
func TrimSuffixWhitespace(b []byte) []byte {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	n := len(b) - 1
	for n >= 0 && (b[n] == ' ' || b[n] == '\t' || b[n] == '\r' || b[n] == '\n') {
		n--
	}
	return b[:n+1]
}

// TrimSuffixString 修剪 b 末尾的一个有效 JSON 字符串。
// 如果末尾不存在有效的 JSON 字符串，则行为未定义。
func TrimSuffixString(b []byte) []byte {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	if len(b) > 0 && b[len(b)-1] == '"' {
		b = b[:len(b)-1]
	}
	for len(b) >= 2 && !(b[len(b)-1] == '"' && b[len(b)-2] != '\\') {
		b = b[:len(b)-1] // 修剪除未转义引号之外的所有字符
	}
	if len(b) > 0 && b[len(b)-1] == '"' {
		b = b[:len(b)-1]
	}
	return b
}

// HasSuffixByte 报告 b 是否以 c 结尾。
func HasSuffixByte(b []byte, c byte) bool {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	return len(b) > 0 && b[len(b)-1] == c
}

// TrimSuffixByte 如果 b 末尾存在 c，则将其移除。
func TrimSuffixByte(b []byte, c byte) []byte {
	// NOTE: 参数和逻辑保持简单以使其可内联。
	if len(b) > 0 && b[len(b)-1] == c {
		return b[:len(b)-1]
	}
	return b
}

// QuoteRune 为输入中的第一个 rune 添加引号。
func QuoteRune[Bytes ~[]byte | ~string](b Bytes) string {
	r, n := utf8.DecodeRuneInString(string(truncateMaxUTF8(b)))
	if r == utf8.RuneError && n == 1 {
		return `'\x` + strconv.FormatUint(uint64(b[0]), 16) + `'`
	}
	return strconv.QuoteRune(r)
}

// CompareUTF16 按照 UTF-8 编码输入字符串的 UTF-16 码点，
// 按字典序将 x 与 y 进行比较。
// 这实现了 RFC 8785 第 3.2.3 节所指定的排序。
func CompareUTF16[Bytes ~[]byte | ~string](x, y Bytes) int {
	// NOTE: 这是 wire_test.go 中 CompareUTF16Simple 的一个经过优化的、
	// 几乎没有内存分配的实现。FuzzCompareUTF16 验证了两种实现在
	// 比较任意两个字符串时结果一致。
	isUTF16Self := func(r rune) bool {
		return ('\u0000' <= r && r <= '\uD7FF') || ('\uE000' <= r && r <= '\uFFFF')
	}

	for {
		if len(x) == 0 || len(y) == 0 {
			return cmp.Compare(len(x), len(y))
		}

		// ASCII 快速路径。
		if x[0] < utf8.RuneSelf || y[0] < utf8.RuneSelf {
			if x[0] != y[0] {
				return cmp.Compare(x[0], y[0])
			}
			x, y = x[1:], y[1:]
			continue
		}

		// 将下一对 rune 按 UTF-8 解码。
		rx, nx := utf8.DecodeRuneInString(string(truncateMaxUTF8(x)))
		ry, ny := utf8.DecodeRuneInString(string(truncateMaxUTF8(y)))

		selfx := isUTF16Self(rx)
		selfy := isUTF16Self(ry)
		switch {
		// x rune 是单个 UTF-16 码点，
		// 而 y rune 是 UTF-16 码点构成的代理对。
		case selfx && !selfy:
			ry, _ = utf16.EncodeRune(ry)
		// y rune 是单个 UTF-16 码点，
		// 而 x rune 是 UTF-16 码点构成的代理对。
		case selfy && !selfx:
			rx, _ = utf16.EncodeRune(rx)
		}
		if rx != ry {
			return cmp.Compare(rx, ry)
		}

		// 检查是否有无效的 UTF-8，如果有，
		// 我们就只进行逐字节比较。
		if isInvalidUTF8(rx, nx) || isInvalidUTF8(ry, ny) {
			if x[0] != y[0] {
				return cmp.Compare(x[0], y[0])
			}
		}
		x, y = x[nx:], y[ny:]
	}
}

// truncateMaxUTF8 对 b 进行截断，使其至少包含一个 rune。
//
// utf8 包目前缺少泛型变体，这使得作用于 []byte 或 string 的
// 泛型函数变得复杂。
// 作为一种变通方法，我们总是调用作用于字符串的 utf8 函数，
// 但总是对输入进行截断，使得结果保持一致。
//
// 用法示例：
//
//	utf8.DecodeRuneInString(string(truncateMaxUTF8(b)))
//
// 将 []byte 转换为 string 是栈分配的，因为
// truncateMaxUTF8 保证该 []byte 很短。
func truncateMaxUTF8[Bytes ~[]byte | ~string](b Bytes) Bytes {
	// TODO(https://go.dev/issue/56948): 移除这个函数，
	// 改为在使用处直接调用泛型 utf8 函数。
	if len(b) > utf8.UTFMax {
		return b[:utf8.UTFMax]
	}
	return b
}

// TODO(https://go.dev/issue/70547): 改用 utf8.ErrInvalid。
var ErrInvalidUTF8 = errors.New("invalid UTF-8")

func NewInvalidCharacterError[Bytes ~[]byte | ~string](prefix Bytes, where string) error {
	what := QuoteRune(prefix)
	return errors.New("invalid character " + what + " " + where)
}

func NewInvalidEscapeSequenceError[Bytes ~[]byte | ~string](what Bytes) error {
	label := "escape sequence"
	if len(what) > 6 {
		label = "surrogate pair"
	}
	needEscape := strings.IndexFunc(string(what), func(r rune) bool {
		return r == '`' || r == utf8.RuneError || unicode.IsSpace(r) || !unicode.IsPrint(r)
	}) >= 0
	if needEscape {
		return errors.New("invalid " + label + " " + strconv.Quote(string(what)) + " in string")
	} else {
		return errors.New("invalid " + label + " `" + string(what) + "` in string")
	}
}

// TruncatePointer 可选地对 JSON 指针进行截断，
// 确保其长度大致不超过 n。
func TruncatePointer(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := n / 2
	j := len(s) - n/2

	// 如果存在多个名称，则避免截断某个名称。
	if k := strings.LastIndexByte(s[:i], '/'); k > 0 {
		i = k
	}
	if k := strings.IndexByte(s[j:], '/'); k >= 0 {
		j += k + len("/")
	}

	// 避免在 UTF-8 rune 的中间进行截断。
	for i > 0 && isInvalidUTF8(utf8.DecodeLastRuneInString(s[:i])) {
		i--
	}
	for j < len(s) && isInvalidUTF8(utf8.DecodeRuneInString(s[j:])) {
		j++
	}

	// 确定要使用的正确的中间片段。
	var middle string
	switch strings.Count(s[i:j], "/") {
	case 0:
		middle = "…"
	case 1:
		middle = "…/…"
	default:
		middle = "…/…/…"
	}
	if strings.HasPrefix(s[i:j], "/") && middle != "…" {
		middle = strings.TrimPrefix(middle, "…")
	}
	if strings.HasSuffix(s[i:j], "/") && middle != "…" {
		middle = strings.TrimSuffix(middle, "…")
	}
	return s[:i] + middle + s[j:]
}

func isInvalidUTF8(r rune, rn int) bool {
	return r == utf8.RuneError && rn == 1
}
