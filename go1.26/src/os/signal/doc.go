// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package signal 实现对传入信号的访问。

信号主要用于类 Unix 系统。关于本包在 Windows 和 Plan 9 上的用法，
参见下文。

# 信号的类型

SIGKILL 和 SIGSTOP 信号可能无法被程序捕获，因此本包无法影响它们。

同步信号是由程序执行中的错误触发的信号：SIGBUS、SIGFPE 和 SIGSEGV。
仅当它们由程序执行引起时，才被视为同步；而通过 [os.Process.Kill]、
kill 程序或类似机制发送时则不是。一般来说，除下文所述情况外，
Go 程序会将同步信号转换为运行时 panic。

其余的信号是异步信号。它们不是由程序错误触发的，而是由内核或其他
程序发送的。

在异步信号中，当程序失去其控制终端时会发送 SIGHUP 信号。当控制终端
上的用户按下中断字符（默认为 ^C（Control-C））时会发送 SIGINT 信号。
当控制终端上的用户按下退出字符（默认为 ^\（Control-Backslash））时
会发送 SIGQUIT 信号。一般来说，你可以通过按 ^C 让程序直接退出，
也可以通过按 ^\ 让程序带着栈转储退出。

# Go 程序中信号的默认行为

默认情况下，同步信号会被转换为运行时 panic。SIGHUP、SIGINT 或 SIGTERM
信号会导致程序退出。SIGQUIT、SIGILL、SIGTRAP、SIGABRT、SIGSTKFLT、
SIGEMT 或 SIGSYS 信号会导致程序带着栈转储退出。SIGTSTP、SIGTTIN 或
SIGTTOU 信号采用系统默认行为（这些信号被 shell 用于作业控制）。
SIGPROF 信号由 Go 运行时直接处理，以实现 runtime.CPUProfile。
其他信号会被捕获，但不会采取任何动作。

如果 Go 程序启动时忽略 SIGHUP 或 SIGINT（信号处理程序设置为 SIG_IGN），
它们将保持被忽略状态。

如果 Go 程序启动时带有非空信号掩码，通常会被遵循。然而，有些信号会被
显式解除阻塞：同步信号、SIGILL、SIGTRAP、SIGSTKFLT、SIGCHLD、SIGPROF，
以及在 Linux 上的信号 32（SIGCANCEL）和 33（SIGSETXID）（SIGCANCEL 和
SIGSETXID 由 glibc 内部使用）。由 [os.Exec] 或 [os/exec] 启动的子进程
会继承修改后的信号掩码。

# 更改 Go 程序中信号的行为

本包中的函数允许程序更改 Go 程序处理信号的方式。

Notify 会禁用给定一组异步信号的默认行为，转而通过一个或多个已注册的
通道传递它们。具体而言，它适用于信号 SIGHUP、SIGINT、SIGQUIT、SIGABRT
和 SIGTERM。它也适用于作业控制信号 SIGTSTP、SIGTTIN 和 SIGTTOU，
在这种情况下不会发生系统默认行为。它还适用于一些原本不产生任何动作的
信号：SIGUSR1、SIGUSR2、SIGPIPE、SIGALRM、SIGCHLD、SIGCONT、SIGURG、
SIGXCPU、SIGXFSZ、SIGVTALRM、SIGWINCH、SIGIO、SIGPWR、SIGINFO、SIGTHR、
SIGWAITING、SIGLWP、SIGFREEZE、SIGTHAW、SIGLOST、SIGXRES、SIGJVM1、
SIGJVM2，以及系统上使用的任何实时信号。注意，并非所有这些信号在所有
系统上都可用。

如果程序启动时忽略了 SIGHUP 或 SIGINT，并且对其中任一信号调用了
[Notify]，则会为该信号安装一个信号处理程序，它不再被忽略。如果之后对
该信号调用 [Reset] 或 [Ignore]，或者对传递给 Notify 处理该信号的所有
通道调用 [Stop]，则该信号将再次被忽略。Reset 会恢复该信号的系统默认
行为，而 Ignore 会使系统完全忽略该信号。

如果程序启动时带有非空信号掩码，如上所述，有些信号会被显式解除阻塞。
如果对某个被阻塞的信号调用 Notify，它会被解除阻塞。如果之后对该信号
调用 Reset，或者对传递给 Notify 处理该信号的所有通道调用 Stop，
则该信号将再次被阻塞。

# SIGPIPE

当 Go 程序向已损坏的管道写入时，内核会引发 SIGPIPE 信号。

如果程序尚未调用 Notify 来接收 SIGPIPE 信号，那么行为取决于文件
描述符编号。向文件描述符 1 或 2（标准输出或标准错误）上已损坏的管道
写入会导致程序带着 SIGPIPE 信号退出。向其他文件描述符上已损坏的管道
写入则不会对 SIGPIPE 信号采取任何动作，并且该写入会以 [syscall.EPIPE]
错误失败。

如果程序已调用 Notify 来接收 SIGPIPE 信号，则文件描述符编号无关紧要。
SIGPIPE 信号会被传递到 Notify 通道，并且该写入会以 [syscall.EPIPE]
错误失败。

这意味着，默认情况下，命令行程序的行为会像典型的 Unix 命令行程序，
而其他程序在向已关闭的网络连接写入时不会因 SIGPIPE 而崩溃。

# 使用 cgo 或 SWIG 的 Go 程序

在包含非 Go 代码（通常是通过 cgo 或 SWIG 访问的 C/C++ 代码）的 Go
程序中，Go 的启动代码通常先运行。它会在非 Go 启动代码运行之前，
按照 Go 运行时的预期配置信号处理程序。如果非 Go 启动代码希望安装
自己的信号处理程序，它必须采取某些步骤来保持 Go 的良好运行。本节
记录了这些步骤，以及非 Go 代码对信号处理程序设置的更改可能对 Go
程序产生的整体影响。在少数情况下，非 Go 代码可能会在 Go 代码之前
运行，此时下一节也适用。

如果由 Go 程序调用的非 Go 代码不更改任何信号处理程序或掩码，
则其行为与纯 Go 程序相同。

如果非 Go 代码安装了任何信号处理程序，它必须使用 sigaction 的
SA_ONSTACK 标志。否则，一旦收到信号，很可能会导致程序崩溃。
Go 程序通常以有限的栈运行，因此会设置一个备用信号栈。

如果非 Go 代码为任何同步信号（SIGBUS、SIGFPE、SIGSEGV）安装了信号
处理程序，那么它应记录现有的 Go 信号处理程序。如果这些信号在执行 Go
代码时发生，它应调用 Go 信号处理程序（通过查看传递给信号处理程序的 PC
可以确定信号是否在执行 Go 代码时发生）。否则，某些 Go 运行时 panic
将不会如预期发生。

如果非 Go 代码为任何异步信号安装了信号处理程序，它可以选择调用或不调用
Go 信号处理程序。自然，如果它不调用 Go 信号处理程序，上文所述的 Go
行为就不会发生。这对 SIGPROF 信号尤其可能是个问题。

非 Go 代码不应更改 Go 运行时创建的任何线程上的信号掩码。如果非 Go 代码
自行启动新线程，那些线程可以随意设置信号掩码。

如果非 Go 代码启动了新线程、更改了信号掩码，然后在该线程中调用 Go 函数，
Go 运行时会自动解除某些信号的阻塞：同步信号、SIGILL、SIGTRAP、SIGSTKFLT、
SIGCHLD、SIGPROF、SIGCANCEL 和 SIGSETXID。当 Go 函数返回时，非 Go 信号
掩码会被恢复。

如果在未运行 Go 代码的非 Go 线程上调用 Go 信号处理程序，该处理程序通常
会如下将信号转发给非 Go 代码。如果信号是 SIGPROF，Go 处理程序不做任何
事情。否则，Go 处理程序会移除自身、解除该信号的阻塞并再次引发它，
以调用任何非 Go 处理程序或默认系统处理程序。如果程序没有退出，Go
处理程序随后会重新安装自身并继续执行程序。

如果收到 SIGPIPE 信号，当 SIGPIPE 是在 Go 线程上收到时，Go 程序会调用
上文所述的特殊处理。如果 SIGPIPE 是在非 Go 线程上收到，该信号会被转发
给非 Go 处理程序（如果有的话）；如果没有，默认系统处理程序会导致程序
终止。

# 调用 Go 代码的非 Go 程序

当 Go 代码以 -buildmode=c-shared 之类的选项构建时，它会作为现有非 Go
程序的一部分运行。非 Go 代码在 Go 代码启动时可能已经安装了信号处理程序
（在使用 cgo 或 SWIG 时，这种情况也可能在异常情况下发生；此时适用这里
的讨论）。对于 -buildmode=c-archive，Go 运行时会全局构造函数时初始化
信号。对于 -buildmode=c-shared，Go 运行时会共享库被加载时初始化信号。

如果 Go 运行时看到 SIGCANCEL 或 SIGSETXID 信号（仅在 Linux 上使用）
已存在信号处理程序，它会打开 SA_ONSTACK 标志，并保留该信号处理程序。

对于同步信号和 SIGPIPE，Go 运行时将安装一个信号处理程序。它会保存任何
已存在的信号处理程序。如果在执行非 Go 代码时到达同步信号，Go 运行时会
调用已存在的信号处理程序，而不是 Go 信号处理程序。

使用 -buildmode=c-archive 或 -buildmode=c-shared 构建的 Go 代码默认
不会安装任何其他信号处理程序。如果已存在信号处理程序，Go 运行时会打开
SA_ONSTACK 标志，并保留该信号处理程序。如果对某个异步信号调用 Notify，
则会为该信号安装一个 Go 信号处理程序。如果之后对该信号调用 Reset，
则会重新安装该信号原来的处理方式，并在有非 Go 信号处理程序时恢复它。

未使用 -buildmode=c-archive 或 -buildmode=c-shared 构建的 Go 代码会
为上文列出的异步信号安装一个信号处理程序，并保存任何已存在的信号处理
程序。如果信号被传递到非 Go 线程，它的行为如上文所述，不同之处在于，
如果存在已存在的非 Go 信号处理程序，则会在引发信号之前安装该处理程序。

# Windows

在 Windows 上，^C（Control-C）或 ^BREAK（Control-Break）通常会导致
程序退出。如果对 [os.Interrupt] 调用了 Notify，则 ^C 或 ^BREAK 会导致
[os.Interrupt] 被发送到通道上，程序不会退出。如果调用了 Reset，或者对
传递给 Notify 的所有通道调用了 Stop，则会恢复默认行为。

此外，如果调用了 Notify，并且 Windows 向进程发送 CTRL_CLOSE_EVENT、
CTRL_LOGOFF_EVENT 或 CTRL_SHUTDOWN_EVENT，Notify 会返回
syscall.SIGTERM。与 Control-C 和 Control-Break 不同，当收到
CTRL_CLOSE_EVENT、CTRL_LOGOFF_EVENT 或 CTRL_SHUTDOWN_EVENT 时，
Notify 不会改变进程行为 —— 除非进程退出，否则它仍会被终止。但收到
syscall.SIGTERM 会让进程有机会在终止前进行清理。

# Plan 9

在 Plan 9 上，信号具有类型 syscall.Note，它是一个字符串。对一个
syscall.Note 调用 Notify，会导致当该字符串作为 note 发布时，
该值被发送到通道上。
*/
package signal
