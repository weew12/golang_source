// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mime

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// WordEncoder 是 RFC 2047 编码字词编码器。
type WordEncoder byte

const (
	// BEncoding represents Base64 encoding scheme as defined by RFC 2045.
	BEncoding = WordEncoder('b')
	// QEncoding represents the Q-encoding scheme as defined by RFC 2047.
	QEncoding = WordEncoder('q')
)

var (
	errInvalidWord = errors.New("mime: invalid RFC 2047 encoded-word")
)

// Encode 返回 s 的编码字词形式。若 s 为纯 ASCII 且无特殊字符，则原样返回。
// 所提供的 charset 为 s 的 IANA 字符集名称，不区分大小写。
func (e WordEncoder) Encode(charset, s string) string {
	if !needsEncoding(s) {
		return s
	}
	return e.encodeWord(charset, s)
}

func needsEncoding(s string) bool {
	for _, b := range s {
		if (b < ' ' || b > '~') && b != '\t' {
			return true
		}
	}
	return false
}

// encodeWord 将字符串编码为编码字词。
func (e WordEncoder) encodeWord(charset, s string) string {
	var buf strings.Builder
	// Could use a hint like len(s)*3, but that's not enough for cases
	// with word splits and too much for simpler inputs.
	// 48 is close to maxEncodedWordLen/2, but adjusted to allocator size class.
	buf.Grow(48)

	e.openWord(&buf, charset)
	if e == BEncoding {
		e.bEncode(&buf, charset, s)
	} else {
		e.qEncode(&buf, charset, s)
	}
	closeWord(&buf)

	return buf.String()
}

const (
	// 编码字词的最大长度为 75 个字符。
	// 参见 RFC 2047 第 2 节。
	maxEncodedWordLen = 75
	// maxContentLen 是可编码的内容量，不计头部和 2 字节的尾部。
	maxContentLen = maxEncodedWordLen - len("=?UTF-8?q?") - len("?=")
)

var maxBase64Len = base64.StdEncoding.DecodedLen(maxContentLen)

// bEncode 使用 base64 编码 s 并写入 buf。
func (e WordEncoder) bEncode(buf *strings.Builder, charset, s string) {
	w := base64.NewEncoder(base64.StdEncoding, buf)
	// 若字符集不是 UTF-8 或内容较短，则不拆分编码字词。
	if !isUTF8(charset) || base64.StdEncoding.EncodedLen(len(s)) <= maxContentLen {
		io.WriteString(w, s)
		w.Close()
		return
	}

	var currentLen, last, runeLen int
	for i := 0; i < len(s); i += runeLen {
		// 多字节字符不能被拆分到多个编码字词中。
		// 参见 RFC 2047 第 5.3 节。
		_, runeLen = utf8.DecodeRuneInString(s[i:])

		if currentLen+runeLen <= maxBase64Len {
			currentLen += runeLen
		} else {
			io.WriteString(w, s[last:i])
			w.Close()
			e.splitWord(buf, charset)
			last = i
			currentLen = runeLen
		}
	}
	io.WriteString(w, s[last:])
	w.Close()
}

// qEncode 使用 Q 编码 s 并写入 buf。必要时拆分编码字词。
func (e WordEncoder) qEncode(buf *strings.Builder, charset, s string) {
	// 仅在字符集为 UTF-8 时拆分编码字词。
	if !isUTF8(charset) {
		writeQString(buf, s)
		return
	}

	var currentLen, runeLen int
	for i := 0; i < len(s); i += runeLen {
		b := s[i]
		// 多字节字符不能被拆分到多个编码字词中。
		// 参见 RFC 2047 第 5.3 节。
		var encLen int
		if b >= ' ' && b <= '~' && b != '=' && b != '?' && b != '_' {
			runeLen, encLen = 1, 1
		} else {
			_, runeLen = utf8.DecodeRuneInString(s[i:])
			encLen = 3 * runeLen
		}

		if currentLen+encLen > maxContentLen {
			e.splitWord(buf, charset)
			currentLen = 0
		}
		writeQString(buf, s[i:i+runeLen])
		currentLen += encLen
	}
}

// writeQString 使用 Q 编码 s 并写入 buf。
func writeQString(buf *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		switch b := s[i]; {
		case b == ' ':
			buf.WriteByte('_')
		case b >= '!' && b <= '~' && b != '=' && b != '?' && b != '_':
			buf.WriteByte(b)
		default:
			buf.WriteByte('=')
			buf.WriteByte(upperhex[b>>4])
			buf.WriteByte(upperhex[b&0x0f])
		}
	}
}

// openWord 将编码字词的开头写入 buf。
func (e WordEncoder) openWord(buf *strings.Builder, charset string) {
	buf.WriteString("=?")
	buf.WriteString(charset)
	buf.WriteByte('?')
	buf.WriteByte(byte(e))
	buf.WriteByte('?')
}

// closeWord 将编码字词的结尾写入 buf。
func closeWord(buf *strings.Builder) {
	buf.WriteString("?=")
}

// splitWord 关闭当前编码字词并开启一个新的。
func (e WordEncoder) splitWord(buf *strings.Builder, charset string) {
	closeWord(buf)
	buf.WriteByte(' ')
	e.openWord(buf, charset)
}

func isUTF8(charset string) bool {
	return strings.EqualFold(charset, "UTF-8")
}

const upperhex = "0123456789ABCDEF"

// WordDecoder 解码包含 RFC 2047 编码字词的 MIME 头部。
type WordDecoder struct {
	// CharsetReader，若非 nil，定义一个函数用于生成字符集转换读取器，
	// 将给定字符集转换为 UTF-8。
	// 字符集始终为小写。utf-8、iso-8859-1 和 us-ascii 字符集由默认处理。
	// CharsetReader 的其中一个结果值必须非 nil。
	CharsetReader func(charset string, input io.Reader) (io.Reader, error)
}

// Decode 解码一个 RFC 2047 编码字词。
func (d *WordDecoder) Decode(word string) (string, error) {
	// See https://tools.ietf.org/html/rfc2047#section-2 for details.
	// Our decoder is permissive, we accept empty encoded-text.
	if len(word) < 8 || !strings.HasPrefix(word, "=?") || !strings.HasSuffix(word, "?=") || strings.Count(word, "?") != 4 {
		return "", errInvalidWord
	}
	word = word[2 : len(word)-2]

	// split word "UTF-8?q?text" into "UTF-8", 'q', and "text"
	charset, text, _ := strings.Cut(word, "?")
	if charset == "" {
		return "", errInvalidWord
	}
	encoding, text, _ := strings.Cut(text, "?")
	if len(encoding) != 1 {
		return "", errInvalidWord
	}

	content, err := decode(encoding[0], text)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := d.convert(&buf, charset, content); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// DecodeHeader 解码给定字符串中的所有编码字词。
// 当且仅当 d 的 [WordDecoder.CharsetReader] 返回错误时，该函数才返回错误。
func (d *WordDecoder) DecodeHeader(header string) (string, error) {
	// 若没有编码字词，则在创建缓冲区之前返回。
	i := strings.Index(header, "=?")
	if i == -1 {
		return header, nil
	}

	var buf strings.Builder

	buf.WriteString(header[:i])
	header = header[i:]

	betweenWords := false
	for {
		start := strings.Index(header, "=?")
		if start == -1 {
			break
		}
		cur := start + len("=?")

		i := strings.Index(header[cur:], "?")
		if i == -1 {
			break
		}
		charset := header[cur : cur+i]
		cur += i + len("?")

		if len(header) < cur+len("Q??=") {
			break
		}
		encoding := header[cur]
		cur++

		if header[cur] != '?' {
			break
		}
		cur++

		j := strings.Index(header[cur:], "?=")
		if j == -1 {
			break
		}
		text := header[cur : cur+j]
		end := cur + j + len("?=")

		content, err := decode(encoding, text)
		if err != nil {
			betweenWords = false
			buf.WriteString(header[:start+2])
			header = header[start+2:]
			continue
		}

		// Write characters before the encoded-word. White-space and newline
		// characters separating two encoded-words must be deleted.
		if start > 0 && (!betweenWords || hasNonWhitespace(header[:start])) {
			buf.WriteString(header[:start])
		}

		if err := d.convert(&buf, charset, content); err != nil {
			return "", err
		}

		header = header[end:]
		betweenWords = true
	}

	if len(header) > 0 {
		buf.WriteString(header)
	}

	return buf.String(), nil
}

func decode(encoding byte, text string) ([]byte, error) {
	switch encoding {
	case 'B', 'b':
		return base64.StdEncoding.DecodeString(text)
	case 'Q', 'q':
		return qDecode(text)
	default:
		return nil, errInvalidWord
	}
}

func (d *WordDecoder) convert(buf *strings.Builder, charset string, content []byte) error {
	switch {
	case strings.EqualFold("utf-8", charset):
		buf.Write(content)
	case strings.EqualFold("iso-8859-1", charset):
		for _, c := range content {
			buf.WriteRune(rune(c))
		}
	case strings.EqualFold("us-ascii", charset):
		for _, c := range content {
			if c >= utf8.RuneSelf {
				buf.WriteRune(unicode.ReplacementChar)
			} else {
				buf.WriteByte(c)
			}
		}
	default:
		if d.CharsetReader == nil {
			return fmt.Errorf("mime: unhandled charset %q", charset)
		}
		r, err := d.CharsetReader(strings.ToLower(charset), bytes.NewReader(content))
		if err != nil {
			return err
		}
		if _, err = io.Copy(buf, r); err != nil {
			return err
		}
	}
	return nil
}

// hasNonWhitespace 报告 s（假定为 ASCII）是否包含至少一个非空白字节。
func hasNonWhitespace(s string) bool {
	for _, b := range s {
		switch b {
		// 编码字词只能以线性空白字符分隔，不包括垂直制表符 (\v)。
		case ' ', '\t', '\n', '\r':
		default:
			return true
		}
	}
	return false
}

// qDecode 解码 Q 编码的字符串。
func qDecode(s string) ([]byte, error) {
	dec := make([]byte, len(s))
	n := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '_':
			dec[n] = ' '
		case c == '=':
			if i+2 >= len(s) {
				return nil, errInvalidWord
			}
			b, err := readHexByte(s[i+1], s[i+2])
			if err != nil {
				return nil, err
			}
			dec[n] = b
			i += 2
		case (c <= '~' && c >= ' ') || c == '\n' || c == '\r' || c == '\t':
			dec[n] = c
		default:
			return nil, errInvalidWord
		}
		n++
	}

	return dec[:n], nil
}

// readHexByte returns the byte from its quoted-printable representation.
func readHexByte(a, b byte) (byte, error) {
	var hb, lb byte
	var err error
	if hb, err = fromHex(a); err != nil {
		return 0, err
	}
	if lb, err = fromHex(b); err != nil {
		return 0, err
	}
	return hb<<4 | lb, nil
}

func fromHex(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, nil
	// 接受编码错误的字节。
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	}
	return 0, fmt.Errorf("mime: invalid hex byte %#02x", b)
}
