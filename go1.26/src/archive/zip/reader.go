// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zip

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"internal/godebug"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

var zipinsecurepath = godebug.New("zipinsecurepath")

var (
	ErrFormat       = errors.New("zip: not a valid zip file")
	ErrAlgorithm    = errors.New("zip: unsupported compression algorithm")
	ErrChecksum     = errors.New("zip: checksum error")
	ErrInsecurePath = errors.New("zip: insecure file path")
)

// Reader 从 ZIP 归档中提供内容。
type Reader struct {
	r             io.ReaderAt
	File          []*File
	Comment       string
	decompressors map[uint16]Decompressor

	// 某些 JAR 文件是带有 bash 脚本前缀的 zip 文件。
	// baseOffset 字段是 zip 文件本身的起始位置。
	baseOffset int64

	// fileList 是按 ename 排序的文件列表，
	// 供 Open 方法使用。
	fileListOnce sync.Once
	fileList     []fileListEntry
}

// ReadCloser 是一个 [Reader]，在不再需要时必须关闭。
type ReadCloser struct {
	f *os.File
	Reader
}

// File 是 ZIP 归档中的单个文件。
// 文件信息位于内嵌的 [FileHeader] 中。
// 可通过调用 [File.Open] 访问文件内容。
type File struct {
	FileHeader
	zip          *Reader
	zipr         io.ReaderAt
	headerOffset int64 // 包含整体 ZIP 归档的 baseOffset
	zip64        bool  // 是否存在 zip64 扩展信息额外字段
}

// OpenReader 将打开由 name 指定的 Zip 文件并返回一个 ReadCloser。
//
// 如果归档中的任何文件使用了非本地名称
// （如 [filepath.IsLocal] 所定义）或包含反斜杠的名称，
// 并且 GODEBUG 环境变量包含 `zipinsecurepath=0`，
// OpenReader 会返回带有 ErrInsecurePath 错误的 reader。
// Go 的未来版本可能会默认引入此行为。
// 希望接受非本地名称的程序可以忽略
// ErrInsecurePath 错误并使用返回的 reader。
func OpenReader(name string) (*ReadCloser, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	r := new(ReadCloser)
	if err = r.init(f, fi.Size()); err != nil && err != ErrInsecurePath {
		f.Close()
		return nil, err
	}
	r.f = f
	return r, err
}

// NewReader 返回一个新的 [Reader]，从 r 读取，假定其
// 具有给定的字节大小。
//
// 如果归档中的任何文件使用了非本地名称
// （如 [filepath.IsLocal] 所定义）或包含反斜杠的名称，
// 并且 GODEBUG 环境变量包含 `zipinsecurepath=0`，
// NewReader 会返回带有 [ErrInsecurePath] 错误的 reader。
// Go 的未来版本可能会默认引入此行为。
// 希望接受非本地名称的程序可以忽略
// [ErrInsecurePath] 错误并使用返回的 reader。
func NewReader(r io.ReaderAt, size int64) (*Reader, error) {
	if size < 0 {
		return nil, errors.New("zip: size cannot be negative")
	}
	zr := new(Reader)
	var err error
	if err = zr.init(r, size); err != nil && err != ErrInsecurePath {
		return nil, err
	}
	return zr, err
}

func (r *Reader) init(rdr io.ReaderAt, size int64) error {
	end, baseOffset, err := readDirectoryEnd(rdr, size)
	if err != nil {
		return err
	}
	r.r = rdr
	r.baseOffset = baseOffset
	// 由于目录记录数未经验证，在未首先检查指定的
	// 文件数是否合理之前，预分配 r.File 是不安全的，因为一个畸形的归档可能
	// 声称其包含多达 1 << 128 - 1 个文件。由于每个文件都有一个
	// _至少_ 30 字节的头部，因此若 (数据大小 / 30) >= end.directoryRecords，
	// 我们可以安全地预分配。
	if end.directorySize < uint64(size) && (uint64(size)-end.directorySize)/30 >= end.directoryRecords {
		r.File = make([]*File, 0, end.directoryRecords)
	}
	r.Comment = end.comment
	rs := io.NewSectionReader(rdr, 0, size)
	if _, err = rs.Seek(r.baseOffset+int64(end.directoryOffset), io.SeekStart); err != nil {
		return err
	}
	buf := bufio.NewReader(rs)

	// zip 内文件的数量被截断以适应 uint16。
	// 通过读取头部直到遇到一个坏的，
	// 然后仅在文件数模 65536 不正确时
	// 才报告 ErrFormat 或 UnexpectedEOF 来掩盖此问题。
	for {
		f := &File{zip: r, zipr: rdr}
		err = readDirectoryHeader(f, buf)
		if err == ErrFormat || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return err
		}
		f.headerOffset += r.baseOffset
		r.File = append(r.File, f)
	}
	if uint16(len(r.File)) != uint16(end.directoryRecords) { // 这里只比较 16 位
		// 如果我们读取的目录条目数错误，
		// 则返回 readDirectoryHeader 错误。
		return err
	}
	if zipinsecurepath.Value() == "0" {
		for _, f := range r.File {
			if f.Name == "" {
				// Zip 允许空的文件名字段。
				continue
			}
			// zip 规范规定名称必须使用正斜杠，
			// 因此将名称中的任何反斜杠视为不安全。
			if !filepath.IsLocal(f.Name) || strings.Contains(f.Name, `\`) {
				zipinsecurepath.IncNonDefault()
				return ErrInsecurePath
			}
		}
	}
	return nil
}

// RegisterDecompressor 为特定的方法 ID 注册或覆盖自定义解压器。
// 如果找不到给定方法的解压器，
// [Reader] 将默认在包级别查找解压器。
func (r *Reader) RegisterDecompressor(method uint16, dcomp Decompressor) {
	if r.decompressors == nil {
		r.decompressors = make(map[uint16]Decompressor)
	}
	r.decompressors[method] = dcomp
}

func (r *Reader) decompressor(method uint16) Decompressor {
	dcomp := r.decompressors[method]
	if dcomp == nil {
		dcomp = decompressor(method)
	}
	return dcomp
}

// Close 关闭 Zip 文件，使其无法用于 I/O。
func (rc *ReadCloser) Close() error {
	return rc.f.Close()
}

// DataOffset 返回文件的可能已压缩数据的偏移量，
// 相对于 zip 文件的开头。
//
// 大多数调用者应改用 [File.Open]，它会透明地
// 解压数据并校验校验和。
func (f *File) DataOffset() (offset int64, err error) {
	bodyOffset, err := f.findBodyOffset()
	if err != nil {
		return
	}
	return f.headerOffset + bodyOffset, nil
}

// Open 返回一个 [ReadCloser]，提供对 [File] 内容的访问。
// 可以并发读取多个文件。
func (f *File) Open() (io.ReadCloser, error) {
	bodyOffset, err := f.findBodyOffset()
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(f.Name, "/") {
		// ZIP 规范（APPNOTE.TXT）规定，目录在技术上属于零字节文件，
		// 不得有任何关联的文件数据。我们之前尝试在 f.CompressedSize64 != 0
		// 时失败，但事实证明，许多实现（即 Java
		// jar 工具）没有正确地在目录上设置存储方法，
		// 导致出现压缩大小 > 0 但未压缩大小 ==
		// 0 的文件。当目录有关联的未压缩数据时我们仍希望失败，
		// 但对于未压缩大小为零而压缩大小不为零的情况我们是宽容的。
		if f.UncompressedSize64 != 0 {
			return &dirReader{ErrFormat}, nil
		} else {
			return &dirReader{io.EOF}, nil
		}
	}
	size := int64(f.CompressedSize64)
	r := io.NewSectionReader(f.zipr, f.headerOffset+bodyOffset, size)
	dcomp := f.zip.decompressor(f.Method)
	if dcomp == nil {
		return nil, ErrAlgorithm
	}
	var rc io.ReadCloser = dcomp(r)
	var desr io.Reader
	if f.hasDataDescriptor() {
		desr = io.NewSectionReader(f.zipr, f.headerOffset+bodyOffset+size, dataDescriptorLen)
	}
	rc = &checksumReader{
		rc:   rc,
		hash: crc32.NewIEEE(),
		f:    f,
		desr: desr,
	}
	return rc, nil
}

// OpenRaw 返回一个 [Reader]，提供对 [File] 内容的访问而
// 不进行解压。
func (f *File) OpenRaw() (io.Reader, error) {
	bodyOffset, err := f.findBodyOffset()
	if err != nil {
		return nil, err
	}
	r := io.NewSectionReader(f.zipr, f.headerOffset+bodyOffset, int64(f.CompressedSize64))
	return r, nil
}

type dirReader struct {
	err error
}

func (r *dirReader) Read([]byte) (int, error) {
	return 0, r.err
}

func (r *dirReader) Close() error {
	return nil
}

type checksumReader struct {
	rc    io.ReadCloser
	hash  hash.Hash32
	nread uint64 // 到目前为止读取的字节数
	f     *File
	desr  io.Reader // 若非 nil，则是读取数据描述符的位置
	err   error     // 粘性错误
}

func (r *checksumReader) Stat() (fs.FileInfo, error) {
	return headerFileInfo{&r.f.FileHeader}, nil
}

func (r *checksumReader) Read(b []byte) (n int, err error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err = r.rc.Read(b)
	r.hash.Write(b[:n])
	r.nread += uint64(n)
	if r.nread > r.f.UncompressedSize64 {
		return 0, ErrFormat
	}
	if err == nil {
		return
	}
	if err == io.EOF {
		if r.nread != r.f.UncompressedSize64 {
			return 0, io.ErrUnexpectedEOF
		}
		if r.desr != nil {
			if err1 := readDataDescriptor(r.desr, r.f); err1 != nil {
				if err1 == io.EOF {
					err = io.ErrUnexpectedEOF
				} else {
					err = err1
				}
			} else if r.hash.Sum32() != r.f.CRC32 {
				err = ErrChecksum
			}
		} else {
			// 如果没有数据描述符，我们仍将已读取内容的
			// CRC32 与文件头或 TOC 的 CRC32 进行比较，
			// 前提是它看起来已被设置。
			if r.f.CRC32 != 0 && r.hash.Sum32() != r.f.CRC32 {
				err = ErrChecksum
			}
		}
	}
	r.err = err
	return
}

func (r *checksumReader) Close() error { return r.rc.Close() }

// findBodyOffset 做最少的工作来验证文件有头部，
// 并返回文件主体偏移量。
func (f *File) findBodyOffset() (int64, error) {
	var buf [fileHeaderLen]byte
	if _, err := f.zipr.ReadAt(buf[:], f.headerOffset); err != nil {
		return 0, err
	}
	b := readBuf(buf[:])
	if sig := b.uint32(); sig != fileHeaderSignature {
		return 0, ErrFormat
	}
	b = b[22:] // 跳过头部的大部分内容
	filenameLen := int(b.uint16())
	extraLen := int(b.uint16())
	return int64(fileHeaderLen + filenameLen + extraLen), nil
}

// readDirectoryHeader 尝试从 r 读取目录头。
// 如果无法读取完整的头部，它返回 io.ErrUnexpectedEOF，
// 如果找不到有效的头部签名，则返回 ErrFormat。
func readDirectoryHeader(f *File, r io.Reader) error {
	var buf [directoryHeaderLen]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return err
	}
	b := readBuf(buf[:])
	if sig := b.uint32(); sig != directoryHeaderSignature {
		return ErrFormat
	}
	f.CreatorVersion = b.uint16()
	f.ReaderVersion = b.uint16()
	f.Flags = b.uint16()
	f.Method = b.uint16()
	f.ModifiedTime = b.uint16()
	f.ModifiedDate = b.uint16()
	f.CRC32 = b.uint32()
	f.CompressedSize = b.uint32()
	f.UncompressedSize = b.uint32()
	f.CompressedSize64 = uint64(f.CompressedSize)
	f.UncompressedSize64 = uint64(f.UncompressedSize)
	filenameLen := int(b.uint16())
	extraLen := int(b.uint16())
	commentLen := int(b.uint16())
	b = b[4:] // 跳过了起始磁盘号和内部属性（2 个 uint16）
	f.ExternalAttrs = b.uint32()
	f.headerOffset = int64(b.uint32())
	d := make([]byte, filenameLen+extraLen+commentLen)
	if _, err := io.ReadFull(r, d); err != nil {
		return err
	}
	f.Name = string(d[:filenameLen])
	f.Extra = d[filenameLen : filenameLen+extraLen]
	f.Comment = string(d[filenameLen+extraLen:])

	// 确定字符编码。
	utf8Valid1, utf8Require1 := detectUTF8(f.Name)
	utf8Valid2, utf8Require2 := detectUTF8(f.Comment)
	switch {
	case !utf8Valid1 || !utf8Valid2:
		// Name 和 Comment 肯定不是 UTF-8。
		f.NonUTF8 = true
	case !utf8Require1 && !utf8Require2:
		// Name 和 Comment 仅使用与 UTF-8 重叠的单字节符文。
		f.NonUTF8 = false
	default:
		// 可能是 UTF-8，也可能是其他某种编码；保留现有标志。
		// 一些 ZIP 写入器使用 UTF-8 编码但未设置 UTF-8 标志。
		// 由于不可能总是区分有效的 UTF-8 与其他某种
		// 编码（例如 GBK 或 Shift-JIS），我们信任该标志。
		f.NonUTF8 = f.Flags&0x800 == 0
	}

	needUSize := f.UncompressedSize == ^uint32(0)
	needCSize := f.CompressedSize == ^uint32(0)
	needHeaderOffset := f.headerOffset == int64(^uint32(0))

	// 尽力查找我们需要的内容。
	// 其他 zip 作者甚至可能不遵循基本格式，
	// 在这种情况下我们将直接忽略 Extra 内容。
	var modified time.Time
parseExtras:
	for extra := readBuf(f.Extra); len(extra) >= 4; { // 至少需要 tag 和 size
		fieldTag := extra.uint16()
		fieldSize := int(extra.uint16())
		if len(extra) < fieldSize {
			break
		}
		fieldBuf := extra.sub(fieldSize)

		switch fieldTag {
		case zip64ExtraID:
			f.zip64 = true

			// 从 zip64 额外块更新目录值。
			// 只有在先前读取的大小达到最大值时
			// 才应参考它们。
			// 参见 golang.org/issue/13367。
			if needUSize {
				needUSize = false
				if len(fieldBuf) < 8 {
					return ErrFormat
				}
				f.UncompressedSize64 = fieldBuf.uint64()
			}
			if needCSize {
				needCSize = false
				if len(fieldBuf) < 8 {
					return ErrFormat
				}
				f.CompressedSize64 = fieldBuf.uint64()
			}
			if needHeaderOffset {
				needHeaderOffset = false
				if len(fieldBuf) < 8 {
					return ErrFormat
				}
				f.headerOffset = int64(fieldBuf.uint64())
			}
		case ntfsExtraID:
			if len(fieldBuf) < 4 {
				continue parseExtras
			}
			fieldBuf.uint32()        // 保留（忽略）
			for len(fieldBuf) >= 4 { // 至少需要 tag 和 size
				attrTag := fieldBuf.uint16()
				attrSize := int(fieldBuf.uint16())
				if len(fieldBuf) < attrSize {
					continue parseExtras
				}
				attrBuf := fieldBuf.sub(attrSize)
				if attrTag != 1 || attrSize != 24 {
					continue // 忽略不相关的属性
				}

				const ticksPerSecond = 1e7    // Windows 时间戳分辨率
				ts := int64(attrBuf.uint64()) // 自 Windows 纪元以来的 ModTime
				secs := ts / ticksPerSecond
				nsecs := (1e9 / ticksPerSecond) * (ts % ticksPerSecond)
				epoch := time.Date(1601, time.January, 1, 0, 0, 0, 0, time.UTC)
				modified = time.Unix(epoch.Unix()+secs, nsecs)
			}
		case unixExtraID, infoZipUnixExtraID:
			if len(fieldBuf) < 8 {
				continue parseExtras
			}
			fieldBuf.uint32()              // AcTime（忽略）
			ts := int64(fieldBuf.uint32()) // 自 Unix 纪元以来的 ModTime
			modified = time.Unix(ts, 0)
		case extTimeExtraID:
			if len(fieldBuf) < 5 || fieldBuf.uint8()&1 == 0 {
				continue parseExtras
			}
			ts := int64(fieldBuf.uint32()) // 自 Unix 纪元以来的 ModTime
			modified = time.Unix(ts, 0)
		}
	}

	msdosModified := msDosTimeToTime(f.ModifiedDate, f.ModifiedTime)
	f.Modified = msdosModified
	if !modified.IsZero() {
		f.Modified = modified.UTC()

		// 如果设置了旧式 MS-DOS 时间戳，我们可以使用
		// 旧式版本与扩展版本之间的差值来估计时区偏移。
		//
		// 始终使用非 UTC 时区（即使偏移为零）。
		// 因此，FileHeader.Modified.Location() == time.UTC 有助于
		// 判断是否存在扩展时间戳。
		// 对于处理旧式 ZIP 格式时需要做额外时间
		// 计算的用户来说，这是必要的。
		if f.ModifiedTime != 0 || f.ModifiedDate != 0 {
			f.Modified = modified.In(timeZone(msdosModified.Sub(modified)))
		}
	}

	// 假设未压缩大小 2³²-1 可能出现在一个旧的 zip32 文件中，
	// 该文件将输入分片为尽可能最大的块（或只是恶意；上网搜索 42.zip）。
	// 如果 needUSize 仍为 true，意味着我们没有看到 zip64 扩展。
	// 只要压缩大小也不为 2³²-1（不合理）
	// 且头部也不为 2³²-1（同样不合理），
	// 就接受未压缩大小 2³²-1 为有效。
	// 至少这能让 archive/zip 继续处理 42.zip。
	_ = needUSize

	if needCSize || needHeaderOffset {
		return ErrFormat
	}

	return nil
}

func readDataDescriptor(r io.Reader, f *File) error {
	var buf [dataDescriptorLen]byte
	// 规范说："虽然最初并未分配签名，值 0x08074b50
	// 已被普遍采用作为数据描述符记录的签名值。
	// 实现者应注意，可能会遇到带有或不带有该签名标记
	// 数据描述符的 ZIP 文件，在读取 ZIP 文件时
	// 应考虑这两种情况以确保兼容性。"
	//
	// dataDescriptorLen 包含签名的大小，但
	// 首先只读取这 4 个字节以查看其是否存在。
	if _, err := io.ReadFull(r, buf[:4]); err != nil {
		return err
	}
	off := 0
	maybeSig := readBuf(buf[:4])
	if maybeSig.uint32() != dataDescriptorSignature {
		// 没有数据描述符签名。保留这四
		// 个字节。
		off += 4
	}
	if _, err := io.ReadFull(r, buf[off:12]); err != nil {
		return err
	}
	b := readBuf(buf[:12])
	if b.uint32() != f.CRC32 {
		return ErrChecksum
	}

	// 这里随后的两个大小可以是 32 位或 64 位，
	// 但规范对此不是很明确，且已产生不同的
	// 解释，导致不兼容。我们已从中央目录
	// 获得了大小，因此可以
	// 直接忽略这些。

	return nil
}

func readDirectoryEnd(r io.ReaderAt, size int64) (dir *directoryEnd, baseOffset int64, err error) {
	// 在最后 1k 中查找 directoryEndSignature，然后在最后 65k 中查找
	var buf []byte
	var directoryEndOffset int64
	for i, bLen := range []int64{1024, 65 * 1024} {
		if bLen > size {
			bLen = size
		}
		buf = make([]byte, int(bLen))
		if _, err := r.ReadAt(buf, size-bLen); err != nil && err != io.EOF {
			return nil, 0, err
		}
		if p := findSignatureInBlock(buf); p >= 0 {
			buf = buf[p:]
			directoryEndOffset = size - bLen + int64(p)
			break
		}
		if i == 1 || bLen == size {
			return nil, 0, ErrFormat
		}
	}

	// 将头部读入结构体
	b := readBuf(buf[4:]) // 跳过签名
	d := &directoryEnd{
		diskNbr:            uint32(b.uint16()),
		dirDiskNbr:         uint32(b.uint16()),
		dirRecordsThisDisk: uint64(b.uint16()),
		directoryRecords:   uint64(b.uint16()),
		directorySize:      uint64(b.uint32()),
		directoryOffset:    uint64(b.uint32()),
		commentLen:         b.uint16(),
	}
	l := int(d.commentLen)
	if l > len(b) {
		return nil, 0, errors.New("zip: invalid comment length")
	}
	d.comment = string(b[:l])

	// 这些值意味着该文件可能是一个 zip64 文件
	if d.directoryRecords == 0xffff || d.directorySize == 0xffff || d.directoryOffset == 0xffffffff {
		p, err := findDirectory64End(r, directoryEndOffset)
		if err == nil && p >= 0 {
			directoryEndOffset = p
			err = readDirectory64End(r, p, d)
		}
		if err != nil {
			return nil, 0, err
		}
	}

	maxInt64 := uint64(1<<63 - 1)
	if d.directorySize > maxInt64 || d.directoryOffset > maxInt64 {
		return nil, 0, ErrFormat
	}

	baseOffset = directoryEndOffset - int64(d.directorySize) - int64(d.directoryOffset)

	// 确保 directoryOffset 指向我们文件中的某处。
	if o := baseOffset + int64(d.directoryOffset); o < 0 || o >= size {
		return nil, 0, ErrFormat
	}

	// 如果目录结束数据告诉我们要使用非零 baseOffset，
	// 但如果我们假设 baseOffset 为 0 就能找到有效的目录条目，
	// 那么就使用 0 作为 baseOffset。
	// 我们见过一些文件，其目录结束数据给出了
	// 不正确的 baseOffset。
	if baseOffset > 0 {
		off := int64(d.directoryOffset)
		rs := io.NewSectionReader(r, off, size-off)
		if readDirectoryHeader(&File{}, rs) == nil {
			baseOffset = 0
		}
	}

	return d, baseOffset, nil
}

// findDirectory64End 尝试读取目录结束之前的 zip64 定位器，
// 如果找到则返回 zip64 目录结束的偏移量。
func findDirectory64End(r io.ReaderAt, directoryEndOffset int64) (int64, error) {
	locOffset := directoryEndOffset - directory64LocLen
	if locOffset < 0 {
		return -1, nil // 无需在文件之外查找头部
	}
	buf := make([]byte, directory64LocLen)
	if _, err := r.ReadAt(buf, locOffset); err != nil {
		return -1, err
	}
	b := readBuf(buf)
	if sig := b.uint32(); sig != directory64LocSignature {
		return -1, nil
	}
	if b.uint32() != 0 { // 包含 zip64 中央目录结束记录起点的磁盘号
		return -1, nil // 该文件不是有效的 zip64 文件
	}
	p := b.uint64()      // zip64 中央目录结束记录的相对偏移量
	if b.uint32() != 1 { // 磁盘总数
		return -1, nil // 该文件不是有效的 zip64 文件
	}
	return int64(p), nil
}

// readDirectory64End 读取 zip64 目录结束，并用 zip64 目录结束的值
// 更新目录结束。
func readDirectory64End(r io.ReaderAt, offset int64, d *directoryEnd) (err error) {
	buf := make([]byte, directory64EndLen)
	if _, err := r.ReadAt(buf, offset); err != nil {
		return err
	}

	b := readBuf(buf)
	if sig := b.uint32(); sig != directory64EndSignature {
		return ErrFormat
	}

	b = b[12:]                        // 跳过目录大小、版本和所需版本（uint64 + 2 个 uint16）
	d.diskNbr = b.uint32()            // 此磁盘的编号
	d.dirDiskNbr = b.uint32()         // 包含中央目录起点的磁盘编号
	d.dirRecordsThisDisk = b.uint64() // 此磁盘上中央目录中的条目总数
	d.directoryRecords = b.uint64()   // 中央目录中的条目总数
	d.directorySize = b.uint64()      // 中央目录的大小
	d.directoryOffset = b.uint64()    // 中央目录起点相对于起始磁盘号的偏移量

	return nil
}

func findSignatureInBlock(b []byte) int {
	for i := len(b) - directoryEndLen; i >= 0; i-- {
		// 定义来自 struct.go 中的 directoryEndSignature
		if b[i] == 'P' && b[i+1] == 'K' && b[i+2] == 0x05 && b[i+3] == 0x06 {
			// n 是注释的长度
			n := int(b[i+directoryEndLen-2]) | int(b[i+directoryEndLen-1])<<8
			if n+directoryEndLen+i > len(b) {
				// 截断的注释。
				// 一些解析器（例如 Info-ZIP）会忽略截断的注释，
				// 而不是将其视为硬错误。
				return -1
			}
			return i
		}
	}
	return -1
}

type readBuf []byte

func (b *readBuf) uint8() uint8 {
	v := (*b)[0]
	*b = (*b)[1:]
	return v
}

func (b *readBuf) uint16() uint16 {
	v := binary.LittleEndian.Uint16(*b)
	*b = (*b)[2:]
	return v
}

func (b *readBuf) uint32() uint32 {
	v := binary.LittleEndian.Uint32(*b)
	*b = (*b)[4:]
	return v
}

func (b *readBuf) uint64() uint64 {
	v := binary.LittleEndian.Uint64(*b)
	*b = (*b)[8:]
	return v
}

func (b *readBuf) sub(n int) readBuf {
	b2 := (*b)[:n]
	*b = (*b)[n:]
	return b2
}

// fileListEntry 是一个 File 及其 ename。
// 如果 file == nil，则该 fileListEntry 描述一个没有元数据的目录。
type fileListEntry struct {
	name  string
	file  *File
	isDir bool
	isDup bool
}

type fileInfoDirEntry interface {
	fs.FileInfo
	fs.DirEntry
}

func (f *fileListEntry) stat() (fileInfoDirEntry, error) {
	if f.isDup {
		return nil, errors.New(f.name + ": duplicate entries in zip file")
	}
	if !f.isDir {
		return headerFileInfo{&f.file.FileHeader}, nil
	}
	return f, nil
}

// 仅用于目录。
func (f *fileListEntry) Name() string      { _, elem, _ := split(f.name); return elem }
func (f *fileListEntry) Size() int64       { return 0 }
func (f *fileListEntry) Mode() fs.FileMode { return fs.ModeDir | 0555 }
func (f *fileListEntry) Type() fs.FileMode { return fs.ModeDir }
func (f *fileListEntry) IsDir() bool       { return true }
func (f *fileListEntry) Sys() any          { return nil }

func (f *fileListEntry) ModTime() time.Time {
	if f.file == nil {
		return time.Time{}
	}
	return f.file.FileHeader.Modified.UTC()
}

func (f *fileListEntry) Info() (fs.FileInfo, error) { return f, nil }

func (f *fileListEntry) String() string {
	return fs.FormatDirEntry(f)
}

// toValidName 将 name 强制转换为 fs.FS.Open 的有效名称。
func toValidName(name string) string {
	name = strings.ReplaceAll(name, `\`, `/`)
	p := path.Clean(name)

	p = strings.TrimPrefix(p, "/")

	for strings.HasPrefix(p, "../") {
		p = p[len("../"):]
	}

	return p
}

func (r *Reader) initFileList() {
	r.fileListOnce.Do(func() {
		// 预分配索引的最小大小。
		// 我们还可能合成额外的目录条目。
		r.fileList = make([]fileListEntry, 0, len(r.File))
		// files 和 knownDirs 将文件/目录名映射到
		// 我们正在构建的 r.fileList 条目中的索引。
		// 它们用于标记重复条目。
		files := make(map[string]int)
		knownDirs := make(map[string]int)

		// 如果 name 已知是目录，则 dirs[name] 为 true，
		// 因为它作为路径中的前缀出现。
		dirs := make(map[string]bool)

		for _, file := range r.File {
			isDir := len(file.Name) > 0 && file.Name[len(file.Name)-1] == '/'
			name := toValidName(file.Name)
			if name == "" {
				continue
			}

			if idx, ok := files[name]; ok {
				r.fileList[idx].isDup = true
				continue
			}
			if idx, ok := knownDirs[name]; ok {
				r.fileList[idx].isDup = true
				continue
			}

			dir := name
			for {
				if idx := strings.LastIndex(dir, "/"); idx < 0 {
					break
				} else {
					dir = dir[:idx]
				}
				if dirs[dir] {
					break
				}
				dirs[dir] = true
			}

			idx := len(r.fileList)
			entry := fileListEntry{
				name:  name,
				file:  file,
				isDir: isDir,
			}
			r.fileList = append(r.fileList, entry)
			if isDir {
				knownDirs[name] = idx
			} else {
				files[name] = idx
			}
		}
		for dir := range dirs {
			if _, ok := knownDirs[dir]; !ok {
				if idx, ok := files[dir]; ok {
					r.fileList[idx].isDup = true
				} else {
					entry := fileListEntry{
						name:  dir,
						file:  nil,
						isDir: true,
					}
					r.fileList = append(r.fileList, entry)
				}
			}
		}

		slices.SortFunc(r.fileList, func(a, b fileListEntry) int {
			return fileEntryCompare(a.name, b.name)
		})
	})
}

func fileEntryCompare(x, y string) int {
	xdir, xelem, _ := split(x)
	ydir, yelem, _ := split(y)
	if xdir != ydir {
		return strings.Compare(xdir, ydir)
	}
	return strings.Compare(xelem, yelem)
}

// Open 使用 fs.FS.Open 的语义打开 ZIP 归档中
// 指定名称的文件：
// 路径始终以斜杠分隔，没有
// 开头的 / 或 ../ 元素。
func (r *Reader) Open(name string) (fs.File, error) {
	r.initFileList()

	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	e := r.openLookup(name)
	if e == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if e.isDir {
		return &openDir{e, r.openReadDir(name), 0}, nil
	}
	rc, err := e.file.Open()
	if err != nil {
		return nil, err
	}
	return rc.(fs.File), nil
}

func split(name string) (dir, elem string, isDir bool) {
	name, isDir = strings.CutSuffix(name, "/")
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return ".", name, isDir
	}
	return name[:i], name[i+1:], isDir
}

var dotFile = &fileListEntry{name: "./", isDir: true}

func (r *Reader) openLookup(name string) *fileListEntry {
	if name == "." {
		return dotFile
	}

	dir, elem, _ := split(name)
	files := r.fileList
	i, _ := slices.BinarySearchFunc(files, dir, func(a fileListEntry, dir string) (ret int) {
		idir, ielem, _ := split(a.name)
		if dir != idir {
			return strings.Compare(idir, dir)
		}
		return strings.Compare(ielem, elem)
	})
	if i < len(files) {
		fname := files[i].name
		if fname == name || len(fname) == len(name)+1 && fname[len(name)] == '/' && fname[:len(name)] == name {
			return &files[i]
		}
	}
	return nil
}

func (r *Reader) openReadDir(dir string) []fileListEntry {
	files := r.fileList
	i, _ := slices.BinarySearchFunc(files, dir, func(a fileListEntry, dir string) int {
		idir, _, _ := split(a.name)
		if dir != idir {
			return strings.Compare(idir, dir)
		}
		// 查找具有 dir 的第一个条目
		return +1
	})
	j, _ := slices.BinarySearchFunc(files, dir, func(a fileListEntry, dir string) int {
		jdir, _, _ := split(a.name)
		if dir != jdir {
			return strings.Compare(jdir, dir)
		}
		// 查找具有 dir 的最后一个条目
		return -1
	})
	return files[i:j]
}

type openDir struct {
	e      *fileListEntry
	files  []fileListEntry
	offset int
}

func (d *openDir) Close() error               { return nil }
func (d *openDir) Stat() (fs.FileInfo, error) { return d.e.stat() }

func (d *openDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.e.name, Err: errors.New("is a directory")}
}

func (d *openDir) ReadDir(count int) ([]fs.DirEntry, error) {
	n := len(d.files) - d.offset
	if count > 0 && n > count {
		n = count
	}
	if n == 0 {
		if count <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	list := make([]fs.DirEntry, n)
	for i := range list {
		s, err := d.files[d.offset+i].stat()
		if err != nil {
			return nil, err
		} else if s.Name() == "." || !fs.ValidPath(s.Name()) {
			return nil, &fs.PathError{
				Op:   "readdir",
				Path: d.e.name,
				Err:  fmt.Errorf("invalid file name: %v", d.files[d.offset+i].name),
			}
		}
		list[i] = s
	}
	d.offset += n
	return list, nil
}
