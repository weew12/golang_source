// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"errors"
	"path"
)

// SkipDir 作为 [WalkDirFunc] 的返回值，用于跳过当前指定的目录。
// 该值不会被任何函数作为错误返回。
var SkipDir = errors.New("skip this directory")

// SkipAll 作为 [WalkDirFunc] 的返回值，用于跳过所有剩余的文件和目录。
// 该值不会被任何函数作为错误返回。
var SkipAll = errors.New("skip everything and stop the walk")

// WalkDirFunc 是 [WalkDir] 遍历文件/目录时调用的函数类型。
//
// path 参数为遍历路径，以 [WalkDir] 的入参为前缀。
// 例如：以根目录 "dir" 调用 WalkDir，遍历到该目录下名为 "a" 的文件时，
// 遍历函数的 path 参数为 "dir/a"。
//
// d 参数为对应路径的 [DirEntry]。
//
// 函数返回的错误结果决定 [WalkDir] 的后续行为：
// 若返回特殊值 [SkipDir]，WalkDir 会跳过当前目录（d.IsDir() 为 true 时跳过 path 本身，否则跳过 path 的父目录）；
// 若返回特殊值 [SkipAll]，WalkDir 会跳过所有剩余文件和目录；
// 若返回其他非空错误，WalkDir 会立即停止遍历并返回该错误。
//
// err 参数用于报告路径相关的错误，代表 [WalkDir] 不会进入该目录遍历。
// 函数可自行处理该错误：如前文所述，返回错误会导致 WalkDir 停止整个目录树的遍历。
//
// [WalkDir] 会在两种场景下，以非空 err 参数调用该函数：
//
// 第一，根目录的初始 [Stat] 操作失败时，WalkDir 会调用该函数：
// path 设为根目录，d 设为 nil，err 设为 [fs.Stat] 返回的错误。
//
// 第二，目录的 ReadDir 方法（见 [ReadDirFile]）执行失败时，WalkDir 会调用该函数：
// path 设为目录路径，d 设为描述该目录的 [DirEntry]，err 设为 ReadDir 返回的错误。
// 第二种场景下，同一个目录路径会被调用两次函数：
// 第一次在尝试读取目录前调用，err 为 nil，允许函数返回 [SkipDir] 或 [SkipAll] 直接跳过读取；
// 第二次在 ReadDir 失败后调用，用于报告读取错误。（若 ReadDir 成功，则不会触发第二次调用。）
//
// WalkDirFunc 与 [path/filepath.WalkFunc] 的区别：
//
//   - 第二个参数类型为 [DirEntry]，而非 [FileInfo]。
//   - 读取目录前会先调用函数，可通过 [SkipDir] 跳过目录读取，或通过 [SkipAll] 跳过所有剩余内容。
//   - 目录读取失败时，会对该目录进行第二次函数调用以报告错误。
type WalkDirFunc func(path string, d DirEntry, err error) error

// walkDir 递归遍历路径，调用 walkDirFn 函数。
func walkDir(fsys FS, name string, d DirEntry, walkDirFn WalkDirFunc) error {
	if err := walkDirFn(name, d, nil); err != nil || !d.IsDir() {
		if err == SkipDir && d.IsDir() {
			// 成功跳过目录。
			err = nil
		}
		return err
	}

	dirs, err := ReadDir(fsys, name)
	if err != nil {
		// 第二次调用，报告 ReadDir 错误。
		err = walkDirFn(name, d, err)
		if err != nil {
			if err == SkipDir && d.IsDir() {
				err = nil
			}
			return err
		}
	}

	for _, d1 := range dirs {
		name1 := path.Join(name, d1.Name())
		if err := walkDir(fsys, name1, d1, walkDirFn); err != nil {
			if err == SkipDir {
				break
			}
			return err
		}
	}
	return nil
}

// WalkDir 遍历以 root 为根的文件树，对树中的每个文件/目录（包含根节点）调用 fn 函数。
//
// 遍历文件/目录时产生的所有错误都会由 fn 函数处理：
// 详情见 [fs.WalkDirFunc] 文档。
//
// 文件按字典序遍历，保证输出结果确定性，
// 但这要求 WalkDir 在遍历目录前，将整个目录读取到内存中。
//
// WalkDir 不会解析目录中的符号链接，
// 但若根节点 root 本身是符号链接，则会遍历其指向的目标。
func WalkDir(fsys FS, root string, fn WalkDirFunc) error {
	info, err := Stat(fsys, root)
	if err != nil {
		err = fn(root, nil, err)
	} else {
		err = walkDir(fsys, root, FileInfoToDirEntry(info), fn)
	}
	if err == SkipDir || err == SkipAll {
		return nil
	}
	return err
}
