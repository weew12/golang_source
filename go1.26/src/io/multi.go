// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package io

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) {
	return 0, EOF
}

type multiReader struct {
	readers []Reader
}

func (mr *multiReader) Read(p []byte) (n int, err error) {
	for len(mr.readers) > 0 {
		// 优化：展平嵌套的 multiReader（问题 13558）
		if len(mr.readers) == 1 {
			if r, ok := mr.readers[0].(*multiReader); ok {
				mr.readers = r.readers
				continue
			}
		}
		n, err = mr.readers[0].Read(p)
		if err == EOF {
			// 使用 eofReader 而非 nil，避免展平操作后出现空指针恐慌（问题 18232）
			mr.readers[0] = eofReader{} // 允许更早的垃圾回收
			mr.readers = mr.readers[1:]
		}
		if n > 0 || err != EOF {
			if err == EOF && len(mr.readers) > 0 {
				// 暂不返回 EOF，仍有更多读取器待读取
				err = nil
			}
			return
		}
	}
	return 0, EOF
}

func (mr *multiReader) WriteTo(w Writer) (sum int64, err error) {
	return mr.writeToWithBuffer(w, make([]byte, 1024*32))
}

func (mr *multiReader) writeToWithBuffer(w Writer, buf []byte) (sum int64, err error) {
	for i, r := range mr.readers {
		var n int64
		if subMr, ok := r.(*multiReader); ok { // 对嵌套的 multiReader 复用缓冲区
			n, err = subMr.writeToWithBuffer(w, buf)
		} else {
			n, err = copyBuffer(w, r, buf)
		}
		sum += n
		if err != nil {
			mr.readers = mr.readers[i:] // 允许出错后恢复/重试
			return sum, err
		}
		mr.readers[i] = nil // 允许尽早垃圾回收
	}
	mr.readers = nil
	return sum, nil
}

var _ WriterTo = (*multiReader)(nil)

// MultiReader 返回一个读取器，该读取器是所提供输入读取器的逻辑拼接
// 会按顺序读取这些输入读取器。一旦所有输入都返回 EOF，Read 方法将返回 EOF
// 若任意一个读取器返回非空、非 EOF 的错误，Read 方法将返回该错误
func MultiReader(readers ...Reader) Reader {
	r := make([]Reader, len(readers))
	copy(r, readers)
	return &multiReader{r}
}

type multiWriter struct {
	writers []Writer
}

func (t *multiWriter) Write(p []byte) (n int, err error) {
	for _, w := range t.writers {
		n, err = w.Write(p)
		if err != nil {
			return
		}
		if n != len(p) {
			err = ErrShortWrite
			return
		}
	}
	return len(p), nil
}

var _ StringWriter = (*multiWriter)(nil)

func (t *multiWriter) WriteString(s string) (n int, err error) {
	var p []byte // 按需延迟初始化
	for _, w := range t.writers {
		if sw, ok := w.(StringWriter); ok {
			n, err = sw.WriteString(s)
		} else {
			if p == nil {
				p = []byte(s)
			}
			n, err = w.Write(p)
		}
		if err != nil {
			return
		}
		if n != len(s) {
			err = ErrShortWrite
			return
		}
	}
	return len(s), nil
}

// MultiWriter 创建一个写入器，该写入器会将写入操作复制到所有提供的写入器
// 类似于 Unix 的 tee(1) 命令
//
// 每次写入会依次写入列表中的每个写入器
// 若列表中的某个写入器返回错误，整个写入操作将停止并返回该错误
// 不会继续向后续写入器执行写入
func MultiWriter(writers ...Writer) Writer {
	allWriters := make([]Writer, 0, len(writers))
	for _, w := range writers {
		if mw, ok := w.(*multiWriter); ok {
			allWriters = append(allWriters, mw.writers...)
		} else {
			allWriters = append(allWriters, w)
		}
	}
	return &multiWriter{allWriters}
}
