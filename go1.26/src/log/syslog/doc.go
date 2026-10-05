// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package syslog 为系统日志服务提供了一个简单的接口。
// 它可以使用 UNIX 域套接字、UDP 或 TCP 向 syslog 守护进程
// 发送消息。
//
// 只需调用一次 Dial。当写入失败时，
// syslog 客户端会尝试重新连接服务器
// 并再次写入。
//
// syslog 包已被冻结，不再接受新特性。
// 一些外部包提供了更多功能。参见：
//
//	https://godoc.org/?q=syslog
package syslog

// BUG(brainman): 本包未在 Windows 上实现。由于
// syslog 包已被冻结，建议 Windows 用户
// 使用标准库之外的包。背景信息
// 参见 https://golang.org/issue/1108。

// BUG(akumar): 本包未在 Plan 9 上实现。
