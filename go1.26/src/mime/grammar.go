// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mime

// isTSpecial 报告字节 c 是否在 RFC 1521 和 RFC 2045 中定义的 'tspecials' 集合中。
func isTSpecial(c byte) bool {
	// tspecials :=  "(" / ")" / "<" / ">" / "@" /
	//               "," / ";" / ":" / "\" / <">
	//               "/" / "[" / "]" / "?" / "="
	//
	// mask 是一个 128 位位图，1 表示允许的字节，
	// 这样可以通过一次移位和一次与运算来测试字节 c。
	// 若 c >= 128，则 1<<c 和 1<<(c-64) 都为零，
	// 该函数将返回 false。
	const mask = 0 |
		1<<'(' |
		1<<')' |
		1<<'<' |
		1<<'>' |
		1<<'@' |
		1<<',' |
		1<<';' |
		1<<':' |
		1<<'\\' |
		1<<'"' |
		1<<'/' |
		1<<'[' |
		1<<']' |
		1<<'?' |
		1<<'='
	return ((uint64(1)<<c)&(mask&(1<<64-1)) |
		(uint64(1)<<(c-64))&(mask>>64)) != 0
}

// isTokenChar 报告字节 c 是否在 RFC 1521 和 RFC 2045 中定义的 'token' 集合中。
func isTokenChar(c byte) bool {
	// token := 1*<any (US-ASCII) CHAR except SPACE, CTLs,
	//             or tspecials>
	//
	// mask 是一个 128 位位图，1 表示允许的字节，
	// 这样可以通过一次移位和一次与运算来测试字节 c。
	// 若 c >= 128，则 1<<c 和 1<<(c-64) 都为零，
	// 该函数将返回 false。
	const mask = 0 |
		(1<<(10)-1)<<'0' |
		(1<<(26)-1)<<'a' |
		(1<<(26)-1)<<'A' |
		1<<'!' |
		1<<'#' |
		1<<'$' |
		1<<'%' |
		1<<'&' |
		1<<'\'' |
		1<<'*' |
		1<<'+' |
		1<<'-' |
		1<<'.' |
		1<<'^' |
		1<<'_' |
		1<<'`' |
		1<<'{' |
		1<<'|' |
		1<<'}' |
		1<<'~'
	return ((uint64(1)<<c)&(mask&(1<<64-1)) |
		(uint64(1)<<(c-64))&(mask>>64)) != 0
}

// isToken 报告字符串 s 是否为 RFC 1521 和 RFC 2045 中定义的 'token'。
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if !isTokenChar(c) {
			return false
		}
	}
	return true
}
