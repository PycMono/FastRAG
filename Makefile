.PHONY: build run debug test test-pkg test-single es-index init-db clean tidy

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

# 建 ES 索引。索引**不由服务创建**（设计文档 §9.4），上线前先跑这个。
# 已存在则只校验 mapping、不做改动；--print 只打印 JSON 不发送。
es-index:
	bash scripts/create-es-index.sh

# 建 MySQL 库表。同样不由服务创建（设计文档 §9.4），上线前先跑。
# 可重复执行——DDL 全是 IF NOT EXISTS。schema.sql 里自带 CREATE DATABASE / USE，
# 所以**不要**在 mysql 后面指定库名，否则库还不存在时连不上。
#
# ⚠️ --default-character-set=utf8mb4 不能省：容器里的 mysql 客户端默认 latin1，
# 中文（表注释、字段注释）会被**双向编码错**存进去，而且列和连接字符串都查不出问题。
# 本仓踩过：`演示知识库` 变成 `æ¼"ç¤ºçŸ¥è¯†åº“`。所以别直接 `mysql -uroot -p < scripts/schema.sql`。
#
# 本机装了 mysql 客户端、想直连的话，把 `docker exec -i mysql` 去掉即可。
init-db:
	docker exec -i mysql mysql -h127.0.0.1 -P3306 -uroot -p123456 \
		--default-character-set=utf8mb4 < scripts/schema.sql

# 清理构建产物
clean:
	rm -rf bin/

# 整理依赖
tidy:
	go mod tidy
