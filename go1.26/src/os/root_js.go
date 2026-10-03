// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package os

import (
	"errors"
	"slices"
	"syscall"
)

// checkPathEscapes 报告 name 是否逃逸出根。
//
// 由于缺少 openat，当符号链接在解析过程中发生变化时，
// checkPathEscapes 容易受到 TOCTOU 竞态影响。
func checkPathEscapes(r *Root, name string) error {
	return checkPathEscapesInternal(r, name, false)
}

// checkPathEscapesLstat 报告 name 是否逃逸出根。
// 它不会解析最后一个路径组件中的符号链接。
//
// 由于缺少 openat，当符号链接在解析过程中发生变化时，
// checkPathEscapes 容易受到 TOCTOU 竞态影响。
func checkPathEscapesLstat(r *Root, name string) error {
	return checkPathEscapesInternal(r, name, true)
}

func checkPathEscapesInternal(r *Root, name string, lstat bool) error {
	if r.root.closed.Load() {
		return ErrClosed
	}
	parts, suffixSep, err := splitPathInRoot(name, nil, nil)
	if err != nil {
		return err
	}

	i := 0
	symlinks := 0
	base := r.root.name
	for i < len(parts) {
		if parts[i] == ".." {
			// 解析一个或多个父级（".."）路径组件。
			end := i + 1
			for end < len(parts) && parts[end] == ".." {
				end++
			}
			count := end - i
			if count > i {
				return errPathEscapes
			}
			parts = slices.Delete(parts, i-count, end)
			i -= count
			base = r.root.name
			for j := range i {
				base = joinPath(base, parts[j])
			}
			continue
		}

		part := parts[i]
		if i == len(parts)-1 {
			if lstat {
				break
			}
			part += suffixSep
		}

		next := joinPath(base, part)
		fi, err := Lstat(next)
		if err != nil {
			if IsNotExist(err) {
				return nil
			}
			return underlyingError(err)
		}
		if fi.Mode()&ModeSymlink != 0 {
			link, err := Readlink(next)
			if err != nil {
				return errPathEscapes
			}
			symlinks++
			if symlinks > rootMaxSymlinks {
				return errors.New("too many symlinks")
			}
			newparts, newSuffixSep, err := splitPathInRoot(link, parts[:i], parts[i+1:])
			if err != nil {
				return err
			}
			if i == len(parts) {
				// suffixSep 包含链接目标中的任何尾随路径分隔符
				// 字符。
				// 如果我们要替换路径的剩余部分，则保留这些字符。
				// 如果我们要替换路径的某个中间组件，
				// 则忽略它们，因为中间组件必须始终
				// 是目录。
				suffixSep = newSuffixSep
			}
			parts = newparts
			continue
		}
		if !fi.IsDir() && i < len(parts)-1 {
			return syscall.ENOTDIR
		}

		base = next
		i++
	}
	return nil
}
