// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// 本文件实现 nat 到字符串的转换函数。

package big

import (
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"slices"
	"sync"
)

const digits = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// 注意：MaxBase = len(digits)，但为保持 API 兼容性
//       它必须保持为无类型的 rune 常量。

// MaxBase 是字符串转换所接受的最大数字基数。
const MaxBase = 10 + ('z' - 'a' + 1) + ('Z' - 'A' + 1)
const maxBaseSmall = 10 + ('z' - 'a' + 1)

// maxPow 返回 (b**n, n)，其中 b**n 是满足 b**n <= _M 的最大幂。
// 例如，对于 64 位 Word 中的 19 个十进制数字，maxPow(10) == (1e19, 19)。
// 换句话说，基数 b 下至多 n 个数字能放入一个 Word。
// TODO(gri) 用构建时生成的表替换它。
func maxPow(b Word) (p Word, n int) {
	p, n = b, 1 // 假设 b <= _M
	for max := _M / b; p <= max; {
		// p == b**n && p <= max
		p *= b
		n++
	}
	// p == b**n && p <= _M
	return
}

// pow 返回 x**n（n > 0），否则返回 1。
func pow(x Word, n int) (p Word) {
	// n == 所有 0 <= i < imax 的 bi * 2**i 之和，bi 为 0 或 1
	// 因此 x**n == 所有 bi == 1 的 i 的 x**(2**i) 之积
	// （俄罗斯农夫指数算法）
	p = 1
	for n > 0 {
		if n&1 != 0 {
			p *= x
		}
		x *= x
		n >>= 1
	}
	return
}

// scan 错误
var (
	errNoDigits = errors.New("number has no digits")
	errInvalSep = errors.New("'_' must separate successive digits")
)

// scan 从 r（表示给定转换基数下的无符号数）扫描出最长可能前缀
// 所对应的数。
// scan 返回相应的自然数 res、实际基数 b、
// 位数 count，以及读取或语法错误 err（如果有）。
//
// 对于基数 0，下划线字符 “_” 可以出现在基数前缀与相邻数字之间，
// 以及连续数字之间；这样的下划线不改变数的值，也不改变返回的
// 位数。如果没有其他错误，下划线的错误放置会被报告为错误。
// 如果 base != 0，则不识别下划线，因此它们会像任何其他
// 不是有效小数点或数字的字符一样终止扫描。
//
//	number    = mantissa | prefix pmantissa .
//	prefix    = "0" [ "b" | "B" | "o" | "O" | "x" | "X" ] .
//	mantissa  = digits "." [ digits ] | digits | "." digits .
//	pmantissa = [ "_" ] digits "." [ digits ] | [ "_" ] digits | "." digits .
//	digits    = digit { [ "_" ] digit } .
//	digit     = "0" ... "9" | "a" ... "z" | "A" ... "Z" .
//
// 除非设置了 fracOk，否则 base 参数必须为 0 或 2 到 MaxBase 之间的值。
// 如果设置了 fracOk，则 base 参数必须是 0、2、8、10 或 16 之一。
// 提供无效的 base 参数会导致运行
// 时 panic。
//
// 对于基数 0，数前缀决定实际基数：“0b” 或 “0B” 前缀选择基数 2，
// “0o” 或 “0O” 选择基数 8，“0x” 或 “0X” 选择基数 16。
// 如果 fracOk 为 false，则 “0” 前缀（紧跟数字）
// 也会选择基数 8。否则，所选基数为 10，且不接受任何前缀。
//
// 如果设置了 fracOk，则允许一个句点后跟小数部分。
// 结果值的计算方式如同不存在句点；而 count 值用于确定小数部分。
//
// 对于基数 <= 36，大写和小写字母被视为相同：
// 字母 'a' 到 'z' 和 'A' 到 'Z' 表示数字值 10 到 35。
// 对于基数 > 36，大写字母 'A' 到 'Z' 表示数字值 36 到 61。
//
// 结果位数 count > 0 对应于解析到的（非前缀）数字个数。
// 位数 count <= 0 表示存在句点（仅当设置了 fracOk 时），
// 且 -count 是找到的小数位数。
// 在这种情况下，所扫描数的实际值为 res * b**count。
func (z nat) scan(r io.ByteScanner, base int, fracOk bool) (res nat, b, count int, err error) {
	// 拒绝无效基数。
	baseOk := base == 0 ||
		!fracOk && 2 <= base && base <= MaxBase ||
		fracOk && (base == 2 || base == 8 || base == 10 || base == 16)
	if !baseOk {
		panic(fmt.Sprintf("invalid number base %d", base))
	}

	// prev 编码之前见到的字符：它是 '_'、'0'（一个数字）
	// 或 '.'（其他任何字符）之一。有效的分隔符 '_'
	// 只能出现在数字之后，且仅当 base == 0。
	prev := '.'
	invalSep := false

	// 向前看一个字符
	ch, err := r.ReadByte()

	// 确定实际基数。
	b, prefix := base, 0
	if base == 0 {
		// 除非有基数前缀，否则实际基数为 10。
		b = 10
		if err == nil && ch == '0' {
			prev = '0'
			count = 1
			ch, err = r.ReadByte()
			if err == nil {
				// 可能是 0b、0B、0o、0O、0x、0X 之一
				switch ch {
				case 'b', 'B':
					b, prefix = 2, 'b'
				case 'o', 'O':
					b, prefix = 8, 'o'
				case 'x', 'X':
					b, prefix = 16, 'x'
				default:
					if !fracOk {
						b, prefix = 8, '0'
					}
				}
				if prefix != 0 {
					count = 0 // 前缀不计入
					if prefix != '0' {
						ch, err = r.ReadByte()
					}
				}
			}
		}
	}

	// 转换字符串。
	// 算法：将数字按至多 n 个一组收集到 di 中。
	// 对于恰好能装入字的基数（2、4、16），将 di 直接追加到
	// int 表示中，最后再反转（bn==0 标记这种情况）。
	// 对于其他基数，对每一组使用 mulAddWW 将 z 上移一组
	// 并将 di 加到结果中。
	// 更巧妙一些的话，我们还可以处理像 8 和 32 这样不能很好地
	// 装入字的二进制基数（对应 3 位和 5 位的块），
	// 但它们并不太重要。
	z = z[:0]
	b1 := Word(b)
	var bn Word // b1**n（对于 b=2,4,16 的特殊位打包情形为 0）
	var n int   // 能放入 Word 的最大数字个数
	switch b {
	case 2: // 每个数字 1 位
		n = _W
	case 4: // 每个数字 2 位
		n = _W / 2
	case 16: // 每个数字 4 位
		n = _W / 4
	default:
		bn, n = maxPow(b1)
	}
	di := Word(0) // 0 <= di < b1**i < bn
	i := 0        // 0 <= i < n
	dp := -1      // 小数点的位置
	for err == nil {
		if ch == '.' && fracOk {
			fracOk = false
			if prev == '_' {
				invalSep = true
			}
			prev = '.'
			dp = count
		} else if ch == '_' && base == 0 {
			if prev != '0' {
				invalSep = true
			}
			prev = '_'
		} else {
			// 将 rune 转换为数字值 d1
			var d1 Word
			switch {
			case '0' <= ch && ch <= '9':
				d1 = Word(ch - '0')
			case 'a' <= ch && ch <= 'z':
				d1 = Word(ch - 'a' + 10)
			case 'A' <= ch && ch <= 'Z':
				if b <= maxBaseSmall {
					d1 = Word(ch - 'A' + 10)
				} else {
					d1 = Word(ch - 'A' + maxBaseSmall)
				}
			default:
				d1 = MaxBase + 1
			}
			if d1 >= b1 {
				r.UnreadByte() // ch 不再属于该数字
				break
			}
			prev = '0'
			count++

			// 将 d1 收集到 di 中
			di = di*b1 + d1
			i++

			// 如果 di 已“满”，则将其加到结果中
			if i == n {
				if bn == 0 {
					z = append(z, di)
				} else {
					z = z.mulAddWW(z, bn, di)
				}
				di = 0
				i = 0
			}
		}

		ch, err = r.ReadByte()
	}

	if err == io.EOF {
		err = nil
	}

	// 其他错误优先于无效分隔符
	if err == nil && (invalSep || prev == '_') {
		err = errInvalSep
	}

	if count == 0 {
		// 未找到任何数字
		if prefix == '0' {
			// 只有八进制前缀 0（后面可能跟着分隔符和大于 7 的数字）；
			// 解释为十进制 0
			return z[:0], 10, 1, err
		}
		err = errNoDigits // 继续向下执行；结果将为 0
	}

	if bn == 0 {
		if i > 0 {
			// 将剩余的数字块加到结果中。
			// 将该组的数字左对齐；反转后会再移回来。
			z = append(z, di*pow(b1, n-i))
		}
		slices.Reverse(z)
		z = z.norm()
		if i > 0 {
			z = z.rsh(z, uint(n-i)*uint(_W/n))
		}
	} else {
		if i > 0 {
			// 将剩余的数字块加到结果中。
			z = z.mulAddWW(z, pow(b1, i), di)
		}
	}
	res = z

	// 如有小数部分，则调整 count
	if dp >= 0 {
		// 0 <= dp <= count
		count = dp - count
	}

	return
}

// utoa 将 x 转换为给定基数下的 ASCII 表示；
// base 必须在 2 到 MaxBase 之间（含）。
func (x nat) utoa(base int) []byte {
	return x.itoa(false, base)
}

// itoa 类似 utoa，但如果 neg && x != 0，则会在前面加上 '-'。
func (x nat) itoa(neg bool, base int) []byte {
	if base < 2 || base > MaxBase {
		panic("invalid base")
	}

	// x == 0
	if len(x) == 0 {
		return []byte("0")
	}
	// len(x) > 0

	// 分配用于转换的缓冲区
	i := int(float64(x.bitLen())/math.Log2(float64(base))) + 1 // 至多偏差 1
	if neg {
		i++
	}
	s := make([]byte, i)

	// 分别转换 2 的幂基数和非 2 的幂基数
	if b := Word(base); b == b&-b {
		// shift 是基数 b 的数字大小（比特）
		shift := uint(bits.TrailingZeros(uint(b))) // shift > 0，因为 b >= 2
		mask := Word(1<<shift - 1)
		w := x[0]         // 当前字
		nbits := uint(_W) // w 中未处理的位数

		// 转换较低有效字（包含前导零）
		for k := 1; k < len(x); k++ {
			// 转换完整的数字
			for nbits >= shift {
				i--
				s[i] = digits[w&mask]
				w >>= shift
				nbits -= shift
			}

			// 转换任何部分的前导数字并前进到下一个字
			if nbits == 0 {
				// 没有剩余的部分数字，直接前进
				w = x[k]
				nbits = _W
			} else {
				// 当前字 w（== x[k-1]）和下一个字 x[k] 中的部分数字
				w |= x[k] << nbits
				i--
				s[i] = digits[w&mask]

				// 前进
				w = x[k] >> (shift - nbits)
				nbits = _W - (shift - nbits)
			}
		}

		// 转换最高有效字 w 的数字（省略前导零）
		for w != 0 {
			i--
			s[i] = digits[w&mask]
			w >>= shift
		}

	} else {
		stk := getStack()
		defer stk.free()

		bb, ndigits := maxPow(b)

		// 构造 bb*leafSize 的连续平方表，以便在细分中使用
		// 结果 (table != nil) <=> (len(x) > leafSize > 0)
		table := divisors(stk, len(x), b, ndigits, bb)

		// 保留 x，创建本地副本供 convertWords 使用
		q := nat(nil).set(x)

		// 将 q 转换为基数 b 下的字符串 s
		q.convertWords(stk, s, b, ndigits, bb, table)

		// 去除前导零
		// （x != 0；因此 s 必须包含至少一个非零数字
		// 且循环将会终止）
		i = 0
		for s[i] == '0' {
			i++
		}
	}

	if neg {
		i--
		s[i] = '-'
	}

	return s[i:]
}

// 将 q 的字转换为基数 b 的数字并写入 s。如果 q 很大，就使用表格化的除数
// 通过 nat/nat 除法递归地“对半分”。否则，使用重复的 nat/Word 除法
// 迭代地转换。
//
// 迭代方法通过 n 次 divW() 调用处理 n 个 Word，每次调用都会访问
// 逐步缩短的 q 中的每个 Word，总计 n + (n-1) + (n-2) ... + 2 + 1，即 n(n+1)/2 次 divW()。
// 递归转换将 q 除以其近似平方根，得到两部分，每部分的大小是 q 的一半。
// 对两半都使用迭代方法意味着 2 * (n/2)(n/2 + 1)/2 次 divW()
// 加上昂贵的 long div()。渐近地看，这一比例在 divW() 次数减半时是有利的，
// 并且通过递归地拆分子块会变得更好。最佳做法是拆分块，直到再多拆一次
// 所花的时间（因为 nat/nat 的 div()）比迭代方法的两倍 divW() 还长。
// 这个阈值由 leafSize 表示。对 leafSize 在 2..64 范围内的
// 基准测试表明，值 8 和 16 效果很好，在中等长度下提速 4 倍，
// 20000 位数字时约 30 倍。使用 nat_test.go 的 BenchmarkLeafSize 测试
// 针对特定硬件优化 leafSize。
func (q nat) convertWords(stk *stack, s []byte, b Word, ndigits int, bb Word, table []divisor) {
	// 递归地拆分较大的块
	if table != nil {
		// len(q) > leafSize > 0
		var r nat
		index := len(table) - 1
		for len(q) > leafSize {
			// 如果可能，找到接近 sqrt(q) 的除数，但无论如何要 < q
			maxLength := q.bitLen()     // ~= log2 q，或至少是该位长度下可能的最大 q
			minLength := maxLength >> 1 // ~= log2 sqrt(q)
			for index > 0 && table[index-1].nbits > minLength {
				index-- // 所需的
			}
			if table[index].nbits >= maxLength && table[index].bbb.cmp(q) >= 0 {
				index--
				if index < 0 {
					panic("internal inconsistency")
				}
			}

			// 将 q 拆分为两位数 (q'*bbb + r)，以形成独立的子块
			q, r = q.div(stk, r, q, table[index].bbb)

			// 转换子块并在 s[:h] 和 s[h:] 中收集结果
			h := len(s) - table[index].ndigits
			r.convertWords(stk, s[h:], b, ndigits, bb, table[0:index])
			s = s[:h] // == q.convertWords(stk, s, b, ndigits, bb, table[0:index+1])
		}
	}

	// 已拆分完所有大块，现在迭代处理剩余的（小）块
	i := len(s)
	var r Word
	if b == 10 {
		// 在这里对 10 进行硬编码可将速度提高 1.25 倍（允许对常量进行 / 和 % 运算）
		for len(q) > 0 {
			// 取出最低有效的、基数 bb 的“数字”
			q, r = q.divW(q, bb)
			for j := 0; j < ndigits && i > 0; j++ {
				i--
				// 避免 % 计算，因为 r%10 == r - int(r/10)*10；
				// 对于 BenchmarkString10000Base10
				// 和更小的字符串这似乎更快（但对于更大的字符串会稍慢）
				t := r / 10
				s[i] = '0' + byte(r-t*10)
				r = t
			}
		}
	} else {
		for len(q) > 0 {
			// 取出最低有效的、基数 bb 的“数字”
			q, r = q.divW(q, bb)
			for j := 0; j < ndigits && i > 0; j++ {
				i--
				s[i] = digits[r%b]
				r /= b
			}
		}
	}

	// 在前面加上高位零
	for i > 0 { // 只要还需要更多前导零
		i--
		s[i] = '0'
	}
}

// 拆分大于 leafSize 个 Word 的块（设置为 0 则禁用递归转换）
// 使用以下命令对 leafSize 进行基准测试和配置：go test -bench="Leaf"
//
//	8 and 16 effective on 3.0 GHz Xeon "Clovertown" CPU (128 byte cache lines)
//	8 and 16 effective on 2.66 GHz Core 2 Duo "Penryn" CPU
var leafSize int = 8 // 当作单个整体块处理的 Word 大小二进制值的个数

type divisor struct {
	bbb     nat // 除数
	nbits   int // 除数的位长度（不计前导零）~= log2(bbb)
	ndigits int // 用输出基数数字表示时的除数数字长度
}

var cacheBase10 struct {
	sync.Mutex
	table [64]divisor // 基数 10 的缓存除数
}

// expWW 计算 x**y
func (z nat) expWW(stk *stack, x, y Word) nat {
	return z.expNN(stk, nat(nil).setWord(x), nat(nil).setWord(y), nil, false)
}

// 构造 bb*leafSize 的幂表，以便在细分中使用。
func divisors(stk *stack, m int, b Word, ndigits int, bb Word) []divisor {
	// 仅当启用递归转换且 x 很大时才计算表
	if leafSize == 0 || m <= leafSize {
		return nil
	}

	// 确定 k，使得 (bb**leafSize)**(2**k) >= sqrt(x)
	k := 1
	for words := leafSize; words < m>>1 && k < len(cacheBase10.table); words <<= 1 {
		k++
	}

	// 视情况复用并扩展已有的除数表，或新建一个表
	var table []divisor // 当 b == 10 时，table 与 cacheBase10.table 重叠
	if b == 10 {
		cacheBase10.Lock()
		table = cacheBase10.table[0:k] // 复用旧表用于本次转换
	} else {
		table = make([]divisor, k) // 为本次转换新建一个表
	}

	// 扩展表
	if table[k-1].ndigits == 0 {
		// 按需添加新条目
		var larger nat
		for i := 0; i < k; i++ {
			if table[i].ndigits == 0 {
				if i == 0 {
					table[0].bbb = nat(nil).expWW(stk, bb, Word(leafSize))
					table[0].ndigits = ndigits * leafSize
				} else {
					table[i].bbb = nat(nil).sqr(stk, table[i-1].bbb)
					table[i].ndigits = 2 * table[i-1].ndigits
				}

				// 优化：利用宏块中聚集的额外位
				larger = nat(nil).set(table[i].bbb)
				for mulAddVWW(larger, larger, b, 0) == 0 {
					table[i].bbb = table[i].bbb.set(larger)
					table[i].ndigits++
				}

				table[i].nbits = table[i].bbb.bitLen()
			}
		}
	}

	if b == 10 {
		cacheBase10.Unlock()
	}

	return table
}
