// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package time

import (
	"internal/strconv"
	"syscall/js"
)

var platformZoneSources = []string{
	"/usr/share/zoneinfo/",
	"/usr/share/lib/zoneinfo/",
	"/usr/lib/locale/TZ/",
}

func initLocal() {
	localLoc.name = "Local"

	z := zone{}
	d := js.Global().Get("Date").New()
	offset := d.Call("getTimezoneOffset").Int() * -1
	z.offset = offset * 60
	// 根据 https://tc39.github.io/ecma262/#sec-timezoneestring，
	// (new Date()).toTimeString() 返回的时区名称依赖于具体实现，
	// 并且在 Google Chrome 中，它给出的是完全展开的名称而非
	// 缩写。
	// 因此，我们根据偏移量构造名称。
	z.name = "UTC"
	if offset < 0 {
		z.name += "-"
		offset *= -1
	} else {
		z.name += "+"
	}
	z.name += strconv.Itoa(offset / 60)
	min := offset % 60
	if min != 0 {
		z.name += ":" + strconv.Itoa(min)
	}
	localLoc.zone = []zone{z}
}
