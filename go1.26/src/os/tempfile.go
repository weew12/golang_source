// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"errors"
	"internal/bytealg"
	"internal/strconv"
	_ "unsafe" // 用于 go:linkname
)

// 由 runtime 提供的随机数源。
// 我们生成随机的临时文件名，这样文件很可能还不存在——
// 从而将 TempFile 中的尝试次数降到最低。
//
//go:linkname runtime_rand runtime.rand
func runtime_rand() uint64

func nextRandom() string {
	return strconv.FormatUint(uint64(uint32(runtime_rand())), 10)
}

// CreateTemp 在目录 dir 中创建一个新的临时文件，
// 以读写方式打开该文件，并返回结果文件。
// 文件名通过在 pattern 末尾追加一个随机字符串生成。
// 如果 pattern 中包含 "*"，随机字符串会替换最后一个 "*"。
// 文件以模式 0o600（受 umask 影响之前）创建。
// 如果 dir 为空字符串，CreateTemp 使用 [TempDir] 返回的默认临时文件目录。
// 多个程序或 goroutine 同时调用 CreateTemp 时不会选择同一个文件。
// 调用者可以使用文件的 Name 方法查找该文件的路径名。
// 当不再需要该文件时，删除它是调用者的责任。
func CreateTemp(dir, pattern string) (*File, error) {
	if dir == "" {
		dir = TempDir()
	}

	prefix, suffix, err := prefixAndSuffix(pattern)
	if err != nil {
		return nil, &PathError{Op: "createtemp", Path: pattern, Err: err}
	}
	prefix = joinPath(dir, prefix)

	try := 0
	for {
		name := prefix + nextRandom() + suffix
		f, err := OpenFile(name, O_RDWR|O_CREATE|O_EXCL, 0600)
		if IsExist(err) {
			if try++; try < 10000 {
				continue
			}
			return nil, &PathError{Op: "createtemp", Path: prefix + "*" + suffix, Err: ErrExist}
		}
		return f, err
	}
}

var errPatternHasSeparator = errors.New("pattern contains path separator")

// prefixAndSuffix 按最后一个通配符 "*"（如果存在）拆分 pattern，
// 返回 "*" 之前的部分作为 prefix，之后的部分作为 suffix。
func prefixAndSuffix(pattern string) (prefix, suffix string, err error) {
	for i := 0; i < len(pattern); i++ {
		if IsPathSeparator(pattern[i]) {
			return "", "", errPatternHasSeparator
		}
	}
	if pos := bytealg.LastIndexByteString(pattern, '*'); pos != -1 {
		prefix, suffix = pattern[:pos], pattern[pos+1:]
	} else {
		prefix = pattern
	}
	return prefix, suffix, nil
}

// MkdirTemp 在目录 dir 中创建一个新的临时目录，
// 并返回新目录的路径名。
// 新目录的名称通过在 pattern 末尾追加一个随机字符串生成。
// 如果 pattern 中包含 "*"，则随机字符串会替换最后一个 "*"。
// 目录以模式 0o700（受 umask 影响之前）创建。
// 如果 dir 为空字符串，MkdirTemp 使用 TempDir 返回的默认临时文件目录。
// 多个程序或 goroutine 同时调用 MkdirTemp 时不会选择同一个目录。
// 当不再需要该目录时，删除它是调用者的责任。
func MkdirTemp(dir, pattern string) (string, error) {
	if dir == "" {
		dir = TempDir()
	}

	prefix, suffix, err := prefixAndSuffix(pattern)
	if err != nil {
		return "", &PathError{Op: "mkdirtemp", Path: pattern, Err: err}
	}
	prefix = joinPath(dir, prefix)

	try := 0
	for {
		name := prefix + nextRandom() + suffix
		err := Mkdir(name, 0700)
		if err == nil {
			return name, nil
		}
		if IsExist(err) {
			if try++; try < 10000 {
				continue
			}
			return "", &PathError{Op: "mkdirtemp", Path: prefix + "*" + suffix, Err: ErrExist}
		}
		if IsNotExist(err) {
			if _, err := Stat(dir); IsNotExist(err) {
				return "", err
			}
		}
		return "", err
	}
}

func joinPath(dir, name string) string {
	if len(dir) > 0 && IsPathSeparator(dir[len(dir)-1]) {
		return dir + name
	}
	return dir + string(PathSeparator) + name
}
