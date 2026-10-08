# FastRAG

知识库检索服务 — Go 洋葱架构后端骨架。

参考 `micro-framework` 模板构建，仅包含后端代码（无前端）。

## 架构介绍

本项目采用洋葱架构（Onion Architecture），核心思想是分离业务复杂性与技术复杂性，使领域层独立于基础设施。

分层结构（外层依赖内层，内层不感知外层）：

```
cmd/server              // 服务启动入口
application/service     // 应用服务层：编排领域服务，处理用例，DTO→Entity→VO 转换
domain
├── entity              // 领域模型（聚合根、实体、值对象）
├── repository          // 仓储接口（持久化抽象）
└── service             // 领域服务（核心业务逻辑）
infrastructure
├── config              // 配置加载
├── controller/http     // HTTP 请求入口、路由绑定（北向网关）
├── driver              // 第三方 SDK 适配（gingext / mysql / redis）
├── middleware          // 限流、访问日志
├── persistence         // 仓储实现、数据持久化（南向网关）
└── serviceimpl         // 基础设施服务实现（雪花 ID）
common
├── dto                 // 入参定义
├── vo                  // 返回值定义
└── errors              // 业务错误码与预定义错误
```

依赖注入使用 [Uber FX](https://github.com/uber-go/fx)。

### 依赖方向红线

| 层 | 禁止 import |
|----|-------------|
| `domain/` | `gin`、`gorm`、`redis`、`net/http`、`infrastructure/...`、`application/...` |
| `application/` | `gin`、`infrastructure/driver/...`、`infrastructure/controller` |
| `common/` | `domain/...`、`application/...`、`infrastructure/...` |

用 `make lint` 检查。

## 快速开始

前置：Go 1.25+、MySQL。Redis 可选（`config.json` 的 `redis.addr` 留空即不启用）。

```bash
# 下载依赖
go mod download

# 调整 config.json 中的 MySQL 连接信息，然后运行
go run ./cmd/server
# 或
make run
```

服务默认监听 `:8080`，首次启动会自动建表（`knowledge_bases`）。

启动期两种典型报错：

- `panic: dial tcp ...: connect: connection refused` —— MySQL 不可达。go-mysql-sdk 在
  DSN 不可用时是直接 panic 而不是返回错误，所以看到的是原始 panic 而非友好提示。
- `missing type` / `cycle detected` —— DI 装配问题，先跑 `go test ./infrastructure/` 定位。

`config.json` 的 `snowflake_node_id` 是每个实例必须唯一的 0–1023 整数。该键**缺省时取值为 0**，
而 0 是合法节点号，因此多实例共用一份「没写这个键」的配置会生成重复 ID——部署多副本时务必显式配置。

## 常用命令

```bash
make build        # 构建二进制到 bin/server
make run          # 直接运行服务
make test         # 运行所有测试（含 race 检测和覆盖率）
make test-pkg     # 运行指定包测试，如 make test-pkg PKG=application/service/knowledge
make test-single  # 运行单个测试，如 make test-single NAME=TestService_Create PKG=application/service/knowledge
make lint         # 架构红线检查
make tidy         # 整理 go.mod
make clean        # 清理构建产物
```

## 统一响应格式

所有 API 均通过 `gingext.Send` 返回：

```jsonc
// 成功
{"code": 0, "msg": "success", "data": {...}}
// 参数错误（HTTP 400）
{"code": 10001, "msg": "invalid parameter", "data": {}}
// 未找到（HTTP 404）
{"code": 10101, "msg": "knowledge base not found", "data": {}}
// 其他业务/系统错误（HTTP 200 + 业务码）
{"code": 10103, "msg": "knowledge base create failed", "data": {}}
```

### 错误码

| 码段 | 含义 |
|------|------|
| `10001`–`10007` | 通用：参数错误 / 未登录 / 无权限 / 不存在 / 冲突 / 限流 / 内部错误 |
| `10101`–`10106` | knowledge：不存在 / 名称重复 / 创建失败 / 更新失败 / 删除失败 / 检索未实现 |

其中 `10102`（名称重复）当前为**预留**：脚手架未在 `Create` 里做名称唯一性校验，
仓储接口（见 `domain/repository/knowledge`）也没有按名称查询的方法，因此这条码
暂时不会由任何请求返回。等需要真正约束「同一用户下知识库不重名」时，再补仓储方法、
表上的唯一索引与并发创建的处理。

错误定义集中在 `common/errors/errors.go`。新增错误码时在此追加，不要在业务代码里随手 `errors.New`。

错误分为两类：

- `BizError`（预期内，如「知识库不存在」）
- `SysError`（非预期，如「数据库连接失败」）

## 用户身份

骨架阶段尚未接入鉴权模块。`middleware.Bizctx()`（来自 go-gin-sdk）会把请求头
`X-Bizctx-UserID` 解析进 `bizctx`，Controller 通过 `bizctx.GetUserID()` 读取。

```bash
curl -H "X-Bizctx-UserID: u1" http://localhost:8080/api/v1/knowledge-bases
```

接入真实鉴权后，在中间件链里把 session/JWT 解析出的 userID 写入 `bizctx` 即可，
Controller 与 Application Service 无需改动。

## API 示例

```bash
# 创建知识库
curl -X POST http://localhost:8080/api/v1/knowledge-bases \
  -H "Content-Type: application/json" \
  -H "X-Bizctx-UserID: u1" \
  -d '{"name":"产品手册","description":"内部产品文档"}'

# 列表（分页）
curl -H "X-Bizctx-UserID: u1" "http://localhost:8080/api/v1/knowledge-bases?page=1&page_size=10"

# 详情
curl -H "X-Bizctx-UserID: u1" http://localhost:8080/api/v1/knowledge-bases/{id}

# 更新
curl -X PUT http://localhost:8080/api/v1/knowledge-bases/{id} \
  -H "Content-Type: application/json" \
  -H "X-Bizctx-UserID: u1" \
  -d '{"name":"产品手册 v2"}'

# 删除
curl -X DELETE -H "X-Bizctx-UserID: u1" http://localhost:8080/api/v1/knowledge-bases/{id}

# 检索（扩展点，当前返回 code 10106 未实现）
curl -X POST http://localhost:8080/api/v1/knowledge-bases/{id}/search \
  -H "Content-Type: application/json" \
  -H "X-Bizctx-UserID: u1" \
  -d '{"query":"如何配置","top_k":5}'

# 健康检查
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

## 如何新增一个业务模块

以 `order` 为例，照抄 `knowledge` 模块的分层写法：

1. **domain 层**：`domain/entity/order/order.go`、`domain/repository/order/order.go`（仓储接口）
2. **common 层**：`common/dto/order.go`、`common/vo/order.go`，错误码追加到 `common/errors/errors.go`
3. **application 层**：`application/service/order/service.go`，并在 `application/service/register.go` 里 `fx.Provide`
4. **infrastructure 层**：`infrastructure/persistence/order/order_repo.go`（实现仓储接口）、
   `infrastructure/controller/http/order/controller.go`，并在对应的 `register.go` 里注册
5. **建表**：把新实体加进 `cmd/server/main.go` 的 `AutoMigrate` 列表
6. **路由**：在 `infrastructure/controller/http/register.go` 的 `api` Group 下挂载

## 后续规划

- 接入向量库：实现 `domain/repository/knowledge.IKnowledgeRetriever`，
  在 `infrastructure/persistence/register.go` 中替换 `RetrieverStub`，其余各层无需改动。
  接口约定：`RetrieveQuery.TopK` 为 `0` 表示客户端没有指定条数（`top_k` 是可选项），
  由检索实现自己决定默认值——Application Service 只做透传，不替实现方定这个策略
- 文档入库与切片：新增 `document` 模块
- 鉴权：见上文「用户身份」
