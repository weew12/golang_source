// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package exec

// LookPath 按照宿主操作系统的约定，在当前路径中搜索名为 file 的可执行文件。
// 如果 file 包含斜杠，则直接尝试它，不查询默认路径。
// 否则，成功时结果为绝对路径。
//
// 如果解析出的路径相对于当前目录，LookPath 会返回一个满足
// [errors.Is](err, [ErrDot]) 的错误。详情参见包文档。
//
// LookPath 在 PATH 环境变量所指定的目录中查找名为 file 的可执行文件，
// 但以下所述情况除外。
//
//   - 在 Windows 上，文件必须具有 PATHEXT 环境变量所指定的扩展名。
//     当 PATHEXT 未设置时，文件必须具有 ".com"、".exe"、".bat" 或
//     ".cmd" 扩展名。
//   - 在 Plan 9 上，LookPath 查询 path 环境变量。
//     如果 file 以 "/"、"#"、"./" 或 "../" 开头，则直接尝试它，
//     不查询 path。
//   - 在 Wasm 上，LookPath 始终返回错误。
func LookPath(file string) (string, error) {
	return lookPath(file)
}
