// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package jsonopts

import (
	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
)

// Options 是各 json 包之间共享的通用选项类型。
type Options interface {
	// JSONOptions 被导出，以便相关的 json 包能够实现 Options。
	JSONOptions(internal.NotForPublicUse)
}

// Struct 是以结构体形式表示的所有选项的组合。
// 这种形式便于沿着调用栈向下传递以及进行查询。
type Struct struct {
	Flags jsonflags.Flags

	CoderValues
	ArshalValues
}

type CoderValues struct {
	Indent       string // jsonflags.Indent
	IndentPrefix string // jsonflags.IndentPrefix
	ByteLimit    int64  // jsonflags.ByteLimit
	DepthLimit   int    // jsonflags.DepthLimit
}

type ArshalValues struct {
	// Marshalers 和 Unmarshalers 字段使用 any 类型，以避免对
	// *json.Marshalers 和 *json.Unmarshalers 产生具体依赖，
	// 否则又会进而对 "reflect" 包产生依赖。

	Marshalers   any // jsonflags.Marshalers
	Unmarshalers any // jsonflags.Unmarshalers

	Format      string
	FormatDepth int
}

// DefaultOptionsV2 是定义默认 v2 行为的全部选项的集合。
var DefaultOptionsV2 = Struct{
	Flags: jsonflags.Flags{
		Presence: uint64(jsonflags.DefaultV1Flags),
		Values:   uint64(0), // DefaultV1Flags 中的所有标志均为 false
	},
}

// DefaultOptionsV1 是定义默认 v1 行为的全部选项的集合。
var DefaultOptionsV1 = Struct{
	Flags: jsonflags.Flags{
		Presence: uint64(jsonflags.DefaultV1Flags),
		Values:   uint64(jsonflags.DefaultV1Flags), // DefaultV1Flags 中的所有标志均为 true
	},
}

func (*Struct) JSONOptions(internal.NotForPublicUse) {}

// GetUnknownOption 由 "json" 包注入，用于处理在该包中声明的 Options，
// 以便 "jsonopts" 能够处理它们。
var GetUnknownOption = func(Struct, Options) (any, bool) { panic("unknown option") }

func GetOption[T any](opts Options, setter func(T) Options) (T, bool) {
	// 将选项折叠为 *Struct，以简化查找。
	structOpts, ok := opts.(*Struct)
	if !ok {
		var structOpts2 Struct
		structOpts2.Join(opts)
		structOpts = &structOpts2
	}

	// 根据 setter 的返回值查找该选项。
	var zero T
	switch opt := setter(zero).(type) {
	case jsonflags.Bools:
		v := structOpts.Flags.Get(opt)
		ok := structOpts.Flags.Has(opt)
		return any(v).(T), ok
	case Indent:
		if !structOpts.Flags.Has(jsonflags.Indent) {
			return zero, false
		}
		return any(structOpts.Indent).(T), true
	case IndentPrefix:
		if !structOpts.Flags.Has(jsonflags.IndentPrefix) {
			return zero, false
		}
		return any(structOpts.IndentPrefix).(T), true
	case ByteLimit:
		if !structOpts.Flags.Has(jsonflags.ByteLimit) {
			return zero, false
		}
		return any(structOpts.ByteLimit).(T), true
	case DepthLimit:
		if !structOpts.Flags.Has(jsonflags.DepthLimit) {
			return zero, false
		}
		return any(structOpts.DepthLimit).(T), true
	default:
		v, ok := GetUnknownOption(*structOpts, opt)
		return v.(T), ok
	}
}

// JoinUnknownOption 由 "json" 包注入，用于处理在该包中声明的 Options，
// 以便 "jsonopts" 能够处理它们。
var JoinUnknownOption = func(Struct, Options) Struct { panic("unknown option") }

func (dst *Struct) Join(srcs ...Options) {
	dst.join(false, srcs...)
}

func (dst *Struct) JoinWithoutCoderOptions(srcs ...Options) {
	dst.join(true, srcs...)
}

func (dst *Struct) join(excludeCoderOptions bool, srcs ...Options) {
	for _, src := range srcs {
		switch src := src.(type) {
		case nil:
			continue
		case jsonflags.Bools:
			if excludeCoderOptions {
				src &= ^jsonflags.AllCoderFlags
			}
			dst.Flags.Set(src)
		case Indent:
			if excludeCoderOptions {
				continue
			}
			dst.Flags.Set(jsonflags.Multiline | jsonflags.Indent | 1)
			dst.Indent = string(src)
		case IndentPrefix:
			if excludeCoderOptions {
				continue
			}
			dst.Flags.Set(jsonflags.Multiline | jsonflags.IndentPrefix | 1)
			dst.IndentPrefix = string(src)
		case ByteLimit:
			if excludeCoderOptions {
				continue
			}
			dst.Flags.Set(jsonflags.ByteLimit | 1)
			dst.ByteLimit = int64(src)
		case DepthLimit:
			if excludeCoderOptions {
				continue
			}
			dst.Flags.Set(jsonflags.DepthLimit | 1)
			dst.DepthLimit = int(src)
		case *Struct:
			srcFlags := src.Flags // 浅拷贝这些 flags
			if excludeCoderOptions {
				srcFlags.Clear(jsonflags.AllCoderFlags)
			}
			dst.Flags.Join(srcFlags)
			if srcFlags.Has(jsonflags.NonBooleanFlags) {
				if srcFlags.Has(jsonflags.Indent) {
					dst.Indent = src.Indent
				}
				if srcFlags.Has(jsonflags.IndentPrefix) {
					dst.IndentPrefix = src.IndentPrefix
				}
				if srcFlags.Has(jsonflags.ByteLimit) {
					dst.ByteLimit = src.ByteLimit
				}
				if srcFlags.Has(jsonflags.DepthLimit) {
					dst.DepthLimit = src.DepthLimit
				}
				if srcFlags.Has(jsonflags.Marshalers) {
					dst.Marshalers = src.Marshalers
				}
				if srcFlags.Has(jsonflags.Unmarshalers) {
					dst.Unmarshalers = src.Unmarshalers
				}
			}
		default:
			*dst = JoinUnknownOption(*dst, src)
		}
	}
}

type (
	Indent       string // jsontext.WithIndent
	IndentPrefix string // jsontext.WithIndentPrefix
	ByteLimit    int64  // jsontext.WithByteLimit
	DepthLimit   int    // jsontext.WithDepthLimit
	// 用于 "json" 包中声明的 jsonflags.Marshalers 的类型
	// 用于 "json" 包中声明的 jsonflags.Unmarshalers 的类型
)

func (Indent) JSONOptions(internal.NotForPublicUse)       {}
func (IndentPrefix) JSONOptions(internal.NotForPublicUse) {}
func (ByteLimit) JSONOptions(internal.NotForPublicUse)    {}
func (DepthLimit) JSONOptions(internal.NotForPublicUse)   {}
