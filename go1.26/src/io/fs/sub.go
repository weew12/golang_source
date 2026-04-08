// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"errors"
	"path"
)

// SubFS 是具备 Sub 方法的文件系统接口。
type SubFS interface {
	FS

	// Sub 返回与以 dir 为根目录的子树对应的 FS。
	Sub(dir string) (FS, error)
}

// Sub 返回与 fsys 中以 dir 为根目录的子树对应的 [FS]。
//
// 若 dir 为 "."，Sub 直接返回原文件系统 fsys。
// 否则，若文件系统实现了 [SubFS] 接口，Sub 会调用 fsys.Sub(dir)。
// 若未实现，Sub 会返回一个新的 [FS] 实现 sub，
// 其核心逻辑是将 sub.Open(name) 转化为 fsys.Open(path.Join(dir, name))。
// 该实现同时会对 ReadDir、ReadFile、ReadLink、Lstat 和 Glob 方法进行适配转换。
//
// 注意：Sub(os.DirFS("/"), "prefix") 等价于 os.DirFS("/prefix")，
// 且这两种方式都无法保证限制操作系统访问 "/prefix" 之外的路径。
// 原因是 [os.DirFS] 的实现不会检查 "/prefix" 内部指向其他目录的符号链接。
// 也就是说，[os.DirFS] 不能作为 chroot 风格安全机制的通用替代方案，
// 而 Sub 函数也不会改变这一特性。
func Sub(fsys FS, dir string) (FS, error) {
	if !ValidPath(dir) {
		return nil, &PathError{Op: "sub", Path: dir, Err: ErrInvalid}
	}
	if dir == "." {
		return fsys, nil
	}
	if fsys, ok := fsys.(SubFS); ok {
		return fsys.Sub(dir)
	}
	return &subFS{fsys, dir}, nil
}

var _ FS = (*subFS)(nil)
var _ ReadDirFS = (*subFS)(nil)
var _ ReadFileFS = (*subFS)(nil)
var _ ReadLinkFS = (*subFS)(nil)
var _ GlobFS = (*subFS)(nil)

// subFS 是子文件系统的实现，封装了根文件系统与子目录路径
type subFS struct {
	fsys FS
	dir  string
}

// fullName 将名称 name 映射为完整路径 dir/name。
func (f *subFS) fullName(op string, name string) (string, error) {
	if !ValidPath(name) {
		return "", &PathError{Op: op, Path: name, Err: ErrInvalid}
	}
	return path.Join(f.dir, name), nil
}

// shorten 将以 f.dir 为前缀的名称，截取为前缀后的相对路径。
func (f *subFS) shorten(name string) (rel string, ok bool) {
	if name == f.dir {
		return ".", true
	}
	if len(name) >= len(f.dir)+2 && name[len(f.dir)] == '/' && name[:len(f.dir)] == f.dir {
		return name[len(f.dir)+1:], true
	}
	return "", false
}

// fixErr 剔除 PathError 中的路径前缀 f.dir，简化错误信息中的路径。
func (f *subFS) fixErr(err error) error {
	if e, ok := err.(*PathError); ok {
		if short, ok := f.shorten(e.Path); ok {
			e.Path = short
		}
	}
	return err
}

func (f *subFS) Open(name string) (File, error) {
	full, err := f.fullName("open", name)
	if err != nil {
		return nil, err
	}
	file, err := f.fsys.Open(full)
	return file, f.fixErr(err)
}

func (f *subFS) ReadDir(name string) ([]DirEntry, error) {
	full, err := f.fullName("read", name)
	if err != nil {
		return nil, err
	}
	dir, err := ReadDir(f.fsys, full)
	return dir, f.fixErr(err)
}

func (f *subFS) ReadFile(name string) ([]byte, error) {
	full, err := f.fullName("read", name)
	if err != nil {
		return nil, err
	}
	data, err := ReadFile(f.fsys, full)
	return data, f.fixErr(err)
}

func (f *subFS) ReadLink(name string) (string, error) {
	full, err := f.fullName("readlink", name)
	if err != nil {
		return "", err
	}
	target, err := ReadLink(f.fsys, full)
	if err != nil {
		return "", f.fixErr(err)
	}
	return target, nil
}

func (f *subFS) Lstat(name string) (FileInfo, error) {
	full, err := f.fullName("lstat", name)
	if err != nil {
		return nil, err
	}
	info, err := Lstat(f.fsys, full)
	if err != nil {
		return nil, f.fixErr(err)
	}
	return info, nil
}

func (f *subFS) Glob(pattern string) ([]string, error) {
	// 检查模式格式是否合法
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	if pattern == "." {
		return []string{"."}, nil
	}

	full := f.dir + "/" + pattern
	list, err := Glob(f.fsys, full)
	for i, name := range list {
		name, ok := f.shorten(name)
		if !ok {
			return nil, errors.New("invalid result from inner fsys Glob: " + name + " not in " + f.dir) // 本包禁止使用 fmt
		}
		list[i] = name
	}
	return list, f.fixErr(err)
}

func (f *subFS) Sub(dir string) (FS, error) {
	if dir == "." {
		return f, nil
	}
	full, err := f.fullName("sub", dir)
	if err != nil {
		return nil, err
	}
	return &subFS{f.fsys, full}, nil
}
