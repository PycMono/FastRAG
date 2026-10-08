# FastRAG 后端骨架 — 设计文档

日期：2026-10-08
模块路径：`github.com/PycMono/FastRAG`
参考模板：`/Users/allen/projects/work/github/micro-framework`

## 1. 目标与非目标

### 目标

为 FastRAG（知识库检索）搭建一套**可编译、可运行、可扩展**的 Go 后端骨架，分层与编码约定与
micro-framework 保持一致，使后续新增业务模块只需照抄既有模式。

具体交付：

1. **项目分层** —— 洋葱架构四层目录、层间依赖方向、各层 `register.go`。
2. **后端代码** —— `cmd` 启动入口、依赖注入（Uber FX）装配、一个完整的 `knowledge` 示例模块
   （entity → repository 接口 → application service → controller → persistence 实现）。
3. **错误码** —— `CodeError` 接口 + `BizError`/`SysError` 双分支 + 预定义错误码表。
4. **HTTP / MySQL / Gin** —— gin 引擎与中间件链、统一响应封装、`sqlsdk.Provider` 适配、
   Redis 适配、健康检查、雪花 ID、配置加载。

### 非目标（明确不做）

- **前端**：不做 HTML 模板、静态资源、JS/CSS、页面路由、SEO、i18n。用户明确要求「不需要前端，
  只需要后端 Go 的代码」。
- **OAuth 登录 / 邮箱验证码 / session**：不属于本次骨架范围。用户身份通过占位中间件注入（见 §7.2）。
- **token 配额、AI 多 provider 引擎、history/feedback/compete 等业务模块**：不移植。
- **向量库 / embedding / 真正的检索实现**：只留接口扩展点（见 §6.4）。

## 2. 分层与依赖方向

外层依赖内层，内层不感知外层。

| 层 | 允许做 | 禁止做 |
|----|--------|--------|
| `domain/` | 定义 Entity、仓储**接口**、核心业务规则 | import `gin` / `gorm` / `redis` / `net/http`；import `infrastructure/` 或 `application/`；操作数据库 |
| `application/service/` | 编排用例、DTO→Entity→VO 转换、事务边界 | 写核心领域规则；import `gin.Context`；直接调用 Redis/MySQL 客户端 |
| `infrastructure/` | HTTP 入口、路由绑定、仓储**实现**、外部 SDK 适配、配置加载 | 写业务逻辑；直接返回 Entity 给客户端 |
| `common/` | 跨层共享的 DTO / VO / 错误定义 / 纯工具函数 | 含业务逻辑；import `domain/` / `application/` / `infrastructure/` |

请求流转：

```
HTTP Request
  → gin.Engine                        (infrastructure/driver/gingext)
    → middleware 链                    (infrastructure/middleware)
      → Controller                    (infrastructure/controller/http/{module})
        → Application Service          (application/service/{module})
          → Repository 接口             (domain/repository/{module})
            → Repository 实现           (infrastructure/persistence/{module})
        ← VO
      ← gingext.Send() 统一响应封装
```

## 3. 目录结构（完整文件清单）

```
FastRAG/
├── cmd/server/main.go
├── application/service/
│   ├── knowledge/
│   │   ├── service.go
│   │   └── service_test.go
│   └── register.go
├── common/
│   ├── dto/
│   │   ├── knowledge.go
│   │   ├── pagination.go
│   │   └── pagination_test.go
│   ├── vo/
│   │   ├── knowledge.go
│   │   ├── pagination.go
│   │   └── pagination_test.go
│   └── errors/
│       ├── errors.go
│       └── errors_test.go
├── domain/
│   ├── entity/knowledge/knowledge_base.go
│   ├── repository/
│   │   ├── knowledge/knowledge_base.go
│   │   ├── knowledge/retriever.go
│   │   └── id_service.go
│   ├── service/register.go
│   └── register.go
├── infrastructure/
│   ├── init.go
│   ├── init_test.go
│   ├── config/config.go
│   ├── controller/
│   │   ├── register.go
│   │   └── http/
│   │       ├── register.go
│   │       ├── health/controller.go
│   │       └── knowledge/controller.go
│   ├── driver/
│   │   ├── gingext/gingext.go
│   │   ├── gingext/response.go
│   │   ├── gingext/response_test.go
│   │   ├── mysql/mysql.go
│   │   └── redis/redis.go
│   ├── middleware/
│   │   ├── access_log.go
│   │   ├── rate_limit.go
│   │   └── middleware_test.go
│   ├── persistence/
│   │   ├── knowledge/knowledge_base_repo.go
│   │   ├── knowledge/retriever_stub.go
│   │   └── register.go
│   └── serviceimpl/
│       ├── id_service.go
│       └── register.go
├── scripts/lint-architecture.sh
├── .gitignore
├── Makefile
├── README.md
├── config.json
├── go.mod
└── go.sum
```

`infrastructure/controller/http/register.go` 中只注册 API 路由（无页面路由），
所有 JSON API 统一挂在 `api := router.Group("/api/v1")` 下。

## 4. 依赖清单

对齐 micro-framework 版本，裁掉 OAuth / PDF / AI / i18n 相关依赖。

直接依赖：

```
github.com/PycMono/go-gin-sdk    v0.0.6
github.com/PycMono/go-mysql-sdk v1.0.2
github.com/PycMono/go-cache-sdk v1.0.3
github.com/PycMono/go-logger-sdk v1.0.5
github.com/PycMono/go-context-sdk v1.0.2
github.com/gin-gonic/gin        v1.10.0
github.com/redis/go-redis/v9    v9.19.0
go.uber.org/fx                  v1.23.0
github.com/bwmarrin/snowflake   v0.3.0
gorm.io/gorm                    v1.25.1
```

已按 pinned 版本核对过的 API 契约：

- `middleware.CORS()` / `middleware.Tracing()` / `middleware.Bizctx()` / `middleware.Logger()`
  —— v0.0.6 中 `Logger()` **不接受参数**。
- `ginsdk.HTTPServer` / `ginsdk.ServerOptions{Host,Port,ReadTimeout,WriteTimeout}` /
  `ginsdk.NewHTTPServer(handler, opts)` / `(*HTTPServer).Serve(ctx)`（**无返回值**）/
  `(*HTTPServer).Shutdown(ctx) error` / `ginsdk.HTTPJSONBody{Code,Msg,Data}` /
  `ginsdk.StatusOK` / `ginsdk.ErrCodeUnknown`。
- `bizctx.UserID(v) KV` + `bizctx.WithKV(ctx, kv...)` + `bizctx.GetUserID(ctx)`。
- `connect.InitClient(ctx, *connect.Config) (redis.UniversalClient, error)`，
  `connect.Config{AppName,ClientName,Addr,Password,DB,PoolSize}`。
  注意返回值是 `github.com/redis/go-redis/v9.UniversalClient`，驱动层需直接 import 之。
- `sqlsdk.Provider` 接口 —— `UseDB(ctx) *gorm.DB`；`sqlsdk.Options`；`sqlsdk.NewTransProvider(opts)`。
- `logsdk.SetLogger(logsdk.NewLogrus(logsdk.Options{Module}))` / `logsdk.Info(ctx, msg, logsdk.Any(k,v))` /
  `logsdk.Error(ctx, msg, logsdk.Err(err))`。

私有模块下载：需 `GOPRIVATE=github.com/PycMono/*`。

## 5. 错误码体系

`common/errors/errors.go` 保留模板结构：

- `CodeError` 接口：`error` + `Code() int` + `Message() string` + `Unwrap() error`
- `codeError` 基础实现，`Is()` 按 code 判定同类错误
- `BizError`（预期内业务错误，构造 `NewBizError(code,msg)`）/ `SysError`（非预期系统错误）
- `Params(args...)` 格式化填充占位符、`Wrap(err)` 包装底层错误
- `AsBizError(err)` / `AsSysError(err)` 从错误链提取

预定义错误码：

```
通用段
  10001 ErrInvalidParam   10002 ErrUnauthorized  10003 ErrForbidden
  10004 ErrNotFound       10005 ErrConflict      10006 ErrRateLimited
  10007 ErrInternal

knowledge 段
  10101 ErrKnowledgeBaseNotFound
  10102 ErrKnowledgeBaseNameExists
  10103 ErrKnowledgeBaseCreateFailed   (SysError)
  10104 ErrKnowledgeBaseUpdateFailed   (SysError)
  10105 ErrKnowledgeBaseDeleteFailed   (SysError)
  10106 ErrKnowledgeRetrievalNotImpl   (BizError)
```

响应形态（统一走 `gingext.Send`）：

```jsonc
// 成功
{"code": 0, "msg": "success", "data": {...}}
// 参数错误 → HTTP 400
{"code": 10001, "msg": "invalid parameter", "data": {}}
// 未找到 → HTTP 404
{"code": 10101, "msg": "knowledge base not found", "data": {}}
```

**相对模板的两处改进**（汇总见 §8）：

1. `gingext/response.go` 中 `httpStatusForCode` 的 code→HTTP 状态映射，把裸数字换成
   `common/errors` 里的具名常量引用，行为不变。
2. `gingext.Send` 用 `errors.As` 而非类型断言来提取 `CodeError`。模板写的是
   `err.(errors.CodeError)`，而 Controller 的标准写法是
   `fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err)` 包装绑定错误 ——
   包装后动态类型是 `*fmt.wrapError`，类型断言必然失败，于是所有参数错误都会被降级成
   `ErrCodeUnknown`(-10000)「系统错误」，与 README 宣称的 code 10001 不符。
   改用 `errors.As` 后可沿错误链正确取到业务错误码。

## 6. knowledge 示例模块

### 6.1 Entity

`domain/entity/knowledge/knowledge_base.go`，表名 `knowledge_bases`：

| 字段 | Go 类型 | gorm tag | 说明 |
|------|---------|----------|------|
| ID | string | `primaryKey;size:32` | 雪花 ID |
| UserID | string | `index;size:32` | 归属用户 |
| Name | string | `size:128;not null` | 知识库名称 |
| Description | string | `size:512;default:''` | 描述 |
| EmbeddingModel | string | `size:64;default:''` | 预留：embedding 模型标识 |
| DocCount | int | `default:0` | 文档数（预留统计位） |
| Status | int8 | `default:1` | 1=启用 0=停用 |
| CreatedAt | int64 | `autoCreateTime:milli` | 毫秒时间戳 |
| UpdatedAt | int64 | `autoUpdateTime:milli` | 毫秒时间戳 |

提供 `func (KnowledgeBase) TableName() string { return "knowledge_bases" }`。

### 6.2 仓储接口

`domain/repository/knowledge/knowledge_base.go`：

```go
type IKnowledgeBaseRepo interface {
    Create(ctx context.Context, kb *knowledge.KnowledgeBase) error
    GetByIDAndUserID(ctx context.Context, id, userID string) (*knowledge.KnowledgeBase, error)
    ListByUserID(ctx context.Context, userID string, page, pageSize int) (total int64, list []*knowledge.KnowledgeBase, err error)
    Update(ctx context.Context, kb *knowledge.KnowledgeBase) error
    DeleteByIDAndUserID(ctx context.Context, id, userID string) error
}
```

所有按 ID 的操作都带 `userID` 条件，避免越权。

### 6.3 Application Service 与 HTTP 接口

`application/service/knowledge/service.go` 承载 5 个用例，输入 `dto`、输出 `vo`，
分页使用 `dto.PageQuery` + `vo.PageResult[T]`。

| 方法 | 路由 | 请求 | 响应 |
|------|------|------|------|
| Create | `POST /api/v1/knowledge-bases` | `CreateKnowledgeBaseDTO` | `*vo.KnowledgeBaseVO` |
| List | `GET /api/v1/knowledge-bases` | `ListKnowledgeBaseQuery{PageQuery}` | `*vo.KnowledgeBaseListVO` |
| Get | `GET /api/v1/knowledge-bases/:id` | path `:id` | `*vo.KnowledgeBaseVO` |
| Update | `PUT /api/v1/knowledge-bases/:id` | `UpdateKnowledgeBaseDTO` | `{}` |
| Delete | `DELETE /api/v1/knowledge-bases/:id` | path `:id` | `{}` |

（无返回数据的用例传 `nil`，`gingext.Send` 会归一化为 `{}` 而非 `null`。）

Controller 只做：参数绑定 → 取 userID → 调用 service → `gingext.Send`。不做业务判断、不管事务、
不直接调仓储。

`knowledge.GetByIDAndUserID` 查不到时，仓储层把 `gorm.ErrRecordNotFound` 归一化为
`apperrors.ErrKnowledgeBaseNotFound`，避免 gorm 错误码泄漏到上层。

### 6.4 检索扩展点（预留，不实现）

- `domain/repository/knowledge/retriever.go` 定义：

```go
type RetrieveQuery struct { KnowledgeBaseID string; Query string; TopK int }
type RetrievedChunk struct { DocID string; ChunkIndex int; Content string; Score float64 }
type IKnowledgeRetriever interface {
    Retrieve(ctx context.Context, q RetrieveQuery) ([]*RetrievedChunk, error)
}
```

- `infrastructure/persistence/knowledge/retriever_stub.go` 提供占位实现，返回
  `apperrors.ErrKnowledgeRetrievalNotImpl`。
- HTTP 端点 `POST /api/v1/knowledge-bases/:id/search` 已接线，当前返回 code `10106`。

这样接向量库时只需替换 persistence 层实现，其余各层不动。

## 7. 基础设施接线

### 7.1 依赖注入装配

```
cmd/server/main.go
  → logsdk.SetLogger(...)
  → config.MustLoad()
  → fx.New(infrastructure.Init(conf), fx.Invoke(lifecycle hook))
    → infrastructure/init.go
      → fx.Supply(conf)
      → fx.Provide(redis.NewClient)        // 可空
      → fx.Provide(mysql.NewProvider)
      → fx.Provide(gingext.NewEngine)
      → fx.Provide(gingext.NewHTTPServer)
      → controller.Register                // 控制器 + 路由
      → service.Register                   // 应用服务
      → domain.Register                    // 领域服务
      → persistence.Register               // 仓储实现
      → serviceimpl.Register               // 雪花 ID
  Lifecycle.OnStart: AutoMigrate(knowledge_base) → server.Serve(ctx)  (goroutine)
  Lifecycle.OnStop:  server.Shutdown(ctx)
```

### 7.2 中间件链

`gingext.NewEngine` 按序装配：

1. `middleware.Bizctx()` —— go-gin-sdk，注入业务上下文
2. `middleware.CORS()`
3. `middleware.Tracing()`
4. `middleware.Logger()`
5. `mw.AccessLog()` —— 本项目：method / path / status / latency_ms / client_ip
6. `mw.RateLimit()` —— 本项目：IP 级限流
7. `gin.Recovery()`

**关于用户身份（无需自定义中间件）**：go-gin-sdk 的 `middleware.Bizctx()` 会把请求头
`X-Bizctx-UserID` 解析进 `bizctx`；Controller 通过 `bizctx.GetUserID(c.Request.Context())` 读取，
与 micro-framework 的取值方式完全一致。骨架阶段没有鉴权模块，故由该请求头承载身份标识，
knowledge 接口可直接用 curl 验证；接入真实鉴权后，只需在中间件链中把 session/JWT 解析出的
userID 写入 bizctx，Controller 与 Application Service 均无需改动。

### 7.3 配置

`config.json`（`infrastructure/config/config.go` 定义结构，`MustLoad()` 从工作目录读取）：

```jsonc
{
  "app": "fastrag",
  "debug": true,
  "http":   { "host": "", "port": "8080", "read_timeout": 120, "write_timeout": 120 },
  "mysql":  { "host": "127.0.0.1", "port": 3306, "database": "fastrag",
              "user": "root", "password": "123456",
              "max_open": 100, "max_idle": 10,
              "conn_lifetime": 3600, "conn_timeout": 3,
              "log_level": 3, "slow_threshold": 500 },
  "redis":  { "addr": [], "password": "", "db": 0, "pool_size": 5 },
  "snowflake_node_id": 1
}
```

### 7.4 健康检查

`infrastructure/controller/http/health/controller.go`：

- `GET /health` —— 存活探针，恒返回 `{"status":"up","timestamp":...}`
- `GET /ready` —— 就绪探针，检查 MySQL / Redis（Redis 未启用时标注 `disabled`）

## 8. 相对模板的四处刻意偏离

| # | 偏离 | 原因 | 影响面 |
|---|------|------|--------|
| 1 | **Redis 可选**：`redis.addr` 为空时不报错，跳过 Redis 初始化 | 模板中 addr 为空会直接启动失败，骨架不该被 Redis 卡住 | 仅 `driver/redis/redis.go` + health 就绪检查 |
| 2 | **`gingext.Send` 改用 `errors.As`** 提取 `CodeError` | 模板的类型断言遇到 Controller 惯用的 `fmt.Errorf("%w: %v", ...)` 包装必然失败，参数错误会被误报成 `ErrCodeUnknown`(-10000) | 仅 `driver/gingext/response.go` |
| 3 | **补单元测试** | 模板只对 errors 有测试；骨架应示范测试写法。其中 `infrastructure/init_test.go` 是这个骨架唯一的运行时装配验证：`go build`/`go vet` 只能证明能编译，而 fx 的类型键（接口 vs 具体类型）、Provide 冲突、循环依赖都是运行时失败，若无人真正跑一次 `fx.New`，DI 装配错误只能等到手动启动服务时才暴露 | 新增 7 个 `_test.go` |
| 4 | **`vo.NewPageResult` 增加 `pageSize < 1 → 10` 兜底下限** | 模板直接 `int(total) / pageSize`，而 `PageSize` 是零值即为 0 的导出字段，调用方一旦忘记先走 `PageQuery.Limit()` 就会整数除零 panic（普通请求变 500）。`common/` 是每个列表接口都会照抄的公共件，不该带这个雷 | 仅 `common/vo/pagination.go`（3 行）+ 1 个测试 |

除上述四点外，分层、命名、DI 装配方式、统一响应结构、错误码体系、分页工具、身份注入方式
（`middleware.Bizctx()` + `bizctx.GetUserID`）均与 micro-framework 一致。

## 9. 测试与验证

单元测试（纯逻辑，不依赖 DB / Redis）：

- `common/errors/errors_test.go` —— code 判定、`Is()`、`Params()`、`Wrap()`、`AsBizError/AsSysError`
- `common/dto/pagination_test.go` —— `PageQuery` 的 offset/limit 计算与非法值归一
- `common/vo/pagination_test.go` —— `NewPageResult` 边界（整除、余数、total=0）
- `infrastructure/middleware/middleware_test.go` —— 直接测试 `RateLimiter`：放行至上限、
  超限拒绝、不同 IP 互不影响、窗口过期后重置（`RateLimit()` 中间件固定 600 req/min，不便单测）
- `infrastructure/driver/gingext/response_test.go` —— `Send` 的统一响应契约：成功时 `data`
  归一化为 `{}` 而非 `null`、业务数据透传、包装后的业务错误码提取、404 映射、普通 error 的
  兜底码。**这是偏离 #2 的回归护栏**：把 `errors.As` 改回模板的类型断言，该测试即失败
  （已用扰动实验验证：HTTP 400→200、code 10001→-10000）
- `application/service/knowledge/service_test.go` —— fake repo 驱动 5 个用例（含 not found 分支）
- `infrastructure/init_test.go` —— DI 装配冒烟：用 `fx.Decorate` 把 MySQL provider 换成桩，
  真正执行一次 `fx.New(infrastructure.Init(conf), fx.Decorate(...), fx.Invoke(...))`，
  断言依赖图装配无错、8 条路由全部注册到位（`/health`、`/ready` 以及
  `/api/v1/knowledge-bases` 下的 6 条）。**这是骨架唯一的运行时装配验证**——编译期
  看不见 fx 的类型键（接口 vs 具体类型）不匹配、Provide/Decorate 冲突、循环依赖

验证命令：

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPRIVATE="github.com/PycMono/*"
go mod tidy
gofmt -l .                       # 期望无输出
go build ./...
go vet ./...
go test ./...
bash scripts/lint-architecture.sh
```

`scripts/lint-architecture.sh` 移植模板的分层 import 红线检查，去掉页面/前端相关规则：

- `domain/` 不得 import gin / gorm / redis driver / net/http
- `application/` 不得 import gin / driver / controller
- `common/` 不得 import domain / application / infrastructure
- Controller 不得使用 `c.JSON(`（必须用 `gingext.Send`），health 豁免
- API 路由必须注册在 `api := router.Group(v1Prefix)` 分组内

## 10. 未决事项

无。若后续需要 i18n、鉴权、向量检索，均可在既有分层上按 §6.4 的模式扩展。
