// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package csv 读取和写入逗号分隔值（CSV）文件。
// CSV 文件有多种类型；本包支持 RFC 4180 中描述的格式，
// 只是 [Writer] 默认使用 LF 而不是 CRLF 作为换行字符。
//
// 一个 csv 文件包含零条或多条记录，每条记录包含一个或多个字段。
// 每条记录由换行字符分隔。最后一条记录后面
// 可以选择性地跟一个换行字符。
//
//	field1,field2,field3
//
// 空白字符被视为字段的一部分。
//
// 换行字符之前的回车符会被静默移除。
//
// 空行会被忽略。仅含空白字符的行（不包括
// 结尾的换行字符）不算空行。
//
// 以引号字符 " 开头和结尾的字段称为
// 带引号字段。开头和结尾的引号不属于
// 字段的一部分。
//
// 源内容：
//
//	normal string,"quoted-field"
//
// 会得到字段
//
//	{`normal string`, `quoted-field`}
//
// 在带引号字段内，紧跟着第二个引号字符的引号字符
// 被视为单个引号。
//
//	"the ""word"" is true","a ""quoted-field"""
//
// 会得到
//
//	{`the "word" is true`, `a "quoted-field"`}
//
// 换行符和逗号可以包含在带引号字段中
//
//	"Multi-line
//	field","comma is ,"
//
// 会得到
//
//	{`Multi-line
//	field`, `comma is ,`}
package csv

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode"
	"unicode/utf8"
)

// 解析错误会返回 ParseError。
// 行号和列号从 1 开始索引。
type ParseError struct {
	StartLine int   // 记录开始的行
	Line      int   // 发生错误的行
	Column    int   // 发生错误的列（从 1 开始的字节索引）
	Err       error // 实际的错误
}

func (e *ParseError) Error() string {
	if e.Err == ErrFieldCount {
		return fmt.Sprintf("record on line %d: %v", e.Line, e.Err)
	}
	if e.StartLine != e.Line {
		return fmt.Sprintf("record on line %d; parse error on line %d, column %d: %v", e.StartLine, e.Line, e.Column, e.Err)
	}
	return fmt.Sprintf("parse error on line %d, column %d: %v", e.Line, e.Column, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// 这些是可以在 [ParseError.Err] 中返回的错误。
var (
	ErrBareQuote  = errors.New("bare \" in non-quoted-field")
	ErrQuote      = errors.New("extraneous or missing \" in quoted-field")
	ErrFieldCount = errors.New("wrong number of fields")

	// Deprecated: ErrTrailingComma 不再被使用。
	ErrTrailingComma = errors.New("extra delimiter at end of line")
)

var errInvalidDelim = errors.New("csv: invalid field or comment delimiter")

func validDelim(r rune) bool {
	return r != 0 && r != '"' && r != '\r' && r != '\n' && utf8.ValidRune(r) && r != utf8.RuneError
}

// Reader 从 CSV 编码的文件中读取记录。
//
// 由 [NewReader] 返回的 Reader 期望输入符合 RFC 4180。
// 导出的字段可以在首次调用 [Reader.Read] 或 [Reader.ReadAll]
// 之前更改，以自定义各个细节。
//
// Reader 会将其输入中所有 \r\n 序列转换为单纯的 \n，
// 包括多行字段值中的，因此返回的数据不会
// 依赖于输入文件使用哪种行结束约定。
type Reader struct {
	// Comma 是字段分隔符。
	// 由 NewReader 设为逗号（','）。
	// Comma 必须是有效的 rune，且不能是 \r、\n
	// 或 Unicode 替换字符（0xFFFD）。
	Comma rune

	// Comment 若非 0，则它是注释字符。以 Comment 字符开头
	// 且前面没有空白字符的行会被忽略。
	// 若有前导空白字符，则 Comment 字符会成为
	// 字段的一部分，即使 TrimLeadingSpace 为 true。
	// Comment 必须是有效的 rune，且不能是 \r、\n
	// 或 Unicode 替换字符（0xFFFD）。
	// 它也不能等于 Comma。
	Comment rune

	// FieldsPerRecord 是每条记录期望的字段数。
	// 如果 FieldsPerRecord 为正，Read 要求每条记录
	// 具有给定的字段数。如果 FieldsPerRecord 为 0，Read 会将它设为
	// 第一条记录的字段数，从而使后续记录必须
	// 具有相同的字段计数。如果 FieldsPerRecord 为负，则不进行检查，
	// 记录可以具有可变的字段数。
	FieldsPerRecord int

	// 如果 LazyQuotes 为 true，引号可以出现在不带引号的字段中，
	// 非成对的引号也可以出现在带引号的字段中。
	LazyQuotes bool

	// 如果 TrimLeadingSpace 为 true，字段中的前导空白会被忽略。
	// 即使字段分隔符 Comma 是空白，也会这样做。
	TrimLeadingSpace bool

	// ReuseRecord 控制 Read 的调用是否可以为提升性能而返回
	// 与上一次调用返回的切片共享底层数组的切片。
	// 默认情况下，每次调用 Read 都返回由调用者拥有的新分配内存。
	ReuseRecord bool

	// Deprecated: TrailingComma 不再被使用。
	TrailingComma bool

	r *bufio.Reader

	// numLine 是 CSV 文件中当前正在读取的行。
	numLine int

	// offset 是当前读取器位置的输入流字节偏移量。
	offset int64

	// rawBuffer 是仅由 readLine 方法使用的行缓冲区。
	rawBuffer []byte

	// recordBuffer 依次保存未转义的字段。
	// 可以通过 fieldIndexes 中的索引访问这些字段。
	// 例如，对于行 `a,"b","c""d",e`，recordBuffer 将包含 `abc"de`，
	// fieldIndexes 将包含索引 [1, 2, 5, 6]。
	recordBuffer []byte

	// fieldIndexes 是 recordBuffer 内字段的索引。
	// 第 i 个字段在 recordBuffer 中结束于偏移量 fieldIndexes[i]。
	fieldIndexes []int

	// fieldPositions 是 Read 返回的最后一条记录
	// 的字段位置索引。
	fieldPositions []position

	// lastRecord 是记录缓存，仅在 ReuseRecord == true 时使用。
	lastRecord []string
}

// NewReader 返回一个从 r 读取的新 Reader。
func NewReader(r io.Reader) *Reader {
	return &Reader{
		Comma: ',',
		r:     bufio.NewReader(r),
	}
}

// Read 从 r 读取一条记录（一个字段切片）。
// 如果记录的字段数不符合预期，
// Read 会返回该记录以及错误 [ErrFieldCount]。
// 如果记录中包含无法解析的字段，
// Read 会返回部分记录以及解析错误。
// 该部分记录包含出错之前已读取的所有字段。
// 如果没有剩余数据可读，Read 返回 nil, [io.EOF]。
// 如果 [Reader.ReuseRecord] 为 true，返回的切片可能在
// 多次调用 Read 之间共享。
func (r *Reader) Read() (record []string, err error) {
	if r.ReuseRecord {
		record, err = r.readRecord(r.lastRecord)
		r.lastRecord = record
	} else {
		record, err = r.readRecord(nil)
	}
	return record, err
}

// FieldPos 返回 [Reader.Read] 最近返回的切片中
// 给定索引的字段起始位置所对应的行和列。行号和列号从 1 开始；
// 列按字节计数，而不是按 rune。
//
// 如果使用越界的索引调用它，它会 panic。
func (r *Reader) FieldPos(field int) (line, column int) {
	if field < 0 || field >= len(r.fieldPositions) {
		panic("out of range index passed to FieldPos")
	}
	p := &r.fieldPositions[field]
	return p.line, p.col
}

// InputOffset 返回当前读取器位置的输入流字节偏移量。
// 该偏移量给出最近读取的行的末尾位置
// 以及下一行的起始位置。
func (r *Reader) InputOffset() int64 {
	return r.offset
}

// pos 保存字段在当前行中的位置。
type position struct {
	line, col int
}

// ReadAll 从 r 读取所有剩余记录。
// 每条记录是一个字段切片。
// 成功的调用返回 err == nil，而不是 err == [io.EOF]。因为 ReadAll
// 定义为读取到 EOF，它不把文件末尾视为需要
// 报告的错误。
func (r *Reader) ReadAll() (records [][]string, err error) {
	for {
		record, err := r.readRecord(nil)
		if err == io.EOF {
			return records, nil
		}
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
}

// readLine 读取下一行（包括结尾的换行符）。
// 如果在没有结尾换行符的情况下遇到 EOF，则会省略它。
// 如果读取了一些字节，那么错误永远不会是 [io.EOF]。
// 结果仅在下次调用 readLine 之前有效。
func (r *Reader) readLine() ([]byte, error) {
	line, err := r.r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		r.rawBuffer = append(r.rawBuffer[:0], line...)
		for err == bufio.ErrBufferFull {
			line, err = r.r.ReadSlice('\n')
			r.rawBuffer = append(r.rawBuffer, line...)
		}
		line = r.rawBuffer
	}
	readSize := len(line)
	if readSize > 0 && err == io.EOF {
		err = nil
		// 为了向后兼容，在 EOF 之前去掉结尾的 \r。
		if line[readSize-1] == '\r' {
			line = line[:readSize-1]
		}
	}
	r.numLine++
	r.offset += int64(readSize)
	// 将所有输入行中的 \r\n 规范化为 \n。
	if n := len(line); n >= 2 && line[n-2] == '\r' && line[n-1] == '\n' {
		line[n-2] = '\n'
		line = line[:n-1]
	}
	return line, err
}

// lengthNL 报告结尾 \n 的字节数。
func lengthNL(b []byte) int {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return 1
	}
	return 0
}

// nextRune 返回 b 中的下一个 rune，或 utf8.RuneError。
func nextRune(b []byte) rune {
	r, _ := utf8.DecodeRune(b)
	return r
}

func (r *Reader) readRecord(dst []string) ([]string, error) {
	if r.Comma == r.Comment || !validDelim(r.Comma) || (r.Comment != 0 && !validDelim(r.Comment)) {
		return nil, errInvalidDelim
	}

	// 读取一行（自动跳过空行和任何注释）。
	var line []byte
	var errRead error
	for errRead == nil {
		line, errRead = r.readLine()
		if r.Comment != 0 && nextRune(line) == r.Comment {
			line = nil
			continue // 跳过注释行
		}
		if errRead == nil && len(line) == lengthNL(line) {
			line = nil
			continue // 跳过空行
		}
		break
	}
	if errRead == io.EOF {
		return nil, errRead
	}

	// 解析记录中的每个字段。
	var err error
	const quoteLen = len(`"`)
	commaLen := utf8.RuneLen(r.Comma)
	recLine := r.numLine // 记录开始的行
	r.recordBuffer = r.recordBuffer[:0]
	r.fieldIndexes = r.fieldIndexes[:0]
	r.fieldPositions = r.fieldPositions[:0]
	pos := position{line: r.numLine, col: 1}
parseField:
	for {
		if r.TrimLeadingSpace {
			i := bytes.IndexFunc(line, func(r rune) bool {
				return !unicode.IsSpace(r)
			})
			if i < 0 {
				i = len(line)
				pos.col -= lengthNL(line)
			}
			line = line[i:]
			pos.col += i
		}
		if len(line) == 0 || line[0] != '"' {
			// 不带引号的字符串字段
			i := bytes.IndexRune(line, r.Comma)
			field := line
			if i >= 0 {
				field = field[:i]
			} else {
				field = field[:len(field)-lengthNL(field)]
			}
			// 检查以确保字段中不出现引号。
			if !r.LazyQuotes {
				if j := bytes.IndexByte(field, '"'); j >= 0 {
					col := pos.col + j
					err = &ParseError{StartLine: recLine, Line: r.numLine, Column: col, Err: ErrBareQuote}
					break parseField
				}
			}
			r.recordBuffer = append(r.recordBuffer, field...)
			r.fieldIndexes = append(r.fieldIndexes, len(r.recordBuffer))
			r.fieldPositions = append(r.fieldPositions, pos)
			if i >= 0 {
				line = line[i+commaLen:]
				pos.col += i + commaLen
				continue parseField
			}
			break parseField
		} else {
			// 带引号的字符串字段
			fieldPos := pos
			line = line[quoteLen:]
			pos.col += quoteLen
			for {
				i := bytes.IndexByte(line, '"')
				if i >= 0 {
					// 遇到下一个引号。
					r.recordBuffer = append(r.recordBuffer, line[:i]...)
					line = line[i+quoteLen:]
					pos.col += i + quoteLen
					switch rn := nextRune(line); {
					case rn == '"':
						// `""` 序列（追加一个引号）。
						r.recordBuffer = append(r.recordBuffer, '"')
						line = line[quoteLen:]
						pos.col += quoteLen
					case rn == r.Comma:
						// `",` 序列（字段结束）。
						line = line[commaLen:]
						pos.col += commaLen
						r.fieldIndexes = append(r.fieldIndexes, len(r.recordBuffer))
						r.fieldPositions = append(r.fieldPositions, fieldPos)
						continue parseField
					case lengthNL(line) == len(line):
						// `"\n` 序列（行结束）。
						r.fieldIndexes = append(r.fieldIndexes, len(r.recordBuffer))
						r.fieldPositions = append(r.fieldPositions, fieldPos)
						break parseField
					case r.LazyQuotes:
						// `"` 序列（裸引号）。
						r.recordBuffer = append(r.recordBuffer, '"')
					default:
						// `"*` 序列（无效的未转义引号）。
						err = &ParseError{StartLine: recLine, Line: r.numLine, Column: pos.col - quoteLen, Err: ErrQuote}
						break parseField
					}
				} else if len(line) > 0 {
					// 遇到行尾（复制目前的所有数据）。
					r.recordBuffer = append(r.recordBuffer, line...)
					if errRead != nil {
						break parseField
					}
					pos.col += len(line)
					line, errRead = r.readLine()
					if len(line) > 0 {
						pos.line++
						pos.col = 1
					}
					if errRead == io.EOF {
						errRead = nil
					}
				} else {
					// 文件突然结束（EOF 或错误）。
					if !r.LazyQuotes && errRead == nil {
						err = &ParseError{StartLine: recLine, Line: pos.line, Column: pos.col, Err: ErrQuote}
						break parseField
					}
					r.fieldIndexes = append(r.fieldIndexes, len(r.recordBuffer))
					r.fieldPositions = append(r.fieldPositions, fieldPos)
					break parseField
				}
			}
		}
	}
	if err == nil {
		err = errRead
	}

	// 创建一个单独的字符串并从中切出各个切片。
	// 这会将各字段的内存固定在一起，但只需分配一次。
	str := string(r.recordBuffer) // 只转换为字符串一次，以批量分配
	dst = dst[:0]
	if cap(dst) < len(r.fieldIndexes) {
		dst = make([]string, len(r.fieldIndexes))
	}
	dst = dst[:len(r.fieldIndexes)]
	var preIdx int
	for i, idx := range r.fieldIndexes {
		dst[i] = str[preIdx:idx]
		preIdx = idx
	}

	// 检查或更新每条记录期望的字段数。
	if r.FieldsPerRecord > 0 {
		if len(dst) != r.FieldsPerRecord && err == nil {
			err = &ParseError{
				StartLine: recLine,
				Line:      recLine,
				Column:    1,
				Err:       ErrFieldCount,
			}
		}
	} else if r.FieldsPerRecord == 0 {
		r.FieldsPerRecord = len(dst)
	}
	return dst, err
}
