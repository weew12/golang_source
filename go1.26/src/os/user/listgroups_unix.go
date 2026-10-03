// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (((unix && !android) || (js && wasm) || wasip1) && ((!cgo && !darwin) || osusergo)) || aix

package user

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

func listGroupsFromReader(u *User, r io.Reader) ([]string, error) {
	if u.Username == "" {
		return nil, errors.New("user: list groups: empty username")
	}
	primaryGid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, fmt.Errorf("user: list groups for %s: invalid gid %q", u.Username, u.Gid)
	}

	userCommas := []byte("," + u.Username + ",")  // ,john,
	userFirst := userCommas[1:]                   // john,
	userLast := userCommas[:len(userCommas)-1]    // ,john
	userOnly := userCommas[1 : len(userCommas)-1] // john

	// 首先添加主 Gid。
	groups := []string{u.Gid}

	rd := bufio.NewReader(r)
	done := false
	for !done {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				done = true
			} else {
				return groups, err
			}
		}

		// 在用户列表中查找用户名。如果找到该用户，
		// 则将该 GID 追加到 groups 切片中。

		// /etc/passwd 或 /etc/group 没有规范，但我们尝试遵循
		// 与 glibc 解析器相同的规则，它允许行首有注释和空白字符。
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' ||
			// 如果你在某一行中查找 gid，而该行的组名（第一个字段）
			// 以 "+" 或 "-" 开头，glibc 会找不到该记录，我们也应如此。
			line[0] == '+' || line[0] == '-' {
			continue
		}

		// /etc/group 的格式为
		// 	groupname:password:GID:user_list
		// 例如
		// 	wheel:x:10:john,paul,jack
		//	tcpdump:x:72:
		listIdx := bytes.LastIndexByte(line, ':')
		if listIdx == -1 || listIdx == len(line)-1 {
			// 没有逗号，或组列表为空。
			continue
		}
		if bytes.Count(line[:listIdx], colon) != 2 {
			// 冒号数量不正确。
			continue
		}
		list := line[listIdx+1:]
		// 在列表中检查用户，不做拆分或复制。
		if !(bytes.Equal(list, userOnly) || bytes.HasPrefix(list, userFirst) || bytes.HasSuffix(list, userLast) || bytes.Contains(list, userCommas)) {
			continue
		}

		// groupname:password:GID
		parts := bytes.Split(line[:listIdx], colon)
		if len(parts) != 3 || len(parts[0]) == 0 {
			continue
		}
		gid := string(parts[2])
		// 确保它是数字，并且与主 GID 不同。
		numGid, err := strconv.Atoi(gid)
		if err != nil || numGid == primaryGid {
			continue
		}

		groups = append(groups, gid)
	}

	return groups, nil
}

func listGroups(u *User) ([]string, error) {
	f, err := os.Open(groupFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return listGroupsFromReader(u, f)
}
