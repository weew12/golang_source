// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsontest

import (
	"fmt"
	"path"
	"runtime"
)

// TODO(https://go.dev/issue/52751): 替换为原生测试支持。

// CaseName 是带有文件和行号标注的用例名。
type CaseName struct {
	Name  string
	Where CasePos
}

// Name 用调用方的文件和行号标注一个用例名。
func Name(s string) (c CaseName) {
	c.Name = s
	runtime.Callers(2, c.Where.pc[:])
	return c
}

// CasePos 表示一个文件和行号。
type CasePos struct{ pc [1]uintptr }

func (pos CasePos) String() string {
	frames := runtime.CallersFrames(pos.pc[:])
	frame, _ := frames.Next()
	return fmt.Sprintf("%s:%d", path.Base(frame.File), frame.Line)
}
