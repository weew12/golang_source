// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

// Hostname 返回内核报告的主机名。
func Hostname() (name string, err error) {
	return hostname()
}
