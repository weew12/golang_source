// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bufio

import (
	"bytes"
	"errors"
	"io"
	"unicode/utf8"
)

// Scanner 提供便捷接口读取数据，例如换行分隔的文本文件。
// 连续调用 Scanner.Scan 方法会步进文件的“标记”，跳过标记之间的字节。
// 标记的定义由 SplitFunc 类型的分割函数决定；默认分割函数将输入拆分为行，并去除行尾符。
// 本包定义了 Scanner.Split 函数，用于将文件扫描为行、字节、UTF-8 编码的字符和空格分隔的单词。
// 客户端也可提供自定义分割函数。
//
// 扫描在遇到 EOF、首个 I/O 错误或标记过大无法放入 Scanner.Buffer 时不可恢复地停止。
// 扫描停止时，读取器可能已远超最后一个标记。
// 需要更精细控制错误处理、大标记，或必须在读取器上连续扫描的程序，应改用 bufio.Reader。
type Scanner struct {
	r            io.Reader // 客户端提供的读取器
	split        SplitFunc // 分割标记的函数
	maxTokenSize int       // 标记的最大大小；测试时可修改
	token        []byte    // 分割函数返回的最后一个标记
	buf          []byte    // 作为分割函数参数的缓冲区
	start        int       // buf 中首个未处理字节
	end          int       // buf 中数据的结束位置
	err          error     // 持久错误
	empties      int       // 连续空标记的计数
	scanCalled   bool      // Scan 已被调用；缓冲区正在使用
	done         bool      // Scan 已完成
}

// SplitFunc 是用于将输入标记化的分割函数的签名。
// 参数为剩余未处理数据的初始子串，以及 atEOF 标志（报告 Reader 是否无更多数据可提供）。
// 返回值为输入前进的字节数、返回给用户的下一个标记（若有），以及错误（若有）。
//
// 若函数返回错误，扫描停止，此时部分输入可能被丢弃。
// 若错误为 ErrFinalToken，扫描无错误停止。
// 随 ErrFinalToken 传递的非 nil 标记将是最后一个标记，
// 随 ErrFinalToken 传递的 nil 标记会立即停止扫描。
//
// 否则，Scanner 前进输入。若标记非 nil，Scanner 将其返回给用户。
// 若标记为 nil，Scanner 读取更多数据并继续扫描；若无更多数据（atEOF 为 true），Scanner 返回。
// 若数据尚未包含完整标记（例如扫描行时无换行符），SplitFunc 可返回 (0, nil, nil)，
// 示意 Scanner 读取更多数据到切片，并从输入的同一点开始用更长的切片重试。
//
// 除非 atEOF 为 true，否则函数永远不会用空数据切片调用。
// 但若 atEOF 为 true，data 可能非空，且始终包含未处理文本。
type SplitFunc func(data []byte, atEOF bool) (advance int, token []byte, err error)

// Scanner 返回的错误
var (
	ErrTooLong         = errors.New("bufio.Scanner: token too long")
	ErrNegativeAdvance = errors.New("bufio.Scanner: SplitFunc returns negative advance count")
	ErrAdvanceTooFar   = errors.New("bufio.Scanner: SplitFunc returns advance count beyond input")
	ErrBadReadCount    = errors.New("bufio.Scanner: Read returned impossible count")
)

const (
	// MaxScanTokenSize 是缓冲标记的最大大小，除非用户通过 Scanner.Buffer 显式提供缓冲区。
	// 实际最大标记大小可能更小，因为缓冲区可能需要包含换行符等内容。
	MaxScanTokenSize = 64 * 1024

	startBufSize = 4096 // 缓冲区初始分配大小
)

// NewScanner 返回一个从 r 读取的新 Scanner。
// 分割函数默认为 ScanLines。
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{
		r:            r,
		split:        ScanLines,
		maxTokenSize: MaxScanTokenSize,
	}
}

// Err 返回 Scanner 遇到的首个非 EOF 错误。
func (s *Scanner) Err() error {
	if s.err == io.EOF {
		return nil
	}
	return s.err
}

// Bytes 返回 Scanner.Scan 调用生成的最新标记。
// 底层数组可能指向会被后续 Scan 调用覆盖的数据。不进行内存分配。
func (s *Scanner) Bytes() []byte {
	return s.token
}

// Text 将 Scanner.Scan 调用生成的最新标记作为新分配的字符串返回。
func (s *Scanner) Text() string {
	return string(s.token)
}

// ErrFinalToken 是一个特殊的标记错误值。
// 它旨在由分割函数返回，指示扫描应无错误停止。
// 若随此错误传递的标记非 nil，该标记即为最后一个标记。
//
// 该值用于提前停止处理，或在需要传递最终空标记（与 nil 标记不同）时使用。
// 可以用自定义错误值实现相同行为，但在此提供一个更整洁。
// 参见 emptyFinalToken 示例了解该值的用法。
var ErrFinalToken = errors.New("final token")

// Scan 将 Scanner 前进到下一个标记，该标记随后可通过 Scanner.Bytes 或 Scanner.Text 方法获取。
// 当无更多标记时（因到达输入末尾或错误）返回 false。
// Scan 返回 false 后，Scanner.Err 方法将返回扫描期间发生的任何错误，
// 但若为 io.EOF，Scanner.Err 将返回 nil。
// 若分割函数在未前进输入的情况下返回过多空标记，Scan 会 panic。
// 这是扫描器的常见错误模式。
func (s *Scanner) Scan() bool {
	if s.done {
		return false
	}
	s.scanCalled = true
	// 循环直到获得标记
	for {
		// 查看是否能用现有数据获得标记
		// 若数据已耗尽但有错误，给分割函数机会恢复剩余的可能为空的标记
		if s.end > s.start || s.err != nil {
			advance, token, err := s.split(s.buf[s.start:s.end], s.err != nil)
			if err != nil {
				if err == ErrFinalToken {
					s.token = token
					s.done = true
					// 当标记非 nil 时，意味着扫描随尾随标记停止，
					// 因此返回值应为 true 以指示标记存在
					return token != nil
				}
				s.setErr(err)
				return false
			}
			if !s.advance(advance) {
				return false
			}
			s.token = token
			if token != nil {
				if s.err == nil || advance > 0 {
					s.empties = 0
				} else {
					// 在 EOF 处返回标记但未前进输入
					s.empties++
					if s.empties > maxConsecutiveEmptyReads {
						panic("bufio.Scan: too many empty tokens without progressing")
					}
				}
				return true
			}
		}
		// 无法用现有数据生成标记
		// 若已遇到 EOF 或 I/O 错误，结束扫描
		if s.err != nil {
			// 关闭扫描
			s.start = 0
			s.end = 0
			return false
		}
		// 必须读取更多数据
		// 首先，若有大量空闲空间或需要空间，将数据移至缓冲区开头
		if s.start > 0 && (s.end == len(s.buf) || s.start > len(s.buf)/2) {
			copy(s.buf, s.buf[s.start:s.end])
			s.end -= s.start
			s.start = 0
		}
		// 缓冲区已满？若是，调整大小
		if s.end == len(s.buf) {
			// 保证下方乘法不溢出
			const maxInt = int(^uint(0) >> 1)
			if len(s.buf) >= s.maxTokenSize || len(s.buf) > maxInt/2 {
				s.setErr(ErrTooLong)
				return false
			}
			newSize := len(s.buf) * 2
			if newSize == 0 {
				newSize = startBufSize
			}
			newSize = min(newSize, s.maxTokenSize)
			newBuf := make([]byte, newSize)
			copy(newBuf, s.buf[s.start:s.end])
			s.buf = newBuf
			s.end -= s.start
			s.start = 0
		}
		// 终于可以读取输入了。确保不会因行为不当的 Reader 卡住
		// 官方上不需要这么做，但额外小心：Scanner 用于安全、简单的任务
		for loop := 0; ; {
			n, err := s.r.Read(s.buf[s.end:len(s.buf)])
			if n < 0 || len(s.buf)-s.end < n {
				s.setErr(ErrBadReadCount)
				break
			}
			s.end += n
			if err != nil {
				s.setErr(err)
				break
			}
			if n > 0 {
				s.empties = 0
				break
			}
			loop++
			if loop > maxConsecutiveEmptyReads {
				s.setErr(io.ErrNoProgress)
				break
			}
		}
	}
}

// advance 消耗缓冲区的 n 个字节。报告前进是否合法。
func (s *Scanner) advance(n int) bool {
	if n < 0 {
		s.setErr(ErrNegativeAdvance)
		return false
	}
	if n > s.end-s.start {
		s.setErr(ErrAdvanceTooFar)
		return false
	}
	s.start += n
	return true
}

// setErr 记录遇到的首个错误
func (s *Scanner) setErr(err error) {
	if s.err == nil || s.err == io.EOF {
		s.err = err
	}
}

// Buffer 控制 Scanner 的内存分配。
// 它设置扫描时使用的初始缓冲区，以及扫描期间可分配的最大缓冲区大小。
// 缓冲区的内容被忽略。
//
// 最大标记大小必须小于 max 和 cap(buf) 中的较大者。
// 若 max <= cap(buf)，Scanner.Scan 将仅使用此缓冲区，不进行内存分配。
//
// 默认情况下，Scanner.Scan 使用内部缓冲区，并将最大标记大小设为 MaxScanTokenSize。
//
// 若在扫描开始后调用 Buffer，会 panic。
func (s *Scanner) Buffer(buf []byte, max int) {
	if s.scanCalled {
		panic("Buffer called after Scan")
	}
	s.buf = buf[0:cap(buf)]
	s.maxTokenSize = max
}

// Split 设置 Scanner 的分割函数。
// 默认分割函数为 ScanLines。
//
// 若在扫描开始后调用 Split，会 panic。
func (s *Scanner) Split(split SplitFunc) {
	if s.scanCalled {
		panic("Split called after Scan")
	}
	s.split = split
}

// 分割函数

// ScanBytes 是 Scanner 的分割函数，将每个字节作为一个标记返回。
func ScanBytes(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	return 1, data[0:1], nil
}

var errorRune = []byte(string(utf8.RuneError))

// ScanRunes 是 Scanner 的分割函数，将每个 UTF-8 编码的字符作为一个标记返回。
// 返回的字符序列与对输入作为字符串进行 range 循环的结果等效，
// 这意味着错误的 UTF-8 编码会转换为 U+FFFD = "\xef\xbf\xbd"。
// 由于 Scan 接口，客户端无法区分正确编码的替换字符与编码错误。
func ScanRunes(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	// 快速路径 1：ASCII
	if data[0] < utf8.RuneSelf {
		return 1, data[0:1], nil
	}

	// 快速路径 2：正确的 UTF-8 解码无错误
	_, width := utf8.DecodeRune(data)
	if width > 1 {
		// 有效编码。正确编码的非 ASCII 字符的宽度不可能为 1
		return width, data[0:width], nil
	}

	// 已知是错误：width==1 且隐含 r==utf8.RuneError
	// 错误是因为没有完整的字符可解码吗？
	// FullRune 正确区分错误编码与不完整编码
	if !atEOF && !utf8.FullRune(data) {
		// 不完整；获取更多字节
		return 0, nil, nil
	}

	// 存在真正的 UTF-8 编码错误。返回正确编码的错误字符，但仅前进 1 个字节
	// 这与对错误编码字符串进行 range 循环的行为一致
	return 1, errorRune, nil
}

// dropCR 从数据中去除末尾的 \r
func dropCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\r' {
		return data[0 : len(data)-1]
	}
	return data
}

// ScanLines 是 Scanner 的分割函数，将每行文本作为标记返回，并去除任何尾随的行尾标记。
// 返回的行可能为空。行尾标记是一个可选的回车符后跟一个强制的换行符。
// 在正则表达式表示法中，它是 `\r?\n`。
// 输入的最后一个非空行即使没有换行符也会被返回。
func ScanLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		// 有完整的换行符结尾的行
		return i + 1, dropCR(data[0:i]), nil
	}
	// 若在 EOF 处，有最后的未终止行。返回它
	if atEOF {
		return len(data), dropCR(data), nil
	}
	// 请求更多数据
	return 0, nil, nil
}

// isSpace 报告字符是否为 Unicode 空白字符。
// 我们避免依赖 unicode 包，但在测试中检查实现的有效性
func isSpace(r rune) bool {
	if r <= '\u00FF' {
		// 明显的 ASCII 空白：\t 到 \r 加空格。再加两个 Latin-1 特殊字符
		switch r {
		case ' ', '\t', '\n', '\v', '\f', '\r':
			return true
		case '\u0085', '\u00A0':
			return true
		}
		return false
	}
	// 高值字符
	if '\u2000' <= r && r <= '\u200a' {
		return true
	}
	switch r {
	case '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000':
		return true
	}
	return false
}

// ScanWords 是 Scanner 的分割函数，将每个空格分隔的单词作为标记返回，并去除周围的空格。
// 它永远不会返回空字符串。空格的定义由 unicode.IsSpace 设定。
func ScanWords(data []byte, atEOF bool) (advance int, token []byte, err error) {
	// 跳过开头的空格
	start := 0
	for width := 0; start < len(data); start += width {
		var r rune
		r, width = utf8.DecodeRune(data[start:])
		if !isSpace(r) {
			break
		}
	}
	// 扫描直到空格，标记单词结束
	for width, i := 0, start; i < len(data); i += width {
		var r rune
		r, width = utf8.DecodeRune(data[i:])
		if isSpace(r) {
			return i + width, data[start:i], nil
		}
	}
	// 若在 EOF 处，有最后的非空、未终止单词。返回它
	if atEOF && len(data) > start {
		return len(data), data[start:], nil
	}
	// 请求更多数据
	return start, nil, nil
}
