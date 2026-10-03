// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现 Float 的编码/解码。

package big

import (
	"errors"
	"fmt"
	"internal/byteorder"
)

// Gob 编解码版本。允许对编码进行向后兼容的修改。
const floatGobVersion byte = 1

// GobEncode 实现 [encoding/gob.GobEncoder] 接口。
// [Float] 值及其所有属性（精度、舍入模式、准确性）都会被编组。
func (x *Float) GobEncode() ([]byte, error) {
	if x == nil {
		return nil, nil
	}

	// 确定编码所需的最大空间（字节数）
	sz := 1 + 1 + 4 // version + mode|acc|form|neg (3+2+2+1bit) + prec
	n := 0          // 尾数字（word）的数量
	if x.form == finite {
		// 为尾数和指数增加空间
		n = int((x.prec + (_W - 1)) / _W) // 给定精度下所需的尾数长度（以字为单位）
		// 实际的尾数切片可能更短（末尾有 0）或更长（有未使用的位）：
		// - 如果更短，只编码存在的字
		// - 如果更长，在编码为字节时截掉未使用的字
		//   （实际上这不应发生，因为舍入会处理它，
		//   但为了安全起见，总是这样做）
		if len(x.mant) < n {
			n = len(x.mant)
		}
		// len(x.mant) >= n
		sz += 4 + n*_S // exp + mant
	}
	buf := make([]byte, sz)

	buf[0] = floatGobVersion
	b := byte(x.mode&7)<<5 | byte((x.acc+1)&3)<<3 | byte(x.form&3)<<1
	if x.neg {
		b |= 1
	}
	buf[1] = b
	byteorder.BEPutUint32(buf[2:], x.prec)

	if x.form == finite {
		byteorder.BEPutUint32(buf[6:], uint32(x.exp))
		x.mant[len(x.mant)-n:].bytes(buf[10:]) // 截掉未使用的尾随字
	}

	return buf, nil
}

// GobDecode 实现 [encoding/gob.GobDecoder] 接口。
// 结果按照 z 的精度和舍入模式进行舍入，除非 z 的精度为 0，
// 此时 z 会被精确地设置为解码后的值。
func (z *Float) GobDecode(buf []byte) error {
	if len(buf) == 0 {
		// 对端发送了 nil 或默认值。
		*z = Float{}
		return nil
	}
	if len(buf) < 6 {
		return errors.New("Float.GobDecode: buffer too small")
	}

	if buf[0] != floatGobVersion {
		return fmt.Errorf("Float.GobDecode: encoding version %d not supported", buf[0])
	}

	oldPrec := z.prec
	oldMode := z.mode

	b := buf[1]
	z.mode = RoundingMode((b >> 5) & 7)
	z.acc = Accuracy((b>>3)&3) - 1
	z.form = form((b >> 1) & 3)
	z.neg = b&1 != 0
	z.prec = byteorder.BEUint32(buf[2:])

	if z.form == finite {
		if len(buf) < 10 {
			return errors.New("Float.GobDecode: buffer too small for finite form float")
		}
		z.exp = int32(byteorder.BEUint32(buf[6:]))
		z.mant = z.mant.setBytes(buf[10:])
	}

	if oldPrec != 0 {
		z.mode = oldMode
		z.SetPrec(uint(oldPrec))
	}

	if msg := z.validate0(); msg != "" {
		return errors.New("Float.GobDecode: " + msg)
	}

	return nil
}

// AppendText 实现 [encoding.TextAppender] 接口。
// 仅编组 [Float] 值（全精度），精度或准确性等其他属性会被忽略。
func (x *Float) AppendText(b []byte) ([]byte, error) {
	if x == nil {
		return append(b, "<nil>"...), nil
	}
	return x.Append(b, 'g', -1), nil
}

// MarshalText 实现 [encoding.TextMarshaler] 接口。
// 仅编组 [Float] 值（全精度），精度或准确性等其他属性会被忽略。
func (x *Float) MarshalText() (text []byte, err error) {
	return x.AppendText(nil)
}

// UnmarshalText 实现 [encoding.TextUnmarshaler] 接口。
// 结果按照 z 的精度和舍入模式进行舍入。
// 如果 z 的精度为 0，则在舍入生效之前会被改为 64。
func (z *Float) UnmarshalText(text []byte) error {
	// TODO(gri): 去掉 []byte/string 转换
	_, _, err := z.Parse(string(text), 0)
	if err != nil {
		err = fmt.Errorf("math/big: cannot unmarshal %q into a *big.Float (%v)", text, err)
	}
	return err
}
