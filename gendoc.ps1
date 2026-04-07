# 1. 设置 GOROOT 为你克隆的 Go 源码目录
set GOROOT=F:\技术\projects\golang_source\go1.26

# 2. 生成完整的静态文档（不使用 -embed）
doc2go -out ./go1.26-full std