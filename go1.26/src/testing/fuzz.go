// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testing

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

func initFuzzFlags() {
	matchFuzz = flag.String("test.fuzz", "", "run the fuzz test matching `regexp`")
	flag.Var(&fuzzDuration, "test.fuzztime", "time to spend fuzzing; default is to run indefinitely")
	flag.Var(&minimizeDuration, "test.fuzzminimizetime", "time to spend minimizing a value after finding a failing input")

	fuzzCacheDir = flag.String("test.fuzzcachedir", "", "directory where interesting fuzzing inputs are stored (for use only by cmd/go)")
	isFuzzWorker = flag.Bool("test.fuzzworker", false, "coordinate with the parent process to fuzz random values (for use only by cmd/go)")
}

var (
	matchFuzz        *string
	fuzzDuration     durationOrCountFlag
	minimizeDuration = durationOrCountFlag{d: 60 * time.Second, allowZero: true}
	fuzzCacheDir     *string
	isFuzzWorker     *bool

	// corpusDir 是包内模糊测试种子语料库的父目录。
	corpusDir = "testdata/fuzz"
)

// fuzzWorkerExitCode 在模糊测试工作进程发生内部错误后用作退出码。
// 它用于区分内部错误与不受控的 panic 及其他失败。
// 需与 internal/fuzz.workerExitCode 保持一致。
const fuzzWorkerExitCode = 70

// InternalFuzzTarget 是一个内部类型，但由于需要跨包使用而导出；
// 它是 "go test" 命令实现的一部分。
type InternalFuzzTarget struct {
	Name string
	Fn   func(f *F)
}

// F 是传递给模糊测试的类型。
//
// 模糊测试会针对提供的模糊测试目标运行生成的输入，从而发现并报告
// 被测代码中潜在的 bug。
//
// 模糊测试默认会运行种子语料库，其中包含由 [F.Add] 提供的条目以及
// testdata/fuzz/<FuzzTestName> 目录中的条目。在完成必要的设置并调用
// [F.Add] 之后，模糊测试必须调用 [F.Fuzz] 来提供模糊测试目标。
// 示例参见 testing 包文档，详情参见 [F.Fuzz] 和 [F.Add]
// 方法的文档。
//
// *F 的方法只能在 [F.Fuzz] 之前调用。一旦测试开始
// 执行模糊测试目标，就只能使用 [*T] 的方法。在 [F.Fuzz] 函数中
// 唯一允许使用的 *F 方法是 [F.Failed] 和 [F.Name]。
type F struct {
	common
	fstate *fuzzState
	tstate *testState

	// inFuzzFn 在模糊测试函数运行时为 true。当 inFuzzFn 为 true 时，
	// 大多数 F 方法都不能被调用。
	inFuzzFn bool

	// corpus 是一组种子语料库条目，通过 F.Add 添加并从 testdata 加载。
	corpus []corpusEntry

	result     fuzzResult
	fuzzCalled bool
}

var _ TB = (*F)(nil)

// corpusEntry 是与 internal/fuzz.CorpusEntry 相同的类型的别名。
// 我们使用类型别名，是因为不想导出该类型，而 testing 又不能
// 导入 internal/fuzz。
type corpusEntry = struct {
	Parent     string
	Path       string
	Data       []byte
	Values     []any
	Generation int
	IsSeed     bool
}

// Helper 将调用它的函数标记为测试辅助函数。
// 在打印文件和行信息时，该函数将被跳过。
// Helper 可以从多个 goroutine 同时调用。
func (f *F) Helper() {
	if f.inFuzzFn {
		panic("testing: f.Helper was called inside the fuzz target, use t.Helper instead")
	}

	// 此处内联了 common.Helper。
	// 如果直接调用它，它会将 F.Helper 标记为辅助函数，
	// 而不是调用者。
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.helperPCs == nil {
		f.helperPCs = make(map[uintptr]struct{})
	}
	// 此处重复 callerName 中的代码，以避免遍历一个栈帧
	var pc [1]uintptr
	n := runtime.Callers(2, pc[:]) // 跳过 runtime.Callers + Helper
	if n == 0 {
		panic("testing: zero callers found")
	}
	if _, found := f.helperPCs[pc[0]]; !found {
		f.helperPCs[pc[0]] = struct{}{}
		f.helperNames = nil // 该 map 将在下次需要时重新创建
	}
}

// Fail 将函数标记为已失败，但会继续执行。
func (f *F) Fail() {
	// (*F).Fail 可能会由 (*T).Fail 调用，这是应当允许的。但是，
	// 不应允许从 (*F).Fuzz 函数内部直接调用 (*F).Fail。
	if f.inFuzzFn {
		panic("testing: f.Fail was called inside the fuzz target, use t.Fail instead")
	}
	f.common.Helper()
	f.common.Fail()
}

// Skipped 报告测试是否被跳过。
func (f *F) Skipped() bool {
	// (*F).Skipped 可能会由 tRunner 调用，这是应当允许的。但是，
	// 不应允许从 (*F).Fuzz 函数内部直接调用 (*F).Skipped。
	if f.inFuzzFn {
		panic("testing: f.Skipped was called inside the fuzz target, use t.Skipped instead")
	}
	f.common.Helper()
	return f.common.Skipped()
}

// Add 会将参数添加到模糊测试的种子语料库中。如果在模糊测试目标之后
// 或在其内部调用，则该调用不执行任何操作，并且 args 必须与模糊测试目标的
// 参数匹配。
func (f *F) Add(args ...any) {
	var values []any
	for i := range args {
		if t := reflect.TypeOf(args[i]); !supportedTypes[t] {
			panic(fmt.Sprintf("testing: unsupported type to Add %v", t))
		}
		values = append(values, args[i])
	}
	f.corpus = append(f.corpus, corpusEntry{Values: values, IsSeed: true, Path: fmt.Sprintf("seed#%d", len(f.corpus))})
}

// supportedTypes 表示所有可被模糊测试支持的类型。
var supportedTypes = map[reflect.Type]bool{
	reflect.TypeFor[[]byte]():  true,
	reflect.TypeFor[string]():  true,
	reflect.TypeFor[bool]():    true,
	reflect.TypeFor[byte]():    true,
	reflect.TypeFor[rune]():    true,
	reflect.TypeFor[float32](): true,
	reflect.TypeFor[float64](): true,
	reflect.TypeFor[int]():     true,
	reflect.TypeFor[int8]():    true,
	reflect.TypeFor[int16]():   true,
	reflect.TypeFor[int32]():   true,
	reflect.TypeFor[int64]():   true,
	reflect.TypeFor[uint]():    true,
	reflect.TypeFor[uint8]():   true,
	reflect.TypeFor[uint16]():  true,
	reflect.TypeFor[uint32]():  true,
	reflect.TypeFor[uint64]():  true,
}

// Fuzz 运行模糊测试函数 ff，用于模糊测试。如果 ff 针对某组参数
// 失败，则这些参数将被添加到种子语料库中。
//
// ff 必须是一个没有返回值的函数，其第一个参数为 [*T]，
// 其余参数为要模糊测试的类型。
// 例如：
//
//	f.Fuzz(func(t *testing.T, b []byte, i int) { ... })
//
// 允许使用以下类型：[]byte、string、bool、byte、rune、float32、
// float64、int、int8、int16、int32、int64、uint、uint8、uint16、uint32、uint64。
// 未来可能会支持更多类型。
//
// ff 不得调用任何 [*F] 方法，例如 [F.Log]、[F.Error]、[F.Skip]。
// 而应使用对应的 [*T] 方法。在 F.Fuzz 函数中唯一允许使用的
// [*F] 方法是 [F.Failed] 和 [F.Name]。
//
// 该函数应当快速且确定，其行为不应依赖共享状态。在模糊测试函数的
// 多次执行之间，不应保留可变的输入参数或指向它们的指针，
// 因为支撑它们的内存可能在后续调用中被修改。ff 不得
// 修改模糊测试引擎所提供参数的底层数据。
//
// 进行模糊测试时，F.Fuzz 会一直运行，直到发现问题、时间耗尽
// （通过 -fuzztime 设置），或测试进程被信号中断。F.Fuzz
// 应当只调用一次，除非事先调用了 [F.Skip] 或 [F.Fail]。
func (f *F) Fuzz(ff any) {
	if f.fuzzCalled {
		panic("testing: F.Fuzz called more than once")
	}
	f.fuzzCalled = true
	if f.failed {
		return
	}
	f.Helper()

	// ff 的形式应为 func(*testing.T, ...interface{})
	fn := reflect.ValueOf(ff)
	fnType := fn.Type()
	if fnType.Kind() != reflect.Func {
		panic("testing: F.Fuzz must receive a function")
	}
	if fnType.NumIn() < 2 || fnType.In(0) != reflect.TypeFor[*T]() {
		panic("testing: fuzz target must receive at least two arguments, where the first argument is a *T")
	}
	if fnType.NumOut() != 0 {
		panic("testing: fuzz target must not return a value")
	}

	// 保存函数的类型，以便与语料库进行比对。
	var types []reflect.Type
	for i := 1; i < fnType.NumIn(); i++ {
		t := fnType.In(i)
		if !supportedTypes[t] {
			panic(fmt.Sprintf("testing: unsupported type for fuzzing %v", t))
		}
		types = append(types, t)
	}

	// 加载 testdata 种子语料库。检查 testdata 语料库中的条目
	// 以及使用 F.Add 声明的条目的类型。
	//
	// 如果当前是工作进程，则不加载种子语料库；我们不会用到它。
	if f.fstate.mode != fuzzWorker {
		for _, c := range f.corpus {
			if err := f.fstate.deps.CheckCorpus(c.Values, types); err != nil {
				// TODO(#48302): 报告 F.Add 调用的源代码位置。
				f.Fatal(err)
			}
		}

		// 加载种子语料库
		c, err := f.fstate.deps.ReadCorpus(filepath.Join(corpusDir, f.name), types)
		if err != nil {
			f.Fatal(err)
		}
		for i := range c {
			c[i].IsSeed = true // 这些都是种子语料库的值
			if f.fstate.mode == fuzzCoordinator {
				// 如果这是协调者进程，则将这些值清零，因为我们不需要
				// 保留它们。
				c[i].Values = nil
			}
		}

		f.corpus = append(f.corpus, c...)
	}

	// run 针对给定输入调用 fn，将其作为拥有自己 T 的子测试。
	// run 类似于 T.Run。测试过滤与清理的工作方式类似。
	// fn 在它自己的 goroutine 中调用。
	run := func(captureOut io.Writer, e corpusEntry) (ok bool) {
		if e.Values == nil {
			// corpusEntry 的 Values 必须非 nil 才能运行测试。
			// 如果 Values 为 nil，则是我们代码中的 bug。
			panic(fmt.Sprintf("corpus file %q was not unmarshaled", e.Path))
		}
		if shouldFailFast() {
			return true
		}
		testName := f.name
		if e.Path != "" {
			testName = fmt.Sprintf("%s/%s", testName, filepath.Base(e.Path))
		}
		if f.tstate.isFuzzing {
			// 模糊测试期间不保留子测试名称。如果 fn 调用 T.Run，
			// 将会产生数量极多且名称重复的子测试，
			// 从而占用大量内存。由于无法确定性地重新运行它们，
			// 这些子测试名称并没有用处。
			f.tstate.match.clearSubNames()
		}

		ctx, cancelCtx := context.WithCancel(f.ctx)

		// 在此调用点记录栈回溯，这样如果子测试函数（它运行在
		// 单独的栈上）被标记为辅助函数，我们就能继续
		// 向上遍历栈进入父测试。
		var pc [maxStackLen]uintptr
		n := runtime.Callers(2, pc[:])
		t := &T{
			common: common{
				barrier:   make(chan bool),
				signal:    make(chan bool),
				name:      testName,
				parent:    &f.common,
				level:     f.level + 1,
				creator:   pc[:n],
				chatty:    f.chatty,
				ctx:       ctx,
				cancelCtx: cancelCtx,
			},
			tstate: f.tstate,
		}
		if captureOut != nil {
			// t.parent 与 f.common 是同一对象。
			t.parent.w = captureOut
		}
		t.w = indenter{&t.common}
		t.setOutputWriter()
		if t.chatty != nil {
			t.chatty.Updatef(t.name, "=== RUN   %s\n", t.name)
		}
		f.common.inFuzzFn, f.inFuzzFn = true, true
		go tRunner(t, func(t *T) {
			args := []reflect.Value{reflect.ValueOf(t)}
			for _, v := range e.Values {
				args = append(args, reflect.ValueOf(v))
			}
			// 在重置当前覆盖率之前，用 defer 安排快照，以确保
			// 它会在 tRunner 函数退出之前被调用，
			// 无论该函数是正常执行完成、发生了 panic，
			// 还是 fuzzFn 调用了 t.Fatal。
			if f.tstate.isFuzzing {
				defer f.fstate.deps.SnapshotCoverage()
				f.fstate.deps.ResetCoverage()
			}
			fn.Call(args)
		})
		<-t.signal
		if t.chatty != nil && t.chatty.json {
			t.chatty.Updatef(t.parent.name, "=== NAME  %s\n", t.parent.name)
		}
		f.common.inFuzzFn, f.inFuzzFn = false, false
		return !t.Failed()
	}

	switch f.fstate.mode {
	case fuzzCoordinator:
		// 模糊测试已启用，且这是由 'go test' 启动的测试进程。
		// 作为协调者进程，协调各工作进程执行
		// 实际的模糊测试。
		corpusTargetDir := filepath.Join(corpusDir, f.name)
		cacheTargetDir := filepath.Join(*fuzzCacheDir, f.name)
		err := f.fstate.deps.CoordinateFuzzing(
			fuzzDuration.d,
			int64(fuzzDuration.n),
			minimizeDuration.d,
			int64(minimizeDuration.n),
			*parallel,
			f.corpus,
			types,
			corpusTargetDir,
			cacheTargetDir)
		if err != nil {
			f.result = fuzzResult{Error: err}
			f.Fail()
			fmt.Fprintf(f.w, "%v\n", err)
			if crashErr, ok := err.(fuzzCrashError); ok {
				crashPath := crashErr.CrashPath()
				fmt.Fprintf(f.w, "Failing input written to %s\n", crashPath)
				testName := filepath.Base(crashPath)
				fmt.Fprintf(f.w, "To re-run:\ngo test -run=%s/%s\n", f.name, testName)
			}
		}
		// TODO(jayconrod,katiehockman): 汇总各工作进程的统计信息，
		// 并添加到 FuzzResult 中（即耗时、迭代次数）

	case fuzzWorker:
		// 模糊测试已启用，且这是一个工作进程。遵循协调者的
		// 指令。
		if err := f.fstate.deps.RunFuzzWorker(func(e corpusEntry) error {
			// 如果从模糊测试工作进程运行，则不要写入 f.w（它指向 Stdout）。
			// 那会变得非常冗长，尤其是在最小化期间。
			// 改为返回错误，让调用者处理输出。
			var buf strings.Builder
			if ok := run(&buf, e); !ok {
				return errors.New(buf.String())
			}
			return nil
		}); err != nil {
			// 内部错误会通过 f.Fail 标记；用户代码也可以在 F.Fuzz 之前调用它。
			// 工作进程将以 fuzzWorkerExitCode 退出，表示这是一次失败
			// （且 'go test' 应以非零状态退出），但不应当记录失败的输入。
			f.Errorf("communicating with fuzzing coordinator: %v", err)
		}

	default:
		// 模糊测试未启用，或将在稍后进行。现在只运行种子
		// 语料库。
		for _, e := range f.corpus {
			name := fmt.Sprintf("%s/%s", f.name, filepath.Base(e.Path))
			if _, ok, _ := f.tstate.match.fullName(nil, name); ok {
				run(f.w, e)
			}
		}
	}
}

func (f *F) report() {
	if *isFuzzWorker || f.parent == nil {
		return
	}
	dstr := fmtDuration(f.duration)
	format := "--- %s: %s (%s)\n"
	if f.Failed() {
		f.flushToParent(f.name, format, "FAIL", f.name, dstr)
	} else if f.chatty != nil {
		if f.Skipped() {
			f.flushToParent(f.name, format, "SKIP", f.name, dstr)
		} else {
			f.flushToParent(f.name, format, "PASS", f.name, dstr)
		}
	}
}

// fuzzResult 包含一次模糊测试运行的结果。
type fuzzResult struct {
	N     int           // 迭代次数。
	T     time.Duration // 总耗时。
	Error error         // Error 是导致失败的输入所产生的错误
}

func (r fuzzResult) String() string {
	if r.Error == nil {
		return ""
	}
	return r.Error.Error()
}

// fuzzCrashError 由模糊测试期间检测到的失败输入来满足。
// 这些错误会被写入种子语料库，并可以用 'go test' 重新运行。
// 模糊测试框架内部的错误（如协调者进程与
// 工作进程之间的 I/O 错误）不满足此接口。
type fuzzCrashError interface {
	error
	Unwrap() error

	// CrashPath 返回与种子语料库中保存的崩溃输入文件相对应的
	// 子测试路径。可以使用 go test -run=$test/$name 重新运行该测试，
	// 其中 $test 是模糊测试名称，$name 是此处
	// 返回字符串的 filepath.Base。
	CrashPath() string
}

// fuzzState 保存所有模糊测试共有的字段。
type fuzzState struct {
	deps testDeps
	mode fuzzMode
}

type fuzzMode uint8

const (
	seedCorpusOnly fuzzMode = iota
	fuzzCoordinator
	fuzzWorker
)

// runFuzzTests 运行与 -run 模式匹配的模糊测试。它只会针对每个种子语料库
// 运行 (*F).Fuzz 函数，而不会使用模糊测试引擎
// 来生成或变异输入。
func runFuzzTests(deps testDeps, fuzzTests []InternalFuzzTarget, deadline time.Time) (ran, ok bool) {
	ok = true
	if len(fuzzTests) == 0 || *isFuzzWorker {
		return ran, ok
	}
	m := newMatcher(deps.MatchString, *match, "-test.run", *skip)
	var mFuzz *matcher
	if *matchFuzz != "" {
		mFuzz = newMatcher(deps.MatchString, *matchFuzz, "-test.fuzz", *skip)
	}

	for _, procs := range cpuList {
		runtime.GOMAXPROCS(procs)
		for i := uint(0); i < *count; i++ {
			if shouldFailFast() {
				break
			}

			tstate := newTestState(*parallel, m)
			tstate.deadline = deadline
			fstate := &fuzzState{deps: deps, mode: seedCorpusOnly}
			root := common{w: os.Stdout} // 将输出集中到一处
			if Verbose() {
				root.chatty = newChattyPrinter(root.w)
			}
			for _, ft := range fuzzTests {
				if shouldFailFast() {
					break
				}
				testName, matched, _ := tstate.match.fullName(nil, ft.Name)
				if !matched {
					continue
				}
				if mFuzz != nil {
					if _, fuzzMatched, _ := mFuzz.fullName(nil, ft.Name); fuzzMatched {
						// 如果此测试将被模糊测试，则现在不要运行种子语料库。
						// 那会在稍后进行。
						continue
					}
				}
				ctx, cancelCtx := context.WithCancel(context.Background())
				f := &F{
					common: common{
						signal:    make(chan bool),
						barrier:   make(chan bool),
						name:      testName,
						parent:    &root,
						level:     root.level + 1,
						chatty:    root.chatty,
						ctx:       ctx,
						cancelCtx: cancelCtx,
					},
					tstate: tstate,
					fstate: fstate,
				}
				f.w = indenter{&f.common}
				f.setOutputWriter()
				if f.chatty != nil {
					f.chatty.Updatef(f.name, "=== RUN   %s\n", f.name)
				}
				go fRunner(f, ft.Fn)
				<-f.signal
				if f.chatty != nil && f.chatty.json {
					f.chatty.Updatef(f.parent.name, "=== NAME  %s\n", f.parent.name)
				}
				ok = ok && !f.Failed()
				ran = ran || f.ran
			}
			if !ran {
				// 本次迭代没有要运行的测试。
				// 这不会改变，因此没有理由继续尝试。
				break
			}
		}
	}

	return ran, ok
}

// runFuzzing 运行与 -fuzz 模式匹配的模糊测试。必须只匹配一个这样的
// 模糊测试。它会运行模糊测试引擎，针对模糊测试目标生成并
// 变异新的输入。
//
// 如果模糊测试被禁用（未设置 -test.fuzz），runFuzzing
// 会立即返回。
func runFuzzing(deps testDeps, fuzzTests []InternalFuzzTarget) (ok bool) {
	if len(fuzzTests) == 0 || *matchFuzz == "" {
		return true
	}
	m := newMatcher(deps.MatchString, *matchFuzz, "-test.fuzz", *skip)
	tstate := newTestState(1, m)
	tstate.isFuzzing = true
	fstate := &fuzzState{
		deps: deps,
	}
	root := common{w: os.Stdout}
	if *isFuzzWorker {
		root.w = io.Discard
		fstate.mode = fuzzWorker
	} else {
		fstate.mode = fuzzCoordinator
	}
	if Verbose() && !*isFuzzWorker {
		root.chatty = newChattyPrinter(root.w)
	}
	var fuzzTest *InternalFuzzTarget
	var testName string
	var matched []string
	for i := range fuzzTests {
		name, ok, _ := tstate.match.fullName(nil, fuzzTests[i].Name)
		if !ok {
			continue
		}
		matched = append(matched, name)
		fuzzTest = &fuzzTests[i]
		testName = name
	}
	if len(matched) == 0 {
		fmt.Fprintln(os.Stderr, "testing: warning: no fuzz tests to fuzz")
		return true
	}
	if len(matched) > 1 {
		fmt.Fprintf(os.Stderr, "testing: will not fuzz, -fuzz matches more than one fuzz test: %v\n", matched)
		return false
	}

	ctx, cancelCtx := context.WithCancel(context.Background())
	f := &F{
		common: common{
			signal:    make(chan bool),
			barrier:   nil, // 模糊测试时 T.Parallel 不起作用。
			name:      testName,
			parent:    &root,
			level:     root.level + 1,
			chatty:    root.chatty,
			ctx:       ctx,
			cancelCtx: cancelCtx,
		},
		fstate: fstate,
		tstate: tstate,
	}
	f.w = indenter{&f.common}
	f.setOutputWriter()
	if f.chatty != nil {
		f.chatty.Updatef(f.name, "=== RUN   %s\n", f.name)
	}
	go fRunner(f, fuzzTest.Fn)
	<-f.signal
	if f.chatty != nil {
		f.chatty.Updatef(f.parent.name, "=== NAME  %s\n", f.parent.name)
	}
	return !f.failed
}

// fRunner 封装对模糊测试的调用，并确保清理函数被调用、
// 状态标志被设置。fRunner 应在它自己的 goroutine 中调用。
// 要等待其完成，请从 f.signal 接收。
//
// fRunner 类似于 tRunner，后者封装用 T.Run 启动的子测试。
// 单元测试和模糊测试的工作方式略有不同，因此目前这两个
// 函数尚未合并。特别是，由于没有 F.Run 和
// F.Parallel 方法，即没有模糊子测试或并行模糊测试，所以做了
// 一些简化。我们还要求必须调用 F.Fuzz、F.Skip 或 F.Fail。
func fRunner(f *F, fn func(*F)) {
	// 当此 goroutine 结束时（无论是调用了 runtime.Goexit、发生了
	// panic，还是 fn 正常返回），记录耗时并发送
	// t.signal，表示模糊测试已完成。
	defer func() {
		// 检测模糊测试是否在未调用 F.Fuzz、F.Fail 或 F.Skip 的情况下
		// 发生 panic 或调用了 runtime.Goexit。如果是，则 panic
		// （可能会替换 nil 的 panic 值）。fRunner 展开之后不应有任何
		// recover，因此这会使进程崩溃并打印栈信息。
		// 遗憾的是，在此处 recover 会增加栈帧，但原始 panic 的
		// 位置应当仍然
		// 清晰。
		f.checkRaces()
		if f.Failed() {
			numFailed.Add(1)
		}
		err := recover()
		if err == nil {
			f.mu.RLock()
			fuzzNotCalled := !f.fuzzCalled && !f.skipped && !f.failed
			if !f.finished && !f.skipped && !f.failed {
				err = errNilPanicOrGoexit
			}
			f.mu.RUnlock()
			if fuzzNotCalled && err == nil {
				f.Error("returned without calling F.Fuzz, F.Fail, or F.Skip")
			}
		}

		// 使用延迟调用来确保即使清理函数调用了 F.FailNow，
		// 我们也能报告测试已完成。参见 issue 41355。
		didPanic := false
		defer func() {
			if !didPanic {
				// 只有在测试未发生 panic 时才报告测试已完成，
				// 否则测试二进制文件可能会在 panic 被报告给用户之前
				// 就退出。参见 issue 41479。
				f.signal <- true
			}
		}()

		// 如果我们恢复了 panic 或不恰当的 runtime.Goexit，则使测试失败，
		// 将输出日志刷新到根节点，然后 panic。
		doPanic := func(err any) {
			f.Fail()
			if r := f.runCleanup(recoverAndReturnPanic); r != nil {
				f.Logf("cleanup panicked with %v", r)
			}
			for root := &f.common; root.parent != nil; root = root.parent {
				root.mu.Lock()
				root.duration += highPrecisionTimeSince(root.start)
				d := root.duration
				root.mu.Unlock()
				root.flushToParent(root.name, "--- FAIL: %s (%s)\n", root.name, fmtDuration(d))
			}
			didPanic = true
			panic(err)
		}
		if err != nil {
			doPanic(err)
		}

		// 没有 panic 或不恰当的 Goexit。
		f.duration += highPrecisionTimeSince(f.start)

		if len(f.sub) > 0 {
			// 解除运行种子语料库期间调用了 T.Parallel 的输入的阻塞。
			// 这只影响作为普通测试运行的模糊测试。
			// 进行模糊测试时，T.Parallel 不起作用，因此 f.sub 为空，
			// 不会进入此分支。此时 f.barrier 为 nil。
			f.tstate.release()
			close(f.barrier)
			// 等待子测试完成。
			for _, sub := range f.sub {
				<-sub.signal
			}
			cleanupStart := highPrecisionTimeNow()
			err := f.runCleanup(recoverAndReturnPanic)
			f.duration += highPrecisionTimeSince(cleanupStart)
			if err != nil {
				doPanic(err)
			}
		}

		// 在所有子测试完成后进行报告。
		f.report()
		f.done = true
		f.setRan()
	}()
	defer func() {
		if len(f.sub) == 0 {
			f.runCleanup(normalPanic)
		}
	}()

	f.start = highPrecisionTimeNow()
	f.resetRaces()
	fn(f)

	// 当调用 FailNow 或 SkipNow 时，此后的代码将不会被执行。
	f.mu.Lock()
	f.finished = true
	f.mu.Unlock()
}
