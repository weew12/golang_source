// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// aix、darwin、js/wasm、openbsd、solaris 和 wasip1/wasm 未实现
// waitid/wait6。

//go:build aix || darwin || (js && wasm) || openbsd || solaris || wasip1

package os

// blockUntilWaitable 尝试阻塞，直到对 p.Wait 的调用
// 能够立即成功，并报告是否已做到这一点。
// 它实际上并不调用 p.Wait。
// 此版本用于未实现 waitid 的系统，
// 或者我们尚未实现它的系统。请注意这是有竞态的：
// 在极不可能的情况下，对 Process.Signal 的调用可能会将
// 信号发送给错误的进程，参见 issue #13987。
func (p *Process) blockUntilWaitable() (bool, error) {
	return false, nil
}
