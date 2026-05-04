// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mime

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

// FormatMediaType 将媒体类型 t 及参数 param 序列化为符合 RFC 2045 和 RFC 2616 的媒体类型字符串。
// 类型和参数名以小写形式写出。
// 当任一参数导致违反标准时，FormatMediaType 返回空字符串。
func FormatMediaType(t string, param map[string]string) string {
	var b strings.Builder
	if major, sub, ok := strings.Cut(t, "/"); !ok {
		if !isToken(t) {
			return ""
		}
		b.WriteString(strings.ToLower(t))
	} else {
		if !isToken(major) || !isToken(sub) {
			return ""
		}
		b.WriteString(strings.ToLower(major))
		b.WriteByte('/')
		b.WriteString(strings.ToLower(sub))
	}

	for _, attribute := range slices.Sorted(maps.Keys(param)) {
		value := param[attribute]
		b.WriteByte(';')
		b.WriteByte(' ')
		if !isToken(attribute) {
			return ""
		}
		b.WriteString(strings.ToLower(attribute))

		needEnc := needsEncoding(value)
		if needEnc {
			// RFC 2231 section 4
			b.WriteByte('*')
		}
		b.WriteByte('=')

		if needEnc {
			b.WriteString("utf-8''")

			offset := 0
			for index := 0; index < len(value); index++ {
				ch := value[index]
				// {RFC 2231 section 7}
				// attribute-char := <any (US-ASCII) CHAR except SPACE, CTLs, "*", "'", "%", or tspecials>
				if ch <= ' ' || ch >= 0x7F ||
					ch == '*' || ch == '\'' || ch == '%' ||
					isTSpecial(ch) {

					b.WriteString(value[offset:index])
					offset = index + 1

					b.WriteByte('%')
					b.WriteByte(upperhex[ch>>4])
					b.WriteByte(upperhex[ch&0x0F])
				}
			}
			b.WriteString(value[offset:])
			continue
		}

		if isToken(value) {
			b.WriteString(value)
			continue
		}

		b.WriteByte('"')
		offset := 0
		for index := 0; index < len(value); index++ {
			character := value[index]
			if character == '"' || character == '\\' {
				b.WriteString(value[offset:index])
				offset = index
				b.WriteByte('\\')
			}
		}
		b.WriteString(value[offset:])
		b.WriteByte('"')
	}
	return b.String()
}

func checkMediaTypeDisposition(s string) error {
	typ, rest := consumeToken(s)
	if typ == "" {
		return errNoMediaType
	}
	if rest == "" {
		return nil
	}
	var ok bool
	if rest, ok = strings.CutPrefix(rest, "/"); !ok {
		return errNoSlashAfterFirstToken
	}
	subtype, rest := consumeToken(rest)
	if subtype == "" {
		return errNoTokenAfterSlash
	}
	if rest != "" {
		return errUnexpectedContentAfterMediaSubtype
	}
	return nil
}

var (
	errNoMediaType                        = errors.New("mime: no media type")
	errNoSlashAfterFirstToken             = errors.New("mime: expected slash after first token")
	errNoTokenAfterSlash                  = errors.New("mime: expected token after slash")
	errUnexpectedContentAfterMediaSubtype = errors.New("mime: unexpected content after media subtype")
)

// ErrInvalidMediaParameter 在找到媒体类型值但解析可选参数出错时由 [ParseMediaType] 返回。
var ErrInvalidMediaParameter = errors.New("mime: invalid media parameter")

// ParseMediaType 解析媒体类型值及其可选参数，依据 RFC 1521。
// 媒体类型为 Content-Type 和 Content-Disposition 头部中的值（RFC 2183）。
// 成功时，ParseMediaType 返回转换为小写且去除空白后的媒体类型，以及非空的 params 映射。
// 解析可选参数出错时，将返回媒体类型及错误 [ErrInvalidMediaParameter]。
// 返回的映射 params 将小写属性名映射到属性值，属性值的大小写保持不变。
func ParseMediaType(v string) (mediatype string, params map[string]string, err error) {
	base, _, _ := strings.Cut(v, ";")
	mediatype = strings.TrimSpace(strings.ToLower(base))

	err = checkMediaTypeDisposition(mediatype)
	if err != nil {
		return "", nil, err
	}

	params = make(map[string]string)

	// 参数名到参数值映射的基础参数名 -> 参数名 -> 值。
	// 用于包含 '*' 字符的参数。
	// 延迟初始化。
	var continuation map[string]map[string]string

	v = v[len(base):]
	for len(v) > 0 {
		v = strings.TrimLeftFunc(v, unicode.IsSpace)
		if len(v) == 0 {
			break
		}
		key, value, rest := consumeMediaParam(v)
		if key == "" {
			if strings.TrimSpace(rest) == ";" {
				// 忽略尾部冒号。
				// 不是错误。
				break
			}
			// 解析错误。
			return mediatype, nil, ErrInvalidMediaParameter
		}

		pmap := params
		if baseName, _, ok := strings.Cut(key, "*"); ok {
			if continuation == nil {
				continuation = make(map[string]map[string]string)
			}
			if pmap, ok = continuation[baseName]; !ok {
				continuation[baseName] = make(map[string]string)
				pmap = continuation[baseName]
			}
		}
		if v, exists := pmap[key]; exists && v != value {
			// 参数名重复是错误的，但若值相同则允许。
			return "", nil, errDuplicateParamName
		}
		pmap[key] = value
		v = rest
	}

	// 将任何延续或带星号的部分拼接在一起
	//（即 RFC 2231 中带星号的 "foo*0" 或 "foo*"）。
	var buf strings.Builder
	for key, pieceMap := range continuation {
		singlePartKey := key + "*"
		if v, ok := pieceMap[singlePartKey]; ok {
			if decv, ok := decode2231Enc(v); ok {
				params[key] = decv
			}
			continue
		}

		buf.Reset()
		valid := false
		for n := 0; ; n++ {
			simplePart := fmt.Sprintf("%s*%d", key, n)
			if v, ok := pieceMap[simplePart]; ok {
				valid = true
				buf.WriteString(v)
				continue
			}
			encodedPart := simplePart + "*"
			v, ok := pieceMap[encodedPart]
			if !ok {
				break
			}
			valid = true
			if n == 0 {
				if decv, ok := decode2231Enc(v); ok {
					buf.WriteString(decv)
				}
			} else {
				decv, _ := percentHexUnescape(v)
				buf.WriteString(decv)
			}
		}
		if valid {
			params[key] = buf.String()
		}
	}

	return
}

var errDuplicateParamName = errors.New("mime: duplicate parameter name")

func decode2231Enc(v string) (string, bool) {
	charset, v, ok := strings.Cut(v, "'")
	if !ok {
		return "", false
	}
	// TODO: 目前忽略语言部分。如果有人需要，我们会
	// 决定如何在 API 中暴露它。但我不确定
	// 实际上有人在使用它。
	_, extOtherVals, ok := strings.Cut(v, "'")
	if !ok {
		return "", false
	}
	charset = strings.ToLower(charset)
	switch charset {
	case "us-ascii", "utf-8":
	default:
		// 空或不支持的编码。
		return "", false
	}
	return percentHexUnescape(extOtherVals)
}

// consumeToken 从给定字符串开头消耗一个 token，依据 RFC 2045 第 5.1 节（RFC 2183 引用），
// 并返回消耗的 token 及字符串剩余部分。
// 若至少未消耗一个字符，则返回 ("", v)。
func consumeToken(v string) (token, rest string) {
	for i := range len(v) {
		if !isTokenChar(v[i]) {
			return v[:i], v[i:]
		}
	}
	return v, ""
}

// consumeValue 依据 RFC 2045 消耗一个"值"，其中值可以是 'token' 或 'quoted-string'。
// 成功时，consumeValue 返回消耗的值（若是 quoted-string 则进行去引号/转义处理）
// 及字符串剩余部分。失败时返回 ("", v)。
func consumeValue(v string) (value, rest string) {
	if v == "" {
		return
	}
	if v[0] != '"' {
		return consumeToken(v)
	}

	// parse a quoted-string
	buffer := new(strings.Builder)
	for i := 1; i < len(v); i++ {
		r := v[i]
		if r == '"' {
			return buffer.String(), v[i+1:]
		}
		// 当 MSIE 以"内网模式"发送完整文件路径时，不会对反斜杠进行转义：
		// "C:\dev\go\foo.txt"，而非 "C:\\dev\\go\\foo.txt"。
		//
		// 据我们所知，没有已知的 MIME 生成器会为简单 token 字符（如数字和字母）发出不必要的反斜杠转义。
		//
		// 若我们看到不必要的反斜杠转义，假定它来自 MSIE，意图作为字面反斜杠。
		// 这使得 Go 服务器能更好地处理 MSIE，同时不影响其处理符合规范的 MIME 生成器的方式。
		if r == '\\' && i+1 < len(v) && isTSpecial(v[i+1]) {
			buffer.WriteByte(v[i+1])
			i++
			continue
		}
		if r == '\r' || r == '\n' {
			return "", v
		}
		buffer.WriteByte(v[i])
	}
	// 未找到结束引号。
	return "", v
}

func consumeMediaParam(v string) (param, value, rest string) {
	rest = strings.TrimLeftFunc(v, unicode.IsSpace)
	var ok bool
	if rest, ok = strings.CutPrefix(rest, ";"); !ok {
		return "", "", v
	}

	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	param, rest = consumeToken(rest)
	param = strings.ToLower(param)
	if param == "" {
		return "", "", v
	}

	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	if rest, ok = strings.CutPrefix(rest, "="); !ok {
		return "", "", v
	}
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	value, rest2 := consumeValue(rest)
	if value == "" && rest2 == rest {
		return "", "", v
	}
	rest = rest2
	return param, value, rest
}

func percentHexUnescape(s string) (string, bool) {
	// 统计 %，并检查其格式是否正确。
	percents := 0
	for i := 0; i < len(s); {
		if s[i] != '%' {
			i++
			continue
		}
		percents++
		if i+2 >= len(s) || !ishex(s[i+1]) || !ishex(s[i+2]) {
			return "", false
		}
		i += 3
	}
	if percents == 0 {
		return s, true
	}

	t := make([]byte, len(s)-2*percents)
	j := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '%':
			t[j] = unhex(s[i+1])<<4 | unhex(s[i+2])
			j++
			i += 3
		default:
			t[j] = s[i]
			j++
			i++
		}
	}
	return string(t), true
}

func ishex(c byte) bool {
	switch {
	case '0' <= c && c <= '9':
		return true
	case 'a' <= c && c <= 'f':
		return true
	case 'A' <= c && c <= 'F':
		return true
	}
	return false
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}
