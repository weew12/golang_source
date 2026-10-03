// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cmp 提供与比较有序值相关的类型和函数。
package cmp

// Ordered 是一个约束，允许任何有序类型：任何
// 支持 < <= >= > 运算符的类型。
// 如果未来 Go 版本添加了新的有序类型，
// 该约束将被修改以包含它们。
//
// 注意，浮点类型可能包含 NaN（"非数字"）值。
// 当将 NaN 值与任何其他值（无论是否为 NaN）比较时，
// == 或 < 等运算符将始终报告 false。
// 有关比较 NaN 值的一致方法，请参见 [Compare] 函数。
type Ordered interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64 |
		~string
}

// Less 报告 x 是否小于 y。
// 对于浮点类型，NaN 被认为小于任何非 NaN，
// 并且 -0.0 不小于（等于）0.0。
func Less[T Ordered](x, y T) bool {
	return (isNaN(x) && !isNaN(y)) || x < y
}

// Compare 返回
//
//	如果 x 小于 y，则为 -1，
//	 如果 x 等于 y，则为 0，
//	如果 x 大于 y，则为 +1。
//
// 对于浮点类型，NaN 被认为小于任何非 NaN，
// NaN 被认为等于 NaN，并且 -0.0 等于 0.0。
func Compare[T Ordered](x, y T) int {
	xNaN := isNaN(x)
	yNaN := isNaN(y)
	if xNaN {
		if yNaN {
			return 0
		}
		return -1
	}
	if yNaN {
		return +1
	}
	if x < y {
		return -1
	}
	if x > y {
		return +1
	}
	return 0
}

// isNaN 报告 x 是否为 NaN，且不需要 math 包。
// 如果 T 不是浮点类型，它将始终返回 false。
func isNaN[T Ordered](x T) bool {
	return x != x
}

// Or 返回其参数中第一个不等于零值的参数。
// 如果没有任何参数非零，则返回零值。
func Or[T comparable](vals ...T) T {
	var zero T
	for _, val := range vals {
		if val != zero {
			return val
		}
	}
	return zero
}
