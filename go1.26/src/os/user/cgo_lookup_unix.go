// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (cgo || darwin) && !osusergo && unix && !android

package user

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

func current() (*User, error) {
	return lookupUnixUid(syscall.Getuid())
}

func lookupUser(username string) (*User, error) {
	var pwd _C_struct_passwd
	var found bool
	nameC := make([]byte, len(username)+1)
	copy(nameC, username)

	err := retryWithBuffer(userBuffer, func(buf []byte) syscall.Errno {
		var errno syscall.Errno
		pwd, found, errno = _C_getpwnam_r((*_C_char)(unsafe.Pointer(&nameC[0])),
			(*_C_char)(unsafe.Pointer(&buf[0])), _C_size_t(len(buf)))
		return errno
	})
	if err == syscall.ENOENT || (err == nil && !found) {
		return nil, UnknownUserError(username)
	}
	if err != nil {
		return nil, fmt.Errorf("user: lookup username %s: %v", username, err)
	}
	return buildUser(&pwd), nil
}

func lookupUserId(uid string) (*User, error) {
	i, e := strconv.Atoi(uid)
	if e != nil {
		return nil, e
	}
	return lookupUnixUid(i)
}

func lookupUnixUid(uid int) (*User, error) {
	var pwd _C_struct_passwd
	var found bool

	err := retryWithBuffer(userBuffer, func(buf []byte) syscall.Errno {
		var errno syscall.Errno
		pwd, found, errno = _C_getpwuid_r(_C_uid_t(uid),
			(*_C_char)(unsafe.Pointer(&buf[0])), _C_size_t(len(buf)))
		return errno
	})
	if err == syscall.ENOENT || (err == nil && !found) {
		return nil, UnknownUserIdError(uid)
	}
	if err != nil {
		return nil, fmt.Errorf("user: lookup userid %d: %v", uid, err)
	}
	return buildUser(&pwd), nil
}

func buildUser(pwd *_C_struct_passwd) *User {
	u := &User{
		Uid:      strconv.FormatUint(uint64(_C_pw_uid(pwd)), 10),
		Gid:      strconv.FormatUint(uint64(_C_pw_gid(pwd)), 10),
		Username: _C_GoString(_C_pw_name(pwd)),
		Name:     _C_GoString(_C_pw_gecos(pwd)),
		HomeDir:  _C_GoString(_C_pw_dir(pwd)),
	}
	// pw_gecos 字段标准化程度不太高。一些文档说：
	// “它预期是一个以逗号分隔的个人数据列表，
	// 其中第一项是用户的全名。”
	u.Name, _, _ = strings.Cut(u.Name, ",")
	return u
}

func lookupGroup(groupname string) (*Group, error) {
	var grp _C_struct_group
	var found bool

	cname := make([]byte, len(groupname)+1)
	copy(cname, groupname)

	err := retryWithBuffer(groupBuffer, func(buf []byte) syscall.Errno {
		var errno syscall.Errno
		grp, found, errno = _C_getgrnam_r((*_C_char)(unsafe.Pointer(&cname[0])),
			(*_C_char)(unsafe.Pointer(&buf[0])), _C_size_t(len(buf)))
		return errno
	})
	if err == syscall.ENOENT || (err == nil && !found) {
		return nil, UnknownGroupError(groupname)
	}
	if err != nil {
		return nil, fmt.Errorf("user: lookup groupname %s: %v", groupname, err)
	}
	return buildGroup(&grp), nil
}

func lookupGroupId(gid string) (*Group, error) {
	i, e := strconv.Atoi(gid)
	if e != nil {
		return nil, e
	}
	return lookupUnixGid(i)
}

func lookupUnixGid(gid int) (*Group, error) {
	var grp _C_struct_group
	var found bool

	err := retryWithBuffer(groupBuffer, func(buf []byte) syscall.Errno {
		var errno syscall.Errno
		grp, found, errno = _C_getgrgid_r(_C_gid_t(gid),
			(*_C_char)(unsafe.Pointer(&buf[0])), _C_size_t(len(buf)))
		return syscall.Errno(errno)
	})
	if err == syscall.ENOENT || (err == nil && !found) {
		return nil, UnknownGroupIdError(strconv.Itoa(gid))
	}
	if err != nil {
		return nil, fmt.Errorf("user: lookup groupid %d: %v", gid, err)
	}
	return buildGroup(&grp), nil
}

func buildGroup(grp *_C_struct_group) *Group {
	g := &Group{
		Gid:  strconv.Itoa(int(_C_gr_gid(grp))),
		Name: _C_GoString(_C_gr_name(grp)),
	}
	return g
}

type bufferKind _C_int

var (
	userBuffer  = bufferKind(_C__SC_GETPW_R_SIZE_MAX)
	groupBuffer = bufferKind(_C__SC_GETGR_R_SIZE_MAX)
)

func (k bufferKind) initialSize() _C_size_t {
	sz := _C_sysconf(_C_int(k))
	if sz == -1 {
		// DragonFly 和 FreeBSD 没有 _SC_GETPW_R_SIZE_MAX。
		// 此外，并非所有 Linux 系统都有它。例如，musl libc 返回 -1。
		return 1024
	}
	if !isSizeReasonable(int64(sz)) {
		// 截断。如果这真的不够，retryWithBuffer 会在第一次运行时出错。
		return maxBufferSize
	}
	return _C_size_t(sz)
}

// retryWithBuffer 反复调用 f()，每次增大缓冲区的大小，
// 直到 f 成功、以非 ERANGE 错误失败，或缓冲区超过合理的限制。
func retryWithBuffer(kind bufferKind, f func([]byte) syscall.Errno) error {
	buf := make([]byte, kind.initialSize())
	for {
		errno := f(buf)
		if errno == 0 {
			return nil
		} else if runtime.GOOS == "aix" && errno+1 == 0 {
			// 在 AIX 上，getpwuid_r 在缓冲区溢出时似乎返回 -1，
			// 而不是 ERANGE。
		} else if errno != syscall.ERANGE {
			return errno
		}
		newSize := len(buf) * 2
		if !isSizeReasonable(int64(newSize)) {
			return fmt.Errorf("internal buffer exceeds %d bytes", maxBufferSize)
		}
		buf = make([]byte, newSize)
	}
}

const maxBufferSize = 1 << 20

func isSizeReasonable(sz int64) bool {
	return sz > 0 && sz <= maxBufferSize
}

// 因为我们不能在测试中使用 cgo：
func structPasswdForNegativeTest() _C_struct_passwd {
	sp := _C_struct_passwd{}
	*_C_pw_uidp(&sp) = 1<<32 - 2
	*_C_pw_gidp(&sp) = 1<<32 - 3
	return sp
}
