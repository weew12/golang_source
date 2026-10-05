// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package embed 提供对嵌入到运行中的 Go 程序内的文件的访问。
//
// 导入 "embed" 的 Go 源文件可以使用 //go:embed 指令，
// 用一个 string、[]byte 或 [FS] 类型的变量来初始化，
// 其内容为编译时从包目录或子目录读取到的文件内容。
//
// 例如，以下是将名为 hello.txt 的文件嵌入，
// 然后在运行时打印其内容的三种方式。
//
// 将单个文件嵌入到字符串中：
//
//	import _ "embed"
//
//	//go:embed hello.txt
//	var s string
//	print(s)
//
// 将单个文件嵌入到字节切片中：
//
//	import _ "embed"
//
//	//go:embed hello.txt
//	var b []byte
//	print(string(b))
//
// 将一个或多个文件嵌入到文件系统中：
//
//	import "embed"
//
//	//go:embed hello.txt
//	var f embed.FS
//	data, _ := f.ReadFile("hello.txt")
//	print(string(data))
//
// # 指令
//
// 在变量声明上方的 //go:embed 指令使用一个或多个 path.Match 模式
// 来指定要嵌入哪些文件。
//
// 该指令必须紧接在包含单个变量声明的行之前。
// 指令与声明之间只允许有空行和 ‘//’ 行注释。
//
// 变量的类型必须是字符串类型、字节类型的切片，
// 或 [FS]（或 [FS] 的别名）。
//
// 例如：
//
//	package server
//
//	import "embed"
//
//	// content 保存我们静态 Web 服务器的内容。
//	//go:embed image/* template/*
//	//go:embed html/index.html
//	var content embed.FS
//
// Go 构建系统会识别这些指令，并安排用文件系统中匹配的文件
// 填充所声明的变量（在上面的示例中为 content）。
//
// //go:embed 指令为了简洁可以接受多个以空格分隔的模式，
// 但也可以重复使用，以便在模式很多时避免过长的行。
// 这些模式是相对于包含该源文件的包目录进行解释的。
// 路径分隔符为正斜杠，即使在 Windows 系统上也是如此。
// 模式不能包含 ‘.’ 或 ‘..’ 或空的路径元素，也不能以斜杠开头或结尾。
// 要匹配当前目录中的所有内容，请使用 ‘*’ 而不是 ‘.’。
// 为了允许文件名称中包含空格，模式可以写成 Go 的双引号或反引号字符串字面量。
//
// 如果某个模式指定了一个目录，则该目录为根的子目录树中的所有文件
// 都会被（递归地）嵌入，但名称以 ‘.’ 或 ‘_’ 开头的文件会被排除。
// 因此上面示例中的变量几乎等价于：
//
//	// content 是我们静态 Web 服务器的内容。
//	//go:embed image template html/index.html
//	var content embed.FS
//
// 不同之处在于，‘image/*’ 会嵌入 ‘image/.tempfile’，而 ‘image’ 不会。
// 两者都不会嵌入 ‘image/dir/.tempfile’。
//
// 如果某个模式以前缀 ‘all:’ 开头，则遍历目录的规则会改变，
// 以包含那些以 ‘.’ 或 ‘_’ 开头的文件。例如，‘all:image’ 会同时嵌入
// ‘image/.tempfile’ 和 ‘image/dir/.tempfile’。
//
// //go:embed 指令既可以用于导出变量，也可以用于未导出变量，
// 取决于该包是否希望将数据提供给其他包使用。
// 它只能用于包作用域的变量，不能用于局部变量。
//
// 模式不得匹配包所在模块之外的文件，例如 ‘.git/*’、符号链接、
// 'vendor/'，或任何包含 go.mod 的目录（这些是独立的模块）。
// 模式不得匹配名称中包含特殊标点字符  " * < > ? ` ' | / \ 和 : 的文件。
// 对空目录的匹配会被忽略。此后，//go:embed 行中的每个模式
// 都必须至少匹配一个文件或非空目录。
//
// 如果任何模式无效或匹配无效，构建将失败。
//
// # 字符串与字节
//
// 类型为 string 或 []byte 的变量的 //go:embed 行只能有一个模式，
// 并且该模式只能匹配单个文件。该 string 或 []byte 会用
// 该文件的内容进行初始化。
//
// 即使使用 string 或 []byte，//go:embed 指令也要求导入 "embed"。
// 在不引用 [embed.FS] 的源文件中，请使用空白导入（import _ "embed"）。
//
// # 文件系统
//
// 对于嵌入单个文件，string 或 []byte 类型的变量通常是最佳选择。
// [FS] 类型支持嵌入一棵文件树，例如一个包含静态 Web 服务器
// 内容的目录，如上面的示例所示。
//
// FS 实现了 [io/fs] 包的 [FS] 接口，因此它可以与任何理解
// 文件系统的包一起使用，包括 [net/http]、[text/template] 和 [html/template]。
//
// 例如，给定上面示例中的 content 变量，我们可以写：
//
//	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(content))))
//
//	template.ParseFS(content, "*.tmpl")
//
// # 工具
//
// 为了支持分析 Go 包的工具，//go:embed 行中出现的模式
// 可在 “go list” 输出中获得。参见 “go help list” 输出中的
// EmbedPatterns、TestEmbedPatterns 和 XTestEmbedPatterns 字段。
package embed

import (
	"errors"
	"internal/bytealg"
	"internal/stringslite"
	"io"
	"io/fs"
	"time"
)

// FS 是一个只读的文件集合，通常使用 //go:embed 指令进行初始化。
// 当声明时没有 //go:embed 指令时，FS 是一个空文件系统。
//
// FS 是一个只读值，因此可以安全地被多个 goroutine 同时使用，
// 也可以安全地将 FS 类型的值相互赋值。
//
// FS 实现了 fs.FS，因此它可以与任何理解文件系统接口的包一起使用，
// 包括 net/http、text/template 和 html/template。
//
// 有关初始化 FS 的更多详细信息，请参阅包文档。
type FS struct {
	// 编译器知道此结构体的内存布局。
	// 参见 cmd/compile/internal/staticdata 的 WriteEmbed。
	//
	// files 列表按名称排序，但并非简单的字符串比较。
	// 相反，每个文件的名称采用 "dir/elem" 或 "dir/elem/" 的形式。
	// 可选的末尾斜杠表示该文件本身是一个目录。
	// files 列表首先按 dir 排序（如果 dir 缺失，则视为 "."），
	// 然后按 base 排序，因此以下文件列表：
	//
	//	p
	//	q/
	//	q/r
	//	q/s/
	//	q/s/t
	//	q/s/u
	//	q/v
	//	w
	//
	// 实际上排序为：
	//
	//	p       # dir=.    elem=p
	//	q/      # dir=.    elem=q
	//	w       # dir=.    elem=w
	//	q/r     # dir=q    elem=r
	//	q/s/    # dir=q    elem=s
	//	q/v     # dir=q    elem=v
	//	q/s/t   # dir=q/s  elem=t
	//	q/s/u   # dir=q/s  elem=u
	//
	// 这种顺序将目录内容聚集在列表中连续的区段中，
	// 使得读取目录时可以使用二分查找来定位相关的条目序列。
	files *[]file
}

// split 按照上面 FS 结构体注释中描述的方式，
// 将 name 拆分为 dir 和 elem。isDir 报告末尾斜杠是否存在，
// 表示 name 是一个目录。
func split(name string) (dir, elem string, isDir bool) {
	name, isDir = stringslite.CutSuffix(name, "/")
	i := bytealg.LastIndexByteString(name, '/')
	if i < 0 {
		return ".", name, isDir
	}
	return name[:i], name[i+1:], isDir
}

var (
	_ fs.ReadDirFS  = FS{}
	_ fs.ReadFileFS = FS{}
)

// file 是 FS 中的单个文件。
// 它实现了 fs.FileInfo 和 fs.DirEntry。
type file struct {
	// 编译器知道此结构体的内存布局。
	// 参见 cmd/compile/internal/staticdata 的 WriteEmbed。
	name string
	data string
	hash [16]byte // 截断的 SHA256 哈希值
}

var (
	_ fs.FileInfo = (*file)(nil)
	_ fs.DirEntry = (*file)(nil)
)

func (f *file) Name() string               { _, elem, _ := split(f.name); return elem }
func (f *file) Size() int64                { return int64(len(f.data)) }
func (f *file) ModTime() time.Time         { return time.Time{} }
func (f *file) IsDir() bool                { _, _, isDir := split(f.name); return isDir }
func (f *file) Sys() any                   { return nil }
func (f *file) Type() fs.FileMode          { return f.Mode().Type() }
func (f *file) Info() (fs.FileInfo, error) { return f, nil }

func (f *file) Mode() fs.FileMode {
	if f.IsDir() {
		return fs.ModeDir | 0555
	}
	return 0444
}

func (f *file) String() string {
	return fs.FormatFileInfo(f)
}

// dotFile 是根目录对应的文件，
// 在 FS 的 files 列表中会被省略。
var dotFile = &file{name: "./"}

// lookup 返回指定名称的文件，如果不存在则返回 nil。
func (f FS) lookup(name string) *file {
	if !fs.ValidPath(name) {
		// 编译器不应该生成名称无效的文件，
		// 因此这个检查并非严格必要（如果 name 无效，
		// 我们在下面也不应该找到匹配项），但它仍然是一个很好的兜底措施。
		return nil
	}
	if name == "." {
		return dotFile
	}
	if f.files == nil {
		return nil
	}

	// 二分查找以确定 name 在列表中的位置，
	// 然后检查 name 是否确实位于该位置。
	dir, elem, _ := split(name)
	files := *f.files
	i := sortSearch(len(files), func(i int) bool {
		idir, ielem, _ := split(files[i].name)
		return idir > dir || idir == dir && ielem >= elem
	})
	if i < len(files) && stringslite.TrimSuffix(files[i].name, "/") == name {
		return &files[i]
	}
	return nil
}

// readDir 返回目录 dir 对应的文件列表。
func (f FS) readDir(dir string) []file {
	if f.files == nil {
		return nil
	}
	// 二分查找以确定 dir 在列表中的起始和结束位置，
	// 然后返回列表中的那个切片。
	files := *f.files
	i := sortSearch(len(files), func(i int) bool {
		idir, _, _ := split(files[i].name)
		return idir >= dir
	})
	j := sortSearch(len(files), func(j int) bool {
		jdir, _, _ := split(files[j].name)
		return jdir > dir
	})
	return files[i:j]
}

// Open 打开指定名称的文件以供读取，并将其作为 [fs.File] 返回。
//
// 当文件不是目录时，返回的文件实现了 [io.Seeker] 和 [io.ReaderAt]。
func (f FS) Open(name string) (fs.File, error) {
	file := f.lookup(name)
	if file == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if file.IsDir() {
		return &openDir{file, f.readDir(name), 0}, nil
	}
	return &openFile{file, 0}, nil
}

// ReadDir 读取并返回指定名称的整个目录。
func (f FS) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	dir, ok := file.(*openDir)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("not a directory")}
	}
	list := make([]fs.DirEntry, len(dir.files))
	for i := range list {
		list[i] = &dir.files[i]
	}
	return list, nil
}

// ReadFile 读取并返回指定名称文件的内容。
func (f FS) ReadFile(name string) ([]byte, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	ofile, ok := file.(*openFile)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	}
	return []byte(ofile.f.data), nil
}

// openFile 是一个可供读取的普通文件。
type openFile struct {
	f      *file // 文件本身
	offset int64 // 当前读取偏移量
}

var (
	_ io.Seeker   = (*openFile)(nil)
	_ io.ReaderAt = (*openFile)(nil)
)

func (f *openFile) Close() error               { return nil }
func (f *openFile) Stat() (fs.FileInfo, error) { return f.f, nil }

func (f *openFile) Read(b []byte) (int, error) {
	if f.offset >= int64(len(f.f.data)) {
		return 0, io.EOF
	}
	if f.offset < 0 {
		return 0, &fs.PathError{Op: "read", Path: f.f.name, Err: fs.ErrInvalid}
	}
	n := copy(b, f.f.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *openFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case 0:
		// offset += 0
	case 1:
		offset += f.offset
	case 2:
		offset += int64(len(f.f.data))
	}
	if offset < 0 || offset > int64(len(f.f.data)) {
		return 0, &fs.PathError{Op: "seek", Path: f.f.name, Err: fs.ErrInvalid}
	}
	f.offset = offset
	return offset, nil
}

func (f *openFile) ReadAt(b []byte, offset int64) (int, error) {
	if offset < 0 || offset > int64(len(f.f.data)) {
		return 0, &fs.PathError{Op: "read", Path: f.f.name, Err: fs.ErrInvalid}
	}
	n := copy(b, f.f.data[offset:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

// openDir 是一个可供读取的目录。
type openDir struct {
	f      *file  // 目录文件本身
	files  []file // 目录内容
	offset int    // 读取偏移量，是 files 切片中的一个索引
}

func (d *openDir) Close() error               { return nil }
func (d *openDir) Stat() (fs.FileInfo, error) { return d.f, nil }

func (d *openDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.f.name, Err: errors.New("is a directory")}
}

func (d *openDir) ReadDir(count int) ([]fs.DirEntry, error) {
	n := len(d.files) - d.offset
	if n == 0 {
		if count <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if count > 0 && n > count {
		n = count
	}
	list := make([]fs.DirEntry, n)
	for i := range list {
		list[i] = &d.files[d.offset+i]
	}
	d.offset += n
	return list, nil
}

// sortSearch 类似于 sort.Search，但避免了导入。
func sortSearch(n int, f func(int) bool) int {
	// 定义 f(-1) == false 且 f(n) == true。
	// 不变式：f(i-1) == false，f(j) == true。
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1) // 计算 h 时避免溢出
		// i ≤ h < j
		if !f(h) {
			i = h + 1 // 保持 f(i-1) == false
		} else {
			j = h // 保持 f(j) == true
		}
	}
	// i == j，f(i-1) == false，且 f(j) == f(i) == true  =>  答案为 i。
	return i
}
