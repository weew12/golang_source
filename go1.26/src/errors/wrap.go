// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errors

import (
	"internal/reflectlite"
)

// Unwrap 返回对 err 调用 Unwrap 方法的结果，如果 err 的类型
// 包含一个返回 error 的 Unwrap 方法。
// 否则，Unwrap 返回 nil。
//
// Unwrap 仅调用形式为 "Unwrap() error" 的方法。
// 特别地，Unwrap 不会解包由 [Join] 返回的错误。
func Unwrap(err error) error {
	u, ok := err.(interface {
		Unwrap() error
	})
	if !ok {
		return nil
	}
	return u.Unwrap()
}

// Is 报告 err 的树中是否有任何错误与 target 匹配。
// target 必须是可比较的。
//
// 该树由 err 本身，以及通过重复调用其 Unwrap() error 或
// Unwrap() []error 方法所得到的错误组成。当 err 包装多个错误时，
// Is 先检查 err，然后对其子节点进行深度优先遍历。
//
// 如果某个错误等于 target，或者它实现了一个方法 Is(error) bool
// 使得 Is(target) 返回 true，则认为该错误与 target 匹配。
//
// 错误类型可以提供 Is 方法，以使其可被视为等同于一个
// 已有的错误。例如，如果 MyError 定义了
//
//	func (m MyError) Is(target error) bool { return target == fs.ErrExist }
//
// 那么 Is(MyError{}, fs.ErrExist) 返回 true。标准库中的示例
// 参见 [syscall.Errno.Is]。Is 方法应仅浅层地比较 err 和
// target，且不应在两者上调用 [Unwrap]。
func Is(err, target error) bool {
	if err == nil || target == nil {
		return err == target
	}

	isComparable := reflectlite.TypeOf(target).Comparable()
	return is(err, target, isComparable)
}

func is(err, target error, targetComparable bool) bool {
	for {
		if targetComparable && err == target {
			return true
		}
		if x, ok := err.(interface{ Is(error) bool }); ok && x.Is(target) {
			return true
		}
		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
			if err == nil {
				return false
			}
		case interface{ Unwrap() []error }:
			for _, err := range x.Unwrap() {
				if is(err, target, targetComparable) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
}

// As 在 err 的树中查找第一个与 target 匹配的错误，如果找到，
// 则将 target 设置为该错误值并返回 true。否则，返回 false。
//
// 在大多数情况下，优先使用 [AsType]。As 与 [AsType] 等价，
// 但它设置其 target 参数而非返回匹配的错误，且不要求其
// target 参数实现 error。
//
// 该树由 err 本身，以及通过重复调用其 Unwrap() error 或
// Unwrap() []error 方法所得到的错误组成。当 err 包装多个错误时，
// As 先检查 err，然后对其子节点进行深度优先遍历。
//
// 如果错误的具化值可赋值给 target 所指向的值，或者该错误
// 具有一个方法 As(any) bool 使得 As(target) 返回 true，
// 则该错误与 target 匹配。在后一种情况下，由 As 方法负责
// 设置 target。
//
// 错误类型可以提供 As 方法，以使其可被视为
// 另一种错误类型。
//
// 如果 target 不是非 nil 指针，指向实现 error 的类型或
// 任意接口类型，则 As 会 panic。
func As(err error, target any) bool {
	if err == nil {
		return false
	}
	if target == nil {
		panic("errors: target cannot be nil")
	}
	val := reflectlite.ValueOf(target)
	typ := val.Type()
	if typ.Kind() != reflectlite.Ptr || val.IsNil() {
		panic("errors: target must be a non-nil pointer")
	}
	targetType := typ.Elem()
	if targetType.Kind() != reflectlite.Interface && !targetType.Implements(errorType) {
		panic("errors: *target must be interface or implement error")
	}
	return as(err, target, val, targetType)
}

func as(err error, target any, targetVal reflectlite.Value, targetType reflectlite.Type) bool {
	for {
		if reflectlite.TypeOf(err).AssignableTo(targetType) {
			targetVal.Elem().Set(reflectlite.ValueOf(err))
			return true
		}
		if x, ok := err.(interface{ As(any) bool }); ok && x.As(target) {
			return true
		}
		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
			if err == nil {
				return false
			}
		case interface{ Unwrap() []error }:
			for _, err := range x.Unwrap() {
				if err == nil {
					continue
				}
				if as(err, target, targetVal, targetType) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
}

var errorType = reflectlite.TypeOf((*error)(nil)).Elem()

// AsType 在 err 的树中查找第一个与类型 E 匹配的错误，如果找到，
// 则返回该错误值以及 true。否则，返回 E 的零值
// 以及 false。
//
// 该树由 err 本身，以及通过重复调用其 Unwrap() error 或
// Unwrap() []error 方法所得到的错误组成。当 err 包装多个
// 错误时，AsType 先检查 err，然后对其子节点
// 进行深度优先遍历。
//
// 如果类型断言 err.(E) 成立，或者该错误具有一个方法
// As(any) bool 使得当 target 为非 nil 的 *E 时
// err.As(target) 返回 true，则错误 err 与类型 E 匹配。
// 在后一种情况下，由 As 方法负责设置 target。
func AsType[E error](err error) (E, bool) {
	if err == nil {
		var zero E
		return zero, false
	}
	var pe *E // 惰性初始化
	return asType(err, &pe)
}

func asType[E error](err error, ppe **E) (_ E, _ bool) {
	for {
		if e, ok := err.(E); ok {
			return e, true
		}
		if x, ok := err.(interface{ As(any) bool }); ok {
			if *ppe == nil {
				*ppe = new(E)
			}
			if x.As(*ppe) {
				return **ppe, true
			}
		}
		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
			if err == nil {
				return
			}
		case interface{ Unwrap() []error }:
			for _, err := range x.Unwrap() {
				if err == nil {
					continue
				}
				if x, ok := asType(err, ppe); ok {
					return x, true
				}
			}
			return
		default:
			return
		}
	}
}
