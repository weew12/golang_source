// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmt

import (
	"errors"
	"internal/stringslite"
	"slices"
)

// Errorf 根据格式说明符格式化，并将结果字符串作为满足 error 接口的值返回。
//
// 若格式说明符包含 %w 动词且操作数为错误类型，返回的错误将实现 Unwrap 方法并返回该操作数。
// 若存在多个 %w 动词，返回的错误将实现 Unwrap 方法并返回 []error，其中包含所有 %w 操作数（按参数出现顺序排列）。
// 为 %w 动词提供未实现 error 接口的操作数是无效的。除此之外，%w 动词是 %v 的同义词。
func Errorf(format string, a ...any) (err error) {
	// 此函数以一种不太自然的方式拆分，
	// 以便它自身和 errors.New 调用均可内联。
	if err = errorf(format, a...); err != nil {
		return err
	}
	// 无需格式化。可避免一些内存分配和其他工作。
	// 详见 https://go.dev/cl/708836。
	return errors.New(format)
}

// errorf 格式化并返回错误值；若无需格式化则返回 nil。
func errorf(format string, a ...any) error {
	if len(a) == 0 && stringslite.IndexByte(format, '%') == -1 {
		return nil
	}
	p := newPrinter()
	p.wrapErrs = true
	p.doPrintf(format, a)
	s := string(p.buf)
	var err error
	switch len(p.wrappedErrs) {
	case 0:
		err = errors.New(s)
	case 1:
		w := &wrapError{msg: s}
		w.err, _ = a[p.wrappedErrs[0]].(error)
		err = w
	default:
		if p.reordered {
			slices.Sort(p.wrappedErrs)
		}
		var errs []error
		for i, argNum := range p.wrappedErrs {
			if i > 0 && p.wrappedErrs[i-1] == argNum {
				continue
			}
			if e, ok := a[argNum].(error); ok {
				errs = append(errs, e)
			}
		}
		err = &wrapErrors{s, errs}
	}
	p.free()
	return err
}

type wrapError struct {
	msg string
	err error
}

func (e *wrapError) Error() string {
	return e.msg
}

func (e *wrapError) Unwrap() error {
	return e.err
}

type wrapErrors struct {
	msg  string
	errs []error
}

func (e *wrapErrors) Error() string {
	return e.msg
}

func (e *wrapErrors) Unwrap() []error {
	return e.errs
}
