// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gob

import (
	"encoding"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// userTypeInfo 存储与用户交给本包的类型相关联的信息。
// 它只计算一次，并存储在以反射类型为键的 map 中。
type userTypeInfo struct {
	user        reflect.Type // 用户交给我们的类型
	base        reflect.Type // 所有间接寻址之后的基础类型
	indir       int          // 到达基础类型所需的间接寻址层数
	externalEnc int          // xGob、xBinary 或 xText
	externalDec int          // xGob、xBinary 或 xText
	encIndir    int8         // 到达接收者类型所需的间接寻址层数；可能为负数
	decIndir    int8         // 到达接收者类型所需的间接寻址层数；可能为负数
}

// externalEncoding 位
const (
	xGob    = 1 + iota // GobEncoder 或 GobDecoder
	xBinary            // encoding.BinaryMarshaler 或 encoding.BinaryUnmarshaler
	xText              // encoding.TextMarshaler 或 encoding.TextUnmarshaler
)

var userTypeCache sync.Map // map[reflect.Type]*userTypeInfo

// validUserType 返回并保存与用户提供的类型 rt 相关联的信息。
// 如果用户类型无效，err 将为非 nil。用于未设置错误处理器时的场合。
func validUserType(rt reflect.Type) (*userTypeInfo, error) {
	if ui, ok := userTypeCache.Load(rt); ok {
		return ui.(*userTypeInfo), nil
	}

	// 构造一个新的 userTypeInfo 并将其原子地加入 userTypeCache。
	// 如果竞争失败，我们会浪费一点 CPU 并产生一点垃圾，
	// 但仍返回已存在的值。

	ut := new(userTypeInfo)
	ut.base = rt
	ut.user = rt
	// 一个只是指针循环的类型（例如 type T *T）无法在 gob 中表示，
	// 因为 gob 需要一些具体的数据。我们使用 Knuth《计算机程序设计艺术》
	// 第 2 卷第 3.1 节习题 6（pp 539-540）中的循环检测算法。
	// 当我们逐层走过间接寻址时，让另一个类型以半速前进。如果它们相遇，
	// 就存在一个循环。
	slowpoke := ut.base // 以 ut.base 一半的速度前进
	for {
		pt := ut.base
		if pt.Kind() != reflect.Pointer {
			break
		}
		ut.base = pt.Elem()
		if ut.base == slowpoke { // ut.base 超过了 slowpoke
			// 递归指针类型。
			return nil, errors.New("can't represent recursive pointer type " + ut.base.String())
		}
		if ut.indir%2 == 0 {
			slowpoke = slowpoke.Elem()
		}
		ut.indir++
	}

	if ok, indir := implementsInterface(ut.user, gobEncoderInterfaceType); ok {
		ut.externalEnc, ut.encIndir = xGob, indir
	} else if ok, indir := implementsInterface(ut.user, binaryMarshalerInterfaceType); ok {
		ut.externalEnc, ut.encIndir = xBinary, indir
	}

	// NOTE(rsc)：这里本想允许 MarshalText，但这会导致
	// 与 net.IP 旧有编码不兼容。参见 golang.org/issue/6760。
	// } else if ok, indir := implementsInterface(ut.user, textMarshalerInterfaceType); ok {
	// 	ut.externalEnc, ut.encIndir = xText, indir
	// }

	if ok, indir := implementsInterface(ut.user, gobDecoderInterfaceType); ok {
		ut.externalDec, ut.decIndir = xGob, indir
	} else if ok, indir := implementsInterface(ut.user, binaryUnmarshalerInterfaceType); ok {
		ut.externalDec, ut.decIndir = xBinary, indir
	}

	// 参见上面的注释。
	// } else if ok, indir := implementsInterface(ut.user, textUnmarshalerInterfaceType); ok {
	// 	ut.externalDec, ut.decIndir = xText, indir
	// }

	ui, _ := userTypeCache.LoadOrStore(rt, ut)
	return ui.(*userTypeInfo), nil
}

var (
	gobEncoderInterfaceType        = reflect.TypeFor[GobEncoder]()
	gobDecoderInterfaceType        = reflect.TypeFor[GobDecoder]()
	binaryMarshalerInterfaceType   = reflect.TypeFor[encoding.BinaryMarshaler]()
	binaryUnmarshalerInterfaceType = reflect.TypeFor[encoding.BinaryUnmarshaler]()
	textMarshalerInterfaceType     = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshalerInterfaceType   = reflect.TypeFor[encoding.TextUnmarshaler]()

	wireTypeType = reflect.TypeFor[wireType]()
)

// implementsInterface 报告该类型是否实现了
// gobEncoder/gobDecoder 接口。
// 它还返回到达该实现所需的间接寻址层数。
func implementsInterface(typ, gobEncDecType reflect.Type) (success bool, indir int8) {
	if typ == nil {
		return
	}
	rt := typ
	// 该类型可能是指针，我们需要不断解引用到基础类型，
	// 直到找到实现为止。
	for {
		if rt.Implements(gobEncDecType) {
			return true, indir
		}
		if p := rt; p.Kind() == reflect.Pointer {
			indir++
			if indir > 100 { // 间接寻址层数多得离谱
				return false, 0
			}
			rt = p.Elem()
			continue
		}
		break
	}
	// 还没找到，但如果这是基础类型（非指针），指针也许能满足。
	if typ.Kind() != reflect.Pointer {
		// 不是指针，但它的指针能用吗？
		if reflect.PointerTo(typ).Implements(gobEncDecType) {
			return true, -1
		}
	}
	return false, 0
}

// userType 返回并保存与用户提供的类型 rt 相关联的信息。
// 如果用户类型无效，它会调用 error。
func userType(rt reflect.Type) *userTypeInfo {
	ut, err := validUserType(rt)
	if err != nil {
		error_(err)
	}
	return ut
}

// typeId 将 gob 类型表示为一个可以在线路上传递的整数。
// 在内部，typeId 被用作 map 的键，以取回底层的类型信息。
type typeId int32

var typeLock sync.Mutex // 在构建类型时设置
const firstUserId = 64  // 授予用户的最低 id 编号

type gobType interface {
	id() typeId
	setId(id typeId)
	name() string
	string() string // 非公开；仅用于调试
	safeString(seen map[typeId]bool) string
}

var (
	types                = make(map[reflect.Type]gobType, 32)
	idToTypeSlice        = make([]gobType, 1, firstUserId)
	builtinIdToTypeSlice [firstUserId]gobType // 在内建类型建立之后于 init() 中设置
)

func idToType(id typeId) gobType {
	if id < 0 || int(id) >= len(idToTypeSlice) {
		return nil
	}
	return idToTypeSlice[id]
}

func builtinIdToType(id typeId) gobType {
	if id < 0 || int(id) >= len(builtinIdToTypeSlice) {
		return nil
	}
	return builtinIdToTypeSlice[id]
}

func setTypeId(typ gobType) {
	// 在构建递归类型时，可能有人先于我们到达这里。
	if typ.id() != 0 {
		return
	}
	nextId := typeId(len(idToTypeSlice))
	typ.setId(nextId)
	idToTypeSlice = append(idToTypeSlice, typ)
}

func (t typeId) gobType() gobType {
	if t == 0 {
		return nil
	}
	return idToType(t)
}

// string 返回与 typeId 相关联的类型的字符串表示。
func (t typeId) string() string {
	if t.gobType() == nil {
		return "<nil>"
	}
	return t.gobType().string()
}

// Name 返回与 typeId 相关联的类型的名称。
func (t typeId) name() string {
	if t.gobType() == nil {
		return "<nil>"
	}
	return t.gobType().name()
}

// CommonType 保存所有类型的公共元素。
// 它是一个历史遗留物，为二进制兼容性而保留，并且仅为本包编码类型描述符
// 而导出。它不供客户端直接使用。
type CommonType struct {
	Name string
	Id   typeId
}

func (t *CommonType) id() typeId { return t.Id }

func (t *CommonType) setId(id typeId) { t.Id = id }

func (t *CommonType) string() string { return t.Name }

func (t *CommonType) safeString(seen map[typeId]bool) string {
	return t.Name
}

func (t *CommonType) name() string { return t.Name }

// 创建并检查预定义类型
// tBytes 的字符串是 "bytes" 而不是 "[]byte"，以表明其特殊性。

var (
	// 原始类型，初始化期间需要。
	// 始终以指针形式传递，以便 interface{} 类型
	// 通过时不丢失其接口性。
	tBool      = bootstrapType("bool", (*bool)(nil))
	tInt       = bootstrapType("int", (*int)(nil))
	tUint      = bootstrapType("uint", (*uint)(nil))
	tFloat     = bootstrapType("float", (*float64)(nil))
	tBytes     = bootstrapType("bytes", (*[]byte)(nil))
	tString    = bootstrapType("string", (*string)(nil))
	tComplex   = bootstrapType("complex", (*complex128)(nil))
	tInterface = bootstrapType("interface", (*any)(nil))
	// 预留一些 Id 以便兼容性扩展
	tReserved7 = bootstrapType("_reserved1", (*struct{ r7 int })(nil))
	tReserved6 = bootstrapType("_reserved1", (*struct{ r6 int })(nil))
	tReserved5 = bootstrapType("_reserved1", (*struct{ r5 int })(nil))
	tReserved4 = bootstrapType("_reserved1", (*struct{ r4 int })(nil))
	tReserved3 = bootstrapType("_reserved1", (*struct{ r3 int })(nil))
	tReserved2 = bootstrapType("_reserved1", (*struct{ r2 int })(nil))
	tReserved1 = bootstrapType("_reserved1", (*struct{ r1 int })(nil))
)

// 预定义，因为 Decoder 需要它
var tWireType = mustGetTypeInfo(wireTypeType).id
var wireTypeUserInfo *userTypeInfo // wireType 的 userTypeInfo

func init() {
	// 一些魔数，以确保不会出现意外。
	checkId(16, tWireType)
	checkId(17, mustGetTypeInfo(reflect.TypeFor[arrayType]()).id)
	checkId(18, mustGetTypeInfo(reflect.TypeFor[CommonType]()).id)
	checkId(19, mustGetTypeInfo(reflect.TypeFor[sliceType]()).id)
	checkId(20, mustGetTypeInfo(reflect.TypeFor[structType]()).id)
	checkId(21, mustGetTypeInfo(reflect.TypeFor[fieldType]()).id)
	checkId(23, mustGetTypeInfo(reflect.TypeFor[mapType]()).id)

	copy(builtinIdToTypeSlice[:], idToTypeSlice)

	// 将 id 空间上移，以便预定义世界能够扩展
	// 而不破坏现有文件。
	if nextId := len(idToTypeSlice); nextId > firstUserId {
		panic(fmt.Sprintln("nextId too large:", nextId))
	}
	idToTypeSlice = idToTypeSlice[:firstUserId]
	registerBasics()
	wireTypeUserInfo = userType(wireTypeType)
}

// 数组类型
type arrayType struct {
	CommonType
	Elem typeId
	Len  int
}

func newArrayType(name string) *arrayType {
	a := &arrayType{CommonType{Name: name}, 0, 0}
	return a
}

func (a *arrayType) init(elem gobType, len int) {
	// 先设置我们自己的类型 id，再求值元素的类型 id，以防元素就是我们自己。
	setTypeId(a)
	a.Elem = elem.id()
	a.Len = len
}

func (a *arrayType) safeString(seen map[typeId]bool) string {
	if seen[a.Id] {
		return a.Name
	}
	seen[a.Id] = true
	return fmt.Sprintf("[%d]%s", a.Len, a.Elem.gobType().safeString(seen))
}

func (a *arrayType) string() string { return a.safeString(make(map[typeId]bool)) }

// GobEncoder 类型（实现了 GobEncoder 接口的东西）
type gobEncoderType struct {
	CommonType
}

func newGobEncoderType(name string) *gobEncoderType {
	g := &gobEncoderType{CommonType{Name: name}}
	setTypeId(g)
	return g
}

func (g *gobEncoderType) safeString(seen map[typeId]bool) string {
	return g.Name
}

func (g *gobEncoderType) string() string { return g.Name }

// Map 类型
type mapType struct {
	CommonType
	Key  typeId
	Elem typeId
}

func newMapType(name string) *mapType {
	m := &mapType{CommonType{Name: name}, 0, 0}
	return m
}

func (m *mapType) init(key, elem gobType) {
	// 先设置我们自己的类型 id，再求值元素的类型 id，以防元素就是我们自己。
	setTypeId(m)
	m.Key = key.id()
	m.Elem = elem.id()
}

func (m *mapType) safeString(seen map[typeId]bool) string {
	if seen[m.Id] {
		return m.Name
	}
	seen[m.Id] = true
	key := m.Key.gobType().safeString(seen)
	elem := m.Elem.gobType().safeString(seen)
	return fmt.Sprintf("map[%s]%s", key, elem)
}

func (m *mapType) string() string { return m.safeString(make(map[typeId]bool)) }

// 切片类型
type sliceType struct {
	CommonType
	Elem typeId
}

func newSliceType(name string) *sliceType {
	s := &sliceType{CommonType{Name: name}, 0}
	return s
}

func (s *sliceType) init(elem gobType) {
	// 先设置我们自己的类型 id，再求值元素的类型 id，以防元素就是我们自己。
	setTypeId(s)
	// 关于 id 的说明参见 newTypeObject 中的注释。只有切片和
	// 结构体存在相互递归。
	if elem.id() == 0 {
		setTypeId(elem)
	}
	s.Elem = elem.id()
}

func (s *sliceType) safeString(seen map[typeId]bool) string {
	if seen[s.Id] {
		return s.Name
	}
	seen[s.Id] = true
	return fmt.Sprintf("[]%s", s.Elem.gobType().safeString(seen))
}

func (s *sliceType) string() string { return s.safeString(make(map[typeId]bool)) }

// 结构体类型
type fieldType struct {
	Name string
	Id   typeId
}

type structType struct {
	CommonType
	Field []fieldType
}

func (s *structType) safeString(seen map[typeId]bool) string {
	if s == nil {
		return "<nil>"
	}
	if _, ok := seen[s.Id]; ok {
		return s.Name
	}
	seen[s.Id] = true
	str := s.Name + " = struct { "
	for _, f := range s.Field {
		str += fmt.Sprintf("%s %s; ", f.Name, f.Id.gobType().safeString(seen))
	}
	str += "}"
	return str
}

func (s *structType) string() string { return s.safeString(make(map[typeId]bool)) }

func newStructType(name string) *structType {
	s := &structType{CommonType{Name: name}, nil}
	// 由于历史原因，我们在这里而非 init 中设置 id。
	// 详见 newTypeObject 中的注释。
	setTypeId(s)
	return s
}

// newTypeObject 为反射类型 rt 分配一个 gobType。
// 除非 ut 表示一个 GobEncoder，否则 rt 应当是 ut 的基础类型。
// 这仅从编码端调用。解码端仅通过 typeId 和 userTypeInfo 工作。
func newTypeObject(name string, ut *userTypeInfo, rt reflect.Type) (gobType, error) {
	// 该类型实现了 GobEncoder 吗？
	if ut.externalEnc != 0 {
		return newGobEncoderType(name), nil
	}
	var err error
	var type0, type1 gobType
	defer func() {
		if err != nil {
			delete(types, rt)
		}
	}()
	// 先安装顶层类型，再安装子类型（例如先结构体后字段），
	// 以便能够安全地构造递归类型。
	switch t := rt; t.Kind() {
	// 所有基本类型都很简单：它们是预定义的。
	case reflect.Bool:
		return tBool.gobType(), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return tInt.gobType(), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return tUint.gobType(), nil

	case reflect.Float32, reflect.Float64:
		return tFloat.gobType(), nil

	case reflect.Complex64, reflect.Complex128:
		return tComplex.gobType(), nil

	case reflect.String:
		return tString.gobType(), nil

	case reflect.Interface:
		return tInterface.gobType(), nil

	case reflect.Array:
		at := newArrayType(name)
		types[rt] = at
		type0, err = getBaseType("", t.Elem())
		if err != nil {
			return nil, err
		}
		// 历史插曲：
		// 对于数组、map 和切片，我们在元素构造之后才设置类型 id。
		// 这是为了在修复递归类型处理之后保留类型 id 的分配顺序，
		// 因为那次修复改变了类型构建的顺序。以这种方式延迟设置
		// 既保留了类型 id，又允许描述递归类型。下面处理的结构体
		// 本就已正确应对递归，因此它们在字段的 id 之前先分配顶层 id。
		at.init(type0, t.Len())
		return at, nil

	case reflect.Map:
		mt := newMapType(name)
		types[rt] = mt
		type0, err = getBaseType("", t.Key())
		if err != nil {
			return nil, err
		}
		type1, err = getBaseType("", t.Elem())
		if err != nil {
			return nil, err
		}
		mt.init(type0, type1)
		return mt, nil

	case reflect.Slice:
		// []byte == []uint8 是一种特殊情况
		if t.Elem().Kind() == reflect.Uint8 {
			return tBytes.gobType(), nil
		}
		st := newSliceType(name)
		types[rt] = st
		type0, err = getBaseType(t.Elem().Name(), t.Elem())
		if err != nil {
			return nil, err
		}
		st.init(type0)
		return st, nil

	case reflect.Struct:
		st := newStructType(name)
		types[rt] = st
		idToTypeSlice[st.id()] = st
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !isSent(&f) {
				continue
			}
			typ := userType(f.Type).base
			tname := typ.Name()
			if tname == "" {
				t := userType(f.Type).base
				tname = t.String()
			}
			gt, err := getBaseType(tname, f.Type)
			if err != nil {
				return nil, err
			}
			// 某些相互递归的类型可能导致我们在仍在定义元素时就来到这里。
			// 在这里修正元素的类型 id。
			// 我们本可以在构建每个类型之初就设置 id 以更简洁地做到这一点，
			// 但那会破坏二进制兼容性。
			if gt.id() == 0 {
				setTypeId(gt)
			}
			st.Field = append(st.Field, fieldType{f.Name, gt.id()})
		}
		return st, nil

	default:
		return nil, errors.New("gob NewTypeObject can't handle type: " + rt.String())
	}
}

// isExported 报告这是否是一个已导出（大写开头）的名称。
func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
}

// isSent 报告该结构体字段是否要被传输。
// 只有当它是已导出字段，且不是 chan 或 func 字段、也不是指向 chan 或 func
// 的指针时，才会被传输。
func isSent(field *reflect.StructField) bool {
	if !isExported(field.Name) {
		return false
	}
	// 如果该字段是 chan 或 func 或指向它们的指针，则不要发送它。
	// 也就是说，把它当作未导出字段对待。
	typ := field.Type
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Chan || typ.Kind() == reflect.Func {
		return false
	}

	return true
}

// getBaseType 返回描述给定 reflect.Type 基础类型的 Gob 类型。
// 必须持有 typeLock。
func getBaseType(name string, rt reflect.Type) (gobType, error) {
	ut := userType(rt)
	return getType(name, ut, ut.base)
}

// getType 返回描述给定 reflect.Type 的 Gob 类型。
// 只应在处理 GobEncoder/GobDecoder 时调用，因为它们可能是指针。
// 所有其他类型都通过基础类型处理，绝不通过指针处理。
// 必须持有 typeLock。
func getType(name string, ut *userTypeInfo, rt reflect.Type) (gobType, error) {
	typ, present := types[rt]
	if present {
		return typ, nil
	}
	typ, err := newTypeObject(name, ut, rt)
	if err == nil {
		types[rt] = typ
	}
	return typ, err
}

func checkId(want, got typeId) {
	if want != got {
		fmt.Fprintf(os.Stderr, "checkId: %d should be %d\n", int(got), int(want))
		panic("bootstrap type wrong id: " + got.name() + " " + got.string() + " not " + want.string())
	}
}

// 用于构建基本类型；仅从 init() 调用。传入的
// interface 始终引用一个指针。
func bootstrapType(name string, e any) typeId {
	rt := reflect.TypeOf(e).Elem()
	_, present := types[rt]
	if present {
		panic("bootstrap type already present: " + name + ", " + rt.String())
	}
	typ := &CommonType{Name: name}
	types[rt] = typ
	setTypeId(typ)
	return typ.id()
}

// 我们发送和接收的关于此类型的信息的表示。
// 我们发送的每个值前面都带有其类型定义：一个经过编码的 int。
// 不过，在我们第一次发送该值时，我们会先发送对
// (-id, wireType)。
// 出于引导目的，我们假定接收方知道如何解码 wireType；它就是这里的
// wireType 结构体，用发送结构体的 gob 规则来解释，只不过我们假定
// wireType 和 structType 等的 id 是已知的。相关部分在 encode.go 的
// init() 函数中构建。
// 为保持二进制兼容性，如果你扩展此类型，请总是把
// 新字段放在最后。
type wireType struct {
	ArrayT           *arrayType
	SliceT           *sliceType
	StructT          *structType
	MapT             *mapType
	GobEncoderT      *gobEncoderType
	BinaryMarshalerT *gobEncoderType
	TextMarshalerT   *gobEncoderType
}

func (w *wireType) string() string {
	const unknown = "unknown type"
	if w == nil {
		return unknown
	}
	switch {
	case w.ArrayT != nil:
		return w.ArrayT.Name
	case w.SliceT != nil:
		return w.SliceT.Name
	case w.StructT != nil:
		return w.StructT.Name
	case w.MapT != nil:
		return w.MapT.Name
	case w.GobEncoderT != nil:
		return w.GobEncoderT.Name
	case w.BinaryMarshalerT != nil:
		return w.BinaryMarshalerT.Name
	case w.TextMarshalerT != nil:
		return w.TextMarshalerT.Name
	}
	return unknown
}

type typeInfo struct {
	id      typeId
	encInit sync.Mutex // 保护 encoder 的创建
	encoder atomic.Pointer[encEngine]
	wire    wireType
}

// typeInfoMap 是一个指向 map[reflect.Type]*typeInfo 的原子指针。
// 它以写时复制的方式更新。读者只需做一次原子加载
// 即可获得该 map 的当前版本。写者会完整复制
// map，并原子地更新指针以指向新 map。
// 在读取竞争激烈的情况下，这比用互斥锁
// 保护的 map 快得多。
var typeInfoMap atomic.Value

// typeInfoMapInit 在初始化期间代替 typeInfoMap 使用，
// 因为类型在初始化期间是顺序注册的，我们可以省去
// 复制 map 的开销。
// 它在初始化结束前被保存到 typeInfoMap 并置为 nil。
var typeInfoMapInit = make(map[reflect.Type]*typeInfo, 16)

func lookupTypeInfo(rt reflect.Type) *typeInfo {
	if m := typeInfoMapInit; m != nil {
		return m[rt]
	}
	m, _ := typeInfoMap.Load().(map[reflect.Type]*typeInfo)
	return m[rt]
}

func getTypeInfo(ut *userTypeInfo) (*typeInfo, error) {
	rt := ut.base
	if ut.externalEnc != 0 {
		// 我们需要用户类型，而非基础类型。
		rt = ut.user
	}
	if info := lookupTypeInfo(rt); info != nil {
		return info, nil
	}
	return buildTypeInfo(ut, rt)
}

// buildTypeInfo 构造该类型的类型信息，
// 并将其存储在类型信息 map 中。
func buildTypeInfo(ut *userTypeInfo, rt reflect.Type) (*typeInfo, error) {
	typeLock.Lock()
	defer typeLock.Unlock()

	if info := lookupTypeInfo(rt); info != nil {
		return info, nil
	}

	gt, err := getBaseType(rt.Name(), rt)
	if err != nil {
		return nil, err
	}
	info := &typeInfo{id: gt.id()}

	if ut.externalEnc != 0 {
		userType, err := getType(rt.Name(), ut, rt)
		if err != nil {
			return nil, err
		}
		gt := userType.id().gobType().(*gobEncoderType)
		switch ut.externalEnc {
		case xGob:
			info.wire.GobEncoderT = gt
		case xBinary:
			info.wire.BinaryMarshalerT = gt
		case xText:
			info.wire.TextMarshalerT = gt
		}
		rt = ut.user
	} else {
		t := info.id.gobType()
		switch typ := rt; typ.Kind() {
		case reflect.Array:
			info.wire.ArrayT = t.(*arrayType)
		case reflect.Map:
			info.wire.MapT = t.(*mapType)
		case reflect.Slice:
			// []byte == []uint8 是单独处理的特殊情况
			if typ.Elem().Kind() != reflect.Uint8 {
				info.wire.SliceT = t.(*sliceType)
			}
		case reflect.Struct:
			info.wire.StructT = t.(*structType)
		}
	}

	if m := typeInfoMapInit; m != nil {
		m[rt] = info
		return info, nil
	}

	// 创建包含旧内容加上新条目的新 map。
	m, _ := typeInfoMap.Load().(map[reflect.Type]*typeInfo)
	newm := maps.Clone(m)
	newm[rt] = info
	typeInfoMap.Store(newm)
	return info, nil
}

// 仅在 panic 可接受且出乎意料时才调用。
func mustGetTypeInfo(rt reflect.Type) *typeInfo {
	t, err := getTypeInfo(userType(rt))
	if err != nil {
		panic("getTypeInfo: " + err.Error())
	}
	return t
}

// GobEncoder 是描述数据的接口，这些数据为编码值以传输给 GobDecoder
// 提供自己的表示形式。
// 同时实现 GobEncoder 和 GobDecoder 的类型对其数据的表示形式拥有完全
// 控制权，因此可以包含诸如私有字段、通道和函数之类的内容，
// 这些内容通常无法在 gob 流中传输。
//
// 注意：由于 gob 可以被永久存储，保证 GobEncoder 所使用的编码随
// 软件演进保持稳定是一种良好的设计。例如，让 GobEncode
// 在编码中包含一个版本号可能是有意义的。
type GobEncoder interface {
	// GobEncode 返回一个字节切片，表示接收者的编码，以便传输给
	// GobDecoder，通常用于相同的具体类型。
	GobEncode() ([]byte, error)
}

// GobDecoder 是描述数据的接口，这些数据为解码由 GobEncoder
// 发送的传输值提供自己的例程。
type GobDecoder interface {
	// GobDecode 用字节切片所表示的值覆盖接收者（接收者必须是指针），
	// 该字节切片由 GobEncode 写入，通常用于相同的具体类型。
	GobDecode([]byte) error
}

var (
	nameToConcreteType sync.Map // map[string]reflect.Type
	concreteTypeToName sync.Map // map[reflect.Type]string
)

// RegisterName 类似于 [Register]，但使用提供的名称而非
// 类型的默认名称。
func RegisterName(name string, value any) {
	if name == "" {
		// 保留给 nil
		panic("attempt to register empty name")
	}

	ut := userType(reflect.TypeOf(value))

	// 检查不兼容的重复项。该名称必须指向
	// 同一个用户类型，反之亦然。

	// 存储用户提供的名称和类型……
	if t, dup := nameToConcreteType.LoadOrStore(name, reflect.TypeOf(value)); dup && t != ut.user {
		panic(fmt.Sprintf("gob: registering duplicate types for %q: %s != %s", name, t, ut.user))
	}

	// 但类型表中存的是展平后的类型，因为解码需要它。
	if n, dup := concreteTypeToName.LoadOrStore(ut.base, name); dup && n != name {
		nameToConcreteType.Delete(name)
		panic(fmt.Sprintf("gob: registering duplicate names for %s: %q != %q", ut.user, n, name))
	}
}

// Register 记录一个类型（由该类型的一个值标识），并使用其
// 内部类型名。该名称将标识作为 interface 变量发送或接收的值的
// 具体类型。只有将作为 interface 值的实现来传输的类型才需要注册。
// 预期仅在初始化期间使用；如果类型与名称之间的映射不是双射，
// 它会 panic。
func Register(value any) {
	// 对于未命名类型，默认采用打印表示
	rt := reflect.TypeOf(value)
	name := rt.String()

	// 但对于已命名类型（或指向它们的指针），用导入路径来限定（但参见内部注释）。
	// 解引用一层指针，以寻找已命名类型。
	star := ""
	if rt.Name() == "" {
		if pt := rt; pt.Kind() == reflect.Pointer {
			star = "*"
			// 注意：下面这行本应为 rt = pt.Elem()，以实现
			// 上面注释所声称的行为，但修正它会破坏与现有 gob 的
			// 兼容性。
			//
			// 给定以 "full/p" 导入、具有以下定义的包 p：
			//     package p
			//     type T1 struct { ... }
			// 下表展示了 gob 用来命名类型的预期字符串和实际字符串：
			//
			// 类型      正确字符串         实际字符串
			//
			// T1        full/p.T1          full/p.T1
			// *T1       *full/p.T1         *p.T1
			//
			// 缺失的完整路径无法在不破坏现有 gob 解码器的情况下修正。
			rt = pt
		}
	}
	if rt.Name() != "" {
		if rt.PkgPath() == "" {
			name = star + rt.Name()
		} else {
			name = star + rt.PkgPath() + "." + rt.Name()
		}
	}

	RegisterName(name, value)
}

func registerBasics() {
	Register(int(0))
	Register(int8(0))
	Register(int16(0))
	Register(int32(0))
	Register(int64(0))
	Register(uint(0))
	Register(uint8(0))
	Register(uint16(0))
	Register(uint32(0))
	Register(uint64(0))
	Register(float32(0))
	Register(float64(0))
	Register(complex64(0i))
	Register(complex128(0i))
	Register(uintptr(0))
	Register(false)
	Register("")
	Register([]byte(nil))
	Register([]int(nil))
	Register([]int8(nil))
	Register([]int16(nil))
	Register([]int32(nil))
	Register([]int64(nil))
	Register([]uint(nil))
	Register([]uint8(nil))
	Register([]uint16(nil))
	Register([]uint32(nil))
	Register([]uint64(nil))
	Register([]float32(nil))
	Register([]float64(nil))
	Register([]complex64(nil))
	Register([]complex128(nil))
	Register([]uintptr(nil))
	Register([]bool(nil))
	Register([]string(nil))
}

func init() {
	typeInfoMap.Store(typeInfoMapInit)
	typeInfoMapInit = nil
}
