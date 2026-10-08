.PHONY: build run debug test test-pkg test-single lint clean tidy

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

# 清理构建产物
clean:
	rm -rf bin/

# 整理依赖
tidy:
	go mod tidy
