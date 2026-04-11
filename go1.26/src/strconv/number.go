// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strconv

import (
	"errors"
	"internal/strconv"
	"internal/stringslite"
)

// IntSize 是 int 或 uint 值的位大小。
const IntSize = strconv.IntSize

// ParseBool 返回字符串表示的布尔值。
// 它接受 1、t、T、TRUE、true、True、0、f、F、FALSE、false、False。
// 任何其他值都会返回错误。
func ParseBool(str string) (bool, error) {
	x, err := strconv.ParseBool(str)
	if err != nil {
		return x, toError("ParseBool", str, 0, 0, err)
	}
	return x, nil
}

// FormatBool 根据 b 的值返回 "true" 或 "false"。
func FormatBool(b bool) string {
	return strconv.FormatBool(b)
}

// AppendBool 根据 b 的值将 "true" 或 "false" 追加到 dst 并返回扩展后的缓冲区。
func AppendBool(dst []byte, b bool) []byte {
	return strconv.AppendBool(dst, b)
}

// ParseComplex 将字符串 s 转换为复数，精度由 bitSize 指定：
// 64 对应 complex64，128 对应 complex128。
// 当 bitSize=64 时，结果仍为 complex128 类型，但可在不改变其值的情况下转换为 complex64。
//
// s 表示的数字必须为 N、Ni 或 N±Ni 形式，其中 N 代表 [ParseFloat] 可识别的浮点数，
// i 为虚部。若第二个 N 无符号，则两部分之间必须如 ± 所示使用 + 号。
// 若第二个 N 为 NaN，则仅接受 + 号。
// 该形式可加括号，且不能包含任何空格。
// 结果复数由 ParseFloat 转换后的两部分组成。
//
// ParseComplex 返回的错误具体类型为 [*NumError]，且包含 err.Num = s。
//
// 若 s 语法格式不正确，ParseComplex 返回 err.Err = ErrSyntax。
//
// 若 s 语法格式正确，但任一部分超出对应大小最大浮点数的 1/2 ULP 范围，
// ParseComplex 返回 err.Err = ErrRange，且对应部分 c = ±Inf。
func ParseComplex(s string, bitSize int) (complex128, error) {
	x, err := strconv.ParseComplex(s, bitSize)
	if err != nil {
		return x, toError("ParseComplex", s, 0, bitSize, err)
	}
	return x, nil
}

// ParseFloat 将字符串 s 转换为浮点数，精度由 bitSize 指定：
// 32 对应 float32，64 对应 float64。
// 当 bitSize=32 时，结果仍为 float64 类型，但可在不改变其值的情况下转换为 float32。
//
// ParseFloat 接受 Go 语法中 [floating-point literals] 定义的十进制和十六进制浮点数。
// 若 s 格式正确且接近有效浮点数，ParseFloat 使用 IEEE754 无偏舍入返回最接近的浮点数。
// （仅当十六进制表示的位数超过尾数可容纳的位数时，解析十六进制浮点值才会进行舍入。）
//
// ParseFloat 返回的错误具体类型为 *NumError，且包含 err.Num = s。
//
// 若 s 语法格式不正确，ParseFloat 返回 err.Err = ErrSyntax。
//
// 若 s 语法格式正确，但超出给定大小最大浮点数的 1/2 ULP 范围，
// ParseFloat 返回 f = ±Inf，err.Err = ErrRange。
//
// ParseFloat 将字符串 "NaN" 以及（可能带符号的）字符串 "Inf" 和 "Infinity"
// 识别为对应的特殊浮点值。匹配时忽略大小写。
//
// [floating-point literals]: https://go.dev/ref/spec#Floating-point_literals
func ParseFloat(s string, bitSize int) (float64, error) {
	x, err := strconv.ParseFloat(s, bitSize)
	if err != nil {
		return x, toError("ParseFloat", s, 0, bitSize, err)
	}
	return x, nil
}

// ParseUint 与 [ParseInt] 类似，但用于无符号数。
//
// 不允许有符号前缀。
func ParseUint(s string, base int, bitSize int) (uint64, error) {
	x, err := strconv.ParseUint(s, base, bitSize)
	if err != nil {
		return x, toError("ParseUint", s, base, bitSize, err)
	}
	return x, nil
}

// ParseInt 将字符串 s 按给定进制（0、2 到 36）和位大小（0 到 64）解释，并返回对应的值 i。
//
// 字符串可以以 "+" 或 "-" 开头。
//
// 若 base 参数为 0，则真正的进制由符号（若有）后的字符串前缀隐含：
// "0b" 对应 2，"0" 或 "0o" 对应 8，"0x" 对应 16，否则为 10。
// 此外，仅当 base 参数为 0 时，允许使用 Go 语法中 [integer literals] 定义的下划线字符。
//
// bitSize 参数指定结果必须适配的整数类型。
// 位大小 0、8、16、32 和 64 分别对应 int、int8、int16、int32 和 int64。
// 若 bitSize 小于 0 或大于 64，则返回错误。
//
// ParseInt 返回的错误具体类型为 [*NumError]，且包含 err.Num = s。
// 若 s 为空或包含无效数字，err.Err = [ErrSyntax]，返回值为 0；
// 若 s 对应的值无法用给定位大小的有符号整数表示，err.Err = [ErrRange]，
// 返回值为对应位大小和符号的最大幅度整数。
//
// [integer literals]: https://go.dev/ref/spec#Integer_literals
func ParseInt(s string, base int, bitSize int) (i int64, err error) {
	x, err := strconv.ParseInt(s, base, bitSize)
	if err != nil {
		return x, toError("ParseInt", s, base, bitSize, err)
	}
	return x, nil
}

// Atoi 等价于 ParseInt(s, 10, 0)，并转换为 int 类型。
func Atoi(s string) (int, error) {
	x, err := strconv.Atoi(s)
	if err != nil {
		return x, toError("Atoi", s, 0, 0, err)
	}
	return x, nil
}

// FormatComplex 将复数 c 转换为 (a+bi) 形式的字符串，
// 其中 a 和 b 为实部和虚部，根据格式 fmt 和精度 prec 格式化。
//
// 格式 fmt 和精度 prec 的含义与 [FormatFloat] 中相同。
// 它假设原始值是从 bitSize 位的复数值获得的，并对结果进行舍入，
// bitSize 对于 complex64 必须为 64，对于 complex128 必须为 128。
func FormatComplex(c complex128, fmt byte, prec, bitSize int) string {
	return strconv.FormatComplex(c, fmt, prec, bitSize)
}

// FormatFloat 根据格式 fmt 和精度 prec 将浮点数 f 转换为字符串。
// 它假设原始值是从 bitSize 位的浮点值（32 对应 float32，64 对应 float64）获得的，并对结果进行舍入。
//
// 格式 fmt 为以下之一：
//   - 'b'（-ddddp±ddd，二进制指数），
//   - 'e'（-d.dddde±dd，十进制指数），
//   - 'E'（-d.ddddE±dd，十进制指数），
//   - 'f'（-ddd.dddd，无指数），
//   - 'g'（大指数用 'e'，否则用 'f'），
//   - 'G'（大指数用 'E'，否则用 'f'），
//   - 'x'（-0xd.ddddp±ddd，十六进制小数和二进制指数），或
//   - 'X'（-0Xd.ddddP±ddd，十六进制小数和二进制指数）。
//
// 精度 prec 控制 'e'、'E'、'f'、'g'、'G'、'x' 和 'X' 格式打印的位数（不包括指数）。
// 对于 'e'、'E'、'f'、'x' 和 'X'，它是小数点后的位数。
// 对于 'g' 和 'G'，它是有效数字的最大位数（尾随零被移除）。
// 特殊精度 -1 使用必要的最少位数，以使 ParseFloat 能精确返回 f。
// 指数写为十进制整数；
// 对于 'b' 以外的所有格式，它至少为两位数字。
func FormatFloat(f float64, fmt byte, prec, bitSize int) string {
	return strconv.FormatFloat(f, fmt, prec, bitSize)
}

// AppendFloat 将 [FormatFloat] 生成的浮点数 f 的字符串形式追加到 dst 并返回扩展后的缓冲区。
func AppendFloat(dst []byte, f float64, fmt byte, prec, bitSize int) []byte {
	return strconv.AppendFloat(dst, f, fmt, prec, bitSize)
}

// FormatUint 返回 i 在给定进制下的字符串表示，进制范围为 2 <= base <= 36。
// 结果使用小写字母 'a' 到 'z' 表示 >= 10 的数字值。
func FormatUint(i uint64, base int) string {
	return strconv.FormatUint(i, base)
}

// FormatInt 返回 i 在给定进制下的字符串表示，进制范围为 2 <= base <= 36。
// 结果使用小写字母 'a' 到 'z' 表示 >= 10 的数字值。
func FormatInt(i int64, base int) string {
	return strconv.FormatInt(i, base)
}

// Itoa 等价于 [FormatInt](int64(i), 10)。
func Itoa(i int) string {
	return strconv.Itoa(i)
}

// AppendInt 将 [FormatInt] 生成的整数 i 的字符串形式追加到 dst 并返回扩展后的缓冲区。
func AppendInt(dst []byte, i int64, base int) []byte {
	return strconv.AppendInt(dst, i, base)
}

// AppendUint 将 [FormatUint] 生成的无符号整数 i 的字符串形式追加到 dst 并返回扩展后的缓冲区。
func AppendUint(dst []byte, i uint64, base int) []byte {
	return strconv.AppendUint(dst, i, base)
}

// toError 将 internal/strconv.Error 转换为本包 API 保证的错误。
func toError(fn, s string, base, bitSize int, err error) error {
	switch err {
	case strconv.ErrSyntax:
		return syntaxError(fn, s)
	case strconv.ErrRange:
		return rangeError(fn, s)
	case strconv.ErrBase:
		return baseError(fn, s, base)
	case strconv.ErrBitSize:
		return bitSizeError(fn, s, bitSize)
	}
	return err
}

// ErrRange 表示值超出目标类型的范围。
var ErrRange = errors.New("value out of range")

// ErrSyntax 表示值不具有目标类型的正确语法。
var ErrSyntax = errors.New("invalid syntax")

// NumError 记录失败的转换。
type NumError struct {
	Func string // 失败的函数（ParseBool、ParseInt、ParseUint、ParseFloat、ParseComplex）
	Num  string // 输入
	Err  error  // 转换失败的原因（例如 ErrRange、ErrSyntax 等）
}

func (e *NumError) Error() string {
	return "strconv." + e.Func + ": " + "parsing " + Quote(e.Num) + ": " + e.Err.Error()
}

func (e *NumError) Unwrap() error { return e.Err }

// 所有 ParseXXX 函数都允许输入字符串逃逸到错误值中。
// 这会影响 strconv.ParseXXX(string(b)) 调用（其中 b 为 []byte），因为从 []byte 转换必须在堆上分配字符串。
// 如果我们假设错误不频繁，那么可以通过先复制输入来避免将输入逃逸回输出。
// 这允许编译器在大多数 []byte 到字符串的转换中调用 strconv.ParseXXX 而无需堆分配，因为它现在可以证明字符串不会逃逸出 Parse。

func syntaxError(fn, str string) *NumError {
	return &NumError{fn, stringslite.Clone(str), ErrSyntax}
}

func rangeError(fn, str string) *NumError {
	return &NumError{fn, stringslite.Clone(str), ErrRange}
}

func baseError(fn, str string, base int) *NumError {
	return &NumError{fn, stringslite.Clone(str), errors.New("invalid base " + Itoa(base))}
}

func bitSizeError(fn, str string, bitSize int) *NumError {
	return &NumError{fn, stringslite.Clone(str), errors.New("invalid bit size " + Itoa(bitSize))}
}
