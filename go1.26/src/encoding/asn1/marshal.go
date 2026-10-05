// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asn1

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"time"
	"unicode/utf8"
)

var (
	byte00Encoder encoder = byteEncoder(0x00)
	byteFFEncoder encoder = byteEncoder(0xff)
)

// encoder 表示一个等待编组的 ASN.1 元素。
type encoder interface {
	// Len 返回编组此元素所需的字节数。
	Len() int
	// Encode 通过向 dst 写入 Len() 个字节来编码此元素。
	Encode(dst []byte)
}

type byteEncoder byte

func (c byteEncoder) Len() int {
	return 1
}

func (c byteEncoder) Encode(dst []byte) {
	dst[0] = byte(c)
}

type bytesEncoder []byte

func (b bytesEncoder) Len() int {
	return len(b)
}

func (b bytesEncoder) Encode(dst []byte) {
	if copy(dst, b) != len(b) {
		panic("internal error")
	}
}

type stringEncoder string

func (s stringEncoder) Len() int {
	return len(s)
}

func (s stringEncoder) Encode(dst []byte) {
	if copy(dst, s) != len(s) {
		panic("internal error")
	}
}

type multiEncoder []encoder

func (m multiEncoder) Len() int {
	var size int
	for _, e := range m {
		size += e.Len()
	}
	return size
}

func (m multiEncoder) Encode(dst []byte) {
	var off int
	for _, e := range m {
		e.Encode(dst[off:])
		off += e.Len()
	}
}

type setEncoder []encoder

func (s setEncoder) Len() int {
	var size int
	for _, e := range s {
		size += e.Len()
	}
	return size
}

func (s setEncoder) Encode(dst []byte) {
	// 依据 X690 第 11.6 节：set-of 值的各分量值的编码应按升序出现，
	// 编码按八位组字符串进行比较，较短的分子在其尾端
	// 用 0 八位组补齐。
	//
	// 我们先将每个元素编码为其 TLV 编码，然后使用
	// octetSort 得到 X690 DER 规则所期望的排序，再
	// 将排序后的编码写出到 dst。
	l := make([][]byte, len(s))
	for i, e := range s {
		l[i] = make([]byte, e.Len())
		e.Encode(l[i])
	}

	// 由于我们使用 bytes.Compare 比较 TLV 编码，我们
	// 无需像 X690 所建议的那样将 s[i] 和 s[j] 右补齐到相同长度。
	// 如果 len(s[i]) < len(s[j])，s[i] 的长度八位组（即第一个起决定作用的
	// 字节）本身就会小于 s[j] 的长度八位组。这让我们可以跳过
	// 补齐步骤。
	slices.SortFunc(l, bytes.Compare)

	var off int
	for _, b := range l {
		copy(dst[off:], b)
		off += len(b)
	}
}

type taggedEncoder struct {
	// scratch 包含用于编码元素标签和长度的临时空间，
	// 以避免额外的分配。
	scratch [8]byte
	tag     encoder
	body    encoder
}

func (t *taggedEncoder) Len() int {
	return t.tag.Len() + t.body.Len()
}

func (t *taggedEncoder) Encode(dst []byte) {
	t.tag.Encode(dst)
	t.body.Encode(dst[t.tag.Len():])
}

type int64Encoder int64

func (i int64Encoder) Len() int {
	n := 1

	for i > 127 {
		n++
		i >>= 8
	}

	for i < -128 {
		n++
		i >>= 8
	}

	return n
}

func (i int64Encoder) Encode(dst []byte) {
	n := i.Len()

	for j := 0; j < n; j++ {
		dst[j] = byte(i >> uint((n-1-j)*8))
	}
}

func base128IntLength(n int64) int {
	if n == 0 {
		return 1
	}

	l := 0
	for i := n; i > 0; i >>= 7 {
		l++
	}

	return l
}

func appendBase128Int(dst []byte, n int64) []byte {
	l := base128IntLength(n)

	for i := l - 1; i >= 0; i-- {
		o := byte(n >> uint(i*7))
		o &= 0x7f
		if i != 0 {
			o |= 0x80
		}

		dst = append(dst, o)
	}

	return dst
}

func makeBigInt(n *big.Int) (encoder, error) {
	if n == nil {
		return nil, StructuralError{"empty integer"}
	}

	if n.Sign() < 0 {
		// 负数必须转换为二进制补码形式。因此我们会取反并减 1。
		// 如果最高有效位未设置，那么我们需要在开头
		// 用 0xff 补齐，以保持该数为负数。
		nMinus1 := new(big.Int).Neg(n)
		nMinus1.Sub(nMinus1, bigOne)
		bytes := nMinus1.Bytes()
		for i := range bytes {
			bytes[i] ^= 0xff
		}
		if len(bytes) == 0 || bytes[0]&0x80 == 0 {
			return multiEncoder([]encoder{byteFFEncoder, bytesEncoder(bytes)}), nil
		}
		return bytesEncoder(bytes), nil
	} else if n.Sign() == 0 {
		// 零被写为单个 0 字节，而不是没有字节。
		return byte00Encoder, nil
	} else {
		bytes := n.Bytes()
		if len(bytes) > 0 && bytes[0]&0x80 != 0 {
			// 我们必须用 0x00 补齐它，以防止它
			// 看起来像负数。
			return multiEncoder([]encoder{byte00Encoder, bytesEncoder(bytes)}), nil
		}
		return bytesEncoder(bytes), nil
	}
}

func appendLength(dst []byte, i int) []byte {
	n := lengthLength(i)

	for ; n > 0; n-- {
		dst = append(dst, byte(i>>uint((n-1)*8)))
	}

	return dst
}

func lengthLength(i int) (numBytes int) {
	numBytes = 1
	for i > 255 {
		numBytes++
		i >>= 8
	}
	return
}

func appendTagAndLength(dst []byte, t tagAndLength) []byte {
	b := uint8(t.class) << 6
	if t.isCompound {
		b |= 0x20
	}
	if t.tag >= 31 {
		b |= 0x1f
		dst = append(dst, b)
		dst = appendBase128Int(dst, int64(t.tag))
	} else {
		b |= uint8(t.tag)
		dst = append(dst, b)
	}

	if t.length >= 128 {
		l := lengthLength(t.length)
		dst = append(dst, 0x80|byte(l))
		dst = appendLength(dst, t.length)
	} else {
		dst = append(dst, byte(t.length))
	}

	return dst
}

type bitStringEncoder BitString

func (b bitStringEncoder) Len() int {
	return len(b.Bytes) + 1
}

func (b bitStringEncoder) Encode(dst []byte) {
	dst[0] = byte((8 - b.BitLength%8) % 8)
	if copy(dst[1:], b.Bytes) != len(b.Bytes) {
		panic("internal error")
	}
}

type oidEncoder []int

func (oid oidEncoder) Len() int {
	l := base128IntLength(int64(oid[0]*40 + oid[1]))
	for i := 2; i < len(oid); i++ {
		l += base128IntLength(int64(oid[i]))
	}
	return l
}

func (oid oidEncoder) Encode(dst []byte) {
	dst = appendBase128Int(dst[:0], int64(oid[0]*40+oid[1]))
	for i := 2; i < len(oid); i++ {
		dst = appendBase128Int(dst, int64(oid[i]))
	}
}

func makeObjectIdentifier(oid []int) (e encoder, err error) {
	if len(oid) < 2 || oid[0] > 2 || (oid[0] < 2 && oid[1] >= 40) {
		return nil, StructuralError{"invalid object identifier"}
	}

	return oidEncoder(oid), nil
}

func makePrintableString(s string) (e encoder, err error) {
	for i := 0; i < len(s); i++ {
		// 星号在 PrintableString 中经常被使用，尽管它是无效的。
		// 如果专门请求了 PrintableString，那么这段代码允许星号。
		// 与号在解析时由于少数 CA 证书而被允许，
		// 然而在生成新证书时它会被拒绝。
		if !isPrintable(s[i], allowAsterisk, rejectAmpersand) {
			return nil, StructuralError{"PrintableString contains invalid character"}
		}
	}

	return stringEncoder(s), nil
}

func makeIA5String(s string) (e encoder, err error) {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return nil, StructuralError{"IA5String contains invalid character"}
		}
	}

	return stringEncoder(s), nil
}

func makeNumericString(s string) (e encoder, err error) {
	for i := 0; i < len(s); i++ {
		if !isNumeric(s[i]) {
			return nil, StructuralError{"NumericString contains invalid character"}
		}
	}

	return stringEncoder(s), nil
}

func makeUTF8String(s string) encoder {
	return stringEncoder(s)
}

func appendTwoDigits(dst []byte, v int) []byte {
	return append(dst, byte('0'+(v/10)%10), byte('0'+v%10))
}

func appendFourDigits(dst []byte, v int) []byte {
	return append(dst,
		byte('0'+(v/1000)%10),
		byte('0'+(v/100)%10),
		byte('0'+(v/10)%10),
		byte('0'+v%10))
}

func outsideUTCRange(t time.Time) bool {
	year := t.Year()
	return year < 1950 || year >= 2050
}

func makeUTCTime(t time.Time) (e encoder, err error) {
	dst := make([]byte, 0, 18)

	dst, err = appendUTCTime(dst, t)
	if err != nil {
		return nil, err
	}

	return bytesEncoder(dst), nil
}

func makeGeneralizedTime(t time.Time) (e encoder, err error) {
	dst := make([]byte, 0, 20)

	dst, err = appendGeneralizedTime(dst, t)
	if err != nil {
		return nil, err
	}

	return bytesEncoder(dst), nil
}

func appendUTCTime(dst []byte, t time.Time) (ret []byte, err error) {
	year := t.Year()

	switch {
	case 1950 <= year && year < 2000:
		dst = appendTwoDigits(dst, year-1900)
	case 2000 <= year && year < 2050:
		dst = appendTwoDigits(dst, year-2000)
	default:
		return nil, StructuralError{"cannot represent time as UTCTime"}
	}

	return appendTimeCommon(dst, t), nil
}

func appendGeneralizedTime(dst []byte, t time.Time) (ret []byte, err error) {
	year := t.Year()
	if year < 0 || year > 9999 {
		return nil, StructuralError{"cannot represent time as GeneralizedTime"}
	}

	dst = appendFourDigits(dst, year)

	return appendTimeCommon(dst, t), nil
}

func appendTimeCommon(dst []byte, t time.Time) []byte {
	_, month, day := t.Date()

	dst = appendTwoDigits(dst, int(month))
	dst = appendTwoDigits(dst, day)

	hour, min, sec := t.Clock()

	dst = appendTwoDigits(dst, hour)
	dst = appendTwoDigits(dst, min)
	dst = appendTwoDigits(dst, sec)

	_, offset := t.Zone()

	switch {
	case offset/60 == 0:
		return append(dst, 'Z')
	case offset > 0:
		dst = append(dst, '+')
	case offset < 0:
		dst = append(dst, '-')
	}

	offsetMinutes := offset / 60
	if offsetMinutes < 0 {
		offsetMinutes = -offsetMinutes
	}

	dst = appendTwoDigits(dst, offsetMinutes/60)
	dst = appendTwoDigits(dst, offsetMinutes%60)

	return dst
}

func stripTagAndLength(in []byte) []byte {
	_, offset, err := parseTagAndLength(in, 0)
	if err != nil {
		return in
	}
	return in[offset:]
}

func makeBody(value reflect.Value, params fieldParameters) (e encoder, err error) {
	switch value.Type() {
	case flagType:
		return bytesEncoder(nil), nil
	case timeType:
		t, _ := reflect.TypeAssert[time.Time](value)
		if params.timeType == TagGeneralizedTime || outsideUTCRange(t) {
			return makeGeneralizedTime(t)
		}
		return makeUTCTime(t)
	case bitStringType:
		v, _ := reflect.TypeAssert[BitString](value)
		return bitStringEncoder(v), nil
	case objectIdentifierType:
		v, _ := reflect.TypeAssert[ObjectIdentifier](value)
		return makeObjectIdentifier(v)
	case bigIntType:
		v, _ := reflect.TypeAssert[*big.Int](value)
		return makeBigInt(v)
	}

	switch v := value; v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return byteFFEncoder, nil
		}
		return byte00Encoder, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int64Encoder(v.Int()), nil
	case reflect.Struct:
		t := v.Type()

		for i := 0; i < t.NumField(); i++ {
			if !t.Field(i).IsExported() {
				return nil, StructuralError{"struct contains unexported fields"}
			}
		}

		startingField := 0

		n := t.NumField()
		if n == 0 {
			return bytesEncoder(nil), nil
		}

		// 如果结构体的第一个元素是非空的
		// RawContents，那么我们就不必序列化其余内容。
		if t.Field(0).Type == rawContentsType {
			s := v.Field(0)
			if s.Len() > 0 {
				bytes := s.Bytes()
				/* RawContents 将包含标签和
				 * 长度字段，但我们自己也会写入
				 * 这些内容，因此将它们从
				 * bytes 中剥离 */
				return bytesEncoder(stripTagAndLength(bytes)), nil
			}

			startingField = 1
		}

		switch n1 := n - startingField; n1 {
		case 0:
			return bytesEncoder(nil), nil
		case 1:
			return makeField(v.Field(startingField), parseFieldParameters(t.Field(startingField).Tag.Get("asn1")))
		default:
			m := make([]encoder, n1)
			for i := 0; i < n1; i++ {
				m[i], err = makeField(v.Field(i+startingField), parseFieldParameters(t.Field(i+startingField).Tag.Get("asn1")))
				if err != nil {
					return nil, err
				}
			}

			return multiEncoder(m), nil
		}
	case reflect.Slice:
		sliceType := v.Type()
		if sliceType.Elem().Kind() == reflect.Uint8 {
			return bytesEncoder(v.Bytes()), nil
		}

		var fp fieldParameters

		switch l := v.Len(); l {
		case 0:
			return bytesEncoder(nil), nil
		case 1:
			return makeField(v.Index(0), fp)
		default:
			m := make([]encoder, l)

			for i := 0; i < l; i++ {
				m[i], err = makeField(v.Index(i), fp)
				if err != nil {
					return nil, err
				}
			}

			if params.set {
				return setEncoder(m), nil
			}
			return multiEncoder(m), nil
		}
	case reflect.String:
		switch params.stringType {
		case TagIA5String:
			return makeIA5String(v.String())
		case TagPrintableString:
			return makePrintableString(v.String())
		case TagNumericString:
			return makeNumericString(v.String())
		default:
			return makeUTF8String(v.String()), nil
		}
	}

	return nil, StructuralError{"unknown Go type"}
}

func makeField(v reflect.Value, params fieldParameters) (e encoder, err error) {
	if !v.IsValid() {
		return nil, fmt.Errorf("asn1: cannot marshal nil value")
	}
	// 如果该字段是 interface{}，则递归进入其中。
	if v.Kind() == reflect.Interface && v.Type().NumMethod() == 0 {
		return makeField(v.Elem(), params)
	}

	if v.Kind() == reflect.Slice && v.Len() == 0 && params.omitEmpty {
		return bytesEncoder(nil), nil
	}

	if params.optional && params.defaultValue != nil && canHaveDefaultValue(v.Kind()) {
		defaultValue := reflect.New(v.Type()).Elem()
		defaultValue.SetInt(*params.defaultValue)

		if reflect.DeepEqual(v.Interface(), defaultValue.Interface()) {
			return bytesEncoder(nil), nil
		}
	}

	// 如果未给出默认值，则假定该类型的零值为默认值。
	// 这显然不一定正确，但这是 Go 一贯的做法。
	if params.optional && params.defaultValue == nil {
		if reflect.DeepEqual(v.Interface(), reflect.Zero(v.Type()).Interface()) {
			return bytesEncoder(nil), nil
		}
	}

	if v.Type() == rawValueType {
		rv, _ := reflect.TypeAssert[RawValue](v)
		if len(rv.FullBytes) != 0 {
			return bytesEncoder(rv.FullBytes), nil
		}

		t := new(taggedEncoder)

		t.tag = bytesEncoder(appendTagAndLength(t.scratch[:0], tagAndLength{rv.Class, rv.Tag, len(rv.Bytes), rv.IsCompound}))
		t.body = bytesEncoder(rv.Bytes)

		return t, nil
	}

	matchAny, tag, isCompound, ok := getUniversalType(v.Type())
	if !ok || matchAny {
		return nil, StructuralError{fmt.Sprintf("unknown Go type: %v", v.Type())}
	}

	if params.timeType != 0 && tag != TagUTCTime {
		return nil, StructuralError{"explicit time type given to non-time member"}
	}

	if params.stringType != 0 && tag != TagPrintableString {
		return nil, StructuralError{"explicit string type given to non-string member"}
	}

	switch tag {
	case TagPrintableString:
		if params.stringType == 0 {
			// 这是一个没有显式字符串类型的字符串。如果字符串中的
			// 字符集足够有限，我们将使用 PrintableString，
			// 否则将使用 UTF8String。
			for _, r := range v.String() {
				if r >= utf8.RuneSelf || !isPrintable(byte(r), rejectAsterisk, rejectAmpersand) {
					if !utf8.ValidString(v.String()) {
						return nil, errors.New("asn1: string not valid UTF-8")
					}
					tag = TagUTF8String
					break
				}
			}
		} else {
			tag = params.stringType
		}
	case TagUTCTime:
		t, _ := reflect.TypeAssert[time.Time](v)
		if params.timeType == TagGeneralizedTime || outsideUTCRange(t) {
			tag = TagGeneralizedTime
		}
	}

	if params.set {
		if tag != TagSequence {
			return nil, StructuralError{"non sequence tagged as set"}
		}
		tag = TagSet
	}

	// makeField 可能会针对一个应被视为 SET 但未设置 params.set 的切片
	// 被调用，例如当使用带有 SET 类型名称后缀的切片时。
	// 在这种情况下 getUniversalType 返回 TagSet，但 makeBody 并不知道这一点，
	// 因此会把该切片当作序列处理。为解决这个问题，我们设置 params.set。
	if tag == TagSet && !params.set {
		params.set = true
	}

	t := new(taggedEncoder)

	t.body, err = makeBody(v, params)
	if err != nil {
		return nil, err
	}

	bodyLen := t.body.Len()

	class := ClassUniversal
	if params.tag != nil {
		if params.application {
			class = ClassApplication
		} else if params.private {
			class = ClassPrivate
		} else {
			class = ClassContextSpecific
		}

		if params.explicit {
			t.tag = bytesEncoder(appendTagAndLength(t.scratch[:0], tagAndLength{ClassUniversal, tag, bodyLen, isCompound}))

			tt := new(taggedEncoder)

			tt.body = t

			tt.tag = bytesEncoder(appendTagAndLength(tt.scratch[:0], tagAndLength{
				class:      class,
				tag:        *params.tag,
				length:     bodyLen + t.tag.Len(),
				isCompound: true,
			}))

			return tt, nil
		}

		// 隐式标签。
		tag = *params.tag
	}

	t.tag = bytesEncoder(appendTagAndLength(t.scratch[:0], tagAndLength{class, tag, bodyLen, isCompound}))

	return t, nil
}

// Marshal 返回 val 的 ASN.1 编码。
//
// 除 Unmarshal 可识别的结构体标签外，还可以使用以下标签：
//
//	ia5:         使字符串按 ASN.1 IA5String 值编组
//	omitempty:   使空切片被跳过
//	printable:   使字符串按 ASN.1 PrintableString 值编组
//	utf8:        使字符串按 ASN.1 UTF8String 值编组
//	numeric:     使字符串按 ASN.1 NumericString 值编组
//	utc:         使 time.Time 按 ASN.1 UTCTime 值编组
//	generalized: 使 time.Time 按 ASN.1 GeneralizedTime 值编组
func Marshal(val any) ([]byte, error) {
	return MarshalWithParams(val, "")
}

// MarshalWithParams 允许为顶层元素指定字段参数。
// params 的形式与字段标签相同。
func MarshalWithParams(val any, params string) ([]byte, error) {
	e, err := makeField(reflect.ValueOf(val), parseFieldParameters(params))
	if err != nil {
		return nil, err
	}
	b := make([]byte, e.Len())
	e.Encode(b)
	return b, nil
}
