// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// io 包提供I/O原语的基础接口。
// 其主要作用是将此类原语的现有实现（如os包中的实现）
// 封装为抽象功能的共享公共接口，并附带一些其他相关原语。
//
// 由于这些接口和原语封装了具有不同实现的底层操作，
// 除非另有说明，调用方不应假定它们可安全用于并行执行。
package io

import (
	"errors"
	"sync"
)

// Seek 起始基准值。
const (
	SeekStart   = 0 // 相对于文件起始位置进行偏移
	SeekCurrent = 1 // 相对于当前偏移位置进行偏移
	SeekEnd     = 2 // 相对于文件末尾进行偏移
)

// ErrShortWrite 表示写入操作接收的字节数少于请求数量，
// 但未返回显式错误。
var ErrShortWrite = errors.New("short write")

// errInvalidWrite 表示写入操作返回了不合理的字节计数。
var errInvalidWrite = errors.New("invalid write result")

// ErrShortBuffer 表示读取操作需要的缓冲区长度大于提供的长度。
var ErrShortBuffer = errors.New("short buffer")

// EOF 是Read方法在无更多输入数据时返回的错误。
// （Read必须直接返回EOF本身，而非包装EOF的错误，
// 因为调用方会通过==判断EOF。）
// 函数仅应在输入正常结束时返回EOF。
// 若在结构化数据流中意外遇到EOF，
// 应返回[ErrUnexpectedEOF]或其他能提供更多细节的错误。
var EOF = errors.New("EOF")

// ErrUnexpectedEOF 表示在读取固定大小的数据块或数据结构的过程中
// 遇到了EOF。
var ErrUnexpectedEOF = errors.New("unexpected EOF")

// ErrNoProgress 由部分[Reader]调用方返回，
// 当多次调用Read均未返回任何数据或错误时触发，
// 通常表明[Reader]的实现存在问题。
var ErrNoProgress = errors.New("multiple Read calls return no data or error")

// Reader 是封装基础Read方法的接口。
//
// Read 最多将len(p)个字节读入p中。它返回读取的字节数
// （0 <= n <= len(p)）以及遇到的任何错误。即使Read返回
// n < len(p)，调用过程中也可能将整个p用作临时空间。
// 若存在部分可用数据但不足len(p)个字节，Read通常会
// 返回当前可用数据，而非等待更多数据。
//
// 当Read在成功读取n > 0个字节后遇到错误或文件结束条件时，
// 会返回已读取的字节数。它可在本次调用中返回（非nil）错误，
// 或在后续调用中返回错误（且n == 0）。
// 此类常见场景的一个例子是：在输入流末尾返回非零字节数的Reader，
// 可能返回err == EOF或err == nil。下一次Read应返回0, EOF。
//
// 调用方应始终先处理返回的n > 0个字节，再考虑错误err。
// 这样能正确处理读取部分字节后发生的I/O错误，
// 以及两种允许的EOF行为。
//
// 若len(p) == 0，Read应始终返回n == 0。若已知存在错误条件（如EOF），
// 可返回非nil错误。
//
// 不鼓励Read的实现返回零字节计数且nil错误，len(p) == 0的情况除外。
// 调用方应将返回0和nil视为无任何操作发生；
// 尤其该情况不表示EOF。
//
// 实现不得持有p的引用。
type Reader interface {
	Read(p []byte) (n int, err error)
}

// Writer 是封装基础Write方法的接口。
//
// Write 从p中将len(p)个字节写入底层数据流。
// 它返回从p中写入的字节数（0 <= n <= len(p)）
// 以及导致写入提前终止的任何错误。
// 若Write返回n < len(p)，则必须返回非nil错误。
// Write不得修改切片数据，即使是临时修改也不允许。
//
// 实现不得持有p的引用。
type Writer interface {
	Write(p []byte) (n int, err error)
}

// Closer 是封装基础Close方法的接口。
//
// 首次调用Close后的行为未定义。
// 具体实现可自行文档化其行为。
type Closer interface {
	Close() error
}

// Seeker 是封装基础Seek方法的接口。
//
// Seek 将下一次Read或Write的偏移量设置为offset，
// 偏移规则由whence指定：
// [SeekStart] 表示相对于文件起始位置，
// [SeekCurrent] 表示相对于当前偏移位置，
// [SeekEnd] 表示相对于文件末尾
// （例如，offset = -2 指定文件的倒数第二个字节）。
// Seek 返回相对于文件起始位置的新偏移量，或发生的错误。
//
// 偏移至文件起始位置之前属于错误操作。
// 允许偏移至任意正偏移量，但若新偏移量超过
// 底层对象的大小，后续I/O操作的行为由具体实现决定。
type Seeker interface {
	Seek(offset int64, whence int) (int64, error)
}

// ReadWriter 是组合基础Read和Write方法的接口。
type ReadWriter interface {
	Reader
	Writer
}

// ReadCloser 是组合基础Read和Close方法的接口。
type ReadCloser interface {
	Reader
	Closer
}

// WriteCloser 是组合基础Write和Close方法的接口。
type WriteCloser interface {
	Writer
	Closer
}

// ReadWriteCloser 是组合基础Read、Write和Close方法的接口。
type ReadWriteCloser interface {
	Reader
	Writer
	Closer
}

// ReadSeeker 是组合基础Read和Seek方法的接口。
type ReadSeeker interface {
	Reader
	Seeker
}

// ReadSeekCloser 是组合基础Read、Seek和Close方法的接口。
type ReadSeekCloser interface {
	Reader
	Seeker
	Closer
}

// WriteSeeker 是组合基础Write和Seek方法的接口。
type WriteSeeker interface {
	Writer
	Seeker
}

// ReadWriteSeeker 是组合基础Read、Write和Seek方法的接口。
type ReadWriteSeeker interface {
	Reader
	Writer
	Seeker
}

// ReaderFrom 是封装ReadFrom方法的接口。
//
// ReadFrom 从r中读取数据直至EOF或发生错误。
// 返回值n为读取的字节数。
// 读取过程中遇到的除EOF外的任何错误也会一并返回。
//
// 若可用，[Copy]函数会使用[ReaderFrom]。
type ReaderFrom interface {
	ReadFrom(r Reader) (n int64, err error)
}

// WriterTo 是封装WriteTo方法的接口。
//
// WriteTo 向w中写入数据，直至无更多数据可写或发生错误。
// 返回值n为写入的字节数。
// 写入过程中遇到的任何错误也会一并返回。
//
// 若可用，Copy函数会使用WriterTo。
type WriterTo interface {
	WriteTo(w Writer) (n int64, err error)
}

// ReaderAt 是封装基础ReadAt方法的接口。
//
// ReadAt 从底层输入源的off偏移位置开始，
// 将len(p)个字节读入p中。它返回读取的字节数
// （0 <= n <= len(p)）以及遇到的任何错误。
//
// 当ReadAt返回n < len(p)时，会返回非nil错误以解释
// 未返回更多字节的原因。在这一点上，ReadAt比Read更严格。
//
// 即使ReadAt返回n < len(p)，调用过程中也可能将整个p用作临时空间。
// 若存在部分可用数据但不足len(p)个字节，ReadAt会阻塞
// 直至所有数据可用或发生错误。在这一点上，ReadAt与Read不同。
//
// 若ReadAt返回的n = len(p)个字节位于输入源末尾，
// ReadAt可返回err == EOF或err == nil。
//
// 若ReadAt从带有偏移量的输入源读取，
// ReadAt不应影响底层偏移量，也不受其影响。
//
// ReadAt的调用方可对同一输入源并行执行多个ReadAt调用。
//
// 实现不得持有p的引用。
type ReaderAt interface {
	ReadAt(p []byte, off int64) (n int, err error)
}

// WriterAt 是封装基础WriteAt方法的接口。
//
// WriteAt 从p中将len(p)个字节写入底层数据流的off偏移位置。
// 它返回从p中写入的字节数（0 <= n <= len(p)）
// 以及导致写入提前终止的任何错误。
// 若WriteAt返回n < len(p)，则必须返回非nil错误。
//
// 若WriteAt向带有偏移量的目标写入，
// WriteAt不应影响底层偏移量，也不受其影响。
//
// 若写入范围不重叠，WriteAt的调用方可对同一目标
// 并行执行多个WriteAt调用。
//
// 实现不得持有p的引用。
type WriterAt interface {
	WriteAt(p []byte, off int64) (n int, err error)
}

// ByteReader 是封装ReadByte方法的接口。
//
// ReadByte 从输入中读取并返回下一个字节，
// 或返回遇到的任何错误。若ReadByte返回错误，
// 则未消耗任何输入字节，返回的字节值未定义。
//
// ReadByte 为逐字节处理提供了高效接口。
// 未实现ByteReader的[Reader]可通过bufio.NewReader包装
// 以添加该方法。
type ByteReader interface {
	ReadByte() (byte, error)
}

// ByteScanner 是在基础ReadByte方法基础上
// 新增UnreadByte方法的接口。
//
// UnreadByte 会使下一次ReadByte调用返回最后读取的字节。
// 若上一次操作并非成功的ReadByte调用，UnreadByte可能
// 返回错误、回退最后读取的字节（或上一次回退字节的前一个字节），
// 或（在支持[Seeker]接口的实现中）将偏移量定位至当前位置的前一个字节。
type ByteScanner interface {
	ByteReader
	UnreadByte() error
}

// ByteWriter 是封装WriteByte方法的接口。
type ByteWriter interface {
	WriteByte(c byte) error
}

// RuneReader 是封装ReadRune方法的接口。
//
// ReadRune 读取单个编码的Unicode字符，
// 并返回该符文及其字节长度。若无可用字符，会设置err。
type RuneReader interface {
	ReadRune() (r rune, size int, err error)
}

// RuneScanner 是在基础ReadRune方法基础上
// 新增UnreadRune方法的接口。
//
// UnreadRune 会使下一次ReadRune调用返回最后读取的符文。
// 若上一次操作并非成功的ReadRune调用，UnreadRune可能
// 返回错误、回退最后读取的符文（或上一次回退符文的前一个符文），
// 或（在支持[Seeker]接口的实现中）将偏移量定位至当前符文的起始位置。
type RuneScanner interface {
	RuneReader
	UnreadRune() error
}

// StringWriter 是封装WriteString方法的接口。
type StringWriter interface {
	WriteString(s string) (n int, err error)
}

// WriteString 将字符串s的内容写入w，w为接收字节切片的写入器。
// 若w实现[StringWriter]，则直接调用[StringWriter.WriteString]。
// 否则，仅调用一次[Writer.Write]。
func WriteString(w Writer, s string) (n int, err error) {
	if sw, ok := w.(StringWriter); ok {
		return sw.WriteString(s)
	}
	return w.Write([]byte(s))
}

// ReadAtLeast 从r中读取数据至buf，直至至少读取min个字节。
// 它返回复制的字节数，若读取字节数不足则返回错误。
// 仅当未读取任何字节时，错误才为EOF。
// 若在读取不足min个字节后遇到EOF，
// ReadAtLeast返回[ErrUnexpectedEOF]。
// 若min大于buf的长度，ReadAtLeast返回[ErrShortBuffer]。
// 返回时，当且仅当err == nil时，n >= min。
// 若r在至少读取min个字节后返回错误，该错误会被忽略。
func ReadAtLeast(r Reader, buf []byte, min int) (n int, err error) {
	if len(buf) < min {
		return 0, ErrShortBuffer
	}
	for n < min && err == nil {
		var nn int
		nn, err = r.Read(buf[n:])
		n += nn
	}
	if n >= min {
		err = nil
	} else if n > 0 && err == EOF {
		err = ErrUnexpectedEOF
	}
	return
}

// ReadFull 从r中精确读取len(buf)个字节至buf。
// 它返回复制的字节数，若读取字节数不足则返回错误。
// 仅当未读取任何字节时，错误才为EOF。
// 若在读取部分但非全部字节后遇到EOF，
// ReadFull返回[ErrUnexpectedEOF]。
// 返回时，当且仅当err == nil时，n == len(buf)。
// 若r在至少读取len(buf)个字节后返回错误，该错误会被忽略。
func ReadFull(r Reader, buf []byte) (n int, err error) {
	return ReadAtLeast(r, buf, len(buf))
}

// CopyN 从src向dst复制n个字节（或直至发生错误）。
// 它返回复制的字节数，以及复制过程中遇到的首个错误。
// 返回时，当且仅当err == nil时，written == n。
//
// 若dst实现[ReaderFrom]，则通过该接口实现复制。
func CopyN(dst Writer, src Reader, n int64) (written int64, err error) {
	written, err = Copy(dst, LimitReader(src, n))
	if written == n {
		return n, nil
	}
	if written < n && err == nil {
		// src 提前终止；必然是EOF。
		err = EOF
	}
	return
}

// Copy 从src向dst复制数据，直至src到达EOF或发生错误。
// 它返回复制的字节数，以及复制过程中遇到的首个错误（如有）。
//
// 成功的Copy会返回err == nil，而非err == EOF。
// 由于Copy的定义是从src读取至EOF，
// 因此不会将Read返回的EOF视为需要上报的错误。
//
// 若src实现[WriterTo]，
// 则通过调用src.WriteTo(dst)实现复制。
// 否则，若dst实现[ReaderFrom]，
// 则通过调用dst.ReadFrom(src)实现复制。
func Copy(dst Writer, src Reader) (written int64, err error) {
	return copyBuffer(dst, src, nil)
}

// CopyBuffer 与Copy功能相同，区别在于它通过提供的缓冲区（若需要）
// 中转数据，而非分配临时缓冲区。若buf为nil，则自动分配缓冲区；
// 若buf长度为0，CopyBuffer会触发panic。
//
// 若src实现[WriterTo]或dst实现[ReaderFrom]，
// 则不会使用buf执行复制。
func CopyBuffer(dst Writer, src Reader, buf []byte) (written int64, err error) {
	if buf != nil && len(buf) == 0 {
		panic("empty buffer in CopyBuffer")
	}
	return copyBuffer(dst, src, buf)
}

// copyBuffer 是Copy和CopyBuffer的实际实现。
// 若buf为nil，则自动分配缓冲区。
func copyBuffer(dst Writer, src Reader, buf []byte) (written int64, err error) {
	// 若读取器实现了WriteTo方法，直接使用该方法完成复制。
	// 避免内存分配与数据拷贝。
	if wt, ok := src.(WriterTo); ok {
		return wt.WriteTo(dst)
	}
	// 同理，若写入器实现了ReadFrom方法，直接使用该方法完成复制。
	if rf, ok := dst.(ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	if buf == nil {
		size := 32 * 1024
		if l, ok := src.(*LimitedReader); ok && int64(size) > l.N {
			if l.N < 1 {
				size = 1
			} else {
				size = int(l.N)
			}
		}
		buf = make([]byte, size)
	}
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errInvalidWrite
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

// LimitReader 返回一个Reader，该读取器从r中读取数据，
// 但在读取n个字节后以EOF终止。
// 底层实现为*LimitedReader。
func LimitReader(r Reader, n int64) Reader { return &LimitedReader{r, n} }

// LimitedReader 从R中读取数据，但将返回的数据量限制为N个字节。
// 每次调用Read都会更新N以反映剩余的可读数据量。
// 当N <= 0或底层R返回EOF时，Read返回EOF。
type LimitedReader struct {
	R Reader // 底层读取器
	N int64  // 剩余最大可读字节数
}

func (l *LimitedReader) Read(p []byte) (n int, err error) {
	if l.N <= 0 {
		return 0, EOF
	}
	if int64(len(p)) > l.N {
		p = p[0:l.N]
	}
	n, err = l.R.Read(p)
	l.N -= int64(n)
	return
}

// NewSectionReader 返回一个[SectionReader]，
// 该读取器从r的off偏移位置开始读取，
// 读取n个字节后以EOF终止。
func NewSectionReader(r ReaderAt, off int64, n int64) *SectionReader {
	var remaining int64
	const maxint64 = 1<<63 - 1
	if off <= maxint64-n {
		remaining = n + off
	} else {
		// 溢出，无错误返回方式。
		// 假定可读取至偏移量1<<63 - 1。
		remaining = maxint64
	}
	return &SectionReader{r, off, off, remaining, n}
}

// SectionReader 在底层[ReaderAt]的指定片段上
// 实现Read、Seek和ReadAt方法。
type SectionReader struct {
	r     ReaderAt // 创建后保持不变
	base  int64    // 创建后保持不变
	off   int64
	limit int64 // 创建后保持不变
	n     int64 // 创建后保持不变
}

func (s *SectionReader) Read(p []byte) (n int, err error) {
	if s.off >= s.limit {
		return 0, EOF
	}
	if max := s.limit - s.off; int64(len(p)) > max {
		p = p[0:max]
	}
	n, err = s.r.ReadAt(p, s.off)
	s.off += int64(n)
	return
}

var errWhence = errors.New("Seek: invalid whence")
var errOffset = errors.New("Seek: invalid offset")

func (s *SectionReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	default:
		return 0, errWhence
	case SeekStart:
		offset += s.base
	case SeekCurrent:
		offset += s.off
	case SeekEnd:
		offset += s.limit
	}
	if offset < s.base {
		return 0, errOffset
	}
	s.off = offset
	return offset - s.base, nil
}

func (s *SectionReader) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 || off >= s.Size() {
		return 0, EOF
	}
	off += s.base
	if max := s.limit - off; int64(len(p)) > max {
		p = p[0:max]
		n, err = s.r.ReadAt(p, off)
		if err == nil {
			err = EOF
		}
		return n, err
	}
	return s.r.ReadAt(p, off)
}

// Size 返回该片段的字节大小。
func (s *SectionReader) Size() int64 { return s.limit - s.base }

// Outer 返回该片段对应的底层[ReaderAt]与偏移量。
//
// 返回值与创建[SectionReader]时
// 传入[NewSectionReader]的参数一致。
func (s *SectionReader) Outer() (r ReaderAt, off int64, n int64) {
	return s.r, s.base, s.n
}

// OffsetWriter 将在base偏移量处的写入操作，
// 映射到底层写入器的base+off偏移量处。
type OffsetWriter struct {
	w    WriterAt
	base int64 // 初始偏移量
	off  int64 // 当前偏移量
}

// NewOffsetWriter 返回一个[OffsetWriter]，
// 该写入器从off偏移位置开始向w中写入数据。
func NewOffsetWriter(w WriterAt, off int64) *OffsetWriter {
	return &OffsetWriter{w, off, off}
}

func (o *OffsetWriter) Write(p []byte) (n int, err error) {
	n, err = o.w.WriteAt(p, o.off)
	o.off += int64(n)
	return
}

func (o *OffsetWriter) WriteAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, errOffset
	}

	off += o.base
	return o.w.WriteAt(p, off)
}

func (o *OffsetWriter) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	default:
		return 0, errWhence
	case SeekStart:
		offset += o.base
	case SeekCurrent:
		offset += o.off
	}
	if offset < o.base {
		return 0, errOffset
	}
	o.off = offset
	return offset - o.base, nil
}

// TeeReader 返回一个[Reader]，该读取器会将从r中读取的内容
// 同时写入w。
// 通过该读取器从r执行的所有读取操作，
// 都会对应执行向w的写入操作。无内部缓冲——
// 写入必须在读取完成前执行完毕。
// 写入过程中遇到的任何错误都会作为读取错误上报。
func TeeReader(r Reader, w Writer) Reader {
	return &teeReader{r, w}
}

type teeReader struct {
	r Reader
	w Writer
}

func (t *teeReader) Read(p []byte) (n int, err error) {
	n, err = t.r.Read(p)
	if n > 0 {
		if n, err := t.w.Write(p[:n]); err != nil {
			return n, err
		}
	}
	return
}

// Discard 是一个[Writer]，所有对其的Write调用
// 均会成功执行且不做任何实际操作。
var Discard Writer = discard{}

type discard struct{}

// discard 实现ReaderFrom作为优化，
// 使向io.Discard的Copy可避免不必要的操作。
var _ ReaderFrom = discard{}

func (discard) Write(p []byte) (int, error) {
	return len(p), nil
}

func (discard) WriteString(s string) (int, error) {
	return len(s), nil
}

var blackHolePool = sync.Pool{
	New: func() any {
		b := make([]byte, 8192)
		return &b
	},
}

func (discard) ReadFrom(r Reader) (n int64, err error) {
	bufp := blackHolePool.Get().(*[]byte)
	readSize := 0
	for {
		readSize, err = r.Read(*bufp)
		n += int64(readSize)
		if err != nil {
			blackHolePool.Put(bufp)
			if err == EOF {
				return n, nil
			}
			return
		}
	}
}

// NopCloser 返回一个包装了提供的[Reader] r的[ReadCloser]，
// 其Close方法为空操作。
// 若r实现[WriterTo]，返回的[ReadCloser]会通过转发调用至r
// 来实现[WriterTo]接口。
func NopCloser(r Reader) ReadCloser {
	if _, ok := r.(WriterTo); ok {
		return nopCloserWriterTo{r}
	}
	return nopCloser{r}
}

type nopCloser struct {
	Reader
}

func (nopCloser) Close() error { return nil }

type nopCloserWriterTo struct {
	Reader
}

func (nopCloserWriterTo) Close() error { return nil }

func (c nopCloserWriterTo) WriteTo(w Writer) (n int64, err error) {
	return c.Reader.(WriterTo).WriteTo(w)
}

// ReadAll 从r中读取数据直至发生错误或EOF，
// 并返回读取到的数据。
// 成功调用会返回err == nil，而非err == EOF。
// 由于ReadAll的定义是从源读取至EOF，
// 因此不会将Read返回的EOF视为需要上报的错误。
func ReadAll(r Reader) ([]byte, error) {
	// 构建指数级增长大小的切片，
	// 最后复制至大小精确匹配的切片中。
	b := make([]byte, 0, 512)
	// 将next初始值设为256（而非512或1024）
	// 可减少小输入在早期增长阶段的内存占用，
	// 同时我们会快速增大读取尺寸，
	// 因此不会对中大型输入产生实质性影响。
	next := 256
	chunks := make([][]byte, 0, 4)
	// 不变量：finalSize = sum(len(c) for c in chunks)
	var finalSize int
	for {
		n, err := r.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
		if err != nil {
			if err == EOF {
				err = nil
			}
			if len(chunks) == 0 {
				return b, err
			}

			// 构建最终大小精确的切片。
			finalSize += len(b)
			final := append([]byte(nil), make([]byte, finalSize)...)[:0]
			for _, chunk := range chunks {
				final = append(final, chunk...)
			}
			final = append(final, b...)
			return final, err
		}

		if cap(b)-len(b) < cap(b)/16 {
			// 切换至下一个中间切片。
			chunks = append(chunks, b)
			finalSize += len(b)
			b = append([]byte(nil), make([]byte, next)...)[:0]
			next += next / 2
		}
	}
}
