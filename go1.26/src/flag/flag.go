// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package flag 实现命令行标志解析。

# 用法

使用 [flag.String]、[Bool]、[Int] 等来定义标志。

下面声明了一个整数标志 -n，存储在 *int 类型的指针 nFlag 中：

	import "flag"
	var nFlag = flag.Int("n", 1234, "help message for flag n")

如果你愿意，也可以使用 Var() 函数将标志绑定到一个变量。

	var flagvar int
	func init() {
		flag.IntVar(&flagvar, "flagname", 1234, "help message for flagname")
	}

或者，你可以创建满足 Value 接口（带有指针接收者）的自定义标志，
并通过以下方式将它们与标志解析关联起来

	flag.Var(&flagVal, "name", "help message for flagname")

对于这类标志，默认值就是该变量的初始值。

在所有标志都定义好之后，调用

	flag.Parse()

将命令行解析到已定义的标志中。

之后便可以直接使用这些标志。如果直接使用标志本身，
它们都是指针；如果绑定到变量，它们就是值。

	fmt.Println("ip has value ", *ip)
	fmt.Println("flagvar has value ", flagvar)

解析完成后，标志后面的参数可以作为切片 [flag.Args] 获取，
也可以通过 [flag.Arg](i) 单独获取。
参数下标从 0 到 [flag.NArg]-1。

# 命令行标志语法

允许以下形式：

	-flag
	--flag   // double dashes are also permitted
	-flag=x
	-flag x  // non-boolean flags only

可以使用一个或两个短横线；它们是等价的。
最后一种形式不适用于布尔标志，因为命令

	cmd -x *

（其中 * 是 Unix shell 通配符）的含义会因存在名为
0、false 等文件而改变。要关闭布尔标志，必须使用 -flag=false 形式。

标志解析会在第一个非标志参数之前（"-" 是非标志参数）
或终止符 "--" 之后停止。

整数标志接受 1234、0664、0x1234，且可以为负数。
布尔标志可以是：

	1, 0, t, f, T, F, true, false, TRUE, FALSE, True, False

Duration 标志接受任何对 time.ParseDuration 有效的输入。

默认的命令行标志集合由顶层函数控制。
[FlagSet] 类型允许定义相互独立的标志集合，
例如在命令行界面中实现子命令。
[FlagSet] 的方法与命令行标志集合的顶层函数类似。
*/
package flag

import (
	"encoding"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrHelp 是在调用了 -help 或 -h 标志但未定义该标志时返回的错误。
var ErrHelp = errors.New("flag: help requested")

// errParse 由 Set 返回，表示标志的值解析失败，例如为 Int 提供了无效的整数。
// 随后它会经过 failf 包装，以提供更多信息。
var errParse = errors.New("parse error")

// errRange 由 Set 返回，表示标志的值超出范围。
// 随后它会经过 failf 包装，以提供更多信息。
var errRange = errors.New("value out of range")

func numError(err error) error {
	ne, ok := err.(*strconv.NumError)
	if !ok {
		return err
	}
	if ne.Err == strconv.ErrSyntax {
		return errParse
	}
	if ne.Err == strconv.ErrRange {
		return errRange
	}
	return err
}

// -- bool Value
type boolValue bool

func newBoolValue(val bool, p *bool) *boolValue {
	*p = val
	return (*boolValue)(p)
}

func (b *boolValue) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		err = errParse
	}
	*b = boolValue(v)
	return err
}

func (b *boolValue) Get() any { return bool(*b) }

func (b *boolValue) String() string { return strconv.FormatBool(bool(*b)) }

func (b *boolValue) IsBoolFlag() bool { return true }

// 可选接口，用于标识可以不提供 "=value" 文本的布尔标志
type boolFlag interface {
	Value
	IsBoolFlag() bool
}

// -- int Value
type intValue int

func newIntValue(val int, p *int) *intValue {
	*p = val
	return (*intValue)(p)
}

func (i *intValue) Set(s string) error {
	v, err := strconv.ParseInt(s, 0, strconv.IntSize)
	if err != nil {
		err = numError(err)
	}
	*i = intValue(v)
	return err
}

func (i *intValue) Get() any { return int(*i) }

func (i *intValue) String() string { return strconv.Itoa(int(*i)) }

// -- int64 Value
type int64Value int64

func newInt64Value(val int64, p *int64) *int64Value {
	*p = val
	return (*int64Value)(p)
}

func (i *int64Value) Set(s string) error {
	v, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		err = numError(err)
	}
	*i = int64Value(v)
	return err
}

func (i *int64Value) Get() any { return int64(*i) }

func (i *int64Value) String() string { return strconv.FormatInt(int64(*i), 10) }

// -- uint Value
type uintValue uint

func newUintValue(val uint, p *uint) *uintValue {
	*p = val
	return (*uintValue)(p)
}

func (i *uintValue) Set(s string) error {
	v, err := strconv.ParseUint(s, 0, strconv.IntSize)
	if err != nil {
		err = numError(err)
	}
	*i = uintValue(v)
	return err
}

func (i *uintValue) Get() any { return uint(*i) }

func (i *uintValue) String() string { return strconv.FormatUint(uint64(*i), 10) }

// -- uint64 Value
type uint64Value uint64

func newUint64Value(val uint64, p *uint64) *uint64Value {
	*p = val
	return (*uint64Value)(p)
}

func (i *uint64Value) Set(s string) error {
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		err = numError(err)
	}
	*i = uint64Value(v)
	return err
}

func (i *uint64Value) Get() any { return uint64(*i) }

func (i *uint64Value) String() string { return strconv.FormatUint(uint64(*i), 10) }

// -- string Value
type stringValue string

func newStringValue(val string, p *string) *stringValue {
	*p = val
	return (*stringValue)(p)
}

func (s *stringValue) Set(val string) error {
	*s = stringValue(val)
	return nil
}

func (s *stringValue) Get() any { return string(*s) }

func (s *stringValue) String() string { return string(*s) }

// -- float64 Value
type float64Value float64

func newFloat64Value(val float64, p *float64) *float64Value {
	*p = val
	return (*float64Value)(p)
}

func (f *float64Value) Set(s string) error {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		err = numError(err)
	}
	*f = float64Value(v)
	return err
}

func (f *float64Value) Get() any { return float64(*f) }

func (f *float64Value) String() string { return strconv.FormatFloat(float64(*f), 'g', -1, 64) }

// -- time.Duration Value
type durationValue time.Duration

func newDurationValue(val time.Duration, p *time.Duration) *durationValue {
	*p = val
	return (*durationValue)(p)
}

func (d *durationValue) Set(s string) error {
	v, err := time.ParseDuration(s)
	if err != nil {
		err = errParse
	}
	*d = durationValue(v)
	return err
}

func (d *durationValue) Get() any { return time.Duration(*d) }

func (d *durationValue) String() string { return (*time.Duration)(d).String() }

// -- encoding.TextUnmarshaler Value
type textValue struct{ p encoding.TextUnmarshaler }

func newTextValue(val encoding.TextMarshaler, p encoding.TextUnmarshaler) textValue {
	ptrVal := reflect.ValueOf(p)
	if ptrVal.Kind() != reflect.Ptr {
		panic("variable value type must be a pointer")
	}
	defVal := reflect.ValueOf(val)
	if defVal.Kind() == reflect.Ptr {
		defVal = defVal.Elem()
	}
	if defVal.Type() != ptrVal.Type().Elem() {
		panic(fmt.Sprintf("default type does not match variable type: %v != %v", defVal.Type(), ptrVal.Type().Elem()))
	}
	ptrVal.Elem().Set(defVal)
	return textValue{p}
}

func (v textValue) Set(s string) error {
	return v.p.UnmarshalText([]byte(s))
}

func (v textValue) Get() any {
	return v.p
}

func (v textValue) String() string {
	if m, ok := v.p.(encoding.TextMarshaler); ok {
		if b, err := m.MarshalText(); err == nil {
			return string(b)
		}
	}
	return ""
}

// -- func Value
type funcValue func(string) error

func (f funcValue) Set(s string) error { return f(s) }

func (f funcValue) String() string { return "" }

// -- boolFunc Value
type boolFuncValue func(string) error

func (f boolFuncValue) Set(s string) error { return f(s) }

func (f boolFuncValue) String() string { return "" }

func (f boolFuncValue) IsBoolFlag() bool { return true }

// Value 是存储在标志中的动态值的接口。
// （默认值以字符串形式表示。）
//
// 如果某个 Value 具有返回 true 的 IsBoolFlag() bool 方法，
// 命令行解析器会将 -name 视为等价于 -name=true，
// 而不是使用下一个命令行参数。
//
// 对于每个出现的标志，Set 会按命令行顺序被调用一次。
// flag 包可能会以零值接收者（例如 nil 指针）调用 [String] 方法。
type Value interface {
	String() string
	Set(string) error
}

// Getter 是一个接口，允许获取 [Value] 的内容。
// 它包装了 [Value] 接口，而不是作为其一部分，因为它
// 出现在 Go 1 及其兼容性规则之后。本包提供的所有 [Value] 类型
// 都满足 [Getter] 接口，除了 [Func] 使用的类型。
type Getter interface {
	Value
	Get() any
}

// ErrorHandling 定义了解析失败时 [FlagSet.Parse] 的行为。
type ErrorHandling int

// 这些常量使 [FlagSet.Parse] 在解析失败时按所述方式运行。
const (
	ContinueOnError ErrorHandling = iota // 返回一个描述性错误。
	ExitOnError                          // 调用 os.Exit(2)，对于 -h/-help 则调用 Exit(0)。
	PanicOnError                         // 调用 panic 并附带描述性错误。
)

// FlagSet 表示一组已定义的标志。FlagSet 的零值
// 没有名称，并具有 [ContinueOnError] 错误处理方式。
//
// [Flag] 名称在同一个 FlagSet 内必须唯一。若尝试定义一个名称
// 已被使用的标志，将导致 panic。
type FlagSet struct {
	// Usage 是在解析标志过程中发生错误时调用的函数。
	// 该字段是一个函数（而非方法），可以改为指向
	// 自定义的错误处理函数。调用 Usage 之后发生什么取决于
	// ErrorHandling 设置；对于命令行来说，它默认为
	// ExitOnError，即在调用 Usage 之后退出程序。
	Usage func()

	name          string
	parsed        bool
	actual        map[string]*Flag
	formal        map[string]*Flag
	args          []string // 标志之后的参数
	errorHandling ErrorHandling
	output        io.Writer         // 为 nil 表示 stderr；使用 Output() 访问方法
	undef         map[string]string // 在 Set 时还不存在的标志
}

// Flag 表示一个标志的状态。
type Flag struct {
	Name     string // 名称，如命令行中出现的那样
	Usage    string // 帮助消息
	Value    Value  // 设置的值
	DefValue string // 默认值（以文本形式）；用于用法消息
}

// sortFlags 以字典序排序返回标志的切片。
func sortFlags(flags map[string]*Flag) []*Flag {
	result := make([]*Flag, len(flags))
	i := 0
	for _, f := range flags {
		result[i] = f
		i++
	}
	slices.SortFunc(result, func(a, b *Flag) int {
		return strings.Compare(a.Name, b.Name)
	})
	return result
}

// Output 返回用法消息和错误消息的目标。如果
// output 未设置或被设为 nil，则返回 [os.Stderr]。
func (f *FlagSet) Output() io.Writer {
	if f.output == nil {
		return os.Stderr
	}
	return f.output
}

// Name 返回标志集合的名称。
func (f *FlagSet) Name() string {
	return f.name
}

// ErrorHandling 返回标志集合的错误处理行为。
func (f *FlagSet) ErrorHandling() ErrorHandling {
	return f.errorHandling
}

// SetOutput 设置用法消息和错误消息的目标。
// 如果 output 为 nil，则使用 [os.Stderr]。
func (f *FlagSet) SetOutput(output io.Writer) {
	f.output = output
}

// VisitAll 按字典序访问标志，对每个标志调用 fn。
// 它会访问所有标志，包括未设置的标志。
func (f *FlagSet) VisitAll(fn func(*Flag)) {
	for _, flag := range sortFlags(f.formal) {
		fn(flag)
	}
}

// VisitAll 按字典序访问命令行标志，对每个标志调用
// fn。它会访问所有标志，包括未设置的标志。
func VisitAll(fn func(*Flag)) {
	CommandLine.VisitAll(fn)
}

// Visit 按字典序访问标志，对每个标志调用 fn。
// 它只访问已设置的标志。
func (f *FlagSet) Visit(fn func(*Flag)) {
	for _, flag := range sortFlags(f.actual) {
		fn(flag)
	}
}

// Visit 按字典序访问命令行标志，对每个标志调用 fn。
// 它只访问已设置的标志。
func Visit(fn func(*Flag)) {
	CommandLine.Visit(fn)
}

// Lookup 返回指定名称标志的 [Flag] 结构，若不存在则返回 nil。
func (f *FlagSet) Lookup(name string) *Flag {
	return f.formal[name]
}

// Lookup 返回指定名称命令行标志的 [Flag] 结构，
// 若不存在则返回 nil。
func Lookup(name string) *Flag {
	return CommandLine.formal[name]
}

// Set 设置指定名称标志的值。
func (f *FlagSet) Set(name, value string) error {
	return f.set(name, value)
}
func (f *FlagSet) set(name, value string) error {
	flag, ok := f.formal[name]
	if !ok {
		// 记住正在设置一个尚未定义的标志。
		// 在这种情况我们返回错误，但此外，如果
		// 该标志随后被定义，我们希望
		// 在定义处触发 panic。
		// 当定义和 Set 调用都位于 init 代码中，
		// 且由于某种原因 init 代码改变了求值顺序时，就会出现这个问题。
		// 参见 issue 57411。
		_, file, line, ok := runtime.Caller(2)
		if !ok {
			file = "?"
			line = 0
		}
		if f.undef == nil {
			f.undef = map[string]string{}
		}
		f.undef[name] = fmt.Sprintf("%s:%d", file, line)

		return fmt.Errorf("no such flag -%v", name)
	}
	err := flag.Value.Set(value)
	if err != nil {
		return err
	}
	if f.actual == nil {
		f.actual = make(map[string]*Flag)
	}
	f.actual[name] = flag
	return nil
}

// Set 设置指定名称命令行标志的值。
func Set(name, value string) error {
	return CommandLine.set(name, value)
}

// isZeroValue 判断该字符串是否表示某个标志的零值。
func isZeroValue(flag *Flag, value string) (ok bool, err error) {
	// 构造该标志 Value 类型的零值，并检查
	// 调用其 String 方法的结果是否等于传入的值。
	// 除非 Value 类型本身是接口类型，否则这都有效。
	typ := reflect.TypeOf(flag.Value)
	var z reflect.Value
	if typ.Kind() == reflect.Pointer {
		z = reflect.New(typ.Elem())
	} else {
		z = reflect.Zero(typ)
	}
	// 捕获调用 String 方法时的 panic，这不应该阻止
	// 用法消息被打印，但我们应该向用户报告，
	// 以便他们知道要修复自己的代码。
	defer func() {
		if e := recover(); e != nil {
			if typ.Kind() == reflect.Pointer {
				typ = typ.Elem()
			}
			err = fmt.Errorf("panic calling String method on zero %v for flag %s: %v", typ, flag.Name, e)
		}
	}()
	return value == z.Interface().(Value).String(), nil
}

// UnquoteUsage 从标志的用法字符串中提取反引号括起的名称，
// 并返回该名称以及去掉反引号的用法字符串。
// 给定 "a `name` to show"，它返回 ("name", "a name to show")。
// 如果没有反引号，则该名称是对标志值类型的一个合理猜测，
// 如果标志是布尔类型，则为空字符串。
func UnquoteUsage(flag *Flag) (name string, usage string) {
	// 查找反引号括起的名称，但避免使用 strings 包。
	usage = flag.Usage
	for i := 0; i < len(usage); i++ {
		if usage[i] == '`' {
			for j := i + 1; j < len(usage); j++ {
				if usage[j] == '`' {
					name = usage[i+1 : j]
					usage = usage[:i] + name + usage[j+1:]
					return name, usage
				}
			}
			break // 只有一个反引号；使用类型名称。
		}
	}
	// 没有显式名称，所以如果能找到类型就使用类型。
	name = "value"
	switch fv := flag.Value.(type) {
	case boolFlag:
		if fv.IsBoolFlag() {
			name = ""
		}
	case *durationValue:
		name = "duration"
	case *float64Value:
		name = "float"
	case *intValue, *int64Value:
		name = "int"
	case *stringValue:
		name = "string"
	case *uintValue, *uint64Value:
		name = "uint"
	}
	return
}

// PrintDefaults 将集合中所有已定义命令行标志的默认值打印到标准错误
// （除非另有配置）。更多信息请参见全局函数 PrintDefaults 的文档。
func (f *FlagSet) PrintDefaults() {
	var isZeroValueErrs []error
	f.VisitAll(func(flag *Flag) {
		var b strings.Builder
		fmt.Fprintf(&b, "  -%s", flag.Name) // - 前有两个空格；参见接下来两条注释。
		name, usage := UnquoteUsage(flag)
		if len(name) > 0 {
			b.WriteString(" ")
			b.WriteString(name)
		}
		// 单个 ASCII 字母的布尔标志非常常见，因此
		// 我们特殊处理它们，把其用法放在同一行。
		if b.Len() <= 4 { // 空格、空格、'-'、'x'。
			b.WriteString("\t")
		} else {
			// 制表符前的四个空格能在 4 和 8 空格制表位下
			// 都触发良好的对齐。
			b.WriteString("\n    \t")
		}
		b.WriteString(strings.ReplaceAll(usage, "\n", "\n    \t"))

		// 仅当默认值与该标志类型的零值不同时，
		// 才打印默认值。
		if isZero, err := isZeroValue(flag, flag.DefValue); err != nil {
			isZeroValueErrs = append(isZeroValueErrs, err)
		} else if !isZero {
			if _, ok := flag.Value.(*stringValue); ok {
				// 给该值加上引号
				fmt.Fprintf(&b, " (default %q)", flag.DefValue)
			} else {
				fmt.Fprintf(&b, " (default %v)", flag.DefValue)
			}
		}
		fmt.Fprint(f.Output(), b.String(), "\n")
	})
	// 如果对任何零值 flag.Value 调用 String 触发了 panic，则
	// 在打印完整个默认值集合之后打印这些消息，
	// 以便程序员知道要修复该 panic。
	if errs := isZeroValueErrs; len(errs) > 0 {
		fmt.Fprintln(f.Output())
		for _, err := range errs {
			fmt.Fprintln(f.Output(), err)
		}
	}
}

// PrintDefaults 将显示所有已定义命令行标志默认设置的用法消息
// 打印到标准错误（除非另有配置）。
// 对于整数值的标志 x，默认输出形式为
//
//	-x int
//		usage-message-for-x (default 7)
//
// 除单字节名称的 bool 标志外，用法消息都会出现在单独一行。
// 对于 bool 标志，类型会被省略；若标志名是一个字节，
// 用法消息会出现在同一行。如果默认值是该类型的零值，
// 则省略括号中的默认值。所列出的类型（这里是 int）可以通过
// 在标志的用法字符串中放置一个反引号括起的名称来更改；
// 消息中第一个这样的项会被视为要显示的参数名，
// 显示时会去掉消息中的反引号。例如，给定
//
//	flag.String("I", "", "search `directory` for include files")
//
// 输出将是
//
//	-I directory
//		search directory for include files.
//
// 要更改标志消息的目标，请调用 [CommandLine].SetOutput。
func PrintDefaults() {
	CommandLine.PrintDefaults()
}

// defaultUsage 是打印用法消息的默认函数。
func (f *FlagSet) defaultUsage() {
	if f.name == "" {
		fmt.Fprintf(f.Output(), "Usage:\n")
	} else {
		fmt.Fprintf(f.Output(), "Usage of %s:\n", f.name)
	}
	f.PrintDefaults()
}

// 注意：Usage 不仅仅是 defaultUsage(CommandLine)，
// 因为它（通过 godoc 中的 flag Usage）作为
// 如何编写自己的 usage 函数的示例。

// Usage 将记录所有已定义命令行标志的用法消息打印到
// [CommandLine] 的输出，默认是 [os.Stderr]。
// 它在解析标志过程中发生错误时被调用。
// 该函数是一个变量，可以改为指向自定义函数。
// 默认情况下它会打印一个简单的头部并调用 [PrintDefaults]；关于输出格式
// 以及如何控制它的详细信息，请参见 [PrintDefaults] 的文档。
// 自定义的 usage 函数可以选择退出程序；默认情况下退出本来就会发生，
// 因为命令行的错误处理策略被设置为
// [ExitOnError]。
var Usage = func() {
	fmt.Fprintf(CommandLine.Output(), "Usage of %s:\n", os.Args[0])
	PrintDefaults()
}

// NFlag 返回已设置标志的数量。
func (f *FlagSet) NFlag() int { return len(f.actual) }

// NFlag 返回已设置命令行标志的数量。
func NFlag() int { return len(CommandLine.actual) }

// Arg 返回第 i 个参数。Arg(0) 是处理完标志后剩下的第一个参数。
// 如果请求的元素不存在，Arg 返回空字符串。
func (f *FlagSet) Arg(i int) string {
	if i < 0 || i >= len(f.args) {
		return ""
	}
	return f.args[i]
}

// Arg 返回第 i 个命令行参数。Arg(0) 是处理完标志后剩下的第一个参数。
// 如果请求的元素不存在，Arg 返回空字符串。
func Arg(i int) string {
	return CommandLine.Arg(i)
}

// NArg 是处理完标志后剩余参数的数量。
func (f *FlagSet) NArg() int { return len(f.args) }

// NArg 是处理完标志后剩余参数的数量。
func NArg() int { return len(CommandLine.args) }

// Args 返回非标志参数。
func (f *FlagSet) Args() []string { return f.args }

// Args 返回非标志命令行参数。
func Args() []string { return CommandLine.args }

// BoolVar 定义一个具有指定名称、默认值和用法字符串的 bool 标志。
// 参数 p 指向一个 bool 变量，用于存储该标志的值。
func (f *FlagSet) BoolVar(p *bool, name string, value bool, usage string) {
	f.Var(newBoolValue(value, p), name, usage)
}

// BoolVar 定义一个具有指定名称、默认值和用法字符串的 bool 标志。
// 参数 p 指向一个 bool 变量，用于存储该标志的值。
func BoolVar(p *bool, name string, value bool, usage string) {
	CommandLine.Var(newBoolValue(value, p), name, usage)
}

// Bool 定义一个具有指定名称、默认值和用法字符串的 bool 标志。
// 返回值是一个 bool 变量的地址，该变量存储标志的值。
func (f *FlagSet) Bool(name string, value bool, usage string) *bool {
	p := new(bool)
	f.BoolVar(p, name, value, usage)
	return p
}

// Bool 定义一个具有指定名称、默认值和用法字符串的 bool 标志。
// 返回值是一个 bool 变量的地址，该变量存储标志的值。
func Bool(name string, value bool, usage string) *bool {
	return CommandLine.Bool(name, value, usage)
}

// IntVar 定义一个具有指定名称、默认值和用法字符串的 int 标志。
// 参数 p 指向一个 int 变量，用于存储该标志的值。
func (f *FlagSet) IntVar(p *int, name string, value int, usage string) {
	f.Var(newIntValue(value, p), name, usage)
}

// IntVar 定义一个具有指定名称、默认值和用法字符串的 int 标志。
// 参数 p 指向一个 int 变量，用于存储该标志的值。
func IntVar(p *int, name string, value int, usage string) {
	CommandLine.Var(newIntValue(value, p), name, usage)
}

// Int 定义一个具有指定名称、默认值和用法字符串的 int 标志。
// 返回值是一个 int 变量的地址，该变量存储标志的值。
func (f *FlagSet) Int(name string, value int, usage string) *int {
	p := new(int)
	f.IntVar(p, name, value, usage)
	return p
}

// Int 定义一个具有指定名称、默认值和用法字符串的 int 标志。
// 返回值是一个 int 变量的地址，该变量存储标志的值。
func Int(name string, value int, usage string) *int {
	return CommandLine.Int(name, value, usage)
}

// Int64Var 定义一个具有指定名称、默认值和用法字符串的 int64 标志。
// 参数 p 指向一个 int64 变量，用于存储该标志的值。
func (f *FlagSet) Int64Var(p *int64, name string, value int64, usage string) {
	f.Var(newInt64Value(value, p), name, usage)
}

// Int64Var 定义一个具有指定名称、默认值和用法字符串的 int64 标志。
// 参数 p 指向一个 int64 变量，用于存储该标志的值。
func Int64Var(p *int64, name string, value int64, usage string) {
	CommandLine.Var(newInt64Value(value, p), name, usage)
}

// Int64 定义一个具有指定名称、默认值和用法字符串的 int64 标志。
// 返回值是一个 int64 变量的地址，该变量存储标志的值。
func (f *FlagSet) Int64(name string, value int64, usage string) *int64 {
	p := new(int64)
	f.Int64Var(p, name, value, usage)
	return p
}

// Int64 定义一个具有指定名称、默认值和用法字符串的 int64 标志。
// 返回值是一个 int64 变量的地址，该变量存储标志的值。
func Int64(name string, value int64, usage string) *int64 {
	return CommandLine.Int64(name, value, usage)
}

// UintVar 定义一个具有指定名称、默认值和用法字符串的 uint 标志。
// 参数 p 指向一个 uint 变量，用于存储该标志的值。
func (f *FlagSet) UintVar(p *uint, name string, value uint, usage string) {
	f.Var(newUintValue(value, p), name, usage)
}

// UintVar 定义一个具有指定名称、默认值和用法字符串的 uint 标志。
// 参数 p 指向一个 uint 变量，用于存储该标志的值。
func UintVar(p *uint, name string, value uint, usage string) {
	CommandLine.Var(newUintValue(value, p), name, usage)
}

// Uint 定义一个具有指定名称、默认值和用法字符串的 uint 标志。
// 返回值是一个 uint 变量的地址，该变量存储标志的值。
func (f *FlagSet) Uint(name string, value uint, usage string) *uint {
	p := new(uint)
	f.UintVar(p, name, value, usage)
	return p
}

// Uint 定义一个具有指定名称、默认值和用法字符串的 uint 标志。
// 返回值是一个 uint 变量的地址，该变量存储标志的值。
func Uint(name string, value uint, usage string) *uint {
	return CommandLine.Uint(name, value, usage)
}

// Uint64Var 定义一个具有指定名称、默认值和用法字符串的 uint64 标志。
// 参数 p 指向一个 uint64 变量，用于存储该标志的值。
func (f *FlagSet) Uint64Var(p *uint64, name string, value uint64, usage string) {
	f.Var(newUint64Value(value, p), name, usage)
}

// Uint64Var 定义一个具有指定名称、默认值和用法字符串的 uint64 标志。
// 参数 p 指向一个 uint64 变量，用于存储该标志的值。
func Uint64Var(p *uint64, name string, value uint64, usage string) {
	CommandLine.Var(newUint64Value(value, p), name, usage)
}

// Uint64 定义一个具有指定名称、默认值和用法字符串的 uint64 标志。
// 返回值是一个 uint64 变量的地址，该变量存储标志的值。
func (f *FlagSet) Uint64(name string, value uint64, usage string) *uint64 {
	p := new(uint64)
	f.Uint64Var(p, name, value, usage)
	return p
}

// Uint64 定义一个具有指定名称、默认值和用法字符串的 uint64 标志。
// 返回值是一个 uint64 变量的地址，该变量存储标志的值。
func Uint64(name string, value uint64, usage string) *uint64 {
	return CommandLine.Uint64(name, value, usage)
}

// StringVar 定义一个具有指定名称、默认值和用法字符串的 string 标志。
// 参数 p 指向一个 string 变量，用于存储该标志的值。
func (f *FlagSet) StringVar(p *string, name string, value string, usage string) {
	f.Var(newStringValue(value, p), name, usage)
}

// StringVar 定义一个具有指定名称、默认值和用法字符串的 string 标志。
// 参数 p 指向一个 string 变量，用于存储该标志的值。
func StringVar(p *string, name string, value string, usage string) {
	CommandLine.Var(newStringValue(value, p), name, usage)
}

// String 定义一个具有指定名称、默认值和用法字符串的 string 标志。
// 返回值是一个 string 变量的地址，该变量存储标志的值。
func (f *FlagSet) String(name string, value string, usage string) *string {
	p := new(string)
	f.StringVar(p, name, value, usage)
	return p
}

// String 定义一个具有指定名称、默认值和用法字符串的 string 标志。
// 返回值是一个 string 变量的地址，该变量存储标志的值。
func String(name string, value string, usage string) *string {
	return CommandLine.String(name, value, usage)
}

// Float64Var 定义一个具有指定名称、默认值和用法字符串的 float64 标志。
// 参数 p 指向一个 float64 变量，用于存储该标志的值。
func (f *FlagSet) Float64Var(p *float64, name string, value float64, usage string) {
	f.Var(newFloat64Value(value, p), name, usage)
}

// Float64Var 定义一个具有指定名称、默认值和用法字符串的 float64 标志。
// 参数 p 指向一个 float64 变量，用于存储该标志的值。
func Float64Var(p *float64, name string, value float64, usage string) {
	CommandLine.Var(newFloat64Value(value, p), name, usage)
}

// Float64 定义一个具有指定名称、默认值和用法字符串的 float64 标志。
// 返回值是一个 float64 变量的地址，该变量存储标志的值。
func (f *FlagSet) Float64(name string, value float64, usage string) *float64 {
	p := new(float64)
	f.Float64Var(p, name, value, usage)
	return p
}

// Float64 定义一个具有指定名称、默认值和用法字符串的 float64 标志。
// 返回值是一个 float64 变量的地址，该变量存储标志的值。
func Float64(name string, value float64, usage string) *float64 {
	return CommandLine.Float64(name, value, usage)
}

// DurationVar 定义一个具有指定名称、默认值和用法字符串的 time.Duration 标志。
// 参数 p 指向一个 time.Duration 变量，用于存储该标志的值。
// 该标志接受对 time.ParseDuration 有效的值。
func (f *FlagSet) DurationVar(p *time.Duration, name string, value time.Duration, usage string) {
	f.Var(newDurationValue(value, p), name, usage)
}

// DurationVar 定义一个具有指定名称、默认值和用法字符串的 time.Duration 标志。
// 参数 p 指向一个 time.Duration 变量，用于存储该标志的值。
// 该标志接受对 time.ParseDuration 有效的值。
func DurationVar(p *time.Duration, name string, value time.Duration, usage string) {
	CommandLine.Var(newDurationValue(value, p), name, usage)
}

// Duration 定义一个具有指定名称、默认值和用法字符串的 time.Duration 标志。
// 返回值是一个 time.Duration 变量的地址，该变量存储标志的值。
// 该标志接受对 time.ParseDuration 有效的值。
func (f *FlagSet) Duration(name string, value time.Duration, usage string) *time.Duration {
	p := new(time.Duration)
	f.DurationVar(p, name, value, usage)
	return p
}

// Duration 定义一个具有指定名称、默认值和用法字符串的 time.Duration 标志。
// 返回值是一个 time.Duration 变量的地址，该变量存储标志的值。
// 该标志接受对 time.ParseDuration 有效的值。
func Duration(name string, value time.Duration, usage string) *time.Duration {
	return CommandLine.Duration(name, value, usage)
}

// TextVar 定义一个具有指定名称、默认值和用法字符串的标志。
// 参数 p 必须是指向将保存该标志值的变量的指针，
// 并且 p 必须实现 encoding.TextUnmarshaler。
// 如果使用了该标志，标志值将被传递给 p 的 UnmarshalText 方法。
// 默认值的类型必须与 p 的类型相同。
func (f *FlagSet) TextVar(p encoding.TextUnmarshaler, name string, value encoding.TextMarshaler, usage string) {
	f.Var(newTextValue(value, p), name, usage)
}

// TextVar 定义一个具有指定名称、默认值和用法字符串的标志。
// 参数 p 必须是指向将保存该标志值的变量的指针，
// 并且 p 必须实现 encoding.TextUnmarshaler。
// 如果使用了该标志，标志值将被传递给 p 的 UnmarshalText 方法。
// 默认值的类型必须与 p 的类型相同。
func TextVar(p encoding.TextUnmarshaler, name string, value encoding.TextMarshaler, usage string) {
	CommandLine.Var(newTextValue(value, p), name, usage)
}

// Func 定义一个具有指定名称和用法字符串的标志。
// 每次遇到该标志时，都会用标志的值调用 fn。
// 如果 fn 返回非 nil 错误，它将被视为标志值解析错误。
func (f *FlagSet) Func(name, usage string, fn func(string) error) {
	f.Var(funcValue(fn), name, usage)
}

// Func 定义一个具有指定名称和用法字符串的标志。
// 每次遇到该标志时，都会用标志的值调用 fn。
// 如果 fn 返回非 nil 错误，它将被视为标志值解析错误。
func Func(name, usage string, fn func(string) error) {
	CommandLine.Func(name, usage, fn)
}

// BoolFunc 定义一个具有指定名称和用法字符串且不需要提供值的标志。
// 每次遇到该标志时，都会用标志的值调用 fn。
// 如果 fn 返回非 nil 错误，它将被视为标志值解析错误。
func (f *FlagSet) BoolFunc(name, usage string, fn func(string) error) {
	f.Var(boolFuncValue(fn), name, usage)
}

// BoolFunc 定义一个具有指定名称和用法字符串且不需要提供值的标志。
// 每次遇到该标志时，都会用标志的值调用 fn。
// 如果 fn 返回非 nil 错误，它将被视为标志值解析错误。
func BoolFunc(name, usage string, fn func(string) error) {
	CommandLine.BoolFunc(name, usage, fn)
}

// Var 定义一个具有指定名称和用法字符串的标志。标志的类型和值
// 由第一个参数表示，其类型为 [Value]，通常持有
// 用户自定义的 [Value] 实现。例如，
// 调用者可以通过为切片提供 [Value] 的方法，创建一个将逗号分隔的
// 字符串转换为字符串切片的标志；特别是 [Set] 会将
// 逗号分隔的字符串分解到该切片中。
func (f *FlagSet) Var(value Value, name string, usage string) {
	// 标志不能以 "-" 开头或包含 "="。
	if strings.HasPrefix(name, "-") {
		panic(f.sprintf("flag %q begins with -", name))
	} else if strings.Contains(name, "=") {
		panic(f.sprintf("flag %q contains =", name))
	}

	// 将默认值以字符串形式记住；它不会改变。
	flag := &Flag{name, usage, value, value.String()}
	_, alreadythere := f.formal[name]
	if alreadythere {
		var msg string
		if f.name == "" {
			msg = f.sprintf("flag redefined: %s", name)
		} else {
			msg = f.sprintf("%s flag redefined: %s", f.name, name)
		}
		panic(msg) // 仅在以相同名称声明标志时发生
	}
	if pos := f.undef[name]; pos != "" {
		panic(fmt.Sprintf("flag %s set at %s before being defined", name, pos))
	}
	if f.formal == nil {
		f.formal = make(map[string]*Flag)
	}
	f.formal[name] = flag
}

// Var 定义一个具有指定名称和用法字符串的标志。标志的类型和值
// 由第一个参数表示，其类型为 [Value]，通常持有
// 用户自定义的 [Value] 实现。例如，
// 调用者可以通过为切片提供 [Value] 的方法，创建一个将逗号分隔的
// 字符串转换为字符串切片的标志；特别是 [Set] 会将
// 逗号分隔的字符串分解到该切片中。
func Var(value Value, name string, usage string) {
	CommandLine.Var(value, name, usage)
}

// sprintf 格式化消息，将其打印到输出，并返回它。
func (f *FlagSet) sprintf(format string, a ...any) string {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintln(f.Output(), msg)
	return msg
}

// failf 将格式化后的错误和用法消息打印到标准错误，
// 并返回该错误。
func (f *FlagSet) failf(format string, a ...any) error {
	msg := f.sprintf(format, a...)
	f.usage()
	return errors.New(msg)
}

// usage 在指定了标志集合的 Usage 方法时调用它，
// 否则调用合适的默认 usage 函数。
func (f *FlagSet) usage() {
	if f.Usage == nil {
		f.defaultUsage()
	} else {
		f.Usage()
	}
}

// parseOne 解析一个标志。它报告是否遇到了标志。
func (f *FlagSet) parseOne() (bool, error) {
	if len(f.args) == 0 {
		return false, nil
	}
	s := f.args[0]
	if len(s) < 2 || s[0] != '-' {
		return false, nil
	}
	numMinuses := 1
	if s[1] == '-' {
		numMinuses++
		if len(s) == 2 { // "--" 终止标志解析
			f.args = f.args[1:]
			return false, nil
		}
	}
	name := s[numMinuses:]
	if len(name) == 0 || name[0] == '-' || name[0] == '=' {
		return false, f.failf("bad flag syntax: %s", s)
	}

	// 它是一个标志。它有参数吗？
	f.args = f.args[1:]
	hasValue := false
	value := ""
	for i := 1; i < len(name); i++ { // 等号不能是第一个字符
		if name[i] == '=' {
			value = name[i+1:]
			hasValue = true
			name = name[0:i]
			break
		}
	}

	flag, ok := f.formal[name]
	if !ok {
		if name == "help" || name == "h" { // 为提供友好的帮助消息而做的特殊处理。
			f.usage()
			return false, ErrHelp
		}
		return false, f.failf("flag provided but not defined: -%s", name)
	}

	if fv, ok := flag.Value.(boolFlag); ok && fv.IsBoolFlag() { // 特殊情况：不需要参数
		if hasValue {
			if err := fv.Set(value); err != nil {
				return false, f.failf("invalid boolean value %q for -%s: %v", value, name, err)
			}
		} else {
			if err := fv.Set("true"); err != nil {
				return false, f.failf("invalid boolean flag %s: %v", name, err)
			}
		}
	} else {
		// 它必须有一个值，这个值可能是下一个参数。
		if !hasValue && len(f.args) > 0 {
			// 值是下一个参数
			hasValue = true
			value, f.args = f.args[0], f.args[1:]
		}
		if !hasValue {
			return false, f.failf("flag needs an argument: -%s", name)
		}
		if err := flag.Value.Set(value); err != nil {
			return false, f.failf("invalid value %q for flag -%s: %v", value, name, err)
		}
	}
	if f.actual == nil {
		f.actual = make(map[string]*Flag)
	}
	f.actual[name] = flag
	return true, nil
}

// Parse 从参数列表中解析标志定义，参数列表不应
// 包含命令名。必须在 [FlagSet] 中的所有标志
// 都已定义之后、且程序访问标志之前调用。
// 如果设置了 -help 或 -h 但未定义它们，返回值将为 [ErrHelp]。
func (f *FlagSet) Parse(arguments []string) error {
	f.parsed = true
	f.args = arguments
	for {
		seen, err := f.parseOne()
		if seen {
			continue
		}
		if err == nil {
			break
		}
		switch f.errorHandling {
		case ContinueOnError:
			return err
		case ExitOnError:
			if err == ErrHelp {
				os.Exit(0)
			}
			os.Exit(2)
		case PanicOnError:
			panic(err)
		}
	}
	return nil
}

// Parsed 报告 f.Parse 是否已被调用。
func (f *FlagSet) Parsed() bool {
	return f.parsed
}

// Parse 从 [os.Args][1:] 解析命令行标志。必须在所有标志
// 都已定义之后、且程序访问标志之前调用。
func Parse() {
	// 忽略错误；CommandLine 被设置为 ExitOnError。
	CommandLine.Parse(os.Args[1:])
}

// Parsed 报告命令行标志是否已被解析。
func Parsed() bool {
	return CommandLine.Parsed()
}

// CommandLine 是默认的命令行标志集合，从 [os.Args] 解析而来。
// 诸如 [BoolVar]、[Arg] 等顶层函数都是 CommandLine
// 方法的包装。
var CommandLine *FlagSet

func init() {
	// execl 可能会传给我们一个空的 os.Args。
	if len(os.Args) == 0 {
		CommandLine = NewFlagSet("", ExitOnError)
	} else {
		CommandLine = NewFlagSet(os.Args[0], ExitOnError)
	}

	// 用对全局 Usage 的调用来覆盖通用的 FlagSet 默认 Usage。
	// 注意：这不是 CommandLine.Usage = Usage，
	// 因为我们希望之后任何调用都使用 Usage 更新后的值，
	// 而不是执行此行时它持有的值。
	CommandLine.Usage = commandLineUsage
}

func commandLineUsage() {
	Usage()
}

// NewFlagSet 返回一个新的空标志集合，具有指定的名称和
// 错误处理属性。如果名称非空，它将被打印
// 在默认用法消息和错误消息中。
func NewFlagSet(name string, errorHandling ErrorHandling) *FlagSet {
	f := &FlagSet{
		name:          name,
		errorHandling: errorHandling,
	}
	f.Usage = f.defaultUsage
	return f
}

// Init 设置标志集合的名称和错误处理属性。
// 默认情况下，零值 [FlagSet] 使用空名称和
// [ContinueOnError] 错误处理策略。
func (f *FlagSet) Init(name string, errorHandling ErrorHandling) {
	f.name = name
	f.errorHandling = errorHandling
}
