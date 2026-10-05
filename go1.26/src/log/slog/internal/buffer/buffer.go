// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package buffer 提供一个由池分配的字节缓冲区。
package buffer

import "sync"

// Buffer 是一个字节缓冲区。
//
// 此实现改编自 go/src/fmt/print.go 中未导出的类型 buffer。
type Buffer []byte

// 设置初始大小可以带来显著的提速。
var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 1024)
		return (*Buffer)(&b)
	},
}

func New() *Buffer {
	return bufPool.Get().(*Buffer)
}

func (b *Buffer) Free() {
	// 为减少峰值分配，只将较小的缓冲区归还到池中。
	const maxBufferSize = 16 << 10
	if cap(*b) <= maxBufferSize {
		*b = (*b)[:0]
		bufPool.Put(b)
	}
}

func (b *Buffer) Reset() {
	b.SetLen(0)
}

func (b *Buffer) Write(p []byte) (int, error) {
	*b = append(*b, p...)
	return len(p), nil
}

func (b *Buffer) WriteString(s string) (int, error) {
	*b = append(*b, s...)
	return len(s), nil
}

func (b *Buffer) WriteByte(c byte) error {
	*b = append(*b, c)
	return nil
}

func (b *Buffer) String() string {
	return string(*b)
}

func (b *Buffer) Len() int {
	return len(*b)
}

func (b *Buffer) SetLen(n int) {
	*b = (*b)[:n]
}
