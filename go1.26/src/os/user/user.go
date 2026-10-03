// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package user 允许按名称或 id 查找用户账户。

对于大多数 Unix 系统，本包有两个用于将用户和组 id 解析为名称、
以及列出附加组 ID 的内部实现。一个是纯 Go 编写的，解析
/etc/passwd 和 /etc/group。另一个基于 cgo，依赖于标准 C 库（libc）
的例程，例如 getpwuid_r、getgrnam_r 和 getgrouplist。

当 cgo 可用，且所需例程在特定平台上已由 libc 实现时，
会使用基于 cgo（由 libc 支持）的代码。
这可以通过使用 osusergo 构建标签来覆盖，该标签会强制
使用纯 Go 实现。
*/
package user

import (
	"strconv"
)

// 这些值可能会在特定平台和/或构建标志的 init() 中被设为 false，
// 以便让测试知道跳过某些特性的测试。
var (
	userImplemented      = true
	groupImplemented     = true
	groupListImplemented = true
)

// User 表示一个用户账户。
type User struct {
	// Uid 是用户 ID。
	// 在 POSIX 系统上，这是一个表示 uid 的十进制数。
	// 在 Windows 上，这是一个字符串格式的安全标识符（SID）。
	// 在 Plan 9 上，这是 /dev/user 的内容。
	Uid string
	// Gid 是主组 ID。
	// 在 POSIX 系统上，这是一个表示 gid 的十进制数。
	// 在 Windows 上，这是一个字符串格式的 SID。
	// 在 Plan 9 上，这是 /dev/user 的内容。
	Gid string
	// Username 是登录名。
	Username string
	// Name 是用户的真实姓名或显示名称。
	// 它可能为空。
	// 在 POSIX 系统上，这是 GECOS 字段列表中的第一个（或唯一一个）条目。
	// 在 Windows 上，这是用户的显示名称。
	// 在 Plan 9 上，这是 /dev/user 的内容。
	Name string
	// HomeDir 是用户主目录的路径（如果用户有主目录的话）。
	HomeDir string
}

// Group 表示一组用户的集合。
//
// 在 POSIX 系统上，Gid 包含一个表示组 ID 的十进制数。
type Group struct {
	Gid  string // 组 ID
	Name string // 组名
}

// UnknownUserIdError 在找不到用户时由 [LookupId] 返回。
type UnknownUserIdError int

func (e UnknownUserIdError) Error() string {
	return "user: unknown userid " + strconv.Itoa(int(e))
}

// UnknownUserError 在找不到用户时由 [Lookup] 返回。
type UnknownUserError string

func (e UnknownUserError) Error() string {
	return "user: unknown user " + string(e)
}

// UnknownGroupIdError 在找不到组时由 [LookupGroupId] 返回。
type UnknownGroupIdError string

func (e UnknownGroupIdError) Error() string {
	return "group: unknown groupid " + string(e)
}

// UnknownGroupError 在找不到组时由 [LookupGroup] 返回。
type UnknownGroupError string

func (e UnknownGroupError) Error() string {
	return "group: unknown group " + string(e)
}
