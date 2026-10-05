// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// Level 是日志事件的重要性或严重程度。
// 级别越高，事件越重要或越严重。
type Level int

// 常见级别的名称。
//
// 级别数字本质上是任意的，
// 但我们选择它们是为了满足三个约束条件。
// 任何系统都可以在需要时将它们映射到另一种编号方案。
//
// 第一，我们希望默认级别为 Info，由于 Level 是 int，Info 就是
// int 的默认值，即零。
//
// 第二，我们希望便于使用级别来指定 logger 的详细程度。
// 由于级别越大意味着事件越严重，接受级别更小（或更负）事件的
// logger 意味着更详细的 logger。因此 logger 的详细程度
// 是事件严重程度的相反数，默认详细程度 0 接受所有
// 至少与 INFO 同样严重的事件。
//
// 第三，我们希望在级别之间留出一些空间，以容纳
// 那些在级别命名上介于我们之间的方案。例如，Google Cloud Logging 在
// Info 和 Warn 之间定义了一个 Notice 级别。由于这类中间
// 级别很少，数字之间的间隔不必很大。我们的间隔 4 与
// OpenTelemetry 的映射一致。将 OpenTelemetry 在
// DEBUG、INFO、WARN 和 ERROR 范围内的级别减去 9，即可将其转换为对应的 slog
// Level 范围。OpenTelemetry 还有 TRACE 和 FATAL 名称，而 slog
// 没有。但这些 OpenTelemetry 级别仍然可以通过使用适当的整数
// 表示为 slog Level。
const (
	LevelDebug Level = -4
	LevelInfo  Level = 0
	LevelWarn  Level = 4
	LevelError Level = 8
)

// String 返回该级别的名称。
// 如果该级别有名称，则返回
// 该名称的大写形式。
// 如果该级别介于已命名的值之间，则
// 会在大写名称后附加一个整数。
// 示例：
//
//	LevelWarn.String() => "WARN"
//	(LevelInfo+2).String() => "INFO+2"
func (l Level) String() string {
	str := func(base string, val Level) string {
		if val == 0 {
			return base
		}
		sval := strconv.Itoa(int(val))
		if val > 0 {
			sval = "+" + sval
		}
		return base + sval
	}

	switch {
	case l < LevelInfo:
		return str("DEBUG", l-LevelDebug)
	case l < LevelWarn:
		return str("INFO", l-LevelInfo)
	case l < LevelError:
		return str("WARN", l-LevelWarn)
	default:
		return str("ERROR", l-LevelError)
	}
}

// MarshalJSON 通过引用 [Level.String] 的输出来实现
// [encoding/json.Marshaler]。
func (l Level) MarshalJSON() ([]byte, error) {
	// AppendQuote 足以对所有 Level 字符串进行 JSON 编码。
	// 它们不包含任何在转义时会产生无效 JSON
	// 的字符。
	return strconv.AppendQuote(nil, l.String()), nil
}

// UnmarshalJSON 实现 [encoding/json.Unmarshaler]
// 它接受由 [Level.MarshalJSON] 产生的任何字符串，
// 忽略大小写。
// 它还接受会导致输出字符串不同的数字偏移量。
// 例如，"Error-8" 会被序列化为 "INFO"。
func (l *Level) UnmarshalJSON(data []byte) error {
	s, err := strconv.Unquote(string(data))
	if err != nil {
		return err
	}
	return l.parse(s)
}

// AppendText 通过调用 [Level.String] 来实现
// [encoding.TextAppender]。
func (l Level) AppendText(b []byte) ([]byte, error) {
	return append(b, l.String()...), nil
}

// MarshalText 通过调用 [Level.AppendText] 来实现
// [encoding.TextMarshaler]。
func (l Level) MarshalText() ([]byte, error) {
	return l.AppendText(nil)
}

// UnmarshalText 实现 [encoding.TextUnmarshaler]。
// 它接受由 [Level.MarshalText] 产生的任何字符串，
// 忽略大小写。
// 它还接受会导致输出字符串不同的数字偏移量。
// 例如，"Error-8" 会被序列化为 "INFO"。
func (l *Level) UnmarshalText(data []byte) error {
	return l.parse(string(data))
}

func (l *Level) parse(s string) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("slog: level string %q: %w", s, err)
		}
	}()

	name := s
	offset := 0
	if i := strings.IndexAny(s, "+-"); i >= 0 {
		name = s[:i]
		offset, err = strconv.Atoi(s[i:])
		if err != nil {
			return err
		}
	}
	switch strings.ToUpper(name) {
	case "DEBUG":
		*l = LevelDebug
	case "INFO":
		*l = LevelInfo
	case "WARN":
		*l = LevelWarn
	case "ERROR":
		*l = LevelError
	default:
		return errors.New("unknown name")
	}
	*l += Level(offset)
	return nil
}

// Level 返回接收者。
// 它实现 [Leveler]。
func (l Level) Level() Level { return l }

// LevelVar 是一个 [Level] 变量，用于让 [Handler] 的级别能够
// 动态变化。
// 它实现 [Leveler] 以及一个 Set 方法，
// 并且可以安全地被多个 goroutine 使用。
// 零值 LevelVar 对应于 [LevelInfo]。
type LevelVar struct {
	val atomic.Int64
}

// Level 返回 v 的级别。
func (v *LevelVar) Level() Level {
	return Level(int(v.val.Load()))
}

// Set 将 v 的级别设置为 l。
func (v *LevelVar) Set(l Level) {
	v.val.Store(int64(l))
}

func (v *LevelVar) String() string {
	return fmt.Sprintf("LevelVar(%s)", v.Level())
}

// AppendText 通过调用 [Level.AppendText] 来实现
// [encoding.TextAppender]。
func (v *LevelVar) AppendText(b []byte) ([]byte, error) {
	return v.Level().AppendText(b)
}

// MarshalText 通过调用 [LevelVar.AppendText] 来实现
// [encoding.TextMarshaler]。
func (v *LevelVar) MarshalText() ([]byte, error) {
	return v.AppendText(nil)
}

// UnmarshalText 通过调用 [Level.UnmarshalText] 来实现
// [encoding.TextUnmarshaler]。
func (v *LevelVar) UnmarshalText(data []byte) error {
	var l Level
	if err := l.UnmarshalText(data); err != nil {
		return err
	}
	v.Set(l)
	return nil
}

// Leveler 提供一个 [Level] 值。
//
// 由于 Level 本身实现了 Leveler，客户端通常在任何需要 Leveler 的地方
// （例如在 [HandlerOptions] 中）提供 Level 值。
// 需要动态改变级别的客户端可以提供更复杂的
// Leveler 实现，例如 *[LevelVar]。
type Leveler interface {
	Level() Level
}
