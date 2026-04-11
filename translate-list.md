# Go 1.26 标准库源码文档

## 第1部分 输入输出 (Input/Output)

- [X] io：提供I/O原语的基础接口
  - [X] fs: Package fs defines basic interfaces to a file system.
  - [X] ioutil: Package ioutil implements some I/O utility functions.
- [X] bufio：实现带缓冲的I/O，包装io.Reader/io.Writer

## 第2部分 文本处理

- [X] bytes：实现字节切片的操作函数
- [X] strings：实现UTF-8编码字符串的基础操作
- [X] strconv：基本数据类型与其字符串表示的转换
- [ ] fmt：实现格式化I/O，类似C的printf/scanf
- [ ] regexp：实现正则表达式搜索
- [ ] html：提供HTML文本转义与反转义函数
- [ ] unicode：提供Unicode码点属性测试相关函数
- [ ] text/scanner：UTF-8文本的扫描器与分词器
- [ ] text/tabwriter：将制表符列转换为对齐文本的写入过滤器
- [ ] text/template：实现数据驱动的文本生成模板
- [ ] mime：实现MIME规范部分功能

## 第3部分 数据结构与算法

- [ ] container/heap：为实现heap.Interface的类型提供堆操作
- [ ] container/list：实现双向链表
- [ ] container/ring：实现循环链表操作
- [ ] sort：提供切片和自定义集合的排序基础功能
- [ ] slices：定义适用于任意类型切片的通用函数
- [ ] maps：定义适用于任意类型map的通用函数
- [ ] index/suffixarray：基于内存后缀数组实现对数时间子串搜索
- [ ] cmp：提供有序值比较相关的类型和函数
- [ ] iter：提供序列迭代器相关的基础定义和操作
- [ ] unique：提供可比较值的规范化（ intern）工具

## 第4部分 日期与时间

- [ ] time：提供时间测量与展示功能

## 第5部分 数学计算

- [ ] math：提供基础数学常量和数学函数

## 第6部分 文件系统与路径

- [ ] os：提供操作系统功能的平台无关接口
- [ ] path：实现斜杠分隔路径的操作工具
- [ ] embed：提供对Go程序中嵌入文件的访问

## 第7部分 数据持久存储与交换

- [ ] database/sql：提供SQL类数据库的通用接口
- [ ] encoding：定义数据与字节/文本格式相互转换的通用接口
- [ ] errors：实现错误处理相关函数
- [ ] expvar：提供服务器运行计数器等公共变量的标准化接口
- [ ] flag：实现命令行标志解析
- [ ] log：实现简易日志包
- [ ] structs：定义可作为结构体字段的标记类型，用于修改结构体属性

## 第8部分 数据压缩与归档

- [ ] archive/tar：实现tar存档访问
- [ ] archive/zip：提供ZIP归档文件的读写支持
- [ ] compress/bzip2：实现bzip2解压缩
- [ ] compress/flate：实现DEFLATE压缩数据格式（RFC 1951）
- [ ] compress/gzip：实现gzip格式压缩文件读写（RFC 1952）
- [ ] compress/lzw：实现Lempel-Ziv-Welch压缩格式
- [ ] compress/zlib：实现zlib格式压缩数据读写（RFC 1950）

## 第9部分 测试

- [ ] testing：提供Go包自动化测试支持

## 第10部分 进程、线程与 goroutine

- [ ] runtime：包含与Go运行时系统交互的操作，如goroutine控制
- [ ] plugin：实现Go插件的加载与符号解析

## 第11部分 网络通信与互联网

- [ ] net：提供网络I/O的可移植接口，包含TCP/IP、UDP、域名解析等
- [ ] image：实现基础2D图像库

## 第12部分 应用构建与 Debug

- [ ] debug/buildinfo：访问Go二进制文件中的构建信息
- [ ] debug/dwarf：访问可执行文件中的DWARF调试信息
- [ ] debug/elf：实现ELF目标文件访问
- [ ] debug/gosym：访问Go二进制文件中的符号和行号表
- [ ] debug/macho：实现Mach-O目标文件访问
- [ ] debug/pe：实现Windows PE可执行文件访问
- [ ] debug/plan9obj：实现Plan 9 a.out目标文件访问

## 第13部分 运行时特性

- [ ] context：定义Context类型，传递截止时间、取消信号和请求域值

## 第14部分 底层库介绍

- [ ] syscall：包含底层操作系统原语的接口
- [ ] unsafe：包含绕过Go程序类型安全的操作
- [ ] weak：提供安全的弱引用内存方式，不阻止内存回收

## 第15部分 同步

- [ ] sync：提供互斥锁等基础同步原语

## 第16部分 加解密

- [ ] crypto：汇集通用加密常量
- [ ] hash：提供哈希函数接口

## 第17部分 反射

- [ ] reflect：实现运行时反射，允许程序操作任意类型的对象

## 第18部分 语言工具

- [ ] go/ast：声明Go包语法树的表示类型
- [ ] go/build：收集Go包的相关信息
- [ ] go/constant：实现无类型Go常量及其操作
- [ ] go/doc：从Go AST中提取源码文档
- [ ] go/format：实现Go源码的标准格式化
- [ ] go/importer：提供导出数据导入器的访问
- [ ] go/parser：实现Go源码文件解析器
- [ ] go/printer：实现AST节点打印
- [ ] go/scanner：实现Go源码扫描器
- [ ] go/token：定义Go词法标记常量及基础操作
- [ ] go/types：声明数据类型并实现Go包类型检查算法
- [ ] go/version：提供Go版本的相关操作
