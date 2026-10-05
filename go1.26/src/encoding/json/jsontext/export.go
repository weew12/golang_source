// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontext

import (
	"io"

	"encoding/json/internal"
)

// Internal 仅供内部使用。
// 它不受 Go 兼容性约定的约束。
var Internal exporter

type exporter struct{}

// Export 将 "jsontext" 的内部功能暴露给 "json"。
// 其他包无法动态调用它，因为
// 它们无法获得对 internal.AllowInternalUse 值的引用。
func (exporter) Export(p *internal.NotForPublicUse) export {
	if p != &internal.AllowInternalUse {
		panic("unauthorized call to Export")
	}
	return export{}
}

// export 类型将功能暴露给对
// internal.AllowInternalUse 变量可见的包。"json" 包使用它
// 来修改 Encoder 和 Decoder 类型中的底层状态。
// 它直接修改状态，而不是调用 ReadToken 或 WriteToken，
// 因为这样性能更高。公开的 API 需要跟踪状态以确保
// 用户构造出有效的 JSON 值，而 "json" 实现
// 通过代码本身的结构保证它会生成有效的 JSON。
type export struct{}

// Encoder 返回指向底层 encoderState 的指针。
func (export) Encoder(e *Encoder) *encoderState { return &e.s }

// Decoder 返回指向底层 decoderState 的指针。
func (export) Decoder(d *Decoder) *decoderState { return &d.s }

func (export) GetBufferedEncoder(o ...Options) *Encoder {
	return getBufferedEncoder(o...)
}
func (export) PutBufferedEncoder(e *Encoder) {
	putBufferedEncoder(e)
}

func (export) GetStreamingEncoder(w io.Writer, o ...Options) *Encoder {
	return getStreamingEncoder(w, o...)
}
func (export) PutStreamingEncoder(e *Encoder) {
	putStreamingEncoder(e)
}

func (export) GetBufferedDecoder(b []byte, o ...Options) *Decoder {
	return getBufferedDecoder(b, o...)
}
func (export) PutBufferedDecoder(d *Decoder) {
	putBufferedDecoder(d)
}

func (export) GetStreamingDecoder(r io.Reader, o ...Options) *Decoder {
	return getStreamingDecoder(r, o...)
}
func (export) PutStreamingDecoder(d *Decoder) {
	putStreamingDecoder(d)
}

func (export) IsIOError(err error) bool {
	_, ok := err.(*ioError)
	return ok
}
