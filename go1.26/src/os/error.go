// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/poll"
	"io/fs"
)

// 一些常见系统调用错误的可移植对应物。
//
// 从此包返回的错误可以用 [errors.Is] 针对这些错误进行测试。
var (
	// ErrInvalid 表示参数无效。
	// 当接收者为 nil 时，File 上的方法将返回此错误。
	ErrInvalid = fs.ErrInvalid // "invalid argument"

	ErrPermission = fs.ErrPermission // "permission denied"
	ErrExist      = fs.ErrExist      // "file already exists"
	ErrNotExist   = fs.ErrNotExist   // "file does not exist"
	ErrClosed     = fs.ErrClosed     // "file already closed"

	ErrNoDeadline       = errNoDeadline()       // "file type does not support deadline"
	ErrDeadlineExceeded = errDeadlineExceeded() // "i/o timeout"
)

func errNoDeadline() error { return poll.ErrNoDeadline }

// errDeadlineExceeded 返回 os.ErrDeadlineExceeded 的值。
// 此错误来自 internal/poll 包，该包也被 net 包使用。
// 这样做可以确保 net 包在超过截止时间时返回 os.ErrDeadlineExceeded，
// 正如 net.Conn.SetDeadline 所记录的那样，而无需在 net 包中做任何额外工作，
// 也无需让 internal/poll 包导入 os（它无法这样做，因为那会造成循环依赖）。
func errDeadlineExceeded() error { return poll.ErrDeadlineExceeded }

type timeout interface {
	Timeout() bool
}

// PathError 记录一个错误以及导致它的操作和文件路径。
type PathError = fs.PathError

// SyscallError 记录来自特定系统调用的错误。
type SyscallError struct {
	Syscall string
	Err     error
}

func (e *SyscallError) Error() string { return e.Syscall + ": " + e.Err.Error() }

func (e *SyscallError) Unwrap() error { return e.Err }

// Timeout 报告此错误是否表示超时。
func (e *SyscallError) Timeout() bool {
	t, ok := e.Err.(timeout)
	return ok && t.Timeout()
}

// NewSyscallError 返回一个新的 [SyscallError] 作为错误，
// 其中包含给定的系统调用名称和错误详情。
// 为方便起见，如果 err 为 nil，NewSyscallError 返回 nil。
func NewSyscallError(syscall string, err error) error {
	if err == nil {
		return nil
	}
	return &SyscallError{syscall, err}
}

// IsExist 返回一个布尔值，指示其参数是否已知报告
// 文件或目录已存在。它可由 [ErrExist] 以及
// 某些系统调用错误满足。
//
// 此函数早于 [errors.Is]。它仅支持由 os 包返回的错误。
// 新代码应使用 errors.Is(err, fs.ErrExist)。
func IsExist(err error) bool {
	return underlyingErrorIs(err, ErrExist)
}

// IsNotExist 返回一个布尔值，指示其参数是否已知
// 报告文件或目录不存在。它可由
// [ErrNotExist] 以及某些系统调用错误满足。
//
// 此函数早于 [errors.Is]。它仅支持由 os 包返回的错误。
// 新代码应使用 errors.Is(err, fs.ErrNotExist)。
func IsNotExist(err error) bool {
	return underlyingErrorIs(err, ErrNotExist)
}

// IsPermission 返回一个布尔值，指示其参数是否已知
// 报告权限被拒绝。它可由 [ErrPermission] 以及
// 某些系统调用错误满足。
//
// 此函数早于 [errors.Is]。它仅支持由 os 包返回的错误。
// 新代码应使用 errors.Is(err, fs.ErrPermission)。
func IsPermission(err error) bool {
	return underlyingErrorIs(err, ErrPermission)
}

// IsTimeout 返回一个布尔值，指示其参数是否已知
// 报告发生了超时。
//
// 此函数早于 [errors.Is]，并且错误是否表示超时的概念
// 可能含糊不清。例如，Unix 错误 EWOULDBLOCK
// 有时表示超时，有时不表示。
// 新代码应使用 errors.Is，并传入适合返回该错误的调用
// 的值，例如 [os.ErrDeadlineExceeded]。
func IsTimeout(err error) bool {
	terr, ok := underlyingError(err).(timeout)
	return ok && terr.Timeout()
}

func underlyingErrorIs(err, target error) bool {
	// 注意，此函数不是 errors.Is：
	// underlyingError 只解包它历来处理的特定错误包装类型，
	// 而不是所有实现了 Unwrap() 的错误。
	err = underlyingError(err)
	if err == target {
		return true
	}
	// 为保持先前的行为，只检查系统调用错误。
	e, ok := err.(syscallErrorType)
	return ok && e.Is(target)
}

// underlyingError 返回已知 os 错误类型的底层错误。
func underlyingError(err error) error {
	switch err := err.(type) {
	case *PathError:
		return err.Err
	case *LinkError:
		return err.Err
	case *SyscallError:
		return err.Err
	}
	return err
}
