// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js || wasip1

package testing

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// TODO(@musiol, @odeke-em)：当 js/wasm 获得 os.Pipe 实现时，
// 将这段代码重新合并回 example.go。
func runExample(eg InternalExample) (ok bool) {
	if chatty.on {
		fmt.Printf("%s=== RUN   %s\n", chatty.prefix(), eg.Name)
	}

	// 将标准输出捕获到临时文件。我们未使用
	// os.Pipe，因为它在 js/wasm 上不受支持。
	stdout := os.Stdout
	f := createTempFile(eg.Name)
	os.Stdout = f
	finished := false
	start := time.Now()

	// 在延迟调用中进行清理，以便在示例发生 panic 时能够 recover。
	defer func() {
		timeSpent := time.Since(start)

		// 恢复 stdout，获取输出并删除临时文件。
		os.Stdout = stdout
		var buf strings.Builder
		_, seekErr := f.Seek(0, io.SeekStart)
		_, readErr := io.Copy(&buf, f)
		out := buf.String()
		f.Close()
		os.Remove(f.Name())
		if seekErr != nil {
			fmt.Fprintf(os.Stderr, "testing: seek temp file: %v\n", seekErr)
			os.Exit(1)
		}
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "testing: read temp file: %v\n", readErr)
			os.Exit(1)
		}

		err := recover()
		ok = eg.processRunResult(out, timeSpent, finished, err)
	}()

	// 运行示例。
	eg.F()
	finished = true
	return
}

func createTempFile(exampleName string) *os.File {
	for i := 0; ; i++ {
		name := fmt.Sprintf("%s/go-example-stdout-%s-%d.txt", os.TempDir(), exampleName, i)
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			fmt.Fprintf(os.Stderr, "testing: open temp file: %v\n", err)
			os.Exit(1)
		}
		return f
	}
}
