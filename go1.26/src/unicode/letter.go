// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// unicode 包提供了用于测试 Unicode 码点某些属性的数据和函数。
package unicode

const (
	MaxRune         = '\U0010FFFF' // 最大有效 Unicode 码点。
	ReplacementChar = '\uFFFD'     // 表示无效码点。
	MaxASCII        = '\u007F'     // 最大 ASCII 值。
	MaxLatin1       = '\u00FF'     // 最大 Latin-1 值。
)

// RangeTable 通过列出集合内码点的范围来定义一组 Unicode 码点。
// 范围以两个切片列出以节省空间：一个 16 位范围的切片和一个 32 位范围的切片。
// 两个切片必须按升序排列且不重叠。
// 此外，R32 应仅包含 >= 0x10000 (1<<16) 的值。
type RangeTable struct {
	R16         []Range16
	R32         []Range32
	LatinOffset int // R16 中 Hi <= MaxLatin1 的条目数量
}

// Range16 表示一个 16 位 Unicode 码点的范围。范围从 Lo 到 Hi（含两端），
// 具有指定的步长。
type Range16 struct {
	Lo     uint16
	Hi     uint16
	Stride uint16
}

// Range32 表示一个 Unicode 码点的范围，当一个或多个值无法用 16 位表示时使用。
// 范围从 Lo 到 Hi（含两端），具有指定的步长。Lo 和 Hi 必须始终 >= 1<<16。
type Range32 struct {
	Lo     uint32
	Hi     uint32
	Stride uint32
}

// CaseRange 表示用于简单（一个码点到一个码点）大小写转换的 Unicode 码点范围。
// 范围从 Lo 到 Hi（含两端），固定步长为 1。Delta 是需要加到码点上
// 以到达该字符不同大小写对应码点的数值。它们可以是负数。如果为零，
// 表示该字符已处于对应的大小写形式。有一种特殊情况表示
// 交替对应的大写和小写对的序列。它以固定 Delta 出现：
//
//	{UpperLower, UpperLower, UpperLower}
//
// 常量 UpperLower 具有一个在其他情况下不可能出现的 delta 值。
type CaseRange struct {
	Lo    uint32
	Hi    uint32
	Delta d
}

// SpecialCase 表示特定于语言的大小写映射，例如土耳其语。
// SpecialCase 的方法通过覆盖标准映射来进行自定义。
type SpecialCase []CaseRange

// BUG(r): 目前没有完整大小写折叠的机制，即涉及输入或输出中
// 多个 rune 的字符的大小写折叠。

// CaseRanges 中 Delta 数组的索引，用于大小写映射。
const (
	UpperCase = iota
	LowerCase
	TitleCase
	MaxCase
)

type d [MaxCase]rune // 使 CaseRanges 文本更短

// 如果 [CaseRange] 的 Delta 字段为 UpperLower，则表示
// 该 CaseRange 表示如下形式的序列（例如）：
// [Upper] [Lower] [Upper] [Lower]。
const (
	UpperLower = MaxRune + 1 // （不可能是有效的 delta 值。）
)

// linearMax 是对非 Latin1 rune 进行线性搜索的最大表大小。
// 通过运行 'go test -calibrate' 得出。
const linearMax = 18

// is16 报告 r 是否在已排序的 16 位范围切片中。
func is16(ranges []Range16, r uint16) bool {
	if len(ranges) <= linearMax || r <= MaxLatin1 {
		for i := range ranges {
			range_ := &ranges[i]
			if r < range_.Lo {
				return false
			}
			if r <= range_.Hi {
				return range_.Stride == 1 || (r-range_.Lo)%range_.Stride == 0
			}
		}
		return false
	}

	// 对范围进行二分搜索
	lo := 0
	hi := len(ranges)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		range_ := &ranges[m]
		if range_.Lo <= r && r <= range_.Hi {
			return range_.Stride == 1 || (r-range_.Lo)%range_.Stride == 0
		}
		if r < range_.Lo {
			hi = m
		} else {
			lo = m + 1
		}
	}
	return false
}

// is32 报告 r 是否在已排序的 32 位范围切片中。
func is32(ranges []Range32, r uint32) bool {
	if len(ranges) <= linearMax {
		for i := range ranges {
			range_ := &ranges[i]
			if r < range_.Lo {
				return false
			}
			if r <= range_.Hi {
				return range_.Stride == 1 || (r-range_.Lo)%range_.Stride == 0
			}
		}
		return false
	}

	// 对范围进行二分搜索
	lo := 0
	hi := len(ranges)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		range_ := ranges[m]
		if range_.Lo <= r && r <= range_.Hi {
			return range_.Stride == 1 || (r-range_.Lo)%range_.Stride == 0
		}
		if r < range_.Lo {
			hi = m
		} else {
			lo = m + 1
		}
	}
	return false
}

// Is 报告该 rune 是否在指定的范围表中。
func Is(rangeTab *RangeTable, r rune) bool {
	r16 := rangeTab.R16
	// 作为 uint32 比较以正确处理负 rune。
	if len(r16) > 0 && uint32(r) <= uint32(r16[len(r16)-1].Hi) {
		return is16(r16, uint16(r))
	}
	r32 := rangeTab.R32
	if len(r32) > 0 && r >= rune(r32[0].Lo) {
		return is32(r32, uint32(r))
	}
	return false
}

func isExcludingLatin(rangeTab *RangeTable, r rune) bool {
	r16 := rangeTab.R16
	// 作为 uint32 比较以正确处理负 rune。
	if off := rangeTab.LatinOffset; len(r16) > off && uint32(r) <= uint32(r16[len(r16)-1].Hi) {
		return is16(r16[off:], uint16(r))
	}
	r32 := rangeTab.R32
	if len(r32) > 0 && r >= rune(r32[0].Lo) {
		return is32(r32, uint32(r))
	}
	return false
}

// IsUpper 报告该 rune 是否为大写字母。
func IsUpper(r rune) bool {
	// 参见 IsGraphic 中的注释。
	if uint32(r) <= MaxLatin1 {
		return properties[uint8(r)]&pLmask == pLu
	}
	return isExcludingLatin(Upper, r)
}

// IsLower 报告该 rune 是否为小写字母。
func IsLower(r rune) bool {
	// 参见 IsGraphic 中的注释。
	if uint32(r) <= MaxLatin1 {
		return properties[uint8(r)]&pLmask == pLl
	}
	return isExcludingLatin(Lower, r)
}

// IsTitle 报告该 rune 是否为标题大写字母。
func IsTitle(r rune) bool {
	if r <= MaxLatin1 {
		return false
	}
	return isExcludingLatin(Title, r)
}

// lookupCaseRange 返回 rune r 的 CaseRange 映射，如果 r 不存在映射则返回 nil。
func lookupCaseRange(r rune, caseRange []CaseRange) *CaseRange {
	// 对范围进行二分搜索
	lo := 0
	hi := len(caseRange)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		cr := &caseRange[m]
		if rune(cr.Lo) <= r && r <= rune(cr.Hi) {
			return cr
		}
		if r < rune(cr.Lo) {
			hi = m
		} else {
			lo = m + 1
		}
	}
	return nil
}

// convertCase 使用 CaseRange cr 将 r 转换为 _case 指定的大小写。
func convertCase(_case int, r rune, cr *CaseRange) rune {
	delta := cr.Delta[_case]
	if delta > MaxRune {
		// 在 Upper-Lower 序列中（始终以大写字母开头），
		// 实际的 delta 总是如下所示：
		//	{0, 1, 0}    UpperCase（下一个是 Lower）
		//	{-1, 0, -1}  LowerCase（上一个是 Upper、Title）
		// 序列起始处偶数偏移位置的字符为大写；
		// 奇数偏移位置的为小写。
		// 正确的映射可以通过清除或设置序列偏移中的低位来完成。
		// 常量 UpperCase 和 TitleCase 是偶数，而 LowerCase
		// 是奇数，因此我们从 _case 中取低位。
		return rune(cr.Lo) + ((r-rune(cr.Lo))&^1 | rune(_case&1))
	}
	return r + delta
}

// to 使用指定的大小写映射来映射 rune。
// 它还报告 caseRange 中是否包含 r 的映射。
func to(_case int, r rune, caseRange []CaseRange) (mappedRune rune, foundMapping bool) {
	if _case < 0 || MaxCase <= _case {
		return ReplacementChar, false // 作为合理的错误返回
	}
	if cr := lookupCaseRange(r, caseRange); cr != nil {
		return convertCase(_case, r, cr), true
	}
	return r, false
}

// To 将 rune 映射为指定的大小写：[UpperCase]、[LowerCase] 或 [TitleCase]。
func To(_case int, r rune) rune {
	r, _ = to(_case, r, CaseRanges)
	return r
}

// ToUpper 将 rune 映射为大写。
func ToUpper(r rune) rune {
	if r <= MaxASCII {
		if 'a' <= r && r <= 'z' {
			r -= 'a' - 'A'
		}
		return r
	}
	return To(UpperCase, r)
}

// ToLower 将 rune 映射为小写。
func ToLower(r rune) rune {
	if r <= MaxASCII {
		if 'A' <= r && r <= 'Z' {
			r += 'a' - 'A'
		}
		return r
	}
	return To(LowerCase, r)
}

// ToTitle 将 rune 映射为标题大写。
func ToTitle(r rune) rune {
	if r <= MaxASCII {
		if 'a' <= r && r <= 'z' { // 对于 ASCII，标题大写即为大写
			r -= 'a' - 'A'
		}
		return r
	}
	return To(TitleCase, r)
}

// ToUpper 将 rune 映射为大写，优先使用特殊映射。
func (special SpecialCase) ToUpper(r rune) rune {
	r1, hadMapping := to(UpperCase, r, []CaseRange(special))
	if r1 == r && !hadMapping {
		r1 = ToUpper(r)
	}
	return r1
}

// ToTitle 将 rune 映射为标题大写，优先使用特殊映射。
func (special SpecialCase) ToTitle(r rune) rune {
	r1, hadMapping := to(TitleCase, r, []CaseRange(special))
	if r1 == r && !hadMapping {
		r1 = ToTitle(r)
	}
	return r1
}

// ToLower 将 rune 映射为小写，优先使用特殊映射。
func (special SpecialCase) ToLower(r rune) rune {
	r1, hadMapping := to(LowerCase, r, []CaseRange(special))
	if r1 == r && !hadMapping {
		r1 = ToLower(r)
	}
	return r1
}

// caseOrbit 在 tables.go 中定义为 []foldPair。目前所有条目都适合 uint16，
// 因此使用 uint16。如果情况发生变化，编译将失败（复合字面量中的常量将无法
// 适合 uint16），届时此处的类型可以更改为 uint32。
type foldPair struct {
	From uint16
	To   uint16
}

// SimpleFold 遍历在 Unicode 定义的简单大小写折叠下等价的 Unicode 码点。
// 在与该 rune 等价的码点中（包括 rune 本身），SimpleFold 返回
// 大于 r 的最小 rune（如果存在），否则返回 >= 0 的最小 rune。
// 如果 r 不是有效的 Unicode 码点，SimpleFold(r) 返回 r。
//
// 例如：
//
//	SimpleFold('A') = 'a'
//	SimpleFold('a') = 'A'
//
//	SimpleFold('K') = 'k'
//	SimpleFold('k') = '\u212A' (Kelvin symbol, K)
//	SimpleFold('\u212A') = 'K'
//
//	SimpleFold('1') = '1'
//
//	SimpleFold(-2) = -2
func SimpleFold(r rune) rune {
	if r < 0 || r > MaxRune {
		return r
	}

	if int(r) < len(asciiFold) {
		return rune(asciiFold[r])
	}

	// 查阅 caseOrbit 表以处理特殊情况。
	lo := 0
	hi := len(caseOrbit)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if rune(caseOrbit[m].From) < r {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo < len(caseOrbit) && rune(caseOrbit[lo].From) == r {
		return rune(caseOrbit[lo].To)
	}

	// 未指定折叠。这是一个包含 rune 以及 ToLower(rune)
	// 和 ToUpper(rune)（如果它们与 rune 不同）的一元素或二元素等价类。
	if cr := lookupCaseRange(r, CaseRanges); cr != nil {
		if l := convertCase(LowerCase, r, cr); l != r {
			return l
		}
		return convertCase(UpperCase, r, cr)
	}
	return r
}
