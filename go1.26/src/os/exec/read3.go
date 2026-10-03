// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// 这是一个测试程序，用于验证它可以从描述符 3 读取，
// 并且没有打开其他描述符。这不是通过 TestHelperProcess 和
// GO_EXEC_TEST_PID 完成的，因为我们想确保该程序不使用 cgo，
// 因为 C 库可能会在我们背后打开文件描述符，从而干扰测试。
// 参见 issue 25628。
package main

import (
	"fmt"
	"internal/poll"
	"io"
	"os"
	"os/exec"
	"os/exec/internal/fdtest"
	"runtime"
)

func main() {
	fd3 := os.NewFile(3, "fd3")
	defer fd3.Close()

	bs, err := io.ReadAll(fd3)
	if err != nil {
		fmt.Printf("ReadAll from fd 3: %v\n", err)
		os.Exit(1)
	}

	// 现在验证没有其他打开的 fd。
	// stdin == 0
	// stdout == 1
	// stderr == 2
	// 来自父进程的描述符 == 3
	// 4 及以上的所有描述符都应该是可用的，
	// 除非被网络轮询器使用了。
	for fd := uintptr(4); fd <= 100; fd++ {
		if poll.IsPollDescriptor(fd) {
			continue
		}

		if !fdtest.Exists(fd) {
			continue
		}

		fmt.Printf("leaked parent file. fdtest.Exists(%d) got true want false\n", fd)

		fdfile := fmt.Sprintf("/proc/self/fd/%d", fd)
		link, err := os.Readlink(fdfile)
		fmt.Printf("readlink(%q) = %q, %v\n", fdfile, link, err)

		var args []string
		switch runtime.GOOS {
		case "plan9":
			args = []string{fmt.Sprintf("/proc/%d/fd", os.Getpid())}
		case "aix", "solaris", "illumos":
			args = []string{fmt.Sprint(os.Getpid())}
		default:
			args = []string{"-p", fmt.Sprint(os.Getpid())}
		}

		// 确定使用哪个命令来显示打开的文件。
		ofcmd := "lsof"
		switch runtime.GOOS {
		case "dragonfly", "freebsd", "netbsd", "openbsd":
			ofcmd = "fstat"
		case "plan9":
			ofcmd = "/bin/cat"
		case "aix":
			ofcmd = "procfiles"
		case "solaris", "illumos":
			ofcmd = "pfiles"
		}

		cmd := exec.Command(ofcmd, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%#q failed: %v\n", cmd, err)
		}
		fmt.Printf("%s", out)
		os.Exit(1)
	}

	os.Stdout.Write(bs)
}
