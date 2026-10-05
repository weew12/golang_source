// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !js && !wasip1

// TODO(@musiol, @odeke-em)：当 js/wasm 获得 os.Pipe 实现
// 并不再需要这种分离时，将整个文件重新合并回 example.go。

package testing

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func runExample(eg InternalExample) (ok bool) {
	if chatty.on {
		fmt.Printf("%s=== RUN   %s\n", chatty.prefix(), eg.Name)
	}

	// 捕获标准输出。
	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout = w
	outC := make(chan string)
	go func() {
		var buf strings.Builder
		_, err := io.Copy(&buf, r)
		r.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "testing: copying pipe: %v\n", err)
			os.Exit(1)
		}
		outC <- buf.String()
	}()

	finished := false
	start := time.Now()

	// 在延迟调用中进行清理，以便在示例发生 panic 时能够 recover。
	defer func() {
		timeSpent := time.Since(start)

		// 关闭管道，恢复 stdout，获取输出。
		w.Close()
		os.Stdout = stdout
		out := <-outC

		err := recover()
		ok = eg.processRunResult(out, timeSpent, finished, err)
	}()

	// 运行示例。
	eg.F()
	finished = true
	return
}
