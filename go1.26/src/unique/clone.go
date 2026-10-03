// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unique

import (
	"internal/abi"
	"internal/stringslite"
	"unsafe"
)

// clone 复制 value，并可能将 value 中找到的字符串值
// 更新为这些字符串的克隆版本。显式克隆字符串的目的
// 是避免意外地让一个大字符串获得很长的生命周期。
//
// 注意，这会克隆 value 中找到的结构体和数组中的字符串，
// 并且如果 value 本身是字符串，也会克隆 value。然而，如果 value 是
// 接口或切片类型（即通过间接方式找到的），它不会克隆
// 其中的字符串。
func clone[T comparable](value T, seq *cloneSeq) T {
	for _, offset := range seq.stringOffsets {
		ps := (*string)(unsafe.Pointer(uintptr(unsafe.Pointer(&value)) + offset))
		*ps = stringslite.Clone(*ps)
	}
	return abi.EscapeToResultNonString(value)
}

// singleStringClone 描述如何克隆单个字符串。
var singleStringClone = cloneSeq{stringOffsets: []uintptr{0}}

// cloneSeq 描述如何克隆某个特定类型的值。
type cloneSeq struct {
	stringOffsets []uintptr
}

// makeCloneSeq 为某个类型创建 cloneSeq。
func makeCloneSeq(typ *abi.Type) cloneSeq {
	if typ == nil {
		return cloneSeq{}
	}
	if typ.Kind() == abi.String {
		return singleStringClone
	}
	var seq cloneSeq
	switch typ.Kind() {
	case abi.Struct:
		buildStructCloneSeq(typ, &seq, 0)
	case abi.Array:
		buildArrayCloneSeq(typ, &seq, 0)
	}
	return seq
}

// buildStructCloneSeq 为 Kind 为 abi.Struct 的 abi.Type 填充 cloneSeq。
func buildStructCloneSeq(typ *abi.Type, seq *cloneSeq, baseOffset uintptr) {
	styp := typ.StructType()
	for i := range styp.Fields {
		f := &styp.Fields[i]
		switch f.Typ.Kind() {
		case abi.String:
			seq.stringOffsets = append(seq.stringOffsets, baseOffset+f.Offset)
		case abi.Struct:
			buildStructCloneSeq(f.Typ, seq, baseOffset+f.Offset)
		case abi.Array:
			buildArrayCloneSeq(f.Typ, seq, baseOffset+f.Offset)
		}
	}
}

// buildArrayCloneSeq 为 Kind 为 abi.Array 的 abi.Type 填充 cloneSeq。
func buildArrayCloneSeq(typ *abi.Type, seq *cloneSeq, baseOffset uintptr) {
	atyp := typ.ArrayType()
	etyp := atyp.Elem
	offset := baseOffset
	for range atyp.Len {
		switch etyp.Kind() {
		case abi.String:
			seq.stringOffsets = append(seq.stringOffsets, offset)
		case abi.Struct:
			buildStructCloneSeq(etyp, seq, offset)
		case abi.Array:
			buildArrayCloneSeq(etyp, seq, offset)
		}
		offset += etyp.Size()
		align := uintptr(etyp.FieldAlign())
		offset = (offset + align - 1) &^ (align - 1)
	}
}
