// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (!cgo && !darwin && !windows && !plan9) || android || (osusergo && !windows && !plan9)

package user

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
)

var (
	// 未使用的变量（在此实现中）
	// 在测试期间被修改，以覆盖 cgo 实现中的代码路径。
	userBuffer  = 0
	groupBuffer = 0
)

func current() (*User, error) {
	uid := currentUID()
	// $USER 和 /etc/passwd 可能不一致；如果能获取到后者，则优先使用它。
	// 更多信息参见 issue 27524。
	u, err := lookupUserId(uid)
	if err == nil {
		return u, nil
	}

	homeDir, _ := os.UserHomeDir()
	u = &User{
		Uid:      uid,
		Gid:      currentGID(),
		Username: os.Getenv("USER"),
		Name:     "", // 忽略
		HomeDir:  homeDir,
	}
	// 在 Android 上，返回一个虚拟用户而不是失败。
	switch runtime.GOOS {
	case "android":
		if u.Uid == "" {
			u.Uid = "1"
		}
		if u.Username == "" {
			u.Username = "android"
		}
	}
	// cgo 不可用，但如果我们能在没有 cgo 的情况下获得最少的信息，
	// 就使用它：
	if u.Uid != "" && u.Username != "" && u.HomeDir != "" {
		return u, nil
	}
	var missing string
	if u.Username == "" {
		missing = "$USER"
	}
	if u.HomeDir == "" {
		if missing != "" {
			missing += ", "
		}
		missing += "$HOME"
	}
	return u, fmt.Errorf("user: Current requires cgo or %s set in environment", missing)
}

func currentUID() string {
	if id := os.Getuid(); id >= 0 {
		return strconv.Itoa(id)
	}
	// 注意：Windows 返回 -1，但此文件本来就不在 Windows 上使用，
	// 因此不应走到这个空返回路径。
	return ""
}

func currentGID() string {
	if id := os.Getgid(); id >= 0 {
		return strconv.Itoa(id)
	}
	return ""
}
