// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package driver

import (
	"fmt"
	"reflect"
	"strconv"
	"time"
)

// ValueConverter 是提供 ConvertValue 方法的接口。
//
// driver 包提供了 ValueConverter 的各种实现，
// 以提供驱动程序之间一致的转换实现。
// ValueConverters 有若干用途：
//
//   - 将从 sql 包提供的 [Value] 类型转换为数据库表
//     的特定列类型，并确保它合适，例如确保某个 int64
//     能放入表的 uint16 列中。
//
//   - 将数据库给出的值转换为一种
//     driver [Value] 类型。
//
//   - 由 [database/sql] 包使用，用于在扫描中将驱动的 [Value] 类型
//     转换为用户的类型。
type ValueConverter interface {
	// ConvertValue 将值转换为 driver Value。
	ConvertValue(v any) (Value, error)
}

// Valuer 是提供 Value 方法的接口。
//
// [Value] 方法返回的错误会被 database/sql 包包装。
// 这允许调用方在 [database/sql.Query]、[database/sql.Exec] 或
// [database/sql.QueryRow] 等操作之后使用 [errors.Is] 进行精确的错误处理。
//
// 实现 Valuer 接口的类型能够将自身
// 转换为 driver [Value]。
type Valuer interface {
	// Value 返回一个 driver Value。
	// Value 不得 panic。
	Value() (Value, error)
}

// Bool 是一个 [ValueConverter]，将输入值转换为 bool。
//
// 转换规则如下：
//   - 布尔值原样返回
//   - 对于整数类型，
//     1 为 true
//     0 为 false，
//     其他整数为错误
//   - 对于字符串和 []byte，规则与 [strconv.ParseBool] 相同
//   - 所有其他类型为错误
var Bool boolType

type boolType struct{}

var _ ValueConverter = boolType{}

func (boolType) String() string { return "Bool" }

func (boolType) ConvertValue(src any) (Value, error) {
	switch s := src.(type) {
	case bool:
		return s, nil
	case string:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("sql/driver: couldn't convert %q into type bool", s)
		}
		return b, nil
	case []byte:
		b, err := strconv.ParseBool(string(s))
		if err != nil {
			return nil, fmt.Errorf("sql/driver: couldn't convert %q into type bool", s)
		}
		return b, nil
	}

	sv := reflect.ValueOf(src)
	switch sv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		iv := sv.Int()
		if iv == 1 || iv == 0 {
			return iv == 1, nil
		}
		return nil, fmt.Errorf("sql/driver: couldn't convert %d into type bool", iv)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uv := sv.Uint()
		if uv == 1 || uv == 0 {
			return uv == 1, nil
		}
		return nil, fmt.Errorf("sql/driver: couldn't convert %d into type bool", uv)
	}

	return nil, fmt.Errorf("sql/driver: couldn't convert %v (%T) into type bool", src, src)
}

// Int32 是一个 [ValueConverter]，将输入值转换为 int64，
// 并遵守 int32 值的限制。
var Int32 int32Type

type int32Type struct{}

var _ ValueConverter = int32Type{}

func (int32Type) ConvertValue(v any) (Value, error) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i64 := rv.Int()
		if i64 > (1<<31)-1 || i64 < -(1<<31) {
			return nil, fmt.Errorf("sql/driver: value %d overflows int32", v)
		}
		return i64, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u64 := rv.Uint()
		if u64 > (1<<31)-1 {
			return nil, fmt.Errorf("sql/driver: value %d overflows int32", v)
		}
		return int64(u64), nil
	case reflect.String:
		i, err := strconv.Atoi(rv.String())
		if err != nil {
			return nil, fmt.Errorf("sql/driver: value %q can't be converted to int32", v)
		}
		return int64(i), nil
	}
	return nil, fmt.Errorf("sql/driver: unsupported value %v (type %T) converting to int32", v, v)
}

// String 是一个 [ValueConverter]，将其输入转换为字符串。
// 如果该值已经是字符串或 []byte，则保持不变。
// 如果该值属于其他类型，则使用
// fmt.Sprintf("%v", v) 完成到字符串的转换。
var String stringType

type stringType struct{}

func (stringType) ConvertValue(v any) (Value, error) {
	switch v.(type) {
	case string, []byte:
		return v, nil
	}
	return fmt.Sprintf("%v", v), nil
}

// Null 是一个实现 [ValueConverter] 的类型，它允许 nil
// 值，但在其他情况下委托给另一个 [ValueConverter]。
type Null struct {
	Converter ValueConverter
}

func (n Null) ConvertValue(v any) (Value, error) {
	if v == nil {
		return nil, nil
	}
	return n.Converter.ConvertValue(v)
}

// NotNull 是一个实现 [ValueConverter] 的类型，它不允许 nil
// 值，但在其他情况下委托给另一个 [ValueConverter]。
type NotNull struct {
	Converter ValueConverter
}

func (n NotNull) ConvertValue(v any) (Value, error) {
	if v == nil {
		return nil, fmt.Errorf("nil value not allowed")
	}
	return n.Converter.ConvertValue(v)
}

// IsValue 报告 v 是否为有效的 [Value] 参数类型。
func IsValue(v any) bool {
	if v == nil {
		return true
	}
	switch v.(type) {
	case []byte, bool, float64, int64, string, time.Time:
		return true
	case decimalDecompose:
		return true
	}
	return false
}

// IsScanValue 等价于 [IsValue]。
// 它的存在是为了兼容性。
func IsScanValue(v any) bool {
	return IsValue(v)
}

// DefaultParameterConverter 是 [ValueConverter] 的默认实现，
// 在 [Stmt] 未实现 [ColumnConverter] 时使用。
//
// 如果 IsValue(arg)，DefaultParameterConverter 直接返回其参数。
// 否则，如果参数实现了 [Valuer]，则使用其
// Value 方法返回一个 [Value]。作为后备，使用所提供
// 参数的底层类型将其转换为 [Value]：
// 底层整数类型转换为 int64，浮点数转换为 float64，
// bool、string 和 []byte 保持不变。如果参数是 nil
// 指针，defaultConverter.ConvertValue 返回 nil [Value]。
// 如果参数是非 nil 指针，它会被解引用，并递归调用
// defaultConverter.ConvertValue。其他类型
// 则为错误。
var DefaultParameterConverter defaultConverter

type defaultConverter struct{}

var _ ValueConverter = defaultConverter{}

var valuerReflectType = reflect.TypeFor[Valuer]()

// callValuerValue 返回 vr.Value()，只有一个例外：
// 如果 vr.Value 是某个指针类型上自动生成的方法，而该
// 指针为 nil，那么它会在运行时的 panicwrap
// 方法中 panic。此时应将其视为 nil。
// 见 Issue 8415。
//
// 这样做是为了让人们可以在值类型上实现 driver.Value，
// 并且仍然使用指向这些类型的 nil 指针来表示 nil/NULL，就像
// string/*string 一样。
//
// 此函数在 database/sql 包中有对应的镜像。
func callValuerValue(vr Valuer) (v Value, err error) {
	if rv := reflect.ValueOf(vr); rv.Kind() == reflect.Pointer &&
		rv.IsNil() &&
		rv.Type().Elem().Implements(valuerReflectType) {
		return nil, nil
	}
	return vr.Value()
}

func (defaultConverter) ConvertValue(v any) (Value, error) {
	if IsValue(v) {
		return v, nil
	}

	switch vr := v.(type) {
	case Valuer:
		sv, err := callValuerValue(vr)
		if err != nil {
			return nil, err
		}
		if !IsValue(sv) {
			return nil, fmt.Errorf("non-Value type %T returned from Value", sv)
		}
		return sv, nil

	// 目前，继续优先使用 Valuer 接口而非 decimal decompose 接口。
	case decimalDecompose:
		return vr, nil
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		// 间接指针
		if rv.IsNil() {
			return nil, nil
		} else {
			return defaultConverter{}.ConvertValue(rv.Elem().Interface())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return int64(rv.Uint()), nil
	case reflect.Uint64:
		u64 := rv.Uint()
		if u64 >= 1<<63 {
			return nil, fmt.Errorf("uint64 values with high bit set are not supported")
		}
		return int64(u64), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Slice:
		ek := rv.Type().Elem().Kind()
		if ek == reflect.Uint8 {
			return rv.Bytes(), nil
		}
		return nil, fmt.Errorf("unsupported type %T, a slice of %s", v, ek)
	case reflect.String:
		return rv.String(), nil
	}
	return nil, fmt.Errorf("unsupported type %T, a %s", v, rv.Kind())
}

type decimalDecompose interface {
	// Decompose 按部分返回内部十进制状态。
	// 如果提供的 buf 有足够的容量，可以将 buf 作为系数返回，并
	// 适当地设置值和长度。
	Decompose(buf []byte) (form byte, negative bool, coefficient []byte, exponent int32)
}
