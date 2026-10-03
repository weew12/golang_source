// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*

多精度除法。此处有恶龙。

给定 u 和 v，其中 u 为 n+m 位数字，v 为 n 位数字（无前导零），
目标是返回 quo、rem，使得 u = quo*v + rem，其中 0 ≤ rem < v。
即 quo = ⌊u/v⌋，其中 ⌊x⌋ 表示 x 的向下取整（截断为整数），
且 rem = u - quo·v。


长除法

计算机中的除法与小学的长除法过程相同，
但计算机不如小学生那样擅长遵循含糊的指令，
所以我们必须对实际步骤以及可能发生的情况描述得更加精确。

我们从商的最高有效位到最低有效位逐个处理，做以下操作：

 • 猜测一个数字 q，即要从 u 的当前段中减去多少个 v，
   以将最高位清零。
 • 通过计算 q·v 并与 u 的当前段比较来校验该猜测，
   按需调整猜测。
 • 从 u 的当前段中减去 q·v。
 • 将 q 加到结果 quo 的对应段中。

当所有数字都处理完后，最终的余数留在 u 中，
并作为 rem 返回。

例如，下面是 5 位除以 3 位（n=3, m=2）的示意图。

	                 q₂ q₁ q₀
	         _________________
	v₂ v₁ v₀ ) u₄ u₃ u₂ u₁ u₀
	           ↓  ↓  ↓  |  |
	          [u₄ u₃ u₂]|  |
	        - [  q₂·v  ]|  |
	        ----------- ↓  |
	          [  rem  | u₁]|
	        - [    q₁·v   ]|
	           ----------- ↓
	             [  rem  | u₀]
	           - [    q₀·v   ]
	              ------------
	                [  rem   ]

我们不为余数创建新的存储，也不按箭头所示从 u 中复制数字，
而是直接使用 u 的存储作为减法的源和目标，
这样随着除法的进行，余数会覆盖 u 中连续重叠的段，
并用 u 的一个切片来标识当前段。这避免了所有复制
以及余数的移位。

用 n 位的 v 去除 n+m 位的 u（以 B 为基数）通常
最多产生 m+1 位，因为：

  • u < B^(n+m)               [B^(n+m) has n+m+1 digits]
  • v ≥ B^(n-1)               [B^(n-1) is the smallest n-digit number]
  • u/v < B^(n+m) / B^(n-1)   [divide bounds for u, v]
  • u/v < B^(m+1)             [simplify]

第一步是特殊的：它取 u 的最高 n 位，并用 v 的 n 位去除它们，
产生第一个商位和一个 n 位余数。
在示例中，q₂ = ⌊u₄u₃u₂ / v⌋。

第一步用 n 位除以 n 位，以确保只产生
一个数字。

后续每一步把 u 的下一个数字追加到余数上，
并用 v 的 n 位去除这 n+1 位，产生另一个商位和一个
新的 n 位余数。

后续步骤用 n 位去除 n+1 位，这一操作通常
可能产生两个数字。不过，在算法中的用法保证了
该除法只产生一个数字。被除数的形式为
rem·B + d，其中 rem 是上一步的余数，d 是单个
数字，所以：

 • rem ≤ v - 1                 [rem is a remainder from dividing by v]
 • rem·B ≤ v·B - B             [multiply by B]
 • d ≤ B - 1                   [d is a single digit]
 • rem·B + d ≤ v·B - 1         [add]
 • rem·B + d < v·B             [change ≤ to <]
 • (rem·B + d)/v < B           [divide by v]


猜测与校验

每一步我们都需要用 n 位去除 n+1 位，但这是在
实现“除以 n 位”的除法，所以我们不能只是调用一个除法
例程：我们自己_就是_那个除法例程。于是我们先猜测答案，
再用乘法校验它。如果猜测有误，就修正它。

这种猜测怎么可能高效？事实证明下面的
陈述（我们称之为“良好猜测保证”）成立。

如果

 • q = ⌊u/v⌋，其中 u 是 n+1 位而 v 是 n 位，
 • q < B，且
 • v 的最高位 = vₙ₋₁ ≥ B/2，

则 q̂ = ⌊uₙuₙ₋₁ / vₙ₋₁⌋ 满足 q ≤ q̂ ≤ q+2。（证明见下。）

也就是说，如果我们知道答案只有单个数字，并且通过
忽略 u 和 v 的最低 n-1 位、用一个 2 位除以 1 位的除法来猜测答案，
那么这个猜测至少与正确答案一样大。它也不会
大太多：它与正确答案至多相差二。

注意，在整个除法的第一步（这是一次 n 位除以 n 位的
除法）中，2 除以 1 的猜测隐含地使用了 uₙ = 0。

注意，这里使用 2 位除以 1 位的除法并不意味着递归地调用
我们自己。相反，我们使用该操作的高效的直接硬件实现。

注意，因为 q 是 u/v 向下取整，所以 q·v 不能超过 u：u ≥ q·v。
如果某个猜测 q̂ 过大，它就不满足这个检验。换个角度看，
给定 q̂ 的余数 r̂ 为 u - q̂·v，它必须为正。如果它为
负，那么猜测 q̂ 就太大了。

这给了我们一种计算 q 的方法。首先用 2 位除以 1 位的除法计算 q̂。
然后，当 u < q̂·v 时，递减 q̂；这个循环至多执行两次，因为
q̂ ≤ q+2。


缩放输入

良好猜测保证要求 v 的最高位数字（vₙ₋₁）至少为 B/2。
例如在以 10 为基数时，⌊172/19⌋ = 9，但 ⌊18/1⌋ = 18：猜测严重偏离，
因为首位数字 1 小于 B/2 = 5。

我们可以通过将 u 和 v 都乘以适当的值来确保 v 具有较大的最高位数字。
继续上面的例子，如果我们把 172 和 19 都乘以 3，就得到 ⌊516/57⌋，
此时 v 的首位数字 ≥ 5，果然 ⌊51/5⌋ = 10 更接近正确答案 9。
在这里乘以 4 会更简单，因为那可以通过移位完成。具体来说，我们
总是可以数出 v 首位数字中前导零的个数 i，然后将 u 和 v 都左移 i 位。

将 u 和 v 缩放之后，⌊u/v⌋ 的值不变，但余数会被
缩放：172 mod 19 是 1，但 516 mod 57 是 3。我们必须在完成时
将余数除以缩放因子（右移 i 位）。

注意，这些移位发生在整个除法算法的前后，
而不是在逐位迭代的每一步中。

注意缩放输入对可能商的大小的影响。
在缩放后的 u/v 中，u 可能因缩放而增加一位；v 永远不会，因为
我们选择缩放因子时是让 v 的最高位变大而不溢出。
如果缩放后 u 和 v 分别有 n+m 位和 n 位，那么：

  • u < B^(n+m)               [B^(n+m) has n+m+1 digits]
  • v ≥ B^n / 2               [vₙ₋₁ ≥ B/2, so vₙ₋₁·B^(n-1) ≥ B^n/2]
  • u/v < B^(n+m) / (B^n / 2) [divide bounds for u, v]
  • u/v < 2 B^m               [simplify]

商仍然可以有 m+1 个有效位，但若是如此，最高位
必定是 1。这提供了处理结果第一位数字的另一种方式：
将 u 的最高 n 位与 v 比较，并填入 0 或 1。


细化猜测

在我们检查是否 u < q̂·v 之前，可以调整猜测，把它从
q̂ = ⌊uₙuₙ₋₁ / vₙ₋₁⌋ 变成更精细的猜测 ⌊uₙuₙ₋₁uₙ₋₂ / vₙ₋₁vₙ₋₂⌋。
尽管上面没有提到，但良好猜测保证还承诺这个
3 位除以 2 位的除法猜测更精确，与真实答案 q 至多相差一。
从 2 位除以 1 位到 3 位除以 2 位猜测的这一改进也可以
不用 n 位运算完成。

如果我们有一个猜测 q̂ = ⌊uₙuₙ₋₁ / vₙ₋₁⌋，并且想看看它是否也等于
⌊uₙuₙ₋₁uₙ₋₂ / vₙ₋₁vₙ₋₂⌋，我们可以使用与完整除法相同的检查：
如果 uₙuₙ₋₁uₙ₋₂ < q̂·vₙ₋₁vₙ₋₂，则猜测过大，应当减小。

检查 uₙuₙ₋₁uₙ₋₂ < q̂·vₙ₋₁vₙ₋₂ 等同于检查 uₙuₙ₋₁uₙ₋₂ - q̂·vₙ₋₁vₙ₋₂ < 0，
并且

	uₙuₙ₋₁uₙ₋₂ - q̂·vₙ₋₁vₙ₋₂ = (uₙuₙ₋₁·B + uₙ₋₂) - q̂·(vₙ₋₁·B + vₙ₋₂)
	                          [splitting off the bottom digit]
	                      = (uₙuₙ₋₁ - q̂·vₙ₋₁)·B + uₙ₋₂ - q̂·vₙ₋₂
	                          [regrouping]

表达式 (uₙuₙ₋₁ - q̂·vₙ₋₁) 是 uₙuₙ₋₁ / vₙ₋₁ 的余数。
如果初始猜测同时返回 q̂ 及其余数 r̂，那么检查
是否 uₙuₙ₋₁uₙ₋₂ < q̂·vₙ₋₁vₙ₋₂ 就等同于检查 r̂·B + uₙ₋₂ < q̂·vₙ₋₂。

如果我们发现 r̂·B + uₙ₋₂ < q̂·vₙ₋₂，就可以通过
递减 q̂ 并把 vₙ₋₁ 加到 r̂ 上来调整猜测。我们重复这一过程直到 r̂·B + uₙ₋₂ ≥ q̂·vₙ₋₂。
（和之前一样，这种修正至多需要两次。）

现在 q̂ = ⌊uₙuₙ₋₁uₙ₋₂ / vₙ₋₁vₙ₋₂⌋，如上所述，它与正确的 q
至多相差一，而且我们避免了任何 n 位运算。
（如果需要新的余数，可以用 r̂·B + uₙ₋₂ - q̂·vₙ₋₂ 计算。）

最终的检查 u < q̂·v 以及可能的修正必须以全精度进行。
对于随机输入，这一步的修正在绝大多数情况下都极其罕见：3 位除以 2 位的猜测
很少出错。但我们仍必须做这个检查。注意，由于
3 位除以 2 位的猜测至多相差 1，在进行最终
u < q̂·v 检查时，将其作为余数 r = u - q̂·v 计算的一部分会很方便。如果
减法下溢，递减 q̂ 并向 r 加回一个 v 就足以
得到最终的 q、r。

以上就是长除法的全部内容：缩放输入，然后对
每个输出位置循环，猜测、校验并修正下一个输出数字。

对于一个 2n 位数除以一个 n 位数（这是复杂度为 n 的除法情形中最坏的情况），
该算法使用 n+1 次迭代，每次迭代至少要做
一次 1 位乘以 n 位的乘法 q̂·v。那就是 O(n) 次迭代，每次
O(n) 时间，所以总体 O(n²) 时间。


递归除法

对于非常大的输入，有可能改进 O(n²) 算法。
我们把一组 n/2 个真实数字称为一个（非常）“宽数字”。我们可以在
宽数字上运行上面解释的标准长除法算法，而不是在
实际数字上运行。这样步骤会少得多，但每一步涉及的
数学运算工作量更大。

基本长除法使用 2 位除以 1 位的除法来猜测初始 q̂，
而新算法必须使用 2 宽位除以 1 宽位的除法，这实际上
就是一次 n 位除以 n/2 位的除法。这没问题：如果我们用 n/2 位的除法
实现 n 位的除法，当除数小到可以用标准长除法
甚至 2 位除以 1 位的硬件指令处理时，递归就会终止。

例如，下面是 10 位除以 4 的示意图，按对应两个常规数字的
宽位进行。第一步仍然是特殊的，
必须剩下一个（常规）数字，用 4 去除 5 并产生一个小于 v 的 4 位
余数。中间的步骤用 4 去除 6 位，保证每次产生两个输出数字
（一个宽数字）以及 4 位余数。
最后一步必须利用现有的资源：4 位余数再加一位，
共 5 位除以 4。

	                       q₆ q₅ q₄ q₃ q₂ q₁ q₀
	            _______________________________
	v₃ v₂ v₁ v₀ ) u₉ u₈ u₇ u₆ u₅ u₄ u₃ u₂ u₁ u₀
	              ↓  ↓  ↓  ↓  ↓  |  |  |  |  |
	             [u₉ u₈ u₇ u₆ u₅]|  |  |  |  |
	           - [    q₆q₅·v    ]|  |  |  |  |
	           ----------------- ↓  ↓  |  |  |
	                [    rem    |u₄ u₃]|  |  |
	              - [     q₄q₃·v      ]|  |  |
	              -------------------- ↓  ↓  |
	                      [    rem    |u₂ u₁]|
	                    - [     q₂q₁·v      ]|
	                    -------------------- ↓
	                            [    rem    |u₀]
	                          - [     q₀·v     ]
	                          ------------------
	                               [    rem    ]

另一种做法是预先考虑 n/2 能多好地整除 n+m，
按需调整第一步以使用更少的数字，让第一步
更特殊，从而使最后一步完全不特殊。例如，使用
相同的输入，我们可以选择在第一步只使用 4 位数字，
为最后一步留下一个完整的宽位：

	                       q₆ q₅ q₄ q₃ q₂ q₁ q₀
	            _______________________________
	v₃ v₂ v₁ v₀ ) u₉ u₈ u₇ u₆ u₅ u₄ u₃ u₂ u₁ u₀
	              ↓  ↓  ↓  ↓  |  |  |  |  |  |
	             [u₉ u₈ u₇ u₆]|  |  |  |  |  |
	           - [    q₆·v   ]|  |  |  |  |  |
	           -------------- ↓  ↓  |  |  |  |
	             [    rem    |u₅ u₄]|  |  |  |
	           - [     q₅q₄·v      ]|  |  |  |
	           -------------------- ↓  ↓  |  |
	                   [    rem    |u₃ u₂]|  |
	                 - [     q₃q₂·v      ]|  |
	                 -------------------- ↓  ↓
	                         [    rem    |u₁ u₀]
	                       - [     q₁q₀·v      ]
	                       ---------------------
	                               [    rem    ]

如今 divRecursiveStep 中的代码像第一个示例那样工作。也许
将来我们会让它像替代方案那样工作，以避免
最后一次迭代中的特殊情况。

无论哪种方式，每一步都是用 2 宽位除以 1 宽位的除法先近似出的
3 宽位除以 2 宽位的除法，正如我们在常规数字的长除法中所做的那样。
由于我们真正想要的是 3 宽位除以 2 宽位的除法，因此在修正时
不是直接乘以 q̂·v，而是可以使用长除法中的快速细化
（一次 n/2 乘以 n/2 的乘法）将 q 修正为其实际值，
并同时计算出余数（如上所述），然后就此停止，
绝不做完整的 n 乘以 n 的乘法。

我们不是用 n 位除以 n/2 位的除法来产生 n/2 位，而是可以
增加（而非丢弃）一个真实数字，做一次 (n+1) 位除以 (n/2+1) 位的除法，
产生 n/2+1 位。那多出的一个数字将良好猜测保证收紧
为 q ≤ q̂ ≤ q+1，并让我们可以放弃长除法对第一位数字
的特殊处理。这些好处在下面的良好猜测保证证明之后
会有更多讨论。


递归除法有多快？

对于一次 2n 位除以 n 位的除法，该算法在宽位上运行一次 4 除以 2 的长除法，
产生两个宽位外加一个可能的前导常规数字 1，
它可以不用递归处理。也就是说，该算法使用两次
完整迭代，每次使用一次 n 位除以 n/2 位的除法和一次 n/2 位乘以 n/2 位的
乘法，外加若干 n 位加法和减法。标准的
n 位乘以 n 位乘法算法需要 O(n²) 时间，使得总体
算法所需时间为 T(n)，其中

	T(n) = 2T(n/2) + O(n) + O(n²)

根据 Bentley-Haken-Saxe 定理，最终化简为 T(n) = O(n²)。
这相对于常规长除法并没有改进。

当数字个数 n 变得足够大时，可以改用 Karatsuba 乘法
算法，它需要 O(n^log₂3) = O(n^1.6) 时间。
（Karatsuba 乘法在 nat.go 的 func karatsuba 中实现。）
这使得总体递归除法算法同样需要 O(n^1.6) 时间，
这是一种改进，但同样只对足够大的数字成立。

并不要求每次递归都只做两次递归调用。
虽然在一般情况下递归调用的次数会改变时间
分析，但在这里做三次调用并不会改变分析结果：

	T(n) = 3T(n/2) + O(n) + O(n^log₂3)

最终为 T(n) = O(n^log₂3)。因为耗时为 O(n^log₂3) 的 Karatsuba 乘法
本身就在做 3 次半规模的递归，除法做三次
并不会损害渐近性能。当然，在实践中做两次
很可能仍然更快。


良好猜测保证的证明

给定数字 x、y，让我们把它们拆分为除以某个缩放因子 S 后的商和余数，
并附加约束：商 x/y 和 y 的高位部分都小于某个上限 T，
且 y 的高位部分至少为 T 的一半。

	x₁ = ⌊x/S⌋        y₁ = ⌊y/S⌋
	x₀ = x mod S      y₀ = y mod S

	x  = x₁·S + x₀    0 ≤ x₀ < S    x/y < T
	y  = y₁·S + y₀    0 ≤ y₀ < S    T/2 ≤ y₁ < T

再考虑两个截断的商：

	q = ⌊x/y⌋
	q̂ = ⌊x₁/y₁⌋

我们将证明 q ≤ q̂ ≤ q+2。

该保证对缩放因子 S 没有真正的要求：它仅仅是
从 x 和 y 中截去以得到 x₁ 和 y₁ 的数字的量级。
该保证对 T 只有有限的要求：它必须足够大以容纳
商 x/y，且 y₁ 必须具有大致相同的大小。

为了应用到前面关于长除法中 2 除以 1 猜测的讨论，
我们会选择：

	S  = Bⁿ⁻¹
	T  = B
	x  = u
	x₁ = uₙuₙ₋₁
	x₀ = uₙ₋₂...u₀
	y  = v
	y₁ = vₙ₋₁
	y₀ = vₙ₋₂...u₀

这些更简单的变量避免了在证明中重复那些较长的表达式。

另请注意，根据定义，截断除法 ⌊x/y⌋ 满足

	x/y - 1 < ⌊x/y⌋ ≤ x/y。

这一事实将在证明中使用若干次。

证明 q ≤ q̂：

	q̂·y₁ = ⌊x₁/y₁⌋·y₁                      [by definition, q̂ = ⌊x₁/y₁⌋]
	     > (x₁/y₁ - 1)·y₁                  [x₁/y₁ - 1 < ⌊x₁/y₁⌋]
	     = x₁ - y₁                         [distribute y₁]

	So q̂·y₁ > x₁ - y₁.
	Since q̂·y₁ is an integer, q̂·y₁ ≥ x₁ - y₁ + 1.

	q̂ - q = q̂ - ⌊x/y⌋                      [by definition, q = ⌊x/y⌋]
	      ≥ q̂ - x/y                        [⌊x/y⌋ < x/y]
	      = (1/y)·(q̂·y - x)                [factor out 1/y]
	      ≥ (1/y)·(q̂·y₁·S - x)             [y = y₁·S + y₀ ≥ y₁·S]
	      ≥ (1/y)·((x₁ - y₁ + 1)·S - x)    [above: q̂·y₁ ≥ x₁ - y₁ + 1]
	      = (1/y)·(x₁·S - y₁·S + S - x)    [distribute S]
	      = (1/y)·(S - x₀ - y₁·S)          [-x = -x₁·S - x₀]
	      > -y₁·S / y                      [x₀ < S, so S - x₀ > 0; drop it]
	      ≥ -1                             [y₁·S ≤ y]

	So q̂ - q > -1.
	Since q̂ - q is an integer, q̂ - q ≥ 0, or equivalently q ≤ q̂.

证明 q̂ ≤ q+2：

	x₁/y₁ - x/y = x₁·S/y₁·S - x/y          [multiply left term by S/S]
	            ≤ x/y₁·S - x/y             [x₁S ≤ x]
	            = (x/y)·(y/y₁·S - 1)       [factor out x/y]
	            = (x/y)·((y - y₁·S)/y₁·S)  [move -1 into y/y₁·S fraction]
	            = (x/y)·(y₀/y₁·S)          [y - y₁·S = y₀]
	            = (x/y)·(1/y₁)·(y₀/S)      [factor out 1/y₁]
	            < (x/y)·(1/y₁)             [y₀ < S, so y₀/S < 1]
	            ≤ (x/y)·(2/T)              [y₁ ≥ T/2, so 1/y₁ ≤ 2/T]
	            < T·(2/T)                  [x/y < T]
	            = 2                        [T·(2/T) = 2]

	So x₁/y₁ - x/y < 2.

	q̂ - q = ⌊x₁/y₁⌋ - q                    [by definition, q̂ = ⌊x₁/y₁⌋]
	      = ⌊x₁/y₁⌋ - ⌊x/y⌋                [by definition, q = ⌊x/y⌋]
	      ≤ x₁/y₁ - ⌊x/y⌋                  [⌊x₁/y₁⌋ ≤ x₁/y₁]
	      < x₁/y₁ - (x/y - 1)              [⌊x/y⌋ > x/y - 1]
	      = (x₁/y₁ - x/y) + 1              [regrouping]
	      < 2 + 1                          [above: x₁/y₁ - x/y < 2]
	      = 3

	So q̂ - q < 3.
	Since q̂ - q is an integer, q̂ - q ≤ 2.

注意，当 x/y < T/2 时，界收紧为 x₁/y₁ - x/y < 1，因此
q̂ - q ≤ 1。

另请注意，在一般情形 2n 位除以 n 位的除法中，我们虽然不知道
x/y < T，但我们确实知道 x/y < 2T，从而得到界 q̂ - q ≤ 4。因此我们可以
去掉长除法中特殊的第一步，只要我们允许
第一次修正循环最多运行四次。（不过，用简单的比较来判断
第一位数字是 0 还是 1 仍然更高效。）

最后，注意当用 2（缩放后的）去除三个前导的、以 B 为基数的数字时，
我们有 T = B² 且 x/y < B = T/B，这比 x/y < T 是紧得多的界。
这进而给出紧得多的界 x₁/y₁ - x/y < 2/B。这意味着
⌊x₁/y₁⌋ 与 ⌊x/y⌋ 只有在 x/y 比某个整数大不到 2/B 时才可能不同。
对于随机的 x 和 y，发生这种情况的概率是 2/B，或者对于大的 B，
约为零。这意味着在长除法算法中产生 3 位除以 2 位的猜测之后，
修正循环基本上永远不会运行。

在递归算法中，(2·⌊n/2⌋+1) 位除以 (⌊n/2⌋+1) 位除法里多出的那个数字
具有完全相同的效果：需要修正的概率同样是
2/B。更好的是，我们可以允许一般情形 x/y < 2T，而修正
概率只会增长到 4/B，仍然基本上为零。


参考文献

实现长除法并没有很好的参考文献；因此有了这段注释。
以下是一些关于从显而易见的参考文献中能期待什么的说明。

Knuth 第 2 卷（Seminumerical Algorithms）第 4.3.1 节是长除法通常的
经典参考文献，但整个系列高度压缩，从不重复必要的事实，
并把重要的洞见留给习题。
例如，对于把 q̂ 从 2 除以 1 的猜测扩展为 3 除以 2 的猜测的计算，
完全没有给出任何理由，也没有说明它为何能减小误差界。
证明该计算确实具有预期效果被留作习题。
书后提供的那些习题解答完全只是
计算，仍然没有解释正在发生什么，或者你如何
会想到做那些具体的计算。书中从未提到
这个检验把 2 除以 1 的猜测扩展为 3 除以 2 的猜测。良好猜测保证
的证明只针对 2 除以 1 的猜测，并且用反证法论证，
使人难以理解像增加一个数字或调整商的取值范围
这样的修改如何影响总体界。

尽管如此，Knuth 仍然是经典参考文献。它很密集，但充满了
信息和参考文献，而且其证明比许多其他
表述更简单。上面的证明是对 Knuth 证明的重写，以去除
反证法论证，并补充 Knuth 省略的解释或步骤。
但要提防较旧印次中的错误。随身带上已发布的勘误。

Brinch Hansen 的 “Multiple-length Division Revisited: a Tour of the Minefield”
先是对 Knuth（以及其他人的）表述提出直率的批评，然后
给出了更详细、更易于理解的长除法论述，
包括一个 Pascal 实现。但其算法和实现
完全基于 3 除以 2 的除法，这在现代
硬件上远不如使用 2 除以 1 除法的算法有用。其证明有点过于
关注数字计数，显得不必要地复杂，尤其是与
上面给出的证明相比。

Burnikel 和 Ziegler 的 “Fast Recursive Division” 引入了关键洞见：
用对 n/2 位除数的除法的递归调用来实现除以 n 位除数的除法，
依靠 Karatsuba 乘法获得亚二次的运行时间。然而，其表述上的取舍几乎
完全是为了简化运行时间分析，而不是
简化表述。论文没有给出一个循环遍历商的各位数字的
单一算法，而是给出两个相互递归的算法，分别用于
2n 位除以 n 位和 3n 位除以 2n 位。论文也没有给出任何一般的 (n+m) 位除以 n 位
算法。

论文中的证明异常复杂，尤其是考虑到
该算法本质上只是在宽位上进行长除法，因此
通常的长除法证明基本可以原封不动地适用。
*/

package big

import "math/bits"

// rem 返回 r，使得 r = u%v。
// 它使用 z 作为 r 的存储。
func (z nat) rem(stk *stack, u, v nat) (r nat) {
	if alias(z, u) {
		z = nil
	}
	defer stk.restore(stk.save())
	q := stk.nat(max(1, len(u)-(len(v)-1)))
	_, r = q.div(stk, z, u, v)
	return r
}

// div 返回 q、r，使得 q = ⌊u/v⌋、r = u%v = u - q·v。
// 它使用 z 和 z2 作为 q 和 r 的存储。
// 调用者可以传入 stk == nil，以请求 div 自行获取并释放一个。
func (z nat) div(stk *stack, z2, u, v nat) (q, r nat) {
	if len(v) == 0 {
		panic("division by zero")
	}

	if len(v) == 1 {
		// 短除法：针对单字除数的长除法优化。
		// 这种情况下，每一步只需要 2 除以 1 的猜测。
		var r2 Word
		q, r2 = z.divW(u, v[0])
		r = z2.setWord(r2)
		return
	}

	if u.cmp(v) < 0 {
		q = z[:0]
		r = z2.set(u)
		return
	}

	if stk == nil {
		stk = getStack()
		defer stk.free()
	}

	q, r = z.divLarge(stk, z2, u, v)
	return
}

// divW 返回 q、r，使得 q = ⌊x/y⌋、r = x%y = x - q·y。
// 它使用 z 作为 q 的存储。
// 注意 y 是单个数字（Word），不是大数。
func (z nat) divW(x nat, y Word) (q nat, r Word) {
	m := len(x)
	switch {
	case y == 0:
		panic("division by zero")
	case y == 1:
		q = z.set(x) // 结果为 x
		return
	case m == 0:
		q = z[:0] // 结果为 0
		return
	}
	// m > 0
	z = z.make(m)
	r = divWVW(z, 0, x, y)
	q = z.norm()
	return
}

// modW 返回 x % d。
func (x nat) modW(d Word) (r Word) {
	// TODO(agl): 我们实际上不需要存储 q 值。
	var q nat
	q = q.make(len(x))
	return divWVW(q, 0, x, d)
}

// divWVW 用 ⌊x/y⌋ 覆盖 z，并返回余数 r。
// 调用者必须确保 len(z) = len(x)。
func divWVW(z []Word, xn Word, x []Word, y Word) (r Word) {
	r = xn
	if len(x) == 1 {
		qq, rr := bits.Div(uint(r), uint(x[0]), uint(y))
		z[0] = Word(qq)
		return Word(rr)
	}
	rec := reciprocalWord(y)
	for i := len(z) - 1; i >= 0; i-- {
		z[i], r = divWW(r, x[i], y, rec)
	}
	return r
}

// div 返回 q、r，使得 q = ⌊uIn/vIn⌋、r = uIn%vIn = uIn - q·vIn。
// 它使用 z 和 u 作为 q 和 r 的存储。
// 调用者必须确保 len(vIn) ≥ 2（否则使用 divW）
// 且 len(uIn) ≥ len(vIn)（否则答案为 0、uIn）。
func (z nat) divLarge(stk *stack, u, uIn, vIn nat) (q, r nat) {
	n := len(vIn)
	m := len(uIn) - n

	// 缩放输入，使 vIn 的最高位为 1（见上面的“缩放输入”）。
	// vIn 被视为只读输入（它可能正被另一个
	// goroutine 使用），因此我们必须复制一份。
	// uIn 被复制到 u。
	defer stk.restore(stk.save())
	shift := nlz(vIn[n-1])
	v := stk.nat(n)
	u = u.make(len(uIn) + 1)
	if shift == 0 {
		copy(v, vIn)
		copy(u[:len(uIn)], uIn)
		u[len(uIn)] = 0
	} else {
		lshVU(v, vIn, shift)
		u[len(uIn)] = lshVU(u[:len(uIn)], uIn, shift)
	}

	// 调用者不应传入互为别名的 z 和 u，因为它们是
	// 两个不同的输出，但为了以防万一还是做正确处理。
	if alias(z, u) {
		z = nil
	}
	q = z.make(m + 1)

	// 根据规模使用基本长除法或递归长除法。
	if n < divRecursiveThreshold {
		q.divBasic(stk, u, v)
	} else {
		q.divRecursive(stk, u, v)
	}

	q = q.norm()

	// 撤销余数的缩放。
	if shift != 0 {
		rshVU(u, u, shift)
	}
	r = u.norm()

	return q, r
}

// divBasic 实现上面描述的长除法。
// 它用 ⌊u/v⌋ 覆盖 q，并用余数 r 覆盖 u。
// q 必须足够大以容纳 ⌊u/v⌋。
func (q nat) divBasic(stk *stack, u, v nat) {
	n := len(v)
	m := len(u) - n

	defer stk.restore(stk.save())
	qhatv := stk.nat(n + 1)

	// 为下面的 divWW 做准备，预先计算倒数参数。
	vn1 := v[n-1]
	rec := reciprocalWord(vn1)

	// 为 u 添加一个前导 0，用于第一次迭代。
	// 不变式：每次迭代中 ujn == u[j+n]。
	ujn := Word(0)

	// 计算商的每一位数字。
	for j := m; j >= 0; j-- {
		// 计算 2 除以 1 的猜测 q̂。
		qhat := Word(_M)

		// ujn ≤ vn1，否则 q̂ 会超过一位数字。
		// 当 ujn == vn1 时，我们把 q̂ 设为上面的最大数字 M。
		// 否则，我们计算 2 除以 1 的猜测。
		if ujn != vn1 {
			var rhat Word
			qhat, rhat = divWW(ujn, u[j+n-1], vn1, rec)

			// 将 q̂ 细化为 3 除以 2 的猜测。见上面的“细化猜测”。
			vn2 := v[n-2]
			x1, x2 := mulWW(qhat, vn2)
			ujn2 := u[j+n-2]
			for greaterThan(x1, x2, rhat, ujn2) { // x1x2 > r̂ u[j+n-2]
				qhat--
				prevRhat := rhat
				rhat += vn1
				// 如果 r̂ 溢出，那么
				// r̂ u[j+n-2]v[n-1] 现在肯定 > x1 x2。
				if rhat < prevRhat {
					break
				}
				// TODO(rsc): 不需要完整的 mulWW。
				// x2 += vn2; if x2 overflows, x1++
				x1, x2 = mulWW(qhat, vn2)
			}
		}

		// 计算 q̂·v。
		qhatv[n] = mulAddVWW(qhatv[0:n], v, qhat, 0)
		qhl := len(qhatv)
		if j+qhl > len(u) && qhatv[n] == 0 {
			qhl--
		}

		// 从 u 的当前段中减去 q̂·v。
		// 如果下溢，则 q̂·v > u，我们通过
		// 递减 q̂ 并加回 v 来修正。
		c := subVV(u[j:j+qhl], u[j:j+qhl], qhatv[:qhl])
		if c != 0 {
			c := addVV(u[j:j+n], u[j:j+n], v)
			// 如果 n == qhl，来自 subVV 的进位和来自 addVV 的进位
			// 相互抵消，不影响 u[j+n]。
			if n < qhl {
				u[j+n] += c
			}
			qhat--
		}

		ujn = u[j+n-1]

		// 保存商位。
		// 调用者可能知道最高位为零，因而没有为它留出空间。
		if j == m && m == len(q) && qhat == 0 {
			continue
		}
		q[j] = qhat
	}
}

// greaterThan 报告两个两位数 x1 x2 > y1 y2 是否成立。
// TODO(rsc): 与本文件的大部分内容矛盾，这里 x1 是高位
// 数字而 x2 是低位数字。这应当修正。
func greaterThan(x1, x2, y1, y2 Word) bool {
	return x1 > y1 || x1 == y1 && x2 > y2
}

// divRecursiveThreshold 是除数位数，达到该位数时
// divRecursive 比 divBasic 更快。
var divRecursiveThreshold = 40 // 参见 calibrate_test.go

// divRecursive 实现上面描述的递归除法。
// 它用 ⌊u/v⌋ 覆盖 z，并用余数 r 覆盖 u。
// z 必须足够大以容纳 ⌊u/v⌋。
// 这个函数只是围绕真正的实现 divRecursiveStep
// 分配和释放临时对象。
func (z nat) divRecursive(stk *stack, u, v nat) {
	clear(z)
	z.divRecursiveStep(stk, u, v, 0)
}

// divRecursiveStep 是递归除法的实际实现。
// 它将 ⌊u/v⌋ 加到 z，并用余数 r 覆盖 u。
// z 必须足够大以容纳 ⌊u/v⌋。
// 它使用 temps[depth]（按需分配）作为跨递归调用存活的临时对象。
// 它还使用 tmp，但 tmp 不跨递归存活。
func (z nat) divRecursiveStep(stk *stack, u, v nat, depth int) {
	// u 是原始数据的一个子段，可能有前导零。
	// TODO(rsc): v = v.norm() 是无用的，应当删除。
	// 我们知道（并且要求）v 的最高位 ≥ B/2。
	u = u.norm()
	v = v.norm()
	if len(u) == 0 {
		clear(z)
		return
	}

	// 如果现在问题足够小，就退回基本除法。
	n := len(v)
	if n < divRecursiveThreshold {
		z.divBasic(stk, u, v)
		return
	}

	// 如果 u 比 v 短（意味着 u < v），则无事可做。
	m := len(u) - n
	if m < 0 {
		return
	}

	// 我们把连续的 B 位数字视为一个宽数字。
	// （见上面的“递归除法”。）
	//
	// TODO(rsc): 把 B 重命名为 Wide，以避免与 _B 混淆，
	// 后者是完全不同的东西。
	// TODO(rsc): 研究使用 ⌈n/2⌉ 是否比 ⌊n/2⌋ 更好。
	B := n / 2

	// 为下面的 qhat 分配一个 nat。
	defer stk.restore(stk.save())
	qhat0 := stk.nat(B + 1)

	// 计算商的每个宽数字。
	//
	// TODO(rsc): 将循环改为
	//	for j := (m+B-1)/B*B; j > 0; j -= B {
	// 这将使最后一步成为常规步骤，让我们可以
	// 删除下面相当于循环体额外副本的部分。
	j := m
	for j > B {
		// 用 v（2 个宽数字）去除 u[j-B:j+n]（3 个宽数字）。
		// 首先用递归调用做出 2 宽位除以 1 宽位的猜测。
		// 然后把猜测扩展为完整的 3 除以 2（见“细化猜测”）。
		//
		// 对于 2 宽位除以 1 宽位的猜测，我们不使用 2B 位除以 B 位，
		// 而是使用 (2B+1) 位除以 (B+1) 位，这既处理了
		// 结果多出一个前导数字 1 的可能性，又保证
		// 计算出的 q̂ 至多偏差 1 而不是 2。

		// s 是从 3B 位和 2B 位块中要丢弃的数字个数。
		// 我们丢弃 B-1 个，从而剩下 2B+1 和 B+1。
		s := (B - 1)

		// uu 是我们正在处理的 u 中最多 3B 位的段。
		uu := u[j-B:]

		// 计算 2 除以 1 的猜测 q̂，把 r̂ 留在 uu[s:B+n] 中。
		qhat := qhat0
		clear(qhat)
		qhat.divRecursiveStep(stk, uu[s:B+n], v[s:], depth+1)
		qhat = qhat.norm()

		// 扩展为 3 除以 2 的商和余数。
		// 由于 divRecursiveStep 用余数 r̂ 覆盖了 uu 的上部，
		// 整个 uu 已经包含了“细化猜测”讨论中
		// r̂·B + uₙ₋₂ 的等价物。
		// 从中减去 q̂·vₙ₋₂ 将计算出完整长度的余数。
		// 如果该减法下溢，则 q̂·v > u，我们通过
		// 递减 q̂ 并加回 v 来修正，与长除法中相同。

		// TODO(rsc): 这段代码不是做减法和修正，而是计算
		// q̂·vₙ₋₂ 并递减 q̂ 直到该乘积 ≤ u。
		// 但我们可以像上面的注释和长除法中那样直接做减法，
		// 因为我们知道 q̂ 至多偏差一。
		mark := stk.save()
		qhatv := stk.nat(3 * n)
		clear(qhatv)
		qhatv = qhatv.mul(stk, qhat, v[:s])
		for i := 0; i < 2; i++ {
			e := qhatv.cmp(uu.norm())
			if e <= 0 {
				break
			}
			subVW(qhat, qhat, 1)
			c := subVV(qhatv[:s], qhatv[:s], v[:s])
			if len(qhatv) > s {
				subVW(qhatv[s:], qhatv[s:], c)
			}
			addTo(uu[s:], v[s:])
		}
		if qhatv.cmp(uu.norm()) > 0 {
			panic("impossible")
		}
		c := subVV(uu[:len(qhatv)], uu[:len(qhatv)], qhatv)
		if c > 0 {
			subVW(uu[len(qhatv):], uu[len(qhatv):], c)
		}
		addTo(z[j-B:], qhat)
		j -= B
		stk.restore(mark)
	}

	// TODO(rsc): 按上面所述重写循环，并删除所有这些代码。

	// 现在 u < (v<<B)，以相同方式计算低位。
	// 再次选择 shift = B-1。
	s := B - 1
	qhat := qhat0
	clear(qhat)
	qhat.divRecursiveStep(stk, u[s:].norm(), v[s:], depth+1)
	qhat = qhat.norm()
	qhatv := stk.nat(3 * n)
	clear(qhatv)
	qhatv = qhatv.mul(stk, qhat, v[:s])
	// 和之前一样设置正确的余数。
	for i := 0; i < 2; i++ {
		if e := qhatv.cmp(u.norm()); e > 0 {
			subVW(qhat, qhat, 1)
			c := subVV(qhatv[:s], qhatv[:s], v[:s])
			if len(qhatv) > s {
				subVW(qhatv[s:], qhatv[s:], c)
			}
			addTo(u[s:], v[s:])
		}
	}
	if qhatv.cmp(u.norm()) > 0 {
		panic("impossible")
	}
	c := subVV(u[:len(qhatv)], u[:len(qhatv)], qhatv)
	if c > 0 {
		c = subVW(u[len(qhatv):], u[len(qhatv):], c)
	}
	if c > 0 {
		panic("impossible")
	}

	// 完成！
	addTo(z, qhat.norm())
}
