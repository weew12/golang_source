// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package expvar 提供了访问公开变量（例如服务器中的操作计数器）的标准接口。
// 它以 JSON 格式通过 HTTP 在 /debug/vars 上暴露这些变量。从 Go 1.22 起，
// 对 /debug/vars 的请求必须使用 GET。
//
// 对这些公开变量的设置或修改操作是原子的。
//
// 除了添加 HTTP 处理器之外，本包还会注册以下变量：
//
//	cmdline   os.Args
//	memstats  runtime.Memstats
//
// 有时导入本包只是为了获得注册其 HTTP 处理器和上述变量的副作用。
// 若要以这种方式使用它，请将该包链接到你的程序中：
//
//	import _ "expvar"
package expvar

import (
	"encoding/json"
	"internal/godebug"
	"log"
	"math"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// Var 是所有导出变量的抽象类型。
type Var interface {
	// String 返回该变量的合法 JSON 值。
	// String 方法不返回合法 JSON 的类型
	// （例如 time.Time）不得用作 Var。
	String() string
}

type jsonVar interface {
	// appendJSON 将接收者的 JSON 表示追加到 b。
	appendJSON(b []byte) []byte
}

// Int 是一个满足 [Var] 接口的 64 位整数变量。
type Int struct {
	i atomic.Int64
}

func (v *Int) Value() int64 {
	return v.i.Load()
}

func (v *Int) String() string {
	return string(v.appendJSON(nil))
}

func (v *Int) appendJSON(b []byte) []byte {
	return strconv.AppendInt(b, v.i.Load(), 10)
}

func (v *Int) Add(delta int64) {
	v.i.Add(delta)
}

func (v *Int) Set(value int64) {
	v.i.Store(value)
}

// Float 是一个满足 [Var] 接口的 64 位浮点数变量。
type Float struct {
	f atomic.Uint64
}

func (v *Float) Value() float64 {
	return math.Float64frombits(v.f.Load())
}

func (v *Float) String() string {
	return string(v.appendJSON(nil))
}

func (v *Float) appendJSON(b []byte) []byte {
	return strconv.AppendFloat(b, math.Float64frombits(v.f.Load()), 'g', -1, 64)
}

// Add 将 delta 加到 v 上。
func (v *Float) Add(delta float64) {
	for {
		cur := v.f.Load()
		curVal := math.Float64frombits(cur)
		nxtVal := curVal + delta
		nxt := math.Float64bits(nxtVal)
		if v.f.CompareAndSwap(cur, nxt) {
			return
		}
	}
}

// Set 将 v 设置为 value。
func (v *Float) Set(value float64) {
	v.f.Store(math.Float64bits(value))
}

// Map 是一个满足 [Var] 接口的 string 到 Var 的映射变量。
type Map struct {
	m      sync.Map // map[string]Var
	keysMu sync.RWMutex
	keys   []string // 已排序
}

// KeyValue 表示 [Map] 中的单个条目。
type KeyValue struct {
	Key   string
	Value Var
}

func (v *Map) String() string {
	return string(v.appendJSON(nil))
}

func (v *Map) appendJSON(b []byte) []byte {
	return v.appendJSONMayExpand(b, false)
}

func (v *Map) appendJSONMayExpand(b []byte, expand bool) []byte {
	afterCommaDelim := byte(' ')
	mayAppendNewline := func(b []byte) []byte { return b }
	if expand {
		afterCommaDelim = '\n'
		mayAppendNewline = func(b []byte) []byte { return append(b, '\n') }
	}

	b = append(b, '{')
	b = mayAppendNewline(b)
	first := true
	v.Do(func(kv KeyValue) {
		if !first {
			b = append(b, ',', afterCommaDelim)
		}
		first = false
		b = appendJSONQuote(b, kv.Key)
		b = append(b, ':', ' ')
		switch v := kv.Value.(type) {
		case nil:
			b = append(b, "null"...)
		case jsonVar:
			b = v.appendJSON(b)
		default:
			b = append(b, v.String()...)
		}
	})
	b = mayAppendNewline(b)
	b = append(b, '}')
	b = mayAppendNewline(b)
	return b
}

// Init 移除映射中的所有键。
func (v *Map) Init() *Map {
	v.keysMu.Lock()
	defer v.keysMu.Unlock()
	v.keys = v.keys[:0]
	v.m.Clear()
	return v
}

// addKey 更新 v.keys 中已排序的键列表。
func (v *Map) addKey(key string) {
	v.keysMu.Lock()
	defer v.keysMu.Unlock()
	// 使用插入排序将 key 放入已排序的 v.keys。
	i, found := slices.BinarySearch(v.keys, key)
	if found {
		return
	}
	v.keys = slices.Insert(v.keys, i, key)
}

func (v *Map) Get(key string) Var {
	i, _ := v.m.Load(key)
	av, _ := i.(Var)
	return av
}

func (v *Map) Set(key string, av Var) {
	// 在存储值之前，先检查该键是否为新的。在 LoadOrStore 之前先尝试 Load：
	// LoadOrStore 即使在 Load 路径上也会导致键接口逃逸。
	if _, ok := v.m.Load(key); !ok {
		if _, dup := v.m.LoadOrStore(key, av); !dup {
			v.addKey(key)
			return
		}
	}

	v.m.Store(key, av)
}

// Add 将 delta 加到存储在给定映射键下的 *[Int] 值上。
func (v *Map) Add(key string, delta int64) {
	i, ok := v.m.Load(key)
	if !ok {
		var dup bool
		i, dup = v.m.LoadOrStore(key, new(Int))
		if !dup {
			v.addKey(key)
		}
	}

	// 加到 Int 上；否则忽略。
	if iv, ok := i.(*Int); ok {
		iv.Add(delta)
	}
}

// AddFloat 将 delta 加到存储在给定映射键下的 *[Float] 值上。
func (v *Map) AddFloat(key string, delta float64) {
	i, ok := v.m.Load(key)
	if !ok {
		var dup bool
		i, dup = v.m.LoadOrStore(key, new(Float))
		if !dup {
			v.addKey(key)
		}
	}

	// 加到 Float 上；否则忽略。
	if iv, ok := i.(*Float); ok {
		iv.Add(delta)
	}
}

// Delete 从映射中删除给定的键。
func (v *Map) Delete(key string) {
	v.keysMu.Lock()
	defer v.keysMu.Unlock()
	i, found := slices.BinarySearch(v.keys, key)
	if found {
		v.keys = slices.Delete(v.keys, i, i+1)
		v.m.Delete(key)
	}
}

// Do 对映射中的每个条目调用 f。
// 迭代期间映射会被锁定，
// 但已存在的条目可能会被并发更新。
func (v *Map) Do(f func(KeyValue)) {
	v.keysMu.RLock()
	defer v.keysMu.RUnlock()
	for _, k := range v.keys {
		i, _ := v.m.Load(k)
		val, _ := i.(Var)
		f(KeyValue{k, val})
	}
}

// String 是一个字符串变量，并且满足 [Var] 接口。
type String struct {
	s atomic.Value // string
}

func (v *String) Value() string {
	p, _ := v.s.Load().(string)
	return p
}

// String 实现 [Var] 接口。若要获取未加引号的字符串，
// 请使用 [String.Value]。
func (v *String) String() string {
	return string(v.appendJSON(nil))
}

func (v *String) appendJSON(b []byte) []byte {
	return appendJSONQuote(b, v.Value())
}

func (v *String) Set(value string) {
	v.s.Store(value)
}

// Func 通过调用该函数并使用 JSON 格式化其返回值
// 来实现 [Var]。
type Func func() any

func (f Func) Value() any {
	return f()
}

func (f Func) String() string {
	v, _ := json.Marshal(f())
	return string(v)
}

// 所有已发布的变量。
var vars Map

// Publish 声明一个具名的导出变量。应当在包的 init 函数中创建其
// Var 时调用。若该名称已被注册，则会触发 log.Panic。
func Publish(name string, v Var) {
	if _, dup := vars.m.LoadOrStore(name, v); dup {
		log.Panicln("Reuse of exported var name:", name)
	}
	vars.keysMu.Lock()
	defer vars.keysMu.Unlock()
	vars.keys = append(vars.keys, name)
	slices.Sort(vars.keys)
}

// Get 获取一个具名的导出变量。若该名称尚未注册，
// 则返回 nil。
func Get(name string) Var {
	return vars.Get(name)
}

// 用于创建新的导出变量的便捷函数。

func NewInt(name string) *Int {
	v := new(Int)
	Publish(name, v)
	return v
}

func NewFloat(name string) *Float {
	v := new(Float)
	Publish(name, v)
	return v
}

func NewMap(name string) *Map {
	v := new(Map).Init()
	Publish(name, v)
	return v
}

func NewString(name string) *String {
	v := new(String)
	Publish(name, v)
	return v
}

// Do 对每个导出变量调用 f。
// 迭代期间全局变量映射会被锁定，
// 但已存在的条目可能会被并发更新。
func Do(f func(KeyValue)) {
	vars.Do(f)
}

func expvarHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(vars.appendJSONMayExpand(nil, true))
}

// Handler 返回 expvar 的 HTTP Handler。
//
// 仅在需要将处理器安装到非标准位置时才需要它。
func Handler() http.Handler {
	return http.HandlerFunc(expvarHandler)
}

func cmdline() any {
	return os.Args
}

func memstats() any {
	stats := new(runtime.MemStats)
	runtime.ReadMemStats(stats)
	return *stats
}

func init() {
	if godebug.New("httpmuxgo121").Value() == "1" {
		http.HandleFunc("/debug/vars", expvarHandler)
	} else {
		http.HandleFunc("GET /debug/vars", expvarHandler)
	}
	Publish("cmdline", Func(cmdline))
	Publish("memstats", Func(memstats))
}

// TODO: 改用 json.appendString。
func appendJSONQuote(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	for _, r := range s {
		switch {
		case r < ' ' || r == '\\' || r == '"' || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
			switch r {
			case '\\', '"':
				b = append(b, '\\', byte(r))
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '\\', 'u', hex[(r>>12)&0xf], hex[(r>>8)&0xf], hex[(r>>4)&0xf], hex[(r>>0)&0xf])
			}
		case r < utf8.RuneSelf:
			b = append(b, byte(r))
		default:
			b = utf8.AppendRune(b, r)
		}
	}
	b = append(b, '"')
	return b
}
