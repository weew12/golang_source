// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/poll"
	"io"
	"syscall"
)

var (
	pollCopyFileRange = poll.CopyFileRange
	pollSplice        = poll.Splice
)

func (f *File) writeTo(w io.Writer) (written int64, handled bool, err error) {
	pfd, network := getPollFDAndNetwork(w)
	// TODO(panjf2000): 与 File.spliceToFile 相同。
	if pfd == nil || !pfd.IsStream || !isUnixOrTCP(string(network)) {
		return
	}

	sc, err := f.SyscallConn()
	if err != nil {
		return
	}

	rerr := sc.Read(func(fd uintptr) (done bool) {
		written, err, handled = poll.SendFile(pfd, fd, 0)
		return true
	})

	if err == nil {
		err = rerr
	}

	return written, handled, wrapSyscallError("sendfile", err)
}

func (f *File) readFrom(r io.Reader) (written int64, handled bool, err error) {
	// copy_file_range(2) 和 splice(2) 都不支持以 O_APPEND 打开的
	// 目标，因此不必费心尝试用这些系统调用做零拷贝。
	//
	// 详情请访问 https://man7.org/linux/man-pages/man2/copy_file_range.2.html#ERRORS 和
	// https://man7.org/linux/man-pages/man2/splice.2.html#ERRORS。
	if f.appendMode {
		return 0, false, nil
	}

	written, handled, err = f.copyFileRange(r)
	if handled {
		return
	}
	return f.spliceToFile(r)
}

func (f *File) spliceToFile(r io.Reader) (written int64, handled bool, err error) {
	var (
		remain int64
		lr     *io.LimitedReader
	)
	if lr, r, remain = tryLimitedReader(r); remain <= 0 {
		return 0, true, nil
	}

	pfd, _ := getPollFDAndNetwork(r)
	// TODO(panjf2000): 运行一些测试，看看我们是否应该为 splice 解锁非流式描述符。
	// 流式描述符从 splice(2) 中获益最大，而旧内核甚至不支持非流式描述符，
	// 在那些内核中 splice(2) 只会返回 EINVAL；较新的内核支持像 UDP 这样的非流式描述符，但我非常
	// 怀疑 splice(2) 是否能帮到非流式描述符，因为它们通常分别发送小帧，
	// 而一次 splice 调用只会产生一帧。
	// splice(2) 适合处理大数据，但分片的产生在这里削弱了它的优势。
	// 因此，如果 r 不是流式描述符，就不必费心尝试 splice。
	if pfd == nil || !pfd.IsStream {
		return
	}

	written, handled, err = pollSplice(&f.pfd, pfd, remain)

	if lr != nil {
		lr.N = remain - written
	}

	return written, handled, wrapSyscallError("splice", err)
}

func (f *File) copyFileRange(r io.Reader) (written int64, handled bool, err error) {
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

// getPollFDAndNetwork 尝试从给定接口获取 poll.FD 和网络类型，
// 其方式是期望 i 的底层类型是 syscall.Conn 的实现，
// 且该实现包含一个 *net.rawConn。
func getPollFDAndNetwork(i any) (*poll.FD, poll.String) {
	sc, ok := i.(syscall.Conn)
	if !ok {
		return nil, ""
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return nil, ""
	}
	irc, ok := rc.(interface {
		PollFD() *poll.FD
		Network() poll.String
	})
	if !ok {
		return nil, ""
	}
	return irc.PollFD(), irc.Network()
}

func isUnixOrTCP(network string) bool {
	switch network {
	case "tcp", "tcp4", "tcp6", "unix":
		return true
	default:
		return false
	}
}
