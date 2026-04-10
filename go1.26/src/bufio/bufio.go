// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// bufio 包实现了带缓冲的 I/O 操作。它封装了 io.Reader 或 io.Writer
// 对象，创建出同样实现对应接口的 Reader 或 Writer 对象，
// 并提供缓冲功能与文本 I/O 的辅助支持。
package bufio

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	defaultBufSize = 4096
)

var (
	ErrInvalidUnreadByte = errors.New("bufio: invalid use of UnreadByte")
	ErrInvalidUnreadRune = errors.New("bufio: invalid use of UnreadRune")
	ErrBufferFull        = errors.New("bufio: buffer full")
	ErrNegativeCount     = errors.New("bufio: negative count")
)

// 带缓冲的输入操作

// Reader 为 io.Reader 对象实现缓冲读取功能。
// 可通过调用 NewReader 或 NewReaderSize 创建新的 Reader；
// 此外，Reader 的零值可在调用 Reset 方法后使用。
type Reader struct {
	buf          []byte
	rd           io.Reader // 客户端提供的底层读取器
	r, w         int       // 缓冲区的读取与写入位置
	err          error
	lastByte     int // 供 UnreadByte 使用的最后读取字节；-1 表示无效
	lastRuneSize int // 供 UnreadRune 使用的最后读取字符的字节大小；-1 表示无效
}

const minReadBufferSize = 16
const maxConsecutiveEmptyReads = 100

// NewReaderSize 返回一个缓冲区容量至少为指定大小的新 Reader。
// 若传入的 io.Reader 本身已是容量足够的 Reader，则直接返回该底层 Reader。
func NewReaderSize(rd io.Reader, size int) *Reader {
	// 是否已经是 Reader 类型？
	b, ok := rd.(*Reader)
	if ok && len(b.buf) >= size {
		return b
	}
	r := new(Reader)
	r.reset(make([]byte, max(size, minReadBufferSize)), rd)
	return r
}

// NewReader 返回缓冲区使用默认大小的新 Reader。
func NewReader(rd io.Reader) *Reader {
	return NewReaderSize(rd, defaultBufSize)
}

// Size 返回底层缓冲区的字节容量。
func (b *Reader) Size() int { return len(b.buf) }

// Reset 丢弃所有缓冲数据，重置所有状态，并将缓冲读取器切换为从 r 读取数据。
// 对 Reader 零值调用 Reset 会将内部缓冲区初始化为默认大小。
// 调用 b.Reset(b)（即重置 Reader 为自身）不会执行任何操作。
func (b *Reader) Reset(r io.Reader) {
	// 若 Reader r 被传入 NewReader，NewReader 会直接返回 r。
	// 多层代码可能执行此操作，后续再将 r 传入 Reset。
	// 此逻辑用于避免该场景下的无限递归。
	if b == r {
		return
	}
	if b.buf == nil {
		b.buf = make([]byte, defaultBufSize)
	}
	b.reset(b.buf, r)
}

func (b *Reader) reset(buf []byte, r io.Reader) {
	*b = Reader{
		buf:          buf,
		rd:           r,
		lastByte:     -1,
		lastRuneSize: -1,
	}
}

var errNegativeRead = errors.New("bufio: reader returned negative count from Read")

// fill 向缓冲区读取新的数据块。
func (b *Reader) fill() {
	// 将现有数据移至缓冲区开头
	if b.r > 0 {
		copy(b.buf, b.buf[b.r:b.w])
		b.w -= b.r
		b.r = 0
	}

	if b.w >= len(b.buf) {
		panic("bufio: tried to fill full buffer")
	}

	// 读取新数据：限制最大空读次数
	for i := maxConsecutiveEmptyReads; i > 0; i-- {
		n, err := b.rd.Read(b.buf[b.w:])
		if n < 0 {
			panic(errNegativeRead)
		}
		b.w += n
		if err != nil {
			b.err = err
			return
		}
		if n > 0 {
			return
		}
	}
	b.err = io.ErrNoProgress
}

func (b *Reader) readErr() error {
	err := b.err
	b.err = nil
	return err
}

// Peek 返回接下来的 n 个字节，且不移动读取指针。
// 这些字节在下一次读取操作后将失效。
// 必要时，Peek 会向缓冲区读取更多数据以满足 n 个字节的需求。
// 若 Peek 返回的字节数少于 n，会同时返回解释读取不足原因的错误。
// 若 n 大于缓冲区容量，错误为 ErrBufferFull。
//
// 调用 Peek 后，在下次读取操作前，UnreadByte 或 UnreadRune 调用会失败。
func (b *Reader) Peek(n int) ([]byte, error) {
	if n < 0 {
		return nil, ErrNegativeCount
	}

	b.lastByte = -1
	b.lastRuneSize = -1

	for b.w-b.r < n && b.w-b.r < len(b.buf) && b.err == nil {
		b.fill() // b.w-b.r < len(b.buf) 表示缓冲区未满
	}

	if n > len(b.buf) {
		return b.buf[b.r:b.w], ErrBufferFull
	}

	// 0 <= n <= 缓冲区容量
	var err error
	if avail := b.w - b.r; avail < n {
		// 缓冲区数据不足
		n = avail
		err = b.readErr()
		if err == nil {
			err = ErrBufferFull
		}
	}
	return b.buf[b.r : b.r+n], err
}

// Discard 跳过接下来的 n 个字节，返回实际丢弃的字节数。
//
// 若 Discard 跳过的字节数少于 n，会同时返回错误。
// 若 0 <= n <= 缓冲数据量，Discard 保证无需读取底层 io.Reader 即可成功执行。
func (b *Reader) Discard(n int) (discarded int, err error) {
	if n < 0 {
		return 0, ErrNegativeCount
	}
	if n == 0 {
		return
	}

	b.lastByte = -1
	b.lastRuneSize = -1

	remain := n
	for {
		skip := b.Buffered()
		if skip == 0 {
			b.fill()
			skip = b.Buffered()
		}
		if skip > remain {
			skip = remain
		}
		b.r += skip
		remain -= skip
		if remain == 0 {
			return n, nil
		}
		if b.err != nil {
			return n - remain, b.readErr()
		}
	}
}

// Read 将数据读取至 p 中。
// 返回读取到 p 中的字节数。
// 数据最多仅调用一次底层 Reader 的 Read 方法获取，
// 因此 n 可能小于 len(p)。
// 若需精确读取 len(p) 个字节，使用 io.ReadFull(b, p)。
// 若底层 Reader 可在返回 io.EOF 时同时返回非零字节数，
// 本 Read 方法也会遵循该行为；详见 io.Reader 文档。
func (b *Reader) Read(p []byte) (n int, err error) {
	n = len(p)
	if n == 0 {
		if b.Buffered() > 0 {
			return 0, nil
		}
		return 0, b.readErr()
	}
	if b.r == b.w {
		if b.err != nil {
			return 0, b.readErr()
		}
		if len(p) >= len(b.buf) {
			// 大数据量读取，缓冲区为空
			// 直接读取至 p 避免拷贝
			n, b.err = b.rd.Read(p)
			if n < 0 {
				panic(errNegativeRead)
			}
			if n > 0 {
				b.lastByte = int(p[n-1])
				b.lastRuneSize = -1
			}
			return n, b.readErr()
		}
		// 单次读取
		// 不使用 fill 方法，避免循环读取
		b.r = 0
		b.w = 0
		n, b.err = b.rd.Read(b.buf)
		if n < 0 {
			panic(errNegativeRead)
		}
		if n == 0 {
			return 0, b.readErr()
		}
		b.w += n
	}

	// 尽可能拷贝数据
	// 注：若此处切片触发 panic，大概率是底层读取器返回了非法字节数
	// 详见 issue 49795
	n = copy(p, b.buf[b.r:b.w])
	b.r += n
	b.lastByte = int(b.buf[b.r-1])
	b.lastRuneSize = -1
	return n, nil
}

// ReadByte 读取并返回单个字节。
// 若无可用字节，返回错误。
func (b *Reader) ReadByte() (byte, error) {
	b.lastRuneSize = -1
	for b.r == b.w {
		if b.err != nil {
			return 0, b.readErr()
		}
		b.fill() // 缓冲区为空
	}
	c := b.buf[b.r]
	b.r++
	b.lastByte = int(c)
	return c, nil
}

// UnreadByte 撤销上一次读取的字节，仅可撤销最近一次读取的字节。
//
// 若 Reader 上一次调用的方法不是读取操作，UnreadByte 会返回错误。
// 特别说明：Peek、Discard 和 WriteTo 不被视为读取操作。
func (b *Reader) UnreadByte() error {
	if b.lastByte < 0 || b.r == 0 && b.w > 0 {
		return ErrInvalidUnreadByte
	}
	// b.r > 0 || b.w == 0
	if b.r > 0 {
		b.r--
	} else {
		// b.r == 0 && b.w == 0
		b.w = 1
	}
	b.buf[b.r] = byte(b.lastByte)
	b.lastByte = -1
	b.lastRuneSize = -1
	return nil
}

// ReadRune 读取单个 UTF-8 编码的 Unicode 字符，
// 返回该字符及其占用的字节数。
// 若编码字符无效，会消耗 1 个字节并返回 unicode.ReplacementChar (U+FFFD)，大小为 1。
func (b *Reader) ReadRune() (r rune, size int, err error) {
	for b.r+utf8.UTFMax > b.w && !utf8.FullRune(b.buf[b.r:b.w]) && b.err == nil && b.w-b.r < len(b.buf) {
		b.fill() // b.w-b.r < len(buf) 表示缓冲区未满
	}
	b.lastRuneSize = -1
	if b.r == b.w {
		return 0, 0, b.readErr()
	}
	r, size = utf8.DecodeRune(b.buf[b.r:b.w])
	b.r += size
	b.lastByte = int(b.buf[b.r-1])
	b.lastRuneSize = size
	return r, size, nil
}

// UnreadRune 撤销上一次读取的字符。
// 若 Reader 上一次调用的方法不是 ReadRune，UnreadRune 会返回错误。
// （在这一点上它比 UnreadByte 更严格，UnreadByte 可撤销任意读取操作的最后字节）
func (b *Reader) UnreadRune() error {
	if b.lastRuneSize < 0 || b.r < b.lastRuneSize {
		return ErrInvalidUnreadRune
	}
	b.r -= b.lastRuneSize
	b.lastByte = -1
	b.lastRuneSize = -1
	return nil
}

// Buffered 返回当前缓冲区中可读取的字节数。
func (b *Reader) Buffered() int { return b.w - b.r }

// ReadSlice 读取数据直至输入中首次出现分隔符 delim，
// 返回指向缓冲区中字节的切片。
// 该切片数据在下一次读取操作后失效。
// 若 ReadSlice 在找到分隔符前遇到错误，
// 会返回缓冲区中所有数据与该错误（通常为 io.EOF）。
// 若缓冲区填满仍未找到分隔符，ReadSlice 会返回 ErrBufferFull 错误。
// 由于 ReadSlice 返回的数据会被下一次 I/O 操作覆盖，
// 大多数客户端应优先使用 ReadBytes 或 ReadString。
// 当且仅当返回的数据未以分隔符结尾时，ReadSlice 返回的 err 不为 nil。
func (b *Reader) ReadSlice(delim byte) (line []byte, err error) {
	s := 0 // 搜索起始索引
	for {
		// 搜索缓冲区
		if i := bytes.IndexByte(b.buf[b.r+s:b.w], delim); i >= 0 {
			i += s
			line = b.buf[b.r : b.r+i+1]
			b.r += i + 1
			break
		}

		// 是否存在待处理错误
		if b.err != nil {
			line = b.buf[b.r:b.w]
			b.r = b.w
			err = b.readErr()
			break
		}

		// 缓冲区是否已满
		if b.Buffered() >= len(b.buf) {
			b.r = b.w
			line = b.buf
			err = ErrBufferFull
			break
		}

		s = b.w - b.r // 不重复扫描已搜索区域

		b.fill() // 缓冲区未满
	}

	// 处理最后一个字节（若存在）
	if i := len(line) - 1; i >= 0 {
		b.lastByte = int(line[i])
		b.lastRuneSize = -1
	}

	return
}

// ReadLine 是底层的行读取原语。大多数调用者应优先使用
// ReadBytes('\n')、ReadString('\n') 或 Scanner。
//
// ReadLine 尝试返回单行数据，不包含行尾字节。
// 若行长度超出缓冲区容量，isPrefix 会被设为 true，仅返回行的开头部分，
// 剩余内容会在后续调用中返回。当返回行的最后片段时，isPrefix 为 false。
// 返回的缓冲区仅在下一次 ReadLine 调用前有效。
// ReadLine 要么返回非空行数据，要么返回错误，二者不会同时返回。
//
// ReadLine 返回的文本不包含行尾符（"\r\n" 或 "\n"）。
// 若输入末尾无最终行尾符，不会返回任何提示或错误。
// ReadLine 后调用 UnreadByte 总会撤销最后读取的字节
// （可能是行尾符），即使该字节不属于 ReadLine 返回的行数据。
func (b *Reader) ReadLine() (line []byte, isPrefix bool, err error) {
	line, err = b.ReadSlice('\n')
	if err == ErrBufferFull {
		// 处理 "\r\n" 跨缓冲区的情况
		if len(line) > 0 && line[len(line)-1] == '\r' {
			// 将 '\r' 放回缓冲区，从行数据中移除
			// 让下一次 ReadLine 调用检测 "\r\n"
			if b.r == 0 {
				// 理论上不可达
				panic("bufio: tried to rewind past start of buffer")
			}
			b.r--
			line = line[:len(line)-1]
		}
		return line, true, nil
	}

	if len(line) == 0 {
		if err != nil {
			line = nil
		}
		return
	}
	err = nil

	if line[len(line)-1] == '\n' {
		drop := 1
		if len(line) > 1 && line[len(line)-2] == '\r' {
			drop = 2
		}
		line = line[:len(line)-drop]
	}
	return
}

// collectFragments 读取数据直至输入中首次出现分隔符 delim。
// 返回值：(完整缓冲区切片, 分隔符前剩余字节, 前两部分总字节数, 错误)
// 完整结果等价于 bytes.Join(append(fullBuffers, finalFragment), nil)，
// 长度为 totalLen。该返回结构用于帮助调用者减少内存分配与数据拷贝。
func (b *Reader) collectFragments(delim byte) (fullBuffers [][]byte, finalFragment []byte, totalLen int, err error) {
	var frag []byte
	// 使用 ReadSlice 查找分隔符，累积完整缓冲区
	for {
		var e error
		frag, e = b.ReadSlice(delim)
		if e == nil { // 读取到最终片段
			break
		}
		if e != ErrBufferFull { // 非预期错误
			err = e
			break
		}

		// 拷贝缓冲区数据
		buf := bytes.Clone(frag)
		fullBuffers = append(fullBuffers, buf)
		totalLen += len(buf)
	}

	totalLen += len(frag)
	return fullBuffers, frag, totalLen, err
}

// ReadBytes 读取数据直至输入中首次出现分隔符 delim，
// 返回包含截止到分隔符（含）所有数据的切片。
// 若 ReadBytes 在找到分隔符前遇到错误，
// 会返回错误前已读取的数据与该错误（通常为 io.EOF）。
// 当且仅当返回的数据未以分隔符结尾时，ReadBytes 返回的 err 不为 nil。
// 简单场景下，使用 Scanner 会更便捷。
func (b *Reader) ReadBytes(delim byte) ([]byte, error) {
	full, frag, n, err := b.collectFragments(delim)
	// 分配新缓冲区存储完整数据块与最终片段
	buf := make([]byte, n)
	n = 0
	// 拷贝所有数据块与片段
	for i := range full {
		n += copy(buf[n:], full[i])
	}
	copy(buf[n:], frag)
	return buf, err
}

// ReadString 读取数据直至输入中首次出现分隔符 delim，
// 返回包含截止到分隔符（含）所有数据的字符串。
// 若 ReadString 在找到分隔符前遇到错误，
// 会返回错误前已读取的数据与该错误（通常为 io.EOF）。
// 当且仅当返回的数据未以分隔符结尾时，ReadString 返回的 err 不为 nil。
// 简单场景下，使用 Scanner 会更便捷。
func (b *Reader) ReadString(delim byte) (string, error) {
	full, frag, n, err := b.collectFragments(delim)
	// 分配缓冲区存储完整数据块与最终片段
	var buf strings.Builder
	buf.Grow(n)
	// 拷贝所有数据块与片段
	for _, fb := range full {
		buf.Write(fb)
	}
	buf.Write(frag)
	return buf.String(), err
}

// WriteTo 实现 io.WriterTo 接口。
// 该方法可能多次调用底层 Reader 的 Read 方法。
// 若底层读取器支持 WriteTo 方法，
// 本方法会直接调用底层 WriteTo，不使用缓冲。
func (b *Reader) WriteTo(w io.Writer) (n int64, err error) {
	b.lastByte = -1
	b.lastRuneSize = -1

	if b.r < b.w {
		n, err = b.writeBuf(w)
		if err != nil {
			return
		}
	}

	if r, ok := b.rd.(io.WriterTo); ok {
		m, err := r.WriteTo(w)
		n += m
		return n, err
	}

	if w, ok := w.(io.ReaderFrom); ok {
		m, err := w.ReadFrom(b.rd)
		n += m
		return n, err
	}

	if b.w-b.r < len(b.buf) {
		b.fill() // 缓冲区未满
	}

	for b.r < b.w {
		// b.r < b.w 表示缓冲区非空
		m, err := b.writeBuf(w)
		n += m
		if err != nil {
			return n, err
		}
		b.fill() // 缓冲区为空
	}

	if b.err == io.EOF {
		b.err = nil
	}

	return n, b.readErr()
}

var errNegativeWrite = errors.New("bufio: writer returned negative count from Write")

// writeBuf 将 Reader 的缓冲区数据写入写入器
func (b *Reader) writeBuf(w io.Writer) (int64, error) {
	n, err := w.Write(b.buf[b.r:b.w])
	if n < 0 {
		panic(errNegativeWrite)
	}
	b.r += n
	return int64(n), err
}

// 带缓冲的输出操作

// Writer 为 io.Writer 对象实现缓冲写入功能。
// 若写入 Writer 时发生错误，将不再接收任何数据，
// 所有后续写入操作与 Flush 操作都会返回该错误。
// 所有数据写入完成后，客户端应调用 Flush 方法，
// 确保所有数据都已提交至底层 io.Writer。
type Writer struct {
	err error
	buf []byte
	n   int
	wr  io.Writer
}

// NewWriterSize 返回一个缓冲区容量至少为指定大小的新 Writer。
// 若传入的 io.Writer 本身已是容量足够的 Writer，则直接返回该底层 Writer。
func NewWriterSize(w io.Writer, size int) *Writer {
	// 是否已经是 Writer 类型？
	b, ok := w.(*Writer)
	if ok && len(b.buf) >= size {
		return b
	}
	if size <= 0 {
		size = defaultBufSize
	}
	return &Writer{
		buf: make([]byte, size),
		wr:  w,
	}
}

// NewWriter 返回缓冲区使用默认大小的新 Writer。
// 若传入的 io.Writer 本身已是缓冲区足够大的 Writer，
// 则直接返回该底层 Writer。
func NewWriter(w io.Writer) *Writer {
	return NewWriterSize(w, defaultBufSize)
}

// Size 返回底层缓冲区的字节容量。
func (b *Writer) Size() int { return len(b.buf) }

// Reset 丢弃所有未刷新的缓冲数据，清除所有错误，
// 并重置 Writer 以向 w 写入数据。
// 对 Writer 零值调用 Reset 会将内部缓冲区初始化为默认大小。
// 调用 w.Reset(w)（即重置 Writer 为自身）不会执行任何操作。
func (b *Writer) Reset(w io.Writer) {
	// 若 Writer w 被传入 NewWriter，NewWriter 会直接返回 w。
	// 多层代码可能执行此操作，后续再将 w 传入 Reset。
	// 此逻辑用于避免该场景下的无限递归。
	if b == w {
		return
	}
	if b.buf == nil {
		b.buf = make([]byte, defaultBufSize)
	}
	b.err = nil
	b.n = 0
	b.wr = w
}

// Flush 将所有缓冲数据写入底层 io.Writer。
func (b *Writer) Flush() error {
	if b.err != nil {
		return b.err
	}
	if b.n == 0 {
		return nil
	}
	n, err := b.wr.Write(b.buf[0:b.n])
	if n < b.n && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		if n > 0 && n < b.n {
			copy(b.buf[0:b.n-n], b.buf[n:b.n])
		}
		b.n -= n
		b.err = err
		return err
	}
	b.n = 0
	return nil
}

// Available 返回缓冲区中未使用的字节数。
func (b *Writer) Available() int { return len(b.buf) - b.n }

// AvailableBuffer 返回一个容量为 b.Available() 的空缓冲区。
// 该缓冲区用于追加数据，并传入紧随其后的 Write 调用。
// 缓冲区仅在 b 下一次写入操作前有效。
func (b *Writer) AvailableBuffer() []byte {
	return b.buf[b.n:][:0]
}

// Buffered 返回当前缓冲区中已写入的字节数。
func (b *Writer) Buffered() int { return b.n }

// Write 将 p 中的内容写入缓冲区。
// 返回写入的字节数。
// 若 nn < len(p)，会同时返回解释写入不足原因的错误。
func (b *Writer) Write(p []byte) (nn int, err error) {
	for len(p) > b.Available() && b.err == nil {
		var n int
		if b.Buffered() == 0 {
			// 大数据量写入，缓冲区为空
			// 直接写入 p 避免拷贝
			n, b.err = b.wr.Write(p)
		} else {
			n = copy(b.buf[b.n:], p)
			b.n += n
			b.Flush()
		}
		nn += n
		p = p[n:]
	}
	if b.err != nil {
		return nn, b.err
	}
	n := copy(b.buf[b.n:], p)
	b.n += n
	nn += n
	return nn, nil
}

// WriteByte 写入单个字节。
func (b *Writer) WriteByte(c byte) error {
	if b.err != nil {
		return b.err
	}
	if b.Available() <= 0 && b.Flush() != nil {
		return b.err
	}
	b.buf[b.n] = c
	b.n++
	return nil
}

// WriteRune 写入单个 Unicode 码点，
// 返回写入的字节数与可能发生的错误。
func (b *Writer) WriteRune(r rune) (size int, err error) {
	// 以 uint32 比较，正确处理负 rune 值
	if uint32(r) < utf8.RuneSelf {
		err = b.WriteByte(byte(r))
		if err != nil {
			return 0, err
		}
		return 1, nil
	}
	if b.err != nil {
		return 0, b.err
	}
	n := b.Available()
	if n < utf8.UTFMax {
		if b.Flush(); b.err != nil {
			return 0, b.err
		}
		n = b.Available()
		if n < utf8.UTFMax {
			// 仅当缓冲区极小时会触发
			return b.WriteString(string(r))
		}
	}
	size = utf8.EncodeRune(b.buf[b.n:], r)
	b.n += size
	return size, nil
}

// WriteString 写入字符串。
// 返回写入的字节数。
// 若写入字节数小于 len(s)，会同时返回解释写入不足原因的错误。
func (b *Writer) WriteString(s string) (int, error) {
	var sw io.StringWriter
	tryStringWriter := true

	nn := 0
	for len(s) > b.Available() && b.err == nil {
		var n int
		if b.Buffered() == 0 && sw == nil && tryStringWriter {
			// 最多检查一次 b.wr 是否为 StringWriter
			sw, tryStringWriter = b.wr.(io.StringWriter)
		}
		if b.Buffered() == 0 && tryStringWriter {
			// 大数据量写入、缓冲区为空且底层写入器支持 WriteString
			// 直接转发写入至底层 StringWriter，避免额外拷贝
			n, b.err = sw.WriteString(s)
		} else {
			n = copy(b.buf[b.n:], s)
			b.n += n
			b.Flush()
		}
		nn += n
		s = s[n:]
	}
	if b.err != nil {
		return nn, b.err
	}
	n := copy(b.buf[b.n:], s)
	b.n += n
	nn += n
	return nn, nil
}

// ReadFrom 实现 io.ReaderFrom 接口。若底层写入器支持 ReadFrom 方法，
// 本方法会直接调用底层 ReadFrom。
// 若存在缓冲数据且底层支持 ReadFrom，本方法会先填满缓冲区并写入，
// 再调用 ReadFrom。
func (b *Writer) ReadFrom(r io.Reader) (n int64, err error) {
	if b.err != nil {
		return 0, b.err
	}
	readerFrom, readerFromOK := b.wr.(io.ReaderFrom)
	var m int
	for {
		if b.Available() == 0 {
			if err1 := b.Flush(); err1 != nil {
				return n, err1
			}
		}
		if readerFromOK && b.Buffered() == 0 {
			nn, err := readerFrom.ReadFrom(r)
			b.err = err
			n += nn
			return n, err
		}
		nr := 0
		for nr < maxConsecutiveEmptyReads {
			m, err = r.Read(b.buf[b.n:])
			if m != 0 || err != nil {
				break
			}
			nr++
		}
		if nr == maxConsecutiveEmptyReads {
			return n, io.ErrNoProgress
		}
		b.n += m
		n += int64(m)
		if err != nil {
			break
		}
	}
	if err == io.EOF {
		// 若刚好填满缓冲区，提前刷新
		if b.Available() == 0 {
			err = b.Flush()
		} else {
			err = nil
		}
	}
	return n, err
}

// 带缓冲的输入输出

// ReadWriter 存储 Reader 与 Writer 的指针，
// 实现 io.ReadWriter 接口。
type ReadWriter struct {
	*Reader
	*Writer
}

// NewReadWriter 分配一个新的 ReadWriter，读写操作分别转发至 r 和 w。
func NewReadWriter(r *Reader, w *Writer) *ReadWriter {
	return &ReadWriter{r, w}
}
