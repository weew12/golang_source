// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// Value 可以表示任意 Go 值，但与 any 类型不同，
// 它可以在不分配内存的情况下表示大多数小值。
// 零值 Value 对应于 nil。
type Value struct {
	_ [0]func() // 禁止 ==
	// num 保存 Kind 为 Int64、Uint64、Float64、Bool 和 Duration 时的值，
	// 为 KindString 时的字符串长度，以及为 KindTime 时自纪元以来的纳秒数。
	num uint64
	// 如果 any 的类型是 Kind，则值如上所述保存在 num 中。
	// 如果 any 的类型是 *time.Location，则 Kind 为 Time，可以
	// 根据 num 中的 Unix 纳秒数和该 location 构造 time.Time 值
	// （单调时钟不会被保留）。
	// 如果 any 的类型是 stringptr，则 Kind 为 String，字符串值
	// 由 num 中的长度和 any 中的指针组成。
	// 否则，Kind 为 Any，any 即为该值。
	// （这意味着 Attr 不能存储 Kind、*time.Location
	// 或 stringptr 类型的值。）
	any any
}

type (
	stringptr *byte // 当 Value 是字符串时用于 Value.any
	groupptr  *Attr // 当 Value 是 []Attr 时用于 Value.any
)

// Kind 是 [Value] 的种类。
type Kind int

// 以下列表按字母顺序排序，但同样重要的是
// KindAny 为 0，以便零值 Value 表示 nil。

const (
	KindAny Kind = iota
	KindBool
	KindDuration
	KindFloat64
	KindInt64
	KindString
	KindTime
	KindUint64
	KindGroup
	KindLogValuer
)

var kindStrings = []string{
	"Any",
	"Bool",
	"Duration",
	"Float64",
	"Int64",
	"String",
	"Time",
	"Uint64",
	"Group",
	"LogValuer",
}

func (k Kind) String() string {
	if k >= 0 && int(k) < len(kindStrings) {
		return kindStrings[k]
	}
	return "<unknown slog.Kind>"
}

// Kind 的非导出版本，只是为了能将 Kind 存储在 Value 中。
// （没有用户提供的值具有此类型。）
type kind Kind

// Kind 返回 v 的 Kind。
func (v Value) Kind() Kind {
	switch x := v.any.(type) {
	case Kind:
		return x
	case stringptr:
		return KindString
	case timeLocation, timeTime:
		return KindTime
	case groupptr:
		return KindGroup
	case LogValuer:
		return KindLogValuer
	case kind: // kind 只是 Kind 的一个包装
		return KindAny
	default:
		return KindAny
	}
}

//////////////// 构造函数

// StringValue 返回一个用于字符串的新 [Value]。
func StringValue(value string) Value {
	return Value{num: uint64(len(value)), any: stringptr(unsafe.StringData(value))}
}

// IntValue 返回一个用于 int 的 [Value]。
func IntValue(v int) Value {
	return Int64Value(int64(v))
}

// Int64Value 返回一个用于 int64 的 [Value]。
func Int64Value(v int64) Value {
	return Value{num: uint64(v), any: KindInt64}
}

// Uint64Value 返回一个用于 uint64 的 [Value]。
func Uint64Value(v uint64) Value {
	return Value{num: v, any: KindUint64}
}

// Float64Value 返回一个用于浮点数的 [Value]。
func Float64Value(v float64) Value {
	return Value{num: math.Float64bits(v), any: KindFloat64}
}

// BoolValue 返回一个用于 bool 的 [Value]。
func BoolValue(v bool) Value {
	u := uint64(0)
	if v {
		u = 1
	}
	return Value{num: u, any: KindBool}
}

type (
	// *time.Location 的非导出版本，只是为了能将 *time.Location 存储在
	// Value 中。（没有用户提供的值具有此类型。）
	timeLocation *time.Location

	// timeTime 用于 UnixNano 未定义的时间。
	timeTime time.Time
)

// TimeValue 返回一个用于 [time.Time] 的 [Value]。
// 它会丢弃单调时钟部分。
func TimeValue(v time.Time) Value {
	if v.IsZero() {
		// 零值时间上的 UnixNano 是未定义的，因此改用 nil 的
		// *time.Location 来表示零值时间。time.Time.Location 方法
		// 从不返回 nil，因此 any == timeLocation(nil) 的 Value 不会
		// 被误认为是任何其他 Value、time.Time 或其他值。
		return Value{any: timeLocation(nil)}
	}
	nsec := v.UnixNano()
	t := time.Unix(0, nsec)
	if v.Equal(t) {
		// UnixNano 正确地表示了该时间，因此使用零分配的表示方式。
		return Value{num: uint64(nsec), any: timeLocation(v.Location())}
	}
	// 回退到一般形式。
	// 去除单调时钟部分以匹配另一种表示方式。
	return Value{any: timeTime(v.Round(0))}
}

// DurationValue 返回一个用于 [time.Duration] 的 [Value]。
func DurationValue(v time.Duration) Value {
	return Value{num: uint64(v.Nanoseconds()), any: KindDuration}
}

// GroupValue 返回一个用于 Attr 列表的新 [Value]。
// 调用者之后不得修改参数切片。
func GroupValue(as ...Attr) Value {
	// 移除空分组。
	// 总体而言，在构造时这样做比
	// 递归检查每个 Group 是否为空更简单。
	if n := countEmptyGroups(as); n > 0 {
		as2 := make([]Attr, 0, len(as)-n)
		for _, a := range as {
			if !a.Value.isEmptyGroup() {
				as2 = append(as2, a)
			}
		}
		as = as2
	}
	return Value{num: uint64(len(as)), any: groupptr(unsafe.SliceData(as))}
}

// countEmptyGroups 返回其参数中空分组值的数量。
func countEmptyGroups(as []Attr) int {
	n := 0
	for _, a := range as {
		if a.Value.isEmptyGroup() {
			n++
		}
	}
	return n
}

// AnyValue 返回一个用于所提供值的 [Value]。
//
// 如果所提供的值的类型是 Value，则原样返回它，
// 不做修改。
//
// 对于 Go 预声明的 string、bool 或（非复数）数值类型之一的值，
// AnyValue 返回种类为 [KindString]、[KindBool]、[KindUint64]、[KindInt64]
// 或 [KindFloat64] 的 Value。原始数值类型的宽度不会被保留。
//
// 对于 [time.Time] 或 [time.Duration] 值，AnyValue 返回种类为
// [KindTime] 或 [KindDuration] 的 Value。单调时钟不会被保留。
//
// 对于 nil，或所有其他类型的值（包括底层类型为数值的命名类型），
// AnyValue 返回种类为 [KindAny] 的值。
func AnyValue(v any) Value {
	switch v := v.(type) {
	case string:
		return StringValue(v)
	case int:
		return Int64Value(int64(v))
	case uint:
		return Uint64Value(uint64(v))
	case int64:
		return Int64Value(v)
	case uint64:
		return Uint64Value(v)
	case bool:
		return BoolValue(v)
	case time.Duration:
		return DurationValue(v)
	case time.Time:
		return TimeValue(v)
	case uint8:
		return Uint64Value(uint64(v))
	case uint16:
		return Uint64Value(uint64(v))
	case uint32:
		return Uint64Value(uint64(v))
	case uintptr:
		return Uint64Value(uint64(v))
	case int8:
		return Int64Value(int64(v))
	case int16:
		return Int64Value(int64(v))
	case int32:
		return Int64Value(int64(v))
	case float64:
		return Float64Value(v)
	case float32:
		return Float64Value(float64(v))
	case []Attr:
		return GroupValue(v...)
	case Kind:
		return Value{any: kind(v)}
	case Value:
		return v
	default:
		return Value{any: v}
	}
}

//////////////// 访问器

// Any 将 v 的值作为 any 返回。
func (v Value) Any() any {
	switch v.Kind() {
	case KindAny:
		if k, ok := v.any.(kind); ok {
			return Kind(k)
		}
		return v.any
	case KindLogValuer:
		return v.any
	case KindGroup:
		return v.group()
	case KindInt64:
		return int64(v.num)
	case KindUint64:
		return v.num
	case KindFloat64:
		return v.float()
	case KindString:
		return v.str()
	case KindBool:
		return v.bool()
	case KindDuration:
		return v.duration()
	case KindTime:
		return v.time()
	default:
		panic(fmt.Sprintf("bad kind: %s", v.Kind()))
	}
}

// String 将 Value 的值作为字符串返回，格式类似于 [fmt.Sprint]。
// 与在 v 的种类错误时会 panic 的 Int64、Float64 等方法不同，
// String 从不 panic。
func (v Value) String() string {
	if sp, ok := v.any.(stringptr); ok {
		return unsafe.String(sp, v.num)
	}
	var buf []byte
	return string(v.append(buf))
}

func (v Value) str() string {
	return unsafe.String(v.any.(stringptr), v.num)
}

// Int64 将 v 的值作为 int64 返回。如果 v 不是
// 有符号整数，它会 panic。
func (v Value) Int64() int64 {
	if g, w := v.Kind(), KindInt64; g != w {
		panic(fmt.Sprintf("Value kind is %s, not %s", g, w))
	}
	return int64(v.num)
}

// Uint64 将 v 的值作为 uint64 返回。如果 v 不是
// 无符号整数，它会 panic。
func (v Value) Uint64() uint64 {
	if g, w := v.Kind(), KindUint64; g != w {
		panic(fmt.Sprintf("Value kind is %s, not %s", g, w))
	}
	return v.num
}

// Bool 将 v 的值作为 bool 返回。如果 v 不是
// bool，它会 panic。
func (v Value) Bool() bool {
	if g, w := v.Kind(), KindBool; g != w {
		panic(fmt.Sprintf("Value kind is %s, not %s", g, w))
	}
	return v.bool()
}

func (v Value) bool() bool {
	return v.num == 1
}

// Duration 将 v 的值作为 [time.Duration] 返回。如果 v 不是
// time.Duration，它会 panic。
func (v Value) Duration() time.Duration {
	if g, w := v.Kind(), KindDuration; g != w {
		panic(fmt.Sprintf("Value kind is %s, not %s", g, w))
	}

	return v.duration()
}

func (v Value) duration() time.Duration {
	return time.Duration(int64(v.num))
}

// Float64 将 v 的值作为 float64 返回。如果 v 不是
// float64，它会 panic。
func (v Value) Float64() float64 {
	if g, w := v.Kind(), KindFloat64; g != w {
		panic(fmt.Sprintf("Value kind is %s, not %s", g, w))
	}

	return v.float()
}

func (v Value) float() float64 {
	return math.Float64frombits(v.num)
}

// Time 将 v 的值作为 [time.Time] 返回。如果 v 不是
// time.Time，它会 panic。
func (v Value) Time() time.Time {
	if g, w := v.Kind(), KindTime; g != w {
		panic(fmt.Sprintf("Value kind is %s, not %s", g, w))
	}
	return v.time()
}

// 关于时间如何表示，参见 TimeValue。
func (v Value) time() time.Time {
	switch a := v.any.(type) {
	case timeLocation:
		if a == nil {
			return time.Time{}
		}
		return time.Unix(0, int64(v.num)).In(a)
	case timeTime:
		return time.Time(a)
	default:
		panic(fmt.Sprintf("bad time type %T", v.any))
	}
}

// LogValuer 将 v 的值作为 LogValuer 返回。如果 v 不是
// LogValuer，它会 panic。
func (v Value) LogValuer() LogValuer {
	return v.any.(LogValuer)
}

// Group 将 v 的值作为 []Attr 返回。
// 如果 v 的 [Kind] 不是 [KindGroup]，它会 panic。
func (v Value) Group() []Attr {
	if sp, ok := v.any.(groupptr); ok {
		return unsafe.Slice((*Attr)(sp), v.num)
	}
	panic("Group: bad kind")
}

func (v Value) group() []Attr {
	return unsafe.Slice((*Attr)(v.any.(groupptr)), v.num)
}

//////////////// 其他

// Equal 报告 v 和 w 是否表示相同的 Go 值。
func (v Value) Equal(w Value) bool {
	k1 := v.Kind()
	k2 := w.Kind()
	if k1 != k2 {
		return false
	}
	switch k1 {
	case KindInt64, KindUint64, KindBool, KindDuration:
		return v.num == w.num
	case KindString:
		return v.str() == w.str()
	case KindFloat64:
		return v.float() == w.float()
	case KindTime:
		return v.time().Equal(w.time())
	case KindAny, KindLogValuer:
		return v.any == w.any // 如果不可比较可能会 panic
	case KindGroup:
		return slices.EqualFunc(v.group(), w.group(), Attr.Equal)
	default:
		panic(fmt.Sprintf("bad kind: %s", k1))
	}
}

// isEmptyGroup 报告 v 是否为没有属性的分组。
func (v Value) isEmptyGroup() bool {
	if v.Kind() != KindGroup {
		return false
	}
	// 我们不需要递归检查该分组的 Attrs 是否为空，
	// 因为 GroupValue 在构造分组时已移除它们，而且
	// 分组是不可变的。
	return len(v.group()) == 0
}

// append 将 v 的文本表示追加到 dst。
// v 的格式化方式与 fmt.Sprint 相同。
func (v Value) append(dst []byte) []byte {
	switch v.Kind() {
	case KindString:
		return append(dst, v.str()...)
	case KindInt64:
		return strconv.AppendInt(dst, int64(v.num), 10)
	case KindUint64:
		return strconv.AppendUint(dst, v.num, 10)
	case KindFloat64:
		return strconv.AppendFloat(dst, v.float(), 'g', -1, 64)
	case KindBool:
		return strconv.AppendBool(dst, v.bool())
	case KindDuration:
		return append(dst, v.duration().String()...)
	case KindTime:
		return append(dst, v.time().String()...)
	case KindGroup:
		return fmt.Append(dst, v.group())
	case KindAny, KindLogValuer:
		return fmt.Append(dst, v.any)
	default:
		panic(fmt.Sprintf("bad kind: %s", v.Kind()))
	}
}

// LogValuer 是任何能够将自身转换为用于日志的 Value 的 Go 值。
//
// 该机制可用于将昂贵的操作推迟到需要时才执行，
// 或将单个值展开为一系列组件。
type LogValuer interface {
	LogValue() Value
}

const maxLogValues = 100

// Resolve 在 v 实现 [LogValuer] 期间反复调用 v 上的 LogValue，
// 并返回结果。
// 如果 v 解析为一个分组，则该分组的属性值不会被递归
// 解析。
// 如果 LogValue 的调用次数超过阈值，则返回一个包含
// 错误的 Value。
// Resolve 的返回值保证不会是 [KindLogValuer] 种类。
func (v Value) Resolve() (rv Value) {
	orig := v
	defer func() {
		if r := recover(); r != nil {
			rv = AnyValue(fmt.Errorf("LogValue panicked\n%s", stack(3, 5)))
		}
	}()

	for i := 0; i < maxLogValues; i++ {
		if v.Kind() != KindLogValuer {
			return v
		}
		v = v.LogValuer().LogValue()
	}
	err := fmt.Errorf("LogValue called too many times on Value of type %T", orig.Any())
	return AnyValue(err)
}

func stack(skip, nFrames int) string {
	pcs := make([]uintptr, nFrames+1)
	n := runtime.Callers(skip+1, pcs)
	if n == 0 {
		return "(no stack)"
	}
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	i := 0
	for {
		frame, more := frames.Next()
		fmt.Fprintf(&b, "called from %s (%s:%d)\n", frame.Function, frame.File, frame.Line)
		if !more {
			break
		}
		i++
		if i >= nFrames {
			fmt.Fprintf(&b, "(rest of stack elided)\n")
			break
		}
	}
	return b.String()
}
