// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"encoding/binary"
	"math/bits"
)

// stringCache 是用于缓存由 []byte 转换而来的字符串的缓存。
type stringCache = [256]string // 256*unsafe.Sizeof(string("")) => 4KiB

// makeString 返回 b 的字符串形式。
// 如果 c 中存在预先分配的字符串则返回它，
// 否则分配一个新字符串，将其插入缓存并返回。
func makeString(c *stringCache, b []byte) string {
	const (
		minCachedLen = 2   // 单字节字符串已由运行时驻留
		maxCachedLen = 256 // 足以容纳 UUID、IPv6 地址、SHA-256 校验和等
	)
	if c == nil || len(b) < minCachedLen || len(b) > maxCachedLen {
		return string(b)
	}

	// 从字符串的固定宽度前缀和后缀计算哈希值。
	// 这确保对字符串进行哈希是常数时间操作。
	var h uint32
	switch {
	case len(b) >= 8:
		lo := binary.LittleEndian.Uint64(b[:8])
		hi := binary.LittleEndian.Uint64(b[len(b)-8:])
		h = hash64(uint32(lo), uint32(lo>>32)) ^ hash64(uint32(hi), uint32(hi>>32))
	case len(b) >= 4:
		lo := binary.LittleEndian.Uint32(b[:4])
		hi := binary.LittleEndian.Uint32(b[len(b)-4:])
		h = hash64(lo, hi)
	case len(b) >= 2:
		lo := binary.LittleEndian.Uint16(b[:2])
		hi := binary.LittleEndian.Uint16(b[len(b)-2:])
		h = hash64(uint32(lo), uint32(hi))
	}

	// 在缓存中检查该字符串。
	i := h % uint32(len(*c))
	if s := (*c)[i]; s == string(b) {
		return s
	}
	s := string(b)
	(*c)[i] = s
	return s
}

// hash64 将两个 uint32 的哈希返回为单个 uint32。
func hash64(lo, hi uint32) uint32 {
	// 如果 avalanche=true，则这与对 8B 字符串执行 XXH32 哈希完全相同：
	//	var b [8]byte
	//	binary.LittleEndian.PutUint32(b[:4], lo)
	//	binary.LittleEndian.PutUint32(b[4:], hi)
	//	return xxhash.Sum32(b[:])
	const (
		prime1 = 0x9e3779b1
		prime2 = 0x85ebca77
		prime3 = 0xc2b2ae3d
		prime4 = 0x27d4eb2f
		prime5 = 0x165667b1
	)
	h := prime5 + uint32(8)
	h += lo * prime3
	h = bits.RotateLeft32(h, 17) * prime4
	h += hi * prime3
	h = bits.RotateLeft32(h, 17) * prime4
	// 出于性能原因，跳过 XXH32 的最终混合（雪崩）步骤。
	// 经验测试表明，无偏分布方面的改进
	// 不足以抵消计算复杂度带来的额外成本。
	const avalanche = false
	if avalanche {
		h ^= h >> 15
		h *= prime2
		h ^= h >> 13
		h *= prime3
		h ^= h >> 16
	}
	return h
}
