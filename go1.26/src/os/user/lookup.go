// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package user

import "sync"

const (
	userFile  = "/etc/passwd"
	groupFile = "/etc/group"
)

var colon = []byte{':'}

// Current 返回当前用户。
//
// 首次调用会缓存当前用户信息。
// 后续调用会返回缓存的值，不会反映当前用户的更改。
func Current() (*User, error) {
	cache.Do(func() { cache.u, cache.err = current() })
	if cache.err != nil {
		return nil, cache.err
	}
	u := *cache.u // 复制
	return &u, nil
}

// 当前用户的缓存
var cache struct {
	sync.Once
	u   *User
	err error
}

// Lookup 按用户名查找用户。如果找不到该用户，
// 返回的错误类型为 [UnknownUserError]。
func Lookup(username string) (*User, error) {
	if u, err := Current(); err == nil && u.Username == username {
		return u, err
	}
	return lookupUser(username)
}

// LookupId 按 userid 查找用户。如果找不到该用户，
// 返回的错误类型为 [UnknownUserIdError]。
func LookupId(uid string) (*User, error) {
	if u, err := Current(); err == nil && u.Uid == uid {
		return u, err
	}
	return lookupUserId(uid)
}

// LookupGroup 按名称查找组。如果找不到该组，
// 返回的错误类型为 [UnknownGroupError]。
func LookupGroup(name string) (*Group, error) {
	return lookupGroup(name)
}

// LookupGroupId 按 groupid 查找组。如果找不到该组，
// 返回的错误类型为 [UnknownGroupIdError]。
func LookupGroupId(gid string) (*Group, error) {
	return lookupGroupId(gid)
}

// GroupIds 返回该用户所属的组 ID 列表。
func (u *User) GroupIds() ([]string, error) {
	return listGroups(u)
}
