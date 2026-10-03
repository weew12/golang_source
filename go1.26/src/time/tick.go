// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import "unsafe"

// 注意：由于 newTimer 负责分配 struct Ticker，运行时知道它的布局。
// 还要注意，Ticker 和 Timer 具有相同的布局，因此 newTimer 可以同时处理两者。
// initTimer 和 initTicker 字段命名不同，这样
// 用户在没有 unsafe 的情况下无法在两者之间进行转换。

// Ticker 持有一个 channel，它会按间隔
// 传递时钟的“滴答”。
type Ticker struct {
	C          <-chan Time // 传递滴答的 channel。
	initTicker bool
}

// NewTicker 返回一个新的 [Ticker]，其中包含一个 channel，它将在每个
// 滴答后在该 channel 上发送当前时间。滴答的周期
// 由 duration 参数指定。ticker 会调整
// 时间间隔或丢弃滴答，以补偿接收缓慢的接收者。
// 时长 d 必须大于零；否则 NewTicker 将
// panic。
//
// 在 Go 1.23 之前，垃圾回收器不会回收
// 尚未到期或尚未被停止的 ticker，因此代码通常
// 在调用 NewTicker 后立即 defer t.Stop，以便
// 在不再需要该 ticker 时使其可被回收。
// 从 Go 1.23 起，垃圾回收器可以回收未被引用的
// ticker，即使它们尚未被停止。
// Stop 方法不再需要用于帮助垃圾回收器。
// （代码当然仍可能出于其他原因想要调用 Stop 来停止 ticker。）
func NewTicker(d Duration) *Ticker {
	if d <= 0 {
		panic("non-positive interval for NewTicker")
	}
	// 给 channel 一个 1 个元素的时间缓冲区。
	// 如果客户端在读取时落后，我们会丢弃滴答
	// 不管，直到客户端追上。
	c := make(chan Time, 1)
	t := (*Ticker)(unsafe.Pointer(newTimer(when(d), int64(d), sendTime, c, syncTimer(c))))
	t.C = c
	return t
}

// Stop 关闭一个 ticker。Stop 之后，将不再发送滴答。
// Stop 不会关闭 channel，以防止正在从该 channel
// 读取的并发 goroutine 看到一个错误的 "tick"。
func (t *Ticker) Stop() {
	if !t.initTicker {
		// 这是误用，对于 time.Timer 同样的情况会 panic，
		// 但这并非总是 panic，我们保持它不 panic
		// 以避免破坏旧程序。参见 issue 21874。
		return
	}
	stopTimer((*Timer)(unsafe.Pointer(t)))
}

// Reset 停止一个 ticker 并将其周期重置为指定的时长。
// 下一个滴答将在新周期过去后到来。时长 d
// 必须大于零；否则 Reset 将 panic。
func (t *Ticker) Reset(d Duration) {
	if d <= 0 {
		panic("non-positive interval for Ticker.Reset")
	}
	if !t.initTicker {
		panic("time: Reset called on uninitialized Ticker")
	}
	resetTimer((*Timer)(unsafe.Pointer(t)), when(d), int64(d))
}

// Tick 是 [NewTicker] 的便捷包装，仅提供对滴答
// channel 的访问。与 NewTicker 不同，如果 d <= 0，Tick 将返回 nil。
//
// 在 Go 1.23 之前，本文档警告说，底层
// [Ticker] 永远不会被垃圾回收器回收，并且
// 如果关心效率，代码应改用 NewTicker，并在
// 不再需要该 ticker 时调用 [Ticker.Stop]。
// 从 Go 1.23 起，垃圾回收器可以回收未被引用的
// ticker，即使它们尚未被停止。
// Stop 方法不再需要用于帮助垃圾回收器。
// 当 Tick 可以满足需求时，不再有任何理由更偏好 NewTicker。
func Tick(d Duration) <-chan Time {
	if d <= 0 {
		return nil
	}
	return NewTicker(d).C
}
