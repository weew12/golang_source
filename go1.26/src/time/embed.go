// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 此文件与构建标签 timetzdata 一起使用，以将 tzdata 嵌入
// 二进制文件中。

//go:build timetzdata

package time

import _ "time/tzdata"
