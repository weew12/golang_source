// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"context"
	"encoding"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"sync"
	"unicode"
	"unicode/utf8"
)

// TextHandler 是一个 [Handler]，它将 Record 以由空格分隔、
// 并以换行结尾的 key=value 对序列形式写入 [io.Writer]。
type TextHandler struct {
	*commonHandler
}

// NewTextHandler 创建一个写入 w 的 [TextHandler]，
// 使用给定的选项。
// 如果 opts 为 nil，则使用默认选项。
func NewTextHandler(w io.Writer, opts *HandlerOptions) *TextHandler {
	if opts == nil {
		opts = &HandlerOptions{}
	}
	return &TextHandler{
		&commonHandler{
			json: false,
			w:    w,
			opts: *opts,
			mu:   &sync.Mutex{},
		},
	}
}

// Enabled 报告 handler 是否处理给定级别的记录。
// handler 会忽略级别更低的记录。
func (h *TextHandler) Enabled(_ context.Context, level Level) bool {
	return h.commonHandler.enabled(level)
}

// WithAttrs 返回一个新的 [TextHandler]，其属性由
// h 的属性后跟 attrs 组成。
func (h *TextHandler) WithAttrs(attrs []Attr) Handler {
	return &TextHandler{commonHandler: h.commonHandler.withAttrs(attrs)}
}

func (h *TextHandler) WithGroup(name string) Handler {
	return &TextHandler{commonHandler: h.commonHandler.withGroup(name)}
}

// Handle 将它的参数 [Record] 格式化为由空格分隔的
// key=value 项组成的单行。
//
// 如果 Record 的时间为零，则省略时间。
// 否则，键为 "time"，
// 其值以毫秒精度的 RFC3339 格式输出。
//
// 级别的键为 "level"，其值是调用 [Level.String] 的结果。
//
// 如果设置了 AddSource 选项且源信息可用，
// 则键为 "source"，其值以 FILE:LINE 形式输出。
//
// 消息的键为 "msg"。
//
// 若要修改这些属性或其他属性，或将它们从输出中移除，请使用
// [HandlerOptions.ReplaceAttr]。
//
// 如果某个值实现了 [encoding.TextMarshaler]，则写入 MarshalText 的结果。
// 否则，写入 [fmt.Sprint] 的结果。
//
// 如果键和值包含 Unicode 空白字符、不可打印字符、'"' 或 '='，
// 则会用 [strconv.Quote] 对它们加引号。
//
// 分组内的键由以点分隔的组件（键或分组名称）组成。
// 不会进行进一步的转义。
// 因此，无法从键 "a.b.c" 判断是否存在
// 两个分组 "a" 和 "b" 以及一个键 "c"，还是单个分组 "a.b" 和一个键 "c"，
// 或单个分组 "a" 和一个键 "b.c"。
// 如果即使组件内存在点，也有必要重建某个键的分组结构，
// 请使用 [HandlerOptions.ReplaceAttr] 将该信息编码到键中。
//
// 每次调用 Handle 都会导致一次对
// io.Writer.Write 的序列化调用。
func (h *TextHandler) Handle(_ context.Context, r Record) error {
	return h.commonHandler.handle(r)
}

func appendTextValue(s *handleState, v Value) error {
	switch v.Kind() {
	case KindString:
		s.appendString(v.str())
	case KindTime:
		s.appendTime(v.time())
	case KindAny:
		if tm, ok := v.any.(encoding.TextMarshaler); ok {
			data, err := tm.MarshalText()
			if err != nil {
				return err
			}
			// TODO：避免转换为字符串。
			s.appendString(string(data))
			return nil
		}
		if bs, ok := byteSlice(v.any); ok {
			// 自 Go 1.19 起，这只对长于 32 字节的字符串分配内存。
			s.buf.WriteString(strconv.Quote(string(bs)))
			return nil
		}
		s.appendString(fmt.Sprintf("%+v", v.Any()))
	default:
		*s.buf = v.append(*s.buf)
	}
	return nil
}

// byteSlice 如果参数的底层类型是 []byte，则将其作为 []byte 返回，
// 第二个返回值为 true。否则返回 nil, false。
func byteSlice(a any) ([]byte, bool) {
	if bs, ok := a.([]byte); ok {
		return bs, true
	}
	// 与 Printf 的 %s 类似，我们允许切片类型和字节元素类型都被命名。
	t := reflect.TypeOf(a)
	if t != nil && t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return reflect.ValueOf(a).Bytes(), true
	}
	return nil, false
}

func needsQuoting(s string) bool {
	if len(s) == 0 {
		return true
	}
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			// 对除反斜杠之外任何在 JSON 字符串中需要加引号的内容，
			// 以及空格和 '=' 加引号
			if b != '\\' && (b == ' ' || b == '=' || !safeSet[b]) {
				return true
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return true
		}
		i += size
	}
	return false
}
