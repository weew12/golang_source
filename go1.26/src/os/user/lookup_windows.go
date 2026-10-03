// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package user

import (
	"errors"
	"fmt"
	"internal/syscall/windows"
	"internal/syscall/windows/registry"
	"runtime"
	"syscall"
	"unsafe"
)

func isDomainJoined() (bool, error) {
	var domain *uint16
	var status uint32
	err := syscall.NetGetJoinInformation(nil, &domain, &status)
	if err != nil {
		return false, err
	}
	syscall.NetApiBufferFree((*byte)(unsafe.Pointer(domain)))
	return status == syscall.NetSetupDomainName, nil
}

func lookupFullNameDomain(domainAndUser string) (string, error) {
	return syscall.TranslateAccountName(domainAndUser,
		syscall.NameSamCompatible, syscall.NameDisplay, 50)
}

func lookupFullNameServer(servername, username string) (string, error) {
	s, e := syscall.UTF16PtrFromString(servername)
	if e != nil {
		return "", e
	}
	u, e := syscall.UTF16PtrFromString(username)
	if e != nil {
		return "", e
	}
	var p *byte
	e = syscall.NetUserGetInfo(s, u, 10, &p)
	if e != nil {
		return "", e
	}
	defer syscall.NetApiBufferFree(p)
	i := (*syscall.UserInfo10)(unsafe.Pointer(p))
	return windows.UTF16PtrToString(i.FullName), nil
}

func lookupFullName(domain, username, domainAndUser string) (string, error) {
	joined, err := isDomainJoined()
	if err == nil && joined {
		name, err := lookupFullNameDomain(domainAndUser)
		if err == nil {
			return name, nil
		}
	}
	name, err := lookupFullNameServer(domain, username)
	if err == nil {
		return name, nil
	}
	// domain 既不能作为域，也不能作为服务器使用
	// 可能是域服务器不可用
	// 那就把用户名当作全名
	return username, nil
}

// getProfilesDirectory 获取存储用户配置文件的根目录的路径。
func getProfilesDirectory() (string, error) {
	n := uint32(100)
	for {
		b := make([]uint16, n)
		e := windows.GetProfilesDirectory(&b[0], &n)
		if e == nil {
			return syscall.UTF16ToString(b), nil
		}
		if e != syscall.ERROR_INSUFFICIENT_BUFFER {
			return "", e
		}
		if n <= uint32(len(b)) {
			return "", e
		}
	}
}

func isServiceAccount(sid *syscall.SID) bool {
	if !windows.IsValidSid(sid) {
		// 我们不接受来自公共 API 的 SID，因此这永远不会发生。
		// 但保险起见，还是验证一下。
		return false
	}
	// 按照以下文档，下列 RID 被视为服务用户账户：
	// https://learn.microsoft.com/en-us/windows/win32/secauthz/well-known-sids 以及
	// https://learn.microsoft.com/en-us/windows/win32/services/service-user-accounts：
	// - "S-1-5-18": LocalSystem
	// - "S-1-5-19": LocalService
	// - "S-1-5-20": NetworkService
	if windows.GetSidSubAuthorityCount(sid) != windows.SID_REVISION ||
		windows.GetSidIdentifierAuthority(sid) != windows.SECURITY_NT_AUTHORITY {
		return false
	}
	switch windows.GetSidSubAuthority(sid, 0) {
	case windows.SECURITY_LOCAL_SYSTEM_RID,
		windows.SECURITY_LOCAL_SERVICE_RID,
		windows.SECURITY_NETWORK_SERVICE_RID:
		return true
	}
	return false
}

func isValidUserAccountType(sid *syscall.SID, sidType uint32) bool {
	switch sidType {
	case syscall.SidTypeUser:
		return true
	case syscall.SidTypeWellKnownGroup:
		return isServiceAccount(sid)
	}
	return false
}

func isValidGroupAccountType(sidType uint32) bool {
	switch sidType {
	case syscall.SidTypeGroup:
		return true
	case syscall.SidTypeWellKnownGroup:
		// 一些众所周知的组也被视为服务账户，
		// 因此 isValidUserAccountType 对它们会返回 true。
		// 我们历来允许它们出现在 LookupGroup 和 LookupGroupId 中，
		// 所以这里不要把它们当作无效的。
		return true
	case syscall.SidTypeAlias:
		// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-samr/7b2aeb27-92fc-41f6-8437-deb65d950921#gt_0387e636-5654-4910-9519-1f8326cf5ec0
		// SidTypeAlias 也应与 SidTypeGroup 和 SidTypeWellKnownGroup 一起
		// 被视为组类型：
		// "alias object -> resource group: A group object..."
		//
		// 测试表明 "Administrators" 可以被视为 SidTypeAlias 类型。
		return true
	}
	return false
}

// lookupUsernameAndDomain 获取 usid 的用户名和域。
func lookupUsernameAndDomain(usid *syscall.SID) (username, domain string, sidType uint32, e error) {
	username, domain, sidType, e = usid.LookupAccount("")
	if e != nil {
		return "", "", 0, e
	}
	if !isValidUserAccountType(usid, sidType) {
		return "", "", 0, fmt.Errorf("user: should be user account type, not %d", sidType)
	}
	return username, domain, sidType, nil
}

// findHomeDirInRegistry 根据 uid 查找用户主目录路径。
func findHomeDirInRegistry(uid string) (dir string, e error) {
	k, e := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+uid, registry.QUERY_VALUE)
	if e != nil {
		return "", e
	}
	defer k.Close()
	dir, _, e = k.GetStringValue("ProfileImagePath")
	if e != nil {
		return "", e
	}
	return dir, nil
}

// lookupGroupName 接受一个组的名称并获取该组的 SID。
func lookupGroupName(groupname string) (string, error) {
	sid, _, t, e := syscall.LookupSID("", groupname)
	if e != nil {
		return "", e
	}
	if !isValidGroupAccountType(t) {
		return "", fmt.Errorf("lookupGroupName: should be group account type, not %d", t)
	}
	return sid.String()
}

// listGroupsForUsernameAndDomain 接受用户名和域，并获取
// 该用户所属本地组的 SID 列表。
func listGroupsForUsernameAndDomain(username, domain string) ([]string, error) {
	// 检查是否应同时使用域名和用户名。
	var query string
	joined, err := isDomainJoined()
	if err == nil && joined && len(domain) != 0 {
		query = domain + `\` + username
	} else {
		query = username
	}
	q, err := syscall.UTF16PtrFromString(query)
	if err != nil {
		return nil, err
	}
	var p0 *byte
	var entriesRead, totalEntries uint32
	// https://learn.microsoft.com/en-us/windows/win32/api/lmaccess/nf-lmaccess-netusergetlocalgroups
	// NetUserGetLocalGroups() 会返回一个 LocalGroupUserInfo0 列表，
	// 其中的元素保存着该用户所属本地组的名称。
	// 该列表不遵循任何排序顺序。
	err = windows.NetUserGetLocalGroups(nil, q, 0, windows.LG_INCLUDE_INDIRECT, &p0, windows.MAX_PREFERRED_LENGTH, &entriesRead, &totalEntries)
	if err != nil {
		return nil, err
	}
	defer syscall.NetApiBufferFree(p0)
	if entriesRead == 0 {
		return nil, nil
	}
	entries := (*[1024]windows.LocalGroupUserInfo0)(unsafe.Pointer(p0))[:entriesRead:entriesRead]
	var sids []string
	for _, entry := range entries {
		if entry.Name == nil {
			continue
		}
		sid, err := lookupGroupName(windows.UTF16PtrToString(entry.Name))
		if err != nil {
			return nil, err
		}
		sids = append(sids, sid)
	}
	return sids, nil
}

func newUser(uid, gid, dir, username, domain string) (*User, error) {
	domainAndUser := domain + `\` + username
	name, e := lookupFullName(domain, username, domainAndUser)
	if e != nil {
		return nil, e
	}
	u := &User{
		Uid:      uid,
		Gid:      gid,
		Username: domainAndUser,
		Name:     name,
		HomeDir:  dir,
	}
	return u, nil
}

var (
	// 未使用的变量（在此实现中）
	// 在测试期间被修改，以覆盖 cgo 实现中的代码路径。
	userBuffer  = 0
	groupBuffer = 0
)

func current() (*User, error) {
	// 使用 runAsProcessOwner 以确保在当前线程正模拟其他用户时，
	// 调用 syscall.OpenCurrentProcessToken 也能访问到进程令牌。
	// 参见 https://go.dev/issue/68647。
	var usr *User
	err := runAsProcessOwner(func() error {
		t, e := syscall.OpenCurrentProcessToken()
		if e != nil {
			return e
		}
		defer t.Close()
		u, e := t.GetTokenUser()
		if e != nil {
			return e
		}
		pg, e := t.GetTokenPrimaryGroup()
		if e != nil {
			return e
		}
		uid, e := u.User.Sid.String()
		if e != nil {
			return e
		}
		gid, e := pg.PrimaryGroup.String()
		if e != nil {
			return e
		}
		dir, e := t.GetUserProfileDirectory()
		if e != nil {
			return e
		}
		username, e := windows.GetUserName(syscall.NameSamCompatible)
		if e != nil {
			return e
		}
		displayName, e := windows.GetUserName(syscall.NameDisplay)
		if e != nil {
			// 从历史上看，当无法获取显示名称时，
			// 会使用用户名作为回退。
			displayName = username
		}
		usr = &User{
			Uid:      uid,
			Gid:      gid,
			Username: username,
			Name:     displayName,
			HomeDir:  dir,
		}
		return nil
	})
	return usr, err
}

// runAsProcessOwner 在当前进程所有者的上下文中运行 f，
// 即：在调用 f 之前移除任何可能生效的模拟（impersonation），
// 并在之后恢复模拟。
func runAsProcessOwner(f func() error) error {
	var impersonationRollbackErr error
	runtime.LockOSThread()
	defer func() {
		// 如果模拟失败，说明该线程正使用错误的令牌运行，
		// 所以最好终止它。
		// 这是通过不调用 runtime.UnlockOSThread 来实现的。
		if impersonationRollbackErr != nil {
			println("os/user: failed to revert to previous token:", impersonationRollbackErr.Error())
			runtime.Goexit()
		} else {
			runtime.UnlockOSThread()
		}
	}()
	prevToken, isProcessToken, err := getCurrentToken()
	if err != nil {
		return fmt.Errorf("os/user: failed to get current token: %w", err)
	}
	defer prevToken.Close()
	if !isProcessToken {
		if err = windows.RevertToSelf(); err != nil {
			return fmt.Errorf("os/user: failed to revert to self: %w", err)
		}
		defer func() {
			impersonationRollbackErr = windows.ImpersonateLoggedOnUser(prevToken)
		}()
	}
	return f()
}

// getCurrentToken 返回当前线程令牌；如果该线程没有令牌，
// 则返回进程令牌。
func getCurrentToken() (t syscall.Token, isProcessToken bool, err error) {
	thread, _ := windows.GetCurrentThread()
	// 需要 TOKEN_DUPLICATE 和 TOKEN_IMPERSONATE 才能在 ImpersonateLoggedOnUser 中使用该令牌。
	err = windows.OpenThreadToken(thread, syscall.TOKEN_QUERY|syscall.TOKEN_DUPLICATE|syscall.TOKEN_IMPERSONATE, true, &t)
	if errors.Is(err, windows.ERROR_NO_TOKEN) {
		// 没有在模拟，使用进程令牌。
		isProcessToken = true
		t, err = syscall.OpenCurrentProcessToken()
	}
	return t, isProcessToken, err
}

// lookupUserPrimaryGroup 使用以下方法获取用户的主组 SID：
// https://support.microsoft.com/en-us/help/297951/how-to-use-the-primarygroupid-attribute-to-find-the-primary-group-for
// 该方法遵循这个公式：domainRID + "-" + primaryGroupRID
func lookupUserPrimaryGroup(username, domain string) (string, error) {
	// 获取域的 RID
	sid, _, t, e := syscall.LookupSID("", domain)
	if e != nil {
		return "", e
	}
	if t != syscall.SidTypeDomain {
		return "", fmt.Errorf("lookupUserPrimaryGroup: should be domain account type, not %d", t)
	}
	domainRID, e := sid.String()
	if e != nil {
		return "", e
	}
	// 如果用户已加入某个域，则使用名为 "Domain Users" 的
	// 默认主组的 RID：
	// https://support.microsoft.com/en-us/help/243330/well-known-security-identifiers-in-windows-operating-systems
	// SID: S-1-5-21domain-513
	//
	// 获取域用户主组的正确方式是探测服务器 Active Directory 中的
	// 用户 primaryGroupID 属性：
	// https://learn.microsoft.com/en-us/windows/win32/adschema/a-primarygroupid
	//
	// 注意，出于性能原因，在 Windows 上不应修改域用户的主组，
	// 即使这样做是可行的。
	// The .NET Developer's Guide to Directory Services Programming - Page 409
	// https://books.google.bg/books?id=kGApqjobEfsC&lpg=PA410&ots=p7oo-eOQL7&dq=primary%20group%20RID&hl=bg&pg=PA409#v=onepage&q&f=false
	joined, err := isDomainJoined()
	if err == nil && joined {
		return domainRID + "-513", nil
	}
	// 对于非域用户，使用级别 4 调用 NetUserGetInfo()，
	// 在这种情况下不会有任何网络开销。
	// 这里主组同样不应从 RID 513 变化，
	// 但该组将被称为 "None"：
	// https://www.adampalmer.me/iodigitalsec/2013/08/10/windows-null-session-enumeration/
	// "Group 'None' (RID: 513)"
	u, e := syscall.UTF16PtrFromString(username)
	if e != nil {
		return "", e
	}
	d, e := syscall.UTF16PtrFromString(domain)
	if e != nil {
		return "", e
	}
	var p *byte
	e = syscall.NetUserGetInfo(d, u, 4, &p)
	if e != nil {
		return "", e
	}
	defer syscall.NetApiBufferFree(p)
	i := (*windows.UserInfo4)(unsafe.Pointer(p))
	return fmt.Sprintf("%s-%d", domainRID, i.PrimaryGroupID), nil
}

func newUserFromSid(usid *syscall.SID) (*User, error) {
	username, domain, sidType, e := lookupUsernameAndDomain(usid)
	if e != nil {
		return nil, e
	}
	uid, e := usid.String()
	if e != nil {
		return nil, e
	}
	var gid string
	if sidType == syscall.SidTypeWellKnownGroup {
		// 该 SID 不包含域；此函数的 domain 变量已被填充为
		// 该 SID 的标识符颁发机构（identifier authority）。这发生在
		// 诸如 "NT AUTHORITY\LocalSystem" 之类的特殊服务用户账户上。
		// 这种情况下，gid 与用户 SID 相同。
		gid = uid
	} else {
		gid, e = lookupUserPrimaryGroup(username, domain)
		if e != nil {
			return nil, e
		}
	}
	// 如果该用户至少登录过一次，则其主目录路径应存储在
	// 注册表中该 SID 对应的项下。参考资料：
	// https://social.technet.microsoft.com/wiki/contents/articles/13895.how-to-remove-a-corrupted-user-profile-from-the-registry.aspx
	// https://support.asperasoft.com/hc/en-us/articles/216127438-How-to-delete-Windows-user-profiles
	//
	// 注册表是查找主目录路径最可靠的方式，因为用户
	// 可能已决定将其移到默认位置（例如 C:\users）之外。
	// 参考资料：
	// https://answers.microsoft.com/en-us/windows/forum/windows_7-security/how-do-i-set-a-home-directory-outside-cusers-for-a/aed68262-1bf4-4a4d-93dc-7495193a440f
	dir, e := findHomeDirInRegistry(uid)
	if e != nil {
		// 如果注册表中不存在主目录路径，则该用户可能
		// 尚未登录；退而使用 getProfilesDirectory()。
		// 根据 SID 找到用户名，并将其追加到
		// getProfilesDirectory() 的结果之后。这里域无关紧要。
		dir, e = getProfilesDirectory()
		if e != nil {
			return nil, e
		}
		dir += `\` + username
	}
	return newUser(uid, gid, dir, username, domain)
}

func lookupUser(username string) (*User, error) {
	sid, _, t, e := syscall.LookupSID("", username)
	if e != nil {
		return nil, e
	}
	if !isValidUserAccountType(sid, t) {
		return nil, fmt.Errorf("user: should be user account type, not %d", t)
	}
	return newUserFromSid(sid)
}

func lookupUserId(uid string) (*User, error) {
	sid, e := syscall.StringToSid(uid)
	if e != nil {
		return nil, e
	}
	return newUserFromSid(sid)
}

func lookupGroup(groupname string) (*Group, error) {
	sid, err := lookupGroupName(groupname)
	if err != nil {
		return nil, err
	}
	return &Group{Name: groupname, Gid: sid}, nil
}

func lookupGroupId(gid string) (*Group, error) {
	sid, err := syscall.StringToSid(gid)
	if err != nil {
		return nil, err
	}
	groupname, _, t, err := sid.LookupAccount("")
	if err != nil {
		return nil, err
	}
	if !isValidGroupAccountType(t) {
		return nil, fmt.Errorf("lookupGroupId: should be group account type, not %d", t)
	}
	return &Group{Name: groupname, Gid: gid}, nil
}

func listGroups(user *User) ([]string, error) {
	var sids []string
	if u, err := Current(); err == nil && u.Uid == user.Uid {
		// 从当前进程令牌中获取当前用户的组
		// 更快也更可靠。
		err := runAsProcessOwner(func() error {
			t, err := syscall.OpenCurrentProcessToken()
			if err != nil {
				return err
			}
			defer t.Close()
			groups, err := windows.GetTokenGroups(t)
			if err != nil {
				return err
			}
			for _, g := range groups.AllGroups() {
				sid, err := g.Sid.String()
				if err != nil {
					return err
				}
				sids = append(sids, sid)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		sid, err := syscall.StringToSid(user.Uid)
		if err != nil {
			return nil, err
		}
		username, domain, _, err := lookupUsernameAndDomain(sid)
		if err != nil {
			return nil, err
		}
		sids, err = listGroupsForUsernameAndDomain(username, domain)
		if err != nil {
			return nil, err
		}
	}
	// 如果用户的主组还不在列表中，则将其加入。
	// 这样做只是为了符合 POSIX 的主组概念。
	for _, sid := range sids {
		if sid == user.Gid {
			return sids, nil
		}
	}
	return append(sids, user.Gid), nil
}
