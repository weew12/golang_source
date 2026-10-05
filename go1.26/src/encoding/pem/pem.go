// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pem 实现 PEM 数据编码，该编码源自隐私增强邮件（Privacy
// Enhanced Mail）。如今 PEM 编码最常见的用途是 TLS 密钥和
// 证书。参见 RFC 1421。
package pem

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"slices"
	"strings"
)

// Block 表示一个 PEM 编码的结构。
//
// 编码形式为：
//
//	-----BEGIN Type-----
//	Headers
//	base64-encoded Bytes
//	-----END Type-----
//
// 其中 [Block.Headers] 是一个可能为空的 Key: Value 行序列。
type Block struct {
	Type    string            // 类型，取自前言（即 "RSA PRIVATE KEY"）。
	Headers map[string]string // 可选的头部。
	Bytes   []byte            // 内容的解码字节。通常是 DER 编码的 ASN.1 结构。
}

// getLine 从给定的字节数组中返回第一个以 \r\n 或 \n 分隔的行。
// 该行不包含结尾的空白字符或结尾的换行
// 字节。字节数组的剩余部分（同样不包含换行
// 字节）也会被返回，并且它总是小于原始的
// 参数。
func getLine(data []byte) (line, rest []byte, consumed int) {
	i := bytes.IndexByte(data, '\n')
	var j int
	if i < 0 {
		i = len(data)
		j = i
	} else {
		j = i + 1
		if i > 0 && data[i-1] == '\r' {
			i--
		}
	}
	return bytes.TrimRight(data[0:i], " \t"), data[j:], j
}

// removeSpacesAndTabs 返回其输入的一份副本，其中所有空格和制表符
// 都已被移除（如果存在的话）。否则，输入原样返回。
//
// base64 解码器已经会跳过换行字符，因此我们无需在此处
// 将它们过滤掉。
func removeSpacesAndTabs(data []byte) []byte {
	if !bytes.ContainsAny(data, " \t") {
		// 快速路径；PEM 内的大多数 base64 数据包含换行符，
		// 但没有空格或制表符。跳过额外的分配和工作。
		return data
	}
	result := make([]byte, len(data))
	n := 0

	for _, b := range data {
		if b == ' ' || b == '\t' {
			continue
		}
		result[n] = b
		n++
	}

	return result[0:n]
}

var pemStart = []byte("\n-----BEGIN ")
var pemEnd = []byte("\n-----END ")
var pemEndOfLine = []byte("-----")
var colon = []byte(":")

// Decode 会在输入中查找下一个 PEM 格式的块（证书、私钥
// 等）。它返回该块以及输入的剩余部分。如果
// 未找到 PEM 数据，则 p 为 nil，并会在 rest 中返回
// 整个输入。块必须从一行的开头开始，并在一行的末尾结束。
func Decode(data []byte) (p *Block, rest []byte) {
	// pemStart 以换行符开头。然而，在字节数组的最开头，
	// 我们会接受不带该换行符的起始字符串。
	rest = data

	endTrailerIndex := 0
	for {
		// 如果我们已经尝试解析过一个块，就跳过我们已经
		// 见过的 END。
		if endTrailerIndex < 0 || endTrailerIndex > len(rest) {
			return nil, data
		}
		rest = rest[endTrailerIndex:]

		// 找到第一个 END 行，然后找到该结束行之前最后一个
		// BEGIN 行。这让我们可以跳过任何没有匹配 END 的
		// 重复 BEGIN 行。
		endIndex := bytes.Index(rest, pemEnd)
		if endIndex < 0 {
			return nil, data
		}
		endTrailerIndex = endIndex + len(pemEnd)
		beginIndex := bytes.LastIndex(rest[:endIndex], pemStart[1:])
		if beginIndex < 0 || (beginIndex > 0 && rest[beginIndex-1] != '\n') {
			continue
		}
		rest = rest[beginIndex+len(pemStart)-1:]
		endIndex -= beginIndex + len(pemStart) - 1
		endTrailerIndex -= beginIndex + len(pemStart) - 1

		var typeLine []byte
		var consumed int
		typeLine, rest, consumed = getLine(rest)
		endIndex -= consumed
		endTrailerIndex -= consumed
		if !bytes.HasSuffix(typeLine, pemEndOfLine) {
			continue
		}
		typeLine = typeLine[0 : len(typeLine)-len(pemEndOfLine)]

		p = &Block{
			Headers: make(map[string]string),
			Type:    string(typeLine),
		}

		for {
			// 这个循环会终止，因为 getLine 的第二个结果
			// 总是小于其参数。
			if len(rest) == 0 {
				return nil, data
			}
			line, next, consumed := getLine(rest)

			key, val, ok := bytes.Cut(line, colon)
			if !ok {
				break
			}

			// TODO(agl): 需要处理跨多行的值。
			key = bytes.TrimSpace(key)
			val = bytes.TrimSpace(val)
			p.Headers[string(key)] = string(val)
			rest = next
			endIndex -= consumed
			endTrailerIndex -= consumed
		}

		// 如果存在头部，则头部与 END 行之间必定有一个换行符，
		// 因此 endIndex 应 >= 0。
		if len(p.Headers) > 0 && endIndex < 0 {
			continue
		}

		// 在结束行的 "-----" 之后，应该有相同的类型，
		// 然后是最后五个短横线。
		endTrailer := rest[endTrailerIndex:]
		endTrailerLen := len(typeLine) + len(pemEndOfLine)
		if len(endTrailer) < endTrailerLen {
			continue
		}

		restOfEndLine := endTrailer[endTrailerLen:]
		endTrailer = endTrailer[:endTrailerLen]
		if !bytes.HasPrefix(endTrailer, typeLine) ||
			!bytes.HasSuffix(endTrailer, pemEndOfLine) {
			continue
		}

		// 该行必须以仅含空白字符结尾。
		if s, _, _ := getLine(restOfEndLine); len(s) != 0 {
			continue
		}

		p.Bytes = []byte{}
		if endIndex > 0 {
			base64Data := removeSpacesAndTabs(rest[:endIndex])
			p.Bytes = make([]byte, base64.StdEncoding.DecodedLen(len(base64Data)))
			n, err := base64.StdEncoding.Decode(p.Bytes, base64Data)
			if err != nil {
				continue
			}
			p.Bytes = p.Bytes[:n]
		}

		// 这里的 -1 是因为如果 PEM 块为空，我们可能只匹配到了
		// 不含前导换行符的 pemEnd。
		_, rest, _ = getLine(rest[endIndex+len(pemEnd)-1:])
		return p, rest
	}
}

const pemLineLength = 64

type lineBreaker struct {
	line [pemLineLength]byte
	used int
	out  io.Writer
}

var nl = []byte{'\n'}

func (l *lineBreaker) Write(b []byte) (n int, err error) {
	if l.used+len(b) < pemLineLength {
		copy(l.line[l.used:], b)
		l.used += len(b)
		return len(b), nil
	}

	n, err = l.out.Write(l.line[0:l.used])
	if err != nil {
		return
	}
	excess := pemLineLength - l.used
	l.used = 0

	n, err = l.out.Write(b[0:excess])
	if err != nil {
		return
	}

	n, err = l.out.Write(nl)
	if err != nil {
		return
	}

	return l.Write(b[excess:])
}

func (l *lineBreaker) Close() (err error) {
	if l.used > 0 {
		_, err = l.out.Write(l.line[0:l.used])
		if err != nil {
			return
		}
		_, err = l.out.Write(nl)
	}

	return
}

func writeHeader(out io.Writer, k, v string) error {
	_, err := out.Write([]byte(k + ": " + v + "\n"))
	return err
}

// Encode 将 b 的 PEM 编码写入 out。
func Encode(out io.Writer, b *Block) error {
	// 在写入任何输出之前检查无效的块。
	for k := range b.Headers {
		if strings.Contains(k, ":") {
			return errors.New("pem: cannot encode a header key that contains a colon")
		}
	}

	// 下面所有的错误都来自底层 io.Writer 的转发，
	// 因此现在可以安全地写入数据。

	if _, err := out.Write(pemStart[1:]); err != nil {
		return err
	}
	if _, err := out.Write([]byte(b.Type + "-----\n")); err != nil {
		return err
	}

	if len(b.Headers) > 0 {
		const procType = "Proc-Type"
		h := make([]string, 0, len(b.Headers))
		hasProcType := false
		for k := range b.Headers {
			if k == procType {
				hasProcType = true
				continue
			}
			h = append(h, k)
		}
		// Proc-Type 头部必须首先写入。
		// 参见 RFC 1421 第 4.6.1.1 节
		if hasProcType {
			if err := writeHeader(out, procType, b.Headers[procType]); err != nil {
				return err
			}
		}
		// 为了输出的一致性，按 key 排序写入其他头部。
		slices.Sort(h)
		for _, k := range h {
			if err := writeHeader(out, k, b.Headers[k]); err != nil {
				return err
			}
		}
		if _, err := out.Write(nl); err != nil {
			return err
		}
	}

	var breaker lineBreaker
	breaker.out = out

	b64 := base64.NewEncoder(base64.StdEncoding, &breaker)
	if _, err := b64.Write(b.Bytes); err != nil {
		return err
	}
	b64.Close()
	breaker.Close()

	if _, err := out.Write(pemEnd[1:]); err != nil {
		return err
	}
	_, err := out.Write([]byte(b.Type + "-----\n"))
	return err
}

// EncodeToMemory 返回 b 的 PEM 编码。
//
// 如果 b 的头部无效而无法编码，
// EncodeToMemory 返回 nil。如果需要
// 报告此错误情况的详细信息，请改用 [Encode]。
func EncodeToMemory(b *Block) []byte {
	var buf bytes.Buffer
	if err := Encode(&buf, b); err != nil {
		return nil
	}
	return buf.Bytes()
}
