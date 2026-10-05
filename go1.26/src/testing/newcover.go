// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 支持采用重新设计的覆盖率实现的测试覆盖率。

package testing

import (
	"fmt"
	"os"
	_ "unsafe" // 用于 linkname
)

// cover 变量存储当前覆盖率模式以及一个
// 在测试运行结束时调用的清理函数。
var cover struct {
	mode        string
	tearDown    func(coverprofile string, gocoverdir string) (string, error)
	snapshotcov func() float64
}

// registerCover 在 “go test -cover” 运行期间被调用。
// 它用于记录一个“清理”函数
// （在测试完成时调用）以及覆盖率模式。
func registerCover(mode string, tearDown func(coverprofile string, gocoverdir string) (string, error), snapcov func() float64) {
	if mode == "" {
		return
	}
	cover.mode = mode
	cover.tearDown = tearDown
	cover.snapshotcov = snapcov
}

// coverReport 报告覆盖率百分比，并在需要时
// 写入覆盖率配置文件。
// 这会调用 _testmain.go 中的一个回调，该回调会在
// 测试执行完成的位置输出覆盖率数据，
// 用于 “go test -cover” 运行。
func coverReport() {
	if errmsg, err := cover.tearDown(*coverProfile, *gocoverdir); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", errmsg, err)
		os.Exit(2)
	}
}

// Coverage 以 [0, 1] 范围内的分数形式报告当前代码覆盖率。
// 如果未启用覆盖率，Coverage 返回 0。
//
// 在运行大量顺序测试用例时，每运行完一个就检查一次 Coverage，
// 有助于识别哪些测试用例覆盖了新的代码路径。
// 它不能替代 'go test -cover' 和 'go tool cover' 生成的报告。
func Coverage() float64 {
	if cover.mode == "" {
		return 0.0
	}
	return cover.snapshotcov()
}
