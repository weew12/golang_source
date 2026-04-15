// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syntax

// Simplify 返回一个与 re 等价的正则表达式，但没有计数重复
// 以及各种其他简化，例如将 /(?:a+)+/ 重写为 /a+/。
// 结果正则表达式将正确执行，但其字符串表示
// 不会产生相同的解析树，因为捕获括号
// 可能已被复制或移除。例如，/(x){1,2}/ 的简化形式
// 为 /(x)(x)?/，但两个括号都作为 $1 捕获。
// 返回的正则表达式可能与原始表达式共享结构，也可能就是原始表达式。
func (re *Regexp) Simplify() *Regexp {
	if re == nil {
		return nil
	}
	switch re.Op {
	case OpCapture, OpConcat, OpAlternate:
		// 简化子项，如果子项发生变化则构建新的 Regexp。
		nre := re
		for i, sub := range re.Sub {
			nsub := sub.Simplify()
			if nre == re && nsub != sub {
				// 开始复制。
				nre = new(Regexp)
				*nre = *re
				nre.Rune = nil
				nre.Sub = append(nre.Sub0[:0], re.Sub[:i]...)
			}
			if nre != re {
				nre.Sub = append(nre.Sub, nsub)
			}
		}
		return nre

	case OpStar, OpPlus, OpQuest:
		sub := re.Sub[0].Simplify()
		return simplify1(re.Op, re.Flags, sub, re)

	case OpRepeat:
		// 特殊的特殊情况：x{0} 匹配空字符串
		// 甚至不需要考虑 x。
		if re.Min == 0 && re.Max == 0 {
			return &Regexp{Op: OpEmptyMatch}
		}

		// 精彩的部分开始了。
		sub := re.Sub[0].Simplify()

		// x{n,} 表示至少 n 次 x 的匹配。
		if re.Max == -1 {
			// 特殊情况：x{0,} 是 x*。
			if re.Min == 0 {
				return simplify1(OpStar, re.Flags, sub, nil)
			}

			// 特殊情况：x{1,} 是 x+。
			if re.Min == 1 {
				return simplify1(OpPlus, re.Flags, sub, nil)
			}

			// 一般情况：x{4,} 是 xxxx+。
			nre := &Regexp{Op: OpConcat}
			nre.Sub = nre.Sub0[:0]
			for i := 0; i < re.Min-1; i++ {
				nre.Sub = append(nre.Sub, sub)
			}
			nre.Sub = append(nre.Sub, simplify1(OpPlus, re.Flags, sub, nil))
			return nre
		}

		// 特殊情况 x{0} 已在上面处理。

		// 特殊情况：x{1} 就是 x。
		if re.Min == 1 && re.Max == 1 {
			return sub
		}

		// 一般情况：x{n,m} 表示 n 个 x 副本和 m 个 x? 副本。
		// 如果我们嵌套最后的 m 个副本，机器将做更少的工作，
		// 使得 x{2,5} = xx(x(x(x)?)?)?

		// 构建前导前缀：xx。
		var prefix *Regexp
		if re.Min > 0 {
			prefix = &Regexp{Op: OpConcat}
			prefix.Sub = prefix.Sub0[:0]
			for i := 0; i < re.Min; i++ {
				prefix.Sub = append(prefix.Sub, sub)
			}
		}

		// 构建并附加后缀：(x(x(x)?)?)?
		if re.Max > re.Min {
			suffix := simplify1(OpQuest, re.Flags, sub, nil)
			for i := re.Min + 1; i < re.Max; i++ {
				nre2 := &Regexp{Op: OpConcat}
				nre2.Sub = append(nre2.Sub0[:0], sub, suffix)
				suffix = simplify1(OpQuest, re.Flags, nre2, nil)
			}
			if prefix == nil {
				return suffix
			}
			prefix.Sub = append(prefix.Sub, suffix)
		}
		if prefix != nil {
			return prefix
		}

		// 某些退化情况，如 min > max 或 min < max < 0。
		// 作为不可能的匹配处理。
		return &Regexp{Op: OpNoMatch}
	}

	return re
}

// simplify1 为一元操作符 OpStar、OpPlus 和 OpQuest 实现 Simplify。
// 它返回等价于以下结构的简单正则表达式：
//
//	Regexp{Op: op, Flags: flags, Sub: {sub}}
//
// 假设 sub 已经是简单的，且不会先分配该结构。
// 如果要返回的正则表达式等价于 re，
// simplify1 将返回 re。
//
// simplify1 从 Simplify 中提取出来，因为其他操作符的实现
// 会生成这些一元表达式。
// 让它们调用 simplify1 可以确保它们生成的表达式是简单的。
func simplify1(op Op, flags Flags, sub, re *Regexp) *Regexp {
	// 特殊情况：重复空字符串任意多次，
	// 但结果仍然是空字符串。
	if sub.Op == OpEmptyMatch {
		return sub
	}
	// 如果标志匹配，则操作符是幂等的。
	if op == sub.Op && flags&NonGreedy == sub.Flags&NonGreedy {
		return sub
	}
	if re != nil && re.Op == op && re.Flags&NonGreedy == flags&NonGreedy && sub == re.Sub[0] {
		return re
	}

	re = &Regexp{Op: op, Flags: flags}
	re.Sub = append(re.Sub0[:0], sub)
	return re
}
