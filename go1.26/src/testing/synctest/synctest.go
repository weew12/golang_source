// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// synctest 包为测试并发代码提供支持。
//
// [Test] 函数在一个隔离的“气泡”中运行一个函数。
// 在气泡内启动的任何 goroutine 也是该气泡的一部分。
//
// 每个测试都应完全自包含：
// 以下准则应适用于大多数测试：
//
//   - 避免与并非从测试内部启动的 goroutine 交互。
//   - 避免使用网络。按需使用假的网络实现。
//   - 避免与外部进程交互。
//   - 避免在后台任务中泄漏 goroutine。
//
// # 时间
//
// 在气泡内，[time] 包使用假时钟。
// 每个气泡都有自己的时钟。
// 初始时间为 UTC 2000-01-01 午夜。
//
// 只有当气泡中的每个 goroutine 都处于持久阻塞状态时，
// 气泡内的时间才会推进。
// “持久阻塞”的确切定义见下文。
//
// 例如，下面这个测试会立即运行，而不会耗时
// 两秒：
//
//	func TestTime(t *testing.T) {
//		synctest.Test(t, func(t *testing.T) {
//			start := time.Now() // 始终为 UTC 2000-01-01 午夜
//			go func() {
//				time.Sleep(1 * time.Second)
//				t.Log(time.Since(start)) // 始终记录 "1s"
//			}()
//			time.Sleep(2 * time.Second) // 上面的 goroutine 会在此 Sleep 返回之前运行
//			t.Log(time.Since(start))    // 始终记录 "2s"
//		})
//	}
//
// 当气泡的根 goroutine 退出时，时间停止推进。
//
// # 阻塞
//
// 当气泡中的某个 goroutine 处于阻塞状态，
// 且只能被同一气泡中的另一个 goroutine 解除阻塞时，
// 它处于“持久阻塞”状态。
// 能够被来自其气泡外部的事件解除阻塞的 goroutine 不属于持久阻塞。
//
// [Wait] 函数会阻塞，直到气泡中的所有其他 goroutine
// 都处于持久阻塞状态。
//
// 例如：
//
//	func TestWait(t *testing.T) {
//		synctest.Test(t, func(t *testing.T) {
//			done := false
//			go func() {
//				done = true
//			}()
//			// Wait 会阻塞，直到上面的 goroutine 执行完毕。
//			synctest.Wait()
//			t.Log(done) // 始终记录 "true"
//		})
//	}
//
// 当气泡中的每个 goroutine 都处于持久阻塞状态时：
//
//   - 如果 [Wait] 已被调用，则它会返回。
//   - 否则，时间会推进到下一个能够解除至少一个 goroutine 阻塞的时刻，
//     前提是存在这样的时刻，并且气泡的根 goroutine 尚未退出。
//   - 否则，出现死锁，[Test] 会 panic。
//
// 以下操作会持久阻塞一个 goroutine：
//
//   - 在气泡内创建的 channel 上进行阻塞式发送或接收
//   - 阻塞式 select 语句，其中每个 case 都是
//     在气泡内创建的 channel
//   - [sync.Cond.Wait]
//   - [sync.WaitGroup.Wait]，当 [sync.WaitGroup.Add] 在气泡内被调用时
//   - [time.Sleep]
//
// 不在上述列表中的操作不属于持久阻塞。
// 特别地，以下操作可能会阻塞一个 goroutine，
// 但不属于持久阻塞，因为该 goroutine 可以被
// 在其气泡外部发生的事件解除阻塞：
//
//   - 锁定 [sync.Mutex] 或 [sync.RWMutex]
//   - 阻塞于 I/O，例如从网络套接字读取
//   - 系统调用
//
// # 隔离
//
// 在气泡内创建的 channel、[time.Timer] 或 [time.Ticker]
// 会与该气泡关联。从气泡外部操作与气泡关联的 channel、timer 或
// ticker 会 panic。
//
// [sync.WaitGroup] 在首次调用 Add 或 Go 时会与
// 某个气泡关联。一旦 WaitGroup 与某个气泡关联，
// 从该气泡外部调用 Add 或 Go 就是致命错误。
// （作为一项技术限制，定义为包变量的 WaitGroup，
// 例如 "var wg sync.WaitGroup"，无法与气泡关联，
// 对其执行的操作可能不属于持久阻塞。
// 此限制不适用于存储在包变量中的 *WaitGroup，
// 例如 "var wg = new(sync.WaitGroup)"。）
//
// [sync.Cond.Wait] 属于持久阻塞。从气泡外部唤醒气泡中
// 阻塞于 Cond.Wait 的 goroutine 是致命错误。
//
// 通过 [runtime.AddCleanup] 和 [runtime.SetFinalizer]
// 注册的清理函数和终结器
// 在任何气泡之外运行。
//
// # 示例：Context.AfterFunc
//
// 此示例演示如何测试 [context.AfterFunc] 函数。
//
// AfterFunc 注册一个函数，在 context 被取消后
// 于新的 goroutine 中执行。
//
// 该测试验证该函数在 context 被取消之前不会运行，
// 并在 context 被取消之后运行。
//
//	func TestContextAfterFunc(t *testing.T) {
//		synctest.Test(t, func(t *testing.T) {
//			// 创建一个可以被取消的 context.Context。
//			ctx, cancel := context.WithCancel(t.Context())
//
//			// context.AfterFunc 注册一个在 context 被取消时
//			// 被调用的函数。
//			afterFuncCalled := false
//			context.AfterFunc(ctx, func() {
//				afterFuncCalled = true
//			})
//
//			// context 尚未被取消，因此 AfterFunc 未被调用。
//			synctest.Wait()
//			if afterFuncCalled {
//				t.Fatalf("before context is canceled: AfterFunc called")
//			}
//
//			// 取消 context 并等待 AfterFunc 执行完毕。
//			// 验证 AfterFunc 已运行。
//			cancel()
//			synctest.Wait()
//			if !afterFuncCalled {
//				t.Fatalf("after context is canceled: AfterFunc not called")
//			}
//		})
//	}
//
// # 示例：Context.WithTimeout
//
// 此示例演示如何测试 [context.WithTimeout] 函数。
//
// WithTimeout 创建一个在超时后被取消的 context。
//
// 该测试验证 context 在超时到期之前不会被取消，
// 并在超时到期之后被取消。
//
//	func TestContextWithTimeout(t *testing.T) {
//		synctest.Test(t, func(t *testing.T) {
//			// 创建一个在超时后被取消的 context.Context。
//			const timeout = 5 * time.Second
//			ctx, cancel := context.WithTimeout(t.Context(), timeout)
//			defer cancel()
//
//			// 等待略少于超时的时间。
//			time.Sleep(timeout - time.Nanosecond)
//			synctest.Wait()
//			if err := ctx.Err(); err != nil {
//				t.Fatalf("before timeout: ctx.Err() = %v, want nil\n", err)
//			}
//
//			// 等待剩余的时间直到超时。
//			time.Sleep(time.Nanosecond)
//			synctest.Wait()
//			if err := ctx.Err(); err != context.DeadlineExceeded {
//				t.Fatalf("after timeout: ctx.Err() = %v, want DeadlineExceeded\n", err)
//			}
//		})
//	}
//
// # 示例：HTTP 100 Continue
//
// 此示例演示如何测试 [http.Transport] 的 100 Continue 处理。
//
// 发送请求的 HTTP 客户端可以包含一个 "Expect: 100-continue" 头，
// 以告知服务器客户端还有额外数据要发送。
// 服务器随后可以响应一个 100 Continue 信息响应
// 来请求该数据，或者用其他状态码告知客户端不需要该数据。
// 例如，上传大文件的客户端可能会使用此特性，
// 在发送文件之前确认服务器愿意接收该文件。
//
// 该测试确认：在发送 "Expect: 100-continue" 头时，
// HTTP 客户端在服务器请求之前不会发送请求的内容，
// 并且在收到 100 Continue 响应之后确实会发送该内容。
//
//	func TestHTTPTransport100Continue(t *testing.T) {
//		synctest.Test(t, func(*testing.T) {
//			// 创建一个进程内的假网络连接。
//			// 我们无法为此测试使用环回网络连接，
//			// 因为阻塞于网络 I/O 的 goroutine 会阻止 synctest
//			// 气泡进入空闲状态。
//			srvConn, cliConn := net.Pipe()
//			defer cliConn.Close()
//			defer srvConn.Close()
//
//			tr := &http.Transport{
//				// 使用上面创建的假网络连接。
//				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
//					return cliConn, nil
//				},
//				// 启用 "Expect: 100-continue" 处理。
//				ExpectContinueTimeout: 5 * time.Second,
//			}
//
//			// 发送一个设置了 "Expect: 100-continue" 头的请求。
//			// 在一个新的 goroutine 中发送它，因为它要到测试结束才会完成。
//			body := "request body"
//			go func() {
//				req, _ := http.NewRequest("PUT", "http://test.tld/", strings.NewReader(body))
//				req.Header.Set("Expect", "100-continue")
//				resp, err := tr.RoundTrip(req)
//				if err != nil {
//					t.Errorf("RoundTrip: unexpected error %v\n", err)
//				} else {
//					resp.Body.Close()
//				}
//			}()
//
//			// 读取客户端发送的请求头。
//			req, err := http.ReadRequest(bufio.NewReader(srvConn))
//			if err != nil {
//				t.Fatalf("ReadRequest: %v\n", err)
//			}
//
//			// 启动一个新的 goroutine，将客户端发送的正文复制到缓冲区中。
//			// 等待气泡中的所有 goroutine 阻塞，并验证我们尚未
//			// 从客户端读取到任何内容。
//			var gotBody bytes.Buffer
//			go io.Copy(&gotBody, req.Body)
//			synctest.Wait()
//			if got, want := gotBody.String(), ""; got != want {
//				t.Fatalf("before sending 100 Continue, read body: %q, want %q\n", got, want)
//			}
//
//			// 向客户端写入一个 "100 Continue" 响应，并验证
//			// 它发送了请求正文。
//			srvConn.Write([]byte("HTTP/1.1 100 Continue\r\n\r\n"))
//			synctest.Wait()
//			if got, want := gotBody.String(), body; got != want {
//				t.Fatalf("after sending 100 Continue, read body: %q, want %q\n", got, want)
//			}
//
//			// 最后发送 "200 OK" 响应以结束该请求。
//			srvConn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
//
//			// 我们在测试期间启动了多个 goroutine。
//			// synctest.Test 调用会等待它们全部退出后再返回。
//		})
//	}
package synctest

import (
	"internal/synctest"
	"testing"
	_ "unsafe" // for linkname
)

// Test 在一个新的气泡中执行 f。
//
// Test 会等待气泡中的所有 goroutine 退出后再返回。
// 如果气泡中的 goroutine 出现死锁，测试就会失败。
//
// 不得从气泡内部调用 Test。
//
// 提供给 f 的 [*testing.T] 具有以下特性：
//
//   - T.Cleanup 函数在气泡内运行，
//     紧接在 Test 返回之前。
//   - T.Context 返回一个带有与气泡关联的
//     Done channel 的 [context.Context]。
//   - 不得调用 T.Run、T.Parallel 和 T.Deadline。
func Test(t *testing.T, f func(*testing.T)) {
	var ok bool
	synctest.Run(func() {
		ok = testingSynctestTest(t, f)
	})
	if !ok {
		// 在气泡外使测试失败，
		// 这样测试时长就会使用真实时间设置。
		t.FailNow()
	}
}

//go:linkname testingSynctestTest testing/synctest.testingSynctestTest
func testingSynctestTest(t *testing.T, f func(*testing.T)) bool

// Wait 会阻塞，直到当前气泡中
// 除当前 goroutine 之外的每个 goroutine 都处于持久阻塞状态。
//
// 不得从气泡外部调用 Wait。
// 不得由同一气泡中的多个 goroutine
// 并发调用 Wait。
func Wait() {
	synctest.Wait()
}
