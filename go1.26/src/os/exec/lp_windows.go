// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package exec

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound 是路径搜索未能找到可执行文件时产生的错误。
var ErrNotFound = errors.New("executable file not found in %PATH%")

func chkStat(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	if d.IsDir() {
		return fs.ErrPermission
	}
	return nil
}

func hasExt(file string) bool {
	i := strings.LastIndex(file, ".")
	if i < 0 {
		return false
	}
	return strings.LastIndexAny(file, `:\/`) < i
}

func findExecutable(file string, exts []string) (string, error) {
	if len(exts) == 0 {
		return file, chkStat(file)
	}
	if hasExt(file) {
		if chkStat(file) == nil {
			return file, nil
		}
		// 继续检查下面的 exts，这样像 "foo.bat.exe" 这样名称奇怪的
		// 程序也能被解析，而不是失败。
	}
	for _, e := range exts {
		if f := file + e; chkStat(f) == nil {
			return f, nil
		}
	}
	if hasExt(file) {
		return "", fs.ErrNotExist
	}
	return "", ErrNotFound
}

func lookPath(file string) (string, error) {
	if err := validateLookPath(file); err != nil {
		return "", &Error{file, err}
	}

	return lookPathExts(file, pathExt())
}

// lookExtensions 根据其 dir 和 path 查找 Windows 可执行文件。
// 它使用 LookPath 尝试适当的扩展名。
// lookExtensions 不搜索 PATH，而是将 `prog` 转换为 `.\prog`。
//
// 如果路径已经具有 PATHEXT 中找到的扩展名，
// lookExtensions 会直接返回它，而不再搜索其他扩展名。
// 例如，即使程序实际上是 "C:\foo\example.com.exe"，
// "C:\foo\example.com" 也会被原样返回。
func lookExtensions(path, dir string) (string, error) {
	if err := validateLookPath(path); err != nil {
		return "", &Error{path, err}
	}

	if filepath.Base(path) == path {
		path = "." + string(filepath.Separator) + path
	}
	exts := pathExt()
	if ext := filepath.Ext(path); ext != "" {
		for _, e := range exts {
			if strings.EqualFold(ext, e) {
				// 假定 path 已经被解析过了。
				return path, nil
			}
		}
	}
	if dir == "" {
		return lookPathExts(path, exts)
	}
	if filepath.VolumeName(path) != "" {
		return lookPathExts(path, exts)
	}
	if len(path) > 1 && os.IsPathSeparator(path[0]) {
		return lookPathExts(path, exts)
	}
	dirandpath := filepath.Join(dir, path)
	// 我们假定 LookPath 只会添加文件扩展名。
	lp, err := lookPathExts(dirandpath, exts)
	if err != nil {
		return "", err
	}
	ext := strings.TrimPrefix(lp, dirandpath)
	return path + ext, nil
}

func pathExt() []string {
	var exts []string
	x := os.Getenv(`PATHEXT`)
	if x != "" {
		for e := range strings.SplitSeq(strings.ToLower(x), `;`) {
			if e == "" {
				continue
			}
			if e[0] != '.' {
				e = "." + e
			}
			exts = append(exts, e)
		}
	} else {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	return exts
}

// lookPathExts 为给定的 PATHEXT 列表实现 LookPath。
func lookPathExts(file string, exts []string) (string, error) {
	if strings.ContainsAny(file, `:\/`) {
		f, err := findExecutable(file, exts)
		if err == nil {
			return f, nil
		}
		return "", &Error{file, err}
	}

	// 在 Windows 上，创建 NoDefaultCurrentDirectoryInExePath
	// 环境变量（任意值或没有值都可以！）表示路径查找应跳过当前目录。
	// 理论上我们应该调用 NeedCurrentDirectoryForExePathW，
	// “因为这个环境变量的注册表位置可能会变化”，
	// 但这似乎极不可能：那样会破坏所有以这种方式配置环境的用户！
	// https://docs.microsoft.com/en-us/windows/win32/api/processenv/nf-processenv-needcurrentdirectoryforexepathw
	// 另见 go.dev/issue/43947。
	var (
		dotf   string
		dotErr error
	)
	if _, found := os.LookupEnv("NoDefaultCurrentDirectoryInExePath"); !found {
		if f, err := findExecutable(filepath.Join(".", file), exts); err == nil {
			if execerrdot.Value() == "0" {
				execerrdot.IncNonDefault()
				return f, nil
			}
			dotf, dotErr = f, &Error{file, ErrDot}
		}
	}

	path := os.Getenv("path")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			// 跳过空条目，与 PowerShell 的行为保持一致。
			// （参见 https://go.dev/issue/61493#issuecomment-1649724826。）
			continue
		}

		if f, err := findExecutable(filepath.Join(dir, file), exts); err == nil {
			if dotErr != nil {
				// https://go.dev/issue/53536：如果我们隐式解析出了一个相对路径，
				// 并且它与从显式 %PATH% 中解析出的可执行文件相同，
				// 则优先使用可执行文件的显式名称（很可能不会报错），
				// 而不是使用带 ErrDot 的等价隐式名称。
				//
				// 否则，一旦我们发现显式路径不匹配，就立即返回隐式路径的 ErrDot。
				dotfi, dotfiErr := os.Lstat(dotf)
				fi, fiErr := os.Lstat(f)
				if dotfiErr != nil || fiErr != nil || !os.SameFile(dotfi, fi) {
					return dotf, dotErr
				}
			}

			if !filepath.IsAbs(f) {
				if execerrdot.Value() != "0" {
					// 如果这就是我们已经找到的同一个相对路径，
					// 那么 dotErr 非 nil，且我们已在上文检查过它。
					// 否则，将此路径记录为我们必须解析到的路径，
					// 无论是否带有 dotErr。
					if dotErr == nil {
						dotf, dotErr = f, &Error{file, ErrDot}
					}
					continue
				}
				execerrdot.IncNonDefault()
			}
			return f, nil
		}
	}

	if dotErr != nil {
		return dotf, dotErr
	}
	return "", &Error{file, ErrNotFound}
}
