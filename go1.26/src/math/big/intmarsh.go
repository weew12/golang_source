// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现 Int 的编码/解码。

package big

import (
	"bytes"
	"fmt"
)

// Gob 编解码版本。允许对编码进行向后兼容的修改。
const intGobVersion byte = 1

// GobEncode 实现 [encoding/gob.GobEncoder] 接口。
func (x *Int) GobEncode() ([]byte, error) {
	if x == nil {
		return nil, nil
	}
	buf := make([]byte, 1+len(x.abs)*_S) // 用于版本和符号位的额外字节
	i := x.abs.bytes(buf) - 1            // i >= 0
	b := intGobVersion << 1              // 为符号位腾出空间
	if x.neg {
		b |= 1
	}
	buf[i] = b
	return buf[i:], nil
}

// GobDecode 实现 [encoding/gob.GobDecoder] 接口。
func (z *Int) GobDecode(buf []byte) error {
	if len(buf) == 0 {
		// 对端发送了 nil 或默认值。
		*z = Int{}
		return nil
	}
	b := buf[0]
	if b>>1 != intGobVersion {
		return fmt.Errorf("Int.GobDecode: encoding version %d not supported", b>>1)
	}
	z.neg = b&1 != 0
	z.abs = z.abs.setBytes(buf[1:])
	return nil
}

// AppendText 实现 [encoding.TextAppender] 接口。
func (x *Int) AppendText(b []byte) (text []byte, err error) {
	return x.Append(b, 10), nil
}

// MarshalText 实现 [encoding.TextMarshaler] 接口。
func (x *Int) MarshalText() (text []byte, err error) {
	return x.AppendText(nil)
}

// UnmarshalText 实现 [encoding.TextUnmarshaler] 接口。
func (z *Int) UnmarshalText(text []byte) error {
	if _, ok := z.setFromScanner(bytes.NewReader(text), 0); !ok {
		return fmt.Errorf("math/big: cannot unmarshal %q into a *big.Int", text)
	}
	return nil
}

// 这里保留 JSON 编组函数只是为了 API 向后兼容
//（即那些显式查找这两个方法的程序）。仅使用 TextMarshaler，
// JSON 也能正常工作。

// MarshalJSON 实现 [encoding/json.Marshaler] 接口。
func (x *Int) MarshalJSON() ([]byte, error) {
	if x == nil {
		return []byte("null"), nil
	}
	return x.abs.itoa(x.neg, 10), nil
}

// UnmarshalJSON 实现 [encoding/json.Unmarshaler] 接口。
func (z *Int) UnmarshalJSON(text []byte) error {
	// 就像主 JSON 包一样，忽略 null。
	if string(text) == "null" {
		return nil
	}
	return z.UnmarshalText(text)
}
