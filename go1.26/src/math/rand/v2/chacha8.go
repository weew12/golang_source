// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rand

import (
	"errors"
	"internal/byteorder"
	"internal/chacha8rand"
)

// ChaCha8 是一个基于 ChaCha8 的密码学强
// 随机数生成器。
type ChaCha8 struct {
	state chacha8rand.State

	// readBuf 的最后 readLen 个字节仍待 Read 消费。
	readBuf [8]byte
	readLen int // 0 <= readLen <= 8
}

// NewChaCha8 返回一个用给定种子播种的新 ChaCha8。
func NewChaCha8(seed [32]byte) *ChaCha8 {
	c := new(ChaCha8)
	c.state.Init(seed)
	return c
}

// Seed 重置 ChaCha8，使其行为与 NewChaCha8(seed) 相同。
func (c *ChaCha8) Seed(seed [32]byte) {
	c.state.Init(seed)
	c.readLen = 0
	c.readBuf = [8]byte{}
}

// Uint64 返回一个均匀分布的随机 uint64 值。
func (c *ChaCha8) Uint64() uint64 {
	for {
		x, ok := c.state.Next()
		if ok {
			return x
		}
		c.state.Refill()
	}
}

// Read 恰好将 len(p) 个字节读入 p。
// 它总是返回 len(p) 和一个 nil 错误。
//
// 如果对 Read 和 Uint64 的调用交替进行，两者返回位的顺序
// 是未定义的，并且 Read 可能返回在
// 最后一次调用 Uint64 之前生成的位。
func (c *ChaCha8) Read(p []byte) (n int, err error) {
	if c.readLen > 0 {
		n = copy(p, c.readBuf[len(c.readBuf)-c.readLen:])
		c.readLen -= n
		p = p[n:]
	}
	for len(p) >= 8 {
		byteorder.LEPutUint64(p, c.Uint64())
		p = p[8:]
		n += 8
	}
	if len(p) > 0 {
		byteorder.LEPutUint64(c.readBuf[:], c.Uint64())
		n += copy(p, c.readBuf[:])
		c.readLen = 8 - len(p)
	}
	return
}

// UnmarshalBinary 实现了 [encoding.BinaryUnmarshaler] 接口。
func (c *ChaCha8) UnmarshalBinary(data []byte) error {
	data, ok := cutPrefix(data, []byte("readbuf:"))
	if ok {
		var buf []byte
		buf, data, ok = readUint8LengthPrefixed(data)
		if !ok {
			return errors.New("invalid ChaCha8 Read buffer encoding")
		}
		c.readLen = copy(c.readBuf[len(c.readBuf)-len(buf):], buf)
	}
	return chacha8rand.Unmarshal(&c.state, data)
}

func cutPrefix(s, prefix []byte) (after []byte, found bool) {
	if len(s) < len(prefix) || string(s[:len(prefix)]) != string(prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

func readUint8LengthPrefixed(b []byte) (buf, rest []byte, ok bool) {
	if len(b) == 0 || len(b) < int(1+b[0]) {
		return nil, nil, false
	}
	return b[1 : 1+b[0]], b[1+b[0]:], true
}

// AppendBinary 实现了 [encoding.BinaryAppender] 接口。
func (c *ChaCha8) AppendBinary(b []byte) ([]byte, error) {
	if c.readLen > 0 {
		b = append(b, "readbuf:"...)
		b = append(b, uint8(c.readLen))
		b = append(b, c.readBuf[len(c.readBuf)-c.readLen:]...)
	}
	return append(b, chacha8rand.Marshal(&c.state)...), nil
}

// MarshalBinary 实现了 [encoding.BinaryMarshaler] 接口。
func (c *ChaCha8) MarshalBinary() ([]byte, error) {
	// (chacha8rand.Marshal + c.readBuf + "readbuf:") 的最大长度是 64
	return c.AppendBinary(make([]byte, 0, 64))
}
