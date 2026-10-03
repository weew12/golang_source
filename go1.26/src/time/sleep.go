// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	"internal/godebug"
	"unsafe"
)

// Sleep 至少将当前 goroutine 暂停时长 d。
// 负数或零时长会导致 Sleep 立即返回。
func Sleep(d Duration)

var asynctimerchan = godebug.New("asynctimerchan")

// syncTimer 将 c 作为 unsafe.Pointer 返回，以便传递给 newTimer。
// 如果 GODEBUG asynctimerchan 已禁用异步 timer channel
// 代码，那么 syncTimer 总是返回 nil，以禁用运行时中
// 特殊的 channel 代码路径。
func syncTimer(c chan Time) unsafe.Pointer {
	// 如果 asynctimerchan=1，我们甚至不会将 channel timer
	// 告知运行时，这样我们就会走 Go 1.23 之前的代码路径。
	if asynctimerchan.Value() == "1" {
		asynctimerchan.IncNonDefault()
		return nil
	}

	// 否则传递给运行时。
	// 这会处理 asynctimerchan=0（Go 1.23 的默认行为），
	// 以及 asynctimerchan=2，它类似于 asynctimerchan=1，
	// 但完全由运行时实现。
	// 使用 asynctimerchan=2 的唯一原因是调试
	// 一个由 asynctimerchan=1 修复的问题：它启用了新的
	// 可被 GC 回收的 timer channel（#61542），但不启用同步 channel（#37196）。
	//
	// 如果我们决定回退同步 channel，我们仍然拥有
	// 一个经过充分测试的异步运行时实现（asynctimerchan=2），
	// 并且可以让此函数始终返回 c。
	//
	// 如果我们决定保留同步 channel，我们可以删除运行时中
	// 所有对 asynctimerchan 的处理，只保留下这个
	// 函数来处理 asynctimerchan=1。
	return *(*unsafe.Pointer)(unsafe.Pointer(&c))
}

// when 是一个辅助函数，用于设置 runtimeTimer 的 'when' 字段。
// 它返回距今 Duration d 之后的时刻，以纳秒为单位。
// 如果 d 为负数，则被忽略。如果由于溢出导致返回值
// 小于零，则返回 MaxInt64。
func when(d Duration) int64 {
	if d <= 0 {
		return runtimeNano()
	}
	t := runtimeNano() + int64(d)
	if t < 0 {
		// 注意：runtimeNano() 和 d 始终为正，因此相加
		//（包括溢出）绝不会得到 t == 0。
		t = 1<<63 - 1 // math.MaxInt64
	}
	return t
}

// 这些函数由 package runtime 推送到 package time。

// 参数 cp 是一个 chan Time，但 runtime 中的声明使用指针，
// 因此我们这里也使用指针。这让一些会激进地
// 比较 linkname 符号定义的工具更满意。
//
//go:linkname newTimer
func newTimer(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) *Timer

//go:linkname stopTimer
func stopTimer(*Timer) bool

//go:linkname resetTimer
func resetTimer(t *Timer, when, period int64) bool

// 注意：由于 newTimer 负责分配 struct Timer，运行时知道它的布局。
// 运行时还知道 Ticker 和 Timer 具有相同的布局。
// channel 之后有额外的字段，预留给运行时使用，
// 用户无法访问。

// Timer 类型表示单个事件。
// 当 Timer 到期时，当前时间将被发送到 C，
// 除非该 Timer 是由 [AfterFunc] 创建的。
// Timer 必须使用 [NewTimer] 或 AfterFunc 创建。
type Timer struct {
	C         <-chan Time
	initTimer bool
}

// Stop 阻止 [Timer] 触发。
// 如果该调用停止了 timer，则返回 true；如果 timer 已经
// 到期或已被停止，则返回 false。
//
// 对于使用 [AfterFunc](d, f) 创建的基于函数的 timer，
// 如果 t.Stop 返回 false，那么说明 timer 已经到期，
// 并且函数 f 已在它自己的 goroutine 中启动；
// Stop 在返回前不会等待 f 完成。
// 如果调用者需要知道 f 是否已完成，
// 必须与 f 显式协调。
//
// 对于使用 NewTimer(d) 创建的基于 chan 的 timer，从 Go 1.23 起，
// 在 Stop 返回后从 t.C 进行的任何接收都保证会阻塞，
// 而不会接收到 Stop 之前的过期时间值；
// 如果程序尚未从 t.C 接收过，并且 timer 正在
// 运行，则 Stop 保证返回 true。
// 在 Go 1.23 之前，使用 Stop 的唯一安全做法是在 Stop 返回 false 时
// 插入额外的 <-t.C 以排空可能存在的过期值。
// 详情参见 [NewTimer] 文档。
func (t *Timer) Stop() bool {
	if !t.initTimer {
		panic("time: Stop called on uninitialized Timer")
	}
	return stopTimer(t)
}

// NewTimer 创建一个新的 Timer，它将在至少经过时长 d 后
// 在其 channel 上发送当前时间。
//
// 在 Go 1.23 之前，垃圾回收器不会回收
// 尚未到期或尚未被停止的 timer，因此代码通常
// 在调用 NewTimer 后立即 defer t.Stop，以便
// 在不再需要该 timer 时使其可被回收。
// 从 Go 1.23 起，垃圾回收器可以回收未被引用的
// timer，即使它们尚未到期或尚未被停止。
// Stop 方法不再需要用于帮助垃圾回收器。
// （代码当然仍可能出于其他原因想要调用 Stop 来停止 timer。）
//
// 在 Go 1.23 之前，与 Timer 关联的 channel 是
// 异步的（带缓冲，容量为 1），这意味着
// 即使在 [Timer.Stop] 或 [Timer.Reset] 返回之后，
// 也可能接收到过期的时间值。
// 从 Go 1.23 起，该 channel 是同步的（无缓冲，容量为 0），
// 消除了这些过期值的可能性。
//
// GODEBUG 设置 asynctimerchan=1 会恢复这两项
// Go 1.23 之前的行为：设置后，未到期的 timer 不会被垃圾回收，
// 并且 channel 将具有缓冲容量。该设置在
// Go 1.27 或更高版本中可能被移除。
func NewTimer(d Duration) *Timer {
	c := make(chan Time, 1)
	t := newTimer(when(d), 0, sendTime, c, syncTimer(c))
	t.C = c
	return t
}

// Reset 将 timer 改为在时长 d 后到期。
// 如果 timer 此前处于活动状态，则返回 true；如果 timer 已
// 到期或已被停止，则返回 false。
//
// 对于使用 [AfterFunc](d, f) 创建的基于函数的 timer，Reset 要么重新安排
// f 的运行时间（此时 Reset 返回 true），要么安排 f
// 再次运行（此时返回 false）。
// 当 Reset 返回 false 时，Reset 既不会在返回前等待先前的 f
// 完成，也不保证随后运行 f 的
// goroutine 不会与先前那个并发运行。
// 如果调用者需要知道 f 先前的执行
// 是否已完成，必须与 f 显式协调。
//
// 对于使用 NewTimer 创建的基于 chan 的 timer，从 Go 1.23 起，
// 在 Reset 返回后从 t.C 进行的任何接收都保证不会
// 接收到与先前 timer 设置对应的时间值；
// 如果程序尚未从 t.C 接收过，并且 timer 正在
// 运行，则 Reset 保证返回 true。
// 在 Go 1.23 之前，使用 Reset 的唯一安全做法是先调用 [Timer.Stop]
// 并显式排空 timer。
// 详情参见 [NewTimer] 文档。
func (t *Timer) Reset(d Duration) bool {
	if !t.initTimer {
		panic("time: Reset called on uninitialized Timer")
	}
	w := when(d)
	return resetTimer(t, w, 0)
}

// sendTime 在 c 上对当前时间执行非阻塞发送。
func sendTime(c any, seq uintptr, delta int64) {
	// delta 表示 channel 发送本应发生的时间距今多久。
	// 当前时间可以被任意推迟到未来，因为运行时
	// 可以把 sendTime 调用延迟到某个 goroutine 尝试从
	// channel 接收时。减去 delta 以回到我们过去
	// 用于发送的旧时间。
	select {
	case c.(chan Time) <- Now().Add(Duration(-delta)):
	default:
	}
}

// After 等待时长过去，然后在返回的 channel 上发送当前时间。
// 它等价于 [NewTimer](d).C。
//
// 在 Go 1.23 之前，本文档警告说，底层
// [Timer] 在 timer 触发之前不会被垃圾回收器回收，
// 并且如果关心效率，代码应改用
// NewTimer，并在不再需要该 timer 时调用 [Timer.Stop]。
// 从 Go 1.23 起，垃圾回收器可以回收未被引用的、
// 未被停止的 timer。当 After 可以满足需求时，没有理由更偏好 NewTimer。
func After(d Duration) <-chan Time {
	return NewTimer(d).C
}

// AfterFunc 等待时长过去，然后在它自己的 goroutine 中调用 f。
// 它返回一个 [Timer]，可以使用其 Stop 方法
// 来取消该调用。
// 返回的 Timer 的 C 字段未被使用，将为 nil。
func AfterFunc(d Duration, f func()) *Timer {
	return newTimer(when(d), 0, goFunc, f, nil)
}

func goFunc(arg any, seq uintptr, delta int64) {
	go arg.(func())()
}
