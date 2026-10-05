// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2

package json

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"strconv"
	"strings"
	"time"

	"encoding/json/internal"
	"encoding/json/internal/jsonflags"
	"encoding/json/internal/jsonopts"
	"encoding/json/internal/jsonwire"
	"encoding/json/jsontext"
)

var (
	timeDurationType = reflect.TypeFor[time.Duration]()
	timeTimeType     = reflect.TypeFor[time.Time]()
)

func makeTimeArshaler(fncs *arshaler, t reflect.Type) *arshaler {
	// 理想情况下，time 类型应该实现 MarshalerTo 和 UnmarshalerFrom，
	// 但这会引入从 time 包到 json 包的依赖。
	// 考虑到 time 被广泛使用，更可接受的是我们引入
	// 从 json 到 time 的依赖。
	//
	// 像这样注入 arshaling 功能，与真正在 time 类型上声明方法
	// 并不完全相同，因为对 time 类型的嵌入
	// 将无法转发这一功能。
	switch t {
	case timeDurationType:
		fncs.nonDefault = true
		marshalNano := fncs.marshal
		fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) error {
			xe := export.Encoder(enc)
			var m durationArshaler
			if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
				if !m.initFormat(mo.Format) {
					return newInvalidFormatError(enc, t)
				}
			} else if mo.Flags.Get(jsonflags.FormatDurationAsNano) {
				return marshalNano(enc, va, mo)
			} else {
				// TODO(https://go.dev/issue/71631): 决定默认的 duration 表示形式。
				return newMarshalErrorBefore(enc, t, errors.New("no default representation (see https://go.dev/issue/71631); specify an explicit format"))
			}

			m.td, _ = reflect.TypeAssert[time.Duration](va.Value)
			k := stringOrNumberKind(!m.isNumeric() || xe.Tokens.Last.NeedObjectName() || mo.Flags.Get(jsonflags.StringifyNumbers))
			if err := xe.AppendRaw(k, true, m.appendMarshal); err != nil {
				if !isSyntacticError(err) && !export.IsIOError(err) {
					err = newMarshalErrorBefore(enc, t, err)
				}
				return err
			}
			return nil
		}
		unmarshalNano := fncs.unmarshal
		fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) error {
			xd := export.Decoder(dec)
			var u durationArshaler
			if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
				if !u.initFormat(uo.Format) {
					return newInvalidFormatError(dec, t)
				}
			} else if uo.Flags.Get(jsonflags.FormatDurationAsNano) {
				return unmarshalNano(dec, va, uo)
			} else {
				// TODO(https://go.dev/issue/71631): 决定默认的 duration 表示形式。
				return newUnmarshalErrorBeforeWithSkipping(dec, t, errors.New("no default representation (see https://go.dev/issue/71631); specify an explicit format"))
			}

			stringify := !u.isNumeric() || xd.Tokens.Last.NeedObjectName() || uo.Flags.Get(jsonflags.StringifyNumbers)
			var flags jsonwire.ValueFlags
			td, _ := reflect.TypeAssert[*time.Duration](va.Addr())
			val, err := xd.ReadValue(&flags)
			if err != nil {
				return err
			}
			switch k := val.Kind(); k {
			case 'n':
				if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
					*td = time.Duration(0)
				}
				return nil
			case '"':
				if !stringify {
					break
				}
				val = jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
				if err := u.unmarshal(val); err != nil {
					return newUnmarshalErrorAfter(dec, t, err)
				}
				*td = u.td
				return nil
			case '0':
				if stringify {
					break
				}
				if err := u.unmarshal(val); err != nil {
					return newUnmarshalErrorAfter(dec, t, err)
				}
				*td = u.td
				return nil
			}
			return newUnmarshalErrorAfter(dec, t, nil)
		}
	case timeTimeType:
		fncs.nonDefault = true
		fncs.marshal = func(enc *jsontext.Encoder, va addressableValue, mo *jsonopts.Struct) (err error) {
			xe := export.Encoder(enc)
			var m timeArshaler
			if mo.Format != "" && mo.FormatDepth == xe.Tokens.Depth() {
				if !m.initFormat(mo.Format) {
					return newInvalidFormatError(enc, t)
				}
			}

			m.tt, _ = reflect.TypeAssert[time.Time](va.Value)
			k := stringOrNumberKind(!m.isNumeric() || xe.Tokens.Last.NeedObjectName() || mo.Flags.Get(jsonflags.StringifyNumbers))
			if err := xe.AppendRaw(k, !m.hasCustomFormat(), m.appendMarshal); err != nil {
				if mo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
					return internal.NewMarshalerError(va.Addr().Interface(), err, "MarshalJSON") // 与 unmarshal 不同，总是被包装
				}
				if !isSyntacticError(err) && !export.IsIOError(err) {
					err = newMarshalErrorBefore(enc, t, err)
				}
				return err
			}
			return nil
		}
		fncs.unmarshal = func(dec *jsontext.Decoder, va addressableValue, uo *jsonopts.Struct) (err error) {
			xd := export.Decoder(dec)
			var u timeArshaler
			if uo.Format != "" && uo.FormatDepth == xd.Tokens.Depth() {
				if !u.initFormat(uo.Format) {
					return newInvalidFormatError(dec, t)
				}
			} else if uo.Flags.Get(jsonflags.ParseTimeWithLooseRFC3339) {
				u.looseRFC3339 = true
			}

			stringify := !u.isNumeric() || xd.Tokens.Last.NeedObjectName() || uo.Flags.Get(jsonflags.StringifyNumbers)
			var flags jsonwire.ValueFlags
			tt, _ := reflect.TypeAssert[*time.Time](va.Addr())
			val, err := xd.ReadValue(&flags)
			if err != nil {
				return err
			}
			switch k := val.Kind(); k {
			case 'n':
				if !uo.Flags.Get(jsonflags.MergeWithLegacySemantics) {
					*tt = time.Time{}
				}
				return nil
			case '"':
				if !stringify {
					break
				}
				val = jsonwire.UnquoteMayCopy(val, flags.IsVerbatim())
				if err := u.unmarshal(val); err != nil {
					if uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
						return err // 与 marshal 不同，从不被包装
					}
					return newUnmarshalErrorAfter(dec, t, err)
				}
				*tt = u.tt
				return nil
			case '0':
				if stringify {
					break
				}
				if err := u.unmarshal(val); err != nil {
					if uo.Flags.Get(jsonflags.ReportErrorsWithLegacySemantics) {
						return err // 与 marshal 不同，从不被包装
					}
					return newUnmarshalErrorAfter(dec, t, err)
				}
				*tt = u.tt
				return nil
			}
			return newUnmarshalErrorAfter(dec, t, nil)
		}
	}
	return fncs
}

type durationArshaler struct {
	td time.Duration

	// base 记录表示形式，其中：
	//   - 0 使用 time.Duration.String
	//   - 1e0、1e3、1e6 或 1e9 使用 duration 的十进制编码，表示为
	//     纳秒、微秒、毫秒或秒。
	//   - 8601 使用 ISO 8601
	base uint64
}

func (a *durationArshaler) initFormat(format string) (ok bool) {
	switch format {
	case "units":
		a.base = 0
	case "sec":
		a.base = 1e9
	case "milli":
		a.base = 1e6
	case "micro":
		a.base = 1e3
	case "nano":
		a.base = 1e0
	case "iso8601":
		a.base = 8601
	default:
		return false
	}
	return true
}

func (a *durationArshaler) isNumeric() bool {
	return a.base != 0 && a.base != 8601
}

func (a *durationArshaler) appendMarshal(b []byte) ([]byte, error) {
	switch a.base {
	case 0:
		return append(b, a.td.String()...), nil
	case 8601:
		return appendDurationISO8601(b, a.td), nil
	default:
		return appendDurationBase10(b, a.td, a.base), nil
	}
}

func (a *durationArshaler) unmarshal(b []byte) (err error) {
	switch a.base {
	case 0:
		a.td, err = time.ParseDuration(string(b))
	case 8601:
		a.td, err = parseDurationISO8601(b)
	default:
		a.td, err = parseDurationBase10(b, a.base)
	}
	return err
}

type timeArshaler struct {
	tt time.Time

	// base 记录表示形式，其中：
	//   - 0 使用时间戳的 RFC 3339 编码
	//   - 1e0、1e3、1e6 或 1e9 使用时间戳的十进制编码，表示为
	//     自 Unix 纪元以来的秒、毫秒、微秒或纳秒。
	//   - math.MaxUint 使用 time.Time.Format 编码时间戳
	base   uint64
	format string // 传递给 time.Parse 的时间格式

	looseRFC3339 bool
}

func (a *timeArshaler) initFormat(format string) bool {
	// 我们假定 time 包中导出的常量
	// 总是以大写 ASCII 字母开头。
	if len(format) == 0 {
		return false
	}
	a.base = math.MaxUint // 意味着自定义格式
	if c := format[0]; !('a' <= c && c <= 'z') && !('A' <= c && c <= 'Z') {
		a.format = format
		return true
	}
	switch format {
	case "ANSIC":
		a.format = time.ANSIC
	case "UnixDate":
		a.format = time.UnixDate
	case "RubyDate":
		a.format = time.RubyDate
	case "RFC822":
		a.format = time.RFC822
	case "RFC822Z":
		a.format = time.RFC822Z
	case "RFC850":
		a.format = time.RFC850
	case "RFC1123":
		a.format = time.RFC1123
	case "RFC1123Z":
		a.format = time.RFC1123Z
	case "RFC3339":
		a.base = 0
		a.format = time.RFC3339
	case "RFC3339Nano":
		a.base = 0
		a.format = time.RFC3339Nano
	case "Kitchen":
		a.format = time.Kitchen
	case "Stamp":
		a.format = time.Stamp
	case "StampMilli":
		a.format = time.StampMilli
	case "StampMicro":
		a.format = time.StampMicro
	case "StampNano":
		a.format = time.StampNano
	case "DateTime":
		a.format = time.DateTime
	case "DateOnly":
		a.format = time.DateOnly
	case "TimeOnly":
		a.format = time.TimeOnly
	case "unix":
		a.base = 1e0
	case "unixmilli":
		a.base = 1e3
	case "unixmicro":
		a.base = 1e6
	case "unixnano":
		a.base = 1e9
	default:
		// 拒绝任何 Go 标识符，以防将来支持新的常量。
		if strings.TrimFunc(format, isLetterOrDigit) == "" {
			return false
		}
		a.format = format
	}
	return true
}

func (a *timeArshaler) isNumeric() bool {
	return int(a.base) > 0
}

func (a *timeArshaler) hasCustomFormat() bool {
	return a.base == math.MaxUint
}

func (a *timeArshaler) appendMarshal(b []byte) ([]byte, error) {
	switch a.base {
	case 0:
		format := cmp.Or(a.format, time.RFC3339Nano)
		n0 := len(b)
		b = a.tt.AppendFormat(b, format)
		// 并非所有 Go 时间戳都能表示为有效的 RFC 3339。
		// 显式检查这些边界情况。
		// 参见 https://go.dev/issue/4556 和 https://go.dev/issue/54580。
		switch b := b[n0:]; {
		case b[len("9999")] != '-': // 年份必须正好是 4 位数字宽
			return b, errors.New("year outside of range [0,9999]")
		case b[len(b)-1] != 'Z':
			c := b[len(b)-len("Z07:00")]
			if ('0' <= c && c <= '9') || parseDec2(b[len(b)-len("07:00"):]) >= 24 {
				return b, errors.New("timezone hour outside of range [0,23]")
			}
		}
		return b, nil
	case math.MaxUint:
		return a.tt.AppendFormat(b, a.format), nil
	default:
		return appendTimeUnix(b, a.tt, a.base), nil
	}
}

func (a *timeArshaler) unmarshal(b []byte) (err error) {
	switch a.base {
	case 0:
		// 使用 time.Time.UnmarshalText 以避免可能的字符串分配。
		if err := a.tt.UnmarshalText(b); err != nil {
			return err
		}
		// TODO(https://go.dev/issue/57912):
		// RFC 3339 规定了有效时间戳的语法。
		// 然而，"time" 中的解析功能过于宽松，
		// 会错误地将无效时间戳当作有效。
		// 当 "time" 为我们检查时，请移除这些手动检查。
		newParseError := func(layout, value, layoutElem, valueElem, message string) error {
			return &time.ParseError{Layout: layout, Value: value, LayoutElem: layoutElem, ValueElem: valueElem, Message: message}
		}
		switch {
		case a.looseRFC3339:
			return nil
		case b[len("2006-01-02T")+1] == ':': // 小时必须为两位数字
			return newParseError(time.RFC3339, string(b), "15", string(b[len("2006-01-02T"):][:1]), "")
		case b[len("2006-01-02T15:04:05")] == ',': // 亚秒分隔符必须是一个点号
			return newParseError(time.RFC3339, string(b), ".", ",", "")
		case b[len(b)-1] != 'Z':
			switch {
			case parseDec2(b[len(b)-len("07:00"):]) >= 24: // 时区小时必须在范围内
				return newParseError(time.RFC3339, string(b), "Z07:00", string(b[len(b)-len("Z07:00"):]), ": timezone hour out of range")
			case parseDec2(b[len(b)-len("00"):]) >= 60: // 时区分钟必须在范围内
				return newParseError(time.RFC3339, string(b), "Z07:00", string(b[len(b)-len("Z07:00"):]), ": timezone minute out of range")
			}
		}
		return nil
	case math.MaxUint:
		a.tt, err = time.Parse(a.format, string(b))
		return err
	default:
		a.tt, err = parseTimeUnix(b, a.base)
		return err
	}
}

// appendDurationBase10 将 d 格式化为十进制小数并追加，
// 其中 pow10 是用于缩小该数字的 10 的幂。
func appendDurationBase10(b []byte, d time.Duration, pow10 uint64) []byte {
	b, n := mayAppendDurationSign(b, d)            // 追加符号
	whole, frac := bits.Div64(0, n, uint64(pow10)) // 计算整数和小数部分
	b = strconv.AppendUint(b, whole, 10)           // 追加整数部分
	return appendFracBase10(b, frac, pow10)        // 追加小数部分
}

// parseDurationBase10 从十进制小数中解析出 d，
// 其中 pow10 是用于放大该数字的 10 的幂。
func parseDurationBase10(b []byte, pow10 uint64) (time.Duration, error) {
	suffix, neg := consumeSign(b, false)                     // 消费符号
	wholeBytes, fracBytes := bytesCutByte(suffix, '.', true) // 消费整数和小数部分
	whole, okWhole := jsonwire.ParseUint(wholeBytes)         // 解析整数部分；可能溢出
	frac, okFrac := parseFracBase10(fracBytes, pow10)        // 解析小数部分
	hi, lo := bits.Mul64(whole, uint64(pow10))               // 若 hi > 0 则溢出
	sum, co := bits.Add64(lo, uint64(frac), 0)               // 若 co > 0 则溢出
	switch d := mayApplyDurationSign(sum, neg); {            // 若 neg != (d < 0) 则溢出
	case (!okWhole && whole != math.MaxUint64) || !okFrac:
		return 0, fmt.Errorf("invalid duration %q: %w", b, strconv.ErrSyntax)
	case !okWhole || hi > 0 || co > 0 || neg != (d < 0):
		return 0, fmt.Errorf("invalid duration %q: %w", b, strconv.ErrRange)
	default:
		return d, nil
	}
}

// appendDurationISO8601 追加一个使用受限语法的 ISO 8601 duration，
// 其中前导和尾随的零以及零值指示符会被省略。
// 它只使用小时、分钟和秒的指示符，因为 ISO 8601 将这些定义为“精确的（accurate）”，
// 而年、月、周和日则是“名义的（nominal）”。
func appendDurationISO8601(b []byte, d time.Duration) []byte {
	if d == 0 {
		return append(b, "PT0S"...)
	}
	b, n := mayAppendDurationSign(b, d)
	b = append(b, "PT"...)
	n, nsec := bits.Div64(0, n, 1e9)  // 计算纳秒部分
	n, sec := bits.Div64(0, n, 60)    // 计算秒部分
	hour, min := bits.Div64(0, n, 60) // 计算小时和分钟部分
	if hour > 0 {
		b = append(strconv.AppendUint(b, hour, 10), 'H')
	}
	if min > 0 {
		b = append(strconv.AppendUint(b, min, 10), 'M')
	}
	if sec > 0 || nsec > 0 {
		b = append(appendFracBase10(strconv.AppendUint(b, sec, 10), nsec, 1e9), 'S')
	}
	return b
}

// daysPerYear 是根据格里高利历（Gregorian calendar）计算的
// 一年的精确平均天数，该历法每年会多出一天，即
// 4 的倍数，除非它能被 100 整除但不能被 400 整除。
// 这并未考虑闰秒，因为闰秒是不确定的。
const daysPerYear = 365.2425

var errInaccurateDateUnits = errors.New("inaccurate year, month, week, or day units")

// parseDurationISO8601 根据 ISO 8601-1:2019 第 5.5.2.2 节和第 5.5.2.3 节
// 解析 duration，并带有以下限制或扩展：
//
//   - 根据 ISO 8601-2:2019 第 4.4.1.9 节，允许为负 duration
//     使用前导负号。我们不允许为每个“时间尺度分量（time scale component）”
//     使用负值——第 4.4.1.1 节允许这样做，
//     但解析器很少支持。
//
//   - 允许前导正号（并被忽略）。
//     ISO 8601 并未要求这一点，但也没有禁止。
//     这有一些先例，因为它得到 ISO 8601-2-2019 第 14.1 节
//     所规定的 duration 算术原则的支持。
//     值得注意的是，ISO 8601 的 JavaScript 语法允许前导正号。
//
//   - 分数值仅允许用于最后一个时间分量中的精确单位
//     （即小时、分钟和秒），
//     这是 ISO 8601-1:2019 第 5.5.2.3 节所允许的。
//
//   - 小数点（'.'）和逗号（','）都支持作为
//     数字整数部分与小数部分之间的分隔符，
//     如 ISO 8601-1:2019 第 3.2.6 节所规定。
//     虽然 ISO 8601 推荐逗号作为默认分隔符，
//     但大多数格式化程序使用小数点。
//
//   - 前导零会被忽略。ISO 8601 并未要求这一点，
//     但标准也未禁止。许多解析器支持这一点。
//
//   - 支持小写指示符。ISO 8601 并未要求这一点，
//     但标准也未禁止。许多解析器支持这一点。
//
// 如果存在年、月、周或日的名义单位，
// 这会生成一个尽力而为的值，并同时报告 [errInaccurateDateUnits]。
//
// 所接受的语法与 JavaScript 的 Duration 完全相同：
//
//	https://tc39.es/proposal-temporal/#prod-Duration
//
// 我们遵循 JavaScript 的语法，因为 JSON 本身派生自 JavaScript。
// 只要 JavaScript 中的算术不使用高于 "hours" 的 largestUnit 值
// （这是默认值），Temporal.Duration.toJSON 方法就保证
// 能产生可由此函数解析的输出。
// 即使它使用了，这也会以不精确的单位进行尽力而为的解析，
// 但会报告 [errInaccurateDateUnits]。
func parseDurationISO8601(b []byte) (time.Duration, error) {
	var invalid, overflow, inaccurate, sawFrac bool
	var sumNanos, n, co uint64

	// cutBytes 类似于 [bytes.Cut]，但使用 c0 或 c1 作为分隔符。
	cutBytes := func(b []byte, c0, c1 byte) (prefix, suffix []byte, ok bool) {
		for i, c := range b {
			if c == c0 || c == c1 {
				return b[:i], b[i+1:], true
			}
		}
		return b, nil, false
	}

	// mayParseUnit 尝试解析由 desHi 和 desLo 单位字符
	// 标识的另一个日期或时间数字。
	// 如果当前单位对应的部分不存在，则原样返回 b。
	mayParseUnit := func(b []byte, desHi, desLo byte, unit time.Duration) []byte {
		number, suffix, ok := cutBytes(b, desHi, desLo)
		if !ok || sawFrac {
			return b // 指示符不存在，或者已经出现过分数，而分数只能出现在最后一个分量中
		}

		// 解析该数字。
		// 最后一部分中的精确单位允许有分数。
		whole, frac, ok := cutBytes(number, '.', ',')
		if ok {
			sawFrac = true
			invalid = invalid || len(frac) == len("") || unit > time.Hour
			if unit == time.Second {
				n, ok = parsePaddedBase10(frac, uint64(time.Second))
				invalid = invalid || !ok
			} else {
				f, err := strconv.ParseFloat("0."+string(frac), 64)
				invalid = invalid || err != nil || len(bytes.Trim(frac[len("."):], "0123456789")) > 0
				n = uint64(math.Round(f * float64(unit))) // 从不溢出，因为 f 在 [0..1] 范围内
			}
			sumNanos, co = bits.Add64(sumNanos, n, 0) // 若 co > 0 则溢出
			overflow = overflow || co > 0
		}
		for len(whole) > 1 && whole[0] == '0' {
			whole = whole[len("0"):] // 去除前导零
		}
		n, ok := jsonwire.ParseUint(whole)         // 若 !ok && MaxUint64 则溢出
		hi, lo := bits.Mul64(n, uint64(unit))      // 若 hi > 0 则溢出
		sumNanos, co = bits.Add64(sumNanos, lo, 0) // 若 co > 0 则溢出
		invalid = invalid || (!ok && n != math.MaxUint64)
		overflow = overflow || (!ok && n == math.MaxUint64) || hi > 0 || co > 0
		inaccurate = inaccurate || unit > time.Hour
		return suffix
	}

	suffix, neg := consumeSign(b, true)
	prefix, suffix, okP := cutBytes(suffix, 'P', 'p')
	durDate, durTime, okT := cutBytes(suffix, 'T', 't')
	invalid = invalid || len(prefix) > 0 || !okP || (okT && len(durTime) == 0) || len(durDate)+len(durTime) == 0
	if len(durDate) > 0 { // duration 的名义部分
		durDate = mayParseUnit(durDate, 'Y', 'y', time.Duration(daysPerYear*24*60*60*1e9))
		durDate = mayParseUnit(durDate, 'M', 'm', time.Duration(daysPerYear/12*24*60*60*1e9))
		durDate = mayParseUnit(durDate, 'W', 'w', time.Duration(7*24*60*60*1e9))
		durDate = mayParseUnit(durDate, 'D', 'd', time.Duration(24*60*60*1e9))
		invalid = invalid || len(durDate) > 0 // 未知元素
	}
	if len(durTime) > 0 { // duration 的精确部分
		durTime = mayParseUnit(durTime, 'H', 'h', time.Duration(60*60*1e9))
		durTime = mayParseUnit(durTime, 'M', 'm', time.Duration(60*1e9))
		durTime = mayParseUnit(durTime, 'S', 's', time.Duration(1e9))
		invalid = invalid || len(durTime) > 0 // 未知元素
	}
	d := mayApplyDurationSign(sumNanos, neg)
	overflow = overflow || (neg != (d < 0) && d != 0) // 使带符号 duration 溢出

	switch {
	case invalid:
		return 0, fmt.Errorf("invalid ISO 8601 duration %q: %w", b, strconv.ErrSyntax)
	case overflow:
		return 0, fmt.Errorf("invalid ISO 8601 duration %q: %w", b, strconv.ErrRange)
	case inaccurate:
		return d, fmt.Errorf("invalid ISO 8601 duration %q: %w", b, errInaccurateDateUnits)
	default:
		return d, nil
	}
}

// mayAppendDurationSign 在 d 为负时追加一个负号。
func mayAppendDurationSign(b []byte, d time.Duration) ([]byte, uint64) {
	if d < 0 {
		b = append(b, '-')
		d *= -1
	}
	return b, uint64(d)
}

// mayApplyDurationSign 在指定 neg 时反转 n 的符号。
func mayApplyDurationSign(n uint64, neg bool) time.Duration {
	if neg {
		return -1 * time.Duration(n)
	} else {
		return +1 * time.Duration(n)
	}
}

// appendTimeUnix 将 t 格式化为十进制小数并追加，
// 其中 pow10 是用于放大该数字的 10 的幂。
func appendTimeUnix(b []byte, t time.Time, pow10 uint64) []byte {
	sec, nsec := t.Unix(), int64(t.Nanosecond())
	if sec < 0 {
		b = append(b, '-')
		sec, nsec = negateSecNano(sec, nsec)
	}
	switch {
	case pow10 == 1e0: // 单位为秒的快速情形
		b = strconv.AppendUint(b, uint64(sec), 10)
		return appendFracBase10(b, uint64(nsec), 1e9)
	case uint64(sec) < 1e9: // 单位不是秒但不会溢出的中间情形
		b = strconv.AppendUint(b, uint64(sec)*uint64(pow10)+uint64(uint64(nsec)/(1e9/pow10)), 10)
		return appendFracBase10(b, (uint64(nsec)*pow10)%1e9, 1e9)
	default: // 单位不是秒且会发生溢出的慢速情形
		b = strconv.AppendUint(b, uint64(sec), 10)
		b = appendPaddedBase10(b, uint64(nsec)/(1e9/pow10), pow10)
		return appendFracBase10(b, (uint64(nsec)*pow10)%1e9, 1e9)
	}
}

// parseTimeUnix 从十进制小数中解析出 t，
// 其中 pow10 是用于缩小该数字的 10 的幂。
func parseTimeUnix(b []byte, pow10 uint64) (time.Time, error) {
	suffix, neg := consumeSign(b, false)                     // 消费符号
	wholeBytes, fracBytes := bytesCutByte(suffix, '.', true) // 消费整数和小数部分
	whole, okWhole := jsonwire.ParseUint(wholeBytes)         // 解析整数部分；可能溢出
	frac, okFrac := parseFracBase10(fracBytes, 1e9/pow10)    // 解析小数部分
	var sec, nsec int64
	switch {
	case pow10 == 1e0: // 单位为秒的快速情形
		sec = int64(whole) // 稍后在取负之后检查溢出
		nsec = int64(frac) // 不会溢出
	case okWhole: // 单位不是秒但不会溢出的中间情形
		sec = int64(whole / pow10)                     // 稍后在取负之后检查溢出
		nsec = int64((whole%pow10)*(1e9/pow10) + frac) // 不会溢出
	case !okWhole && whole == math.MaxUint64: // 单位不是秒且发生溢出的慢速情形
		width := int(math.Log10(float64(pow10)))                                // 计算 len(strconv.Itoa(pow10-1))
		whole, okWhole = jsonwire.ParseUint(wholeBytes[:len(wholeBytes)-width]) // 解析高位的整数部分
		mid, _ := parsePaddedBase10(wholeBytes[len(wholeBytes)-width:], pow10)  // 解析低位的整数部分
		sec = int64(whole)                                                      // 稍后在取负之后检查溢出
		nsec = int64(mid*(1e9/pow10) + frac)                                    // 不会溢出
	}
	if neg {
		sec, nsec = negateSecNano(sec, nsec)
	}
	switch t := time.Unix(sec, nsec).UTC(); {
	case (!okWhole && whole != math.MaxUint64) || !okFrac:
		return time.Time{}, fmt.Errorf("invalid time %q: %w", b, strconv.ErrSyntax)
	case !okWhole || neg != (t.Unix() < 0):
		return time.Time{}, fmt.Errorf("invalid time %q: %w", b, strconv.ErrRange)
	default:
		return t, nil
	}
}

// negateSecNano 对一个 Unix 时间戳取负，其中 nsec 必须在 [0, 1e9) 范围内。
func negateSecNano(sec, nsec int64) (int64, int64) {
	sec = ^sec               // 二进制补码取负（即 -1*sec + 1）
	nsec = -nsec + 1e9       // 对 nsec 取负并加上 1e9（即 sec 取负带来的额外 +1）
	sec += int64(nsec / 1e9) // 处理 nsec 最初为零时可能发生的溢出
	nsec %= 1e9              // 确保 nsec 保持在 [0, 1e9) 范围内
	return sec, nsec
}

// appendFracBase10 追加 n/max10 的小数部分，
// 其中 max10 是大于 n 的 10 的幂。
func appendFracBase10(b []byte, n, max10 uint64) []byte {
	if n == 0 {
		return b
	}
	return bytes.TrimRight(appendPaddedBase10(append(b, '.'), n, max10), "0")
}

// parseFracBase10 解析 n/max10 的小数部分，
// 其中 max10 是大于 n 的 10 的幂。
func parseFracBase10(b []byte, max10 uint64) (n uint64, ok bool) {
	switch {
	case len(b) == 0:
		return 0, true
	case len(b) < len(".0") || b[0] != '.':
		return 0, false
	}
	return parsePaddedBase10(b[len("."):], max10)
}

// appendPaddedBase10 追加 n 的零填充编码，
// 其中 max10 是大于 n 的 10 的幂。
func appendPaddedBase10(b []byte, n, max10 uint64) []byte {
	if n < max10/10 {
		// n 的格式化结果比 log10(max10) 短，
		// 因此加上 max10/10 以确保长度等于 log10(max10)。
		i := len(b)
		b = strconv.AppendUint(b, n+max10/10, 10)
		b[i]-- // 减去所加的 max10/10
		return b
	}
	return strconv.AppendUint(b, n, 10)
}

// parsePaddedBase10 将 b 解析为 n 的零填充编码，
// 其中 max10 是大于 n 的 10 的幂。
// 被截断的后缀视为隐式的零。
// 扩展的后缀会被忽略，但会验证其只包含数字。
func parsePaddedBase10(b []byte, max10 uint64) (n uint64, ok bool) {
	pow10 := uint64(1)
	for pow10 < max10 {
		n *= 10
		if len(b) > 0 {
			if b[0] < '0' || '9' < b[0] {
				return n, false
			}
			n += uint64(b[0] - '0')
			b = b[1:]
		}
		pow10 *= 10
	}
	if len(b) > 0 && len(bytes.TrimRight(b, "0123456789")) > 0 {
		return n, false // 尾随字符不是数字
	}
	return n, true
}

// consumeSign 消费一个可选的前导负号或正号。
func consumeSign(b []byte, allowPlus bool) ([]byte, bool) {
	if len(b) > 0 {
		if b[0] == '-' {
			return b[len("-"):], true
		} else if b[0] == '+' && allowPlus {
			return b[len("+"):], false
		}
	}
	return b, false
}

// bytesCutByte 类似于 bytes.Cut(b, []byte{c})，
// 不同之处在于 c 可以选择性地包含在后缀中。
func bytesCutByte(b []byte, c byte, include bool) ([]byte, []byte) {
	if i := bytes.IndexByte(b, c); i >= 0 {
		if include {
			return b[:i], b[i:]
		}
		return b[:i], b[i+1:]
	}
	return b, nil
}

// parseDec2 将 b 解析为一个无符号的、十进制、2 位数字。
// 如果数字不是十进制，则结果未定义。
func parseDec2(b []byte) byte {
	if len(b) < 2 {
		return 0
	}
	return 10*(b[0]-'0') + (b[1] - '0')
}
