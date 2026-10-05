// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"runtime"
)

// AllocsPerRun 返回调用 f 期间的平均分配次数。
// 尽管返回值类型为 float64，但它始终是一个整数值。
//
// 为了计算分配次数，该函数会先运行一次作为预热。
// 随后将测量并返回指定运行次数内的平均分配次数。
//
// AllocsPerRun 在测量期间将 [runtime.GOMAXPROCS] 设置为 1，并在返回前恢复它。
func AllocsPerRun(runs int, f func()) (avg float64) {
	if parallelStart.Load() != parallelStop.Load() {
		panic("testing: AllocsPerRun called during parallel test")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	// 预热函数
	f()

	// 测量初始统计信息
	var memstats runtime.MemStats
	runtime.ReadMemStats(&memstats)
	mallocs := 0 - memstats.Mallocs

	// 按指定次数运行函数
	for i := 0; i < runs; i++ {
		f()
	}

	// 读取最终统计信息
	runtime.ReadMemStats(&memstats)
	mallocs += memstats.Mallocs

	// 计算各次运行中分配次数的平均值（不计入预热）。
	// 由于该 API 设计欠佳，我们被迫返回 float64，但
	// 以整数方式进行除法，这样我们可以判断 AllocsPerRun()==1
	// 而不是 AllocsPerRun()<2。
	return float64(mallocs / uint64(runs))
}
