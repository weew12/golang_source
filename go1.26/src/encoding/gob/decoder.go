// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gob

import (
	"bufio"
	"errors"
	"internal/saferio"
	"io"
	"reflect"
	"sync"
)

// tooBig 为大小提供一个健全性检查；在多个地方使用。其上限在 32 位系统上
// 为 1GB，在 64 位系统上为 8GB，留出一点增长空间而不会溢出。
const tooBig = (1 << 30) << (^uint(0) >> 62)

// Decoder 管理从连接远端读取类型和数据信息的接收过程。
// 它可以安全地被多个 goroutine 并发使用。
//
// Decoder 对解码所得的输入大小只做基本的健全性检查，
// 且其限制不可配置。在解码来自不可信来源的 gob 数据时要小心。
type Decoder struct {
	mutex        sync.Mutex                              // 每个条目必须原子地接收
	r            io.Reader                               // 数据来源
	buf          decBuffer                               // 用于从 r 更高效地进行 i/o 的缓冲区
	wireType     map[typeId]*wireType                    // 从远端 ID 到本地描述的 map
	decoderCache map[reflect.Type]map[typeId]**decEngine // 已编译引擎的缓存
	ignorerCache map[typeId]**decEngine                  // 被忽略对象的同类缓存
	freeList     *decoderState                           // 空闲的 decoderState 列表；避免重复分配
	countBuf     []byte                                  // 解析消息时用于解码整数
	err          error
	// ignoreDepth 跟踪递归解析被忽略字段的深度
	ignoreDepth int
}

// NewDecoder 返回一个新的解码器，它从 [io.Reader] 读取。
// 如果 r 未同时实现 [io.ByteReader]，它将被包装在一个
// [bufio.Reader] 中。
func NewDecoder(r io.Reader) *Decoder {
	dec := new(Decoder)
	// 我们把读取字节的能力当作缓冲的一个合理替代品。
	if _, ok := r.(io.ByteReader); !ok {
		r = bufio.NewReader(r)
	}
	dec.r = r
	dec.wireType = make(map[typeId]*wireType)
	dec.decoderCache = make(map[reflect.Type]map[typeId]**decEngine)
	dec.ignorerCache = make(map[typeId]**decEngine)
	dec.countBuf = make([]byte, 9) // 计数可能是 uint64（可能性不大！），需要 9 字节

	return dec
}

// recvType 加载一个类型的定义。
func (dec *Decoder) recvType(id typeId) {
	// 我们是否已经见过此类型？那是一个错误
	if id < firstUserId || dec.wireType[id] != nil {
		dec.err = errors.New("gob: duplicate type received")
		return
	}

	// Type：
	wire := new(wireType)
	dec.decodeValue(tWireType, reflect.ValueOf(wire))
	if dec.err != nil {
		return
	}
	// 记住我们已经见过此类型。
	dec.wireType[id] = wire
}

var errBadCount = errors.New("invalid message length")

// recvMessage 从输入中读取下一个以计数分隔的条目。它是
// Encoder.writeMessage 的逆操作。它在 EOF 或读取消息时出现其他错误时
// 返回 false。
func (dec *Decoder) recvMessage() bool {
	// 读取一个计数。
	nbytes, _, err := decodeUintReader(dec.r, dec.countBuf)
	if err != nil {
		dec.err = err
		return false
	}
	if nbytes >= tooBig {
		dec.err = errBadCount
		return false
	}
	dec.readMessage(int(nbytes))
	return dec.err == nil
}

// readMessage 从输入中读取接下来的 nbytes 个字节。
func (dec *Decoder) readMessage(nbytes int) {
	if dec.buf.Len() != 0 {
		// 此时缓冲区应始终为空。
		panic("non-empty decoder buffer")
	}
	// 读取数据
	var buf []byte
	buf, dec.err = saferio.ReadData(dec.r, uint64(nbytes))
	dec.buf.SetBytes(buf)
	if dec.err == io.EOF {
		dec.err = io.ErrUnexpectedEOF
	}
}

// toInt 根据编组规则，将经过编码的 uint64 转换为 int。
func toInt(x uint64) int64 {
	i := int64(x >> 1)
	if x&1 != 0 {
		i = ^i
	}
	return i
}

func (dec *Decoder) nextInt() int64 {
	n, _, err := decodeUintReader(&dec.buf, dec.countBuf)
	if err != nil {
		dec.err = err
	}
	return toInt(n)
}

func (dec *Decoder) nextUint() uint64 {
	n, _, err := decodeUintReader(&dec.buf, dec.countBuf)
	if err != nil {
		dec.err = err
	}
	return n
}

// decodeTypeSequence 解析：
// TypeSequence
//
//	(TypeDefinition DelimitedTypeDefinition*)?
//
// 并返回下一个值的类型 id。它在 EOF 时返回 -1。返回时，dec.buf 的剩余部分就是
// 待解码的值。如果这是一个接口值，可以通过重置该缓冲区来忽略它。
func (dec *Decoder) decodeTypeSequence(isInterface bool) typeId {
	firstMessage := true
	for dec.err == nil {
		if dec.buf.Len() == 0 {
			if !dec.recvMessage() {
				// 只有当输入为空时，我们才能返回 io.EOF。
				// 如果我们读取了一个或多个类型描述消息，
				// 那么要求其后必须跟随一个数据项消息。
				// 如果在此之前遇到 EOF，则返回 ErrUnexpectedEOF。
				if !firstMessage && dec.err == io.EOF {
					dec.err = io.ErrUnexpectedEOF
				}
				break
			}
		}
		// 接收一个类型 id。
		id := typeId(dec.nextInt())
		if id >= 0 {
			// 其后跟随值。
			return id
		}
		// 其后跟随 (-id) 的类型定义。
		dec.recvType(-id)
		if dec.err != nil {
			break
		}
		// 解码接口时，在类型之后缓冲区中可能还留有一个
		// DelimitedValue。跳过它的计数。
		// （或者，缓冲区为空，此时字节计数将由 recvMessage 吸收。）
		if dec.buf.Len() > 0 {
			if !isInterface {
				dec.err = errors.New("extra data in buffer")
				break
			}
			dec.nextUint()
		}
		firstMessage = false
	}
	return -1
}

// Decode 从输入流中读取下一个值，并将其存储到空接口值所表示的数据中。
// 如果 e 为 nil，则该值将被丢弃。否则，e 底层所表示的值必须是指向
// 接收到的下一个数据项正确类型的指针。
// 如果输入已到达 EOF，Decode 返回 [io.EOF] 且不修改 e。
func (dec *Decoder) Decode(e any) error {
	if e == nil {
		return dec.DecodeValue(reflect.Value{})
	}
	value := reflect.ValueOf(e)
	// 如果 e 表示的是一个值而非指针，那么结果将无法传回调用者。确保它是一个指针。
	if value.Kind() != reflect.Pointer {
		dec.err = errors.New("gob: attempt to decode into a non-pointer")
		return dec.err
	}
	return dec.DecodeValue(value)
}

// DecodeValue 从输入流中读取下一个值。
// 如果 v 是零值 reflect.Value（v.Kind() == Invalid），DecodeValue 会丢弃该值。
// 否则，它会将值存储到 v 中。在这种情况下，v 必须表示一个指向数据的非 nil 指针，
// 或者是一个可赋值的 reflect.Value（v.CanSet()）。
// 如果输入已到达 EOF，DecodeValue 返回 [io.EOF] 且不修改 v。
func (dec *Decoder) DecodeValue(v reflect.Value) error {
	if v.IsValid() {
		if v.Kind() == reflect.Pointer && !v.IsNil() {
			// 这没问题，我们会通过该指针存储。
		} else if !v.CanSet() {
			return errors.New("gob: DecodeValue of unassignable value")
		}
	}
	// 确保这里始终是单线程执行的。
	dec.mutex.Lock()
	defer dec.mutex.Unlock()

	dec.buf.Reset() // 以防上次调用遗留了数据。
	dec.err = nil
	id := dec.decodeTypeSequence(false)
	if dec.err == nil {
		dec.decodeValue(id, v)
	}
	return dec.err
}

// 如果 debug.go 被编译进程序中，debugFunc 会通过调用该文件的 Debug 函数，打印从 r
// 读取的 gob 数据的可读表示。否则它为 nil。
var debugFunc func(io.Reader)
