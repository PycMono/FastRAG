# FastRAG

知识库检索与检索质量评测服务 — Go 洋葱架构后端骨架。

参考 `micro-framework` 模板构建。Go 后端与 Vue 3 + TypeScript 知识库工作台位于同一仓库。
前端构建产物通过 `go:embed` 内嵌到二进制，挂在根路径；生产环境只部署一个 Go 服务，无需 Node.js 或 CDN。

除文档导入与混合检索外，仓库还内置离线检索评测：题库生成与人工复核、Ragas 指标评分、
失败项重评、基线回归对比和单题诊断。评测默认关闭，需要单独的 Python 评分服务，不影响正常检索。

## 架构介绍

本项目采用洋葱架构（Onion Architecture），核心思想是分离业务复杂性与技术复杂性，使领域层独立于基础设施。

分层结构（外层依赖内层，内层不感知外层）：

```
frontend/               // Vue 3 工作台源码与前端构建配置
cmd/server              // 服务启动入口
application/service     // 应用服务层：编排领域服务，处理用例，DTO→Entity→VO 转换
├── ingest              // 文档导入 / 删除
├── search              // 混合检索
└── evaluation          // 评测任务、题库标注、单题诊断
domain
├── entity              // 领域模型（聚合根、实体；带 gorm tag 的即表模型）
├── factory             // 实体的命名构造：保证 CreateTs 漏填不了
├── repository          // 仓储接口（持久化抽象）
├── interfaces          // 外部服务接口（embedding / rerank）
├── service             // 领域服务（切片）与其参数类型 ChunkOptions / ChunkInput
└── register.go         // 领域层 fx 注册
infrastructure
├── config              // 配置加载
├── evaluation.go       // 评测服务装配（任务执行器、评分客户端、后台 worker）
├── controller/http     // HTTP 请求入口、路由绑定（北向网关）
│   ├── doc             // 导入 / 删除
│   ├── search          // 检索
│   ├── evaluation      // 评测任务、题库、记录与单题诊断
│   ├── health          // /health、/ready 探针
│   └── web             // Vue 工作台入口与内嵌静态资源
├── driver              // 第三方 SDK 适配（es / gingext / mysql / redis）
├── middleware          // 访问日志
├── persistence         // 仓储实现、数据持久化（南向网关）
└── serviceimpl         // 基础设施服务实现（embedding / rerank / 雪花 ID）
common
├── constants           // 枚举与默认值
├── dto                 // 入参定义
├── vo                  // 返回值定义
├── errors              // 业务错误码与预定义错误
└── utils               // 通用小工具（泛型去重、SHA-256 指纹）
```

评测数据保存在 MySQL 的 `eval_*` 表：已发布题库、草稿、任务、逐题结果、基线与对比报告都在库里；
ES 只承载检索切片，不存评测数据。任务保留天数由 `evaluation.retention_days` 控制，
超期任务每天自动清理，基线任务不会被清掉。

**领域实体直接就是持久化模型**：`domain/entity/` 下的 `knowledge.go` /
`knowledge_doc.go` 上挂着 `gorm:"column:..."` 和 `TableName()`，没有再单设 `po` / `mapper` 两层。
`chunk.go` / `collection.go` 不带 tag：它们不是表。

两张表里**只有 `knowledge_doc` 的 DDL 归本服务**（`deploy/schema.sql`）；
`knowledge` 由外部系统预置（§1.2），列定义归建库方——实体上的 tag 只是**读**它时的映射，
改 tag 不会去动那张表。本服务对它只写三列：`doc_count` / `chunk_count` / `update_ts`。
所以只有 `knowledge_doc` 的 tag 与 `deploy/schema.sql` 是"同一份列定义的两个写法、
改一边就要改另一边"；不一致都不会报错，只会静默查不到数据。

两张表的关系是**一个来源 URL 建一个库，一个库下挂多篇文档**：库级的 `source_url` 记在
`knowledge` 上，文档级的内容与标题记在 `knowledge_doc` 上（`title` / `content`），
所以同一个 URL 拆出来的多篇不必各自抄一遍来源。

依赖注入使用 [Uber FX](https://github.com/uber-go/fx)。

### 依赖方向红线

| 层 | 禁止 import |
|----|-------------|
| `domain/` | `gin`、`gorm`、`redis`、`net/http`、`infrastructure/...`、`application/...` |
| `application/` | `gin`、`infrastructure/driver/...`、`infrastructure/controller` |
| `common/` | `domain/...`、`application/...`、`infrastructure/...` |

这三条靠 code review 守，仓库里没有自动化检查（早期有过一个架构 lint 脚本，已删除）。

表中的 `...` 包含该层根包本身：例如 `application/` 的红线既匹配 `infrastructure/driver`、
`infrastructure/controller` 这两个层根包，也匹配它们的子包；`domain/`、`common/` 的红线同样
匹配 `infrastructure`、`application` 这些不带子路径的层根包。

## 快速开始

前置：Go 1.26+、MySQL、Elasticsearch（需装 analysis-ik 插件，见 `deploy/README.md`）。
Redis 可选（`config.json` 的 `redis.addr` 留空即不启用）。

**库表和索引要先建好**——服务自己不做这件事（设计文档 §9.4）：

```bash
make init-db     # MySQL 建表
make es-index    # ES 建索引
```

```bash
# 下载依赖
go mod download

# 首次运行先复制示例配置，再按本机环境调整 MySQL 等连接信息
cp config.example.json config.json
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

索引没建的话，服务**也不检测**——启动期不查、运行期也不兜底，理由见设计文档 §9.4。
后果由 ES 自行呈现：或者报 `index_not_found`，或者更糟——被 `action.auto_create_index`
静默建出一个动态 mapping 的索引。索引是部署前的外部前提（`make es-index`），
没建好不该让整个服务起不来。

`config.json` 的 `snowflake_node_id` 是每个实例必须唯一的 0–1023 整数。该键**缺省时取值为 0**，
而 0 是合法节点号，因此多实例共用一份「没写这个键」的配置会生成重复 ID——部署多副本时务必显式配置。

### 启用检索评测（可选）

评测默认关闭，不影响检索。它需要一个独立的 Python 评分服务：

```bash
cd deploy/ragas
cp .env.example .env        # 填 EVAL_SERVICE_TOKEN（至少 24 位随机串）
docker compose up -d --build
```

再在 `config.json` 的 `evaluation` 块打开 `enabled`，填写裁判模型（也可以在配置页「检索评测 / MiniMax」分组里改），
启动 FastRAG 时设置同一个 token：

```bash
export FASTRAG_EVAL_TOKEN='与 deploy/ragas/.env 的 EVAL_SERVICE_TOKEN 一致'
go run ./cmd/server
```

开启评测但 `FASTRAG_EVAL_TOKEN` 少于 24 位时，服务拒绝启动。评分服务只绑定回环地址；
Go 与它不在同一台机器时用 VPN/隧道连接，不要暴露到公网。详细说明见
[检索评测](evaluation/README.md)。

## 常用命令

```bash
make build        # 构建 Vue + Go 到 bin/server（构建机需要 Node.js）
make backend-build # 仅构建 Go，使用仓库中已有的前端产物
make frontend-dev # 启动 Vue 开发服务（首次先 npm --prefix frontend ci）
make run          # 直接运行服务
make debug        # dlv headless 调试，监听 :2345（等 Delve 接入）
make test         # 运行所有测试（含 race 检测和覆盖率）
make test-pkg     # 运行指定包测试，如 make test-pkg PKG=infrastructure/persistence
make test-single  # 运行单个测试，如 make test-single NAME=TestKNNClauses PKG=infrastructure/persistence
make init-db      # 建 MySQL 库表（可重复执行）
make es-index     # 建 ES 索引（已存在则只校验 mapping）
make tidy         # 整理 go.mod
make clean        # 清理构建产物
```

## 配置文件

配置只有一份 **`config.json`**，从 `config.example.json` 复制而来：

```bash
cp config.example.json config.json   # 再按本机环境改 MySQL / ES 连接信息
```

它装着本机连接信息与凭据（数据库密码、上游 `api_key`、评测裁判 API Key），已在 `.gitignore` 里，**不入库**；
`config.example.json` 与它结构完全一致，只是把密码和 `api_key` 留空，可以安全进仓库。

两点要知道：

- `Load()` 读的是**相对路径** `config.json`，所以服务必须**从仓库根目录启动**。
- 启动时跑一次 `validate()`（设计文档 §7）：`embedding` 配错、或 `rerank.enabled=true`
  却没有可用模型，都会在**启动期**直接报错退出，不会拖到第一次检索才暴露。

顶层各块：

| 块 | 作用 | 关键点 |
|----|------|--------|
| `app` / `debug` | 应用名 / 调试模式 | `debug=false` 把 Gin 切到 `ReleaseMode`；`app` 用作 Redis 连接名 |
| `http` | 监听地址与读写超时（秒） | 默认 `:8080`；`host` 留空 = 监听 `0.0.0.0` |
| `mysql` | 业务库连接（文档行、KB 计数） | 库表要先 `make init-db` |
| `redis` | 可选 | `addr` 为空即不启用 |
| `snowflake_node_id` | 雪花 ID 节点号 `[0,1023]` | 多实例必须各不相同；缺省 = 0，会生成重复 ID |
| `es` | ES 地址与索引名 | 只含「怎么连」；索引要先 `make es-index`。建索引的 dim / 分词器等**不在这里**，在 `deploy/create-es-index.sh` |
| `embedding` / `rerank` | 模型服务商注册表 | 结构见下一节「模型配置」 |
| `search` | 混合检索的默认参数 | `dense_weight` / `rank_constant` 等，可被单次请求覆盖 |
| `parser` | 文档解析后端注册表 | `POST /api/v1/docs/parse` 用；结构与 `embedding` 同类，见下 |
| `evaluation` | 离线检索评测 | 默认关闭；开启需 `FASTRAG_EVAL_TOKEN` 与独立评分服务 |
| `security` | `trust_request_tenant` 启动闸门 | 见「租户与鉴权」 |

`security` 不置 `trust_request_tenant=true`，服务**拒绝启动**（§6.1）；
`search` 各键与 `dense_weight` 的取值语义见「API 示例」里的检索说明。

### 评测配置

`evaluation` 块控制题库、任务和评分服务。常用字段：

| 键 | 说明 |
|---|---|
| `enabled` | 总开关。关闭时 `/api/admin/eval/*` 返回 `10601`，检索不受影响 |
| `retention_days` | 任务保留天数，默认 30；超期任务每天清理，基线任务保留 |
| `judge` | 裁判模型（`base_url` / `model` / `api_key`）。Go 随评分请求下发，不写入任务快照 |
| `scorer_base_url` | Python 评分服务地址，默认 `http://127.0.0.1:18080` |
| `request_timeout_seconds` / `run_timeout_seconds` | 单次评分请求 / 整个任务超时，默认 240 / 3600 秒 |
| `auto_generate` / `auto_sample_count` | 导入文档后是否自动准备候选题、每篇抽多少 |

这些字段也可以在配置页修改。裁判模型的地址、名称和 API Key 下次评分时生效；
保留天数、超时等其余字段需要人工重启。

## 模型配置

`embedding` 和 `rerank` 各是一组服务商，分**外层**与 `models` 两层：
共用的字段放外层大家一起持有，只有逐家不同的才放进 `models`。所以加一家
通常只要 4、5 行：

```json
"embedding": {
  "default": "bge-m3",
  "dim": 1024,
  "batch_size": 16,
  "timeout_ms": 60000,
  "models": {
    "bge-m3": { "url": "http://127.0.0.1:11434/v1/embeddings" },
    "qwen": {
      "protocol": "dashscope",
      "url": "https://…/api/v1/services/embeddings/text-embedding/text-embedding",
      "api_key": "sk-…",
      "model": "qwen3.7-text-embedding",
      "batch_size": 10
    }
  }
}
```

几点必须知道的：

- **`dim` 和 `top_n` 只在外层**：一个是索引的性质，一个是服务级的策略，
  任何一家单独改了都不成立。`query_prefix` 反过来只在 entry 里。
- **`protocol` 取 `openai`（默认）或 `dashscope`**，指的是上游原生协议，
  不是两套实现——分批、并发、重试、排序都是共用的。
- **换 embedding 服务商不会让旧数据变得不可用，也不会保证新旧向量在同一个空间里。**
  维度不同会被 ES 直接拦下（会响）；维度相同但向量空间不同则是**静默**的噪声排序，
  接口不报错、日志不报警。要换就重建索引重导，本服务不替你保持自洽。
- **换 rerank 服务商随时安全**：它只在查询期调用，不落库。

### 解析后端

`parser` 与上面两块同构，只是外层没有共用字段——`timeout_ms` 逐家不同（自部署的
Docling 能等，腾讯要卡小）：

```json
"parser": {
  "default": "docling",
  "backends": {
    "docling": {
      "type": "docling",
      "base_url": "http://127.0.0.1:5001",
      "timeout_ms": 110000,
      "max_retries": 1,
      "retry_wait_ms": 1000,
      "formats": ["pdf","docx","doc","pptx","xlsx","html","htm","md","txt","png","jpg","jpeg","tiff"],
      "options": {
        "do_ocr": true,
        "table_mode": "accurate",
        "image_export_mode": "placeholder"
      }
    },
    "tencent": {
      "type": "tencent",
      "secret_id": "",
      "secret_key": "",
      "region": "ap-guangzhou",
      "timeout_ms": 110000,
      "max_retries": 1,
      "retry_wait_ms": 1000,
      "formats": ["pdf","docx","doc","pptx","xlsx","md","txt","png","jpg","jpeg"]
    }
  }
}
```

- **`type` 取 `docling` 或 `tencent`**。自部署 Docling 用
  `docker compose --profile docling up -d` 起（镜像约 14GB，所以默认不启动）；
  腾讯那家要填 `secret_id` / `secret_key`——**把它设为 `default` 时缺密钥会在启动期报错**
  （其余非默认后端只做字段校验，不查密钥）。
- **`default` 决定这次用哪家**：接口不带选后端的参数，改它再重启即换后端。
- **`timeout_ms` 必须小于 `http.write_timeout`**（默认 120s），启动期会校验。
  它是这次解析的**总预算**，不是「每次尝试各算一份」——超时重试不会撞穿写超时。
- **`formats` 是该后端支持的扩展名**，请求的扩展名不在里面就直接返回参数错误，
  不白跑一次解析。留空（或不写）表示「不声明、一律放行」。扩展名只认「末段最后一个点
  之后全是字母」的后缀，所以 `https://arxiv.org/pdf/1706.03762v7` 这类省略扩展名的地址
  不会被误杀——它会被交给后端，`doc_name` 取末段 `1706.03762v7`。
- **解析产物恒是 markdown**，之后与 `format: "markdown"` 走同一条切片 → 向量化路径。
- **能提前判的纯参数错误在解析之前报完**：坏 scheme、不支持的格式、非法的切片参数都不会
  白花一次解析。但模型名不在此列——它在解析之后的落库步里才查（`ingestDoc`），
  拼错模型名要先付一次解析的费用。

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
| `10201`–`10206` | 文档：切片为空 / 切片过长 / 导入失败 / 删除失败 / embedding 异常 / 向量库异常 |
| `10301`–`10302` | 检索失败 / rerank 服务失败 |
| `10401` | 解析失败：解析上游超时 / 返回非 200 / 返回体不可解析 |
| `10501` | 文档不存在：`doc_id` 查无此行，**或**该行不属于这个 `tenant`（两者同一个码） |
| `10601`–`10602` | 评测未开启 / 评测操作失败 |

`10101`–`10106` 里，知识库的 CRUD 错误码已基本无主：CRUD 全在外部系统里（§1.2），
本服务不提供对应接口。**唯一还在用的是 `10101`（库不存在）**——`FindByNo` 找不到库、
或 tenant 不匹配时都会返回它（`knowledge_repo.go`）。其余 `10102`–`10106` 只剩兼容
意义，其中 `10106`（检索未实现）尤其名不副实——检索早已落地并改用 `10301`，别再用；
`10102`–`10106` 现在只被 `common/errors/errors_test.go` 引用。
`10501` 沿用同一条原则：详情 / 切片接口拿到的 `doc_id` 不属于该租户时，返回的与「不存在」
一字不差——分开就等于送出一个可枚举的越权探测接口（`docview` 服务）。
`10002`（未登录）/`10003`（无权限）同理：本服务不做鉴权（见下文「租户与鉴权」）。

错误定义集中在 `common/errors/errors.go`。新增错误码时在此追加，不要在业务代码里随手 `errors.New`。

错误分为两类：

- `BizError`（预期内，如「切片结果为空」「dense_weight 超出范围」）
- `SysError`（非预期，如「embedding 服务异常」「数据库连接失败」）

## 租户与鉴权

**本服务不做鉴权**：租户 `tenant` 就是请求体里的一个普通字符串参数，不做任何校验。

它同时是单索引多租户的隔离键（设计文档 D2、§9）：ES 里同一份索引装着所有租户的切片，
靠 `tenant` 这个 `keyword` 字段做 term 过滤（**不能是 text**——用分词匹配的话
`demo` 会命中 `demo-other` 的文档，那是越权而不是召回变小），MySQL 侧每个查询也都带
`tenant` 条件。

这等于把「调用方必须自己保证 `tenant` 是真的」当前提：网关、内部服务间调用都行，
但一旦把 `tenant` 直接暴露给终端用户，改一个字段就能读到别人的库。

`security.trust_request_tenant` 就是把这条前提变成一次显式承认——不置为 `true`
服务直接拒绝启动（`infrastructure/init.go` 的 `checkTrustRequestTenant`，§6.1）。
这道闸门防的是「把服务顺手摆到公网上」这种改动，代码本身不会因此报任何错。

## 知识库工作台

生产页面仍在服务根路径：<http://localhost:8080/>。Vue 源码位于 `frontend/`，构建资源位于
`infrastructure/controller/http/web/static/`，随 Go 程序发布。页面不依赖在线字体或 CDN。

- **文档管理**：服务端分页，查看原文、检索切片，确认删除。
- **导入文档**：Markdown/TXT 文件上传、粘贴正文、URL 解析导入，高级参数默认折叠。
- **检索测试**：混合检索、模型精排、模型与权重设置、结果分页、原文溯源。
- **检索评测**：新建题库（抽样、后台出题、人工核对、发布）、运行评估、指标与失败详情、失败项重评、基线回归对比、单题诊断。
- **统一工作区**：设置租户与已有知识库编号，保存在当前浏览器，切换后清空旧结果。
- **产品首页**：大标题与功能展示，顶部导航直达各项工具，支持移动端。
- **配置管理**：分组或完整 JSON 编辑、模型新增/删除、凭据脱敏、版本冲突检测；保存后人工重启生效。
- **运行日志**：应用日志按级别、关键词和时间查询，展示错误详情，支持自动刷新。

配置、日志和评测管理默认关闭；启动前设置至少 24 字符的 `FASTRAG_ADMIN_TOKEN`，在页面输入同一令牌后访问。
令牌仅保存于页面内存。日志按北京时间自然月保存在 `logs/service-YYYY-MM.jsonl`，仅保留当月并在跨月后自动清理，页面查询最近 2000 条结构化应用日志，不包含 Docker、数据库或任意控制台输出。
评测还要求 `FASTRAG_EVAL_TOKEN` 与 Python 评分服务的 `EVAL_SERVICE_TOKEN` 一致。
配置保存及管理接口的完整说明见 [管理功能说明](frontend/README.md#首页配置与运行日志)。

检索支持 query 直达链接，例如 <http://localhost:8080/?q=呼吸衰竭的病因和临床表现>。
检索分页只浏览本次返回的前 `limit`（≤100）条，文档列表分页则由服务端提供。
工作区初始值为 `demo` / `demo-kb`；填写编号不会创建知识库，也不代表身份授权。

```sh
make build         # 完整构建前端与 Go；构建机需要 Node.js 20.19+ 或 22.12+
make frontend-dev  # 开发页面 http://127.0.0.1:5173，API 默认代理到 :8080
make backend-build # 仅构建 Go，使用仓库中已有的前端产物
```

前端修改后需重新构建并重启 Go 服务。开发、部署和测试说明见 [frontend/README.md](frontend/README.md)。

## 检索评测

评测只回答一个问题：**给定问题和参考答案，检索有没有把需要的原文片段找出来、排对**。
它不评最终回答的生成质量，也不需要把生成模型接进评测链路。

### 题库

- 已发布题库不可变，一个版本一行，包含 manifest、profiles 和 cases 三份快照。
- 两类来源：`md-original` 使用原始 Markdown 证据（没有 ES chunk ID）；新题库从当前知识库抽样（有 chunk ID）。
- 新建题库按**每篇 md 最多 50 段、总数最多 100 段**按篇均衡抽样，只抽原文，不调用模型。
- 「后台生成」逐题调用模型生成问题和参考答案，**关闭页面也会继续**；进度自动刷新，可以暂停/继续；单题失败最多重试 3 次，坏题跳过，不拖停整批。
- 只有人工标记通过的题会进入题库；至少 3 题才能发布。发布时核验原文哈希，版本不可覆盖。

### 运行与指标

选题库、实验名和假设后，按题库里固化的检索 profile（返回条数、召回数、语义权重、精排开关）
逐题调用进程内检索服务（与线上同一条检索链路）。任务单飞，可取消、有任务超时；元数据写入 `eval_run`，
逐题结果写入 `eval_run_row`。空检索和边界题按策略判定，不调用模型；检索失败和评分失败分开统计。

| 指标 | 含义 | 计算方式 |
|---|---|---|
| 上下文相关性 | 返回片段与问题是否相关 | 模型判断 |
| 证据排序质量 | 片段中有多少真正支撑参考答案 | 有参考 chunk ID 时走确定性 ID 指标；否则模型逐片段并发判断 |
| 必要信息覆盖 | 参考答案的信息有多少被检索覆盖 | 有参考 chunk ID 时走确定性 ID 指标；否则模型判断 |

不调用模型的诊断还包括：Recall@5、Precision@5、MRR@5、Success@5、nDCG@5、边界违规、
检索失败率、空结果率、重复率、平均返回条数/字符数、检索 P50/P95、评分 P95、评分覆盖率。

评分失败会标为该项失败，页面不会把它显示成 0 分；可以只重评失败项。
评分按批调用 Ragas `evaluate()`（每批 3 题），裁判输出上限 8192 tokens，单指标超时 120 秒。

### 验收与单题诊断

- 完整成功的已复核任务可以设为基线；
- 新任务与基线按显式门槛比较：覆盖下降、排序下降、延迟倍率；
- 题集、检索配置或裁判版本变化时判 `insufficient`，不硬比较；
- 报告保留逐题证据、退步题和关键题回归。

单题诊断不需要建题库：对单个问题做即时检索，查看实际生效配置、各阶段候选与耗时、
关键词/向量排名、RRF 融合分、精排分和片段原文。它用于定位「为什么这题没搜到」，不是统计评测。

### 存储与接口

评测数据全部在 MySQL：`eval_dataset`、`eval_draft`、`eval_run`、`eval_run_row`、
`eval_baseline`、`eval_comparison`。DDL 在 `deploy/schema.sql`，先 `make init-db`。
`retention_days` 到期后每天清理非基线任务，逐题结果和验收报告随任务级联删除。

评测管理接口都在 `/api/admin/eval/*`，需要 `FASTRAG_ADMIN_TOKEN`；
Go 调用 Python 评分服务使用 `FASTRAG_EVAL_TOKEN`。

| 接口 | 用途 |
|---|---|
| `GET /api/admin/eval/datasets` | 列出已发布题库 |
| `GET/POST /api/admin/eval/runs` | 列出 / 创建评估任务 |
| `GET /api/admin/eval/runs/:id` | 任务详情 |
| `GET /api/admin/eval/runs/:id/items` | 分页查看逐题结果 |
| `POST /api/admin/eval/runs/:id/cancel` | 取消任务 |
| `POST /api/admin/eval/runs/:id/rescore` | 仅重评失败指标 |
| `DELETE /api/admin/eval/runs/:id` | 删除任务记录 |
| `POST /api/admin/eval/runs/:id/baseline` | 设为基线 |
| `POST /api/admin/eval/runs/:id/compare` | 与基线对比 |
| `POST /api/admin/eval/diagnose` | 单题诊断 |
| `GET/POST /api/admin/eval/drafts` | 列出 / 新建题库草稿 |
| `POST /api/admin/eval/drafts/:id/generate` | 生成单题 |
| `POST /api/admin/eval/drafts/:id/generate-all` | 后台生成全部未完成题 |
| `POST /api/admin/eval/drafts/:id/generate-stop` | 暂停后台生成 |
| `PUT /api/admin/eval/drafts/:id` | 保存人工复核 |
| `DELETE /api/admin/eval/drafts/:id` | 删除草稿 |
| `POST /api/admin/eval/drafts/:id/publish` | 发布为不可变题库 |

完整的数据格式、评测口径和部署说明见 [evaluation/README.md](evaluation/README.md)。

## API 示例

业务路由按能力分五件事（`infrastructure/controller/http/register.go`）：写文档（`/api/v1/docs`、
`/api/v1/docs/batch`）、解析导入（`/api/v1/docs/parse`）、删文档（`/api/v1/docs/delete`）、
检索（`/api/v1/search`）、看文档（三条只读 GET）。
知识库的 CRUD 不在本服务范围内（§1.2）——`knowledge` 表由外部系统维护，本服务只读它。

```bash
# 导入文档。format 取 markdown / text / chunks；
# refresh=true 表示写完立刻刷索引、返回即可搜（默认 false，按 30s 周期）
# model 指定这篇的向量用哪家算（config.embedding.models 的 key），省略则用 default
curl -X POST http://localhost:8080/api/v1/docs \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"demo","kb_no":"demo-kb","doc_name":"产品手册.md","format":"text",
       "model":"bge-m3","content":"……正文……","refresh":true}'

# 批量导入：请求体是数组，最多 100 篇。恒返回 200 + 逐条明细，
# 单条失败不改状态码——否则调用方分不清「全挂」和「挂 3 条」，只能整批重试
curl -X POST http://localhost:8080/api/v1/docs/batch \
  -H 'Content-Type: application/json' \
  -d '[{"tenant":"demo","kb_no":"demo-kb","doc_name":"a.md","format":"text","content":"…"},
       {"tenant":"demo","kb_no":"demo-kb","doc_name":"b.md","format":"text","content":"…"}]'

# 解析导入：给一个可公网下载的 URL，服务端解析成 markdown 后入库
# 用哪家后端由 config.parser.default 决定（改配置重启即换）；
# doc_name 省略时取 URL 末段（去查询串、百分号解码）
curl -sS -X POST http://127.0.0.1:8080/api/v1/docs/parse -H 'Content-Type: application/json' -d '{
  "tenant": "demo", "kb_no": "demo-kb",
  "url": "https://example.com/xxx.pdf", "refresh": true
}'

# 删除文档（POST 而非 DELETE：DELETE 带 body 合法，但网关和客户端库常把它丢掉）
curl -X POST http://localhost:8080/api/v1/docs/delete \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"demo","kb_no":"demo-kb","doc_name":"产品手册.md"}'

# 检索。dense_weight 省略则用配置默认值；<0.01 只走 BM25，>0.99 只走 kNN
# rerank_switch=true 且 config.rerank.enabled=true 时会调用精排；
# retrieve_count 限制融合后送入精排的候选数量（默认使用配置或 limit*2，上限 200，至少 limit）
# 两路召回量独立读取 search.bm25_top / knn_top，近似搜索候选池读取 num_candidates
# embed_model / rerank_model 分别选两路用哪家，省略则各用各的 default；
# 名字不存在返回 code=10001 并列出可用的名字，不会悄悄回落到默认那家
#
# min_score 是**最终分**的阈值，>0 才启用，筛完可能不足 limit 条；
# 注意量纲随 rerank_switch 变：开精排时是模型分（0~1），关时是 RRF 融合分（0.01 量级），
# 精排失败或未产生模型分时保留融合排序，跳过精排阈值，并返回 meta.warnings
# fusion_min_score 独立过滤精排前的融合分；diagnostics=true 返回诊断信息
#
# 早先的 `biz_tags` 请求参数已移除：带上它不会再报错，而是被**静默忽略**——
# 别以为它还在筛库，请求会照常按 tenant 检索全部 kb_nos
curl -X POST http://localhost:8080/api/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"demo","kb_nos":["demo-kb"],"query":"如何配置",
       "limit":5,"dense_weight":0.5,"min_score":0,"rerank_switch":false,
       "embed_model":"bge-m3","rerank_model":"qwen3-rerank"}'

# ── 只读三条：GET + query 参数（本仓唯一走 query string 的业务接口）──────
# 为什么是 GET 而不是 POST：这三条没有副作用、参数都是标量，用 query 才能直接
# 贴进浏览器地址栏排查。写侧那四条不改，仍是 POST + JSON。

# 列某库下的文档（分页）。**刻意不返回正文**——列表是导航用的，
# 一页几十篇背正文会让响应体没有上界；也不返回 source_url（库级的，每篇都一样）
curl 'http://localhost:8080/api/v1/docs?tenant=demo&kb_no=demo-kb&page=1&page_size=10'

# 单篇详情：整篇原文 + 库级来源 URL。
# doc_id 查无此行、或不属于该 tenant，都返回 10501
curl 'http://localhost:8080/api/v1/docs/detail?tenant=demo&doc_id=12'

# 枚举某篇的切片。limit 默认 200、封顶 500（超了**钳到** 500，不像 page_size 那样报错）
# total=0 是**合法状态**：ES 可见性有两档延迟，刚导入的文档原文立刻能看、
# 切片要等下一轮刷新（最长约 30s）
curl 'http://localhost:8080/api/v1/docs/chunks?tenant=demo&doc_id=12&limit=200&offset=0'

# 探针
curl http://localhost:8080/health
curl http://localhost:8080/ready

# 知识库工作台（挂在根路径，不在 /api 下）
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
5. **建表**：DDL 写进 `deploy/schema.sql`，然后 `make init-db`（服务不建表）
6. **路由**：在 `infrastructure/controller/http/register.go` 的 `api` Group 下挂载

定包的原则只有一条：**一个类型该住哪，看它在哪个包的函数签名里被需要**。
谁签名要它，它就得待在谁够得着的地方；没人跨包要它，就别给它单开一个包或目录。
本仓那几个「看着像模块、其实是层」的目录（`entity/`、`repository/`、`persistence/`、
`driver/`）都是这么收拢来的——它们下面不再按业务分子目录，只有业务真的需要
独立的一层时（比如 `application/service/` 下的 `ingest`、`search`）才分。
