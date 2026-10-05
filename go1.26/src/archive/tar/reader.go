// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tar

import (
	"bytes"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Reader 提供对 tar 归档内容的顺序访问。
// Reader.Next 前进到归档中的下一个文件（包括第一个），
// 此后 Reader 可当作 io.Reader 来访问该文件的数据。
type Reader struct {
	r    io.Reader
	pad  int64      // 当前文件条目之后的对齐填充量（被忽略）
	curr fileReader // 当前文件条目的 Reader
	blk  block      // 用作临时本地存储的缓冲区

	// err 是一个持久错误。
	// 只有 Reader 的每个导出方法有责任
	// 确保该错误是粘性的。
	err error
}

type fileReader interface {
	io.Reader
	fileState

	WriteTo(io.Writer) (int64, error)
}

// NewReader 创建一个从 r 读取的新 [Reader]。
func NewReader(r io.Reader) *Reader {
	return &Reader{r: r, curr: &regFileReader{r, 0}}
}

// Next 前进到 tar 归档中的下一个条目。
// Header.Size 决定下一个文件可以读取多少字节。
// 当前文件中任何剩余的数据都会被自动丢弃。
// 在归档末尾，Next 返回错误 io.EOF。
//
// 如果 Next 遇到非本地名称（由 [filepath.IsLocal] 定义）
// 且 GODEBUG 环境变量包含 `tarinsecurepath=0`，
// 则 Next 返回该头部以及一个 [ErrInsecurePath] 错误。
// Go 的未来版本可能会默认引入此行为。
// 想要接受非本地名称的程序可以忽略
// [ErrInsecurePath] 错误并使用返回的头部。
func (tr *Reader) Next() (*Header, error) {
	if tr.err != nil {
		return nil, tr.err
	}
	hdr, err := tr.next()
	tr.err = err
	if err == nil && !filepath.IsLocal(hdr.Name) {
		if tarinsecurepath.Value() == "0" {
			tarinsecurepath.IncNonDefault()
			err = ErrInsecurePath
		}
	}
	return hdr, err
}

func (tr *Reader) next() (*Header, error) {
	var paxHdrs map[string]string
	var gnuLongName, gnuLongLink string

	// 从外部看，Next 遍历 tar 归档时就好像它是一系列
	// 文件。在内部，tar 格式经常使用虚假的“文件”来添加
	// 描述下一个文件的元数据。这些元数据“文件”通常
	// 不应对外可见。因此，这个循环会遍历
	// 一个或多个“头部文件”，直到找到一个“普通文件”。
	format := FormatUSTAR | FormatPAX | FormatGNU
	for {
		// 丢弃文件的剩余部分以及任何填充。
		if err := discard(tr.r, tr.curr.physicalRemaining()); err != nil {
			return nil, err
		}
		if _, err := tryReadFull(tr.r, tr.blk[:tr.pad]); err != nil {
			return nil, err
		}
		tr.pad = 0

		hdr, rawHdr, err := tr.readHeader()
		if err != nil {
			return nil, err
		}
		if err := tr.handleRegularFile(hdr); err != nil {
			return nil, err
		}
		format.mayOnlyBe(hdr.Format)

		// 检查 PAX/GNU 特殊头部和文件。
		switch hdr.Typeflag {
		case TypeXHeader, TypeXGlobalHeader:
			format.mayOnlyBe(FormatPAX)
			paxHdrs, err = parsePAX(tr)
			if err != nil {
				return nil, err
			}
			if hdr.Typeflag == TypeXGlobalHeader {
				mergePAX(hdr, paxHdrs)
				return &Header{
					Name:       hdr.Name,
					Typeflag:   hdr.Typeflag,
					Xattrs:     hdr.Xattrs,
					PAXRecords: hdr.PAXRecords,
					Format:     format,
				}, nil
			}
			continue // 这是一个影响下一个头部的元头部
		case TypeGNULongName, TypeGNULongLink:
			format.mayOnlyBe(FormatGNU)
			realname, err := readSpecialFile(tr)
			if err != nil {
				return nil, err
			}

			var p parser
			switch hdr.Typeflag {
			case TypeGNULongName:
				gnuLongName = p.parseString(realname)
			case TypeGNULongLink:
				gnuLongLink = p.parseString(realname)
			}
			continue // 这是一个影响下一个头部的元头部
		default:
			// 旧的 GNU 稀疏格式在这里处理，因为从技术上讲
			// 它只是一个带有附加属性的普通文件。

			if err := mergePAX(hdr, paxHdrs); err != nil {
				return nil, err
			}
			if gnuLongName != "" {
				hdr.Name = gnuLongName
			}
			if gnuLongLink != "" {
				hdr.Linkname = gnuLongLink
			}
			if hdr.Typeflag == TypeRegA {
				if strings.HasSuffix(hdr.Name, "/") {
					hdr.Typeflag = TypeDir // 旧式归档使用尾部斜杠表示目录
				} else {
					hdr.Typeflag = TypeReg
				}
			}

			// 扩展头部可能已更新了大小。
			// 因此，在合并 PAX 头部之后要再次设置 regFileReader。
			if err := tr.handleRegularFile(hdr); err != nil {
				return nil, err
			}

			// 稀疏格式依赖于能够从逻辑数据段读取；
			// 必须在此前调用过 handleRegularFile。
			if err := tr.handleSparseFile(hdr, rawHdr); err != nil {
				return nil, err
			}

			// 设置对格式的最终推测。
			if format.has(FormatUSTAR) && format.has(FormatPAX) {
				format.mayOnlyBe(FormatUSTAR)
			}
			hdr.Format = format
			return hdr, nil // 这是一个文件，所以停止
		}
	}
}

// handleRegularFile 设置当前文件读取器和填充，使其
// 只能读取后续的逻辑数据段。它会正确处理
// 不包含数据段的特殊头部。
func (tr *Reader) handleRegularFile(hdr *Header) error {
	nb := hdr.Size
	if isHeaderOnlyType(hdr.Typeflag) {
		nb = 0
	}
	if nb < 0 {
		return ErrHeader
	}

	tr.pad = blockPadding(nb)
	tr.curr = &regFileReader{r: tr.r, nb: nb}
	return nil
}

// handleSparseFile 检查当前文件是否为任意类型的稀疏格式，
// 并相应地设置 curr 读取器。
func (tr *Reader) handleSparseFile(hdr *Header, rawHdr *block) error {
	var spd sparseDatas
	var err error
	if hdr.Typeflag == TypeGNUSparse {
		spd, err = tr.readOldGNUSparseMap(hdr, rawHdr)
	} else {
		spd, err = tr.readGNUSparsePAXHeaders(hdr)
	}

	// 如果 sp 非 nil，那么这是一个稀疏文件。
	// 注意 len(sp) == 0 是有可能的。
	if err == nil && spd != nil {
		if isHeaderOnlyType(hdr.Typeflag) || !validateSparseEntries(spd, hdr.Size) {
			return ErrHeader
		}
		sph := invertSparseEntries(spd, hdr.Size)
		tr.curr = &sparseFileReader{tr.curr, sph, 0}
	}
	return err
}

// readGNUSparsePAXHeaders 检查 PAX 头部中是否有 GNU 稀疏头部。
// 如果找到，则该函数读取稀疏映射并返回它。
// 这里假定 0.0 头部已经被 PAX 头部解析逻辑
// 转换为 0.1 头部。
func (tr *Reader) readGNUSparsePAXHeaders(hdr *Header) (sparseDatas, error) {
	// 识别 GNU 头部的版本。
	var is1x0 bool
	major, minor := hdr.PAXRecords[paxGNUSparseMajor], hdr.PAXRecords[paxGNUSparseMinor]
	switch {
	case major == "0" && (minor == "0" || minor == "1"):
		is1x0 = false
	case major == "1" && minor == "0":
		is1x0 = true
	case major != "" || minor != "":
		return nil, nil // 未知的 GNU 稀疏 PAX 版本
	case hdr.PAXRecords[paxGNUSparseMap] != "":
		is1x0 = false // 0.0 和 0.1 没有显式的版本记录，所以只能猜测
	default:
		return nil, nil // 不是 PAX 格式的 GNU 稀疏文件。
	}
	hdr.Format.mayOnlyBe(FormatPAX)

	// 根据 GNU 稀疏 PAX 头部更新 hdr。
	if name := hdr.PAXRecords[paxGNUSparseName]; name != "" {
		hdr.Name = name
	}
	size := hdr.PAXRecords[paxGNUSparseSize]
	if size == "" {
		size = hdr.PAXRecords[paxGNUSparseRealSize]
	}
	if size != "" {
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil {
			return nil, ErrHeader
		}
		hdr.Size = n
	}

	// 根据相应格式读取稀疏映射。
	if is1x0 {
		return readGNUSparseMap1x0(tr.curr)
	}
	return readGNUSparseMap0x1(hdr.PAXRecords)
}

// mergePAX 将 paxHdrs 合并到 hdr 中 Header 的所有相关字段。
func mergePAX(hdr *Header, paxHdrs map[string]string) (err error) {
	for k, v := range paxHdrs {
		if v == "" {
			continue // 保留原始的 USTAR 值
		}
		var id64 int64
		switch k {
		case paxPath:
			hdr.Name = v
		case paxLinkpath:
			hdr.Linkname = v
		case paxUname:
			hdr.Uname = v
		case paxGname:
			hdr.Gname = v
		case paxUid:
			id64, err = strconv.ParseInt(v, 10, 64)
			hdr.Uid = int(id64) // 可能发生整数溢出
		case paxGid:
			id64, err = strconv.ParseInt(v, 10, 64)
			hdr.Gid = int(id64) // 可能发生整数溢出
		case paxAtime:
			hdr.AccessTime, err = parsePAXTime(v)
		case paxMtime:
			hdr.ModTime, err = parsePAXTime(v)
		case paxCtime:
			hdr.ChangeTime, err = parsePAXTime(v)
		case paxSize:
			hdr.Size, err = strconv.ParseInt(v, 10, 64)
		default:
			if strings.HasPrefix(k, paxSchilyXattr) {
				if hdr.Xattrs == nil {
					hdr.Xattrs = make(map[string]string)
				}
				hdr.Xattrs[k[len(paxSchilyXattr):]] = v
			}
		}
		if err != nil {
			return ErrHeader
		}
	}
	hdr.PAXRecords = paxHdrs
	return nil
}

// parsePAX 解析 PAX 头部。
// 如果扩展头部（类型 'x'）无效，则返回 ErrHeader。
func parsePAX(r io.Reader) (map[string]string, error) {
	buf, err := readSpecialFile(r)
	if err != nil {
		return nil, err
	}
	sbuf := string(buf)

	// 为了支持 GNU PAX 稀疏格式 0.0。
	// 此函数将稀疏格式 0.0 头部转换为格式 0.1
	// 头部，因为 0.0 头部不符合 PAX 规范。
	var sparseMap []string

	paxHdrs := make(map[string]string)
	for len(sbuf) > 0 {
		key, value, residual, err := parsePAXRecord(sbuf)
		if err != nil {
			return nil, ErrHeader
		}
		sbuf = residual

		switch key {
		case paxGNUSparseOffset, paxGNUSparseNumBytes:
			// 校验稀疏头部的顺序和值。
			if (len(sparseMap)%2 == 0 && key != paxGNUSparseOffset) ||
				(len(sparseMap)%2 == 1 && key != paxGNUSparseNumBytes) ||
				strings.Contains(value, ",") {
				return nil, ErrHeader
			}
			sparseMap = append(sparseMap, value)
		default:
			paxHdrs[key] = value
		}
	}
	if len(sparseMap) > 0 {
		paxHdrs[paxGNUSparseMap] = strings.Join(sparseMap, ",")
	}
	return paxHdrs, nil
}

// readHeader 读取下一个块头部，并假定底层读取器
// 已经对齐到块边界。它返回头部的原始块，
// 以备需要进一步处理。
//
// 仅当发生以下情况之一时，err 才会被设置为 io.EOF：
//   - 恰好读取了 0 字节并遇到 EOF。
//   - 恰好读取了 1 个全零块并遇到 EOF。
//   - 至少读取了 2 个全零块。
func (tr *Reader) readHeader() (*Header, *block, error) {
	// 两个全零字节块标志着归档的结束。
	if _, err := io.ReadFull(tr.r, tr.blk[:]); err != nil {
		return nil, nil, err // 这里 EOF 是可以的；恰好读取了 0 字节
	}
	if bytes.Equal(tr.blk[:], zeroBlock[:]) {
		if _, err := io.ReadFull(tr.r, tr.blk[:]); err != nil {
			return nil, nil, err // 这里 EOF 是可以的；恰好读取了 1 个全零块
		}
		if bytes.Equal(tr.blk[:], zeroBlock[:]) {
			return nil, nil, io.EOF // 正常 EOF；恰好读取了 2 个全零块
		}
		return nil, nil, ErrHeader // 全零块之后跟着非零块
	}

	// 验证头部是否匹配已知格式。
	format := tr.blk.getFormat()
	if format == FormatUnknown {
		return nil, nil, ErrHeader
	}

	var p parser
	hdr := new(Header)

	// 解包 V7 头部。
	v7 := tr.blk.toV7()
	hdr.Typeflag = v7.typeFlag()[0]
	hdr.Name = p.parseString(v7.name())
	hdr.Linkname = p.parseString(v7.linkName())
	hdr.Size = p.parseNumeric(v7.size())
	hdr.Mode = p.parseNumeric(v7.mode())
	hdr.Uid = int(p.parseNumeric(v7.uid()))
	hdr.Gid = int(p.parseNumeric(v7.gid()))
	hdr.ModTime = time.Unix(p.parseNumeric(v7.modTime()), 0)

	// 解包格式特定的字段。
	if format > formatV7 {
		ustar := tr.blk.toUSTAR()
		hdr.Uname = p.parseString(ustar.userName())
		hdr.Gname = p.parseString(ustar.groupName())
		hdr.Devmajor = p.parseNumeric(ustar.devMajor())
		hdr.Devminor = p.parseNumeric(ustar.devMinor())

		var prefix string
		switch {
		case format.has(FormatUSTAR | FormatPAX):
			hdr.Format = format
			ustar := tr.blk.toUSTAR()
			prefix = p.parseString(ustar.prefix())

			// 为了检测 Format，检查块是否格式正确，因为
			// 解析器比 USTAR 实际允许的更宽松。
			notASCII := func(r rune) bool { return r >= 0x80 }
			if bytes.IndexFunc(tr.blk[:], notASCII) >= 0 {
				hdr.Format = FormatUnknown // 块中存在非 ASCII 字符。
			}
			nul := func(b []byte) bool { return int(b[len(b)-1]) == 0 }
			if !(nul(v7.size()) && nul(v7.mode()) && nul(v7.uid()) && nul(v7.gid()) &&
				nul(v7.modTime()) && nul(ustar.devMajor()) && nul(ustar.devMinor())) {
				hdr.Format = FormatUnknown // 数值字段必须以 NUL 结尾
			}
		case format.has(formatSTAR):
			star := tr.blk.toSTAR()
			prefix = p.parseString(star.prefix())
			hdr.AccessTime = time.Unix(p.parseNumeric(star.accessTime()), 0)
			hdr.ChangeTime = time.Unix(p.parseNumeric(star.changeTime()), 0)
		case format.has(FormatGNU):
			hdr.Format = format
			var p2 parser
			gnu := tr.blk.toGNU()
			if b := gnu.accessTime(); b[0] != 0 {
				hdr.AccessTime = time.Unix(p2.parseNumeric(b), 0)
			}
			if b := gnu.changeTime(); b[0] != 0 {
				hdr.ChangeTime = time.Unix(p2.parseNumeric(b), 0)
			}

			// 在 Go1.8 之前，Writer 有一个 bug：在某些罕见情况下
			// 会输出无效的 tar 文件，因为其逻辑
			// 错误地认为旧的 GNU 格式有一个 prefix 字段。
			// 这是错误的，会导致输出文件弄乱
			// 通常未被使用的 atime 和 ctime 字段。
			//
			// 为了能够继续读取由以前有 bug 的 Go 版本
			// 创建的 tar 文件，我们会怀疑地解析 atime 和 ctime 字段。
			// 如果我们无法解析它们，并且 prefix 字段看起来
			// 像 ASCII 字符串，那么就回退到 Go1.8 之前的行为，
			// 即把这些字段当作 USTAR 的 prefix 字段。
			//
			// 注意，这不会对所有可能的由 Go1.8 之前工具链
			// 生成的文件使用回退逻辑。如果生成的文件
			// 恰好有一个 prefix 字段，能被解析为有效的
			// atime 和 ctime 字段（例如，当它们是有效的八进制字符串时），
			// 那么就无法区分有效的 GNU 文件
			// 和无效的 Go1.8 之前文件。
			//
			// 参见 https://golang.org/issues/12594
			// 参见 https://golang.org/issues/21005
			if p2.err != nil {
				hdr.AccessTime, hdr.ChangeTime = time.Time{}, time.Time{}
				ustar := tr.blk.toUSTAR()
				if s := p.parseString(ustar.prefix()); isASCII(s) {
					prefix = s
				}
				hdr.Format = FormatUnknown // 有 bug 的文件不是 GNU
			}
		}
		if len(prefix) > 0 {
			hdr.Name = prefix + "/" + hdr.Name
		}
	}
	return hdr, &tr.blk, p.err
}

// readOldGNUSparseMap 从旧的 GNU 稀疏格式中读取稀疏映射。
// 如果稀疏映射足够小，它会被存储在 tar 头部中。
// 如果它超过四个条目，则会使用一个或多个扩展头部
// 来存储稀疏映射的其余部分。
//
// Header.Size 不反映所使用的任何扩展头部的大小。
// 因此，此函数会从原始 io.Reader 读取以获取额外的头部。
// 此方法在处理过程中会修改 blk。
func (tr *Reader) readOldGNUSparseMap(hdr *Header, blk *block) (sparseDatas, error) {
	// 确保输入格式是 GNU。
	// 遗憾的是，STAR 格式也有一种稀疏头部格式，它使用
	// 相同的类型标志，但布局完全不同。
	if blk.getFormat() != FormatGNU {
		return nil, ErrHeader
	}
	hdr.Format.mayOnlyBe(FormatGNU)

	var p parser
	hdr.Size = p.parseNumeric(blk.toGNU().realSize())
	if p.err != nil {
		return nil, p.err
	}
	s := blk.toGNU().sparse()
	spd := make(sparseDatas, 0, s.maxEntries())
	for {
		for i := 0; i < s.maxEntries(); i++ {
			// 此终止条件与 GNU 和 BSD tar 相同。
			if s.entry(i).offset()[0] == 0x00 {
				break // 不要返回，需要处理扩展头部（即使为空）
			}
			offset := p.parseNumeric(s.entry(i).offset())
			length := p.parseNumeric(s.entry(i).length())
			if p.err != nil {
				return nil, p.err
			}
			spd = append(spd, sparseEntry{Offset: offset, Length: length})
		}

		if s.isExtended()[0] > 0 {
			// 还有更多条目。读取一个扩展头部并解析其条目。
			if _, err := mustReadFull(tr.r, blk[:]); err != nil {
				return nil, err
			}
			s = blk.toSparse()
			continue
		}
		return spd, nil // 完成
	}
}

// readGNUSparseMap1x0 读取存储在 GNU 的 PAX 稀疏格式
// 1.0 版本中的稀疏映射。稀疏映射的格式由一系列
// 以换行结尾的数值字段组成。第一个字段是条目数量，
// 且总是存在。其后是各个条目，由两个
// 字段（offset、length）组成。此函数必须在包含最后一个换行符的
// 块的结束边界处停止读取。
//
// 注意，GNU 手册说数值应以八进制
// 格式编码。然而，GNU tar 工具本身以十进制输出这些值。
// 因此，此库将这些值视为以十进制编码。
func readGNUSparseMap1x0(r io.Reader) (sparseDatas, error) {
	var (
		cntNewline int64
		buf        bytes.Buffer
		blk        block
		totalSize  int
	)

	// feedTokens 将 r 中的数据按块复制到 buf，直到
	// buf 中至少有 cnt 个换行符。它不会读取超出所需数量的块。
	feedTokens := func(n int64) error {
		for cntNewline < n {
			totalSize += len(blk)
			if totalSize > maxSpecialFileSize {
				return errSparseTooLong
			}
			if _, err := mustReadFull(r, blk[:]); err != nil {
				return err
			}
			buf.Write(blk[:])
			for _, c := range blk {
				if c == '\n' {
					cntNewline++
				}
			}
		}
		return nil
	}

	// nextToken 获取下一个以换行符分隔的标记。这里假定
	// 缓冲区中至少存在一个换行符。
	nextToken := func() string {
		cntNewline--
		tok, _ := buf.ReadString('\n')
		return strings.TrimRight(tok, "\n")
	}

	// 解析条目数量。
	// 使用抗整数溢出的算法来检查它。
	if err := feedTokens(1); err != nil {
		return nil, err
	}
	numEntries, err := strconv.ParseInt(nextToken(), 10, 0) // 有意按原生 int 解析
	if err != nil || numEntries < 0 || int(2*numEntries) < int(numEntries) {
		return nil, ErrHeader
	}

	// 解析所有成员条目。
	// 此后 numEntries 是可信的，因为 feedTokens 会根据
	// maxSpecialFileSize 限制标记的数量。
	if err := feedTokens(2 * numEntries); err != nil {
		return nil, err
	}
	spd := make(sparseDatas, 0, numEntries)
	for i := int64(0); i < numEntries; i++ {
		offset, err1 := strconv.ParseInt(nextToken(), 10, 64)
		length, err2 := strconv.ParseInt(nextToken(), 10, 64)
		if err1 != nil || err2 != nil {
			return nil, ErrHeader
		}
		spd = append(spd, sparseEntry{Offset: offset, Length: length})
	}
	return spd, nil
}

// readGNUSparseMap0x1 读取存储在 GNU 的 PAX 稀疏格式
// 0.1 版本中的稀疏映射。稀疏映射存储在 PAX 头部中。
func readGNUSparseMap0x1(paxHdrs map[string]string) (sparseDatas, error) {
	// 获取条目数量。
	// 使用抗整数溢出的算法来检查它。
	numEntriesStr := paxHdrs[paxGNUSparseNumBlocks]
	numEntries, err := strconv.ParseInt(numEntriesStr, 10, 0) // 有意按原生 int 解析
	if err != nil || numEntries < 0 || int(2*numEntries) < int(numEntries) {
		return nil, ErrHeader
	}

	// sparseMap 中每个条目应该有两个数字。
	sparseMap := strings.Split(paxHdrs[paxGNUSparseMap], ",")
	if len(sparseMap) == 1 && sparseMap[0] == "" {
		sparseMap = sparseMap[:0]
	}
	if int64(len(sparseMap)) != 2*numEntries {
		return nil, ErrHeader
	}

	// 遍历稀疏映射中的各个条目。
	// 现在 numEntries 是可信的。
	spd := make(sparseDatas, 0, numEntries)
	for len(sparseMap) >= 2 {
		offset, err1 := strconv.ParseInt(sparseMap[0], 10, 64)
		length, err2 := strconv.ParseInt(sparseMap[1], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, ErrHeader
		}
		spd = append(spd, sparseEntry{Offset: offset, Length: length})
		sparseMap = sparseMap[2:]
	}
	return spd, nil
}

// Read 从 tar 归档中的当前文件读取。
// 当到达该文件末尾时，它返回 (0, io.EOF)，
// 直到调用 [Next] 前进到下一个文件。
//
// 如果当前文件是稀疏的，那么被标记为空洞的区域
// 会被读回为 NUL 字节。
//
// 对特殊类型（如 [TypeLink]、[TypeSymlink]、[TypeChar]、
// [TypeBlock]、[TypeDir] 和 [TypeFifo]）调用 Read 会返回 (0, [io.EOF])，
// 无论 [Header.Size] 声称什么。
func (tr *Reader) Read(b []byte) (int, error) {
	if tr.err != nil {
		return 0, tr.err
	}
	n, err := tr.curr.Read(b)
	if err != nil && err != io.EOF {
		tr.err = err
	}
	return n, err
}

// writeTo 将当前文件的内容写入 w。
// 写入的字节数与当前文件中剩余的字节数相匹配。
//
// 如果当前文件是稀疏的且 w 是 io.WriteSeeker，
// 那么 writeTo 使用 Seek 跳过 Header.SparseHoles 中定义的空洞，
// 假定被跳过的区域填充了 NUL。
// 它总是写入最后一个字节，以确保 w 的大小正确。
//
// TODO(dsnet): 在添加稀疏文件支持时重新导出此函数。
// 参见 https://golang.org/issue/22735
func (tr *Reader) writeTo(w io.Writer) (int64, error) {
	if tr.err != nil {
		return 0, tr.err
	}
	n, err := tr.curr.WriteTo(w)
	if err != nil {
		tr.err = err
	}
	return n, err
}

// regFileReader 是一个 fileReader，用于从普通文件条目读取数据。
type regFileReader struct {
	r  io.Reader // 底层 Reader
	nb int64     // 剩余要读取的字节数
}

func (fr *regFileReader) Read(b []byte) (n int, err error) {
	if int64(len(b)) > fr.nb {
		b = b[:fr.nb]
	}
	if len(b) > 0 {
		n, err = fr.r.Read(b)
		fr.nb -= int64(n)
	}
	switch {
	case err == io.EOF && fr.nb > 0:
		return n, io.ErrUnexpectedEOF
	case err == nil && fr.nb == 0:
		return n, io.EOF
	default:
		return n, err
	}
}

func (fr *regFileReader) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, struct{ io.Reader }{fr})
}

// logicalRemaining 实现 fileState.logicalRemaining。
func (fr regFileReader) logicalRemaining() int64 {
	return fr.nb
}

// physicalRemaining 实现 fileState.physicalRemaining。
func (fr regFileReader) physicalRemaining() int64 {
	return fr.nb
}

// sparseFileReader 是一个 fileReader，用于从稀疏文件条目读取数据。
type sparseFileReader struct {
	fr  fileReader  // 底层 fileReader
	sp  sparseHoles // 规范化后的稀疏空洞列表
	pos int64       // 稀疏文件中的当前位置
}

func (sr *sparseFileReader) Read(b []byte) (n int, err error) {
	finished := int64(len(b)) >= sr.logicalRemaining()
	if finished {
		b = b[:sr.logicalRemaining()]
	}

	b0 := b
	endPos := sr.pos + int64(len(b))
	for endPos > sr.pos && err == nil {
		var nf int // 片段中读取的字节数
		holeStart, holeEnd := sr.sp[0].Offset, sr.sp[0].endOffset()
		if sr.pos < holeStart { // 在数据片段中
			bf := b[:min(int64(len(b)), holeStart-sr.pos)]
			nf, err = tryReadFull(sr.fr, bf)
		} else { // 在空洞片段中
			bf := b[:min(int64(len(b)), holeEnd-sr.pos)]
			nf, err = tryReadFull(zeroReader{}, bf)
		}
		b = b[nf:]
		sr.pos += int64(nf)
		if sr.pos >= holeEnd && len(sr.sp) > 1 {
			sr.sp = sr.sp[1:] // 确保最后一个片段始终保留
		}
	}

	n = len(b0) - len(b)
	switch {
	case err == io.EOF:
		return n, errMissData // 密集文件中的数据比稀疏文件少
	case err != nil:
		return n, err
	case sr.logicalRemaining() == 0 && sr.physicalRemaining() > 0:
		return n, errUnrefData // 密集文件中的数据比稀疏文件多
	case finished:
		return n, io.EOF
	default:
		return n, nil
	}
}

func (sr *sparseFileReader) WriteTo(w io.Writer) (n int64, err error) {
	ws, ok := w.(io.WriteSeeker)
	if ok {
		if _, err := ws.Seek(0, io.SeekCurrent); err != nil {
			ok = false // 并非所有 io.Seeker 都能真正 seek
		}
	}
	if !ok {
		return io.Copy(w, struct{ io.Reader }{sr})
	}

	var writeLastByte bool
	pos0 := sr.pos
	for sr.logicalRemaining() > 0 && !writeLastByte && err == nil {
		var nf int64 // 片段的大小
		holeStart, holeEnd := sr.sp[0].Offset, sr.sp[0].endOffset()
		if sr.pos < holeStart { // 在数据片段中
			nf = holeStart - sr.pos
			nf, err = io.CopyN(ws, sr.fr, nf)
		} else { // 在空洞片段中
			nf = holeEnd - sr.pos
			if sr.physicalRemaining() == 0 {
				writeLastByte = true
				nf--
			}
			_, err = ws.Seek(nf, io.SeekCurrent)
		}
		sr.pos += nf
		if sr.pos >= holeEnd && len(sr.sp) > 1 {
			sr.sp = sr.sp[1:] // 确保最后一个片段始终保留
		}
	}

	// 如果最后一个片段是空洞，则 seek 到 EOF 前 1 个字节处，并
	// 写入单个字节以确保文件大小正确。
	if writeLastByte && err == nil {
		_, err = ws.Write([]byte{0})
		sr.pos++
	}

	n = sr.pos - pos0
	switch {
	case err == io.EOF:
		return n, errMissData // 密集文件中的数据比稀疏文件少
	case err != nil:
		return n, err
	case sr.logicalRemaining() == 0 && sr.physicalRemaining() > 0:
		return n, errUnrefData // 密集文件中的数据比稀疏文件多
	default:
		return n, nil
	}
}

func (sr sparseFileReader) logicalRemaining() int64 {
	return sr.sp[len(sr.sp)-1].endOffset() - sr.pos
}
func (sr sparseFileReader) physicalRemaining() int64 {
	return sr.fr.physicalRemaining()
}

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}

// mustReadFull 类似 io.ReadFull，不同之处在于当读取 len(b) 字节之前
// 遇到 io.EOF 时，它返回 io.ErrUnexpectedEOF。
func mustReadFull(r io.Reader, b []byte) (int, error) {
	n, err := tryReadFull(r, b)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// tryReadFull 类似 io.ReadFull，不同之处在于当读取 len(b) 字节之前
// 遇到 io.EOF 时，它返回 io.EOF。
func tryReadFull(r io.Reader, b []byte) (n int, err error) {
	for len(b) > n && err == nil {
		var nn int
		nn, err = r.Read(b[n:])
		n += nn
	}
	if len(b) == n && err == io.EOF {
		err = nil
	}
	return n, err
}

// readSpecialFile 类似 io.ReadAll，不同之处在于如果读取的内容
// 超过 maxSpecialFileSize，则返回 ErrFieldTooLong。
func readSpecialFile(r io.Reader) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, maxSpecialFileSize+1))
	if len(buf) > maxSpecialFileSize {
		return nil, ErrFieldTooLong
	}
	return buf, err
}

// discard 跳过 r 中的 n 个字节，如果无法做到则报告错误。
func discard(r io.Reader, n int64) error {
	// 如果可能，Seek 到数据段结束前的最后一个字节。
	// 这样做是因为 Seek 常常延迟报告错误；这会掩盖
	// 流可能被截断的事实。我们可以依靠
	// 随后不久执行的 io.CopyN 来触发任何 IO 错误。
	var seekSkipped int64 // 通过 Seek 跳过的字节数
	if sr, ok := r.(io.Seeker); ok && n > 1 {
		// 并非所有 io.Seeker 都能真正 Seek。例如，os.Stdin 实现了
		// io.Seeker，但调用 Seek 总是返回错误且不执行
		// 任何操作。因此，我们尝试对当前位置做一次无害的 seek，
		// 以查看是否真正支持 Seek。
		pos1, err := sr.Seek(0, io.SeekCurrent)
		if pos1 >= 0 && err == nil {
			// 似乎支持 Seek，所以执行真正的 Seek。
			pos2, err := sr.Seek(n-1, io.SeekCurrent)
			if pos2 < 0 || err != nil {
				return err
			}
			seekSkipped = pos2 - pos1
		}
	}

	copySkipped, err := io.CopyN(io.Discard, r, n-seekSkipped)
	if err == io.EOF && seekSkipped+copySkipped < n {
		err = io.ErrUnexpectedEOF
	}
	return err
}
