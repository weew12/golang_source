// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 解析 "zoneinfo" 时区文件。
// 这是 OS X、Linux、BSD、Sun 及其他系统上使用的一种相当标准的文件格式。
// 参见 tzfile(5)、https://en.wikipedia.org/wiki/Zoneinfo，
// 以及 ftp://munnari.oz.au/pub/oldtz/

package time

import (
	"errors"
	"internal/bytealg"
	"runtime"
	"syscall"
	_ "unsafe" // 用于 linkname
)

// registerLoadFromEmbeddedTZData 由 time/tzdata 包调用（如果该包已被导入）。
//
//go:linkname registerLoadFromEmbeddedTZData
func registerLoadFromEmbeddedTZData(f func(string) (string, error)) {
	loadFromEmbeddedTZData = f
}

// loadFromEmbeddedTZData 用于从嵌入到二进制文件自身的 tzdata 信息中
// 加载特定的 tzdata 文件。
// 当 time/tzdata 包被导入时，会通过 registerLoadFromEmbeddedTzdata
// 设置它。
var loadFromEmbeddedTZData func(zipname string) (string, error)

// maxFileSize 是 readFile 读取文件所允许的最大大小。
// 作为参考，Go 分发的 zoneinfo.zip 约为 350 KB，
// 因此 10MB 是绰绰有余的。
const maxFileSize = 10 << 20

type fileSizeError string

func (f fileSizeError) Error() string {
	return "time: file " + string(f) + " is too large"
}

// io.Seek* 常量的副本，以避免导入 "io"：
const (
	seekStart   = 0
	seekCurrent = 1
	seekEnd     = 2
)

// 用于二进制数据块的简单 I/O 接口。
type dataIO struct {
	p     []byte
	error bool
}

func (d *dataIO) read(n int) []byte {
	if len(d.p) < n {
		d.p = nil
		d.error = true
		return nil
	}
	p := d.p[0:n]
	d.p = d.p[n:]
	return p
}

func (d *dataIO) big4() (n uint32, ok bool) {
	p := d.read(4)
	if len(p) < 4 {
		d.error = true
		return 0, false
	}
	return uint32(p[3]) | uint32(p[2])<<8 | uint32(p[1])<<16 | uint32(p[0])<<24, true
}

func (d *dataIO) big8() (n uint64, ok bool) {
	n1, ok1 := d.big4()
	n2, ok2 := d.big4()
	if !ok1 || !ok2 {
		d.error = true
		return 0, false
	}
	return (uint64(n1) << 32) | uint64(n2), true
}

func (d *dataIO) byte() (n byte, ok bool) {
	p := d.read(1)
	if len(p) < 1 {
		d.error = true
		return 0, false
	}
	return p[0], true
}

// rest 返回缓冲区中剩余的数据。
func (d *dataIO) rest() []byte {
	r := d.p
	d.p = nil
	return r
}

// 在第一个 NUL 处停止以生成字符串
func byteString(p []byte) string {
	if i := bytealg.IndexByte(p, 0); i != -1 {
		p = p[:i]
	}
	return string(p)
}

var errBadData = errors.New("malformed time zone information")

// LoadLocationFromTZData 返回一个具有给定名称的 Location，
// 该 Location 由 IANA 时区数据库格式的数据初始化。
// 数据应采用标准 IANA 时区文件的格式
// （例如 Unix 系统上 /etc/localtime 的内容）。
func LoadLocationFromTZData(name string, data []byte) (*Location, error) {
	d := dataIO{data, false}

	// 4 字节魔数 "TZif"
	if magic := d.read(4); string(magic) != "TZif" {
		return nil, errBadData
	}

	// 1 字节版本，然后是 15 字节填充
	var version int
	var p []byte
	if p = d.read(16); len(p) != 16 {
		return nil, errBadData
	} else {
		switch p[0] {
		case 0:
			version = 1
		case '2':
			version = 2
		case '3':
			version = 3
		default:
			return nil, errBadData
		}
	}

	// 六个大端序 32 位整数：
	//	UTC/本地指示符的数量
	//	标准/墙钟指示符的数量
	//	闰秒的数量
	//	转换时间的数量
	//	本地时区的数量
	//	时区缩写字符串的字符数量
	const (
		NUTCLocal = iota
		NStdWall
		NLeap
		NTime
		NZone
		NChar
	)
	var n [6]int
	for i := 0; i < 6; i++ {
		nn, ok := d.big4()
		if !ok {
			return nil, errBadData
		}
		if uint32(int(nn)) != nn {
			return nil, errBadData
		}
		n[i] = int(nn)
	}

	// 如果版本为 2 或 3，则数据先以 32 位格式写出，
	// 然后再以 64 位格式写出一遍。
	// 跳过 32 位格式并读取 64 位格式，因为它能
	// 描述更宽泛的日期范围。

	is64 := false
	if version > 1 {
		// 跳过 32 位数据。
		skip := n[NTime]*4 +
			n[NTime] +
			n[NZone]*6 +
			n[NChar] +
			n[NLeap]*8 +
			n[NStdWall] +
			n[NUTCLocal]
		// 跳过我们刚刚读取的版本 2 头部。
		skip += 4 + 16
		d.read(skip)

		is64 = true

		// 再次读取各计数，它们可能不同。
		for i := 0; i < 6; i++ {
			nn, ok := d.big4()
			if !ok {
				return nil, errBadData
			}
			if uint32(int(nn)) != nn {
				return nil, errBadData
			}
			n[i] = int(nn)
		}
	}

	size := 4
	if is64 {
		size = 8
	}

	// 转换时间。
	txtimes := dataIO{d.read(n[NTime] * size), false}

	// 转换时间对应的时区索引。
	txzones := d.read(n[NTime])

	// 时区信息结构
	zonedata := dataIO{d.read(n[NZone] * 6), false}

	// 时区缩写。
	abbrev := d.read(n[NChar])

	// 闰秒时间对
	d.read(n[NLeap] * (size + 4))

	// 与本地时间类型相关联的 tx 时间
	// 是被指定为标准时间还是墙钟时间。
	isstd := d.read(n[NStdWall])

	// 与本地时间类型相关联的 tx 时间
	// 是被指定为 UTC 还是本地时间。
	isutc := d.read(n[NUTCLocal])

	if d.error { // 数据耗尽
		return nil, errBadData
	}

	var extend string
	rest := d.rest()
	if len(rest) > 2 && rest[0] == '\n' && rest[len(rest)-1] == '\n' {
		extend = string(rest[1 : len(rest)-1])
	}

	// 现在我们就可以构建有用的数据结构了。
	// 首先是时区信息。
	//	utcoff[4] isdst[1] nameindex[1]
	nzone := n[NZone]
	if nzone == 0 {
		// 拒绝没有任何时区的 tzdata 文件。它们没有任何有用的内容。
		// 这还能避免之后在我们添加并使用一个伪造转换时发生 panic（golang.org/issue/29437）。
		return nil, errBadData
	}
	zones := make([]zone, nzone)
	for i := range zones {
		var ok bool
		var n uint32
		if n, ok = zonedata.big4(); !ok {
			return nil, errBadData
		}
		if uint32(int(n)) != n {
			return nil, errBadData
		}
		zones[i].offset = int(int32(n))
		var b byte
		if b, ok = zonedata.byte(); !ok {
			return nil, errBadData
		}
		zones[i].isDST = b != 0
		if b, ok = zonedata.byte(); !ok || int(b) >= len(abbrev) {
			return nil, errBadData
		}
		zones[i].name = byteString(abbrev[b:])
		if runtime.GOOS == "aix" && len(name) > 8 && (name[:8] == "Etc/GMT+" || name[:8] == "Etc/GMT-") {
			// AIX 7.2 TL 0 在处理 Etc 中的文件时有一个 bug，
			// GMT+1 会返回 GMT-1，而不是 GMT+1 或 -01。
			if name != "Etc/GMT+0" {
				// GMT+0 是正常的
				zones[i].name = name[4:]
			}
		}
	}

	// 现在是转换时间信息。
	tx := make([]zoneTrans, n[NTime])
	for i := range tx {
		var n int64
		if !is64 {
			if n4, ok := txtimes.big4(); !ok {
				return nil, errBadData
			} else {
				n = int64(int32(n4))
			}
		} else {
			if n8, ok := txtimes.big8(); !ok {
				return nil, errBadData
			} else {
				n = int64(n8)
			}
		}
		tx[i].when = n
		if int(txzones[i]) >= len(zones) {
			return nil, errBadData
		}
		tx[i].index = txzones[i]
		if i < len(isstd) {
			tx[i].isstd = isstd[i] != 0
		}
		if i < len(isutc) {
			tx[i].isutc = isutc[i] != 0
		}
	}

	if len(tx) == 0 {
		// 构建伪造转换以覆盖所有时间。
		// 这种情况发生在像 "Etc/GMT0" 这样的固定位置。
		tx = append(tx, zoneTrans{when: alpha, index: 0})
	}

	// 至此已确保成功。
	l := &Location{zone: zones, tx: tx, name: name, extend: extend}

	// 用当前此刻的信息填充缓存，
	// 因为这将是最常见的查找。
	sec, _, _ := runtimeNow()
	for i := range tx {
		if tx[i].when <= sec && (i+1 == len(tx) || sec < tx[i+1].when) {
			l.cacheStart = tx[i].when
			l.cacheEnd = omega
			l.cacheZone = &l.zone[tx[i].index]
			if i+1 < len(tx) {
				l.cacheEnd = tx[i+1].when
			} else if l.extend != "" {
				// 如果我们处于已知时区转换的末尾，
				// 就尝试 extend 字符串。
				if name, offset, estart, eend, isDST, ok := tzset(l.extend, l.cacheStart, sec); ok {
					l.cacheStart = estart
					l.cacheEnd = eend
					// 查找由 tzset 返回的时区，以尽可能避免分配。
					if zoneIdx := findZone(l.zone, name, offset, isDST); zoneIdx != -1 {
						l.cacheZone = &l.zone[zoneIdx]
					} else {
						l.cacheZone = &zone{
							name:   name,
							offset: offset,
							isDST:  isDST,
						}
					}
				}
			}
			break
		}
	}

	return l, nil
}

func findZone(zones []zone, name string, offset int, isDST bool) int {
	for i, z := range zones {
		if z.name == name && z.offset == offset && z.isDST == isDST {
			return i
		}
	}
	return -1
}

// loadTzinfoFromDirOrZip 返回 dir 中具有给定名称的文件的内容。
// dir 可以是未压缩的 zip 文件，也可以是一个目录。
func loadTzinfoFromDirOrZip(dir, name string) ([]byte, error) {
	if len(dir) > 4 && dir[len(dir)-4:] == ".zip" {
		return loadTzinfoFromZip(dir, name)
	}
	if dir != "" {
		name = dir + "/" + name
	}
	return readFile(name)
}

// 有 500 多个 zoneinfo 文件。与其逐个分发它们，
// 我们将它们放在一个未压缩的 zip 文件中一起发布。
// 以这种方式使用时，zip 文件格式充当了各个小文件的
// 一种通用可读容器。我们选择 zip 而不是 tar，
// 因为 zip 文件具有连续的内容表，使得单个文件的查找更快，
// 而且 zip 文件中每个文件的额外开销远小于 tar 的 512 字节。

// get4 返回 b 中的小端序 32 位值。
func get4(b []byte) int {
	if len(b) < 4 {
		return 0
	}
	return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24
}

// get2 返回 b 中的小端序 16 位值。
func get2(b []byte) int {
	if len(b) < 2 {
		return 0
	}
	return int(b[0]) | int(b[1])<<8
}

// loadTzinfoFromZip 返回给定的未压缩 zip 文件中
// 具有给定名称的文件的内容。
func loadTzinfoFromZip(zipfile, name string) ([]byte, error) {
	fd, err := open(zipfile)
	if err != nil {
		return nil, err
	}
	defer closefd(fd)

	const (
		zecheader = 0x06054b50
		zcheader  = 0x02014b50
		ztailsize = 22

		zheadersize = 30
		zheader     = 0x04034b50
	)

	buf := make([]byte, ztailsize)
	if err := preadn(fd, buf, -ztailsize); err != nil || get4(buf) != zecheader {
		return nil, errors.New("corrupt zip file " + zipfile)
	}
	n := get2(buf[10:])
	size := get4(buf[12:])
	off := get4(buf[16:])

	buf = make([]byte, size)
	if err := preadn(fd, buf, off); err != nil {
		return nil, errors.New("corrupt zip file " + zipfile)
	}

	for i := 0; i < n; i++ {
		// zip entry layout:
		//	0	magic[4]
		//	4	madevers[1]
		//	5	madeos[1]
		//	6	extvers[1]
		//	7	extos[1]
		//	8	flags[2]
		//	10	meth[2]
		//	12	modtime[2]
		//	14	moddate[2]
		//	16	crc[4]
		//	20	csize[4]
		//	24	uncsize[4]
		//	28	namelen[2]
		//	30	xlen[2]
		//	32	fclen[2]
		//	34	disknum[2]
		//	36	iattr[2]
		//	38	eattr[4]
		//	42	off[4]
		//	46	name[namelen]
		//	46+namelen+xlen+fclen - next header
		//
		if get4(buf) != zcheader {
			break
		}
		meth := get2(buf[10:])
		size := get4(buf[24:])
		namelen := get2(buf[28:])
		xlen := get2(buf[30:])
		fclen := get2(buf[32:])
		off := get4(buf[42:])
		zname := buf[46 : 46+namelen]
		buf = buf[46+namelen+xlen+fclen:]
		if string(zname) != name {
			continue
		}
		if meth != 0 {
			return nil, errors.New("unsupported compression for " + name + " in " + zipfile)
		}

		// zip per-file header layout:
		//	0	magic[4]
		//	4	extvers[1]
		//	5	extos[1]
		//	6	flags[2]
		//	8	meth[2]
		//	10	modtime[2]
		//	12	moddate[2]
		//	14	crc[4]
		//	18	csize[4]
		//	22	uncsize[4]
		//	26	namelen[2]
		//	28	xlen[2]
		//	30	name[namelen]
		//	30+namelen+xlen - file data
		//
		buf = make([]byte, zheadersize+namelen)
		if err := preadn(fd, buf, off); err != nil ||
			get4(buf) != zheader ||
			get2(buf[8:]) != meth ||
			get2(buf[26:]) != namelen ||
			string(buf[30:30+namelen]) != name {
			return nil, errors.New("corrupt zip file " + zipfile)
		}
		xlen = get2(buf[28:])

		buf = make([]byte, size)
		if err := preadn(fd, buf, off+30+namelen+xlen); err != nil {
			return nil, errors.New("corrupt zip file " + zipfile)
		}

		return buf, nil
	}

	return nil, syscall.ENOENT
}

// loadTzinfoFromTzdata 从通常见于 android 的 tzdata 数据库文件中，
// 返回具有给定名称的时区信息。
var loadTzinfoFromTzdata func(file, name string) ([]byte, error)

// loadTzinfo 从给定来源返回具有给定名称的时区信息。
// 来源可以是时区数据库目录、tzdata 数据库文件，
// 或包含此类目录内容的未压缩 zip 文件。
func loadTzinfo(name string, source string) ([]byte, error) {
	if len(source) >= 6 && source[len(source)-6:] == "tzdata" {
		return loadTzinfoFromTzdata(source, name)
	}
	return loadTzinfoFromDirOrZip(source, name)
}

// loadLocation 从指定来源之一返回具有给定名称的 Location。
// 支持的来源列表参见 loadTzinfo。
// 与给定名称匹配且成功加载并解析的第一个时区数据
// 将作为 Location 返回。
func loadLocation(name string, sources []string) (z *Location, firstErr error) {
	for _, source := range sources {
		zoneData, err := loadTzinfo(name, source)
		if err == nil {
			if z, err = LoadLocationFromTZData(name, zoneData); err == nil {
				return z, nil
			}
		}
		if firstErr == nil && err != syscall.ENOENT {
			firstErr = err
		}
	}
	if loadFromEmbeddedTZData != nil {
		zoneData, err := loadFromEmbeddedTZData(name)
		if err == nil {
			if z, err = LoadLocationFromTZData(name, []byte(zoneData)); err == nil {
				return z, nil
			}
		}
		if firstErr == nil && err != syscall.ENOENT {
			firstErr = err
		}
	}
	if source, ok := gorootZoneSource(runtime.GOROOT()); ok {
		zoneData, err := loadTzinfo(name, source)
		if err == nil {
			if z, err = LoadLocationFromTZData(name, zoneData); err == nil {
				return z, nil
			}
		}
		if firstErr == nil && err != syscall.ENOENT {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, errors.New("unknown time zone " + name)
}

// readFile 读取并返回指定名称文件的内容。
// 它是 os.ReadFile 的一个简单实现，在此重新实现
// 以避免依赖 io/ioutil 或 os。
// 如果 name 超过 maxFileSize 字节，它会返回一个错误。
func readFile(name string) ([]byte, error) {
	f, err := open(name)
	if err != nil {
		return nil, err
	}
	defer closefd(f)
	var (
		buf [4096]byte
		ret []byte
		n   int
	)
	for {
		n, err = read(f, buf[:])
		if n > 0 {
			ret = append(ret, buf[:n]...)
		}
		if n == 0 || err != nil {
			break
		}
		if len(ret) > maxFileSize {
			return nil, fileSizeError(name)
		}
	}
	return ret, err
}
