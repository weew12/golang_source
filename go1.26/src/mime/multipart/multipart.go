// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

/*
Package multipart 实现 MIME 多部分解析，如 RFC 2046 所定义。

该实现足以处理 HTTP（RFC 2388）和流行浏览器生成的多部分主体。

# 限制

为防止恶意输入，此包对所处理的 MIME 数据大小设置了限制。

[Reader.NextPart] 和 [Reader.NextRawPart] 将每个部分的标题数限制为 10000，
[Reader.ReadForm] 将所有 FileHeader 中的标题总数限制为 10000。
这些限制可通过 GODEBUG=multipartmaxheaders=<值> 设置进行调整。

Reader.ReadForm 进一步将表单中的部分数限制为 1000。
此限制可通过 GODEBUG=multipartmaxparts=<值> 设置进行调整。
*/
package multipart

import (
	"bufio"
	"bytes"
	"fmt"
	"internal/godebug"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
)

var emptyParams = make(map[string]string)

// 此常量至少需要为 76 才能使此包正常工作。
// 这是因为 \r\n--separator_of_len_70- 会填满缓冲区，
// 此时从中消费单个字节是不安全的。
const peekBufferSize = 4096

// Part 代表多部分主体中的单个部分。
type Part struct {
	// 部分的正文标题（若有），其键以与 Go http.Request 标题相同的方式规范化。
	// 例如，"foo-bar" 会变为大小写 "Foo-Bar"。
	Header textproto.MIMEHeader

	mr *Reader

	disposition       string
	dispositionParams map[string]string

	// r 要么是直接从 mr 读取的读取器，要么是包装该读取器的读取器，
	// 对 Content-Transfer-Encoding 进行解码。
	r io.Reader

	n       int   // mr.bufReader 中等待的已知数据字节数
	total   int64 // 已读取的数据字节总数
	err     error // 当 n == 0 时返回的错误
	readErr error // 从 mr.bufReader 观察到的读取错误
}

// FormName 若 p 的 Content-Disposition 为 "form-data" 类型则返回 name 参数。
// 否则返回空字符串。
func (p *Part) FormName() string {
	// See https://tools.ietf.org/html/rfc2183 section 2 for EBNF
	// of Content-Disposition value format.
	if p.dispositionParams == nil {
		p.parseContentDisposition()
	}
	if p.disposition != "form-data" {
		return ""
	}
	return p.dispositionParams["name"]
}

// FileName 返回 [Part] 的 Content-Disposition 头部中的 filename 参数。
// 若非空，在返回前会通过 filepath.Base（取决于平台）处理。
func (p *Part) FileName() string {
	if p.dispositionParams == nil {
		p.parseContentDisposition()
	}
	filename := p.dispositionParams["filename"]
	if filename == "" {
		return ""
	}
	// RFC 7578, Section 4.2 requires that if a filename is provided, the
	// directory path information must not be used.
	return filepath.Base(filename)
}

func (p *Part) parseContentDisposition() {
	v := p.Header.Get("Content-Disposition")
	var err error
	p.disposition, p.dispositionParams, err = mime.ParseMediaType(v)
	if err != nil {
		p.dispositionParams = emptyParams
	}
}

// NewReader 创建一个新的 multipart [Reader]，从 r 读取，使用给定的 MIME 边界。
//
// 边界通常从消息的 "Content-Type" 头部的 "boundary" 参数获取。
// 使用 [mime.ParseMediaType] 解析此类头部。
func NewReader(r io.Reader, boundary string) *Reader {
	b := []byte("\r\n--" + boundary + "--")
	return &Reader{
		bufReader:        bufio.NewReaderSize(&stickyErrorReader{r: r}, peekBufferSize),
		nl:               b[:2],
		nlDashBoundary:   b[:len(b)-2],
		dashBoundaryDash: b[2:],
		dashBoundary:     b[2 : len(b)-2],
	}
}

// stickyErrorReader 是一个 io.Reader，一旦看到错误就永远不会调用其底层 Reader 的 Read。
// （因为 io.Reader 接口的契约对错误之后的 Read 返回值没有任何保证，
// 而此包在出错后会进行多次 Read）
type stickyErrorReader struct {
	r   io.Reader
	err error
}

func (r *stickyErrorReader) Read(p []byte) (n int, _ error) {
	if r.err != nil {
		return 0, r.err
	}
	n, r.err = r.r.Read(p)
	return n, r.err
}

func newPart(mr *Reader, rawPart bool, maxMIMEHeaderSize, maxMIMEHeaders int64) (*Part, error) {
	bp := &Part{
		Header: make(map[string][]string),
		mr:     mr,
	}
	if err := bp.populateHeaders(maxMIMEHeaderSize, maxMIMEHeaders); err != nil {
		return nil, err
	}
	bp.r = partReader{bp}

	// rawPart 用于在 Part.NextPart 和 Part.NextRawPart 之间切换。
	if !rawPart {
		const cte = "Content-Transfer-Encoding"
		if strings.EqualFold(bp.Header.Get(cte), "quoted-printable") {
			bp.Header.Del(cte)
			bp.r = quotedprintable.NewReader(bp.r)
		}
	}
	return bp, nil
}

func (p *Part) populateHeaders(maxMIMEHeaderSize, maxMIMEHeaders int64) error {
	r := textproto.NewReader(p.mr.bufReader)
	header, err := readMIMEHeader(r, maxMIMEHeaderSize, maxMIMEHeaders)
	if err == nil {
		p.Header = header
	}
	// TODO: Add a distinguishable error to net/textproto.
	if err != nil && err.Error() == "message too large" {
		err = ErrMessageTooLarge
	}
	return err
}

// Read 读取一个部分的主体，在其标题之后、下一个部分开始之前（若有）。
func (p *Part) Read(d []byte) (n int, err error) {
	return p.r.Read(d)
}

// partReader 通过直接从包装的 *Part 读取原始字节来实现 io.Reader，
// 不进行任何 Transfer-Encoding 解码。
type partReader struct {
	p *Part
}

func (pr partReader) Read(d []byte) (int, error) {
	p := pr.p
	br := p.mr.bufReader

	// 读取到缓冲区，直到我们识别出一些要返回的数据，
	// 或者找到停止的理由（边界或读取错误）。
	for p.n == 0 && p.err == nil {
		peek, _ := br.Peek(br.Buffered())
		p.n, p.err = scanUntilBoundary(peek, p.mr.dashBoundary, p.mr.nlDashBoundary, p.total, p.readErr)
		if p.n == 0 && p.err == nil {
			// 强制缓冲 I/O 将更多数据读入缓冲区。
			_, p.readErr = br.Peek(len(peek) + 1)
			if p.readErr == io.EOF {
				p.readErr = io.ErrUnexpectedEOF
			}
		}
	}

	// 从"要返回的数据"部分读取缓冲区。
	if p.n == 0 {
		return 0, p.err
	}
	n := len(d)
	if n > p.n {
		n = p.n
	}
	n, _ = br.Read(d[:n])
	p.total += int64(n)
	p.n -= n
	if p.n == 0 {
		return n, p.err
	}
	return n, nil
}

// scanUntilBoundary 扫描 buf 以识别可以安全地作为 Part 主体的部分返回的数据量。
// dashBoundary 为 "--boundary"。
// nlDashBoundary 为 "\r\n--boundary" 或 "\n--boundary"，取决于当前模式。
// 下面的注释（和名称）假定为 "\n--boundary"，但两者均可接受。
// total 是到目前为止已读取的字节数。若 total == 0，则识别前导 "--boundary"。
// readErr 是读取 buf 中字节后出现的读取错误（若有）。
// scanUntilBoundary 返回可作为 Part 主体部分返回的来自 buf 的数据字节数，
// 以及在这些数据字节完成后要返回的错误（若有）。
func scanUntilBoundary(buf, dashBoundary, nlDashBoundary []byte, total int64, readErr error) (int, error) {
	if total == 0 {
		// At beginning of body, allow dashBoundary.
		if bytes.HasPrefix(buf, dashBoundary) {
			switch matchAfterPrefix(buf, dashBoundary, readErr) {
			case -1:
				return len(dashBoundary), nil
			case 0:
				return 0, nil
			case +1:
				return 0, io.EOF
			}
		}
		if bytes.HasPrefix(dashBoundary, buf) {
			return 0, readErr
		}
	}

	// Search for "\n--boundary".
	if i := bytes.Index(buf, nlDashBoundary); i >= 0 {
		switch matchAfterPrefix(buf[i:], nlDashBoundary, readErr) {
		case -1:
			return i + len(nlDashBoundary), nil
		case 0:
			return i, nil
		case +1:
			return i, io.EOF
		}
	}
	if bytes.HasPrefix(nlDashBoundary, buf) {
		return 0, readErr
	}

	// Otherwise, anything up to the final \n is not part of the boundary
	// and so must be part of the body.
	// Also if the section from the final \n onward is not a prefix of the boundary,
	// it too must be part of the body.
	i := bytes.LastIndexByte(buf, nlDashBoundary[0])
	if i >= 0 && bytes.HasPrefix(nlDashBoundary, buf[i:]) {
		return i, nil
	}
	return len(buf), readErr
}

// 匹配结束后缀，检查 buf 是否应被视为匹配边界。
// 前缀为 "--boundary" 或 "\r\n--boundary" 或 "\n--boundary"，
// 调用者已验证 bytes.HasPrefix(buf, prefix) 为 true。
//
// 若缓冲区确实匹配边界，matchAfterPrefix 返回 +1，
// 意味着前缀后跟双破折号、空格、制表符、cr、nl 或输入结束。
// 若缓冲区肯定不匹配边界，返回 -1，
// 意味着前缀后跟其他字符。
// 例如，"--foobar" 不匹配 "--foo"。
// 若需要读取更多输入才能做出决定，返回 0，
// 意味着 len(buf) == len(prefix) 且 readErr == nil。
func matchAfterPrefix(buf, prefix []byte, readErr error) int {
	if len(buf) == len(prefix) {
		if readErr != nil {
			return +1
		}
		return 0
	}
	c := buf[len(prefix)]

	if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
		return +1
	}

	// Try to detect boundaryDash
	if c == '-' {
		if len(buf) == len(prefix)+1 {
			if readErr != nil {
				// Prefix + "-" does not match
				return -1
			}
			return 0
		}
		if buf[len(prefix)+1] == '-' {
			return +1
		}
	}

	return -1
}

func (p *Part) Close() error {
	io.Copy(io.Discard, p)
	return nil
}

// Reader 是 MIME 多部分主体的迭代器。
// Reader 的底层解析器根据需要消费其输入。不支持寻址。
type Reader struct {
	bufReader *bufio.Reader
	tempDir   string // 用于测试

	currentPart *Part
	partsRead   int

	nl               []byte // "\r\n" 或 "\n"（在看看到第一个边界行后设置）
	nlDashBoundary   []byte // nl + "--boundary"
	dashBoundaryDash []byte // "--boundary--"
	dashBoundary     []byte // "--boundary"
}

// maxMIMEHeaderSize 是我们将解析的 MIME 标题的最大大小，
// 包括标题键、值和映射开销。
const maxMIMEHeaderSize = 10 << 20

// multipartmaxheaders 是 NextPart 将返回的最大标题条目数，
// 也是 Reader.ReadForm 将在 FileHeaders 中返回的最大标题条目总数。
var multipartmaxheaders = godebug.New("multipartmaxheaders")

func maxMIMEHeaders() int64 {
	if s := multipartmaxheaders.Value(); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v >= 0 {
			multipartmaxheaders.IncNonDefault()
			return v
		}
	}
	return 10000
}

// NextPart 返回多部分中的下一个部分或一个错误。
// 当没有更多部分时，返回错误 [io.EOF]。
//
// 作为一个特例，若 "Content-Transfer-Encoding" 头部具有 "quoted-printable" 值，
// 该头部会被隐藏，正文在 Read 调用期间会被透明解码。
func (r *Reader) NextPart() (*Part, error) {
	return r.nextPart(false, maxMIMEHeaderSize, maxMIMEHeaders())
}

// NextRawPart 返回多部分中的下一个部分或一个错误。
// 当没有更多部分时，返回错误 [io.EOF]。
//
// 与 [Reader.NextPart] 不同，它对 "Content-Transfer-Encoding: quoted-printable" 没有特殊处理。
func (r *Reader) NextRawPart() (*Part, error) {
	return r.nextPart(true, maxMIMEHeaderSize, maxMIMEHeaders())
}

func (r *Reader) nextPart(rawPart bool, maxMIMEHeaderSize, maxMIMEHeaders int64) (*Part, error) {
	if r.currentPart != nil {
		r.currentPart.Close()
	}
	if string(r.dashBoundary) == "--" {
		return nil, fmt.Errorf("multipart: boundary is empty")
	}
	expectNewPart := false
	for {
		line, err := r.bufReader.ReadSlice('\n')

		if err == io.EOF && r.isFinalBoundary(line) {
			// 若缓冲区以 "--boundary--" 结尾但没有尾随 "\r\n"，
			// ReadSlice 将返回错误（因为缺少 '\n'），但这是有效的
			// 多部分 EOF，所以我们需要返回 io.EOF 而不是 fmt 包装的错误。
			return nil, io.EOF
		}
		if err != nil {
			return nil, fmt.Errorf("multipart: NextPart: %w", err)
		}

		if r.isBoundaryDelimiterLine(line) {
			r.partsRead++
			bp, err := newPart(r, rawPart, maxMIMEHeaderSize, maxMIMEHeaders)
			if err != nil {
				return nil, err
			}
			r.currentPart = bp
			return bp, nil
		}

		if r.isFinalBoundary(line) {
			// 期望 EOF
			return nil, io.EOF
		}

		if expectNewPart {
			return nil, fmt.Errorf("multipart: expecting a new Part; got line %q", string(line))
		}

		if r.partsRead == 0 {
			// 跳过行
			continue
		}

		// 消费 "\n" 或 "\r\n" 分隔符，即前一个部分的主体
		// 与我们现在期望跟随的边界行之间的分隔符。（一个新部分或结束边界）
		if bytes.Equal(line, r.nl) {
			expectNewPart = true
			continue
		}

		return nil, fmt.Errorf("multipart: unexpected line in Next(): %q", line)
	}
}

// isFinalBoundary 报告 line 是否为最终边界行，表示所有部分已结束。
// 它匹配 `^--boundary--[ \t]*(\r\n)?$`。
func (r *Reader) isFinalBoundary(line []byte) bool {
	if !bytes.HasPrefix(line, r.dashBoundaryDash) {
		return false
	}
	rest := line[len(r.dashBoundaryDash):]
	rest = skipLWSPChar(rest)
	return len(rest) == 0 || bytes.Equal(rest, r.nl)
}

func (r *Reader) isBoundaryDelimiterLine(line []byte) (ret bool) {
	// https://tools.ietf.org/html/rfc2046#section-5.1
	//   The boundary delimiter line is then defined as a line
	//   consisting entirely of two hyphen characters ("-",
	//   decimal value 45) followed by the boundary parameter
	//   value from the Content-Type header field, optional linear
	//   whitespace, and a terminating CRLF.
	if !bytes.HasPrefix(line, r.dashBoundary) {
		return false
	}
	rest := line[len(r.dashBoundary):]
	rest = skipLWSPChar(rest)

	// 在第一个部分，检查我们的行是否以 \n 结尾而不是 \r\n，
	// 如果是则切换到该模式。这是对规范的反面，但实践中会发生。
	if r.partsRead == 0 && len(rest) == 1 && rest[0] == '\n' {
		r.nl = r.nl[1:]
		r.nlDashBoundary = r.nlDashBoundary[1:]
	}
	return bytes.Equal(rest, r.nl)
}

// skipLWSPChar 返回去除前导空格和制表符的 b。
// RFC 822 定义：
//
//	LWSP-char = SPACE / HTAB
func skipLWSPChar(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}
