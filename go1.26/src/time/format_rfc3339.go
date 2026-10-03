// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import "errors"

// RFC 3339 是最常用的格式。
//
// Time.(Marshal|Unmarshal)(Text|JSON) 方法隐式使用它。
// 此外，根据 https://go.dev/issue/52746 上的分析，
// RFC 3339 占所有显式指定时间格式的 57%，
// 而第二流行的格式仅占 8%。
// 与其他所有格式相比，RFC 3339 的压倒性使用证明了
// 添加优化格式化和解析的逻辑是合理的。

func (t Time) appendFormatRFC3339(b []byte, nanos bool) []byte {
	_, offset, abs := t.locabs()

	// 格式化日期。
	year, month, day := abs.days().date()
	b = appendInt(b, year, 4)
	b = append(b, '-')
	b = appendInt(b, int(month), 2)
	b = append(b, '-')
	b = appendInt(b, day, 2)

	b = append(b, 'T')

	// 格式化时间。
	hour, min, sec := abs.clock()
	b = appendInt(b, hour, 2)
	b = append(b, ':')
	b = appendInt(b, min, 2)
	b = append(b, ':')
	b = appendInt(b, sec, 2)

	if nanos {
		std := stdFracSecond(stdFracSecond9, 9, '.')
		b = appendNano(b, t.Nanosecond(), std)
	}

	if offset == 0 {
		return append(b, 'Z')
	}

	// 格式化时区。
	zone := offset / 60 // 转换为分钟
	if zone < 0 {
		b = append(b, '-')
		zone = -zone
	} else {
		b = append(b, '+')
	}
	b = appendInt(b, zone/60, 2)
	b = append(b, ':')
	b = appendInt(b, zone%60, 2)
	return b
}

func (t Time) appendStrictRFC3339(b []byte) ([]byte, error) {
	n0 := len(b)
	b = t.appendFormatRFC3339(b, true)

	// 并非所有有效的 Go 时间戳都能序列化为有效的 RFC 3339。
	// 显式检查这些边界情况。
	// 参见 https://go.dev/issue/4556 和 https://go.dev/issue/54580。
	num2 := func(b []byte) byte { return 10*(b[0]-'0') + (b[1] - '0') }
	switch {
	case b[n0+len("9999")] != '-': // 年份必须正好 4 位宽
		return b, errors.New("year outside of range [0,9999]")
	case b[len(b)-1] != 'Z':
		c := b[len(b)-len("Z07:00")]
		if ('0' <= c && c <= '9') || num2(b[len(b)-len("07:00"):]) >= 24 {
			return b, errors.New("timezone hour outside of range [0,23]")
		}
	}
	return b, nil
}

func parseRFC3339[bytes []byte | string](s bytes, local *Location) (Time, bool) {
	// parseUint 将 s 解析为无符号十进制整数，并
	// 验证其在某个范围内。
	// 如果无效或超出范围，
	// 它会将 ok 设为 false 并返回 min 值。
	ok := true
	parseUint := func(s bytes, min, max int) (x int) {
		for _, c := range []byte(s) {
			if c < '0' || '9' < c {
				ok = false
				return min
			}
			x = x*10 + int(c) - '0'
		}
		if x < min || max < x {
			ok = false
			return min
		}
		return x
	}

	// 解析日期和时间。
	if len(s) < len("2006-01-02T15:04:05") {
		return Time{}, false
	}
	year := parseUint(s[0:4], 0, 9999)                       // 例如 2006
	month := parseUint(s[5:7], 1, 12)                        // 例如 01
	day := parseUint(s[8:10], 1, daysIn(Month(month), year)) // 例如 02
	hour := parseUint(s[11:13], 0, 23)                       // 例如 15
	min := parseUint(s[14:16], 0, 59)                        // 例如 04
	sec := parseUint(s[17:19], 0, 59)                        // 例如 05
	if !ok || !(s[4] == '-' && s[7] == '-' && s[10] == 'T' && s[13] == ':' && s[16] == ':') {
		return Time{}, false
	}
	s = s[19:]

	// 解析小数秒。
	var nsec int
	if len(s) >= 2 && s[0] == '.' && isDigit(s, 1) {
		n := 2
		for ; n < len(s) && isDigit(s, n); n++ {
		}
		nsec, _, _ = parseNanoseconds(s, n)
		s = s[n:]
	}

	// 解析时区。
	t := Date(year, Month(month), day, hour, min, sec, nsec, UTC)
	if len(s) != 1 || s[0] != 'Z' {
		if len(s) != len("-07:00") {
			return Time{}, false
		}
		hr := parseUint(s[1:3], 0, 23) // 例如 07
		mm := parseUint(s[4:6], 0, 59) // 例如 00
		if !ok || !((s[0] == '-' || s[0] == '+') && s[3] == ':') {
			return Time{}, false
		}
		zoneOffset := (hr*60 + mm) * 60
		if s[0] == '-' {
			zoneOffset *= -1
		}
		t.addSec(-int64(zoneOffset))

		// 如果可能，使用具有给定偏移的本地时区。
		if _, offset, _, _, _ := local.lookup(t.unixSec()); offset == zoneOffset {
			t.setLoc(local)
		} else {
			t.setLoc(FixedZone("", zoneOffset))
		}
	}
	return t, true
}

func parseStrictRFC3339(b []byte) (Time, error) {
	t, ok := parseRFC3339(b, Local)
	if !ok {
		t, err := Parse(RFC3339, string(b))
		if err != nil {
			return Time{}, err
		}

		// 解析模板语法无法正确验证 RFC 3339。
		// 显式检查 Parse 无法验证的情况。
		// 参见 https://go.dev/issue/54580。
		num2 := func(b []byte) byte { return 10*(b[0]-'0') + (b[1] - '0') }
		switch {
		// TODO(https://go.dev/issue/54580): 目前禁用了严格解析。
		// 通过 GODEBUG 选择退出后可以再次启用。
		case true:
			return t, nil
		case b[len("2006-01-02T")+1] == ':': // 小时必须是两位数字
			return Time{}, &ParseError{RFC3339, string(b), "15", string(b[len("2006-01-02T"):][:1]), ""}
		case b[len("2006-01-02T15:04:05")] == ',': // 亚秒分隔符必须是句点
			return Time{}, &ParseError{RFC3339, string(b), ".", ",", ""}
		case b[len(b)-1] != 'Z':
			switch {
			case num2(b[len(b)-len("07:00"):]) >= 24: // 时区小时必须在范围内
				return Time{}, &ParseError{RFC3339, string(b), "Z07:00", string(b[len(b)-len("Z07:00"):]), ": timezone hour out of range"}
			case num2(b[len(b)-len("00"):]) >= 60: // 时区分钟必须在范围内
				return Time{}, &ParseError{RFC3339, string(b), "Z07:00", string(b[len(b)-len("Z07:00"):]), ": timezone minute out of range"}
			}
		default: // 未知错误；不应发生
			return Time{}, &ParseError{RFC3339, string(b), RFC3339, string(b), ""}
		}
	}
	return t, nil
}
