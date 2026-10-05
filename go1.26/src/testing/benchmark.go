// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"context"
	"flag"
	"fmt"
	"internal/sysinfo"
	"io"
	"math"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

func initBenchmarkFlags() {
	matchBenchmarks = flag.String("test.bench", "", "run only benchmarks matching `regexp`")
	benchmarkMemory = flag.Bool("test.benchmem", false, "print memory allocations for benchmarks")
	flag.Var(&benchTime, "test.benchtime", "run each benchmark for duration `d` or N times if `d` is of the form Nx")
}

var (
	matchBenchmarks *string
	benchmarkMemory *bool

	benchTime = durationOrCountFlag{d: 1 * time.Second} // 在 testing 包的测试期间会更改
)

type durationOrCountFlag struct {
	d         time.Duration
	n         int
	allowZero bool
}

func (f *durationOrCountFlag) String() string {
	if f.n > 0 {
		return fmt.Sprintf("%dx", f.n)
	}
	return f.d.String()
}

func (f *durationOrCountFlag) Set(s string) error {
	if strings.HasSuffix(s, "x") {
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 0)
		if err != nil || n < 0 || (!f.allowZero && n == 0) {
			return fmt.Errorf("invalid count")
		}
		*f = durationOrCountFlag{n: int(n)}
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 || (!f.allowZero && d == 0) {
		return fmt.Errorf("invalid duration")
	}
	*f = durationOrCountFlag{d: d}
	return nil
}

// 全局锁，确保同一时间只有一个基准测试在运行。
var benchmarkLock sync.Mutex

// 用于每个基准测试以测量内存。
var memStats runtime.MemStats

// InternalBenchmark 是一个内部类型，但因其跨包使用而被导出；
// 它是 "go test" 命令实现的一部分。
type InternalBenchmark struct {
	Name string
	F    func(b *B)
}

// B 是传递给 [Benchmark] 函数的类型，用于管理基准测试的
// 计时并控制迭代次数。
//
// 当 Benchmark 函数返回或调用 [B.FailNow]、[B.Fatal]、[B.Fatalf]、
// [B.SkipNow]、[B.Skip] 或 [B.Skipf] 中的任一方法时，基准测试结束。
// 这些方法只能从运行 Benchmark 函数的 goroutine 中调用。
// 其他报告方法，例如 [B.Log] 和 [B.Error] 的各种变体，
// 可以从多个 goroutine 同时调用。
//
// 与测试类似，基准测试日志在执行期间累积，
// 并在结束时转储到标准输出。与测试不同的是，基准测试日志
// 总是会打印出来，以免隐藏那些可能正在影响基准测试结果的输出。
type B struct {
	common
	importPath       string // 包含该基准测试的包的导入路径
	bstate           *benchState
	N                int
	previousN        int           // 上一次运行中的迭代次数
	previousDuration time.Duration // 上一次运行的总耗时
	benchFunc        func(b *B)
	benchTime        durationOrCountFlag
	bytes            int64
	missingBytes     bool // 其中一个子基准测试未设置字节数。
	timerOn          bool
	showAllocResult  bool
	result           BenchmarkResult
	parallelism      int // RunParallel 创建 parallelism*GOMAXPROCS 个 goroutine
	// memStats.Mallocs 和 memStats.TotalAlloc 的初始状态。
	startAllocs uint64
	startBytes  uint64
	// 本次测试运行后的净总量。
	netAllocs uint64
	netBytes  uint64
	// ReportMetric 收集的额外指标。
	extra map[string]float64

	// loop 跟踪 B.Loop 的状态
	loop struct {
		// n 是目标迭代次数。它会随着执行而增加。
		// 当基准测试循环完成时，我们将其提交给 b.N，以便用户
		// 可以据此进行报告，但在此之前我们避免将其暴露出来。
		n uint64
		// i 是当前 Loop 的迭代次数。它严格单调递增
		// 趋近于 n。
		//
		// 最高位用于污染 Loop 的快速路径，使其回退到
		// 慢速路径。
		i uint64

		done bool // 当 B.Loop 返回 false 时设置
	}
}

// StartTimer 开始为测试计时。该函数会在基准测试开始前
// 自动调用，但也可用于在调用 [B.StopTimer] 后恢复计时。
func (b *B) StartTimer() {
	if !b.timerOn {
		runtime.ReadMemStats(&memStats)
		b.startAllocs = memStats.Mallocs
		b.startBytes = memStats.TotalAlloc
		b.start = highPrecisionTimeNow()
		b.timerOn = true
		b.loop.i &^= loopPoisonTimer
	}
}

// StopTimer 停止为测试计时。可在执行你不想测量的步骤时
// 用它来暂停计时器。
func (b *B) StopTimer() {
	if b.timerOn {
		b.duration += highPrecisionTimeSince(b.start)
		runtime.ReadMemStats(&memStats)
		b.netAllocs += memStats.Mallocs - b.startAllocs
		b.netBytes += memStats.TotalAlloc - b.startBytes
		b.timerOn = false
		// 如果计时器停止时调用 B.Loop，则失败。
		b.loop.i |= loopPoisonTimer
	}
}

// ResetTimer 将已流逝的基准测试时间和内存分配计数器清零，
// 并删除用户报告的指标。
// 它不影响计时器是否正在运行。
func (b *B) ResetTimer() {
	if b.extra == nil {
		// 在读取内存统计信息之前分配 extra map。
		// 预先设定其大小以减少再次分配的可能性。
		b.extra = make(map[string]float64, 16)
	} else {
		clear(b.extra)
	}
	if b.timerOn {
		runtime.ReadMemStats(&memStats)
		b.startAllocs = memStats.Mallocs
		b.startBytes = memStats.TotalAlloc
		b.start = highPrecisionTimeNow()
	}
	b.duration = 0
	b.netAllocs = 0
	b.netBytes = 0
}

// SetBytes 记录单次操作处理的字节数。
// 如果调用此方法，基准测试将报告 ns/op 和 MB/s。
func (b *B) SetBytes(n int64) { b.bytes = n }

// ReportAllocs 为该基准测试启用内存分配统计。
// 它等价于设置 -test.benchmem，但只影响调用
// ReportAllocs 的那个基准测试函数。
func (b *B) ReportAllocs() {
	b.showAllocResult = true
}

// runN 以指定的迭代次数运行单个基准测试。
func (b *B) runN(n int) {
	benchmarkLock.Lock()
	defer benchmarkLock.Unlock()
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer func() {
		b.runCleanup(normalPanic)
		b.checkRaces()
	}()
	// 通过清除上一次运行的垃圾来尽量为每次运行
	// 获得可比较的环境。
	runtime.GC()
	b.resetRaces()
	b.N = n
	b.loop.n = 0
	b.loop.i = 0
	b.loop.done = false
	b.ctx = ctx
	b.cancelCtx = cancelCtx

	b.parallelism = 1
	b.ResetTimer()
	b.StartTimer()
	b.benchFunc(b)
	b.StopTimer()
	b.previousN = n
	b.previousDuration = b.duration

	if b.loop.n > 0 && !b.loop.done && !b.failed {
		b.Error("benchmark function returned without B.Loop() == false (break or return in loop?)")
	}
}

// run1 运行 benchFunc 的第一次迭代。它报告是否应
// 继续运行该基准测试的更多迭代。
func (b *B) run1() bool {
	if bstate := b.bstate; bstate != nil {
		// 如有需要，扩展 maxLen。
		if n := len(b.name) + bstate.extLen + 1; n > bstate.maxLen {
			bstate.maxLen = n + 8 // 增加额外余量，以避免尺寸跳跃过于频繁。
		}
	}
	go func() {
		// 无论我们是正常返回还是经由 FailNow 的 runtime.Goexit 退出，
		// 都发出完成信号。
		defer func() {
			b.signal <- true
		}()

		b.runN(1)
	}()
	<-b.signal
	if b.failed {
		fmt.Fprintf(b.w, "%s--- FAIL: %s\n%s", b.chatty.prefix(), b.name, b.output)
		return false
	}
	// 只有在我们确定不会继续时才打印输出。
	// 否则会在 processBench 中打印。
	b.mu.RLock()
	finished := b.finished
	b.mu.RUnlock()
	if b.hasSub.Load() || finished {
		tag := "BENCH"
		if b.skipped {
			tag = "SKIP"
		}
		if b.chatty != nil && (len(b.output) > 0 || finished) {
			b.trimOutput()
			fmt.Fprintf(b.w, "%s--- %s: %s\n%s", b.chatty.prefix(), tag, b.name, b.output)
		}
		return false
	}
	return true
}

var labelsOnce sync.Once

// run 在单独的 goroutine 中执行基准测试，包括其所有
// 子基准测试。b 必须没有子基准测试。
func (b *B) run() {
	labelsOnce.Do(func() {
		fmt.Fprintf(b.w, "goos: %s\n", runtime.GOOS)
		fmt.Fprintf(b.w, "goarch: %s\n", runtime.GOARCH)
		if b.importPath != "" {
			fmt.Fprintf(b.w, "pkg: %s\n", b.importPath)
		}
		if cpu := sysinfo.CPUName(); cpu != "" {
			fmt.Fprintf(b.w, "cpu: %s\n", cpu)
		}
	})
	if b.bstate != nil {
		// 正在运行 go test --test.bench
		b.bstate.processBench(b) // 必须调用 doBench。
	} else {
		// 正在运行 func Benchmark。
		b.doBench()
	}
}

func (b *B) doBench() BenchmarkResult {
	go b.launch()
	<-b.signal
	return b.result
}

// 运行次数不要超过 1e9 次。（这也使 n 在 32 位平台上保持在 int 范围内。）
const maxBenchPredictIters = 1_000_000_000

func predictN(goalns int64, prevIters int64, prevns int64, last int64) int {
	if prevns == 0 {
		// 向上取整以避免除以零。参见 https://go.dev/issue/70709。
		prevns = 1
	}

	// 运算顺序很重要。
	// 对于非常快的基准测试，prevIters ~= prevns。
	// 如果先做除法，会得到 0 或 1，
	// 这可能隐藏一个数量级的执行时间差异。
	// 因此先做乘法，再做除法。
	n := goalns * prevIters / prevns
	// 运行比我们认为所需更多的迭代次数（1.2 倍）。
	n += n / 5
	// 增长不要太快，以防之前出现过计时误差。
	n = min(n, 100*last)
	// 确保至少比上次多运行一次。
	n = max(n, last+1)
	// 运行次数不要超过 1e9 次。（这也使 n 在 32 位平台上保持在 int 范围内。）
	n = min(n, maxBenchPredictIters)
	return int(n)
}

// launch 启动基准测试函数。它会逐步增加基准测试的
// 迭代次数，直到基准测试运行达到所请求的 benchtime。
// launch 由 doBench 函数作为单独的 goroutine 运行。
// 必须先对 b 调用过 run1。
func (b *B) launch() {
	// 无论我们是正常返回还是经由 FailNow 的 runtime.Goexit 退出，
	// 都发出完成信号。
	defer func() {
		b.signal <- true
	}()

	// b.Loop 有自己的渐进逻辑，所以我们只需运行它一次。
	// 如果 b.loop.n 非零，说明 b.Loop 已经运行过。
	if b.loop.n == 0 {
		// 基准测试至少运行指定的时间量。
		if b.benchTime.n > 0 {
			// 我们已经在 run1 中运行了一次迭代。
			// 如果请求的是 -benchtime=1x，则使用该结果。
			// 参见 https://golang.org/issue/32051。
			if b.benchTime.n > 1 {
				b.runN(b.benchTime.n)
			}
		} else {
			d := b.benchTime.d
			for n := int64(1); !b.failed && b.duration < d && n < 1e9; {
				last := n
				// 预测所需的迭代次数。
				goalns := d.Nanoseconds()
				prevIters := int64(b.N)
				n = int64(predictN(goalns, prevIters, b.duration.Nanoseconds(), last))
				b.runN(int(n))
			}
		}
	}
	b.result = BenchmarkResult{b.N, b.duration, b.bytes, b.netAllocs, b.netBytes, b.extra}
}

// Elapsed 返回该基准测试测得的已流逝时间。
// Elapsed 报告的时长与 [B.StartTimer]、[B.StopTimer] 和
// [B.ResetTimer] 所测量的时长一致。
func (b *B) Elapsed() time.Duration {
	d := b.duration
	if b.timerOn {
		d += highPrecisionTimeSince(b.start)
	}
	return d
}

// ReportMetric 将 "n unit" 添加到报告的基准测试结果中。
// 如果该指标是按次迭代的，调用方应除以 b.N，
// 按照惯例，单位应以 "/op" 结尾。
// ReportMetric 会覆盖同一单位之前报告的任何值。
// 如果 unit 为空字符串或包含任何空白字符，
// ReportMetric 会 panic。
// 如果 unit 是基准测试框架本身通常报告的单位
// （例如 "allocs/op"），ReportMetric 将覆盖该指标。
// 将 "ns/op" 设为 0 将抑制该内置指标。
func (b *B) ReportMetric(n float64, unit string) {
	if unit == "" {
		panic("metric unit must not be empty")
	}
	if strings.IndexFunc(unit, unicode.IsSpace) >= 0 {
		panic("metric unit must not contain whitespace")
	}
	b.extra[unit] = n
}

func (b *B) stopOrScaleBLoop() bool {
	t := b.Elapsed()
	if t >= b.benchTime.d {
		// 我们已达到目标
		return false
	}
	// 循环缩放
	goalns := b.benchTime.d.Nanoseconds()
	prevIters := int64(b.loop.n)
	b.loop.n = uint64(predictN(goalns, prevIters, t.Nanoseconds(), prevIters))
	if b.loop.n&loopPoisonMask != 0 {
		// 迭代次数不应达到这么高，但如果真达到了，
		// 我们就会有大麻烦。
		panic("loop iteration target overflow")
	}
	// predictN 可能已对迭代次数设置了上限；如果已达该上限，
	// 确保终止。
	return uint64(prevIters) < b.loop.n
}

func (b *B) loopSlowPath() bool {
	// 一致性检查
	if !b.timerOn {
		b.Fatal("B.Loop called with timer stopped")
	}
	if b.loop.i&loopPoisonMask != 0 {
		panic(fmt.Sprintf("unknown loop stop condition: %#x", b.loop.i))
	}

	if b.loop.n == 0 {
		// 这是基准测试函数中对 b.Loop() 的第一次调用。
		if b.benchTime.n > 0 {
			// 固定迭代次数。
			b.loop.n = uint64(b.benchTime.n)
		} else {
			// 将目标初始化为 1，以启动循环缩放。
			b.loop.n = 1
		}
		// 在 b.Loop 循环内部，我们不使用 b.N（以避免混淆）。
		b.N = 0
		b.ResetTimer()

		// 开始下一次迭代。
		b.loop.i++
		return true
	}

	// 我们是否应继续迭代？
	var more bool
	if b.benchTime.n > 0 {
		// 迭代次数是固定的，所以我们应该已经运行了这么多次，
		// 现在完成了。
		if b.loop.i != uint64(b.benchTime.n) {
			// 在这种情况下我们不应能进入慢速路径。
			panic(fmt.Sprintf("iteration count %d < fixed target %d", b.loop.i, b.benchTime.n))
		}
		more = false
	} else {
		// 处理固定时间的情况
		more = b.stopOrScaleBLoop()
	}
	if !more {
		b.StopTimer()
		// 提交迭代次数
		b.N = int(b.loop.n)
		b.loop.done = true
		return false
	}

	// 开始下一次迭代。
	b.loop.i++
	return true
}

// 只要基准测试应继续运行，Loop 就返回 true。
//
// 一个典型的基准测试结构如下：
//
//	func Benchmark(b *testing.B) {
//		... setup ...
//		for b.Loop() {
//			... code to measure ...
//		}
//		... cleanup ...
//	}
//
// Loop 在基准测试中第一次被调用时会重置基准测试计时器，
// 因此在开始基准测试循环之前执行的任何设置都不会计入
// 基准测试的测量。同样，当它返回 false 时，它会停止
// 计时器，因此清理代码不会被测量。
//
// 在 "for b.Loop() { ... }" 循环体内，循环内函数调用的参数和
// 结果以及被赋值的变量会保持存活，从而阻止编译器完全
// 优化掉循环体。
// 目前，这是通过一个编译器变换实现的，它用
// runtime.KeepAlive 内建调用包装这类变量。这仅适用于
// 语法上位于循环花括号之间的语句，并且循环
// 条件必须完全写作 "b.Loop()"。
//
// 在 Loop 返回 false 之后，b.N 包含已运行的总迭代次数，
// 因此基准测试可以使用 b.N 来计算其他平均指标。
//
// 在引入 Loop 之前，基准测试被期望包含一个
// 从 0 到 b.N 的显式循环。基准测试应使用 Loop 或包含一个
// 到 b.N 的循环，但不能同时使用两者。Loop 提供了对基准测试
// 计时器更自动化的管理，并且每次测量只运行一次基准测试函数，
// 而基于 b.N 的基准测试必须多次运行基准测试函数（以及任何
// 相关的设置和清理）。
func (b *B) Loop() bool {
	// 这里这样编写是为了让快速路径尽可能快并且可以
	// 内联。
	//
	// 有三种情况会让我们离开快速路径：
	//
	// - 在第一次调用时，i 和 n 都为 0。
	//
	// - 如果循环到达第 n 次迭代，那么 i == n，我们需要
	//   确定新的目标迭代次数或判断是否已完成。
	//
	// - 如果计时器停止，它会污染 i 的最高位，以便慢速
	//   路径可以进行一致性检查并失败。
	if b.loop.i < b.loop.n {
		b.loop.i++
		return true
	}
	return b.loopSlowPath()
}

// loopPoison 常量可以通过按位或运算合并进 B.loop.i，使其回退
// 到慢速路径。
const (
	loopPoisonTimer = uint64(1 << (63 - iota))
	// 如有必要，在此处添加更多 poison 位。

	// loopPoisonMask 是所有 loop poison 位的集合。(iota-1) 是我们刚刚设置的位的索引，
	// 我们据此重建该位掩码。我们减去
	// 1 以设置该位以下的所有位，然后对结果取反
	// 得到掩码。抱歉，但并不抱歉。
	loopPoisonMask = ^uint64((1 << (63 - (iota - 1))) - 1)
)

// BenchmarkResult 包含一次基准测试运行的结果。
type BenchmarkResult struct {
	N         int           // 迭代次数。
	T         time.Duration // 总耗时。
	Bytes     int64         // 单次迭代处理的字节数。
	MemAllocs uint64        // 内存分配总次数。
	MemBytes  uint64        // 分配的总字节数。

	// Extra 记录由 ReportMetric 报告的额外指标。
	Extra map[string]float64
}

// NsPerOp 返回 "ns/op" 指标。
func (r BenchmarkResult) NsPerOp() int64 {
	if v, ok := r.Extra["ns/op"]; ok {
		return int64(v)
	}
	if r.N <= 0 {
		return 0
	}
	return r.T.Nanoseconds() / int64(r.N)
}

// mbPerSec 返回 "MB/s" 指标。
func (r BenchmarkResult) mbPerSec() float64 {
	if v, ok := r.Extra["MB/s"]; ok {
		return v
	}
	if r.Bytes <= 0 || r.T <= 0 || r.N <= 0 {
		return 0
	}
	return (float64(r.Bytes) * float64(r.N) / 1e6) / r.T.Seconds()
}

// AllocsPerOp 返回 "allocs/op" 指标，
// 其计算方式为 r.MemAllocs / r.N。
func (r BenchmarkResult) AllocsPerOp() int64 {
	if v, ok := r.Extra["allocs/op"]; ok {
		return int64(v)
	}
	if r.N <= 0 {
		return 0
	}
	return int64(r.MemAllocs) / int64(r.N)
}

// AllocedBytesPerOp 返回 "B/op" 指标，
// 其计算方式为 r.MemBytes / r.N。
func (r BenchmarkResult) AllocedBytesPerOp() int64 {
	if v, ok := r.Extra["B/op"]; ok {
		return int64(v)
	}
	if r.N <= 0 {
		return 0
	}
	return int64(r.MemBytes) / int64(r.N)
}

// String 返回基准测试结果的摘要。
// 它遵循 https://golang.org/design/14313-benchmark-format 中的
// 基准测试结果行格式，不包括
// 基准测试名称。
// 额外指标会覆盖同名的内置指标。
// String 不包含 allocs/op 或 B/op，因为它们由
// [BenchmarkResult.MemString] 报告。
func (r BenchmarkResult) String() string {
	buf := new(strings.Builder)
	fmt.Fprintf(buf, "%8d", r.N)

	// 以浮点数形式获取 ns/op。
	ns, ok := r.Extra["ns/op"]
	if !ok {
		ns = float64(r.T.Nanoseconds()) / float64(r.N)
	}
	if ns != 0 {
		buf.WriteByte('\t')
		prettyPrint(buf, ns, "ns/op")
	}

	if mbs := r.mbPerSec(); mbs != 0 {
		fmt.Fprintf(buf, "\t%7.2f MB/s", mbs)
	}

	// 打印未在标准指标中体现的
	// 额外指标。
	var extraKeys []string
	for k := range r.Extra {
		switch k {
		case "ns/op", "MB/s", "B/op", "allocs/op":
			// 在别处报告的内置指标。
			continue
		}
		extraKeys = append(extraKeys, k)
	}
	slices.Sort(extraKeys)
	for _, k := range extraKeys {
		buf.WriteByte('\t')
		prettyPrint(buf, r.Extra[k], k)
	}
	return buf.String()
}

func prettyPrint(w io.Writer, x float64, unit string) {
	// 将所有数字的小数点前部分打印为 10 位，
	// 小数字则打印为四位有效数字。字段宽度
	// 的选择是为了让整数部分占 10 位，同时对齐
	// 所有小数格式的小数点。
	var format string
	switch y := math.Abs(x); {
	case y == 0 || y >= 999.95:
		format = "%10.0f %s"
	case y >= 99.995:
		format = "%12.1f %s"
	case y >= 9.9995:
		format = "%13.2f %s"
	case y >= 0.99995:
		format = "%14.3f %s"
	case y >= 0.099995:
		format = "%15.4f %s"
	case y >= 0.0099995:
		format = "%16.5f %s"
	case y >= 0.00099995:
		format = "%17.6f %s"
	default:
		format = "%18.7f %s"
	}
	fmt.Fprintf(w, format, x, unit)
}

// MemString 以与 'go test' 相同的格式返回 r.AllocedBytesPerOp 和 r.AllocsPerOp。
func (r BenchmarkResult) MemString() string {
	return fmt.Sprintf("%8d B/op\t%8d allocs/op",
		r.AllocedBytesPerOp(), r.AllocsPerOp())
}

// benchmarkName 返回基准测试的全名，包括 procs 后缀。
func benchmarkName(name string, n int) string {
	if n != 1 {
		return fmt.Sprintf("%s-%d", name, n)
	}
	return name
}

type benchState struct {
	match *matcher

	maxLen int // 记录的最大基准测试名称。
	extLen int // 最大扩展长度。
}

// RunBenchmarks 是一个内部函数，但因其跨包使用而被导出；
// 它是 "go test" 命令实现的一部分。
func RunBenchmarks(matchString func(pat, str string) (bool, error), benchmarks []InternalBenchmark) {
	runBenchmarks("", matchString, benchmarks)
}

func runBenchmarks(importPath string, matchString func(pat, str string) (bool, error), benchmarks []InternalBenchmark) bool {
	// 如果未指定标志，则不运行基准测试。
	if len(*matchBenchmarks) == 0 {
		return true
	}
	// 收集匹配的基准测试并确定最长的名称。
	maxprocs := 1
	for _, procs := range cpuList {
		if procs > maxprocs {
			maxprocs = procs
		}
	}
	bstate := &benchState{
		match:  newMatcher(matchString, *matchBenchmarks, "-test.bench", *skip),
		extLen: len(benchmarkName("", maxprocs)),
	}
	var bs []InternalBenchmark
	for _, Benchmark := range benchmarks {
		if _, matched, _ := bstate.match.fullName(nil, Benchmark.Name); matched {
			bs = append(bs, Benchmark)
			benchName := benchmarkName(Benchmark.Name, maxprocs)
			if l := len(benchName) + bstate.extLen + 1; l > bstate.maxLen {
				bstate.maxLen = l
			}
		}
	}
	main := &B{
		common: common{
			name:  "Main",
			w:     os.Stdout,
			bench: true,
		},
		importPath: importPath,
		benchFunc: func(b *B) {
			for _, Benchmark := range bs {
				b.Run(Benchmark.Name, Benchmark.F)
			}
		},
		benchTime: benchTime,
		bstate:    bstate,
	}
	if Verbose() {
		main.chatty = newChattyPrinter(main.w)
	}
	main.runN(1)
	return !main.failed
}

// processBench 针对配置的 CPU 数量运行基准测试 b 并打印结果。
func (s *benchState) processBench(b *B) {
	for i, procs := range cpuList {
		for j := uint(0); j < *count; j++ {
			runtime.GOMAXPROCS(procs)
			benchName := benchmarkName(b.name, procs)

			// 如果是 chatty 模式，我们已经打印过此信息。
			if b.chatty == nil {
				fmt.Fprintf(b.w, "%-*s\t", s.maxLen, benchName)
			}
			// 对除第一次迭代之外的所有迭代重新计算运行时间。
			if i > 0 || j > 0 {
				b = &B{
					common: common{
						signal: make(chan bool),
						name:   b.name,
						w:      b.w,
						chatty: b.chatty,
						bench:  true,
					},
					benchFunc: b.benchFunc,
					benchTime: b.benchTime,
				}
				b.setOutputWriter()
				b.run1()
			}
			r := b.doBench()
			if b.failed {
				// 此处的输出可能非常长，但通常不会。
				// 无论如何我们都会全部打印，因为我们不想截断
				// 基准测试失败的原因。
				fmt.Fprintf(b.w, "%s--- FAIL: %s\n%s", b.chatty.prefix(), benchName, b.output)
				continue
			}
			results := r.String()
			if b.chatty != nil {
				fmt.Fprintf(b.w, "%-*s\t", s.maxLen, benchName)
			}
			if *benchmarkMemory || b.showAllocResult {
				results += "\t" + r.MemString()
			}
			fmt.Fprintln(b.w, results)
			// 与测试不同，我们忽略 -chatty 标志，总是打印基准测试的输出，
			// 因为生成输出的时间会使结果产生偏差。
			if len(b.output) > 0 {
				b.trimOutput()
				fmt.Fprintf(b.w, "%s--- BENCH: %s\n%s", b.chatty.prefix(), benchName, b.output)
			}
			if p := runtime.GOMAXPROCS(-1); p != procs {
				fmt.Fprintf(os.Stderr, "testing: %s left GOMAXPROCS set to %d\n", benchName, p)
			}
			if b.chatty != nil && b.chatty.json {
				b.chatty.Updatef("", "=== NAME  %s\n", "")
			}
		}
	}
}

// 如果 hideStdoutForTesting 为 true，Run 不会打印 benchName。
// 这避免了在 testing 包自身运行 'go test' 时产生多余的打印，
// 因为它会在自己的测试中调用 b.Run（参见 sub_test.go）。
var hideStdoutForTesting = false

// Run 以给定名称将基准测试 f 作为子基准测试运行。它报告
// 是否存在任何失败。
//
// 子基准测试与任何其他基准测试类似。至少调用过一次 Run 的
// 基准测试自身不会被测量，并且会以 N=1 调用一次。
func (b *B) Run(name string, f func(b *B)) bool {
	// 由于 b 有子基准测试，我们将不再把它本身作为基准测试来运行。
	// 释放锁并在退出时重新获取，以确保锁的配对。
	b.hasSub.Store(true)
	benchmarkLock.Unlock()
	defer benchmarkLock.Lock()

	benchName, ok, partial := b.name, true, false
	if b.bstate != nil {
		benchName, ok, partial = b.bstate.match.fullName(&b.common, name)
	}
	if !ok {
		return true
	}
	var pc [maxStackLen]uintptr
	n := runtime.Callers(2, pc[:])
	sub := &B{
		common: common{
			signal:  make(chan bool),
			name:    benchName,
			parent:  &b.common,
			level:   b.level + 1,
			creator: pc[:n],
			w:       b.w,
			chatty:  b.chatty,
			bench:   true,
		},
		importPath: b.importPath,
		benchFunc:  f,
		benchTime:  b.benchTime,
		bstate:     b.bstate,
	}
	sub.setOutputWriter()
	if partial {
		// 部分名称匹配，例如 -bench=X/Y 匹配 BenchmarkX。
		// 仅处理子基准测试（如果有）。
		sub.hasSub.Store(true)
	}

	if b.chatty != nil {
		labelsOnce.Do(func() {
			fmt.Printf("goos: %s\n", runtime.GOOS)
			fmt.Printf("goarch: %s\n", runtime.GOARCH)
			if b.importPath != "" {
				fmt.Printf("pkg: %s\n", b.importPath)
			}
			if cpu := sysinfo.CPUName(); cpu != "" {
				fmt.Printf("cpu: %s\n", cpu)
			}
		})

		if !hideStdoutForTesting {
			if b.chatty.json {
				b.chatty.Updatef(benchName, "=== RUN   %s\n", benchName)
			}
			fmt.Println(benchName)
		}
	}

	if sub.run1() {
		sub.run()
	}
	b.add(sub.result)
	return !sub.failed
}

// add 模拟在单次迭代中按顺序运行基准测试。它用于在
// func Benchmark 与 Run 结合使用时给出一些有意义的结果。
func (b *B) add(other BenchmarkResult) {
	r := &b.result
	// 聚合后的 BenchmarkResult 类似于在单个基准测试中按顺序运行
	// 所有子基准测试。
	r.N = 1
	r.T += time.Duration(other.NsPerOp())
	if other.Bytes == 0 {
		// 如果并非所有子基准测试都设置了 Bytes，那么在聚合中
		// 对其求和就没有意义。
		b.missingBytes = true
		r.Bytes = 0
	}
	if !b.missingBytes {
		r.Bytes += other.Bytes
	}
	r.MemAllocs += uint64(other.AllocsPerOp())
	r.MemBytes += uint64(other.AllocedBytesPerOp())
}

// trimOutput 缩短基准测试的输出，输出可能非常长。
func (b *B) trimOutput() {
	// 由于基准测试会运行多次，输出可能会出现多次，
	// 但至少它会被看到。这没什么大不了的，
	// 因为基准测试很少打印，但以防万一，如果太长我们就截断它。
	const maxNewlines = 10
	for nlCount, j := 0, 0; j < len(b.output); j++ {
		if b.output[j] == '\n' {
			nlCount++
			if nlCount >= maxNewlines {
				b.output = append(b.output[:j], "\n\t... [output truncated]\n"...)
				break
			}
		}
	}
}

// PB 由 RunParallel 用于运行并行基准测试。
type PB struct {
	globalN *atomic.Uint64 // 在所有工作 goroutine 之间共享的迭代计数器
	grain   uint64         // 一次从 globalN 获取这么多次迭代
	cache   uint64         // 已获取迭代的本地缓存
	bN      uint64         // 要执行的迭代总次数（b.N）
}

// Next 报告是否还有更多迭代要执行。
func (pb *PB) Next() bool {
	if pb.cache == 0 {
		n := pb.globalN.Add(pb.grain)
		if n <= pb.bN {
			pb.cache = pb.grain
		} else if n < pb.bN+pb.grain {
			pb.cache = pb.bN + pb.grain - n
		} else {
			return false
		}
	}
	pb.cache--
	return true
}

// RunParallel 并行运行基准测试。
// 它创建多个 goroutine，并将 b.N 次迭代分配给它们。
// goroutine 的数量默认为 GOMAXPROCS。要增加非 CPU 密集型
// 基准测试的并行度，可在 RunParallel 之前调用 [B.SetParallelism]。
// RunParallel 通常与 go test -cpu 标志一起使用。
//
// body 函数将在每个 goroutine 中运行。它应设置任何
// goroutine 局部的状态，然后迭代直到 pb.Next 返回 false。
// 它不应使用 [B.StartTimer]、[B.StopTimer] 或 [B.ResetTimer] 函数，
// 因为它们具有全局效果。它也不应调用 [B.Run]。
//
// RunParallel 报告的 ns/op 值是整个基准测试的墙钟时间，
// 而不是各个并行 goroutine 的墙钟时间或 CPU 时间之和。
func (b *B) RunParallel(body func(*PB)) {
	if b.N == 0 {
		return // 探测时无需做任何事。
	}
	// 将 grain 大小计算为耗时约 100µs 的迭代次数。
	// 100µs 足以摊销开销并提供足够的
	// 动态负载均衡。
	grain := uint64(0)
	if b.previousN > 0 && b.previousDuration > 0 {
		grain = 1e5 * uint64(b.previousN) / uint64(b.previousDuration)
	}
	if grain < 1 {
		grain = 1
	}
	// 我们预计内层循环和函数调用至少需要 10ns，
	// 因此迭代次数不要超过 100µs/10ns=1e4。
	if grain > 1e4 {
		grain = 1e4
	}

	var n atomic.Uint64
	numProcs := b.parallelism * runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	wg.Add(numProcs)
	for p := 0; p < numProcs; p++ {
		go func() {
			defer wg.Done()
			pb := &PB{
				globalN: &n,
				grain:   grain,
				bN:      uint64(b.N),
			}
			body(pb)
		}()
	}
	wg.Wait()
	if n.Load() <= uint64(b.N) && !b.Failed() {
		b.Fatal("RunParallel: body exited without pb.Next() == false")
	}
}

// SetParallelism 将 [B.RunParallel] 使用的 goroutine 数量设置为 p*GOMAXPROCS。
// 对于 CPU 密集型基准测试，通常无需调用 SetParallelism。
// 如果 p 小于 1，此调用将不起作用。
func (b *B) SetParallelism(p int) {
	if p >= 1 {
		b.parallelism = p
	}
}

// Benchmark 对单个函数进行基准测试。它对于创建不使用
// "go test" 命令的自定义基准测试很有用。
//
// 如果 f 依赖于 testing 标志，则必须在调用 Benchmark 之前
// 以及在调用 [flag.Parse] 之前使用 [Init] 注册这些标志。
//
// 如果 f 调用 Run，结果将是对在单个基准测试中按顺序运行
// 其所有不调用 Run 的子基准测试的估计。
func Benchmark(f func(b *B)) BenchmarkResult {
	b := &B{
		common: common{
			signal: make(chan bool),
			w:      discard{},
		},
		benchFunc: f,
		benchTime: benchTime,
	}
	b.setOutputWriter()
	if b.run1() {
		b.run()
	}
	return b.result
}

type discard struct{}

func (discard) Write(b []byte) (n int, err error) { return len(b), nil }
