// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run decgen.go -output dec_helpers.go

package gob

import (
	"encoding"
	"errors"
	"internal/saferio"
	"io"
	"math"
	"math/bits"
	"reflect"
)

var (
	errBadUint = errors.New("gob: encoded unsigned integer out of range")
	errBadType = errors.New("gob: unknown type id or corrupted data")
	errRange   = errors.New("gob: bad data: field numbers out of bounds")
)

type decHelper func(state *decoderState, v reflect.Value, length int, ovfl error) bool

// decoderState 是解码器实例的执行状态。嵌套对象会创建一个新的状态。
type decoderState struct {
	dec *Decoder
	// 缓冲区多存了一层间接引用，因为在解码过程中（读取接口值时）加载类型时
	// 它可能会被替换。
	b        *decBuffer
	fieldnum int           // 最后读取的字段号。
	next     *decoderState // 用于空闲列表
}

// decBuffer 是一个极其简单、快速的只读字节缓冲区实现。
// 它通过调用 Size 初始化，然后将数据复制到 Bytes() 返回的切片中。
type decBuffer struct {
	data   []byte
	offset int // 读取偏移量。
}

func (d *decBuffer) Read(p []byte) (int, error) {
	n := copy(p, d.data[d.offset:])
	if n == 0 && len(p) != 0 {
		return 0, io.EOF
	}
	d.offset += n
	return n, nil
}

func (d *decBuffer) Drop(n int) {
	if n > d.Len() {
		panic("drop")
	}
	d.offset += n
}

func (d *decBuffer) ReadByte() (byte, error) {
	if d.offset >= len(d.data) {
		return 0, io.EOF
	}
	c := d.data[d.offset]
	d.offset++
	return c, nil
}

func (d *decBuffer) Len() int {
	return len(d.data) - d.offset
}

func (d *decBuffer) Bytes() []byte {
	return d.data[d.offset:]
}

// SetBytes 将缓冲区设为这些字节，丢弃任何现有数据。
func (d *decBuffer) SetBytes(data []byte) {
	d.data = data
	d.offset = 0
}

func (d *decBuffer) Reset() {
	d.data = d.data[0:0]
	d.offset = 0
}

// 我们单独传入 bytes.Buffer，以便在无需完整 Decoder 的情况下更轻松地测试该基础设施。
func (dec *Decoder) newDecoderState(buf *decBuffer) *decoderState {
	d := dec.freeList
	if d == nil {
		d = new(decoderState)
		d.dec = dec
	} else {
		dec.freeList = d.next
	}
	d.b = buf
	return d
}

func (dec *Decoder) freeDecoderState(d *decoderState) {
	d.next = dec.freeList
	dec.freeList = d
}

func overflow(name string) error {
	return errors.New(`value for "` + name + `" out of range`)
}

// decodeUintReader 从 io.Reader 读取一个编码后的无符号整数。
// 仅由 Decoder 用于读取消息长度。
func decodeUintReader(r io.Reader, buf []byte) (x uint64, width int, err error) {
	width = 1
	n, err := io.ReadFull(r, buf[0:width])
	if n == 0 {
		return
	}
	b := buf[0]
	if b <= 0x7f {
		return uint64(b), width, nil
	}
	n = -int(int8(b))
	if n > uint64Size {
		err = errBadUint
		return
	}
	width, err = io.ReadFull(r, buf[0:n])
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return
	}
	// 可以检查最高字节是否为零，但这并不值得。
	for _, b := range buf[0:width] {
		x = x<<8 | uint64(b)
	}
	width++ // 长度字节 +1
	return
}

// decodeUint 从 state.r 读取一个编码后的无符号整数。
// 不检查溢出。
func (state *decoderState) decodeUint() (x uint64) {
	b, err := state.b.ReadByte()
	if err != nil {
		error_(err)
	}
	if b <= 0x7f {
		return uint64(b)
	}
	n := -int(int8(b))
	if n > uint64Size {
		error_(errBadUint)
	}
	buf := state.b.Bytes()
	if len(buf) < n {
		errorf("invalid uint data length %d: exceeds input size %d", n, len(buf))
	}
	// 无需检查错误；无论如何循环都是安全的。
	// 可以检查最高字节是否为零，但这并不值得。
	for _, b := range buf[0:n] {
		x = x<<8 | uint64(b)
	}
	state.b.Drop(n)
	return x
}

// decodeInt 从 state.r 读取一个编码后的有符号整数。
// 不检查溢出。
func (state *decoderState) decodeInt() int64 {
	x := state.decodeUint()
	if x&1 != 0 {
		return ^int64(x >> 1)
	}
	return int64(x >> 1)
}

// getLength 解码下一个 uint，并确保它是其后数据项可能的大小，
// 这意味着它必须能放进一个非负 int 中，且能放进缓冲区。
func (state *decoderState) getLength() (int, bool) {
	n := int(state.decodeUint())
	if n < 0 || state.b.Len() < n || tooBig <= n {
		return 0, false
	}
	return n, true
}

// decOp 是给定类型的解码算子的签名。
type decOp func(i *decInstr, state *decoderState, v reflect.Value)

// 解码机器的“指令”
type decInstr struct {
	op    decOp
	field int   // 线类型的字段号
	index []int // 目标类型的字段访问索引
	ovfl  error // 溢出/下溢的错误消息（对于数组，则是元素的）
}

// ignoreUint 丢弃一个没有目标的 uint 值。
func ignoreUint(i *decInstr, state *decoderState, v reflect.Value) {
	state.decodeUint()
}

// ignoreTwoUints 丢弃一个没有目标的 uint 值。它用于跳过复数值。
func ignoreTwoUints(i *decInstr, state *decoderState, v reflect.Value) {
	state.decodeUint()
	state.decodeUint()
}

// 由于编码器不写入零值，因此如果我们到达某个解码器，就说明有一个值需要提取
// 并存储。字段号已经读取完毕（我们正是据此知道要调用此解码器）。
// 每个解码器都负责处理与数据结构相关联的任何间接寻址。如果由此到达的任何
// 指针为 nil，则必须进行分配。

// decAlloc 接收一个值，并返回一个可设置、可被赋值的值。如果该值是指针，
// decAlloc 保证它指向存储。各个解码器的调用方应已使用过 decAlloc。
// 各个解码器本身不需要再调用它。
func decAlloc(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

// decBool 解码一个 uint，并将其作为布尔值存储到 value 中。
func decBool(i *decInstr, state *decoderState, value reflect.Value) {
	value.SetBool(state.decodeUint() != 0)
}

// decInt8 解码一个整数，并将其作为 int8 存储到 value 中。
func decInt8(i *decInstr, state *decoderState, value reflect.Value) {
	v := state.decodeInt()
	if v < math.MinInt8 || math.MaxInt8 < v {
		error_(i.ovfl)
	}
	value.SetInt(v)
}

// decUint8 解码一个无符号整数，并将其作为 uint8 存储到 value 中。
func decUint8(i *decInstr, state *decoderState, value reflect.Value) {
	v := state.decodeUint()
	if math.MaxUint8 < v {
		error_(i.ovfl)
	}
	value.SetUint(v)
}

// decInt16 解码一个整数，并将其作为 int16 存储到 value 中。
func decInt16(i *decInstr, state *decoderState, value reflect.Value) {
	v := state.decodeInt()
	if v < math.MinInt16 || math.MaxInt16 < v {
		error_(i.ovfl)
	}
	value.SetInt(v)
}

// decUint16 解码一个无符号整数，并将其作为 uint16 存储到 value 中。
func decUint16(i *decInstr, state *decoderState, value reflect.Value) {
	v := state.decodeUint()
	if math.MaxUint16 < v {
		error_(i.ovfl)
	}
	value.SetUint(v)
}

// decInt32 解码一个整数，并将其作为 int32 存储到 value 中。
func decInt32(i *decInstr, state *decoderState, value reflect.Value) {
	v := state.decodeInt()
	if v < math.MinInt32 || math.MaxInt32 < v {
		error_(i.ovfl)
	}
	value.SetInt(v)
}

// decUint32 解码一个无符号整数，并将其作为 uint32 存储到 value 中。
func decUint32(i *decInstr, state *decoderState, value reflect.Value) {
	v := state.decodeUint()
	if math.MaxUint32 < v {
		error_(i.ovfl)
	}
	value.SetUint(v)
}

// decInt64 解码一个整数，并将其作为 int64 存储到 value 中。
func decInt64(i *decInstr, state *decoderState, value reflect.Value) {
	v := state.decodeInt()
	value.SetInt(v)
}

// decUint64 解码一个无符号整数，并将其作为 uint64 存储到 value 中。
func decUint64(i *decInstr, state *decoderState, value reflect.Value) {
	v := state.decodeUint()
	value.SetUint(v)
}

// 浮点数以 uint64 形式传输，其中保存着底层表示的位。它们以字节反转的形式发送，
// 指数端先出现，因此（例如）整数浮点数可以更紧凑地传输。此例程完成这种字节还原。
func float64FromBits(u uint64) float64 {
	v := bits.ReverseBytes64(u)
	return math.Float64frombits(v)
}

// float32FromBits 解码一个无符号整数，将其视为 32 位浮点数并返回。
// 它是 float32 和 complex64 的辅助函数。它返回 float64，因为反射需要这个类型，
// 但已知其返回值可以精确地用 float32 表示。
func float32FromBits(u uint64, ovfl error) float64 {
	v := float64FromBits(u)
	av := v
	if av < 0 {
		av = -av
	}
	// +Inf 在 32 位和 64 位浮点数中都可以。下溢总是可以的。
	if math.MaxFloat32 < av && av <= math.MaxFloat64 {
		error_(ovfl)
	}
	return v
}

// decFloat32 解码一个无符号整数，将其视为 32 位浮点数并存储到 value 中。
func decFloat32(i *decInstr, state *decoderState, value reflect.Value) {
	value.SetFloat(float32FromBits(state.decodeUint(), i.ovfl))
}

// decFloat64 解码一个无符号整数，将其视为 64 位浮点数并存储到 value 中。
func decFloat64(i *decInstr, state *decoderState, value reflect.Value) {
	value.SetFloat(float64FromBits(state.decodeUint()))
}

// decComplex64 解码一对无符号整数，将它们视为一对浮点数，并作为 complex64 存储到 value 中。
// 实部在前。
func decComplex64(i *decInstr, state *decoderState, value reflect.Value) {
	real := float32FromBits(state.decodeUint(), i.ovfl)
	imag := float32FromBits(state.decodeUint(), i.ovfl)
	value.SetComplex(complex(real, imag))
}

// decComplex128 解码一对无符号整数，将它们视为一对浮点数，并作为 complex128 存储到 value 中。
// 实部在前。
func decComplex128(i *decInstr, state *decoderState, value reflect.Value) {
	real := float64FromBits(state.decodeUint())
	imag := float64FromBits(state.decodeUint())
	value.SetComplex(complex(real, imag))
}

// decUint8Slice 解码一个字节切片，并将描述该数据的切片头部存储到 value 中。
// uint8 切片编码为一个无符号计数后跟原始字节。
func decUint8Slice(i *decInstr, state *decoderState, value reflect.Value) {
	n, ok := state.getLength()
	if !ok {
		errorf("bad %s slice length: %d", value.Type(), n)
	}
	if value.Cap() < n {
		safe := saferio.SliceCap[byte](uint64(n))
		if safe < 0 {
			errorf("%s slice too big: %d elements", value.Type(), n)
		}
		value.Set(reflect.MakeSlice(value.Type(), safe, safe))
		ln := safe
		i := 0
		for i < n {
			if i >= ln {
				// 由于使用了 saferio.SliceCap，我们没有分配整个切片。
				// 再为多一个元素增长切片。
				// 切片已满，因此这应该会提升容量。
				value.Grow(1)
			}
			// 复制进 s，最多复制到容量或 n 中较小的那个。
			ln = value.Cap()
			if ln > n {
				ln = n
			}
			value.SetLen(ln)
			sub := value.Slice(i, ln)
			if _, err := state.b.Read(sub.Bytes()); err != nil {
				errorf("error decoding []byte at %d: %s", i, err)
			}
			i = ln
		}
	} else {
		value.SetLen(n)
		if _, err := state.b.Read(value.Bytes()); err != nil {
			errorf("error decoding []byte: %s", err)
		}
	}
}

// decString 解码字节数组，并将描述该数据的字符串头部存储到 value 中。
// 字符串编码为一个无符号计数后跟原始字节。
func decString(i *decInstr, state *decoderState, value reflect.Value) {
	n, ok := state.getLength()
	if !ok {
		errorf("bad %s slice length: %d", value.Type(), n)
	}
	// 读取数据。
	data := state.b.Bytes()
	if len(data) < n {
		errorf("invalid string length %d: exceeds input size %d", n, len(data))
	}
	s := string(data[:n])
	state.b.Drop(n)
	value.SetString(s)
}

// ignoreUint8Array 跳过没有目标的字节切片值的数据。
func ignoreUint8Array(i *decInstr, state *decoderState, value reflect.Value) {
	n, ok := state.getLength()
	if !ok {
		errorf("slice length too large")
	}
	bn := state.b.Len()
	if bn < n {
		errorf("invalid slice length %d: exceeds input size %d", n, bn)
	}
	state.b.Drop(n)
}

// 执行引擎

// 编码器引擎是一个指令数组，由传入的解码器的字段号索引。
// 它按字段号随机访问执行。
type decEngine struct {
	instr    []decInstr
	numInstr int // 活跃指令的数量
}

// decodeSingle 解码一个非结构体的顶层值，并将其存储到 value 中。
// 这类值前面有一个零，使它们具有结构体字段的内存布局（尽管字段号非法）。
func (dec *Decoder) decodeSingle(engine *decEngine, value reflect.Value) {
	state := dec.newDecoderState(&dec.buf)
	defer dec.freeDecoderState(state)
	state.fieldnum = singletonField
	if state.decodeUint() != 0 {
		errorf("decode: corrupted data: non-zero delta for singleton")
	}
	instr := &engine.instr[singletonField]
	instr.op(instr, state, value)
}

// decodeStruct 解码一个顶层结构体，并将其存储到 value 中。
// Indir 是针对值而非类型的。调用时它可能与构建引擎时计算出的 ut.indir 不同。
// 对于 decodeSingle 而言不会出现这种状态，因为它是直接从用户的值调用的，
// 而非从引擎内部调用。
func (dec *Decoder) decodeStruct(engine *decEngine, value reflect.Value) {
	state := dec.newDecoderState(&dec.buf)
	defer dec.freeDecoderState(state)
	state.fieldnum = -1
	for state.b.Len() > 0 {
		delta := int(state.decodeUint())
		if delta < 0 {
			errorf("decode: corrupted data: negative delta")
		}
		if delta == 0 { // 结构体终止符是增量为 0 的字段号
			break
		}
		if state.fieldnum >= len(engine.instr)-delta { // 相减以避免溢出
			error_(errRange)
		}
		fieldnum := state.fieldnum + delta
		instr := &engine.instr[fieldnum]
		var field reflect.Value
		if instr.index != nil {
			// 否则该字段对我们来说是未知的，instr.op 是一个忽略算子。
			field = value.FieldByIndex(instr.index)
			if field.Kind() == reflect.Pointer {
				field = decAlloc(field)
			}
		}
		instr.op(instr, state, field)
		state.fieldnum = fieldnum
	}
}

var noValue reflect.Value

// ignoreStruct 丢弃没有目标的结构体的数据。
func (dec *Decoder) ignoreStruct(engine *decEngine) {
	state := dec.newDecoderState(&dec.buf)
	defer dec.freeDecoderState(state)
	state.fieldnum = -1
	for state.b.Len() > 0 {
		delta := int(state.decodeUint())
		if delta < 0 {
			errorf("ignore decode: corrupted data: negative delta")
		}
		if delta == 0 { // 结构体终止符是增量为 0 的字段号
			break
		}
		fieldnum := state.fieldnum + delta
		if fieldnum >= len(engine.instr) {
			error_(errRange)
		}
		instr := &engine.instr[fieldnum]
		instr.op(instr, state, noValue)
		state.fieldnum = fieldnum
	}
}

// ignoreSingle 丢弃没有目标的顶层非结构体值的数据。
// 当以 nil 值调用 Decode 时使用它。
func (dec *Decoder) ignoreSingle(engine *decEngine) {
	state := dec.newDecoderState(&dec.buf)
	defer dec.freeDecoderState(state)
	state.fieldnum = singletonField
	delta := int(state.decodeUint())
	if delta != 0 {
		errorf("decode: corrupted data: non-zero delta for singleton")
	}
	instr := &engine.instr[singletonField]
	instr.op(instr, state, noValue)
}

// decodeArrayHelper 完成解码数组和切片的工作。
func (dec *Decoder) decodeArrayHelper(state *decoderState, value reflect.Value, elemOp decOp, length int, ovfl error, helper decHelper) {
	if helper != nil && helper(state, value, length, ovfl) {
		return
	}
	instr := &decInstr{elemOp, 0, nil, ovfl}
	isPtr := value.Type().Elem().Kind() == reflect.Pointer
	ln := value.Len()
	for i := 0; i < length; i++ {
		if state.b.Len() == 0 {
			errorf("decoding array or slice: length exceeds input size (%d elements)", length)
		}
		if i >= ln {
			// 这是一个我们只分配了部分的切片。
			// 将其增长到 length。
			value.Grow(1)
			cp := value.Cap()
			if cp > length {
				cp = length
			}
			value.SetLen(cp)
			ln = cp
		}
		v := value.Index(i)
		if isPtr {
			v = decAlloc(v)
		}
		elemOp(instr, state, v)
	}
}

// decodeArray 解码一个数组，并将其存储到 value 中。
// 长度是位于元素之前的一个无符号整数。尽管长度是冗余的
// （它是类型的一部分），但它是一个有用的检查，并被包含在编码中。
func (dec *Decoder) decodeArray(state *decoderState, value reflect.Value, elemOp decOp, length int, ovfl error, helper decHelper) {
	if n := state.decodeUint(); n != uint64(length) {
		errorf("length mismatch in decodeArray")
	}
	dec.decodeArrayHelper(state, value, elemOp, length, ovfl, helper)
}

// decodeIntoValue 是 map 解码的辅助函数。
func decodeIntoValue(state *decoderState, op decOp, isPtr bool, value reflect.Value, instr *decInstr) reflect.Value {
	v := value
	if isPtr {
		v = decAlloc(value)
	}

	op(instr, state, v)
	return value
}

// decodeMap 解码一个 map，并将其存储到 value 中。
// Map 编码为一个长度后跟 key:value 对。
// 由于 map 的内部结构对我们不可见，我们必须使用反射而不是指针技巧。
func (dec *Decoder) decodeMap(mtyp reflect.Type, state *decoderState, value reflect.Value, keyOp, elemOp decOp, ovfl error) {
	n := int(state.decodeUint())
	if value.IsNil() {
		value.Set(reflect.MakeMapWithSize(mtyp, n))
	}
	keyIsPtr := mtyp.Key().Kind() == reflect.Pointer
	elemIsPtr := mtyp.Elem().Kind() == reflect.Pointer
	keyInstr := &decInstr{keyOp, 0, nil, ovfl}
	elemInstr := &decInstr{elemOp, 0, nil, ovfl}
	keyP := reflect.New(mtyp.Key())
	elemP := reflect.New(mtyp.Elem())
	for i := 0; i < n; i++ {
		key := decodeIntoValue(state, keyOp, keyIsPtr, keyP.Elem(), keyInstr)
		elem := decodeIntoValue(state, elemOp, elemIsPtr, elemP.Elem(), elemInstr)
		value.SetMapIndex(key, elem)
		keyP.Elem().SetZero()
		elemP.Elem().SetZero()
	}
}

// ignoreArrayHelper 完成丢弃数组和切片的工作。
func (dec *Decoder) ignoreArrayHelper(state *decoderState, elemOp decOp, length int) {
	instr := &decInstr{elemOp, 0, nil, errors.New("no error")}
	for i := 0; i < length; i++ {
		if state.b.Len() == 0 {
			errorf("decoding array or slice: length exceeds input size (%d elements)", length)
		}
		elemOp(instr, state, noValue)
	}
}

// ignoreArray 丢弃没有目标的数组值的数据。
func (dec *Decoder) ignoreArray(state *decoderState, elemOp decOp, length int) {
	if n := state.decodeUint(); n != uint64(length) {
		errorf("length mismatch in ignoreArray")
	}
	dec.ignoreArrayHelper(state, elemOp, length)
}

// ignoreMap 丢弃没有目标的 map 值的数据。
func (dec *Decoder) ignoreMap(state *decoderState, keyOp, elemOp decOp) {
	n := int(state.decodeUint())
	keyInstr := &decInstr{keyOp, 0, nil, errors.New("no error")}
	elemInstr := &decInstr{elemOp, 0, nil, errors.New("no error")}
	for i := 0; i < n; i++ {
		keyOp(keyInstr, state, noValue)
		elemOp(elemInstr, state, noValue)
	}
}

// decodeSlice 解码一个切片，并将其存储到 value 中。
// 切片编码为一个无符号长度后跟元素。
func (dec *Decoder) decodeSlice(state *decoderState, value reflect.Value, elemOp decOp, ovfl error, helper decHelper) {
	u := state.decodeUint()
	typ := value.Type()
	size := uint64(typ.Elem().Size())
	nBytes := u * size
	n := int(u)
	// 注意此计算中的溢出。
	if n < 0 || uint64(n) != u || nBytes > tooBig || (size > 0 && nBytes/size != u) {
		// 这里我们不检查 n 是否超过缓冲区长度，因为如果是接口切片，
		// 会发生缓冲区重新加载。
		errorf("%s slice too big: %d elements of %d bytes", typ.Elem(), u, size)
	}
	if value.Cap() < n {
		safe := saferio.SliceCapWithSize(size, uint64(n))
		if safe < 0 {
			errorf("%s slice too big: %d elements of %d bytes", typ.Elem(), u, size)
		}
		value.Set(reflect.MakeSlice(typ, safe, safe))
	} else {
		value.SetLen(n)
	}
	dec.decodeArrayHelper(state, value, elemOp, n, ovfl, helper)
}

// ignoreSlice 跳过没有目标的切片值的数据。
func (dec *Decoder) ignoreSlice(state *decoderState, elemOp decOp) {
	dec.ignoreArrayHelper(state, elemOp, int(state.decodeUint()))
}

// decodeInterface 解码一个接口值，并将其存储到 value 中。
// 接口编码为一个具体类型的名称后跟一个值。
// 如果名称为空，则该值为 nil，且不发送任何值。
func (dec *Decoder) decodeInterface(ityp reflect.Type, state *decoderState, value reflect.Value) {
	// 读取具体类型的名称。
	nr := state.decodeUint()
	if nr > 1<<31 { // 对于匿名类型，零是允许的
		errorf("invalid type name length %d", nr)
	}
	if nr > uint64(state.b.Len()) {
		errorf("invalid type name length %d: exceeds input size", nr)
	}
	n := int(nr)
	name := state.b.Bytes()[:n]
	state.b.Drop(n)
	// 分配目标接口值。
	if len(name) == 0 {
		// 将 nil 接口值复制到目标。
		value.SetZero()
		return
	}
	if len(name) > 1024 {
		errorf("name too long (%d bytes): %.20q...", len(name), name)
	}
	// 具体类型必须已注册。
	typi, ok := nameToConcreteType.Load(string(name))
	if !ok {
		errorf("name not registered for interface: %q", name)
	}
	typ := typi.(reflect.Type)

	// 读取具体值的类型 id。
	concreteId := dec.decodeTypeSequence(true)
	if concreteId < 0 {
		error_(dec.err)
	}
	// 接下来是值的字节计数；我们并不关心它是多少（它存在是为了在我们想通过完全
	// 跳过它来忽略该值时使用）。
	state.decodeUint()
	// 读取具体值。
	v := allocValue(typ)
	dec.decodeValue(concreteId, v)
	if dec.err != nil {
		error_(dec.err)
	}
	// 将具体值赋给接口。
	// 小心行事；它可能并不满足该接口。
	if !typ.AssignableTo(ityp) {
		errorf("%s is not assignable to type %s", typ, ityp)
	}
	// 将接口值复制到目标。
	value.Set(v)
}

// ignoreInterface 丢弃没有目标的接口值的数据。
func (dec *Decoder) ignoreInterface(state *decoderState) {
	// 读取具体类型的名称。
	n, ok := state.getLength()
	if !ok {
		errorf("bad interface encoding: name too large for buffer")
	}
	bn := state.b.Len()
	if bn < n {
		errorf("invalid interface value length %d: exceeds input size %d", n, bn)
	}
	state.b.Drop(n)
	id := dec.decodeTypeSequence(true)
	if id < 0 {
		error_(dec.err)
	}
	// 此时，解码器缓冲区中包含一个带分隔的值。直接丢弃它。
	n, ok = state.getLength()
	if !ok {
		errorf("bad interface encoding: data length too large for buffer")
	}
	state.b.Drop(n)
}

// decodeGobDecoder 解码实现了 GobDecoder 接口的东西。
// 数据编码为一个字节切片。
func (dec *Decoder) decodeGobDecoder(ut *userTypeInfo, state *decoderState, value reflect.Value) {
	// 读取该值的字节。
	n, ok := state.getLength()
	if !ok {
		errorf("GobDecoder: length too large for buffer")
	}
	b := state.b.Bytes()
	if len(b) < n {
		errorf("GobDecoder: invalid data length %d: exceeds input size %d", n, len(b))
	}
	b = b[:n]
	state.b.Drop(n)
	var err error
	// 我们知道它是其中之一。
	switch ut.externalDec {
	case xGob:
		gobDecoder, _ := reflect.TypeAssert[GobDecoder](value)
		err = gobDecoder.GobDecode(b)
	case xBinary:
		binaryUnmarshaler, _ := reflect.TypeAssert[encoding.BinaryUnmarshaler](value)
		err = binaryUnmarshaler.UnmarshalBinary(b)
	case xText:
		textUnmarshaler, _ := reflect.TypeAssert[encoding.TextUnmarshaler](value)
		err = textUnmarshaler.UnmarshalText(b)
	}
	if err != nil {
		error_(err)
	}
}

// ignoreGobDecoder 丢弃没有目标的 GobDecoder 值的数据。
func (dec *Decoder) ignoreGobDecoder(state *decoderState) {
	// 读取该值的字节。
	n, ok := state.getLength()
	if !ok {
		errorf("GobDecoder: length too large for buffer")
	}
	bn := state.b.Len()
	if bn < n {
		errorf("GobDecoder: invalid data length %d: exceeds input size %d", n, bn)
	}
	state.b.Drop(n)
}

// 按 Go 类型索引。
var decOpTable = [...]decOp{
	reflect.Bool:       decBool,
	reflect.Int8:       decInt8,
	reflect.Int16:      decInt16,
	reflect.Int32:      decInt32,
	reflect.Int64:      decInt64,
	reflect.Uint8:      decUint8,
	reflect.Uint16:     decUint16,
	reflect.Uint32:     decUint32,
	reflect.Uint64:     decUint64,
	reflect.Float32:    decFloat32,
	reflect.Float64:    decFloat64,
	reflect.Complex64:  decComplex64,
	reflect.Complex128: decComplex128,
	reflect.String:     decString,
}

// 按 gob 类型索引。tComplex 将在 type.init() 期间添加。
var decIgnoreOpMap = map[typeId]decOp{
	tBool:    ignoreUint,
	tInt:     ignoreUint,
	tUint:    ignoreUint,
	tFloat:   ignoreUint,
	tBytes:   ignoreUint8Array,
	tString:  ignoreUint8Array,
	tComplex: ignoreTwoUints,
}

// decOpFor 返回 rt 下基础类型的解码算子，以及到达它所需的间接寻址层数。
func (dec *Decoder) decOpFor(wireId typeId, rt reflect.Type, name string, inProgress map[reflect.Type]*decOp) *decOp {
	ut := userType(rt)
	// 如果该类型实现了 GobEncoder，我们无需进一步处理即可处理它。
	if ut.externalDec != 0 {
		return dec.gobDecodeOpFor(ut)
	}

	// 如果该类型已在处理中，则它是递归类型（例如 map[string]*T）。
	// 返回我们正在构建的算子的指针。
	if opPtr := inProgress[rt]; opPtr != nil {
		return opPtr
	}
	typ := ut.base
	var op decOp
	k := typ.Kind()
	if int(k) < len(decOpTable) {
		op = decOpTable[k]
	}
	if op == nil {
		inProgress[rt] = &op
		// 特殊情况
		switch t := typ; t.Kind() {
		case reflect.Array:
			name = "element of " + name
			elemId := dec.wireType[wireId].ArrayT.Elem
			elemOp := dec.decOpFor(elemId, t.Elem(), name, inProgress)
			ovfl := overflow(name)
			helper := decArrayHelper[t.Elem().Kind()]
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.decodeArray(state, value, *elemOp, t.Len(), ovfl, helper)
			}

		case reflect.Map:
			keyId := dec.wireType[wireId].MapT.Key
			elemId := dec.wireType[wireId].MapT.Elem
			keyOp := dec.decOpFor(keyId, t.Key(), "key of "+name, inProgress)
			elemOp := dec.decOpFor(elemId, t.Elem(), "element of "+name, inProgress)
			ovfl := overflow(name)
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.decodeMap(t, state, value, *keyOp, *elemOp, ovfl)
			}

		case reflect.Slice:
			name = "element of " + name
			if t.Elem().Kind() == reflect.Uint8 {
				op = decUint8Slice
				break
			}
			var elemId typeId
			if tt := builtinIdToType(wireId); tt != nil {
				elemId = tt.(*sliceType).Elem
			} else {
				elemId = dec.wireType[wireId].SliceT.Elem
			}
			elemOp := dec.decOpFor(elemId, t.Elem(), name, inProgress)
			ovfl := overflow(name)
			helper := decSliceHelper[t.Elem().Kind()]
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.decodeSlice(state, value, *elemOp, ovfl, helper)
			}

		case reflect.Struct:
			// 生成一个闭包，调用嵌套类型的引擎。
			ut := userType(typ)
			enginePtr, err := dec.getDecEnginePtr(wireId, ut)
			if err != nil {
				error_(err)
			}
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				// 通过 enginePtr 进行间接寻址，以推迟递归结构体的求值。
				dec.decodeStruct(*enginePtr, value)
			}
		case reflect.Interface:
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.decodeInterface(t, state, value)
			}
		}
	}
	if op == nil {
		errorf("decode can't handle type %s", rt)
	}
	return &op
}

var maxIgnoreNestingDepth = 10000

// decIgnoreOpFor 返回没有目标的字段的解码算子。
func (dec *Decoder) decIgnoreOpFor(wireId typeId, inProgress map[typeId]*decOp) *decOp {
	// 跟踪我们为尝试跳过嵌套的忽略字段而递归的深度。
	dec.ignoreDepth++
	defer func() { dec.ignoreDepth-- }()
	if dec.ignoreDepth > maxIgnoreNestingDepth {
		error_(errors.New("invalid nesting depth"))
	}
	// 如果该类型已在处理中，则它是递归类型（例如 map[string]*T）。
	// 返回我们正在构建的算子的指针。
	if opPtr := inProgress[wireId]; opPtr != nil {
		return opPtr
	}
	op, ok := decIgnoreOpMap[wireId]
	if !ok {
		inProgress[wireId] = &op
		if wireId == tInterface {
			// 特殊情况，因为它是一个方法：被忽略的项可能定义类型，
			// 我们需要在解码器中记录它们的状态。
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.ignoreInterface(state)
			}
			return &op
		}
		// 特殊情况
		wire := dec.wireType[wireId]
		switch {
		case wire == nil:
			errorf("bad data: undefined type %s", wireId.string())
		case wire.ArrayT != nil:
			elemId := wire.ArrayT.Elem
			elemOp := dec.decIgnoreOpFor(elemId, inProgress)
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.ignoreArray(state, *elemOp, wire.ArrayT.Len)
			}

		case wire.MapT != nil:
			keyId := dec.wireType[wireId].MapT.Key
			elemId := dec.wireType[wireId].MapT.Elem
			keyOp := dec.decIgnoreOpFor(keyId, inProgress)
			elemOp := dec.decIgnoreOpFor(elemId, inProgress)
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.ignoreMap(state, *keyOp, *elemOp)
			}

		case wire.SliceT != nil:
			elemId := wire.SliceT.Elem
			elemOp := dec.decIgnoreOpFor(elemId, inProgress)
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.ignoreSlice(state, *elemOp)
			}

		case wire.StructT != nil:
			// 生成一个闭包，调用嵌套类型的引擎。
			enginePtr, err := dec.getIgnoreEnginePtr(wireId)
			if err != nil {
				error_(err)
			}
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				// 通过 enginePtr 进行间接寻址，以推迟递归结构体的求值
				state.dec.ignoreStruct(*enginePtr)
			}

		case wire.GobEncoderT != nil, wire.BinaryMarshalerT != nil, wire.TextMarshalerT != nil:
			op = func(i *decInstr, state *decoderState, value reflect.Value) {
				state.dec.ignoreGobDecoder(state)
			}
		}
	}
	if op == nil {
		errorf("bad data: ignore can't handle type %s", wireId.string())
	}
	return &op
}

// gobDecodeOpFor 返回一个已知实现 GobDecoder 的类型的算子。
func (dec *Decoder) gobDecodeOpFor(ut *userTypeInfo) *decOp {
	rcvrType := ut.user
	if ut.decIndir == -1 {
		rcvrType = reflect.PointerTo(rcvrType)
	} else if ut.decIndir > 0 {
		for i := int8(0); i < ut.decIndir; i++ {
			rcvrType = rcvrType.Elem()
		}
	}
	var op decOp
	op = func(i *decInstr, state *decoderState, value reflect.Value) {
		// 现在我们有了基础类型。如果接收者是指针，我们需要它的地址。
		if value.Kind() != reflect.Pointer && rcvrType.Kind() == reflect.Pointer {
			value = value.Addr()
		}
		state.dec.decodeGobDecoder(ut, state, value)
	}
	return &op
}

// compatibleType 询问：这两个 gob 类型兼容吗？
// 它会为基础类型、数组、map 和切片，以及 GobEncoder/Decoder 对给出答案。
// 结构体被视为可以；字段稍后检查。
func (dec *Decoder) compatibleType(fr reflect.Type, fw typeId, inProgress map[reflect.Type]typeId) bool {
	if rhs, ok := inProgress[fr]; ok {
		return rhs == fw
	}
	inProgress[fr] = fw
	ut := userType(fr)
	wire, ok := dec.wireType[fw]
	// 如果 wire 是用某种编码方法编码的，那么 fr 必须有该方法。
	// 如果没有，则它也必定没有。
	// ut 中至多有一个布尔值为真。
	// 将来我们或许可以放宽这一约束，以便利用 wireType 中的数据来选择解码方法。
	// 这些括号看起来奇怪，但却是正确的。
	if (ut.externalDec == xGob) != (ok && wire.GobEncoderT != nil) ||
		(ut.externalDec == xBinary) != (ok && wire.BinaryMarshalerT != nil) ||
		(ut.externalDec == xText) != (ok && wire.TextMarshalerT != nil) {
		return false
	}
	if ut.externalDec != 0 { // 此测试优先于所有其他测试。
		return true
	}
	switch t := ut.base; t.Kind() {
	default:
		// chan 等：无法处理。
		return false
	case reflect.Bool:
		return fw == tBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fw == tInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return fw == tUint
	case reflect.Float32, reflect.Float64:
		return fw == tFloat
	case reflect.Complex64, reflect.Complex128:
		return fw == tComplex
	case reflect.String:
		return fw == tString
	case reflect.Interface:
		return fw == tInterface
	case reflect.Array:
		if !ok || wire.ArrayT == nil {
			return false
		}
		array := wire.ArrayT
		return t.Len() == array.Len && dec.compatibleType(t.Elem(), array.Elem, inProgress)
	case reflect.Map:
		if !ok || wire.MapT == nil {
			return false
		}
		MapType := wire.MapT
		return dec.compatibleType(t.Key(), MapType.Key, inProgress) && dec.compatibleType(t.Elem(), MapType.Elem, inProgress)
	case reflect.Slice:
		// 它是字节数组吗？
		if t.Elem().Kind() == reflect.Uint8 {
			return fw == tBytes
		}
		// 提取并比较元素类型。
		var sw *sliceType
		if tt := builtinIdToType(fw); tt != nil {
			sw, _ = tt.(*sliceType)
		} else if wire != nil {
			sw = wire.SliceT
		}
		elem := userType(t.Elem()).base
		return sw != nil && dec.compatibleType(elem, sw.Elem, inProgress)
	case reflect.Struct:
		return true
	}
}

// typeString 返回由 remoteId 标识的类型的可读描述。
func (dec *Decoder) typeString(remoteId typeId) string {
	typeLock.Lock()
	defer typeLock.Unlock()
	if t := idToType(remoteId); t != nil {
		// 全局已知类型。
		return t.string()
	}
	return dec.wireType[remoteId].string()
}

// compileSingle 为非结构体的顶层值（包括 GobDecoder）编译解码器引擎。
func (dec *Decoder) compileSingle(remoteId typeId, ut *userTypeInfo) (engine *decEngine, err error) {
	rt := ut.user
	engine = new(decEngine)
	engine.instr = make([]decInstr, 1) // 一个项
	name := rt.String()                // 尽力而为
	if !dec.compatibleType(rt, remoteId, make(map[reflect.Type]typeId)) {
		remoteType := dec.typeString(remoteId)
		// 常见的令人困惑的情况：本地接口类型，远端具体类型。
		if ut.base.Kind() == reflect.Interface && remoteId != tInterface {
			return nil, errors.New("gob: local interface type " + name + " can only be decoded from remote interface type; received concrete type " + remoteType)
		}
		return nil, errors.New("gob: decoding into local type " + name + ", received remote type " + remoteType)
	}
	op := dec.decOpFor(remoteId, rt, name, make(map[reflect.Type]*decOp))
	ovfl := errors.New(`value for "` + name + `" out of range`)
	engine.instr[singletonField] = decInstr{*op, singletonField, nil, ovfl}
	engine.numInstr = 1
	return
}

// compileIgnoreSingle 为将被丢弃的非结构体顶层值编译解码器引擎。
func (dec *Decoder) compileIgnoreSingle(remoteId typeId) *decEngine {
	engine := new(decEngine)
	engine.instr = make([]decInstr, 1) // 一个项
	op := dec.decIgnoreOpFor(remoteId, make(map[typeId]*decOp))
	ovfl := overflow(dec.typeString(remoteId))
	engine.instr[0] = decInstr{*op, 0, nil, ovfl}
	engine.numInstr = 1
	return engine
}

// compileDec 为一个值编译解码器引擎。如果该值不是结构体，它会调用 compileSingle。
func (dec *Decoder) compileDec(remoteId typeId, ut *userTypeInfo) (engine *decEngine, err error) {
	defer catchError(&err)
	rt := ut.base
	srt := rt
	if srt.Kind() != reflect.Struct || ut.externalDec != 0 {
		return dec.compileSingle(remoteId, ut)
	}
	var wireStruct *structType
	// 内建类型可以来自全局池；其余必须由解码器定义。
	// 另外我们现在知道是在解码结构体，因此客户端一定发送了一个。
	if t := builtinIdToType(remoteId); t != nil {
		wireStruct, _ = t.(*structType)
	} else {
		wire := dec.wireType[remoteId]
		if wire == nil {
			error_(errBadType)
		}
		wireStruct = wire.StructT
	}
	if wireStruct == nil {
		errorf("type mismatch in decoder: want struct type %s; got non-struct", rt)
	}
	engine = new(decEngine)
	engine.instr = make([]decInstr, len(wireStruct.Field))
	seen := make(map[reflect.Type]*decOp)
	// 遍历线类型的字段。
	for fieldnum := 0; fieldnum < len(wireStruct.Field); fieldnum++ {
		wireField := wireStruct.Field[fieldnum]
		if wireField.Name == "" {
			errorf("empty name for remote field of type %s", wireStruct.Name)
		}
		ovfl := overflow(wireField.Name)
		// 找到本地类型中同名的字段。
		localField, present := srt.FieldByName(wireField.Name)
		// TODO(r)：匿名名称
		if !present || !isExported(wireField.Name) {
			op := dec.decIgnoreOpFor(wireField.Id, make(map[typeId]*decOp))
			engine.instr[fieldnum] = decInstr{*op, fieldnum, nil, ovfl}
			continue
		}
		if !dec.compatibleType(localField.Type, wireField.Id, make(map[reflect.Type]typeId)) {
			errorf("wrong type (%s) for received field %s.%s", localField.Type, wireStruct.Name, wireField.Name)
		}
		op := dec.decOpFor(wireField.Id, localField.Type, localField.Name, seen)
		engine.instr[fieldnum] = decInstr{*op, fieldnum, localField.Index, ovfl}
		engine.numInstr++
	}
	return
}

// getDecEnginePtr 返回指定类型的引擎。
func (dec *Decoder) getDecEnginePtr(remoteId typeId, ut *userTypeInfo) (enginePtr **decEngine, err error) {
	rt := ut.user
	decoderMap, ok := dec.decoderCache[rt]
	if !ok {
		decoderMap = make(map[typeId]**decEngine)
		dec.decoderCache[rt] = decoderMap
	}
	if enginePtr, ok = decoderMap[remoteId]; !ok {
		// 为处理递归类型，在编译前先将此引擎标记为进行中。
		enginePtr = new(*decEngine)
		decoderMap[remoteId] = enginePtr
		*enginePtr, err = dec.compileDec(remoteId, ut)
		if err != nil {
			delete(decoderMap, remoteId)
		}
	}
	return
}

// emptyStruct 是我们在忽略结构体值时编译成的类型。
type emptyStruct struct{}

var emptyStructType = reflect.TypeFor[emptyStruct]()

// getIgnoreEnginePtr 返回指定类型的引擎，用于该值将被丢弃的情况。
func (dec *Decoder) getIgnoreEnginePtr(wireId typeId) (enginePtr **decEngine, err error) {
	var ok bool
	if enginePtr, ok = dec.ignorerCache[wireId]; !ok {
		// 为处理递归类型，在编译前先将此引擎标记为进行中。
		enginePtr = new(*decEngine)
		dec.ignorerCache[wireId] = enginePtr
		wire := dec.wireType[wireId]
		if wire != nil && wire.StructT != nil {
			*enginePtr, err = dec.compileDec(wireId, userType(emptyStructType))
		} else {
			*enginePtr = dec.compileIgnoreSingle(wireId)
		}
		if err != nil {
			delete(dec.ignorerCache, wireId)
		}
	}
	return
}

// decodeValue 解码表示某个值的数据流，并将其存储到 value 中。
func (dec *Decoder) decodeValue(wireId typeId, value reflect.Value) {
	defer catchError(&dec.err)
	// 如果该值为 nil，意味着我们应该直接忽略此项。
	if !value.IsValid() {
		dec.decodeIgnoredValue(wireId)
		return
	}
	// 向下解引用到基础类型。
	ut := userType(value.Type())
	base := ut.base
	var enginePtr **decEngine
	enginePtr, dec.err = dec.getDecEnginePtr(wireId, ut)
	if dec.err != nil {
		return
	}
	value = decAlloc(value)
	engine := *enginePtr
	if st := base; st.Kind() == reflect.Struct && ut.externalDec == 0 {
		wt := dec.wireType[wireId]
		if engine.numInstr == 0 && st.NumField() > 0 &&
			wt != nil && len(wt.StructT.Field) > 0 {
			name := base.Name()
			errorf("type mismatch: no fields matched compiling decoder for %s", name)
		}
		dec.decodeStruct(engine, value)
	} else {
		dec.decodeSingle(engine, value)
	}
}

// decodeIgnoredValue 解码表示指定类型的值的数据流并丢弃它。
func (dec *Decoder) decodeIgnoredValue(wireId typeId) {
	var enginePtr **decEngine
	enginePtr, dec.err = dec.getIgnoreEnginePtr(wireId)
	if dec.err != nil {
		return
	}
	wire := dec.wireType[wireId]
	if wire != nil && wire.StructT != nil {
		dec.ignoreStruct(*enginePtr)
	} else {
		dec.ignoreSingle(*enginePtr)
	}
}

const (
	intBits     = 32 << (^uint(0) >> 63)
	uintptrBits = 32 << (^uintptr(0) >> 63)
)

func init() {
	var iop, uop decOp
	switch intBits {
	case 32:
		iop = decInt32
		uop = decUint32
	case 64:
		iop = decInt64
		uop = decUint64
	default:
		panic("gob: unknown size of int/uint")
	}
	decOpTable[reflect.Int] = iop
	decOpTable[reflect.Uint] = uop

	// 最后是 uintptr
	switch uintptrBits {
	case 32:
		uop = decUint32
	case 64:
		uop = decUint64
	default:
		panic("gob: unknown size of uintptr")
	}
	decOpTable[reflect.Uintptr] = uop
}

// Gob 依赖于能够获取它创建的零值 Value 的地址，
// 因此使用这个包装器替代标准的 reflect.Zero。
// 每次调用都会分配一次。
func allocValue(t reflect.Type) reflect.Value {
	return reflect.New(t).Elem()
}
