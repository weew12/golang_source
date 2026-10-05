// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gob

import (
	"errors"
	"io"
	"reflect"
	"sync"
)

// Encoder 管理向连接另一端传输类型和数据信息。
// 它可以安全地被多个 goroutine 并发使用。
type Encoder struct {
	mutex      sync.Mutex              // 每个条目必须原子地发送
	w          []io.Writer             // 数据发送到哪里
	sent       map[reflect.Type]typeId // 我们已经发送过哪些类型
	countState *encoderState           // 用于写入计数的暂存
	freeList   *encoderState           // 空闲的 encoderState 列表；避免重复分配
	byteBuf    encBuffer               // 顶层 encoderState 的缓冲区
	err        error
}

// 在编码一条消息之前，我们在缓冲区头部预留空间，用来编码
// 消息的长度。这意味着我们可以使用该缓冲区组装消息，
// 而无需另一次分配。
const maxLength = 9 // 编码后长度的最大大小。
var spaceForLength = make([]byte, maxLength)

// NewEncoder 返回一个新的编码器，它将在 [io.Writer] 上传输。
func NewEncoder(w io.Writer) *Encoder {
	enc := new(Encoder)
	enc.w = []io.Writer{w}
	enc.sent = make(map[reflect.Type]typeId)
	enc.countState = enc.newEncoderState(new(encBuffer))
	return enc
}

// writer 返回编码器正在使用的最内层 writer。
func (enc *Encoder) writer() io.Writer {
	return enc.w[len(enc.w)-1]
}

// pushWriter 向编码器添加一个 writer。
func (enc *Encoder) pushWriter(w io.Writer) {
	enc.w = append(enc.w, w)
}

// popWriter 弹出最内层 writer。
func (enc *Encoder) popWriter() {
	enc.w = enc.w[0 : len(enc.w)-1]
}

func (enc *Encoder) setError(err error) {
	if enc.err == nil { // 记住第一个。
		enc.err = err
	}
}

// writeMessage 发送数据项，其前面带有表示其长度的无符号计数。
func (enc *Encoder) writeMessage(w io.Writer, b *encBuffer) {
	// 消息头部已经预留了用于存放长度的空间。
	// 这有点取巧：我们从 bytes.Buffer 中取出切片并手工摆弄它。
	message := b.Bytes()
	messageLen := len(message) - maxLength
	// 长度不能大于解码器所能处理的大小。
	if messageLen >= tooBig {
		enc.setError(errors.New("gob: encoder: message too big"))
		return
	}
	// 编码长度。
	enc.countState.b.Reset()
	enc.countState.encodeUint(uint64(messageLen))
	// 复制长度，使其成为消息的前缀。
	offset := maxLength - enc.countState.b.Len()
	copy(message[offset:], enc.countState.b.Bytes())
	// 写入数据。
	_, err := w.Write(message[offset:])
	// 排空缓冲区，并在前端恢复空间，以便存放下一条消息的计数。
	b.Reset()
	b.Write(spaceForLength)
	if err != nil {
		enc.setError(err)
	}
}

// sendActualType 发送所请求的类型，不再做进一步探查，
// 除非它之前已经发送过。
func (enc *Encoder) sendActualType(w io.Writer, state *encoderState, ut *userTypeInfo, actual reflect.Type) (sent bool) {
	if _, alreadySent := enc.sent[actual]; alreadySent {
		return false
	}
	info, err := getTypeInfo(ut)
	if err != nil {
		enc.setError(err)
		return
	}
	// 发送对 (-id, type)
	// Id：
	state.encodeInt(-int64(info.id))
	// Type：
	enc.encode(state.b, reflect.ValueOf(info.wire), wireTypeUserInfo)
	enc.writeMessage(w, state.b)
	if enc.err != nil {
		return
	}

	// 记住我们已经发送过此类型，包括用户给我们的类型和基础类型。
	enc.sent[ut.base] = info.id
	if ut.user != ut.base {
		enc.sent[ut.user] = info.id
	}
	// 现在发送内部类型
	switch st := actual; st.Kind() {
	case reflect.Struct:
		for i := 0; i < st.NumField(); i++ {
			if isExported(st.Field(i).Name) {
				enc.sendType(w, state, st.Field(i).Type)
			}
		}
	case reflect.Array, reflect.Slice:
		enc.sendType(w, state, st.Elem())
	case reflect.Map:
		enc.sendType(w, state, st.Key())
		enc.sendType(w, state, st.Elem())
	}
	return true
}

// sendType 在必要时向另一端发送类型信息。
func (enc *Encoder) sendType(w io.Writer, state *encoderState, origt reflect.Type) (sent bool) {
	ut := userType(origt)
	if ut.externalEnc != 0 {
		// 规则不同：无论底层类型的表示形式如何，
		// 我们都需要告诉另一端基础类型是一个 GobEncoder。
		return enc.sendActualType(w, state, ut, ut.base)
	}

	// 这是一个具体值，因此向下深入到基础类型。
	switch rt := ut.base; rt.Kind() {
	default:
		// 基本类型和接口无需描述。
		return
	case reflect.Slice:
		// 如果是 []uint8，则不要发送；它被视为基本类型。
		if rt.Elem().Kind() == reflect.Uint8 {
			return
		}
		// 否则我们就发送。
		break
	case reflect.Array:
		// 数组必须发送，以便我们知道它们的长度和元素类型。
		break
	case reflect.Map:
		// map 必须发送，以便我们知道它们的长度和键/值类型。
		break
	case reflect.Struct:
		// 结构体必须发送，以便我们知道它们的字段。
		break
	case reflect.Chan, reflect.Func:
		// 如果我们走到这里，说明它是结构体的一个字段；忽略它。
		return
	}

	return enc.sendActualType(w, state, ut, ut.base)
}

// Encode 传输由空接口值表示的数据项，保证所有必要的类型信息
// 都已先行传输。向 Encoder 传入 nil 指针会 panic，
// 因为它们无法由 gob 传输。
func (enc *Encoder) Encode(e any) error {
	return enc.EncodeValue(reflect.ValueOf(e))
}

// sendTypeDescriptor 确保远端知晓此类型。
// 如果这是该类型第一次被发送，它将发送一个描述符。
func (enc *Encoder) sendTypeDescriptor(w io.Writer, state *encoderState, ut *userTypeInfo) {
	// 确保另一端已知晓该类型。
	// 首先，我们是否已经发送过此类型？
	rt := ut.base
	if ut.externalEnc != 0 {
		rt = ut.user
	}
	if _, alreadySent := enc.sent[rt]; !alreadySent {
		// 没有，那就发送它。
		sent := enc.sendType(w, state, rt)
		if enc.err != nil {
			return
		}
		// 如果类型信息仍未传输，这意味着我们在顶层有一个
		// 单例基本类型（int、[]byte 等）。我们不需要发送类型信息，
		// 但确实需要更新 enc.sent。
		if !sent {
			info, err := getTypeInfo(ut)
			if err != nil {
				enc.setError(err)
				return
			}
			enc.sent[rt] = info.id
		}
	}
}

// sendTypeId 发送 id，该 id 必须已经定义。
func (enc *Encoder) sendTypeId(state *encoderState, ut *userTypeInfo) {
	// 标识该顶层值的类型。
	state.encodeInt(int64(enc.sent[ut.base]))
}

// EncodeValue 传输由反射值表示的数据项，保证所有必要的类型信息
// 都已先行传输。向 EncodeValue 传入 nil 指针会 panic，
// 因为它们无法由 gob 传输。
func (enc *Encoder) EncodeValue(value reflect.Value) error {
	if value.Kind() == reflect.Invalid {
		return errors.New("gob: cannot encode nil value")
	}
	if value.Kind() == reflect.Pointer && value.IsNil() {
		panic("gob: cannot encode nil pointer of type " + value.Type().String())
	}

	// 确保此处是单线程执行的，以便多个
	// goroutine 可以共享一个 encoder。
	enc.mutex.Lock()
	defer enc.mutex.Unlock()

	// 移除因先前错误而残留的任何嵌套 writer。
	enc.w = enc.w[0:1]

	ut, err := validUserType(value.Type())
	if err != nil {
		return err
	}

	enc.err = nil
	enc.byteBuf.Reset()
	enc.byteBuf.Write(spaceForLength)
	state := enc.newEncoderState(&enc.byteBuf)

	enc.sendTypeDescriptor(enc.writer(), state, ut)
	enc.sendTypeId(state, ut)
	if enc.err != nil {
		return enc.err
	}

	// 编码该对象。
	enc.encode(state.b, value, ut)
	if enc.err == nil {
		enc.writeMessage(enc.writer(), state.b)
	}

	enc.freeEncoderState(state)
	return enc.err
}
