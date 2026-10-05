// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zip

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"strings"
	"unicode/utf8"
)

var (
	errLongName  = errors.New("zip: FileHeader.Name too long")
	errLongExtra = errors.New("zip: FileHeader.Extra too long")
)

// Writer 实现了一个 zip 文件写入器。
type Writer struct {
	cw          *countWriter
	dir         []*header
	last        *fileWriter
	closed      bool
	compressors map[uint16]Compressor
	comment     string

	// testHookCloseSizeOffset 若非 nil，则在 Close 时以中央目录的
	// 大小和偏移量调用。
	testHookCloseSizeOffset func(size, offset uint64)
}

type header struct {
	*FileHeader
	offset uint64
	raw    bool
}

// NewWriter 返回一个新的 [Writer]，将 zip 文件写入 w。
func NewWriter(w io.Writer) *Writer {
	return &Writer{cw: &countWriter{w: bufio.NewWriter(w)}}
}

// SetOffset 设置 zip 数据在底层写入器中起始位置的偏移量。
// 当 zip 数据被追加到已有文件（例如二进制可执行文件）时应当使用它。
// 它必须在写入任何数据之前调用。
func (w *Writer) SetOffset(n int64) {
	if w.cw.count != 0 {
		panic("zip: SetOffset called after data was written")
	}
	w.cw.count = n
}

// Flush 将所有缓冲数据刷新到底层写入器。
// 通常不需要调用 Flush；调用 Close 就足够了。
func (w *Writer) Flush() error {
	return w.cw.w.(*bufio.Writer).Flush()
}

// SetComment 设置中央目录结束记录的注释字段。
// 它只能在 [Writer.Close] 之前调用。
func (w *Writer) SetComment(comment string) error {
	if len(comment) > uint16max {
		return errors.New("zip: Writer.Comment too long")
	}
	w.comment = comment
	return nil
}

// Close 通过写入中央目录来完成 zip 文件的写入。
// 它不会关闭底层写入器。
func (w *Writer) Close() error {
	if w.last != nil && !w.last.closed {
		if err := w.last.close(); err != nil {
			return err
		}
		w.last = nil
	}
	if w.closed {
		return errors.New("zip: writer closed twice")
	}
	w.closed = true

	// 写入中央目录
	start := w.cw.count
	for _, h := range w.dir {
		var buf [directoryHeaderLen]byte
		b := writeBuf(buf[:])
		b.uint32(uint32(directoryHeaderSignature))
		b.uint16(h.CreatorVersion)
		b.uint16(h.ReaderVersion)
		b.uint16(h.Flags)
		b.uint16(h.Method)
		b.uint16(h.ModifiedTime)
		b.uint16(h.ModifiedDate)
		b.uint32(h.CRC32)
		if h.isZip64() || h.offset >= uint32max {
			// 该文件需要 zip64 头。在两个
			// 32 位大小字段（以及稍后的偏移量）中存储 maxint，以表明
			// 应使用 zip64 扩展头。
			b.uint32(uint32max) // 压缩后大小
			b.uint32(uint32max) // 未压缩大小

			// 向 Extra 追加一个 zip64 扩展块
			var buf [28]byte // 2x uint16 + 3x uint64
			eb := writeBuf(buf[:])
			eb.uint16(zip64ExtraID)
			eb.uint16(24) // 大小 = 3x uint64
			eb.uint64(h.UncompressedSize64)
			eb.uint64(h.CompressedSize64)
			eb.uint64(h.offset)
			h.Extra = append(h.Extra, buf[:]...)
		} else {
			b.uint32(h.CompressedSize)
			b.uint32(h.UncompressedSize)
		}

		b.uint16(uint16(len(h.Name)))
		b.uint16(uint16(len(h.Extra)))
		b.uint16(uint16(len(h.Comment)))
		b = b[4:] // 跳过磁盘编号起始和内部文件属性（2x uint16）
		b.uint32(h.ExternalAttrs)
		if h.offset > uint32max {
			b.uint32(uint32max)
		} else {
			b.uint32(uint32(h.offset))
		}
		if _, err := w.cw.Write(buf[:]); err != nil {
			return err
		}
		if _, err := io.WriteString(w.cw, h.Name); err != nil {
			return err
		}
		if _, err := w.cw.Write(h.Extra); err != nil {
			return err
		}
		if _, err := io.WriteString(w.cw, h.Comment); err != nil {
			return err
		}
	}
	end := w.cw.count

	records := uint64(len(w.dir))
	size := uint64(end - start)
	offset := uint64(start)

	if f := w.testHookCloseSizeOffset; f != nil {
		f(size, offset)
	}

	if records >= uint16max || size >= uint32max || offset >= uint32max {
		var buf [directory64EndLen + directory64LocLen]byte
		b := writeBuf(buf[:])

		// zip64 中央目录结束记录
		b.uint32(directory64EndSignature)
		b.uint64(directory64EndLen - 12) // 长度减去签名字段（uint32）和长度字段（uint64）
		b.uint16(zipVersion45)           // 创建版本
		b.uint16(zipVersion45)           // 解压所需版本
		b.uint32(0)                      // 此磁盘的编号
		b.uint32(0)                      // 中央目录起始所在磁盘的编号
		b.uint64(records)                // 此磁盘上中央目录中的条目总数
		b.uint64(records)                // 中央目录中的条目总数
		b.uint64(size)                   // 中央目录的大小
		b.uint64(offset)                 // 中央目录起始相对于起始磁盘编号的偏移量

		// zip64 中央目录结束定位器
		b.uint32(directory64LocSignature)
		b.uint32(0)           // zip64 中央目录结束所在磁盘的编号
		b.uint64(uint64(end)) // zip64 中央目录结束记录的相对偏移量
		b.uint32(1)           // 磁盘总数

		if _, err := w.cw.Write(buf[:]); err != nil {
			return err
		}

		// 在常规结束记录中存储最大值，以表明
		// 应改用 zip64 值
		records = uint16max
		size = uint32max
		offset = uint32max
	}

	// 写入结束记录
	var buf [directoryEndLen]byte
	b := writeBuf(buf[:])
	b.uint32(uint32(directoryEndSignature))
	b = b[4:]                        // 跳过磁盘编号和第一个磁盘编号（2x uint16）
	b.uint16(uint16(records))        // 此磁盘的条目数
	b.uint16(uint16(records))        // 条目总数
	b.uint32(uint32(size))           // 目录大小
	b.uint32(uint32(offset))         // 目录起始
	b.uint16(uint16(len(w.comment))) // EOCD 注释的字节大小
	if _, err := w.cw.Write(buf[:]); err != nil {
		return err
	}
	if _, err := io.WriteString(w.cw, w.comment); err != nil {
		return err
	}

	return w.cw.w.(*bufio.Writer).Flush()
}

// Create 使用提供的名称向 zip 文件添加一个文件。
// 它返回一个 [Writer]，应将文件内容写入该写入器。
// 文件内容将使用 [Deflate] 方法压缩。
// 名称必须是相对路径：它不能以盘符（例如 C:）或前导斜杠开头，
// 并且只允许使用正斜杠。要创建目录而不是文件，请在名称末尾
// 添加斜杠。重复的名称不会覆盖之前的条目，而是追加到 zip 文件中。
// 文件内容必须在下一次调用 [Writer.Create]、[Writer.CreateHeader] 或
// [Writer.Close] 之前写入该 [io.Writer]。
func (w *Writer) Create(name string) (io.Writer, error) {
	header := &FileHeader{
		Name:   name,
		Method: Deflate,
	}
	return w.CreateHeader(header)
}

// detectUTF8 报告 s 是否为有效的 UTF-8 字符串，以及该字符串
// 是否必须被视为 UTF-8 编码（即与 CP-437、ASCII
// 或任何其他常见编码均不兼容）。
func detectUTF8(s string) (valid, require bool) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		// 官方上，ZIP 使用 CP-437，但许多读取器使用系统的
		// 本地字符编码。大多数编码与 CP-437 的很大
		// 一部分兼容，而 CP-437 本身类似 ASCII。
		//
		// 禁止 0x7e 和 0x5c，因为 EUC-KR 和 Shift-JIS 会将这些
		// 字符替换为本地化的货币符号和上划线字符。
		if r < 0x20 || r > 0x7d || r == 0x5c {
			if !utf8.ValidRune(r) || (r == utf8.RuneError && size == 1) {
				return false, false
			}
			require = true
		}
	}
	return true, require
}

// prepare 执行 CreateHeader 和 CreateRaw 开头所需的簿记操作。
func (w *Writer) prepare(fh *FileHeader) error {
	if w.last != nil && !w.last.closed {
		if err := w.last.close(); err != nil {
			return err
		}
	}
	if len(w.dir) > 0 && w.dir[len(w.dir)-1].FileHeader == fh {
		// 参见 https://golang.org/issue/11144 以了解这一困惑。
		return errors.New("archive/zip: invalid duplicate FileHeader")
	}
	return nil
}

// CreateHeader 使用提供的 [FileHeader] 作为文件元数据向 zip 归档添加一个文件。
// [Writer] 取得 fh 的所有权，并可能修改其字段。
// 调用方在调用 [Writer.CreateHeader] 之后不得修改 fh。
//
// 这返回一个 [Writer]，应将文件内容写入该写入器。
// 文件内容必须在下一次调用 [Writer.Create]、[Writer.CreateHeader]、
// [Writer.CreateRaw] 或 [Writer.Close] 之前写入该 io.Writer。
func (w *Writer) CreateHeader(fh *FileHeader) (io.Writer, error) {
	if err := w.prepare(fh); err != nil {
		return nil, err
	}

	// ZIP 格式在字符编码方面的状况令人遗憾。
	// 官方上，除非设置了 UTF-8 标志位，名称和注释字段
	// 应以 CP-437 编码（它与 ASCII 基本兼容）。
	// 然而，存在几个问题：
	//
	//	* 许多 ZIP 读取器仍不支持 UTF-8。
	//	* 如果清除 UTF-8 标志，若干读取器会将名称和注释字段
	//	简单解释为本地系统编码。
	//
	// 为了避免破坏不支持 UTF-8 的读取器，
	// 如果字符串与 CP-437 兼容，我们避免设置 UTF-8 标志。
	// 然而，如果字符串需要多字节 UTF-8 编码并且是
	// 有效的 UTF-8 字符串，那么我们就设置 UTF-8 位。
	//
	// 对于用户明确希望将编码指定为 UTF-8 的情况，
	// 他们需要自行设置标志位。
	utf8Valid1, utf8Require1 := detectUTF8(fh.Name)
	utf8Valid2, utf8Require2 := detectUTF8(fh.Comment)
	switch {
	case fh.NonUTF8:
		fh.Flags &^= 0x800
	case (utf8Require1 || utf8Require2) && (utf8Valid1 && utf8Valid2):
		fh.Flags |= 0x800
	}

	fh.CreatorVersion = fh.CreatorVersion&0xff00 | zipVersion20 // 保留兼容性字节
	fh.ReaderVersion = zipVersion20

	// 如果设置了 Modified，它将优先于 MS-DOS 时间戳字段。
	if !fh.Modified.IsZero() {
		// 与 FileHeader.SetModTime 方法相反，我们有意
		// 不转换为 UTC，因为我们假设用户打算使用指定的
		// 时区来编码日期。用户可能希望拥有这种控制权，
		// 因为许多遗留 ZIP 读取器按本地时区解释时间戳。
		//
		// 只有当用户自己直接设置 Modified 字段时，
		// 时区才不是 UTC。所有其他方式都设置为 UTC。
		fh.ModifiedDate, fh.ModifiedTime = timeToMsDosTime(fh.Modified)

		// 使用 "extended timestamp" 格式，因为这是 Info-ZIP 所使用的。
		// 几乎每个主流 ZIP 实现都使用不同的格式，
		// 但至少大多数似乎能够理解其他格式。
		//
		// 如果只编码修改时间这一个时间戳，这种格式在本地头和
		// 中央头中恰好是相同的。
		var mbuf [9]byte // 2*SizeOf(uint16) + SizeOf(uint8) + SizeOf(uint32)
		mt := uint32(fh.Modified.Unix())
		eb := writeBuf(mbuf[:])
		eb.uint16(extTimeExtraID)
		eb.uint16(5)  // 大小：SizeOf(uint8) + SizeOf(uint32)
		eb.uint8(1)   // 标志：ModTime
		eb.uint32(mt) // ModTime
		fh.Extra = append(fh.Extra, mbuf[:]...)
	}

	var (
		ow io.Writer
		fw *fileWriter
	)
	h := &header{
		FileHeader: fh,
		offset:     uint64(w.cw.count),
	}

	if strings.HasSuffix(fh.Name, "/") {
		// 将压缩方法设置为 Store，以确保数据长度确实为零，
		// writeHeader 方法总是为大小字段编码这个值。
		// 这是必要的，因为大多数压缩格式即使在压缩空字符串时
		// 也有非零长度。
		fh.Method = Store
		fh.Flags &^= 0x8 // 我们将不写入数据描述符

		// 显式清除大小，因为它们对目录没有意义。
		fh.CompressedSize = 0
		fh.CompressedSize64 = 0
		fh.UncompressedSize = 0
		fh.UncompressedSize64 = 0

		ow = dirWriter{}
	} else {
		fh.Flags |= 0x8 // 我们将写入数据描述符

		fw = &fileWriter{
			zipw:      w.cw,
			compCount: &countWriter{w: w.cw},
			crc32:     crc32.NewIEEE(),
		}
		comp := w.compressor(fh.Method)
		if comp == nil {
			return nil, ErrAlgorithm
		}
		var err error
		fw.comp, err = comp(fw.compCount)
		if err != nil {
			return nil, err
		}
		fw.rawCount = &countWriter{w: fw.comp}
		fw.header = h
		ow = fw
	}
	w.dir = append(w.dir, h)
	if err := writeHeader(w.cw, h); err != nil {
		return nil, err
	}
	// 如果我们创建的是目录，则 fw 为 nil。
	w.last = fw
	return ow, nil
}

func writeHeader(w io.Writer, h *header) error {
	const maxUint16 = 1<<16 - 1
	if len(h.Name) > maxUint16 {
		return errLongName
	}
	if len(h.Extra) > maxUint16 {
		return errLongExtra
	}

	var buf [fileHeaderLen]byte
	b := writeBuf(buf[:])
	b.uint32(uint32(fileHeaderSignature))
	b.uint16(h.ReaderVersion)
	b.uint16(h.Flags)
	b.uint16(h.Method)
	b.uint16(h.ModifiedTime)
	b.uint16(h.ModifiedDate)
	// 在原始模式（调用方负责压缩）下，根据头部标志，
	// 这些值要么在此处写入，要么在尾随的数据描述符中写入。
	if h.raw && !h.hasDataDescriptor() {
		b.uint32(h.CRC32)
		b.uint32(uint32(min(h.CompressedSize64, uint32max)))
		b.uint32(uint32(min(h.UncompressedSize64, uint32max)))
	} else {
		// 当本包处理压缩时，这些值
		// 始终写入尾随的数据描述符中。
		b.uint32(0) // crc32
		b.uint32(0) // 压缩后大小
		b.uint32(0) // 未压缩大小
	}
	b.uint16(uint16(len(h.Name)))
	b.uint16(uint16(len(h.Extra)))
	if _, err := w.Write(buf[:]); err != nil {
		return err
	}
	if _, err := io.WriteString(w, h.Name); err != nil {
		return err
	}
	_, err := w.Write(h.Extra)
	return err
}

// CreateRaw 使用提供的 [FileHeader] 向 zip 归档添加一个文件，并
// 返回一个 [Writer]，应将文件内容写入该写入器。文件内容必须在
// 下一次调用 [Writer.Create]、[Writer.CreateHeader]、[Writer.CreateRaw]
// 或 [Writer.Close] 之前写入该 io.Writer。
//
// 与 [Writer.CreateHeader] 不同，传递给 Writer 的字节不会被压缩。
//
// CreateRaw 的参数存储在 w 中。如果该参数是指向从内存数据创建的
// [Reader] 所获取的 [File] 中内嵌 [FileHeader] 的指针，
// 那么 w 将引用该内存的全部内容。
func (w *Writer) CreateRaw(fh *FileHeader) (io.Writer, error) {
	if err := w.prepare(fh); err != nil {
		return nil, err
	}

	fh.CompressedSize = uint32(min(fh.CompressedSize64, uint32max))
	fh.UncompressedSize = uint32(min(fh.UncompressedSize64, uint32max))

	h := &header{
		FileHeader: fh,
		offset:     uint64(w.cw.count),
		raw:        true,
	}
	w.dir = append(w.dir, h)
	if err := writeHeader(w.cw, h); err != nil {
		return nil, err
	}

	if strings.HasSuffix(fh.Name, "/") {
		w.last = nil
		return dirWriter{}, nil
	}

	fw := &fileWriter{
		header: h,
		zipw:   w.cw,
	}
	w.last = fw
	return fw, nil
}

// Copy 将文件 f（从 [Reader] 获取）复制到 w 中。它直接复制原始
// 形式，绕过解压、压缩和校验。
func (w *Writer) Copy(f *File) error {
	r, err := f.OpenRaw()
	if err != nil {
		return err
	}
	// 复制 FileHeader，这样 w 就不会存储指向 f 的整个归档
	// 数据的指针。参见 #65499。
	fh := f.FileHeader
	fw, err := w.CreateRaw(&fh)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, r)
	return err
}

// RegisterCompressor 为特定的方法 ID 注册或覆盖自定义压缩器。
// 如果找不到给定方法的压缩器，[Writer] 将默认
// 在包级别查找压缩器。
func (w *Writer) RegisterCompressor(method uint16, comp Compressor) {
	if w.compressors == nil {
		w.compressors = make(map[uint16]Compressor)
	}
	w.compressors[method] = comp
}

// AddFS 将 fs.FS 中的文件添加到归档。
// 它从文件系统根目录开始遍历目录树，
// 使用 deflate 将每个文件添加到 zip，同时保持目录结构。
func (w *Writer) AddFS(fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !d.IsDir() && !info.Mode().IsRegular() {
			return errors.New("zip: cannot add non-regular file")
		}
		h, err := FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = name
		if d.IsDir() {
			h.Name += "/"
		}
		h.Method = Deflate
		fw, err := w.CreateHeader(h)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := fsys.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(fw, f)
		return err
	})
}

func (w *Writer) compressor(method uint16) Compressor {
	comp := w.compressors[method]
	if comp == nil {
		comp = compressor(method)
	}
	return comp
}

type dirWriter struct{}

func (dirWriter) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	return 0, errors.New("zip: write to directory")
}

type fileWriter struct {
	*header
	zipw      io.Writer
	rawCount  *countWriter
	comp      io.WriteCloser
	compCount *countWriter
	crc32     hash.Hash32
	closed    bool
}

func (w *fileWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("zip: write to closed file")
	}
	if w.raw {
		return w.zipw.Write(p)
	}
	w.crc32.Write(p)
	return w.rawCount.Write(p)
}

func (w *fileWriter) close() error {
	if w.closed {
		return errors.New("zip: file closed twice")
	}
	w.closed = true
	if w.raw {
		return w.writeDataDescriptor()
	}
	if err := w.comp.Close(); err != nil {
		return err
	}

	// 更新 FileHeader
	fh := w.header.FileHeader
	fh.CRC32 = w.crc32.Sum32()
	fh.CompressedSize64 = uint64(w.compCount.count)
	fh.UncompressedSize64 = uint64(w.rawCount.count)

	if fh.isZip64() {
		fh.CompressedSize = uint32max
		fh.UncompressedSize = uint32max
		fh.ReaderVersion = zipVersion45 // 需要 4.5 - File 使用 ZIP64 格式扩展
	} else {
		fh.CompressedSize = uint32(fh.CompressedSize64)
		fh.UncompressedSize = uint32(fh.UncompressedSize64)
	}

	return w.writeDataDescriptor()
}

func (w *fileWriter) writeDataDescriptor() error {
	if !w.hasDataDescriptor() {
		return nil
	}
	// 写入数据描述符。这比人们想象的要复杂，
	// 例如参见 zipfile.c:putextended() 中的注释和
	// https://bugs.openjdk.org/browse/JDK-7073588。
	// 这里的做法是在需要时写入 8 字节大小，而不在本地头中
	// 添加 zip64 扩展（反正也太晚了）。
	var buf []byte
	if w.isZip64() {
		buf = make([]byte, dataDescriptor64Len)
	} else {
		buf = make([]byte, dataDescriptorLen)
	}
	b := writeBuf(buf)
	b.uint32(dataDescriptorSignature) // 事实上标准，OS X 要求
	b.uint32(w.CRC32)
	if w.isZip64() {
		b.uint64(w.CompressedSize64)
		b.uint64(w.UncompressedSize64)
	} else {
		b.uint32(w.CompressedSize)
		b.uint32(w.UncompressedSize)
	}
	_, err := w.zipw.Write(buf)
	return err
}

type countWriter struct {
	w     io.Writer
	count int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.count += int64(n)
	return n, err
}

type nopCloser struct {
	io.Writer
}

func (w nopCloser) Close() error {
	return nil
}

type writeBuf []byte

func (b *writeBuf) uint8(v uint8) {
	(*b)[0] = v
	*b = (*b)[1:]
}

func (b *writeBuf) uint16(v uint16) {
	binary.LittleEndian.PutUint16(*b, v)
	*b = (*b)[2:]
}

func (b *writeBuf) uint32(v uint32) {
	binary.LittleEndian.PutUint32(*b, v)
	*b = (*b)[4:]
}

func (b *writeBuf) uint64(v uint64) {
	binary.LittleEndian.PutUint64(*b, v)
	*b = (*b)[8:]
}
