// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rand

import (
	"errors"
	"internal/byteorder"
	"math/bits"
)

// https://numpy.org/devdocs/reference/random/upgrading-pcg64.html
// https://github.com/imneme/pcg-cpp/commit/871d0494ee9c9a7b7c43f753e3d8ca47c26f8005

// PCG 是一个具有 128 位内部状态的 PCG 生成器。
// 零值 PCG 等价于 NewPCG(0, 0)。
type PCG struct {
	hi uint64
	lo uint64
}

// NewPCG 返回一个用给定值播种的新 PCG。
func NewPCG(seed1, seed2 uint64) *PCG {
	return &PCG{seed1, seed2}
}

// Seed 重置 PCG，使其行为与 NewPCG(seed1, seed2) 相同。
func (p *PCG) Seed(seed1, seed2 uint64) {
	p.hi = seed1
	p.lo = seed2
}

// AppendBinary 实现了 [encoding.BinaryAppender] 接口。
func (p *PCG) AppendBinary(b []byte) ([]byte, error) {
	b = append(b, "pcg:"...)
	b = byteorder.BEAppendUint64(b, p.hi)
	b = byteorder.BEAppendUint64(b, p.lo)
	return b, nil
}

// MarshalBinary 实现了 [encoding.BinaryMarshaler] 接口。
func (p *PCG) MarshalBinary() ([]byte, error) {
	return p.AppendBinary(make([]byte, 0, 20))
}

var errUnmarshalPCG = errors.New("invalid PCG encoding")

// UnmarshalBinary 实现了 [encoding.BinaryUnmarshaler] 接口。
func (p *PCG) UnmarshalBinary(data []byte) error {
	if len(data) != 20 || string(data[:4]) != "pcg:" {
		return errUnmarshalPCG
	}
	p.hi = byteorder.BEUint64(data[4:])
	p.lo = byteorder.BEUint64(data[4+8:])
	return nil
}

func (p *PCG) next() (hi, lo uint64) {
	// https://github.com/imneme/pcg-cpp/blob/428802d1a5/include/pcg_random.hpp#L161
	//
	// Numpy 的 PCG 乘以 64 位值 cheapMul，
	// 而不是这里和官方 PCG 代码中使用的 128 位值。
	// 这似乎不值得，至少对 Go 而言是这样：乘数中没有任何高位
	// 会削弱低位对最高位的影响，
	// 而且它只节省了 3 次乘法中的 1 次。
	// （在 32 位系统上，它节省了 6 次中的 1 次，因为 Mul64 要做 4 次。）
	const (
		mulHi = 2549297995355413924
		mulLo = 4865540595714422341
		incHi = 6364136223846793005
		incLo = 1442695040888963407
	)

	// state = state * mul + inc
	hi, lo = bits.Mul64(p.lo, mulLo)
	hi += p.hi*mulLo + p.lo*mulHi
	lo, c := bits.Add64(lo, incLo, 0)
	hi, _ = bits.Add64(hi, incHi, c)
	p.lo = lo
	p.hi = hi
	return hi, lo
}

// Uint64 返回一个均匀分布的随机 uint64 值。
func (p *PCG) Uint64() uint64 {
	hi, lo := p.next()

	// XSL-RR 会是
	//	hi, lo := p.next()
	//	return bits.RotateLeft64(lo^hi, -int(hi>>58))
	// 但 Numpy 使用 DXSM，且 O'Neill 建议也这样做。
	// 参见 https://github.com/golang/go/issues/21835#issuecomment-739065688
	// 及其后续评论。

	// DXSM "double xorshift multiply"（双重异或移位乘法）
	// https://github.com/imneme/pcg-cpp/blob/428802d1a5/include/pcg_random.hpp#L1015

	// https://github.com/imneme/pcg-cpp/blob/428802d1a5/include/pcg_random.hpp#L176
	const cheapMul = 0xda942042e4dd58b5
	hi ^= hi >> 32
	hi *= cheapMul
	hi ^= hi >> 48
	hi *= (lo | 1)
	return hi
}
