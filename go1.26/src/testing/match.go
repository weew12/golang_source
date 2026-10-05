// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// matcher 对子测试和子基准测试的名称进行清理、去重和过滤。
type matcher struct {
	filter    filterMatch
	skip      filterMatch
	matchFunc func(pat, str string) (bool, error)

	mu sync.Mutex

	// subNames 用于对子测试名称去重。
	// 每个键是子测试名称与父测试去重后名称的组合。
	// 每个值是该子测试名称已出现的次数。
	subNames map[string]int32
}

type filterMatch interface {
	// matches 使用给定的匹配函数，将名称与接收者的模式字符串进行匹配检查。
	matches(name []string, matchString func(pat, str string) (bool, error)) (ok, partial bool)

	// verify 通过调用给定的匹配函数，检查接收者的模式字符串是否为有效的过滤器。
	verify(name string, matchString func(pat, str string) (bool, error)) error
}

// simpleMatch 在所有模式字符串按顺序匹配时匹配测试名称。
type simpleMatch []string

// alternationMatch 在其中一个备选匹配时匹配测试名称。
type alternationMatch []filterMatch

// TODO: 修复 test_main 以避免竞态并改进缓存，同时允许消除此 Mutex。
var matchMutex sync.Mutex

func allMatcher() *matcher {
	return newMatcher(nil, "", "", "")
}

func newMatcher(matchString func(pat, str string) (bool, error), patterns, name, skips string) *matcher {
	var filter, skip filterMatch
	if patterns == "" {
		filter = simpleMatch{} // 始终 partial 为 true
	} else {
		filter = splitRegexp(patterns)
		if err := filter.verify(name, matchString); err != nil {
			fmt.Fprintf(os.Stderr, "testing: invalid regexp for %s\n", err)
			os.Exit(1)
		}
	}
	if skips == "" {
		skip = alternationMatch{} // 始终为 false
	} else {
		skip = splitRegexp(skips)
		if err := skip.verify("-test.skip", matchString); err != nil {
			fmt.Fprintf(os.Stderr, "testing: invalid regexp for %v\n", err)
			os.Exit(1)
		}
	}
	return &matcher{
		filter:    filter,
		skip:      skip,
		matchFunc: matchString,
		subNames:  map[string]int32{},
	}
}

func (m *matcher) fullName(c *common, subname string) (name string, ok, partial bool) {
	name = subname

	m.mu.Lock()
	defer m.mu.Unlock()

	if c != nil && c.level > 0 {
		name = m.unique(c.name, rewrite(subname))
	}

	matchMutex.Lock()
	defer matchMutex.Unlock()

	// 我们每次检查完整的路径数组，以处理模式中包含 '/' 的情况。
	elem := strings.Split(name, "/")

	// filter 必须匹配。
	// 接受可能稍后产生完全匹配的部分匹配。
	ok, partial = m.filter.matches(elem, m.matchFunc)
	if !ok {
		return name, false, false
	}

	// skip 必须不匹配。
	// 忽略部分匹配，以便稍后能获得更精确的匹配。
	skip, partialSkip := m.skip.matches(elem, m.matchFunc)
	if skip && !partialSkip {
		return name, false, false
	}

	return name, ok, partial
}

// clearSubNames 清除 matcher 的内部状态，可能会释放内存。
// 调用此方法后，T.Name 可能返回与之前子测试相同的字符串。
func (m *matcher) clearSubNames() {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.subNames)
}

func (m simpleMatch) matches(name []string, matchString func(pat, str string) (bool, error)) (ok, partial bool) {
	for i, s := range name {
		if i >= len(m) {
			break
		}
		if ok, _ := matchString(m[i], s); !ok {
			return false, false
		}
	}
	return true, len(name) < len(m)
}

func (m simpleMatch) verify(name string, matchString func(pat, str string) (bool, error)) error {
	for i, s := range m {
		m[i] = rewrite(s)
	}
	// 在进行任何处理之前先验证过滤器。
	for i, s := range m {
		if _, err := matchString(s, "non-empty"); err != nil {
			return fmt.Errorf("element %d of %s (%q): %s", i, name, s, err)
		}
	}
	return nil
}

func (m alternationMatch) matches(name []string, matchString func(pat, str string) (bool, error)) (ok, partial bool) {
	for _, m := range m {
		if ok, partial = m.matches(name, matchString); ok {
			return ok, partial
		}
	}
	return false, false
}

func (m alternationMatch) verify(name string, matchString func(pat, str string) (bool, error)) error {
	for i, m := range m {
		if err := m.verify(name, matchString); err != nil {
			return fmt.Errorf("alternation %d of %s", i, err)
		}
	}
	return nil
}

func splitRegexp(s string) filterMatch {
	a := make(simpleMatch, 0, strings.Count(s, "/"))
	b := make(alternationMatch, 0, strings.Count(s, "|"))
	cs := 0
	cp := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '[':
			cs++
		case ']':
			if cs--; cs < 0 { // 不匹配的 ']' 是合法的。
				cs = 0
			}
		case '(':
			if cs == 0 {
				cp++
			}
		case ')':
			if cs == 0 {
				cp--
			}
		case '\\':
			i++
		case '/':
			if cs == 0 && cp == 0 {
				a = append(a, s[:i])
				s = s[i+1:]
				i = 0
				continue
			}
		case '|':
			if cs == 0 && cp == 0 {
				a = append(a, s[:i])
				s = s[i+1:]
				i = 0
				b = append(b, a)
				a = make(simpleMatch, 0, len(a))
				continue
			}
		}
		i++
	}

	a = append(a, s)
	if len(b) == 0 {
		return a
	}
	return append(b, a)
}

// unique 通过（如有必要）附加一个或多个计数，为给定的父名称和子名称创建唯一名称。
func (m *matcher) unique(parent, subname string) string {
	base := parent + "/" + subname

	for {
		n := m.subNames[base]
		if n < 0 {
			panic("subtest count overflow")
		}
		m.subNames[base] = n + 1

		if n == 0 && subname != "" {
			prefix, nn := parseSubtestNumber(base)
			if len(prefix) < len(base) && nn < m.subNames[prefix] {
				// 此测试被显式命名为类似 "parent/subname#NN" 的形式，
				// 而 #NN 已被用于 "parent/subname" 的第 NN 次出现。
				// 循环以添加一个用于消歧的后缀。
				continue
			}
			return base
		}

		name := fmt.Sprintf("%s#%02d", base, n)
		if m.subNames[name] != 0 {
			// 这是 base 的第 n 次出现，但名称 "parent/subname#NN"
			// 与*显式*命名为 "parent/subname#NN" 的子测试的第一次出现冲突。
			// 尝试下一个数字。
			continue
		}

		return name
	}
}

// parseSubtestNumber 将子测试名称拆分为格式为 "#%02d" 的 int32 后缀（如果存在），
// 以及该后缀之前的（总是存在的）前缀。
func parseSubtestNumber(s string) (prefix string, nn int32) {
	i := strings.LastIndex(s, "#")
	if i < 0 {
		return s, 0
	}

	prefix, suffix := s[:i], s[i+1:]
	if len(suffix) < 2 || (len(suffix) > 2 && suffix[0] == '0') {
		// 即使 suffix 是数字，它也不可能是 "%02" 格式
		// 字符串的输出：它的位数太少或前导零太多。
		return s, 0
	}
	if suffix == "00" {
		if !strings.HasSuffix(prefix, "/") {
			// 我们仅将 "#00" 用作以空字符串命名的子测试的后缀 ——
			// 如果子测试名称非空，它就不是有效的后缀。
			return s, 0
		}
	}

	n, err := strconv.ParseInt(suffix, 10, 32)
	if err != nil || n < 0 {
		return s, 0
	}
	return prefix, int32(n)
}

// rewrite 将子名称重写为仅包含可打印字符且不含空白。
func rewrite(s string) string {
	b := []byte{}
	for _, r := range s {
		switch {
		case isSpace(r):
			b = append(b, '_')
		case !strconv.IsPrint(r):
			s := strconv.QuoteRune(r)
			b = append(b, s[1:len(s)-1]...)
		default:
			b = append(b, string(r)...)
		}
	}
	return string(b)
}

func isSpace(r rune) bool {
	if r < 0x2000 {
		switch r {
		// 注意：与 Unicode Z 类不同。
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0, 0x1680:
			return true
		}
	} else {
		if r <= 0x200a {
			return true
		}
		switch r {
		case 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
			return true
		}
	}
	return false
}
