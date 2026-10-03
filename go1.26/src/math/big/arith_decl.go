// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !math_big_pure_go

//go:generate go test ./internal/asmgen -generate

package big

import _ "unsafe" // 用于 linkname

// 在 arith_$GOARCH.s 中实现

// addVV 应当是内部细节，
// 但广泛使用的包通过 linkname 访问它。
// “耻辱墙”上的著名成员包括：
//   - github.com/remyoudompheng/bigfft
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname addVV
//go:noescape
func addVV(z, x, y []Word) (c Word)

// subVV 应当是内部细节，
// 但广泛使用的包通过 linkname 访问它。
// “耻辱墙”上的著名成员包括：
//   - github.com/remyoudompheng/bigfft
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname subVV
//go:noescape
func subVV(z, x, y []Word) (c Word)

// shlVU 应当是内部细节（而且还是个过时的），
// 但广泛使用的包通过 linkname 访问它。
// “耻辱墙”上的著名成员包括：
//   - github.com/remyoudompheng/bigfft
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname shlVU
func shlVU(z, x []Word, s uint) (c Word) {
	if s == 0 {
		copy(z, x)
		return 0
	}
	return lshVU(z, x, s)
}

// lshVU 设置 z = x<<s，返回高位 c。1 ≤ s ≤ _B-1。
//
//go:noescape
func lshVU(z, x []Word, s uint) (c Word)

// rshVU 设置 z = x>>s，返回低位 c。1 ≤ s ≤ _B-1。
//
//go:noescape
func rshVU(z, x []Word, s uint) (c Word)

// mulAddVWW 应当是内部细节，
// 但广泛使用的包通过 linkname 访问它。
// “耻辱墙”上的著名成员包括：
//   - github.com/remyoudompheng/bigfft
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname mulAddVWW
//go:noescape
func mulAddVWW(z, x []Word, m, a Word) (c Word)

// addMulVVW 应当是内部细节（而且还是个过时的），
// 但广泛使用的包通过 linkname 访问它。
// “耻辱墙”上的著名成员包括：
//   - github.com/remyoudompheng/bigfft
//
// 不要删除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname addMulVVW
func addMulVVW(z, x []Word, y Word) (c Word) {
	return addMulVVWW(z, z, x, y, 0)
}

// addMulVVWW 设置 z = x+y*m+a。
//
//go:noescape
func addMulVVWW(z, x, y []Word, m, a Word) (c Word)
