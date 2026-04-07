// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package tar 实现了对 tar 存档的访问。
//
// 磁带存档（tar）是一种用于存储文件序列的文件格式，可以以流式方式读取和写入。
// 本包旨在覆盖该格式的大多数变体，包括由 GNU 和 BSD tar 工具生成的格式。
package tar

import (
	"errors"
	"fmt"
	"internal/godebug"
	"io/fs"
	"maps"
	"math"
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// BUG：在32位架构上使用 Header 中的 Uid 和 Gid 字段可能会溢出。
// 如果在解码时遇到较大的值，存储在 Header 中的结果将是截断后的版本。

var tarinsecurepath = godebug.New("tarinsecurepath")

var (
	ErrHeader          = errors.New("archive/tar: invalid tar header")
	ErrWriteTooLong    = errors.New("archive/tar: write too long")
	ErrFieldTooLong    = errors.New("archive/tar: header field too long")
	ErrWriteAfterClose = errors.New("archive/tar: write after close")
	ErrInsecurePath    = errors.New("archive/tar: insecure file path")
	errMissData        = errors.New("archive/tar: sparse file references non-existent data")
	errUnrefData       = errors.New("archive/tar: sparse file contains unreferenced data")
	errWriteHole       = errors.New("archive/tar: write non-NUL byte in sparse hole")
	errSparseTooLong   = errors.New("archive/tar: sparse map too long")
)

type headerError []string

func (he headerError) Error() string {
	const prefix = "archive/tar: cannot encode header"
	var ss []string
	for _, s := range he {
		if s != "" {
			ss = append(ss, s)
		}
	}
	if len(ss) == 0 {
		return prefix
	}
	return fmt.Sprintf("%s: %v", prefix, strings.Join(ss, "; and "))
}

// Header.Typeflag 的类型标志。
const (
	// 类型 '0' 表示普通文件。
	TypeReg = '0'

	// 已弃用：请使用 TypeReg 代替。
	TypeRegA = '\x00'

	// 类型 '1' 到 '6' 是仅头标志，可能没有数据体。
	TypeLink    = '1' // 硬链接
	TypeSymlink = '2' // 符号链接
	TypeChar    = '3' // 字符设备节点
	TypeBlock   = '4' // 块设备节点
	TypeDir     = '5' // 目录
	TypeFifo    = '6' // FIFO 节点

	// 类型 '7' 是保留的。
	TypeCont = '7'

	// 类型 'x' 用于 PAX 格式，存储仅与下一个文件相关的键值记录。
	// 本包透明地处理这些类型。
	TypeXHeader = 'x'

	// 类型 'g' 用于 PAX 格式，存储与所有后续文件相关的键值记录。
	// 本包仅支持解析和组合此类头部，但当前不支持跨文件持久化全局状态。
	TypeXGlobalHeader = 'g'

	// 类型 'S' 表示 GNU 格式中的稀疏文件。
	TypeGNUSparse = 'S'

	// 类型 'L' 和 'K' 用于 GNU 格式的元文件，用于存储下一个文件的路径或链接名。
	// 本包透明地处理这些类型。
	TypeGNULongName = 'L'
	TypeGNULongLink = 'K'
)

// PAX 扩展头部记录的关键词。
const (
	paxNone     = "" // 表示没有合适的 PAX 键
	paxPath     = "path"
	paxLinkpath = "linkpath"
	paxSize     = "size"
	paxUid      = "uid"
	paxGid      = "gid"
	paxUname    = "uname"
	paxGname    = "gname"
	paxMtime    = "mtime"
	paxAtime    = "atime"
	paxCtime    = "ctime"   // 从后续修订的 PAX 规范中移除，但曾是有效的
	paxCharset  = "charset" // 当前未使用
	paxComment  = "comment" // 当前未使用

	paxSchilyXattr = "SCHILY.xattr."

	// PAX 扩展头部中 GNU 稀疏文件的关键词。
	paxGNUSparse          = "GNU.sparse."
	paxGNUSparseNumBlocks = "GNU.sparse.numblocks"
	paxGNUSparseOffset    = "GNU.sparse.offset"
	paxGNUSparseNumBytes  = "GNU.sparse.numbytes"
	paxGNUSparseMap       = "GNU.sparse.map"
	paxGNUSparseName      = "GNU.sparse.name"
	paxGNUSparseMajor     = "GNU.sparse.major"
	paxGNUSparseMinor     = "GNU.sparse.minor"
	paxGNUSparseSize      = "GNU.sparse.size"
	paxGNUSparseRealSize  = "GNU.sparse.realsize"
)

// basicKeys 是内置支持的 PAX 键集合。
// 这不包括 "charset" 或 "comment"，两者都是 PAX 特有的，
// 不太可能作为 Header 的一流特性添加。
// 用户可以使用 PAXRecords 字段自行设置。
var basicKeys = map[string]bool{
	paxPath: true, paxLinkpath: true, paxSize: true, paxUid: true, paxGid: true,
	paxUname: true, paxGname: true, paxMtime: true, paxAtime: true, paxCtime: true,
}

// Header 表示 tar 存档中的单个头部。
// 某些字段可能未填充。
//
// 为了向前兼容，从 Reader.Next 获取 Header 的用户，
// 以某种方式修改它，然后将其传回 Writer.WriteHeader 时，
// 应通过创建一个新的 Header 并复制他们希望保留的字段来实现。
type Header struct {
	// Typeflag 是头部条目的类型。
	// 零值会根据 Name 中是否存在尾部斜杠自动提升为 TypeReg 或 TypeDir。
	Typeflag byte

	Name     string // 文件条目的名称
	Linkname string // 链接的目标名称（对 TypeLink 或 TypeSymlink 有效）

	Size  int64  // 逻辑文件大小（字节）
	Mode  int64  // 权限和模式位
	Uid   int    // 所有者的用户 ID
	Gid   int    // 所有者的组 ID
	Uname string // 所有者的用户名
	Gname string // 所有者的组名

	// 如果 Format 未指定，则 Writer.WriteHeader 会将 ModTime
	// 舍入到最接近的秒，并忽略 AccessTime 和 ChangeTime 字段。
	//
	// 要使用 AccessTime 或 ChangeTime，请将 Format 指定为 PAX 或 GNU。
	// 要使用亚秒级精度，请将 Format 指定为 PAX。
	ModTime    time.Time // 修改时间
	AccessTime time.Time // 访问时间（需要 PAX 或 GNU 支持）
	ChangeTime time.Time // 变更时间（需要 PAX 或 GNU 支持）

	Devmajor int64 // 主设备号（对 TypeChar 或 TypeBlock 有效）
	Devminor int64 // 次设备号（对 TypeChar 或 TypeBlock 有效）

	// Xattrs 在 "SCHILY.xattr." 命名空间下以 PAX 记录存储扩展属性。
	//
	// 以下在语义上是等价的：
	//  h.Xattrs[key] = value
	//  h.PAXRecords["SCHILY.xattr."+key] = value
	//
	// 调用 Writer.WriteHeader 时，Xattrs 的内容将优先于 PAXRecords 中的内容。
	//
	// 已弃用：请使用 PAXRecords 代替。
	Xattrs map[string]string

	// PAXRecords 是 PAX 扩展头部记录的映射。
	//
	// 用户定义的记录应具有以下形式的键：
	//	VENDOR.keyword
	// 其中 VENDOR 是全大写的某个命名空间，keyword 不得包含 '=' 字符（例如 "GOLANG.pkg.version"）。
	// 键和值应为非空的 UTF-8 字符串。
	//
	// 调用 Writer.WriteHeader 时，从 Header 的其他字段派生的 PAX 记录优先于 PAXRecords。
	PAXRecords map[string]string

	// Format 指定 tar 头部的格式。
	//
	// 这由 Reader.Next 设置为对格式的最佳猜测。
	// 由于 Reader 宽松地读取一些不兼容的文件，这可能为 FormatUnknown。
	//
	// 如果在调用 Writer.WriteHeader 时未指定格式，
	// 则它使用能够编码此 Header 的第一种格式（按 USTAR、PAX、GNU 的顺序）（参见 Format）。
	Format Format
}

// sparseEntry 表示文件中在 Offset 处的一个 Length 大小的片段。
type sparseEntry struct{ Offset, Length int64 }

func (s sparseEntry) endOffset() int64 { return s.Offset + s.Length }

// 稀疏文件可以表示为 sparseDatas 或 sparseHoles。
// 只要总大小已知，它们是等价的，可以相互转换。
// 支持稀疏文件的各种 tar 格式以 sparseDatas 形式表示稀疏文件。
// 也就是说，它们指定文件中有数据的片段，并将其他所有内容视为零字节。
// 因此，本包中的编码和解码逻辑处理 sparseDatas。
//
// 然而，外部 API 使用 sparseHoles 而不是 sparseDatas，因为 sparseHoles 的零值逻辑上表示一个普通文件（即其中没有空洞）。
// 另一方面，sparseDatas 的零值意味着文件中没有数据，这相当奇怪。
//
// 例如，如果底层原始文件包含 10 字节数据：
//
//	var compactFile = "abcdefgh"
//
// 并且稀疏映射具有以下条目：
//
//	var spd sparseDatas = []sparseEntry{
//		{Offset: 2,  Length: 5},  // 2..6 的数据片段
//		{Offset: 18, Length: 3},  // 18..20 的数据片段
//	}
//	var sph sparseHoles = []sparseEntry{
//		{Offset: 0,  Length: 2},  // 0..1 的空洞片段
//		{Offset: 7,  Length: 11}, // 7..17 的空洞片段
//		{Offset: 21, Length: 4},  // 21..24 的空洞片段
//	}
//
// 那么 Header.Size 为 25 的最终稀疏文件的内容为：
//
//	var sparseFile = "\x00"*2 + "abcde" + "\x00"*11 + "fgh" + "\x00"*4
type (
	sparseDatas []sparseEntry
	sparseHoles []sparseEntry
)

// validateSparseEntries 报告 sp 是否为有效的稀疏映射。
// sp 表示数据片段还是空洞片段无关紧要。
func validateSparseEntries(sp []sparseEntry, size int64) bool {
	// 验证所有稀疏条目。这些与 BSD tar 工具执行的检查相同。
	if size < 0 {
		return false
	}
	var pre sparseEntry
	for _, cur := range sp {
		switch {
		case cur.Offset < 0 || cur.Length < 0:
			return false // 负值永远不可接受
		case cur.Offset > math.MaxInt64-cur.Length:
			return false // 大长度导致整数溢出
		case cur.endOffset() > size:
			return false // 区域超出实际大小
		case pre.endOffset() > cur.Offset:
			return false // 区域不能重叠且必须按顺序
		}
		pre = cur
	}
	return true
}

// alignSparseEntries 修改 src 并返回 dst，其中每个片段的起始偏移量向上对齐到最近的块边界，每个结束偏移量向下对齐到最近的块边界。
//
// 尽管 Go tar Reader 和 BSD tar 工具可以处理具有任意偏移量和长度的条目，
// 但 GNU tar 工具只能处理偏移量和长度是 blockSize 倍数的条目。
func alignSparseEntries(src []sparseEntry, size int64) []sparseEntry {
	dst := src[:0]
	for _, s := range src {
		pos, end := s.Offset, s.endOffset()
		pos += blockPadding(+pos) // 向上舍入到最近的 blockSize
		if end != size {
			end -= blockPadding(-end) // 向下舍入到最近的 blockSize
		}
		if pos < end {
			dst = append(dst, sparseEntry{Offset: pos, Length: end - pos})
		}
	}
	return dst
}

// invertSparseEntries 将稀疏映射从一种形式转换为另一种形式。
// 如果输入是 sparseHoles，则输出 sparseDatas，反之亦然。
// 输入必须已经验证过。
//
// 此函数修改 src 并返回一个规范化后的映射，其中：
//   - 相邻的片段合并在一起
//   - 只有最后一个片段可能为空
//   - 最后一个片段的 endOffset 等于总大小
func invertSparseEntries(src []sparseEntry, size int64) []sparseEntry {
	dst := src[:0]
	var pre sparseEntry
	for _, cur := range src {
		if cur.Length == 0 {
			continue // 跳过空片段
		}
		pre.Length = cur.Offset - pre.Offset
		if pre.Length > 0 {
			dst = append(dst, pre) // 仅添加非空片段
		}
		pre.Offset = cur.endOffset()
	}
	pre.Length = size - pre.Offset // 可能是唯一的空片段
	return append(dst, pre)
}

// fileState 跟踪当前文件的逻辑（包括稀疏空洞）和物理（实际在 tar 存档中）剩余字节数。
//
// 不变式：logicalRemaining >= physicalRemaining
type fileState interface {
	logicalRemaining() int64
	physicalRemaining() int64
}

// allowedFormats 确定可以使用哪些格式。
// 返回的值是多种可能格式的逻辑或。
// 如果值为 FormatUnknown，则无法编码输入的 Header，并返回解释原因的错误。
//
// 作为检查字段的副作用，此函数返回 paxHdrs，其中包含所有无法直接编码的字段。
// 值接收器确保此方法不会修改源 Header。
func (h Header) allowedFormats() (format Format, paxHdrs map[string]string, err error) {
	format = FormatUSTAR | FormatPAX | FormatGNU
	paxHdrs = make(map[string]string)

	var whyNoUSTAR, whyNoPAX, whyNoGNU string
	var preferPAX bool // 优先选择 PAX 而非 USTAR
	verifyString := func(s string, size int, name, paxKey string) {
		// NUL 终止符对于 path 和 linkpath 是可选的。
		// 从技术上讲，对于 uname 和 gname 是必需的，
		// 但 GNU 和 BSD tar 都不检查它。
		tooLong := len(s) > size
		allowLongGNU := paxKey == paxPath || paxKey == paxLinkpath
		if hasNUL(s) || (tooLong && !allowLongGNU) {
			whyNoGNU = fmt.Sprintf("GNU cannot encode %s=%q", name, s)
			format.mustNotBe(FormatGNU)
		}
		if !isASCII(s) || tooLong {
			canSplitUSTAR := paxKey == paxPath
			if _, _, ok := splitUSTARPath(s); !canSplitUSTAR || !ok {
				whyNoUSTAR = fmt.Sprintf("USTAR cannot encode %s=%q", name, s)
				format.mustNotBe(FormatUSTAR)
			}
			if paxKey == paxNone {
				whyNoPAX = fmt.Sprintf("PAX cannot encode %s=%q", name, s)
				format.mustNotBe(FormatPAX)
			} else {
				paxHdrs[paxKey] = s
			}
		}
		if v, ok := h.PAXRecords[paxKey]; ok && v == s {
			paxHdrs[paxKey] = v
		}
	}
	verifyNumeric := func(n int64, size int, name, paxKey string) {
		if !fitsInBase256(size, n) {
			whyNoGNU = fmt.Sprintf("GNU cannot encode %s=%d", name, n)
			format.mustNotBe(FormatGNU)
		}
		if !fitsInOctal(size, n) {
			whyNoUSTAR = fmt.Sprintf("USTAR cannot encode %s=%d", name, n)
			format.mustNotBe(FormatUSTAR)
			if paxKey == paxNone {
				whyNoPAX = fmt.Sprintf("PAX cannot encode %s=%d", name, n)
				format.mustNotBe(FormatPAX)
			} else {
				paxHdrs[paxKey] = strconv.FormatInt(n, 10)
			}
		}
		if v, ok := h.PAXRecords[paxKey]; ok && v == strconv.FormatInt(n, 10) {
			paxHdrs[paxKey] = v
		}
	}
	verifyTime := func(ts time.Time, size int, name, paxKey string) {
		if ts.IsZero() {
			return // 总是可以
		}
		if !fitsInBase256(size, ts.Unix()) {
			whyNoGNU = fmt.Sprintf("GNU cannot encode %s=%v", name, ts)
			format.mustNotBe(FormatGNU)
		}
		isMtime := paxKey == paxMtime
		fitsOctal := fitsInOctal(size, ts.Unix())
		if (isMtime && !fitsOctal) || !isMtime {
			whyNoUSTAR = fmt.Sprintf("USTAR cannot encode %s=%v", name, ts)
			format.mustNotBe(FormatUSTAR)
		}
		needsNano := ts.Nanosecond() != 0
		if !isMtime || !fitsOctal || needsNano {
			preferPAX = true // USTAR 可能会截断亚秒级测量值
			if paxKey == paxNone {
				whyNoPAX = fmt.Sprintf("PAX cannot encode %s=%v", name, ts)
				format.mustNotBe(FormatPAX)
			} else {
				paxHdrs[paxKey] = formatPAXTime(ts)
			}
		}
		if v, ok := h.PAXRecords[paxKey]; ok && v == formatPAXTime(ts) {
			paxHdrs[paxKey] = v
		}
	}

	// 检查基本字段。
	var blk block
	v7 := blk.toV7()
	ustar := blk.toUSTAR()
	gnu := blk.toGNU()
	verifyString(h.Name, len(v7.name()), "Name", paxPath)
	verifyString(h.Linkname, len(v7.linkName()), "Linkname", paxLinkpath)
	verifyString(h.Uname, len(ustar.userName()), "Uname", paxUname)
	verifyString(h.Gname, len(ustar.groupName()), "Gname", paxGname)
	verifyNumeric(h.Mode, len(v7.mode()), "Mode", paxNone)
	verifyNumeric(int64(h.Uid), len(v7.uid()), "Uid", paxUid)
	verifyNumeric(int64(h.Gid), len(v7.gid()), "Gid", paxGid)
	verifyNumeric(h.Size, len(v7.size()), "Size", paxSize)
	verifyNumeric(h.Devmajor, len(ustar.devMajor()), "Devmajor", paxNone)
	verifyNumeric(h.Devminor, len(ustar.devMinor()), "Devminor", paxNone)
	verifyTime(h.ModTime, len(v7.modTime()), "ModTime", paxMtime)
	verifyTime(h.AccessTime, len(gnu.accessTime()), "AccessTime", paxAtime)
	verifyTime(h.ChangeTime, len(gnu.changeTime()), "ChangeTime", paxCtime)

	// 检查仅头类型。
	var whyOnlyPAX, whyOnlyGNU string
	switch h.Typeflag {
	case TypeReg, TypeChar, TypeBlock, TypeFifo, TypeGNUSparse:
		// 排除 TypeLink 和 TypeSymlink，因为它们可能引用目录。
		if strings.HasSuffix(h.Name, "/") {
			return FormatUnknown, nil, headerError{"filename may not have trailing slash"}
		}
	case TypeXHeader, TypeGNULongName, TypeGNULongLink:
		return FormatUnknown, nil, headerError{"cannot manually encode TypeXHeader, TypeGNULongName, or TypeGNULongLink headers"}
	case TypeXGlobalHeader:
		h2 := Header{Name: h.Name, Typeflag: h.Typeflag, Xattrs: h.Xattrs, PAXRecords: h.PAXRecords, Format: h.Format}
		if !reflect.DeepEqual(h, h2) {
			return FormatUnknown, nil, headerError{"only PAXRecords should be set for TypeXGlobalHeader"}
		}
		whyOnlyPAX = "only PAX supports TypeXGlobalHeader"
		format.mayOnlyBe(FormatPAX)
	}
	if !isHeaderOnlyType(h.Typeflag) && h.Size < 0 {
		return FormatUnknown, nil, headerError{"negative size on header-only type"}
	}

	// 检查 PAX 记录。
	if len(h.Xattrs) > 0 {
		for k, v := range h.Xattrs {
			paxHdrs[paxSchilyXattr+k] = v
		}
		whyOnlyPAX = "only PAX supports Xattrs"
		format.mayOnlyBe(FormatPAX)
	}
	if len(h.PAXRecords) > 0 {
		for k, v := range h.PAXRecords {
			switch _, exists := paxHdrs[k]; {
			case exists:
				continue // 不覆盖现有记录
			case h.Typeflag == TypeXGlobalHeader:
				paxHdrs[k] = v // 复制所有记录
			case !basicKeys[k] && !strings.HasPrefix(k, paxGNUSparse):
				paxHdrs[k] = v // 忽略可能冲突的本地记录
			}
		}
		whyOnlyPAX = "only PAX supports PAXRecords"
		format.mayOnlyBe(FormatPAX)
	}
	for k, v := range paxHdrs {
		if !validPAXRecord(k, v) {
			return FormatUnknown, nil, headerError{fmt.Sprintf("invalid PAX record: %q", k+" = "+v)}
		}
	}

	// TODO(dsnet)：当添加稀疏支持时重新启用此功能。
	// 参见 https://golang.org/issue/22735
	/*
		// 检查稀疏文件。
		if len(h.SparseHoles) > 0 || h.Typeflag == TypeGNUSparse {
			if isHeaderOnlyType(h.Typeflag) {
				return FormatUnknown, nil, headerError{"header-only type cannot be sparse"}
			}
			if !validateSparseEntries(h.SparseHoles, h.Size) {
				return FormatUnknown, nil, headerError{"invalid sparse holes"}
			}
			if h.Typeflag == TypeGNUSparse {
				whyOnlyGNU = "only GNU supports TypeGNUSparse"
				format.mayOnlyBe(FormatGNU)
			} else {
				whyNoGNU = "GNU supports sparse files only with TypeGNUSparse"
				format.mustNotBe(FormatGNU)
			}
			whyNoUSTAR = "USTAR does not support sparse files"
			format.mustNotBe(FormatUSTAR)
		}
	*/

	// 检查所需格式。
	if wantFormat := h.Format; wantFormat != FormatUnknown {
		if wantFormat.has(FormatPAX) && !preferPAX {
			wantFormat.mayBe(FormatUSTAR) // PAX 也允许 USTAR
		}
		format.mayOnlyBe(wantFormat) // 设置允许格式和所需格式的并集
	}
	if format == FormatUnknown {
		switch h.Format {
		case FormatUSTAR:
			err = headerError{"Format specifies USTAR", whyNoUSTAR, whyOnlyPAX, whyOnlyGNU}
		case FormatPAX:
			err = headerError{"Format specifies PAX", whyNoPAX, whyOnlyGNU}
		case FormatGNU:
			err = headerError{"Format specifies GNU", whyNoGNU, whyOnlyPAX}
		default:
			err = headerError{whyNoUSTAR, whyNoPAX, whyNoGNU, whyOnlyPAX, whyOnlyGNU}
		}
	}
	return format, paxHdrs, err
}

// FileInfo 返回 Header 的 fs.FileInfo。
func (h *Header) FileInfo() fs.FileInfo {
	return headerFileInfo{h}
}

// headerFileInfo 实现了 fs.FileInfo。
type headerFileInfo struct {
	h *Header
}

func (fi headerFileInfo) Size() int64        { return fi.h.Size }
func (fi headerFileInfo) IsDir() bool        { return fi.Mode().IsDir() }
func (fi headerFileInfo) ModTime() time.Time { return fi.h.ModTime }
func (fi headerFileInfo) Sys() any           { return fi.h }

// Name 返回文件的基本名称。
func (fi headerFileInfo) Name() string {
	if fi.IsDir() {
		return path.Base(path.Clean(fi.h.Name))
	}
	return path.Base(fi.h.Name)
}

// Mode 返回 headerFileInfo 的权限和模式位。
func (fi headerFileInfo) Mode() (mode fs.FileMode) {
	// 设置文件权限位。
	mode = fs.FileMode(fi.h.Mode).Perm()

	// 设置 setuid、setgid 和 sticky 位。
	if fi.h.Mode&c_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if fi.h.Mode&c_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if fi.h.Mode&c_ISVTX != 0 {
		mode |= fs.ModeSticky
	}

	// 设置文件模式位；清除 perm、setuid、setgid 和 sticky 位。
	switch m := fs.FileMode(fi.h.Mode) &^ 07777; m {
	case c_ISDIR:
		mode |= fs.ModeDir
	case c_ISFIFO:
		mode |= fs.ModeNamedPipe
	case c_ISLNK:
		mode |= fs.ModeSymlink
	case c_ISBLK:
		mode |= fs.ModeDevice
	case c_ISCHR:
		mode |= fs.ModeDevice
		mode |= fs.ModeCharDevice
	case c_ISSOCK:
		mode |= fs.ModeSocket
	}

	switch fi.h.Typeflag {
	case TypeSymlink:
		mode |= fs.ModeSymlink
	case TypeChar:
		mode |= fs.ModeDevice
		mode |= fs.ModeCharDevice
	case TypeBlock:
		mode |= fs.ModeDevice
	case TypeDir:
		mode |= fs.ModeDir
	case TypeFifo:
		mode |= fs.ModeNamedPipe
	}

	return mode
}

func (fi headerFileInfo) String() string {
	return fs.FormatFileInfo(fi)
}

// sysStat 如果非 nil，则从 fi 的系统相关字段填充 h。
var sysStat func(fi fs.FileInfo, h *Header, doNameLookups bool) error

const (
	// USTAR 规范中的模式常量：
	// 参见 http://pubs.opengroup.org/onlinepubs/9699919799/utilities/pax.html#tag_20_92_13_06
	c_ISUID = 04000 // 设置 uid
	c_ISGID = 02000 // 设置 gid
	c_ISVTX = 01000 // 保存文本（sticky 位）

	// 常见的 Unix 模式常量；这些未在任何常见的 tar 标准中定义。
	// Header.FileInfo 理解这些，但 FileInfoHeader 永远不会产生这些。
	c_ISDIR  = 040000  // 目录
	c_ISFIFO = 010000  // FIFO
	c_ISREG  = 0100000 // 普通文件
	c_ISLNK  = 0120000 // 符号链接
	c_ISBLK  = 060000  // 块特殊文件
	c_ISCHR  = 020000  // 字符特殊文件
	c_ISSOCK = 0140000 // 套接字
)

// FileInfoHeader 从 fi 创建一个部分填充的 [Header]。
// 如果 fi 描述一个符号链接，FileInfoHeader 将 link 记录为链接目标。
// 如果 fi 描述一个目录，则在名称后附加斜杠。
//
// 由于 fs.FileInfo 的 Name 方法仅返回它所描述文件的基本名称，
// 可能需要修改 Header.Name 以提供文件的完整路径名。
//
// 如果 fi 实现了 [FileInfoNames]，则 Header.Gname 和 Header.Uname
// 由该接口的方法提供。
func FileInfoHeader(fi fs.FileInfo, link string) (*Header, error) {
	if fi == nil {
		return nil, errors.New("archive/tar: FileInfo is nil")
	}
	fm := fi.Mode()
	h := &Header{
		Name:    fi.Name(),
		ModTime: fi.ModTime(),
		Mode:    int64(fm.Perm()), // 稍后与 c_IS* 常量进行或运算
	}
	switch {
	case fm.IsRegular():
		h.Typeflag = TypeReg
		h.Size = fi.Size()
	case fi.IsDir():
		h.Typeflag = TypeDir
		h.Name += "/"
	case fm&fs.ModeSymlink != 0:
		h.Typeflag = TypeSymlink
		h.Linkname = link
	case fm&fs.ModeDevice != 0:
		if fm&fs.ModeCharDevice != 0 {
			h.Typeflag = TypeChar
		} else {
			h.Typeflag = TypeBlock
		}
	case fm&fs.ModeNamedPipe != 0:
		h.Typeflag = TypeFifo
	case fm&fs.ModeSocket != 0:
		return nil, fmt.Errorf("archive/tar: sockets not supported")
	default:
		return nil, fmt.Errorf("archive/tar: unknown file mode %v", fm)
	}
	if fm&fs.ModeSetuid != 0 {
		h.Mode |= c_ISUID
	}
	if fm&fs.ModeSetgid != 0 {
		h.Mode |= c_ISGID
	}
	if fm&fs.ModeSticky != 0 {
		h.Mode |= c_ISVTX
	}
	// 如果可能，从特定于操作系统的 FileInfo 字段填充其他字段。
	if sys, ok := fi.Sys().(*Header); ok {
		// 此 FileInfo 来自 Header（而非操作系统）。使用原始 Header 填充所有剩余字段。
		h.Uid = sys.Uid
		h.Gid = sys.Gid
		h.Uname = sys.Uname
		h.Gname = sys.Gname
		h.AccessTime = sys.AccessTime
		h.ChangeTime = sys.ChangeTime
		h.Xattrs = maps.Clone(sys.Xattrs)
		if sys.Typeflag == TypeLink {
			// 硬链接
			h.Typeflag = TypeLink
			h.Size = 0
			h.Linkname = sys.Linkname
		}
		h.PAXRecords = maps.Clone(sys.PAXRecords)
	}
	var doNameLookups = true
	if iface, ok := fi.(FileInfoNames); ok {
		doNameLookups = false
		var err error
		h.Gname, err = iface.Gname()
		if err != nil {
			return nil, err
		}
		h.Uname, err = iface.Uname()
		if err != nil {
			return nil, err
		}
	}
	if sysStat != nil {
		return h, sysStat(fi, h, doNameLookups)
	}
	return h, nil
}

// FileInfoNames 扩展了 [fs.FileInfo]。
// 将其实例传递给 [FileInfoHeader] 允许调用者通过直接指定 Uname 和 Gname 来避免系统相关的名称查找。
type FileInfoNames interface {
	fs.FileInfo
	// Uname 应返回用户名。
	Uname() (string, error)
	// Gname 应返回组名。
	Gname() (string, error)
}

// isHeaderOnlyType 检查给定的类型标志是否为即使指定了大小也没有数据部分的类型。
func isHeaderOnlyType(flag byte) bool {
	switch flag {
	case TypeLink, TypeSymlink, TypeChar, TypeBlock, TypeDir, TypeFifo:
		return true
	default:
		return false
	}
}
