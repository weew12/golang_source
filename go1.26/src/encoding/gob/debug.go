// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 删除下一行即可将其包含进 gob 包。
//
//go:build ignore

package gob

// 此文件通常不包含在 gob 包中。仅用于调试本包自身。
// 除了读取 uint 之外，它实现了一个独立于 Decoder 所实现读取器的读取器。
// 若要启用 Debug 函数，请删除上面的 +build ignore 行，然后执行
//	go install

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

var dumpBytes = false // 若为 true，则在每个条目处打印输入缓冲区中剩余的字节。

// Init 安装调试设施。如果此文件未编译进包中，
// 那么 codec_test.go 中的测试为空操作。
func init() {
	debugFunc = Debug
}

var (
	blanks = bytes.Repeat([]byte{' '}, 3*10)
	empty  = []byte(": <empty>\n")
	tabs   = strings.Repeat("\t", 100)
)

// tab 在打印时对自身进行缩进。
type tab int

func (t tab) String() string {
	n := int(t)
	if n > len(tabs) {
		n = len(tabs)
	}
	return tabs[0:n]
}

func (t tab) print() {
	fmt.Fprint(os.Stderr, t)
}

// peekReader 包装一个 io.Reader，允许向前预读以查看即将到来的内容，
// 而不会从 Reader 的客户端那里窃取数据。
type peekReader struct {
	r    io.Reader
	data []byte // 预读数据
}

// newPeekReader 返回一个包装 r 的 peekReader。
func newPeekReader(r io.Reader) *peekReader {
	return &peekReader{r: r}
}

// Read 是常规方法。它会先取用已经预读的数据。
func (p *peekReader) Read(b []byte) (n int, err error) {
	if len(p.data) == 0 {
		return p.r.Read(b)
	}
	// 尽可能地从预读数据中满足需求。
	n = copy(b, p.data)
	// 将数据下移到切片的开头，以避免无限增长
	copy(p.data, p.data[n:])
	p.data = p.data[:len(p.data)-n]
	return
}

// peek 从流的未读部分返回尽可能多的字节，
// 最多不超过 b 的长度。
func (p *peekReader) peek(b []byte) (n int, err error) {
	if len(p.data) > 0 {
		n = copy(b, p.data)
		if n == len(b) {
			return
		}
		b = b[n:]
	}
	if len(b) == 0 {
		return
	}
	m, e := io.ReadFull(p.r, b)
	if m > 0 {
		p.data = append(p.data, b[:m]...)
	}
	n += m
	if e == io.ErrUnexpectedEOF {
		// 这意味着 m > 0，但我们到达了 EOF。如果我们已获取到数据，
		// 就不会抱怨无法预读足够的内容。
		if n > 0 {
			e = nil
		} else {
			e = io.EOF
		}
	}
	return n, e
}

type debugger struct {
	mutex          sync.Mutex
	remain         int  // 已知输入中剩余的字节数
	remainingKnown bool // 'remain' 的值是否有效
	r              *peekReader
	wireType       map[typeId]*wireType
	tmp            []byte // 用于解码 uint 的暂存空间。
}

// dump 打印输入中接下来的 nBytes 个字节。
// 它安排各次调用之间的输出对齐，
// 以便于看出哪些内容已被消费。
func (deb *debugger) dump(format string, args ...any) {
	if !dumpBytes {
		return
	}
	fmt.Fprintf(os.Stderr, format+" ", args...)
	if !deb.remainingKnown {
		return
	}
	if deb.remain < 0 {
		fmt.Fprintf(os.Stderr, "remaining byte count is negative! %d\n", deb.remain)
		return
	}
	data := make([]byte, deb.remain)
	n, _ := deb.r.peek(data)
	if n == 0 {
		os.Stderr.Write(empty)
		return
	}
	b := new(bytes.Buffer)
	fmt.Fprintf(b, "[%d]{\n", deb.remain)
	// 填充空格直到第一个字节
	lineLength := 0
	if n := len(data); n%10 != 0 {
		lineLength = 10 - n%10
		fmt.Fprintf(b, "\t%s", blanks[:lineLength*3])
	}
	// 每行 10 个字节
	for len(data) > 0 {
		if lineLength == 0 {
			fmt.Fprint(b, "\t")
		}
		m := 10 - lineLength
		lineLength = 0
		if m > len(data) {
			m = len(data)
		}
		fmt.Fprintf(b, "% x\n", data[:m])
		data = data[m:]
	}
	fmt.Fprint(b, "}\n")
	os.Stderr.Write(b.Bytes())
}

// Debug 打印从 r 读取的 gob 数据的可读表示。
// 除非构建包时启用了调试，否则它为空操作。
func Debug(r io.Reader) {
	err := debug(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gob debug: %s\n", err)
	}
}

// debug 实现了 Debug，但会捕获 panic，并将其作为
// 错误返回，供 Debug 打印。
func debug(r io.Reader) (err error) {
	defer catchError(&err)
	fmt.Fprintln(os.Stderr, "Start of debugging")
	deb := &debugger{
		r:        newPeekReader(r),
		wireType: make(map[typeId]*wireType),
		tmp:      make([]byte, 16),
	}
	if b, ok := r.(*bytes.Buffer); ok {
		deb.remain = b.Len()
		deb.remainingKnown = true
	}
	deb.gobStream()
	return
}

// 记录我们已消费了一些字节
func (deb *debugger) consumed(n int) {
	if deb.remainingKnown {
		deb.remain -= n
	}
}

// int64 解码并返回下一个整数，该整数必须存在。
// 如果你可能处于 EOF，请不要调用此函数。
func (deb *debugger) int64() int64 {
	return toInt(deb.uint64())
}

// uint64 返回并解码下一个无符号整数，该整数必须存在。
// 如果你可能处于 EOF，请不要调用此函数。
// TODO: 更好地处理错误。
func (deb *debugger) uint64() uint64 {
	n, w, err := decodeUintReader(deb.r, deb.tmp)
	if err != nil {
		errorf("debug: read error: %s", err)
	}
	deb.consumed(w)
	return n
}

// GobStream:
//
//	DelimitedMessage* (until EOF)
func (deb *debugger) gobStream() {
	// 确保此处是单线程执行的。
	deb.mutex.Lock()
	defer deb.mutex.Unlock()

	for deb.delimitedMessage(0) {
	}
}

// DelimitedMessage:
//
//	uint(lengthOfMessage) Message
func (deb *debugger) delimitedMessage(indent tab) bool {
	for {
		n := deb.loadBlock(true)
		if n < 0 {
			return false
		}
		deb.dump("Delimited message of length %d", n)
		deb.message(indent)
	}
	return true
}

// loadBlock 让我们准备好读取输入中接下来指定的长度的消息。
// 它返回该块的长度。参数指明现在是否可接受 EOF。
// 如果可以且遇到了 EOF，则返回值为负数。
func (deb *debugger) loadBlock(eofOK bool) int {
	n64, w, err := decodeUintReader(deb.r, deb.tmp) // deb.uint64 在 EOF 时会报错
	if err != nil {
		if eofOK && err == io.EOF {
			return -1
		}
		errorf("debug: unexpected error: %s", err)
	}
	deb.consumed(w)
	n := int(n64)
	if n < 0 {
		errorf("huge value for message length: %d", n64)
	}
	return int(n)
}

// Message:
//
//	TypeSequence TypedValue
//
// TypeSequence
//
//	(TypeDefinition DelimitedTypeDefinition*)?
//
// DelimitedTypeDefinition:
//
//	uint(lengthOfTypeDefinition) TypeDefinition
//
// TypedValue:
//
//	int(typeId) Value
func (deb *debugger) message(indent tab) bool {
	for {
		// 将 uint64 转换为有符号整数 typeId
		uid := deb.int64()
		id := typeId(uid)
		deb.dump("type id=%d", id)
		if id < 0 {
			deb.typeDefinition(indent, -id)
			n := deb.loadBlock(false)
			deb.dump("Message of length %d", n)
			continue
		} else {
			deb.value(indent, id)
			break
		}
	}
	return true
}

// 辅助方法，便于扫描类型描述符。

// common 返回输入点处的 CommonType。
func (deb *debugger) common() CommonType {
	fieldNum := -1
	name := ""
	id := typeId(0)
	for {
		delta := deb.delta(-1)
		if delta == 0 {
			break
		}
		fieldNum += delta
		switch fieldNum {
		case 0:
			name = deb.string()
		case 1:
			// Id typeId
			id = deb.typeId()
		default:
			errorf("corrupted CommonType, delta is %d fieldNum is %d", delta, fieldNum)
		}
	}
	return CommonType{name, id}
}

// uint 以 uint（而非 uint64）返回输入点处的无符号整数。
func (deb *debugger) uint() uint {
	return uint(deb.uint64())
}

// int 以 int（而非 int64）返回输入点处的有符号整数。
func (deb *debugger) int() int {
	return int(deb.int64())
}

// typeId 返回输入点处的类型 id。
func (deb *debugger) typeId() typeId {
	return typeId(deb.int64())
}

// string 返回输入点处的字符串。
func (deb *debugger) string() string {
	x := int(deb.uint64())
	b := make([]byte, x)
	nb, _ := deb.r.Read(b)
	if nb != x {
		errorf("corrupted type")
	}
	deb.consumed(nb)
	return string(b)
}

// delta 返回输入点处的字段增量。expect 参数若为非负数，
// 则指明该值应当是多少。
func (deb *debugger) delta(expect int) int {
	delta := int(deb.uint64())
	if delta < 0 || (expect >= 0 && delta != expect) {
		errorf("decode: corrupted type: delta %d expected %d", delta, expect)
	}
	return delta
}

// TypeDefinition:
//
//	[int(-typeId) (already read)] encodingOfWireType
func (deb *debugger) typeDefinition(indent tab, id typeId) {
	deb.dump("type definition for id %d", id)
	// 编码内容是一个 wireType。像往常一样解码该结构
	fieldNum := -1
	wire := new(wireType)
	// 一个 wireType 定义单个字段。
	delta := deb.delta(-1)
	fieldNum += delta
	switch fieldNum {
	case 0: // 数组类型，一个形如 {{Common}, elem, length} 的字段
		// 字段号 0 是 CommonType
		deb.delta(1)
		com := deb.common()
		// 字段号 1 是 elem 的类型 Id
		deb.delta(1)
		id := deb.typeId()
		// 字段号 3 是长度
		deb.delta(1)
		length := deb.int()
		wire.ArrayT = &arrayType{com, id, length}

	case 1: // 切片类型，一个形如 {{Common}, elem} 的字段
		// 字段号 0 是 CommonType
		deb.delta(1)
		com := deb.common()
		// 字段号 1 是 elem 的类型 Id
		deb.delta(1)
		id := deb.typeId()
		wire.SliceT = &sliceType{com, id}

	case 2: // 结构体类型，一个形如 {{Common}, []fieldType} 的字段
		// 字段号 0 是 CommonType
		deb.delta(1)
		com := deb.common()
		// 字段号 1 是 FieldType 的切片
		deb.delta(1)
		numField := int(deb.uint())
		field := make([]*fieldType, numField)
		for i := 0; i < numField; i++ {
			field[i] = new(fieldType)
			deb.delta(1) // fieldType 的字段 0：name
			field[i].Name = deb.string()
			deb.delta(1) // fieldType 的字段 1：id
			field[i].Id = deb.typeId()
			deb.delta(0) // fieldType 结束
		}
		wire.StructT = &structType{com, field}

	case 3: // map 类型，一个形如 {{Common}, key, elem} 的字段
		// 字段号 0 是 CommonType
		deb.delta(1)
		com := deb.common()
		// 字段号 1 是 key 的类型 Id
		deb.delta(1)
		keyId := deb.typeId()
		// 字段号 2 是 elem 的类型 Id
		deb.delta(1)
		elemId := deb.typeId()
		wire.MapT = &mapType{com, keyId, elemId}
	case 4: // GobEncoder 类型，一个形如 {{Common}} 的字段
		// 字段号 0 是 CommonType
		deb.delta(1)
		com := deb.common()
		wire.GobEncoderT = &gobEncoderType{com}
	case 5: // BinaryMarshaler 类型，一个形如 {{Common}} 的字段
		// 字段号 0 是 CommonType
		deb.delta(1)
		com := deb.common()
		wire.BinaryMarshalerT = &gobEncoderType{com}
	case 6: // TextMarshaler 类型，一个形如 {{Common}} 的字段
		// 字段号 0 是 CommonType
		deb.delta(1)
		com := deb.common()
		wire.TextMarshalerT = &gobEncoderType{com}
	default:
		errorf("bad field in type %d", fieldNum)
	}
	deb.printWireType(indent, wire)
	deb.delta(0) // 结束内部类型（arrayType 等）
	deb.delta(0) // 结束 wireType
	// 记住我们已经见过此类型。
	deb.wireType[id] = wire
}

// Value:
//
//	SingletonValue | StructValue
func (deb *debugger) value(indent tab, id typeId) {
	wire, ok := deb.wireType[id]
	if ok && wire.StructT != nil {
		deb.structValue(indent, id)
	} else {
		deb.singletonValue(indent, id)
	}
}

// SingletonValue:
//
//	uint(0) FieldValue
func (deb *debugger) singletonValue(indent tab, id typeId) {
	deb.dump("Singleton value")
	// 它是内建类型吗？
	wire := deb.wireType[id]
	if builtinIdToType(id) == nil && wire == nil {
		errorf("type id %d not defined", id)
	}
	m := deb.uint64()
	if m != 0 {
		errorf("expected zero; got %d", m)
	}
	deb.fieldValue(indent, id)
}

// InterfaceValue:
//
//	NilInterfaceValue | NonNilInterfaceValue
func (deb *debugger) interfaceValue(indent tab) {
	deb.dump("Start of interface value")
	if nameLen := deb.uint64(); nameLen == 0 {
		deb.nilInterfaceValue(indent)
	} else {
		deb.nonNilInterfaceValue(indent, int(nameLen))
	}
}

// NilInterfaceValue:
//
//	uint(0) [already read]
func (deb *debugger) nilInterfaceValue(indent tab) int {
	fmt.Fprintf(os.Stderr, "%snil interface\n", indent)
	return 0
}

// NonNilInterfaceValue:
//
//	ConcreteTypeName TypeSequence InterfaceContents
//
// ConcreteTypeName:
//
//	uint(lengthOfName) [already read=n] name
//
// InterfaceContents:
//
//	int(concreteTypeId) DelimitedValue
//
// DelimitedValue:
//
//	uint(length) Value
func (deb *debugger) nonNilInterfaceValue(indent tab, nameLen int) {
	// ConcreteTypeName
	b := make([]byte, nameLen)
	deb.r.Read(b) // TODO: 检查这些读取操作！！
	deb.consumed(nameLen)
	name := string(b)

	for {
		id := deb.typeId()
		if id < 0 {
			deb.typeDefinition(indent, -id)
			n := deb.loadBlock(false)
			deb.dump("Nested message of length %d", n)
		} else {
			// DelimitedValue
			x := deb.uint64() // 以备我们可能想忽略该值；我们并不忽略。
			fmt.Fprintf(os.Stderr, "%sinterface value, type %q id=%d; valueLength %d\n", indent, name, id, x)
			deb.value(indent, id)
			break
		}
	}
}

// printCommonType 打印一个公共类型；由 printWireType 使用。
func (deb *debugger) printCommonType(indent tab, kind string, common *CommonType) {
	indent.print()
	fmt.Fprintf(os.Stderr, "%s %q id=%d\n", kind, common.Name, common.Id)
}

// printWireType 打印一个 wireType 的内容。
func (deb *debugger) printWireType(indent tab, wire *wireType) {
	fmt.Fprintf(os.Stderr, "%stype definition {\n", indent)
	indent++
	switch {
	case wire.ArrayT != nil:
		deb.printCommonType(indent, "array", &wire.ArrayT.CommonType)
		fmt.Fprintf(os.Stderr, "%slen %d\n", indent+1, wire.ArrayT.Len)
		fmt.Fprintf(os.Stderr, "%selemid %d\n", indent+1, wire.ArrayT.Elem)
	case wire.MapT != nil:
		deb.printCommonType(indent, "map", &wire.MapT.CommonType)
		fmt.Fprintf(os.Stderr, "%skey id=%d\n", indent+1, wire.MapT.Key)
		fmt.Fprintf(os.Stderr, "%selem id=%d\n", indent+1, wire.MapT.Elem)
	case wire.SliceT != nil:
		deb.printCommonType(indent, "slice", &wire.SliceT.CommonType)
		fmt.Fprintf(os.Stderr, "%selem id=%d\n", indent+1, wire.SliceT.Elem)
	case wire.StructT != nil:
		deb.printCommonType(indent, "struct", &wire.StructT.CommonType)
		for i, field := range wire.StructT.Field {
			fmt.Fprintf(os.Stderr, "%sfield %d:\t%s\tid=%d\n", indent+1, i, field.Name, field.Id)
		}
	case wire.GobEncoderT != nil:
		deb.printCommonType(indent, "GobEncoder", &wire.GobEncoderT.CommonType)
	}
	indent--
	fmt.Fprintf(os.Stderr, "%s}\n", indent)
}

// fieldValue 打印任意类型的值，例如结构体字段。
// FieldValue:
//
//	builtinValue | ArrayValue | MapValue | SliceValue | StructValue | InterfaceValue
func (deb *debugger) fieldValue(indent tab, id typeId) {
	if builtinIdToType(id) != nil {
		if id == tInterface {
			deb.interfaceValue(indent)
		} else {
			deb.printBuiltin(indent, id)
		}
		return
	}
	wire, ok := deb.wireType[id]
	if !ok {
		errorf("type id %d not defined", id)
	}
	switch {
	case wire.ArrayT != nil:
		deb.arrayValue(indent, wire)
	case wire.MapT != nil:
		deb.mapValue(indent, wire)
	case wire.SliceT != nil:
		deb.sliceValue(indent, wire)
	case wire.StructT != nil:
		deb.structValue(indent, id)
	case wire.GobEncoderT != nil:
		deb.gobEncoderValue(indent, id)
	default:
		panic("bad wire type for field")
	}
}

// printBuiltin 打印一个并非基本类型的值，
// 即其类型在引导时对 gob 是已知的。
func (deb *debugger) printBuiltin(indent tab, id typeId) {
	switch id {
	case tBool:
		x := deb.int64()
		if x == 0 {
			fmt.Fprintf(os.Stderr, "%sfalse\n", indent)
		} else {
			fmt.Fprintf(os.Stderr, "%strue\n", indent)
		}
	case tInt:
		x := deb.int64()
		fmt.Fprintf(os.Stderr, "%s%d\n", indent, x)
	case tUint:
		x := deb.uint64()
		fmt.Fprintf(os.Stderr, "%s%d\n", indent, x)
	case tFloat:
		x := deb.uint64()
		fmt.Fprintf(os.Stderr, "%s%g\n", indent, float64FromBits(x))
	case tComplex:
		r := deb.uint64()
		i := deb.uint64()
		fmt.Fprintf(os.Stderr, "%s%g+%gi\n", indent, float64FromBits(r), float64FromBits(i))
	case tBytes:
		x := int(deb.uint64())
		b := make([]byte, x)
		deb.r.Read(b)
		deb.consumed(x)
		fmt.Fprintf(os.Stderr, "%s{% x}=%q\n", indent, b, b)
	case tString:
		x := int(deb.uint64())
		b := make([]byte, x)
		deb.r.Read(b)
		deb.consumed(x)
		fmt.Fprintf(os.Stderr, "%s%q\n", indent, b)
	default:
		panic("unknown builtin")
	}
}

// ArrayValue:
//
//	uint(n) FieldValue*n
func (deb *debugger) arrayValue(indent tab, wire *wireType) {
	elemId := wire.ArrayT.Elem
	u := deb.uint64()
	length := int(u)
	for i := 0; i < length; i++ {
		deb.fieldValue(indent, elemId)
	}
	if length != wire.ArrayT.Len {
		fmt.Fprintf(os.Stderr, "%s(wrong length for array: %d should be %d)\n", indent, length, wire.ArrayT.Len)
	}
}

// MapValue:
//
//	uint(n) (FieldValue FieldValue)*n  [n (key, value) pairs]
func (deb *debugger) mapValue(indent tab, wire *wireType) {
	keyId := wire.MapT.Key
	elemId := wire.MapT.Elem
	u := deb.uint64()
	length := int(u)
	for i := 0; i < length; i++ {
		deb.fieldValue(indent+1, keyId)
		deb.fieldValue(indent+1, elemId)
	}
}

// SliceValue:
//
//	uint(n) (n FieldValue)
func (deb *debugger) sliceValue(indent tab, wire *wireType) {
	elemId := wire.SliceT.Elem
	u := deb.uint64()
	length := int(u)
	deb.dump("Start of slice of length %d", length)

	for i := 0; i < length; i++ {
		deb.fieldValue(indent, elemId)
	}
}

// StructValue:
//
//	(uint(fieldDelta) FieldValue)*
func (deb *debugger) structValue(indent tab, id typeId) {
	deb.dump("Start of struct value of %q id=%d\n<<\n", id.name(), id)
	fmt.Fprintf(os.Stderr, "%s%s struct {\n", indent, id.name())
	wire, ok := deb.wireType[id]
	if !ok {
		errorf("type id %d not defined", id)
	}
	strct := wire.StructT
	fieldNum := -1
	indent++
	for {
		delta := deb.uint64()
		if delta == 0 { // 结构体终止符是增量为零的 fieldnum
			break
		}
		fieldNum += int(delta)
		if fieldNum < 0 || fieldNum >= len(strct.Field) {
			deb.dump("field number out of range: prevField=%d delta=%d", fieldNum-int(delta), delta)
			break
		}
		fmt.Fprintf(os.Stderr, "%sfield %d:\t%s\n", indent, fieldNum, wire.StructT.Field[fieldNum].Name)
		deb.fieldValue(indent+1, strct.Field[fieldNum].Id)
	}
	indent--
	fmt.Fprintf(os.Stderr, "%s} // end %s struct\n", indent, id.name())
	deb.dump(">> End of struct value of type %d %q", id, id.name())
}

// GobEncoderValue:
//
//	uint(n) byte*n
func (deb *debugger) gobEncoderValue(indent tab, id typeId) {
	len := deb.uint64()
	deb.dump("GobEncoder value of %q id=%d, length %d\n", id.name(), id, len)
	fmt.Fprintf(os.Stderr, "%s%s (implements GobEncoder)\n", indent, id.name())
	data := make([]byte, len)
	_, err := deb.r.Read(data)
	if err != nil {
		errorf("gobEncoder data read: %s", err)
	}
	fmt.Fprintf(os.Stderr, "%s[% .2x]\n", indent+1, data)
}
