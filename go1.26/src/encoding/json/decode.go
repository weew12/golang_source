// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 使用原生 Go 类型（布尔、浮点、字符串、数组和映射）表示 JSON 数据结构。

//go:build !goexperiment.jsonv2

package json

import (
	"encoding"
	"encoding/base64"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
	_ "unsafe" // 用于 linkname
)

// Unmarshal 解析 JSON 编码的数据，并将结果存储在 v 所指向的值中。
// 如果 v 为 nil 或不是指针，Unmarshal 返回一个 [InvalidUnmarshalError]。
//
// Unmarshal 使用 [Marshal] 所用编码的逆过程，根据需要分配映射、切片和指针，
// 并遵循以下附加规则：
//
// 将 JSON 反序列化到指针时，Unmarshal 首先处理 JSON 为字面量 null 的情况。
// 在这种情况下，Unmarshal 将该指针设为 nil。否则，Unmarshal 将 JSON
// 反序列化到该指针所指向的值中。如果指针为 nil，Unmarshal 会为其分配一个
// 新值供其指向。
//
// 将 JSON 反序列化到实现了 [Unmarshaler] 的值时，Unmarshal 会调用该值的
// [Unmarshaler.UnmarshalJSON] 方法，包括输入为 JSON null 的情况。
// 否则，如果该值实现了 [encoding.TextUnmarshaler] 且输入是一个 JSON
// 带引号字符串，Unmarshal 会以该字符串去引号后的形式调用
// [encoding.TextUnmarshaler.UnmarshalText]。
//
// 将 JSON 反序列化到结构体时，Unmarshal 会将传入的对象键与 [Marshal] 所用的
// 键（结构体字段名或其标签）进行匹配，忽略大小写。如果多个结构体字段与某个
// 对象键匹配，则优先选择大小写完全匹配的字段，而不是不区分大小写匹配的字段。
//
// 传入的对象成员按观察到的顺序处理。如果对象包含重复的键，后出现的重复项
// 将替换或合并到先前的值中。
//
// 将 JSON 反序列化到接口值时，Unmarshal 会在该接口值中存储以下之一：
//
//   - bool，对应 JSON 布尔值
//   - float64，对应 JSON 数字
//   - string，对应 JSON 字符串
//   - []any，对应 JSON 数组
//   - map[string]any，对应 JSON 对象
//   - nil，对应 JSON null
//
// 将 JSON 数组反序列化到切片时，Unmarshal 会将切片长度重置为零，然后逐个
// 将元素追加到切片。作为一种特殊情况，将空 JSON 数组反序列化到切片时，
// Unmarshal 会用一个新的空切片替换该切片。
//
// 将 JSON 数组反序列化到 Go 数组时，Unmarshal 会将 JSON 数组元素解码到
// 对应的 Go 数组元素中。如果 Go 数组小于 JSON 数组，多余的 JSON 数组元素
// 会被丢弃。如果 JSON 数组小于 Go 数组，多余的 Go 数组元素会被设为零值。
//
// 将 JSON 对象反序列化到映射时，Unmarshal 首先建立一个要使用的映射。
// 如果映射为 nil，Unmarshal 会分配一个新映射。否则 Unmarshal 会复用现有
// 映射，保留已有的条目。然后 Unmarshal 将 JSON 对象中的键值对存储到映射中。
// 映射的键类型必须为任意字符串类型、整数，或实现了 [encoding.TextUnmarshaler]。
//
// 如果 JSON 编码的数据包含语法错误，Unmarshal 返回一个 [SyntaxError]。
//
// 如果某个 JSON 值不适合给定的目标类型，或者某个 JSON 数字溢出了目标类型，
// Unmarshal 会跳过该字段并尽可能完成反序列化。如果没有遇到更严重的错误，
// Unmarshal 会返回一个 [UnmarshalTypeError]，描述最早出现的此类错误。
// 在任何情况下，都不能保证问题字段之后所有剩余字段都会被反序列化到目标对象中。
//
// JSON null 值反序列化到接口、映射、指针或切片时，会将该 Go 值设为 nil。
// 由于 null 在 JSON 中常用来表示“不存在”，将 JSON null 反序列化到任何其他
// Go 类型都不会对值产生影响，也不会产生错误。
//
// 反序列化带引号的字符串时，无效的 UTF-8 或无效的 UTF-16 代理对不会被视为
// 错误。它们会被替换为 Unicode 替换字符 U+FFFD。
func Unmarshal(data []byte, v any) error {
	// 检查格式是否正确。
	// 避免在发现 JSON 语法错误之前填充了半个数据结构。
	var d decodeState
	err := checkValid(data, &d.scan)
	if err != nil {
		return err
	}

	d.init(data)
	return d.unmarshal(v)
}

// Unmarshaler 是由能够反序列化自身 JSON 描述的类型所实现的接口。
// 可以假定输入是某个 JSON 值的有效编码。如果 UnmarshalJSON 希望在返回后
// 保留数据，它必须复制 JSON 数据。
type Unmarshaler interface {
	UnmarshalJSON([]byte) error
}

// UnmarshalTypeError 描述一个不适合某个特定 Go 类型值的 JSON 值。
type UnmarshalTypeError struct {
	Value  string       // JSON 值的描述 - "bool"、"array"、"number -5"
	Type   reflect.Type // 它无法赋值到的 Go 值的类型
	Offset int64        // 读取 Offset 字节后发生错误
	Struct string       // 包含该字段的结构体类型名称
	Field  string       // 从根节点到该字段的完整路径，包含内嵌结构体
}

func (e *UnmarshalTypeError) Error() string {
	if e.Struct != "" || e.Field != "" {
		return "json: cannot unmarshal " + e.Value + " into Go struct field " + e.Struct + "." + e.Field + " of type " + e.Type.String()
	}
	return "json: cannot unmarshal " + e.Value + " into Go value of type " + e.Type.String()
}

// UnmarshalFieldError 描述一个导致未导出（因此不可写）结构体字段的 JSON 对象键。
//
// Deprecated: 不再使用；保留仅为保持兼容性。
type UnmarshalFieldError struct {
	Key   string
	Type  reflect.Type
	Field reflect.StructField
}

func (e *UnmarshalFieldError) Error() string {
	return "json: cannot unmarshal object key " + strconv.Quote(e.Key) + " into unexported field " + e.Field.Name + " of type " + e.Type.String()
}

// InvalidUnmarshalError 描述传递给 [Unmarshal] 的无效参数。
// （[Unmarshal] 的参数必须是非 nil 指针。）
type InvalidUnmarshalError struct {
	Type reflect.Type
}

func (e *InvalidUnmarshalError) Error() string {
	if e.Type == nil {
		return "json: Unmarshal(nil)"
	}

	if e.Type.Kind() != reflect.Pointer {
		return "json: Unmarshal(non-pointer " + e.Type.String() + ")"
	}
	return "json: Unmarshal(nil " + e.Type.String() + ")"
}

func (d *decodeState) unmarshal(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &InvalidUnmarshalError{reflect.TypeOf(v)}
	}

	d.scan.reset()
	d.scanWhile(scanSkipSpace)
	// 我们解码 rv 而不是 rv.Elem，因为 Unmarshaler 接口的检测
	// 必须作用于值的顶层。
	err := d.value(rv)
	if err != nil {
		return d.addErrorContext(err)
	}
	return d.savedError
}

// Number 表示一个 JSON 数字字面量。
type Number string

// String 返回该数字的字面量文本。
func (n Number) String() string { return string(n) }

// Float64 将该数字以 float64 返回。
func (n Number) Float64() (float64, error) {
	return strconv.ParseFloat(string(n), 64)
}

// Int64 将该数字以 int64 返回。
func (n Number) Int64() (int64, error) {
	return strconv.ParseInt(string(n), 10, 64)
}

// errorContext 为解码期间的类型错误提供上下文。
type errorContext struct {
	Struct     reflect.Type
	FieldStack []string
}

// decodeState 表示解码 JSON 值时的状态。
type decodeState struct {
	data                  []byte
	off                   int // data 中的下一个读取偏移量
	opcode                int // 上一次读取的结果
	scan                  scanner
	errorContext          *errorContext
	savedError            error
	useNumber             bool
	disallowUnknownFields bool
}

// readIndex 返回上一次读取的字节的位置。
func (d *decodeState) readIndex() int {
	return d.off - 1
}

// phasePanicMsg 用作 panic 消息，当我们遇到本不该发生的情况时使用。
// 它可能表明 JSON 解码器存在 bug，或者有东西在解码器执行期间修改了 data 切片。
const phasePanicMsg = "JSON decoder out of sync - data changing underfoot?"

func (d *decodeState) init(data []byte) *decodeState {
	d.data = data
	d.off = 0
	d.savedError = nil
	if d.errorContext != nil {
		d.errorContext.Struct = nil
		// 复用已为 FieldStack 切片分配的空间。
		d.errorContext.FieldStack = d.errorContext.FieldStack[:0]
	}
	return d
}

// saveError 保存首次调用时传入的 err，供反序列化结束时报告。
func (d *decodeState) saveError(err error) {
	if d.savedError == nil {
		d.savedError = d.addErrorContext(err)
	}
}

// addErrorContext 返回一个用 d.errorContext 中的信息增强的新错误
func (d *decodeState) addErrorContext(err error) error {
	if d.errorContext != nil && (d.errorContext.Struct != nil || len(d.errorContext.FieldStack) > 0) {
		switch err := err.(type) {
		case *UnmarshalTypeError:
			err.Struct = d.errorContext.Struct.Name()
			fieldStack := d.errorContext.FieldStack
			if err.Field != "" {
				fieldStack = append(fieldStack, err.Field)
			}
			err.Field = strings.Join(fieldStack, ".")
		}
	}
	return err
}

// skip 扫描到已开始解析的内容的末尾。
func (d *decodeState) skip() {
	s, data, i := &d.scan, d.data, d.off
	depth := len(s.parseState)
	for {
		op := s.step(s, data[i])
		i++
		if len(s.parseState) < depth {
			d.off = i
			d.opcode = op
			return
		}
	}
}

// scanNext 处理 d.data[d.off] 处的字节。
func (d *decodeState) scanNext() {
	if d.off < len(d.data) {
		d.opcode = d.scan.step(&d.scan, d.data[d.off])
		d.off++
	} else {
		d.opcode = d.scan.eof()
		d.off = len(d.data) + 1 // 用 len+1 标记已处理 EOF
	}
}

// scanWhile 处理 d.data[d.off:] 中的字节，直到收到不等于 op 的扫描码。
func (d *decodeState) scanWhile(op int) {
	s, data, i := &d.scan, d.data, d.off
	for i < len(data) {
		newOp := s.step(s, data[i])
		i++
		if newOp != op {
			d.opcode = newOp
			d.off = i
			return
		}
	}

	d.off = len(data) + 1 // 用 len+1 标记已处理 EOF
	d.opcode = d.scan.eof()
}

// rescanLiteral 类似于 scanWhile(scanContinue)，但它针对解码字面量这一常见
// 情况做了特化。解码器会对输入扫描两次，一次用于检查语法错误和值的长度，
// 第二次用于执行解码。
//
// 只有在第二步中我们才使用 decodeState 对字面量进行分词，因此我们知道不存在
// 任何语法错误。我们可以利用这一点，更快地扫描字面量的字节。
func (d *decodeState) rescanLiteral() {
	data, i := d.data, d.off
Switch:
	switch data[i-1] {
	case '"': // 字符串
		for ; i < len(data); i++ {
			switch data[i] {
			case '\\':
				i++ // 转义字符
			case '"':
				i++ // 也将结尾引号分词
				break Switch
			}
		}
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-': // 数字
		for ; i < len(data); i++ {
			switch data[i] {
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
				'.', 'e', 'E', '+', '-':
			default:
				break Switch
			}
		}
	case 't': // true
		i += len("rue")
	case 'f': // false
		i += len("alse")
	case 'n': // null
		i += len("ull")
	}
	if i < len(data) {
		d.opcode = stateEndValue(&d.scan, data[i])
	} else {
		d.opcode = scanEnd
	}
	d.off = i + 1
}

// value 从 d.data[d.off-1:] 消费一个 JSON 值，解码到 v 中，并提前读取
// 后续字节。如果 v 无效，则丢弃该值。值的第一个字节已经被读取。
func (d *decodeState) value(v reflect.Value) error {
	switch d.opcode {
	default:
		panic(phasePanicMsg)

	case scanBeginArray:
		if v.IsValid() {
			if err := d.array(v); err != nil {
				return err
			}
		} else {
			d.skip()
		}
		d.scanNext()

	case scanBeginObject:
		if v.IsValid() {
			if err := d.object(v); err != nil {
				return err
			}
		} else {
			d.skip()
		}
		d.scanNext()

	case scanBeginLiteral:
		// 字面量内部的所有字节都返回 scanContinue 操作码。
		start := d.readIndex()
		d.rescanLiteral()

		if v.IsValid() {
			if err := d.literalStore(d.data[start:d.readIndex()], v, false); err != nil {
				return err
			}
		}
	}
	return nil
}

type unquotedValue struct{}

// valueQuoted 类似于 value，但会将带引号的字符串字面量或字面量 null 解码到
// 接口值中。如果它发现除带引号字符串字面量或 null 之外的任何内容，
// valueQuoted 会返回 unquotedValue{}。
func (d *decodeState) valueQuoted() any {
	switch d.opcode {
	default:
		panic(phasePanicMsg)

	case scanBeginArray, scanBeginObject:
		d.skip()
		d.scanNext()

	case scanBeginLiteral:
		v := d.literalInterface()
		switch v.(type) {
		case nil, string:
			return v
		}
	}
	return unquotedValue{}
}

// indirect 沿着 v 向下遍历，按需分配指针，直到遇到非指针。
// 如果遇到 Unmarshaler，indirect 会停止并返回它。
// 如果 decodingNull 为 true，indirect 会在第一个可设置的指针处停止，
// 以便可以将其设为 nil。
func indirect(v reflect.Value, decodingNull bool) (Unmarshaler, encoding.TextUnmarshaler, reflect.Value) {
	// Issue #24153 表明，通常并不能保证可以通过调用 Value.Addr().Elem()
	// 对 reflect.Value 进行往返转换，并期望对于派生自未导出内嵌结构体字段的值
	// 仍然可设置。
	//
	// 下面的逻辑在首次对值取地址时（为满足可能的指针方法）有效地做到了这一点，
	// 并在必要时继续解引用后续指针。
	//
	// 在首次往返之后，我们将 v 设回原始值，以保留 reflect.Value 中包含的
	// 原始读写标志。
	v0 := v
	haveAddr := false

	// 如果 v 是命名类型且可寻址，则从它的地址开始，
	// 这样如果该类型有指针方法，我们就能找到它们。
	if v.Kind() != reflect.Pointer && v.Type().Name() != "" && v.CanAddr() {
		haveAddr = true
		v = v.Addr()
	}
	for {
		// 从接口加载值，但仅当结果将具有有用的可寻址性时。
		if v.Kind() == reflect.Interface && !v.IsNil() {
			e := v.Elem()
			if e.Kind() == reflect.Pointer && !e.IsNil() && (!decodingNull || e.Elem().Kind() == reflect.Pointer) {
				haveAddr = false
				v = e
				continue
			}
		}

		if v.Kind() != reflect.Pointer {
			break
		}

		if decodingNull && v.CanSet() {
			break
		}

		// 防止当 v 是指向自身地址的接口时出现无限循环：
		//     var v any
		//     v = &v
		if v.Elem().Kind() == reflect.Interface && v.Elem().Elem().Equal(v) {
			v = v.Elem()
			break
		}
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		if v.Type().NumMethod() > 0 && v.CanInterface() {
			if u, ok := reflect.TypeAssert[Unmarshaler](v); ok {
				return u, nil, reflect.Value{}
			}
			if !decodingNull {
				if u, ok := reflect.TypeAssert[encoding.TextUnmarshaler](v); ok {
					return nil, u, reflect.Value{}
				}
			}
		}

		if haveAddr {
			v = v0 // restore original value after round-trip Value.Addr().Elem()
			haveAddr = false
		} else {
			v = v.Elem()
		}
	}
	return nil, nil, v
}

// array 从 d.data[d.off-1:] 消费一个数组，解码到 v 中。
// 数组的第一个字节（'['）已经被读取。
func (d *decodeState) array(v reflect.Value) error {
	// 检查 unmarshaler。
	u, ut, pv := indirect(v, false)
	if u != nil {
		start := d.readIndex()
		d.skip()
		return u.UnmarshalJSON(d.data[start:d.off])
	}
	if ut != nil {
		d.saveError(&UnmarshalTypeError{Value: "array", Type: v.Type(), Offset: int64(d.off)})
		d.skip()
		return nil
	}
	v = pv

	// 检查目标的类型。
	switch v.Kind() {
	case reflect.Interface:
		if v.NumMethod() == 0 {
			// 解码到 nil 接口？切换到非反射代码。
			ai := d.arrayInterface()
			v.Set(reflect.ValueOf(ai))
			return nil
		}
		// 否则它是无效的。
		fallthrough
	default:
		d.saveError(&UnmarshalTypeError{Value: "array", Type: v.Type(), Offset: int64(d.off)})
		d.skip()
		return nil
	case reflect.Array, reflect.Slice:
		break
	}

	i := 0
	for {
		// 向前查找 ] - 只可能在第一次迭代时发生。
		d.scanWhile(scanSkipSpace)
		if d.opcode == scanEndArray {
			break
		}

		// 扩展切片长度，必要时增长切片。
		if v.Kind() == reflect.Slice {
			if i >= v.Cap() {
				v.Grow(1)
			}
			if i >= v.Len() {
				v.SetLen(i + 1)
			}
		}

		if i < v.Len() {
			// 解码到元素。
			if err := d.value(v.Index(i)); err != nil {
				return err
			}
		} else {
			// 固定数组已用尽：跳过。
			if err := d.value(reflect.Value{}); err != nil {
				return err
			}
		}
		i++

		// 下一个标记必须是 , 或 ]。
		if d.opcode == scanSkipSpace {
			d.scanWhile(scanSkipSpace)
		}
		if d.opcode == scanEndArray {
			break
		}
		if d.opcode != scanArrayValue {
			panic(phasePanicMsg)
		}
	}

	if i < v.Len() {
		if v.Kind() == reflect.Array {
			for ; i < v.Len(); i++ {
				v.Index(i).SetZero() // 将数组剩余部分置零
			}
		} else {
			v.SetLen(i) // 截断切片
		}
	}
	if i == 0 && v.Kind() == reflect.Slice {
		v.Set(reflect.MakeSlice(v.Type(), 0, 0))
	}
	return nil
}

var nullLiteral = []byte("null")
var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// object 从 d.data[d.off-1:] 消费一个对象，解码到 v 中。
// 对象的第一个字节（'{'）已经被读取。
func (d *decodeState) object(v reflect.Value) error {
	// 检查 unmarshaler。
	u, ut, pv := indirect(v, false)
	if u != nil {
		start := d.readIndex()
		d.skip()
		return u.UnmarshalJSON(d.data[start:d.off])
	}
	if ut != nil {
		d.saveError(&UnmarshalTypeError{Value: "object", Type: v.Type(), Offset: int64(d.off)})
		d.skip()
		return nil
	}
	v = pv
	t := v.Type()

	// 解码到 nil 接口？切换到非反射代码。
	if v.Kind() == reflect.Interface && v.NumMethod() == 0 {
		oi := d.objectInterface()
		v.Set(reflect.ValueOf(oi))
		return nil
	}

	var fields structFields

	// 检查目标类型：
	//   struct 或
	//   map[T1]T2，其中 T1 是字符串、整数类型，
	//             或 encoding.TextUnmarshaler
	switch v.Kind() {
	case reflect.Map:
		// 映射键必须具有字符串种类、整数种类，或者是 encoding.TextUnmarshaler。
		switch t.Key().Kind() {
		case reflect.String,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		default:
			if !reflect.PointerTo(t.Key()).Implements(textUnmarshalerType) {
				d.saveError(&UnmarshalTypeError{Value: "object", Type: t, Offset: int64(d.off)})
				d.skip()
				return nil
			}
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(t))
		}
	case reflect.Struct:
		fields = cachedTypeFields(t)
		// 没问题
	default:
		d.saveError(&UnmarshalTypeError{Value: "object", Type: t, Offset: int64(d.off)})
		d.skip()
		return nil
	}

	var mapElem reflect.Value
	var origErrorContext errorContext
	if d.errorContext != nil {
		origErrorContext = *d.errorContext
	}

	for {
		// 读取字符串键的开头引号 " 或结尾 }。
		d.scanWhile(scanSkipSpace)
		if d.opcode == scanEndObject {
			// closing } - 只能在第一次迭代时发生。
			break
		}
		if d.opcode != scanBeginLiteral {
			panic(phasePanicMsg)
		}

		// 读取键。
		start := d.readIndex()
		d.rescanLiteral()
		item := d.data[start:d.readIndex()]
		key, ok := unquoteBytes(item)
		if !ok {
			panic(phasePanicMsg)
		}

		// 找出与键对应的字段。
		var subv reflect.Value
		destring := false // 该值是否被包裹在字符串中需要先解码

		if v.Kind() == reflect.Map {
			elemType := t.Elem()
			if !mapElem.IsValid() {
				mapElem = reflect.New(elemType).Elem()
			} else {
				mapElem.SetZero()
			}
			subv = mapElem
		} else {
			f := fields.byExactName[string(key)]
			if f == nil {
				f = fields.byFoldedName[string(foldName(key))]
			}
			if f != nil {
				subv = v
				destring = f.quoted
				if d.errorContext == nil {
					d.errorContext = new(errorContext)
				}
				for i, ind := range f.index {
					if subv.Kind() == reflect.Pointer {
						if subv.IsNil() {
							// 如果结构体内嵌了指向未导出类型的指针，
							// 由于该字段未导出，无法设置新分配的值。
							//
							// 参见 https://golang.org/issue/21357
							if !subv.CanSet() {
								d.saveError(fmt.Errorf("json: cannot set embedded pointer to unexported struct: %v", subv.Type().Elem()))
								// 使 subv 失效，以确保 d.value(subv) 跳过该 JSON 值
								// 而不将其赋给 subv。
								subv = reflect.Value{}
								destring = false
								break
							}
							subv.Set(reflect.New(subv.Type().Elem()))
						}
						subv = subv.Elem()
					}
					if i < len(f.index)-1 {
						d.errorContext.FieldStack = append(
							d.errorContext.FieldStack,
							subv.Type().Field(ind).Name,
						)
					}
					subv = subv.Field(ind)
				}
				d.errorContext.Struct = t
				d.errorContext.FieldStack = append(d.errorContext.FieldStack, f.name)
			} else if d.disallowUnknownFields {
				d.saveError(fmt.Errorf("json: unknown field %q", key))
			}
		}

		// 读取值之前的 :。
		if d.opcode == scanSkipSpace {
			d.scanWhile(scanSkipSpace)
		}
		if d.opcode != scanObjectKey {
			panic(phasePanicMsg)
		}
		d.scanWhile(scanSkipSpace)

		if destring {
			switch qv := d.valueQuoted().(type) {
			case nil:
				if err := d.literalStore(nullLiteral, subv, false); err != nil {
					return err
				}
			case string:
				if err := d.literalStore([]byte(qv), subv, true); err != nil {
					return err
				}
			default:
				d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal unquoted value into %v", subv.Type()))
			}
		} else {
			if err := d.value(subv); err != nil {
				return err
			}
		}

		// 将值写回映射；
		// 如果使用结构体，subv 已经指向结构体内部。
		if v.Kind() == reflect.Map {
			kt := t.Key()
			var kv reflect.Value
			if reflect.PointerTo(kt).Implements(textUnmarshalerType) {
				kv = reflect.New(kt)
				if err := d.literalStore(item, kv, true); err != nil {
					return err
				}
				kv = kv.Elem()
			} else {
				switch kt.Kind() {
				case reflect.String:
					kv = reflect.New(kt).Elem()
					kv.SetString(string(key))
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					s := string(key)
					n, err := strconv.ParseInt(s, 10, 64)
					if err != nil || kt.OverflowInt(n) {
						d.saveError(&UnmarshalTypeError{Value: "number " + s, Type: kt, Offset: int64(start + 1)})
						break
					}
					kv = reflect.New(kt).Elem()
					kv.SetInt(n)
				case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
					s := string(key)
					n, err := strconv.ParseUint(s, 10, 64)
					if err != nil || kt.OverflowUint(n) {
						d.saveError(&UnmarshalTypeError{Value: "number " + s, Type: kt, Offset: int64(start + 1)})
						break
					}
					kv = reflect.New(kt).Elem()
					kv.SetUint(n)
				default:
					panic("json: Unexpected key type") // 不应发生
				}
			}
			if kv.IsValid() {
				v.SetMapIndex(kv, subv)
			}
		}

		// 下一个标记必须是 , 或 }。
		if d.opcode == scanSkipSpace {
			d.scanWhile(scanSkipSpace)
		}
		if d.errorContext != nil {
			// 将 errorContext 重置为其原始状态。
			// 为 FieldStack 保留相同的底层数组，以复用空间并避免不必要的分配。
			d.errorContext.FieldStack = d.errorContext.FieldStack[:len(origErrorContext.FieldStack)]
			d.errorContext.Struct = origErrorContext.Struct
		}
		if d.opcode == scanEndObject {
			break
		}
		if d.opcode != scanObjectValue {
			panic(phasePanicMsg)
		}
	}
	return nil
}

// convertNumber 将数字字面量 s 转换为 float64 或 Number，
// 具体取决于 d.useNumber 的设置。
func (d *decodeState) convertNumber(s string) (any, error) {
	if d.useNumber {
		return Number(s), nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, &UnmarshalTypeError{Value: "number " + s, Type: reflect.TypeFor[float64](), Offset: int64(d.off)}
	}
	return f, nil
}

var numberType = reflect.TypeFor[Number]()

// literalStore 将存储在 item 中的字面量解码到 v 中。
//
// fromQuoted 表示该字面量是否来自从 ",string" 结构体标签选项解包出的字符串。
// 这仅用于生成更有帮助的错误消息。
func (d *decodeState) literalStore(item []byte, v reflect.Value, fromQuoted bool) error {
	// 检查 unmarshaler。
	if len(item) == 0 {
		// 给定了空字符串。
		d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type()))
		return nil
	}
	isNull := item[0] == 'n' // null
	u, ut, pv := indirect(v, isNull)
	if u != nil {
		return u.UnmarshalJSON(item)
	}
	if ut != nil {
		if item[0] != '"' {
			if fromQuoted {
				d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type()))
				return nil
			}
			val := "number"
			switch item[0] {
			case 'n':
				val = "null"
			case 't', 'f':
				val = "bool"
			}
			d.saveError(&UnmarshalTypeError{Value: val, Type: v.Type(), Offset: int64(d.readIndex())})
			return nil
		}
		s, ok := unquoteBytes(item)
		if !ok {
			if fromQuoted {
				return fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type())
			}
			panic(phasePanicMsg)
		}
		return ut.UnmarshalText(s)
	}

	v = pv

	switch c := item[0]; c {
	case 'n': // null
		// 主解析器确保只有 true 和 false 能到达这里，
		// 但如果这是带引号的字符串输入，则可能是任何内容。
		if fromQuoted && string(item) != "null" {
			d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type()))
			break
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice:
			v.SetZero()
			// 否则，对基本类型/字符串忽略 null
		}
	case 't', 'f': // true, false
		value := item[0] == 't'
		// 主解析器确保只有 true 和 false 能到达这里，
		// 但如果这是带引号的字符串输入，则可能是任何内容。
		if fromQuoted && string(item) != "true" && string(item) != "false" {
			d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type()))
			break
		}
		switch v.Kind() {
		default:
			if fromQuoted {
				d.saveError(fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type()))
			} else {
				d.saveError(&UnmarshalTypeError{Value: "bool", Type: v.Type(), Offset: int64(d.readIndex())})
			}
		case reflect.Bool:
			v.SetBool(value)
		case reflect.Interface:
			if v.NumMethod() == 0 {
				v.Set(reflect.ValueOf(value))
			} else {
				d.saveError(&UnmarshalTypeError{Value: "bool", Type: v.Type(), Offset: int64(d.readIndex())})
			}
		}

	case '"': // string
		s, ok := unquoteBytes(item)
		if !ok {
			if fromQuoted {
				return fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type())
			}
			panic(phasePanicMsg)
		}
		switch v.Kind() {
		default:
			d.saveError(&UnmarshalTypeError{Value: "string", Type: v.Type(), Offset: int64(d.readIndex())})
		case reflect.Slice:
			if v.Type().Elem().Kind() != reflect.Uint8 {
				d.saveError(&UnmarshalTypeError{Value: "string", Type: v.Type(), Offset: int64(d.readIndex())})
				break
			}
			b := make([]byte, base64.StdEncoding.DecodedLen(len(s)))
			n, err := base64.StdEncoding.Decode(b, s)
			if err != nil {
				d.saveError(err)
				break
			}
			v.SetBytes(b[:n])
		case reflect.String:
			t := string(s)
			if v.Type() == numberType && !isValidNumber(t) {
				return fmt.Errorf("json: invalid number literal, trying to unmarshal %q into Number", item)
			}
			v.SetString(t)
		case reflect.Interface:
			if v.NumMethod() == 0 {
				v.Set(reflect.ValueOf(string(s)))
			} else {
				d.saveError(&UnmarshalTypeError{Value: "string", Type: v.Type(), Offset: int64(d.readIndex())})
			}
		}

	default: // number
		if c != '-' && (c < '0' || c > '9') {
			if fromQuoted {
				return fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type())
			}
			panic(phasePanicMsg)
		}
		switch v.Kind() {
		default:
			if v.Kind() == reflect.String && v.Type() == numberType {
				// s 必须是有效的数字，因为它已经过词法分析。
				v.SetString(string(item))
				break
			}
			if fromQuoted {
				return fmt.Errorf("json: invalid use of ,string struct tag, trying to unmarshal %q into %v", item, v.Type())
			}
			d.saveError(&UnmarshalTypeError{Value: "number", Type: v.Type(), Offset: int64(d.readIndex())})
		case reflect.Interface:
			n, err := d.convertNumber(string(item))
			if err != nil {
				d.saveError(err)
				break
			}
			if v.NumMethod() != 0 {
				d.saveError(&UnmarshalTypeError{Value: "number", Type: v.Type(), Offset: int64(d.readIndex())})
				break
			}
			v.Set(reflect.ValueOf(n))

		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(string(item), 10, 64)
			if err != nil || v.OverflowInt(n) {
				d.saveError(&UnmarshalTypeError{Value: "number " + string(item), Type: v.Type(), Offset: int64(d.readIndex())})
				break
			}
			v.SetInt(n)

		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			n, err := strconv.ParseUint(string(item), 10, 64)
			if err != nil || v.OverflowUint(n) {
				d.saveError(&UnmarshalTypeError{Value: "number " + string(item), Type: v.Type(), Offset: int64(d.readIndex())})
				break
			}
			v.SetUint(n)

		case reflect.Float32, reflect.Float64:
			n, err := strconv.ParseFloat(string(item), v.Type().Bits())
			if err != nil || v.OverflowFloat(n) {
				d.saveError(&UnmarshalTypeError{Value: "number " + string(item), Type: v.Type(), Offset: int64(d.readIndex())})
				break
			}
			v.SetFloat(n)
		}
	}
	return nil
}

// xxxInterface 系列例程构建要存储在空接口中的值。它们并非严格必要，
// 但它们在这种常见情况下避免了反射的开销。

// valueInterface 类似于 value，但返回 any。
func (d *decodeState) valueInterface() (val any) {
	switch d.opcode {
	default:
		panic(phasePanicMsg)
	case scanBeginArray:
		val = d.arrayInterface()
		d.scanNext()
	case scanBeginObject:
		val = d.objectInterface()
		d.scanNext()
	case scanBeginLiteral:
		val = d.literalInterface()
	}
	return
}

// arrayInterface 类似于 array，但返回 []any。
func (d *decodeState) arrayInterface() []any {
	var v = make([]any, 0)
	for {
		// 向前查找 ] - 只可能在第一次迭代时发生。
		d.scanWhile(scanSkipSpace)
		if d.opcode == scanEndArray {
			break
		}

		v = append(v, d.valueInterface())

		// 下一个标记必须是 , 或 ]。
		if d.opcode == scanSkipSpace {
			d.scanWhile(scanSkipSpace)
		}
		if d.opcode == scanEndArray {
			break
		}
		if d.opcode != scanArrayValue {
			panic(phasePanicMsg)
		}
	}
	return v
}

// objectInterface 类似于 object，但返回 map[string]any。
func (d *decodeState) objectInterface() map[string]any {
	m := make(map[string]any)
	for {
		// 读取字符串键的开头引号 " 或结尾 }。
		d.scanWhile(scanSkipSpace)
		if d.opcode == scanEndObject {
			// closing } - 只能在第一次迭代时发生。
			break
		}
		if d.opcode != scanBeginLiteral {
			panic(phasePanicMsg)
		}

		// 读取字符串键。
		start := d.readIndex()
		d.rescanLiteral()
		item := d.data[start:d.readIndex()]
		key, ok := unquote(item)
		if !ok {
			panic(phasePanicMsg)
		}

		// 读取值之前的 :。
		if d.opcode == scanSkipSpace {
			d.scanWhile(scanSkipSpace)
		}
		if d.opcode != scanObjectKey {
			panic(phasePanicMsg)
		}
		d.scanWhile(scanSkipSpace)

		// 读取值。
		m[key] = d.valueInterface()

		// 下一个标记必须是 , 或 }。
		if d.opcode == scanSkipSpace {
			d.scanWhile(scanSkipSpace)
		}
		if d.opcode == scanEndObject {
			break
		}
		if d.opcode != scanObjectValue {
			panic(phasePanicMsg)
		}
	}
	return m
}

// literalInterface 从 d.data[d.off-1:] 消费并返回一个字面量，
// 并提前读取后续字节。字面量的第一个字节已经被读取
// （调用者正是据此知道它是一个字面量）。
func (d *decodeState) literalInterface() any {
	// 字面量内部的所有字节都返回 scanContinue 操作码。
	start := d.readIndex()
	d.rescanLiteral()

	item := d.data[start:d.readIndex()]

	switch c := item[0]; c {
	case 'n': // null
		return nil

	case 't', 'f': // true, false
		return c == 't'

	case '"': // string
		s, ok := unquote(item)
		if !ok {
			panic(phasePanicMsg)
		}
		return s

	default: // number
		if c != '-' && (c < '0' || c > '9') {
			panic(phasePanicMsg)
		}
		n, err := d.convertNumber(string(item))
		if err != nil {
			d.saveError(err)
		}
		return n
	}
}

// getu4 从 s 的开头解码 \uXXXX，返回十六进制值，或返回 -1。
func getu4(s []byte) rune {
	if len(s) < 6 || s[0] != '\\' || s[1] != 'u' {
		return -1
	}
	var r rune
	for _, c := range s[2:6] {
		switch {
		case '0' <= c && c <= '9':
			c = c - '0'
		case 'a' <= c && c <= 'f':
			c = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			c = c - 'A' + 10
		default:
			return -1
		}
		r = r*16 + rune(c)
	}
	return r
}

// unquote 将带引号的 JSON 字符串字面量 s 转换为实际字符串 t。
// 其规则与 Go 不同，因此不能使用 strconv.Unquote。
func unquote(s []byte) (t string, ok bool) {
	s, ok = unquoteBytes(s)
	t = string(s)
	return
}

func unquoteBytes(s []byte) (t []byte, ok bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return
	}
	s = s[1 : len(s)-1]

	// 检查是否存在不寻常的字符。如果没有，
	// 则无需去除引号，因此返回原始字节的切片。
	r := 0
	for r < len(s) {
		c := s[r]
		if c == '\\' || c == '"' || c < ' ' {
			break
		}
		rr, size := utf8.DecodeRune(s[r:])
		if rr == utf8.RuneError && size == 1 {
			break
		}
		r += size
	}
	if r == len(s) {
		return s, true
	}

	b := make([]byte, len(s)+2*utf8.UTFMax)
	w := copy(b, s[0:r])
	for r < len(s) {
		// 空间不足？只有在 s 充满畸形 UTF-8 且我们将
		// 每个字节都替换为 RuneError 时才会发生。
		if w >= len(b)-2*utf8.UTFMax {
			nb := make([]byte, (len(b)+utf8.UTFMax)*2)
			copy(nb, b[0:w])
			b = nb
		}
		switch c := s[r]; {
		case c == '\\':
			r++
			if r >= len(s) {
				return
			}
			switch s[r] {
			default:
				return
			case '"', '\\', '/', '\'':
				b[w] = s[r]
				r++
				w++
			case 'b':
				b[w] = '\b'
				r++
				w++
			case 'f':
				b[w] = '\f'
				r++
				w++
			case 'n':
				b[w] = '\n'
				r++
				w++
			case 'r':
				b[w] = '\r'
				r++
				w++
			case 't':
				b[w] = '\t'
				r++
				w++
			case 'u':
				r--
				rr := getu4(s[r:])
				if rr < 0 {
					return
				}
				r += 6
				if utf16.IsSurrogate(rr) {
					rr1 := getu4(s[r:])
					if dec := utf16.DecodeRune(rr, rr1); dec != unicode.ReplacementChar {
						// 有效的代理对；消费它。
						r += 6
						w += utf8.EncodeRune(b[w:], dec)
						break
					}
					// 无效的代理对；回退到替换符文。
					rr = unicode.ReplacementChar
				}
				w += utf8.EncodeRune(b[w:], rr)
			}

		// 引号、控制字符是无效的。
		case c == '"', c < ' ':
			return

		// ASCII
		case c < utf8.RuneSelf:
			b[w] = c
			r++
			w++

		// 强制转换为格式良好的 UTF-8。
		default:
			rr, size := utf8.DecodeRune(s[r:])
			r += size
			w += utf8.EncodeRune(b[w:], rr)
		}
	}
	return b[0:w], true
}
