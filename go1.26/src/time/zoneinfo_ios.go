// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ios

package time

import (
	"syscall"
)

var platformZoneSources []string // iOS 上没有

func gorootZoneSource(goroot string) (string, bool) {
	// 初始化时的工作目录是
	// app bundle 的根目录："/private/.../bundlename.app"。对于连接设备（tethered）的 iOS 构建，
	// 我们将 zoneinfo.zip 保存在那里。
	// 对于自托管的 iOS 构建，zoneinfo.zip 位于 GOROOT 中。
	var roots []string
	if goroot != "" {
		roots = append(roots, goroot+"/lib/time")
	}
	wd, err := syscall.Getwd()
	if err == nil {
		roots = append(roots, wd)
	}
	for _, r := range roots {
		var st syscall.Stat_t
		fd, err := syscall.Open(r, syscall.O_RDONLY, 0)
		if err != nil {
			continue
		}
		defer syscall.Close(fd)
		if err := syscall.Fstat(fd, &st); err == nil {
			return r + "/zoneinfo.zip", true
		}
	}
	return "", false
}

func initLocal() {
	// TODO(crawshaw): [NSTimeZone localTimeZone]
	localLoc = *UTC
}
