// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO: 此文件仅包含土耳其语和阿塞拜疆语的特殊大小写规则。
// 它应该涵盖所有具有特殊大小写规则的语言并自动生成，
// 但这需要先进行一些 API 开发。

package unicode

var TurkishCase SpecialCase = _TurkishCase
var _TurkishCase = SpecialCase{
	CaseRange{0x0049, 0x0049, d{0, 0x131 - 0x49, 0}},
	CaseRange{0x0069, 0x0069, d{0x130 - 0x69, 0, 0x130 - 0x69}},
	CaseRange{0x0130, 0x0130, d{0, 0x69 - 0x130, 0}},
	CaseRange{0x0131, 0x0131, d{0x49 - 0x131, 0, 0x49 - 0x131}},
}

var AzeriCase SpecialCase = _TurkishCase
