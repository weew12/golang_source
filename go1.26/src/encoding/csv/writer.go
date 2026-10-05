// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package csv

import (
	"bufio"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Writer 使用 CSV 编码写入记录。
//
// 由 [NewWriter] 返回的 Writer 写入以换行符结尾的记录，并使用 ',' 作为字段分隔符。
// 导出的字段可以在首次调用 [Writer.Write] 或 [Writer.WriteAll]
// 之前更改，以自定义各个细节。
//
// [Writer.Comma] 是字段分隔符。
//
// 如果 [Writer.UseCRLF] 为 true，
// Writer 会用 \r\n 而不是 \n 结束每一输出行。
//
// 单条记录的写入是带缓冲的。
// 所有数据写入完毕后，客户端应调用
// [Writer.Flush] 方法，以保证所有数据都已转发到
// 底层 [io.Writer]。任何发生的错误都应
// 通过调用 [Writer.Error] 方法检查。
type Writer struct {
	Comma   rune // 字段分隔符（由 NewWriter 设为 ','）
	UseCRLF bool // 若为 true，则使用 \r\n 作为行终止符
	w       *bufio.Writer
}

// NewWriter 返回一个写入 w 的新 Writer。
func NewWriter(w io.Writer) *Writer {
	return &Writer{
		Comma: ',',
		w:     bufio.NewWriter(w),
	}
}

// Write 将单条 CSV 记录连同任何必要的引号一起写入 w。
// 记录是一个字符串切片，其中每个字符串为一个字段。
// 写入是带缓冲的，因此最终必须调用 [Writer.Flush] 以确保
// 记录被写入底层 [io.Writer]。
func (w *Writer) Write(record []string) error {
	if !validDelim(w.Comma) {
		return errInvalidDelim
	}

	for n, field := range record {
		if n > 0 {
			if _, err := w.w.WriteRune(w.Comma); err != nil {
				return err
			}
		}

		// 如果不需要带引号的字段，那么直接
		// 写出该字段并继续处理下一个字段。
		if !w.fieldNeedsQuotes(field) {
			if _, err := w.w.WriteString(field); err != nil {
				return err
			}
			continue
		}

		if err := w.w.WriteByte('"'); err != nil {
			return err
		}
		for len(field) > 0 {
			// 搜索特殊字符。
			i := strings.IndexAny(field, "\"\r\n")
			if i < 0 {
				i = len(field)
			}

			// 逐字复制特殊字符之前的所有内容。
			if _, err := w.w.WriteString(field[:i]); err != nil {
				return err
			}
			field = field[i:]

			// 编码该特殊字符。
			if len(field) > 0 {
				var err error
				switch field[0] {
				case '"':
					_, err = w.w.WriteString(`""`)
				case '\r':
					if !w.UseCRLF {
						err = w.w.WriteByte('\r')
					}
				case '\n':
					if w.UseCRLF {
						_, err = w.w.WriteString("\r\n")
					} else {
						err = w.w.WriteByte('\n')
					}
				}
				field = field[1:]
				if err != nil {
					return err
				}
			}
		}
		if err := w.w.WriteByte('"'); err != nil {
			return err
		}
	}
	var err error
	if w.UseCRLF {
		_, err = w.w.WriteString("\r\n")
	} else {
		err = w.w.WriteByte('\n')
	}
	return err
}

// Flush 将任何缓冲数据写入底层 [io.Writer]。
// 要检查 Flush 期间是否发生错误，请调用 [Writer.Error]。
func (w *Writer) Flush() {
	w.w.Flush()
}

// Error 报告在之前的 [Writer.Write] 或 [Writer.Flush] 期间
// 发生的任何错误。
func (w *Writer) Error() error {
	_, err := w.w.Write(nil)
	return err
}

// WriteAll 使用 [Writer.Write] 将多条 CSV 记录写入 w，
// 然后调用 [Writer.Flush]，返回 Flush 产生的任何错误。
func (w *Writer) WriteAll(records [][]string) error {
	for _, record := range records {
		err := w.Write(record)
		if err != nil {
			return err
		}
	}
	return w.w.Flush()
}

// fieldNeedsQuotes 报告字段是否必须用引号括起来。
// 包含 Comma 的字段、包含引号或换行符的字段，
// 以及以空格开头的字段必须用引号括起来。
// 我们过去会给空字符串加引号，但（自 Go 1.4 起）已不再这样做。
// 这两种表示应当是等价的，但 Postgres 在数据库导入期间
// 会区分带引号与不带引号的空字符串，且它有一个选项
// 可以为不带引号的 CSV 强制使用带引号行为，却没有选项
// 为带引号的 CSV 强制使用不带引号行为，这使得
// 带引号空字符串的 CSV 的实用性严格更低。
// 不给空字符串加引号也使本包与 Microsoft Excel 和 Google Drive
// 的行为一致。
// 对于 Postgres，请给数据终止字符串 `\.` 加引号。
func (w *Writer) fieldNeedsQuotes(field string) bool {
	if field == "" {
		return false
	}

	if field == `\.` {
		return true
	}

	if w.Comma < utf8.RuneSelf {
		for i := 0; i < len(field); i++ {
			c := field[i]
			if c == '\n' || c == '\r' || c == '"' || c == byte(w.Comma) {
				return true
			}
		}
	} else {
		if strings.ContainsRune(field, w.Comma) || strings.ContainsAny(field, "\"\r\n") {
			return true
		}
	}

	r1, _ := utf8.DecodeRuneInString(field)
	return unicode.IsSpace(r1)
}
