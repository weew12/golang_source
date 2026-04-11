// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmt

import (
	"internal/fmtsort"
	"io"
	"os"
	"reflect"
	"strconv"
	"sync"
	"unicode/utf8"
)

// 供 buffer.WriteString 使用的字符串。
// 与使用字节数组调用 buffer.Write 相比，这种方式开销更小。
const (
	commaSpaceString  = ", "
	nilAngleString    = "<nil>"
	nilParenString    = "(nil)"
	nilString         = "nil"
	mapString         = "map["
	percentBangString = "%!"
	missingString     = "(MISSING)"
	badIndexString    = "(BADINDEX)"
	panicString       = "(PANIC="
	extraString       = "%!(EXTRA "
	badWidthString    = "%!(BADWIDTH)"
	badPrecString     = "%!(BADPREC)"
	noVerbString      = "%!(NOVERB)"
	invReflectString  = "<invalid reflect.Value>"
)

// State 表示传递给自定义格式化器的打印器状态。
// 它提供对 [io.Writer] 接口的访问，以及操作数格式说明符的标志和选项信息。
type State interface {
	// Write 是用于输出待打印格式化内容的函数。
	Write(b []byte) (n int, err error)
	// Width 返回宽度选项的值以及该选项是否已设置。
	Width() (wid int, ok bool)
	// Precision 返回精度选项的值以及该选项是否已设置。
	Precision() (prec int, ok bool)

	// Flag 报告字符标志 c 是否已设置。
	Flag(c int) bool
}

// Formatter 由任何实现了 Format 方法的值实现。
// 该实现控制如何解释 [State] 和 rune，
// 并可调用 [Sprint] 或 [Fprint] 等方法生成输出。
type Formatter interface {
	Format(f State, verb rune)
}

// Stringer 由任何实现了 String 方法的值实现，
// 该方法定义了该值的“原生”格式。
// 当值作为操作数传递给接受字符串的格式，
// 或传递给 [Print] 这类无格式打印器时，会调用 String 方法进行打印。
type Stringer interface {
	String() string
}

// GoStringer 由任何实现了 GoString 方法的值实现，
// 该方法定义了该值的 Go 语法格式。
// 当值作为操作数传递给 %#v 格式时，会调用 GoString 方法进行打印。
type GoStringer interface {
	GoString() string
}

// FormatString 返回一个字符串，代表 [State] 捕获的完整格式化指令，
// 后跟参数动词。（[State] 本身不包含动词）
// 结果以百分号开头，后跟任意标志、宽度和精度。
// 未设置的标志、宽度和精度将被省略。
// 该函数允许 [Formatter] 重建触发 Format 调用的原始指令。
func FormatString(state State, verb rune) string {
	var tmp [16]byte // 使用局部缓冲区
	b := append(tmp[:0], '%')
	for _, c := range " +-#0" { // 所有已知标志
		if state.Flag(int(c)) { // 出于历史原因，参数为 int 类型
			b = append(b, byte(c))
		}
	}
	if w, ok := state.Width(); ok {
		b = strconv.AppendInt(b, int64(w), 10)
	}
	if p, ok := state.Precision(); ok {
		b = append(b, '.')
		b = strconv.AppendInt(b, int64(p), 10)
	}
	b = utf8.AppendRune(b, verb)
	return string(b)
}

// 使用简单的 []byte 而非 bytes.Buffer，避免引入大型依赖
type buffer []byte

func (b *buffer) write(p []byte) {
	*b = append(*b, p...)
}

func (b *buffer) writeString(s string) {
	*b = append(*b, s...)
}

func (b *buffer) writeByte(c byte) {
	*b = append(*b, c)
}

func (b *buffer) writeRune(r rune) {
	*b = utf8.AppendRune(*b, r)
}

// pp 用于存储打印器状态，并通过 sync.Pool 复用以避免内存分配
type pp struct {
	buf buffer

	// arg 以 interface{} 类型存储当前项
	arg any

	// value 用于反射值，替代 arg
	value reflect.Value

	// fmt 用于格式化整数、字符串等基础项
	fmt fmt

	// reordered 记录格式化字符串是否使用了参数重排序
	reordered bool
	// goodArgNum 记录最近的重排序指令是否有效
	goodArgNum bool
	// panicking 由 catchPanic 设置，避免无限 panic、recover 递归
	panicking bool
	// erroring 在打印错误字符串时设置，防止调用 handleMethods
	erroring bool
	// wrapErs 在格式化字符串可能包含 %w 动词时设置
	wrapErrs bool
	// wrappedErrs 记录 %w 动词的目标参数
	wrappedErrs []int
}

var ppFree = sync.Pool{
	New: func() any { return new(pp) },
}

// newPrinter 分配一个新的 pp 结构体，或获取一个缓存的结构体
func newPrinter() *pp {
	p := ppFree.Get().(*pp)
	p.panicking = false
	p.erroring = false
	p.wrapErrs = false
	p.fmt.init(&p.buf)
	return p
}

// free 将已使用的 pp 结构体存入 ppFree；避免每次调用都分配内存
func (p *pp) free() {
	// 正确使用 sync.Pool 要求每个元素的内存开销大致相同。
	// 当存储的类型包含可变大小的缓冲区时，为保证这一特性，
	// 我们对放回池中的缓冲区设置最大容量硬限制。
	// 若缓冲区超出限制，我们丢弃缓冲区，仅回收打印器本身。
	//
	// 参见 https://golang.org/issue/23199
	if cap(p.buf) > 64*1024 {
		p.buf = nil
	} else {
		p.buf = p.buf[:0]
	}
	if cap(p.wrappedErrs) > 8 {
		p.wrappedErrs = nil
	}

	p.arg = nil
	p.value = reflect.Value{}
	p.wrappedErrs = p.wrappedErrs[:0]
	ppFree.Put(p)
}

func (p *pp) Width() (wid int, ok bool) { return p.fmt.wid, p.fmt.widPresent }

func (p *pp) Precision() (prec int, ok bool) { return p.fmt.prec, p.fmt.precPresent }

func (p *pp) Flag(b int) bool {
	switch b {
	case '-':
		return p.fmt.minus
	case '+':
		return p.fmt.plus || p.fmt.plusV
	case '#':
		return p.fmt.sharp || p.fmt.sharpV
	case ' ':
		return p.fmt.space
	case '0':
		return p.fmt.zero
	}
	return false
}

// Write 实现 [io.Writer] 接口，以便我们可以（通过 [State]）在 pp 上调用 [Fprintf]，
// 用于自定义动词的递归调用
func (p *pp) Write(b []byte) (ret int, err error) {
	p.buf.write(b)
	return len(b), nil
}

// WriteString 实现 [io.StringWriter] 接口，以便我们可以（通过 state）在 pp 上调用 [io.WriteString]，
// 提升执行效率
func (p *pp) WriteString(s string) (ret int, err error) {
	p.buf.writeString(s)
	return len(s), nil
}

// 以下函数以 'f' 结尾，接收格式化字符串

// Fprintf 根据格式说明符进行格式化，并写入 w
// 返回写入的字节数以及遇到的任何写入错误
func Fprintf(w io.Writer, format string, a ...any) (n int, err error) {
	p := newPrinter()
	p.doPrintf(format, a)
	n, err = w.Write(p.buf)
	p.free()
	return
}

// Printf 根据格式说明符进行格式化，并写入标准输出
// 返回写入的字节数以及遇到的任何写入错误
func Printf(format string, a ...any) (n int, err error) {
	return Fprintf(os.Stdout, format, a...)
}

// Sprintf 根据格式说明符进行格式化，并返回生成的字符串
func Sprintf(format string, a ...any) string {
	p := newPrinter()
	p.doPrintf(format, a)
	s := string(p.buf)
	p.free()
	return s
}

// Appendf 根据格式说明符进行格式化，将结果追加到字节切片，
// 并返回更新后的切片
func Appendf(b []byte, format string, a ...any) []byte {
	p := newPrinter()
	p.doPrintf(format, a)
	b = append(b, p.buf...)
	p.free()
	return b
}

// 以下函数不接收格式化字符串

// Fprint 使用操作数的默认格式进行格式化，并写入 w
// 当两个操作数均非字符串时，在操作数之间添加空格
// 返回写入的字节数以及遇到的任何写入错误
func Fprint(w io.Writer, a ...any) (n int, err error) {
	p := newPrinter()
	p.doPrint(a)
	n, err = w.Write(p.buf)
	p.free()
	return
}

// Print 使用操作数的默认格式进行格式化，并写入标准输出
// 当两个操作数均非字符串时，在操作数之间添加空格
// 返回写入的字节数以及遇到的任何写入错误
func Print(a ...any) (n int, err error) {
	return Fprint(os.Stdout, a...)
}

// Sprint 使用操作数的默认格式进行格式化，并返回生成的字符串
// 当两个操作数均非字符串时，在操作数之间添加空格
func Sprint(a ...any) string {
	p := newPrinter()
	p.doPrint(a)
	s := string(p.buf)
	p.free()
	return s
}

// Append 使用操作数的默认格式进行格式化，将结果追加到字节切片，
// 并返回更新后的切片
// 当两个操作数均非字符串时，在操作数之间添加空格
func Append(b []byte, a ...any) []byte {
	p := newPrinter()
	p.doPrint(a)
	b = append(b, p.buf...)
	p.free()
	return b
}

// 以下函数以 'ln' 结尾，不接收格式化字符串，
// 始终在操作数之间添加空格，并在最后一个操作数后添加换行符

// Fprintln 使用操作数的默认格式进行格式化，并写入 w
// 始终在操作数之间添加空格，并追加换行符
// 返回写入的字节数以及遇到的任何写入错误
func Fprintln(w io.Writer, a ...any) (n int, err error) {
	p := newPrinter()
	p.doPrintln(a)
	n, err = w.Write(p.buf)
	p.free()
	return
}

// Println 使用操作数的默认格式进行格式化，并写入标准输出
// 始终在操作数之间添加空格，并追加换行符
// 返回写入的字节数以及遇到的任何写入错误
func Println(a ...any) (n int, err error) {
	return Fprintln(os.Stdout, a...)
}

// Sprintln 使用操作数的默认格式进行格式化，并返回生成的字符串
// 始终在操作数之间添加空格，并追加换行符
func Sprintln(a ...any) string {
	p := newPrinter()
	p.doPrintln(a)
	s := string(p.buf)
	p.free()
	return s
}

// Appendln 使用操作数的默认格式进行格式化，将结果追加到字节切片，
// 并返回更新后的切片。始终在操作数之间添加空格，并追加换行符
func Appendln(b []byte, a ...any) []byte {
	p := newPrinter()
	p.doPrintln(a)
	b = append(b, p.buf...)
	p.free()
	return b
}

// getField 获取结构体值的第 i 个字段
// 若该字段本身是非空接口，则返回接口内部的值，而非接口本身
func getField(v reflect.Value, i int) reflect.Value {
	val := v.Field(i)
	if val.Kind() == reflect.Interface && !val.IsNil() {
		val = val.Elem()
	}
	return val
}

// tooLarge 报告整数的大小是否过大，
// 不适合用作格式化宽度或精度
func tooLarge(x int) bool {
	const max int = 1e6
	return x > max || x < -max
}

// parsenum 将 ASCII 转换为整数。若不存在数字，num 为 0（且 isnum 为 false）
func parsenum(s string, start, end int) (num int, isnum bool, newi int) {
	if start >= end {
		return 0, false, end
	}
	for newi = start; newi < end && '0' <= s[newi] && s[newi] <= '9'; newi++ {
		if tooLarge(num) {
			return 0, false, end // 溢出；极可能是超长数字
		}
		num = num*10 + int(s[newi]-'0')
		isnum = true
	}
	return
}

func (p *pp) unknownType(v reflect.Value) {
	if !v.IsValid() {
		p.buf.writeString(nilAngleString)
		return
	}
	p.buf.writeByte('?')
	p.buf.writeString(v.Type().String())
	p.buf.writeByte('?')
}

func (p *pp) badVerb(verb rune) {
	p.erroring = true
	p.buf.writeString(percentBangString)
	p.buf.writeRune(verb)
	p.buf.writeByte('(')
	switch {
	case p.arg != nil:
		p.buf.writeString(reflect.TypeOf(p.arg).String())
		p.buf.writeByte('=')
		p.printArg(p.arg, 'v')
	case p.value.IsValid():
		p.buf.writeString(p.value.Type().String())
		p.buf.writeByte('=')
		p.printValue(p.value, 'v', 0)
	default:
		p.buf.writeString(nilAngleString)
	}
	p.buf.writeByte(')')
	p.erroring = false
}

func (p *pp) fmtBool(v bool, verb rune) {
	switch verb {
	case 't', 'v':
		p.fmt.fmtBoolean(v)
	default:
		p.badVerb(verb)
	}
}

// fmt0x64 将 uint64 格式化为十六进制，并根据要求添加 0x 前缀，
// 通过临时设置 sharp 标志实现
func (p *pp) fmt0x64(v uint64, leading0x bool) {
	sharp := p.fmt.sharp
	p.fmt.sharp = leading0x
	p.fmt.fmtInteger(v, 16, unsigned, 'v', ldigits)
	p.fmt.sharp = sharp
}

// fmtInteger 格式化有符号或无符号整数
func (p *pp) fmtInteger(v uint64, isSigned bool, verb rune) {
	switch verb {
	case 'v':
		if p.fmt.sharpV && !isSigned {
			p.fmt0x64(v, true)
		} else {
			p.fmt.fmtInteger(v, 10, isSigned, verb, ldigits)
		}
	case 'd':
		p.fmt.fmtInteger(v, 10, isSigned, verb, ldigits)
	case 'b':
		p.fmt.fmtInteger(v, 2, isSigned, verb, ldigits)
	case 'o', 'O':
		p.fmt.fmtInteger(v, 8, isSigned, verb, ldigits)
	case 'x':
		p.fmt.fmtInteger(v, 16, isSigned, verb, ldigits)
	case 'X':
		p.fmt.fmtInteger(v, 16, isSigned, verb, udigits)
	case 'c':
		p.fmt.fmtC(v)
	case 'q':
		p.fmt.fmtQc(v)
	case 'U':
		p.fmt.fmtUnicode(v)
	default:
		p.badVerb(verb)
	}
}

// fmtFloat 格式化浮点数。每个动词的默认精度
// 作为 fmt_float 调用的最后一个参数指定
func (p *pp) fmtFloat(v float64, size int, verb rune) {
	switch verb {
	case 'v':
		p.fmt.fmtFloat(v, size, 'g', -1)
	case 'b', 'g', 'G', 'x', 'X':
		p.fmt.fmtFloat(v, size, verb, -1)
	case 'f', 'e', 'E':
		p.fmt.fmtFloat(v, size, verb, 6)
	case 'F':
		p.fmt.fmtFloat(v, size, 'f', 6)
	default:
		p.badVerb(verb)
	}
}

// fmtComplex 格式化复数 v，其中
// r = real(v)，j = imag(v)，格式为 (r+ji)
// 使用 fmtFloat 对 r 和 j 进行格式化
func (p *pp) fmtComplex(v complex128, size int, verb rune) {
	// 确保在调用 fmtFloat 前检测到所有不支持的动词，
	// 避免生成错误的错误字符串
	switch verb {
	case 'v', 'b', 'g', 'G', 'x', 'X', 'f', 'F', 'e', 'E':
		oldPlus := p.fmt.plus
		p.buf.writeByte('(')
		p.fmtFloat(real(v), size/2, verb)
		// 虚部始终带有符号
		p.fmt.plus = true
		p.fmtFloat(imag(v), size/2, verb)
		p.buf.writeString("i)")
		p.fmt.plus = oldPlus
	default:
		p.badVerb(verb)
	}
}

func (p *pp) fmtString(v string, verb rune) {
	switch verb {
	case 'v':
		if p.fmt.sharpV {
			p.fmt.fmtQ(v)
		} else {
			p.fmt.fmtS(v)
		}
	case 's':
		p.fmt.fmtS(v)
	case 'x':
		p.fmt.fmtSx(v, ldigits)
	case 'X':
		p.fmt.fmtSx(v, udigits)
	case 'q':
		p.fmt.fmtQ(v)
	default:
		p.badVerb(verb)
	}
}

func (p *pp) fmtBytes(v []byte, verb rune, typeString string) {
	switch verb {
	case 'v', 'd':
		if p.fmt.sharpV {
			p.buf.writeString(typeString)
			if v == nil {
				p.buf.writeString(nilParenString)
				return
			}
			p.buf.writeByte('{')
			for i, c := range v {
				if i > 0 {
					p.buf.writeString(commaSpaceString)
				}
				p.fmt0x64(uint64(c), true)
			}
			p.buf.writeByte('}')
		} else {
			p.buf.writeByte('[')
			for i, c := range v {
				if i > 0 {
					p.buf.writeByte(' ')
				}
				p.fmt.fmtInteger(uint64(c), 10, unsigned, verb, ldigits)
			}
			p.buf.writeByte(']')
		}
	case 's':
		p.fmt.fmtBs(v)
	case 'x':
		p.fmt.fmtBx(v, ldigits)
	case 'X':
		p.fmt.fmtBx(v, udigits)
	case 'q':
		p.fmt.fmtQ(string(v))
	default:
		p.printValue(reflect.ValueOf(v), verb, 0)
	}
}

func (p *pp) fmtPointer(value reflect.Value, verb rune) {
	var u uintptr
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		u = uintptr(value.UnsafePointer())
	default:
		p.badVerb(verb)
		return
	}

	switch verb {
	case 'v':
		if p.fmt.sharpV {
			p.buf.writeByte('(')
			p.buf.writeString(value.Type().String())
			p.buf.writeString(")(")
			if u == 0 {
				p.buf.writeString(nilString)
			} else {
				p.fmt0x64(uint64(u), true)
			}
			p.buf.writeByte(')')
		} else {
			if u == 0 {
				p.fmt.padString(nilAngleString)
			} else {
				p.fmt0x64(uint64(u), !p.fmt.sharp)
			}
		}
	case 'p':
		p.fmt0x64(uint64(u), !p.fmt.sharp)
	case 'b', 'o', 'd', 'x', 'X':
		p.fmtInteger(uint64(u), unsigned, verb)
	default:
		p.badVerb(verb)
	}
}

func (p *pp) catchPanic(arg any, verb rune, method string) {
	if err := recover(); err != nil {
		// 若为空指针，直接输出 "<nil>"。最可能的原因是
		// Stringer 未做空值防护，或值接收器使用了空指针，
		// 无论哪种情况，"<nil>" 都是合适的输出
		if v := reflect.ValueOf(arg); v.Kind() == reflect.Pointer && v.IsNil() {
			p.buf.writeString(nilAngleString)
			return
		}
		// 否则打印简洁的 panic 信息。大多数情况下，
		// panic 值本身可以正常打印
		if p.panicking {
			// 嵌套 panic；printArg 中的递归无法成功执行
			panic(err)
		}

		oldFlags := p.fmt.fmtFlags
		// 该输出使用默认行为
		p.fmt.clearflags()

		p.buf.writeString(percentBangString)
		p.buf.writeRune(verb)
		p.buf.writeString(panicString)
		p.buf.writeString(method)
		p.buf.writeString(" method: ")
		p.panicking = true
		p.printArg(err, 'v')
		p.panicking = false
		p.buf.writeByte(')')

		p.fmt.fmtFlags = oldFlags
	}
}

func (p *pp) handleMethods(verb rune) (handled bool) {
	if p.erroring {
		return
	}
	if verb == 'w' {
		// %w 仅能与 Errorf 配合使用，且参数必须为 error 类型，否则无效
		_, ok := p.arg.(error)
		if !ok || !p.wrapErrs {
			p.badVerb(verb)
			return true
		}
		// 若参数实现了 Formatter，将动词 'v' 传递给它
		verb = 'v'
	}

	// 是否为 Formatter？
	if formatter, ok := p.arg.(Formatter); ok {
		handled = true
		defer p.catchPanic(p.arg, verb, "Format")
		formatter.Format(p, verb)
		return
	}

	// 若需要 Go 语法格式且参数支持该格式，立即处理
	if p.fmt.sharpV {
		if stringer, ok := p.arg.(GoStringer); ok {
			handled = true
			defer p.catchPanic(p.arg, verb, "GoString")
			// 直接打印 GoString 的结果，不加修饰
			p.fmt.fmtS(stringer.GoString())
			return
		}
	} else {
		// 若格式接受字符串，检查值是否实现了任意字符串相关接口
		// Println 等方法会将动词设为 %v，该动词支持字符串格式化
		switch verb {
		case 'v', 's', 'x', 'X', 'q':
			// 是否为 error 或 Stringer？
			// 函数体中的重复代码是必要的：
			// 设置 handled 和延迟调用 catchPanic
			// 必须在调用方法前执行
			switch v := p.arg.(type) {
			case error:
				handled = true
				defer p.catchPanic(p.arg, verb, "Error")
				p.fmtString(v.Error(), verb)
				return

			case Stringer:
				handled = true
				defer p.catchPanic(p.arg, verb, "String")
				p.fmtString(v.String(), verb)
				return
			}
		}
	}
	return false
}

func (p *pp) printArg(arg any, verb rune) {
	p.arg = arg
	p.value = reflect.Value{}

	if arg == nil {
		switch verb {
		case 'T', 'v':
			p.fmt.padString(nilAngleString)
		default:
			p.badVerb(verb)
		}
		return
	}

	// 特殊处理规则
	// %T（值的类型）和 %p（值的地址）为特殊动词，始终优先处理
	switch verb {
	case 'T':
		p.fmt.fmtS(reflect.TypeOf(arg).String())
		return
	case 'p':
		p.fmtPointer(reflect.ValueOf(arg), 'p')
		return
	}

	// 部分类型无需反射即可处理
	switch f := arg.(type) {
	case bool:
		p.fmtBool(f, verb)
	case float32:
		p.fmtFloat(float64(f), 32, verb)
	case float64:
		p.fmtFloat(f, 64, verb)
	case complex64:
		p.fmtComplex(complex128(f), 64, verb)
	case complex128:
		p.fmtComplex(f, 128, verb)
	case int:
		p.fmtInteger(uint64(f), signed, verb)
	case int8:
		p.fmtInteger(uint64(f), signed, verb)
	case int16:
		p.fmtInteger(uint64(f), signed, verb)
	case int32:
		p.fmtInteger(uint64(f), signed, verb)
	case int64:
		p.fmtInteger(uint64(f), signed, verb)
	case uint:
		p.fmtInteger(uint64(f), unsigned, verb)
	case uint8:
		p.fmtInteger(uint64(f), unsigned, verb)
	case uint16:
		p.fmtInteger(uint64(f), unsigned, verb)
	case uint32:
		p.fmtInteger(uint64(f), unsigned, verb)
	case uint64:
		p.fmtInteger(f, unsigned, verb)
	case uintptr:
		p.fmtInteger(uint64(f), unsigned, verb)
	case string:
		p.fmtString(f, verb)
	case []byte:
		p.fmtBytes(f, verb, "[]byte")
	case reflect.Value:
		// 处理包含特殊方法且可提取的值
		// 因为 printValue 在深度 0 时不处理这类值
		if f.IsValid() && f.CanInterface() {
			p.arg = f.Interface()
			if p.handleMethods(verb) {
				return
			}
		}
		p.printValue(f, verb, 0)
	default:
		// 若类型非基础类型，可能包含方法
		if !p.handleMethods(verb) {
			// 需使用反射，因为该类型没有可用于格式化的接口方法
			p.printValue(reflect.ValueOf(f), verb, 0)
		}
	}
}

// printValue 与 printArg 类似，但接收反射值而非 interface{} 值
// 不处理 'p' 和 'T' 动词，这些动词已由 printArg 提前处理
func (p *pp) printValue(value reflect.Value, verb rune, depth int) {
	// 若未被 printArg 处理（depth == 0），处理包含特殊方法的值
	if depth > 0 && value.IsValid() && value.CanInterface() {
		p.arg = value.Interface()
		if p.handleMethods(verb) {
			return
		}
	}
	p.arg = nil
	p.value = value

	switch f := value; value.Kind() {
	case reflect.Invalid:
		if depth == 0 {
			p.buf.writeString(invReflectString)
		} else {
			switch verb {
			case 'v':
				p.buf.writeString(nilAngleString)
			default:
				p.badVerb(verb)
			}
		}
	case reflect.Bool:
		p.fmtBool(f.Bool(), verb)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		p.fmtInteger(uint64(f.Int()), signed, verb)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		p.fmtInteger(f.Uint(), unsigned, verb)
	case reflect.Float32:
		p.fmtFloat(f.Float(), 32, verb)
	case reflect.Float64:
		p.fmtFloat(f.Float(), 64, verb)
	case reflect.Complex64:
		p.fmtComplex(f.Complex(), 64, verb)
	case reflect.Complex128:
		p.fmtComplex(f.Complex(), 128, verb)
	case reflect.String:
		p.fmtString(f.String(), verb)
	case reflect.Map:
		if p.fmt.sharpV {
			p.buf.writeString(f.Type().String())
			if f.IsNil() {
				p.buf.writeString(nilParenString)
				return
			}
			p.buf.writeByte('{')
		} else {
			p.buf.writeString(mapString)
		}
		sorted := fmtsort.Sort(f)
		for i, m := range sorted {
			if i > 0 {
				if p.fmt.sharpV {
					p.buf.writeString(commaSpaceString)
				} else {
					p.buf.writeByte(' ')
				}
			}
			p.printValue(m.Key, verb, depth+1)
			p.buf.writeByte(':')
			p.printValue(m.Value, verb, depth+1)
		}
		if p.fmt.sharpV {
			p.buf.writeByte('}')
		} else {
			p.buf.writeByte(']')
		}
	case reflect.Struct:
		if p.fmt.sharpV {
			p.buf.writeString(f.Type().String())
		}
		p.buf.writeByte('{')
		for i := 0; i < f.NumField(); i++ {
			if i > 0 {
				if p.fmt.sharpV {
					p.buf.writeString(commaSpaceString)
				} else {
					p.buf.writeByte(' ')
				}
			}
			if p.fmt.plusV || p.fmt.sharpV {
				if name := f.Type().Field(i).Name; name != "" {
					p.buf.writeString(name)
					p.buf.writeByte(':')
				}
			}
			p.printValue(getField(f, i), verb, depth+1)
		}
		p.buf.writeByte('}')
	case reflect.Interface:
		value := f.Elem()
		if !value.IsValid() {
			if p.fmt.sharpV {
				p.buf.writeString(f.Type().String())
				p.buf.writeString(nilParenString)
			} else {
				p.buf.writeString(nilAngleString)
			}
		} else {
			p.printValue(value, verb, depth+1)
		}
	case reflect.Array, reflect.Slice:
		switch verb {
		case 's', 'q', 'x', 'X':
			// 对上述动词，特殊处理字节和 uint8 切片/数组
			t := f.Type()
			if t.Elem().Kind() == reflect.Uint8 {
				var bytes []byte
				if f.Kind() == reflect.Slice || f.CanAddr() {
					bytes = f.Bytes()
				} else {
					// 处理数组场景：不可取地址的数组无法调用 Bytes()，
					// 因此手动构建切片。该场景罕见，
					// 若反射能提供更多支持会更好
					bytes = make([]byte, f.Len())
					for i := range bytes {
						bytes[i] = byte(f.Index(i).Uint())
					}
				}
				p.fmtBytes(bytes, verb, t.String())
				return
			}
		}
		if p.fmt.sharpV {
			p.buf.writeString(f.Type().String())
			if f.Kind() == reflect.Slice && f.IsNil() {
				p.buf.writeString(nilParenString)
				return
			}
			p.buf.writeByte('{')
			for i := 0; i < f.Len(); i++ {
				if i > 0 {
					p.buf.writeString(commaSpaceString)
				}
				p.printValue(f.Index(i), verb, depth+1)
			}
			p.buf.writeByte('}')
		} else {
			p.buf.writeByte('[')
			for i := 0; i < f.Len(); i++ {
				if i > 0 {
					p.buf.writeByte(' ')
				}
				p.printValue(f.Index(i), verb, depth+1)
			}
			p.buf.writeByte(']')
		}
	case reflect.Pointer:
		// 指向数组、切片、结构体或映射的指针？顶层允许，
		// 嵌套则禁止（避免循环）
		if depth == 0 && f.UnsafePointer() != nil {
			switch a := f.Elem(); a.Kind() {
			case reflect.Array, reflect.Slice, reflect.Struct, reflect.Map:
				p.buf.writeByte('&')
				p.printValue(a, verb, depth+1)
				return
			}
		}
		fallthrough
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		p.fmtPointer(f, verb)
	default:
		p.unknownType(f)
	}
}

// intFromArg 获取 a 中第 argNumth 个元素。返回时，isInt 报告参数是否为整数类型
func intFromArg(a []any, argNum int) (num int, isInt bool, newArgNum int) {
	newArgNum = argNum
	if argNum < len(a) {
		num, isInt = a[argNum].(int) // 绝大多数情况都有效
		if !isInt {
			// 额外处理逻辑
			switch v := reflect.ValueOf(a[argNum]); v.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				n := v.Int()
				if int64(int(n)) == n {
					num = int(n)
					isInt = true
				}
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				n := v.Uint()
				if int64(n) >= 0 && uint64(int(n)) == n {
					num = int(n)
					isInt = true
				}
			default:
				// 保持默认值 0, false
			}
		}
		newArgNum = argNum + 1
		if tooLarge(num) {
			num = 0
			isInt = false
		}
	}
	return
}

// parseArgNumber 返回方括号内数字的值减 1
// （显式参数编号从 1 开始，而我们使用从 0 开始的索引）
// 已知 format[0] 位置为左括号
// 返回值为索引、需要消耗的字节数（至右括号，若存在）以及数字是否解析成功
// 若无右括号，消耗字节数为 1
func parseArgNumber(format string) (index int, wid int, ok bool) {
	// 至少需要 3 个字节：[n]
	if len(format) < 3 {
		return 0, 1, false
	}

	// 查找右括号
	for i := 1; i < len(format); i++ {
		if format[i] == ']' {
			width, ok, newi := parsenum(format, 1, i)
			if !ok || newi != i {
				return 0, i + 1, false
			}
			return width - 1, i + 1, true // 参数编号从 1 开始，跳过括号
		}
	}
	return 0, 1, false
}

// argNumber 返回下一个待求值的参数，可为传入的 argNum 值，
// 或 format[i:] 起始的方括号整数。同时返回 i 的新值，
// 即下一个待处理格式化字节的索引
func (p *pp) argNumber(argNum int, format string, i int, numArgs int) (newArgNum, newi int, found bool) {
	if len(format) <= i || format[i] != '[' {
		return argNum, i, false
	}
	p.reordered = true
	index, wid, ok := parseArgNumber(format[i:])
	if ok && 0 <= index && index < numArgs {
		return index, i + wid, true
	}
	p.goodArgNum = false
	return argNum, i + wid, ok
}

func (p *pp) badArgNum(verb rune) {
	p.buf.writeString(percentBangString)
	p.buf.writeRune(verb)
	p.buf.writeString(badIndexString)
}

func (p *pp) missingArg(verb rune) {
	p.buf.writeString(percentBangString)
	p.buf.writeRune(verb)
	p.buf.writeString(missingString)
}

func (p *pp) doPrintf(format string, a []any) {
	end := len(format)
	argNum := 0         // 每个非平凡格式处理一个参数
	afterIndex := false // 格式化字符串中上一项为 [3] 这类参数索引
	p.reordered = false
formatLoop:
	for i := 0; i < end; {
		p.goodArgNum = true
		lasti := i
		for i < end && format[i] != '%' {
			i++
		}
		if i > lasti {
			p.buf.writeString(format[lasti:i])
		}
		if i >= end {
			// 格式化字符串处理完毕
			break
		}

		// 处理一个动词
		i++

		// 处理标志
		p.fmt.clearflags()
	simpleFormat:
		for ; i < end; i++ {
			c := format[i]
			switch c {
			case '#':
				p.fmt.sharp = true
			case '0':
				p.fmt.zero = true
			case '+':
				p.fmt.plus = true
			case '-':
				p.fmt.minus = true
			case ' ':
				p.fmt.space = true
			default:
				// 快速处理无精度、无宽度、无参数索引的
				// ASCII 小写简单动词通用场景
				if 'a' <= c && c <= 'z' && argNum < len(a) {
					switch c {
					case 'w':
						p.wrappedErrs = append(p.wrappedErrs, argNum)
						fallthrough
					case 'v':
						// Go 语法格式
						p.fmt.sharpV = p.fmt.sharp
						p.fmt.sharp = false
						// 结构体字段格式
						p.fmt.plusV = p.fmt.plus
						p.fmt.plus = false
					}
					p.printArg(a[argNum], rune(c))
					argNum++
					i++
					continue formatLoop
				}
				// 格式比简单标志+动词更复杂，或格式非法
				break simpleFormat
			}
		}

		// 处理显式参数索引
		argNum, i, afterIndex = p.argNumber(argNum, format, i, len(a))

		// 处理宽度
		if i < end && format[i] == '*' {
			i++
			p.fmt.wid, p.fmt.widPresent, argNum = intFromArg(a, argNum)

			if !p.fmt.widPresent {
				p.buf.writeString(badWidthString)
			}

			// 宽度为负数，取其绝对值并设置减号标志
			if p.fmt.wid < 0 {
				p.fmt.wid = -p.fmt.wid
				p.fmt.minus = true
				p.fmt.zero = false // 禁止右侧补零
			}
			afterIndex = false
		} else {
			p.fmt.wid, p.fmt.widPresent, i = parsenum(format, i, end)
			if afterIndex && p.fmt.widPresent { // "%[3]2d"
				p.goodArgNum = false
			}
		}

		// 处理精度
		if i+1 < end && format[i] == '.' {
			i++
			if afterIndex { // "%[3].2d"
				p.goodArgNum = false
			}
			argNum, i, afterIndex = p.argNumber(argNum, format, i, len(a))
			if i < end && format[i] == '*' {
				i++
				p.fmt.prec, p.fmt.precPresent, argNum = intFromArg(a, argNum)
				// 负精度无意义
				if p.fmt.prec < 0 {
					p.fmt.prec = 0
					p.fmt.precPresent = false
				}
				if !p.fmt.precPresent {
					p.buf.writeString(badPrecString)
				}
				afterIndex = false
			} else {
				p.fmt.prec, p.fmt.precPresent, i = parsenum(format, i, end)
				if !p.fmt.precPresent {
					p.fmt.prec = 0
					p.fmt.precPresent = true
				}
			}
		}

		if !afterIndex {
			argNum, i, afterIndex = p.argNumber(argNum, format, i, len(a))
		}

		if i >= end {
			p.buf.writeString(noVerbString)
			break
		}

		verb, size := utf8.DecodeRuneInString(format[i:])
		i += size

		switch {
		case verb == '%': // 百分号不占用操作数，忽略宽度和精度
			p.buf.writeByte('%')
		case !p.goodArgNum:
			p.badArgNum(verb)
		case argNum >= len(a): // 无剩余参数供当前动词打印
			p.missingArg(verb)
		case verb == 'w':
			p.wrappedErrs = append(p.wrappedErrs, argNum)
			fallthrough
		case verb == 'v':
			// Go 语法格式
			p.fmt.sharpV = p.fmt.sharp
			p.fmt.sharp = false
			// 结构体字段格式
			p.fmt.plusV = p.fmt.plus
			p.fmt.plus = false
			fallthrough
		default:
			p.printArg(a[argNum], verb)
			argNum++
		}
	}

	// 检查是否存在多余参数，除非调用乱序访问参数
	// 乱序场景下检测所有参数是否用完成本过高，且未用完也可接受
	if !p.reordered && argNum < len(a) {
		p.fmt.clearflags()
		p.buf.writeString(extraString)
		for i, arg := range a[argNum:] {
			if i > 0 {
				p.buf.writeString(commaSpaceString)
			}
			if arg == nil {
				p.buf.writeString(nilAngleString)
			} else {
				p.buf.writeString(reflect.TypeOf(arg).String())
				p.buf.writeByte('=')
				p.printArg(arg, 'v')
			}
		}
		p.buf.writeByte(')')
	}
}

func (p *pp) doPrint(a []any) {
	prevString := false
	for argNum, arg := range a {
		isString := arg != nil && reflect.TypeOf(arg).Kind() == reflect.String
		// 两个非字符串参数之间添加空格
		if argNum > 0 && !isString && !prevString {
			p.buf.writeByte(' ')
		}
		p.printArg(arg, 'v')
		prevString = isString
	}
}

// doPrintln 与 doPrint 类似，但始终在参数之间添加空格，
// 并在最后一个参数后添加换行符
func (p *pp) doPrintln(a []any) {
	for argNum, arg := range a {
		if argNum > 0 {
			p.buf.writeByte(' ')
		}
		p.printArg(arg, 'v')
	}
	p.buf.writeByte('\n')
}
