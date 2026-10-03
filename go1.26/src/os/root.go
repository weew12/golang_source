// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"errors"
	"internal/bytealg"
	"internal/stringslite"
	"internal/testlog"
	"io/fs"
	"runtime"
	"slices"
	"time"
)

// OpenInRoot 在目录 dir 中打开文件 name。
// 它等价于先执行 OpenRoot(dir)，然后在根中打开该文件。
//
// 如果 name 的任何一个组件引用了 dir 之外的位置，
// OpenInRoot 会返回错误。
//
// 详情和限制参见 [Root]。
func OpenInRoot(dir, name string) (*File, error) {
	r, err := OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Open(name)
}

// Root 可用于仅访问单个目录树内的文件。
//
// Root 上的方法只能访问根目录之下的文件和目录。
// 如果传给 Root 方法的文件名的任何一个组件引用了
// 根之外的位置，该方法会返回错误。
// 文件名可以引用目录本身（.）。
//
// Root 上的方法会跟随符号链接，但符号链接不得
// 引用根之外的位置。
// 符号链接不得是绝对路径。
//
// Root 上的方法不禁止跨越文件系统边界、Linux 绑定挂载、
// /proc 特殊文件，或访问 Unix 设备文件。
//
// Root 上的方法可以安全地同时从多个 goroutine 使用。
//
// 在大多数平台上，创建 Root 会打开一个引用该目录的
// 文件描述符或句柄。如果该目录被移动，Root 上的方法
// 引用的是其新位置处的原目录。
//
// Root 的行为在某些平台上有所不同：
//
//   - 当 GOOS=windows 时，文件名不得引用 Windows 保留的设备名，
//     例如 NUL 和 COM1。
//   - 在 Unix 上，[Root.Chmod]、[Root.Chown] 和 [Root.Chtimes] 容易受到竞态条件影响。
//     如果操作的目标在操作进行期间从普通文件变为符号链接，
//     该操作可能会作用于链接本身而非链接目标。
//   - 当 GOOS=js 时，Root 在符号链接验证中容易受到 TOCTOU
//     （检查时-使用时）攻击，且无法保证操作不会逃逸出根。
//   - 当 GOOS=plan9 或 GOOS=js 时，Root 不会跨重命名跟踪目录。
//     在这些平台上，Root 引用的是目录名，而不是文件描述符。
//   - WASI preview 1（GOOS=wasip1）不支持 [Root.Chmod]。
type Root struct {
	root *root
}

const (
	// 在根中解析文件时我们会跟随的符号链接的最大数量。
	// 8 是 __POSIX_SYMLOOP_MAX（SYMLOOP_MAX 允许的最小值），
	// 也是一个常见的限制。
	rootMaxSymlinks = 8
)

// OpenRoot 打开指定的目录。
// 它会跟随目录名中的符号链接。
// 如果出错，错误的类型为 [*PathError]。
func OpenRoot(name string) (*Root, error) {
	testlog.Open(name)
	return openRootNolog(name)
}

// Name 返回传给 OpenRoot 的目录名。
//
// 在 [Close] 之后调用 Name 是安全的。
func (r *Root) Name() string {
	return r.root.Name()
}

// Close 关闭该 Root。
// 调用 Close 之后，Root 上的方法会返回错误。
func (r *Root) Close() error {
	return r.root.Close()
}

// Open 以只读方式打开根中指定的文件。
// 更多详情参见 [Open]。
func (r *Root) Open(name string) (*File, error) {
	return r.OpenFile(name, O_RDONLY, 0)
}

// Create 在根中创建或截断指定的文件。
// 更多详情参见 [Create]。
func (r *Root) Create(name string) (*File, error) {
	return r.OpenFile(name, O_RDWR|O_CREATE|O_TRUNC, 0666)
}

// OpenFile 打开根中指定的文件。
// 更多详情参见 [OpenFile]。
//
// 如果 perm 包含除九个最低有效位（0o777）之外的位，
// OpenFile 会返回错误。
func (r *Root) OpenFile(name string, flag int, perm FileMode) (*File, error) {
	if perm&0o777 != perm {
		return nil, &PathError{Op: "openat", Path: name, Err: errors.New("unsupported file mode")}
	}
	r.logOpen(name)
	rf, err := rootOpenFileNolog(r, name, flag, perm)
	if err != nil {
		return nil, err
	}
	rf.appendMode = flag&O_APPEND != 0
	return rf, nil
}

// OpenRoot 打开根中指定的目录。
// 如果出错，错误的类型为 [*PathError]。
func (r *Root) OpenRoot(name string) (*Root, error) {
	r.logOpen(name)
	return openRootInRoot(r, name)
}

// Chmod 将根中指定文件的模式更改为 mode。
// 更多详情参见 [Chmod]。
func (r *Root) Chmod(name string, mode FileMode) error {
	return rootChmod(r, name, mode)
}

// Mkdir 在根中创建一个新目录，
// 使用指定的名称和权限位（在 umask 之前）。
// 更多详情参见 [Mkdir]。
//
// 如果 perm 包含除九个最低有效位（0o777）之外的位，
// Mkdir 会返回错误。
func (r *Root) Mkdir(name string, perm FileMode) error {
	if perm&0o777 != perm {
		return &PathError{Op: "mkdirat", Path: name, Err: errors.New("unsupported file mode")}
	}
	return rootMkdir(r, name, perm)
}

// MkdirAll 在根中创建一个新目录，以及任何必要的父目录。
// 更多详情参见 [MkdirAll]。
//
// 如果 perm 包含除九个最低有效位（0o777）之外的位，
// MkdirAll 会返回错误。
func (r *Root) MkdirAll(name string, perm FileMode) error {
	if perm&0o777 != perm {
		return &PathError{Op: "mkdirat", Path: name, Err: errors.New("unsupported file mode")}
	}
	return rootMkdirAll(r, name, perm)
}

// Chown 更改根中指定文件的数字 uid 和 gid。
// 更多详情参见 [Chown]。
func (r *Root) Chown(name string, uid, gid int) error {
	return rootChown(r, name, uid, gid)
}

// Lchown 更改根中指定文件的数字 uid 和 gid。
// 更多详情参见 [Lchown]。
func (r *Root) Lchown(name string, uid, gid int) error {
	return rootLchown(r, name, uid, gid)
}

// Chtimes 更改根中指定文件的访问时间和修改时间。
// 更多详情参见 [Chtimes]。
func (r *Root) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return rootChtimes(r, name, atime, mtime)
}

// Remove 删除根中指定的文件或（空）目录。
// 更多详情参见 [Remove]。
func (r *Root) Remove(name string) error {
	return rootRemove(r, name)
}

// RemoveAll 删除根中指定的文件或目录及其包含的所有子项。
// 更多详情参见 [RemoveAll]。
func (r *Root) RemoveAll(name string) error {
	return rootRemoveAll(r, name)
}

// Stat 返回描述根中指定文件的 [FileInfo]。
// 更多详情参见 [Stat]。
func (r *Root) Stat(name string) (FileInfo, error) {
	r.logStat(name)
	return rootStat(r, name, false)
}

// Lstat 返回描述根中指定文件的 [FileInfo]。
// 如果该文件是符号链接，返回的 FileInfo
// 描述的是该符号链接。
// 更多详情参见 [Lstat]。
func (r *Root) Lstat(name string) (FileInfo, error) {
	r.logStat(name)
	return rootStat(r, name, true)
}

// Readlink 返回根中指定符号链接的目标。
// 更多详情参见 [Readlink]。
func (r *Root) Readlink(name string) (string, error) {
	return rootReadlink(r, name)
}

// Rename 将 oldname 重命名（移动）为 newname。
// 两个路径都相对于根。
// 更多详情参见 [Rename]。
func (r *Root) Rename(oldname, newname string) error {
	return rootRename(r, oldname, newname)
}

// Link 将 newname 创建为指向 oldname 文件的硬链接。
// 两个路径都相对于根。
// 更多详情参见 [Link]。
//
// 如果 oldname 是符号链接，Link 创建的是指向 oldname 而非其目标的新链接。
// 此行为在某些平台上可能与 [Link] 的行为不同。
//
// 当 GOOS=js 时，如果 oldname 是符号链接，Link 会返回错误。
func (r *Root) Link(oldname, newname string) error {
	return rootLink(r, oldname, newname)
}

// Symlink 将 newname 创建为指向 oldname 的符号链接。
// 更多详情参见 [Symlink]。
//
// Symlink 不校验 oldname，
// 它可能引用根之外的位置。
//
// 在 Windows 上，如果 oldname 引用了根内的目录，
// 则创建目录链接。否则创建文件链接。
func (r *Root) Symlink(oldname, newname string) error {
	return rootSymlink(r, oldname, newname)
}

// ReadFile 读取根中指定的文件并返回其内容。
// 更多详情参见 [ReadFile]。
func (r *Root) ReadFile(name string) ([]byte, error) {
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readFileContents(statOrZero(f), f.Read)
}

// WriteFile 将 data 写入根中指定的文件，必要时创建它。
// 更多详情参见 [WriteFile]。
func (r *Root) WriteFile(name string, data []byte, perm FileMode) error {
	f, err := r.OpenFile(name, O_WRONLY|O_CREATE|O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err1 := f.Close(); err == nil {
		err = err1
	}
	return err
}

func (r *Root) logOpen(name string) {
	if log := testlog.Logger(); log != nil {
		// 如果 r 的名称自打开以来发生了变化，这就不准确，
		// 但这是我们能做到的最好结果。
		log.Open(joinPath(r.Name(), name))
	}
}

func (r *Root) logStat(name string) {
	if log := testlog.Logger(); log != nil {
		// 如果 r 的名称自打开以来发生了变化，这就不准确，
		// 但这是我们能做到的最好结果。
		log.Stat(joinPath(r.Name(), name))
	}
}

// splitPathInRoot 将路径拆分为各个组件，
// 并将其与给定的 prefix 和 suffix 连接。
//
// 该路径相对于一个 Root，不得为绝对路径、
// 卷相对路径或 ""。
//
// "." 组件会被移除，但最后一个组件除外。
//
// 最后一个组件之后的路径分隔符会在 suffixSep 中返回。
func splitPathInRoot(s string, prefix, suffix []string) (_ []string, suffixSep string, err error) {
	if len(s) == 0 {
		return nil, "", errors.New("empty path")
	}
	if IsPathSeparator(s[0]) {
		return nil, "", errPathEscapes
	}

	if runtime.GOOS == "windows" {
		// Windows 会在打开路径之前对其进行清理。
		s, err = rootCleanPath(s, prefix, suffix)
		if err != nil {
			return nil, "", err
		}
		prefix = nil
		suffix = nil
	}

	parts := slices.Clone(prefix)
	i, j := 0, 1
	for {
		if j < len(s) && !IsPathSeparator(s[j]) {
			// 继续寻找该组件的结尾。
			j++
			continue
		}
		parts = append(parts, s[i:j])
		// 前进到下一个组件，或路径末尾。
		partEnd := j
		for j < len(s) && IsPathSeparator(s[j]) {
			j++
		}
		if j == len(s) {
			// 如果这是最后一个路径组件，
			// 保留任何尾部的路径分隔符。
			suffixSep = s[partEnd:]
			break
		}
		if parts[len(parts)-1] == "." {
			// 移除 "." 组件，但末尾的除外。
			parts = parts[:len(parts)-1]
		}
		i = j
	}
	if len(suffix) > 0 && len(parts) > 0 && parts[len(parts)-1] == "." {
		// 如果我们要连接到后缀，则移除末尾的 "." 组件。
		parts = parts[:len(parts)-1]
	}
	parts = append(parts, suffix...)
	return parts, suffixSep, nil
}

// FS 返回一个用于根中文件树的文件系统（一个 fs.FS）。
//
// 结果实现了 [io/fs.StatFS]、[io/fs.ReadFileFS]、
// [io/fs.ReadDirFS] 和 [io/fs.ReadLinkFS]。
func (r *Root) FS() fs.FS {
	return (*rootFS)(r)
}

type rootFS Root

func (rfs *rootFS) Open(name string) (fs.File, error) {
	r := (*Root)(rfs)
	if !isValidRootFSPath(name) {
		return nil, &PathError{Op: "open", Path: name, Err: ErrInvalid}
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (rfs *rootFS) ReadDir(name string) ([]DirEntry, error) {
	r := (*Root)(rfs)
	if !isValidRootFSPath(name) {
		return nil, &PathError{Op: "readdir", Path: name, Err: ErrInvalid}
	}

	// 这并不高效：我们只是打开一个普通文件并对其执行 ReadDir。
	// 理想情况下，我们会完全跳过创建 *File 而直接
	// 在文件描述符上操作，但这需要对目录读取进行
	// 一些大规模的改造。
	//
	// 目前这样就足够了。
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dirs, err := f.ReadDir(-1)
	slices.SortFunc(dirs, func(a, b DirEntry) int {
		return bytealg.CompareString(a.Name(), b.Name())
	})
	return dirs, err
}

func (rfs *rootFS) ReadFile(name string) ([]byte, error) {
	r := (*Root)(rfs)
	if !isValidRootFSPath(name) {
		return nil, &PathError{Op: "readfile", Path: name, Err: ErrInvalid}
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readFileContents(statOrZero(f), f.Read)
}

func (rfs *rootFS) ReadLink(name string) (string, error) {
	r := (*Root)(rfs)
	if !isValidRootFSPath(name) {
		return "", &PathError{Op: "readlink", Path: name, Err: ErrInvalid}
	}
	return r.Readlink(name)
}

func (rfs *rootFS) Stat(name string) (FileInfo, error) {
	r := (*Root)(rfs)
	if !isValidRootFSPath(name) {
		return nil, &PathError{Op: "stat", Path: name, Err: ErrInvalid}
	}
	return r.Stat(name)
}

func (rfs *rootFS) Lstat(name string) (FileInfo, error) {
	r := (*Root)(rfs)
	if !isValidRootFSPath(name) {
		return nil, &PathError{Op: "lstat", Path: name, Err: ErrInvalid}
	}
	return r.Lstat(name)
}

// isValidRootFSPath 报告 name 是否是传给 Root.FS 方法的有效文件名。
func isValidRootFSPath(name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	if runtime.GOOS == "windows" {
		// fs.FS 路径以 / 分隔。
		// 在 Windows 上，如果路径包含任何 \ 分隔符就拒绝它。
		// 其他形式的无效路径（例如 "NUL"）由
		// Root 通常的文件查找机制处理。
		if stringslite.IndexByte(name, '\\') >= 0 {
			return false
		}
	}
	return true
}
