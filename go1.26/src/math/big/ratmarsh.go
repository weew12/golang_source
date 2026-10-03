// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现 Rat 的编码/解码。

package big

import (
	"errors"
	"fmt"
	"internal/byteorder"
	"math"
)

// Gob 编解码版本。允许对编码进行向后兼容的修改。
const ratGobVersion byte = 1

// GobEncode 实现 [encoding/gob.GobEncoder] 接口。
func (x *Rat) GobEncode() ([]byte, error) {
	if x == nil {
		return nil, nil
	}
	buf := make([]byte, 1+4+(len(x.a.abs)+len(x.b.abs))*_S) // 用于版本和符号位（1）、分子长度（4）的额外字节
	i := x.b.abs.bytes(buf)
	j := x.a.abs.bytes(buf[:i])
	n := i - j
	if int(uint32(n)) != n {
		// 这不应该发生
		return nil, errors.New("Rat.GobEncode: numerator too large")
	}
	byteorder.BEPutUint32(buf[j-4:j], uint32(n))
	j -= 1 + 4
	b := ratGobVersion << 1 // 为符号位腾出空间
	if x.a.neg {
		b |= 1
	}
	buf[j] = b
	return buf[j:], nil
}

// GobDecode 实现 [encoding/gob.GobDecoder] 接口。
func (z *Rat) GobDecode(buf []byte) error {
	if len(buf) == 0 {
		// 对端发送了 nil 或默认值。
		*z = Rat{}
		return nil
	}
	if len(buf) < 5 {
		return errors.New("Rat.GobDecode: buffer too small")
	}
	b := buf[0]
	if b>>1 != ratGobVersion {
		return fmt.Errorf("Rat.GobDecode: encoding version %d not supported", b>>1)
	}
	const j = 1 + 4
	ln := byteorder.BEUint32(buf[j-4 : j])
	if uint64(ln) > math.MaxInt-j {
		return errors.New("Rat.GobDecode: invalid length")
	}
	i := j + int(ln)
	if len(buf) < i {
		return errors.New("Rat.GobDecode: buffer too small")
	}
	z.a.neg = b&1 != 0
	z.a.abs = z.a.abs.setBytes(buf[j:i])
	z.b.abs = z.b.abs.setBytes(buf[i:])
	return nil
}

// AppendText 实现 [encoding.TextAppender] 接口。
func (x *Rat) AppendText(b []byte) ([]byte, error) {
	if x.IsInt() {
		return x.a.AppendText(b)
	}
	return x.marshal(b), nil
}

// MarshalText 实现 [encoding.TextMarshaler] 接口。
func (x *Rat) MarshalText() (text []byte, err error) {
	return x.AppendText(nil)
}

// UnmarshalText 实现 [encoding.TextUnmarshaler] 接口。
func (z *Rat) UnmarshalText(text []byte) error {
	// TODO(gri): 去掉 []byte/string 转换
	if _, ok := z.SetString(string(text)); !ok {
		return fmt.Errorf("math/big: cannot unmarshal %q into a *big.Rat", text)
	}
	return nil
}
