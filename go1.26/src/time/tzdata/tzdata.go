// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package tzdata 提供时区数据库的内嵌副本。
// 如果该包在程序中的任何位置被导入，那么当
// time 包在系统上找不到 tzdata 文件时，
// 就会使用这份内嵌信息。
//
// 导入该包会使程序体积增大约
// 450 KB。
//
// 该包通常应由程序的 main 包导入，
// 而不是由库导入。库通常不应决定是否
// 在程序中包含时区数据库。
//
// 如果使用 -tags timetzdata 构建，该包会被自动导入。
package tzdata

// 该包的测试是 time/tzdata_test.go。

import (
	"errors"
	"syscall"
	_ "unsafe" // 用于 go:linkname
)

// registerLoadFromEmbeddedTZData 定义在 package time 中。
//
//go:linkname registerLoadFromEmbeddedTZData time.registerLoadFromEmbeddedTZData
func registerLoadFromEmbeddedTZData(func(string) (string, error))

func init() {
	registerLoadFromEmbeddedTZData(loadFromEmbeddedTZData)
}

// get4s 返回 s 开头处的小端序 32 位值。
func get4s(s string) int {
	if len(s) < 4 {
		return 0
	}
	return int(s[0]) | int(s[1])<<8 | int(s[2])<<16 | int(s[3])<<24
}

// get2s 返回 s 开头处的小端序 16 位值。
func get2s(s string) int {
	if len(s) < 2 {
		return 0
	}
	return int(s[0]) | int(s[1])<<8
}

// loadFromEmbeddedTZData 返回非压缩 zip 文件中具有给定名称的文件内容，
// 该文件的内容可以在 embeddedTzdata 中找到。
// 这类似于 time.loadTzinfoFromZip。
func loadFromEmbeddedTZData(name string) (string, error) {
	const (
		zecheader = 0x06054b50
		zcheader  = 0x02014b50
		ztailsize = 22

		zheadersize = 30
		zheader     = 0x04034b50
	)

	// zipdata 由 zzipdata.go 提供，
	// 该文件由 cmd/dist 在 make.bash 期间生成。
	z := zipdata

	idx := len(z) - ztailsize
	n := get2s(z[idx+10:])
	idx = get4s(z[idx+16:])

	for i := 0; i < n; i++ {
		// 关于 zip 条目布局，参见 time.loadTzinfoFromZip。
		if get4s(z[idx:]) != zcheader {
			break
		}
		meth := get2s(z[idx+10:])
		size := get4s(z[idx+24:])
		namelen := get2s(z[idx+28:])
		xlen := get2s(z[idx+30:])
		fclen := get2s(z[idx+32:])
		off := get4s(z[idx+42:])
		zname := z[idx+46 : idx+46+namelen]
		idx += 46 + namelen + xlen + fclen
		if zname != name {
			continue
		}
		if meth != 0 {
			return "", errors.New("unsupported compression for " + name + " in embedded tzdata")
		}

		// 关于 zip 每文件头布局，参见 time.loadTzinfoFromZip。
		idx = off
		if get4s(z[idx:]) != zheader ||
			get2s(z[idx+8:]) != meth ||
			get2s(z[idx+26:]) != namelen ||
			z[idx+30:idx+30+namelen] != name {
			return "", errors.New("corrupt embedded tzdata")
		}
		xlen = get2s(z[idx+28:])
		idx += 30 + namelen + xlen
		return z[idx : idx+size], nil
	}

	return "", syscall.ENOENT
}
