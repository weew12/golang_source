// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testdeps 提供对测试执行所需依赖的访问。
//
// 该包由生成的主包导入，后者将 TestDeps 传入 testing.Main。这允许测试
// 在运行时使用这些包，而无需使它们成为 testing 包的直接依赖。
// testing 包的直接依赖更难编写测试。
package testdeps

import (
	"bufio"
	"context"
	"internal/fuzz"
	"internal/testlog"
	"io"
	"os"
	"os/signal"
	"reflect"
	"regexp"
	"runtime/pprof"
	"strings"
	"sync"
	"time"
)

// Cover 指示是否启用了覆盖率。
var Cover bool

// TestDeps 是 testing.testDeps 接口的一个实现，
// 适合传给 [testing.MainStart]。
type TestDeps struct{}

var matchPat string
var matchRe *regexp.Regexp

func (TestDeps) MatchString(pat, str string) (result bool, err error) {
	if matchRe == nil || matchPat != pat {
		matchPat = pat
		matchRe, err = regexp.Compile(matchPat)
		if err != nil {
			return
		}
	}
	return matchRe.MatchString(str), nil
}

func (TestDeps) StartCPUProfile(w io.Writer) error {
	return pprof.StartCPUProfile(w)
}

func (TestDeps) StopCPUProfile() {
	pprof.StopCPUProfile()
}

func (TestDeps) WriteProfileTo(name string, w io.Writer, debug int) error {
	return pprof.Lookup(name).WriteTo(w, debug)
}

// ImportPath 是测试二进制文件的导入路径，由生成的主函数设置。
var ImportPath string

func (TestDeps) ImportPath() string {
	return ImportPath
}

var ModulePath string

func (TestDeps) ModulePath() string {
	return ModulePath
}

// testLog 实现 testlog.Interface，记录 os 包的操作。
type testLog struct {
	mu  sync.Mutex
	w   *bufio.Writer
	set bool
}

func (l *testLog) Getenv(key string) {
	l.add("getenv", key)
}

func (l *testLog) Open(name string) {
	l.add("open", name)
}

func (l *testLog) Stat(name string) {
	l.add("stat", name)
}

func (l *testLog) Chdir(name string) {
	l.add("chdir", name)
}

// add 将 (op, name) 对添加到测试日志中。
func (l *testLog) add(op, name string) {
	if strings.Contains(name, "\n") || name == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		return
	}
	l.w.WriteString(op)
	l.w.WriteByte(' ')
	l.w.WriteString(name)
	l.w.WriteByte('\n')
}

var log testLog

func (TestDeps) StartTestLog(w io.Writer) {
	log.mu.Lock()
	log.w = bufio.NewWriter(w)
	if !log.set {
		// 定义 TestMain 然后多次运行 m.Run 的测试
		// 会多次调用 StartTestLog/StopTestLog。
		// 检查 log.set 可避免多次调用 testlog.SetLogger
		// （这会导致 panic），同时也避免多次写入头部。
		log.set = true
		testlog.SetLogger(&log)
		log.w.WriteString("# test log\n") // cmd/go/internal/test/test.go 已知这一点
	}
	log.mu.Unlock()
}

func (TestDeps) StopTestLog() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	err := log.w.Flush()
	log.w = nil
	return err
}

// SetPanicOnExit0 告知 os 包是否在 os.Exit(0) 时 panic。
func (TestDeps) SetPanicOnExit0(v bool) {
	testlog.SetPanicOnExit0(v)
}

func (TestDeps) CoordinateFuzzing(
	timeout time.Duration,
	limit int64,
	minimizeTimeout time.Duration,
	minimizeLimit int64,
	parallel int,
	seed []fuzz.CorpusEntry,
	types []reflect.Type,
	corpusDir,
	cacheDir string) (err error) {
	// Fuzzing 可能因超时或用户按下 ^C 而中断。
	// 无论哪种情况，我们都将优雅地停止工作进程并保存
	// 崩溃用例和有趣的值。
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	err = fuzz.CoordinateFuzzing(ctx, fuzz.CoordinateFuzzingOpts{
		Log:             os.Stderr,
		Timeout:         timeout,
		Limit:           limit,
		MinimizeTimeout: minimizeTimeout,
		MinimizeLimit:   minimizeLimit,
		Parallel:        parallel,
		Seed:            seed,
		Types:           types,
		CorpusDir:       corpusDir,
		CacheDir:        cacheDir,
	})
	if err == ctx.Err() {
		return nil
	}
	return err
}

func (TestDeps) RunFuzzWorker(fn func(fuzz.CorpusEntry) error) error {
	// 当用户按下 ^C 时，工作进程可能会也可能不会收到信号。
	// 在 POSIX 操作系统上，发送给进程组的信号会被投递给该组中的所有进程。
	// Windows 上则不是这样。
	// 如果工作进程被中断，则快速返回且不报错。
	// 如果只有协调进程被中断，它会通过关闭每个工作进程的 "fuzz_in" 管道
	// 来通知它们停止。
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	err := fuzz.RunFuzzWorker(ctx, fn)
	if err == ctx.Err() {
		return nil
	}
	return err
}

func (TestDeps) ReadCorpus(dir string, types []reflect.Type) ([]fuzz.CorpusEntry, error) {
	return fuzz.ReadCorpus(dir, types)
}

func (TestDeps) CheckCorpus(vals []any, types []reflect.Type) error {
	return fuzz.CheckCorpus(vals, types)
}

func (TestDeps) ResetCoverage() {
	fuzz.ResetCoverage()
}

func (TestDeps) SnapshotCoverage() {
	fuzz.SnapshotCoverage()
}

var CoverMode string
var Covered string
var CoverSelectedPackages []string

// 下面这些变量在运行时（通过 testmain 中的代码）设置，指向
// internal/coverage/cfile 包中的等效函数；这样做
// 使我们能够让测试仅在 -cover 生效时导入 internal/coverage/cfile
// （而不是为所有测试都导入）。
var (
	CoverSnapshotFunc           func() float64
	CoverProcessTestDirFunc     func(dir string, cfile string, cm string, cpkg string, w io.Writer, selpkgs []string) error
	CoverMarkProfileEmittedFunc func(val bool)
)

func (TestDeps) InitRuntimeCoverage() (mode string, tearDown func(string, string) (string, error), snapcov func() float64) {
	if CoverMode == "" {
		return
	}
	return CoverMode, coverTearDown, CoverSnapshotFunc
}

func coverTearDown(coverprofile string, gocoverdir string) (string, error) {
	var err error
	if gocoverdir == "" {
		gocoverdir, err = os.MkdirTemp("", "gocoverdir")
		if err != nil {
			return "error setting GOCOVERDIR: bad os.MkdirTemp return", err
		}
		defer os.RemoveAll(gocoverdir)
	}
	CoverMarkProfileEmittedFunc(true)
	cmode := CoverMode
	if err := CoverProcessTestDirFunc(gocoverdir, coverprofile, cmode, Covered, os.Stdout, CoverSelectedPackages); err != nil {
		return "error generating coverage report", err
	}
	return "", nil
}
