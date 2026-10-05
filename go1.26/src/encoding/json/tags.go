// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.jsonv2

package json

import (
	"strings"
)

// tagOptions 是结构体字段 "json" 标签中逗号之后的字符串，
// 或空字符串。它不包含前导逗号。
type tagOptions string

// parseTag 将结构体字段的 json 标签拆分为名称和逗号分隔的选项。
func parseTag(tag string) (string, tagOptions) {
	tag, opt, _ := strings.Cut(tag, ",")
	return tag, tagOptions(opt)
}

// Contains 报告一个逗号分隔的选项列表是否包含某个特定的子串标志。
// substr 必须被字符串边界或逗号包围。
func (o tagOptions) Contains(optionName string) bool {
	if len(o) == 0 {
		return false
	}
	s := string(o)
	for s != "" {
		var name string
		name, s, _ = strings.Cut(s, ",")
		if name == optionName {
			return true
		}
	}
	return false
}
