// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix && !ios && !android

// 解析 "zoneinfo" 时区文件。
// 这是 OS X、Linux、BSD、Sun 及其他系统上使用的一种相当标准的文件格式。
// 参见 tzfile(5)、https://en.wikipedia.org/wiki/Zoneinfo，
// 以及 ftp://munnari.oz.au/pub/oldtz/

package time

import (
	"syscall"
)

// 许多系统使用 /usr/share/zoneinfo，Solaris 2 使用
// /usr/share/lib/zoneinfo，IRIX 6 使用 /usr/lib/locale/TZ，
// NixOS 使用 /etc/zoneinfo。
var platformZoneSources = []string{
	"/usr/share/zoneinfo/",
	"/usr/share/lib/zoneinfo/",
	"/usr/lib/locale/TZ/",
	"/etc/zoneinfo",
}

func initLocal() {
	// 查询 $TZ 以找到要使用的时区。
	// 无 $TZ 表示使用系统默认的 /etc/localtime。
	// $TZ="" 表示使用 UTC。
	// $TZ="foo" 或 $TZ=":foo"：如果 foo 是绝对路径，那么将使用 foo
	// 指向的文件来初始化时区；否则将使用文件
	// /usr/share/zoneinfo/foo。

	tz, ok := syscall.Getenv("TZ")
	switch {
	case !ok:
		z, err := loadLocation("localtime", []string{"/etc"})
		if err == nil {
			localLoc = *z
			localLoc.name = "Local"
			return
		}
	case tz != "":
		if tz[0] == ':' {
			tz = tz[1:]
		}
		if tz != "" && tz[0] == '/' {
			if z, err := loadLocation(tz, []string{""}); err == nil {
				localLoc = *z
				if tz == "/etc/localtime" {
					localLoc.name = "Local"
				} else {
					localLoc.name = tz
				}
				return
			}
		} else if tz != "" && tz != "UTC" {
			if z, err := loadLocation(tz, platformZoneSources); err == nil {
				localLoc = *z
				return
			}
		}
	}

	// 回退到 UTC。
	localLoc.name = "UTC"
}
