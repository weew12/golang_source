// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 此文件包含处理模板选项的代码。

package template

import "strings"

// missingKeyAction 定义了在索引映射时键不存在时的响应方式。
type missingKeyAction int

const (
	mapInvalid   missingKeyAction = iota // 返回无效的 reflect.Value。
	mapZeroValue                         // 返回映射元素的零值。
	mapError                             // 报错
)

type option struct {
	missingKey missingKeyAction
}

// Option 为模板设置选项。选项由字符串描述，可以是简单字符串或 "key=value"。
// 选项字符串中最多只能有一个等号。如果选项字符串无法识别或无效，Option 会 panic。
//
// 已知选项：
//
// missingkey: 控制如果在执行期间用不存在的键索引映射时的行为。
//
//	"missingkey=default" 或 "missingkey=invalid"
//		默认行为：什么都不做，继续执行。
//		如果打印，索引操作的结果是字符串 "<no value>"。
//	"missingkey=zero"
//		操作返回映射类型元素的零值。
//	"missingkey=error"
//		执行立即停止并报错。
func (t *Template) Option(opt ...string) *Template {
	t.init()
	for _, s := range opt {
		t.setOption(s)
	}
	return t
}

func (t *Template) setOption(opt string) {
	if opt == "" {
		panic("empty option string")
	}
	// key=value
	if key, value, ok := strings.Cut(opt, "="); ok {
		switch key {
		case "missingkey":
			switch value {
			case "invalid", "default":
				t.option.missingKey = mapInvalid
				return
			case "zero":
				t.option.missingKey = mapZeroValue
				return
			case "error":
				t.option.missingKey = mapError
				return
			}
		}
	}
	panic("unrecognized option: " + opt)
}
