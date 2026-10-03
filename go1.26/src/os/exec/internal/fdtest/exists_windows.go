// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows

package fdtest

// Exists 在 windows 上未实现，会引发 panic。
func Exists(fd uintptr) bool {
	panic("unimplemented")
}
