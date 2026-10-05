// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import (
	"context"
	"errors"
)

// NewMultiHandler 使用给定的 Handler 创建一个 [MultiHandler]。
func NewMultiHandler(handlers ...Handler) *MultiHandler {
	h := make([]Handler, len(handlers))
	copy(h, handlers)
	return &MultiHandler{multi: h}
}

// MultiHandler 是一个 [Handler]，它会调用所有给定的 Handler。
// 它的 Enabled 方法报告是否有任何 handler 的 Enabled 方法返回 true。
// 它的 Handle、WithAttrs 和 WithGroup 方法会在每个已启用的 handler 上调用相应的方法。
type MultiHandler struct {
	multi []Handler
}

func (h *MultiHandler) Enabled(ctx context.Context, l Level) bool {
	for i := range h.multi {
		if h.multi[i].Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (h *MultiHandler) Handle(ctx context.Context, r Record) error {
	var errs []error
	for i := range h.multi {
		if h.multi[i].Enabled(ctx, r.Level) {
			if err := h.multi[i].Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (h *MultiHandler) WithAttrs(attrs []Attr) Handler {
	handlers := make([]Handler, 0, len(h.multi))
	for i := range h.multi {
		handlers = append(handlers, h.multi[i].WithAttrs(attrs))
	}
	return &MultiHandler{multi: handlers}
}

func (h *MultiHandler) WithGroup(name string) Handler {
	handlers := make([]Handler, 0, len(h.multi))
	for i := range h.multi {
		handlers = append(handlers, h.multi[i].WithGroup(name))
	}
	return &MultiHandler{multi: handlers}
}
