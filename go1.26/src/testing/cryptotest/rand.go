// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cryptotest 提供确定性随机源测试。
package cryptotest

import (
	cryptorand "crypto/rand"
	"internal/byteorder"
	"io"
	mathrand "math/rand/v2"
	"sync"
	"testing"

	// 导入 unsafe 与 crypto/rand（后者会导入 crypto/internal/rand），
	// 以使用 crypto/internal/rand.SetTestingReader 的 go:linkname。
	_ "crypto/rand"
	_ "unsafe"
)

//go:linkname randSetTestingReader crypto/internal/rand.SetTestingReader
func randSetTestingReader(r io.Reader)

//go:linkname testingCheckParallel testing.checkParallel
func testingCheckParallel(t *testing.T)

// SetGlobalRandom 在测试 t 期间设置一个全局的、确定性的加密随机性源。
// 它会影响 crypto/rand，以及 crypto/... 各包中所有隐式的
// 加密随机性来源。
//
// 可以在同一个测试中多次调用 SetGlobalRandom，以重置
// 随机流或更改种子。
//
// 由于 SetGlobalRandom 会影响整个进程，因此不能用于
// 并行测试或具有并行祖先的测试。
//
// 注意，密码算法使用随机性的方式通常并未明确规定，且可能随时间变化。
// 因此，如果某个测试期望从加密函数获得特定输出，
// 即使使用了 SetGlobalRandom，将来也可能失败。
//
// 当针对 Go Cryptographic Module v1.0.0 构建时（即当
// [crypto/fips140.Version] 返回 “v1.0.0” 时），不支持 SetGlobalRandom。
func SetGlobalRandom(t *testing.T, seed uint64) {
	if t == nil {
		panic("cryptotest: SetGlobalRandom called with a nil *testing.T")
	}
	if !testing.Testing() {
		panic("cryptotest: SetGlobalRandom used in a non-test binary")
	}
	testingCheckParallel(t)

	var s [32]byte
	byteorder.LEPutUint64(s[:8], seed)
	r := &lockedReader{r: mathrand.NewChaCha8(s)}

	randSetTestingReader(r)
	previous := cryptorand.Reader
	cryptorand.Reader = r
	t.Cleanup(func() {
		cryptorand.Reader = previous
		randSetTestingReader(nil)
	})
}

type lockedReader struct {
	sync.Mutex
	r *mathrand.ChaCha8
}

func (lr *lockedReader) Read(b []byte) (n int, err error) {
	lr.Lock()
	defer lr.Unlock()
	return lr.r.Read(b)
}
