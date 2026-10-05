// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"
)

type InternalExample struct {
	Name      string
	F         func()
	Output    string
	Unordered bool
}

// RunExamples 是一个内部函数，但由于它是跨包使用的而被导出；
// 它是 "go test" 命令实现的一部分。
func RunExamples(matchString func(pat, str string) (bool, error), examples []InternalExample) (ok bool) {
	_, ok = runExamples(matchString, examples)
	return ok
}

func runExamples(matchString func(pat, str string) (bool, error), examples []InternalExample) (ran, ok bool) {
	ok = true

	m := newMatcher(matchString, *match, "-test.run", *skip)

	var eg InternalExample
	for _, eg = range examples {
		_, matched, _ := m.fullName(nil, eg.Name)
		if !matched {
			continue
		}
		ran = true
		if !runExample(eg) {
			ok = false
		}
	}

	return ran, ok
}

// processRunResult 计算运行一个示例测试的结果的摘要和状态。
// stdout 是从测试的标准输出中捕获的输出。
// recovered 是运行测试后调用 recover 的结果，以防测试发生 panic。
//
// 如果 stdout 与预期输出不匹配，或者 recovered 非 nil，它会将失败原因打印到标准输出。
// 如果测试是 chatty/verbose 模式，它会向标准输出打印成功消息。
// 如果 recovered 非 nil，它会以该值触发 panic。
// 如果测试以 nil 触发 panic，或者调用了 runtime.Goexit，它会被
// 判定为失败并以 errNilPanicOrGoexit 触发 panic
func (eg *InternalExample) processRunResult(stdout string, timeSpent time.Duration, finished bool, recovered any) (passed bool) {
	passed = true
	dstr := fmtDuration(timeSpent)
	var fail string
	got := strings.TrimSpace(stdout)
	want := strings.TrimSpace(eg.Output)
	if runtime.GOOS == "windows" {
		got = strings.ReplaceAll(got, "\r\n", "\n")
		want = strings.ReplaceAll(want, "\r\n", "\n")
	}
	if eg.Unordered {
		gotLines := slices.Sorted(strings.SplitSeq(got, "\n"))
		wantLines := slices.Sorted(strings.SplitSeq(want, "\n"))
		if !slices.Equal(gotLines, wantLines) && recovered == nil {
			fail = fmt.Sprintf("got:\n%s\nwant (unordered):\n%s\n", stdout, eg.Output)
		}
	} else {
		if got != want && recovered == nil {
			fail = fmt.Sprintf("got:\n%s\nwant:\n%s\n", got, want)
		}
	}
	if fail != "" || !finished || recovered != nil {
		fmt.Printf("%s--- FAIL: %s (%s)\n%s", chatty.prefix(), eg.Name, dstr, fail)
		passed = false
	} else if chatty.on {
		fmt.Printf("%s--- PASS: %s (%s)\n", chatty.prefix(), eg.Name, dstr)
	}

	if chatty.on && chatty.json {
		fmt.Printf("%s=== NAME   %s\n", chatty.prefix(), "")
	}

	if recovered != nil {
		// 通过 panic 传播之前 recover 到的结果。
		panic(recovered)
	} else if !finished {
		panic(errNilPanicOrGoexit)
	}

	return
}
