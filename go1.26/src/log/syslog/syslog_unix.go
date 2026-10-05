// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows && !plan9

package syslog

import (
	"errors"
	"net"
)

// unixSyslog 使用 Unix 域套接字打开一条到本地机器上运行的
// syslog 守护进程的连接。

func unixSyslog() (conn serverConn, err error) {
	logTypes := []string{"unixgram", "unix"}
	logPaths := []string{"/dev/log", "/var/run/syslog", "/var/run/log"}
	for _, network := range logTypes {
		for _, path := range logPaths {
			conn, err := net.Dial(network, path)
			if err == nil {
				return &netConn{conn: conn, local: true}, nil
			}
		}
	}
	return nil, errors.New("Unix syslog delivery error")
}
