// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"runtime"
	"sync"
	"syscall"
)

var getwdCache struct {
	sync.Mutex
	dir string
}

// Getwd 返回对应当前目录的绝对路径名。
// 如果当前目录可以通过多条路径到达
// （由于符号链接），Getwd 可能返回其中任意一条。
//
// 在 Unix 平台上，如果环境变量 PWD
// 提供了绝对名称，且它是当前目录的名称，
// 则返回它。
func Getwd() (dir string, err error) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		// 直接使用 syscall.Getwd：
		//   - plan9：原因参见 CL 89575；
		//   - windows：syscall 实现已经足够，
		//     而且我们不应依赖 $PWD。
		dir, err = syscall.Getwd()
		return dir, NewSyscallError("getwd", err)
	}

	// 笨拙但广泛使用的权宜之计：
	// 如果设置了 $PWD 且它与 "." 匹配，就使用它。
	var dot FileInfo
	dir = Getenv("PWD")
	if len(dir) > 0 && dir[0] == '/' {
		dot, err = statNolog(".")
		if err != nil {
			return "", err
		}
		d, err := statNolog(dir)
		if err == nil && SameFile(dot, d) {
			return dir, nil
		}
		// 如果这里的 err 是 ENAMETOOLONG，下面的 syscall.Getwd
		// 也会以同样的错误失败，但无论如何还是试一试，
		// 因为回退代码要慢得多。
	}

	// 如果操作系统提供了 Getwd 调用，就使用它。
	if syscall.ImplementsGetwd {
		dir, err = ignoringEINTR2(syscall.Getwd)
		// 如果结果太长，Linux 返回 ENAMETOOLONG。
		// 一些 BSD 系统似乎返回 EINVAL。
		// FreeBSD 系统似乎使用 ENOMEM
		// Solaris 似乎使用 ERANGE。
		if err != syscall.ENAMETOOLONG && err != syscall.EINVAL && err != errERANGE && err != errENOMEM {
			return dir, NewSyscallError("getwd", err)
		}
	}

	// 我们正试图找到回到 "." 的路径。
	if dot == nil {
		dot, err = statNolog(".")
		if err != nil {
			return "", err
		}
	}
	// 应用同样的权宜之计，但用的是缓存的 dir 而不是 $PWD。
	getwdCache.Lock()
	dir = getwdCache.dir
	getwdCache.Unlock()
	if len(dir) > 0 {
		d, err := statNolog(dir)
		if err == nil && SameFile(dot, d) {
			return dir, nil
		}
	}

	// 根目录是一种特殊情况，因为它没有父目录
	// 且以斜杠结尾。
	root, err := statNolog("/")
	if err != nil {
		// 无法 stat 根目录——没有希望了。
		return "", err
	}
	if SameFile(root, dot) {
		return "/", nil
	}

	// 通用算法：在父目录中找到名称，
	// 然后找到父目录的名称。每次迭代
	// 都把 /name 添加到 dir 开头。
	dir = ""
	for parent := ".."; ; parent = "../" + parent {
		if len(parent) >= 1024 { // 合理性检查
			return "", NewSyscallError("getwd", syscall.ENAMETOOLONG)
		}
		fd, err := openDirNolog(parent)
		if err != nil {
			return "", err
		}

		for {
			names, err := fd.Readdirnames(100)
			if err != nil {
				fd.Close()
				// Readdirnames 可能返回 io.EOF 或其他错误。
				// 无论如何，我们到这里是因为 syscall.Getwd
				// 未实现或以 ENAMETOOLONG 失败，
				// 因此返回最合理的错误。
				if syscall.ImplementsGetwd {
					return "", NewSyscallError("getwd", syscall.ENAMETOOLONG)
				}
				return "", NewSyscallError("getwd", errENOSYS)
			}
			for _, name := range names {
				d, _ := lstatNolog(parent + "/" + name)
				if SameFile(d, dot) {
					dir = "/" + name + dir
					goto Found
				}
			}
		}

	Found:
		pd, err := fd.Stat()
		fd.Close()
		if err != nil {
			return "", err
		}
		if SameFile(pd, root) {
			break
		}
		// 为下一轮做准备。
		dot = pd
	}

	// 保存答案作为提示，以避免下次走这条昂贵的路径。
	getwdCache.Lock()
	getwdCache.dir = dir
	getwdCache.Unlock()

	return dir, nil
}
