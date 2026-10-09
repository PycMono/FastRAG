.PHONY: build run debug test test-pkg test-single lint es-vectors es-vector-full mysql clean tidy

# 构建项目
build:
	go build -o bin/server ./cmd/server

# 运行服务
run:
	go run ./cmd/server

# 调试模式启动后端（等待 Delve 连接，端口 2345）
debug:
	@dlv debug ./cmd/server --headless --listen=:2345 --api-version=2 --accept-multiclient

# 运行所有测试
test:
	go test -v -race -cover ./...

# 运行指定包的测试
test-pkg:
	go test -v -race -cover ./$(PKG)/...

# 运行单个测试
test-single:
	go test -v -run $(NAME) ./$(PKG)/...

# 架构红线检查
lint:
	bash scripts/lint-architecture.sh

# 看 ES 里有没有向量（排查用：_source 里看不到 *_vec 是 ES 9 的正常行为）
es-vectors:
	bash scripts/es-inspect-vector.sh

# 同上，但把 1024 维向量**全部**打出来（前 6 维看不够时用）
es-vector-full:
	bash scripts/es-inspect-vector.sh --full

# 连 MySQL 改数据（**别用 docker exec mysql mysql**，那条连接客户端字符集是 latin1，
# 手工插中文会变 æ¼”ç¤º... 这种双向编码错。详见 scripts/mysql.sh 头部注释）
mysql:
	bash scripts/mysql.sh

# 清理构建产物
clean:
	rm -rf bin/

# 整理依赖
tidy:
	go mod tidy
