// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bytes

// 用于数据编组的简易字节缓冲区。

import (
	"errors"
	"io"
	"unicode/utf8"
)

// smallBufferSize 是初始分配的最小容量。
const smallBufferSize = 64

// Buffer 是一个可变大小的字节缓冲区，提供 [Buffer.Read] 和 [Buffer.Write] 方法。
// Buffer 的零值是一个可直接使用的空缓冲区。
type Buffer struct {
	buf      []byte // 内容为字节 buf[off : len(buf)]
	off      int    // 从 &buf[off] 读取，向 &buf[len(buf)] 写入
	lastRead readOp // 上次读操作，用于 Unread* 方法正常工作

	// 复制并修改非零值的 Buffer 容易出错，
	// 但我们无法采用 WaitGroup 和 Mutex 所用的 noCopy 技巧，
	// 该技巧会触发 vet 的 copylocks 检查器报告误用，
	// 因为 vet 无法可靠区分零值和非零值场景。
	// 历史详情见 #26462、#25907、#47276、#48398。
}

// readOp 常量描述缓冲区上执行的最后一次操作，
// 以便 UnreadRune 和 UnreadByte 检查非法使用。
// opReadRuneX 常量的取值经过设计，转换为 int 后对应所读取符文的字节大小。
type readOp int8

// 此处不使用 iota，因为数值需要与名称和注释对应，显式声明更易读。
const (
	opRead      readOp = -1 // 其他任意读操作
	opInvalid   readOp = 0  // 非读操作
	opReadRune1 readOp = 1  // 读取大小为 1 字节的符文
	opReadRune2 readOp = 2  // 读取大小为 2 字节的符文
	opReadRune3 readOp = 3  // 读取大小为 3 字节的符文
	opReadRune4 readOp = 4  // 读取大小为 4 字节的符文
)

// 若无法分配内存存储缓冲区数据，会触发 panic 并传入 ErrTooLarge。
var ErrTooLarge = errors.New("bytes.Buffer: too large")
var errNegativeRead = errors.New("bytes.Buffer: reader returned negative count from Read")

const maxInt = int(^uint(0) >> 1)

// Bytes 返回一个长度为 b.Len() 的切片，包含缓冲区的未读部分。
// 该切片仅在下次缓冲区修改前有效（即仅在下次调用 [Buffer.Read]、[Buffer.Write]、
// [Buffer.Reset] 或 [Buffer.Truncate] 等方法前有效）。
// 至少在下次缓冲区修改前，该切片与缓冲区内容共用底层数组，
// 因此直接修改切片会影响后续读取结果。
func (b *Buffer) Bytes() []byte { return b.buf[b.off:] }

// AvailableBuffer 返回一个容量为 b.Available() 的空缓冲区。
// 该缓冲区用于追加数据，并传入紧随其后的 [Buffer.Write] 调用。
// 该切片仅在对 b 执行下次写操作前有效。
func (b *Buffer) AvailableBuffer() []byte { return b.buf[len(b.buf):] }

// String 将缓冲区未读部分的内容以字符串形式返回。
// 若 [Buffer] 为 nil 指针，返回 "<nil>"。
//
// 如需更高效地构建字符串，参见 [strings.Builder] 类型。
func (b *Buffer) String() string {
	if b == nil {
		// 特殊场景，便于调试
		return "<nil>"
	}
	return string(b.buf[b.off:])
}

// Peek 返回接下来的 n 个字节，且不移动缓冲区读取指针。
// 若 Peek 返回的字节数少于 n，会同时返回 [io.EOF]。
// 该切片仅在下次调用读或写方法前有效。
// 至少在下次缓冲区修改前，该切片与缓冲区内容共用底层数组，
// 因此直接修改切片会影响后续读取结果。
func (b *Buffer) Peek(n int) ([]byte, error) {
	if b.Len() < n {
		return b.buf[b.off:], io.EOF
	}
	return b.buf[b.off : b.off+n], nil
}

// empty 报告缓冲区的未读部分是否为空。
func (b *Buffer) empty() bool { return len(b.buf) <= b.off }

// Len 返回缓冲区未读部分的字节数；
// b.Len() == len(b.Bytes())。
func (b *Buffer) Len() int { return len(b.buf) - b.off }

// Cap 返回缓冲区底层字节切片的容量，即为缓冲区数据分配的总空间。
func (b *Buffer) Cap() int { return cap(b.buf) }

// Available 返回缓冲区中未使用的字节数。
func (b *Buffer) Available() int { return cap(b.buf) - len(b.buf) }

// Truncate 丢弃缓冲区中除前 n 个未读字节外的所有数据，
// 但继续使用相同的已分配存储空间。
// 若 n 为负数或大于缓冲区长度，会触发 panic。
func (b *Buffer) Truncate(n int) {
	if n == 0 {
		b.Reset()
		return
	}
	b.lastRead = opInvalid
	if n < 0 || n > b.Len() {
		panic("bytes.Buffer: truncation out of range")
	}
	b.buf = b.buf[:b.off+n]
}

// Reset 将缓冲区置空，
// 但保留底层存储空间供后续写入使用。
// Reset 等价于 [Buffer.Truncate]
func (b *Buffer) Reset() {
	b.buf = b.buf[:0]
	b.off = 0
	b.lastRead = opInvalid
}

// tryGrowByReslice 是 grow 的内联版本，适用于仅需重切片内部缓冲区的快速场景。
// 返回应写入字节的索引以及操作是否成功。
func (b *Buffer) tryGrowByReslice(n int) (int, bool) {
	if l := len(b.buf); n <= cap(b.buf)-l {
		b.buf = b.buf[:l+n]
		return l, true
	}
	return 0, false
}

// grow 扩容缓冲区，确保能容纳额外 n 个字节。
// 返回应写入字节的索引。
// 若缓冲区无法扩容，会触发 panic 并抛出 ErrTooLarge。
func (b *Buffer) grow(n int) int {
	m := b.Len()
	// 若缓冲区为空，重置以回收空间
	if m == 0 && b.off != 0 {
		b.Reset()
	}
	// 尝试通过重切片扩容
	if i, ok := b.tryGrowByReslice(n); ok {
		return i
	}
	if b.buf == nil && n <= smallBufferSize {
		b.buf = make([]byte, n, smallBufferSize)
		return 0
	}
	c := cap(b.buf)
	if n <= c/2-m {
		// 可前移数据而非分配新切片。
		// 仅需 m+n <= c 即可前移，但我们将容量翻倍，
		// 避免频繁拷贝数据。
		copy(b.buf, b.buf[b.off:])
	} else if c > maxInt-c-n {
		panic(ErrTooLarge)
	} else {
		// 加上 b.off 以补偿切片前端被截取的 b.buf[:b.off]
		b.buf = growSlice(b.buf[b.off:], b.off+n)
	}
	// 恢复 b.off 和 b.buf 的长度
	b.off = 0
	b.buf = b.buf[:m+n]
	return m
}

// Grow 按需扩容缓冲区容量，确保能容纳额外 n 个字节。
// 执行 Grow(n) 后，至少可向缓冲区写入 n 个字节而无需再次分配内存。
// 若 n 为负数，Grow 会触发 panic。
// 若缓冲区无法扩容，会触发 panic 并抛出 [ErrTooLarge]。
func (b *Buffer) Grow(n int) {
	if n < 0 {
		panic("bytes.Buffer.Grow: negative count")
	}
	m := b.grow(n)
	b.buf = b.buf[:m]
}

// Write 将 p 的内容追加到缓冲区，按需扩容。
// 返回值 n 为 p 的长度；err 始终为 nil。
// 若缓冲区过大，Write 会触发 panic 并抛出 [ErrTooLarge]。
func (b *Buffer) Write(p []byte) (n int, err error) {
	b.lastRead = opInvalid
	m, ok := b.tryGrowByReslice(len(p))
	if !ok {
		m = b.grow(len(p))
	}
	return copy(b.buf[m:], p), nil
}

// WriteString 将 s 的内容追加到缓冲区，按需扩容。
// 返回值 n 为 s 的长度；err 始终为 nil。
// 若缓冲区过大，WriteString 会触发 panic 并抛出 [ErrTooLarge]。
func (b *Buffer) WriteString(s string) (n int, err error) {
	b.lastRead = opInvalid
	m, ok := b.tryGrowByReslice(len(s))
	if !ok {
		m = b.grow(len(s))
	}
	return copy(b.buf[m:], s), nil
}

// MinRead 是 [Buffer.ReadFrom] 调用 [Buffer.Read] 时传入的最小切片大小。
// 只要缓冲区在容纳 r 的内容后仍有至少 MinRead 字节的剩余空间，
// [Buffer.ReadFrom] 就不会扩容底层缓冲区。
const MinRead = 512

// ReadFrom 从 r 读取数据直至 EOF，并将其追加到缓冲区，按需扩容。
// 返回值 n 为读取的字节数。读取过程中遇到的除 io.EOF 外的错误都会被返回。
// 若缓冲区过大，ReadFrom 会触发 panic 并抛出 [ErrTooLarge]。
func (b *Buffer) ReadFrom(r io.Reader) (n int64, err error) {
	b.lastRead = opInvalid
	for {
		i := b.grow(MinRead)
		b.buf = b.buf[:i]
		m, e := r.Read(b.buf[i:cap(b.buf)])
		if m < 0 {
			panic(errNegativeRead)
		}

		b.buf = b.buf[:i+m]
		n += int64(m)
		if e == io.EOF {
			return n, nil // e 为 EOF，显式返回 nil
		}
		if e != nil {
			return n, e
		}
	}
}

// growSlice 将 b 扩容 n 个字节，保留 b 的原始内容。
// 若分配失败，会触发 panic 并抛出 ErrTooLarge。
func growSlice(b []byte, n int) []byte {
	defer func() {
		if recover() != nil {
			panic(ErrTooLarge)
		}
	}()
	// TODO(http://golang.org/issue/51462): 应采用 append-make 写法，
	// 让编译器调用 runtime.growslice。例如：
	//	return append(b, make([]byte, n)...)
	// 该写法可避免对已分配切片前 len(b) 个字节的不必要清零，
	// 但会导致 b 逃逸到堆中。
	//
	// 改用 nil 切片的 append-make 写法，确保分配的缓冲区对齐至最近的规格大小。
	c := len(b) + n // 确保容纳 n 个元素的空间
	if c < 2*cap(b) {
		// 扩容倍率历来为 2 倍。未来可完全依赖 append 决定扩容倍率。
		c = 2 * cap(b)
	}
	b2 := append([]byte(nil), make([]byte, c)...)
	i := copy(b2, b)
	return b2[:i]
}

// WriteTo 向 w 写入数据直至缓冲区清空或发生错误。
// 返回值 n 为写入的字节数；其值始终可存入 int，
// 但采用 int64 以匹配 [io.WriterTo] 接口。写入过程中遇到的任何错误都会被返回。
func (b *Buffer) WriteTo(w io.Writer) (n int64, err error) {
	b.lastRead = opInvalid
	if nBytes := b.Len(); nBytes > 0 {
		m, e := w.Write(b.buf[b.off:])
		if m > nBytes {
			panic("bytes.Buffer.WriteTo: invalid Write count")
		}
		b.off += m
		n = int64(m)
		if e != nil {
			return n, e
		}
		// 根据 io.Writer 中 Write 方法的定义，所有字节都应被写入
		if m != nBytes {
			return n, io.ErrShortWrite
		}
	}
	// 缓冲区现已清空，执行重置
	b.Reset()
	return n, nil
}

// WriteByte 将字节 c 追加到缓冲区，按需扩容。
// 返回的错误始终为 nil，仅为匹配 [bufio.Writer] 的 WriteByte 方法。
// 若缓冲区过大，WriteByte 会触发 panic 并抛出 [ErrTooLarge]。
func (b *Buffer) WriteByte(c byte) error {
	b.lastRead = opInvalid
	m, ok := b.tryGrowByReslice(1)
	if !ok {
		m = b.grow(1)
	}
	b.buf[m] = c
	return nil
}

// WriteRune 将 Unicode 码点 r 的 UTF-8 编码追加到缓冲区，
// 返回其编码长度和错误（错误始终为 nil，仅为匹配 [bufio.Writer] 的 WriteRune 方法）。
// 缓冲区按需扩容；若缓冲区过大，WriteRune 会触发 panic 并抛出 [ErrTooLarge]。
func (b *Buffer) WriteRune(r rune) (n int, err error) {
	// 以 uint32 比较，正确处理负符文
	if uint32(r) < utf8.RuneSelf {
		b.WriteByte(byte(r))
		return 1, nil
	}
	b.lastRead = opInvalid
	m, ok := b.tryGrowByReslice(utf8.UTFMax)
	if !ok {
		m = b.grow(utf8.UTFMax)
	}
	b.buf = utf8.AppendRune(b.buf[:m], r)
	return len(b.buf) - m, nil
}

// Read 从缓冲区读取接下来 len(p) 个字节，或直至缓冲区清空。
// 返回值 n 为读取的字节数。若缓冲区无数据可返回，err 为 [io.EOF]（len(p) 为 0 时除外）；
// 其他情况 err 为 nil。
func (b *Buffer) Read(p []byte) (n int, err error) {
	b.lastRead = opInvalid
	if b.empty() {
		// 缓冲区为空，重置以回收空间
		b.Reset()
		if len(p) == 0 {
			return 0, nil
		}
		return 0, io.EOF
	}
	n = copy(p, b.buf[b.off:])
	b.off += n
	if n > 0 {
		b.lastRead = opRead
	}
	return n, nil
}

// Next 返回一个包含缓冲区接下来 n 个字节的切片，
// 移动缓冲区读取指针，效果等同于通过 [Buffer.Read] 返回这些字节。
// 若缓冲区字节数少于 n，Next 返回整个缓冲区。
// 该切片仅在下次调用读或写方法前有效。
func (b *Buffer) Next(n int) []byte {
	b.lastRead = opInvalid
	m := b.Len()
	if n > m {
		n = m
	}
	data := b.buf[b.off : b.off+n]
	b.off += n
	if n > 0 {
		b.lastRead = opRead
	}
	return data
}

// ReadByte 读取并返回缓冲区的下一个字节。
// 若无可用字节，返回错误 [io.EOF]。
func (b *Buffer) ReadByte() (byte, error) {
	if b.empty() {
		// 缓冲区为空，重置以回收空间
		b.Reset()
		return 0, io.EOF
	}
	c := b.buf[b.off]
	b.off++
	b.lastRead = opRead
	return c, nil
}

// ReadRune 读取并返回缓冲区中下一个 UTF-8 编码的 Unicode 码点。
// 若无可用字节，返回错误 io.EOF。
// 若字节为非法 UTF-8 编码，消耗 1 个字节并返回 U+FFFD 和 1。
func (b *Buffer) ReadRune() (r rune, size int, err error) {
	if b.empty() {
		// 缓冲区为空，重置以回收空间
		b.Reset()
		return 0, 0, io.EOF
	}
	c := b.buf[b.off]
	if c < utf8.RuneSelf {
		b.off++
		b.lastRead = opReadRune1
		return rune(c), 1, nil
	}
	r, n := utf8.DecodeRune(b.buf[b.off:])
	b.off += n
	b.lastRead = readOp(n)
	return r, n, nil
}

// UnreadRune 撤销上次 [Buffer.ReadRune] 返回的符文读取操作。
// 若缓冲区最近一次读写操作并非成功的 [Buffer.ReadRune]，UnreadRune 返回错误。
// （在这一点上，它比 [Buffer.UnreadByte] 更严格，后者可撤销任意读操作的最后一个字节）。
func (b *Buffer) UnreadRune() error {
	if b.lastRead <= opInvalid {
		return errors.New("bytes.Buffer: UnreadRune: previous operation was not a successful ReadRune")
	}
	if b.off >= int(b.lastRead) {
		b.off -= int(b.lastRead)
	}
	b.lastRead = opInvalid
	return nil
}

var errUnreadByte = errors.New("bytes.Buffer: UnreadByte: previous operation was not a successful read")

// UnreadByte 撤销最近一次成功读取至少一个字节的读操作，回退最后一个字节。
// 若上次读取后执行了写入操作、上次读取返回错误或读取字节数为 0，
// UnreadByte 返回错误。
func (b *Buffer) UnreadByte() error {
	if b.lastRead == opInvalid {
		return errUnreadByte
	}
	b.lastRead = opInvalid
	if b.off > 0 {
		b.off--
	}
	return nil
}

// ReadBytes 读取数据直至输入中首次出现分隔符 delim，
// 返回一个包含分隔符及之前所有数据的切片。
// 若 ReadBytes 在找到分隔符前遇到错误，
// 返回错误前读取的数据和该错误（通常为 [io.EOF]）。
// 当且仅当返回的数据不以分隔符结尾时，ReadBytes 返回的 err 不为 nil。
func (b *Buffer) ReadBytes(delim byte) (line []byte, err error) {
	slice, err := b.readSlice(delim)
	// 返回 slice 的副本。缓冲区的底层数组可能被后续调用覆盖
	line = append(line, slice...)
	return line, err
}

// readSlice 与 ReadBytes 功能一致，但返回内部缓冲区数据的引用。
func (b *Buffer) readSlice(delim byte) (line []byte, err error) {
	i := IndexByte(b.buf[b.off:], delim)
	end := b.off + i + 1
	if i < 0 {
		end = len(b.buf)
		err = io.EOF
	}
	line = b.buf[b.off:end]
	b.off = end
	b.lastRead = opRead
	return line, err
}

// ReadString 读取数据直至输入中首次出现分隔符 delim，
// 返回一个包含分隔符及之前所有数据的字符串。
// 若 ReadString 在找到分隔符前遇到错误，
// 返回错误前读取的数据和该错误（通常为 [io.EOF]）。
// 当且仅当返回的数据不以分隔符结尾时，ReadString 返回的 err 不为 nil。
func (b *Buffer) ReadString(delim byte) (line string, err error) {
	slice, err := b.readSlice(delim)
	return string(slice), err
}

// NewBuffer 使用 buf 作为初始内容创建并初始化一个新的 [Buffer]。
// 新的 [Buffer] 会接管 buf，调用方在此之后不应再使用 buf。
// NewBuffer 用于准备读取已有数据的 [Buffer]，也可用于设置写入用内部缓冲区的初始大小。
// 实现该需求时，buf 应具备所需容量但长度为 0。
//
// 大多数场景下，new([Buffer])（或仅声明 [Buffer] 变量）足以初始化 [Buffer]。
func NewBuffer(buf []byte) *Buffer { return &Buffer{buf: buf} }

// NewBufferString 使用字符串 s 作为初始内容创建并初始化一个新的 [Buffer]。
// 用于准备读取已有字符串的缓冲区。
//
// 大多数场景下，new([Buffer])（或仅声明 [Buffer] 变量）足以初始化 [Buffer]。
func NewBufferString(s string) *Buffer {
	return &Buffer{buf: []byte(s)}
}
