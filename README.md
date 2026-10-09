# FastRAG

知识库检索服务 — Go 洋葱架构后端骨架。

参考 `micro-framework` 模板构建。后端服务 + 一个零依赖的演示页
（`index.html` 直接 `go:embed` 进二进制，挂在根路径，没有前端构建步骤）。

## 架构介绍

本项目采用洋葱架构（Onion Architecture），核心思想是分离业务复杂性与技术复杂性，使领域层独立于基础设施。

分层结构（外层依赖内层，内层不感知外层）：

```
cmd/server              // 服务启动入口
cmd/reconcile           // 计数对账命令
application/service     // 应用服务层：编排领域服务，处理用例，DTO→Entity→VO 转换
├── ingest              // 文档导入 / 删除
└── search              // 混合检索
domain
├── entity              // 领域模型（聚合根、实体；带 gorm tag 的即表模型）
├── factory             // 实体的命名构造：保证 CreateTs/DeleteTs 漏填不了
├── repository          // 仓储接口（持久化抽象）
├── interfaces          // 外部服务接口（embedding / rerank）
├── service             // 领域服务（切片）与其参数类型 ChunkOptions / ChunkInput
└── register.go         // 领域层 fx 注册
infrastructure
├── config              // 配置加载
├── controller/http     // HTTP 请求入口、路由绑定（北向网关）
│   ├── doc             // 导入 / 删除
│   ├── search          // 检索
│   ├── health          // /health、/ready 探针
│   └── web             // 根路径的演示页
├── driver              // 第三方 SDK 适配（es / gingext / mysql / redis）
├── middleware          // 访问日志（限流本期不做）
├── persistence         // 仓储实现、数据持久化（南向网关）
└── serviceimpl         // 基础设施服务实现（embedding / rerank / 雪花 ID）
common
├── constants           // 枚举与默认值
├── dto                 // 入参定义
├── vo                  // 返回值定义
└── errors              // 业务错误码与预定义错误
```

**领域实体直接就是持久化模型**：`domain/entity/` 下的 `knowledge_base.go` /
`knowledge_doc.go` 上挂着 `gorm:"column:..."` 和 `TableName()`，没有再单设 `po` / `mapper` 两层。
实体上的 tag 与 `scripts/schema.sql` 是同一份列定义的两个写法——改一边就要改另一边，
不一致不会报错，只会静默查不到数据。`chunk.go` / `collection.go` 不带 tag：它们不是表。

依赖注入使用 [Uber FX](https://github.com/uber-go/fx)。

### 依赖方向红线

| 层 | 禁止 import |
|----|-------------|
| `domain/` | `gin`、`gorm`、`redis`、`net/http`、`infrastructure/...`、`application/...` |
| `application/` | `gin`、`infrastructure/driver/...`、`infrastructure/controller` |
| `common/` | `domain/...`、`application/...`、`infrastructure/...` |

这三条靠 code review 守，仓库里没有自动化检查（早期有过一个 `scripts/lint-architecture.sh`，已删除）。

表中的 `...` 包含该层根包本身：例如 `application/` 的红线既匹配 `infrastructure/driver`、
`infrastructure/controller` 这两个层根包，也匹配它们的子包；`domain/`、`common/` 的红线同样
匹配 `infrastructure`、`application` 这些不带子路径的层根包。

## 快速开始

前置：Go 1.25+、MySQL、Elasticsearch（需装 analysis-ik 插件，见 `deploy/README.md`）。
Redis 可选（`config.json` 的 `redis.addr` 留空即不启用）。

**库表和索引要先建好**——服务自己不做这件事（设计文档 §9.4）：

```bash
make init-db     # MySQL 建表
make es-index    # ES 建索引
```

```bash
# 下载依赖
go mod download

# 调整 config.json 中的 MySQL 连接信息，然后运行
go run ./cmd/server
# 或
make run
```

服务默认监听 `:8080`。**不建表、也不建索引**——两者都是上线前由人先建好的（见上）。

启动期两种典型报错：

- `panic: dial tcp ...: connect: connection refused` —— MySQL 不可达。go-mysql-sdk 在
  DSN 不可用时是直接 panic 而不是返回错误，所以看到的是原始 panic 而非友好提示。
- `missing type` / `cycle detected` —— DI 装配问题。fx 的报错本身就带缺失的类型与依赖链，
  照着看即可；这类错误只在**启动时**才暴露，改了 `register.go` 记得把服务拉起来确认一次。

索引没建的情况**不在启动期报**，而是在第一次检索/导入时报（`code=10207`），
错误信息里带要执行的命令。这样 ES 没准备好不至于让整个服务起不来。

`config.json` 的 `snowflake_node_id` 是每个实例必须唯一的 0–1023 整数。该键**缺省时取值为 0**，
而 0 是合法节点号，因此多实例共用一份「没写这个键」的配置会生成重复 ID——部署多副本时务必显式配置。

## 常用命令

```bash
make build        # 构建二进制到 bin/server
make run          # 直接运行服务
make test         # 运行所有测试（含 race 检测和覆盖率）
make test-pkg     # 运行指定包测试，如 make test-pkg PKG=infrastructure/persistence
make test-single  # 运行单个测试，如 make test-single NAME=TestKNNClauses PKG=infrastructure/persistence
make init-db      # 建 MySQL 库表（可重复执行）
make es-index     # 建 ES 索引（已存在则只校验 mapping）
make tidy         # 整理 go.mod
make clean        # 清理构建产物
```

## 统一响应格式

所有 JSON API 均通过 `ginsdk.Send` 返回（`/health`、`/ready` 两个探针除外：K8s 探针需要固定格式，
因此直接用 `c.JSON`，见 `infrastructure/controller/http/health/controller.go`）：

```jsonc
// 成功
{"code": 0, "msg": "success", "data": {...}}
// 参数错误
{"code": 10001, "msg": "dense_weight 必须在 [0, 1]", "data": {}}
// 切片为空（正文全是空白、或切片参数把内容滤光了）
{"code": 10201, "msg": "chunk result is empty", "data": {}}
// 向量库异常
{"code": 10206, "msg": "vector store failed", "data": {}}
// 索引没建（详见 design §9.4；msg 里带要执行的命令）
{"code": 10207, "msg": "ES 索引 \"fastrag\" 不存在。…bash scripts/create-es-index.sh", "data": {}}
```

**HTTP 状态码恒为 `200`**，成败一律由 `code` 表达。这是 `ginsdk.Send` 的契约：
状态码不再承载语义，就不会出现「状态码说成功、body 说失败」两套信号打架，
网关也不会按 4xx/5xx 自作主张重试。调用方（含 `infrastructure/controller/http/web/`
的前端页面）必须按 `code !== 0` 判失败，不要用 `resp.ok` / `resp.status`。

代价是中间件和监控看不见失败了，所以 `ginsdk.Send` 会把错误挂到 `c.Errors`，
`Tracing` / `Metrics` / `Logger` 三个中间件靠它识别失败请求。这一点由 SDK
保证，业务代码不必操心——但**自定义 Sender 时必须自己调 `ctx.Error(err)`**。

### 错误码

| 码段 | 含义 |
|------|------|
| `10001`–`10007` | 通用：参数错误 / 未登录 / 无权限 / 不存在 / 冲突 / 限流 / 内部错误 |
| `10101`–`10106` | knowledge：不存在 / 名称重复 / 创建失败 / 更新失败 / 删除失败 / 检索未实现 |
| `10201`–`10207` | 文档：切片为空 / 切片过长 / 导入失败 / 删除失败 / embedding 异常 / 向量库异常 / 索引缺失 |
| `10301` | 检索失败 |

`10101`–`10106` 是脚手架时期的遗留，现在**没有一个会由 HTTP 请求返回**：
知识库的 CRUD 全在外部系统里（设计文档 §1.2），本服务只读写 `knowledge_base` 表、
不提供对应接口。其中 `10106`（检索未实现）尤其名不副实——检索已经落地并改用 `10301`，
它只剩兼容意义，别再用。这几条现在只被 `common/errors/errors_test.go` 引用。
`10002`（未登录）/`10003`（无权限）同理：本服务不做鉴权（见下文「租户与鉴权」）。

错误定义集中在 `common/errors/errors.go`。新增错误码时在此追加，不要在业务代码里随手 `errors.New`。

错误分为两类：

- `BizError`（预期内，如「切片结果为空」「dense_weight 超出范围」）
- `SysError`（非预期，如「embedding 服务异常」「数据库连接失败」）

## 租户与鉴权

**本服务不做鉴权**：租户 `account` 就是请求体里的一个普通字符串参数，不做任何校验。

它同时是单索引多租户的隔离键（设计文档 D2、§9）：ES 里同一份索引装着所有租户的切片，
靠 `account` 这个 `keyword` 字段做 term 过滤（**不能是 text**——用分词匹配的话
`demo` 会命中 `demo-other` 的文档，那是越权而不是召回变小），MySQL 侧每个查询也都带
`account` 条件。

这等于把「调用方必须自己保证 `account` 是真的」当前提：网关、内部服务间调用都行，
但一旦把 `account` 直接暴露给终端用户，改一个字段就能读到别人的库。

`security.trust_request_account` 就是把这条前提变成一次显式承认——不置为 `true`
服务直接拒绝启动（`infrastructure/init.go` 的 `checkTrustRequestAccount`，§6.1）。
这道闸门防的是「把服务顺手摆到公网上」这种改动，代码本身不会因此报任何错。

## API 示例

业务路由只有三个（`infrastructure/controller/http/register.go`）：写文档、删文档、检索。
知识库的 CRUD 不在本服务范围内（§1.2）——`knowledge_base` 表由外部系统维护。

```bash
# 导入文档。format 取 markdown / text / chunks；
# refresh=true 表示写完立刻刷索引、返回即可搜（默认 false，按 30s 周期）
curl -X POST http://localhost:8080/api/v1/docs \
  -H 'Content-Type: application/json' \
  -d '{"account":"demo","kb_no":"demo-kb","doc_name":"产品手册.md","format":"text",
       "content":"……正文……","refresh":true}'

# 批量导入：请求体是数组，最多 100 篇。恒返回 200 + 逐条明细，
# 单条失败不改状态码——否则调用方分不清「全挂」和「挂 3 条」，只能整批重试
curl -X POST http://localhost:8080/api/v1/docs/batch \
  -H 'Content-Type: application/json' \
  -d '[{"account":"demo","kb_no":"demo-kb","doc_name":"a.md","format":"text","content":"…"},
       {"account":"demo","kb_no":"demo-kb","doc_name":"b.md","format":"text","content":"…"}]'

# 删除文档（POST 而非 DELETE：DELETE 带 body 合法，但网关和客户端库常把它丢掉）
curl -X POST http://localhost:8080/api/v1/docs/delete \
  -H 'Content-Type: application/json' \
  -d '{"account":"demo","kb_no":"demo-kb","doc_name":"产品手册.md"}'

# 检索。dense_weight 省略则用配置默认值；<=0.01 只走 BM25，>=0.99 只走 kNN
curl -X POST http://localhost:8080/api/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"account":"demo","kb_nos":["demo-kb"],"query":"如何配置",
       "limit":5,"dense_weight":0.5,"rerank_switch":false}'

# 探针
curl http://localhost:8080/health
curl http://localhost:8080/ready

# 演示页（挂在根路径，不在 /api 下：它是给人看的 HTML，不是接口）
open http://localhost:8080/
```

## 如何新增一个业务模块

以 `order` 为例，照抄 `ingest` / `search` 的分层写法：

1. **domain 层**：实体放 `domain/entity/order.go`（要落库的话，`gorm:"column:..."` 与
   `TableName()` 直接写在这上面）、仓储接口放 `domain/repository/order.go`
2. **common 层**：`common/dto/order.go`、`common/vo/order.go`，错误码追加到 `common/errors/errors.go`
3. **application 层**：新建 `application/service/order/`，并在 `application/service/register.go` 里 `fx.Provide`
4. **infrastructure 层**：仓储实现 `infrastructure/persistence/order_repo.go`、
   Controller `infrastructure/controller/http/order/controller.go`；
   前者在 `infrastructure/persistence/register.go` 里注册，后者在
   `infrastructure/controller/http/register.go` 里注册
5. **建表**：DDL 写进 `scripts/schema.sql`，然后 `make init-db`（服务不建表）
6. **路由**：在 `infrastructure/controller/http/register.go` 的 `api` Group 下挂载

定包的原则只有一条：**一个类型该住哪，看它在哪个包的函数签名里被需要**。
谁签名要它，它就得待在谁够得着的地方；没人跨包要它，就别给它单开一个包或目录。
本仓那几个「看着像模块、其实是层」的目录（`entity/`、`repository/`、`persistence/`、
`driver/`）都是这么收拢来的——它们下面不再按业务分子目录，只有业务真的需要
独立的一层时（比如 `application/service/` 下的 `ingest`、`search`）才分。

## 后续规划

清单在设计文档 §13「待定」里，这里只点出三条 demo 阶段**明确不做**的：

- **写侧一致性（代次切换）** —— 重导入或并发写同一文档时，同一篇文档的新旧切片
  可能同时在 ES 里，检索会重复召回同一段内容的两个版本。窗口已经从「一个刷新周期」
  压到「一次请求内的几十毫秒」（`DeleteExcept` 的 `WithRefresh(true)`），但没有消除。
  方案与代价见 §13 待定 9，要等核心功能做完再上
- **写侧限流** —— 见 §10.3
- **embedding 失败重试** —— 当前靠调用方重试（§11 场景 8）

其余待定项（检索深翻页、`rank_constant` 调参、gRPC 接入、`_routing`、多 `search_mode`
的并行度、租户与切片规模）逐条列在 §13 的表里。
