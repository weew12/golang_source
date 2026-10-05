// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filepath

import (
	"strings"
	"syscall"
)

// normVolumeName 类似于 VolumeName，但会将盘符转为大写。
// EvalSymlinks 的结果必须唯一，因此我们有
// EvalSymlinks(`c:\a`) == EvalSymlinks(`C:\a`)。
func normVolumeName(path string) string {
	volume := VolumeName(path)

	if len(volume) > 2 { // 是UNC
		return volume
	}

	return strings.ToUpper(volume)
}

// normBase 返回 path 的最后一个元素，且大小写正确。
func normBase(path string) (string, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}

	var data syscall.Win32finddata

	h, err := syscall.FindFirstFile(p, &data)
	if err != nil {
		return "", err
	}
	syscall.FindClose(h)

	return syscall.UTF16ToString(data.FileName[:]), nil
}

// baseIsDotDot 报告 path 的最后一个元素是否为 ".."。
// 给定的 path 应当已事先经过 'Clean'。
func baseIsDotDot(path string) bool {
	i := strings.LastIndexByte(path, Separator)
	return path[i+1:] == ".."
}

// toNorm 返回保证唯一的规范化路径。
// 它应接受以下格式：
//   - UNC 路径                              （例如 \\server\share\foo\bar）
//   - 绝对路径                              （例如 C:\foo\bar）
//   - 以盘符开头的相对路径                  （例如 C:foo\bar, C:..\foo\bar, C:.., C:.）
//   - 以 '\' 开头的相对路径                 （例如 \foo\bar）
//   - 不以 '\' 开头的相对路径               （例如 foo\bar, ..\foo\bar, .., .）
//
// 返回的规范化路径将与输入路径具有相同的形式（上述 5 种之一）。
// 如果两个路径 A 和 B 以相同格式指向同一文件，则 toNorm(A) 应等于 toNorm(B)。
// 除测试外，normBase 参数应等于 normBase 函数。参见 normBase 函数的文档。
func toNorm(path string, normBase func(string) (string, error)) (string, error) {
	if path == "" {
		return path, nil
	}

	volume := normVolumeName(path)
	path = path[len(volume):]

	// 跳过特殊情况
	if path == "" || path == "." || path == `\` {
		return volume + path, nil
	}

	var normPath string

	for {
		if baseIsDotDot(path) {
			normPath = path + `\` + normPath

			break
		}

		name, err := normBase(volume + path)
		if err != nil {
			return "", err
		}

		normPath = name + `\` + normPath

		i := strings.LastIndexByte(path, Separator)
		if i == -1 {
			break
		}
		if i == 0 { // `\Go` 或 `C:\Go`
			normPath = `\` + normPath

			break
		}

		path = path[:i]
	}

	normPath = normPath[:len(normPath)-1] // 移除尾部的 '\'

	return volume + normPath, nil
}

func evalSymlinks(path string) (string, error) {
	newpath, err := walkSymlinks(path)
	if err != nil {
		return "", err
	}
	newpath, err = toNorm(newpath, normBase)
	if err != nil {
		return "", err
	}
	return newpath, nil
}
