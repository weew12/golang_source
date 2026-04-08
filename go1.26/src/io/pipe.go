// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 管道适配器，用于将期望 io.Reader 的代码与期望 io.Writer 的代码连接起来。

package io

import (
	"errors"
	"sync"
)

// onceError 是一个仅会存储一次错误的对象。
type onceError struct {
	sync.Mutex // 保护后续字段
	err        error
}

func (a *onceError) Store(err error) {
	a.Lock()
	defer a.Unlock()
	if a.err != nil {
		return
	}
	a.err = err
}
func (a *onceError) Load() error {
	a.Lock()
	defer a.Unlock()
	return a.err
}

// ErrClosedPipe 是对已关闭管道执行读或写操作时返回的错误。
var ErrClosedPipe = errors.New("io: read/write on closed pipe")

// pipe 是 PipeReader 和 PipeWriter 底层共享的管道结构体。
type pipe struct {
	wrMu sync.Mutex // 序列化写入操作
	wrCh chan []byte
	rdCh chan int

	once sync.Mutex // 保护 done 通道的关闭操作
	done chan struct{}
	rerr onceError
	werr onceError
}

func (p *pipe) read(b []byte) (n int, err error) {
	select {
	case <-p.done:
		return 0, p.readCloseError()
	default:
	}

	select {
	case bw := <-p.wrCh:
		nr := copy(b, bw)
		p.rdCh <- nr
		return nr, nil
	case <-p.done:
		return 0, p.readCloseError()
	}
}

func (p *pipe) closeRead(err error) error {
	if err == nil {
		err = ErrClosedPipe
	}
	p.rerr.Store(err)
	p.once.Do(func() { close(p.done) })
	return nil
}

func (p *pipe) write(b []byte) (n int, err error) {
	select {
	case <-p.done:
		return 0, p.writeCloseError()
	default:
		p.wrMu.Lock()
		defer p.wrMu.Unlock()
	}

	for once := true; once || len(b) > 0; once = false {
		select {
		case p.wrCh <- b:
			nw := <-p.rdCh
			b = b[nw:]
			n += nw
		case <-p.done:
			return n, p.writeCloseError()
		}
	}
	return n, nil
}

func (p *pipe) closeWrite(err error) error {
	if err == nil {
		err = EOF
	}
	p.werr.Store(err)
	p.once.Do(func() { close(p.done) })
	return nil
}

// readCloseError 被视为 pipe 类型的内部方法。
func (p *pipe) readCloseError() error {
	rerr := p.rerr.Load()
	if werr := p.werr.Load(); rerr == nil && werr != nil {
		return werr
	}
	return ErrClosedPipe
}

// writeCloseError 被视为 pipe 类型的内部方法。
func (p *pipe) writeCloseError() error {
	werr := p.werr.Load()
	if rerr := p.rerr.Load(); werr == nil && rerr != nil {
		return rerr
	}
	return ErrClosedPipe
}

// PipeReader 是管道的读取端。
type PipeReader struct{ pipe }

// Read 实现标准的 Read 接口：
// 它从管道中读取数据，会阻塞直到写入方写入数据或写入端关闭。
// 若写入端因错误关闭，该错误会作为 err 返回；否则 err 为 EOF。
func (r *PipeReader) Read(data []byte) (n int, err error) {
	return r.pipe.read(data)
}

// Close 关闭读取端；后续对管道写入端的写入操作将返回 ErrClosedPipe 错误。
func (r *PipeReader) Close() error {
	return r.CloseWithError(nil)
}

// CloseWithError 关闭读取端；后续对管道写入端的写入操作将返回 err 错误。
//
// 若已存在错误，CloseWithError 不会覆盖原有错误，且始终返回 nil。
func (r *PipeReader) CloseWithError(err error) error {
	return r.pipe.closeRead(err)
}

// PipeWriter 是管道的写入端。
type PipeWriter struct{ r PipeReader }

// Write 实现标准的 Write 接口：
// 它向管道写入数据，会阻塞直到一个或多个读取方消费完所有数据，或读取端关闭。
// 若读取端因错误关闭，该错误会作为 err 返回；否则 err 为 ErrClosedPipe。
func (w *PipeWriter) Write(data []byte) (n int, err error) {
	return w.r.pipe.write(data)
}

// Close 关闭写入端；后续从管道读取端的读取操作将返回0字节数据及EOF。
func (w *PipeWriter) Close() error {
	return w.CloseWithError(nil)
}

// CloseWithError 关闭写入端；后续从管道读取端的读取操作将返回0字节数据及err错误，
// 若err为nil则返回EOF。
//
// 若已存在错误，CloseWithError 不会覆盖原有错误，且始终返回 nil。
func (w *PipeWriter) CloseWithError(err error) error {
	return w.r.pipe.closeWrite(err)
}

// Pipe 创建一个同步的内存管道。
// 它可用于将期望 io.Reader 的代码与期望 io.Writer 的代码连接起来。
//
// 管道上的读操作与写操作一一对应，除非需要多次读操作才能消费完一次写入的数据。
// 也就是说，对 PipeWriter 的每次写入都会阻塞，直到 PipeReader 的一次或多次读操作
// 完全消费完写入的数据。
// 数据从写入操作直接复制到对应的读操作（或多次读操作）中，无内部缓冲。
//
// 并行调用 Read、Write 或 Close 是安全的。
// 并行调用多次 Read、并行调用多次 Write 同样安全：
// 各个调用会被串行化执行。
func Pipe() (*PipeReader, *PipeWriter) {
	pw := &PipeWriter{r: PipeReader{pipe: pipe{
		wrCh: make(chan []byte),
		rdCh: make(chan int),
		done: make(chan struct{}),
	}}}
	return &pw.r, pw
}
