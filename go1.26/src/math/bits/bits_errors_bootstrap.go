// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build compiler_bootstrap

// 此版本仅用于引导（bootstrap）（在此路径上我们希望
// 避免对变量使用 go:linkname）。

package bits

type errorString string

func (e errorString) RuntimeError() {}

func (e errorString) Error() string {
	return "runtime error: " + string(e)
}

var overflowError = error(errorString("integer overflow"))

var divideError = error(errorString("integer divide by zero"))
