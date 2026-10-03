// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package user

import (
	"fmt"
	"os"
	"syscall"
)

// Plan 9 上的部分 os/user 支持。
// 支持 Current()，但不支持 Lookup()/LookupId()。
// 后两者需要解析 /adm/users。

func init() {
	userImplemented = false
	groupImplemented = false
	groupListImplemented = false
}

var (
	// 未使用的变量（在此实现中）
	// 在测试期间被修改，以覆盖 cgo 实现中的代码路径。
	userBuffer  = 0
	groupBuffer = 0
)

func current() (*User, error) {
	ubytes, err := os.ReadFile("/dev/user")
	if err != nil {
		return nil, fmt.Errorf("user: %s", err)
	}

	uname := string(ubytes)

	u := &User{
		Uid:      uname,
		Gid:      uname,
		Username: uname,
		Name:     uname,
		HomeDir:  os.Getenv("home"),
	}

	return u, nil
}

func lookupUser(username string) (*User, error) {
	return nil, syscall.EPLAN9
}

func lookupUserId(uid string) (*User, error) {
	return nil, syscall.EPLAN9
}

func lookupGroup(groupname string) (*Group, error) {
	return nil, syscall.EPLAN9
}

func lookupGroupId(string) (*Group, error) {
	return nil, syscall.EPLAN9
}

func listGroups(*User) ([]string, error) {
	return nil, syscall.EPLAN9
}
