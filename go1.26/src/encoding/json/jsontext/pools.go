// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"bytes"
	"io"
	"math/bits"
	"sync"
)

// TODO(https://go.dev/issue/47657): 使用 sync.PoolOf。

var (
	// 由于没有可输出的 io.Writer，它拥有内部缓冲区。
	// 由于在正常使用中缓冲区可能变得任意大，
	// 因此有一套统计跟踪逻辑，依据利用率历史来决定
	// 是否回收内部缓冲区。
	bufferedEncoderPool = &sync.Pool{New: func() any { return new(Encoder) }}

	// 它拥有内部缓冲区，但该缓冲区仅用于在将缓冲的 JSON
	// flush 到底层 io.Writer 之前临时存储它们。
	// 在足够高效的流式模式下，我们不期望缓冲区
	// 增长到任意大。因此，我们避免回收大缓冲区。
	streamingEncoderPool = &sync.Pool{New: func() any { return new(Encoder) }}

	// 它不拥有内部缓冲区，因为
	// 缓冲区直接取自所提供的 bytes.Buffer。
	bytesBufferEncoderPool = &sync.Pool{New: func() any { return new(Encoder) }}
)

// bufferStatistics 是用于跟踪缓冲区利用率的统计信息。
// 它用于决定是否回收缓冲区，
// 以避免 https://go.dev/issue/23199。
type bufferStatistics struct {
	strikes int // 缓冲区未被充分利用的次数
	prevLen int // 上一个缓冲区的长度
}

func getBufferedEncoder(opts ...Options) *Encoder {
	e := bufferedEncoderPool.Get().(*Encoder)
	if e.s.Buf == nil {
		// 向上取整到最接近的 2ⁿ，以充分利用 malloc 的大小类别。
		// 参见 Go1.15 中的 runtime/sizeclasses.go。
		// 与 63 做逻辑或，以确保 64 为最小缓冲区大小。
		n := 1 << bits.Len(uint(e.s.bufStats.prevLen|63))
		e.s.Buf = make([]byte, 0, n)
	}
	e.s.reset(e.s.Buf[:0], nil, opts...)
	return e
}
func putBufferedEncoder(e *Encoder) {
	if cap(e.s.availBuffer) > 64<<10 {
		e.s.availBuffer = nil // 避免固定住任意大的内存量
	}

	// 仅当利用率足够高时才回收大缓冲区。
	// 如果某个缓冲区连续多次未被充分利用，
	// 则将其丢弃，确保单个大缓冲区
	// 不会被持续不断的小规模使用所保活。
	//
	// 最坏情况下的利用率计算如下：
	//	MIN_UTILIZATION_THRESHOLD / (1 + MAX_NUM_STRIKES)
	//
	// 对于下面所选的常量，这是 (25%)/(1+4) ⇒ 5%。
	// 这看似很低，但它为绝对最坏情况下的利用率
	// 提供了一个下界。如果没有这项检查，
	// 理论上这会是 0%，那要糟糕得多。
	//
	// 参见 https://go.dev/issue/27735。
	switch {
	case cap(e.s.Buf) <= 4<<10: // 总是回收小于 4KiB 的缓冲区
		e.s.bufStats.strikes = 0
	case cap(e.s.Buf)/4 <= len(e.s.Buf): // 至少 25% 利用率
		e.s.bufStats.strikes = 0
	case e.s.bufStats.strikes < 4: // 至多 4 次未充分利用
		e.s.bufStats.strikes++
	default: // 丢弃缓冲区；太大且太频繁地未被充分利用
		e.s.bufStats.strikes = 0
		e.s.bufStats.prevLen = len(e.s.Buf) // 下次分配大小的启发式估计
		e.s.Buf = nil
	}
	bufferedEncoderPool.Put(e)
}

func getStreamingEncoder(w io.Writer, opts ...Options) *Encoder {
	if _, ok := w.(*bytes.Buffer); ok {
		e := bytesBufferEncoderPool.Get().(*Encoder)
		e.s.reset(nil, w, opts...) // 缓冲区取自 bytes.Buffer
		return e
	} else {
		e := streamingEncoderPool.Get().(*Encoder)
		e.s.reset(e.s.Buf[:0], w, opts...) // 保留现有缓冲区
		return e
	}
}
func putStreamingEncoder(e *Encoder) {
	if cap(e.s.availBuffer) > 64<<10 {
		e.s.availBuffer = nil // 避免固定住任意大的内存量
	}
	if _, ok := e.s.wr.(*bytes.Buffer); ok {
		e.s.wr, e.s.Buf = nil, nil // 避免固定住所提供的 bytes.Buffer
		bytesBufferEncoderPool.Put(e)
	} else {
		e.s.wr = nil // 避免固定住所提供的 io.Writer
		if cap(e.s.Buf) > 64<<10 {
			e.s.Buf = nil // 避免固定住任意大的内存量
		}
		streamingEncoderPool.Put(e)
	}
}

var (
	// 它不拥有内部缓冲区，因为缓冲区是由外部提供的。
	bufferedDecoderPool = &sync.Pool{New: func() any { return new(Decoder) }}

	// 它拥有内部缓冲区，但该缓冲区仅用于临时存储
	// 从底层 io.Reader 获取的缓冲 JSON。
	// 在足够高效的流式模式下，我们不期望缓冲区
	// 增长到任意大。因此，我们避免回收大缓冲区。
	streamingDecoderPool = &sync.Pool{New: func() any { return new(Decoder) }}

	// 它不拥有内部缓冲区，因为
	// 缓冲区直接取自所提供的 bytes.Buffer。
	bytesBufferDecoderPool = bufferedDecoderPool
)

func getBufferedDecoder(b []byte, opts ...Options) *Decoder {
	d := bufferedDecoderPool.Get().(*Decoder)
	d.s.reset(b, nil, opts...)
	return d
}
func putBufferedDecoder(d *Decoder) {
	d.s.buf = nil // 避免固定住所提供的缓冲区
	bufferedDecoderPool.Put(d)
}

func getStreamingDecoder(r io.Reader, opts ...Options) *Decoder {
	if _, ok := r.(*bytes.Buffer); ok {
		d := bytesBufferDecoderPool.Get().(*Decoder)
		d.s.reset(nil, r, opts...) // 缓冲区取自 bytes.Buffer
		return d
	} else {
		d := streamingDecoderPool.Get().(*Decoder)
		d.s.reset(d.s.buf[:0], r, opts...) // 保留现有缓冲区
		return d
	}
}
func putStreamingDecoder(d *Decoder) {
	if _, ok := d.s.rd.(*bytes.Buffer); ok {
		d.s.rd, d.s.buf = nil, nil // 避免固定住所提供的 bytes.Buffer
		bytesBufferDecoderPool.Put(d)
	} else {
		d.s.rd = nil // 避免固定住所提供的 io.Reader
		if cap(d.s.buf) > 64<<10 {
			d.s.buf = nil // 避免固定住任意大的内存量
		}
		streamingDecoderPool.Put(d)
	}
}
