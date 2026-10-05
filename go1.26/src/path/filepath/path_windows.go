// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filepath

import (
	"os"
	"strings"
	"syscall"
)

// HasPrefix 的存在是为了历史兼容性，不应使用。
//
// Deprecated: HasPrefix 不遵守路径边界，
// 并且在需要时不会忽略大小写。
func HasPrefix(p, prefix string) bool {
	if strings.HasPrefix(p, prefix) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(p), strings.ToLower(prefix))
}

func splitList(path string) []string {
	// os/exec 的 LookPath 中使用了相同的实现；
	// 修改此处时请考虑修改 os/exec。

	if path == "" {
		return []string{}
	}

	// 拆分路径，尊重但保留引号。
	list := []string{}
	start := 0
	quo := false
	for i := 0; i < len(path); i++ {
		switch c := path[i]; {
		case c == '"':
			quo = !quo
		case c == ListSeparator && !quo:
			list = append(list, path[start:i])
			start = i + 1
		}
	}
	list = append(list, path[start:])

	// 移除引号。
	for i, s := range list {
		list[i] = strings.ReplaceAll(s, `"`, ``)
	}

	return list
}

func abs(path string) (string, error) {
	if path == "" {
		// syscall.FullPath 在空路径上返回错误，因为它不是有效路径。
		// 为实现 Abs 在空字符串输入时返回工作目录的行为，
		// 通过将其改为 "." 路径来特殊处理空路径。参见 golang.org/issue/24441。
		path = "."
	}
	fullPath, err := syscall.FullPath(path)
	if err != nil {
		return "", err
	}
	return Clean(fullPath), nil
}

func join(elem []string) string {
	var b strings.Builder
	var lastChar byte
	for _, e := range elem {
		switch {
		case b.Len() == 0:
			// 原样添加第一个非空路径元素。
		case os.IsPathSeparator(lastChar):
			// 如果路径以斜杠结尾，则从下一个路径元素中去除所有前导斜杠，
			// 以避免由非 UNC 元素创建出 UNC 路径（任何以 "\\" 开头的路径）。
			//
			// 当第一个元素是不完整的 UNC 路径（例如 "\\"）时，
			// Join 的正确行为未明确规定。我们目前会连接后续
			// 元素，因此 Join("\\", "host", "share") 产生 "\\host\share"。
			for len(e) > 0 && os.IsPathSeparator(e[0]) {
				e = e[1:]
			}
			// 如果路径是 \ 且下一个路径元素是 ??，
			// 则添加额外的 .\ 以创建 \.\??，而不是 \??\
			// （Root Local Device 路径）。
			if b.Len() == 1 && strings.HasPrefix(e, "??") && (len(e) == len("??") || os.IsPathSeparator(e[2])) {
				b.WriteString(`.\`)
			}
		case lastChar == ':':
			// 如果路径以冒号结尾，保持路径相对于驱动器上的当前目录，
			// 并且不添加分隔符。保留下一个路径元素中的前导斜杠，
			// 这可能使路径变为绝对路径。
			//
			// 	Join(`C:`, `f`) = `C:f`
			//	Join(`C:`, `\f`) = `C:\f`
		default:
			// 在所有其他情况下，在元素之间添加分隔符。
			b.WriteByte('\\')
			lastChar = '\\'
		}
		if len(e) > 0 {
			b.WriteString(e)
			lastChar = e[len(e)-1]
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return Clean(b.String())
}

func sameWord(a, b string) bool {
	return strings.EqualFold(a, b)
}
