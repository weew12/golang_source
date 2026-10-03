// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 解析 Plan 9 timezone(2) 文件。

package time

import (
	"syscall"
)

var platformZoneSources []string // Plan 9 上没有

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

// 从 strings 复制而来以避免依赖。
func fields(s string) []string {
	// 先统计字段数量。
	n := 0
	inField := false
	for _, rune := range s {
		wasInField := inField
		inField = !isSpace(rune)
		if inField && !wasInField {
			n++
		}
	}

	// 现在创建它们。
	a := make([]string, n)
	na := 0
	fieldStart := -1 // 在寻找字段起始位置时设为 -1。
	for i, rune := range s {
		if isSpace(rune) {
			if fieldStart >= 0 {
				a[na] = s[fieldStart:i]
				na++
				fieldStart = -1
			}
		} else if fieldStart == -1 {
			fieldStart = i
		}
	}
	if fieldStart >= 0 { // 最后一个字段可能结束于 EOF。
		a[na] = s[fieldStart:]
	}
	return a
}

func loadZoneDataPlan9(s string) (l *Location, err error) {
	f := fields(s)
	if len(f) < 4 {
		if len(f) == 2 && f[0] == "GMT" {
			return UTC, nil
		}
		return nil, errBadData
	}

	var zones [2]zone

	// 标准时区偏移
	o, err := atoi(f[1])
	if err != nil {
		return nil, errBadData
	}
	zones[0] = zone{name: f[0], offset: o, isDST: false}

	// 备用时区偏移
	o, err = atoi(f[3])
	if err != nil {
		return nil, errBadData
	}
	zones[1] = zone{name: f[2], offset: o, isDST: true}

	// 转换时间对
	var tx []zoneTrans
	f = f[4:]
	for i := 0; i < len(f); i++ {
		zi := 0
		if i%2 == 0 {
			zi = 1
		}
		t, err := atoi(f[i])
		if err != nil {
			return nil, errBadData
		}
		t -= zones[0].offset
		tx = append(tx, zoneTrans{when: int64(t), index: uint8(zi)})
	}

	// 已确定能成功。
	l = &Location{zone: zones[:], tx: tx}

	// 用当前时刻的信息填充缓存，
	// 因为这将是最常见的查询。
	sec, _, _ := runtimeNow()
	for i := range tx {
		if tx[i].when <= sec && (i+1 == len(tx) || sec < tx[i+1].when) {
			l.cacheStart = tx[i].when
			l.cacheEnd = omega
			if i+1 < len(tx) {
				l.cacheEnd = tx[i+1].when
			}
			l.cacheZone = &l.zone[tx[i].index]
		}
	}

	return l, nil
}

func loadZoneFilePlan9(name string) (*Location, error) {
	b, err := readFile(name)
	if err != nil {
		return nil, err
	}
	return loadZoneDataPlan9(string(b))
}

func initLocal() {
	t, ok := syscall.Getenv("timezone")
	if ok {
		if z, err := loadZoneDataPlan9(t); err == nil {
			localLoc = *z
			return
		}
	} else {
		if z, err := loadZoneFilePlan9("/adm/timezone/local"); err == nil {
			localLoc = *z
			localLoc.name = "Local"
			return
		}
	}

	// 回退到 UTC。
	localLoc.name = "UTC"
}
