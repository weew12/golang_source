// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog/internal/buffer"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// JSONHandler 是一个 [Handler]，它将 Record 以行分隔的 JSON 对象
// 形式写入 [io.Writer]。
type JSONHandler struct {
	*commonHandler
}

// NewJSONHandler 创建一个写入 w 的 [JSONHandler]，
// 使用给定的选项。
// 如果 opts 为 nil，则使用默认选项。
func NewJSONHandler(w io.Writer, opts *HandlerOptions) *JSONHandler {
	if opts == nil {
		opts = &HandlerOptions{}
	}
	return &JSONHandler{
		&commonHandler{
			json: true,
			w:    w,
			opts: *opts,
			mu:   &sync.Mutex{},
		},
	}
}

// Enabled 报告 handler 是否处理给定级别的记录。
// handler 会忽略级别更低的记录。
func (h *JSONHandler) Enabled(_ context.Context, level Level) bool {
	return h.commonHandler.enabled(level)
}

// WithAttrs 返回一个新的 [JSONHandler]，其属性由
// h 的属性后跟 attrs 组成。
func (h *JSONHandler) WithAttrs(attrs []Attr) Handler {
	return &JSONHandler{commonHandler: h.commonHandler.withAttrs(attrs)}
}

func (h *JSONHandler) WithGroup(name string) Handler {
	return &JSONHandler{commonHandler: h.commonHandler.withGroup(name)}
}

// Handle 将它的参数 [Record] 格式化为单行 JSON 对象。
//
// 如果 Record 的时间为零，则省略时间。
// 否则，键为 "time"，
// 其值按照 json.Marshal 的方式输出。
//
// 级别的键为 "level"，其值是调用 [Level.String] 的结果。
//
// 如果设置了 AddSource 选项且源信息可用，
// 则键为 "source"，其值是 [Source] 类型的记录。
//
// 消息的键为 "msg"。
//
// 若要修改这些属性或其他属性，或将它们从输出中移除，请使用
// [HandlerOptions.ReplaceAttr]。
//
// 值的格式化方式与设置了 SetEscapeHTML(false) 的 [encoding/json.Encoder] 相同，
// 但有两个例外。
//
// 第一，Value 类型为 error 的 Attr 会通过调用其 Error 方法
// 格式化为字符串。只有 Attr 中的 error 会得到这种特殊处理，
// 嵌入在由 [encoding/json] 包处理的 struct、slice、map 或其他
// 数据结构中的 error 不会。
//
// 第二，编码失败不会导致 Handle 返回错误。
// 相反，错误消息会被格式化为字符串。
//
// 每次调用 Handle 都会导致一次对 io.Writer.Write 的序列化调用。
func (h *JSONHandler) Handle(_ context.Context, r Record) error {
	return h.commonHandler.handle(r)
}

// 改编自 time.Time.MarshalJSON，以避免分配内存。
func appendJSONTime(s *handleState, t time.Time) {
	if y := t.Year(); y < 0 || y >= 10000 {
		// RFC 3339 明确规定年份恰好为 4 位数字。
		// 更多讨论参见 golang.org/issue/4556#c15。
		s.appendError(errors.New("time.Time year outside of range [0,9999]"))
	}
	s.buf.WriteByte('"')
	*s.buf = t.AppendFormat(*s.buf, time.RFC3339Nano)
	s.buf.WriteByte('"')
}

func appendJSONValue(s *handleState, v Value) error {
	switch v.Kind() {
	case KindString:
		s.appendString(v.str())
	case KindInt64:
		*s.buf = strconv.AppendInt(*s.buf, v.Int64(), 10)
	case KindUint64:
		*s.buf = strconv.AppendUint(*s.buf, v.Uint64(), 10)
	case KindFloat64:
		// json.Marshal 对浮点数的处理有些古怪；它并不
		// 总是与 strconv.AppendFloat 匹配。所以直接调用它。
		// 这样做开销较大，但浮点数很少见。
		if err := appendJSONMarshal(s.buf, v.Float64()); err != nil {
			return err
		}
	case KindBool:
		*s.buf = strconv.AppendBool(*s.buf, v.Bool())
	case KindDuration:
		// 执行 json.Marshal 所做的操作。
		*s.buf = strconv.AppendInt(*s.buf, int64(v.Duration()), 10)
	case KindTime:
		s.appendTime(v.Time())
	case KindAny:
		a := v.Any()
		_, jm := a.(json.Marshaler)
		if err, ok := a.(error); ok && !jm {
			s.appendString(err.Error())
		} else {
			return appendJSONMarshal(s.buf, a)
		}
	default:
		panic(fmt.Sprintf("bad kind: %s", v.Kind()))
	}
	return nil
}

type jsonEncoder struct {
	buf *bytes.Buffer
	// 使用 json.Encoder 以避免转义 HTML。
	json *json.Encoder
}

var jsonEncoderPool = &sync.Pool{
	New: func() any {
		enc := &jsonEncoder{
			buf: new(bytes.Buffer),
		}
		enc.json = json.NewEncoder(enc.buf)
		enc.json.SetEscapeHTML(false)
		return enc
	},
}

func appendJSONMarshal(buf *buffer.Buffer, v any) error {
	j := jsonEncoderPool.Get().(*jsonEncoder)
	defer func() {
		// 为了减少峰值分配，只将较小的缓冲区归还到池中。
		const maxBufferSize = 16 << 10
		if j.buf.Cap() > maxBufferSize {
			return
		}
		j.buf.Reset()
		jsonEncoderPool.Put(j)
	}()

	if err := j.json.Encode(v); err != nil {
		return err
	}

	bs := j.buf.Bytes()
	buf.Write(bs[:len(bs)-1]) // 移除末尾的换行符
	return nil
}

// appendEscapedJSONString 对 s 进行 JSON 转义，并将其追加到 buf。
// 它不会在字符串两边加上引号。
//
// 修改自 encoding/json/encode.go:encodeState.string，
// 其中 escapeHTML 被设置为 false。
func appendEscapedJSONString(buf []byte, s string) []byte {
	char := func(b byte) { buf = append(buf, b) }
	str := func(s string) { buf = append(buf, s...) }

	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if safeSet[b] {
				i++
				continue
			}
			if start < i {
				str(s[start:i])
			}
			char('\\')
			switch b {
			case '\\', '"':
				char(b)
			case '\n':
				char('n')
			case '\r':
				char('r')
			case '\t':
				char('t')
			default:
				// 这会编码小于 0x20 的字节，但 \t、\n 和 \r 除外。
				str(`u00`)
				char(hex[b>>4])
				char(hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			if start < i {
				str(s[start:i])
			}
			str(`\ufffd`)
			i += size
			start = i
			continue
		}
		// U+2028 是行分隔符（LINE SEPARATOR）。
		// U+2029 是段落分隔符（PARAGRAPH SEPARATOR）。
		// 从技术上讲，它们都是 JSON 字符串中的有效字符，
		// 但在必须作为 JavaScript 求值的 JSONP 中无法使用，
		// 并可能在那里导致安全漏洞。转义它们是合法的 JSON，
		// 因此我们无条件地这样做。
		// 讨论参见 http://timelessrepo.com/json-isnt-a-javascript-subset。
		if c == '\u2028' || c == '\u2029' {
			if start < i {
				str(s[start:i])
			}
			str(`\u202`)
			char(hex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		str(s[start:])
	}
	return buf
}

const hex = "0123456789abcdef"

// 复制自 encoding/json/tables.go。
//
// 如果具有给定数组位置的 ASCII 字符可以在 JSON 字符串中
// 无需进一步转义地表示，则 safeSet 中对应位置为 true。
//
// 除 ASCII 控制字符（0-31）、双引号（"）和
// 反斜杠字符（"\"）外，所有值都为 true。
var safeSet = [utf8.RuneSelf]bool{
	' ':      true,
	'!':      true,
	'"':      false,
	'#':      true,
	'$':      true,
	'%':      true,
	'&':      true,
	'\'':     true,
	'(':      true,
	')':      true,
	'*':      true,
	'+':      true,
	',':      true,
	'-':      true,
	'.':      true,
	'/':      true,
	'0':      true,
	'1':      true,
	'2':      true,
	'3':      true,
	'4':      true,
	'5':      true,
	'6':      true,
	'7':      true,
	'8':      true,
	'9':      true,
	':':      true,
	';':      true,
	'<':      true,
	'=':      true,
	'>':      true,
	'?':      true,
	'@':      true,
	'A':      true,
	'B':      true,
	'C':      true,
	'D':      true,
	'E':      true,
	'F':      true,
	'G':      true,
	'H':      true,
	'I':      true,
	'J':      true,
	'K':      true,
	'L':      true,
	'M':      true,
	'N':      true,
	'O':      true,
	'P':      true,
	'Q':      true,
	'R':      true,
	'S':      true,
	'T':      true,
	'U':      true,
	'V':      true,
	'W':      true,
	'X':      true,
	'Y':      true,
	'Z':      true,
	'[':      true,
	'\\':     false,
	']':      true,
	'^':      true,
	'_':      true,
	'`':      true,
	'a':      true,
	'b':      true,
	'c':      true,
	'd':      true,
	'e':      true,
	'f':      true,
	'g':      true,
	'h':      true,
	'i':      true,
	'j':      true,
	'k':      true,
	'l':      true,
	'm':      true,
	'n':      true,
	'o':      true,
	'p':      true,
	'q':      true,
	'r':      true,
	's':      true,
	't':      true,
	'u':      true,
	'v':      true,
	'w':      true,
	'x':      true,
	'y':      true,
	'z':      true,
	'{':      true,
	'|':      true,
	'}':      true,
	'~':      true,
	'\u007f': true,
}
