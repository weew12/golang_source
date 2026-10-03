// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/filepathlite"
	"internal/syscall/windows"
	"syscall"
)

const (
	PathSeparator     = '\\' // 特定于操作系统的路径分隔符
	PathListSeparator = ';'  // 特定于操作系统的路径列表分隔符
)

// IsPathSeparator 报告 c 是否为目录分隔符字符。
func IsPathSeparator(c uint8) bool {
	// 注意：Windows 接受 / 作为路径分隔符。
	return c == '\\' || c == '/'
}

// splitPath 返回基本名称和父目录。
func splitPath(path string) (string, string) {
	if path == "" {
		return ".", "."
	}

	// 前 prefixlen 个字节是父目录的一部分。
	// 前缀由卷名（如果有）和第一个 \（如果有意义）组成。
	prefixlen := filepathlite.VolumeNameLen(path)
	if len(path) > prefixlen && IsPathSeparator(path[prefixlen]) {
		if prefixlen == 0 {
			// 这是相对于当前卷的路径，例如 \foo。
			// 将开头的 \ 包含在前缀中。
			prefixlen = 1
		} else if path[prefixlen-1] == ':' {
			// 这是命名驱动器上的绝对路径，例如 c:\foo。
			// 将开头的 \ 包含在前缀中。
			prefixlen++
		}
	}

	i := len(path) - 1

	// 移除结尾的斜杠。
	for i >= prefixlen && IsPathSeparator(path[i]) {
		i--
	}
	path = path[:i+1]

	// 找到最后一个路径分隔符。其后面的内容就是基本名称。
	for i >= prefixlen && !IsPathSeparator(path[i]) {
		i--
	}
	basename := path[i+1:]
	if basename == "" {
		basename = "."
	}

	// 移除结尾的斜杠。剩余部分就是 dirname。
	for i >= prefixlen && IsPathSeparator(path[i]) {
		i--
	}
	dirname := path[:i+1]
	if dirname == "" {
		dirname = "."
	}

	return dirname, basename
}

func dirname(path string) string {
	vol := filepathlite.VolumeName(path)
	i := len(path) - 1
	for i >= len(vol) && !IsPathSeparator(path[i]) {
		i--
	}
	dir := path[len(vol) : i+1]
	last := len(dir) - 1
	if last > 0 && IsPathSeparator(dir[last]) {
		dir = dir[:last]
	}
	if dir == "" {
		dir = "."
	}
	return vol + dir
}

// fixLongPath 在需要时返回路径的扩展长度（以 \\?\ 为前缀）形式，
// 以避免 Windows 施加的默认 260 字符文件路径限制。
// 如果路径足够短，或已经带有扩展长度前缀，
// fixLongPath 会原样返回 path。如果路径是相对路径，
// 且将其与当前工作目录连接后得到的路径太长，
// fixLongPath 会返回带扩展长度前缀的绝对路径。
//
// 参见 https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file#maximum-path-length-limitation
func fixLongPath(path string) string {
	if windows.CanUseLongPaths {
		return path
	}
	return addExtendedPrefix(path)
}

// addExtendedPrefix 将扩展路径前缀（\\?\）添加到 path。
func addExtendedPrefix(path string) string {
	if len(path) >= 4 {
		if path[:4] == `\??\` {
			// 已经使用 \??\ 扩展过了
			return path
		}
		if IsPathSeparator(path[0]) && IsPathSeparator(path[1]) && path[2] == '?' && IsPathSeparator(path[3]) {
			// 已经使用 \\?\ 或任意组合的目录分隔符扩展过了。
			return path
		}
	}

	// 如果路径“很短”，则什么都不做（也不分配内存）。
	// 根据经验（至少在 Windows Server 2013 构建器上），
	// 内核可以任意接受小于 248 字节的路径。这与
	// 上面文档所述一致：
	// “当使用 API 创建目录时，指定的路径不能长到
	// 无法追加 8.3 文件名（也就是说，目录名不能超过
	// MAX_PATH 减 12）。”由于 MAX_PATH 为 260，260 - 12 = 248。
	//
	// MSDN 文档似乎表明长度为 248 字节的普通路径
	// 可以工作；但根据经验，路径必须小于 248 字节。
	pathLength := len(path)
	if !filepathlite.IsAbs(path) {
		// 如果路径是相对路径，我们需要在判断它是否太长之前，
		// 将工作目录加一个分隔符前置到该路径。
		// 我们不想在这里调用 syscall.Getwd，因为每次以相对路径
		// 调用 fixLongPath 时该调用开销都很大，所以我们使用缓存。
		// 请注意，如果在未使用 os.Chdir 的情况下更改了工作目录，
		// 即直接使用 syscall.Chdir 或 cgo，getwdCache 可能已过时。
		// 这没关系，因为最坏的情况只是我们未能修复该路径。
		getwdCache.Lock()
		if getwdCache.dir == "" {
			// 初始化工作目录缓存。
			getwdCache.dir, _ = syscall.Getwd()
		}
		pathLength += len(getwdCache.dir) + 1
		getwdCache.Unlock()
	}

	if pathLength < 248 {
		// 不修复。（这就是 Go 1.7 及更早版本的行为，
		// 不会自动生成 \\?\ 形式）
		return path
	}

	var isUNC, isDevice bool
	if len(path) >= 2 && IsPathSeparator(path[0]) && IsPathSeparator(path[1]) {
		if len(path) >= 4 && path[2] == '.' && IsPathSeparator(path[3]) {
			// 以 //./ 开头
			isDevice = true
		} else {
			// 以 // 开头
			isUNC = true
		}
	}
	var prefix []uint16
	if isUNC {
		// UNC 路径，前置 \\?\UNC\ 前缀。
		prefix = []uint16{'\\', '\\', '?', '\\', 'U', 'N', 'C', '\\'}
	} else if isDevice {
		// 不要给设备路径添加扩展前缀，因为那会
		// 改变其含义。
	} else {
		prefix = []uint16{'\\', '\\', '?', '\\'}
	}

	p, err := syscall.UTF16FromString(path)
	if err != nil {
		return path
	}
	// 使用路径长度加上空终止符来估算所需的缓冲区大小。
	// pathLength 包含工作目录。这应当是准确的，
	// 除非工作目录在未使用 os.Chdir 的情况下发生了更改。
	n := uint32(pathLength) + 1
	var buf []uint16
	for {
		buf = make([]uint16, n+uint32(len(prefix)))
		n, err = syscall.GetFullPathName(&p[0], n, &buf[len(prefix)], nil)
		if err != nil {
			return path
		}
		if n <= uint32(len(buf)-len(prefix)) {
			buf = buf[:n+uint32(len(prefix))]
			break
		}
	}
	if isUNC {
		// 移除开头的 \\。
		buf = buf[2:]
	}
	copy(buf, prefix)
	return syscall.UTF16ToString(buf)
}
