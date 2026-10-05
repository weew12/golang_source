// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package binary 实现了数字与字节序列之间的简单转换，以及 varint 的编码和解码。
//
// 数字通过读取和写入固定大小的值进行转换。
// 固定大小的值可以是固定大小的算术类型（bool、int8、uint8、int16、
// float32、complex64 等），也可以是仅包含固定大小值的数组或结构体。
//
// varint 函数使用变长编码对单个整数值进行编码和解码；较小的值需要更少的字节。
// 有关规范，请参见
// https://developers.google.com/protocol-buffers/docs/encoding。
//
// 本包更注重简洁性而非效率。需要高性能序列化（尤其是对于大型数据结构）的客户端
// 应研究更高级的解决方案，例如 [encoding/gob] 包，
// 或用于 protocol buffers 的 [google.golang.org/protobuf]。
package binary

import (
	"errors"
	"io"
	"math"
	"reflect"
	"slices"
	"sync"
)

var errBufferTooSmall = errors.New("buffer too small")

// ByteOrder 指定如何将字节切片转换为
// 16 位、32 位或 64 位无符号整数。
//
// 它由 [LittleEndian]、[BigEndian] 和 [NativeEndian] 实现。
type ByteOrder interface {
	Uint16([]byte) uint16
	Uint32([]byte) uint32
	Uint64([]byte) uint64
	PutUint16([]byte, uint16)
	PutUint32([]byte, uint32)
	PutUint64([]byte, uint64)
	String() string
}

// AppendByteOrder 指定如何将 16 位、32 位或 64 位无符号整数
// 追加到字节切片中。
//
// 它由 [LittleEndian]、[BigEndian] 和 [NativeEndian] 实现。
type AppendByteOrder interface {
	AppendUint16([]byte, uint16) []byte
	AppendUint32([]byte, uint32) []byte
	AppendUint64([]byte, uint64) []byte
	String() string
}

// LittleEndian 是 [ByteOrder] 和 [AppendByteOrder] 的小端字节序实现。
var LittleEndian littleEndian

// BigEndian 是 [ByteOrder] 和 [AppendByteOrder] 的大端字节序实现。
var BigEndian bigEndian

type littleEndian struct{}

// Uint16 返回 b[0:2] 的 uint16 表示。
func (littleEndian) Uint16(b []byte) uint16 {
	_ = b[1] // 向编译器提示边界检查；参见 golang.org/issue/14808
	return uint16(b[0]) | uint16(b[1])<<8
}

// PutUint16 将 v 存入 b[0:2]。
func (littleEndian) PutUint16(b []byte, v uint16) {
	_ = b[1] // 提前进行边界检查，以保证下面写入的安全性
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

// AppendUint16 将 v 的字节追加到 b，并返回追加后的切片。
func (littleEndian) AppendUint16(b []byte, v uint16) []byte {
	return append(b,
		byte(v),
		byte(v>>8),
	)
}

// Uint32 返回 b[0:4] 的 uint32 表示。
func (littleEndian) Uint32(b []byte) uint32 {
	_ = b[3] // 向编译器提示边界检查；参见 golang.org/issue/14808
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// PutUint32 将 v 存入 b[0:4]。
func (littleEndian) PutUint32(b []byte, v uint32) {
	_ = b[3] // 提前进行边界检查，以保证下面写入的安全性
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// AppendUint32 将 v 的字节追加到 b，并返回追加后的切片。
func (littleEndian) AppendUint32(b []byte, v uint32) []byte {
	return append(b,
		byte(v),
		byte(v>>8),
		byte(v>>16),
		byte(v>>24),
	)
}

// Uint64 返回 b[0:8] 的 uint64 表示。
func (littleEndian) Uint64(b []byte) uint64 {
	_ = b[7] // 向编译器提示边界检查；参见 golang.org/issue/14808
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

// PutUint64 将 v 存入 b[0:8]。
func (littleEndian) PutUint64(b []byte, v uint64) {
	_ = b[7] // 提前进行边界检查，以保证下面写入的安全性
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
}

// AppendUint64 将 v 的字节追加到 b，并返回追加后的切片。
func (littleEndian) AppendUint64(b []byte, v uint64) []byte {
	return append(b,
		byte(v),
		byte(v>>8),
		byte(v>>16),
		byte(v>>24),
		byte(v>>32),
		byte(v>>40),
		byte(v>>48),
		byte(v>>56),
	)
}

func (littleEndian) String() string { return "LittleEndian" }

func (littleEndian) GoString() string { return "binary.LittleEndian" }

type bigEndian struct{}

// Uint16 返回 b[0:2] 的 uint16 表示。
func (bigEndian) Uint16(b []byte) uint16 {
	_ = b[1] // 向编译器提示边界检查；参见 golang.org/issue/14808
	return uint16(b[1]) | uint16(b[0])<<8
}

// PutUint16 将 v 存入 b[0:2]。
func (bigEndian) PutUint16(b []byte, v uint16) {
	_ = b[1] // 提前进行边界检查，以保证下面写入的安全性
	b[0] = byte(v >> 8)
	b[1] = byte(v)
}

// AppendUint16 将 v 的字节追加到 b，并返回追加后的切片。
func (bigEndian) AppendUint16(b []byte, v uint16) []byte {
	return append(b,
		byte(v>>8),
		byte(v),
	)
}

// Uint32 返回 b[0:4] 的 uint32 表示。
func (bigEndian) Uint32(b []byte) uint32 {
	_ = b[3] // 向编译器提示边界检查；参见 golang.org/issue/14808
	return uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
}

// PutUint32 将 v 存入 b[0:4]。
func (bigEndian) PutUint32(b []byte, v uint32) {
	_ = b[3] // 提前进行边界检查，以保证下面写入的安全性
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

// AppendUint32 将 v 的字节追加到 b，并返回追加后的切片。
func (bigEndian) AppendUint32(b []byte, v uint32) []byte {
	return append(b,
		byte(v>>24),
		byte(v>>16),
		byte(v>>8),
		byte(v),
	)
}

// Uint64 返回 b[0:8] 的 uint64 表示。
func (bigEndian) Uint64(b []byte) uint64 {
	_ = b[7] // 向编译器提示边界检查；参见 golang.org/issue/14808
	return uint64(b[7]) | uint64(b[6])<<8 | uint64(b[5])<<16 | uint64(b[4])<<24 |
		uint64(b[3])<<32 | uint64(b[2])<<40 | uint64(b[1])<<48 | uint64(b[0])<<56
}

// PutUint64 将 v 存入 b[0:8]。
func (bigEndian) PutUint64(b []byte, v uint64) {
	_ = b[7] // 提前进行边界检查，以保证下面写入的安全性
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
}

// AppendUint64 将 v 的字节追加到 b，并返回追加后的切片。
func (bigEndian) AppendUint64(b []byte, v uint64) []byte {
	return append(b,
		byte(v>>56),
		byte(v>>48),
		byte(v>>40),
		byte(v>>32),
		byte(v>>24),
		byte(v>>16),
		byte(v>>8),
		byte(v),
	)
}

func (bigEndian) String() string { return "BigEndian" }

func (bigEndian) GoString() string { return "binary.BigEndian" }

func (nativeEndian) String() string { return "NativeEndian" }

func (nativeEndian) GoString() string { return "binary.NativeEndian" }

// Read 从 r 读取结构化二进制数据并存入 data。
// data 必须是指向固定大小值的指针，或者是固定大小值的切片。
// 从 r 读取的字节按指定的字节序解码，并写入 data 的各个连续字段。
// 解码布尔值时，零字节被解码为 false，任何其他非零字节被解码为 true。
// 读入结构体时，字段名为空白（_）的字段数据会被跳过；
// 也就是说，空白字段名可用于填充。
// 读入结构体时，所有非空白字段都必须导出，否则 Read 可能会 panic。
//
// 只有在未读取任何字节时，错误才为 [io.EOF]。
// 如果在读取了一些但并非全部字节之后发生 [io.EOF]，
// Read 会返回 [io.ErrUnexpectedEOF]。
func Read(r io.Reader, order ByteOrder, data any) error {
	// 基本类型和切片的快速路径。
	if n, _ := intDataSize(data); n != 0 {
		bs := make([]byte, n)
		if _, err := io.ReadFull(r, bs); err != nil {
			return err
		}

		if decodeFast(bs, order, data) {
			return nil
		}
	}

	// 回退到基于反射的解码。
	v := reflect.ValueOf(data)
	size := -1
	switch v.Kind() {
	case reflect.Pointer:
		v = v.Elem()
		size = dataSize(v)
	case reflect.Slice:
		size = dataSize(v)
	}
	if size < 0 {
		return errors.New("binary.Read: invalid type " + reflect.TypeOf(data).String())
	}

	d := &decoder{order: order, buf: make([]byte, size)}
	if _, err := io.ReadFull(r, d.buf); err != nil {
		return err
	}
	d.value(v)
	return nil
}

// Decode 按给定的字节序将 buf 中的二进制数据解码到 data。
// 如果 buf 太小，它会返回错误，否则返回从 buf 消耗的字节数。
func Decode(buf []byte, order ByteOrder, data any) (int, error) {
	if n, _ := intDataSize(data); n != 0 {
		if len(buf) < n {
			return 0, errBufferTooSmall
		}

		if decodeFast(buf, order, data) {
			return n, nil
		}
	}

	// 回退到基于反射的解码。
	v := reflect.ValueOf(data)
	size := -1
	switch v.Kind() {
	case reflect.Pointer:
		v = v.Elem()
		size = dataSize(v)
	case reflect.Slice:
		size = dataSize(v)
	}
	if size < 0 {
		return 0, errors.New("binary.Decode: invalid type " + reflect.TypeOf(data).String())
	}

	if len(buf) < size {
		return 0, errBufferTooSmall
	}
	d := &decoder{order: order, buf: buf[:size]}
	d.value(v)
	return size, nil
}

func decodeFast(bs []byte, order ByteOrder, data any) bool {
	switch data := data.(type) {
	case *bool:
		*data = bs[0] != 0
	case *int8:
		*data = int8(bs[0])
	case *uint8:
		*data = bs[0]
	case *int16:
		*data = int16(order.Uint16(bs))
	case *uint16:
		*data = order.Uint16(bs)
	case *int32:
		*data = int32(order.Uint32(bs))
	case *uint32:
		*data = order.Uint32(bs)
	case *int64:
		*data = int64(order.Uint64(bs))
	case *uint64:
		*data = order.Uint64(bs)
	case *float32:
		*data = math.Float32frombits(order.Uint32(bs))
	case *float64:
		*data = math.Float64frombits(order.Uint64(bs))
	case []bool:
		for i, x := range bs { // 对于 8 位值，直接循环遍历输入更简单。
			data[i] = x != 0
		}
	case []int8:
		for i, x := range bs {
			data[i] = int8(x)
		}
	case []uint8:
		copy(data, bs)
	case []int16:
		for i := range data {
			data[i] = int16(order.Uint16(bs[2*i:]))
		}
	case []uint16:
		for i := range data {
			data[i] = order.Uint16(bs[2*i:])
		}
	case []int32:
		for i := range data {
			data[i] = int32(order.Uint32(bs[4*i:]))
		}
	case []uint32:
		for i := range data {
			data[i] = order.Uint32(bs[4*i:])
		}
	case []int64:
		for i := range data {
			data[i] = int64(order.Uint64(bs[8*i:]))
		}
	case []uint64:
		for i := range data {
			data[i] = order.Uint64(bs[8*i:])
		}
	case []float32:
		for i := range data {
			data[i] = math.Float32frombits(order.Uint32(bs[4*i:]))
		}
	case []float64:
		for i := range data {
			data[i] = math.Float64frombits(order.Uint64(bs[8*i:]))
		}
	default:
		return false
	}
	return true
}

// Write 将 data 的二进制表示写入 w。
// data 必须是固定大小值、固定大小值的切片，或指向此类数据的指针。
// 布尔值编码为一个字节：true 为 1，false 为 0。
// 写入 w 的字节按指定的字节序编码，并从 data 的各个连续字段读取。
// 写入结构体时，字段名为空白（_）的字段会写入零值。
func Write(w io.Writer, order ByteOrder, data any) error {
	// 基本类型和切片的快速路径。
	if n, bs := intDataSize(data); n != 0 {
		if bs == nil {
			bs = make([]byte, n)
			encodeFast(bs, order, data)
		}

		_, err := w.Write(bs)
		return err
	}

	// 回退到基于反射的编码。
	v := reflect.Indirect(reflect.ValueOf(data))
	size := dataSize(v)
	if size < 0 {
		return errors.New("binary.Write: some values are not fixed-sized in type " + reflect.TypeOf(data).String())
	}

	buf := make([]byte, size)
	e := &encoder{order: order, buf: buf}
	e.value(v)
	_, err := w.Write(buf)
	return err
}

// Encode 按给定的字节序将 data 的二进制表示编码到 buf。
// 如果 buf 太小，它会返回错误，否则返回写入 buf 的字节数。
func Encode(buf []byte, order ByteOrder, data any) (int, error) {
	// 基本类型和切片的快速路径。
	if n, _ := intDataSize(data); n != 0 {
		if len(buf) < n {
			return 0, errBufferTooSmall
		}

		encodeFast(buf, order, data)
		return n, nil
	}

	// 回退到基于反射的编码。
	v := reflect.Indirect(reflect.ValueOf(data))
	size := dataSize(v)
	if size < 0 {
		return 0, errors.New("binary.Encode: some values are not fixed-sized in type " + reflect.TypeOf(data).String())
	}

	if len(buf) < size {
		return 0, errBufferTooSmall
	}
	e := &encoder{order: order, buf: buf}
	e.value(v)
	return size, nil
}

// Append 将 data 的二进制表示追加到 buf。
// buf 可以为 nil，此时会分配一个新的缓冲区。
// 关于哪些 data 是可接受的，请参见 [Write]。
// 它返回包含 data 的（可能已扩展的）缓冲区，或返回错误。
func Append(buf []byte, order ByteOrder, data any) ([]byte, error) {
	// 基本类型和切片的快速路径。
	if n, _ := intDataSize(data); n != 0 {
		buf, pos := ensure(buf, n)
		encodeFast(pos, order, data)
		return buf, nil
	}

	// 回退到基于反射的编码。
	v := reflect.Indirect(reflect.ValueOf(data))
	size := dataSize(v)
	if size < 0 {
		return nil, errors.New("binary.Append: some values are not fixed-sized in type " + reflect.TypeOf(data).String())
	}

	buf, pos := ensure(buf, size)
	e := &encoder{order: order, buf: pos}
	e.value(v)
	return buf, nil
}

func encodeFast(bs []byte, order ByteOrder, data any) {
	switch v := data.(type) {
	case *bool:
		if *v {
			bs[0] = 1
		} else {
			bs[0] = 0
		}
	case bool:
		if v {
			bs[0] = 1
		} else {
			bs[0] = 0
		}
	case []bool:
		for i, x := range v {
			if x {
				bs[i] = 1
			} else {
				bs[i] = 0
			}
		}
	case *int8:
		bs[0] = byte(*v)
	case int8:
		bs[0] = byte(v)
	case []int8:
		for i, x := range v {
			bs[i] = byte(x)
		}
	case *uint8:
		bs[0] = *v
	case uint8:
		bs[0] = v
	case []uint8:
		copy(bs, v)
	case *int16:
		order.PutUint16(bs, uint16(*v))
	case int16:
		order.PutUint16(bs, uint16(v))
	case []int16:
		for i, x := range v {
			order.PutUint16(bs[2*i:], uint16(x))
		}
	case *uint16:
		order.PutUint16(bs, *v)
	case uint16:
		order.PutUint16(bs, v)
	case []uint16:
		for i, x := range v {
			order.PutUint16(bs[2*i:], x)
		}
	case *int32:
		order.PutUint32(bs, uint32(*v))
	case int32:
		order.PutUint32(bs, uint32(v))
	case []int32:
		for i, x := range v {
			order.PutUint32(bs[4*i:], uint32(x))
		}
	case *uint32:
		order.PutUint32(bs, *v)
	case uint32:
		order.PutUint32(bs, v)
	case []uint32:
		for i, x := range v {
			order.PutUint32(bs[4*i:], x)
		}
	case *int64:
		order.PutUint64(bs, uint64(*v))
	case int64:
		order.PutUint64(bs, uint64(v))
	case []int64:
		for i, x := range v {
			order.PutUint64(bs[8*i:], uint64(x))
		}
	case *uint64:
		order.PutUint64(bs, *v)
	case uint64:
		order.PutUint64(bs, v)
	case []uint64:
		for i, x := range v {
			order.PutUint64(bs[8*i:], x)
		}
	case *float32:
		order.PutUint32(bs, math.Float32bits(*v))
	case float32:
		order.PutUint32(bs, math.Float32bits(v))
	case []float32:
		for i, x := range v {
			order.PutUint32(bs[4*i:], math.Float32bits(x))
		}
	case *float64:
		order.PutUint64(bs, math.Float64bits(*v))
	case float64:
		order.PutUint64(bs, math.Float64bits(v))
	case []float64:
		for i, x := range v {
			order.PutUint64(bs[8*i:], math.Float64bits(x))
		}
	}
}

// Size 返回 [Write] 为编码值 v 会生成多少字节，v
// 必须是固定大小值、固定大小值的切片，或指向此类数据的指针。
// 如果 v 不属于这些类型，Size 返回 -1。
func Size(v any) int {
	switch data := v.(type) {
	case bool, int8, uint8:
		return 1
	case *bool:
		if data == nil {
			return -1
		}
		return 1
	case *int8:
		if data == nil {
			return -1
		}
		return 1
	case *uint8:
		if data == nil {
			return -1
		}
		return 1
	case []bool:
		return len(data)
	case []int8:
		return len(data)
	case []uint8:
		return len(data)
	case int16, uint16:
		return 2
	case *int16:
		if data == nil {
			return -1
		}
		return 2
	case *uint16:
		if data == nil {
			return -1
		}
		return 2
	case []int16:
		return 2 * len(data)
	case []uint16:
		return 2 * len(data)
	case int32, uint32:
		return 4
	case *int32:
		if data == nil {
			return -1
		}
		return 4
	case *uint32:
		if data == nil {
			return -1
		}
		return 4
	case []int32:
		return 4 * len(data)
	case []uint32:
		return 4 * len(data)
	case int64, uint64:
		return 8
	case *int64:
		if data == nil {
			return -1
		}
		return 8
	case *uint64:
		if data == nil {
			return -1
		}
		return 8
	case []int64:
		return 8 * len(data)
	case []uint64:
		return 8 * len(data)
	case float32:
		return 4
	case *float32:
		if data == nil {
			return -1
		}
		return 4
	case float64:
		return 8
	case *float64:
		if data == nil {
			return -1
		}
		return 8
	case []float32:
		return 4 * len(data)
	case []float64:
		return 8 * len(data)
	}
	return dataSize(reflect.Indirect(reflect.ValueOf(v)))
}

var structSize sync.Map // map[reflect.Type]int

// dataSize 返回 v 所表示的实际数据在内存中占用的字节数。
// 对于复合结构，它累加各元素的大小。因此，例如对于切片，
// 它返回切片长度乘以元素大小，而不计入头部占用的内存。
// 如果 v 的类型不可接受，dataSize 返回 -1。
func dataSize(v reflect.Value) int {
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		t := v.Type().Elem()
		if size, ok := structSize.Load(t); ok {
			return size.(int) * v.Len()
		}

		size := sizeof(t)
		if size >= 0 {
			if t.Kind() == reflect.Struct {
				structSize.Store(t, size)
			}
			return size * v.Len()
		}

	case reflect.Struct:
		t := v.Type()
		if size, ok := structSize.Load(t); ok {
			return size.(int)
		}
		size := sizeof(t)
		structSize.Store(t, size)
		return size

	default:
		if v.IsValid() {
			return sizeof(v.Type())
		}
	}

	return -1
}

// sizeof 返回给定类型变量的大小（>= 0），如果类型不可接受则返回 -1。
func sizeof(t reflect.Type) int {
	switch t.Kind() {
	case reflect.Array:
		if s := sizeof(t.Elem()); s >= 0 {
			return s * t.Len()
		}

	case reflect.Struct:
		sum := 0
		for i, n := 0, t.NumField(); i < n; i++ {
			s := sizeof(t.Field(i).Type)
			if s < 0 {
				return -1
			}
			sum += s
		}
		return sum

	case reflect.Bool,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return int(t.Size())
	}

	return -1
}

type coder struct {
	order  ByteOrder
	buf    []byte
	offset int
}

type decoder coder
type encoder coder

func (d *decoder) bool() bool {
	x := d.buf[d.offset]
	d.offset++
	return x != 0
}

func (e *encoder) bool(x bool) {
	if x {
		e.buf[e.offset] = 1
	} else {
		e.buf[e.offset] = 0
	}
	e.offset++
}

func (d *decoder) uint8() uint8 {
	x := d.buf[d.offset]
	d.offset++
	return x
}

func (e *encoder) uint8(x uint8) {
	e.buf[e.offset] = x
	e.offset++
}

func (d *decoder) uint16() uint16 {
	x := d.order.Uint16(d.buf[d.offset : d.offset+2])
	d.offset += 2
	return x
}

func (e *encoder) uint16(x uint16) {
	e.order.PutUint16(e.buf[e.offset:e.offset+2], x)
	e.offset += 2
}

func (d *decoder) uint32() uint32 {
	x := d.order.Uint32(d.buf[d.offset : d.offset+4])
	d.offset += 4
	return x
}

func (e *encoder) uint32(x uint32) {
	e.order.PutUint32(e.buf[e.offset:e.offset+4], x)
	e.offset += 4
}

func (d *decoder) uint64() uint64 {
	x := d.order.Uint64(d.buf[d.offset : d.offset+8])
	d.offset += 8
	return x
}

func (e *encoder) uint64(x uint64) {
	e.order.PutUint64(e.buf[e.offset:e.offset+8], x)
	e.offset += 8
}

func (d *decoder) int8() int8 { return int8(d.uint8()) }

func (e *encoder) int8(x int8) { e.uint8(uint8(x)) }

func (d *decoder) int16() int16 { return int16(d.uint16()) }

func (e *encoder) int16(x int16) { e.uint16(uint16(x)) }

func (d *decoder) int32() int32 { return int32(d.uint32()) }

func (e *encoder) int32(x int32) { e.uint32(uint32(x)) }

func (d *decoder) int64() int64 { return int64(d.uint64()) }

func (e *encoder) int64(x int64) { e.uint64(uint64(x)) }

func (d *decoder) value(v reflect.Value) {
	switch v.Kind() {
	case reflect.Array:
		l := v.Len()
		for i := 0; i < l; i++ {
			d.value(v.Index(i))
		}

	case reflect.Struct:
		t := v.Type()
		l := v.NumField()
		for i := 0; i < l; i++ {
			// 注意：下面调用 v.CanSet() 是一种优化。
			// 仅检查字段名就足够了，但为每个字段创建 StructField
			// 信息的开销很大（运行 "go test -bench=ReadStruct" 并在此代码
			// 发生更改时比较结果）。
			if v := v.Field(i); v.CanSet() || t.Field(i).Name != "_" {
				d.value(v)
			} else {
				d.skip(v)
			}
		}

	case reflect.Slice:
		l := v.Len()
		for i := 0; i < l; i++ {
			d.value(v.Index(i))
		}

	case reflect.Bool:
		v.SetBool(d.bool())

	case reflect.Int8:
		v.SetInt(int64(d.int8()))
	case reflect.Int16:
		v.SetInt(int64(d.int16()))
	case reflect.Int32:
		v.SetInt(int64(d.int32()))
	case reflect.Int64:
		v.SetInt(d.int64())

	case reflect.Uint8:
		v.SetUint(uint64(d.uint8()))
	case reflect.Uint16:
		v.SetUint(uint64(d.uint16()))
	case reflect.Uint32:
		v.SetUint(uint64(d.uint32()))
	case reflect.Uint64:
		v.SetUint(d.uint64())

	case reflect.Float32:
		v.SetFloat(float64(math.Float32frombits(d.uint32())))
	case reflect.Float64:
		v.SetFloat(math.Float64frombits(d.uint64()))

	case reflect.Complex64:
		v.SetComplex(complex(
			float64(math.Float32frombits(d.uint32())),
			float64(math.Float32frombits(d.uint32())),
		))
	case reflect.Complex128:
		v.SetComplex(complex(
			math.Float64frombits(d.uint64()),
			math.Float64frombits(d.uint64()),
		))
	}
}

func (e *encoder) value(v reflect.Value) {
	switch v.Kind() {
	case reflect.Array:
		l := v.Len()
		for i := 0; i < l; i++ {
			e.value(v.Index(i))
		}

	case reflect.Struct:
		t := v.Type()
		l := v.NumField()
		for i := 0; i < l; i++ {
			// 参见 decoder.value() 中对应代码的注释
			if v := v.Field(i); v.CanSet() || t.Field(i).Name != "_" {
				e.value(v)
			} else {
				e.skip(v)
			}
		}

	case reflect.Slice:
		l := v.Len()
		for i := 0; i < l; i++ {
			e.value(v.Index(i))
		}

	case reflect.Bool:
		e.bool(v.Bool())

	case reflect.Int8:
		e.int8(int8(v.Int()))
	case reflect.Int16:
		e.int16(int16(v.Int()))
	case reflect.Int32:
		e.int32(int32(v.Int()))
	case reflect.Int64:
		e.int64(v.Int())

	case reflect.Uint8:
		e.uint8(uint8(v.Uint()))
	case reflect.Uint16:
		e.uint16(uint16(v.Uint()))
	case reflect.Uint32:
		e.uint32(uint32(v.Uint()))
	case reflect.Uint64:
		e.uint64(v.Uint())

	case reflect.Float32:
		e.uint32(math.Float32bits(float32(v.Float())))
	case reflect.Float64:
		e.uint64(math.Float64bits(v.Float()))

	case reflect.Complex64:
		x := v.Complex()
		e.uint32(math.Float32bits(float32(real(x))))
		e.uint32(math.Float32bits(float32(imag(x))))
	case reflect.Complex128:
		x := v.Complex()
		e.uint64(math.Float64bits(real(x)))
		e.uint64(math.Float64bits(imag(x)))
	}
}

func (d *decoder) skip(v reflect.Value) {
	d.offset += dataSize(v)
}

func (e *encoder) skip(v reflect.Value) {
	n := dataSize(v)
	clear(e.buf[e.offset : e.offset+n])
	e.offset += n
}

// intDataSize 返回编码时表示 data 所需的数据大小，
// 并在无需转换时可选地返回包含已编码数据的字节切片。
// 如果该类型无法由 Read 或 Write 中的快速路径实现，则返回 0, nil。
func intDataSize(data any) (int, []byte) {
	switch data := data.(type) {
	case bool, int8, uint8, *bool, *int8, *uint8:
		return 1, nil
	case []bool:
		return len(data), nil
	case []int8:
		return len(data), nil
	case []uint8:
		return len(data), data
	case int16, uint16, *int16, *uint16:
		return 2, nil
	case []int16:
		return 2 * len(data), nil
	case []uint16:
		return 2 * len(data), nil
	case int32, uint32, *int32, *uint32:
		return 4, nil
	case []int32:
		return 4 * len(data), nil
	case []uint32:
		return 4 * len(data), nil
	case int64, uint64, *int64, *uint64:
		return 8, nil
	case []int64:
		return 8 * len(data), nil
	case []uint64:
		return 8 * len(data), nil
	case float32, *float32:
		return 4, nil
	case float64, *float64:
		return 8, nil
	case []float32:
		return 4 * len(data), nil
	case []float64:
		return 8 * len(data), nil
	}
	return 0, nil
}

// ensure 将 buf 增长到长度 len(buf) + n，并返回增长后的缓冲区
// 以及从 buf 原始长度处开始的切片（即 buf2[len(buf):]）。
func ensure(buf []byte, n int) (buf2, pos []byte) {
	l := len(buf)
	buf = slices.Grow(buf, n)[:l+n]
	return buf, buf[l:]
}
