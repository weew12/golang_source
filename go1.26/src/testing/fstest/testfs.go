// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// fstest 包实现对文件系统实现和用户的测试支持。
package fstest

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"testing/iotest"
)

// TestFS 测试一个文件系统实现。
// 它会遍历 fsys 中的整个文件树，
// 打开并检查每个文件的行为是否正确。
// 不会跟随符号链接，
// 但如果文件系统实现了 [fs.ReadLinkFS]，
// 则会检查它们的 Lstat 值。
// 它还会检查文件系统至少包含预期的文件。
// 作为一种特殊情况，如果未列出任何预期文件，则 fsys 必须为空。
// 否则，fsys 必须至少包含所列出的文件；它也可以包含其他文件。
// fsys 的内容不得在 TestFS 运行时并发更改。
//
// 如果 TestFS 发现任何不当行为，它会返回第一个错误或
// 一个错误列表。可使用 [errors.Is] 或 [errors.AsType] 进行检查。
//
// 在测试中的典型用法是：
//
//	if err := fstest.TestFS(myFS, "file/that/should/be/present"); err != nil {
//		t.Fatal(err)
//	}
func TestFS(fsys fs.FS, expected ...string) error {
	if err := testFS(fsys, expected...); err != nil {
		return err
	}
	for _, name := range expected {
		if i := strings.Index(name, "/"); i >= 0 {
			dir, dirSlash := name[:i], name[:i+1]
			var subExpected []string
			for _, name := range expected {
				if strings.HasPrefix(name, dirSlash) {
					subExpected = append(subExpected, name[len(dirSlash):])
				}
			}
			sub, err := fs.Sub(fsys, dir)
			if err != nil {
				return err
			}
			if err := testFS(sub, subExpected...); err != nil {
				return fmt.Errorf("testing fs.Sub(fsys, %s): %w", dir, err)
			}
			break // one sub-test is enough
		}
	}
	return nil
}

func testFS(fsys fs.FS, expected ...string) error {
	t := fsTester{fsys: fsys}
	t.checkDir(".")
	t.checkOpen(".")
	found := make(map[string]bool)
	for _, dir := range t.dirs {
		found[dir] = true
	}
	for _, file := range t.files {
		found[file] = true
	}
	delete(found, ".")
	if len(expected) == 0 && len(found) > 0 {
		list := slices.Sorted(maps.Keys(found))
		if len(list) > 15 {
			list = append(list[:10], "...")
		}
		t.errorf("expected empty file system but found files:\n%s", strings.Join(list, "\n"))
	}
	for _, name := range expected {
		if !found[name] {
			t.errorf("expected but not found: %s", name)
		}
	}
	if len(t.errors) == 0 {
		return nil
	}
	return fmt.Errorf("TestFS found errors:\n%w", errors.Join(t.errors...))
}

// fsTester 保存运行测试所需的状态。
type fsTester struct {
	fsys   fs.FS
	errors []error
	dirs   []string
	files  []string
}

// errorf 向错误列表中添加一个错误。
func (t *fsTester) errorf(format string, args ...any) {
	t.errors = append(t.errors, fmt.Errorf(format, args...))
}

func (t *fsTester) openDir(dir string) fs.ReadDirFile {
	f, err := t.fsys.Open(dir)
	if err != nil {
		t.errorf("%s: Open: %w", dir, err)
		return nil
	}
	d, ok := f.(fs.ReadDirFile)
	if !ok {
		f.Close()
		t.errorf("%s: Open returned File type %T, not a fs.ReadDirFile", dir, f)
		return nil
	}
	return d
}

// checkDir 检查目录 dir，该目录预期存在
// （它要么是根目录，要么是在目录列表中发现的且 IsDir 为 true 的目录）。
func (t *fsTester) checkDir(dir string) {
	// 读取整个目录。
	t.dirs = append(t.dirs, dir)
	d := t.openDir(dir)
	if d == nil {
		return
	}
	list, err := d.ReadDir(-1)
	if err != nil {
		d.Close()
		t.errorf("%s: ReadDir(-1): %w", dir, err)
		return
	}

	// 检查所有子项。
	var prefix string
	if dir == "." {
		prefix = ""
	} else {
		prefix = dir + "/"
	}
	for _, info := range list {
		name := info.Name()
		switch {
		case name == ".", name == "..", name == "":
			t.errorf("%s: ReadDir: child has invalid name: %#q", dir, name)
			continue
		case strings.Contains(name, "/"):
			t.errorf("%s: ReadDir: child name contains slash: %#q", dir, name)
			continue
		case strings.Contains(name, `\`):
			t.errorf("%s: ReadDir: child name contains backslash: %#q", dir, name)
			continue
		}
		path := prefix + name
		t.checkStat(path, info)
		t.checkOpen(path)
		switch info.Type() {
		case fs.ModeDir:
			t.checkDir(path)
		case fs.ModeSymlink:
			// 不再进一步处理。
			// 避免跟随符号链接，以避免可能无界的递归。
			t.files = append(t.files, path)
		default:
			t.checkFile(path)
		}
	}

	// 检查 EOF 处的 ReadDir(-1)。
	list2, err := d.ReadDir(-1)
	if len(list2) > 0 || err != nil {
		d.Close()
		t.errorf("%s: ReadDir(-1) at EOF = %d entries, %w, wanted 0 entries, nil", dir, len(list2), err)
		return
	}

	// 检查 EOF 处的 ReadDir(1)（结果不同）。
	list2, err = d.ReadDir(1)
	if len(list2) > 0 || err != io.EOF {
		d.Close()
		t.errorf("%s: ReadDir(1) at EOF = %d entries, %w, wanted 0 entries, EOF", dir, len(list2), err)
		return
	}

	// 检查 close 不会报告错误。
	if err := d.Close(); err != nil {
		t.errorf("%s: Close: %w", dir, err)
	}

	// 检查关闭两次不会崩溃。
	// 返回值无关紧要。
	d.Close()

	// 重新打开目录，第二次读取，确保内容一致。
	if d = t.openDir(dir); d == nil {
		return
	}
	defer d.Close()
	list2, err = d.ReadDir(-1)
	if err != nil {
		t.errorf("%s: second Open+ReadDir(-1): %w", dir, err)
		return
	}
	t.checkDirList(dir, "first Open+ReadDir(-1) vs second Open+ReadDir(-1)", list, list2)

	// 重新打开目录，第三次分片读取，确保内容一致。
	if d = t.openDir(dir); d == nil {
		return
	}
	defer d.Close()
	list2 = nil
	for {
		n := 1
		if len(list2) > 0 {
			n = 2
		}
		frag, err := d.ReadDir(n)
		if len(frag) > n {
			t.errorf("%s: third Open: ReadDir(%d) after %d: %d entries (too many)", dir, n, len(list2), len(frag))
			return
		}
		list2 = append(list2, frag...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.errorf("%s: third Open: ReadDir(%d) after %d: %w", dir, n, len(list2), err)
			return
		}
		if n == 0 {
			t.errorf("%s: third Open: ReadDir(%d) after %d: 0 entries but nil error", dir, n, len(list2))
			return
		}
	}
	t.checkDirList(dir, "first Open+ReadDir(-1) vs third Open+ReadDir(1,2) loop", list, list2)

	// 如果 fsys 有 ReadDir，则检查其是否匹配且已排序。
	if fsys, ok := t.fsys.(fs.ReadDirFS); ok {
		list2, err := fsys.ReadDir(dir)
		if err != nil {
			t.errorf("%s: fsys.ReadDir: %w", dir, err)
			return
		}
		t.checkDirList(dir, "first Open+ReadDir(-1) vs fsys.ReadDir", list, list2)

		for i := 0; i+1 < len(list2); i++ {
			if list2[i].Name() >= list2[i+1].Name() {
				t.errorf("%s: fsys.ReadDir: list not sorted: %s before %s", dir, list2[i].Name(), list2[i+1].Name())
			}
		}
	}

	// 也检查 fs.ReadDir。
	list2, err = fs.ReadDir(t.fsys, dir)
	if err != nil {
		t.errorf("%s: fs.ReadDir: %w", dir, err)
		return
	}
	t.checkDirList(dir, "first Open+ReadDir(-1) vs fs.ReadDir", list, list2)

	for i := 0; i+1 < len(list2); i++ {
		if list2[i].Name() >= list2[i+1].Name() {
			t.errorf("%s: fs.ReadDir: list not sorted: %s before %s", dir, list2[i].Name(), list2[i+1].Name())
		}
	}

	t.checkGlob(dir, list2)
}

// formatEntry 将 fs.DirEntry 格式化为字符串，用于错误消息和比较。
func formatEntry(entry fs.DirEntry) string {
	return fmt.Sprintf("%s IsDir=%v Type=%v", entry.Name(), entry.IsDir(), entry.Type())
}

// formatInfoEntry 将 fs.FileInfo 格式化为与 formatEntry 结果类似的字符串，用于错误消息和比较。
func formatInfoEntry(info fs.FileInfo) string {
	return fmt.Sprintf("%s IsDir=%v Type=%v", info.Name(), info.IsDir(), info.Mode().Type())
}

// formatInfo 将 fs.FileInfo 格式化为字符串，用于错误消息和比较。
func formatInfo(info fs.FileInfo) string {
	return fmt.Sprintf("%s IsDir=%v Mode=%v Size=%d ModTime=%v", info.Name(), info.IsDir(), info.Mode(), info.Size(), info.ModTime())
}

// checkGlob 检查当文件系统实现 GlobFS 时各种 glob 模式是否正常工作。
func (t *fsTester) checkGlob(dir string, list []fs.DirEntry) {
	if _, ok := t.fsys.(fs.GlobFS); !ok {
		return
	}

	// 构造一个仅匹配 dir 的复杂 glob 模式前缀。
	var glob string
	if dir != "." {
		elem := strings.Split(dir, "/")
		for i, e := range elem {
			var pattern []rune
			for j, r := range e {
				if r == '*' || r == '?' || r == '\\' || r == '[' || r == '-' {
					pattern = append(pattern, '\\', r)
					continue
				}
				switch (i + j) % 5 {
				case 0:
					pattern = append(pattern, r)
				case 1:
					pattern = append(pattern, '[', r, ']')
				case 2:
					pattern = append(pattern, '[', r, '-', r, ']')
				case 3:
					pattern = append(pattern, '[', '\\', r, ']')
				case 4:
					pattern = append(pattern, '[', '\\', r, '-', '\\', r, ']')
				}
			}
			elem[i] = string(pattern)
		}
		glob = strings.Join(elem, "/") + "/"
	}

	// 测试能否检测到格式错误的模式。
	// 错误很可能是 path.ErrBadPattern，但不一定。
	if _, err := t.fsys.(fs.GlobFS).Glob(glob + "nonexist/[]"); err == nil {
		t.errorf("%s: Glob(%#q): bad pattern not detected", dir, glob+"nonexist/[]")
	}

	// 尝试找出仅出现在部分最终名称中的字母。
	c := rune('a')
	for ; c <= 'z'; c++ {
		have, haveNot := false, false
		for _, d := range list {
			if strings.ContainsRune(d.Name(), c) {
				have = true
			} else {
				haveNot = true
			}
		}
		if have && haveNot {
			break
		}
	}
	if c > 'z' {
		c = 'a'
	}
	glob += "*" + string(c) + "*"

	var want []string
	for _, d := range list {
		if strings.ContainsRune(d.Name(), c) {
			want = append(want, path.Join(dir, d.Name()))
		}
	}

	names, err := t.fsys.(fs.GlobFS).Glob(glob)
	if err != nil {
		t.errorf("%s: Glob(%#q): %w", dir, glob, err)
		return
	}
	if slices.Equal(want, names) {
		return
	}

	if !slices.IsSorted(names) {
		t.errorf("%s: Glob(%#q): unsorted output:\n%s", dir, glob, strings.Join(names, "\n"))
		slices.Sort(names)
	}

	var problems []string
	for len(want) > 0 || len(names) > 0 {
		switch {
		case len(want) > 0 && len(names) > 0 && want[0] == names[0]:
			want, names = want[1:], names[1:]
		case len(want) > 0 && (len(names) == 0 || want[0] < names[0]):
			problems = append(problems, "missing: "+want[0])
			want = want[1:]
		default:
			problems = append(problems, "extra: "+names[0])
			names = names[1:]
		}
	}
	t.errorf("%s: Glob(%#q): wrong output:\n%s", dir, glob, strings.Join(problems, "\n"))
}

// checkStat 检查对 path 的直接 stat 是否与 entry 匹配，
// 其中 entry 是在父目录的目录列表中找到的。
func (t *fsTester) checkStat(path string, entry fs.DirEntry) {
	file, err := t.fsys.Open(path)
	if err != nil {
		t.errorf("%s: Open: %w", path, err)
		return
	}
	info, err := file.Stat()
	file.Close()
	if err != nil {
		t.errorf("%s: Stat: %w", path, err)
		return
	}
	fentry := formatEntry(entry)
	fientry := formatInfoEntry(info)
	// 注意：对于符号链接，此处不一致是可以的，因为 Open 会解引用符号链接。
	if fentry != fientry && entry.Type()&fs.ModeSymlink == 0 {
		t.errorf("%s: mismatch:\n\tentry = %s\n\tfile.Stat() = %s", path, fentry, fientry)
	}

	einfo, err := entry.Info()
	if err != nil {
		t.errorf("%s: entry.Info: %w", path, err)
		return
	}
	finfo := formatInfo(info)
	if entry.Type()&fs.ModeSymlink != 0 {
		// 对于符号链接，只需检查 entry.Info 与 entry 在公共字段上是否匹配。
		// Open 会解引用符号链接，因此 info 本身可能不同。
		feentry := formatInfoEntry(einfo)
		if fentry != feentry {
			t.errorf("%s: mismatch\n\tentry = %s\n\tentry.Info() = %s\n", path, fentry, feentry)
		}
	} else {
		feinfo := formatInfo(einfo)
		if feinfo != finfo {
			t.errorf("%s: mismatch:\n\tentry.Info() = %s\n\tfile.Stat() = %s\n", path, feinfo, finfo)
		}
	}

	// Stat 应与 Open+Stat 相同，即使对于符号链接也是如此。
	info2, err := fs.Stat(t.fsys, path)
	if err != nil {
		t.errorf("%s: fs.Stat: %w", path, err)
		return
	}
	finfo2 := formatInfo(info2)
	if finfo2 != finfo {
		t.errorf("%s: fs.Stat(...) = %s\n\twant %s", path, finfo2, finfo)
	}

	if fsys, ok := t.fsys.(fs.StatFS); ok {
		info2, err := fsys.Stat(path)
		if err != nil {
			t.errorf("%s: fsys.Stat: %w", path, err)
			return
		}
		finfo2 := formatInfo(info2)
		if finfo2 != finfo {
			t.errorf("%s: fsys.Stat(...) = %s\n\twant %s", path, finfo2, finfo)
		}
	}

	if fsys, ok := t.fsys.(fs.ReadLinkFS); ok {
		info2, err := fsys.Lstat(path)
		if err != nil {
			t.errorf("%s: fsys.Lstat: %v", path, err)
			return
		}
		fientry2 := formatInfoEntry(info2)
		if fentry != fientry2 {
			t.errorf("%s: mismatch:\n\tentry = %s\n\tfsys.Lstat(...) = %s", path, fentry, fientry2)
		}
		feinfo := formatInfo(einfo)
		finfo2 := formatInfo(info2)
		if feinfo != finfo2 {
			t.errorf("%s: mismatch:\n\tentry.Info() = %s\n\tfsys.Lstat(...) = %s\n", path, feinfo, finfo2)
		}
	}
}

// checkDirList 检查两个目录列表包含相同的文件和文件信息。
// 列表的顺序无需匹配。
func (t *fsTester) checkDirList(dir, desc string, list1, list2 []fs.DirEntry) {
	old := make(map[string]fs.DirEntry)
	checkMode := func(entry fs.DirEntry) {
		if entry.IsDir() != (entry.Type()&fs.ModeDir != 0) {
			if entry.IsDir() {
				t.errorf("%s: ReadDir returned %s with IsDir() = true, Type() & ModeDir = 0", dir, entry.Name())
			} else {
				t.errorf("%s: ReadDir returned %s with IsDir() = false, Type() & ModeDir = ModeDir", dir, entry.Name())
			}
		}
	}

	for _, entry1 := range list1 {
		old[entry1.Name()] = entry1
		checkMode(entry1)
	}

	var diffs []string
	for _, entry2 := range list2 {
		entry1 := old[entry2.Name()]
		if entry1 == nil {
			checkMode(entry2)
			diffs = append(diffs, "+ "+formatEntry(entry2))
			continue
		}
		if formatEntry(entry1) != formatEntry(entry2) {
			diffs = append(diffs, "- "+formatEntry(entry1), "+ "+formatEntry(entry2))
		}
		delete(old, entry2.Name())
	}
	for _, entry1 := range old {
		diffs = append(diffs, "- "+formatEntry(entry1))
	}

	if len(diffs) == 0 {
		return
	}

	slices.SortFunc(diffs, func(a, b string) int {
		fa := strings.Fields(a)
		fb := strings.Fields(b)
		// 按名称排序（i < j），然后按 +/- 排序（j < i，因为 + < -）
		return strings.Compare(fa[1]+" "+fb[0], fb[1]+" "+fa[0])
	})

	t.errorf("%s: diff %s:\n\t%s", dir, desc, strings.Join(diffs, "\n\t"))
}

// checkFile 检查基本的文件读取是否正确工作。
func (t *fsTester) checkFile(file string) {
	t.files = append(t.files, file)

	// 读取整个文件。
	f, err := t.fsys.Open(file)
	if err != nil {
		t.errorf("%s: Open: %w", file, err)
		return
	}

	data, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		t.errorf("%s: Open+ReadAll: %w", file, err)
		return
	}

	if err := f.Close(); err != nil {
		t.errorf("%s: Close: %w", file, err)
	}

	// 检查关闭两次不会崩溃。
	// 返回值无关紧要。
	f.Close()

	// 检查 ReadFile 在存在时是否正常工作。
	if fsys, ok := t.fsys.(fs.ReadFileFS); ok {
		data2, err := fsys.ReadFile(file)
		if err != nil {
			t.errorf("%s: fsys.ReadFile: %w", file, err)
			return
		}
		t.checkFileRead(file, "ReadAll vs fsys.ReadFile", data, data2)

		// 修改数据并再次检查。修改
		// 返回的字节切片不应影响下一次调用。
		for i := range data2 {
			data2[i]++
		}
		data2, err = fsys.ReadFile(file)
		if err != nil {
			t.errorf("%s: second call to fsys.ReadFile: %w", file, err)
			return
		}
		t.checkFileRead(file, "Readall vs second fsys.ReadFile", data, data2)

		t.checkBadPath(file, "ReadFile",
			func(name string) error { _, err := fsys.ReadFile(name); return err })
	}

	// 检查 fs.ReadFile 与 t.fsys 是否能正常工作。
	data2, err := fs.ReadFile(t.fsys, file)
	if err != nil {
		t.errorf("%s: fs.ReadFile: %w", file, err)
		return
	}
	t.checkFileRead(file, "ReadAll vs fs.ReadFile", data, data2)

	// 使用 iotest.TestReader 检查小批量读取、Seek、ReadAt。
	f, err = t.fsys.Open(file)
	if err != nil {
		t.errorf("%s: second Open: %w", file, err)
		return
	}
	defer f.Close()
	if err := iotest.TestReader(f, data); err != nil {
		t.errorf("%s: failed TestReader:\n\t%s", file, strings.ReplaceAll(err.Error(), "\n", "\n\t"))
	}
}

func (t *fsTester) checkFileRead(file, desc string, data1, data2 []byte) {
	if string(data1) != string(data2) {
		t.errorf("%s: %s: different data returned\n\t%q\n\t%q", file, desc, data1, data2)
		return
	}
}

// checkOpen 通过尝试打开然后关闭给定的文件路径来验证文件打开行为。
func (t *fsTester) checkOpen(file string) {
	t.checkBadPath(file, "Open", func(file string) error {
		f, err := t.fsys.Open(file)
		if err == nil {
			f.Close()
		}
		return err
	})
}

// checkBadPath 检查文件名的各种无效形式无法使用 open 打开。
func (t *fsTester) checkBadPath(file string, desc string, open func(string) error) {
	bad := []string{
		"/" + file,
		file + "/.",
	}
	if file == "." {
		bad = append(bad, "/")
	}
	if i := strings.Index(file, "/"); i >= 0 {
		bad = append(bad,
			file[:i]+"//"+file[i+1:],
			file[:i]+"/./"+file[i+1:],
			file[:i]+`\`+file[i+1:],
			file[:i]+"/../"+file,
		)
	}
	if i := strings.LastIndex(file, "/"); i >= 0 {
		bad = append(bad,
			file[:i]+"//"+file[i+1:],
			file[:i]+"/./"+file[i+1:],
			file[:i]+`\`+file[i+1:],
			file+"/../"+file[i+1:],
		)
	}

	for _, b := range bad {
		if err := open(b); err == nil {
			t.errorf("%s: %s(%s) succeeded, want error", file, desc, b)
		}
	}
}
