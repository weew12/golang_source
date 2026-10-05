// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tar

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"time"
)

// Writer 提供 tar 归档的顺序写入。
// [Writer.WriteHeader] 使用提供的 [Header] 开始一个新文件，
// 之后可将 Writer 视为 io.Writer 来提供该文件的数据。
type Writer struct {
	w    io.Writer
	pad  int64      // 当前文件条目之后要写入的填充量
	curr fileWriter // 当前文件条目的 Writer
	hdr  Header     // Header 的浅拷贝，可安全修改
	blk  block      // 用作临时本地存储的缓冲区

	// err 是一个持久性错误。
	// 只有 Writer 的每个导出方法有责任
	// 确保此错误是粘性的。
	err error
}

// NewWriter 创建一个写入 w 的新 Writer。
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w, curr: &regFileWriter{w, 0}}
}

type fileWriter interface {
	io.Writer
	fileState

	ReadFrom(io.Reader) (int64, error)
}

// Flush 完成当前文件块填充的写入。
// 调用 Flush 之前必须已完整写入当前文件。
//
// 这一步并非必需，因为下一次调用 [Writer.WriteHeader] 或 [Writer.Close]
// 会隐式刷出文件的填充。
func (tw *Writer) Flush() error {
	if tw.err != nil {
		return tw.err
	}
	if nb := tw.curr.logicalRemaining(); nb > 0 {
		return fmt.Errorf("archive/tar: missed writing %d bytes", nb)
	}
	if _, tw.err = tw.w.Write(zeroBlock[:tw.pad]); tw.err != nil {
		return tw.err
	}
	tw.pad = 0
	return nil
}

// WriteHeader 写入 hdr 并准备接收该文件的内容。
// Header.Size 决定下一个文件可以写入多少字节。
// 如果当前文件未完整写入，则返回错误。
// 它会在写入头部之前隐式刷出所有必要的填充。
func (tw *Writer) WriteHeader(hdr *Header) error {
	if err := tw.Flush(); err != nil {
		return err
	}
	tw.hdr = *hdr // Header 的浅拷贝

	// 避免使用遗留的 TypeRegA 标志，并自动将其
	// 提升为使用 TypeReg 或 TypeDir。
	if tw.hdr.Typeflag == TypeRegA {
		if strings.HasSuffix(tw.hdr.Name, "/") {
			tw.hdr.Typeflag = TypeDir
		} else {
			tw.hdr.Typeflag = TypeReg
		}
	}

	// 对 ModTime 取整，并忽略 AccessTime 和 ChangeTime，
	// 除非显式选择了格式。
	// 这确保 WriteHeader 的常规用法（不指定格式）
	// 不会总是导致选择 PAX 格式，
	// 因为那会使每个头部增加 1KiB。
	if tw.hdr.Format == FormatUnknown {
		tw.hdr.ModTime = tw.hdr.ModTime.Round(time.Second)
		tw.hdr.AccessTime = time.Time{}
		tw.hdr.ChangeTime = time.Time{}
	}

	allowedFormats, paxHdrs, err := tw.hdr.allowedFormats()
	switch {
	case allowedFormats.has(FormatUSTAR):
		tw.err = tw.writeUSTARHeader(&tw.hdr)
		return tw.err
	case allowedFormats.has(FormatPAX):
		tw.err = tw.writePAXHeader(&tw.hdr, paxHdrs)
		return tw.err
	case allowedFormats.has(FormatGNU):
		tw.err = tw.writeGNUHeader(&tw.hdr)
		return tw.err
	default:
		return err // 非致命错误
	}
}

func (tw *Writer) writeUSTARHeader(hdr *Header) error {
	// 检查是否可以使用 USTAR 前缀/后缀拆分。
	var namePrefix string
	if prefix, suffix, ok := splitUSTARPath(hdr.Name); ok {
		namePrefix, hdr.Name = prefix, suffix
	}

	// 打包主头部。
	var f formatter
	blk := tw.templateV7Plus(hdr, f.formatString, f.formatOctal)
	f.formatString(blk.toUSTAR().prefix(), namePrefix)
	blk.setFormat(FormatUSTAR)
	if f.err != nil {
		return f.err // 不应发生，因为头部已通过验证
	}
	return tw.writeRawHeader(blk, hdr.Size, hdr.Typeflag)
}

func (tw *Writer) writePAXHeader(hdr *Header, paxHdrs map[string]string) error {
	realName, realSize := hdr.Name, hdr.Size

	// TODO(dsnet): 在添加稀疏支持时重新启用此项。
	// 见 https://golang.org/issue/22735
	/*
		// 处理稀疏文件。
		var spd sparseDatas
		var spb []byte
		if len(hdr.SparseHoles) > 0 {
			sph := append([]sparseEntry{}, hdr.SparseHoles...) // 复制稀疏映射
			sph = alignSparseEntries(sph, hdr.Size)
			spd = invertSparseEntries(sph, hdr.Size)

			// 格式化稀疏映射。
			hdr.Size = 0 // 替换为编码后的大小
			spb = append(strconv.AppendInt(spb, int64(len(spd)), 10), '\n')
			for _, s := range spd {
				hdr.Size += s.Length
				spb = append(strconv.AppendInt(spb, s.Offset, 10), '\n')
				spb = append(strconv.AppendInt(spb, s.Length, 10), '\n')
			}
			pad := blockPadding(int64(len(spb)))
			spb = append(spb, zeroBlock[:pad]...)
			hdr.Size += int64(len(spb)) // 计入编码后的稀疏映射

			// 添加并修改相应的 PAX 记录。
			dir, file := path.Split(realName)
			hdr.Name = path.Join(dir, "GNUSparseFile.0", file)
			paxHdrs[paxGNUSparseMajor] = "1"
			paxHdrs[paxGNUSparseMinor] = "0"
			paxHdrs[paxGNUSparseName] = realName
			paxHdrs[paxGNUSparseRealSize] = strconv.FormatInt(realSize, 10)
			paxHdrs[paxSize] = strconv.FormatInt(hdr.Size, 10)
			delete(paxHdrs, paxPath) // 由 paxGNUSparseName 记录
		}
	*/
	_ = realSize

	// 将 PAX 记录写入输出。
	isGlobal := hdr.Typeflag == TypeXGlobalHeader
	if len(paxHdrs) > 0 || isGlobal {
		// 将每条记录写入缓冲区。
		var buf strings.Builder
		// 对键排序以保证确定性顺序。
		for _, k := range slices.Sorted(maps.Keys(paxHdrs)) {
			rec, err := formatPAXRecord(k, paxHdrs[k])
			if err != nil {
				return err
			}
			buf.WriteString(rec)
		}

		// 写入扩展头部文件。
		var name string
		var flag byte
		if isGlobal {
			name = realName
			if name == "" {
				name = "GlobalHead.0.0"
			}
			flag = TypeXGlobalHeader
		} else {
			dir, file := path.Split(realName)
			name = path.Join(dir, "PaxHeaders.0", file)
			flag = TypeXHeader
		}
		data := buf.String()
		if len(data) > maxSpecialFileSize {
			return ErrFieldTooLong
		}
		if err := tw.writeRawFile(name, data, flag, FormatPAX); err != nil || isGlobal {
			return err // 全局头在这里返回
		}
	}

	// 打包主头部。
	var f formatter // 忽略错误，因为它们是预期之内的
	fmtStr := func(b []byte, s string) { f.formatString(b, toASCII(s)) }
	blk := tw.templateV7Plus(hdr, fmtStr, f.formatOctal)
	blk.setFormat(FormatPAX)
	if err := tw.writeRawHeader(blk, hdr.Size, hdr.Typeflag); err != nil {
		return err
	}

	// TODO(dsnet): 在添加稀疏支持时重新启用此项。
	// 见 https://golang.org/issue/22735
	/*
		// 如有必要，写入稀疏映射并设置稀疏写入器。
		if len(spd) > 0 {
			// 使用 tw.curr，因为稀疏映射已计入 hdr.Size。
			if _, err := tw.curr.Write(spb); err != nil {
				return err
			}
			tw.curr = &sparseFileWriter{tw.curr, spd, 0}
		}
	*/
	return nil
}

func (tw *Writer) writeGNUHeader(hdr *Header) error {
	// 如果 Name 或 Linkname 超出字段大小，则使用长链接文件。
	const longName = "././@LongLink"
	if len(hdr.Name) > nameSize {
		data := hdr.Name + "\x00"
		if err := tw.writeRawFile(longName, data, TypeGNULongName, FormatGNU); err != nil {
			return err
		}
	}
	if len(hdr.Linkname) > nameSize {
		data := hdr.Linkname + "\x00"
		if err := tw.writeRawFile(longName, data, TypeGNULongLink, FormatGNU); err != nil {
			return err
		}
	}

	// 打包主头部。
	var f formatter // 忽略错误，因为它们是预期之内的
	var spd sparseDatas
	var spb []byte
	blk := tw.templateV7Plus(hdr, f.formatString, f.formatNumeric)
	if !hdr.AccessTime.IsZero() {
		f.formatNumeric(blk.toGNU().accessTime(), hdr.AccessTime.Unix())
	}
	if !hdr.ChangeTime.IsZero() {
		f.formatNumeric(blk.toGNU().changeTime(), hdr.ChangeTime.Unix())
	}
	// TODO(dsnet): 在添加稀疏支持时重新启用此项。
	// 见 https://golang.org/issue/22735
	/*
		if hdr.Typeflag == TypeGNUSparse {
			sph := append([]sparseEntry{}, hdr.SparseHoles...) // 复制稀疏映射
			sph = alignSparseEntries(sph, hdr.Size)
			spd = invertSparseEntries(sph, hdr.Size)

			// 格式化稀疏映射。
			formatSPD := func(sp sparseDatas, sa sparseArray) sparseDatas {
				for i := 0; len(sp) > 0 && i < sa.MaxEntries(); i++ {
					f.formatNumeric(sa.Entry(i).Offset(), sp[0].Offset)
					f.formatNumeric(sa.Entry(i).Length(), sp[0].Length)
					sp = sp[1:]
				}
				if len(sp) > 0 {
					sa.IsExtended()[0] = 1
				}
				return sp
			}
			sp2 := formatSPD(spd, blk.GNU().Sparse())
			for len(sp2) > 0 {
				var spHdr block
				sp2 = formatSPD(sp2, spHdr.Sparse())
				spb = append(spb, spHdr[:]...)
			}

			// 更新头部块中的大小字段。
			realSize := hdr.Size
			hdr.Size = 0 // 编码后的大小；不计入编码后的稀疏映射
			for _, s := range spd {
				hdr.Size += s.Length
			}
			copy(blk.V7().Size(), zeroBlock[:]) // 重置字段
			f.formatNumeric(blk.V7().Size(), hdr.Size)
			f.formatNumeric(blk.GNU().RealSize(), realSize)
		}
	*/
	blk.setFormat(FormatGNU)
	if err := tw.writeRawHeader(blk, hdr.Size, hdr.Typeflag); err != nil {
		return err
	}

	// 如有必要，写入扩展稀疏映射并设置稀疏写入器。
	if len(spd) > 0 {
		// 使用 tw.w，因为稀疏映射未计入 hdr.Size。
		if _, err := tw.w.Write(spb); err != nil {
			return err
		}
		tw.curr = &sparseFileWriter{tw.curr, spd, 0}
	}
	return nil
}

type (
	stringFormatter func([]byte, string)
	numberFormatter func([]byte, int64)
)

// templateV7Plus 使用 hdr 中的值填充块的 V7 字段。
// 它还使用提供的格式化器填充在 USTAR、PAX 和 GNU 格式中
// 共享的字段（uname、gname、devmajor、devminor）。
//
// 返回的块仅在下次调用
// templateV7Plus 或 writeRawFile 之前有效。
func (tw *Writer) templateV7Plus(hdr *Header, fmtStr stringFormatter, fmtNum numberFormatter) *block {
	tw.blk.reset()

	modTime := hdr.ModTime
	if modTime.IsZero() {
		modTime = time.Unix(0, 0)
	}

	v7 := tw.blk.toV7()
	v7.typeFlag()[0] = hdr.Typeflag
	fmtStr(v7.name(), hdr.Name)
	fmtStr(v7.linkName(), hdr.Linkname)
	fmtNum(v7.mode(), hdr.Mode)
	fmtNum(v7.uid(), int64(hdr.Uid))
	fmtNum(v7.gid(), int64(hdr.Gid))
	fmtNum(v7.size(), hdr.Size)
	fmtNum(v7.modTime(), modTime.Unix())

	ustar := tw.blk.toUSTAR()
	fmtStr(ustar.userName(), hdr.Uname)
	fmtStr(ustar.groupName(), hdr.Gname)
	fmtNum(ustar.devMajor(), hdr.Devmajor)
	fmtNum(ustar.devMinor(), hdr.Devminor)

	return &tw.blk
}

// writeRawFile 使用给定的名称和标志类型写入一个最小文件。
// 它使用 format 编码头部格式，并将 data 写入主体。
// 它对所有其他字段使用默认值（与 BSD 和 GNU tar 的做法一致）。
func (tw *Writer) writeRawFile(name, data string, flag byte, format Format) error {
	tw.blk.reset()

	// 尽力处理文件名。
	name = toASCII(name)
	if len(name) > nameSize {
		name = name[:nameSize]
	}
	name = strings.TrimRight(name, "/")

	var f formatter
	v7 := tw.blk.toV7()
	v7.typeFlag()[0] = flag
	f.formatString(v7.name(), name)
	f.formatOctal(v7.mode(), 0)
	f.formatOctal(v7.uid(), 0)
	f.formatOctal(v7.gid(), 0)
	f.formatOctal(v7.size(), int64(len(data))) // 必须小于 8GiB
	f.formatOctal(v7.modTime(), 0)
	tw.blk.setFormat(format)
	if f.err != nil {
		return f.err // 仅在违反大小条件时发生
	}

	// 写入头部和数据。
	if err := tw.writeRawHeader(&tw.blk, int64(len(data)), flag); err != nil {
		return err
	}
	_, err := io.WriteString(tw, data)
	return err
}

// writeRawHeader 写入 blk 的值，无论其值是什么。
// 它会设置 Writer，使其能够接收给定大小的文件。
// 如果 flag 是特殊的仅头部标志，则大小按零处理。
func (tw *Writer) writeRawHeader(blk *block, size int64, flag byte) error {
	if err := tw.Flush(); err != nil {
		return err
	}
	if _, err := tw.w.Write(blk[:]); err != nil {
		return err
	}
	if isHeaderOnlyType(flag) {
		size = 0
	}
	tw.curr = &regFileWriter{tw.w, size}
	tw.pad = blockPadding(size)
	return nil
}

// AddFS 将 fs.FS 中的文件添加到归档中。
// 它从文件系统的根开始遍历目录树，
// 在保持目录结构的同时将每个文件添加到 tar 归档中。
func (tw *Writer) AddFS(fsys fs.FS) error {
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
		linkTarget := ""
		if typ := d.Type(); typ == fs.ModeSymlink {
			var err error
			linkTarget, err = fs.ReadLink(fsys, name)
			if err != nil {
				return err
			}
		} else if !typ.IsRegular() && typ != fs.ModeDir {
			return errors.New("tar: cannot add non-regular file")
		}
		h, err := FileInfoHeader(info, linkTarget)
		if err != nil {
			return err
		}
		h.Name = name
		if d.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := fsys.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// splitUSTARPath 根据 USTAR 前缀和后缀规则拆分路径。
// 如果路径不可拆分，则返回 ("", "", false)。
func splitUSTARPath(name string) (prefix, suffix string, ok bool) {
	length := len(name)
	if length <= nameSize || !isASCII(name) {
		return "", "", false
	} else if length > prefixSize+1 {
		length = prefixSize + 1
	} else if name[length-1] == '/' {
		length--
	}

	i := strings.LastIndex(name[:length], "/")
	nlen := len(name) - i - 1 // nlen 是后缀的长度
	plen := i                 // plen 是前缀的长度
	if i <= 0 || nlen > nameSize || nlen == 0 || plen > prefixSize {
		return "", "", false
	}
	return name[:i], name[i+1:], true
}

// Write 写入 tar 归档中的当前文件。
// 如果在 [Writer.WriteHeader] 之后写入超过
// Header.Size 字节，则 Write 返回错误 [ErrWriteTooLong]。
//
// 对 [TypeLink]、[TypeSymlink]、[TypeChar]、
// [TypeBlock]、[TypeDir] 和 [TypeFifo] 等特殊类型调用 Write
// 会返回 (0, [ErrWriteTooLong])，无论 [Header.Size] 如何声明。
func (tw *Writer) Write(b []byte) (int, error) {
	if tw.err != nil {
		return 0, tw.err
	}
	n, err := tw.curr.Write(b)
	if err != nil && err != ErrWriteTooLong {
		tw.err = err
	}
	return n, err
}

// readFrom 通过从 r 读取来填充当前文件的内容。
// 读取的字节数必须与当前文件中剩余的字节数一致。
//
// 如果当前文件是稀疏的且 r 是 io.ReadSeeker，
// 那么 readFrom 使用 Seek 跳过 Header.SparseHoles 中定义的洞，
// 假定被跳过的区域全是 NUL。
// 它总是读取最后一个字节，以确保 r 的大小正确。
//
// TODO(dsnet): 在添加稀疏文件支持时重新导出此项。
// 见 https://golang.org/issue/22735
func (tw *Writer) readFrom(r io.Reader) (int64, error) {
	if tw.err != nil {
		return 0, tw.err
	}
	n, err := tw.curr.ReadFrom(r)
	if err != nil && err != ErrWriteTooLong {
		tw.err = err
	}
	return n, err
}

// Close 通过刷出填充并写入尾部来关闭 tar 归档。
// 如果当前文件（来自先前对 [Writer.WriteHeader] 的调用）未完整写入，
// 则返回错误。
func (tw *Writer) Close() error {
	if tw.err == ErrWriteAfterClose {
		return nil
	}
	if tw.err != nil {
		return tw.err
	}

	// 尾部：两个零块。
	err := tw.Flush()
	for i := 0; i < 2 && err == nil; i++ {
		_, err = tw.w.Write(zeroBlock[:])
	}

	// 确保所有后续操作都无效。
	tw.err = ErrWriteAfterClose
	return err // 报告 IO 错误
}

// regFileWriter 是一个用于向普通文件条目写入数据的 fileWriter。
type regFileWriter struct {
	w  io.Writer // 底层 Writer
	nb int64     // 剩余待写入的字节数
}

func (fw *regFileWriter) Write(b []byte) (n int, err error) {
	overwrite := int64(len(b)) > fw.nb
	if overwrite {
		b = b[:fw.nb]
	}
	if len(b) > 0 {
		n, err = fw.w.Write(b)
		fw.nb -= int64(n)
	}
	switch {
	case err != nil:
		return n, err
	case overwrite:
		return n, ErrWriteTooLong
	default:
		return n, nil
	}
}

func (fw *regFileWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{fw}, r)
}

// logicalRemaining 实现 fileState.logicalRemaining。
func (fw regFileWriter) logicalRemaining() int64 {
	return fw.nb
}

// physicalRemaining 实现 fileState.physicalRemaining。
func (fw regFileWriter) physicalRemaining() int64 {
	return fw.nb
}

// sparseFileWriter 是一个用于向稀疏文件条目写入数据的 fileWriter。
type sparseFileWriter struct {
	fw  fileWriter  // 底层 fileWriter
	sp  sparseDatas // 规范化的数据片段列表
	pos int64       // 稀疏文件中的当前位置
}

func (sw *sparseFileWriter) Write(b []byte) (n int, err error) {
	overwrite := int64(len(b)) > sw.logicalRemaining()
	if overwrite {
		b = b[:sw.logicalRemaining()]
	}

	b0 := b
	endPos := sw.pos + int64(len(b))
	for endPos > sw.pos && err == nil {
		var nf int // 片段中写入的字节数
		dataStart, dataEnd := sw.sp[0].Offset, sw.sp[0].endOffset()
		if sw.pos < dataStart { // 处于洞片段中
			bf := b[:min(int64(len(b)), dataStart-sw.pos)]
			nf, err = zeroWriter{}.Write(bf)
		} else { // 处于数据片段中
			bf := b[:min(int64(len(b)), dataEnd-sw.pos)]
			nf, err = sw.fw.Write(bf)
		}
		b = b[nf:]
		sw.pos += int64(nf)
		if sw.pos >= dataEnd && len(sw.sp) > 1 {
			sw.sp = sw.sp[1:] // 确保最后一个片段始终保留
		}
	}

	n = len(b0) - len(b)
	switch {
	case err == ErrWriteTooLong:
		return n, errMissData // 不可能；意味着验证逻辑存在 bug
	case err != nil:
		return n, err
	case sw.logicalRemaining() == 0 && sw.physicalRemaining() > 0:
		return n, errUnrefData // 不可能；意味着验证逻辑存在 bug
	case overwrite:
		return n, ErrWriteTooLong
	default:
		return n, nil
	}
}

func (sw *sparseFileWriter) ReadFrom(r io.Reader) (n int64, err error) {
	rs, ok := r.(io.ReadSeeker)
	if ok {
		if _, err := rs.Seek(0, io.SeekCurrent); err != nil {
			ok = false // 并非所有 io.Seeker 都能真正 seek
		}
	}
	if !ok {
		return io.Copy(struct{ io.Writer }{sw}, r)
	}

	var readLastByte bool
	pos0 := sw.pos
	for sw.logicalRemaining() > 0 && !readLastByte && err == nil {
		var nf int64 // 片段的大小
		dataStart, dataEnd := sw.sp[0].Offset, sw.sp[0].endOffset()
		if sw.pos < dataStart { // 处于洞片段中
			nf = dataStart - sw.pos
			if sw.physicalRemaining() == 0 {
				readLastByte = true
				nf--
			}
			_, err = rs.Seek(nf, io.SeekCurrent)
		} else { // 处于数据片段中
			nf = dataEnd - sw.pos
			nf, err = io.CopyN(sw.fw, rs, nf)
		}
		sw.pos += nf
		if sw.pos >= dataEnd && len(sw.sp) > 1 {
			sw.sp = sw.sp[1:] // 确保最后一个片段始终保留
		}
	}

	// 如果最后一个片段是洞，则 seek 到 EOF 前 1 字节处，
	// 并读取单个字节以确保文件大小正确。
	if readLastByte && err == nil {
		_, err = mustReadFull(rs, []byte{0})
		sw.pos++
	}

	n = sw.pos - pos0
	switch {
	case err == io.EOF:
		return n, io.ErrUnexpectedEOF
	case err == ErrWriteTooLong:
		return n, errMissData // 不可能；意味着验证逻辑存在 bug
	case err != nil:
		return n, err
	case sw.logicalRemaining() == 0 && sw.physicalRemaining() > 0:
		return n, errUnrefData // 不可能；意味着验证逻辑存在 bug
	default:
		return n, ensureEOF(rs)
	}
}

func (sw sparseFileWriter) logicalRemaining() int64 {
	return sw.sp[len(sw.sp)-1].endOffset() - sw.pos
}

func (sw sparseFileWriter) physicalRemaining() int64 {
	return sw.fw.physicalRemaining()
}

// zeroWriter 只能用 NUL 写入，否则返回 errWriteHole。
type zeroWriter struct{}

func (zeroWriter) Write(b []byte) (int, error) {
	for i, c := range b {
		if c != 0 {
			return i, errWriteHole
		}
	}
	return len(b), nil
}

// ensureEOF 检查 r 是否处于 EOF，若不是则报告 ErrWriteTooLong。
func ensureEOF(r io.Reader) error {
	n, err := tryReadFull(r, []byte{0})
	switch {
	case n > 0:
		return ErrWriteTooLong
	case err == io.EOF:
		return nil
	default:
		return err
	}
}
