# 多服务商模型配置（model registry）设计

状态：待评审
范围：`infrastructure/config`、`domain/interfaces`、`infrastructure/serviceimpl`、
`application/service/{ingest,search}`、`common/dto`、`config.example.json`

## 1. 要解决的问题

embedding 和 rerank 现在各是**一份**配置：`embedding.base_url` / `embedding.model`、
`rerank.base_url` / `rerank.model`，配哪家就只能是哪家。

但同一家服务商往往两样都提供，而用户会**按优惠政策在几家之间来回切**。切换一次就要改
配置、重启服务，而且一旦两个库用了不同家的向量，也没有任何东西能把这件事表达出来。

目标：

- 配置里可以同时存在**多组** embedding、多组 rerank；
- 调用方在**导入**和**检索**时各自指定用哪一组（`"model": "qwen3-rerank"`）；
- 不指定时走配置里的默认；
- 加一家服务商 = 只加一段 JSON，不改代码、不加分支。

## 2. 一个必须先拆开的事实

rerank 和 embedding 看着对称，实际不是一回事。

| | rerank | embedding |
|---|---|---|
| 什么时候用 | 只在检索时 | 导入时算切片向量、检索时算查询向量 |
| 落库吗 | 不落，纯查询期调用 | 落，它就是库里的数据 |
| 逐请求换模型 | **安全**，换个模型只是这次的排序变好变坏 | **不安全**，等于换了查询向量所在的坐标系 |

embedding 换家有两种翻车方式，一种会响，一种不会：

- **维度不同** → 会响。写入时 ES 的 bulk 逐条报 `dense_vector` 维度错误（`Save` 已经在
  查 `items[].status`），查询时 ES 直接 400。
- **维度相同、向量空间不同**（bge-m3 1024 → qwen 也配 1024）→ **完全静默**。余弦相似度
  照样算得出来，只是排出来的顺序是噪声，接口不报错、日志不报警。

**已决定：第二种不管。** 不落痕、不校验、`knowledge_doc` 不加列，旧数据不做任何特殊处理。
这是明确接受的代价，记在第 8 节。

因此本方案里：

- **rerank 的 `model` 是真正的"这次用哪家"**；
- **embedding 的 `model` 是"这批数据是谁生成的"**，接口提供它、但不负责替你保持自洽。

## 3. 配置形状

`embedding` 和 `rerank` 都改成 `{default, models}` 结构，`models` 是 map。
下面是完整的 config.json，可以直接抄成 `config.json` 用：

```jsonc
{
  "app": "fastrag",
  "debug": true,
  "http": { "host": "", "port": "8080", "read_timeout": 120, "write_timeout": 120 },
  "mysql": {
    "host": "127.0.0.1", "port": 3306, "database": "fastrag",
    "user": "root", "password": "123456",
    "max_open": 100, "max_idle": 10, "conn_lifetime": 3600,
    "conn_timeout": 3, "log_level": 3, "slow_threshold": 500
  },
  "redis": { "addr": [], "password": "", "db": 0, "pool_size": 5 },
  "snowflake_node_id": 1,
  "es": {
    "addrs": ["http://127.0.0.1:9200"],
    "index": "fastrag", "username": "", "password": ""
  },

  // ────────────────── 这一块从「一家」变成「一组」 ──────────────────
  "embedding": {
    // 请求里没带 model 时用哪个。必须命中下面某个 key，否则启动报错
    "default": "bge-m3",

    // key = 你给这家起的名（调用方就用这个名字），随便起，短一点好写
    "models": {

      // ── 本机 Ollama，OpenAI 兼容，不需要 key ──
      "bge-m3": {
        "protocol": "openai",                              // 可省略，默认就是 openai
        "url": "http://127.0.0.1:11434/v1/embeddings",     // 完整地址，后缀也要写
        "api_key": "",                                     // Ollama 不用
        "model": "",                                       // 留空 = 用 key，即 "bge-m3"
        "dim": 1024,                                       // 可省略；写上有错早报
        "batch_size": 16,
        "timeout_ms": 60000,
        "query_prefix": ""                                 // bge-m3 不加前缀
      },

      // ── 通义，原生协议 ──
      "qwen": {
        "protocol": "dashscope",
        "url": "https://dashscope.aliyuncs.com/api/v1/services/embeddings/text-embedding/text-embedding",
        "api_key": "sk-你的key",
        "model": "text-embedding-v4",                      // 上游真名，和 key 不一样没关系
        "dim": 1024,
        "batch_size": 10,                                  // 通义单次上限 10 条
        "timeout_ms": 30000,
        "parameters": { "dimension": 1024 }                // 原样塞进请求的 parameters 块
      }

      // 再加一家？在这里再加一段就行，代码一个字都不用改
    }
  },

  // ────────────────── 同样从「一家」变成「一组」 ──────────────────
  "rerank": {
    "enabled": true,              // false = 整个实例不重排，下面 models 可以为空
    "default": "qwen3-rerank",

    "models": {
      "bge-reranker": {
        "protocol": "openai",
        "url": "https://api.siliconflow.cn/v1/rerank",
        "api_key": "sk-你的key",
        "model": "BAAI/bge-reranker-v2-m3",
        "batch_size": 32,
        "concurrency": 4,
        "timeout_ms": 10000,
        "top_n": 0                // 0 = 全部返回，最后按 limit 截断；别改成 5
      },
      "qwen3-rerank": {
        "protocol": "dashscope",
        "url": "https://ws-s5iiibr6uezast8b.cn-beijing.maas.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank",
        "api_key": "sk-你的key",
        "model": "qwen3.7-text-rerank",
        "batch_size": 32,
        "concurrency": 4,
        "timeout_ms": 10000,
        "top_n": 0
      }
    }
  },

  "search": {
    "bm25_top": 50, "knn_top": 50, "num_candidates": 200,
    "rank_constant": 60, "dense_weight": 0.5, "default_retrieve_count": 0
  },
  "security": { "trust_request_account": true }
}
```

调用方怎么用（导入和检索各指定一次，不指定就走上面的 `default`）：

```bash
# 导入：这篇文档的向量用 bge-m3 算
curl -X POST http://localhost:8080/api/v1/docs \
  -H 'Content-Type: application/json' \
  -d '{"account":"demo","kb_no":"demo-kb","doc_name":"a.md","format":"text",
       "model":"bge-m3","content":"……正文……"}'

# 检索：查询向量用 bge-m3 算，精排用 qwen3-rerank
curl -X POST http://localhost:8080/api/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"account":"demo","kb_nos":["demo-kb"],"query":"如何配置",
       "embed_model":"bge-m3","rerank_model":"qwen3-rerank","rerank_switch":true}'
```

`"model":"qwen-not-exist"` 这种拼错的名字 → 返回 `code=10001`，消息里列出可用的名字，
**不会**悄悄用默认那家。

三条约定：

1. **`models` 的 key 是别名，`model` 是发给上游的真名**，`model` 留空时取 key。
   这样上游叫 `BAAI/bge-reranker-v2-m3` 也不影响你在请求里写 `"model": "bge"`。
   请求里传的一律是**别名**。
2. **`url` 写完整端点**，实现不再拼 `/embeddings`、`/rerank` 后缀。
   DashScope 的重排路径是 `/api/v1/services/rerank/text-rerank/text-rerank`，
   压根不在任何 base 之下；保留"base + 固定后缀"只会逼出一个 `path_override` 之类的例外字段。
3. **`default` 是请求没带 model 时的兜底**。它必须命中 `models` 里的一个 key，否则启动报错。
   `enabled=false` 时 `rerank.models` 允许为空。

Go 侧结构：

```go
type Config struct {
    // ...
    Embedding EmbeddingConfig `json:"embedding"`
    Rerank    RerankConfig    `json:"rerank"`
}

type EmbeddingConfig struct {
    Default string                    `json:"default"`
    Models  map[string]EmbeddingEntry `json:"models"`
}

type EmbeddingEntry struct {
    Protocol    string         `json:"protocol"`     // openai（默认）| dashscope
    URL         string         `json:"url"`          // 完整端点
    APIKey      string         `json:"api_key"`
    Model       string         `json:"model"`        // 留空 = 用 map 的 key
    Dim         int            `json:"dim"`
    BatchSize   int            `json:"batch_size"`
    TimeoutMS   int            `json:"timeout_ms"`
    QueryPrefix string         `json:"query_prefix"`
    Parameters  map[string]any `json:"parameters"`   // 仅 dashscope：并入 parameters 块
}

type RerankConfig struct {
    Enabled bool                 `json:"enabled"`
    Default string               `json:"default"`
    Models  map[string]RerankEntry `json:"models"`
}

type RerankEntry struct {
    Protocol    string         `json:"protocol"`
    URL         string         `json:"url"`
    APIKey      string         `json:"api_key"`
    Model       string         `json:"model"`
    TimeoutMS   int            `json:"timeout_ms"`
    BatchSize   int            `json:"batch_size"`
    Concurrency int            `json:"concurrency"`
    TopN        int            `json:"top_n"`
    Parameters  map[string]any `json:"parameters"`
}
```

各 entry 的字段就是原来那份扁平配置的字段，只是从"整个服务一份"变成"每家一份"。
`NewEmbedding(conf)` → `NewEmbedding(entry)`、`NewRerankImpl(conf)` → `NewRerankImpl(entry)`，
两个实现体只改构造签名和入口，内部逻辑不动。

## 4. 协议：openai 与 dashscope

`protocol` 字段两处都要，因为请求体**和**响应体都不一样。

### 4.1 embedding

| | openai（现状） | dashscope |
|---|---|---|
| body | `{model, input: ["..."]}` | `{model, input: {texts: [...]}, parameters: {...}}` |
| 响应 | `data[].{index, embedding}` | `output.embeddings[].{text_index, embedding}` |
| query/doc 区分 | 靠 `query_prefix` 字符串前缀 | **协议自带**：`parameters.text_type` = `query` / `document` |

`text_type` 由实现按调用的方法决定——`EmbedDocs` → `document`，`EmbedQuery` → `query`；
配置里的 `parameters` 先并入、随后被实现覆写，避免手滑把 query 当 document 编码而不报错。

现有端口把 `EmbedDocs` / `EmbedQuery` 分成两个方法（原本是为了非对称前缀），
到 dashscope 这里正好对上协议要求，不需要额外改造。

### 4.2 rerank

| | openai（现状） | dashscope |
|---|---|---|
| body | `{model, query, documents, return_documents}` | `{model, input: {query, documents}, parameters: {top_n, return_documents}}` |
| 响应 | `results[].{index, relevance_score}` | `output.results[].{index, relevance_score}` |

⚠️ **dashscope 的响应形状尚未实测确认。** 本机没有 key、官方文档站抓不到，
所以实现先同时认 `results` 和 `output.results` 两个位置。第一次真调用时把原始响应贴出来，
再按实际形状收窄——不要照着猜的写死。

### 4.3 分批时 `top_n` 必须等于本批长度

这是移植 dashscope 最容易踩的坑，单独列出来。

DashScope 的 `parameters.top_n` 是"只返回前 N 条"，而我们**需要每一批的完整分数**才能
合并排序。示例里写的是 `top_n: 5`,照抄进分批逻辑，每批就只有 5 条候选拿到分数，
其余全部落回 0 分，批与批之间的分数不再可比，合并后顺序是乱的——而且不报错。

规则：**发给上游的 `top_n` 恒等于本批 documents 数量**，业务上的 topN 永远只在最后
自己截断。openai 那一路不发 `top_n`，天然没这个问题；dashscope 那一路必须显式写死。

## 5. 端口：两个注册表

新增两个端口，各放在自己那一份 `domain/interfaces/` 文件里，不新开文件：

```go
// domain/interfaces/embedding.go
//
// IEmbeddingRegistry 按名字取向量化实现。名字来自请求体，取不到时返回参数错误。
type IEmbeddingRegistry interface {
    Get(name string) (IEmbedding, error) // name 为空 → 配置里的 default
    Names() []string                     // 报错时列出来，让调用方知道有哪些可用
}

// domain/interfaces/rerank.go
type IRerankRegistry interface {
    Get(name string) (IRerank, error)
    Names() []string
}
```

实现放 `infrastructure/serviceimpl/registry.go`：

```go
type embeddingRegistry struct {
    def    string
    byName map[string]interfaces.IEmbedding
}
```

- **启动时一次性建好**，把每个 entry 造成一个实现塞进 map。不连网、不懒加载、
  **没有锁**——建好之后再没写过，只读 map 天然并发安全（批量导入的 4 个 goroutine
  各自 `Get` 互不干扰）。
- `Get("")` 用 `def`；名字不在 map 里返回 `NewParamError`，消息里带可用名单。
- `NewRerankRegistry` 在 `rerank.enabled=false` 时 `byName` 为空、`Get` 对**任何**名字
  都返回 `nopRerank{}`，且永不出错——保持"没配 rerank 服务也能正常启动和检索"这个
  现有语义。`enabled=true` 时按正常规则查表。

编译期断言照现有风格追加：

```go
var (
    _ interfaces.IEmbedding        = (*Embedding)(nil)
    _ interfaces.IEmbeddingRegistry = (*embeddingRegistry)(nil)
    _ interfaces.IRerank           = (*Rerank)(nil)
    _ interfaces.IRerankRegistry   = (*rerankRegistry)(nil)
)
```

`domain/interfaces` 只放接口，注册表实现留在 `serviceimpl`——应用层拿到的是端口，
不会 import 到基础设施。

## 6. 应用层改造

### 6.1 检索 `application/service/search/service.go`

`Service` 的字段 `embedder interfaces.IEmbedding` / `rerank interfaces.IRerank`
换成两个注册表。`Search` 开头先把两个名字都解出来：

```go
embedder, err := s.embeddings.Get(in.EmbedModel)   // 名字写错 → 在这里就返回，不白跑一次 ES
if err != nil { return nil, err }
reranker, err := s.reranks.Get(in.RerankModel)
if err != nil { return nil, err }
```

- 解在最前面是刻意的：拼错的名字应当**立刻**报错，而不是先花一次 ES 往返再报。
  代价是 `dense_weight=0`（压根不走向量路）时也会校验 `embed_model`——这是想要的，
  传了一个用不上的错名字同样应该被指出。
- 步骤 ② 的 `s.embedder.EmbedQuery` 改用解出来的 `embedder`。
- 步骤 ⑥ 的判据从 `opts.Rerank && s.rerank != nil && len(items) > 1` 改为
  `opts.Rerank && len(items) > 1`（注册表不会返回 nil），并把 `reranker` 传进
  `rerankItems`。rerank 失败照旧只记警告、退回融合原序。
- 一处 `EmbedQuery` 的 `useKNN()` 短路逻辑保持不变：`dense_weight <= 0.01` 时仍然不发
  这次调用。

### 6.2 导入 `application/service/ingest/service.go`

字段 `embedder interfaces.IEmbedding` 换成 `embeddings interfaces.IEmbeddingRegistry`。
在 `Ingest` 开头（① 定位 KB 之后、② 切片之前）解出本轮使用的实现：

```go
embedder, err := s.embeddings.Get(in.Model)
```

解出来的这个传给 `embedChunks`（方法签名加一个参数），而**不是**存回 `Service` 的字段——
`Service` 是单例，把逐请求的东西写进去就是数据竞争。`BatchIngest` 的 4 个 goroutine
各自解各自的，互不影响。

### 6.3 DTO

```go
// common/dto/doc.go — DocIngestDTO
Model string `json:"model" binding:"omitempty,max=64"`   // 用哪家算向量；空 = 配置默认

// common/dto/search.go — SearchDTO
EmbedModel  string `json:"embed_model" binding:"omitempty,max=64"`
RerankModel string `json:"rerank_model" binding:"omitempty,max=64"`
```

检索侧两路可能来自不同家，所以是两个字段；导入侧只有向量化一件事，就叫 `model`。

## 7. 启动校验

在 `config.Load()` 里 `json.Unmarshal` 之后加一段校验，`MustLoad()` 和任何直接调
`Load()` 的地方都会走到。任一条件不满足就返回错误、服务起不来：

1. `embedding.models` 非空；`rerank.enabled=true` 时 `rerank.models` 非空。
2. `default` 非空，且必须命中对应的一个 key。
3. 每个 entry 的 `url` 非空；`protocol` ∈ {`""`, `openai`, `dashscope`}。
4. **所有 embedding entry 的 `dim` 必须一致**（`dim > 0` 的那些之间）。
   ES 只有一个 `content_vec`，`dims` 建索引时就定死了（§9.4）；两个不同维度的 entry
   同时存在，其中至少一个必然写不进去。
消息里给出当前可用的名字列表。

## 8. 已接受的代价

**换 embedding 服务商后，用旧模型生成的库检索会退化成噪声排序，并且不报错。**

维度和向量空间都不匹配时，只有维度这一层会被 ES 拦住；维度恰好一致的情况（比如
1024 → 1024）没有任何信号。这是本方案明确选择的：接口提供 `model` 让调用方切换，
不落痕、不比对、不在 `knowledge_doc` 上加列，旧数据不做任何特殊处理。

## 9. 验证

1. **配置校验的单元测试**：`default` 指向不存在的 key / `models` 为空 /
   `rerank.enabled=true` 但 `models` 为空 / 两个 embedding entry 维度不同
   ——四种都必须启动报错，且消息里带可用名字。
2. **协议构造与解析的单元测试**（纯函数，照 `rerank_test.go` 的路子）：
   - dashscope rerank 的请求体含 `input.{query,documents}` 与 `parameters.top_n`，
     且**分批时 `top_n` 等于本批长度**；
   - dashscope embedding 的 `text_type` 随 `EmbedDocs`/`EmbedQuery` 切换；
   - 两种响应形状（`results` / `output.results`）都能解析出分数。
3. **真栈 A/B**：同一份 `config.json` 里放两个 key，用不同 `model` 各导一篇文档、
   各搜一次，确认走的是对应那家；省略 `model` 走默认；传一个不存在的名字返回
   `code=10001` 且消息里列出可用名字。
4. **回归**：`go build ./... && go vet ./... && go test ./...` 全绿；`gofmt -l` 不新增脏文件。

## 10. 不做的事

- **不给 embedding 落痕、不做一致性校验、旧数据不做任何处理**（第 2、8 节的明确决定）。
- **不支持一个索引里混不同维度的 embedding 模型**。`content_vec` 是一列，`dims` 建好
  改不了（§9.4），配了两个不同维度的 entry 至少一个必然写不进去。
- **不做配置热加载**。改配置仍然要重启。
- **不给 openai 协议补 `parameters` 逃生口以外的可选项**。真需要塞额外字段时用
  `parameters`，不为此再加字段。
- **不合并 `enabled` 与 `models` 为空**：`enabled` 是"这个实例要不要做重排"的部署决定，
  与"有哪几家可选"无关，语义不同，保留。

## 11. 落地顺序与文件清单

按依赖顺序：

1. `infrastructure/config/config.go` — 两个配置块改结构 + 校验；
   `config.example.json` 同步换形状。
2. `domain/interfaces/embedding.go`、`rerank.go` — 加两个注册表端口。
3. `infrastructure/serviceimpl/embedding.go`、`rerank.go` — 构造改收 entry；
   rerank 加 protocol 分支；embedding 加 protocol 分支。
4. `infrastructure/serviceimpl/registry.go`（新）+ `register.go` — 注册表实现与装配。
5. `common/dto/doc.go`、`search.go` — 三个新字段。
6. `application/service/ingest/service.go`、`search/service.go` — 换成按名字解析。
7. `README.md` — 「API 示例」补 `model` / `embed_model` / `rerank_model`；
   配置一节写明第 8 节的代价。
8. `docs/superpowers/specs/2026-10-09-rerank-implementation-plan.md` — 它是单服务商版本，
   本方案是它的超集；在其开头加一句指向本文，内容不动。

⚠️ **落地前需先与在飞分支对齐**：工作区里有一份未提交的 rerank 实现
（`infrastructure/serviceimpl/rerank.go`、`rerank_test.go`、`httpclient.go` 等）。
第 3 步会改 `NewRerankImpl` 的签名，**必然打破未提交的 `rerank_test.go`**。
落地时要么等那份改动合入后再动，要么同步改那个测试文件——不能放着不管。
