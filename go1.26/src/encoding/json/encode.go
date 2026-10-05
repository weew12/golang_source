// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.jsonv2

// 包 json 实现了 RFC 7159 中定义的 JSON 的编码与解码。
// JSON 与 Go 值之间的映射关系在 Marshal 和 Unmarshal 函数的文档中描述。
//
// 有关本包的介绍，请参阅 "JSON and Go"：
// https://golang.org/doc/articles/json_and_go.html
//
// # 安全性考量
//
// JSON 标准（RFC 7159）对若干解析器行为的定义较为宽松。因此，许多 JSON
// 解析器在各种场景下的行为有所不同。解析器的这些差异意味着，使用多个独立
// JSON 解析器实现的系统可能会以不同方式解析同一个 JSON 对象。
//
// 出于安全目的而依赖 JSON 对象被一致解析的系统，应当仔细理解本解析器的
// 行为，以及这些行为如何可能导致与其他解析器实现的互操作性问题。
//
// 由于 Go 向后兼容性承诺（https://go.dev/doc/go1compat），本包表现出一些
// 可能导致互操作性问题但无法更改的行为。尤其是以下解析行为可能引发问题：
//
//   - 如果 JSON 对象包含重复的键，键会按观察到的顺序处理，这意味着后出现的
//     值将替换或合并到先前的值中，具体取决于字段类型（特别是映射和结构体会
//     将值合并，而其他类型则替换值）。
//   - 将 JSON 对象解析到 Go 结构体时，键以不区分大小写的方式来匹配。
//   - 将 JSON 对象解析到 Go 结构体时，JSON 对象中的未知键会被忽略
//     （除非使用了 [Decoder] 且已调用 [Decoder.DisallowUnknownFields]）。
//   - JSON 字符串中的无效 UTF-8 字节会被替换为 Unicode 替换字符。
//   - 大整数 JSON 数字在反序列化到浮点类型时会丢失精度。
package json

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/base64"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
	_ "unsafe" // 用于 linkname
)

// Marshal 返回 v 的 JSON 编码。
//
// Marshal 会递归地遍历值 v。
// 如果遇到的值实现了 [Marshaler] 且不是 nil 指针，Marshal 会调用
// [Marshaler.MarshalJSON] 来生成 JSON。如果不存在 [Marshaler.MarshalJSON]
// 方法，但该值实现了 [encoding.TextMarshaler]，Marshal 会调用
// [encoding.TextMarshaler.MarshalText] 并将结果编码为 JSON 字符串。
// nil 指针例外并非严格必要，而是为了模仿 [Unmarshaler.UnmarshalJSON]
// 行为中类似且必要的例外。
//
// 否则，Marshal 使用以下依赖于类型的默认编码：
//
// 布尔值编码为 JSON 布尔值。
//
// 浮点、整数和 [Number] 值编码为 JSON 数字。
// NaN 和 +/-Inf 值将返回一个 [UnsupportedValueError]。
//
// 字符串值编码为强制转换为有效 UTF-8 的 JSON 字符串，
// 无效字节会被替换为 Unicode 替换字符。
// 为了使 JSON 可以安全地嵌入 HTML <script> 标签内，
// 字符串使用 [HTMLEscape] 编码，
// 它会将 "<"、">"、"&"、U+2028 和 U+2029 转义为
// "\u003c"、"\u003e"、"\u0026"、"\u2028" 和 "\u2029"。
// 使用 [Encoder] 时可以通过调用 [Encoder.SetEscapeHTML](false)
// 禁用这种替换。
//
// 数组和切片值编码为 JSON 数组，但 []byte 编码为 base64 编码的字符串，
// 而 nil 切片编码为 JSON null 值。
//
// 结构体值编码为 JSON 对象。
// 每个导出的结构体字段都会成为对象的一个成员，使用字段名作为对象键，
// 除非该字段因下列原因之一而被省略。
//
// 每个结构体字段的编码都可以通过结构体字段标签中 "json" 键下存储的格式
// 字符串进行定制。该格式字符串给出字段的名称，后面可能跟着一个以逗号分隔的
// 选项列表。名称可以为空，以便在不覆盖默认字段名的情况下指定选项。
//
// "omitempty" 选项指定：如果字段具有空值，则应从编码中省略该字段。
// 空值定义为 false、0、nil 指针、nil 接口值，以及长度为零的任意数组、
// 切片、映射或字符串。
//
// 作为一种特殊情况，如果字段标签为 "-"，则该字段始终被省略。
// 注意，名称为 "-" 的字段仍可以通过标签 "-," 生成。
//
// 结构体字段标签及其含义的示例：
//
//	// 字段在 JSON 中以键 "myName" 出现。
//	Field int `json:"myName"`
//
//	// 字段在 JSON 中以键 "myName" 出现，
//	// 并且如果其值为空（如上定义），则从对象中省略该字段。
//	Field int `json:"myName,omitempty"`
//
//	// 字段在 JSON 中以键 "Field"（默认值）出现，
//	// 但如果为空则跳过该字段。
//	// 注意开头的逗号。
//	Field int `json:",omitempty"`
//
//	// 该字段被本包忽略。
//	Field int `json:"-"`
//
//	// 字段在 JSON 中以键 "-" 出现。
//	Field int `json:"-,"`
//
// "omitzero" 选项指定：如果字段具有零值，则应从编码中省略该字段，
// 具体规则如下：
//
// 1) 如果字段类型具有 "IsZero() bool" 方法，将使用该方法判断该值是否为零。
//
// 2) 否则，如果该值是其类型的零值，则认为它为零。
//
// 如果同时指定了 "omitempty" 和 "omitzero"，则只要值为空或为零（或两者
// 皆是），该字段就会被省略。
//
// "string" 选项表示字段以 JSON 编码字符串的形式存储为 JSON。
// 它仅适用于字符串、浮点、整数或布尔类型的字段。这种额外的编码层级
// 有时用于与 JavaScript 程序通信：
//
//	Int64String int64 `json:",string"`
//
// 如果键名是一个非空字符串，且仅由 Unicode 字母、数字以及除引号、
// 反斜杠和逗号之外的 ASCII 标点组成，则使用该键名。
//
// 内嵌结构体字段通常会被序列化，就好像其内部导出的字段是外部结构体的
// 字段一样，并遵循通常的 Go 可见性规则，但按下一段所述进行修正。
// JSON 标签中给出了名称的匿名字段会以该名称处理，而不是作为匿名字段。
// 接口类型的匿名字段会以该类型名作为其名称处理，而不是作为匿名字段。
//
// 在决定序列化或反序列化哪个字段时，结构体字段的 Go 可见性规则会针对
// JSON 进行修正。如果同一层级存在多个字段，且该层级是嵌套最浅的层级
// （因此也是通常 Go 规则所选择的嵌套层级），则适用以下附加规则：
//
// 1) 在这些字段中，如果有任何字段带有 JSON 标签，则只考虑带标签的字段，
// 即使存在多个原本会冲突的未带标签字段。
//
// 2) 如果恰好只有一个字段（根据第一条规则带或不带标签），则选择该字段。
//
// 3) 否则存在多个字段，则全部忽略；不会发生错误。
//
// 对匿名字段结构体的处理是 Go 1.1 的新特性。
// 在 Go 1.1 之前，匿名字段结构体会被忽略。要在当前版本和更早版本中
// 都强制忽略某个匿名字段结构体，请给该字段设置 JSON 标签 "-"。
//
// 映射值编码为 JSON 对象。映射的键类型必须是字符串、整数类型，或实现了
// [encoding.TextMarshaler]。映射键会被排序并用作 JSON 对象键，具体应用
// 以下规则，并遵循上文针对字符串值所述的 UTF-8 强制转换：
//   - 任意字符串类型的键直接使用
//   - 实现了 [encoding.TextMarshaler] 的键会被序列化
//   - 整数键会被转换为字符串
//
// 指针值编码为所指向的值。
// nil 指针编码为 JSON null 值。
//
// 接口值编码为接口中包含的值。
// nil 接口值编码为 JSON null 值。
//
// 通道、复数和函数值无法编码为 JSON。
// 尝试编码此类值会导致 Marshal 返回一个 [UnsupportedTypeError]。
//
// JSON 无法表示循环数据结构，Marshal 也不处理它们。
// 将循环结构传递给 Marshal 会导致错误。
func Marshal(v any) ([]byte, error) {
	e := newEncodeState()
	defer encodeStatePool.Put(e)

	err := e.marshal(v, encOpts{escapeHTML: true})
	if err != nil {
		return nil, err
	}
	buf := append([]byte(nil), e.Bytes()...)

	return buf, nil
}

// MarshalIndent 类似于 [Marshal]，但会应用 [Indent] 来格式化输出。
// 输出中的每个 JSON 元素都会在新的一行开始，该行以 prefix 开头，后面根据
// 缩进嵌套层级跟着 indent 的一份或多份副本。
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	b2 := make([]byte, 0, indentGrowthFactor*len(b))
	b2, err = appendIndent(b2, b, prefix, indent)
	if err != nil {
		return nil, err
	}
	return b2, nil
}

// Marshaler 是由能够将自身序列化为有效 JSON 的类型所实现的接口。
type Marshaler interface {
	MarshalJSON() ([]byte, error)
}

// UnsupportedTypeError 在尝试编码不受支持的值类型时由 [Marshal] 返回。
type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return "json: unsupported type: " + e.Type.String()
}

// UnsupportedValueError 在尝试编码不受支持的值时由 [Marshal] 返回。
type UnsupportedValueError struct {
	Value reflect.Value
	Str   string
}

func (e *UnsupportedValueError) Error() string {
	return "json: unsupported value: " + e.Str
}

// 在 Go 1.2 之前，当尝试编码包含无效 UTF-8 序列的字符串值时，
// [Marshal] 会返回一个 InvalidUTF8Error。
// 从 Go 1.2 起，[Marshal] 改为通过将无效字节替换为 Unicode 替换字符
// U+FFFD 来将该字符串强制转换为有效 UTF-8。
//
// Deprecated: 不再使用；保留仅为保持兼容性。
type InvalidUTF8Error struct {
	S string // 导致该错误的整个字符串值
}

func (e *InvalidUTF8Error) Error() string {
	return "json: invalid UTF-8 in string: " + strconv.Quote(e.S)
}

// MarshalerError 表示调用 [Marshaler.MarshalJSON] 或
// [encoding.TextMarshaler.MarshalText] 方法时产生的错误。
type MarshalerError struct {
	Type       reflect.Type
	Err        error
	sourceFunc string
}

func (e *MarshalerError) Error() string {
	srcFunc := e.sourceFunc
	if srcFunc == "" {
		srcFunc = "MarshalJSON"
	}
	return "json: error calling " + srcFunc +
		" for type " + e.Type.String() +
		": " + e.Err.Error()
}

// Unwrap 返回底层错误。
func (e *MarshalerError) Unwrap() error { return e.Err }

const hex = "0123456789abcdef"

// encodeState 将 JSON 编码到 bytes.Buffer 中。
type encodeState struct {
	bytes.Buffer // 累积的输出

	// 记录当前递归调用路径中已经见过的指针，以避免可能导致栈溢出的循环。
	// 只有当 ptrLevel 大于 startDetectingCyclesAfter 时才执行相对昂贵的
	// 映射操作，这样在嵌套指针深度合理时我们就跳过这些工作。
	ptrLevel uint
	ptrSeen  map[any]struct{}
}

const startDetectingCyclesAfter = 1000

var encodeStatePool sync.Pool

func newEncodeState() *encodeState {
	if v := encodeStatePool.Get(); v != nil {
		e := v.(*encodeState)
		e.Reset()
		if len(e.ptrSeen) > 0 {
			panic("ptrEncoder.encode should have emptied ptrSeen via defers")
		}
		e.ptrLevel = 0
		return e
	}
	return &encodeState{ptrSeen: make(map[any]struct{})}
}

// jsonError 是仅供内部使用的错误包装类型。
// 带错误的 panic 会被包装在 jsonError 中，以便顶层的 recover
// 能将本包有意触发的 panic 与其他 panic 区分开。
type jsonError struct{ error }

func (e *encodeState) marshal(v any, opts encOpts) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if je, ok := r.(jsonError); ok {
				err = je.error
			} else {
				panic(r)
			}
		}
	}()
	e.reflectValue(reflect.ValueOf(v), opts)
	return nil
}

// error 通过以 jsonError 包装的 err 触发 panic 来中止编码。
func (e *encodeState) error(err error) {
	panic(jsonError{err})
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

func (e *encodeState) reflectValue(v reflect.Value, opts encOpts) {
	valueEncoder(v)(e, v, opts)
}

type encOpts struct {
	// quoted 使基本类型字段被编码在 JSON 字符串内部。
	quoted bool
	// escapeHTML 使 '<'、'>' 和 '&' 在 JSON 字符串中被转义。
	escapeHTML bool
}

type encoderFunc func(e *encodeState, v reflect.Value, opts encOpts)

var encoderCache sync.Map // map[reflect.Type]encoderFunc

func valueEncoder(v reflect.Value) encoderFunc {
	if !v.IsValid() {
		return invalidValueEncoder
	}
	return typeEncoder(v.Type())
}

func typeEncoder(t reflect.Type) encoderFunc {
	if fi, ok := encoderCache.Load(t); ok {
		return fi.(encoderFunc)
	}

	// 为了处理递归类型，我们在构建之前先在映射中放入一个间接函数。
	// 如果类型是递归的，对该类型的第二次查找将返回该间接函数。
	//
	// 该间接函数仅用于递归类型，以及在并发调用 typeEncoder 的短暂期间使用。
	indirect := sync.OnceValue(func() encoderFunc {
		return newTypeEncoder(t, true)
	})
	fi, loaded := encoderCache.LoadOrStore(t, encoderFunc(func(e *encodeState, v reflect.Value, opts encOpts) {
		indirect()(e, v, opts)
	}))
	if loaded {
		return fi.(encoderFunc)
	}

	f := indirect()
	encoderCache.Store(t, f)
	return f
}

var (
	marshalerType     = reflect.TypeFor[Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// newTypeEncoder 为某个类型构造一个 encoderFunc。
// 仅当 allowAddr 为 true 时，返回的编码器才会检查 CanAddr。
func newTypeEncoder(t reflect.Type, allowAddr bool) encoderFunc {
	// 如果我们有一个非指针值，其类型以值接收者实现了 Marshaler，
	// 那么最好取该值的地址——否则在将该值转换为接口时会产生一次分配。
	if t.Kind() != reflect.Pointer && allowAddr && reflect.PointerTo(t).Implements(marshalerType) {
		return newCondAddrEncoder(addrMarshalerEncoder, newTypeEncoder(t, false))
	}
	if t.Implements(marshalerType) {
		return marshalerEncoder
	}
	if t.Kind() != reflect.Pointer && allowAddr && reflect.PointerTo(t).Implements(textMarshalerType) {
		return newCondAddrEncoder(addrTextMarshalerEncoder, newTypeEncoder(t, false))
	}
	if t.Implements(textMarshalerType) {
		return textMarshalerEncoder
	}

	switch t.Kind() {
	case reflect.Bool:
		return boolEncoder
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intEncoder
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return uintEncoder
	case reflect.Float32:
		return float32Encoder
	case reflect.Float64:
		return float64Encoder
	case reflect.String:
		return stringEncoder
	case reflect.Interface:
		return interfaceEncoder
	case reflect.Struct:
		return newStructEncoder(t)
	case reflect.Map:
		return newMapEncoder(t)
	case reflect.Slice:
		return newSliceEncoder(t)
	case reflect.Array:
		return newArrayEncoder(t)
	case reflect.Pointer:
		return newPtrEncoder(t)
	default:
		return unsupportedTypeEncoder
	}
}

func invalidValueEncoder(e *encodeState, v reflect.Value, _ encOpts) {
	e.WriteString("null")
}

func marshalerEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		e.WriteString("null")
		return
	}
	m, ok := reflect.TypeAssert[Marshaler](v)
	if !ok {
		e.WriteString("null")
		return
	}
	b, err := m.MarshalJSON()
	if err == nil {
		e.Grow(len(b))
		out := e.AvailableBuffer()
		out, err = appendCompact(out, b, opts.escapeHTML)
		e.Buffer.Write(out)
	}
	if err != nil {
		e.error(&MarshalerError{v.Type(), err, "MarshalJSON"})
	}
}

func addrMarshalerEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	va := v.Addr()
	if va.IsNil() {
		e.WriteString("null")
		return
	}
	m, _ := reflect.TypeAssert[Marshaler](va)
	b, err := m.MarshalJSON()
	if err == nil {
		e.Grow(len(b))
		out := e.AvailableBuffer()
		out, err = appendCompact(out, b, opts.escapeHTML)
		e.Buffer.Write(out)
	}
	if err != nil {
		e.error(&MarshalerError{v.Type(), err, "MarshalJSON"})
	}
}

func textMarshalerEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		e.WriteString("null")
		return
	}
	m, ok := reflect.TypeAssert[encoding.TextMarshaler](v)
	if !ok {
		e.WriteString("null")
		return
	}
	b, err := m.MarshalText()
	if err != nil {
		e.error(&MarshalerError{v.Type(), err, "MarshalText"})
	}
	e.Write(appendString(e.AvailableBuffer(), b, opts.escapeHTML))
}

func addrTextMarshalerEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	va := v.Addr()
	if va.IsNil() {
		e.WriteString("null")
		return
	}
	m, _ := reflect.TypeAssert[encoding.TextMarshaler](va)
	b, err := m.MarshalText()
	if err != nil {
		e.error(&MarshalerError{v.Type(), err, "MarshalText"})
	}
	e.Write(appendString(e.AvailableBuffer(), b, opts.escapeHTML))
}

func boolEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	b := e.AvailableBuffer()
	b = mayAppendQuote(b, opts.quoted)
	b = strconv.AppendBool(b, v.Bool())
	b = mayAppendQuote(b, opts.quoted)
	e.Write(b)
}

func intEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	b := e.AvailableBuffer()
	b = mayAppendQuote(b, opts.quoted)
	b = strconv.AppendInt(b, v.Int(), 10)
	b = mayAppendQuote(b, opts.quoted)
	e.Write(b)
}

func uintEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	b := e.AvailableBuffer()
	b = mayAppendQuote(b, opts.quoted)
	b = strconv.AppendUint(b, v.Uint(), 10)
	b = mayAppendQuote(b, opts.quoted)
	e.Write(b)
}

type floatEncoder int // number of bits

func (bits floatEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	f := v.Float()
	if math.IsInf(f, 0) || math.IsNaN(f) {
		e.error(&UnsupportedValueError{v, strconv.FormatFloat(f, 'g', -1, int(bits))})
	}

	// 按照 ES6 数字到字符串转换的方式进行转换。
	// 这与大多数其他 JSON 生成器一致。
	// 参见 golang.org/issue/6384 和 golang.org/issue/14135。
	// 类似 fmt 的 %g，但指数截断值不同，
	// 且指数本身不填充为两位数字。
	b := e.AvailableBuffer()
	b = mayAppendQuote(b, opts.quoted)
	abs := math.Abs(f)
	fmt := byte('f')
	// 注意：必须对底层 float32 值使用 float32 比较，才能得到精确的截断值。
	if abs != 0 {
		if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			fmt = 'e'
		}
	}
	b = strconv.AppendFloat(b, f, fmt, -1, int(bits))
	if fmt == 'e' {
		// 将 e-09 整理为 e-9
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	b = mayAppendQuote(b, opts.quoted)
	e.Write(b)
}

var (
	float32Encoder = (floatEncoder(32)).encode
	float64Encoder = (floatEncoder(64)).encode
)

func stringEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	if v.Type() == numberType {
		numStr := v.String()
		// 在 Go1.5 中，空字符串编码为 "0"，虽然这不是有效的数字字面量，
		// 但为保持兼容性，我们在之后才检查有效性。
		if numStr == "" {
			numStr = "0" // Number 的零值
		}
		if !isValidNumber(numStr) {
			e.error(fmt.Errorf("json: invalid number literal %q", numStr))
		}
		b := e.AvailableBuffer()
		b = mayAppendQuote(b, opts.quoted)
		b = append(b, numStr...)
		b = mayAppendQuote(b, opts.quoted)
		e.Write(b)
		return
	}
	if opts.quoted {
		b := appendString(nil, v.String(), opts.escapeHTML)
		e.Write(appendString(e.AvailableBuffer(), b, false)) // 无需再次转义，因为它已经被转义
	} else {
		e.Write(appendString(e.AvailableBuffer(), v.String(), opts.escapeHTML))
	}
}

func isValidNumber(s string) bool {
	// 本函数实现 JSON 数字语法。
	// 参见 https://tools.ietf.org/html/rfc7159#section-6
	// 以及 https://www.json.org/img/number.png

	if s == "" {
		return false
	}

	// 可选的 -
	if s[0] == '-' {
		s = s[1:]
		if s == "" {
			return false
		}
	}

	// 数字
	switch {
	default:
		return false

	case s[0] == '0':
		s = s[1:]

	case '1' <= s[0] && s[0] <= '9':
		s = s[1:]
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	}

	// . 后跟一个或多个数字。
	if len(s) >= 2 && s[0] == '.' && '0' <= s[1] && s[1] <= '9' {
		s = s[2:]
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	}

	// e 或 E，后跟可选的 - 或 +，
	// 以及一个或多个数字。
	if len(s) >= 2 && (s[0] == 'e' || s[0] == 'E') {
		s = s[1:]
		if s[0] == '+' || s[0] == '-' {
			s = s[1:]
			if s == "" {
				return false
			}
		}
		for len(s) > 0 && '0' <= s[0] && s[0] <= '9' {
			s = s[1:]
		}
	}

	// 确保我们已经到达末尾。
	return s == ""
}

func interfaceEncoder(e *encodeState, v reflect.Value, opts encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}
	e.reflectValue(v.Elem(), opts)
}

func unsupportedTypeEncoder(e *encodeState, v reflect.Value, _ encOpts) {
	e.error(&UnsupportedTypeError{v.Type()})
}

type structEncoder struct {
	fields structFields
}

type structFields struct {
	list         []field
	byExactName  map[string]*field
	byFoldedName map[string]*field
}

func (se structEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	next := byte('{')
FieldLoop:
	for i := range se.fields.list {
		f := &se.fields.list[i]

		// 通过跟随 f.index 找到嵌套的结构体字段。
		fv := v
		for _, i := range f.index {
			if fv.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue FieldLoop
				}
				fv = fv.Elem()
			}
			fv = fv.Field(i)
		}

		if (f.omitEmpty && isEmptyValue(fv)) ||
			(f.omitZero && (f.isZero == nil && fv.IsZero() || (f.isZero != nil && f.isZero(fv)))) {
			continue
		}
		e.WriteByte(next)
		next = ','
		if opts.escapeHTML {
			e.WriteString(f.nameEscHTML)
		} else {
			e.WriteString(f.nameNonEsc)
		}
		opts.quoted = f.quoted
		f.encoder(e, fv, opts)
	}
	if next == '{' {
		e.WriteString("{}")
	} else {
		e.WriteByte('}')
	}
}

func newStructEncoder(t reflect.Type) encoderFunc {
	se := structEncoder{fields: cachedTypeFields(t)}
	return se.encode
}

type mapEncoder struct {
	elemEnc encoderFunc
}

func (me mapEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}
	if e.ptrLevel++; e.ptrLevel > startDetectingCyclesAfter {
		// 我们已经嵌套了大量 ptrEncoder.encode 调用；
		// 开始检查是否遇到了指针循环。
		ptr := v.UnsafePointer()
		if _, ok := e.ptrSeen[ptr]; ok {
			e.error(&UnsupportedValueError{v, fmt.Sprintf("encountered a cycle via %s", v.Type())})
		}
		e.ptrSeen[ptr] = struct{}{}
		defer delete(e.ptrSeen, ptr)
	}
	e.WriteByte('{')

	// 提取并排序键。
	var (
		sv  = make([]reflectWithString, v.Len())
		mi  = v.MapRange()
		err error
	)
	for i := 0; mi.Next(); i++ {
		if sv[i].ks, err = resolveKeyName(mi.Key()); err != nil {
			e.error(fmt.Errorf("json: encoding error for type %q: %q", v.Type().String(), err.Error()))
		}
		sv[i].v = mi.Value()
	}
	slices.SortFunc(sv, func(i, j reflectWithString) int {
		return strings.Compare(i.ks, j.ks)
	})

	for i, kv := range sv {
		if i > 0 {
			e.WriteByte(',')
		}
		e.Write(appendString(e.AvailableBuffer(), kv.ks, opts.escapeHTML))
		e.WriteByte(':')
		me.elemEnc(e, kv.v, opts)
	}
	e.WriteByte('}')
	e.ptrLevel--
}

func newMapEncoder(t reflect.Type) encoderFunc {
	switch t.Key().Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		if !t.Key().Implements(textMarshalerType) {
			return unsupportedTypeEncoder
		}
	}
	me := mapEncoder{typeEncoder(t.Elem())}
	return me.encode
}

func encodeByteSlice(e *encodeState, v reflect.Value, _ encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}

	s := v.Bytes()
	b := e.AvailableBuffer()
	b = append(b, '"')
	b = base64.StdEncoding.AppendEncode(b, s)
	b = append(b, '"')
	e.Write(b)
}

// sliceEncoder 只是包装一个 arrayEncoder，并检查以确保该值不为 nil。
type sliceEncoder struct {
	arrayEnc encoderFunc
}

func (se sliceEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}
	if e.ptrLevel++; e.ptrLevel > startDetectingCyclesAfter {
		// 我们已经嵌套了大量 ptrEncoder.encode 调用；
		// 开始检查是否遇到了指针循环。
		// 这里我们使用一个结构体来记住指向切片首个元素的指针及其长度。
		ptr := struct {
			ptr any // 始终是 unsafe.Pointer，但避免依赖 unsafe 包
			len int
		}{v.UnsafePointer(), v.Len()}
		if _, ok := e.ptrSeen[ptr]; ok {
			e.error(&UnsupportedValueError{v, fmt.Sprintf("encountered a cycle via %s", v.Type())})
		}
		e.ptrSeen[ptr] = struct{}{}
		defer delete(e.ptrSeen, ptr)
	}
	se.arrayEnc(e, v, opts)
	e.ptrLevel--
}

func newSliceEncoder(t reflect.Type) encoderFunc {
	// 字节切片得到特殊处理；数组则不会。
	if t.Elem().Kind() == reflect.Uint8 {
		p := reflect.PointerTo(t.Elem())
		if !p.Implements(marshalerType) && !p.Implements(textMarshalerType) {
			return encodeByteSlice
		}
	}
	enc := sliceEncoder{newArrayEncoder(t)}
	return enc.encode
}

type arrayEncoder struct {
	elemEnc encoderFunc
}

func (ae arrayEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	e.WriteByte('[')
	n := v.Len()
	for i := 0; i < n; i++ {
		if i > 0 {
			e.WriteByte(',')
		}
		ae.elemEnc(e, v.Index(i), opts)
	}
	e.WriteByte(']')
}

func newArrayEncoder(t reflect.Type) encoderFunc {
	enc := arrayEncoder{typeEncoder(t.Elem())}
	return enc.encode
}

type ptrEncoder struct {
	elemEnc encoderFunc
}

func (pe ptrEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	if v.IsNil() {
		e.WriteString("null")
		return
	}
	if e.ptrLevel++; e.ptrLevel > startDetectingCyclesAfter {
		// 我们已经嵌套了大量 ptrEncoder.encode 调用；
		// 开始检查是否遇到了指针循环。
		ptr := v.Interface()
		if _, ok := e.ptrSeen[ptr]; ok {
			e.error(&UnsupportedValueError{v, fmt.Sprintf("encountered a cycle via %s", v.Type())})
		}
		e.ptrSeen[ptr] = struct{}{}
		defer delete(e.ptrSeen, ptr)
	}
	pe.elemEnc(e, v.Elem(), opts)
	e.ptrLevel--
}

func newPtrEncoder(t reflect.Type) encoderFunc {
	enc := ptrEncoder{typeEncoder(t.Elem())}
	return enc.encode
}

type condAddrEncoder struct {
	canAddrEnc, elseEnc encoderFunc
}

func (ce condAddrEncoder) encode(e *encodeState, v reflect.Value, opts encOpts) {
	if v.CanAddr() {
		ce.canAddrEnc(e, v, opts)
	} else {
		ce.elseEnc(e, v, opts)
	}
}

// newCondAddrEncoder 返回一个编码器，它检查其值是否 CanAddr，
// 若是则委托给 canAddrEnc，否则委托给 elseEnc。
func newCondAddrEncoder(canAddrEnc, elseEnc encoderFunc) encoderFunc {
	enc := condAddrEncoder{canAddrEnc: canAddrEnc, elseEnc: elseEnc}
	return enc.encode
}

func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// 反斜杠和引号字符是保留的，但除此之外，
			// 标签名中允许使用任何标点字符。
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

func typeByIndex(t reflect.Type, index []int) reflect.Type {
	for _, i := range index {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		t = t.Field(i).Type
	}
	return t
}

type reflectWithString struct {
	v  reflect.Value
	ks string
}

func resolveKeyName(k reflect.Value) (string, error) {
	if k.Kind() == reflect.String {
		return k.String(), nil
	}
	if tm, ok := reflect.TypeAssert[encoding.TextMarshaler](k); ok {
		if k.Kind() == reflect.Pointer && k.IsNil() {
			return "", nil
		}
		buf, err := tm.MarshalText()
		return string(buf), err
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10), nil
	}
	panic("unexpected map key type")
}

func appendString[Bytes []byte | string](dst []byte, src Bytes, escapeHTML bool) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(src); {
		if b := src[i]; b < utf8.RuneSelf {
			if htmlSafeSet[b] || (!escapeHTML && safeSet[b]) {
				i++
				continue
			}
			dst = append(dst, src[start:i]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				// 这会对小于 0x20 的字节进行编码，但 \b、\f、\n、\r 和 \t 除外。
				// 如果设置了 escapeHTML，它还会转义 <、> 和 &，
				// 因为当用户控制的字符串被渲染为 JSON
				// 并提供给某些浏览器时，它们可能导致安全漏洞。
				dst = append(dst, '\\', 'u', '0', '0', hex[b>>4], hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		// TODO(https://go.dev/issue/56948): 使用通用的 utf8 功能。
		// 目前，仅将字节切片的一小部分转换为字符串，
		// 以便它可以在栈上分配。由于额外的拷贝，这会略微减慢 []byte 的处理速度，
		// 但字符串性能基本保持不变。
		n := min(len(src)-i, utf8.UTFMax)
		c, size := utf8.DecodeRuneInString(string(src[i : i+n]))
		if c == utf8.RuneError && size == 1 {
			dst = append(dst, src[start:i]...)
			dst = append(dst, `\ufffd`...)
			i += size
			start = i
			continue
		}
		// U+2028 是行分隔符。
		// U+2029 是段分隔符。
		// 它们在技术上都是 JSON 字符串中的有效字符，
		// 但在必须作为 JavaScript 求值的 JSONP 中不起作用，
		// 并且可能在那里导致安全漏洞。对它们进行转义是有效的 JSON，
		// 因此我们无条件地这样做。
		// 参见 https://en.wikipedia.org/wiki/JSON#Safety。
		if c == '\u2028' || c == '\u2029' {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, src[start:]...)
	dst = append(dst, '"')
	return dst
}

// field 表示在结构体中发现的单个字段。
type field struct {
	name      string
	nameBytes []byte // []byte(name)

	nameNonEsc  string // `"` + name + `":`
	nameEscHTML string // `"` + HTMLEscape(name) + `":`

	tag       bool
	index     []int
	typ       reflect.Type
	omitEmpty bool
	omitZero  bool
	isZero    func(reflect.Value) bool
	quoted    bool

	encoder encoderFunc
}

type isZeroer interface {
	IsZero() bool
}

var isZeroerType = reflect.TypeFor[isZeroer]()

func typeFields(t reflect.Type) structFields {
	// 当前层级和下一层级要探索的匿名字段。
	current := []field{}
	next := []field{{typ: t}}

	// 当前层级和下一层级已排队的名称计数。
	var count, nextCount map[reflect.Type]int

	// 已在更早层级访问过的类型。
	visited := map[reflect.Type]bool{}

	// 已发现的字段。
	var fields []field

	// 用于对字段名运行 appendHTMLEscape 的缓冲区。
	var nameEscBuf []byte

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}

		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true

			// 扫描 f.typ 以查找要包含的字段。
			for i := 0; i < f.typ.NumField(); i++ {
				sf := f.typ.Field(i)
				if sf.Anonymous {
					t := sf.Type
					if t.Kind() == reflect.Pointer {
						t = t.Elem()
					}
					if !sf.IsExported() && t.Kind() != reflect.Struct {
						// 忽略未导出的非结构体类型的内嵌字段。
						continue
					}
					// 不要忽略未导出结构体类型的内嵌字段，
					// 因为它们可能具有导出字段。
				} else if !sf.IsExported() {
					// 忽略未导出的非内嵌字段。
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts := parseTag(tag)
				if !isValidTag(name) {
					name = ""
				}
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i

				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					// 跟随指针。
					ft = ft.Elem()
				}

				// 只有字符串、浮点、整数和布尔值可以被加引号。
				quoted := false
				if opts.Contains("string") {
					switch ft.Kind() {
					case reflect.Bool,
						reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
						reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
						reflect.Float32, reflect.Float64,
						reflect.String:
						quoted = true
					}
				}

				// 记录已发现的字段和索引序列。
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					field := field{
						name:      name,
						tag:       tagged,
						index:     index,
						typ:       ft,
						omitEmpty: opts.Contains("omitempty"),
						omitZero:  opts.Contains("omitzero"),
						quoted:    quoted,
					}
					field.nameBytes = []byte(field.name)

					// 提前构建 nameEscHTML 和 nameNonEsc。
					nameEscBuf = appendHTMLEscape(nameEscBuf[:0], field.nameBytes)
					field.nameEscHTML = `"` + string(nameEscBuf) + `":`
					field.nameNonEsc = `"` + field.name + `":`

					if field.omitZero {
						t := sf.Type
						// 提供一个使用类型 IsZero 方法的函数。
						switch {
						case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
							field.isZero = func(v reflect.Value) bool {
								// 避免在 nil 接口或
								// 含 nil 指针的非 nil 接口上调用 IsZero 而 panic。
								return v.IsNil() ||
									(v.Elem().Kind() == reflect.Pointer && v.Elem().IsNil()) ||
									v.Interface().(isZeroer).IsZero()
							}
						case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
							field.isZero = func(v reflect.Value) bool {
								// 避免在 nil 指针上调用 IsZero 而 panic。
								return v.IsNil() || v.Interface().(isZeroer).IsZero()
							}
						case t.Implements(isZeroerType):
							field.isZero = func(v reflect.Value) bool {
								return v.Interface().(isZeroer).IsZero()
							}
						case reflect.PointerTo(t).Implements(isZeroerType):
							field.isZero = func(v reflect.Value) bool {
								if !v.CanAddr() {
									// 临时封装 v，以便可以取其地址。
									v2 := reflect.New(v.Type()).Elem()
									v2.Set(v)
									v = v2
								}
								return v.Addr().Interface().(isZeroer).IsZero()
							}
						}
					}

					fields = append(fields, field)
					if count[f.typ] > 1 {
						// 如果存在多个实例，则再添加一个，
						// 以便销毁代码能看到重复项。
						// 它只关心 1 和 2 的区别，
						// 因此无需再生成更多副本。
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}

				// 记录新的匿名结构体，供下一轮探索。
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, field{name: ft.Name(), index: index, typ: ft})
				}
			}
		}
	}

	slices.SortFunc(fields, func(a, b field) int {
		// 按字段名排序，深度相同则按"名称来自 json 标签"打破平局，
		// 再按索引序列打破平局。
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tag != b.tag {
			if a.tag {
				return -1
			}
			return +1
		}
		return slices.Compare(a.index, b.index)
	})

	// 删除所有被 Go 的内嵌字段规则隐藏的字段，
	// 但带有 JSON 标签的字段会被提升。

	// 字段按主序为名称、次序为字段索引长度排序。
	// 遍历名称；对每个名称，通过选择一个存活的主导字段来删除被隐藏的字段。
	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		// 每个名称一次迭代。
		// 找出与此第一个字段同名的字段序列。
		fi := fields[i]
		name := fi.name
		for advance = 1; i+advance < len(fields); advance++ {
			fj := fields[i+advance]
			if fj.name != name {
				break
			}
		}
		if advance == 1 { // Only one field with this name
			out = append(out, fi)
			continue
		}
		dominant, ok := dominantField(fields[i : i+advance])
		if ok {
			out = append(out, dominant)
		}
	}

	fields = out
	slices.SortFunc(fields, func(i, j field) int {
		return slices.Compare(i.index, j.index)
	})

	for i := range fields {
		f := &fields[i]
		f.encoder = typeEncoder(typeByIndex(t, f.index))
	}
	exactNameIndex := make(map[string]*field, len(fields))
	foldedNameIndex := make(map[string]*field, len(fields))
	for i, field := range fields {
		exactNameIndex[field.name] = &fields[i]
		// 出于历史原因，第一个折叠匹配优先。
		if _, ok := foldedNameIndex[string(foldName(field.nameBytes))]; !ok {
			foldedNameIndex[string(foldName(field.nameBytes))] = &fields[i]
		}
	}
	return structFields{fields, exactNameIndex, foldedNameIndex}
}

// dominantField 查看所有已知同名的字段，
// 使用 Go 的内嵌规则（并根据 JSON 标签的存在进行修正）来找出
// 主导其他字段的那一个字段。如果存在多个顶层字段，
// 布尔值为 false：这种情况在 Go 中是一个错误，我们会跳过所有这些字段。
func dominantField(fields []field) (field, bool) {
	// 字段按索引长度递增的顺序排序，然后按是否有标签排序。
	// 这意味着第一个字段是主导字段。我们只需检查错误情况：
	// 顶层有两个字段，且两者都有标签或都没有标签。
	if len(fields) > 1 && len(fields[0].index) == len(fields[1].index) && fields[0].tag == fields[1].tag {
		return field{}, false
	}
	return fields[0], true
}

var fieldCache sync.Map // map[reflect.Type]structFields

// cachedTypeFields 类似于 typeFields，但使用缓存来避免重复工作。
func cachedTypeFields(t reflect.Type) structFields {
	if f, ok := fieldCache.Load(t); ok {
		return f.(structFields)
	}
	f, _ := fieldCache.LoadOrStore(t, typeFields(t))
	return f.(structFields)
}

func mayAppendQuote(b []byte, quoted bool) []byte {
	if quoted {
		b = append(b, '"')
	}
	return b
}
