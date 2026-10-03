// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 进程等。

package os

import (
	"internal/testlog"
	"runtime"
	"syscall"
)

// Args 保存命令行参数，以程序名开头。
var Args []string

func init() {
	if runtime.GOOS == "windows" {
		// 在 exec_windows.go 中初始化。
		return
	}
	Args = runtime_args()
}

func runtime_args() []string // 位于 runtime 包中

// Getuid 返回调用者的数字用户 ID。
//
// 在 Windows 上，它返回 -1。
func Getuid() int { return syscall.Getuid() }

// Geteuid 返回调用者的有效数字用户 ID。
//
// 在 Windows 上，它返回 -1。
func Geteuid() int { return syscall.Geteuid() }

// Getgid 返回调用者的数字组 ID。
//
// 在 Windows 上，它返回 -1。
func Getgid() int { return syscall.Getgid() }

// Getegid 返回调用者的有效数字组 ID。
//
// 在 Windows 上，它返回 -1。
func Getegid() int { return syscall.Getegid() }

// Getgroups 返回调用者所属组的数字 ID 列表。
//
// 在 Windows 上，它返回 [syscall.EWINDOWS]。有关可能的替代方案，
// 参见 [os/user] 包。
func Getgroups() ([]int, error) {
	gids, e := syscall.Getgroups()
	return gids, NewSyscallError("getgroups", e)
}

// Exit 使当前程序以给定的状态码退出。
// 按惯例，状态码 0 表示成功，非零表示错误。
// 程序会立即终止；deferred 函数不会运行。
//
// 为便于移植，状态码应在 [0, 125] 范围内。
func Exit(code int) {
	if code == 0 && testlog.PanicOnExit0() {
		// 我们被告知在调用 os.Exit(0) 时 panic。
		// 这用于让过早意外调用 os.Exit(0) 的
		// 测试失败。
		panic("unexpected call to os.Exit(0) during test")
	}

	// 通知 runtime 正在调用 os.Exit。如果启用了 -race，
	// 这将给竞态检测器一个让程序失败的机会
	// （存在竞态的进程无权成功完成）。如果启用了覆盖率，
	// 则此调用将使我们能够写出覆盖率数据文件。
	runtime_beforeExit(code)

	syscall.Exit(code)
}

func runtime_beforeExit(exitCode int) // 在 runtime 中实现
