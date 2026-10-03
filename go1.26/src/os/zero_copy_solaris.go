// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/poll"
	"io"
	"runtime"
	"syscall"
)

func (f *File) writeTo(w io.Writer) (written int64, handled bool, err error) {
	return 0, false, nil
}

// readFrom 基本上是 net.sendFile 的一次重构，但经过调整以适用于 *File 的目标。
func (f *File) readFrom(r io.Reader) (written int64, handled bool, err error) {
	var remain int64 = 0 // 0 表示一直发送直到 EOF
	lr, ok := r.(*io.LimitedReader)
	if ok {
		remain, r = lr.N, lr.R
		if remain <= 0 {
			return 0, true, nil
		}
	}

	var src *File
	switch v := r.(type) {
	case *File:
		src = v
	case fileWithoutWriteTo:
		src = v.File
	default:
		return 0, false, nil
	}

	if src.checkValid("ReadFrom") != nil {
		// 避免返回该错误，因为我们报告 handled 为 false，
		// 将后续的错误处理留给调用者负责。
		return 0, false, nil
	}

	// 如果 fd_in 和 fd_out 指向同一个文件，且源范围与目标范围重叠，
	// SunOS 上的 sendfile(2) 会允许这种重叠，并像 memmove 一样工作，
	// 这种情况下复制后文件内容保持不变，这不是我们想要的。
	// 因此，当文件复制自身时，我们直接在此退出，交给通用复制处理。
	if f.pfd.Sysfd == src.pfd.Sysfd {
		return 0, false, nil
	}

	// 在 illumos 上，当目标文件是标准流（stdout/stderr）时，
	// sendfile() 似乎会出现间歇性失败，因此我们保守地跳过
	// 除常规文件以外的任何内容，交给通用复制处理。
	// 更多细节请查看 https://go.dev/issue/68863。
	if runtime.GOOS == "illumos" {
		fi, err := f.Stat()
		if err != nil {
			return 0, false, nil
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, false, nil
		}
		if typ := st.Mode & syscall.S_IFMT; typ != syscall.S_IFREG {
			return 0, false, nil
		}
	}

	sc, err := src.SyscallConn()
	if err != nil {
		return
	}

	// Solaris 和 illumos 上的 sendfile() 系统调用支持文件到文件的复制。
	// 更多细节请查看 https://docs.oracle.com/cd/E86824_01/html/E54768/sendfile-3ext.html 和
	// https://docs.oracle.com/cd/E88353_01/html/E37843/sendfile-3c.html 以及
	// https://illumos.org/man/3EXT/sendfile。
	rerr := sc.Read(func(fd uintptr) bool {
		written, err, handled = poll.SendFile(&f.pfd, fd, remain)
		return true
	})
	if lr != nil {
		lr.N = remain - written
	}
	if err == nil {
		err = rerr
	}

	return written, handled, wrapSyscallError("sendfile", err)
}
