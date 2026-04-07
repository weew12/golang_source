// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
zip 包提供对 ZIP 归档文件的读写支持。

详情参见 [ZIP 规范]。

本包不支持磁盘跨卷存储。

关于 ZIP64 的说明：

为保持向后兼容，FileHeader 同时包含 32 位和 64 位大小字段。
64 位字段始终存储正确数值，普通归档文件中两类字段值一致。
对于需要使用 ZIP64 格式的文件，32 位字段会设为 0xffffffff，此时必须使用 64 位字段。

[ZIP specification]: https://support.pkware.com/pkzip/appnote
*/
package zip

import (
	"io/fs"
	"path"
	"time"
)

// 压缩方法
const (
	Store   uint16 = 0 // 无压缩
	Deflate uint16 = 8 // DEFLATE 压缩
)

const (
	fileHeaderSignature      = 0x04034b50
	directoryHeaderSignature = 0x02014b50
	directoryEndSignature    = 0x06054b50
	directory64LocSignature  = 0x07064b50
	directory64EndSignature  = 0x06064b50
	dataDescriptorSignature  = 0x08074b50 // 事实标准；OS X Finder 要求此字段
	fileHeaderLen            = 30         // + 文件名 + 额外数据
	directoryHeaderLen       = 46         // + 文件名 + 额外数据 + 注释
	directoryEndLen          = 22         // + 注释
	dataDescriptorLen        = 16         // 四个 uint32：描述符签名、crc32、压缩后大小、原始大小
	dataDescriptor64Len      = 24         // 两个 uint32：签名、crc32 | 两个 uint64：压缩后大小、原始大小
	directory64LocLen        = 20         //
	directory64EndLen        = 56         // + 额外数据

	// CreatorVersion 首字节的常量
	creatorFAT    = 0
	creatorUnix   = 3
	creatorNTFS   = 11
	creatorVFAT   = 14
	creatorMacOSX = 19

	// 版本号
	zipVersion20 = 20 // 2.0 版本
	zipVersion45 = 45 // 4.5 版本（支持 ZIP64 归档读写）

	// 非 ZIP64 文件的大小限制
	uint16max = (1 << 16) - 1
	uint32max = (1 << 32) - 1

	// 额外头部 ID
	//
	// ID 0~31 为 PKWARE 官方保留使用
	// 超出该范围的 ID 由第三方厂商定义
	// 由于 ZIP 格式缺乏高精度时间戳（也未官方指定日期字段的时区），
	// 诸多 competing 额外字段被设计出来，广泛使用后事实上成为“官方”标准
	//
	// 参见 http://mdfs.net/Docs/Comp/Archiving/Zip/ExtraField
	zip64ExtraID       = 0x0001 // Zip64 扩展信息
	ntfsExtraID        = 0x000a // NTFS 格式
	unixExtraID        = 0x000d // UNIX 格式
	extTimeExtraID     = 0x5455 // 扩展时间戳
	infoZipUnixExtraID = 0x5855 // Info-ZIP Unix 扩展
)

// FileHeader 描述 ZIP 文件中的单个文件条目
// 详情参见 [ZIP 规范]
//
// [ZIP specification]: https://support.pkware.com/pkzip/appnote
type FileHeader struct {
	// Name 为文件名称
	//
	// 必须为相对路径，不能以盘符（如 C:）开头，
	// 且必须使用正斜杠而非反斜杠。末尾带斜杠表示该条目为目录，不应包含数据
	Name string

	// Comment 为任意用户自定义字符串，长度小于 64KiB
	Comment string

	// NonUTF8 标识 Name 和 Comment 未使用 UTF-8 编码
	//
	// 按规范，唯一允许的其他编码应为 CP-437，
	// 但历史上很多 ZIP 读取器会将 Name 和 Comment 按系统本地字符编码解析
	//
	// 仅当用户需要为特定本地化区域生成非可移植 ZIP 文件时，才应设置该标志。
	// 其他情况下，Writer 会为合法 UTF-8 字符串自动设置 ZIP 格式的 UTF-8 标志
	NonUTF8 bool

	CreatorVersion uint16
	ReaderVersion  uint16
	Flags          uint16

	// Method 为压缩方法，为 0 时使用 Store（无压缩）
	Method uint16

	// Modified 为文件的修改时间
	//
	// 读取时，优先使用扩展时间戳而非旧版 MS-DOS 日期字段，
	// 时间差值会作为时区偏移。若仅存在 MS-DOS 日期，则时区默认为 UTC
	//
	// 写入时，始终生成与时区无关的扩展时间戳。
	// 旧版 MS-DOS 日期字段会按 Modified 时间的所在时区编码
	Modified time.Time

	// ModifiedTime 为 MS-DOS 编码格式的时间
	//
	// 已弃用：请改用 Modified
	ModifiedTime uint16

	// ModifiedDate 为 MS-DOS 编码格式的日期
	//
	// 已弃用：请改用 Modified
	ModifiedDate uint16

	// CRC32 为文件内容的 CRC32 校验和
	CRC32 uint32

	// CompressedSize 为文件压缩后的字节大小
	// 若文件压缩前/压缩后大小超出 32 位范围，该字段会设为 ^uint32(0)
	//
	// 已弃用：请改用 CompressedSize64
	CompressedSize uint32

	// UncompressedSize 为文件未压缩的字节大小
	// 若文件压缩前/压缩后大小超出 32 位范围，该字段会设为 ^uint32(0)
	//
	// 已弃用：请改用 UncompressedSize64
	UncompressedSize uint32

	// CompressedSize64 为文件压缩后的字节大小
	CompressedSize64 uint64

	// UncompressedSize64 为文件未压缩的字节大小
	UncompressedSize64 uint64

	Extra         []byte
	ExternalAttrs uint32 // 含义取决于 CreatorVersion
}

// FileInfo 返回该 FileHeader 对应的 fs.FileInfo
func (h *FileHeader) FileInfo() fs.FileInfo {
	return headerFileInfo{h}
}

// headerFileInfo 实现 fs.FileInfo 接口
type headerFileInfo struct {
	fh *FileHeader
}

func (fi headerFileInfo) Name() string { return path.Base(fi.fh.Name) }
func (fi headerFileInfo) Size() int64 {
	if fi.fh.UncompressedSize64 > 0 {
		return int64(fi.fh.UncompressedSize64)
	}
	return int64(fi.fh.UncompressedSize)
}
func (fi headerFileInfo) IsDir() bool { return fi.Mode().IsDir() }
func (fi headerFileInfo) ModTime() time.Time {
	if fi.fh.Modified.IsZero() {
		return fi.fh.ModTime()
	}
	return fi.fh.Modified.UTC()
}
func (fi headerFileInfo) Mode() fs.FileMode { return fi.fh.Mode() }
func (fi headerFileInfo) Type() fs.FileMode { return fi.fh.Mode().Type() }
func (fi headerFileInfo) Sys() any          { return fi.fh }

func (fi headerFileInfo) Info() (fs.FileInfo, error) { return fi, nil }

func (fi headerFileInfo) String() string {
	return fs.FormatFileInfo(fi)
}

// FileInfoHeader 从 fs.FileInfo 创建一个部分填充的 FileHeader
// 由于 fs.FileInfo 的 Name 方法仅返回文件的基础名称，
// 可能需要修改返回头部的 Name 字段以设置文件完整路径
// 若需要压缩，调用方应设置 FileHeader.Method 字段，该字段默认为空
func FileInfoHeader(fi fs.FileInfo) (*FileHeader, error) {
	size := fi.Size()
	fh := &FileHeader{
		Name:               fi.Name(),
		UncompressedSize64: uint64(size),
	}
	fh.SetModTime(fi.ModTime())
	fh.SetMode(fi.Mode())
	if fh.UncompressedSize64 > uint32max {
		fh.UncompressedSize = uint32max
	} else {
		fh.UncompressedSize = uint32(fh.UncompressedSize64)
	}
	return fh, nil
}

type directoryEnd struct {
	diskNbr            uint32 // 未使用
	dirDiskNbr         uint32 // 未使用
	dirRecordsThisDisk uint64 // 未使用
	directoryRecords   uint64
	directorySize      uint64
	directoryOffset    uint64 // 相对于文件起始位置
	commentLen         uint16
	comment            string
}

// timeZone 根据给定的偏移量返回对应的 *time.Location
// 若偏移量无效，则使用 0 偏移量
func timeZone(offset time.Duration) *time.Location {
	const (
		minOffset   = -12 * time.Hour  // 例如贝克岛，时区 -12:00
		maxOffset   = +14 * time.Hour  // 例如莱恩岛，时区 +14:00
		offsetAlias = 15 * time.Minute // 例如尼泊尔，时区 +5:45
	)
	offset = offset.Round(offsetAlias)
	if offset < minOffset || maxOffset < offset {
		offset = 0
	}
	return time.FixedZone("", int(offset/time.Second))
}

// msDosTimeToTime 将 MS-DOS 日期时间转换为 time.Time
// 精度为 2 秒
// 参见：https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-dosdatetimetofiletime
func msDosTimeToTime(dosDate, dosTime uint16) time.Time {
	return time.Date(
		// 日期位：0-4 日；5-8 月；9-15 1980 年起的年份
		int(dosDate>>9+1980),
		time.Month(dosDate>>5&0xf),
		int(dosDate&0x1f),

		// 时间位：0-4 秒/2；5-10 分；11-15 时
		int(dosTime>>11),
		int(dosTime>>5&0x3f),
		int(dosTime&0x1f*2),
		0, // 纳秒

		time.UTC,
	)
}

// timeToMsDosTime 将 time.Time 转换为 MS-DOS 日期时间
// 精度为 2 秒
// 参见：https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-filetimetodosdatetime
func timeToMsDosTime(t time.Time) (fDate uint16, fTime uint16) {
	fDate = uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	fTime = uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	return
}

// ModTime 通过旧版 ModifiedDate 和 ModifiedTime 字段返回 UTC 格式的修改时间
//
// 已弃用：请改用 Modified
func (h *FileHeader) ModTime() time.Time {
	return msDosTimeToTime(h.ModifiedDate, h.ModifiedTime)
}

// SetModTime 将 Modified、ModifiedTime 和 ModifiedDate 字段设置为给定的 UTC 时间
//
// 已弃用：请改用 Modified
func (h *FileHeader) SetModTime(t time.Time) {
	t = t.UTC() // 转换为 UTC 以保证兼容性
	h.Modified = t
	h.ModifiedDate, h.ModifiedTime = timeToMsDosTime(t)
}

const (
	// Unix 相关常量。规范中未提及，但各类工具均约定使用这些值
	s_IFMT   = 0xf000
	s_IFSOCK = 0xc000
	s_IFLNK  = 0xa000
	s_IFREG  = 0x8000
	s_IFBLK  = 0x6000
	s_IFDIR  = 0x4000
	s_IFCHR  = 0x2000
	s_IFIFO  = 0x1000
	s_ISUID  = 0x800
	s_ISGID  = 0x400
	s_ISVTX  = 0x200

	msdosDir      = 0x10
	msdosReadOnly = 0x01
)

// Mode 返回 FileHeader 的权限与模式位
func (h *FileHeader) Mode() (mode fs.FileMode) {
	switch h.CreatorVersion >> 8 {
	case creatorUnix, creatorMacOSX:
		mode = unixModeToFileMode(h.ExternalAttrs >> 16)
	case creatorNTFS, creatorVFAT, creatorFAT:
		mode = msdosModeToFileMode(h.ExternalAttrs)
	}
	if len(h.Name) > 0 && h.Name[len(h.Name)-1] == '/' {
		mode |= fs.ModeDir
	}
	return mode
}

// SetMode 修改 FileHeader 的权限与模式位
func (h *FileHeader) SetMode(mode fs.FileMode) {
	h.CreatorVersion = h.CreatorVersion&0xff | creatorUnix<<8
	h.ExternalAttrs = fileModeToUnixMode(mode) << 16

	// 同时设置 MSDOS 属性，与原始 zip 工具行为一致
	if mode&fs.ModeDir != 0 {
		h.ExternalAttrs |= msdosDir
	}
	if mode&0200 == 0 {
		h.ExternalAttrs |= msdosReadOnly
	}
}

// isZip64 判断文件大小是否超出 32 位限制
func (h *FileHeader) isZip64() bool {
	return h.CompressedSize64 >= uint32max || h.UncompressedSize64 >= uint32max
}

// hasDataDescriptor 判断文件头部是否包含数据描述符
func (h *FileHeader) hasDataDescriptor() bool {
	return h.Flags&0x8 != 0
}

func msdosModeToFileMode(m uint32) (mode fs.FileMode) {
	if m&msdosDir != 0 {
		mode = fs.ModeDir | 0777
	} else {
		mode = 0666
	}
	if m&msdosReadOnly != 0 {
		mode &^= 0222
	}
	return mode
}

func fileModeToUnixMode(mode fs.FileMode) uint32 {
	var m uint32
	switch mode & fs.ModeType {
	default:
		m = s_IFREG
	case fs.ModeDir:
		m = s_IFDIR
	case fs.ModeSymlink:
		m = s_IFLNK
	case fs.ModeNamedPipe:
		m = s_IFIFO
	case fs.ModeSocket:
		m = s_IFSOCK
	case fs.ModeDevice:
		m = s_IFBLK
	case fs.ModeDevice | fs.ModeCharDevice:
		m = s_IFCHR
	}
	if mode&fs.ModeSetuid != 0 {
		m |= s_ISUID
	}
	if mode&fs.ModeSetgid != 0 {
		m |= s_ISGID
	}
	if mode&fs.ModeSticky != 0 {
		m |= s_ISVTX
	}
	return m | uint32(mode&0777)
}

func unixModeToFileMode(m uint32) fs.FileMode {
	mode := fs.FileMode(m & 0777)
	switch m & s_IFMT {
	case s_IFBLK:
		mode |= fs.ModeDevice
	case s_IFCHR:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case s_IFDIR:
		mode |= fs.ModeDir
	case s_IFIFO:
		mode |= fs.ModeNamedPipe
	case s_IFLNK:
		mode |= fs.ModeSymlink
	case s_IFREG:
		// 无需额外处理
	case s_IFSOCK:
		mode |= fs.ModeSocket
	}
	if m&s_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if m&s_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if m&s_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	return mode
}
