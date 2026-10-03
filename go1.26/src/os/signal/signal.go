// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package signal

import (
	"context"
	"os"
	"slices"
	"sync"
)

var handlers struct {
	sync.Mutex
	// 将通道映射到应发送给它的信号。
	m map[chan<- os.Signal]*handler
	// 将信号映射到接收它的通道数量。
	ref [numSig]int64
	// 在通道正在被停止时，将通道映射到信号。
	// 这不是一个 map，因为条目在这里存在的时间非常短暂。
	// 我们需要一个单独的容器，因为我们必须让 m 始终与 ref 对应，
	// 而且我们还需要跟踪正在被停止的通道的 *handler 值。
	// 参见 Stop 函数。
	stopping []stopping
}

type stopping struct {
	c chan<- os.Signal
	h *handler
}

type handler struct {
	mask [(numSig + 31) / 32]uint32
}

func (h *handler) want(sig int) bool {
	return (h.mask[sig/32]>>uint(sig&31))&1 != 0
}

func (h *handler) set(sig int) {
	h.mask[sig/32] |= 1 << uint(sig&31)
}

func (h *handler) clear(sig int) {
	h.mask[sig/32] &^= 1 << uint(sig&31)
}

// 停止将信号 sigs 中继到任何先前注册以接收它们的通道，并将信号处理程序
// 重置为其原始值（action=disableSignal）或忽略这些信号
// （action=ignoreSignal）。
func cancel(sigs []os.Signal, action func(int)) {
	handlers.Lock()
	defer handlers.Unlock()

	remove := func(n int) {
		var zerohandler handler

		for c, h := range handlers.m {
			if h.want(n) {
				handlers.ref[n]--
				h.clear(n)
				if h.mask == zerohandler.mask {
					delete(handlers.m, c)
				}
			}
		}

		action(n)
	}

	if len(sigs) == 0 {
		for n := 0; n < numSig; n++ {
			remove(n)
		}
	} else {
		for _, s := range sigs {
			remove(signum(s))
		}
	}
}

// Ignore 使所提供的信号被忽略。如果程序收到它们，不会发生任何事情。
// Ignore 会撤销先前对所提供的信号调用 [Notify] 的效果。
// 如果未提供信号，则所有传入信号都会被忽略。
func Ignore(sig ...os.Signal) {
	cancel(sig, ignoreSignal)
}

// Ignored 报告 sig 当前是否被忽略。
func Ignored(sig os.Signal) bool {
	sn := signum(sig)
	return sn >= 0 && signalIgnored(sn)
}

var (
	// watchSignalLoopOnce 保护对有条件初始化的 watchSignalLoop 的调用。
	// 如果 watchSignalLoop 非 nil，它将在 Notify 被调用后延迟地在
	// 一个 goroutine 中运行一次。参见 Issue 21576。
	watchSignalLoopOnce sync.Once
	watchSignalLoop     func()
)

// Notify 使包 signal 将传入信号中继到 c。
// 如果未提供信号，则所有传入信号都会被中继到 c。
// 否则，只有所提供的信号会被中继。
//
// 包 signal 不会阻塞向 c 的发送：调用者必须确保 c 具有足够的缓冲空间，
// 以跟上预期的信号速率。对于仅用于通知一个信号值的通道，大小为 1 的
// 缓冲区就足够了。
//
// 允许使用同一个通道多次调用 Notify：每次调用都会扩展发送到该通道的
// 信号集合。从该集合中移除信号的唯一方法是调用 [Stop]。
//
// 允许使用不同的通道和相同的信号多次调用 Notify：每个通道会独立地
// 接收传入信号的副本。
func Notify(c chan<- os.Signal, sig ...os.Signal) {
	if c == nil {
		panic("os/signal: Notify using nil channel")
	}

	handlers.Lock()
	defer handlers.Unlock()

	h := handlers.m[c]
	if h == nil {
		if handlers.m == nil {
			handlers.m = make(map[chan<- os.Signal]*handler)
		}
		h = new(handler)
		handlers.m[c] = h
	}

	add := func(n int) {
		if n < 0 {
			return
		}
		if !h.want(n) {
			h.set(n)
			if handlers.ref[n] == 0 {
				enableSignal(n)

				// 运行时要求我们在启动监视器之前先启用信号。
				watchSignalLoopOnce.Do(func() {
					if watchSignalLoop != nil {
						go watchSignalLoop()
					}
				})
			}
			handlers.ref[n]++
		}
	}

	if len(sig) == 0 {
		for n := 0; n < numSig; n++ {
			add(n)
		}
	} else {
		for _, s := range sig {
			add(signum(s))
		}
	}
}

// Reset 会撤销先前对所提供的信号调用 [Notify] 的效果。
// 如果未提供信号，则所有信号处理程序都会被重置。
func Reset(sig ...os.Signal) {
	cancel(sig, disableSignal)
}

// Stop 使包 signal 停止将传入信号中继到 c。
// 它会撤销先前使用 c 调用 [Notify] 的所有效果。
// 当 Stop 返回时，可以保证 c 不会再收到任何信号。
func Stop(c chan<- os.Signal) {
	handlers.Lock()

	h := handlers.m[c]
	if h == nil {
		handlers.Unlock()
		return
	}
	delete(handlers.m, c)

	for n := 0; n < numSig; n++ {
		if h.want(n) {
			handlers.ref[n]--
			if handlers.ref[n] == 0 {
				disableSignal(n)
			}
		}
	}

	// 信号将不再被传递到该通道。
	// 我们希望避免对诸如 SIGINT 之类的信号产生竞态：
	// 它要么应被传递到该通道，要么程序应采取默认动作（也就是退出）。
	// 为了避免出现这样的可能性 —— 信号被传递、信号处理程序被调用，
	// 然后 Stop 在下面的 process 函数有机会将其发送到通道之前就注销了
	// 该通道 —— 将该通道放入一个正在被停止的通道列表中，并在完全移除它
	// 之前等待信号传递归于静止。

	handlers.stopping = append(handlers.stopping, stopping{c, h})

	handlers.Unlock()

	signalWaitUntilIdle()

	handlers.Lock()

	for i, s := range handlers.stopping {
		if s.c == c {
			handlers.stopping = slices.Delete(handlers.stopping, i, i+1)
			break
		}
	}

	handlers.Unlock()
}

// 等待直到不再有等待传递的信号。
// 由 runtime 包定义。
func signalWaitUntilIdle()

func process(sig os.Signal) {
	n := signum(sig)
	if n < 0 {
		return
	}

	handlers.Lock()
	defer handlers.Unlock()

	for c, h := range handlers.m {
		if h.want(n) {
			// 发送但不为它阻塞
			select {
			case c <- sig:
			default:
			}
		}
	}

	// 避免 Stop 中提到的竞态。
	for _, d := range handlers.stopping {
		if d.h.want(n) {
			select {
			case d.c <- sig:
			default:
			}
		}
	}
}

// NotifyContext 返回 parent context 的一个副本，当所列信号之一到达时、
// 当返回的 stop 函数被调用时，或者当 parent context 的 Done 通道被关闭时
// （以先发生者为准），该副本会被标记为已完成（其 Done 通道被关闭）。
//
// stop 函数会注销信号行为，与 [signal.Reset] 一样，这可能会恢复给定信号的
// 默认行为。例如，Go 程序收到 [os.Interrupt] 时的默认行为是退出。
// 调用 NotifyContext(parent, os.Interrupt) 会将行为改为取消返回的 context。
// 在调用返回的 stop 函数之前，后续收到的中断不会触发默认（退出）行为。
//
// 如果某个信号导致返回的 context 被取消，对它调用 [context.Cause]
// 会返回一个描述该信号的错误。
//
// stop 函数会释放与其关联的资源，因此代码应在此 Context 中运行的操作完成
// 且不再需要将信号转移到该 context 时，尽快调用 stop。
func NotifyContext(parent context.Context, signals ...os.Signal) (ctx context.Context, stop context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	c := &signalCtx{
		Context: ctx,
		cancel:  cancel,
		signals: signals,
	}
	c.ch = make(chan os.Signal, 1)
	Notify(c.ch, c.signals...)
	if ctx.Err() == nil {
		go func() {
			select {
			case s := <-c.ch:
				c.cancel(signalError(s.String() + " signal received"))
			case <-c.Done():
			}
		}()
	}
	return c, c.stop
}

type signalCtx struct {
	context.Context

	cancel  context.CancelCauseFunc
	signals []os.Signal
	ch      chan os.Signal
}

func (c *signalCtx) stop() {
	c.cancel(nil)
	Stop(c.ch)
}

type stringer interface {
	String() string
}

func (c *signalCtx) String() string {
	var buf []byte
	// 我们知道 c.Context 的类型是 context.cancelCtx，也知道 cancelCtx
	// 的 String 方法返回的字符串以 ".WithCancel" 结尾。
	name := c.Context.(stringer).String()
	name = name[:len(name)-len(".WithCancel")]
	buf = append(buf, "signal.NotifyContext("+name...)
	if len(c.signals) != 0 {
		buf = append(buf, ", ["...)
		for i, s := range c.signals {
			buf = append(buf, s.String()...)
			if i != len(c.signals)-1 {
				buf = append(buf, ' ')
			}
		}
		buf = append(buf, ']')
	}
	buf = append(buf, ')')
	return string(buf)
}

type signalError string

func (s signalError) Error() string {
	return string(s)
}
