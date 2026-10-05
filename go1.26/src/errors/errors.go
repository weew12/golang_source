// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package errors 实现了操作错误的函数。
//
// [New] 函数创建的错误，其内容仅为一条文本消息。
//
// 如果错误 e 的类型具有以下方法之一，则称 e 包装了另一个错误：
//
//	Unwrap() error
//	Unwrap() []error
//
// 如果 e.Unwrap() 返回一个非 nil 的错误 w 或包含 w 的切片，
// 则称 e 包装了 w。e.Unwrap() 返回 nil 错误
// 表示 e 没有包装任何错误。Unwrap 方法返回
// 包含 nil 错误值的 []error 是无效的。
//
// 创建包装错误的一种简便方法是调用 [fmt.Errorf] 并
// 对错误参数应用 %w 动词：
//
//	wrapsErr := fmt.Errorf("... %w ...", ..., err, ...)
//
// 对错误进行连续解包会形成一棵树。[Is] 和 [As]
// 函数通过先检查错误本身，然后依次检查其每个
// 子节点的树来检查错误的树
// （先序、深度优先遍历）。
//
// 关于包装的理念以及何时进行包装的更深入讨论，
// 参见 https://go.dev/blog/go1.13-errors。
//
// [Is] 检查其第一个参数的树，寻找与第二个参数
// 匹配的错误。它报告是否找到匹配项。应优先使用
// 它而不是简单的相等性检查：
//
//	if errors.Is(err, fs.ErrExist)
//
// 优于
//
//	if err == fs.ErrExist
//
// 因为如果 err 包装了 [io/fs.ErrExist]，前者会成功。
//
// [AsType] 检查其参数的树，寻找类型与其类型参数
// 匹配的错误。如果成功，则返回该类型对应的值
// 以及 true。否则，返回该类型的零值
// 以及 false。以下形式
//
//	if perr, ok := errors.AsType[*fs.PathError](err); ok {
//		fmt.Println(perr.Path)
//	}
//
// 优于
//
//	if perr, ok := err.(*fs.PathError); ok {
//		fmt.Println(perr.Path)
//	}
//
// 因为如果 err 包装了一个 [*io/fs.PathError]，前者会成功。
package errors

// New 返回一个格式化后为给定文本的错误。
// 即使文本相同，每次调用 New 也都会返回一个不同的错误值。
func New(text string) error {
	return &errorString{text}
}

// errorString 是 error 的一个简单实现。
type errorString struct {
	s string
}

func (e *errorString) Error() string {
	return e.s
}

// ErrUnsupported 表示请求的操作无法执行，因为
// 它不受支持。例如，在使用不支持硬链接的
// 文件系统时调用 [os.Link]。
//
// 函数和方法不应返回此错误，而应返回一个
// 包含适当上下文、满足以下条件的错误：
//
//	errors.Is(err, errors.ErrUnsupported)
//
// 可以通过直接包装 ErrUnsupported 或实现 [Is] 方法来实现。
//
// 函数和方法应记录将会返回包装此错误的
// 错误的情况。
var ErrUnsupported = New("unsupported operation")
