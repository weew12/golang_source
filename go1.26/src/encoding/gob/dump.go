// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

// 需要将 gob 包与 debug.go 一起编译才能构建此程序。
// 操作方法参见 debug.go 中的注释。

import (
	"encoding/gob"
	"fmt"
	"os"
)

func main() {
	var err error
	file := os.Stdin
	if len(os.Args) > 1 {
		file, err = os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "dump: %s\n", err)
			os.Exit(1)
		}
		defer file.Close()
	}
	gob.Debug(file)
}
