// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/poll"
	"io"
)

var pollCopyFileRange = poll.CopyFileRange

func (f *File) writeTo(w io.Writer) (written int64, handled bool, err error) {
	return 0, false, nil
}

func (f *File) readFrom(r io.Reader) (written int64, handled bool, err error) {
	// copy_file_range(2) 不支持以 O_APPEND 打开的
	// 目标，因此不必费心尝试用这些系统调用做零拷贝。
	//
	// 详情请访问 https://man.freebsd.org/cgi/man.cgi?copy_file_range(2)#ERRORS。
	if f.appendMode {
		return 0, false, nil
	}

	var (
		remain int64
		lr     *io.LimitedReader
	)
	if lr, r, remain = tryLimitedReader(r); remain <= 0 {
		return 0, true, nil
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

	written, handled, err = pollCopyFileRange(&f.pfd, &src.pfd, remain)
	if lr != nil {
		lr.N -= written
	}

	return written, handled, wrapSyscallError("copy_file_range", err)
}
