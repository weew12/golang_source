// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/bytealg"
	"internal/filepathlite"
	"io"
	"io/fs"
	"slices"
)

type readdirMode int

const (
	readdirName readdirMode = iota
	readdirDirEntry
	readdirFileInfo
)

// Readdir 读取与 file 关联的目录的内容，
// 并按目录顺序返回至多 n 个 [FileInfo] 值的切片，
// 其内容与 [Lstat] 返回的相同。对同一文件的后续调用将
// 产生更多的 FileInfo。
//
// 如果 n > 0，Readdir 最多返回 n 个 FileInfo 结构。在这种情况下，
// 如果 Readdir 返回空切片，它将返回一个解释原因的非 nil 错误。
// 在目录末尾，该错误为 [io.EOF]。
//
// 如果 n <= 0，Readdir 在单个切片中返回目录中的所有 FileInfo。
// 在这种情况下，如果 Readdir 成功（一直读取到目录末尾），
// 它将返回该切片和 nil 错误。如果在到达目录末尾之前
// 遇到错误，Readdir 返回截至该处读取的 FileInfo 和一个非 nil 错误。
//
// 大多数客户端更适合使用更高效的 ReadDir 方法。
func (f *File) Readdir(n int) ([]FileInfo, error) {
	if f == nil {
		return nil, ErrInvalid
	}
	_, _, infos, err := f.readdir(n, readdirFileInfo)
	if infos == nil {
		// Readdir 历来总是返回非 nil 的空切片，从不为 nil，
		// 即使发生错误也是如此（除上文使用 nil 接收者误用的情况）。
		// 保持这种约定，以避免破坏过于敏感的调用者。
		infos = []FileInfo{}
	}
	return infos, err
}

// Readdirnames 读取与 file 关联的目录的内容，
// 并按目录顺序返回至多 n 个目录中文件名的切片。
// 对同一文件的后续调用将产生更多的文件名。
//
// 如果 n > 0，Readdirnames 最多返回 n 个名称。在这种情况下，
// 如果 Readdirnames 返回空切片，它将返回一个解释原因的非 nil 错误。
// 在目录末尾，该错误为 [io.EOF]。
//
// 如果 n <= 0，Readdirnames 在单个切片中返回目录中的所有名称。
// 在这种情况下，如果 Readdirnames 成功（一直读取到目录末尾），
// 它将返回该切片和 nil 错误。如果在到达目录末尾之前
// 遇到错误，Readdirnames 返回截至该处读取的名称和一个非 nil 错误。
func (f *File) Readdirnames(n int) (names []string, err error) {
	if f == nil {
		return nil, ErrInvalid
	}
	names, _, _, err = f.readdir(n, readdirName)
	if names == nil {
		// Readdirnames 历来总是返回非 nil 的空切片，从不为 nil，
		// 即使发生错误也是如此（除上文使用 nil 接收者误用的情况）。
		// 保持这种约定，以避免破坏过于敏感的调用者。
		names = []string{}
	}
	return names, err
}

// DirEntry 是从目录中读取的一个条目
// （使用 [ReadDir] 函数或 [File.ReadDir] 方法）。
type DirEntry = fs.DirEntry

// ReadDir 读取与文件 f 关联的目录的内容，
// 并按目录顺序返回一个 [DirEntry] 值的切片。
// 对同一文件的后续调用将产生目录中更靠后的 DirEntry 记录。
//
// 如果 n > 0，ReadDir 最多返回 n 个 DirEntry 记录。
// 在这种情况下，如果 ReadDir 返回空切片，它将返回一个解释原因的错误。
// 在目录末尾，该错误为 [io.EOF]。
//
// 如果 n <= 0，ReadDir 返回目录中剩余的所有 DirEntry 记录。
// 成功时，它返回 nil 错误（而非 io.EOF）。
func (f *File) ReadDir(n int) ([]DirEntry, error) {
	if f == nil {
		return nil, ErrInvalid
	}
	_, dirents, _, err := f.readdir(n, readdirDirEntry)
	if dirents == nil {
		// 与 Readdir 和 Readdirnames 保持一致：不要返回 nil 切片。
		dirents = []DirEntry{}
	}
	return dirents, err
}

// ReadDir 读取指定名称的目录，
// 按文件名排序返回其所有目录条目。
// 如果读取目录时发生错误，
// ReadDir 返回在出错之前能够读取的条目，
// 以及该错误。
func ReadDir(name string) ([]DirEntry, error) {
	f, err := openDir(name)
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

// CopyFS 将文件系统 fsys 复制到目录 dir 中，
// 必要时创建 dir。
//
// 文件以模式 0o666 加上源中的任何执行权限创建，
// 目录以模式 0o777 创建（在应用 umask 之前）。
//
// CopyFS 不会覆盖已存在的文件。如果 fsys 中的某个文件名
// 已存在于目标中，CopyFS 将返回一个错误，
// 使得 errors.Is(err, fs.ErrExist) 为 true。
//
// dir 中的符号链接会被跟随。
//
// 在 CopyFS 运行期间添加到 fsys 的新文件
// （包括 dir 是 fsys 的子目录的情况）不保证会被复制。
//
// 复制在遇到第一个错误时停止并返回该错误。
func CopyFS(dir string, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		fpath, err := filepathlite.Localize(path)
		if err != nil {
			return err
		}
		newPath := joinPath(dir, fpath)

		switch d.Type() {
		case ModeDir:
			return MkdirAll(newPath, 0777)
		case ModeSymlink:
			target, err := fs.ReadLink(fsys, path)
			if err != nil {
				return err
			}
			return Symlink(target, newPath)
		case 0:
			r, err := fsys.Open(path)
			if err != nil {
				return err
			}
			defer r.Close()
			info, err := r.Stat()
			if err != nil {
				return err
			}
			w, err := OpenFile(newPath, O_CREATE|O_EXCL|O_WRONLY, 0666|info.Mode()&0777)
			if err != nil {
				return err
			}

			if _, err := io.Copy(w, r); err != nil {
				w.Close()
				return &PathError{Op: "Copy", Path: newPath, Err: err}
			}
			return w.Close()
		default:
			return &PathError{Op: "CopyFS", Path: path, Err: ErrInvalid}
		}
	})
}
