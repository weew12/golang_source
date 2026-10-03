// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

// 在 wasip1 中，zoneinfo 由运行时管理。
var platformZoneSources = []string{}

func initLocal() {
	localLoc.name = "Local"
}
