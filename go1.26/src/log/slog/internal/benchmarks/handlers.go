// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package benchmarks

// 用于基准测试的 Handler。

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"log/slog/internal/buffer"
	"strconv"
	"time"
)

// fastTextHandler 以类似于 slog.TextHandler 的格式将 Record 写入 io.Writer，
// 但不进行引号处理或加锁。它还有一些出于性能考虑的捷径，
// 例如将时间写为自 epoch 以来的秒数而不是字符串。
//
// 它旨在表示一种同步写入文本（而非二进制）的高性能 Handler。
type fastTextHandler struct {
	w io.Writer
}

func newFastTextHandler(w io.Writer) slog.Handler {
	return &fastTextHandler{w: w}
}

func (h *fastTextHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *fastTextHandler) Handle(_ context.Context, r slog.Record) error {
	buf := buffer.New()
	defer buf.Free()

	if !r.Time.IsZero() {
		buf.WriteString("time=")
		h.appendTime(buf, r.Time)
		buf.WriteByte(' ')
	}
	buf.WriteString("level=")
	*buf = strconv.AppendInt(*buf, int64(r.Level), 10)
	buf.WriteByte(' ')
	buf.WriteString("msg=")
	buf.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		buf.WriteByte(' ')
		buf.WriteString(a.Key)
		buf.WriteByte('=')
		h.appendValue(buf, a.Value)
		return true
	})
	buf.WriteByte('\n')
	_, err := h.w.Write(*buf)
	return err
}

func (h *fastTextHandler) appendValue(buf *buffer.Buffer, v slog.Value) {
	switch v.Kind() {
	case slog.KindString:
		buf.WriteString(v.String())
	case slog.KindInt64:
		*buf = strconv.AppendInt(*buf, v.Int64(), 10)
	case slog.KindUint64:
		*buf = strconv.AppendUint(*buf, v.Uint64(), 10)
	case slog.KindFloat64:
		*buf = strconv.AppendFloat(*buf, v.Float64(), 'g', -1, 64)
	case slog.KindBool:
		*buf = strconv.AppendBool(*buf, v.Bool())
	case slog.KindDuration:
		*buf = strconv.AppendInt(*buf, v.Duration().Nanoseconds(), 10)
	case slog.KindTime:
		h.appendTime(buf, v.Time())
	case slog.KindAny:
		a := v.Any()
		switch a := a.(type) {
		case error:
			buf.WriteString(a.Error())
		default:
			fmt.Fprint(buf, a)
		}
	default:
		panic(fmt.Sprintf("bad kind: %s", v.Kind()))
	}
}

func (h *fastTextHandler) appendTime(buf *buffer.Buffer, t time.Time) {
	*buf = strconv.AppendInt(*buf, t.Unix(), 10)
}

func (h *fastTextHandler) WithAttrs([]slog.Attr) slog.Handler {
	panic("fastTextHandler: With unimplemented")
}

func (*fastTextHandler) WithGroup(string) slog.Handler {
	panic("fastTextHandler: WithGroup unimplemented")
}

// asyncHandler 模拟一种将 Record 传递给后台 goroutine 进行处理的 Handler。
// 由于向 channel 发送数据可能因加锁而代价高昂，
// 我们通过将 Record 添加到环形缓冲区来模拟无锁队列。
// 省略加锁使其几乎只是 Record 的一次拷贝，
// 但这是值得测量的，因为 Record 偏大。
// 由于实际上没有任何东西从环形缓冲区读取，因此它可以处理任意数量的
// Record，既不会阻塞也不会分配内存。
type asyncHandler struct {
	ringBuffer [100]slog.Record
	next       int
}

func newAsyncHandler() *asyncHandler {
	return &asyncHandler{}
}

func (*asyncHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *asyncHandler) Handle(_ context.Context, r slog.Record) error {
	h.ringBuffer[h.next] = r.Clone()
	h.next = (h.next + 1) % len(h.ringBuffer)
	return nil
}

func (*asyncHandler) WithAttrs([]slog.Attr) slog.Handler {
	panic("asyncHandler: With unimplemented")
}

func (*asyncHandler) WithGroup(string) slog.Handler {
	panic("asyncHandler: WithGroup unimplemented")
}

// disabledHandler 的 Enabled 方法始终返回 false。
type disabledHandler struct{}

func (disabledHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (disabledHandler) Handle(context.Context, slog.Record) error { panic("should not be called") }

func (disabledHandler) WithAttrs([]slog.Attr) slog.Handler {
	panic("disabledHandler: With unimplemented")
}

func (disabledHandler) WithGroup(string) slog.Handler {
	panic("disabledHandler: WithGroup unimplemented")
}
