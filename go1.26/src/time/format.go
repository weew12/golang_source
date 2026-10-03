// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	"errors"
	"internal/stringslite"
	_ "unsafe" // 用于 linkname
)

// 这些是预定义的布局，供 [Time.Format] 和 [time.Parse] 使用。
// 这些布局中使用的参考时间是如下特定的时间戳：
//
//	01/02 03:04:05PM '06 -0700
//
// （2006 年 1 月 2 日 15:04:05，位于 GMT 以西七小时的时区）。
// 该值记录为下面列出的名为 [Layout] 的常量。作为 Unix 时间，它是 1136239445。
// 由于 MST 是 GMT-0700，Unix date 命令打印该参考时间会显示为：
//
//	Mon Jan 2 15:04:05 MST 2006
//
// 日期采用了将数字月份放在日期之前的美国惯例，这是一个令人遗憾的历史错误。
//
// Time.Format 的示例详细演示了布局字符串的工作方式，是一个很好的参考。
//
// 注意 [RFC822]、[RFC850] 和 [RFC1123] 格式只应应用于本地时间。
// 将它们应用于 UTC 时间会使用 "UTC" 作为时区缩写，而严格来说这些 RFC
// 要求在这种情况下使用 "GMT"。
// 在解析时使用 [RFC1123] 或 [RFC1123Z] 格式时，请注意这些格式为月中的日期部分
// 定义了前导零，这并不被 RFC 1123 严格允许。这在解析某个月前 9 天出现的
// 日期字符串时会导致错误。
// 通常，对于坚持该格式的服务器，应使用 [RFC1123Z] 而不是 [RFC1123]，
// 而对于新协议应优先使用 [RFC3339]。
// [RFC3339]、[RFC822]、[RFC822Z]、[RFC1123] 和 [RFC1123Z] 在格式化时很有用；
// 当与 time.Parse 一起使用时，它们并不接受 RFC 允许的所有时间格式，
// 并且会接受未正式定义的时间格式。
// [RFC3339Nano] 格式会从秒字段中移除末尾的零，因此格式化后可能无法正确排序。
//
// 大多数程序可以使用已定义的常量之一作为传给 Format 或 Parse 的布局。
// 除非你要创建自定义布局字符串，否则可以忽略本注释的其余部分。
//
// 要定义自己的格式，请写下参考时间按你的方式格式化后看起来是什么样的；
// 有关示例，请参见 [ANSIC]、[StampMicro] 或 [Kitchen] 等常量的值。
// 这种模式旨在展示参考时间的样子，以便 Format 和 Parse 方法可以将相同的
// 变换应用于一般的时间值。
//
// 以下是布局字符串各组成部分的摘要。每个元素通过示例展示参考时间某个元素的
// 格式化方式。只有这些值会被识别。布局字符串中未被识别为参考时间一部分的文本
// 会在 Format 期间原样输出，并期望在 Parse 的输入中原样出现。
//
//	年: "2006" "06"
//	月: "Jan" "January" "01" "1"
//	星期: "Mon" "Monday"
//	月中的日: "2" "_2" "02"
//	年中的日: "__2" "002"
//	小时: "15" "3" "03"（下午或上午）
//	分钟: "4" "04"
//	秒: "5" "05"
//	AM/PM 标记: "PM"
//
// 数字时区偏移的格式如下：
//
//	"-0700"     ±hhmm
//	"-07:00"    ±hh:mm
//	"-07"       ±hh
//	"-070000"   ±hhmmss
//	"-07:00:00" ±hh:mm:ss
//
// 将格式中的符号替换为 Z 会触发 ISO 8601 行为，
// 即对于 UTC 时区打印 Z 而不是偏移量。因此：
//
//	"Z0700"      Z or ±hhmm
//	"Z07:00"     Z or ±hh:mm
//	"Z07"        Z or ±hh
//	"Z070000"    Z or ±hhmmss
//	"Z07:00:00"  Z or ±hh:mm:ss
//
// 在格式字符串中，"_2" 和 "__2" 中的下划线表示空格，如果后面的数字有多个
// 数字，这些空格可能会被数字替换，以兼容固定宽度的 Unix 时间格式。
// 前导零表示补零的值。
//
// __2 和 002 格式是空格填充和零填充的三字符年中的日；
// 没有不填充的年中的日格式。
//
// 逗号或小数点后跟一个或多个零表示小数秒，会打印到给定的小数位数。
// 逗号或小数点后跟一个或多个九表示小数秒，会打印到给定的小数位数并移除
// 末尾的零。
// 例如 "15:04:05,000" 或 "15:04:05.000" 以毫秒精度格式化或解析。
//
// 由于诸如用于空格填充的 _ 和用于时区信息的 Z 等格式，一些有效的布局
// 对于 time.Parse 是无效的时间值。
const (
	Layout      = "01/02 03:04:05PM '06 -0700" // 按数字顺序排列的参考时间。
	ANSIC       = "Mon Jan _2 15:04:05 2006"
	UnixDate    = "Mon Jan _2 15:04:05 MST 2006"
	RubyDate    = "Mon Jan 02 15:04:05 -0700 2006"
	RFC822      = "02 Jan 06 15:04 MST"
	RFC822Z     = "02 Jan 06 15:04 -0700" // 带数字时区的 RFC822
	RFC850      = "Monday, 02-Jan-06 15:04:05 MST"
	RFC1123     = "Mon, 02 Jan 2006 15:04:05 MST"
	RFC1123Z    = "Mon, 02 Jan 2006 15:04:05 -0700" // 带数字时区的 RFC1123
	RFC3339     = "2006-01-02T15:04:05Z07:00"
	RFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
	Kitchen     = "3:04PM"
	// 便捷时间戳。
	Stamp      = "Jan _2 15:04:05"
	StampMilli = "Jan _2 15:04:05.000"
	StampMicro = "Jan _2 15:04:05.000000"
	StampNano  = "Jan _2 15:04:05.000000000"
	DateTime   = "2006-01-02 15:04:05"
	DateOnly   = "2006-01-02"
	TimeOnly   = "15:04:05"
)

const (
	_                        = iota
	stdLongMonth             = iota + stdNeedDate  // "January"
	stdMonth                                       // "Jan"
	stdNumMonth                                    // "1"
	stdZeroMonth                                   // "01"
	stdLongWeekDay                                 // "Monday"
	stdWeekDay                                     // "Mon"
	stdDay                                         // "2"
	stdUnderDay                                    // "_2"
	stdZeroDay                                     // "02"
	stdUnderYearDay          = iota + stdNeedYday  // "__2"
	stdZeroYearDay                                 // "002"
	stdHour                  = iota + stdNeedClock // "15"
	stdHour12                                      // "3"
	stdZeroHour12                                  // "03"
	stdMinute                                      // "4"
	stdZeroMinute                                  // "04"
	stdSecond                                      // "5"
	stdZeroSecond                                  // "05"
	stdLongYear              = iota + stdNeedDate  // "2006"
	stdYear                                        // "06"
	stdPM                    = iota + stdNeedClock // "PM"
	stdpm                                          // "pm"
	stdTZ                    = iota                // "MST"
	stdISO8601TZ                                   // "Z0700"  // 对 UTC 打印 Z
	stdISO8601SecondsTZ                            // "Z070000"
	stdISO8601ShortTZ                              // "Z07"
	stdISO8601ColonTZ                              // "Z07:00" // 对 UTC 打印 Z
	stdISO8601ColonSecondsTZ                       // "Z07:00:00"
	stdNumTZ                                       // "-0700"  // 始终为数字
	stdNumSecondsTz                                // "-070000"
	stdNumShortTZ                                  // "-07"    // 始终为数字
	stdNumColonTZ                                  // "-07:00" // 始终为数字
	stdNumColonSecondsTZ                           // "-07:00:00"
	stdFracSecond0                                 // ".0", ".00", ... , 包含末尾的零
	stdFracSecond9                                 // ".9", ".99", ..., 省略末尾的零

	stdNeedDate       = 1 << 8             // 需要月、日、年
	stdNeedYday       = 1 << 9             // 需要 yday
	stdNeedClock      = 1 << 10            // 需要时、分、秒
	stdArgShift       = 16                 // 位于高位的额外参数，高于低位的 stdArgShift
	stdSeparatorShift = 28                 // 用于小数秒分隔符的高 4 位额外参数
	stdMask           = 1<<stdArgShift - 1 // 屏蔽掉参数
)

// std0x 记录 "01"、"02"、...、"06" 对应的 std 值。
var std0x = [...]int{stdZeroMonth, stdZeroDay, stdZeroHour12, stdZeroMinute, stdZeroSecond, stdYear}

// startsWithLowerCase 报告字符串开头是否为小写字母。
// 其目的是在查找 "Mon" 时避免匹配到 "Month" 之类的字符串。
func startsWithLowerCase(str string) bool {
	if len(str) == 0 {
		return false
	}
	c := str[0]
	return 'a' <= c && c <= 'z'
}

// nextStdChunk 在 layout 中查找 std 字符串的首次出现，
// 并返回其前面的文本、std 字符串以及其后的文本。
//
// nextStdChunk 本应是内部细节，
// 但许多广泛使用的包通过 linkname 访问它。
// 耻辱榜上的知名成员包括：
//   - github.com/searKing/golang/go
//
// 不要移除或更改类型签名。
// 参见 go.dev/issue/67401。
//
//go:linkname nextStdChunk
func nextStdChunk(layout string) (prefix string, std int, suffix string) {
	for i := 0; i < len(layout); i++ {
		switch c := int(layout[i]); c {
		case 'J': // January, Jan
			if len(layout) >= i+3 && layout[i:i+3] == "Jan" {
				if len(layout) >= i+7 && layout[i:i+7] == "January" {
					return layout[0:i], stdLongMonth, layout[i+7:]
				}
				if !startsWithLowerCase(layout[i+3:]) {
					return layout[0:i], stdMonth, layout[i+3:]
				}
			}

		case 'M': // Monday, Mon, MST
			if len(layout) >= i+3 {
				if layout[i:i+3] == "Mon" {
					if len(layout) >= i+6 && layout[i:i+6] == "Monday" {
						return layout[0:i], stdLongWeekDay, layout[i+6:]
					}
					if !startsWithLowerCase(layout[i+3:]) {
						return layout[0:i], stdWeekDay, layout[i+3:]
					}
				}
				if layout[i:i+3] == "MST" {
					return layout[0:i], stdTZ, layout[i+3:]
				}
			}

		case '0': // 01, 02, 03, 04, 05, 06, 002
			if len(layout) >= i+2 && '1' <= layout[i+1] && layout[i+1] <= '6' {
				return layout[0:i], std0x[layout[i+1]-'1'], layout[i+2:]
			}
			if len(layout) >= i+3 && layout[i+1] == '0' && layout[i+2] == '2' {
				return layout[0:i], stdZeroYearDay, layout[i+3:]
			}

		case '1': // 15, 1
			if len(layout) >= i+2 && layout[i+1] == '5' {
				return layout[0:i], stdHour, layout[i+2:]
			}
			return layout[0:i], stdNumMonth, layout[i+1:]

		case '2': // 2006, 2
			if len(layout) >= i+4 && layout[i:i+4] == "2006" {
				return layout[0:i], stdLongYear, layout[i+4:]
			}
			return layout[0:i], stdDay, layout[i+1:]

		case '_': // _2, _2006, __2
			if len(layout) >= i+2 && layout[i+1] == '2' {
				// _2006 实际上是一个字面量 _，后跟 stdLongYear
				if len(layout) >= i+5 && layout[i+1:i+5] == "2006" {
					return layout[0 : i+1], stdLongYear, layout[i+5:]
				}
				return layout[0:i], stdUnderDay, layout[i+2:]
			}
			if len(layout) >= i+3 && layout[i+1] == '_' && layout[i+2] == '2' {
				return layout[0:i], stdUnderYearDay, layout[i+3:]
			}

		case '3':
			return layout[0:i], stdHour12, layout[i+1:]

		case '4':
			return layout[0:i], stdMinute, layout[i+1:]

		case '5':
			return layout[0:i], stdSecond, layout[i+1:]

		case 'P': // PM
			if len(layout) >= i+2 && layout[i+1] == 'M' {
				return layout[0:i], stdPM, layout[i+2:]
			}

		case 'p': // pm
			if len(layout) >= i+2 && layout[i+1] == 'm' {
				return layout[0:i], stdpm, layout[i+2:]
			}

		case '-': // -070000, -07:00:00, -0700, -07:00, -07
			if len(layout) >= i+7 && layout[i:i+7] == "-070000" {
				return layout[0:i], stdNumSecondsTz, layout[i+7:]
			}
			if len(layout) >= i+9 && layout[i:i+9] == "-07:00:00" {
				return layout[0:i], stdNumColonSecondsTZ, layout[i+9:]
			}
			if len(layout) >= i+5 && layout[i:i+5] == "-0700" {
				return layout[0:i], stdNumTZ, layout[i+5:]
			}
			if len(layout) >= i+6 && layout[i:i+6] == "-07:00" {
				return layout[0:i], stdNumColonTZ, layout[i+6:]
			}
			if len(layout) >= i+3 && layout[i:i+3] == "-07" {
				return layout[0:i], stdNumShortTZ, layout[i+3:]
			}

		case 'Z': // Z070000, Z07:00:00, Z0700, Z07:00,
			if len(layout) >= i+7 && layout[i:i+7] == "Z070000" {
				return layout[0:i], stdISO8601SecondsTZ, layout[i+7:]
			}
			if len(layout) >= i+9 && layout[i:i+9] == "Z07:00:00" {
				return layout[0:i], stdISO8601ColonSecondsTZ, layout[i+9:]
			}
			if len(layout) >= i+5 && layout[i:i+5] == "Z0700" {
				return layout[0:i], stdISO8601TZ, layout[i+5:]
			}
			if len(layout) >= i+6 && layout[i:i+6] == "Z07:00" {
				return layout[0:i], stdISO8601ColonTZ, layout[i+6:]
			}
			if len(layout) >= i+3 && layout[i:i+3] == "Z07" {
				return layout[0:i], stdISO8601ShortTZ, layout[i+3:]
			}

		case '.', ',': // ,000、.000、,999 或 .999 - 小数秒的重复数字。
			if i+1 < len(layout) && (layout[i+1] == '0' || layout[i+1] == '9') {
				ch := layout[i+1]
				j := i + 1
				for j < len(layout) && layout[j] == ch {
					j++
				}
				// 数字串必须在此结束 - 只有小数秒是全数字的。
				if !isDigit(layout, j) {
					code := stdFracSecond0
					if layout[i+1] == '9' {
						code = stdFracSecond9
					}
					std := stdFracSecond(code, j-(i+1), c)
					return layout[0:i], std, layout[j:]
				}
			}
		}
	}
	return layout, 0, ""
}

var longDayNames = []string{
	"Sunday",
	"Monday",
	"Tuesday",
	"Wednesday",
	"Thursday",
	"Friday",
	"Saturday",
}

var shortDayNames = []string{
	"Sun",
	"Mon",
	"Tue",
	"Wed",
	"Thu",
	"Fri",
	"Sat",
}

var shortMonthNames = []string{
	"Jan",
	"Feb",
	"Mar",
	"Apr",
	"May",
	"Jun",
	"Jul",
	"Aug",
	"Sep",
	"Oct",
	"Nov",
	"Dec",
}

var longMonthNames = []string{
	"January",
	"February",
	"March",
	"April",
	"May",
	"June",
	"July",
	"August",
	"September",
	"October",
	"November",
	"December",
}

// match 报告 s1 和 s2 在忽略大小写的情况下是否匹配。
// 假定 s1 和 s2 长度相同。
func match(s1, s2 string) bool {
	for i := 0; i < len(s1); i++ {
		c1 := s1[i]
		c2 := s2[i]
		if c1 != c2 {
			// 切换到小写；已知 'a'-'A' 是单个位。
			c1 |= 'a' - 'A'
			c2 |= 'a' - 'A'
			if c1 != c2 || c1 < 'a' || c1 > 'z' {
				return false
			}
		}
	}
	return true
}

func lookup(tab []string, val string) (int, string, error) {
	for i, v := range tab {
		if len(val) >= len(v) && match(val[:len(v)], v) {
			return i, val[len(v):], nil
		}
	}
	return -1, val, errBad
}

// appendInt 将 x 的十进制形式追加到 b 并返回结果。
// 如果十进制形式（不含符号）短于 width，则结果用前导 0 填充。
// 与 strconv 中的功能重复，但避免了依赖。
func appendInt(b []byte, x int, width int) []byte {
	u := uint(x)
	if x < 0 {
		b = append(b, '-')
		u = uint(-x)
	}

	// 2 位和 4 位字段在时间格式中最常见。
	utod := func(u uint) byte { return '0' + byte(u) }
	switch {
	case width == 2 && u < 1e2:
		return append(b, utod(u/1e1), utod(u%1e1))
	case width == 4 && u < 1e4:
		return append(b, utod(u/1e3), utod(u/1e2%1e1), utod(u/1e1%1e1), utod(u%1e1))
	}

	// 计算十进制数字的位数。
	var n int
	if u == 0 {
		n = 1
	}
	for u2 := u; u2 > 0; u2 /= 10 {
		n++
	}

	// 添加 0 填充。
	for pad := width - n; pad > 0; pad-- {
		b = append(b, '0')
	}

	// 确保容量。
	if len(b)+n <= cap(b) {
		b = b[:len(b)+n]
	} else {
		b = append(b, make([]byte, n)...)
	}

	// 按相反顺序组装十进制。
	i := len(b) - 1
	for u >= 10 && i > 0 {
		q := u / 10
		b[i] = utod(u - q*10)
		u = q
		i--
	}
	b[i] = utod(u)
	return b
}

// 从不打印，只需非 nil 以供 atoi 返回。
var errAtoi = errors.New("time: invalid number")

// 与 strconv 中的功能重复，但避免了依赖。
func atoi[bytes []byte | string](s bytes) (x int, err error) {
	neg := false
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	q, rem, err := leadingInt(s)
	x = int(q)
	if err != nil || len(rem) > 0 {
		return 0, errAtoi
	}
	if neg {
		x = -x
	}
	return x, nil
}

// 传给 appendNano 的 "std" 值包含两个打包字段：小数点后的
// 位数以及分隔符字符（句点或逗号）。
// 这些函数对该变量进行打包和解包。
func stdFracSecond(code, n, c int) int {
	// 使用 0xfff 让失败情况显得更加荒谬。
	if c == '.' {
		return code | ((n & 0xfff) << stdArgShift)
	}
	return code | ((n & 0xfff) << stdArgShift) | 1<<stdSeparatorShift
}

func digitsLen(std int) int {
	return (std >> stdArgShift) & 0xfff
}

func separator(std int) byte {
	if (std >> stdSeparatorShift) == 0 {
		return '.'
	}
	return ','
}

// appendNano 将作为纳秒的小数秒追加到 b 并返回结果。
// nanosec 必须在 [0, 999999999] 范围内。
func appendNano(b []byte, nanosec int, std int) []byte {
	trim := std&stdMask == stdFracSecond9
	n := digitsLen(std)
	if trim && (n == 0 || nanosec == 0) {
		return b
	}
	dot := separator(std)
	b = append(b, dot)
	b = appendInt(b, nanosec, 9)
	if n < 9 {
		b = b[:len(b)-9+n]
	}
	if trim {
		for len(b) > 0 && b[len(b)-1] == '0' {
			b = b[:len(b)-1]
		}
		if len(b) > 0 && b[len(b)-1] == dot {
			b = b[:len(b)-1]
		}
	}
	return b
}

// String 返回按格式字符串格式化的时间
//
//	"2006-01-02 15:04:05.999999999 -0700 MST"
//
// 如果时间具有单调时钟读数，返回的字符串会包含一个最终字段 "m=±<value>"，
// 其中 value 是单调时钟读数，格式化为十进制秒数。
//
// 返回的字符串用于调试；对于稳定的序列化表示，
// 请使用 t.MarshalText、t.MarshalBinary，或带有显式格式字符串的 t.Format。
func (t Time) String() string {
	s := t.Format("2006-01-02 15:04:05.999999999 -0700 MST")

	// 将单调时钟读数格式化为 m=±ddd.nnnnnnnnn。
	if t.wall&hasMonotonic != 0 {
		m2 := uint64(t.ext)
		sign := byte('+')
		if t.ext < 0 {
			sign = '-'
			m2 = -m2
		}
		m1, m2 := m2/1e9, m2%1e9
		m0, m1 := m1/1e9, m1%1e9
		buf := make([]byte, 0, 24)
		buf = append(buf, " m="...)
		buf = append(buf, sign)
		wid := 0
		if m0 != 0 {
			buf = appendInt(buf, int(m0), 0)
			wid = 9
		}
		buf = appendInt(buf, int(m1), wid)
		buf = append(buf, '.')
		buf = appendInt(buf, int(m2), 9)
		s += string(buf)
	}
	return s
}

// GoString 实现了 [fmt.GoStringer]，并将 t 格式化为可在 Go 源代码中打印的形式。
func (t Time) GoString() string {
	abs := t.absSec()
	year, month, day := abs.days().date()
	hour, minute, second := abs.clock()

	buf := make([]byte, 0, len("time.Date(9999, time.September, 31, 23, 59, 59, 999999999, time.Local)"))
	buf = append(buf, "time.Date("...)
	buf = appendInt(buf, year, 0)
	if January <= month && month <= December {
		buf = append(buf, ", time."...)
		buf = append(buf, longMonthNames[month-1]...)
	} else {
		// 构造一个日期超出标准范围的 time.Time 很困难，
		// 但我们不妨尝试处理这种情况。
		buf = appendInt(buf, int(month), 0)
	}
	buf = append(buf, ", "...)
	buf = appendInt(buf, day, 0)
	buf = append(buf, ", "...)
	buf = appendInt(buf, hour, 0)
	buf = append(buf, ", "...)
	buf = appendInt(buf, minute, 0)
	buf = append(buf, ", "...)
	buf = appendInt(buf, second, 0)
	buf = append(buf, ", "...)
	buf = appendInt(buf, t.Nanosecond(), 0)
	buf = append(buf, ", "...)
	switch loc := t.Location(); loc {
	case UTC, nil:
		buf = append(buf, "time.UTC"...)
	case Local:
		buf = append(buf, "time.Local"...)
	default:
		// 有几种显示它的方案，但没有一种是好的：
		//
		// - 使用 Location(loc.name)，它在语法上并不完全有效
		// - 使用 LoadLocation(loc.name)，嵌入时会导致语法错误，
		// 而且还需要在不导入 fmt 或 strconv 的情况下对字符串进行转义
		// - 尝试使用 FixedZone，它同样需要对名称进行转义，
		// 并且会不准确地表示诸如 "America/Los_Angeles" 的夏令时偏移
		// - 使用指针格式，它并不比旧的 fmt.Sprintf("%#v", t) 格式更差。
		//
		// 其中，Location(loc.name) 破坏性最小。这是我们希望不要经常遇到的边界情况。
		buf = append(buf, `time.Location(`...)
		buf = append(buf, quote(loc.name)...)
		buf = append(buf, ')')
	}
	buf = append(buf, ')')
	return string(buf)
}

// Format 返回时间值的文本表示，按照参数定义的布局进行格式化。
// 有关如何表示布局格式，请参见名为 [Layout] 的常量的文档。
//
// [Time.Format] 的可执行示例详细演示了布局字符串的工作方式，是一个很好的参考。
func (t Time) Format(layout string) string {
	const bufSize = 64
	var b []byte
	max := len(layout) + 10
	if max < bufSize {
		var buf [bufSize]byte
		b = buf[:0]
	} else {
		b = make([]byte, 0, max)
	}
	b = t.AppendFormat(b, layout)
	return string(b)
}

// AppendFormat 类似于 [Time.Format]，但会将文本表示追加到 b
// 并返回扩展后的缓冲区。
func (t Time) AppendFormat(b []byte, layout string) []byte {
	// 针对 RFC3339 进行优化，因为它占所有表示形式的一半以上。
	switch layout {
	case RFC3339:
		return t.appendFormatRFC3339(b, false)
	case RFC3339Nano:
		return t.appendFormatRFC3339(b, true)
	default:
		return t.appendFormat(b, layout)
	}
}

func (t Time) appendFormat(b []byte, layout string) []byte {
	name, offset, abs := t.locabs()
	days := abs.days()

	var (
		year  int = -1
		month Month
		day   int
		yday  int = -1
		hour  int = -1
		min   int
		sec   int
	)

	// 每次迭代生成一个 std 值。
	for layout != "" {
		prefix, std, suffix := nextStdChunk(layout)
		if prefix != "" {
			b = append(b, prefix...)
		}
		if std == 0 {
			break
		}
		layout = suffix

		// 如需要，计算年、月、日。
		if year < 0 && std&stdNeedDate != 0 {
			year, month, day = days.date()
		}
		if yday < 0 && std&stdNeedYday != 0 {
			_, yday = days.yearYday()
		}

		// 如需要，计算时、分、秒。
		if hour < 0 && std&stdNeedClock != 0 {
			hour, min, sec = abs.clock()
		}

		switch std & stdMask {
		case stdYear:
			y := year
			if y < 0 {
				y = -y
			}
			b = appendInt(b, y%100, 2)
		case stdLongYear:
			b = appendInt(b, year, 4)
		case stdMonth:
			b = append(b, month.String()[:3]...)
		case stdLongMonth:
			m := month.String()
			b = append(b, m...)
		case stdNumMonth:
			b = appendInt(b, int(month), 0)
		case stdZeroMonth:
			b = appendInt(b, int(month), 2)
		case stdWeekDay:
			b = append(b, days.weekday().String()[:3]...)
		case stdLongWeekDay:
			s := days.weekday().String()
			b = append(b, s...)
		case stdDay:
			b = appendInt(b, day, 0)
		case stdUnderDay:
			if day < 10 {
				b = append(b, ' ')
			}
			b = appendInt(b, day, 0)
		case stdZeroDay:
			b = appendInt(b, day, 2)
		case stdUnderYearDay:
			if yday < 100 {
				b = append(b, ' ')
				if yday < 10 {
					b = append(b, ' ')
				}
			}
			b = appendInt(b, yday, 0)
		case stdZeroYearDay:
			b = appendInt(b, yday, 3)
		case stdHour:
			b = appendInt(b, hour, 2)
		case stdHour12:
			// 正午是 12PM，午夜是 12AM。
			hr := hour % 12
			if hr == 0 {
				hr = 12
			}
			b = appendInt(b, hr, 0)
		case stdZeroHour12:
			// 正午是 12PM，午夜是 12AM。
			hr := hour % 12
			if hr == 0 {
				hr = 12
			}
			b = appendInt(b, hr, 2)
		case stdMinute:
			b = appendInt(b, min, 0)
		case stdZeroMinute:
			b = appendInt(b, min, 2)
		case stdSecond:
			b = appendInt(b, sec, 0)
		case stdZeroSecond:
			b = appendInt(b, sec, 2)
		case stdPM:
			if hour >= 12 {
				b = append(b, "PM"...)
			} else {
				b = append(b, "AM"...)
			}
		case stdpm:
			if hour >= 12 {
				b = append(b, "pm"...)
			} else {
				b = append(b, "am"...)
			}
		case stdISO8601TZ, stdISO8601ColonTZ, stdISO8601SecondsTZ, stdISO8601ShortTZ, stdISO8601ColonSecondsTZ, stdNumTZ, stdNumColonTZ, stdNumSecondsTz, stdNumShortTZ, stdNumColonSecondsTZ:
			// 丑陋的特殊情况。我们取巧地将 "Z" 变体
			// 视为"按 ISO 8601 格式化的时区"。
			if offset == 0 && (std == stdISO8601TZ || std == stdISO8601ColonTZ || std == stdISO8601SecondsTZ || std == stdISO8601ShortTZ || std == stdISO8601ColonSecondsTZ) {
				b = append(b, 'Z')
				break
			}
			zone := offset / 60 // 转换为分钟
			absoffset := offset
			if zone < 0 {
				b = append(b, '-')
				zone = -zone
				absoffset = -absoffset
			} else {
				b = append(b, '+')
			}
			b = appendInt(b, zone/60, 2)
			if std == stdISO8601ColonTZ || std == stdNumColonTZ || std == stdISO8601ColonSecondsTZ || std == stdNumColonSecondsTZ {
				b = append(b, ':')
			}
			if std != stdNumShortTZ && std != stdISO8601ShortTZ {
				b = appendInt(b, zone%60, 2)
			}

			// 如果合适则追加秒
			if std == stdISO8601SecondsTZ || std == stdNumSecondsTz || std == stdNumColonSecondsTZ || std == stdISO8601ColonSecondsTZ {
				if std == stdNumColonSecondsTZ || std == stdISO8601ColonSecondsTZ {
					b = append(b, ':')
				}
				b = appendInt(b, absoffset%60, 2)
			}

		case stdTZ:
			if name != "" {
				b = append(b, name...)
				break
			}
			// 此时间没有已知的时区，但我们必须打印一个。
			// 使用 -0700 格式。
			zone := offset / 60 // 转换为分钟
			if zone < 0 {
				b = append(b, '-')
				zone = -zone
			} else {
				b = append(b, '+')
			}
			b = appendInt(b, zone/60, 2)
			b = appendInt(b, zone%60, 2)
		case stdFracSecond0, stdFracSecond9:
			b = appendNano(b, t.Nanosecond(), std)
		}
	}
	return b
}

var errBad = errors.New("bad value for field") // 占位符，不会传给用户

// ParseError 描述解析时间字符串时出现的问题。
type ParseError struct {
	Layout     string
	Value      string
	LayoutElem string
	ValueElem  string
	Message    string
}

// newParseError 创建一个新的 ParseError。
// 提供的 value 和 valueElem 会被克隆，以避免它们的值逃逸。
func newParseError(layout, value, layoutElem, valueElem, message string) *ParseError {
	valueCopy := stringslite.Clone(value)
	valueElemCopy := stringslite.Clone(valueElem)
	return &ParseError{layout, valueCopy, layoutElem, valueElemCopy, message}
}

// 这些取自 unicode/utf8 和 strconv 并复现那个包中的行为，
// 因为我们不能依赖其中任何一个。
const (
	lowerhex  = "0123456789abcdef"
	runeSelf  = 0x80
	runeError = '\uFFFD'
)

func quote(s string) string {
	buf := make([]byte, 1, len(s)+2) // slice 至少为 len(s) + 引号长度
	buf[0] = '"'
	for i, c := range s {
		if c >= runeSelf || c < ' ' {
			// 这意味着你要求我们解析一个包含不可打印或非 ASCII 字符的
			// time.Duration 或 time.Location。
			// 我们预计不常遇到这种情况。我们可以尝试完全忠实地
			// 复现 strconv.Quote 的行为，但鉴于我们预计很少遇到
			// 这些边界情况，速度和简洁性更重要。
			var width int
			if c == runeError {
				width = 1
				if i+2 < len(s) && s[i:i+3] == string(runeError) {
					width = 3
				}
			} else {
				width = len(string(c))
			}
			for j := 0; j < width; j++ {
				buf = append(buf, `\x`...)
				buf = append(buf, lowerhex[s[i+j]>>4])
				buf = append(buf, lowerhex[s[i+j]&0xF])
			}
		} else {
			if c == '"' || c == '\\' {
				buf = append(buf, '\\')
			}
			buf = append(buf, byte(c))
		}
	}
	buf = append(buf, '"')
	return string(buf)
}

// Error 返回 ParseError 的字符串表示。
func (e *ParseError) Error() string {
	if e.Message == "" {
		return "parsing time " +
			quote(e.Value) + " as " +
			quote(e.Layout) + ": cannot parse " +
			quote(e.ValueElem) + " as " +
			quote(e.LayoutElem)
	}
	return "parsing time " +
		quote(e.Value) + e.Message
}

// isDigit 报告 s[i] 是否在范围内且为十进制数字。
func isDigit[bytes []byte | string](s bytes, i int) bool {
	if len(s) <= i {
		return false
	}
	c := s[i]
	return '0' <= c && c <= '9'
}

// getnum 将 s[0:1] 或 s[0:2]（fixed 强制为 s[0:2]）解析为
// 十进制整数，并返回该整数和字符串的剩余部分。
func getnum(s string, fixed bool) (int, string, error) {
	if !isDigit(s, 0) {
		return 0, s, errBad
	}
	if !isDigit(s, 1) {
		if fixed {
			return 0, s, errBad
		}
		return int(s[0] - '0'), s[1:], nil
	}
	return int(s[0]-'0')*10 + int(s[1]-'0'), s[2:], nil
}

// getnum3 将 s[0:1]、s[0:2] 或 s[0:3]（fixed 强制为 s[0:3]）解析为
// 十进制整数，并返回该整数和字符串的剩余部分。
func getnum3(s string, fixed bool) (int, string, error) {
	var n, i int
	for i = 0; i < 3 && isDigit(s, i); i++ {
		n = n*10 + int(s[i]-'0')
	}
	if i == 0 || fixed && i != 3 {
		return 0, s, errBad
	}
	return n, s[i:], nil
}

func cutspace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s
}

// skip 从 value 中移除给定前缀，
// 将连续的空白字符视为等价。
func skip(value, prefix string) (string, error) {
	for len(prefix) > 0 {
		if prefix[0] == ' ' {
			if len(value) > 0 && value[0] != ' ' {
				return value, errBad
			}
			prefix = cutspace(prefix)
			value = cutspace(value)
			continue
		}
		if len(value) == 0 || value[0] != prefix[0] {
			return value, errBad
		}
		prefix = prefix[1:]
		value = value[1:]
	}
	return value, nil
}

// Parse 解析格式化的字符串并返回它所表示的时间值。
// 有关如何表示格式，请参见名为 [Layout] 的常量的文档。第二个参数必须能够
// 使用作为第一个参数提供的格式字符串（布局）来解析。
//
// [Time.Format] 的示例详细演示了布局字符串的工作方式，是一个很好的参考。
//
// 仅在解析时，输入可以在秒字段之后紧接着包含一个小数秒字段，
// 即使布局并未表明其存在。在这种情况下，逗号或小数点后跟最大一串数字
// 会被解析为小数秒。小数秒会被截断到纳秒精度。
//
// 布局中省略的元素被假定为零，或者在零不可能时假定为一，因此解析 "3:04pm"
// 返回对应 0 年 Jan 1 15:04:00 UTC 的时间（注意，由于年份为 0，该时间
// 早于零值 Time）。
// 年份必须在 0000..9999 范围内。星期几仅做语法检查，除此之外会被忽略。
//
// 对于指定两位数年份 06 的布局，值 NN >= 69 会被视为 19NN，NN < 69
// 会被视为 20NN。
//
// 本注释的其余部分描述时区的处理。
//
// 在没有时区指示符的情况下，Parse 返回 UTC 时间。
//
// 当解析带有诸如 -0700 的时区偏移的时间时，如果该偏移对应当前本地位置
// （[Local]）所使用的时区，则 Parse 会在返回的时间中使用该位置和时区。
// 否则，它会将该时间记录为处于一个虚构的位置，时间固定为给定的时区偏移。
//
// 当解析带有诸如 MST 的时区缩写的时间时，如果该时区缩写当前本地位置有
// 已定义的偏移，则使用该偏移。无论位置如何，时区缩写 "UTC" 都被识别为 UTC。
// 如果时区缩写未知，Parse 会将该时间记录为处于一个虚构的位置，
// 使用给定的时区缩写和零偏移。这种选择意味着这样的时间可以用相同的布局
// 无损地解析和重新格式化，但表示中使用的确切时刻会因实际时区偏移而不同。
// 为避免此类问题，请优先使用采用数字时区偏移的时间布局，或使用
// [ParseInLocation]。
func Parse(layout, value string) (Time, error) {
	// 针对 RFC3339 进行优化，因为它占所有表示形式的一半以上。
	if layout == RFC3339 || layout == RFC3339Nano {
		if t, ok := parseRFC3339(value, Local); ok {
			return t, nil
		}
	}
	return parse(layout, value, UTC, Local)
}

// ParseInLocation 类似于 Parse，但有两点重要区别。
// 第一，在没有时区信息的情况下，Parse 将时间解释为 UTC；
// ParseInLocation 将时间解释为处于给定位置。
// 第二，给定一个时区偏移或缩写时，Parse 尝试将其与 Local 位置匹配；
// ParseInLocation 则使用给定位置。
func ParseInLocation(layout, value string, loc *Location) (Time, error) {
	// 针对 RFC3339 进行优化，因为它占所有表示形式的一半以上。
	if layout == RFC3339 || layout == RFC3339Nano {
		if t, ok := parseRFC3339(value, loc); ok {
			return t, nil
		}
	}
	return parse(layout, value, loc, loc)
}

func parse(layout, value string, defaultLocation, local *Location) (Time, error) {
	alayout, avalue := layout, value
	rangeErrString := "" // 如果某个值超出范围则设置
	amSet := false       // 对于午夜，我们是否需要从小时中减去 12？
	pmSet := false       // 我们是否需要给小时加上 12？

	// 正在构造的时间。
	var (
		year       int
		month      int = -1
		day        int = -1
		yday       int = -1
		hour       int
		min        int
		sec        int
		nsec       int
		z          *Location
		zoneOffset int = -1
		zoneName   string
	)

	// 每次迭代处理一个 std 值。
	for {
		var err error
		prefix, std, suffix := nextStdChunk(layout)
		stdstr := layout[len(prefix) : len(layout)-len(suffix)]
		value, err = skip(value, prefix)
		if err != nil {
			return Time{}, newParseError(alayout, avalue, prefix, value, "")
		}
		if std == 0 {
			if len(value) != 0 {
				return Time{}, newParseError(alayout, avalue, "", value, ": extra text: "+quote(value))
			}
			break
		}
		layout = suffix
		var p string
		hold := value
		switch std & stdMask {
		case stdYear:
			if len(value) < 2 {
				err = errBad
				break
			}
			p, value = value[0:2], value[2:]
			year, err = atoi(p)
			if err != nil {
				break
			}
			if year >= 69 { // 在某些时区，Unix 时间始于 1969 年 12 月 31 日
				year += 1900
			} else {
				year += 2000
			}
		case stdLongYear:
			if len(value) < 4 || !isDigit(value, 0) {
				err = errBad
				break
			}
			p, value = value[0:4], value[4:]
			year, err = atoi(p)
		case stdMonth:
			month, value, err = lookup(shortMonthNames, value)
			month++
		case stdLongMonth:
			month, value, err = lookup(longMonthNames, value)
			month++
		case stdNumMonth, stdZeroMonth:
			month, value, err = getnum(value, std == stdZeroMonth)
			if err == nil && (month <= 0 || 12 < month) {
				rangeErrString = "month"
			}
		case stdWeekDay:
			// 除错误检查外忽略星期几。
			_, value, err = lookup(shortDayNames, value)
		case stdLongWeekDay:
			_, value, err = lookup(longDayNames, value)
		case stdDay, stdUnderDay, stdZeroDay:
			if std == stdUnderDay && len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			day, value, err = getnum(value, std == stdZeroDay)
			// 注意，这里我们允许任意一位或两位的日。
			// 月、日、年的组合在解析完成后进行验证。
		case stdUnderYearDay, stdZeroYearDay:
			for i := 0; i < 2; i++ {
				if std == stdUnderYearDay && len(value) > 0 && value[0] == ' ' {
					value = value[1:]
				}
			}
			yday, value, err = getnum3(value, std == stdZeroYearDay)
			// 注意，这里我们允许任意一位、两位或三位的年中的日。
			// 年中的日与年的组合在解析完成后进行验证。
		case stdHour:
			hour, value, err = getnum(value, false)
			if hour < 0 || 24 <= hour {
				rangeErrString = "hour"
			}
		case stdHour12, stdZeroHour12:
			hour, value, err = getnum(value, std == stdZeroHour12)
			if hour < 0 || 12 < hour {
				rangeErrString = "hour"
			}
		case stdMinute, stdZeroMinute:
			min, value, err = getnum(value, std == stdZeroMinute)
			if min < 0 || 60 <= min {
				rangeErrString = "minute"
			}
		case stdSecond, stdZeroSecond:
			sec, value, err = getnum(value, std == stdZeroSecond)
			if err != nil {
				break
			}
			if sec < 0 || 60 <= sec {
				rangeErrString = "second"
				break
			}
			// 特殊情况：我们有一个小数秒，但格式中没有
			// 小数秒吗？
			if len(value) >= 2 && commaOrPeriod(value[0]) && isDigit(value, 1) {
				_, std, _ = nextStdChunk(layout)
				std &= stdMask
				if std == stdFracSecond0 || std == stdFracSecond9 {
					// 布局中有小数秒；正常继续
					break
				}
				// 布局中没有小数秒，但输入中有一个。
				n := 2
				for ; n < len(value) && isDigit(value, n); n++ {
				}
				nsec, rangeErrString, err = parseNanoseconds(value, n)
				value = value[n:]
			}
		case stdPM:
			if len(value) < 2 {
				err = errBad
				break
			}
			p, value = value[0:2], value[2:]
			switch p {
			case "PM":
				pmSet = true
			case "AM":
				amSet = true
			default:
				err = errBad
			}
		case stdpm:
			if len(value) < 2 {
				err = errBad
				break
			}
			p, value = value[0:2], value[2:]
			switch p {
			case "pm":
				pmSet = true
			case "am":
				amSet = true
			default:
				err = errBad
			}
		case stdISO8601TZ, stdISO8601ShortTZ, stdISO8601ColonTZ, stdISO8601SecondsTZ, stdISO8601ColonSecondsTZ:
			if len(value) >= 1 && value[0] == 'Z' {
				value = value[1:]
				z = UTC
				break
			}
			fallthrough
		case stdNumTZ, stdNumShortTZ, stdNumColonTZ, stdNumSecondsTz, stdNumColonSecondsTZ:
			var sign, hour, min, seconds string
			if std == stdISO8601ColonTZ || std == stdNumColonTZ {
				if len(value) < 6 {
					err = errBad
					break
				}
				if value[3] != ':' {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], value[4:6], "00", value[6:]
			} else if std == stdNumShortTZ || std == stdISO8601ShortTZ {
				if len(value) < 3 {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], "00", "00", value[3:]
			} else if std == stdISO8601ColonSecondsTZ || std == stdNumColonSecondsTZ {
				if len(value) < 9 {
					err = errBad
					break
				}
				if value[3] != ':' || value[6] != ':' {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], value[4:6], value[7:9], value[9:]
			} else if std == stdISO8601SecondsTZ || std == stdNumSecondsTz {
				if len(value) < 7 {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], value[3:5], value[5:7], value[7:]
			} else {
				if len(value) < 5 {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], value[3:5], "00", value[5:]
			}
			var hr, mm, ss int
			hr, _, err = getnum(hour, true)
			if err == nil {
				mm, _, err = getnum(min, true)
				if err == nil {
					ss, _, err = getnum(seconds, true)
				}
			}

			// 范围测试使用 > 而不是 >=，
			// 因为有些人确实会写 24 小时
			// 或 60 分钟或 60 秒的偏移。
			if hr > 24 {
				rangeErrString = "time zone offset hour"
			}
			if mm > 60 {
				rangeErrString = "time zone offset minute"
			}
			if ss > 60 {
				rangeErrString = "time zone offset second"
			}

			zoneOffset = (hr*60+mm)*60 + ss // 偏移量以秒为单位
			switch sign[0] {
			case '+':
			case '-':
				zoneOffset = -zoneOffset
			default:
				err = errBad
			}
		case stdTZ:
			// 它看起来像一个时区吗？
			if len(value) >= 3 && value[0:3] == "UTC" {
				z = UTC
				value = value[3:]
				break
			}
			n, ok := parseTimeZone(value)
			if !ok {
				err = errBad
				break
			}
			zoneName, value = value[:n], value[n:]

		case stdFracSecond0:
			// stdFracSecond0 要求数字位数与布局中指定的完全相同。
			ndigit := 1 + digitsLen(std)
			if len(value) < ndigit {
				err = errBad
				break
			}
			nsec, rangeErrString, err = parseNanoseconds(value, ndigit)
			value = value[ndigit:]

		case stdFracSecond9:
			if len(value) < 2 || !commaOrPeriod(value[0]) || value[1] < '0' || '9' < value[1] {
				// 省略小数秒。
				break
			}
			// 接受任意数量的数字，甚至多于要求的数量，
			// 因为 stdSecond 分支就是这么做的。
			i := 0
			for i+1 < len(value) && '0' <= value[i+1] && value[i+1] <= '9' {
				i++
			}
			nsec, rangeErrString, err = parseNanoseconds(value, 1+i)
			value = value[1+i:]
		}
		if rangeErrString != "" {
			return Time{}, newParseError(alayout, avalue, stdstr, value, ": "+rangeErrString+" out of range")
		}
		if err != nil {
			return Time{}, newParseError(alayout, avalue, stdstr, hold, "")
		}
	}
	if pmSet && hour < 12 {
		hour += 12
	} else if amSet && hour == 12 {
		hour = 0
	}

	// 将 yday 转换为日、月。
	if yday >= 0 {
		var d int
		var m int
		if isLeap(year) {
			if yday == 31+29 {
				m = int(February)
				d = 29
			} else if yday > 31+29 {
				yday--
			}
		}
		if yday < 1 || yday > 365 {
			return Time{}, newParseError(alayout, avalue, "", value, ": day-of-year out of range")
		}
		if m == 0 {
			m = (yday-1)/31 + 1
			if daysBefore(Month(m+1)) < yday {
				m++
			}
			d = yday - daysBefore(Month(m))
		}
		// 如果已经见过月、日，那么 yday 的 m、d 必须匹配。
		// 否则，根据 m、d 设置它们。
		if month >= 0 && month != m {
			return Time{}, newParseError(alayout, avalue, "", value, ": day-of-year does not match month")
		}
		month = m
		if day >= 0 && day != d {
			return Time{}, newParseError(alayout, avalue, "", value, ": day-of-year does not match day")
		}
		day = d
	} else {
		if month < 0 {
			month = int(January)
		}
		if day < 0 {
			day = 1
		}
	}

	// 验证月中的日。
	if day < 1 || day > daysIn(Month(month), year) {
		return Time{}, newParseError(alayout, avalue, "", value, ": day out of range")
	}

	if z != nil {
		return Date(year, Month(month), day, hour, min, sec, nsec, z), nil
	}

	if zoneOffset != -1 {
		t := Date(year, Month(month), day, hour, min, sec, nsec, UTC)
		t.addSec(-int64(zoneOffset))

		// 查找具有给定偏移的本地时区。
		// 如果该时区在给定时间生效，则使用它。
		name, offset, _, _, _ := local.lookup(t.unixSec())
		if offset == zoneOffset && (zoneName == "" || name == zoneName) {
			t.setLoc(local)
			return t, nil
		}

		// 否则创建一个伪造的时区来记录偏移。
		zoneNameCopy := stringslite.Clone(zoneName) // 避免泄漏输入值
		t.setLoc(FixedZone(zoneNameCopy, zoneOffset))
		return t, nil
	}

	if zoneName != "" {
		t := Date(year, Month(month), day, hour, min, sec, nsec, UTC)
		// 查找具有给定偏移的本地时区。
		// 如果该时区在给定时间生效，则使用它。
		offset, ok := local.lookupName(zoneName, t.unixSec())
		if ok {
			t.addSec(-int64(offset))
			t.setLoc(local)
			return t, nil
		}

		// 否则，创建一个偏移未知的伪造时区。
		if len(zoneName) > 3 && zoneName[:3] == "GMT" {
			offset, _ = atoi(zoneName[3:]) // 由 parseGMT 保证正确。
			offset *= 3600
		}
		zoneNameCopy := stringslite.Clone(zoneName) // 避免泄漏输入值
		t.setLoc(FixedZone(zoneNameCopy, offset))
		return t, nil
	}

	// 否则，回退到默认值。
	return Date(year, Month(month), day, hour, min, sec, nsec, defaultLocation), nil
}

// parseTimeZone 解析时区字符串并返回其长度。时区是人为生成的且不可预测。
// 我们无法进行精确的错误检查。
// 另一方面，对于正确的解析，字符串开头必须有一个时区，
// 因此几乎总是可以确定那里有一个时区。我们查看字符串开头的一串大写字母。
// 如果超过 5 个，则为错误。
// 如果有 4 或 5 个且最后一个是 T，则它是时区。
// 如果有 3 个，则它是时区。
// 否则，除特殊情况外，它不是时区。
// GMT 很特殊，因为它可以带小时偏移。
func parseTimeZone(value string) (length int, ok bool) {
	if len(value) < 3 {
		return 0, false
	}
	// 特殊情况 1：ChST 和 MeST 是仅有的带小写字母的时区。
	if len(value) >= 4 && (value[:4] == "ChST" || value[:4] == "MeST") {
		return 4, true
	}
	// 特殊情况 2：GMT 可能带小时偏移；特殊处理它。
	if value[:3] == "GMT" {
		length = parseGMT(value)
		return length, true
	}
	// 特殊情况 3：有些时区没有名称，但具有 +/-00 格式
	if value[0] == '+' || value[0] == '-' {
		length = parseSignedOffset(value)
		ok := length > 0 // 如果输入错误，parseSignedOffset 返回 0
		return length, ok
	}
	// 有多少个大写字母？至少需要三个，最多五个。
	var nUpper int
	for nUpper = 0; nUpper < 6; nUpper++ {
		if nUpper >= len(value) {
			break
		}
		if c := value[nUpper]; c < 'A' || 'Z' < c {
			break
		}
	}
	switch nUpper {
	case 0, 1, 2, 6:
		return 0, false
	case 5: // 必须以 T 结尾才能匹配。
		if value[4] == 'T' {
			return 5, true
		}
	case 4:
		// 必须以 T 结尾，只有一个特殊情况例外。
		if value[3] == 'T' || value[:4] == "WITA" {
			return 4, true
		}
	case 3:
		return 3, true
	}
	return 0, false
}

// parseGMT 解析 GMT 时区。已知输入字符串以 "GMT" 开头。
// 该函数检查其后是否跟有一个符号以及一个在 -23 到 +23 范围内
// （不包括零）的数字。
func parseGMT(value string) int {
	value = value[3:]
	if len(value) == 0 {
		return 3
	}

	return 3 + parseSignedOffset(value)
}

// parseSignedOffset 解析带符号的时区偏移（例如 "+03" 或 "-04"）。
// 该函数检查一个在 -23 到 +23 范围内（不包括零）的带符号数字。
// 返回找到的偏移字符串的长度，否则返回 0。
func parseSignedOffset(value string) int {
	sign := value[0]
	if sign != '-' && sign != '+' {
		return 0
	}
	x, rem, err := leadingInt(value[1:])

	// 如果 leadingInt 没有消耗任何内容则失败
	if err != nil || value[1:] == rem {
		return 0
	}
	if x > 23 {
		return 0
	}
	return len(value) - len(rem)
}

func commaOrPeriod(b byte) bool {
	return b == '.' || b == ','
}

func parseNanoseconds[bytes []byte | string](value bytes, nbytes int) (ns int, rangeErrString string, err error) {
	if !commaOrPeriod(value[0]) {
		err = errBad
		return
	}
	if nbytes > 10 {
		value = value[:10]
		nbytes = 10
	}
	if ns, err = atoi(value[1:nbytes]); err != nil {
		return
	}
	if ns < 0 {
		rangeErrString = "fractional second"
		return
	}
	// 我们需要纳秒，这意味着按格式中缺失的数字位数
	// 进行缩放，最大长度为 10。
	scaleDigits := 10 - nbytes
	for i := 0; i < scaleDigits; i++ {
		ns *= 10
	}
	return
}

var errLeadingInt = errors.New("time: bad [0-9]*") // 从不打印

// leadingInt 从 s 中消耗开头的 [0-9]*。
func leadingInt[bytes []byte | string](s bytes) (x uint64, rem bytes, err error) {
	i := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		if x > 1<<63/10 {
			// 溢出
			return 0, rem, errLeadingInt
		}
		x = x*10 + uint64(c) - '0'
		if x > 1<<63 {
			// 溢出
			return 0, rem, errLeadingInt
		}
	}
	return x, s[i:], nil
}

// leadingFraction 从 s 中消耗开头的 [0-9]*。
// 它仅用于小数部分，因此溢出时不会返回错误，
// 只是停止累积精度。
func leadingFraction(s string) (x uint64, scale float64, rem string) {
	i := 0
	scale = 1
	overflow := false
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		if overflow {
			continue
		}
		if x > (1<<63-1)/10 {
			// 溢出有可能得到正数，所以要小心。
			overflow = true
			continue
		}
		y := x*10 + uint64(c) - '0'
		if y > 1<<63 {
			overflow = true
			continue
		}
		x = y
		scale *= 10
	}
	return x, scale, s[i:]
}

// parseDurationError 描述解析持续时长字符串时出现的问题。
type parseDurationError struct {
	message string
	value   string
}

func (e *parseDurationError) Error() string {
	return "time: " + e.message + " " + quote(e.value)
}

var unitMap = map[string]uint64{
	"ns": uint64(Nanosecond),
	"us": uint64(Microsecond),
	"µs": uint64(Microsecond), // U+00B5 = 微符号
	"μs": uint64(Microsecond), // U+03BC = 希腊字母 mu
	"ms": uint64(Millisecond),
	"s":  uint64(Second),
	"m":  uint64(Minute),
	"h":  uint64(Hour),
}

// ParseDuration 解析持续时长字符串。
// 持续时长字符串是一个可能带符号的十进制数字序列，
// 每个数字带有可选的小数部分和单位后缀，
// 例如 "300ms"、"-1.5h" 或 "2h45m"。
// 有效的时间单位有 "ns"、"us"（或 "µs"）、"ms"、"s"、"m"、"h"。
func ParseDuration(s string) (Duration, error) {
	// [-+]?([0-9]*(\.[0-9]*)?[a-z]+)+
	orig := s
	var d uint64
	neg := false

	// 消耗 [-+]?
	if s != "" {
		c := s[0]
		if c == '-' || c == '+' {
			neg = c == '-'
			s = s[1:]
		}
	}
	// 特殊情况：如果剩下的只有 "0"，则为零。
	if s == "0" {
		return 0, nil
	}
	if s == "" {
		return 0, &parseDurationError{"invalid duration", orig}
	}
	for s != "" {
		var (
			v, f  uint64      // 小数点前、后的整数
			scale float64 = 1 // value = v + f/scale
		)

		var err error

		// 下一个字符必须是 [0-9.]
		if !(s[0] == '.' || '0' <= s[0] && s[0] <= '9') {
			return 0, &parseDurationError{"invalid duration", orig}
		}
		// 消耗 [0-9]*
		pl := len(s)
		v, s, err = leadingInt(s)
		if err != nil {
			return 0, &parseDurationError{"invalid duration", orig}
		}
		pre := pl != len(s) // 我们是否在句点之前消耗了任何内容

		// 消耗 (\.[0-9]*)?
		post := false
		if s != "" && s[0] == '.' {
			s = s[1:]
			pl := len(s)
			f, scale, s = leadingFraction(s)
			post = pl != len(s)
		}
		if !pre && !post {
			// 没有数字（例如 ".s" 或 "-.s"）
			return 0, &parseDurationError{"invalid duration", orig}
		}

		// 消耗单位。
		i := 0
		for ; i < len(s); i++ {
			c := s[i]
			if c == '.' || '0' <= c && c <= '9' {
				break
			}
		}
		if i == 0 {
			return 0, &parseDurationError{"missing unit in duration", orig}
		}
		u := s[:i]
		s = s[i:]
		unit, ok := unitMap[u]
		if !ok {
			return 0, &parseDurationError{"unknown unit " + quote(u) + " in duration", orig}
		}
		if v > 1<<63/unit {
			// 溢出
			return 0, &parseDurationError{"invalid duration", orig}
		}
		v *= unit
		if f > 0 {
			// 对于小时的小数部分，需要 float64 才能达到纳秒精度。
			// v >= 0 && (f*unit/scale) <= 3.6e+12（ns/h，h 是最大的单位）
			v += uint64(float64(f) * (float64(unit) / scale))
			if v > 1<<63 {
				// 溢出
				return 0, &parseDurationError{"invalid duration", orig}
			}
		}
		d += v
		if d > 1<<63 {
			return 0, &parseDurationError{"invalid duration", orig}
		}
	}
	if neg {
		return -Duration(d), nil
	}
	if d > 1<<63-1 {
		return 0, &parseDurationError{"invalid duration", orig}
	}
	return Duration(d), nil
}
