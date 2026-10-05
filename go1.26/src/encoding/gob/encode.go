// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run encgen.go -output enc_helpers.go

package gob

import (
	"encoding"
	"encoding/binary"
	"math"
	"math/bits"
	"reflect"
	"sync"
)

const uint64Size = 8

type encHelper func(state *encoderState, v reflect.Value) bool

// encoderState 是编码器实例的全局执行状态。
// 字段号采用增量编码，且总是递增。字段号被初始化为 -1，因此 0 会以增量 (1) 出现。
// 增量为 0 则终止该结构体。
type encoderState struct {
	enc      *Encoder
	b        *encBuffer
	sendZero bool                 // 正在编码数组元素或 map 的键/值对；发送零值
	fieldnum int                  // 最后写入的字段号。
	buf      [1 + uint64Size]byte // 编码器使用的缓冲区；放在此处以避免分配。
	next     *encoderState        // 用于空闲列表
}

// encBuffer 是一个极其简单、快速的只写字节缓冲区实现。
// 它从不返回非 nil 错误，但 Write 会返回错误值，以便与 io.Writer 匹配。
type encBuffer struct {
	data    []byte
	scratch [64]byte
}

var encBufferPool = sync.Pool{
	New: func() any {
		e := new(encBuffer)
		e.data = e.scratch[0:0]
		return e
	},
}

func (e *encBuffer) writeByte(c byte) {
	e.data = append(e.data, c)
}

func (e *encBuffer) Write(p []byte) (int, error) {
	e.data = append(e.data, p...)
	return len(p), nil
}

func (e *encBuffer) WriteString(s string) {
	e.data = append(e.data, s...)
}

func (e *encBuffer) Len() int {
	return len(e.data)
}

func (e *encBuffer) Bytes() []byte {
	return e.data
}

func (e *encBuffer) Reset() {
	if len(e.data) >= tooBig {
		e.data = e.scratch[0:0]
	} else {
		e.data = e.data[0:0]
	}
}

func (enc *Encoder) newEncoderState(b *encBuffer) *encoderState {
	e := enc.freeList
	if e == nil {
		e = new(encoderState)
		e.enc = enc
	} else {
		enc.freeList = e.next
	}
	e.sendZero = false
	e.fieldnum = 0
	e.b = b
	if len(b.data) == 0 {
		b.data = b.scratch[0:0]
	}
	return e
}

func (enc *Encoder) freeEncoderState(e *encoderState) {
	e.next = enc.freeList
	enc.freeList = e
}

// 无符号整数采用双状态编码。如果该数小于 128（0 到 0x7F），则直接写入其值。
// 否则，该值以大写端序（big-endian）字节顺序写入，并在其前面加上取负后的字节长度。

// encodeUint 将一个编码后的无符号整数写入 state.b。
func (state *encoderState) encodeUint(x uint64) {
	if x <= 0x7F {
		state.b.writeByte(uint8(x))
		return
	}

	binary.BigEndian.PutUint64(state.buf[1:], x)
	bc := bits.LeadingZeros64(x) >> 3      // 8 - bytelen(x)
	state.buf[bc] = uint8(bc - uint64Size) // 然后再减去 8 以得到 -bytelen(x)

	state.b.Write(state.buf[bc : uint64Size+1])
}

// encodeInt 将一个编码后的有符号整数写入 state.w。
// 编码的最低位表示是否需要对 uint 的（其余位）取位补，以恢复出 int。
func (state *encoderState) encodeInt(i int64) {
	var x uint64
	if i < 0 {
		x = uint64(^i<<1) | 1
	} else {
		x = uint64(i << 1)
	}
	state.encodeUint(x)
}

// encOp 是给定类型的编码算子的签名。
type encOp func(i *encInstr, state *encoderState, v reflect.Value)

// 编码机器的“指令”
type encInstr struct {
	op    encOp
	field int   // 输入中的字段号
	index []int // 结构体索引
	indir int   // 到达结构体中该值需要多少层指针间接寻址
}

// update 发出一个字段号，并更新状态以记录其值，用于增量编码。
// 如果指令指针为 nil，则不做任何事。
func (state *encoderState) update(instr *encInstr) {
	if instr != nil {
		state.encodeUint(uint64(instr.field - state.fieldnum))
		state.fieldnum = instr.field
	}
}

// 每个复合类型的编码器都负责处理与数据结构元素相关联的任何间接寻址。
// 如果由此到达的任何指针为 nil，则不写入任何字节。如果数据项为零值，则不写入
// 任何字节。单个值——int、string 等——会在调用其编码器之前先进行间接寻址。
// 否则，输出（对于标量）就是字段号（作为一个编码后的整数），后跟相应格式的
// 字段数据。

// encIndirect 对 pv 解引用 indir 次并返回结果。
func encIndirect(pv reflect.Value, indir int) reflect.Value {
	for ; indir > 0; indir-- {
		if pv.IsNil() {
			break
		}
		pv = pv.Elem()
	}
	return pv
}

// encBool 将 v 引用的 bool 编码为无符号的 0 或 1。
func encBool(i *encInstr, state *encoderState, v reflect.Value) {
	b := v.Bool()
	if b || state.sendZero {
		state.update(i)
		if b {
			state.encodeUint(1)
		} else {
			state.encodeUint(0)
		}
	}
}

// encInt 编码 v 引用的有符号整数（int int8 int16 int32 int64）。
func encInt(i *encInstr, state *encoderState, v reflect.Value) {
	value := v.Int()
	if value != 0 || state.sendZero {
		state.update(i)
		state.encodeInt(value)
	}
}

// encUint 编码 v 引用的无符号整数（uint uint8 uint16 uint32 uint64 uintptr）。
func encUint(i *encInstr, state *encoderState, v reflect.Value) {
	value := v.Uint()
	if value != 0 || state.sendZero {
		state.update(i)
		state.encodeUint(value)
	}
}

// floatBits 返回一个 uint64，其中保存着某个浮点数的位。
// 浮点数以 uint64 形式传输，其中保存着底层表示的位。它们以字节反转的形式发送，
// 指数端先出现，因此（例如）整数浮点数可以更紧凑地传输。此例程完成这种字节交换。
func floatBits(f float64) uint64 {
	u := math.Float64bits(f)
	return bits.ReverseBytes64(u)
}

// encFloat 编码 v 引用的浮点值（float32 float64）。
func encFloat(i *encInstr, state *encoderState, v reflect.Value) {
	f := v.Float()
	if f != 0 || state.sendZero {
		bits := floatBits(f)
		state.update(i)
		state.encodeUint(bits)
	}
}

// encComplex 编码 v 引用的复数值（complex64 complex128）。
// 复数就是一对浮点数，实部在前。
func encComplex(i *encInstr, state *encoderState, v reflect.Value) {
	c := v.Complex()
	if c != 0+0i || state.sendZero {
		rpart := floatBits(real(c))
		ipart := floatBits(imag(c))
		state.update(i)
		state.encodeUint(rpart)
		state.encodeUint(ipart)
	}
}

// encUint8Array 编码 v 引用的字节数组。
// 字节数组编码为一个无符号计数后跟原始字节。
func encUint8Array(i *encInstr, state *encoderState, v reflect.Value) {
	b := v.Bytes()
	if len(b) > 0 || state.sendZero {
		state.update(i)
		state.encodeUint(uint64(len(b)))
		state.b.Write(b)
	}
}

// encString 编码 v 引用的字符串。
// 字符串编码为一个无符号计数后跟原始字节。
func encString(i *encInstr, state *encoderState, v reflect.Value) {
	s := v.String()
	if len(s) > 0 || state.sendZero {
		state.update(i)
		state.encodeUint(uint64(len(s)))
		state.b.WriteString(s)
	}
}

// encStructTerminator 将编码后的结构体末尾编码为字段增量 0。
func encStructTerminator(i *encInstr, state *encoderState, v reflect.Value) {
	state.encodeUint(0)
}

// 执行引擎

// encEngine 是一个由编码数据（通常是结构体）的字段号索引的指令数组。
// 它自上而下执行，遍历该结构体。
type encEngine struct {
	instr []encInstr
}

const singletonField = 0

// valid 报告该值是否有效且为非 nil 指针。
// （切片、map 和 chan 会自行处理。）
func valid(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Invalid:
		return false
	case reflect.Pointer:
		return !v.IsNil()
	}
	return true
}

// encodeSingle 编码单个顶层的非结构体值。
func (enc *Encoder) encodeSingle(b *encBuffer, engine *encEngine, value reflect.Value) {
	state := enc.newEncoderState(b)
	defer enc.freeEncoderState(state)
	state.fieldnum = singletonField
	// 没有外围结构体来为传输划定框架，因此即使该项为零值也必须生成数据。
	// 为此，设置 sendZero。
	state.sendZero = true
	instr := &engine.instr[singletonField]
	if instr.indir > 0 {
		value = encIndirect(value, instr.indir)
	}
	if valid(value) {
		instr.op(instr, state, value)
	}
}

// encodeStruct 编码单个结构体值。
func (enc *Encoder) encodeStruct(b *encBuffer, engine *encEngine, value reflect.Value) {
	if !valid(value) {
		return
	}
	state := enc.newEncoderState(b)
	defer enc.freeEncoderState(state)
	state.fieldnum = -1
	for i := 0; i < len(engine.instr); i++ {
		instr := &engine.instr[i]
		if i >= value.NumField() {
			// encStructTerminator
			instr.op(instr, state, reflect.Value{})
			break
		}
		field := value.FieldByIndex(instr.index)
		if instr.indir > 0 {
			field = encIndirect(field, instr.indir)
			// TODO：field 是否保证有效？如果是，我们可以避免这个检查。
			if !valid(field) {
				continue
			}
		}
		instr.op(instr, state, field)
	}
}

// encodeArray 编码一个数组。
func (enc *Encoder) encodeArray(b *encBuffer, value reflect.Value, op encOp, elemIndir int, length int, helper encHelper) {
	state := enc.newEncoderState(b)
	defer enc.freeEncoderState(state)
	state.fieldnum = -1
	state.sendZero = true
	state.encodeUint(uint64(length))
	if helper != nil && helper(state, value) {
		return
	}
	for i := 0; i < length; i++ {
		elem := value.Index(i)
		if elemIndir > 0 {
			elem = encIndirect(elem, elemIndir)
			// TODO：elem 是否保证有效？如果是，我们可以避免这个检查。
			if !valid(elem) {
				errorf("encodeArray: nil element")
			}
		}
		op(nil, state, elem)
	}
}

// encodeReflectValue 是 map 的辅助函数。它编码值 v。
func encodeReflectValue(state *encoderState, v reflect.Value, op encOp, indir int) {
	for i := 0; i < indir && v.IsValid(); i++ {
		v = reflect.Indirect(v)
	}
	if !v.IsValid() {
		errorf("encodeReflectValue: nil element")
	}
	op(nil, state, v)
}

// encodeMap 将 map 编码为无符号计数后跟 key:value 对。
func (enc *Encoder) encodeMap(b *encBuffer, mv reflect.Value, keyOp, elemOp encOp, keyIndir, elemIndir int) {
	state := enc.newEncoderState(b)
	state.fieldnum = -1
	state.sendZero = true
	state.encodeUint(uint64(mv.Len()))
	mi := mv.MapRange()
	for mi.Next() {
		encodeReflectValue(state, mi.Key(), keyOp, keyIndir)
		encodeReflectValue(state, mi.Value(), elemOp, elemIndir)
	}
	enc.freeEncoderState(state)
}

// encodeInterface 编码接口值 iv。
// 要发送一个接口，我们先发送一个标识具体类型的字符串，后跟类型标识符
// （这可能需要立即定义该类型），再后跟具体值。nil 值会以空字符串作为名称发送，
// 其后不跟随任何值。
func (enc *Encoder) encodeInterface(b *encBuffer, iv reflect.Value) {
	// Gob 可以编码 nil 接口值，但不能编码持有 nil 指针的有类型接口值，
	// 因为 nil 指针不指向任何值。
	elem := iv.Elem()
	if elem.Kind() == reflect.Pointer && elem.IsNil() {
		errorf("gob: cannot encode nil pointer of type %s inside interface", iv.Elem().Type())
	}
	state := enc.newEncoderState(b)
	state.fieldnum = -1
	state.sendZero = true
	if iv.IsNil() {
		state.encodeUint(0)
		return
	}

	ut := userType(iv.Elem().Type())
	namei, ok := concreteTypeToName.Load(ut.base)
	if !ok {
		errorf("type not registered for interface: %s", ut.base)
	}
	name := namei.(string)

	// 发送名称。
	state.encodeUint(uint64(len(name)))
	state.b.WriteString(name)
	// 如有必要，定义类型 id。
	enc.sendTypeDescriptor(enc.writer(), state, ut)
	// 发送类型 id。
	enc.sendTypeId(state, ut)
	// 将值编码进一个新缓冲区。任何嵌套的类型定义都应写入 b，
	// 且在编码后的值之前。
	enc.pushWriter(b)
	data := encBufferPool.Get().(*encBuffer)
	data.Write(spaceForLength)
	enc.encode(data, elem, ut)
	if enc.err != nil {
		error_(enc.err)
	}
	enc.popWriter()
	enc.writeMessage(b, data)
	data.Reset()
	encBufferPool.Put(data)
	if enc.err != nil {
		error_(enc.err)
	}
	enc.freeEncoderState(state)
}

// encodeGobEncoder 编码实现了 GobEncoder 接口的值。
// 数据以字节数组的形式发送。
func (enc *Encoder) encodeGobEncoder(b *encBuffer, ut *userTypeInfo, v reflect.Value) {
	// TODO：我们是否应该捕获被调用方法产生的 panic？

	var data []byte
	var err error
	// 我们知道它是其中之一。
	switch ut.externalEnc {
	case xGob:
		gobEncoder, _ := reflect.TypeAssert[GobEncoder](v)
		data, err = gobEncoder.GobEncode()
	case xBinary:
		binaryMarshaler, _ := reflect.TypeAssert[encoding.BinaryMarshaler](v)
		data, err = binaryMarshaler.MarshalBinary()
	case xText:
		textMarshaler, _ := reflect.TypeAssert[encoding.TextMarshaler](v)
		data, err = textMarshaler.MarshalText()
	}
	if err != nil {
		error_(err)
	}
	state := enc.newEncoderState(b)
	state.fieldnum = -1
	state.encodeUint(uint64(len(data)))
	state.b.Write(data)
	enc.freeEncoderState(state)
}

var encOpTable = [...]encOp{
	reflect.Bool:       encBool,
	reflect.Int:        encInt,
	reflect.Int8:       encInt,
	reflect.Int16:      encInt,
	reflect.Int32:      encInt,
	reflect.Int64:      encInt,
	reflect.Uint:       encUint,
	reflect.Uint8:      encUint,
	reflect.Uint16:     encUint,
	reflect.Uint32:     encUint,
	reflect.Uint64:     encUint,
	reflect.Uintptr:    encUint,
	reflect.Float32:    encFloat,
	reflect.Float64:    encFloat,
	reflect.Complex64:  encComplex,
	reflect.Complex128: encComplex,
	reflect.String:     encString,
}

// encOpFor 返回（指向）rt 下基础类型的编码算子，以及到达它所需的间接寻址层数。
func encOpFor(rt reflect.Type, inProgress map[reflect.Type]*encOp, building map[*typeInfo]bool) (*encOp, int) {
	ut := userType(rt)
	// 如果该类型实现了 GobEncoder，我们无需进一步处理即可处理它。
	if ut.externalEnc != 0 {
		return gobEncodeOpFor(ut)
	}
	// 如果该类型已在处理中，则它是递归类型（例如 map[string]*T）。
	// 返回我们正在构建的算子的指针。
	if opPtr := inProgress[rt]; opPtr != nil {
		return opPtr, ut.indir
	}
	typ := ut.base
	indir := ut.indir
	k := typ.Kind()
	var op encOp
	if int(k) < len(encOpTable) {
		op = encOpTable[k]
	}
	if op == nil {
		inProgress[rt] = &op
		// 特殊情况
		switch t := typ; t.Kind() {
		case reflect.Slice:
			if t.Elem().Kind() == reflect.Uint8 {
				op = encUint8Array
				break
			}
			// 切片有一个头部；我们解码它以找到底层数组。
			elemOp, elemIndir := encOpFor(t.Elem(), inProgress, building)
			helper := encSliceHelper[t.Elem().Kind()]
			op = func(i *encInstr, state *encoderState, slice reflect.Value) {
				if !state.sendZero && slice.Len() == 0 {
					return
				}
				state.update(i)
				state.enc.encodeArray(state.b, slice, *elemOp, elemIndir, slice.Len(), helper)
			}
		case reflect.Array:
			// 真正的数组在类型中已带有大小。
			elemOp, elemIndir := encOpFor(t.Elem(), inProgress, building)
			helper := encArrayHelper[t.Elem().Kind()]
			op = func(i *encInstr, state *encoderState, array reflect.Value) {
				state.update(i)
				state.enc.encodeArray(state.b, array, *elemOp, elemIndir, array.Len(), helper)
			}
		case reflect.Map:
			keyOp, keyIndir := encOpFor(t.Key(), inProgress, building)
			elemOp, elemIndir := encOpFor(t.Elem(), inProgress, building)
			op = func(i *encInstr, state *encoderState, mv reflect.Value) {
				// 我们发送零长度（但非 nil）的 map，因为接收方可能想使用该 map。
				// （map 不使用 append。）
				if !state.sendZero && mv.IsNil() {
					return
				}
				state.update(i)
				state.enc.encodeMap(state.b, mv, *keyOp, *elemOp, keyIndir, elemIndir)
			}
		case reflect.Struct:
			// 生成一个闭包，调用嵌套类型的引擎。
			getEncEngine(userType(typ), building)
			info := mustGetTypeInfo(typ)
			op = func(i *encInstr, state *encoderState, sv reflect.Value) {
				state.update(i)
				// 通过 info 进行间接寻址，以推迟递归结构体的求值
				enc := info.encoder.Load()
				state.enc.encodeStruct(state.b, enc, sv)
			}
		case reflect.Interface:
			op = func(i *encInstr, state *encoderState, iv reflect.Value) {
				if !state.sendZero && (!iv.IsValid() || iv.IsNil()) {
					return
				}
				state.update(i)
				state.enc.encodeInterface(state.b, iv)
			}
		}
	}
	if op == nil {
		errorf("can't happen: encode type %s", rt)
	}
	return &op, indir
}

// gobEncodeOpFor 返回一个已知实现 GobEncoder 的类型的算子。
func gobEncodeOpFor(ut *userTypeInfo) (*encOp, int) {
	rt := ut.user
	if ut.encIndir == -1 {
		rt = reflect.PointerTo(rt)
	} else if ut.encIndir > 0 {
		for i := int8(0); i < ut.encIndir; i++ {
			rt = rt.Elem()
		}
	}
	var op encOp
	op = func(i *encInstr, state *encoderState, v reflect.Value) {
		if ut.encIndir == -1 {
			// 需要向上爬一层，把值变成指针。
			if !v.CanAddr() {
				errorf("unaddressable value of type %s", rt)
			}
			v = v.Addr()
		}
		if !state.sendZero && v.IsZero() {
			return
		}
		state.update(i)
		state.enc.encodeGobEncoder(state.b, ut, v)
	}
	return &op, int(ut.encIndir) // encIndir：算子将以 p == 接收者的地址被调用。
}

// compileEnc 返回用于编译该类型的引擎。
func compileEnc(ut *userTypeInfo, building map[*typeInfo]bool) *encEngine {
	srt := ut.base
	engine := new(encEngine)
	seen := make(map[reflect.Type]*encOp)
	rt := ut.base
	if ut.externalEnc != 0 {
		rt = ut.user
	}
	if ut.externalEnc == 0 && srt.Kind() == reflect.Struct {
		for fieldNum, wireFieldNum := 0, 0; fieldNum < srt.NumField(); fieldNum++ {
			f := srt.Field(fieldNum)
			if !isSent(&f) {
				continue
			}
			op, indir := encOpFor(f.Type, seen, building)
			engine.instr = append(engine.instr, encInstr{*op, wireFieldNum, f.Index, indir})
			wireFieldNum++
		}
		if srt.NumField() > 0 && len(engine.instr) == 0 {
			errorf("type %s has no exported fields", rt)
		}
		engine.instr = append(engine.instr, encInstr{encStructTerminator, 0, nil, 0})
	} else {
		engine.instr = make([]encInstr, 1)
		op, indir := encOpFor(rt, seen, building)
		engine.instr[0] = encInstr{*op, singletonField, nil, indir}
	}
	return engine
}

// getEncEngine 返回用于编译该类型的引擎。
func getEncEngine(ut *userTypeInfo, building map[*typeInfo]bool) *encEngine {
	info, err := getTypeInfo(ut)
	if err != nil {
		error_(err)
	}
	enc := info.encoder.Load()
	if enc == nil {
		enc = buildEncEngine(info, ut, building)
	}
	return enc
}

func buildEncEngine(info *typeInfo, ut *userTypeInfo, building map[*typeInfo]bool) *encEngine {
	// 检查递归类型。
	if building != nil && building[info] {
		return nil
	}
	info.encInit.Lock()
	defer info.encInit.Unlock()
	enc := info.encoder.Load()
	if enc == nil {
		if building == nil {
			building = make(map[*typeInfo]bool)
		}
		building[info] = true
		enc = compileEnc(ut, building)
		info.encoder.Store(enc)
	}
	return enc
}

func (enc *Encoder) encode(b *encBuffer, value reflect.Value, ut *userTypeInfo) {
	defer catchError(&enc.err)
	engine := getEncEngine(ut, nil)
	indir := ut.indir
	if ut.externalEnc != 0 {
		indir = int(ut.encIndir)
	}
	for i := 0; i < indir; i++ {
		value = reflect.Indirect(value)
	}
	if ut.externalEnc == 0 && value.Kind() == reflect.Struct {
		enc.encodeStruct(b, engine, value)
	} else {
		enc.encodeSingle(b, engine, value)
	}
}
