// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	"errors"
	"internal/syscall/windows/registry"
	"syscall"
)

var platformZoneSources []string // 无：Windows 改用系统调用

// TODO(rsc): 回退到使用 zoneinfo 文件的副本。

// BUG(brainman,rsc): 在 Windows 上，操作系统不提供完整的
// 时区信息。
// 该实现假设今年的夏令时规则
// 同样适用于所有过去和未来的年份。

// matchZoneKey 检查 stdname 和 dstname 是否与存储在
// 已打开的注册表键 zones 下的 kname 键中对应的键值
// "MUI_Std" 和 "MUI_Dlt" 或 "Std" 和 "Dlt" 匹配。
func matchZoneKey(zones registry.Key, kname string, stdname, dstname string) (matched bool, err2 error) {
	k, err := registry.OpenKey(zones, kname, registry.READ)
	if err != nil {
		return false, err
	}
	defer k.Close()

	var std, dlt string
	// 先尝试 MUI_Std 和 MUI_Dlt，如果发生*任何*错误则回退到 Std 和 Dlt
	std, err = k.GetMUIStringValue("MUI_Std")
	if err == nil {
		dlt, err = k.GetMUIStringValue("MUI_Dlt")
	}
	if err != nil { // 回退到 Std 和 Dlt
		if std, _, err = k.GetStringValue("Std"); err != nil {
			return false, err
		}
		if dlt, _, err = k.GetStringValue("Dlt"); err != nil {
			return false, err
		}
	}

	if std != stdname {
		return false, nil
	}
	if dlt != dstname && dstname != stdname {
		return false, nil
	}
	return true, nil
}

// toEnglishName 在注册表中搜索一个时区的英文名称，
// 该时区的 zone 名称为 stdname 和 dstname，并返回该英文名称。
func toEnglishName(stdname, dstname string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Time Zones`, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames()
	if err != nil {
		return "", err
	}
	for _, name := range names {
		matched, err := matchZoneKey(k, name, stdname, dstname)
		if err == nil && matched {
			return name, nil
		}
	}
	return "", errors.New(`English name for time zone "` + stdname + `" not found in registry`)
}

// extractCAPS 从描述 desc 中提取大写字母。
func extractCAPS(desc string) string {
	var short []rune
	for _, c := range desc {
		if 'A' <= c && c <= 'Z' {
			short = append(short, c)
		}
	}
	return string(short)
}

// abbrev 返回用于给定 zone z 的缩写。
func abbrev(z *syscall.Timezoneinformation) (std, dst string) {
	stdName := syscall.UTF16ToString(z.StandardName[:])
	a, ok := abbrs[stdName]
	if !ok {
		dstName := syscall.UTF16ToString(z.DaylightName[:])
		// 也许 stdName 不是英文。尝试转换它。
		englishName, err := toEnglishName(stdName, dstName)
		if err == nil {
			a, ok = abbrs[englishName]
			if ok {
				return a.std, a.dst
			}
		}
		// 回退到使用大写字母
		return extractCAPS(stdName), extractCAPS(dstName)
	}
	return a.std, a.dst
}

// pseudoUnix 返回由给定年份中的系统日期+时间 d 表示的
// 伪 Unix 时间（自 1970 年 1 月 1 日 *本地时间* 起的秒数）。
// 由调用者负责将此本地时间转换为基于 UTC 的时间。
func pseudoUnix(year int, d *syscall.Systemtime) int64 {
	// Windows 以 "月中的日" 格式指定夏令时信息：
	// d.Month 是月份编号（1-12）
	// d.DayOfWeek 是相应的星期几（星期日=0 到星期六=6）
	// d.Day 是月内的第几周（1 到 5，其中 5 表示该月的最后一周）
	// d.Hour、d.Minute 和 d.Second 是绝对时间
	day := 1
	t := Date(year, Month(d.Month), day, int(d.Hour), int(d.Minute), int(d.Second), 0, UTC)
	i := int(d.DayOfWeek) - int(t.Weekday())
	if i < 0 {
		i += 7
	}
	day += i
	if week := int(d.Day) - 1; week < 4 {
		day += week * 7
	} else {
		// 该日的“最后一个”实例。
		day += 4 * 7
		if day > daysIn(Month(d.Month), year) {
			day -= 7
		}
	}
	return t.sec() + int64(day-1)*secondsPerDay + internalToUnix
}

func initLocalFromTZI(i *syscall.Timezoneinformation) {
	l := &localLoc

	l.name = "Local"

	nzone := 1
	if i.StandardDate.Month > 0 {
		nzone++
	}
	l.zone = make([]zone, nzone)

	stdname, dstname := abbrev(i)

	std := &l.zone[0]
	std.name = stdname
	if nzone == 1 {
		// 无夏令时。
		std.offset = -int(i.Bias) * 60
		l.cacheStart = alpha
		l.cacheEnd = omega
		l.cacheZone = std
		l.tx = make([]zoneTrans, 1)
		l.tx[0].when = l.cacheStart
		l.tx[0].index = 0
		return
	}

	// 如果 StandardDate 未设置，则必须忽略 StandardBias，
	// 因此该计算被推迟到上面 nzone==1 的
	// return 之后。
	std.offset = -int(i.Bias+i.StandardBias) * 60

	dst := &l.zone[1]
	dst.name = dstname
	dst.offset = -int(i.Bias+i.DaylightBias) * 60
	dst.isDST = true

	// 调整顺序，使 d0 为第一个转换日期，d1 为第二个，
	// i0 为第一个转换后的 zone 索引，i1 为第二个。
	d0 := &i.StandardDate
	d1 := &i.DaylightDate
	i0 := 0
	i1 := 1
	if d0.Month > d1.Month {
		d0, d1 = d1, d0
		i0, i1 = i1, i0
	}

	// 每年 2 次转换，本年前后各 100 年
	l.tx = make([]zoneTrans, 400)

	t := Now().UTC()
	year := t.Year()
	txi := 0
	for y := year - 100; y < year+100; y++ {
		tx := &l.tx[txi]
		tx.when = pseudoUnix(y, d0) - int64(l.zone[i1].offset)
		tx.index = uint8(i0)
		txi++

		tx = &l.tx[txi]
		tx.when = pseudoUnix(y, d1) - int64(l.zone[i0].offset)
		tx.index = uint8(i1)
		txi++
	}
}

var usPacific = syscall.Timezoneinformation{
	Bias: 8 * 60,
	StandardName: [32]uint16{
		'P', 'a', 'c', 'i', 'f', 'i', 'c', ' ', 'S', 't', 'a', 'n', 'd', 'a', 'r', 'd', ' ', 'T', 'i', 'm', 'e',
	},
	StandardDate: syscall.Systemtime{Month: 11, Day: 1, Hour: 2},
	DaylightName: [32]uint16{
		'P', 'a', 'c', 'i', 'f', 'i', 'c', ' ', 'D', 'a', 'y', 'l', 'i', 'g', 'h', 't', ' ', 'T', 'i', 'm', 'e',
	},
	DaylightDate: syscall.Systemtime{Month: 3, Day: 2, Hour: 2},
	DaylightBias: -60,
}

var aus = syscall.Timezoneinformation{
	Bias: -10 * 60,
	StandardName: [32]uint16{
		'A', 'U', 'S', ' ', 'E', 'a', 's', 't', 'e', 'r', 'n', ' ', 'S', 't', 'a', 'n', 'd', 'a', 'r', 'd', ' ', 'T', 'i', 'm', 'e',
	},
	StandardDate: syscall.Systemtime{Month: 4, Day: 1, Hour: 3},
	DaylightName: [32]uint16{
		'A', 'U', 'S', ' ', 'E', 'a', 's', 't', 'e', 'r', 'n', ' ', 'D', 'a', 'y', 'l', 'i', 'g', 'h', 't', ' ', 'T', 'i', 'm', 'e',
	},
	DaylightDate: syscall.Systemtime{Month: 10, Day: 1, Hour: 2},
	DaylightBias: -60,
}

func initLocal() {
	var i syscall.Timezoneinformation
	if _, err := syscall.GetTimeZoneInformation(&i); err != nil {
		localLoc.name = "UTC"
		return
	}
	initLocalFromTZI(&i)
}
