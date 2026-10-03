// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package time 提供用于测量和显示时间的功能。
//
// 日历计算始终假定使用公历（Gregorian calendar），且不含闰秒。
//
// # 单调时钟（Monotonic Clocks）
//
// 操作系统同时提供“挂钟（wall clock）”和“单调时钟（monotonic clock）”，
// 前者会因时钟同步而改变，后者则不会。一般规则是：挂钟用于告知时间，
// 单调时钟用于测量时间。为了不拆分 API，本包中由 [time.Now] 返回的 Time
// 同时包含挂钟读数和单调时钟读数；后续的告知时间操作使用挂钟读数，而后续的
// 测量时间操作，特别是比较和减法，使用单调时钟读数。
//
// 例如，下面的代码总是计算出一个约为 20 毫秒的正的已流逝时间，
// 即使在被计时的操作期间挂钟被修改也是如此：
//
//	start := time.Now()
//	... 一个耗时 20 毫秒的操作 ...
//	t := time.Now()
//	elapsed := t.Sub(start)
//
// 其他惯用法，例如 [time.Since](start)、[time.Until](deadline) 以及
// time.Now().Before(deadline)，同样能够抵御挂钟被重置的影响。
//
// 本节其余部分给出操作如何使用单调时钟的精确细节，但理解这些细节
// 并非使用本包所必需。
//
// time.Now 返回的 Time 包含一个单调时钟读数。
// 如果 Time t 具有单调时钟读数，t.Add 会将同一时长同时加到挂钟读数和
// 单调时钟读数上以计算结果。由于 t.AddDate(y, m, d)、t.Round(d) 和
// t.Truncate(d) 是挂钟时间运算，它们总是从结果中去除任何单调时钟读数。
// 由于 t.In、t.Local 和 t.UTC 用于影响挂钟时间的解释方式，它们也会从
// 结果中去除任何单调时钟读数。去除单调时钟读数的规范做法是使用 t = t.Round(0)。
//
// 如果 Time t 和 u 都包含单调时钟读数，那么运算 t.After(u)、t.Before(u)、
// t.Equal(u)、t.Compare(u) 和 t.Sub(u) 仅使用单调时钟读数进行，忽略挂钟
// 读数。如果 t 或 u 中的任一个不含单调时钟读数，这些运算则退回到使用
// 挂钟读数。
//
// 在某些系统上，如果计算机进入睡眠，单调时钟会停止。在此类系统上，
// t.Sub(u) 可能无法准确反映 t 与 u 之间实际流逝的时间。其他对时间进行
// 减法的函数和方法同样如此，例如 [Since]、[Until]、[Time.Before]、[Time.After]、
// [Time.Add]、[Time.Equal] 和 [Time.Compare]。在某些情况下，你可能需要去除
// 单调时钟以获得准确结果。
//
// 由于单调时钟读数在当前进程之外没有意义，由 t.GobEncode、
// t.MarshalBinary、t.MarshalJSON 和 t.MarshalText 生成的序列化形式会省略
// 单调时钟读数，而 t.Format 也不为其提供格式。类似地，构造器 [time.Date]、
// [time.Parse]、[time.ParseInLocation] 和 [time.Unix]，以及反序列化方法
// t.GobDecode、t.UnmarshalBinary、t.UnmarshalJSON 和 t.UnmarshalText 总是
// 创建不含单调时钟读数的时间。
//
// 单调时钟读数只存在于 [Time] 值中。它不是 [Duration] 值的一部分，
// 也不是 t.Unix 及其相关方法返回的 Unix 时间的一部分。
//
// 注意，Go 的 == 运算符不仅比较时间瞬间，还比较 [Location] 和单调时钟
// 读数。关于 Time 值相等性测试的讨论，请参阅 Time 类型的文档。
//
// 为了调试，如果存在单调时钟读数，t.String 的结果会包含该读数。
// 如果 t != u 是由于单调时钟读数不同，那么打印 t.String() 和 u.String()
// 时该差异将可见。
//
// # 定时器精度（Timer Resolution）
//
// [Timer] 的精度取决于 Go 运行时、操作系统和底层硬件。
// 在 Unix 上，精度约为 1 毫秒。
// 在 Windows 1803 及更高版本上，精度约为 0.5 毫秒。
// 在较旧的 Windows 版本上，默认精度约为 16 毫秒，但可以使用
// [golang.org/x/sys/windows.TimeBeginPeriod] 请求更高的精度。
package time

import (
	"errors"
	"math/bits"
	_ "unsafe" // 用于 go:linkname
)

// Time 表示一个具有纳秒精度的时间瞬间。
//
// 使用时间的程序通常应以值而非指针的形式存储和传递它们。也就是说，
// 时间变量和结构体字段的类型应为 [time.Time]，而不是 *time.Time。
//
// 除方法 [Time.GobDecode]、[Time.UnmarshalBinary]、[Time.UnmarshalJSON] 和
// [Time.UnmarshalText] 不是并发安全的之外，Time 值可被多个 goroutine 同时使用。
//
// 时间瞬间可以使用 [Time.Before]、[Time.After] 和 [Time.Equal] 方法进行比较。
// [Time.Sub] 方法对两个瞬间求差，产生一个 [Duration]。
// [Time.Add] 方法将一个 Time 与一个 Duration 相加，产生一个 Time。
//
// Time 类型的零值是 1 年 1 月 1 日 00:00:00.000000000 UTC。
// 由于该时间在实践中最不可能出现，[Time.IsZero] 方法提供了一种简单的
// 方式来检测尚未被显式初始化的时间。
//
// 每个时间都有一个关联的 [Location]。方法 [Time.Local]、[Time.UTC] 和 Time.In
// 返回一个带有特定 Location 的 Time。使用这些方法改变 Time 值的 Location
// 不会改变它所表示的实际瞬间，只会改变解释它所使用的时区。
//
// 由 [Time.GobEncode]、[Time.MarshalBinary]、[Time.AppendBinary]、
// [Time.MarshalJSON]、[Time.MarshalText] 和 [Time.AppendText] 方法保存的
// Time 值的表示形式会存储 [Time.Location] 的偏移量，但不存储位置名称。
// 因此它们会丢失有关夏令时（Daylight Saving Time）的信息。
//
// 除了必需的“挂钟”读数外，Time 还可以包含当前进程单调时钟的可选读数，
// 以便在比较或减法时提供额外的精度。
// 详情请参阅包文档中的“单调时钟”部分。
//
// 注意，Go 的 == 运算符不仅比较时间瞬间，还比较 Location 和单调时钟读数。
// 因此，在未先保证所有值都设置了相同 Location（可通过使用 UTC 或 Local
// 方法实现）且通过设置 t = t.Round(0) 去除了单调时钟读数之前，Time 值
// 不应被用作 map 或数据库的键。一般而言，应优先使用 t.Equal(u) 而非
// t == u，因为 t.Equal 使用可用的最精确比较，并且能正确处理只有一个
// 参数具有单调时钟读数的情况。
type Time struct {
	// wall 和 ext 编码挂钟秒数、挂钟纳秒数，
	// 以及可选的以纳秒为单位的单调时钟读数。
	//
	// 从高位到低位，wall 编码一个 1 位标志（hasMonotonic）、
	// 一个 33 位秒字段和一个 30 位挂钟纳秒字段。
	// 纳秒字段的范围是 [0, 999999999]。
	// 如果 hasMonotonic 位为 0，则 33 位字段必须为零，
	// 自 1 年 1 月 1 日以来的完整带符号 64 位挂钟秒数存储在 ext 中。
	// 如果 hasMonotonic 位为 1，则 33 位字段保存自 1885 年 1 月 1 日以来的
	// 33 位无符号挂钟秒数，而 ext 保存一个带符号 64 位单调时钟读数，
	// 即自进程启动以来的纳秒数。
	wall uint64
	ext  int64

	// loc 指定应用于确定与此 Time 对应的
	// 分钟、小时、月、日和年的 Location。
	// nil 位置表示 UTC。
	// 所有 UTC 时间都以 loc==nil 表示，绝不会以 loc==&utcLoc 表示。
	loc *Location
}

const (
	hasMonotonic = 1 << 63
	maxWall      = wallToInternal + (1<<33 - 1) // 2157 年
	minWall      = wallToInternal               // 1885 年
	nsecMask     = 1<<30 - 1
	nsecShift    = 30
)

// 这些用于操作挂钟和单调时钟读数的辅助方法采用指针接收者，
// 即使它们并不修改时间，这样做是为了使调用开销更低。

// nsec 返回时间的纳秒数。
func (t *Time) nsec() int32 {
	return int32(t.wall & nsecMask)
}

// sec 返回自 1 年 1 月 1 日以来时间的秒数。
func (t *Time) sec() int64 {
	if t.wall&hasMonotonic != 0 {
		return wallToInternal + int64(t.wall<<1>>(nsecShift+1))
	}
	return t.ext
}

// unixSec 返回自 1970 年 1 月 1 日以来时间的秒数（Unix 时间）。
func (t *Time) unixSec() int64 { return t.sec() + internalToUnix }

// addSec 向时间添加 d 秒。
func (t *Time) addSec(d int64) {
	if t.wall&hasMonotonic != 0 {
		sec := int64(t.wall << 1 >> (nsecShift + 1))
		dsec := sec + d
		if 0 <= dsec && dsec <= 1<<33-1 {
			t.wall = t.wall&nsecMask | uint64(dsec)<<nsecShift | hasMonotonic
			return
		}
		// 挂钟秒数现在超出打包字段的范围。
		// 移到 ext。
		t.stripMono()
	}

	// 检查 t.ext 与 d 之和是否溢出并正确处理。
	sum := t.ext + d
	if (sum > t.ext) == (d > 0) {
		t.ext = sum
	} else if d > 0 {
		t.ext = 1<<63 - 1
	} else {
		t.ext = -(1<<63 - 1)
	}
}

// setLoc 设置与时间关联的位置。
func (t *Time) setLoc(loc *Location) {
	if loc == &utcLoc {
		loc = nil
	}
	t.stripMono()
	t.loc = loc
}

// stripMono 去除 t 中的单调时钟读数。
func (t *Time) stripMono() {
	if t.wall&hasMonotonic != 0 {
		t.ext = t.sec()
		t.wall &= nsecMask
	}
}

// setMono 设置 t 中的单调时钟读数。
// 如果 t 无法保存单调时钟读数，
// 因为其挂钟时间太大，
// 则 setMono 是空操作。
func (t *Time) setMono(m int64) {
	if t.wall&hasMonotonic == 0 {
		sec := t.ext
		if sec < minWall || maxWall < sec {
			return
		}
		t.wall |= hasMonotonic | uint64(sec-minWall)<<nsecShift
	}
	t.ext = m
}

// mono 返回 t 的单调时钟读数。
// 若读数缺失，则返回 0。
// 此函数仅用于测试，
// 因此严格来说 0 也是一个有效的
// 单调时钟读数也没关系。
func (t *Time) mono() int64 {
	if t.wall&hasMonotonic == 0 {
		return 0
	}
	return t.ext
}

// IsZero 报告 t 是否表示零时间瞬间，
// 即 1 年 1 月 1 日 00:00:00 UTC。
func (t Time) IsZero() bool {
	// 如果在 t.wall 中设置了 hasMonotonic，那么该时间不可能早于 1885 年，因此不可能是 1 年。
	// 如果 hasMonotonic 为零，那么 wall 中除纳秒字段外的所有位都应为 0。
	// 因此若没有纳秒则 t.wall == 0，若没有秒则 t.ext == 0。
	// 这等价于 t.sec() == 0 && t.nsec() == 0，但更高效。
	return t.wall == 0 && t.ext == 0
}

// After 报告时间瞬间 t 是否在 u 之后。
func (t Time) After(u Time) bool {
	if t.wall&u.wall&hasMonotonic != 0 {
		return t.ext > u.ext
	}
	ts := t.sec()
	us := u.sec()
	return ts > us || ts == us && t.nsec() > u.nsec()
}

// Before 报告时间瞬间 t 是否在 u 之前。
func (t Time) Before(u Time) bool {
	if t.wall&u.wall&hasMonotonic != 0 {
		return t.ext < u.ext
	}
	ts := t.sec()
	us := u.sec()
	return ts < us || ts == us && t.nsec() < u.nsec()
}

// Compare 将时间瞬间 t 与 u 进行比较。如果 t 在 u 之前，返回 -1；
// 如果 t 在 u 之后，返回 +1；如果二者相同，返回 0。
func (t Time) Compare(u Time) int {
	var tc, uc int64
	if t.wall&u.wall&hasMonotonic != 0 {
		tc, uc = t.ext, u.ext
	} else {
		tc, uc = t.sec(), u.sec()
		if tc == uc {
			tc, uc = int64(t.nsec()), int64(u.nsec())
		}
	}
	switch {
	case tc < uc:
		return -1
	case tc > uc:
		return +1
	}
	return 0
}

// Equal 报告 t 和 u 是否表示同一时间瞬间。
// 即使两个时间位于不同位置，它们也可能相等。
// 例如，6:00 +0200 和 4:00 UTC 是 Equal 的。
// 关于对 Time 值使用 == 的陷阱，请参阅 Time 类型的文档；
// 大多数代码应改用 Equal。
func (t Time) Equal(u Time) bool {
	if t.wall&u.wall&hasMonotonic != 0 {
		return t.ext == u.ext
	}
	return t.sec() == u.sec() && t.nsec() == u.nsec()
}

// Month 指定一年中的月份（一月 = 1，……）。
type Month int

const (
	January Month = 1 + iota
	February
	March
	April
	May
	June
	July
	August
	September
	October
	November
	December
)

// String 返回月份的英文名称（"January"、"February"、……）。
func (m Month) String() string {
	if January <= m && m <= December {
		return longMonthNames[m-1]
	}
	buf := make([]byte, 20)
	n := fmtInt(buf, uint64(m))
	return "%!Month(" + string(buf[n:]) + ")"
}

// Weekday 指定一周中的某天（星期日 = 0，……）。
type Weekday int

const (
	Sunday Weekday = iota
	Monday
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
)

// String 返回星期的英文名称（"Sunday"、"Monday"、……）。
func (d Weekday) String() string {
	if Sunday <= d && d <= Saturday {
		return longDayNames[d]
	}
	buf := make([]byte, 20)
	n := fmtInt(buf, uint64(d))
	return "%!Weekday(" + string(buf[n:]) + ")"
}

// 关于时间的计算
//
// Time 的零值被定义为
//	1 年 1 月 1 日 00:00:00.000000000 UTC
// 它（1）看起来像一个零，或者说是日期中你能得到的最接近零的值
// （1-1-1 00:00:00 UTC），（2）在实践中极不可能出现，因而适合作为
// “未设置”哨兵值，不像 1970 年 1 月 1 日，并且（3）即使在 UTC 以西的
// 时区中，其年份也非负，不像 1-1-0 00:00:00 UTC，后者在纽约会是
// 12-31-(-1) 19:00:00。
//
// 零 Time 值并不强制时间表示使用某个特定的纪元。例如，若要在内部使用
// Unix 纪元，我们可以定义：为了区分零值与 1970 年 1 月 1 日，该时间
// 将用 sec=-1、nsec=1e9 表示。然而，它确实暗示了一种表示方式，即以
// 1-1-1 00:00:00 UTC 作为纪元，而我们正是这样做的。
//
// Add 和 Sub 计算不关心纪元的选择。
//
// 展示类计算——年、月、分钟等等——都严重依赖于对正常量进行除法和取模。
// 对于日历计算，我们希望这些除法向下取整，即使对负值也是如此，这样余数
// 始终为正，但 Go 的除法（像大多数硬件除法指令一样）是向零取整的。我们
// 仍然可以进行这些计算，然后针对负的被除数调整结果，但一遍又一遍地写这种
// 调整很烦人。取而代之，我们可以改为使用一个足够久远的、使我们所关心的
// 所有时间都为正的纪元，这样向零取整与向下取整就一致了。这些展示例程
// 本来就必须加上时区偏移，因此再加上到备用纪元的平移开销很小。例如，
// 有一个非负时间 t 意味着我们可以写
//
//	sec = t % 60
//
// 而不是
//
//	sec = t % 60
//	if sec < 0 {
//		sec += 60
//	}
//
// 处处如此。
//
// 日历以精确的 400 年为周期运行：为 1970-2369 年打印的 400 年日历同样
// 适用于 2370-2769 年。甚至星期几也吻合。选择一个周期边界，使得那些
// 例外年份总是尽可能地被推迟，可以简化日期计算：0 年 3 月 1 日就是这样
// 一天：第一个闰日（2 月 29 日）在四年减一天之后，第一个没有 2 月 29 日
// 的 4 的倍数年在 100 年减一天之后，而第一个有 2 月 29 日的 100 的倍数年
// 在 400 年减一天之后。对于任何 Y = 0 mod 400，Y 年的 3 月 1 日也是
// 这样一天。
//
// 最后，如果 Unix 纪元与久远纪元之间的差值可以用一个 int64 常量表示，
// 那会很方便。
//
// 这三项考虑——选择一个尽可能早的纪元、它始于 0 mod 400 年的 3 月 1 日、
// 并且不比 1970 年早超过 2⁶³ 秒——将我们带到 -292277022400 年。
// 我们称这一时刻为绝对零瞬间，并将以 uint64 秒为单位、自此年起的
// 度量称为绝对时间。
//
// 以 int64 秒为单位、自 1 年起的度量——即 Time 的 sec 字段所使用的表示
// 形式——称为内部时间。
//
// 以 int64 秒为单位、自 1970 年起的度量称为 Unix 时间。
//
// 人们很容易想直接使用 1 年作为绝对纪元，并定义这些例程仅对年份 >= 1
// 有效。然而，那样的话，在 UTC 以西的时区中显示该纪元时例程就会失效，
// 因为那是 0 年。要说正确打印零时间在一半的时区中不受支持，这似乎站不住脚。
// 相比之下，错误处理 -292277022400 年中的某些时间则是合理的。
//
// 所有这些对 API 的调用者都是不透明的，如果出现更好的实现，可以更改。
//
// 日期计算使用了 Cassio Neri 和 Lorenz Schneider 的以下巧妙数学，
// “Euclidean affine functions and their application to calendar algorithms,”
// SP&E 2023. https://doi.org/10.1002/spe.3172
//
// 将“日历除法”（f, f°, f*）定义为一个由三个函数组成的组合，它把
// 一个时间单位转换为整数个更大的单位及余数，再转换回来。
// 例如，在没有闰年的日历中，(d/365, d%365, y*365) 就是将天数转换为
// 年的日历除法：
//
//	(f)  year := days/365
//	(f°) yday := days%365
//	(f*) days := year*365 (+ yday)
//
// 注意 f* 通常是那个“容易”写的函数：它是反转更复杂除法的日历乘法。
//
// Neri 和 Schneider 证明，当 f* 取如下形式
//
//	f*(n) = (a n + b) / c
//
// 使用向下取整的整数除法且 a ≥ c > 0，
// 他们称之为欧几里得仿射函数（Euclidean affine function，EAF），此时：
//
//	f(n) = (c n + c - b - 1) / a
//	f°(n) = (c n + c - b - 1) % a / c
//
// 这为任何能够将日历乘法写成 EAF 形式的日历除法给出了相当直接的计算。
// 由于纪元已被移到 3 月 1 日，所有日历乘法都可以写成 EAF 形式。
// 当把一个日期拆分为 [century, cyear, amonth, mday] 时，
// 其中 century、cyear 和 mday 以 0 为基，
// 而 amonth 以 3 为基（三月 = 3，……，一月 = 13，二月 = 14），
// 写成 EAF 形式的日历乘法为：
//
//	yday = (153 (amonth-3) + 2) / 5 = (153 amonth - 457) / 5
//	cday = 365 cyear + cyear/4 = 1461 cyear / 4
//	centurydays = 36524 century + century/4 = 146097 century / 4
//	days = centurydays + cday + yday + mday.
//
// 每个等式我们只能处理一个周期循环，因此年份计算必须拆分为
// [century, cyear]，以同时处理 100 年周期和 400 年周期。
//
// yday 的计算并不显而易见，但它源于这样一个事实：三月到一月的日历
// 重复着 5 个月 153 天的周期 31、30、31、30、31（我们不关心二月，
// 因为 yday 只计算 2 月 1 日 _之前_ 的天数，二月是最后一个月）。
//
// 使用从 f* 推导 f 和 f° 的规则，这些乘法转换为如下除法：
//
//	century := (4 days + 3) / 146097
//	cdays := (4 days + 3) % 146097 / 4
//	cyear := (4 cdays + 3) / 1461
//	ayday := (4 cdays + 3) % 1461 / 4
//	amonth := (5 ayday + 461) / 153
//	mday := (5 ayday + 461) % 153 / 5
//
// ayday 和 amonth 中的 a 表示绝对（以 3 月 1 日为基），
// 以区别于标准的 yday（以 1 月 1 日为基）。
//
// 计算完这些之后，我们可以在假定存在从 bool 到 int 0 或 1 的无分支转换
// （此处记作 int(b)）的前提下，用无分支数学将 3 月 1 日日历转换为标准的
// 1 月 1 日日历：
//
//	isJanFeb := int(yday >= marchThruDecember)
//	month := amonth - isJanFeb*12
//	year := century*100 + cyear + isJanFeb
//	isLeap := int(cyear%4 == 0) & (int(cyear != 0) | int(century%4 == 0))
//	day := 1 + mday
//	yday := 1 + ayday + 31 + 28 + isLeap&^isJanFeb - 365*isJanFeb
//
// isLeap 是标准的闰年规则，但拆分年份的形式使所有除法都简化为二进制
// 掩码运算。注意 day 和 yday 以 1 为基，与 mday 和 ayday 相反。

// 为了将各种单位区分开，我们为每种单位定义了整数类型。
// 这些类型从不存储在接口中，也不会被分配，
// 因此它们的类型信息不会出现在 Go 二进制文件中。
const (
	secondsPerMinute = 60
	secondsPerHour   = 60 * secondsPerMinute
	secondsPerDay    = 24 * secondsPerHour
	secondsPerWeek   = 7 * secondsPerDay
	daysPer400Years  = 365*400 + 97

	// 从 3 月 1 日到年末的天数
	marchThruDecember = 31 + 30 + 31 + 30 + 31 + 31 + 30 + 31 + 30 + 31

	// absoluteYears 是我们从内部时间中减去以得到绝对时间的年数。
	// 该值必须是 0 mod 400，它定义了上文“关于时间的计算”注释中提到的
	// “绝对零瞬间”：-absoluteYears 年 3 月 1 日。
	// 绝对纪元之前的日期将无法正确计算，
	// 除此之外该值可按需更改。
	absoluteYears = 292277022400

	// 零 Time 的年份。
	// 下面的 unixToInternal 计算假定如此。
	internalYear = 1

	// 用于在内部时间与绝对时间或 Unix 时间之间转换的偏移量。
	absoluteToInternal int64 = -(absoluteYears*365.2425 + marchThruDecember) * secondsPerDay
	internalToAbsolute       = -absoluteToInternal

	unixToInternal int64 = (1969*365 + 1969/4 - 1969/100 + 1969/400) * secondsPerDay
	internalToUnix int64 = -unixToInternal

	absoluteToUnix = absoluteToInternal + internalToUnix
	unixToAbsolute = unixToInternal + internalToAbsolute

	wallToInternal int64 = (1884*365 + 1884/4 - 1884/100 + 1884/400) * secondsPerDay
)

// absSeconds 计数自绝对零瞬间以来的秒数。
type absSeconds uint64

// absDays 计数自绝对零瞬间以来的天数。
type absDays uint64

// absCentury 计数自绝对零瞬间以来的世纪数。
type absCentury uint64

// absCyear 计数自一个世纪开始以来的年数。
type absCyear int

// absYday 计数自一年开始以来的天数。
// 注意绝对年份始于 3 月 1 日。
type absYday int

// absMonth 计数自一年开始以来的月数。
// absMonth=0 表示三月。
type absMonth int

// absLeap 是一个单个位（0 或 1），表示给定年份是否为闰年。
type absLeap int

// absJanFeb 是一个单个位（0 或 1），表示给定日期是否落在一月或二月。
// 这是一个特殊情况，因为绝对年份始于三月（不同于普通的日历年）。
type absJanFeb int

// dateToAbsDays 接收一个标准的年/月/日，并返回从绝对纪元到该日的
// 天数。
// days 参数可以超出范围，特别是可以为负数。
func dateToAbsDays(year int64, month Month, day int) absDays {
	// 参见上文“关于时间的计算”注释。
	amonth := uint32(month)
	janFeb := uint32(0)
	if amonth < 3 {
		janFeb = 1
	}
	amonth += 12 * janFeb
	y := uint64(year) - uint64(janFeb) + absoluteYears

	// 对于范围为 [3,14] 的 amonth，我们希望：
	//
	//	ayday := (153*amonth - 457) / 5
	//
	// （参见上文“关于时间的计算”注释，
	// 以及 Neri 和 Schneider，第 7 节。）
	//
	// 这等价于：
	//
	//	ayday := (979*amonth - 2919) >> 5
	//
	// 而后一种形式少用几条指令，
	// 所以使用它，可节省几个周期。
	// 关于此优化的更多内容，参见 Neri 和 Schneider，第 8.3 节。
	//
	// （注意并没有省去除法，因为编译器在所有情况下
	// 都无需除法即可实现 / 5。）
	ayday := (979*amonth - 2919) >> 5

	century := y / 100
	cyear := uint32(y % 100)
	cday := 1461 * cyear / 4
	centurydays := 146097 * century / 4

	return absDays(centurydays + uint64(int64(cday+ayday)+int64(day)-1))
}

// days 将绝对秒数转换为绝对天数。
func (abs absSeconds) days() absDays {
	return absDays(abs / secondsPerDay)
}

// split 把 days 拆分为 century、cyear、ayday。
func (days absDays) split() (century absCentury, cyear absCyear, ayday absYday) {
	// 参见上文“关于时间的计算”注释。
	d := 4*uint64(days) + 3
	century = absCentury(d / 146097)

	// 这应当是
	//	cday := uint32(d % 146097) / 4
	//	cd := 4*cday + 3
	// 也就是
	//	cday := uint32(d % 146097) >> 2
	//	cd := cday<<2 + 3
	// 但当然 (x>>2<<2)+3 == x|3，
	// 所以改为这样做。
	cd := uint32(d%146097) | 3

	// 对于范围为 [0,146097] 的 cdays（100 年），我们希望：
	//
	//	cyear := (4 cdays + 3) / 1461
	//	yday := (4 cdays + 3) % 1461 / 4
	//
	// （参见上文“关于时间的计算”注释，
	// 以及 Neri 和 Schneider，第 7 节。）
	//
	// 这等价于：
	//
	//	cyear := (2939745 cdays) >> 32
	//	yday := (2939745 cdays) & 0xFFFFFFFF / 2939745 / 4
	//
	// 所以改为这样做，可节省几个周期。
	// 关于此优化的更多内容，参见 Neri 和 Schneider，第 8.3 节。
	hi, lo := bits.Mul32(2939745, cd)
	cyear = absCyear(hi)
	ayday = absYday(lo / 2939745 / 4)
	return
}

// split 把 ayday 拆分为绝对月份和标准的（以 1 为基的）月内日。
func (ayday absYday) split() (m absMonth, mday int) {
	// 参见上文“关于时间的计算”注释。
	//
	// 对于范围为 [0,366] 的 yday，
	//
	//	amonth := (5 yday + 461) / 153
	//	mday := (5 yday + 461) % 153 / 5
	//
	// 等价于：
	//
	//	amonth = (2141 yday + 197913) >> 16
	//	mday = (2141 yday + 197913) & 0xFFFF / 2141
	//
	// 所以改为这样做，可节省几个周期。
	// 参见 Neri 和 Schneider，第 8.3 节。
	d := 2141*uint32(ayday) + 197913
	return absMonth(d >> 16), 1 + int((d&0xFFFF)/2141)
}

// janFeb 在以 3 月 1 日为基的 ayday 落在一月或二月时返回 1，否则返回 0。
func (ayday absYday) janFeb() absJanFeb {
	// 参见上文“关于时间的计算”注释。
	jf := absJanFeb(0)
	if ayday >= marchThruDecember {
		jf = 1
	}
	return jf
}

// month 返回 (m, janFeb) 对应的标准 Month
func (m absMonth) month(janFeb absJanFeb) Month {
	// 参见上文“关于时间的计算”注释。
	return Month(m) - Month(janFeb)*12
}

// leap 在 (century, cyear) 是闰年时返回 1，否则返回 0。
func (century absCentury) leap(cyear absCyear) absLeap {
	// 参见上文“关于时间的计算”注释。
	y4ok := 0
	if cyear%4 == 0 {
		y4ok = 1
	}
	y100ok := 0
	if cyear != 0 {
		y100ok = 1
	}
	y400ok := 0
	if century%4 == 0 {
		y400ok = 1
	}
	return absLeap(y4ok & (y100ok | y400ok))
}

// year 返回 (century, cyear, janFeb) 对应的标准年份。
func (century absCentury) year(cyear absCyear, janFeb absJanFeb) int {
	// 参见上文“关于时间的计算”注释。
	return int(uint64(century)*100-absoluteYears) + int(cyear) + int(janFeb)
}

// yday 返回 (ayday, janFeb, leap) 对应的标准（以 1 为基的）yday。
func (ayday absYday) yday(janFeb absJanFeb, leap absLeap) int {
	// 参见上文“关于时间的计算”注释。
	return int(ayday) + (1 + 31 + 28) + int(leap)&^int(janFeb) - 365*int(janFeb)
}

// date 将天数转换为标准的年、月、日。
func (days absDays) date() (year int, month Month, day int) {
	century, cyear, ayday := days.split()
	amonth, day := ayday.split()
	janFeb := ayday.janFeb()
	year = century.year(cyear, janFeb)
	month = amonth.month(janFeb)
	return
}

// yearYday 将天数转换为标准年份和以 1 为基的 yday。
func (days absDays) yearYday() (year, yday int) {
	century, cyear, ayday := days.split()
	janFeb := ayday.janFeb()
	year = century.year(cyear, janFeb)
	yday = ayday.yday(janFeb, century.leap(cyear))
	return
}

// absSec 返回时间 t 的绝对秒数，并按分区偏移量进行调整。
// 它在计算诸如 Month 或 Hour 之类的展示属性时被调用。
// 我们更愿意把它命名为 abs，但存在指向 abs 的 linkname，这会使那样做有问题。
// 参见下面的 timeAbs。
func (t Time) absSec() absSeconds {
	l := t.loc
	// 尽可能避免函数调用。
	if l == nil || l == &localLoc {
		l = l.get()
	}
	sec := t.unixSec()
	if l != &utcLoc {
		if l.cacheZone != nil && l.cacheStart <= sec && sec < l.cacheEnd {
			sec += int64(l.cacheZone.offset)
		} else {
			_, offset, _, _, _ := l.lookup(sec)
			sec += int64(offset)
		}
	}
	return absSeconds(sec + (unixToInternal + internalToAbsolute))
}

// locabs 是 Zone 与 abs 方法的结合，
// 通过一次分区查找同时提取两个返回值。
func (t Time) locabs() (name string, offset int, abs absSeconds) {
	l := t.loc
	if l == nil || l == &localLoc {
		l = l.get()
	}
	// 如果命中本地时间缓存，则避免函数调用。
	sec := t.unixSec()
	if l != &utcLoc {
		if l.cacheZone != nil && l.cacheStart <= sec && sec < l.cacheEnd {
			name = l.cacheZone.name
			offset = l.cacheZone.offset
		} else {
			name, offset, _, _, _ = l.lookup(sec)
		}
		sec += int64(offset)
	} else {
		name = "UTC"
	}
	abs = absSeconds(sec + (unixToInternal + internalToAbsolute))
	return
}

// Date 返回 t 所在的年、月、日。
func (t Time) Date() (year int, month Month, day int) {
	return t.absSec().days().date()
}

// Year 返回 t 所在的年份。
func (t Time) Year() int {
	century, cyear, ayday := t.absSec().days().split()
	janFeb := ayday.janFeb()
	return century.year(cyear, janFeb)
}

// Month 返回 t 指定的一年中的月份。
func (t Time) Month() Month {
	_, _, ayday := t.absSec().days().split()
	amonth, _ := ayday.split()
	return amonth.month(ayday.janFeb())
}

// Day 返回 t 指定的月内日。
func (t Time) Day() int {
	_, _, ayday := t.absSec().days().split()
	_, day := ayday.split()
	return day
}

// Weekday 返回 t 指定的星期几。
func (t Time) Weekday() Weekday {
	return t.absSec().days().weekday()
}

// weekday 返回 days 指定的星期几。
func (days absDays) weekday() Weekday {
	// 绝对年份的 3 月 1 日，就像 2000 年的 3 月 1 日一样，是星期三。
	return Weekday((uint64(days) + uint64(Wednesday)) % 7)
}

// ISOWeek 返回 t 所在的 ISO 8601 年份和周数。
// 周的范围是 1 到 53。n 年的 1 月 1 日到 1 月 3 日可能属于 n-1 年的
// 第 52 或 53 周，而 12 月 29 日到 12 月 31 日可能属于 n+1 年的第 1 周。
func (t Time) ISOWeek() (year, week int) {
	// 根据以下规则：一个日历年中的第一个日历周是包含该年第一个星期四的那一周，
	// 而最后一周是紧接在下一日历年第一个日历周之前的那一周。
	// 详情请参见 https://www.iso.org/obp/ui#iso:std:iso:8601:-1:ed-1:v1:en:term:3.1.1.23。

	// 周从星期一开始
	// 星期一 星期二 星期三 星期四 星期五 星期六 星期日
	// 1      2       3         4        5      6        7
	// +3     +2      +1        0        -1     -2       -3
	// 相对星期四的偏移
	days := t.absSec().days()
	thu := days + absDays(Thursday-((days-1).weekday()+1))
	year, yday := thu.yearYday()
	return year, (yday-1)/7 + 1
}

// Clock 返回 t 指定的一天内的小时、分钟和秒。
func (t Time) Clock() (hour, min, sec int) {
	return t.absSec().clock()
}

// clock 返回 abs 指定的一天内的小时、分钟和秒。
func (abs absSeconds) clock() (hour, min, sec int) {
	sec = int(abs % secondsPerDay)
	hour = sec / secondsPerHour
	sec -= hour * secondsPerHour
	min = sec / secondsPerMinute
	sec -= min * secondsPerMinute
	return
}

// Hour 返回 t 指定的一天内的小时，范围是 [0, 23]。
func (t Time) Hour() int {
	return int(t.absSec()%secondsPerDay) / secondsPerHour
}

// Minute 返回 t 指定的小时内的分钟偏移，范围是 [0, 59]。
func (t Time) Minute() int {
	return int(t.absSec()%secondsPerHour) / secondsPerMinute
}

// Second 返回 t 指定的分钟内的秒偏移，范围是 [0, 59]。
func (t Time) Second() int {
	return int(t.absSec() % secondsPerMinute)
}

// Nanosecond 返回 t 指定的秒内的纳秒偏移，
// 范围是 [0, 999999999]。
func (t Time) Nanosecond() int {
	return int(t.nsec())
}

// YearDay 返回 t 指定的一年中的第几天，非闰年的范围是 [1,365]，
// 闰年是 [1,366]。
func (t Time) YearDay() int {
	_, yday := t.absSec().days().yearYday()
	return yday
}

// Duration 表示两个瞬间之间经过的时间，
// 以 int64 纳秒计数表示。该表示将最大可表示时长
// 限制在约 290 年。
type Duration int64

const (
	minDuration Duration = -1 << 63
	maxDuration Duration = 1<<63 - 1
)

// 常用时长。没有为 Day 或更大的单位定义常量，
// 以避免在夏令时时区转换时产生混淆。
//
// 要计算一个 [Duration] 中某种单位的数量，可进行除法：
//
//	second := time.Second
//	fmt.Print(int64(second/time.Millisecond)) // 输出 1000
//
// 要将一个整数个某单位转换为 Duration，可进行乘法：
//
//	seconds := 10
//	fmt.Print(time.Duration(seconds)*time.Second) // 输出 10s
const (
	Nanosecond  Duration = 1
	Microsecond          = 1000 * Nanosecond
	Millisecond          = 1000 * Microsecond
	Second               = 1000 * Millisecond
	Minute               = 60 * Second
	Hour                 = 60 * Minute
)

// String 返回以 "72h3m0.5s" 形式表示该时长的字符串。
// 前导的零单位会被省略。作为特例，小于一秒的时长在格式化时会使用更小的
// 单位（毫秒、微秒或纳秒），以确保首位数字非零。零时长格式化为 0s。
func (d Duration) String() string {
	// 这一步可内联，以利用“函数外联”（function outlining）。
	// 因此，调用方可以决定字符串是否必须分配在堆上。
	var arr [32]byte
	n := d.format(&arr)
	return string(arr[n:])
}

// format 将 d 的表示格式化到 buf 的末尾，
// 并返回第一个字符的偏移量。
func (d Duration) format(buf *[32]byte) int {
	// 最大时间为 2540400h10m10.000000000s
	w := len(buf)

	u := uint64(d)
	neg := d < 0
	if neg {
		u = -u
	}

	if u < uint64(Second) {
		// 特例：如果时长小于一秒，
		// 则使用更小的单位，例如 1.2ms
		var prec int
		w--
		buf[w] = 's'
		w--
		switch {
		case u == 0:
			buf[w] = '0'
			return w
		case u < uint64(Microsecond):
			// 打印纳秒
			prec = 0
			buf[w] = 'n'
		case u < uint64(Millisecond):
			// 打印微秒
			prec = 3
			// U+00B5 'µ' 微符号 == 0xC2 0xB5
			w-- // 需要为两个字节留出空间。
			copy(buf[w:], "µ")
		default:
			// 打印毫秒
			prec = 6
			buf[w] = 'm'
		}
		w, u = fmtFrac(buf[:w], u, prec)
		w = fmtInt(buf[:w], u)
	} else {
		w--
		buf[w] = 's'

		w, u = fmtFrac(buf[:w], u, 9)

		// u 现在是整数秒
		w = fmtInt(buf[:w], u%60)
		u /= 60

		// u 现在是整数分钟
		if u > 0 {
			w--
			buf[w] = 'm'
			w = fmtInt(buf[:w], u%60)
			u /= 60

			// u 现在是整数小时
			// 在小时处停止，因为每天的长度可能不同。
			if u > 0 {
				w--
				buf[w] = 'h'
				w = fmtInt(buf[:w], u)
			}
		}
	}

	if neg {
		w--
		buf[w] = '-'
	}

	return w
}

// fmtFrac 将 v/10**prec 的小数部分（例如 ".12345"）格式化到
// buf 的末尾，并省略末尾的零。当小数部分为 0 时，它也会省略
// 小数点。它返回输出字节开始处的索引以及 v/10**prec 的值。
func fmtFrac(buf []byte, v uint64, prec int) (nw int, nv uint64) {
	// 省略末尾的零，包括小数点在内。
	w := len(buf)
	print := false
	for i := 0; i < prec; i++ {
		digit := v % 10
		print = print || digit != 0
		if print {
			w--
			buf[w] = byte(digit) + '0'
		}
		v /= 10
	}
	if print {
		w--
		buf[w] = '.'
	}
	return w, v
}

// fmtInt 将 v 格式化到 buf 的末尾。
// 它返回输出开始处的索引。
func fmtInt(buf []byte, v uint64) int {
	w := len(buf)
	if v == 0 {
		w--
		buf[w] = '0'
	} else {
		for v > 0 {
			w--
			buf[w] = byte(v%10) + '0'
			v /= 10
		}
	}
	return w
}

// Nanoseconds 将该时长返回为整数纳秒计数。
func (d Duration) Nanoseconds() int64 { return int64(d) }

// Microseconds 将该时长返回为整数微秒计数。
func (d Duration) Microseconds() int64 { return int64(d) / 1e3 }

// Milliseconds 将该时长返回为整数毫秒计数。
func (d Duration) Milliseconds() int64 { return int64(d) / 1e6 }

// 这些方法返回 float64，因为主要用例是打印像 1.5s 这样的浮点数，
// 而截断为整数会使它们在这些情况下变得无用。
// 我们自行拆分整数和小数部分，保证将返回的 float64 转换为整数时
// 的舍入方式与纯整数转换相同，即使在某些情况下，
// 比如 float64(d.Nanoseconds())/1e9 会以不同方式舍入。

// Seconds 将该时长返回为浮点秒数。
func (d Duration) Seconds() float64 {
	sec := d / Second
	nsec := d % Second
	return float64(sec) + float64(nsec)/1e9
}

// Minutes 将该时长返回为浮点分钟数。
func (d Duration) Minutes() float64 {
	min := d / Minute
	nsec := d % Minute
	return float64(min) + float64(nsec)/(60*1e9)
}

// Hours 将该时长返回为浮点小时数。
func (d Duration) Hours() float64 {
	hour := d / Hour
	nsec := d % Hour
	return float64(hour) + float64(nsec)/(60*60*1e9)
}

// Truncate 返回将 d 向零舍入到 m 的倍数后的结果。
// 如果 m <= 0，Truncate 原样返回 d。
func (d Duration) Truncate(m Duration) Duration {
	if m <= 0 {
		return d
	}
	return d - d%m
}

// lessThanHalf 报告 x+x < y 是否成立，同时避免溢出，
// 这里假定 x 和 y 都为正（Duration 是有符号的）。
func lessThanHalf(x, y Duration) bool {
	return uint64(x)+uint64(x) < uint64(y)
}

// Round 返回将 d 舍入到 m 的最近倍数后的结果。
// 对于中间值的舍入行为是向远离零的方向舍入。
// 如果结果超出 [Duration] 能够存储的最大（或最小）值，
// Round 返回最大（或最小）时长。
// 如果 m <= 0，Round 原样返回 d。
func (d Duration) Round(m Duration) Duration {
	if m <= 0 {
		return d
	}
	r := d % m
	if d < 0 {
		r = -r
		if lessThanHalf(r, m) {
			return d + r
		}
		if d1 := d - m + r; d1 < d {
			return d1
		}
		return minDuration // 溢出
	}
	if lessThanHalf(r, m) {
		return d - r
	}
	if d1 := d + m - r; d1 > d {
		return d1
	}
	return maxDuration // 溢出
}

// Abs 返回 d 的绝对值。
// 作为特例，Duration([math.MinInt64]) 会被转换为 Duration([math.MaxInt64])，
// 其幅值减少 1 纳秒。
func (d Duration) Abs() Duration {
	switch {
	case d >= 0:
		return d
	case d == minDuration:
		return maxDuration
	default:
		return -d
	}
}

// Add 返回时间 t+d。
func (t Time) Add(d Duration) Time {
	dsec := int64(d / 1e9)
	nsec := t.nsec() + int32(d%1e9)
	if nsec >= 1e9 {
		dsec++
		nsec -= 1e9
	} else if nsec < 0 {
		dsec--
		nsec += 1e9
	}
	t.wall = t.wall&^nsecMask | uint64(nsec) // 更新 nsec
	t.addSec(dsec)
	if t.wall&hasMonotonic != 0 {
		te := t.ext + int64(d)
		if d < 0 && te > t.ext || d > 0 && te < t.ext {
			// 单调时钟读数现在超出范围；退化为仅使用挂钟。
			t.stripMono()
		} else {
			t.ext = te
		}
	}
	return t
}

// Sub 返回时长 t-u。如果结果超出 [Duration] 能够存储的最大（或最小）
// 值，将返回最大（或最小）时长。
// 要计算时长 d 对应的 t-d，请使用 t.Add(-d)。
func (t Time) Sub(u Time) Duration {
	if t.wall&u.wall&hasMonotonic != 0 {
		return subMono(t.ext, u.ext)
	}
	d := Duration(t.sec()-u.sec())*Second + Duration(t.nsec()-u.nsec())
	// 检查溢出或下溢。
	switch {
	case u.Add(d).Equal(t):
		return d // d 是正确的
	case t.Before(u):
		return minDuration // t - u 为负数，超出范围
	default:
		return maxDuration // t - u 为正数，超出范围
	}
}

func subMono(t, u int64) Duration {
	d := Duration(t - u)
	if d < 0 && t > u {
		return maxDuration // t - u 为正数，超出范围
	}
	if d > 0 && t < u {
		return minDuration // t - u 为负数，超出范围
	}
	return d
}

// Since 返回自 t 以来经过的时间。
// 它是 time.Now().Sub(t) 的简写。
func Since(t Time) Duration {
	if t.wall&hasMonotonic != 0 && !runtimeIsBubbled() {
		// 常见情况优化：如果 t 具有单调时间，则 Sub 只会使用它。
		return subMono(runtimeNano()-startNano, t.ext)
	}
	return Now().Sub(t)
}

// Until 返回到达 t 之前的时长。
// 它是 t.Sub(time.Now()) 的简写。
func Until(t Time) Duration {
	if t.wall&hasMonotonic != 0 && !runtimeIsBubbled() {
		// 常见情况优化：如果 t 具有单调时间，则 Sub 只会使用它。
		return subMono(t.ext, runtimeNano()-startNano)
	}
	return t.Sub(Now())
}

// AddDate 返回将给定的年数、月数和天数加到 t 上
// 所对应的时间。
// 例如，对 2011 年 1 月 1 日应用 AddDate(-1, 2, 3)
// 返回 2010 年 3 月 4 日。
//
// 注意，日期从根本上与时区耦合，而像天这样的日历周期并没有固定的
// 时长。AddDate 使用 Time 值的 Location 来确定这些时长。这意味着，相同
// 的 AddDate 参数可能会根据基础 Time 值及其 Location 产生不同的绝对时间
// 偏移。例如，对 3 月 27 日的 12:00 应用 AddDate(0, 0, 1) 总是返回 3 月 28 日
// 的 12:00。在某些位置和某些年份，这是 24 小时的偏移。在另一些情况下，
// 由于夏令时转换，它是 23 小时的偏移。
//
// AddDate 会像 Date 那样对其结果进行规范化，
// 因此，例如在 10 月 31 日上加一个月会得到
// 12 月 1 日，即 11 月 31 日的规范化形式。
func (t Time) AddDate(years int, months int, days int) Time {
	year, month, day := t.Date()
	hour, min, sec := t.Clock()
	return Date(year+years, month+Month(months), day+days, hour, min, sec, int(t.nsec()), t.Location())
}

// daysBefore 返回非闰年中月份 m 之前的天数。
// daysBefore(December+1) 返回 365。
func daysBefore(m Month) int {
	adj := 0
	if m >= March {
		adj = -2
	}

	// 在二月之后进行 -2 调整后，
	// 我们需要计算以下序列的累加和：
	//	0  31  30  31  30  31  30  31  31  30  31  30  31
	// 即：
	//	0  31  61  92 122 153 183 214 245 275 306 336 367
	// 这几乎正好是 367/12×(m-1)，只是存在偶尔的差一，
	// 这暗示可能存在形如 (a×m + b)/c 的整数近似。
	// 对较小的 a、b、c 进行暴力搜索发现，
	// (214×m - 211) / 7 能完美计算该函数。
	return (214*int(m)-211)/7 + adj
}

func daysIn(m Month, year int) int {
	if m == February {
		if isLeap(year) {
			return 29
		}
		return 28
	}
	// 在排除二月的特殊情况后，模式为
	//	31 30 31 30 31 30 31 31 30 31 30 31
	// 加上 m&1 会产生基本的交替；
	// 加上 (m>>3)&1 则会从八月开始反转该交替。
	return 30 + int((m+m>>3)&1)
}

// 由 runtime 包提供。
//
// now 返回当前真实时间，它已被 runtimeNow 取代，后者在适当时返回伪造的
// synctest 时钟。
//
// now 本应是内部细节，
// 但被广泛使用的包通过 linkname 访问它。
// 耻辱柱上的著名成员包括：
//   - gitee.com/quant1x/gox
//   - github.com/phuslu/log
//   - github.com/sethvargo/go-limiter
//   - github.com/ulule/limiter/v3
//
// 不要移除或更改类型签名。
// 参见 go.dev/issue/67401。
func now() (sec int64, nsec int32, mono int64)

// runtimeNow 返回当前时间。
// 在 synctest.Run 气泡内调用时，它返回该组的伪造时钟。
//
//go:linkname runtimeNow
func runtimeNow() (sec int64, nsec int32, mono int64)

// runtimeNano 返回运行时时钟当前的值，以纳秒为单位。
// 在 synctest.Run 气泡内调用时，它返回该组的伪造时钟。
//
//go:linkname runtimeNano
func runtimeNano() int64

//go:linkname runtimeIsBubbled
func runtimeIsBubbled() bool

// 单调时间被报告为相对于 startNano 的偏移量。
// 我们将 startNano 初始化为 runtimeNano() - 1，这样在单调时间精度相当低
// 的系统上（例如 Windows 2008，它似乎具有 15ms 的默认精度），
// 我们就不会报告 0 的单调时间。
// （调用方可能想用 0 表示“时间未设置”。）
var startNano int64 = runtimeNano() - 1

// x/tools 在其测试中使用了 time.Now 的 linkname。无害。
//go:linkname Now

// Now 返回当前本地时间。
func Now() Time {
	sec, nsec, mono := runtimeNow()
	if mono == 0 {
		return Time{uint64(nsec), sec + unixToInternal, Local}
	}
	mono -= startNano
	sec += unixToInternal - minWall
	if uint64(sec)>>33 != 0 {
		// 在存储单调时间时，秒字段溢出了可用的 33 位。
		// 在 2157 年 3 月 16 日之后将如此。
		return Time{uint64(nsec), sec + minWall, Local}
	}
	return Time{hasMonotonic | uint64(sec)<<nsecShift | uint64(nsec), mono, Local}
}

func unixTime(sec int64, nsec int32) Time {
	return Time{uint64(nsec), sec + unixToInternal, Local}
}

// UTC 返回将位置设置为 UTC 的 t。
func (t Time) UTC() Time {
	t.setLoc(&utcLoc)
	return t
}

// Local 返回将位置设置为本地时间的 t。
func (t Time) Local() Time {
	t.setLoc(Local)
	return t
}

// In 返回 t 的一个副本，它表示同一时间瞬间，
// 但为显示目的将该副本的位置信息设置为 loc。
//
// 如果 loc 为 nil，In 会 panic。
func (t Time) In(loc *Location) Time {
	if loc == nil {
		panic("time: missing Location in call to Time.In")
	}
	t.setLoc(loc)
	return t
}

// Location 返回与 t 关联的时区信息。
func (t Time) Location() *Location {
	l := t.loc
	if l == nil {
		l = UTC
	}
	return l
}

// Zone 计算在时间 t 生效的时区，返回时区的缩写名称
// （例如 "CET"）以及其相对 UTC 以东的偏移秒数。
func (t Time) Zone() (name string, offset int) {
	name, offset, _, _, _ = t.loc.lookup(t.unixSec())
	return
}

// ZoneBounds 返回在时间 t 生效的时区的边界。
// 该时区始于 start，下一个时区始于 end。
// 如果该时区始于时间的起点，则 start 将作为零 Time 返回。
// 如果该时区永远持续，则 end 将作为零 Time 返回。
// 返回的时间的 Location 将与 t 相同。
func (t Time) ZoneBounds() (start, end Time) {
	_, _, startSec, endSec, _ := t.loc.lookup(t.unixSec())
	if startSec != alpha {
		start = unixTime(startSec, 0)
		start.setLoc(t.loc)
	}
	if endSec != omega {
		end = unixTime(endSec, 0)
		end.setLoc(t.loc)
	}
	return
}

// Unix 将 t 作为 Unix 时间返回，即自 1970 年 1 月 1 日 UTC 以来
// 经过的秒数。结果不依赖于与 t 关联的位置。
// 类 Unix 操作系统通常将时间记录为 32 位的秒计数，
// 但由于此处的方法返回 64 位值，因此它在过去或未来数十亿年
// 内都有效。
func (t Time) Unix() int64 {
	return t.unixSec()
}

// UnixMilli 将 t 作为 Unix 时间返回，即自 1970 年 1 月 1 日 UTC 以来
// 经过的毫秒数。如果以毫秒表示的 Unix 时间无法用 int64 表示
// （即日期距 1970 年往前或往后超过约 2.92 亿年），则结果未定义。
// 结果不依赖于与 t 关联的位置。
func (t Time) UnixMilli() int64 {
	return t.unixSec()*1e3 + int64(t.nsec())/1e6
}

// UnixMicro 将 t 作为 Unix 时间返回，即自 1970 年 1 月 1 日 UTC 以来
// 经过的微秒数。如果以微秒表示的 Unix 时间无法用 int64 表示
// （即日期早于 -290307 年或晚于 294246 年），则结果未定义。
// 结果不依赖于与 t 关联的位置。
func (t Time) UnixMicro() int64 {
	return t.unixSec()*1e6 + int64(t.nsec())/1e3
}

// UnixNano 将 t 作为 Unix 时间返回，即自 1970 年 1 月 1 日 UTC 以来
// 经过的纳秒数。如果以纳秒表示的 Unix 时间无法用 int64 表示
// （即日期早于 1678 年或晚于 2262 年），则结果未定义。注意这意味着
// 对零 Time 调用 UnixNano 的结果是未定义的。结果不依赖于与 t 关联的
// 位置。
func (t Time) UnixNano() int64 {
	return (t.unixSec())*1e9 + int64(t.nsec())
}

const (
	timeBinaryVersionV1 byte = iota + 1 // 一般情况
	timeBinaryVersionV2                 // 仅用于 LMT
)

// AppendBinary 实现 [encoding.BinaryAppender] 接口。
func (t Time) AppendBinary(b []byte) ([]byte, error) {
	var offsetMin int16 // UTC 以东的分钟数。-1 表示 UTC。
	var offsetSec int8
	version := timeBinaryVersionV1

	if t.Location() == UTC {
		offsetMin = -1
	} else {
		_, offset := t.Zone()
		if offset%60 != 0 {
			version = timeBinaryVersionV2
			offsetSec = int8(offset % 60)
		}

		offset /= 60
		if offset < -32768 || offset == -1 || offset > 32767 {
			return b, errors.New("Time.MarshalBinary: unexpected zone offset")
		}
		offsetMin = int16(offset)
	}

	sec := t.sec()
	nsec := t.nsec()
	b = append(b,
		version,       // 字节 0：版本
		byte(sec>>56), // 字节 1-8：秒
		byte(sec>>48),
		byte(sec>>40),
		byte(sec>>32),
		byte(sec>>24),
		byte(sec>>16),
		byte(sec>>8),
		byte(sec),
		byte(nsec>>24), // 字节 9-12：纳秒
		byte(nsec>>16),
		byte(nsec>>8),
		byte(nsec),
		byte(offsetMin>>8), // 字节 13-14：时区偏移（分钟）
		byte(offsetMin),
	)
	if version == timeBinaryVersionV2 {
		b = append(b, byte(offsetSec))
	}
	return b, nil
}

// MarshalBinary 实现 [encoding.BinaryMarshaler] 接口。
func (t Time) MarshalBinary() ([]byte, error) {
	b, err := t.AppendBinary(make([]byte, 0, 16))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// UnmarshalBinary 实现 [encoding.BinaryUnmarshaler] 接口。
func (t *Time) UnmarshalBinary(data []byte) error {
	buf := data
	if len(buf) == 0 {
		return errors.New("Time.UnmarshalBinary: no data")
	}

	version := buf[0]
	if version != timeBinaryVersionV1 && version != timeBinaryVersionV2 {
		return errors.New("Time.UnmarshalBinary: unsupported version")
	}

	wantLen := /*版本*/ 1 + /*秒*/ 8 + /*纳秒*/ 4 + /*时区偏移*/ 2
	if version == timeBinaryVersionV2 {
		wantLen++
	}
	if len(buf) != wantLen {
		return errors.New("Time.UnmarshalBinary: invalid length")
	}

	buf = buf[1:]
	sec := int64(buf[7]) | int64(buf[6])<<8 | int64(buf[5])<<16 | int64(buf[4])<<24 |
		int64(buf[3])<<32 | int64(buf[2])<<40 | int64(buf[1])<<48 | int64(buf[0])<<56

	buf = buf[8:]
	nsec := int32(buf[3]) | int32(buf[2])<<8 | int32(buf[1])<<16 | int32(buf[0])<<24

	buf = buf[4:]
	offset := int(int16(buf[1])|int16(buf[0])<<8) * 60
	if version == timeBinaryVersionV2 {
		offset += int(buf[2])
	}

	*t = Time{}
	t.wall = uint64(nsec)
	t.ext = sec

	if offset == -1*60 {
		t.setLoc(&utcLoc)
	} else if _, localoff, _, _, _ := Local.lookup(t.unixSec()); offset == localoff {
		t.setLoc(Local)
	} else {
		t.setLoc(FixedZone("", offset))
	}

	return nil
}

// TODO(rsc): 在 Go 2 中移除 GobEncoder、GobDecoder、MarshalJSON、UnmarshalJSON。
// 相同的语义将由通用的 MarshalBinary、MarshalText、
// UnmarshalBinary、UnmarshalText 提供。

// GobEncode 实现 gob.GobEncoder 接口。
func (t Time) GobEncode() ([]byte, error) {
	return t.MarshalBinary()
}

// GobDecode 实现 gob.GobDecoder 接口。
func (t *Time) GobDecode(data []byte) error {
	return t.UnmarshalBinary(data)
}

// MarshalJSON 实现 [encoding/json.Marshaler] 接口。
// 时间是一个采用 RFC 3339 格式、带亚秒精度的带引号字符串。
// 如果该时间戳无法表示为有效的 RFC 3339
// （例如年份超出范围），则报告一个错误。
func (t Time) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(RFC3339Nano)+len(`""`))
	b = append(b, '"')
	b, err := t.appendStrictRFC3339(b)
	b = append(b, '"')
	if err != nil {
		return nil, errors.New("Time.MarshalJSON: " + err.Error())
	}
	return b, nil
}

// UnmarshalJSON 实现 [encoding/json.Unmarshaler] 接口。
// 时间必须是采用 RFC 3339 格式的带引号字符串。
func (t *Time) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	// TODO(https://go.dev/issue/47353)：正确地反转义 JSON 字符串。
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return errors.New("Time.UnmarshalJSON: input is not a JSON string")
	}
	data = data[len(`"`) : len(data)-len(`"`)]
	var err error
	*t, err = parseStrictRFC3339(data)
	return err
}

func (t Time) appendTo(b []byte, errPrefix string) ([]byte, error) {
	b, err := t.appendStrictRFC3339(b)
	if err != nil {
		return nil, errors.New(errPrefix + err.Error())
	}
	return b, nil
}

// AppendText 实现 [encoding.TextAppender] 接口。
// 时间采用 RFC 3339 格式、带亚秒精度进行格式化。
// 如果该时间戳无法表示为有效的 RFC 3339
// （例如年份超出范围），则返回一个错误。
func (t Time) AppendText(b []byte) ([]byte, error) {
	return t.appendTo(b, "Time.AppendText: ")
}

// MarshalText 实现 [encoding.TextMarshaler] 接口。其输出
// 与调用 [Time.AppendText] 方法的结果一致。
//
// 更多信息请参见 [Time.AppendText]。
func (t Time) MarshalText() ([]byte, error) {
	return t.appendTo(make([]byte, 0, len(RFC3339Nano)), "Time.MarshalText: ")
}

// UnmarshalText 实现 [encoding.TextUnmarshaler] 接口。
// 时间必须采用 RFC 3339 格式。
func (t *Time) UnmarshalText(data []byte) error {
	var err error
	*t, err = parseStrictRFC3339(data)
	return err
}

// Unix 返回给定 Unix 时间对应的本地 Time，
// 即自 1970 年 1 月 1 日 UTC 以来 sec 秒和 nsec 纳秒。
// 传入超出 [0, 999999999] 范围的 nsec 是合法的。
// 并非所有 sec 值都有对应的时间值。其中一个这样的值
// 是 1<<63-1（最大的 int64 值）。
func Unix(sec int64, nsec int64) Time {
	if nsec < 0 || nsec >= 1e9 {
		n := nsec / 1e9
		sec += n
		nsec -= n * 1e9
		if nsec < 0 {
			nsec += 1e9
			sec--
		}
	}
	return unixTime(sec, int32(nsec))
}

// UnixMilli 返回给定 Unix 时间对应的本地 Time，
// 即自 1970 年 1 月 1 日 UTC 以来 msec 毫秒。
func UnixMilli(msec int64) Time {
	return Unix(msec/1e3, (msec%1e3)*1e6)
}

// UnixMicro 返回给定 Unix 时间对应的本地 Time，
// 即自 1970 年 1 月 1 日 UTC 以来 usec 微秒。
func UnixMicro(usec int64) Time {
	return Unix(usec/1e6, (usec%1e6)*1e3)
}

// IsDST 报告配置位置中的时间是否处于夏令时。
func (t Time) IsDST() bool {
	_, _, _, _, isDST := t.loc.lookup(t.Unix())
	return isDST
}

func isLeap(year int) bool {
	// year%4 == 0 && (year%100 != 0 || year%400 == 0)
	// 最低 2 位必须清零。
	// 对于 25 的倍数，最低 4 位必须清零。
	// 感谢 Cassio Neri 提供这个技巧。
	mask := 0xf
	if year%25 != 0 {
		mask = 3
	}
	return year&mask == 0
}

// norm 返回 nhi、nlo，使得
//
//	hi * base + lo == nhi * base + nlo
//	0 <= nlo < base
func norm(hi, lo, base int) (nhi, nlo int) {
	if lo < 0 {
		n := (-lo-1)/base + 1
		hi -= n
		lo += n * base
	}
	if lo >= base {
		n := lo / base
		hi += n
		lo -= n * base
	}
	return hi, lo
}

// Date 返回对应于
//
//	yyyy-mm-dd hh:mm:ss + nsec nanoseconds
//
// 的时间，位于给定位置中该时间对应的适当时区。
//
// month、day、hour、min、sec 和 nsec 值可以超出其通常的范围，
// 并会在转换过程中被规范化。
// 例如，10 月 32 日会转换为 11 月 1 日。
//
// 夏令时转换会跳过或重复某些时间。
// 例如，在美国，2011 年 3 月 13 日 2:15am 从未出现，
// 而 2011 年 11 月 6 日 1:15am 出现了两次。在这种情况下，
// 时区的选择、因而时间的选择，并没有很好的定义。
// Date 返回一个在转换所涉及的两个时区之一中正确的时间，
// 但不保证是哪一个。
//
// 如果 loc 为 nil，Date 会 panic。
func Date(year int, month Month, day, hour, min, sec, nsec int, loc *Location) Time {
	if loc == nil {
		panic("time: missing Location in call to Date")
	}

	// 规范化月份，溢出到年份。
	m := int(month) - 1
	year, m = norm(year, m, 12)
	month = Month(m) + 1

	// 规范化 nsec、sec、min、hour，溢出到日。
	sec, nsec = norm(sec, nsec, 1e9)
	min, sec = norm(min, sec, 60)
	hour, min = norm(hour, min, 60)
	day, hour = norm(day, hour, 24)

	// 转换为绝对时间，然后再转换为 Unix 时间。
	unix := int64(dateToAbsDays(int64(year), month, day))*secondsPerDay +
		int64(hour*secondsPerHour+min*secondsPerMinute+sec) +
		absoluteToUnix

	// 查找预期时间的时区偏移，以便我们能调整到 UTC。
	// lookup 函数期望 UTC，因此我们首先传入 unix，
	// 希望它不会太接近时区转换点，
	// 如果接近则再进行调整。
	_, offset, start, end, _ := loc.lookup(unix)
	if offset != 0 {
		utc := unix - int64(offset)
		// 如果 utc 对于我们找到的时区有效，那么我们就得到了正确的偏移。
		// 如果无效，则通过在位置中查找 utc 来获取正确的偏移。
		if utc < start || utc >= end {
			_, offset, _, _, _ = loc.lookup(utc)
		}
		unix -= int64(offset)
	}

	t := unixTime(unix, int32(nsec))
	t.setLoc(loc)
	return t
}

// Truncate 返回将 t 向下舍入到 d 的倍数（自零时间起）后的结果。
// 如果 d <= 0，Truncate 返回去除任何单调时钟读数但其余保持不变的 t。
//
// Truncate 将时间视为自零时间起的绝对时长进行运算；它不作用于时间
// 的展示形式。因此，取决于时间的 Location，Truncate(Hour) 可能返回
// 一个分钟非零的时间。
func (t Time) Truncate(d Duration) Time {
	t.stripMono()
	if d <= 0 {
		return t
	}
	_, r := div(t, d)
	return t.Add(-r)
}

// Round 返回将 t 舍入到 d 的最近倍数（自零时间起）后的结果。
// 对于中间值的舍入行为是向上舍入。
// 如果 d <= 0，Round 返回去除任何单调时钟读数但其余保持不变的 t。
//
// Round 将时间视为自零时间起的绝对时长进行运算；它不作用于时间
// 的展示形式。因此，取决于时间的 Location，Round(Hour) 可能返回
// 一个分钟非零的时间。
func (t Time) Round(d Duration) Time {
	t.stripMono()
	if d <= 0 {
		return t
	}
	_, r := div(t, d)
	if lessThanHalf(r, d) {
		return t.Add(-r)
	}
	return t.Add(d - r)
}

// div 将 t 除以 d，并返回商的奇偶性和余数。
// 我们不再使用商的奇偶性（现在是四舍五入而非银行家舍入），
// 但保留它以防我们改变主意。
func div(t Time, d Duration) (qmod2 int, r Duration) {
	neg := false
	nsec := t.nsec()
	sec := t.sec()
	if sec < 0 {
		// 对绝对值进行运算。
		neg = true
		sec = -sec
		nsec = -nsec
		if nsec < 0 {
			nsec += 1e9
			sec-- // -- 之前 sec >= 1，因此是安全的
		}
	}

	switch {
	// 特例：2d 整除 1 秒。
	case d < Second && Second%(d+d) == 0:
		qmod2 = int(nsec/int32(d)) & 1
		r = Duration(nsec % int32(d))

	// 特例：d 是 1 秒的倍数。
	case d%Second == 0:
		d1 := int64(d / Second)
		qmod2 = int(sec/d1) & 1
		r = Duration(sec%d1)*Second + Duration(nsec)

	// 一般情况。
	// 如果运用更多技巧，这可能会更快，
	// 但它实际上只是为了避开 API 中的特例限制。
	// 没有人会关心这些情况。
	default:
		// 将纳秒计算为 128 位数。
		sec := uint64(sec)
		tmp := (sec >> 32) * 1e9
		u1 := tmp >> 32
		u0 := tmp << 32
		tmp = (sec & 0xFFFFFFFF) * 1e9
		u0x, u0 := u0, u0+tmp
		if u0 < u0x {
			u1++
		}
		u0x, u0 = u0, u0+uint64(nsec)
		if u0 < u0x {
			u1++
		}

		// 通过为递减的 k 减去 r<<k 来计算余数。
		// 商的奇偶性是我们在最后一轮是否相减。
		d1 := uint64(d)
		for d1>>63 != 1 {
			d1 <<= 1
		}
		d0 := uint64(0)
		for {
			qmod2 = 0
			if u1 > d1 || u1 == d1 && u0 >= d0 {
				// 相减
				qmod2 = 1
				u0x, u0 = u0, u0-d0
				if u0 > u0x {
					u1--
				}
				u1 -= d1
			}
			if d1 == 0 && d0 == uint64(d) {
				break
			}
			d0 >>= 1
			d0 |= (d1 & 1) << 63
			d1 >>= 1
		}
		r = Duration(u0)
	}

	if neg && r != 0 {
		// 如果输入为负且不是 d 的精确倍数，我们计算出的 q、r 满足
		//	q*d + r = -t
		// 但正确的答案由 -(q-1)、d-r 给出：
		//	q*d + r = -t
		//	-q*d - r = t
		//	-(q-1)*d + (d - r) = t
		qmod2 ^= 1
		r = d - r
	}
	return
}

// 令人遗憾的 Linkname 兼容性
//
// timeAbs、absDate 和 absClock 模仿旧的内部细节，已不再使用。
// 被广泛使用的包通过 linkname 引用它们以获取“更快”的时间例程。
// 耻辱柱上的著名成员包括：
//   - gitee.com/quant1x/gox
//   - github.com/phuslu/log
//
// phuslu 硬编码了 'Unix time + 9223372028715321600' [sic]
// 作为 absDate 和 absClock 的输入，使用了旧的以 1 月 1 日为基的
// 绝对时间。
// quant1x 通过 linkname 引用了 time.Time.abs 方法，并将该方法的
// 结果传给 absDate 和 absClock。
//
// 要让这两者都能继续工作，迫使我们在此提供这三个例程，它们基于
// 旧的以 1 月 1 日为基的纪元运行，而非新的以 3 月 1 日为基的纪元。
// 而 time.Time.abs 被 linkname 引用这一事实意味着，我们必须把当前的
// abs 方法命名为别的名字（即上文定义的 time.Time.absSec），才能
// 在此提供旧例程的这种模拟。
//
// 如果没有这些热衷 linkname 的包引用，这段代码根本不会被链接到二进制
// 文件中。特别地，尽管名为 time.Time.abs，它并不出现在 time.Time 的
// 方法表中。
//
// 不要移除这些例程或其 linkname，也不要更改类型签名或参数的含义。

//go:linkname legacyTimeTimeAbs time.Time.abs
func legacyTimeTimeAbs(t Time) uint64 {
	return uint64(t.absSec() - marchThruDecember*secondsPerDay)
}

//go:linkname legacyAbsClock time.absClock
func legacyAbsClock(abs uint64) (hour, min, sec int) {
	return absSeconds(abs + marchThruDecember*secondsPerDay).clock()
}

//go:linkname legacyAbsDate time.absDate
func legacyAbsDate(abs uint64, full bool) (year int, month Month, day int, yday int) {
	d := absSeconds(abs + marchThruDecember*secondsPerDay).days()
	year, month, day = d.date()
	_, yday = d.yearYday()
	yday-- // yearYday 以 1 为基，旧 API 以 0 为基
	return
}
