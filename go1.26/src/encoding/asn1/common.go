// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asn1

import (
	"reflect"
	"strconv"
	"strings"
)

// ASN.1 对象前面带有元数据：
//   标签：对象的类型
//   一个表示此对象是否为复合对象的标志
//   class 类型：标签的命名空间
//   对象的长度（以字节为单位）

// 以下是一些标准的标签和类

// ASN.1 标签表示后续对象的类型。
const (
	TagBoolean         = 1
	TagInteger         = 2
	TagBitString       = 3
	TagOctetString     = 4
	TagNull            = 5
	TagOID             = 6
	TagEnum            = 10
	TagUTF8String      = 12
	TagSequence        = 16
	TagSet             = 17
	TagNumericString   = 18
	TagPrintableString = 19
	TagT61String       = 20
	TagIA5String       = 22
	TagUTCTime         = 23
	TagGeneralizedTime = 24
	TagGeneralString   = 27
	TagBMPString       = 30
)

// ASN.1 类类型表示标签的命名空间。
const (
	ClassUniversal       = 0
	ClassApplication     = 1
	ClassContextSpecific = 2
	ClassPrivate         = 3
)

type tagAndLength struct {
	class, tag, length int
	isCompound         bool
}

// ASN.1 有 IMPLICIT 和 EXPLICIT 标签，可分别译为“代替”和“除……之外还”。
// 未指定时，每个原始类型在 UNIVERSAL 类中都有一个默认标签。
//
// 例如：BIT STRING 默认被标记为 [UNIVERSAL 3]（尽管 ASN.1 实际上
// 并没有 UNIVERSAL 关键字）。然而，若写成 [IMPLICIT
// CONTEXT-SPECIFIC 42]，则意味着该标签被另一个标签替换。
//
// 另一方面，若写成 [EXPLICIT CONTEXT-SPECIFIC 10]，则会有一个
// /额外的/ 标签包裹默认标签。这个显式标签会设置复合标志。
//
// （这样做是为了消除与可选元素之间的歧义。）
//
// 你可以将 EXPLICIT 和 IMPLICIT 标签任意层叠，但我们这里不支持这样做。
// 我们支持在结构体字段上通过标签字符串进行单层的 EXPLICIT 或 IMPLICIT 标记。

// fieldParameters 是结构体字段的标签字符串解析后的表示。
type fieldParameters struct {
	optional     bool   // 当且仅当该字段为 OPTIONAL 时为 true
	explicit     bool   // 当且仅当使用了 EXPLICIT 标签时为 true。
	application  bool   // 当且仅当使用了 APPLICATION 标签时为 true。
	private      bool   // 当且仅当使用了 PRIVATE 标签时为 true。
	defaultValue *int64 // INTEGER 类型字段的默认值（可能为 nil）。
	tag          *int   // EXPLICIT 或 IMPLICIT 标签（可能为 nil）。
	stringType   int    // 编组时使用的字符串标签。
	timeType     int    // 编组时使用的时间标签。
	set          bool   // 当且仅当应编码为 SET 时为 true
	omitEmpty    bool   // 当且仅当编组时为空则应省略时为 true。

	// 不变式：
	//   如果设置了 explicit，则 tag 非 nil。
}

// 给定一个格式如包注释所述的标签字符串，
// parseFieldParameters 会将其解析为 fieldParameters 结构体，
// 并忽略字符串中无法识别的部分。
func parseFieldParameters(str string) (ret fieldParameters) {
	var part string
	for len(str) > 0 {
		part, str, _ = strings.Cut(str, ",")
		switch {
		case part == "optional":
			ret.optional = true
		case part == "explicit":
			ret.explicit = true
			if ret.tag == nil {
				ret.tag = new(int)
			}
		case part == "generalized":
			ret.timeType = TagGeneralizedTime
		case part == "utc":
			ret.timeType = TagUTCTime
		case part == "ia5":
			ret.stringType = TagIA5String
		case part == "printable":
			ret.stringType = TagPrintableString
		case part == "numeric":
			ret.stringType = TagNumericString
		case part == "utf8":
			ret.stringType = TagUTF8String
		case strings.HasPrefix(part, "default:"):
			i, err := strconv.ParseInt(part[8:], 10, 64)
			if err == nil {
				ret.defaultValue = new(int64)
				*ret.defaultValue = i
			}
		case strings.HasPrefix(part, "tag:"):
			i, err := strconv.Atoi(part[4:])
			if err == nil {
				ret.tag = new(int)
				*ret.tag = i
			}
		case part == "set":
			ret.set = true
		case part == "application":
			ret.application = true
			if ret.tag == nil {
				ret.tag = new(int)
			}
		case part == "private":
			ret.private = true
			if ret.tag == nil {
				ret.tag = new(int)
			}
		case part == "omitempty":
			ret.omitEmpty = true
		}
	}
	return
}

// 给定一个反射得到的 Go 类型，getUniversalType 返回默认的标签编号
// 以及预期的复合标志。
func getUniversalType(t reflect.Type) (matchAny bool, tagNumber int, isCompound, ok bool) {
	switch t {
	case rawValueType:
		return true, -1, false, true
	case objectIdentifierType:
		return false, TagOID, false, true
	case bitStringType:
		return false, TagBitString, false, true
	case timeType:
		return false, TagUTCTime, false, true
	case enumeratedType:
		return false, TagEnum, false, true
	case bigIntType:
		return false, TagInteger, false, true
	}
	switch t.Kind() {
	case reflect.Bool:
		return false, TagBoolean, false, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return false, TagInteger, false, true
	case reflect.Struct:
		return false, TagSequence, true, true
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return false, TagOctetString, false, true
		}
		if strings.HasSuffix(t.Name(), "SET") {
			return false, TagSet, true, true
		}
		return false, TagSequence, true, true
	case reflect.String:
		return false, TagPrintableString, false, true
	}
	return false, 0, false, false
}
