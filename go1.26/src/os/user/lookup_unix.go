// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ((unix && !android) || (js && wasm) || wasip1) && ((!cgo && !darwin) || osusergo)

package user

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

// lineFunc 返回一个值、一个错误，或返回 (nil, nil) 以跳过该行。
type lineFunc func(line []byte) (v any, err error)

// readColonFile 将 r 解析为 /etc/group 或 /etc/passwd 风格的文件，
// 对每一行运行 fn。readColonFile 返回一个值、一个错误，或者如果到达
// 文件末尾都未匹配则返回 (nil, nil)。
//
// readCols 是将传递给 fn 的以冒号分隔字段的最小数量；
// 在过长的行中，多余的字段可能会被静默丢弃。
func readColonFile(r io.Reader, fn lineFunc, readCols int) (v any, err error) {
	rd := bufio.NewReader(r)

	// 逐行读取文件。
	for {
		var isPrefix bool
		var wholeLine []byte

		// 读取下一行。我们以块为单位读取（尽可能多地利用读取器的
		// 缓冲区），在每一步检查是否已读取足够的列，并将最终结果
		// 存储在 wholeLine 中。
		for {
			var line []byte
			line, isPrefix, err = rd.ReadLine()

			if err != nil {
				// 如果到达 EOF 都未匹配，应返回 (nil, nil)。
				if err == io.EOF {
					err = nil
				}
				return nil, err
			}

			// 简单的常见情况：行足够短，可以放入单个读取器的缓冲区。
			if !isPrefix && len(wholeLine) == 0 {
				wholeLine = line
				break
			}

			wholeLine = append(wholeLine, line...)

			// 检查是否已读取整行（或足够的列）。
			if !isPrefix || bytes.Count(wholeLine, []byte{':'}) >= readCols {
				break
			}
		}

		// /etc/passwd 或 /etc/group 没有规范，但我们尝试遵循
		// 与 glibc 解析器相同的规则，它允许行首有注释和空白字符。
		wholeLine = bytes.TrimSpace(wholeLine)
		if len(wholeLine) == 0 || wholeLine[0] == '#' {
			continue
		}
		v, err = fn(wholeLine)
		if v != nil || err != nil {
			return
		}

		// 如有必要，跳过该行的其余部分
		for ; isPrefix; _, isPrefix, err = rd.ReadLine() {
			if err != nil {
				// 如果到达 EOF 都未匹配，应返回 (nil, nil)。
				if err == io.EOF {
					err = nil
				}
				return nil, err
			}
		}
	}
}

func matchGroupIndexValue(value string, idx int) lineFunc {
	var leadColon string
	if idx > 0 {
		leadColon = ":"
	}
	substr := []byte(leadColon + value + ":")
	return func(line []byte) (v any, err error) {
		if !bytes.Contains(line, substr) || bytes.Count(line, colon) < 3 {
			return
		}
		// wheel:*:0:root
		parts := strings.SplitN(string(line), ":", 4)
		if len(parts) < 4 || parts[0] == "" || parts[idx] != value ||
			// 如果文件包含 +foo 而你搜索 "foo"，glibc 会返回
			// "invalid argument" 错误。类似地，如果你为组名以 "+"
			// 或 "-" 开头的那一行搜索 gid，glibc 会找不到该记录。
			parts[0][0] == '+' || parts[0][0] == '-' {
			return
		}
		if _, err := strconv.Atoi(parts[2]); err != nil {
			return nil, nil
		}
		return &Group{Name: parts[0], Gid: parts[2]}, nil
	}
}

func findGroupId(id string, r io.Reader) (*Group, error) {
	if v, err := readColonFile(r, matchGroupIndexValue(id, 2), 3); err != nil {
		return nil, err
	} else if v != nil {
		return v.(*Group), nil
	}
	return nil, UnknownGroupIdError(id)
}

func findGroupName(name string, r io.Reader) (*Group, error) {
	if v, err := readColonFile(r, matchGroupIndexValue(name, 0), 3); err != nil {
		return nil, err
	} else if v != nil {
		return v.(*Group), nil
	}
	return nil, UnknownGroupError(name)
}

// 如果某一行的给定索引处具有给定值，则为该行返回一个 *User。
func matchUserIndexValue(value string, idx int) lineFunc {
	var leadColon string
	if idx > 0 {
		leadColon = ":"
	}
	substr := []byte(leadColon + value + ":")
	return func(line []byte) (v any, err error) {
		if !bytes.Contains(line, substr) || bytes.Count(line, colon) < 6 {
			return
		}
		// kevin:x:1005:1006::/home/kevin:/usr/bin/zsh
		parts := strings.SplitN(string(line), ":", 7)
		if len(parts) < 6 || parts[idx] != value || parts[0] == "" ||
			parts[0][0] == '+' || parts[0][0] == '-' {
			return
		}
		if _, err := strconv.Atoi(parts[2]); err != nil {
			return nil, nil
		}
		if _, err := strconv.Atoi(parts[3]); err != nil {
			return nil, nil
		}
		u := &User{
			Username: parts[0],
			Uid:      parts[2],
			Gid:      parts[3],
			Name:     parts[4],
			HomeDir:  parts[5],
		}
		// pw_gecos 字段并没有完全标准化。有些文档说：
		// "It is expected to be a comma separated list of
		// personal data where the first item is the full name of the
		// user."
		u.Name, _, _ = strings.Cut(u.Name, ",")
		return u, nil
	}
}

func findUserId(uid string, r io.Reader) (*User, error) {
	i, e := strconv.Atoi(uid)
	if e != nil {
		return nil, errors.New("user: invalid userid " + uid)
	}
	if v, err := readColonFile(r, matchUserIndexValue(uid, 2), 6); err != nil {
		return nil, err
	} else if v != nil {
		return v.(*User), nil
	}
	return nil, UnknownUserIdError(i)
}

func findUsername(name string, r io.Reader) (*User, error) {
	if v, err := readColonFile(r, matchUserIndexValue(name, 0), 6); err != nil {
		return nil, err
	} else if v != nil {
		return v.(*User), nil
	}
	return nil, UnknownUserError(name)
}

func lookupGroup(groupname string) (*Group, error) {
	f, err := os.Open(groupFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return findGroupName(groupname, f)
}

func lookupGroupId(id string) (*Group, error) {
	f, err := os.Open(groupFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return findGroupId(id, f)
}

func lookupUser(username string) (*User, error) {
	f, err := os.Open(userFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return findUsername(username, f)
}

func lookupUserId(uid string) (*User, error) {
	f, err := os.Open(userFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return findUserId(uid, f)
}
