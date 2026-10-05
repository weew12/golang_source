// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gob

import "fmt"

// 解码和编码中的错误通过 panic 和 recover 处理。
// 由用户错误引起的 panic（即除 "index out of bounds" 这类运行时 panic 之外
// 的所有情况）不会离开引发它们的文件，而是会被转换为普通的错误返回。
// 不返回错误的编码和解码函数及方法，要么使用 panic 报告错误，要么保证不会出错。

// gobError 用于区分本包中产生的错误（panic）。
type gobError struct {
	err error
}

// errorf 类似 error_，但接受 Printf 风格的参数来构造错误。
// 它总是给消息加上 "gob: " 前缀。
func errorf(format string, args ...any) {
	error_(fmt.Errorf("gob: "+format, args...))
}

// error_ 包装参数错误，并将其用作 panic 的参数。
func error_(err error) {
	panic(gobError{err})
}

// catchError 旨在作为延迟函数使用，用于将 panic(gobError) 转换为
// 普通错误。它会覆盖延迟调用它的那个函数的错误返回值。
func catchError(err *error) {
	if e := recover(); e != nil {
		ge, ok := e.(gobError)
		if !ok {
			panic(e)
		}
		*err = ge.err
	}
}
