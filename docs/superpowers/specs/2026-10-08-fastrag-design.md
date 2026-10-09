# FastRAG 设计文档

> 知识库检索服务 · ES 单索引多租户 + 混合检索

**日期**：2026-10-08（2026-10-09 修订）
**技术栈**：Go 1.24 / Gin / GORM / go.uber.org/fx / MySQL / Elasticsearch 9.x / OpenAI 兼容 embedding 服务
**架构**：DDD 洋葱架构
**规模目标**：多租户 SaaS

> 本文档正文是**设计说明**（决策、取舍、结构）。
> **每个文件的完整代码在文末「附录 A」**，按层组织，可直接对照实现。

---

## 0. 一句话

> **内容下沉到 ES 供查询，MySQL 只存"文档级"业务状态；写入是 ES 先写、MySQL 后写，查询只碰 ES + 一次批量 SQL。**

```text
【写入】  markdown/切片 ──► ES                ← 内容 + 向量（先写新的，再删旧的）
                        └──► MySQL            ← 文档级业务状态

【查询】  query ──► ES 混合检索 ──► 存活校验 ──► 返回
```

> ⚠️ **部署前提（先看这条）**：本服务**不做鉴权**，`account` 是请求体里的普通参数（§6）。
> 能改请求体的人就能读写任意租户。**只能部署在可信内网**，且调用方必须自己完成对最终用户的鉴权。
> 为防止"不知道这件事的人"把它摆到公网上，启动时会检查 `security.trust_request_account`，
> 未显式置 `true` 直接拒绝启动（§6.1）。

---

## 1. 范围与输入契约

**服务对外只有四个接口**（`api/v1` 前缀，路径与附录代码逐字一致）：

| 方法 | 路径 | 作用 | 阶段 |
|---|---|---|---|
| `POST` | `/api/v1/docs` | 导入单篇（markdown / text / 已切好的切片） | P0 |
| `POST` | `/api/v1/docs/batch` | 批量导入，单篇错误隔离 | P1 |
| `POST` | `/api/v1/docs/delete` | 删除文档（或整库文档） | P1 |
| `POST` | `/api/v1/search` | 混合检索 | P0 |

> 删除做成 `POST .../delete` 而不是 `DELETE`，是因为删除要带 `doc_name`（中文、含空格、可能很长），
> 塞进 URL 需要额外编码规则；放进 body 就没有这个问题。这是有意的取舍，不是漏改。

**边界**：从"干净的文本"接手；**假设知识库已存在**——`knowledge_base` 由外部预置，本服务**只读**它（拿 `kb_id` / `account` / `name` / `search_mode` / `biz_tag`），不提供建库、发布、版本管理的接口。`kb_no` 由建库方在建库时分配，FastRAG 只消费、不生成。

> **`knowledge_base` 的列所有权**：本服务**只写** `doc_count` / `chunk_count` / `update_ts` 三列（计数冗余，§5.1 第 ⑧ 步）。
> 其余列（`name` / `search_mode` / `biz_tag` / `split_options` / `delete_ts` …）归**建库方**所有，本服务只读不写。
> 这条要跟建库方对齐——否则两边各写各的，同一行的 `update_ts` 会互相覆盖。

导入接口接受两种形态，走同一条下游链路（ES 写切片 → ES 清旧切片 → MySQL 计数），形态 A 多一步切片：

```jsonc
// 形态 A：给 markdown，FastRAG 负责切
POST /api/v1/docs
{
  "account": "acc_001",                                      // 租户，调用方直接给（§6）
  "kb_no": "KB20261008001",
  "doc_name": "产品手册.md",
  "format": "markdown",
  "content": "# 产品介绍\n\n## 概述\n...",
  "split_options": { "chunk_size": 800, "split_level": 2 }   // 可选，覆盖默认
}

// 形态 B：给已切好的切片，FastRAG 只索引
POST /api/v1/docs
{
  "account": "acc_001",
  "kb_no": "KB20261008001",
  "doc_name": "产品手册.md",
  "format": "chunks",
  "chunks": [
    { "title": "产品介绍，概述", "content": "# 产品介绍\n\n## 概述\n..." },
    { "title": "产品介绍，功能", "content": "..." }
  ]
}
```

`account` 是**普通请求参数**，不走鉴权链、不做身份解析（§6）；它与 `kb_no` 一起定位知识库。

---

## 2. 四条核心决策

### D1 · 内容下沉 ES，查询不回表取内容

ES 文档存 `title` + `content` + 向量，查询命中后直接返回，不去 MySQL 取正文。省一次跨存储往返（代价：ES 存储翻倍）。

### D2 · 单索引多租户，靠 `term(account)` 过滤

所有租户的切片进同一个物理索引 `fastrag`，每条切片带 `account`（`keyword`），查询**强制**加 `term(account)`。

- 一租户一索引在 SaaS 租户数成千上万时，分片数、cluster state、小索引开销、跨租户检索都会失控。
- 分桶 + 大租户独占是为亿级准备的复杂度，本期不需要。

代价：单索引下大租户集中打热分片。量级未到，先接受；扩容路径见 §9.2。

### D3 · MySQL 只存"文档级"状态，切片级全部下沉

MySQL 存知识库与文档（万级行）；切片的一切（内容、向量、标题、位置、计数）都在 ES。不做切片级 MySQL 表——亿行表的索引维护、分页、DDL 都是负担，而查询路径根本不需要它。计数用冗余列（`doc.chunk_count`、`kb.chunk_count`）。

### D4 · 简单一致：先写后删 + 存活校验

写入不做跨存储事务，也不引入代次（`ver`）这类并发控制。三步：

```text
① ES     写切片（新内容先进去）
② ES     删掉这篇文档里 _id 不在新集合中的旧切片
③ MySQL  upsert 文档行（content_hash / chunk_count）+ 回写 KB 计数
```

**顺序为什么是"先写后删"而不是"先删后写"**：`chunk_id = sha256(doc_id, order, content)`，内容一变 `_id` 就变。所以重导入时旧切片不会被覆盖，必须显式清掉。清在写之前的话，一旦写失败，这篇文档的内容就真的没了；清在写之后，失败时旧切片多留一会儿，下次重导入会再清一遍——**重导入失败是可恢复的**。

**② 为什么不是"删掉这篇文档的全部切片"**：那样会把内容没变的切片也删掉再写回来，白白放大一次写入。按 `_id` 做差集只删真正过期的那些——内容没变的重导入几乎不产生删除。

失败点与后果：

| 断点 | 后果 | 是否安全 |
|---|---|---|
| ① 失败 | 什么都没发生 | ✅ 重试 |
| ① 成功、② 失败 | 旧切片残留，被存活校验按 `doc_id` 兜住 | ✅ 下次重导入清理 |
| ② 成功、③ 失败 | 新文档：孤儿切片 → 查不到；老文档：计数 / 指纹滞后 | ✅ 对账重算（§11 场景 2） |

**对外的承诺就一句，写进接口文档的也是这一句**：

> ✅ 保证：**不返回 MySQL 中不存在或已删除的文档**。
> ⚠️ 不保证：文档内容在任何时刻都是**单一版本**——重导入窗口内可能新旧各返回一部分。

「不返回错文档」是这套设计真正守住的底线（下面两条不变量）。「内容只有一个版本」**没有守住**，不能把它算进正确性里，调用方如果要靠它做业务判断（比如拿检索结果去覆盖写回），必须先知道这一点。

**已知缺口（本期接受）**：

1. **重导入窗口**：① 与 ② 之间，同一文档的新旧切片同时在 ES 里。因为刷新是 30s 级别的（§4.2），窗口实际可能持续一个刷新周期，期间检索返回这篇文档的新旧两份内容。
2. **并发同名导入**：没有串行化点，可能返回**被截断的内容**——这一条比第 1 条严重，§5.5 单列了。

两条都要一个串行化点才能消掉，也就是下面那套被推迟的代次协议。

> **这条是刻意推迟的增强，不是遗漏。** 早先设计过一套"MySQL 预占 `ver` → ES 写带 `ver` 的切片 → 删 `ver` 更小的 → MySQL 提交"的四步协议，
> 用来在**并发导入同一文档**时保证"只有代次最高的那套可见"。它能消掉上面那个重复窗口，也能保证并发写者不会让两组矛盾内容同时被检索到。
> 代价是 `knowledge_doc` 多一列、ES mapping 多一个字段、两个仓储方法（`ReserveVer` / `CommitIngest`）、查询侧多一次代次比对。
> **本期不做**（用户决定：先把 RAG 的核心功能做完整，再回头做一致性增强）。设计见 §13 待定 9。

---

## 3. 架构分层

依赖方向单向：接入 → 应用 → 领域 ← 基础设施。

### 3.1 目录结构

```text
FastRAG/
├── cmd/
│   └── server/main.go                     # fx 装配入口
├── application/
│   └── service/
│       ├── doc_ingest.go                  # 导入编排（切片→两写）
│       └── search.go                      # 检索编排（两路→融合→重排）
├── domain/
│   ├── entity/
│   │   ├── knowledge_base.go              # KB 聚合根（只读）
│   │   ├── knowledge_doc.go               # 文档聚合根
│   │   └── chunk.go                       # 切片（不落 MySQL）
│   ├── value_object/
│   │   ├── chunk_options.go               # 切片参数 + Validate
│   │   └── search_options.go              # 检索参数 + Validate
│   ├── factory/
│   │   ├── knowledge_base.go
│   │   └── knowledge_doc.go
│   ├── repository/                        # ← 存储端口（接口）
│   │   ├── id_service.go
│   │   └── knowledge/
│   │       ├── knowledge_base.go          # IKnowledgeBaseRepo
│   │       ├── knowledge_doc.go           # IKnowledgeDocRepo
│   │       └── vector_store.go            # IVectorStore：切片存 ES（D3）
│   ├── interfaces/                        # ← 外部服务端口（接口）
│   │   └── embedding_service.go           # IEmbeddingService
│   ├── service/
│   │   └── splitter.go                    # 切片领域服务
│   └── event/
│       └── doc_indexed.go
├── infrastructure/
│   ├── controller/http/
│   │   ├── register.go                    # 全部路由挂载点（A4.15）
│   │   ├── doc/controller.go              # 导入 / 批量导入 / 删除（A4.13）
│   │   ├── search/controller.go           # 检索（A4.14）
│   │   ├── health/controller.go           # /health、/ready（A4.2）
│   │   └── web/                           # 演示页（A4.18）
│   │       ├── web.go                     # goembed，GET /
│   │       └── index.html                 # 单文件前端，无构建步骤
│   ├── persistence/                       # ← 端口实现
│   │   ├── knowledge/
│   │   │   ├── knowledge_base_repo.go     # IKnowledgeBaseRepo（GORM，只读）
│   │   │   ├── knowledge_doc_repo.go      # IKnowledgeDocRepo（GORM）
│   │   │   ├── vector_store_es.go         # IVectorStore（ES 实现）
│   │   │   └── index_template.go          # ES mapping / settings
│   │   ├── po/
│   │   ├── mapper/                        # Entity <-> PO
│   │   ├── migration/
│   │   └── register.go
│   ├── serviceimpl/
│   │   └── embedding_openai.go            # IEmbeddingService（外部 API）
│   ├── driver/                            # 只做连接 / 通用请求
│   │   ├── mysql/
│   │   ├── redis/
│   │   └── es/
│   ├── config/config.go
│   └── init.go                            # fx 依赖装配
└── common/
    ├── dto/
    ├── bizerrors/
    ├── constants/
    ├── utils/
    └── mapper/
```

**接口归口的判据**：

| 层 | 装什么 | 判据 |
|---|---|---|
| `domain/repository/` | **持久化端口** | 存 / 取领域对象 |
| `domain/interfaces/` | **外部服务端口** | 无状态调用，不存东西 |

所以 `IVectorStore` 在 `repository/`——切片持久化在 ES（D3），**ES 就是切片的仓储**；`IEmbeddingService` 在 `interfaces/`——它不存任何东西，是纯计算的外部 API 调用。

> 反过来说：如果哪天 ES 退化成纯索引、另有权威存储，`IVectorStore` 就该挪到 `interfaces/`——判据是"它是权威存储还是外部服务"，不是"它叫什么"。FastRAG 当前没有这个前提（D3），所以它落在 `repository/`。

### 3.2 依赖注入（fx）

```go
// infrastructure/init.go —— 装配概览。**完整代码见 A4.16**。
func core(conf *config.Config) fx.Option {
    return fx.Options(
        fx.Supply(conf),

        // Redis 已从图上摘掉：它原本只服务限流器，而限流本期不做（§10.3）。
        // 骨架里的 driver/redis 与 config.redis 保留不动，等真要限流时再接回来。
        fx.Provide(mysql.NewProvider),   // 返回 *sqlsdk.TransProvider
        fx.Provide(es.NewClient),

        // TransProvider 一份实例按两个接口分别暴露：
        // 仓储要 sqlsdk.Provider，写编排要 transaction.Manager
        fx.Provide(func(p *sqlsdk.TransProvider) sqlsdk.Provider { return p }),
        fx.Provide(func(p *sqlsdk.TransProvider) transaction.Manager { return p }),

        persistence.Register,    // 三个仓储，构造函数直接返回接口类型
        serviceimpl.Register,    // embedding / rerank / 雪花 ID
        domain.Register,
        service.Register,        // application/service/register.go（A3.4）

        // 两道启动期闸门：配置未显式声明"可信内网"就拒绝启动（§6.1）；
        // 建 ES 索引只做这一次（§9.4）
        fx.Invoke(checkTrustRequestAccount),
        fx.Invoke(ensureIndexOnStart),
    )
}
```

> 三个入口的差别只在 `Init`（HTTP）多挂 `gingext.NewEngine` + `controller.Register`，
> `InitCLI` 不加。`core` 是共用的，所以两道 `fx.Invoke` 闸门三个入口都过——
> 这正是把校验放这里、而不是放 `main` 里的原因。

```go
// 各层的构造函数返回的就是接口，不需要 fx.Annotate/fx.As 包一层：
repository 侧是 `knowledgerepo.IKnowledgeBaseRepo` / `IKnowledgeDocRepo` / `IVectorStore`，
serviceimpl 侧是 `interfaces.IEmbeddingService`（`NewOpenAIEmbedding`）/
`interfaces.IRerankService`（`NewRerankStub`）/ `repository.IIDService`。
```

---

## 4. 数据模型

### 4.1 MySQL 表

两张表，全部万级行。表名与 `infrastructure/persistence/migration/schema.sql` 保持单一事实源。

```sql
-- ① 知识库（外部预置；本服务只写 doc_count/chunk_count/update_ts 三列，见 §1 列所有权）
CREATE TABLE `knowledge_base` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `no`              VARCHAR(64)  NOT NULL COMMENT '对外编号',
  `account`         VARCHAR(64)  NOT NULL COMMENT '租户',
  `name`            VARCHAR(255) NOT NULL,
  `description`     VARCHAR(1024) DEFAULT '',
  `knowledge_type`  VARCHAR(32)  NOT NULL DEFAULT 'ordinary' COMMENT 'ordinary/qa',
  `search_mode`     VARCHAR(32)  NOT NULL DEFAULT 'title_and_content' COMMENT 'title/title_and_content',
  `biz_tag`         VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '业务标签，租户内再分域，单值',
  `split_options`   JSON         DEFAULT NULL COMMENT '切片参数快照',

  -- 冗余计数（列表页用，见 D3）
  `doc_count`       BIGINT       NOT NULL DEFAULT 0,
  `chunk_count`     BIGINT       NOT NULL DEFAULT 0,

  `is_system`       TINYINT      NOT NULL DEFAULT 0,
  `create_ts`       BIGINT       NOT NULL,
  `update_ts`       BIGINT       NOT NULL,
  `delete_ts`       BIGINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_no` (`no`, `delete_ts`),
  KEY `idx_account` (`account`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ② 文档
CREATE TABLE `knowledge_doc` (
  `id`              BIGINT UNSIGNED NOT NULL COMMENT '雪花 ID，写入前预分配',
  `kb_id`           BIGINT UNSIGNED NOT NULL,
  `account`         VARCHAR(64)  NOT NULL,
  `name`            VARCHAR(512) NOT NULL COMMENT '文档名。同名重导入 = 更新，doc_id 不变',
  `content_hash`    CHAR(64)     NOT NULL COMMENT '内容指纹',
  `chunk_count`     INT          NOT NULL DEFAULT 0,
  `create_ts`       BIGINT       NOT NULL,
  `update_ts`       BIGINT       NOT NULL,
  `delete_ts`       BIGINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_kb_name` (`kb_id`, `name`, `delete_ts`),
  KEY `idx_kb_delete` (`kb_id`, `delete_ts`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

两条关键约定：

1. **`id` 由应用侧预分配（雪花），不用自增。** ES 切片要带 `doc_id`，得先知道它；等自增 ID 会把顺序反过来。
2. **文档身份 = `(kb_id, name)`，不是 `(kb_id, content_hash)`。** 同名重导入 = 更新（同一 `doc_id`，新 `content_hash`），不新增行，否则内容一变就攒僵尸文档行。

> 本设计**没有**给这两张表加列。§13 待定 9 的一致性增强会需要 `knowledge_doc.ver`，本期不做。

### 4.2 ES 索引 mapping

单索引 `fastrag`。`dims` / `analyzer` 是模板值，由配置注入（§10.3）。

```json
{
  "settings": {
    "number_of_shards": 3,
    "number_of_replicas": 1,
    "refresh_interval": "30s",
    "analysis": {
      "analyzer": {
        "ik_max_word": { "type": "custom", "tokenizer": "ik_max_word" },
        "ik_smart":    { "type": "custom", "tokenizer": "ik_smart" }
      }
    }
  },
  "mappings": {
    "properties": {
      "account":      { "type": "keyword" },
      "kb_id":        { "type": "long" },
      "doc_id":       { "type": "long" },
      "chunk_id":     { "type": "keyword" },
      "order":        { "type": "integer" },
      "biz_tag":      { "type": "keyword" },

      "title":        { "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },
      "content":      { "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },
      "heading_path": { "type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart" },

      "title_vec":    { "type": "dense_vector", "dims": 1024, "index": true, "similarity": "cosine" },
      "content_vec":  { "type": "dense_vector", "dims": 1024, "index": true, "similarity": "cosine" },

      "create_ts":    { "type": "long" }
    }
  }
}
```

> **向量字段刻意不写 `index_options`，让 ES 9 自己填 `bbq_hnsw`。**
> 上面两行只声明了 `dims / index / similarity`，但 ES 建出来的实际 mapping 是——
> 这是从运行中的索引上读回来的，不是猜的：
>
> ```json
> "content_vec": {
>   "type": "dense_vector", "dims": 1024, "index": true, "similarity": "cosine",
>   "index_options": { "type": "bbq_hnsw", "m": 16, "ef_construction": 100,
>                      "rescore_vector": { "oversample": 3.0 } }
> }
> ```
>
> `bbq_hnsw` 是二值量化 HNSW，图的内存占用大约是纯 HNSW 的 1/4，代价是召回有损；
> ES 靠 `rescore_vector.oversample: 3.0`（多捞 3 倍候选再用全精度重排）补回来一部分。
> **召回实测不达标时，把它覆盖成纯 `hnsw` 就是那个旋钮**——
> 见 A4.7 里 `vectorField()` 的注释。注意 `index_options` 建好之后就改不了了，
> 要换只能重建索引重导。

| 字段 | 用途 |
|---|---|
| `account` | 租户隔离 filter（D2）。**必须 `keyword`，写成 `text` 就是越权** |
| `kb_id` / `biz_tag` | KB 过滤 / 租户内再分域过滤，均 keyword |
| `doc_id` | 存活校验的关键——收集后批量查 `knowledge_doc` |
| `chunk_id` | 作 `_id`，生成规则见 §5.3 |
| `content` | 内容下沉（D1），查询直接返回 |
| `title` / `heading_path` | 标题召回与展示 / 标题链 |
| `title_vec` / `content_vec` | 向量字段 |

不存 `doc_name` / `kb_name`：存活校验本来就要查 `knowledge_doc`，顺手 JOIN 拿到名字。

> ⚠️ **在 Kibana 里看 `_source` 是看不到 `title_vec` / `content_vec` 的，这不是 bug。**
>
> ES 9 新增了 `index.mapping.exclude_source_vectors`，**新建索引默认就是 `true`**。
> 它做的事是在**存储层**把 dense_vector 从 `_source` 里摘掉（不是查询时才过滤），
> 顺带 `_search` / `_mget` / `_get` 的响应里也不返回。省下来的正是每切片那 8KB 向量。
>
> 已经实测确认过（ES 9.5.5，本索引 `exclude_source_vectors = true`）：
>
> - **向量确实在**：绕开应用层直接发 `knn` 查询，命中 3 条、cosine ≈ 0.79；
>   `/api/v1/search` 在 `dense_weight` 为 0.0 / 0.5 / 1.0 三条路径上都出结果。
> - **想看的话有两条路**：查询时加 `"_source": {"exclude_vectors": false}`，
>   或者用 `fields` API（`{"_source": false, "fields": ["content_vec"]}`）——
>   后者是官方推荐，值从内部表示 rehydrate 回来，所以**精度是量化后的**，
>   和写入时不完全 bit 相等（本索引用的是 bbq/int8 量化，本来也不是原值）。
> - **`_reindex` 不会丢向量**：实测 157 条全量复制过去，目标索引里 `content_vec` 仍是 1024 维。
>   ES 自己会处理 rehydrate，不用担心 §9.2 的扩容路径。
>
> **我们的代码不受影响**：`sourceFields()` 本来就不取 `*_vec`（§7.2 只用它们算相似度，
> 不需要回传），所以这个开关开着对我们只有好处——省磁盘，没有代价。
> **别为了"能在 Kibana 里看见"把它关掉**：关掉之后向量会在 `_source` 里再存一份原始表示，
> 磁盘直接多出 §12.1 容量估算里那 8KB/切片——纯粹为了调试方便付的账不划算。
> 真要排查，用上面那两条路，临时开着看就行。
>
> 手敲上面那些查询太麻烦，所以仓库里放了个脚本（`make es-vectors` 或直接跑）：
>
> ```bash
> make es-vectors                                  # 总数 + 抽一条切片
> make es-vector-full                              # 把 1024 个数字**全部**打出来
> bash scripts/es-inspect-vector.sh <chunk_id>     # 指定切片（chunk_id 就是 _id）
> bash scripts/es-inspect-vector.sh --account demo # 某个租户
> ```
>
> 它做三件事：用 `exists` 聚合数一遍**有多少切片带向量**（而不是翻 `_source` 翻不到就下结论）；
> 用 `fields` API 把某条切片的 `content_vec` / `title_vec` 打出来；
> 最后**拿这条切片自己的向量去 kNN 搜它自己**，命中且 `score≈1.0` 才算数——
> 毕竟"看得见"不是目的，"能用它检索"才是。

**可见性 SLA**：`refresh_interval: 30s` 是拿"导入后立即可搜"换写入吞吐。落地的口径是——

- **对外承诺的可见性延迟 = 一次刷新周期（默认 30s）**，不是"导入返回即可搜"。§12 的验收标准按这个口径写。
- 需要"导入即可搜"的场景（如演示、单篇补录、冒烟验证）由调用方在**请求级**传 `"refresh": true`：导入接口在写完 ES 后立刻 `_refresh` 一次，返回即可检索。代价是该次请求多花一次索引刷新，批量导入**不要**开。
  - 用立即刷新，不用 ES 的 `refresh=wait_for`：后者的语义是"等到下一次定期刷新"（最多干等 30s），而这里既然都主动刷新了，就没有等的必要。
- 真要求全局近实时，就把 `refresh_interval` 调成 `1s`，代价是分段数暴涨、写入吞吐明显下降（§9.3）。这是一个部署取舍，不是代码问题。

---

## 5. 写入路径

### 5.1 编排

```go
// application/service/ingest/service.go
// ↑ 本段是**编排示意**，略去了错误包装与日志细节；完整可落盘的代码见 A3.1。

func (s *Service) Ingest(
    ctx context.Context,
    in *dto.DocIngestDTO,
) (*vo.DocIngestVO, error) {
    if err := in.Validate(); err != nil {
        return nil, err
    }

    // 1) 定位 KB + 校验租户归属（account 来自请求参数，见 §6）
    kb, err := s.kbRepo.LoadByNo(ctx, in.KBNo, in.Account)
    if err != nil {
        return nil, err
    }

    // 2) 切分（形态 B 时直接透传归一化）
    var chunks entity.Chunks
    if in.Format == constants.FormatChunks {
        chunks, err = s.splitter.Normalize(ctx, kb, in.Chunks)
    } else {
        chunks, err = s.splitter.Split(ctx, kb, in.Content, in.SplitOptions)
    }
    if err != nil {
        return nil, err
    }
    if len(chunks) == 0 {
        return nil, bizerrors.NewErrParam("切片结果为空")
    }

    // 3) 向量化在应用层做（仓储不调外部 API），工厂只负责组装。
    //    放在任何写之前：失败就当这次导入没发生过。
    //    注：索引的创建**不在这里**，在启动期做一次（见 §9.4）。
    titleVecs, contentVecs, err := s.embedChunks(ctx, chunks)
    if err != nil {
        return nil, errors.WithStack(err)
    }

    now := time.Now()
    hash := contentHash(chunks)
    bizTag := kb.BizTag // 只认 KB 上的值，文档级不允许覆盖（§7.2）

    // 4) 定 doc_id：**同名重导入沿用库里那一行的 id**，只有首次导入才分配。
    //    换新 id 会让新切片成为孤儿、旧切片又删不掉——见 §5.3。
    //    这一步必须早于 ES 写入，因为 chunk_id 是从 doc_id 派生的（§5.3）。
    existing, err := s.docRepo.LoadByName(ctx, kb.ID, in.DocName)
    if err != nil {
        return nil, err
    }
    var docID uint64
    if existing != nil {
        docID = existing.ID
    } else {
        docID = uint64(s.idGen.NextIntID())
    }
    vectors := factory.BuildVectorDocs(kb, docID, bizTag, chunks, titleVecs, contentVecs, now)

    // 5) 【第一写】ES 写切片。新内容先进去，旧内容原地不动——
    //    这样写失败时这篇文档仍然可查（只是内容还是旧的），重试即可。
    if err := s.store.Save(ctx, vectors); err != nil {
        return nil, errors.WithStack(err)
    }

    // 6) 【刷新】把新切片、以及「可能还没刷出来的旧切片」推进可检索视图。
    //    delete_by_query 只看得到已刷新的段，不刷新的话下面的差集删除
    //    会看不见上一批旧切片，删了等于没删（§5.2 ② 前的那次刷新）。
    //    重导入必刷；首次导入只在调用方要求"导入即可搜"时刷。
    if existing != nil || in.Refresh {
        if err := s.store.Refresh(ctx); err != nil {
            logsdk.Warn(ctx, "刷新索引失败，本次可能清不掉旧切片", logsdk.Any("doc_id", docID))
        }
    }

    // 7) 【第二写】ES 清掉这篇文档里 _id 不在新集合中的旧切片。
    //
    //    只删差集而不是 DeleteByQuery(doc_id) 全删：内容没变的切片 _id 不变，
    //    留着就行，全删再写回来是白放大一次写入。
    //
    //    失败不影响正确性：旧切片多留一会儿，下次重导入会再清一遍（§5.2）。
    if _, err := s.store.DeleteExcept(ctx, knowledgerepo.VectorFilter{
        Account: kb.Account, DocID: docID,
    }, factory.ChunkIDs(vectors)); err != nil {
        logsdk.Warn(ctx, "清理旧切片失败，不影响检索正确性", logsdk.Any("doc_id", docID))
    }

    // 8) 【第三写】MySQL 写入文档行 + 回写 KB 计数。
    //
    //    实体在这里才构造——所有字段都已定稿，不存在"先建行后填内容"的中间态。
    doc := factory.NewDoc(docID, kb, in.DocName, hash, len(chunks), now)

    //    计数与写入必须在同一个事务里：Save 返回的 oldChunkCount 是「更新前」的值，
    //    事务外再拿去算差值的话，中途挤进来的另一次导入会让差值算错，
    //    KB 的冗余计数就永久性偏了。
    var created bool
    if err := s.tm.Transaction(ctx, func(ctx context.Context) error {
        oldChunkCount, isNew, err := s.docRepo.Save(ctx, doc)
        if err != nil {
            return err
        }
        created = isNew
        if err := s.kbRepo.ApplyChunkDelta(ctx, kb.ID, int64(len(chunks)-oldChunkCount)); err != nil {
            return err
        }
        if isNew {
            return s.kbRepo.ApplyDocDelta(ctx, kb.ID, 1)
        }
        return nil
    }); err != nil {
        // 文档行没落 → 切片成了孤儿，被存活校验挡在检索之外（§7.3）。
        // 这是「可过滤」的一侧：不返回错内容，只是这篇暂时搜不到。
        logsdk.Error(ctx, "写入文档行失败，该文档当前不可检索，等对账重算",
            logsdk.Any("doc_id", docID), logsdk.Err(err))
        return nil, errors.WithStack(err)
    }

    return &vo.DocIngestVO{
        DocID: docID, DocName: in.DocName,
        ChunkCount: len(chunks), Created: created,
    }, nil
}
```

### 5.2 写入次序

```text
                    ┌── 重导入 / refresh: true 时先刷一次 ──┐
                    ▼                                    │
ES 写新切片 ──────► 刷新 ──────► ES 删旧切片（_id 差集） ─┴─► MySQL 写文档行 + 计数
     ①                           ②                                ③
```

**② 前面那次刷新不是可选项。** `delete_by_query` 只能作用在**已刷新的段**上，而默认刷新周期是 30s（§4.2）。两层后果：

| 谁没刷新 | 后果 |
|---|---|
| 上一批旧切片（两次导入间隔 < 30s） | ③ 看不见它们 → 一条也删不掉，旧切片留到**再下一次**重导入才被清掉 |
| 本次的新切片 | 不影响 ③（新切片在保留集合里，本来就删不到），但影响「导入即可搜」 |

也就是说，不刷新时 `§12.1` 那条「重导入后旧切片被清掉」的验收会**时灵时不灵**——取决于两次导入隔了多久。所以重导入路径**必刷**（`existing != nil`），首次导入只在调用方传 `refresh: true` 时刷。

代价：`_refresh` 是**整个索引**级别的，ES 没有按文档刷新。单索引多租户（D2）下会顺带刷到别的租户的段，所以它只出现在这两条路径上，常规写入不碰。

| 步骤 | 失败后果 | 是否安全 | 修复手段 |
|---|---|---|---|
| 刷新 | 本次 ② 可能删不掉上一批旧切片 → 重复召回，留到下次重导入 | ✅ | 下次重导入清理 |
| ① ES 写 | 什么都没发生 | ✅ | 重试 |
| ② ES 删差集 | 旧切片残留，被存活校验按 `doc_id` 兜住（不返回错内容，只是重复） | ✅ | 下次重导入清理 |
| ③ MySQL | 新文档：孤儿切片 → 搜不到；老文档：计数 / 指纹滞后 | ✅ | 对账重算 |

**为什么"先写后删"而不是"先删后写"**：`chunk_id` 里含内容（§5.3），内容一变 `_id` 就变，所以重导入时旧切片**不会被覆盖**，必须显式清掉。清在写之前的话，一旦写失败，这篇文档的内容就真的没了；清在写之后，失败时旧内容还躺在 ES 里，重试一次就恢复——**重导入失败是可恢复的**。

**为什么 ② 是差集而不是全删**：`DeleteByQuery(doc_id)` 会把内容没变的切片也删掉再写回来，白白放大一次写入。按 `_id` 取差集只删真正过期的那些，内容没变的重导入几乎不产生删除。

**为什么 MySQL 在最后**：文档行是存活校验的依据（§7.3）。放在最后，就意味着「MySQL 里查不到的切片一律不展示」——所以无论前两步怎么失败，检索都不会返回错误内容，最坏只是这篇暂时搜不到。

> ⚠️ **已知缺口**：① 与 ② 之间有一个短暂窗口，同一文档的新旧切片同时在 ES 里。又因为 ES 刷新是 30s 级别的（§4.2），这个窗口实际可能持续到一个刷新周期——期间检索会返回这篇文档的新旧两份内容。要消掉它就得引入代次机制，本期不做（§2 D4 末段、§13 待定 9）。

### 5.3 chunk_id 生成

```go
// chunk_id = sha256(doc_id, order, content)[:32] —— 纯函数、可复现
```

**前提：`doc_id` 在重导入之间必须稳定。** 这是 §5.1 第 ④ 步要先 `LoadByName` 再决定 id 的原因，也是这一段最容易被写错的地方。

`doc_id` 一旦换新，三处同时失效：

| 以 `doc_id` 为轴的东西 | 换了新 id 之后 |
|---|---|
| ES `_id`（`chunk_id` 的输入） | 新旧切片 `_id` 完全不同，不可能互相覆盖 |
| §5.2 ② 的 `DeleteExcept` 过滤条件 | 按新 id 删，一条旧切片也匹配不到 |
| §7.3 的存活校验 | 新切片对应的文档行**不存在**（行还是旧 id），全被丢掉 |

净效果是**新内容一条都搜不到、旧内容一条不少地继续被搜到**，而接口返回成功。所以「重导入 = 更新」这条语义的落点在 `doc_id` 的复用上，不在 `chunk_count` 的更新上。

在此前提下，`chunk_id` 由内容派生带来两个后果，都是刻意接受的：

1. 重导入时旧切片不会被同名覆盖，必须靠 §5.2 第 ② 步按 `_id` 差集清掉；
2. 内容没变的那部分切片 `_id` 不变，重导入对它们是原地覆盖——差集里也算「保留」，零成本。

幂等因此来自 **「稳定的 `doc_id` + 先写后删 + `_id` 差集」**，而不是「同名覆盖」。

### 5.4 删除编排

顺序：**MySQL（软删）→ ES（清理）**。

```text
删文档:  MySQL 软删(delete_ts)  ──►  ES DeleteByQuery(doc_id)  ──►  计数回退
删 KB :  MySQL 软删(kb)         ──►  ES DeleteByQuery(kb_id)   ──►  doc 一并软删
```

| 失败点 | 后果 | 是否安全 |
|---|---|---|
| MySQL 软删成功，ES 未删 | 幽灵切片 | ✅ 存活校验按 `doc.Deleted()` 过滤（§7.3） |
| ES 先删成功，MySQL 软删失败 | 文档在、切片没了 → 漏召回 | ⚠️ 所以 MySQL 必须在前 |

MySQL 软删权威且即时，ES 清理可延迟、可重试。

> 删除**不需要** §5.2 ② 前那次刷新。同样的可见性问题在这里不成害：没被删掉的切片其文档行已经软删，§7.3 的存活校验会把它挡在结果之外——删不掉只是留下幽灵切片，不会返回错内容。重导入则不同，那里删不掉会**真的返回旧内容**，所以只有那条路径要付刷新的代价。

删除走的是 `DeleteByQuery`（删全部），**不是** §5.2 第 ② 步的 `DeleteExcept`（按 `_id` 留一部分）。
两者别混：删除的语义是"这篇文档的东西一件不留"，任何"保留集合"都是 bug。

删除接口的 API 形态见 §1（`POST /api/v1/docs/delete`），P1 交付。

### 5.5 批量导入

应用层用**受限并发工作池**编排：固定并发度 4（embedding 是外部依赖，串行太慢、放开太快会打死供应商）；单文档错误隔离；结果聚合返回 `{成功 n, 失败 m, 失败明细}`。

并发度写死 4 而不是走配置，是因为本期不做限流（§10.3）——工作池是唯一的背压手段，把它做成可调项会给人"已经限流了"的错觉。

同步请求，受 HTTP 超时约束；大文档由上游分批调用。

**并发写同一文档 —— 本期明确不支持，且失败模式比"内容混两代"更糟。**

`doc_id` 复用（§5.3）之后，同一个文档的两次并发导入会**用同一个 `doc_id`** 写 ES，于是：

```text
A 写 setA ──┐
            ├─► A 的 DeleteExcept(keep=setA) 删掉 setB 里 _id 不同的那些
B 写 setB ──┘
            └─► B 的 DeleteExcept(keep=setB) 删掉 setA 里 _id 不同的那些

最终存活 = setA ∩ setB
```

`_id` 含内容，所以交集只剩「两边内容和顺序都一样」的那些切片。两次导入内容不同时，**结果是文档内容不完整**（缺掉差异切片）；内容碰巧相同时才全部存活。

这不是"可过滤的一侧"——文档行存在、切片也在，返回的却是一份被截断的内容。它属于「并发同名导入」这个明确排除在外的场景，**服务端不做串行化**（用户决定：不引入锁机制）。

**批次内同名：服务端直接拦掉。** 这一类不需要锁、不需要跨请求协调，同步扫一遍就能判定，是唯一"免费"能堵住的口子，所以堵：

- `BatchIngest` 在起工作池**之前**同步 `duplicateDocNames` 预扫（A3.1），批内重复出现的 `doc_name` 逐条标记为失败并**跳过**，不进工作池。
- 整批**不返回 400**，仍按控制器的约定恒返回 200 + 明细（A4.13）——重复的是那一两条，合法的几十条照常导入。
- 为什么不"后一条覆盖前一条"：同名两条该留哪一份是业务判断，服务端替调用方定夺，只会让"我明明传了正确的那份"无从解释。

**跨请求并发同名仍然不支持。** 单篇导入在途时提交同名文档，仍然会落到上面那个交集截断。这一条只能靠调用方自己保证：

> **调用方不要让同一个文档的两份导入同时在途。**

首次导入的并发创建同理：两个请求都 `LoadByName` 查不到，各自分配 id，MySQL 的唯一键 `(kb_id, name, delete_ts)` 只让一个建行成功；落败那套切片 `doc_id` 指向不存在的行，被存活校验丢掉（内容安全，只是白跑一次 embedding），但 `chunk_count` 会被后写的那次覆盖成落败者的切片数——**计数漂移，需要 §A5.1 对账**（§11 场景 5）。

这是"去掉一致性增强"的直接代价（§2 D4）。要根除它，得让"谁的切片算数"有一个串行化点，也就是 §13 待定 9 那套代次协议，或一个跨请求的锁。

---

## 6. 租户参数

**不做鉴权**。`account` 就是请求体里的一个普通字段，谁调谁填：

```text
调用方 ──► { "account": "acc_001", "kb_no": "...", ... }
              │
              FastRAG 直接当成可信输入
              │
              仓储层每个方法都带 account 条件，SQL 里 `account = ?`
```

1. `account` 来自请求参数，**没有中间件、没有 token 解析、没有身份注入**。
2. 所有仓储方法签名都带 `account`（如 `LoadByNo(ctx, no, account)`），SQL 里带 `account = ?`——**租户条件不漏写这件事由数据访问层保证**，不靠每个用例自己记得加。
3. `kb_no` 是对外编号不是凭证；两者必须自洽（`kb_no` 指向的库其 `account` 必须等于传入值），不自洽时返回 **404 `ErrKnowledgeBaseNotFound`，不是 403**。
   - 403 等于承认"这个 `kb_no` 存在，只是不归你"——那就把一个可枚举的租户探测接口送出去了。404 让"库不存在"和"库不归你"**不可区分**（附录 A4.3 里两种情况返回同一个错误）。
   - 代价：调用方拿到 404 时分不清是编号写错了还是租户串了。这是有意的——排障要靠日志，不靠响应码。

> ⚠️ **这意味着 FastRAG 不提供越权防护**：能改请求体的人就能读写任意租户。它只能在**可信内网**暴露，且调用方自己已完成对最终用户的鉴权。日后要对外时，再把 `account` 改为从 token 解析——仓储层的 `account = ?` 不用动。

### 6.1 启动期强制开关

一个"不做鉴权"的服务最危险的失败方式不是被攻击，是**有人不知情地把它摆到了公网上**。加一道启动断言，让这件事必须被显式确认：

```jsonc
{
  "security": {
    "trust_request_account": false   // 默认 false
  }
}
```

| 取值 | 行为 |
|---|---|
| `false`（默认） | **拒绝启动**，打印一段说明为什么、以及怎么改 |
| `true` | 正常启动，并按上文的"可信输入"处理 `account` |

拒绝时的日志（这是运维唯一会看到的提示，写清楚）：

```text
本服务不提供越权防护：account 来自请求体，任何人都能读写任意租户。
它只能在可信内网部署，且调用方已完成对最终用户的鉴权。
确认这一点后，把配置 security.trust_request_account 置为 true。
```

默认值取 `false` 是刻意的：让"开箱即跑"这件事在**部署**（有人看着日志）时失败，而不是在**生产**（没人看日志）时静默地不设防。本地开发要跑，就在配置里写一行 `true`。

---

## 7. 查询路径

### 7.1 编排

```go
// application/service/search/service.go
// ↑ 编排示意；完整代码见 A3.3。

func (s *Service) Search(
    ctx context.Context,
    in *dto.SearchDTO,
) (*vo.SearchResultVO, error) {
    opts, err := s.resolveOptions(in)
    if err != nil {
        return nil, err
    }

    // 1) 加载 KB（account 来自请求参数，仓储层强制带 account 条件，见 §6）
    kbs, err := s.kbRepo.LoadByNos(ctx, in.KBNos, in.Account)
    if err != nil {
        return nil, err
    }
    kbs = kbs.FilterByBizTags(opts.BizTags)
    if len(kbs) == 0 {
        return &vo.SearchResultVO{Items: []*vo.SearchItemVO{}}, nil
    }

    // 2) 查询向量（dense_weight=0 时不发这一路，也就不必花这次调用）
    var queryVec []float32
    if opts.UseKNN() {
        queryVec, err = s.embedder.EmbedQuery(ctx, opts.Query)
        if err != nil {
            return nil, err
        }
    }

    // 3) 按 search_mode 分组检索：最多两组，组内字段一致所以能合成一次请求。
    //    不取并集——那会悄悄把「只查标题」的库按正文搜（§7.2）
    groups := groupBySearchMode(kbs)
    pool := make([]knowledgerepo.SearchResult, 0, len(groups))
    for _, g := range groups {
        r, err := s.store.Search(ctx, s.buildReq(g, opts, queryVec))
        if err != nil {
            return nil, err
        }
        pool = append(pool, r)
    }

    // 4) 应用层加权 RRF 融合（Fuse 在同包 rrf.go，A3.2）
    //    逐组逐路喂进去；各组先拼成一个大列表会按分组顺序给出固定偏置
    routes := make([]Route, 0, 2*len(pool))
    for _, r := range pool {
        if len(r.BM25) > 0 {
            routes = append(routes, Route{Hits: r.BM25, Weight: 1 - opts.DenseWeight})
        }
        if len(r.KNN) > 0 {
            routes = append(routes, Route{Hits: r.KNN, Weight: opts.DenseWeight})
        }
    }
    merged := Fuse(routes, s.tuning.RankConstant, opts.Limit*fusionOversample)

    // 5) 存活校验 + 补全展示信息（1 次批量 SQL，不取内容——D1，§7.3）
    items, err := s.filterAlive(ctx, merged, kbs)
    if err != nil {
        return nil, err
    }

    // 6) 可选重排（P2）
    if opts.Rerank && s.rerank != nil {
        items, err = s.rerankItems(ctx, opts.Query, items)
        if err != nil {
            return nil, err
        }
    }

    if len(items) > opts.Limit {
        items = items[:opts.Limit]
    }
    return &vo.SearchResultVO{Items: items}, nil
}
```

### 7.2 混合检索

ES 原生的 `rrf` retriever 在 `basic` license 下不可用（报 non-compliant），带 `rank` 的 `sub_searches` 同样付费。核心检索不能绑在付费特性上，因此用**应用层 RRF 融合**。

对同一过滤条件发两个请求，并行：

```json
// 请求 1：BM25
{
  "size": 100,
  "_source": ["chunk_id"],
  "query": {
    "bool": {
      "filter": [ /* 见下 */ ],
      "should": [
        { "match": { "title":        { "query": "如何配置", "boost": 2.0 } } },
        { "match": { "content":      { "query": "如何配置" } } },
        { "match": { "heading_path": { "query": "如何配置", "boost": 1.5 } } }
      ]
    }
  }
}
```

```json
// 请求 2：kNN
{
  "size": 100,
  "_source": ["chunk_id"],
  "knn": {
    "field": "content_vec",
    "query_vector": [/* 1024 维 */],
    "k": 100,
    "num_candidates": 500,
    "filter": { "bool": { "filter": [ /* 见下 */ ] } }
  }
}
```

融合在应用层：

```go
// rrfScore = Σ_r w_r / (rank_constant + rank_r)，rank_constant 默认 60（配置项）
func fuseRRF(lists []RankedList, rankConstant int) []vo.ScoredChunkID {
    scores := make(map[string]float64, 256)
    for _, list := range lists {
        // rank 是**这一路内部**的名次，从 1 起算——不是拼成一个大列表之后的名次。
        // 拼完再排名次会让后拼接的路整体沉底，且路的先后影响结果（A3.2）
        for rank, id := range list.IDs {
            scores[id] += list.Weight / float64(rankConstant+rank+1)
        }
    }
    // 按分数降序，同分按 chunk_id 稳定排序（保证结果可复现）
    ...
}
```

单请求 `knn` 只能指定一个 `field`。本期先用一路 `content_vec`——`title` 的信息已通过 `heading_path` 与 BM25 进来；需要时再加，融合框架天然支持 N 路。

**filter**：

```json
{
  "bool": {
    "filter": [
      { "term":  { "account": "acc_001" } },
      { "terms": { "kb_id": [101, 102] } }
    ]
  }
}
```

`term(account)` 是硬隔离，绝不能省（D2）；指定了 `biz_tag` 时再加一个 `term(biz_tag)`。

**`search_mode` 落地**：

| `search_mode` | BM25 路字段 | kNN 路向量 |
|---|---|---|
| `title` | `title` + `heading_path` | `title_vec` |
| `title_and_content` | `title` + `content` + `heading_path` | `content_vec`（默认） |

**多库混查时按 `search_mode` 分组检索，不要取并集。** 一次查 3 个库，其中 1 个是 `title` 模式，如果直接"任一库开了内容检索就全局按内容检索"，就等于**悄悄改写了那 2 个标题模式库的语义**——它们建库时明确说了"只用标题"，结果被按正文搜了，召回里冒出一堆标题对不上、正文里却出现关键词的切片。

正确做法（融合本来就在应用层，分组几乎不增加复杂度）：

```text
kbs ──► 按 search_mode 分组 ──┬─► [title 组]             ──► Search(field=title_vec)    ─┐
                             └─► [title_and_content 组] ──► Search(field=content_vec)  ─┤
                                                                                       ▼
                                       每组各留两路（BM25 / kNN），各自按名次独立成表
                                                                                       │
                                                                                       ▼
                                                     加权 RRF（每路 rank 从 1 起，Σ w_r/(k+rank)）
```

分组依据是"检索字段组合"，不是库本身——同一个库里所有切片模式相同，所以分组天然按库来。`dense_weight` 的短路判定（下表）对每个分组独立生效：某组只该发 BM25 时，该组就只发一个请求。

**代价**：N 种 `search_mode` 就是 N 次 ES 请求，而不是 1 次。实际取值只有两种，且两组请求可以并行发。若某天 `search_mode` 变成每库可配的细粒度开关，这里要重新想——但那是产品形态变了，不是实现变了。

**`dense_weight` 调度**：

| `dense_weight` | 行为 |
|---|---|
| `<= 0.01` | 只发 BM25 |
| `>= 0.99` | 只发 kNN |
| 其他 | 两路都发，加权后融合 |

### 7.3 存活校验

```go
// ↑ 本段是**编排示意**，按「已经融合完的 vo.SearchResult」写，
//   为的是把「丢弃哪几类」讲清楚；真实签名收的是两路原始命中
//   `[]knowledgerepo.VectorHit`，融合在过滤**之后**做（§7.2）。
//   完整可落盘的代码见 A3.3。
func (s *SearchService) filterAlive(
    ctx context.Context,
    hits vo.SearchResult,
    kbs entity.KnowledgeBases,
) (vo.SearchResult, error) {
    // 1) 收集去重后的 doc_id（文档级，不是 chunk_id）——百级，不是千级
    docIDs := hits.UniqueDocIDs()

    // 2) 一次批量查文档表
    docs, err := s.docRepo.LoadByIDs(ctx, docIDs)
    if err != nil {
        return nil, err
    }
    docMap := docs.GetIDMap()

    // 3) 丢弃孤儿 / 幽灵（D4 的兜底）
    kbMap := kbs.GetIDMap()
    out := make(vo.SearchResult, 0, len(hits))
    for _, h := range hits {
        doc, ok := docMap[h.DocID]
        if !ok {
            continue                        // 孤儿：切片写了但文档行没落（§5.2 ③ 失败）
        }
        if doc.Deleted() {
            continue                        // 幽灵：MySQL 已软删，ES 还没清完（§5.4）
        }
        kb, ok := kbMap[h.KBID]
        if !ok {
            continue
        }
        h.DocName = doc.Name
        h.KBName = kb.Name
        out = append(out, h)
    }
    return out, nil
}
```

收集的是 `doc_id` 而非 `chunk_id`：批量 SQL 的 IN 列表是**去重后的文档数**（通常 < 50），不是召回条数——这正是 MySQL 只存文档级的原因（D3）。

这一步是整个设计的**兜底**：MySQL 的文档行是唯一的存活依据，前两步写挂了、删除清漏了，都在这里被静默丢掉——检索宁可少返回，也不返回错内容。

> **`LoadByIDs` 刻意不过滤 `delete_ts`**：上面两种丢弃要靠它返回的行来区分（孤儿 / 已删）。SQL 里加 `delete_ts = 0` 会让"已删"和"不存在"变成同一个 `!ok`，日志里就查不出到底是哪一种。
>
> **这一层的有效性依赖 `doc_id` 稳定**：它靠切片带的 `doc_id` 去查文档行，所以 §5.1 第 ④ 步必须复用已有行的 id（§5.3）。一旦重导入换了新 id，新切片查不到行、全被这里丢掉，而旧切片查得到行、原样返回。
>
> ⚠️ **但它挡不住"旧切片"**：重导入时 §5.2 的 ① ② 之间，同一文档的新旧切片 `doc_id` 相同、文档行也确实存在，两者都算"存活"。要挡它得靠代次比对，本期不做（§2 D4、§13 待定 9）。

### 7.4 分页

本期只做 top-N。RRF 融合在应用层，ES 的 `from/size` / `search_after` 作用不到融合后的排名——翻页要么把候选池放大到 `page × size`（成本随页码线性涨），要么维护游标。

- 只接受 `limit`（默认 10，上限 100），不接受 `offset`。
- 需要"看更多"时提高 `limit` 或收窄查询。

---

## 8. 切片算法

### 8.1 结构感知（markdown 标题树）

```text
输入：markdown 文本
  │
  ├─ ① 建标题树（buildHeadingTree）
  │     状态机扫描，识别代码围栏 / 缩进代码块 / 表格
  │     这些区域内的 # 不是标题 —— 必须跳过，否则切错
  │
  ├─ ② DFS 切分（extractSlices）
  │     在 node.Level >= splitLevel 处切开（默认 level 2）
  │
  ├─ ③ 贪心打包（buildContentSlices）
  │     availableSize = chunkSize - prefixLen - 2
  │     超出则物理切分；剩余 < 50 字符则并入前一片
  │
  └─ ④ 标题链回放（prefix）
        祖先标题链拼成 prefix，回放到每个切片开头
        "产品介绍，概述，配置方法\n\n<正文>"
```

第 ④ 步是廉价版的 Contextual Retrieval：每个切片自带上下文路径，向量化时自然进入 embedding，召回准确率显著优于裸切片，而成本只是字符串拼接。

| 参数 | 默认 | 说明 |
|---|---|---|
| `chunk_size` | 800 | 目标字符数 |
| `split_level` | 2 | 切到几级标题 |
| `min_chunk` | 50 | 小于此值并入前一片 |

单切片超长则拒绝并报 `ErrChunkTooLarge`（embedding 有输入长度限制）。

### 8.2 兜底切片器

非结构化纯文本用递归分隔符：段落 → 换行 → 空格 → 字符。

`Splitter` 按 `format` 选实现：markdown → 结构感知；text → 递归分隔符；chunks → 透传归一化。

### 8.3 overlap（**P2，本期不实现**）

`ChunkOptions` 里**没有 `Overlap` 字段**，两个切片器都不做 overlap。原因是两者的情况不一样：

- **结构感知**：不该 overlap。标题链 prefix 已经提供了上下文，overlap 反而会把上一节的尾巴粘进下一节的标题链里——**破坏结构边界**，那是它唯一的优势。
- **递归分隔符兜底**：理论上该有 10~15% overlap（纯文本没有结构，靠 overlap 兜住被切断的句子）。但本期这条路径只是兜底，真实语料以 markdown 为主，先不做。

要做的话落点在 A2.16 的兜底切片器：加一个 `Overlap int` 参数、按 rune 回退若干字符。注意**别把它塞进结构感知切片器**——那两者的取舍是相反的。

---

## 9. 索引策略

### 9.1 单索引

见 D2。隔离靠字段不靠物理隔离：

```json
{ "term": { "account": "acc_001" } }
```

`account` 必须 `keyword`，且每个查询都必须带——这是唯一的租户隔离手段，漏了就是越权。仓储层每个方法都带 `account` 参数，越权在数据访问层就失败（§6）。

### 9.2 扩容路径（预留，不实现）

切片量上来后，把 `account → index` 的映射从"恒等"换成"分桶/独占"，查询侧改成按 `terms(account)` 分组、分组并行。只动 `IVectorStore` 实现与少量编排，领域层与应用用例不变——这也是 `IVectorStore` 做成接口的原因。

#### 关于 ES 的 `_routing`（评估过，本期不用）

ES 有个 `_routing`，把文档按 routing 值哈希钉到某个分片。看起来很像"按租户分片"，所以单独说清楚——**它不是隔离手段**。

本地 ES 9.5.5 实测（6 租户 / 3 分片 / `routing=account`）：

```text
分片 0: acc_4, acc_5
分片 1: acc_1, acc_2, acc_3      ← 三个租户挤在一个分片
分片 2: acc_6

routing=acc_1 的查询命中: ['acc_1','acc_2','acc_3']    ← 另外两个租户的数据照样返回
match_all（不带 routing）命中: 全部 6 条                ← routing 完全不过滤
```

routing 买到的是**分片局部性**（扫 1 个分片而不是 3 个），`term(account)` 一个字都不能省。而它带来的新风险是**静默漏数据**：

```text
routing=acc_1（数据在分片 1）去查 acc_6（数据在分片 2） → 0 条，不报错
同一查询不写 routing                                  → 1 条
```

所以真要用，mapping 必须带 `"_routing": {"required": true}`，否则漏填的文档从此刻起用 routing 永远查不到（实测漏填会 400 `routing_missing_exception`，这个开关必须开）。

不用它的理由：① 它就是 D2 里判过"亿级才需要"的分桶复杂度；② 它**让热分片更严重**——大租户语料全压一个分片，而默认 routing 至少均匀散；③ 它是**单向门**，`_routing` 写入时定死，事后加 = 全量重导。租户分布还不知道的情况下，没有理由现在锁死。等真出现"跨租户查询扇出成瓶颈"，加 `_routing` + `required`、查询侧带 `routing=account`、重导一次即可。

### 9.3 已知代价

- **热点分片**：大租户集中打热分片。量级未到，先接受（§9.2 是出路）。
- **mapping 变更需全量重导**：单索引没有"换索引 + 切别名"的平滑机制，换 embedding 维度 = 停写重导。
- **旧切片暂时占存储**：重导入时，`DeleteExcept`（§5.2 ②）失败的话旧切片会一直躺着，要等下一次重导入才清。极端情况下 ES 里会同时存在同一文档的新旧两份内容——这是刻意的，换来"写失败不丢内容"。要监控的话看 `doc_id` 去重后的切片数与 MySQL `chunk_count` 的比值，明显大于 1 就说明清理跑得不勤。

### 9.4 索引的创建时机

**`EnsureIndex` 只在启动时调一次，不在写入路径上。**

```text
cmd/server 启动 ──► fx OnStart ──► vectorStore.EnsureIndex(ctx) ──┬─ 成功 → 继续启动
                                                                  └─ 失败 → 拒绝启动
```

早先的写法是在每次 `Ingest` 里调一次（"这样部署时不用手工建索引"），这是把**部署期动作塞进了数据通路**，代价是：

| 问题 | 说明 |
|---|---|
| 失败点错位 | 索引建不出来/配置错，应该在**启动**时炸（有运维看着），而不是在第一个用户导入时 |
| 每请求一次往返 | `IndicesExists` 每个导入请求都发 |
| 是个假保证 | `EnsureIndex` 只看"索引在不在"，**不校验 mapping**（dense_vector 的 dims 不可变，校验了也修不了）。给的是"索引存在"的安心感，不是"索引正确" |
| 权限 | 运行时凭据因此需要建索引权限；挪到启动后，运行时可降为纯数据读写 |
| 并发 | 多副本同时首次启动会并发 PUT，实现需容忍 `resource_already_exists_exception`（A4.6 有这个处理，保留） |

**`EnsureIndex` 的端口方法保留**——启动期仍要调它，只是调用点从"每个请求"变成"进程一次"。

`deploy/README.md` 里那句手工建索引的 `curl` 于是只剩一个用途：**冒烟测试**——确认 mapping 能被集群接受、IK 分词器真的生效。它不是部署步骤。

> 启动期建索引意味着**服务启动前 ES 必须可达**。这是有意的 fail-fast：宁可起不来，也不要带着"索引其实不对"的状态服务请求。若编排上必须先起进程再等依赖，就在 `OnStart` 里做有限次退避重试，仍然失败则退出。

---

## 10. 关键接口

### 10.1 向量存储

```go
// domain/repository/knowledge/vector_store.go
type IVectorStore interface {
    // 索引长什么样（维度、分片、分词器）是部署环境的事实，
    // 由实现从配置读——端口里不该出现 Analyzer/Shards 这类 ES 概念
    EnsureIndex(ctx context.Context) error

    Save(ctx context.Context, docs []VectorDoc) error

    // Refresh 强制刷新索引，让刚写入的切片立刻可检索。
    //
    // 两个用途：重导入时让 DeleteExcept 看得见「还没刷出来的旧切片」
    // （§5.2 ② 的前提），以及调用方要求「导入即可搜」（§4.2）。
    // 粒度是整个索引（ES 没有按文档刷新），别放进常规写入路径。
    Refresh(ctx context.Context) error

    // DeleteByQuery 删除命中的**全部**切片（文档删除 / 整库删除用）
    DeleteByQuery(ctx context.Context, filter VectorFilter) (int64, error)

    // DeleteExcept 删掉 filter 命中里 _id 不在 keepIDs 中的那些（§5.2 ②）。
    // 与 DeleteByQuery 分开是有意的：这两个的杀伤面差一个数量级，
    // 合成一个方法、靠参数区分，早晚会有人把"留差集"传成"全删"。
    DeleteExcept(ctx context.Context, filter VectorFilter, keepIDs []string) (int64, error)

    // 发 BM25 + kNN 两路，各自原样返回、不融合——融合在应用层（§7.2），
    // 这样换引擎时融合权重策略不受影响
    Search(ctx context.Context, req VectorSearchReq) (SearchResult, error)
}
```

端口类型（`VectorDoc` / `VectorFilter` / `VectorSearchReq` / `VectorHit` / `SearchResult`）与接口同包，定义在 `domain/repository/knowledge/vector_store.go`——
基础设施只认这些，不认 `entity`。向量化**不在仓储里做**（仓储不调外部 API），
由应用层调 `IEmbeddingService` 算好后经 `factory.BuildVectorDocs` 组装（§5.1）。
实现：`infrastructure/persistence/knowledge/vector_store_es.go`。

关键字段（完整定义见 A2.10）：`VectorDoc.ChunkID` 既是 ES 的 `_id`，也是 §5.2 第 ② 步差集清理的「保留集合」；`VectorHit.DocID` 由 `Search` 原样带回来，供 §7.3 做存活校验。

### 10.2 Embedding

```go
// domain/interfaces/embedding_service.go
type IEmbeddingService interface {
    // 非对称编码：query 与 doc 可能用不同前缀/指令
    EmbedDocs(ctx context.Context, texts []string) ([][]float32, error)
    EmbedQuery(ctx context.Context, text string) ([]float32, error)

    Dim() int
    Model() string
}
```

区分 `EmbedDocs` / `EmbedQuery` 是必要的：**非对称**模型（E5 的 `"query: "` / `"passage: "`、bge-large-zh 的中文指令）检索时要给 query 加指令前缀，doc 侧不加，混用会显著掉分。

> **本期选的 `bge-m3` 是对称模型，不加前缀**——所以 `query_prefix` 在配置里留空，
> 不是忘了填。早先这里把 bge-m3 和 E5 并列举例，是错的：bge-m3 从不使用检索指令，
> 给它加 `"query: "` 反而是把分布推偏。

实现：`embedding_openai.go`——OpenAI 兼容协议，可覆盖火山引擎 / 通义 / vLLM / 自建 model_proxy / Ollama。

### 10.3 配置面

`infrastructure/config/config.go` 需补（当前骨架只有 MySQL / Redis / 雪花）：

```jsonc
{
  "elasticsearch": {
    "addrs": ["http://127.0.0.1:9200"],
    "index": "fastrag",
    "username": "", "password": "",
    "dim": 1024, "analyzer": "ik_max_word", "search_analyzer": "ik_smart",
    "shards": 3, "replicas": 1, "refresh_interval": "30s"
  },
  "embedding": {
    "base_url": "", "api_key": "", "model": "", "dim": 1024,
    "batch_size": 32, "timeout_ms": 30000,
    // 非对称模型（E5、bge-large-zh）检索时要给 query 加指令前缀，doc 侧不加。
    // 前缀写错不会报错，只是效果变差——很难归因，所以值得单独配一项。
    // 本期的 bge-m3 是对称模型，留空
    "query_prefix": ""
  },
  "search": {
    "bm25_top": 100, "knn_top": 100, "num_candidates": 500, "rank_constant": 60,
    // 默认稠密权重；1.0 = 纯向量，0.0 = 纯 BM25。请求体可覆盖
    "dense_weight": 0.5
  },
  "security": {
    // 必须显式为 true 才启动，见 §6.1。默认 false = 拒绝启动
    "trust_request_account": false
  }
}
```

> **本期不做限流。** 早先设计过一套 Redis 滑窗（`EmbedDocs` 让路、`EmbedQuery` 保底），已整块移除——
> 这是 demo，先把核心链路跑通。代价要说清楚：**批量导入会无节制地打 embedding 接口**，
> 并发度只受 §5.5 的固定工作池（4）约束，没有全局配额概念。
> 真要多租户上量，这一块得补回来，见 §13 待定。

> **仓库里的 `config.json` 与上面这段有出入，以仓库为准。** 差异都是本机 demo 环境的取值，
> 不是配置项本身的差异：`shards: 1 / replicas: 0`（单机一台 ES，副本数给 1 只会一直黄）、
> `batch_size: 16 / timeout_ms: 60000`（本地 Ollama 首次加载模型慢，超时给宽一点）、
> `bm25_top / knn_top / num_candidates = 50 / 50 / 200`（demo 语料小，取小值省算力）、
> `trust_request_account: true`（§6.1 的闸门，本机跑必须显式打开）。
> **上面那段保留的是"生产该怎么填"，不是"现在填了什么"**——两者的取舍不同，
> 混在一起读会以为 `replicas: 1` 已经在跑了。

---

## 11. 一致性

**不变量**：

> **I1**：查询**不会返回 MySQL 里不存在或已删除的文档的内容**。孤儿切片、幽灵切片都会被存活校验丢掉（§7.3）。
> **I2**：查询返回的每一条，其文档在 MySQL 中必然存在且未删除。
> **I3**：`doc.BizTag` 与 `account` 一样是硬边界——ES 侧 `term` 过滤 + 仓储层 `account` 条件，两者都不靠应用层自觉。

> ⚠️ **I1/I2 到此为止，不包含"内容只有单一版本"。** 本期刻意不做一致性增强，于是有两类内容层面的缺口——见下表第 3、4、5 行与 §2 D4：
>
> - **重复**：重导入窗口内、或 ② 失败时，同一文档的新旧切片一起被召回；
> - **截断**：并发同名导入时，两次 `DeleteExcept` 互相删掉对方的内容，只剩两份的交集（§5.5）。
>
> 这两类都是内容层面的问题，**不是越权、也不是读到已删数据**——文档确实存在。但第 5 行的截断**不能算"可过滤的一侧"**：它返回了一份不完整的内容，调用方拿它做业务判断会出错。

| # | 场景 | 检测 | 后果 | 处理 |
|---|---|---|---|---|
| 1 | ① ES 写失败 | 调用失败 | 什么都没发生 | 重试导入 |
| 2 | ①成功、③ MySQL 写失败 | 无 | 新文档：孤儿切片 → 搜不到；老文档：旧内容还在，计数 / 指纹滞后 | 对账重算（A5.1） |
| 3 | ② `DeleteExcept` 失败 | 无 | 旧切片残留 → 同一文档返回新旧两份内容 | 不影响"不返回错文档"，但会重复召回；下次重导入再清 |
| 4 | 重导入 ① 与 ② 之间 | 无 | 同上：窗口内新旧切片同时可见 | 设计使然（§5.2）。**修它要引入代次机制，本期不做** |
| 5 | 并发导入同一文档 | 无 | 行**已存在**时：两次 `DeleteExcept` 互删，只剩交集 → **内容被截断**。行**不存在**时：MySQL 唯一键只让一个建行，落败者的切片成孤儿（安全），但 `chunk_count` 被覆盖成落败者的切片数 → 计数漂移 | 明确不支持（§5.5）。**调用方必须按 `doc_name` 去重**；漂移部分靠对账重算（A5.1） |
| 6 | 文档删除，ES 删失败 | 无 | 幽灵切片 | 存活校验按 `doc.Deleted()` 过滤（§7.3） |
| 7 | 计数漂移 | 对账 | 列表页数字不准 | 重算 `chunk_count` |
| 8 | embedding 不可用 | 调用失败 | 导入 / 检索阻塞 | **本期无重试与限流**，调用方自行重试（§10.3） |
| 9 | mapping 变更（换维度） | 人工 | 需全量重导 | 停写 → 新建索引 → 重导 → 切 |

`chunk_count` 用差值维护（§5.1 第 ⑧ 步），删除与异常仍可能漂移，后续用对账重算回写。

> **I1 的边界要说准**："可过滤"意味着**漏召回**和**重复召回**，但**绝不含"返回已在 MySQL 里不存在或已删除的文档的内容"**。
>
> ⚠️ **本期有三个已知例外，都是"推迟一致性增强"换来的**（§2 D4、§13 待定 9）：
>
> 1. **重复召回**：重导入时 §5.2 的 ① ② 之间，同一文档的新旧切片同时被检索到。受 ES 30s 刷新周期影响，窗口可能持续到一个刷新周期。
> 2. **重复召回**：② `DeleteExcept` 失败，旧切片一直躺着，直到下次重导入。
> 3. **内容截断**：并发导入同一文档（§5.5）没有串行化点，两次删差集互删，只剩交集。**批次内**的同名已被 `BatchIngest` 同步预扫挡掉（§5.5、A3.1），剩下的只有**跨请求**并发这一种。
>
> 前两条是重复，**第三条是内容不完整**，严重程度不同。三者都要一个串行化点才能堵住，也就是那套被推迟的代次协议。

---

## 12. 分期交付

### P0 · 可检索

- [ ] 骨架：fx 装配 + 配置 + MySQL / ES 驱动
- [ ] 领域模型：`KnowledgeBase` / `KnowledgeDoc` / `Chunk` + 工厂
- [ ] 切片：结构感知（markdown 标题树）
- [ ] ES 单索引 + 启动期 `EnsureIndex`（幂等，§9.4）
- [ ] 导入：`POST /api/v1/docs`（ES 切片 → ES 清旧切片差集 → MySQL 文档行 + 计数，§5.2）
- [ ] 检索：混合检索（kNN + BM25，应用层 RRF 融合）——`term(account)` 在 ES 侧过滤，存活校验在应用层（§7.3）
- [ ] **验收**：给一篇 markdown，导入后**在一个刷新周期内**（默认 30s）检索到，且内容与源文一致

### P1 · 多租户与运维

- [ ] 租户隔离：仓储层强制带 `account` 条件（§6）
- [ ] 启动期强制开关 `security.trust_request_account`（§6.1）
- [ ] 删除编排：文档 / KB 软删 + ES `DeleteByQuery`（§5.4）
- [ ] 批量导入（受限并发 + 单文档错误隔离 + 批内同名拒绝，A3.1）
- [ ] 计数校正
- [ ] **验收**：两个租户的数据互相搜不到；删文档后立刻搜不到；**冒烟回路 ①导入→②检索→③重导入→④再检索 只搜得到新内容**（连做两轮，判定表见 §12.1）

### P2 · 增强

- [ ] **写侧一致性增强（代次切换）**——重导入/并发写同一文档时的可见性裁决，`ver` 机制，**demo 阶段明确不做**，见 §2 D4、§13 待定 9
- [ ] 写侧限流（Redis 滑窗 / 令牌桶）——**demo 阶段明确不做**，见 §10.3
- [ ] embedding 调用重试 + 熔断
- [ ] Rerank 集成
- [ ] `biz_tag` 多值 / 元数据过滤
- [ ] `IVectorStore` 第二实现（Milvus / Qdrant）

### 12.1 验收基线

P0/P1 的"验收"如果不写数，就只是"跑通了"。下面这一节的意义是把"跑通"和"能用"分开。

**先说一条前提**：检索质量**没有现成基线可抄**。`chunk_size` / `rank_constant` / `bm25_top` / `knn_top` / 双向量字段这些参数，在别的语料上的最优值未必适用于你的语料。所以第一件事不是调参，是**先建一个可复现的评测集**：

> 从真实语料里挑 **50～100 条**真实问题，人工标注每条的正确答案切片（recall 的 ground truth）。这事看着土，但没有它，后面所有"调参"都是凭感觉。

| 类别 | 指标 | 怎么测 | 起始目标 |
|---|---|---|---|
| 质量 | Recall@10、MRR@10 | 上面那个标注集，跑 `scripts/eval`（P1 补） | 先量出**当前值**再定目标；低于 0.7 就不该上生产 |
| 质量 | 多租户泄漏 | 构造 A 租户的 50 条 query 打到 B 租户语料 | **必须 0 命中**。这条是硬门槛，不是指标 |
| 延迟 | 检索 P95 / P99（含 embedding） | 压测，单机单副本 | P95 < 800ms；ES 侧（不含 embedding）P95 < 200ms |
| 延迟 | 导入 P95（单篇 10k 字 markdown） | 顺序导入 100 篇取分位 | < 5s（取决于 embedding 供应商） |
| 吞吐 | 导入吞吐（切片/秒） | 批量导入 10 万切片 | 只受 embedding 配额与 §5.5 工作池(4)限制 |
| 容量 | ES 磁盘 / 内存 | 见下方估算 | —— |
| 恢复 | 单文档重导耗时；对账全量耗时 | 手工触发 | 重导 = 单篇导入耗时；对账 ≈ 文档行数 / 1000 秒 |
| 规模 | 租户数 × 每租户文档数 × 每文档切片数 | 假设值，**需产品确认** | 见下方（注意上界是 10⁹ 切片，不是 3000 万） |

**容量估算**（这是设计里最容易被低估的一项）：

```text
单切片 ≈ 向量 1024 × 4B × 2 个 = 8 KB
        + 正文 ~800 字 × 3B(中文) = 2.4 KB
        + 标题链 / 元数据          ≈ 0.5 KB
        ≈ 11 KB

× (1 + replicas=1)  = 22 KB / 切片   ← 副本也算盘
× 旧切片残留 ~1.2x    ≈ 26 KB / 切片   ← §5.2 ② 清理滞后时同一文档会有两份

1000 万切片 ≈ 260 GB
```

**规模假设**（写死在设计里的是错的做法，但必须有个数才能估容量）。

这里要分清**目标规模**和**上界**——上一版把两者混成了一行，还乘错了：

| 项 | 目标规模（设计按此做） | 上界（模型在此处失效） |
|---|---|---|
| 租户数 | ≤ 1 万 | —— |
| 每租户文档数 | 百 ~ 千 | 1000 |
| 每文档切片数 | 10 ~ 100 | 100 |
| **MySQL 文档行** | 10⁶ | **10⁷**（1 万 × 1000）——InnoDB 仍扛得住，但已不该是单库单表 |
| **ES 切片总数** | **10⁷** | **10⁹**（1 万 × 1000 × 100）——**单索引多租户在这量级不成立** |

对上前文的容量估算（26 KB / 切片）：

```text
目标 10⁷ 切片 × 26 KB ≈  260 GB   ← 单索引 3 分片，够用
上界 10⁹ 切片 × 26 KB ≈  26 TB    ← 需要分索引 / 分桶 / 只留热数据
```

所以「`_routing` 分桶」和「大租户独占索引」（§9.2）不是可有可无的优化，而是**目标规模翻 100 倍之后的必需品**——本期不做是因为目标规模在 260 GB 这一档，不是因为上界只有 3000 万。

> 这几个数目前是**假设**，不是需求。落盘后第一件事是找产品确认，因为 `shards` / `num_candidates` / 是否需要分桶，全都挂在这上面。

**冒烟回路（比指标更早的一步）**：落盘后第一条要跑的既不是评测集也不是压测，而是这条四步回路——它覆盖的是本期改动最集中、也最容易静默出错的那条路径：

```text
① 导入一篇 → ② 检索，能搜到 → ③ 改一个字的正文，重导入 → ④ 再检索，只应搜到新内容
```

第 ④ 步是最关键的一步，也是最需要在**真 ES** 上验的一步——原因见 §5.2：清旧切片依赖 §5.2 ② 之前那次刷新，刷不上就时灵时不灵。**只跑一遍不算过**，因为它可能刚好落在刷新之后：

| 检查 | 方法 | 期望 |
|---|---|---|
| 旧切片真的没了 | 按 `doc_id` 聚合数切片（Kibana：`terms` agg on `doc_id`，或 `_count?q=doc_id:N`） | 只剩新内容那批，条数 == MySQL 的 `chunk_count` |
| 新内容真的能搜到 | 用只有新正文里才有的词检索 | 命中 |
| 旧内容搜不到 | 用只有旧正文里才有的词检索 | 不命中 |
| 连续两次导入 | 把 ①→④ 连做两轮，间隔 < 30s | 第二轮同样只剩新切片（盯 §5.2 ② 的刷新缺口） |
| 批内同名被拦 | 一次 `POST /docs/batch` 里塞两条同 `doc_name` | 该两条均出现在 `errors` 里，其余照常成功 |

> MySQL `chunk_count` 与 ES 切片数的差值是判断"§5.2 ② 有没有漏删"的直接手段，也是 §A5.1 对账命令真正要对上的东西。这条对不上，后面所有检索质量指标都建在流沙上。

**实测记录（2026-10-09，本机单机环境）**：上表五行**全部通过**。

| 检查 | 实测 |
|---|---|
| 旧切片真的没了 | 重导入后 ES `_count` = 3，与 MySQL `chunk_count` = 3 一致；旧标记词 0 命中 |
| 新内容真的能搜到 | 新标记词 1 命中 |
| 旧内容搜不到 | 旧标记词 0 命中 |
| 连续两次导入 | 再连做两轮，ES 恒为 3，`doc_count` / `chunk_count` 稳定在 1 / 3 |
| 批内同名被拦 | 两条同 `doc_name` 均进 `errors`，其余照常成功，整批 200 |

跑这个回路的价值不在"确认它能用"，而在**它逮到了两个真 bug 和一处启动缺口**，三个都是读代码看不出来的：

1. **重导入后旧切片时有时无**（A6 第 7 条、A4.6）。ES 的可见性有两档延迟，
   代码只堵了"让删除看得见旧切片"那一档，漏了"让删除结果被检索看见"那一档。
   症状恰好是"刚导入完查是 5 条，隔 40s 再查是 3 条"——**只有把 §12.1 那条
   "连做两轮、间隔 < 30s"真跑了才会撞上**。
2. **每次重导入都报 `created = true`，KB 的 `doc_count` 越滚越大**（A4.4）。
   判定条件用了"主键是不是我刚生成的那个"，而重导入恰恰**复用**库里那行的 id，
   于是条件恒真。实测同一篇重导入两次，`doc_count` 从 1 变成 3。
   这条不在上面那张表里——它是靠"顺手多导入一次看看计数"发现的。
3. **服务打了满屏 `[Fx] RUNNING` 却没监听任何端口**（A4.16）。
   `ginsdk.HTTPServer` 不会自己 `ListenAndServe`，附录漏了这一句。

> 三个问题的共同点：**都不报错**。第 1 个要等 30s 才自愈、第 2 个只是计数慢慢变大、
> 第 3 个压根没有请求发出去。这正是把冒烟回路排在压测和评测集之前的原因——
> 指标测不出"静默地做错了"。

**仍未验证的部分**：上表之外的质量与性能指标（Recall@10、P95/P99、容量）**一条都没测**。
它们要么需要标注集，要么需要压测环境，都不在本期范围内（§12.2 P1）。

---

## 13. 待定

| # | 问题 | 当前取值 | 备选 |
|---|---|---|---|
| 1 | embedding 模型与维度 | **已定：`bge-m3`，1024 维**（本机 Ollama 提供，`/v1/embeddings`）。索引已按 1024 建好，两者对齐**实测过**：一次请求塞 2 条文本，回来 2 个 1024 维向量 | 换模型 = 必须重建索引重导（`dims` 建好后改不了，§9.4、§11 场景 9） |
| 2 | 检索深翻页 | 只做 top-N | 单路 `search_after` + 客户端二次融合 |
| 3 | `account` 形态 | 字符串 | 纯数字 ID（`_routing` 若要启用，数字型更省） |
| 4 | `rank_constant` | 60（RFC 标准值，**未调**） | 需用语料评测集验证（§12.1） |
| 5 | `biz_tag` 单值 vs 多值 | 单值 | 多值需改 JSON 列 + ES 多值 keyword |
| 6 | 是否需要 gRPC 接入 | 先只做 HTTP | 同时提供 gRPC |
| 7 | **写侧限流** | **不做（demo）** | Redis 滑窗；或调用侧网关限流（§10.3） |
| 8 | **embedding 失败重试** | **不做** | 指数退避重试 + 熔断；当前靠调用方重试（§11 场景 8） |
| 9 | **写侧一致性（代次切换）** | **不做**——重导入/并发写同一文档时，新旧切片可能同时在 ES 里，检索会重复召回。**窗口已从"一个刷新周期"压到"一次请求内的几十毫秒"**（A4.6 的 `WithRefresh(true)`），但没消除 | 四步协议：MySQL 预占 `ver`（`ver+1` 读回，唯一串行化点）→ ES 写带 `ver` 的切片 → 删 `ver` 更小的 → MySQL 带 `ver` 条件提交。代价：`knowledge_doc` 多一列、ES mapping 多一字段、仓储多两个方法（`ReserveVer`/`CommitIngest`）、查询侧多一次比对。**要等核心功能做完再上**（§2 D4） |
| 10 | **多 `search_mode` 混查的并行度** | 每种模式一组，组间串行 | 组间并行（§7.2） |
| 11 | **可见性 SLA** | 一个刷新周期（30s）；重导入路径必刷，首次导入可传 `refresh: true`（§4.2） | `refresh_interval: 1s`（贵）；或把 `_refresh` 收窄到具体分片 |
| 12 | 租户 / 切片规模 | 假设值，**待产品确认** | 直接决定分片数与是否启用 `_routing`（§12.1） |
| 13 | **服务端口** | `8080`（仓库 `config.json`）。本机 8080 上原本还挂着另一个无关项目，**已停掉**，现在 8080 由本服务独占。冒烟期间曾临时跑在 8090 | 改配置即可。注意 `startHTTPServer` 的探测只挡得住**完全冲突**，挡不住 `*:8080` 与 `127.0.0.1:8080` 的**部分重叠**（A4.16） |

---

# 附录 A · 完整代码（P0 + P1）

> **怎么读**：按层给全量代码，每个文件标题下的路径就是它该待的位置。
>
> ⚠️ **本附录的时效已过：代码已经落盘、编译、跑通并完成冒烟验收（§12.1）。**
> 仓库里的真实文件才是事实来源；本附录与它有出入时，以代码为准。
> 落盘过程中发现并修掉的三处问题——ES v9 的命名空间 API、`c.Data` 之外的
> 演示页路由、以及 `ginsdk.HTTPServer` 不会自启——都已回写进对应小节，
> 但**不敢保证没有别的出入**：这一节最初是按记忆写的，不是照着源码抄的
> （详见 A6 第 1 条）。

## A0 · 前置说明

### A0.1 需要新增的依赖

```bash
go get github.com/elastic/go-elasticsearch/v9@v9.4.2
```

`go.mod` 目前**没有** ES 客户端。版本与本机已有的 `9.5.5` 镜像对齐（同 v9 大版本）。

Embedding 客户端用标准库 `net/http` 手写，不引第三方 SDK——OpenAI 兼容协议就是一个 POST。

### A0.2 与现有骨架的关系

骨架里那套「知识库 CRUD」(`knowledge_bases` 表 + `entity/knowledge.KnowledgeBase` + 5 个接口) 是**上一版的产物**，
本设计已经把 KB 管理移出范围（§1.2）。落盘时：

| 现有文件 | 处置 |
|---|---|
| `domain/entity/knowledge/knowledge_base.go` | **替换**（字段全变，见 A2.3） |
| `domain/repository/knowledge/knowledge_base.go` | **替换**（改成只读端口，见 A2.8） |
| `domain/repository/knowledge/retriever.go` | **删除**，其职责被 `vector_store.go` 取代（A2.10） |
| `infrastructure/persistence/knowledge/retriever_stub.go` | **删除**，被 `vector_store_es.go` 取代（A4.6） |
| `infrastructure/persistence/knowledge/index_template.go` | **保留**，只订正一处过期注释（A4.7） |
| `infrastructure/controller/http/knowledge/controller.go` | **替换**为 `doc` + `search` 两个 controller（A4.13 / A4.14） |
| `common/dto/knowledge.go`、`common/vo/knowledge.go` | **替换** |
| `infrastructure/persistence/migration/schema.sql` | **基本不动**——`knowledge_doc` 的列本设计全用得上，没有新增列（§4.1） |
| `infrastructure/driver/redis/*` | **保留但不上图**——它原本只服务限流器，限流本期不做（A4.9）。`fx` 里不再 provide，避免起一个没人用的连接 |

### A0.3 相对 §10.1 的签名修正

| 原写法 | 改成 | 为什么 |
|---|---|---|
| `EnsureIndex(ctx, spec vo.IndexSpec)` | `EnsureIndex(ctx) error` | `IndexSpec` 里有 `Analyzer/Shards/RefreshInterval`，全是 ES 概念。端口带它就等于把 ES 泄进 domain；实现自己从配置读即可 |
| `BuildDocs(ctx, kb, docID, chunks)` | 删掉，拆成 `factory.BuildVectorDocs` + `Save` | 仓储不该调 embedding API（上一轮讨论过） |
| `HybridSearch(...) ([]VectorHit, error)` | `Search(...) (SearchResult, error)`，`SearchResult{BM25, KNN}` | 原签名说"返回两路交应用层融合"却只返回一个列表，自相矛盾 |
| `DeleteByQuery(ctx, filter)` | 保留，**另加** `DeleteExcept(ctx, filter, keepIDs)` | 重导入要"保留新集合、删掉其余"，这是 `DeleteByQuery`（删全部）表达不了的。杀伤面差一个数量级，不该共用一个方法靠参数区分 |

> 还有一处**不是签名**的改动：`EnsureIndex` 的**调用点**从每次导入挪到启动期（§9.4）。端口方法本身不变。

---

## A1 · common 层

### A1.1 `common/constants/knowledge.go`

```go
package constants

// 导入形态：调用方给什么，FastRAG 就按什么处理
const (
	FormatMarkdown = "markdown" // 给 markdown 原文，FastRAG 负责切
	FormatText     = "text"     // 给纯文本，走递归分隔符兜底
	FormatChunks   = "chunks"   // 给已切好的切片，只做索引
)

// 检索模式（knowledge_base.search_mode）
const (
	SearchModeTitle           = "title"             // 只查标题
	SearchModeTitleAndContent = "title_and_content" // 标题 + 正文
)

// 知识库类型（knowledge_base.knowledge_type）
const (
	KnowledgeTypeOrdinary = "ordinary"
	KnowledgeTypeQA       = "qa"
)

// 切片默认参数
const (
	DefaultChunkSize  = 800
	DefaultSplitLevel = 2
	DefaultMinChunk   = 50
)

// 检索默认值
const (
	DefaultSearchLimit = 10
	MaxSearchLimit     = 100
	MaxIngestChunks    = 5000 // 单文档切片数上限，超过基本可以断定是切错了
)

// 向量维度上限，与 IndexSpec.Validate 保持一致
const MaxVectorDim = 4096
```

### A1.2 `common/errors/errors.go`（增补）

在现有文件的错误码区追加：

```go
// 导入相关错误码
const (
	CodeDocChunkEmpty    = 10201 // 切片结果为空
	CodeChunkTooLarge    = 10202 // 单切片过长
	CodeDocIngestFail    = 10203 // 导入失败
	CodeDocDeleteFail    = 10204 // 文档删除失败
	CodeEmbeddingFail    = 10205 // embedding 服务异常
	CodeVectorStoreFail  = 10206 // 向量库异常
	CodeIndexInitFail    = 10207 // 索引初始化失败
)

// 检索相关错误码
const (
	CodeSearchFail = 10301 // 检索失败
)

var (
	ErrDocChunkEmpty      = NewBizError(CodeDocChunkEmpty, "chunk result is empty")
	ErrChunkTooLarge      = NewBizError(CodeChunkTooLarge, "chunk too large for embedding")
	ErrDocIngestFailed    = NewSysError(CodeDocIngestFail, "doc ingest failed")
	ErrDocDeleteFailed    = NewSysError(CodeDocDeleteFail, "doc delete failed")
	ErrEmbeddingFailed    = NewSysError(CodeEmbeddingFail, "embedding service failed")
	ErrVectorStoreFailed  = NewSysError(CodeVectorStoreFail, "vector store failed")
	ErrIndexInitFailed    = NewSysError(CodeIndexInitFail, "index init failed")
	ErrSearchFailed       = NewSysError(CodeSearchFail, "search failed")
)

// NewParamError 生成带自定义文案的参数错误。
// 基础错误 ErrInvalidParam 的 msg 是固定串，Params() 走的是 Sprintf，
// 没有占位符时参数会被丢掉，所以另开一个构造函数。
func NewParamError(msg string) *BizError {
	return NewBizError(CodeInvalidParam, msg)
}
```

> **删掉了一个错误码**：原稿有 `CodeKnowledgeBaseMismatch` / `ErrKBMismatch`（映射到 403），
> 用来区分「库不存在」和「库不归你」。这个区分本身就是漏洞——它把接口变成了租户枚举器（§6 第 3 条）。
> 两种情况统一返回 `ErrKnowledgeBaseNotFound` → 404（A4.3 实现），所以这个码连同 `gingext` 里的 403 映射一起去掉。

### A1.3 `common/dto/doc.go`

```go
package dto

// ChunkDTO 调用方直接给出的切片（format = chunks 时使用）
type ChunkDTO struct {
	Title   string `json:"title" binding:"max=512"`
	Content string `json:"content" binding:"required"`
}

// SplitOptionsDTO 切片参数，可选，覆盖 KB 上的默认值
type SplitOptionsDTO struct {
	ChunkSize  int `json:"chunk_size" binding:"omitempty,gte=100,lte=4000"`
	SplitLevel int `json:"split_level" binding:"omitempty,gte=1,lte=6"`
	MinChunk   int `json:"min_chunk" binding:"omitempty,gte=0,lte=500"`
}

// DocIngestDTO 文档导入请求
//
// Account 是普通参数（§6），不做鉴权解析。
type DocIngestDTO struct {
	Account      string          `json:"account" binding:"required,max=64"`
	KBNo         string          `json:"kb_no" binding:"required,max=64"`
	DocName      string          `json:"doc_name" binding:"required,max=512"`
	Format       string          `json:"format" binding:"required,oneof=markdown text chunks"`
	Content      string          `json:"content"`
	Chunks       []ChunkDTO      `json:"chunks"`
	SplitOptions *SplitOptionsDTO `json:"split_options"`

	// Refresh 为 true 时，写完 ES 立刻刷新索引，返回即可检索（§4.2）。
	// 默认 false：常规导入按 30s 的刷新周期，不付这次刷新的钱。
	Refresh bool `json:"refresh"`

	// 这里**没有** biz_tag：切片上的 biz_tag 一律取自 KB（§7.2）。
	// 让文档覆盖它会让这个文档按 KB 的 tag 搜不到、按自己的 tag 库又被筛掉。
}

// DocDeleteDTO 文档删除请求
type DocDeleteDTO struct {
	Account string `json:"account" binding:"required,max=64"`
	KBNo    string `json:"kb_no" binding:"required,max=64"`
	DocName string `json:"doc_name" binding:"required,max=512"`
}

// KBDeleteDTO 知识库删除请求：软删 KB 及其全部文档，并清理 ES
type KBDeleteDTO struct {
	Account string `json:"account" binding:"required,max=64"`
	KBNo    string `json:"kb_no" binding:"required,max=64"`
}
```

### A1.4 `common/dto/search.go`

```go
package dto

// SearchDTO 检索请求
//
// 只接受 limit，不接受 offset——RRF 融合在应用层，ES 的 from/size
// 作用不到融合后的排名（§7.4）。
type SearchDTO struct {
	Account string   `json:"account" binding:"required,max=64"`
	KBNos   []string `json:"kb_nos" binding:"required,min=1,max=10,dive,max=64"`
	Query   string   `json:"query" binding:"required,max=1000"`

	BizTags []string `json:"biz_tags" binding:"omitempty,max=10,dive,max=64"`

	Limit int `json:"limit" binding:"omitempty,gte=1,lte=100"`

	// DenseWeight 向量路权重：<=0.01 只走 BM25，>=0.99 只走 kNN，其余两路加权融合。
	// nil 表示用配置里的默认值。
	DenseWeight *float64 `json:"dense_weight" binding:"omitempty,gte=0,lte=1"`

	RerankSwitch bool `json:"rerank_switch"`
}
```

### A1.5 `common/vo/knowledge.go`

```go
package vo

// KnowledgeBaseVO 知识库对外表示（本服务只读，不提供写接口）
type KnowledgeBaseVO struct {
	ID            uint64 `json:"id"`
	No            string `json:"no"`
	Account       string `json:"account"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	KnowledgeType string `json:"knowledge_type"`
	SearchMode    string `json:"search_mode"`
	BizTag        string `json:"biz_tag"`
	DocCount      int64  `json:"doc_count"`
	ChunkCount    int64  `json:"chunk_count"`
}

// SearchItemVO 一条检索结果
//
// 带 Content：内容下沉在 ES（D1），这里不需要回表取正文，
// 只补了 DocName / KBName 两个展示字段（来自存活校验那一次批量 SQL）。
type SearchItemVO struct {
	ChunkID     string  `json:"chunk_id"`
	DocID       uint64  `json:"doc_id"`
	DocName     string  `json:"doc_name"`
	KBID        uint64  `json:"kb_id"`
	KBName      string  `json:"kb_name"`
	KBNo        string  `json:"kb_no"`
	Order       int     `json:"order"`
	Title       string  `json:"title"`
	Content     string  `json:"content"`
	HeadingPath string  `json:"heading_path"`
	Score       float64 `json:"score"`
}

// SearchResultVO 检索响应
//
// 没有 total / page：本期只做 top-N，不做深翻页（§7.4）。
type SearchResultVO struct {
	Items []*SearchItemVO `json:"items"`
}

// DocIngestVO 导入响应
type DocIngestVO struct {
	DocID      uint64 `json:"doc_id"`
	DocName    string `json:"doc_name"`
	ChunkCount int    `json:"chunk_count"`
	Created    bool   `json:"created"` // true = 新建，false = 覆盖已有同名文档
}

// DocDeleteVO 删除响应
type DocDeleteVO struct {
	DocName      string `json:"doc_name"`
	DeletedChunks int64 `json:"deleted_chunks"`
}
```

---

## A2 · domain 层

### A2.1 `domain/value_object/chunk_options.go`

```go
package value_object

import (
	"encoding/json"

	"github.com/PycMono/FastRAG/common/constants"
	apperrors "github.com/PycMono/FastRAG/common/errors"
)

// ChunkOptions 切片参数。
//
// 零值合法：表示「用默认值」，由 Normalize() 补齐。
type ChunkOptions struct {
	ChunkSize  int
	SplitLevel int
	MinChunk   int
}

// Normalize 把零值补成默认值，并做范围校验。
func (o ChunkOptions) Normalize() (ChunkOptions, error) {
	out := o
	if out.ChunkSize == 0 {
		out.ChunkSize = constants.DefaultChunkSize
	}
	if out.SplitLevel == 0 {
		out.SplitLevel = constants.DefaultSplitLevel
	}
	if out.MinChunk == 0 {
		out.MinChunk = constants.DefaultMinChunk
	}

	switch {
	case out.ChunkSize < 100 || out.ChunkSize > 4000:
		return out, apperrors.NewParamError("chunk_size 必须在 [100, 4000]")
	case out.SplitLevel < 1 || out.SplitLevel > 6:
		return out, apperrors.NewParamError("split_level 必须在 [1, 6]")
	case out.MinChunk < 0 || out.MinChunk*2 > out.ChunkSize:
		return out, apperrors.NewParamError("min_chunk 必须非负且不超过 chunk_size 的一半")
	}
	return out, nil
}

// Override 用非零字段覆盖当前值（请求参数覆盖 KB 上的快照）。
func (o ChunkOptions) Override(p *ChunkOptions) ChunkOptions {
	if p == nil {
		return o
	}
	out := o
	if p.ChunkSize > 0 {
		out.ChunkSize = p.ChunkSize
	}
	if p.SplitLevel > 0 {
		out.SplitLevel = p.SplitLevel
	}
	if p.MinChunk > 0 {
		out.MinChunk = p.MinChunk
	}
	return out
}

// ParseSplitOptions 解析 KB 上存的 split_options JSON 快照。
// 解析失败不报错——快照是历史数据，坏了就退回默认值，不该阻断导入。
func ParseSplitOptions(raw []byte) ChunkOptions {
	var opts ChunkOptions
	if len(raw) == 0 {
		return opts
	}
	// KB 上的快照用的是 snake_case key，与 DTO 一致
	var probe struct {
		ChunkSize  int `json:"chunk_size"`
		SplitLevel int `json:"split_level"`
		MinChunk   int `json:"min_chunk"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ChunkOptions{}
	}
	opts.ChunkSize = probe.ChunkSize
	opts.SplitLevel = probe.SplitLevel
	opts.MinChunk = probe.MinChunk
	return opts
}
```

### A2.2 `domain/value_object/search_options.go`

```go
package value_object

import (
	"strings"

	"github.com/PycMono/FastRAG/common/constants"
	apperrors "github.com/PycMono/FastRAG/common/errors"
)

// SearchOptions 检索参数。
type SearchOptions struct {
	Query       string
	Limit       int
	BizTags     []string
	DenseWeight float64
	Rerank      bool
}

// Normalize 补齐默认值并校验。
func (o SearchOptions) Normalize() (SearchOptions, error) {
	out := o

	out.Query = strings.TrimSpace(out.Query)
	if out.Query == "" {
		return out, apperrors.NewParamError("query 不能为空")
	}

	if out.Limit == 0 {
		out.Limit = constants.DefaultSearchLimit
	}
	if out.Limit < 1 || out.Limit > constants.MaxSearchLimit {
		return out, apperrors.NewParamError("limit 必须在 [1, 100]")
	}

	if out.DenseWeight < 0 || out.DenseWeight > 1 {
		return out, apperrors.NewParamError("dense_weight 必须在 [0, 1]")
	}

	return out, nil
}

// UseBM25 / UseKNN 判断两路各自要不要发。
// 阈值不是 0/1 而是 ±0.01，是为了容忍配置里写的 0.999 这类值。
func (o SearchOptions) UseBM25() bool { return o.DenseWeight <= 0.99 }
func (o SearchOptions) UseKNN() bool  { return o.DenseWeight >= 0.01 }
```

### A2.3 `domain/entity/knowledge/knowledge_base.go`

```go
package knowledge

import "github.com/PycMono/FastRAG/common/constants"

// KnowledgeBase 知识库聚合根。
//
// 本服务**只读**它：KB 由外部预置，这里不做创建/发布/版本（§1.2）。
type KnowledgeBase struct {
	ID            uint64 // 内部主键，join 用
	No            string // 对外编号，接口用
	Account       string // 租户
	Name          string
	Description   string
	KnowledgeType string
	SearchMode    string
	BizTag        string
	SplitOptions  []byte // 切片参数快照（JSON 原文）
	DocCount      int64
	ChunkCount    int64
	IsSystem      bool
	CreateTs      int64
	UpdateTs      int64
	DeleteTs      int64
}

// Deleted 是否已软删。
func (kb *KnowledgeBase) Deleted() bool { return kb.DeleteTs > 0 }

// UsesTitleOnly 检索模式是否只查标题。
func (kb *KnowledgeBase) UsesTitleOnly() bool {
	return kb.SearchMode == constants.SearchModeTitle
}
```

### A2.4 `domain/entity/knowledge/knowledge_doc.go`

```go
package knowledge

// KnowledgeDoc 文档聚合根。
//
// 切片不入 MySQL（D3），所以这个聚合只承载「文档级」状态：
// 身份、内容指纹、切片计数。
type KnowledgeDoc struct {
	ID          uint64 // 雪花 ID，写入前预分配
	KBID        uint64
	Account     string
	Name        string // 文档身份 = (KBID, Name)，同名重导入即更新
	ContentHash string // SHA256
	ChunkCount  int
	CreateTs    int64
	UpdateTs    int64
	DeleteTs    int64
}

// Deleted 是否已软删。存活校验靠它丢弃幽灵切片（§7.3）。
func (d *KnowledgeDoc) Deleted() bool { return d.DeleteTs > 0 }

// IsSameContent 内容是否未变（可跳过重灌）。
func (d *KnowledgeDoc) IsSameContent(hash string) bool {
	return d.ContentHash != "" && d.ContentHash == hash
}
```

### A2.5 `domain/entity/knowledge/chunk.go`

```go
package knowledge

// Chunk 切片。
//
// 它不是聚合根——切片没有独立生命周期，完全从属于文档（D3）。
type Chunk struct {
	Order       int    // 文档内序号，从 0 起
	Title       string // 切片标题（通常是最近一级标题）
	HeadingPath string // 标题链，"产品介绍，概述，配置方法"
	Content     string // 正文（已含标题链 prefix）
}

// Chunks 切片集合。
type Chunks []*Chunk

// Titles 取出所有切片标题，供 embedding 批量调用使用。
func (cs Chunks) Titles() []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Title
	}
	return out
}

// Contents 取出所有切片正文。
func (cs Chunks) Contents() []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Content
	}
	return out
}

// TotalChars 总字符数，用于日志与容量预估。
func (cs Chunks) TotalChars() int {
	n := 0
	for _, c := range cs {
		n += len([]rune(c.Content))
	}
	return n
}
```

### A2.6 `domain/factory/knowledge_doc.go`

```go
package factory

import (
	"time"

	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
)

// NewDoc 建一个文档聚合。
//
// 只在**所有内容都定下来之后**调一次——ES 切片写完、contentHash 算完
// （§5.2 ③）。这样实体从诞生起就是终态，不存在"先建行、后填内容"的中间态，
// 也就不存在"ES 写失败但库里已经记了新计数"的谎报窗口。
//
// 禁止在别处直接 &KnowledgeDoc{}——CreateTs / DeleteTs 漏填就是脏数据。
func NewDoc(
	docID uint64,
	kb *knowledgeentity.KnowledgeBase,
	name, contentHash string,
	chunkCount int,
	now time.Time,
) *knowledgeentity.KnowledgeDoc {
	ts := now.UnixMilli()
	return &knowledgeentity.KnowledgeDoc{
		ID:          docID,
		KBID:        kb.ID,
		Account:     kb.Account,
		Name:        name,
		ContentHash: contentHash,
		ChunkCount:  chunkCount,
		CreateTs:    ts,
		UpdateTs:    ts,
		DeleteTs:    0,
	}
}
```

> **没有 `ApplyReingest` / `ApplyContent` 这类"改已加载实体"的方法。**
> 早先有一版是「先建行 → 写 ES → 再回来改 ContentHash/ChunkCount」，
> 结果写出过 `delta = len(chunks) - old.ChunkCount` 恒为 0 的 bug——
> `old` 与 `doc` 是同一个指针，改 `doc` 就等于改 `old`。
> 现在重导入的元数据更新由 `IKnowledgeDocRepo.Save` 直接写库行，
> 内存里没有"同一份实体的旧快照"，那个别名陷阱从结构上就不存在了。

### A2.7 `domain/factory/vector_doc.go`

```go
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
)

// ChunkID 由 (docID, order, content) 派生。
//
// 纯函数、可复现，内容一变 ID 就变。因此重导入时旧切片不会被覆盖，
// 必须由 §5.2 第 ② 步按 _id 差集显式清掉；内容没变的那部分 ID 不变，
// 原地覆盖、也不会被删——差集清理对它们是零成本的。
func ChunkID(docID uint64, order int, content string) string {
	h := sha256.New()
	h.Write([]byte(strconv.FormatUint(docID, 10)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(order)))
	h.Write([]byte{0})
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// BuildVectorDocs 把领域切片 + 已算好的向量，组装成存储中立的 VectorDoc。
//
// 向量由调用方（应用层）通过 IEmbeddingService 算好再传进来，
// 工厂只做组装——它不碰网络，所以可测。
//
// 返回的 ChunkID 列表就是 §5.2 第 ② 步「保留集合」的来源，
// 调用方不用自己再算一遍（算两遍就多一处可能算岔的地方）。
func BuildVectorDocs(
	kb *knowledgeentity.KnowledgeBase,
	docID uint64,
	bizTag string,
	chunks knowledgeentity.Chunks,
	titleVecs, contentVecs [][]float32,
	now time.Time,
) []knowledgerepo.VectorDoc {
	if len(titleVecs) != len(chunks) || len(contentVecs) != len(chunks) {
		// 向量条数与切片数必须一一对应；对不上属于调用方 bug，
		// 直接 panic 比写出一半脏数据好
		panic("factory: 向量条数与切片数不一致")
	}

	ts := now.UnixMilli()
	docs := make([]knowledgerepo.VectorDoc, len(chunks))
	for i, c := range chunks {
		docs[i] = knowledgerepo.VectorDoc{
			ChunkID:     ChunkID(docID, c.Order, c.Content),
			Account:     kb.Account,
			KBID:        kb.ID,
			DocID:       docID,
			Order:       c.Order,
			BizTag:      bizTag,
			Title:       c.Title,
			Content:     c.Content,
			HeadingPath: c.HeadingPath,
			TitleVec:    titleVecs[i],
			ContentVec:  contentVecs[i],
			CreateTs:    ts,
		}
	}
	return docs
}

// ChunkIDs 取出切片 ID 列表，供 §5.2 第 ② 步的差集清理使用。
func ChunkIDs(docs []knowledgerepo.VectorDoc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ChunkID
	}
	return out
}

// EmbeddingTexts 返回喂给 embedding 的文本。
//
// 有标题链时用「标题链 + 正文」，让上下文信息自然地进 embedding——
// 这就是第 §8.1 步那个廉价版 Contextual Retrieval 的落点。
func EmbeddingTexts(chunks knowledgeentity.Chunks) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Content // 结构感知切片已把标题链写进 Content 开头
	}
	return out
}
```

### A2.8 `domain/repository/knowledge/knowledge_base.go`

```go
package knowledge

import (
	"context"

	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
)

// IKnowledgeBaseRepo 知识库仓储（只读）。
//
// KB 由外部预置，本服务不提供写方法。
// 所有方法都带 account 条件（§6）——租户条件不漏写由这一层保证。
type IKnowledgeBaseRepo interface {
	// LoadByNo 按对外编号加载。
	//
	// account 不匹配与库不存在**返回同一个 ErrKnowledgeBaseNotFound**（§6.1、A4.3）——
	// 分开的话就是送出一个可枚举的租户探测接口。
	LoadByNo(ctx context.Context, no, account string) (*knowledgeentity.KnowledgeBase, error)

	// LoadByNos 批量加载，用于一次检索涉及多个 KB 的场景。
	// 返回结果只含属于 account 且未删除的 KB，缺失的静默跳过。
	LoadByNos(ctx context.Context, nos []string, account string) (knowledgeentity.KnowledgeBases, error)

	// ApplyChunkDelta 按差值更新 chunk_count。
	// 用差值而非无条件 +1：重导入时先扣旧值，避免计数漂移（D3）。
	ApplyChunkDelta(ctx context.Context, kbID uint64, delta int64) error

	// ApplyDocDelta 按差值更新 doc_count。
	ApplyDocDelta(ctx context.Context, kbID uint64, delta int64) error

	// SetCounts 用绝对值覆盖两个计数，供对账任务使用（A3.5）。
	// 与上面两个差值方法分开：对账的语义就是「以真实数据为准」，
	// 再走一次差值等于把漂移原样带过去。
	SetCounts(ctx context.Context, kbID, docCount, chunkCount int64) error
}
```

### A2.9 `domain/repository/knowledge/knowledge_doc.go`

```go
package knowledge

import (
	"context"

	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
)

// IKnowledgeDocRepo 文档仓储。
type IKnowledgeDocRepo interface {
	// LoadByName 按 (kbID, name) 查文档。查不到返回 (nil, nil)，不是错误——
	// 「不存在」是重导入路径上的正常分支。
	LoadByName(ctx context.Context, kbID uint64, name string) (*knowledgeentity.KnowledgeDoc, error)

	// LoadByIDs 批量加载，供检索时的存活校验使用。
	//
	// 刻意**不过滤** delete_ts：调用方要靠返回的行区分「不存在」和「已删」两种情况，
	// SQL 里加 delete_ts = 0 会把它们混成一个 (nil, nil)，日志里就查不出是哪种（§7.3）。
	LoadByIDs(ctx context.Context, ids []uint64) (knowledgeentity.KnowledgeDocs, error)

	// Save 写入/更新文档行（§5.2 ③）。
	//
	// 语义是 upsert，键是 (kb_id, name)：doc 不存在则按 doc.ID 建行，
	// 已存在则更新 content_hash / chunk_count / update_ts。**不动 id**——
	// 重导入沿用同一个 doc_id，ES 那边的切片才接得上（§5.3）。
	//
	// 两个返回值都服务于 KB 层的冗余计数：
	//   created       这次是不是新建了行 → 调用方据此决定要不要 +1 KB 的 doc_count；
	//   oldChunkCount **更新前**的切片数 → 和本次的 len(chunks) 算差值。
	//
	// 实现要点：读旧值 + upsert 必须在**同一个事务**里（调用方套 s.tm.Transaction），
	// 否则中途挤进来的另一次导入会让差值算错，KB 计数就永久性偏了（A4.4）。
	Save(ctx context.Context, doc *knowledgeentity.KnowledgeDoc) (oldChunkCount int, created bool, err error)

	// SoftDelete 按 (kbID, name) 软删，返回受影响的文档。
	SoftDelete(ctx context.Context, kbID uint64, name string, now int64) (*knowledgeentity.KnowledgeDoc, error)

	// SoftDeleteByKBID 软删某 KB 下所有文档，返回文档数。
	SoftDeleteByKBID(ctx context.Context, kbID uint64, account string, now int64) (int64, error)

	// CountByKBID 统计某 KB 下未删除的文档数，供计数校正使用。
	CountByKBID(ctx context.Context, kbID uint64) (int64, error)

	// SumChunkCountByKBID 汇总某 KB 下未删除文档的切片数，供计数校正使用。
	SumChunkCountByKBID(ctx context.Context, kbID uint64) (int64, error)
}
```

### A2.10 `domain/repository/knowledge/vector_store.go`

```go
package knowledge

import "context"

// ─── 存储中立的类型 ───────────────────────────────────────────────────────────
//
// 这些类型是「端口自己的语言」：实现方只认它们，不认 entity，
// 所以换 ES → Milvus/Qdrant 时上层一行不用改。

// VectorDoc 一份待写入的切片文档。
type VectorDoc struct {
	ChunkID     string
	Account     string
	KBID        uint64
	DocID       uint64
	Order       int
	BizTag      string
	Title       string
	Content     string
	HeadingPath string
	TitleVec    []float32
	ContentVec  []float32
	CreateTs    int64
}

// VectorFilter 删除条件。零值字段表示「不过滤」。
//
// 全是等值条件：account / kb_id / doc_id。三个都空是「删全库」，
// 实现会拒绝（见 A4.6）——那几乎一定是调用方漏传了参数。
type VectorFilter struct {
	Account string
	KBID    uint64
	DocID   uint64
}

// VectorSearchReq 检索请求。
type VectorSearchReq struct {
	Account    string
	KBIDs      []uint64
	BizTags    []string
	Query      string
	QueryVec   []float32
	TitleOnly  bool // search_mode = title 时为 true
	DenseWeight float64

	BM25Top       int
	KNNTops       int
	NumCandidates int
}

// VectorHit 一条命中。
type VectorHit struct {
	ChunkID     string
	DocID       uint64
	KBID        uint64
	Order       int
	Title       string
	Content     string
	HeadingPath string
	Score       float64 // 单路原分；融合后的分数由应用层回填
}

// SearchResult 两路原始命中，**未融合**。
//
// 刻意分成两路返回：融合策略（RRF）属于应用层（§7.2），
// 这样换引擎时权重策略不受影响。
// DenseWeight 让它只发一路时，另一路为 nil。
type SearchResult struct {
	BM25 []VectorHit
	KNN  []VectorHit
}

// ─── 端口 ─────────────────────────────────────────────────────────────────────

// IVectorStore 切片仓储。
//
// 为什么叫 Store 却放在 repository：切片持久化在 ES（D3），
// 它就是切片的仓储，和其他两个仓储地位相同（§3.1）。
type IVectorStore interface {
	// EnsureIndex 确保索引存在（幂等）。
	// 不带 spec 参数——索引长什么样（维度、分片、分词器）是部署环境的事实，
	// 由实现从配置读，端口不该出现 Analyzer/Shards 这种 ES 概念。
	//
	// **只在启动期调一次**，不在写入路径上（§9.4）。它只看「索引在不在」，
	// 不校验 mapping —— dense_vector 的 dims 不可变，校验了也修不了。
	EnsureIndex(ctx context.Context) error

	// Save 批量写入切片。同一 chunk_id 覆盖。
	Save(ctx context.Context, docs []VectorDoc) error

	// Refresh 强制刷新索引，让刚写入的切片立刻可检索。
	//
	// 两个用途：重导入时让 DeleteExcept 看得见「还没刷出来的旧切片」（§5.2 ②），
	// 以及调用方要求「导入即可搜」（§4.2）。粒度是整个索引——ES 没有按文档刷新。
	// 它属于「实现细节泄漏到端口」，但 fx 只认接口（A4.16），放接口上比让装配层
	// 去 import 具体类型省事。
	Refresh(ctx context.Context) error

	// DeleteByQuery 按条件删除**命中的全部**切片，返回删除条数。
	// 用于文档删除 / 整库删除（§5.4）。
	DeleteByQuery(ctx context.Context, filter VectorFilter) (int64, error)

	// DeleteExcept 删除 filter 命中的切片里 _id **不在** keepIDs 中的那些，返回删除条数。
	//
	// 只用于 §5.2 第 ② 步「写完新切片后清掉旧的」。单独一个方法而不是给
	// DeleteByQuery 加参数：这两者杀伤面差一个数量级——`DeleteExcept` 少传了
	// keepIDs 就等于把整篇文档删空，混在一起早晚出事。
	//
	// filter 必须指定 DocID：没有 doc_id 就没有「这篇文档的旧切片」可言。
	//
	// 失败不影响正确性：旧切片多留一会儿而已，下次重导入会再清一遍。
	DeleteExcept(ctx context.Context, filter VectorFilter, keepIDs []string) (int64, error)

	// Search 发 BM25 / kNN 两路，原样返回，不融合。
	Search(ctx context.Context, req VectorSearchReq) (SearchResult, error)
}
```

### A2.11 `domain/interfaces/embedding_service.go`

```go
package interfaces

import "context"

// IEmbeddingService 向量化能力。
//
// 它留在 interfaces/ 而不是 repository/：它不存任何东西，
// 是无状态的外部 API 调用（§3.1 的判据）。
type IEmbeddingService interface {
	// EmbedDocs 批量编码文档。返回顺序与入参一一对应。
	EmbedDocs(ctx context.Context, texts []string) ([][]float32, error)

	// EmbedQuery 编码查询串。
	//
	// 必须与 EmbedDocs 分开：非对称模型（E5、bge-large-zh）检索时
	// 要给 query 加指令前缀（如 "query: "），doc 侧不加，混用会显著掉分。
	EmbedQuery(ctx context.Context, text string) ([]float32, error)

	Dim() int
	Model() string
}
```

### A2.12 `domain/interfaces/rerank.go`

```go
package interfaces

import "context"

// RerankCandidate 待重排候选。
type RerankCandidate struct {
	ChunkID string
	Content string
}

// IRerankService 重排能力（P2，本期只留接口与空实现）。
type IRerankService interface {
	Rerank(ctx context.Context, query string, cands []RerankCandidate, topN int) ([]int, error)
}
```

### A2.13 `domain/value_object/search_tuning.go`

```go
package value_object

// SearchTuning 检索调参。
//
// 单独抽出来是因为它来自配置，而 **application 层不能 import infrastructure/config**
// （§3 的分层铁律）。让 infrastructure 读配置、构造这个中立结构再注入，
// 应用层就只依赖 domain。
type SearchTuning struct {
	BM25Top       int     // 每路召回条数
	KNNTops       int
	NumCandidates int     // kNN 的候选池，越大越准越慢
	RankConstant  int     // RRF 的 k，论文默认 60
	DenseWeight   float64 // 默认向量路权重
}

// WithDefaults 补齐零值，避免配置漏填导致召回为 0。
func (t SearchTuning) WithDefaults() SearchTuning {
	out := t
	if out.BM25Top <= 0 {
		out.BM25Top = 100
	}
	if out.KNNTops <= 0 {
		out.KNNTops = 100
	}
	if out.NumCandidates <= 0 {
		out.NumCandidates = out.KNNTops * 5
	}
	if out.RankConstant <= 0 {
		out.RankConstant = 60
	}
	if out.DenseWeight <= 0 {
		out.DenseWeight = 0.5
	}
	return out
}
```

### A2.14 `domain/value_object/chunk_input.go`

```go
package value_object

// ChunkInput 调用方直接给出的裸切片（format = chunks）。
//
// 不用 common/dto 的 ChunkDTO：domain 不该认识传输层结构，
// 由 controller → application 时做一次转换。
type ChunkInput struct {
	Title   string
	Content string
}
```

### A2.15 `domain/entity/knowledge/collection.go`

```go
package knowledge

// KnowledgeBases 知识库集合。
type KnowledgeBases []*KnowledgeBase

// IndexByID 建 ID 索引，供检索结果回填 kb_name / kb_no。
func (ks KnowledgeBases) IndexByID() map[uint64]*KnowledgeBase {
	out := make(map[uint64]*KnowledgeBase, len(ks))
	for _, kb := range ks {
		out[kb.ID] = kb
	}
	return out
}

// FilterByBizTags 按业务标签过滤。
//
// 语义刻意定得很窄：请求带了 biz_tags，就只留标签精确命中的库。
// 不做「无标签的库视为通用、一律保留」——那种隐含规则会让调用方
// 搞不清自己为什么召回多了。
func (ks KnowledgeBases) FilterByBizTags(tags []string) KnowledgeBases {
	if len(tags) == 0 {
		return ks
	}
	want := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		want[t] = struct{}{}
	}

	out := make(KnowledgeBases, 0, len(ks))
	for _, kb := range ks {
		if _, ok := want[kb.BizTag]; ok {
			out = append(out, kb)
		}
	}
	return out
}

// KnowledgeDocs 文档集合。
type KnowledgeDocs []*KnowledgeDoc

// AliveMap 返回「未删除」文档的 ID → 文档。
//
// 存活校验（§7.3）就靠这一张表：查不到 = 孤儿切片，已软删 = 幽灵切片，
// 两种都丢掉。
func (ds KnowledgeDocs) AliveMap() map[uint64]*KnowledgeDoc {
	out := make(map[uint64]*KnowledgeDoc, len(ds))
	for _, d := range ds {
		if d.Deleted() {
			continue
		}
		out[d.ID] = d
	}
	return out
}
```

### A2.16 `domain/service/splitter.go`

```go
package service

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/PycMono/FastRAG/common/constants"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/domain/value_object"
)

// Splitter 切片领域服务。
//
// 放 domain 而不是 application：「怎么切才算对」是业务规则，不是流程编排。
// 它不碰数据库、不发网络请求，所以可以纯单测。
type Splitter struct{}

func NewSplitter() *Splitter { return &Splitter{} }

// Split 按形态切分。format = chunks 不走这里——调用方已经切好了，走 Normalize。
func (s *Splitter) Split(content, format string, opts value_object.ChunkOptions) (knowledgeentity.Chunks, error) {
	nOpts, err := opts.Normalize()
	if err != nil {
		return nil, err
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return nil, apperrors.NewParamError("content 不能为空")
	}

	var chunks knowledgeentity.Chunks
	switch format {
	case constants.FormatMarkdown:
		chunks = splitMarkdown(content, nOpts)
	case constants.FormatText:
		chunks = splitBySeparators(content, "", "", nOpts.ChunkSize)
	default:
		return nil, apperrors.NewParamError("不支持的 format: " + format)
	}

	return finalize(chunks, nOpts), nil
}

// Normalize 归一化调用方直接给出的切片（format = chunks）。
// 不重切，只做三件事：滤空、补标题链、重新编号。
func (s *Splitter) Normalize(in []value_object.ChunkInput) (knowledgeentity.Chunks, error) {
	out := make(knowledgeentity.Chunks, 0, len(in))
	for _, c := range in {
		body := strings.TrimSpace(c.Content)
		if body == "" {
			continue
		}
		title := strings.TrimSpace(c.Title)
		out = append(out, &knowledgeentity.Chunk{
			Title:       title,
			HeadingPath: title,
			Content:     renderPiece(title, body),
		})
	}
	if len(out) == 0 {
		return nil, apperrors.ErrDocChunkEmpty
	}
	for i, c := range out {
		c.Order = i
	}
	return out, nil
}

// ─── markdown 结构感知（§8.1）─────────────────────────────────────────────────

var (
	// ATX 标题：# ~ ######，允许结尾的闭合 #
	reATXHeading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	// 代码围栏开关
	reFence = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
)

// mdBlock 一个标题小节：只含「本级的正文」，不含子标题的正文。
type mdBlock struct {
	level int
	title string
	path  string // 标题链，"一级，二级"
	body  string
}

// parseMarkdownBlocks 按标题把 markdown 切成有序小节，并维护标题链。
//
// 代码围栏内的 # 不算标题——这是最容易出错的地方：
// 文档里嵌一段 shell 注释就能把整篇的层级结构带偏，而且不会报错。
func parseMarkdownBlocks(content string) []mdBlock {
	var (
		blocks []mdBlock
		stack  []string // 标题栈：stack[i] 是当前的第 i+1 级标题
		cur    mdBlock
		fence  string // 非空表示正在代码围栏内
	)

	flush := func() {
		if strings.TrimSpace(cur.body) != "" {
			blocks = append(blocks, cur)
		}
		cur = mdBlock{}
	}

	for _, line := range strings.Split(content, "\n") {
		if m := reFence.FindStringSubmatch(line); m != nil {
			if fence == "" {
				fence = m[1]
			} else if strings.HasPrefix(m[1], fence[:1]) {
				fence = ""
			}
			cur.body += line + "\n"
			continue
		}
		if fence != "" {
			cur.body += line + "\n"
			continue
		}

		m := reATXHeading.FindStringSubmatch(line)
		if m == nil {
			cur.body += line + "\n"
			continue
		}

		flush()

		level := len(m[1])
		title := strings.TrimSpace(m[2])

		if level-1 < len(stack) {
			stack = stack[:level-1]
		}
		// 跳级（h2 直接跟 h4）时补空位，保证「栈深度 == 标题级别」
		for len(stack) < level-1 {
			stack = append(stack, "")
		}
		stack = append(stack, title)

		cur = mdBlock{level: level, title: title, path: joinTitlePath(stack)}
	}
	flush()

	return blocks
}

func joinTitlePath(stack []string) string {
	parts := make([]string, 0, len(stack))
	for _, t := range stack {
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "，")
}

// lastSegment 取标题链的最后一段。
func lastSegment(path string) string {
	const sep = "，"
	if i := strings.LastIndex(path, sep); i >= 0 {
		return path[i+len(sep):]
	}
	return path
}

// renderPiece 把标题链拼到正文前面。
//
// 这就是那个廉价版的 Contextual Retrieval：切片一旦离开文档，
// 单看正文常常不知道自己在讲谁；把标题链写进去，BM25 和 embedding 都能吃到。
func renderPiece(path, body string) string {
	if path == "" {
		return body
	}
	return path + "\n" + body
}

// splitMarkdown 结构感知切分：小节点合并，超长节点降级递归。
func splitMarkdown(content string, opts value_object.ChunkOptions) knowledgeentity.Chunks {
	var (
		chunks  knowledgeentity.Chunks
		buf     strings.Builder
		bufPath string
		bufLen  int
	)

	flush := func() {
		text := strings.TrimSpace(buf.String())
		if text != "" {
			chunks = append(chunks, &knowledgeentity.Chunk{
				Title:       lastSegment(bufPath),
				HeadingPath: bufPath,
				Content:     text,
			})
		}
		buf.Reset()
		bufLen = 0
	}

	for _, b := range parseMarkdownBlocks(content) {
		body := strings.TrimSpace(b.body)
		if body == "" {
			continue
		}

		piece := renderPiece(b.path, body)
		n := runeLen(piece)

		if n > opts.ChunkSize {
			// 单个节点就超长：先落掉攒着的，再把这个节点降级递归
			flush()
			chunks = append(chunks,
				splitBySeparators(body, lastSegment(b.path), b.path, opts.ChunkSize)...)
			continue
		}

		// 只在同一标题链下合并。跨标题合并虽然能凑满长度，
		// 但切片的 HeadingPath 会变得名不副实——宁短一点也别骗人
		if bufLen > 0 && (bufPath != b.path || bufLen+n > opts.ChunkSize) {
			flush()
		}
		if bufLen == 0 {
			bufPath = b.path
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
			bufLen += 2
		}
		buf.WriteString(piece)
		bufLen += n
	}
	flush()

	return chunks
}

// ─── 兜底切片器（§8.2）────────────────────────────────────────────────────────

// separators 递归降级用的分隔符：从「语义边界明确」到「只能硬切」。
var separators = []string{
	"\n\n", "\n",
	"。", "！", "？", "；",
	". ", "! ", "? ", "; ",
	"，", ", ", " ",
}

// splitBySeparators 递归降级切分。
//
// 先拿最强的分隔符切；段还是超长就换更弱的分隔符再切，
// 直到切得动、或者分隔符用尽只能硬切。
// 比「按固定长度硬切」强的地方在于：它优先在语义边界断开。
func splitBySeparators(text, title, path string, size int) knowledgeentity.Chunks {
	prefix := ""
	if path != "" {
		prefix = path + "\n"
	}

	// 前缀也占预算，否则长标题会把正文挤没
	budget := size - runeLen(prefix)
	if budget < size/4 {
		budget = size / 4
	}

	return splitRec(strings.TrimSpace(text), prefix, title, path, budget, 0)
}

func splitRec(text, prefix, title, path string, budget, depth int) knowledgeentity.Chunks {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	if runeLen(text) <= budget {
		return knowledgeentity.Chunks{{
			Title: title, HeadingPath: path, Content: prefix + text,
		}}
	}

	// 分隔符用尽仍超长：只能硬切。宁可切碎，也不能丢内容
	if depth >= len(separators) {
		return hardCut(text, prefix, title, path, budget)
	}

	sep := separators[depth]

	var (
		out    knowledgeentity.Chunks
		cur    strings.Builder
		curLen int
	)

	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			out = append(out, &knowledgeentity.Chunk{
				Title: title, HeadingPath: path, Content: prefix + s,
			})
		}
		cur.Reset()
		curLen = 0
	}

	for _, part := range strings.Split(text, sep) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if runeLen(part) > budget {
			// 这一段自己就超预算：落掉手上的，对它再降一级
			flush()
			out = append(out, splitRec(part, prefix, title, path, budget, depth+1)...)
			continue
		}

		if curLen > 0 && curLen+runeLen(part)+len(sep) > budget {
			flush()
		}
		if curLen > 0 {
			cur.WriteString(sep)
			curLen += len(sep)
		}
		cur.WriteString(part)
		curLen += runeLen(part)
	}
	flush()

	return out
}

// hardCut 兜底中的兜底：按 rune 硬切。
// 走到这一步说明文本里连一个空格都没有（base64、无空格长串），
// 切碎了也比丢了强。
func hardCut(text, prefix, title, path string, budget int) knowledgeentity.Chunks {
	if budget < 1 {
		budget = 1
	}

	runes := []rune(text)
	out := make(knowledgeentity.Chunks, 0, len(runes)/budget+1)
	for len(runes) > 0 {
		n := budget
		if n > len(runes) {
			n = len(runes)
		}
		out = append(out, &knowledgeentity.Chunk{
			Title: title, HeadingPath: path, Content: prefix + string(runes[:n]),
		})
		runes = runes[n:]
	}
	return out
}

// finalize 合并过短切片并重新编号。
//
// 过短切片（比如只有一行标题、结论只有三个字的节点）在向量检索里
// 几乎必然是噪声：它的向量不携带判别信息，却要占掉一个 topK 名额。
func finalize(chunks knowledgeentity.Chunks, opts value_object.ChunkOptions) knowledgeentity.Chunks {
	out := make(knowledgeentity.Chunks, 0, len(chunks))

	for _, c := range chunks {
		if len(out) > 0 && runeLen(c.Content) < opts.MinChunk {
			prev := out[len(out)-1]
			if merged := prev.Content + "\n\n" + c.Content; runeLen(merged) <= opts.ChunkSize {
				prev.Content = merged
				continue
			}
		}
		out = append(out, c)
	}

	for i, c := range out {
		c.Order = i
	}
	return out
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }
```

> ⚠️ **不做 overlap**（§8.3 已标 P2）。结构感知切片下 overlap 会破坏标题边界——把上一节的尾巴粘进下一节的标题链里，而那正是它唯一的优势。兜底的递归分隔器本该有 overlap，但本期这条路径只是兜底，先不做。

### A2.17 `domain/service/register.go`（改写）

```go
package service

import "go.uber.org/fx"

// Register 注册所有领域层服务。
//
// 切片器虽然是纯逻辑，但注册进来更好：应用层直接要一个 *Splitter，
// 而不是到处 service.NewSplitter()——将来它一旦需要注入字典/配置，
// 只需改这一处。
var Register = fx.Options(
	fx.Provide(NewSplitter),
)
```

---

## A3 · application 层

### A3.1 `application/service/ingest/service.go`

```go
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/PycMono/FastRAG/common/constants"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/domain/factory"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	domainservice "github.com/PycMono/FastRAG/domain/service"
	"github.com/PycMono/FastRAG/domain/value_object"
	logsdk "github.com/PycMono/go-logger-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"
)

// batchConcurrency 批量导入的并发度。
//
// 压得很低是刻意的：真正的瓶颈在 embedding 服务，并发开大只会把下游打挂，
// 结果整体更慢（§5.5）。
const batchConcurrency = 4

// Service 文档导入应用服务。
type Service struct {
	kbRepo   knowledgerepo.IKnowledgeBaseRepo
	docRepo  knowledgerepo.IKnowledgeDocRepo
	store    knowledgerepo.IVectorStore
	embedder interfaces.IEmbeddingService
	splitter *domainservice.Splitter
	idGen    repository.IIDService
	tm       transaction.Manager
}

func NewService(
	kbRepo knowledgerepo.IKnowledgeBaseRepo,
	docRepo knowledgerepo.IKnowledgeDocRepo,
	store knowledgerepo.IVectorStore,
	embedder interfaces.IEmbeddingService,
	splitter *domainservice.Splitter,
	idGen repository.IIDService,
	tm transaction.Manager,
) *Service {
	return &Service{
		kbRepo:   kbRepo,
		docRepo:  docRepo,
		store:    store,
		embedder: embedder,
		splitter: splitter,
		idGen:    idGen,
		tm:       tm,
	}
}

// Ingest 导入一份文档。同名重导入视作「更新」。
//
// 编排见 §5.1，三步写入次序见 §5.2：
//
//	① ES 写新切片 → ② ES 删旧切片（_id 差集）→ ③ MySQL 写文档行 + 计数
//
// 「更新」这条语义的落点是 **doc_id 的复用**（第 ④ 步）：重导入必须先
// LoadByName 取回库里那一行的 id，而不是每次都分配新雪花。切片、删差集、
// 存活校验三处都以 doc_id 为轴，id 一换就全盘失效，且失败是静默的——
// 接口返回成功，用户搜到的还是旧内容（§5.3）。
//
// 顺序是"先写后删、MySQL 最后"：ES 里先有内容、再清旧的，所以中途失败时
// 这篇文档总能查到点什么，不会因为一次失败的写入把内容清空。
// MySQL 放最后是因为文档行是存活校验的依据（§7.3）——它没落，切片就算孤儿，
// 宁可查不到也不要返回错内容。
//
// **并发同名导入不在本方法的保护范围内**（§5.5）：两次并发调用可能互相删掉
// 对方的切片，只剩交集。调用方必须按 doc_name 去重。
func (s *Service) Ingest(ctx context.Context, in *dto.DocIngestDTO) (*vo.DocIngestVO, error) {
	// ① 定位 KB。kb_no 指向的库 account 不等于传入值，在这里就被挡掉（§6）
	kb, err := s.kbRepo.LoadByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return nil, err
	}

	// ② 切片参数：KB 快照 → 请求覆盖 → 归一化
	opts, err := s.resolveOptions(kb, in.SplitOptions)
	if err != nil {
		return nil, err
	}

	chunks, err := s.split(in, opts)
	if err != nil {
		return nil, err
	}
	if len(chunks) > constants.MaxIngestChunks {
		return nil, apperrors.NewParamError(fmt.Sprintf(
			"切片数 %d 超过上限 %d，请检查切片参数或文档格式", len(chunks), constants.MaxIngestChunks))
	}

	// ③ 向量化。放在任何写操作之前——失败时什么都没动，重试是干净的。
	//    索引的创建**不在这里**，那是启动期做一次的事（§9.4）。
	titleVecs, contentVecs, err := s.embedChunks(ctx, chunks)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	hash := contentHash(chunks)

	// biz_tag **只认 KB 上的那一个值**，文档级别不允许覆盖。
	//
	// 允许覆盖会让文档变得「谁都搜不到」：检索侧先用 KB 的 tag 筛库
	// （§7.1 ①），再在 ES 里 terms(biz_tag) 过滤。一个把 tag 覆盖成 finance
	// 的文档，落在 kb.biz_tag = sales 的库里——按 sales 搜，库进来了但这条被
	// ES 过滤掉；按 finance 搜，库本身就被筛掉了，这条根本没机会。
	// 两头都够不着，而且不报错（§7.2）。
	bizTag := kb.BizTag

	// ④ 定 doc_id。**同名重导入必须沿用库里那一行的 id。**
	//
	//    切片、DeleteExcept、存活校验全都以 doc_id 为轴。换一个新 id 的后果是
	//    双向失效：新内容变成一批没有文档行的孤儿（存活校验全丢掉），
	//    而 DeleteExcept 按新 id 又什么也删不到，旧切片原样留在 ES 里继续被检索——
	//    表现出来就是「导入返回成功，用户却还搜到旧内容」。
	//
	//    只有库里确实没有这一行时才分配雪花 id。
	existing, err := s.docRepo.LoadByName(ctx, kb.ID, in.DocName)
	if err != nil {
		return nil, err
	}
	var docID uint64
	if existing != nil {
		docID = existing.ID // 重导入：沿用原主键
	} else {
		docID = uint64(s.idGen.NextIntID()) // 首次导入：雪花预分配，不需要 DB 往返
	}

	vectors := factory.BuildVectorDocs(kb, docID, bizTag, chunks, titleVecs, contentVecs, now)

	// ⑤ 【第一写】ES 写新切片（§5.2 ①）
	//
	//    新内容先进去，旧切片原地不动——所以这一步失败时这篇文档仍然可查
	//    （只是内容还是旧的），重试导入即可。
	if err := s.store.Save(ctx, vectors); err != nil {
		logsdk.Error(ctx, "切片写入失败，文档内容未变",
			logsdk.Any("account", kb.Account),
			logsdk.Any("kb_no", kb.No),
			logsdk.Any("doc_id", docID),
			logsdk.Err(err),
		)
		return nil, err
	}

	// ⑥ 【刷新】把「刚写进去的」和「可能还没刷出来的旧切片」推进可检索视图
	//
	//    delete_by_query 只能作用在**已刷新的段**上，而 ES 默认 30s 才刷新一次
	//    （§4.2）。不刷新会在两处踩空：
	//
	//      · 重导入间隔 < 一个刷新周期时，上一批旧切片还没进段 → ⑦ 根本看不见
	//        它们，于是删了个寂寞，旧切片一直留到再下一次重导入才被清掉。
	//        也就是说 §12.1 那条「重导入后旧切片被清掉」的验收会**时灵时不灵**；
	//      · 调用方要「导入即可搜」时（§4.2），新切片同样还没进段。
	//
	//    两种触发条件对应这两种用途：重导入（existing != nil）必刷，
	//    首次导入只在调用方显式要求时刷——常规首次导入没有旧切片要清，
	//    没必要付这次刷新的钱。
	//
	//    刷新失败不致命：新内容会在下一次定期刷新后可见，旧切片则要等下一次
	//    重导入才清掉。记警告，不把这次导入判失败。
	if existing != nil || in.Refresh {
		if err := s.store.Refresh(ctx); err != nil {
			logsdk.Warn(ctx, "刷新索引失败，本次可能清不掉旧切片",
				logsdk.Any("doc_id", docID), logsdk.Err(err))
		}
	}

	// ⑦ 【第二写】ES 清掉这篇文档里 _id 不在新集合中的旧切片（§5.2 ②）
	//
	//    只删差集，不 DeleteByQuery(doc_id) 全删：内容没变的切片 _id 不变，
	//    留着即可，全删再写回来是白放大一次写入。
	//
	//    失败不影响正确性：旧切片多留一会儿，下次重导入会再清一遍。
	//    代价是这篇文档可能短暂地返回新旧两份内容（§11 场景 3）。
	if _, err := s.store.DeleteExcept(ctx, knowledgerepo.VectorFilter{
		Account: kb.Account, DocID: docID,
	}, factory.ChunkIDs(vectors)); err != nil {
		logsdk.Warn(ctx, "清理旧切片失败，不影响检索正确性",
			logsdk.Any("doc_id", docID),
			logsdk.Err(err),
		)
	}

	// ⑧ 【第三写】MySQL 写文档行 + 回写 KB 计数（§5.2 ③）
	//
	//    实体到这里才构造：所有字段都已定稿，不存在"先建行、后填内容"的中间态，
	//    也就不会出现"ES 写失败但库里已经记了新计数"的谎报（A2.6）。
	//
	//    重导入时这里传的 CreateTs 是本次的时间，但 Save 的 upsert 分支
	//    只写 content_hash / chunk_count / update_ts 三列（A4.4），
	//    create_ts 保持库里原值——文档的创建时间不会因为重导入而漂移。
	doc := factory.NewDoc(docID, kb, in.DocName, hash, len(chunks), now)

	var created bool
	if err := s.tm.Transaction(ctx, func(txCtx context.Context) error {
		oldChunkCount, isNew, err := s.docRepo.Save(txCtx, doc)
		if err != nil {
			return err
		}
		created = isNew
		if err := s.kbRepo.ApplyChunkDelta(txCtx, kb.ID, int64(len(chunks)-oldChunkCount)); err != nil {
			return err
		}
		if isNew {
			return s.kbRepo.ApplyDocDelta(txCtx, kb.ID, 1)
		}
		return nil
	}); err != nil {
		// 刻意不吞这个错误。文档行没落 → 刚才写进 ES 的切片成了孤儿，
		// 被 §7.3 的存活校验挡在检索之外。这是「可过滤」的一侧：
		// 不会返回错内容，只是这篇暂时搜不到。等对账重算（§11 场景 2）
		logsdk.Error(ctx, "写入文档行失败，该文档当前不可检索，等对账重算",
			logsdk.Any("account", kb.Account),
			logsdk.Any("kb_no", kb.No),
			logsdk.Any("doc_id", docID),
			logsdk.Err(err),
		)
		return nil, err
	}

	return &vo.DocIngestVO{
		DocID:      docID,
		DocName:    in.DocName,
		ChunkCount: len(chunks),
		Created:    created,
	}, nil
}

// DeleteDoc 软删文档并清理 ES（§5.4：MySQL 必须在前）。
func (s *Service) DeleteDoc(ctx context.Context, in *dto.DocDeleteDTO) (*vo.DocDeleteVO, error) {
	kb, err := s.kbRepo.LoadByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()

	var doc *knowledgeentity.KnowledgeDoc
	if err := s.tm.Transaction(ctx, func(txCtx context.Context) error {
		d, err := s.docRepo.SoftDelete(txCtx, kb.ID, in.DocName, now)
		if err != nil {
			return err
		}
		doc = d
		if err := s.kbRepo.ApplyChunkDelta(txCtx, kb.ID, -int64(d.ChunkCount)); err != nil {
			return err
		}
		return s.kbRepo.ApplyDocDelta(txCtx, kb.ID, -1)
	}); err != nil {
		return nil, err
	}

	// ES 清理放到事务之后：MySQL 是权威且即时的，ES 可以迟、可以重试。
	// 这一步失败只会留下幽灵切片，被存活校验按 doc.Deleted() 过滤掉（§5.4）
	deleted, err := s.store.DeleteByQuery(ctx, knowledgerepo.VectorFilter{
		Account: kb.Account, DocID: doc.ID,
	})
	if err != nil {
		logsdk.Error(ctx, "清理 ES 切片失败，将留下幽灵切片",
			logsdk.Any("doc_id", doc.ID), logsdk.Err(err))
		// 不向上返回：MySQL 侧的删除已经生效，对调用方来说这次删除是成功的
	}

	return &vo.DocDeleteVO{DocName: doc.Name, DeletedChunks: deleted}, nil
}

// DeleteKB 清理某个知识库在本服务里的全部派生数据。
//
// KB 行本身由外部系统维护（§1.2），这里只负责：本服务的文档软删 + ES 切片清理。
// 一旦引入删除动作就必须走这里——只删 MySQL 的话，ES 里会堆满查不到来源的幽灵切片。
func (s *Service) DeleteKB(ctx context.Context, in *dto.KBDeleteDTO) error {
	kb, err := s.kbRepo.LoadByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	if err := s.tm.Transaction(ctx, func(txCtx context.Context) error {
		_, err := s.docRepo.SoftDeleteByKBID(txCtx, kb.ID, kb.Account, now)
		return err
	}); err != nil {
		return err
	}

	if _, err := s.store.DeleteByQuery(ctx, knowledgerepo.VectorFilter{
		Account: kb.Account, KBID: kb.ID,
	}); err != nil {
		logsdk.Error(ctx, "清理 ES 切片失败", logsdk.Any("kb_id", kb.ID), logsdk.Err(err))
	}
	return nil
}

// ─── 批量导入（P1，§5.5）─────────────────────────────────────────────────────

// BatchResult 批量导入结果。
type BatchResult struct {
	Total   int               `json:"total"`
	Success int               `json:"success"`
	Failed  int               `json:"failed"`
	Items   []*vo.DocIngestVO `json:"items"`
	Errors  []*BatchError     `json:"errors"`
}

// BatchError 单条失败明细。
type BatchError struct {
	DocName string `json:"doc_name"`
	Message string `json:"message"`
}

// docKey 文档的同一性，与 MySQL 的唯一键 (kb_id, name) 对齐。
//
// 用 kb_no 而不是 kb_id：这一层还没查库，拿不到自增主键。
// kb_no 到 kb_id 是一对一（§4.1），所以在这里做去重等价。
type docKey struct {
	Account string
	KBNo    string
	DocName string
}

// duplicateDocNames 找出批次内出现多次的文档。
//
// 为什么是拒绝而不是「后一条覆盖前一条」：同名两条该留哪一份是业务决策，
// 服务端替调用方定夺，只会让「我明明传了正确的那份」变成无从解释。
// 也不能靠并发写去碰运气——同一个 doc_id 上两路 DeleteExcept 会互相删掉
// 对方刚写的切片，最终只剩两边集合的交集，内容被截断（§5.5）。
//
// 必须**同步**跑完再起工作池：放进 goroutine 里就回到竞态了。
func duplicateDocNames(items []*dto.DocIngestDTO) map[docKey]struct{} {
	count := make(map[docKey]int, len(items))
	for _, in := range items {
		count[docKey{in.Account, in.KBNo, in.DocName}]++
	}
	dup := make(map[docKey]struct{})
	for k, n := range count {
		if n > 1 {
			dup[k] = struct{}{}
		}
	}
	return dup
}

// BatchIngest 批量导入：受限并发 + 单条错误隔离。
//
// 同步执行，受 HTTP 超时约束；超大文档由上游分批调用。
// 不用事务包整批：一条失败不该把已经灌好的几十条一起回滚。
func (s *Service) BatchIngest(ctx context.Context, items []*dto.DocIngestDTO) *BatchResult {
	res := &BatchResult{Total: len(items)}

	// 批次内同名的先挑出来标记失败并排除。整批不因它们返回 400——
	// 控制器已约定「恒返回 200 + 明细」（A4.13），合法的那几十条照常导。
	dup := duplicateDocNames(items)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, batchConcurrency)

	for _, item := range items {
		if _, isDup := dup[docKey{item.Account, item.KBNo, item.DocName}]; isDup {
			res.Failed++
			res.Errors = append(res.Errors, &BatchError{
				DocName: item.DocName,
				Message: "同一批次内 doc_name 重复，无法判定以哪一份为准，本批全部跳过",
			})
			continue
		}

		wg.Add(1)
		go func(in *dto.DocIngestDTO) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				res.Failed++
				res.Errors = append(res.Errors, &BatchError{
					DocName: in.DocName, Message: ctx.Err().Error(),
				})
				mu.Unlock()
				return
			}

			out, err := s.Ingest(ctx, in)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Failed++
				res.Errors = append(res.Errors, &BatchError{
					DocName: in.DocName, Message: err.Error(),
				})
				return
			}
			res.Success++
			res.Items = append(res.Items, out)
		}(item)
	}
	wg.Wait()

	// 并发完成顺序不稳定，返回前定序——否则同一批请求两次返回的顺序都不一样
	sort.Slice(res.Items, func(i, j int) bool { return res.Items[i].DocName < res.Items[j].DocName })
	sort.Slice(res.Errors, func(i, j int) bool { return res.Errors[i].DocName < res.Errors[j].DocName })

	return res
}

// ─── 内部 ────────────────────────────────────────────────────────────────────

func (s *Service) resolveOptions(
	kb *knowledgeentity.KnowledgeBase, override *dto.SplitOptionsDTO,
) (value_object.ChunkOptions, error) {
	opts := value_object.ParseSplitOptions(kb.SplitOptions)
	if override != nil {
		opts = opts.Override(&value_object.ChunkOptions{
			ChunkSize:  override.ChunkSize,
			SplitLevel: override.SplitLevel,
			MinChunk:   override.MinChunk,
		})
	}
	return opts.Normalize()
}

func (s *Service) split(
	in *dto.DocIngestDTO, opts value_object.ChunkOptions,
) (knowledgeentity.Chunks, error) {
	if in.Format == constants.FormatChunks {
		inputs := make([]value_object.ChunkInput, 0, len(in.Chunks))
		for _, c := range in.Chunks {
			inputs = append(inputs, value_object.ChunkInput{Title: c.Title, Content: c.Content})
		}
		return s.splitter.Normalize(inputs)
	}
	return s.splitter.Split(in.Content, in.Format, opts)
}

// embedChunks 算两路向量。
//
// title_vec 与 content_vec 分开存：标题短、噪声低，适合精排；
// 正文长、信息全，适合召回。检索时按 search_mode 决定用哪一路（§7.2）。
func (s *Service) embedChunks(
	ctx context.Context, chunks knowledgeentity.Chunks,
) (titleVecs, contentVecs [][]float32, err error) {
	titleVecs, err = s.embedder.EmbedDocs(ctx, chunks.Titles())
	if err != nil {
		return nil, nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	contentVecs, err = s.embedder.EmbedDocs(ctx, factory.EmbeddingTexts(chunks))
	if err != nil {
		return nil, nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	// 条数对不上说明实现有问题。不能放过去——错位的向量会静默地
	// 把 A 切片的向量挂到 B 切片上，检索出的结果全错但看起来一切正常
	if len(titleVecs) != len(chunks) || len(contentVecs) != len(chunks) {
		return nil, nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
			"向量条数不匹配：标题 %d / 正文 %d，切片 %d",
			len(titleVecs), len(contentVecs), len(chunks)))
	}
	return titleVecs, contentVecs, nil
}

// contentHash 对**切片结果**取指纹，而不是对原文取。
//
// 判断依据应该是「最终索引内容有没有变」：原文改了排版、空白但切出来一样，
// 重灌一遍纯属浪费 embedding 调用。
func contentHash(chunks knowledgeentity.Chunks) string {
	h := sha256.New()
	for _, c := range chunks {
		h.Write([]byte(c.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
```

### A3.2 `application/service/search/rrf.go`

```go
package search

import (
	"sort"

	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
)

// fusionOversample 融合前先把候选放大若干倍。
//
// 两路各取 topN，融合后靠前的位置可能凑不满 N 条；后面还有存活校验
// 会再砍一刀（§7.3）。放大一点，保证最终还能凑够 limit。
const fusionOversample = 4

// Route 一路检索结果 + 它在融合里的权重。
//
// 「一路」= 一个分组的一条检索路径（BM25 或 kNN）。分组见 §7.2：
// 不同 search_mode 的库不并集检索，所以多库查询会产生多路。
type Route struct {
	Hits   []knowledgerepo.VectorHit
	Weight float64
}

// Fuse 用加权 RRF 融合多路结果（§7.2）。
//
//	score(d) = Σ_r  w_r / (k + rank_r(d))
//
// 关键在 **rank_r 是「这一路内部」的名次**，不是把所有路拼成一个大列表之后的名次。
// 拼成大列表再算名次会有排序偏置：后拼接的那一路的第一名，名次会被排到
// 前一路所有命中之后，等于凭空判它出局。多分组检索（§7.2）下这会让其中一组
// 的召回永远沉底，而结果还"看起来挺正常"。
//
// 因为每一路各自从 rank=1 起算，Σ 是对称的，**路的先后顺序不影响结果**。
//
// 权重让 dense_weight 真的起作用：w_knn = dense_weight，w_bm25 = 1 - dense_weight。
// 只发一路时（dense_weight 取 0 或 1）权重退化成常数，不影响名次。
//
// 不用 ES 原生的 rrf retriever：那是 basic license 下的付费特性，
// 会直接报 non-compliant。核心检索不能绑在付费特性上。
func Fuse(routes []Route, k, limit int) []knowledgerepo.VectorHit {
	if k <= 0 {
		k = 60 // RRF 论文的默认值
	}

	type entry struct {
		hit   knowledgerepo.VectorHit
		score float64
	}

	acc := make(map[string]*entry, 256)
	for _, r := range routes {
		if r.Weight <= 0 {
			continue // 权重为 0 的路不参与：贡献恒为 0，跳过省一遍遍历
		}
		for rank, h := range r.Hits {
			e, ok := acc[h.ChunkID]
			if !ok {
				e = &entry{hit: h}
				acc[h.ChunkID] = e
			}
			// rank 从 0 起，公式里的名次是 1-based
			e.score += r.Weight / float64(k+rank+1)
		}
	}

	out := make([]knowledgerepo.VectorHit, 0, len(acc))
	for _, e := range acc {
		e.hit.Score = e.score
		out = append(out, e.hit)
	}

	// 同分时按 chunk_id 定序：map 遍历顺序是随机的，
	// 不 tie-break 的话同一个请求两次返回的顺序会不一样
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ChunkID < out[j].ChunkID
	})

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
```

### A3.3 `application/service/search/service.go`

```go
package search

import (
	"context"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/domain/interfaces"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/domain/value_object"
	logsdk "github.com/PycMono/go-logger-sdk"
)

// Service 检索应用服务。
type Service struct {
	kbRepo   knowledgerepo.IKnowledgeBaseRepo
	docRepo  knowledgerepo.IKnowledgeDocRepo
	store    knowledgerepo.IVectorStore
	embedder interfaces.IEmbeddingService
	rerank   interfaces.IRerankService // P2，可为 nil
	tuning   value_object.SearchTuning
}

func NewService(
	kbRepo knowledgerepo.IKnowledgeBaseRepo,
	docRepo knowledgerepo.IKnowledgeDocRepo,
	store knowledgerepo.IVectorStore,
	embedder interfaces.IEmbeddingService,
	rerank interfaces.IRerankService,
	tuning value_object.SearchTuning,
) *Service {
	return &Service{
		kbRepo:   kbRepo,
		docRepo:  docRepo,
		store:    store,
		embedder: embedder,
		rerank:   rerank,
		tuning:   tuning.WithDefaults(),
	}
}

// Search 混合检索。编排见 §7.1。
func (s *Service) Search(ctx context.Context, in *dto.SearchDTO) (*vo.SearchResultVO, error) {
	opts, err := s.resolveOptions(in)
	if err != nil {
		return nil, err
	}
	empty := &vo.SearchResultVO{Items: []*vo.SearchItemVO{}}

	// ① 加载 KB。不属于该 account 的库在仓储层就被丢掉了（§6）
	kbs, err := s.kbRepo.LoadByNos(ctx, in.KBNos, in.Account)
	if err != nil {
		return nil, err
	}
	kbs = kbs.FilterByBizTags(opts.BizTags)
	if len(kbs) == 0 {
		return empty, nil
	}

	// ② 查询向量。dense_weight 为 0 时不发这一路，也就不必花这次调用
	var queryVec []float32
	if opts.UseKNN() {
		queryVec, err = s.embedder.EmbedQuery(ctx, opts.Query)
		if err != nil {
			return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
		}
	}

	// ③ 分组检索：按 search_mode 把库分组，每组各发一次请求
	//
	//    不取并集——那会悄悄把标题模式的库按正文搜，等于改写了它的语义（§7.2）。
	//    最多两组，组内字段一致所以能合成一次请求。
	groups := groupBySearchMode(kbs)
	pool := make([]knowledgerepo.SearchResult, 0, len(groups))
	for _, g := range groups {
		r, err := s.store.Search(ctx, s.buildReq(g, opts, queryVec))
		if err != nil {
			return nil, err
		}
		pool = append(pool, r)
	}

	// ④ 应用层加权 RRF 融合
	//
	//    逐组逐路喂进去，**不先把各组拼成一个大列表**——那样后一组的
	//    第一名会被排到前一组所有命中之后，等于按分组顺序给了个固定的偏置
	//    （A3.2 的 Fuse 注释）。每组两路各自从 rank=1 起算就不偏。
	routes := make([]Route, 0, 2*len(pool))
	for _, r := range pool {
		// 空的一路（dense_weight 把它短路掉了）不占位，权重为 0 的路 Fuse 会跳过
		if len(r.BM25) > 0 {
			routes = append(routes, Route{Hits: r.BM25, Weight: 1 - opts.DenseWeight})
		}
		if len(r.KNN) > 0 {
			routes = append(routes, Route{Hits: r.KNN, Weight: opts.DenseWeight})
		}
	}
	merged := Fuse(routes, s.tuning.RankConstant, opts.Limit*fusionOversample)

	// ⑤ 存活校验 + 补展示字段（1 次批量 SQL，不取内容——D1）
	items, err := s.filterAlive(ctx, merged, kbs)
	if err != nil {
		return nil, err
	}

	// ⑥ 可选重排（P2）。未实现时接口返回原序，不阻断检索
	if opts.Rerank && s.rerank != nil {
		items, err = s.rerankItems(ctx, opts.Query, items)
		if err != nil {
			logsdk.Warn(ctx, "重排失败，退回融合原序", logsdk.Err(err))
		}
	}

	if len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return &vo.SearchResultVO{Items: items}, nil
}

func (s *Service) resolveOptions(in *dto.SearchDTO) (value_object.SearchOptions, error) {
	weight := s.tuning.DenseWeight
	if in.DenseWeight != nil {
		weight = *in.DenseWeight
	}
	return value_object.SearchOptions{
		Query:       in.Query,
		Limit:       in.Limit,
		BizTags:     in.BizTags,
		DenseWeight: weight,
		Rerank:      in.RerankSwitch,
	}.Normalize()
}

// searchModeGroup 一组 search_mode 相同的库。
type searchModeGroup struct {
	titleOnly bool
	kbs       knowledgeentity.KnowledgeBases
}

// groupBySearchMode 按「是否只查标题」把库分组。
//
// 为什么不取并集：只要有一个库开了正文检索，就全局按正文检索的话，
// 那些建库时明确声明「只用标题」的库就被按正文搜了——召回里会冒出
// 一堆标题对不上、正文里却有这个词的切片。那是静默改写语义（§7.2）。
func groupBySearchMode(kbs knowledgeentity.KnowledgeBases) []searchModeGroup {
	var contentSearch, titleSearch knowledgeentity.KnowledgeBases
	for _, kb := range kbs {
		if kb.UsesTitleOnly() {
			titleSearch = append(titleSearch, kb)
		} else {
			contentSearch = append(contentSearch, kb)
		}
	}

	groups := make([]searchModeGroup, 0, 2)
	if len(contentSearch) > 0 {
		groups = append(groups, searchModeGroup{titleOnly: false, kbs: contentSearch})
	}
	if len(titleSearch) > 0 {
		groups = append(groups, searchModeGroup{titleOnly: true, kbs: titleSearch})
	}
	return groups
}

func (s *Service) buildReq(
	g searchModeGroup,
	opts value_object.SearchOptions,
	queryVec []float32,
) knowledgerepo.VectorSearchReq {
	ids := make([]uint64, 0, len(g.kbs))
	for _, kb := range g.kbs {
		ids = append(ids, kb.ID)
	}

	return knowledgerepo.VectorSearchReq{
		// 组内必然同 account：LoadByNos 加载时已按 account 过滤过（§6）
		Account:       g.kbs[0].Account,
		KBIDs:         ids,
		BizTags:       opts.BizTags,
		Query:         opts.Query,
		QueryVec:      queryVec,
		TitleOnly:     g.titleOnly,
		DenseWeight:   opts.DenseWeight,
		BM25Top:       s.tuning.BM25Top,
		KNNTops:       s.tuning.KNNTops,
		NumCandidates: s.tuning.NumCandidates,
	}
}

// filterAlive 丢掉孤儿与幽灵切片（§7.3）。
//
// 两个要点：
//  1. doc_id 先去重再查库——召回 200 条通常只落在几十个文档上，
//     按召回条数去查数据库是纯浪费；
//  2. 内容不回表取（D1），这里只补 doc_name / kb_name 两个展示字段。
//
// 它同时兜住了「写一半失败」和「删一半失败」两种残留：
//   - 孤儿：切片写进 ES 了，但 MySQL 文档行没落（§5.2 ③ 失败）→ 查不到 doc，丢弃；
//   - 幽灵：MySQL 已软删，ES 还没清完（§5.4）→ 查不到 doc，丢弃。
//
// 它的前提是 doc_id 稳定：全靠切片带的 doc_id 去查行，所以 §5.1 第 ④ 步
// 必须复用已有行的 id（§5.3）。若重导入换了新 id，新切片查不到行被全部丢掉，
// 旧切片反而一路通行——那正是"导入成功却还搜到旧内容"。
//
// 但它**兜不住**重导入时的重复召回：新旧切片 doc_id 相同、文档行是同一行，
// 存活校验看不出差别（§11 场景 3/4）。写侧一致性本期不做（§13 待定 9）。
func (s *Service) filterAlive(
	ctx context.Context,
	hits []knowledgerepo.VectorHit,
	kbs knowledgeentity.KnowledgeBases,
) ([]*vo.SearchItemVO, error) {
	if len(hits) == 0 {
		return []*vo.SearchItemVO{}, nil
	}

	idSet := make(map[uint64]struct{}, len(hits))
	for _, h := range hits {
		idSet[h.DocID] = struct{}{}
	}
	ids := make([]uint64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	docs, err := s.docRepo.LoadByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	alive := docs.AliveMap()
	kbMap := kbs.IndexByID()

	items := make([]*vo.SearchItemVO, 0, len(hits))
	for _, h := range hits {
		doc, ok := alive[h.DocID]
		if !ok {
			continue // 孤儿 / 幽灵切片：静默丢弃，这正是 D4 想要的效果
		}
		item := &vo.SearchItemVO{
			ChunkID:     h.ChunkID,
			DocID:       h.DocID,
			DocName:     doc.Name,
			KBID:        h.KBID,
			Order:       h.Order,
			Title:       h.Title,
			Content:     h.Content,
			HeadingPath: h.HeadingPath,
			Score:       h.Score,
		}
		if kb, ok := kbMap[h.KBID]; ok {
			item.KBName = kb.Name
			item.KBNo = kb.No
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) rerankItems(
	ctx context.Context, query string, items []*vo.SearchItemVO,
) ([]*vo.SearchItemVO, error) {
	cands := make([]interfaces.RerankCandidate, len(items))
	for i, it := range items {
		cands[i] = interfaces.RerankCandidate{ChunkID: it.ChunkID, Content: it.Content}
	}

	order, err := s.rerank.Rerank(ctx, query, cands, len(items))
	if err != nil {
		return nil, err
	}

	out := make([]*vo.SearchItemVO, 0, len(order))
	for _, idx := range order {
		if idx >= 0 && idx < len(items) {
			out = append(out, items[idx])
		}
	}
	return out, nil
}
```

### A3.4 `application/service/register.go`（改写）

```go
package service

import (
	"github.com/PycMono/FastRAG/application/service/ingest"
	"github.com/PycMono/FastRAG/application/service/search"
	"go.uber.org/fx"
)

// Register 注册所有应用层服务。
//
// 原来的 knowledge.NewService 已去掉：KB 管理移出范围（§1.2），
// KB 只读，没有写用例。
var Register = fx.Options(
	fx.Provide(ingest.NewService),
	fx.Provide(search.NewService),
)
```

### A3.5 `application/service/ingest/reconcile.go`（P1 计数校正）

```go
package ingest

import (
	"context"
)

// ReconcileResult 单个知识库的对账结果。
type ReconcileResult struct {
	KBNo             string `json:"kb_no"`
	DocCountBefore   int64  `json:"doc_count_before"`
	DocCountAfter    int64  `json:"doc_count_after"`
	ChunkCountBefore int64  `json:"chunk_count_before"`
	ChunkCountAfter  int64  `json:"chunk_count_after"`
}

// Reconcile 重算某个知识库的 doc_count / chunk_count 并回写。
//
// 计数是用差值维护的（§5.1），删除与异常会让它慢慢漂移——漂移只影响
// 列表页那几个数字，不影响检索正确性，所以做成离线任务而不是实时强一致。
//
// **只信 MySQL**，不去 ES 数切片：ES 是弱一致的那一侧（§5.2），
// 拿它当权威会把孤儿切片算进真实数据里。
func (s *Service) Reconcile(ctx context.Context, kbNo, account string) (*ReconcileResult, error) {
	kb, err := s.kbRepo.LoadByNo(ctx, kbNo, account)
	if err != nil {
		return nil, err
	}

	docCount, err := s.docRepo.CountByKBID(ctx, kb.ID)
	if err != nil {
		return nil, err
	}
	chunkCount, err := s.docRepo.SumChunkCountByKBID(ctx, kb.ID)
	if err != nil {
		return nil, err
	}

	// 用绝对值回写而不是补差值：对账的意义就是「以真实数据为准」，
	// 再走一次差值只是把漂移原样带过去
	if err := s.kbRepo.SetCounts(ctx, kb.ID, docCount, chunkCount); err != nil {
		return nil, err
	}

	return &ReconcileResult{
		KBNo:             kb.No,
		DocCountBefore:   kb.DocCount, DocCountAfter: docCount,
		ChunkCountBefore: kb.ChunkCount, ChunkCountAfter: chunkCount,
	}, nil
}
```

> 这需要给 `IKnowledgeBaseRepo` 加一个 `SetCounts(ctx, kbID, docCount, chunkCount int64) error`（绝对值写入，与 `ApplyChunkDelta` 的差值写入区分开）。A4.3 里写了。

---

## A4 · infrastructure 层

### A4.1 `infrastructure/config/config.go`（增补）

```go
// Config 追加四个配置块
type Config struct {
	// …既有字段不变…
	Elasticsearch ESConfig        `json:"elasticsearch"`
	Embedding     EmbeddingConfig `json:"embedding"`
	Search        SearchConfig    `json:"search"`
	Security      SecurityConfig  `json:"security"`
}

// SecurityConfig 部署前提的显式确认（§6.1）。
//
// 默认零值 = false = 拒绝启动。这是刻意的：让「开箱即跑」这个动作
// 在部署时（有人看日志）失败，而不是在生产时静默地不设防。
type SecurityConfig struct {
	TrustRequestAccount bool `json:"trust_request_account"`
}

// ESConfig Elasticsearch 配置
type ESConfig struct {
	Addrs           []string `json:"addrs"`       // 必须带 scheme，如 http://127.0.0.1:9200
	Index           string   `json:"index"`       // 留空用 es.IndexName
	Username        string   `json:"username"`
	Password        string   `json:"password"`
	Dim             int      `json:"dim"`             // 必须与 embedding 模型一致
	Analyzer        string   `json:"analyzer"`        // 索引分词器
	SearchAnalyzer  string   `json:"search_analyzer"` // 查询分词器
	Shards          int      `json:"shards"`
	Replicas        int      `json:"replicas"`
	RefreshInterval string   `json:"refresh_interval"`
}

// EmbeddingConfig 向量化服务配置（OpenAI 兼容协议）
type EmbeddingConfig struct {
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	Model       string `json:"model"`
	Dim         int    `json:"dim"`
	BatchSize   int    `json:"batch_size"`
	TimeoutMS   int    `json:"timeout_ms"`
	QueryPrefix string `json:"query_prefix"` // 非对称编码前缀，如 "query: "；多数模型留空
}

// SearchConfig 检索调参
type SearchConfig struct {
	BM25Top       int     `json:"bm25_top"`
	KNNTops       int     `json:"knn_top"`
	NumCandidates int     `json:"num_candidates"`
	RankConstant  int     `json:"rank_constant"`
	DenseWeight   float64 `json:"dense_weight"`
}
```

### A4.2 `infrastructure/driver/es/es.go`

```go
package es

import (
	"fmt"

	"github.com/PycMono/FastRAG/infrastructure/config"
	elasticsearch "github.com/elastic/go-elasticsearch/v9"
)

// NewClient 构造 ES 客户端。
//
// 用 esapi（低层 API）而不是 v9 的 typed API：我们的查询体是手写 JSON，
// 低层 API 直接吃 io.Reader，反而比 typed API 那套泛型 option 链更直白。
func NewClient(conf *config.Config) (*elasticsearch.Client, error) {
	if len(conf.Elasticsearch.Addrs) == 0 || conf.Elasticsearch.Addrs[0] == "" {
		return nil, fmt.Errorf("elasticsearch.addrs 未配置")
	}

	client, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: conf.Elasticsearch.Addrs,
		Username:  conf.Elasticsearch.Username,
		Password:  conf.Elasticsearch.Password,

		MaxRetries: 3,
		// 429 也要重试：ES 写入队列满时会限流，这正是最该退避重试的场景
		RetryOnStatus: []int{502, 503, 504, 429},
	})
	if err != nil {
		return nil, fmt.Errorf("init elasticsearch client failed: %w", err)
	}
	return client, nil
}
```

> ⚠️ `NewClient` **不发探活请求**。ES 没起来时服务照样能启动，直到第一次导入才报错。
> 这是刻意的：本地开发常常先起服务后起 ES。生产上请用 `/ready` 探针兜住（P2）。

### A4.3 `infrastructure/persistence/knowledge/knowledge_base_repo.go`

```go
package knowledge

import (
	"context"
	"errors"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/infrastructure/persistence/mapper"
	"github.com/PycMono/FastRAG/infrastructure/persistence/po"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"gorm.io/gorm"
)

// KnowledgeBaseRepo 知识库仓储（只读）。
//
// 这一层是租户隔离的最后一道闸：所有语句都带 account = ?，
// 漏写在 code review 里能看出来，靠每个用例自己记得加则看不出来（§6）。
type KnowledgeBaseRepo struct {
	provider sqlsdk.Provider
}

func NewKnowledgeBaseRepo(provider sqlsdk.Provider) knowledgerepo.IKnowledgeBaseRepo {
	return &KnowledgeBaseRepo{provider: provider}
}

func (r *KnowledgeBaseRepo) db(ctx context.Context) *gorm.DB {
	return r.provider.UseDB(ctx).Model(&po.KnowledgeBase{})
}

func (r *KnowledgeBaseRepo) LoadByNo(
	ctx context.Context, no, account string,
) (*knowledgeentity.KnowledgeBase, error) {
	var row po.KnowledgeBase
	err := r.db(ctx).
		Where("no = ? AND account = ? AND delete_ts = 0", no, account).
		First(&row).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 「编号不存在」和「编号存在但不属于你」返回同一个错误。
		// 区分开的话，接口就成了租户枚举器：拿别人的 kb_no 反复试，
		// 就能从错误信息里推断出哪些编号真实存在
		return nil, apperrors.ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return mapper.ToKnowledgeBase(&row), nil
}

func (r *KnowledgeBaseRepo) LoadByNos(
	ctx context.Context, nos []string, account string,
) (knowledgeentity.KnowledgeBases, error) {
	if len(nos) == 0 {
		return nil, nil
	}

	var rows []po.KnowledgeBase
	if err := r.db(ctx).
		Where("no IN ? AND account = ? AND delete_ts = 0", nos, account).
		Find(&rows).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	// 查不到的静默跳过：调用方要的是「这些库里能查到的部分」，
	// 其中一个编号写错不该让整次检索失败
	return mapper.ToKnowledgeBases(rows), nil
}

func (r *KnowledgeBaseRepo) ApplyChunkDelta(ctx context.Context, kbID uint64, delta int64) error {
	if delta == 0 {
		return nil
	}
	return r.applyDelta(ctx, kbID, "chunk_count", delta)
}

func (r *KnowledgeBaseRepo) ApplyDocDelta(ctx context.Context, kbID uint64, delta int64) error {
	if delta == 0 {
		return nil
	}
	return r.applyDelta(ctx, kbID, "doc_count", delta)
}

func (r *KnowledgeBaseRepo) applyDelta(ctx context.Context, kbID uint64, column string, delta int64) error {
	// CAST(... AS SIGNED) 不能省：chunk_count 是 BIGINT UNSIGNED，
	// 直接加负数在 MySQL 上会报 "BIGINT UNSIGNED value is out of range"，
	// 而 GREATEST(..., 0) 则保证计数永远不会掉成负数
	err := r.db(ctx).
		Where("id = ?", kbID).
		Updates(map[string]any{
			column: gorm.Expr("GREATEST(CAST("+column+" AS SIGNED) + ?, 0)", delta),
			"update_ts": time.Now().UnixMilli(),
		}).Error
	if err != nil {
		return apperrors.ErrInternal.Wrap(err)
	}
	return nil
}

// SetCounts 用绝对值覆盖计数，供对账任务使用（§A3.5）。
func (r *KnowledgeBaseRepo) SetCounts(ctx context.Context, kbID, docCount, chunkCount int64) error {
	err := r.db(ctx).
		Where("id = ?", kbID).
		Updates(map[string]any{
			"doc_count":   docCount,
			"chunk_count": chunkCount,
			"update_ts":   time.Now().UnixMilli(),
		}).Error
	if err != nil {
		return apperrors.ErrInternal.Wrap(err)
	}
	return nil
}
```

> `column` 是内部常量（`"chunk_count"` / `"doc_count"`），不来自用户输入，所以字符串拼接安全。

### A4.4 `infrastructure/persistence/knowledge/knowledge_doc_repo.go`

```go
package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/infrastructure/persistence/mapper"
	"github.com/PycMono/FastRAG/infrastructure/persistence/po"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KnowledgeDocRepo 文档仓储。
//
// 为什么还要注入 transaction.Manager：Save 是「锁行读旧值 → upsert」两步，
// 必须整体在一个事务里（见 Save 注释）。sqlsdk.Provider 只暴露 UseDB，
// 给不了事务，所以额外依赖 Manager。
// TransProvider 本来就同时实现了这两个接口，fx 里各 provide 一份即可（A4.16）。
type KnowledgeDocRepo struct {
	provider sqlsdk.Provider
	tm       transaction.Manager
}

func NewKnowledgeDocRepo(
	provider sqlsdk.Provider,
	tm transaction.Manager,
) knowledgerepo.IKnowledgeDocRepo {
	return &KnowledgeDocRepo{provider: provider, tm: tm}
}

func (r *KnowledgeDocRepo) db(ctx context.Context) *gorm.DB {
	return r.provider.UseDB(ctx).Model(&po.KnowledgeDoc{})
}

func (r *KnowledgeDocRepo) LoadByName(
	ctx context.Context, kbID uint64, name string,
) (*knowledgeentity.KnowledgeDoc, error) {
	var row po.KnowledgeDoc
	err := r.db(ctx).
		Where("kb_id = ? AND name = ? AND delete_ts = 0", kbID, name).
		First(&row).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 「不存在」是重导入路径上的正常分支，不是错误。
		// 返回 error 会逼着每个调用方写一遍 errors.Is 判断
		return nil, nil
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return mapper.ToKnowledgeDoc(&row), nil
}

func (r *KnowledgeDocRepo) LoadByIDs(
	ctx context.Context, ids []uint64,
) (knowledgeentity.KnowledgeDocs, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var rows []po.KnowledgeDoc
	// 这里刻意**不**加 delete_ts = 0：软删的也要捞回来，
	// 统一交给 AliveMap 判断。带上条件的话，「文档被删了」会退化成
	// 「文档不存在」，两条不同的异常路径混成一条，排查时看不出真相
	if err := r.db(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return mapper.ToKnowledgeDocs(rows), nil
}

// Save 写入/更新文档行，返回**更新前**的切片数与「是否新建」（§5.2 ③）。
//
// 语义是 upsert，键 (kb_id, name)。刻意不复用 GORM 的 clause.OnConflict 一把梭，
// 而是「先锁行读旧值 → 再 upsert」的两步——因为 oldChunkCount 必须和这次写入
// 原子地取值，否则调用方拿它去算 KB 计数差值时会算错（§5.1 第 ⑧ 步）。
//
//	SELECT ... FOR UPDATE  ← 拿行锁 + 旧 chunk_count
//	INSERT ... ON DUPLICATE KEY UPDATE content_hash/chunk_count/update_ts
//
// 只更新那三列，**绝不动 id 和 create_ts**：
//   - id 一动，ES 里那批切片的 doc_id 就指向一个不存在的行了；
//   - create_ts 一动，「文档创建时间」这个对外字段就变成"最后导入时间"。
func (r *KnowledgeDocRepo) Save(
	ctx context.Context, doc *knowledgeentity.KnowledgeDoc,
) (oldChunkCount int, created bool, err error) {
	row := mapper.ToKnowledgeDocPO(doc)

	err = r.tm.Transaction(ctx, func(txCtx context.Context) error {
		// ① 锁行读旧值。FOR UPDATE 而不是普通 SELECT：后面要拿 oldChunkCount
		//    算差值，读到一半被并发写改了，差值就是错的
		var cur po.KnowledgeDoc
		e := r.db(txCtx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("kb_id = ? AND name = ? AND delete_ts = 0", doc.KBID, doc.Name).
			First(&cur).Error

		switch {
		case errors.Is(e, gorm.ErrRecordNotFound):
			// 没有活着的同名行 → 本次插入一行新的。oldChunkCount 自然是 0
			//
			// 含「软删后同名重建」：软删那行 delete_ts != 0，不在这条查询的范围内，
			// 唯一键 (kb_id, name, delete_ts) 因此被让了出来，落库的是一行**新 id**
			// 的行。调用方那边 LoadByName 同样查不到，于是分配了新雪花——两边一致。
			// 对调用方而言这确实是新建（doc_id 变了），报 created = true 是实话。
			created = true
		case e != nil:
			return apperrors.ErrInternal.Wrap(e)
		default:
			oldChunkCount = int(cur.ChunkCount) // PO 是 int32（对齐 DDL 的 INT），实体统一用 int
			// 查到了活着的同名行 → 本次是覆盖。
			//
			// 这里**不能**用「主键是不是我刚生成的那个」来判：重导入恰恰是
			// 复用库里那一行的 id（§5.3），于是 cur.Id == row.Id 恒成立，
			// 条件退化成恒真，每次重导入都会报 created = true。
			// 后果是 KB 的 doc_count 每重导入一次 +1，越滚越大（实测过：
			// 同一篇文档重导入两次，doc_count 从 1 变成 3）。
			created = false
		}

		// ② upsert。走 GORM 的 OnConflict 是为了让「并发时在唯一键上等待」
		//    这件事交给数据库，而不是自己写 INSERT ... ON DUPLICATE KEY UPDATE 字符串
		if err := r.db(txCtx).Clauses(clause.OnConflict{
			// MySQL 会忽略 Columns，实际走「任意唯一键冲突即更新」；
			// 写上是为了让意图在代码里读得出来
			Columns: []clause.Column{{Name: "kb_id"}, {Name: "name"}, {Name: "delete_ts"}},
			DoUpdates: clause.Assignments(map[string]any{
				"content_hash": doc.ContentHash,
				"chunk_count":  doc.ChunkCount,
				"update_ts":    doc.UpdateTs,
			}),
		}).Create(row).Error; err != nil {
			return apperrors.ErrInternal.Wrap(err)
		}
		return nil
	})

	if err != nil {
		return 0, false, err
	}
	return oldChunkCount, created, nil
}

func (r *KnowledgeDocRepo) SoftDelete(
	ctx context.Context, kbID uint64, name string, now int64,
) (*knowledgeentity.KnowledgeDoc, error) {
	var row po.KnowledgeDoc
	err := r.db(ctx).
		Where("kb_id = ? AND name = ? AND delete_ts = 0", kbID, name).
		First(&row).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.NewParamError("文档不存在: " + name)
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	// 只置 delete_ts，不挪 name。唯一键是 (kb_id, name, delete_ts)，
	// 置了新时间戳就等于把老键让了出来，同名重导入自然会插一行新的
	if err := r.db(ctx).
		Where("id = ? AND delete_ts = 0", row.Id).
		Updates(map[string]any{"delete_ts": now, "update_ts": now}).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	row.DeleteTs, row.UpdateTs = now, now
	return mapper.ToKnowledgeDoc(&row), nil
}

func (r *KnowledgeDocRepo) SoftDeleteByKBID(
	ctx context.Context, kbID uint64, account string, now int64,
) (int64, error) {
	tx := r.db(ctx).
		Where("kb_id = ? AND account = ? AND delete_ts = 0", kbID, account).
		Updates(map[string]any{"delete_ts": now, "update_ts": now})
	if tx.Error != nil {
		return 0, apperrors.ErrInternal.Wrap(tx.Error)
	}
	return tx.RowsAffected, nil
}

func (r *KnowledgeDocRepo) CountByKBID(ctx context.Context, kbID uint64) (int64, error) {
	var n int64
	if err := r.db(ctx).
		Where("kb_id = ? AND delete_ts = 0", kbID).
		Count(&n).Error; err != nil {
		return 0, apperrors.ErrInternal.Wrap(err)
	}
	return n, nil
}

func (r *KnowledgeDocRepo) SumChunkCountByKBID(ctx context.Context, kbID uint64) (int64, error) {
	// COALESCE 不能省：一个文档都没有时 SUM 返回 NULL，
	// 扫进 int64 会直接报 "converting NULL to int64 is unsupported"
	var sum sql.NullInt64
	if err := r.db(ctx).
		Select("COALESCE(SUM(chunk_count), 0) AS total").
		Where("kb_id = ? AND delete_ts = 0", kbID).
		Scan(&sum).Error; err != nil {
		return 0, apperrors.ErrInternal.Wrap(err)
	}
	return sum.Int64, nil
}
```

### A4.5 `infrastructure/persistence/mapper/knowledge.go`

> 前置：`po.KnowledgeDoc` **不加新字段**——一致性增强（代次列）本期不做（§2 D4、§13 待定 9）。
> 本次 mapper 只做一件事：把 `Ver` 映射删掉。

```go
package mapper

import (
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/infrastructure/persistence/po"
)

// ToKnowledgeBase PO → 实体。
//
// KB 是只读的，所以只有这一个方向——没有 ToKnowledgeBasePO。
// 真要写 KB 时再加，那时也该顺手想清楚「谁能改 KB」。
func ToKnowledgeBase(row *po.KnowledgeBase) *knowledgeentity.KnowledgeBase {
	return &knowledgeentity.KnowledgeBase{
		ID:            row.Id,
		No:            row.No,
		Account:       row.Account,
		Name:          row.Name,
		Description:   row.Description,
		KnowledgeType: row.KnowledgeType,
		SearchMode:    row.SearchMode,
		BizTag:        row.BizTag,
		SplitOptions:  row.SplitOptions,
		DocCount:      row.DocCount,
		ChunkCount:    row.ChunkCount,
		IsSystem:      row.IsSystem == 1,
		CreateTs:      row.CreateTs,
		UpdateTs:      row.UpdateTs,
		DeleteTs:      row.DeleteTs,
	}
}

func ToKnowledgeBases(rows []po.KnowledgeBase) knowledgeentity.KnowledgeBases {
	out := make(knowledgeentity.KnowledgeBases, 0, len(rows))
	for i := range rows {
		out = append(out, ToKnowledgeBase(&rows[i]))
	}
	return out
}

func ToKnowledgeDoc(row *po.KnowledgeDoc) *knowledgeentity.KnowledgeDoc {
	return &knowledgeentity.KnowledgeDoc{
		ID:          row.Id,
		KBID:        row.KbId,
		Account:     row.Account,
		Name:        row.Name,
		ContentHash: row.ContentHash,
		ChunkCount:  row.ChunkCount,
		CreateTs:    row.CreateTs,
		UpdateTs:    row.UpdateTs,
		DeleteTs:    row.DeleteTs,
	}
}

func ToKnowledgeDocs(rows []po.KnowledgeDoc) knowledgeentity.KnowledgeDocs {
	out := make(knowledgeentity.KnowledgeDocs, 0, len(rows))
	for i := range rows {
		out = append(out, ToKnowledgeDoc(&rows[i]))
	}
	return out
}

func ToKnowledgeDocPO(doc *knowledgeentity.KnowledgeDoc) *po.KnowledgeDoc {
	return &po.KnowledgeDoc{
		Id:          doc.ID,
		KbId:        doc.KBID,
		Account:     doc.Account,
		Name:        doc.Name,
		ContentHash: doc.ContentHash,
		ChunkCount:  doc.ChunkCount,
		CreateTs:    doc.CreateTs,
		UpdateTs:    doc.UpdateTs,
		DeleteTs:    doc.DeleteTs,
	}
}
```

### A4.6 `infrastructure/persistence/knowledge/vector_store_es.go`

```go
// 本文件是 IVectorStore 的 Elasticsearch 实现。
//
// 对应设计文档 §7.2（两路检索）、§9（单索引多租户）。
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/infrastructure/config"
	elasticsearch "github.com/elastic/go-elasticsearch/v9"
)

// ESVectorStore IVectorStore 的 Elasticsearch 实现。
type ESVectorStore struct {
	client *elasticsearch.Client
	index  string
	spec   IndexSpec
}

func NewESVectorStore(
	client *elasticsearch.Client, conf *config.Config,
) knowledgerepo.IVectorStore {
	spec := IndexSpec{
		Dim:             conf.Elasticsearch.Dim,
		Analyzer:        conf.Elasticsearch.Analyzer,
		SearchAnalyzer:  conf.Elasticsearch.SearchAnalyzer,
		Shards:          conf.Elasticsearch.Shards,
		Replicas:        conf.Elasticsearch.Replicas,
		RefreshInterval: conf.Elasticsearch.RefreshInterval,
	}

	index := conf.Elasticsearch.Index
	if index == "" {
		index = IndexName
	}

	return &ESVectorStore{client: client, index: index, spec: spec}
}

// EnsureIndex 幂等地确保索引存在。
//
// **只在启动期调用一次**（§9.4，A4.16 的 fx OnStart），写入路径上不碰它。
// 理由见 §9.4：写入路径建索引意味着「没建过索引的集群」和「写过数据的集群」
// 走的是两条不同的代码路径，而后者才是常态——那条路径反而从没被验证过。
//
// 已存在就返回，**不校验 mapping 是否与配置一致**：dense_vector 的 dims
// 建好之后改不了，真出现不一致只能新建索引重导（§9.4、§11 场景 9）。
// 自动「修正」在这里反而危险——它多半会把索引删了重建，等于全量丢数据。
func (s *ESVectorStore) EnsureIndex(ctx context.Context) error {
	res, err := s.client.Indices.Exists(
		[]string{s.index},
		s.client.Indices.Exists.WithContext(ctx),
	)
	if err != nil {
		return apperrors.ErrIndexInitFailed.Wrap(err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case 200:
		return nil
	case 404:
		// 继续往下建
	default:
		return apperrors.NewSysError(apperrors.CodeIndexInitFail, fmt.Sprintf(
			"检查索引失败，ES 返回 %d: %s", res.StatusCode, readBody(res.Body)))
	}

	body, err := BuildCreateIndexBody(s.spec)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return apperrors.ErrIndexInitFailed.Wrap(err)
	}

	create, err := s.client.Indices.Create(
		s.index,
		s.client.Indices.Create.WithBody(bytes.NewReader(payload)),
		s.client.Indices.Create.WithContext(ctx),
	)
	if err != nil {
		return apperrors.ErrIndexInitFailed.Wrap(err)
	}
	defer create.Body.Close()

	if create.IsError() {
		return apperrors.NewSysError(apperrors.CodeIndexInitFail,
			fmt.Sprintf("创建索引失败: %s", readBody(create.Body)))
	}
	return nil
}

// Save 批量写入切片。_id 用 chunk_id（§5.3）。
//
// 纯粹的 upsert：_id 相同就覆盖，不同就新增。**不做任何删除**——
// 清理旧切片是 DeleteExcept 的事（§5.2 ②），两者分开是为了让
// 「新增/覆盖」和「删除」在代码里各自可读、各自可失败。
//
// 覆盖语义之所以成立，靠的是 chunk_id 由内容派生：内容没变 → _id 没变 →
// 覆盖同一篇；内容变了 → _id 变了 → 是新增，旧的那份留给 DeleteExcept 清。
//
// 不用 refresh=true：索引刷新是 30s 级别的（§9），把刷新挂到每次导入上
// 会让写入放大成几十倍。代价是刚导入的文档最多 30s 后才能被搜到。
func (s *ESVectorStore) Save(ctx context.Context, docs []knowledgerepo.VectorDoc) error {
	if len(docs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, d := range docs {
		if err := writeJSONLine(&buf, map[string]any{
			"index": map[string]any{"_index": s.index, "_id": d.ChunkID},
		}); err != nil {
			return err
		}
		if err := writeJSONLine(&buf, vectorDocBody(d)); err != nil {
			return err
		}
	}

	res, err := s.client.Bulk(&buf, s.client.Bulk.WithContext(ctx))
	if err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("bulk 请求失败: %s", readBody(res.Body)))
	}

	// bulk 的坑：单条失败时 HTTP 状态码仍然是 200，错误藏在 items 里。
	// 不看 errors 字段的话，导入「成功」了但一半切片根本没进去
	var parsed struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int `json:"status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	if !parsed.Errors {
		return nil
	}

	failed := 0
	for _, item := range parsed.Items {
		for _, r := range item {
			if r.Status >= 300 {
				failed++
			}
		}
	}
	return apperrors.NewSysError(apperrors.CodeVectorStoreFail, fmt.Sprintf(
		"bulk 部分失败: %d/%d 条", failed, len(docs)))
}

// Refresh 强制刷新索引，让刚写入的切片立刻进入可检索视图。
//
// 它存在是因为 ES 的可见性有两档延迟，而这两档都会咬到我们（§5.2 ②）：
//
//   - **新写入的**要等下一次刷新才可见（默认 30s，§4.2）；
//   - **delete_by_query 也只看得到已刷新的段**——所以「上一批还没刷出来的旧切片」
//     对删除动作是**不存在**的，删了等于没删。
//
// 调用方在两种情况下需要它：重导入（要让 ② 看得见上一批旧切片）、
// 以及调用方要求「导入即可搜」（§4.2）。
//
// ⚠️ 粒度是**整个索引**：ES 没有「按文档刷新」这种操作。单索引多租户（D2）下
// 会顺带刷到别的租户的段，所以只在上面两种情况下调，别放进常规写入路径。
func (s *ESVectorStore) Refresh(ctx context.Context) error {
	res, err := s.client.Indices.Refresh(
		s.client.Indices.Refresh.WithContext(ctx),
		s.client.Indices.Refresh.WithIndex(s.index),
	)
	if err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("刷新索引失败: %s", readBody(res.Body)))
	}
	return nil
}

// DeleteByQuery 按条件删除。
func (s *ESVectorStore) DeleteByQuery(
	ctx context.Context, f knowledgerepo.VectorFilter,
) (int64, error) {
	query, ok := buildFilter(f)
	if !ok {
		// 三个条件全空 = 删全库。这几乎一定是调用方漏传了参数，
		// 拒绝掉。宁可报错，也不能把一个索引清空
		return 0, apperrors.NewParamError("删除条件不能全为空")
	}

	body, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}

	res, err := s.client.DeleteByQuery(
		[]string{s.index},
		bytes.NewReader(body),
		s.client.DeleteByQuery.WithContext(ctx),
		// 删到一半撞上版本冲突时继续，别把整批回滚
		s.client.DeleteByQuery.WithConflicts("proceed"),
	)
	if err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return 0, apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("delete_by_query 失败: %s", readBody(res.Body)))
	}

	// 同样注意：delete_by_query 可能返回 200 但 failures 非空
	var parsed struct {
		Deleted  int64 `json:"deleted"`
		Failures []struct {
			Reason struct {
				Reason string `json:"reason"`
			} `json:"reason"`
		} `json:"failures"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	if len(parsed.Failures) > 0 {
		return parsed.Deleted, apperrors.NewSysError(apperrors.CodeVectorStoreFail, fmt.Sprintf(
			"delete_by_query 有 %d 条失败，首条: %s",
			len(parsed.Failures), parsed.Failures[0].Reason.Reason))
	}
	return parsed.Deleted, nil
}

// DeleteExcept 删除过滤范围内、_id 不在 keepIDs 里的切片（§5.2 第 ② 步）。
//
// 这是「先写后删」里的那个"删"。重导入时同一篇文档的切片 _id 会因为内容变化
// 而变（chunk_id = sha256(doc_id, order, content)），所以旧切片不会被新写入
// 覆盖，必须显式清理——删的正是「旧集合减去新集合」这个差集。
//
// 为什么按 _id 差集删、而不是 DeleteByQuery(doc_id) 一把删光：
//   - 按 doc_id 删光会把**内容没变的那批切片**也删掉。它们本来 _id 相同、
//     刚刚被 Save 原样覆盖过一遍，删掉再写一次纯属写入放大；
//   - 更糟的是顺序：Save 在前、删在后，中间这一小段时间内容没变的切片
//     是"写完又被删"——如果删除请求刚好在 Save 之后、下一次导入之前完成，
//     这篇文档就凭空少了几个切片。
//
// 与 DeleteByQuery 分开是有意的：这两个的杀伤面差一个数量级。
// DeleteByQuery 是「删掉命中的全部」，DeleteExcept 是「只删差集」。
// 靠一个空 keepIDs 参数区分（传空 = 删全部），早晚有人传空把整篇文档清空。
//
// 单独开一个方法还有个好处：doc_id 的条件写在这里，调用方没有机会忘。
//
// **它自己带 refresh=true，不依赖调用方**。ES 的删除和写入一样要等刷新才对
// 检索可见，而调用方那次刷新发生在删除**之前**（那次是为了让本方法看得见
// 上一批旧切片），管不到「让删除结果被看见」。少了这一次刷新，重导入后
// 立刻检索会同时返回新旧两份，直到 30s 周期刷新到来为止。
//
// 剩下的窗口（§5.2、§11）：Save 之后、本方法跑完之前，新旧切片共存。
// 这个窗口现在是「一次请求内的几十毫秒」量级——本方法返回时删除已经可见，
// 所以调用方拿到响应之后不会再看到旧内容。真正的并发同名导入导致的
// 互相删除仍在（§5.5），本期接受——写侧一致性（代次切换）是明确推迟的增强项（§13 待定 9）。
func (s *ESVectorStore) DeleteExcept(
	ctx context.Context, f knowledgerepo.VectorFilter, keepIDs []string,
) (int64, error) {
	if f.DocID == 0 {
		// 没有 doc_id 就没有「这篇文档的旧切片」可言。
		// 放行等于按 kb_id 删掉一整个库的切片，必须拦下
		return 0, apperrors.NewParamError("DeleteExcept 必须指定 doc_id")
	}

	base, ok := buildFilter(f)
	if !ok {
		return 0, apperrors.NewParamError("删除条件不能全为空")
	}

	// 在已有条件上再 AND 一个 must_not(_id in keepIDs)。
	// 一定要包进 bool.must：buildFilter 返回的本身就是一个 bool 查询，
	// 直接把 must_not 和它并列会变成「或」的关系——那会把刚写的新切片也删掉。
	inner := base["bool"].(map[string]any)
	must := inner["must"].([]any)
	boolQuery := map[string]any{"must": must}

	// keepIDs 为空时不加 must_not —— terms 查询传空数组会命中 0 条文档，
	// 那样 DeleteExcept 就成了 no-op，旧切片永远清不掉。
	// 空集合的正确语义是"没有要留的"，即删掉全部命中，正好是不加 must_not。
	if len(keepIDs) > 0 {
		boolQuery["must_not"] = []any{
			map[string]any{"terms": map[string]any{"_id": keepIDs}},
		}
	}

	body, err := json.Marshal(map[string]any{
		"query": map[string]any{"bool": boolQuery},
	})
	if err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}

	res, err := s.client.DeleteByQuery(
		[]string{s.index},
		bytes.NewReader(body),
		s.client.DeleteByQuery.WithContext(ctx),
		// 删到一半撞上版本冲突时继续，别把整批回滚
		s.client.DeleteByQuery.WithConflicts("proceed"),
		// refresh 必须开（§5.2 ②）：删除和写入一样，**要等下一次刷新才对检索可见**。
		// 而调用方的刷新在删除之前——那是为了「让 delete_by_query 看得见上一批旧切片」，
		// 帮不到这里的「让删除结果被检索看见」。少了这一次刷新，
		// 重导入之后立刻检索会同时返回新旧两份内容，直到索引的 30s 周期刷新到来，
		// §12.1 那条「重导入后旧切片搜不到」就会时灵时不灵，且窗口正好落在
		// 「刚导入完就去搜」这个最自然的用法上。
		//
		// 语义上也不该省：调用方请求的是「写完即可检索」，而「写完」包含清掉旧的。
		s.client.DeleteByQuery.WithRefresh(true),
	)
	if err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return 0, apperrors.NewSysError(apperrors.CodeVectorStoreFail,
			fmt.Sprintf("delete_except 失败: %s", readBody(res.Body)))
	}

	// 同 DeleteByQuery：200 也可能带 failures
	var parsed struct {
		Deleted  int64 `json:"deleted"`
		Failures []struct {
			Reason struct {
				Reason string `json:"reason"`
			} `json:"reason"`
		} `json:"failures"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return 0, apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	if len(parsed.Failures) > 0 {
		return parsed.Deleted, apperrors.NewSysError(apperrors.CodeVectorStoreFail, fmt.Sprintf(
			"delete_except 有 %d 条失败，首条: %s",
			len(parsed.Failures), parsed.Failures[0].Reason.Reason))
	}
	return parsed.Deleted, nil
}

// Search 发 BM25 + kNN 两路，原样返回、不融合。
func (s *ESVectorStore) Search(
	ctx context.Context, req knowledgerepo.VectorSearchReq,
) (knowledgerepo.SearchResult, error) {
	var out knowledgerepo.SearchResult

	type route struct {
		name string
		body map[string]any
		dst  *[]knowledgerepo.VectorHit
	}

	routes := make([]route, 0, 2)
	if req.DenseWeight <= 0.99 {
		routes = append(routes, route{"bm25", s.bm25Body(req), &out.BM25})
	}
	if req.DenseWeight >= 0.01 && len(req.QueryVec) > 0 {
		routes = append(routes, route{"knn", s.knnBody(req), &out.KNN})
	}

	// 两路并行发：ES 扛得住这个并发，串行只会让 P99 白白翻倍
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, rt := range routes {
		wg.Add(1)
		go func(rt route) {
			defer wg.Done()

			hits, err := s.runSearch(ctx, rt.body)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", rt.name, err))
				return
			}
			*rt.dst = hits
		}(rt)
	}
	wg.Wait()

	if len(errs) > 0 {
		return out, errors.Join(errs...)
	}
	return out, nil
}

func (s *ESVectorStore) runSearch(
	ctx context.Context, body map[string]any,
) ([]knowledgerepo.VectorHit, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, apperrors.ErrSearchFailed.Wrap(err)
	}

	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(s.index),
		s.client.Search.WithBody(bytes.NewReader(payload)),
	)
	if err != nil {
		return nil, apperrors.ErrSearchFailed.Wrap(err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, apperrors.NewSysError(apperrors.CodeSearchFail,
			fmt.Sprintf("检索失败，ES 返回 %d: %s", res.StatusCode, readBody(res.Body)))
	}

	var parsed struct {
		Hits struct {
			Hits []struct {
				Score  float64         `json:"_score"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, apperrors.ErrSearchFailed.Wrap(err)
	}

	out := make([]knowledgerepo.VectorHit, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		var src struct {
			ChunkID     string `json:"chunk_id"`
			DocID       uint64 `json:"doc_id"`
			KBID        uint64 `json:"kb_id"`
			Order       int    `json:"order"`
			Title       string `json:"title"`
			Content     string `json:"content"`
			HeadingPath string `json:"heading_path"`
		}
		if err := json.Unmarshal(h.Source, &src); err != nil {
			// 单条解析失败不该让整次检索挂掉
			continue
		}
		out = append(out, knowledgerepo.VectorHit{
			ChunkID:     src.ChunkID,
			DocID:       src.DocID,
			KBID:        src.KBID,
			Order:       src.Order,
			Title:       src.Title,
			Content:     src.Content,
			HeadingPath: src.HeadingPath,
			Score:       h.Score,
		})
	}
	return out, nil
}

// bm25Body 关键词路。
func (s *ESVectorStore) bm25Body(req knowledgerepo.VectorSearchReq) map[string]any {
	// boost 的取值：标题命中比正文命中强得多——一份文档里正文到处都是
	// 常见词，标题才真正说明这篇在讲什么
	fields := []string{FieldTitle + "^3", FieldHeadingPath + "^2", FieldContent}
	if req.TitleOnly {
		// search_mode = title：不查正文
		fields = []string{FieldTitle + "^3", FieldHeadingPath + "^2"}
	}

	should := make([]any, 0, len(fields))
	for _, f := range fields {
		should = append(should, map[string]any{
			"match": map[string]any{f: map[string]any{"query": req.Query}},
		})
	}

	return map[string]any{
		"size": req.BM25Top,
		"query": map[string]any{
			"bool": map[string]any{
				// filter 里的条件不参与打分，只做范围限定。
				// 租户隔离必须走这里——放进 must 的话，
				// 一个 account 的匹配会把 BM25 分数整体抬高
				"filter":               s.filters(req),
				"should":               should,
				"minimum_should_match": 1,
			},
		},
		"_source": sourceFields(),
	}
}

// knnBody 向量路。
func (s *ESVectorStore) knnBody(req knowledgerepo.VectorSearchReq) map[string]any {
	field := FieldContentVec
	if req.TitleOnly {
		field = FieldTitleVec
	}

	k := req.KNNTops
	numCandidates := req.NumCandidates
	if numCandidates < k {
		// num_candidates 必须 >= k，否则 ES 直接 400
		numCandidates = k
	}

	return map[string]any{
		"size": k,
		"knn": map[string]any{
			"field":          field,
			"query_vector":   req.QueryVec,
			"k":              k,
			"num_candidates": numCandidates,
			"filter":         s.filters(req),
		},
		"_source": sourceFields(),
	}
}

// filters 所有「只做范围限定、不参与打分」的条件。
//
// account 必须在最前，且必须用 term：它是单索引下唯一的租户隔离手段（D2）。
// 写成 match 会按分词匹配，A 租户能查到 B 租户的数据——那是越权，不是召回问题。
func (s *ESVectorStore) filters(req knowledgerepo.VectorSearchReq) []any {
	out := []any{
		map[string]any{"term": map[string]any{FieldAccount: req.Account}},
	}
	if len(req.KBIDs) > 0 {
		out = append(out, map[string]any{"terms": map[string]any{FieldKbId: req.KBIDs}})
	}
	if len(req.BizTags) > 0 {
		out = append(out, map[string]any{"terms": map[string]any{FieldBizTag: req.BizTags}})
	}
	return out
}

// buildFilter 把 VectorFilter 翻成 ES 查询。返回 false 表示条件全空。
func buildFilter(f knowledgerepo.VectorFilter) (map[string]any, bool) {
	must := make([]any, 0, 3)

	if f.Account != "" {
		must = append(must, map[string]any{"term": map[string]any{FieldAccount: f.Account}})
	}
	if f.KBID != 0 {
		must = append(must, map[string]any{"term": map[string]any{FieldKbId: f.KBID}})
	}
	if f.DocID != 0 {
		must = append(must, map[string]any{"term": map[string]any{FieldDocId: f.DocID}})
	}

	if len(must) == 0 {
		return nil, false
	}
	return map[string]any{"bool": map[string]any{"must": must}}, true
}

func sourceFields() []string {
	return []string{
		FieldChunkId, FieldDocId, FieldKbId, FieldOrder,
		FieldTitle, FieldContent, FieldHeadingPath,
	}
}

func vectorDocBody(d knowledgerepo.VectorDoc) map[string]any {
	return map[string]any{
		FieldAccount:     d.Account,
		FieldKbId:        d.KBID,
		FieldDocId:       d.DocID,
		FieldChunkId:     d.ChunkID,
		FieldOrder:       d.Order,
		FieldBizTag:      d.BizTag,
		FieldTitle:       d.Title,
		FieldContent:     d.Content,
		FieldHeadingPath: d.HeadingPath,
		FieldTitleVec:    d.TitleVec,
		FieldContentVec:  d.ContentVec,
		FieldCreateTs:    d.CreateTs,
	}
}

func writeJSONLine(buf *bytes.Buffer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return apperrors.ErrVectorStoreFailed.Wrap(err)
	}
	buf.Write(b)
	buf.WriteByte('\n')
	return nil
}

// readBody 读掉响应体并截断。
// ES 的错误体可以很长，原样塞进错误信息会把日志冲爆。
func readBody(r io.Reader) string {
	const maxLen = 512

	b, _ := io.ReadAll(io.LimitReader(r, maxLen*4))
	// 按 rune 截断：按字节截会把一个中文切成两半，日志里就是乱码
	runes := []rune(strings.TrimSpace(string(b)))
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen]) + "...(截断)"
}
```

### A4.7 `infrastructure/persistence/knowledge/index_template.go`（微调）

文件内容基本不变（A0.2 已说明它挪到 `persistence/knowledge`）。
**本次没有 `ver` 字段，mapping 一个属性都不加**——一致性增强本期不做（§2 D4、§13 待定 9）。
本段只做三处改动，都是注释与测试，**没有功能改动**：

**① 订正一处过期引用**：`DefaultIndexSpec` 的注释里写着「设计文档 §14 的待定项」，
现在是 §13；`Shards=3` 那句里的「设计文档 §9.4」保持不动。

**② 补测试**：`index_template_test.go` 的
`TestBuildCreateIndexBody_AllDesignFieldsPresent` 字段清单漏了 `FieldBizTag`——
字段漏建不会报错，只是查询时的 `terms(biz_tag)` 永远匹配不到，
静默退化成「biz_tag 过滤形同虚设」。这正是那个测试存在的意义。
（顺带把该测试顶部的注释 `// §4.3 定下的字段` 改成 `§4.2`——文档里从来没有 §4.3。）

`TestMapping_IsolationFieldsAreKeyword` 不用动：它已经把 `FieldBizTag` 列在里面了，
所以 biz_tag 的 keyword 类型是有保障的，**缺的只是"这个字段存不存在"**。

```go
	for _, f := range []string{
		FieldAccount, FieldKbId, FieldDocId, FieldChunkId, FieldOrder,
		FieldBizTag,
		FieldTitle, FieldContent, FieldHeadingPath,
		FieldTitleVec, FieldContentVec, FieldCreateTs,
	} {
```

补完之后，磁盘上现有的 `TestBuildCreateIndexBody_Defaults` /
`TestMapping_IsolationFieldsAreKeyword` 都不用动，它们已经覆盖了
`biz_tag` 的 keyword 类型与两个向量字段的维度。

**③ `DefaultIndexSpec` 的注释调整**：

```go
// DefaultIndexSpec 返回本地开发/起步阶段的默认规格。
//
// ⚠️ 运行期**不使用**这个函数：ESVectorStore 的 spec 是从 config.json 的
// elasticsearch 块构造的（A4.6）。留在这里是给单测和「手工建索引」用的——
// 配置漏填时，TestBuildCreateIndexBody_Defaults 会先红，而不是等
// 部署到环境上才发现维度不对。
func DefaultIndexSpec() IndexSpec { … }
```

### A4.8 `infrastructure/persistence/register.go`（改写）

```go
package persistence

import (
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	knowledgepersistence "github.com/PycMono/FastRAG/infrastructure/persistence/knowledge"
	"go.uber.org/fx"
)

// Register 注册所有持久化实现。
//
// 三个仓储都直接返回接口类型，让 fx 按接口装配——
// 应用层拿到的永远是 IVectorStore / IKnowledgeDocRepo，不是具体实现。
//
// 注意 NewKnowledgeDocRepo 有两个参数：sqlsdk.Provider 和 transaction.Manager。
// 后者是 Save 用的（A4.4）——「锁行读旧值、upsert」两步必须在一个事务里，
// 否则两个并发导入会各自读到一个已经被对方改过的 chunk_count，
// 拿去算 KB 计数差值时就会算错。
// 这两个接口由 A4.16 的 TransProvider 一份实例同时 provide，fx 自动装配。
var Register = fx.Options(
	fx.Provide(knowledgepersistence.NewKnowledgeBaseRepo),
	fx.Provide(knowledgepersistence.NewKnowledgeDocRepo),
	fx.Provide(knowledgepersistence.NewESVectorStore),
)

// 编译期断言：实现必须满足端口。
// 放在装配处而不是实现文件里，是为了让「谁该满足哪个接口」一眼可见。
var (
	_ knowledgerepo.IKnowledgeBaseRepo = (*knowledgepersistence.KnowledgeBaseRepo)(nil)
	_ knowledgerepo.IKnowledgeDocRepo  = (*knowledgepersistence.KnowledgeDocRepo)(nil)
	_ knowledgerepo.IVectorStore       = (*knowledgepersistence.ESVectorStore)(nil)
)
```

### A4.9 ~~`domain/interfaces/limiter.go`~~ / A4.10 ~~`infrastructure/driver/redis/limiter.go`~~（**删除，本期不做**）

原设计在这里放了一对限流端口与 Redis 滑窗实现——分 `interactive` / `batch` 两档配额，
让批量导入给实时检索让路。

**本期不做**（用户决定：先是个 demo，把核心功能做扎实再谈优化）。已删除的文件：

| 原编号 | 文件 | 处置 |
|---|---|---|
| A4.9 | `domain/interfaces/limiter.go` | 删除 |
| A4.10 | `infrastructure/driver/redis/limiter.go` | 删除 |
| A4.12 | `infrastructure/serviceimpl/embedding_limited.go` | 删除 |

连带删掉的还有：`config.Config` 的 `limits` 块、`serviceimpl.Register` 里的
`fx.Decorate` 包装、以及 §10.3 的整节限流设计。

**去掉之后剩下什么保护**：批量导入的工作池是**固定 4 并发**（§5.5），
这就是全部的背压。也就是说，一次 1000 篇的批量导入会以 4 路并发一直打到
embedding 服务，没有任何速率上限。只要 embedding 那边不被拖垮，这就是够的
（demo 场景：单机、语料有限、导入是人工触发）。

**什么时候必须把它加回来**：出现下面任一条——

1. 导入从「人工触发」变成「别人调你的接口」——那就等于把一个放大器开放给外部；
2. embedding 服务是**共享**的（别的服务也在用），你的批量导入会挤到别人；
3. 检索的 P99 因为导入而抖动（§12.1 的延迟基线失守）。

到那时端口和实现都在本节的 git 历史里，直接恢复即可——`IRateLimiter` 的形状
（`Wait(ctx, class) error`）是照着「调用点只多一行」设计的，加回来不用动业务代码。

### A4.10 `infrastructure/serviceimpl/embedding_openai.go`

```go
// 本文件用 OpenAI 兼容协议对接向量化服务。
//
// 兼容面很宽：OpenAI / 火山引擎 / 通义 / vLLM / Ollama / 自建 model_proxy
// 都是 POST {base_url}/embeddings，请求体 {model, input}。
// 协议这么简单，引第三方 SDK 反而是给自己加一层要跟着升级的依赖。
package serviceimpl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// maxEmbedResponseBytes 响应体读取上限。
// 一批 32 条 × 1024 维的 float32 JSON 大约 1MB，64MB 已经非常宽裕，
// 但没有上限地 ReadAll 迟早会被一个畸形响应打爆内存。
const maxEmbedResponseBytes = 64 << 20

// OpenAIEmbedding OpenAI 兼容协议的向量化实现。
type OpenAIEmbedding struct {
	client      *http.Client
	baseURL     string
	apiKey      string
	model       string
	dim         int
	batchSize   int
	queryPrefix string
}

func NewOpenAIEmbedding(conf *config.Config) interfaces.IEmbeddingService {
	c := conf.Embedding

	timeout := time.Duration(c.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	batch := c.BatchSize
	if batch <= 0 {
		batch = 32
	}

	return &OpenAIEmbedding{
		client:      &http.Client{Timeout: timeout},
		baseURL:     strings.TrimRight(c.BaseURL, "/"),
		apiKey:      c.APIKey,
		model:       c.Model,
		dim:         c.Dim,
		batchSize:   batch,
		queryPrefix: c.QueryPrefix,
	}
}

func (e *OpenAIEmbedding) Dim() int      { return e.dim }
func (e *OpenAIEmbedding) Model() string { return e.model }

// EmbedDocs 批量编码文档，自动按 batch_size 分片。
//
// 返回顺序与入参严格一一对应：**按响应里的 index 回填，不信返回数组的顺序**。
// 顺序错位是最隐蔽的一类 bug——向量都在、条数也对，只是挂到了错误的切片上，
// 检索结果全错但看起来一切正常。
func (e *OpenAIEmbedding) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += e.batchSize {
		end := start + e.batchSize
		if end > len(texts) {
			end = len(texts)
		}

		vecs, err := e.embed(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// EmbedQuery 编码查询串，带非对称前缀。
//
// 必须与 EmbedDocs 分开：非对称模型（E5、bge-large-zh）检索时要求给 query
// 加指令前缀（如 "query: "），doc 侧不加。混用会显著掉分，
// 而且不会报错——只是效果变差，很难归因。
//
// 本期配的 bge-m3 是对称模型，queryPrefix 为空串，这里等于直接透传。
func (e *OpenAIEmbedding) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.embed(ctx, []string{e.queryPrefix + text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail,
			fmt.Sprintf("embedding 返回 %d 条向量，期望 1 条", len(vecs)))
	}
	return vecs[0], nil
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *OpenAIEmbedding) embed(ctx context.Context, texts []string) ([][]float32, error) {
	payload, err := json.Marshal(map[string]any{"model": e.model, "input": texts})
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedResponseBytes))
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	var parsed embedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
			"embedding 响应不是合法 JSON，HTTP %d: %s", resp.StatusCode, truncate(body, 512)))
	}
	if parsed.Error != nil {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail,
			"embedding 服务返回错误: "+parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
			"embedding 服务 HTTP %d: %s", resp.StatusCode, truncate(body, 512)))
	}

	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			continue
		}
		out[d.Index] = d.Embedding
	}

	for i, v := range out {
		if len(v) == 0 {
			return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail,
				fmt.Sprintf("embedding 第 %d 条缺失", i))
		}
		// 维度对不上必须在这里就拦下：等写进 ES 才发现，
		// 报的会是 dense_vector 的 mapping 错误，离真正的原因很远
		if e.dim > 0 && len(v) != e.dim {
			return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
				"embedding 维度为 %d，配置里写的是 %d；两者必须一致", len(v), e.dim))
		}
	}
	return out, nil
}

func truncate(body []byte, n int) string {
	runes := []rune(strings.TrimSpace(string(body)))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "...(截断)"
}
```

### A4.11 `infrastructure/serviceimpl/rerank_stub.go`

```go
package serviceimpl

import (
	"context"

	"github.com/PycMono/FastRAG/domain/interfaces"
)

// RerankStub 重排占位实现：原样返回，不改顺序。
//
// 本期不做重排（P2），但接口先留好：接上真正的 rerank 服务时，
// 只需要换掉 serviceimpl.Register 里的这一行，应用层一行都不用动。
type RerankStub struct{}

func NewRerankStub() interfaces.IRerankService { return &RerankStub{} }

func (RerankStub) Rerank(
	_ context.Context, _ string, cands []interfaces.RerankCandidate, _ int,
) ([]int, error) {
	out := make([]int, len(cands))
	for i := range out {
		out[i] = i
	}
	return out, nil
}
```

### A4.12 `infrastructure/serviceimpl/register.go`（改写）

```go
package serviceimpl

import (
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"go.uber.org/fx"
)

// Register 注册 serviceimpl 层组件。
//
// 没有限流装饰器。本期不做限流（A4.9），embedding 直接暴露给调用方，
// 唯一的背压是批量导入那个固定 4 并发的工作池（§5.5）。
var Register = fx.Options(
	fx.Provide(func(conf *config.Config) (repository.IIDService, error) {
		svc, err := NewIDService(int64(conf.SnowflakeNodeID))
		if err != nil {
			return nil, err
		}
		return svc, nil
	}),

	fx.Provide(NewOpenAIEmbedding),
	fx.Provide(NewRerankStub),
)

// 编译期断言：实现必须满足端口。
var (
	_ interfaces.IEmbeddingService = (*OpenAIEmbedding)(nil)
	_ interfaces.IRerankService    = (*RerankStub)(nil)
)
```

> 编译期断言从 A4.8（持久化层）搬过来一份，理由一样：
> `NewOpenAIEmbedding` 返回的是接口类型，装配期类型对不上只会在运行时炸，
> 断言让它在 `go build` 就红。

### A4.13 `infrastructure/controller/http/doc/controller.go`

```go
package doc

import (
	"fmt"

	"github.com/PycMono/FastRAG/application/service/ingest"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/gin-gonic/gin"
)

// maxBatchDocs 单次批量导入的文档数上限。
const maxBatchDocs = 100

// Controller 文档导入 HTTP 控制器。
//
// 注意这里**没有任何鉴权中间件**：account 就是请求体里的普通字段（§6）。
// 部署时 FastRAG 只能在可信内网暴露，调用方自己负责对最终用户鉴权。
type Controller struct {
	service *ingest.Service
}

func NewController(service *ingest.Service) *Controller {
	return &Controller{service: service}
}

// Ingest POST /api/v1/docs
func (ctl *Controller) Ingest(c *gin.Context) {
	var param dto.DocIngestDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.Ingest(c.Request.Context(), &param)
	gingext.Send(c, result, err)
}

// BatchIngest POST /api/v1/docs/batch
//
// 请求体是一个数组。**恒返回 200 + 明细**，单条失败不改 HTTP 状态码——
// 否则调用方没法区分「全挂了」和「挂了 3 条」，只能整批重试，
// 而整批重试会把已经成功的那 97 条再灌一遍。
func (ctl *Controller) BatchIngest(c *gin.Context) {
	var params []*dto.DocIngestDTO
	if err := c.ShouldBindJSON(&params); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}
	if len(params) == 0 {
		gingext.Send(c, nil, apperrors.NewParamError("批量导入列表为空"))
		return
	}
	if len(params) > maxBatchDocs {
		gingext.Send(c, nil, apperrors.NewParamError(fmt.Sprintf(
			"单次最多 %d 篇，收到 %d 篇；请上游分批调用", maxBatchDocs, len(params))))
		return
	}

	gingext.Send(c, ctl.service.BatchIngest(c.Request.Context(), params), nil)
}

// Delete POST /api/v1/docs/delete
//
// 用 POST 而不是 DELETE：DELETE 带 body 是合法的，但一些网关与
// 客户端库会把它丢掉，最后收到一个「参数为空」的 400，很难排查。
func (ctl *Controller) Delete(c *gin.Context) {
	var param dto.DocDeleteDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.DeleteDoc(c.Request.Context(), &param)
	gingext.Send(c, result, err)
}
```

### A4.14 `infrastructure/controller/http/search/controller.go`

```go
package search

import (
	"fmt"

	appsvc "github.com/PycMono/FastRAG/application/service/search"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/gin-gonic/gin"
)

// Controller 检索 HTTP 控制器。
type Controller struct {
	service *appsvc.Service
}

func NewController(service *appsvc.Service) *Controller {
	return &Controller{service: service}
}

// Search POST /api/v1/search
func (ctl *Controller) Search(c *gin.Context) {
	var param dto.SearchDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.Search(c.Request.Context(), &param)
	gingext.Send(c, result, err)
}
```

### A4.15 `infrastructure/controller/http/register.go`（改写）

```go
package http

import (
	docctl "github.com/PycMono/FastRAG/infrastructure/controller/http/doc"
	"github.com/PycMono/FastRAG/infrastructure/controller/http/health"
	searchctl "github.com/PycMono/FastRAG/infrastructure/controller/http/search"
	webctl "github.com/PycMono/FastRAG/infrastructure/controller/http/web"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

const v1Prefix = "/api/v1"

// Register 注册所有 HTTP 控制器到 FX 容器
var Register = fx.Options(
	fx.Provide(health.NewController),
	fx.Provide(docctl.NewController),
	fx.Provide(searchctl.NewController),
	fx.Provide(webctl.NewController),
	fx.Invoke(RegisterRoutes),
)

// RouteDeps 路由注册所需依赖（FX 自动按类型注入）
type RouteDeps struct {
	fx.In
	Router    *gin.Engine
	HealthCtl *health.Controller
	DocCtl    *docctl.Controller
	SearchCtl *searchctl.Controller
	WebCtl    *webctl.Controller
}

// RegisterRoutes 统一入口。
//
// 路由刻意做得很少：只有「写文档」「删文档」「检索」三件事（§1.2）。
// 知识库的 CRUD、发布、版本全部不在本服务范围内。
//
// 探针路由（/health、/ready）注册在顶层、不带 /api 前缀——
// K8s 的 livenessProbe / readinessProbe 按固定路径打，前缀变了探针就瞎了。
//
// 演示页挂在根路径，同样是顶层：它是给人看的 HTML，不是 API，
// 塞进 /api/v1 只会让「哪些路径是接口」这件事变得含糊（A4.18）。
func RegisterRoutes(d RouteDeps) {
	registerHealthRoutes(d.Router, d.HealthCtl)
	d.Router.GET("/", d.WebCtl.Index)

	api := d.Router.Group(v1Prefix)
	{
		docs := api.Group("/docs")
		{
			docs.POST("", d.DocCtl.Ingest)
			docs.POST("/batch", d.DocCtl.BatchIngest)
			docs.POST("/delete", d.DocCtl.Delete)
		}

		api.POST("/search", d.SearchCtl.Search)
	}
}

func registerHealthRoutes(r *gin.Engine, ctl *health.Controller) {
	r.GET("/health", ctl.Health)
	r.GET("/ready", ctl.Ready)
}
```

> ⚠️ **这一节曾经漏掉 `registerHealthRoutes`。** 写实现时照着旧版抄，
> `/health` 和 `/ready` 就从路由表里消失了——而 `init_test.go` 里那张
> 「注册路由 = 6 条」的清单正好是照着同一份文档写的，两边一起错，谁也没发现。
> 教训不是「文档写错了」，而是**路由清单和注册代码必须有且只有一个来源**：
> 现在清单是测试里手写的，靠人肉同步，下次改动还会漂。

> **`/ready` 已经改成真检查 ES**（A4.2 早就点名要这么做）。原来它只回「进程活着」，
> ES 挂了照样报健康——而 `es.NewClient` 不发探活请求，ES 没起来服务照常启动，
> `/ready` 是唯一能提前发现这件事的地方。
> Redis 那条检查删掉了：限流本期不做（§10.3），Redis 已从 fx 图里摘掉（A0.2），
> 探一个不参与请求链路的依赖只会让人以为它在起作用。

### A4.16 `infrastructure/init.go`（改写）

```go
package infrastructure

import (
	"context"
	"fmt"

	"github.com/PycMono/FastRAG/application/service"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/domain/value_object"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/FastRAG/infrastructure/controller"
	"github.com/PycMono/FastRAG/infrastructure/driver/es"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/PycMono/FastRAG/infrastructure/driver/mysql"
	"github.com/PycMono/FastRAG/infrastructure/persistence"
	"github.com/PycMono/FastRAG/infrastructure/serviceimpl"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"
	logsdk "github.com/PycMono/go-logger-sdk"
	"go.uber.org/fx"
)

// Init 初始化 HTTP 服务所需的全部组件。
func Init(conf *config.Config) fx.Option {
	return fx.Options(
		core(conf),

		fx.Provide(gingext.NewEngine),
		fx.Provide(gingext.NewHTTPServer),
		controller.Register,

		// 必须显式启动监听。ginsdk 的 HTTPServer 只是个 http.Server 的壳，
		// 自己不会去 ListenAndServe——没有这个 Invoke，服务会打满一屏
		// [Fx] RUNNING 然后安静地不监听任何端口（见下面的 ⚠️）。
		fx.Invoke(startHTTPServer),
	)
}

// InitCLI 只装配命令行任务需要的部分，不启 HTTP Server。
//
// 单独开一个而不是复用 Init：Init 里 controller.Register 会注册路由
// 并把 HTTP 服务拉起来，批处理任务不需要、也不该占着端口。
func InitCLI(conf *config.Config) fx.Option {
	return core(conf)
}

// core 三种入口（HTTP / CLI / 测试）共用的装配。
func core(conf *config.Config) fx.Option {
	return fx.Options(
		fx.Supply(conf),

		fx.Provide(mysql.NewProvider),

		// TransProvider 同时满足两个接口，按接口分别暴露：
		// 仓储要 sqlsdk.Provider（UseDB 是事务感知的，事务里自动用同一个连接），
		// 应用层和 NewKnowledgeDocRepo 都要 transaction.Manager
		//（Save 的「锁行 + upsert」必须在一个事务里，见 A4.4）
		fx.Provide(func(p *sqlsdk.TransProvider) sqlsdk.Provider { return p }),
		fx.Provide(func(p *sqlsdk.TransProvider) transaction.Manager { return p }),

		fx.Provide(es.NewClient),

		// 配置 → 领域调参。
		// 在这里转一次手，application 层就不必 import infrastructure/config（§3）
		fx.Provide(func(c *config.Config) value_object.SearchTuning {
			return value_object.SearchTuning{
				BM25Top:       c.Search.BM25Top,
				KNNTops:       c.Search.KNNTops,
				NumCandidates: c.Search.NumCandidates,
				RankConstant:  c.Search.RankConstant,
				DenseWeight:   c.Search.DenseWeight,
			}
		}),

		persistence.Register,
		serviceimpl.Register,
		domain.Register,
		service.Register,

		fx.Invoke(checkTrustRequestAccount),
		fx.Invoke(ensureIndexOnStart),
	)
}

// checkTrustRequestAccount 是一道防误部署的闸门（§6.1）。
//
// 本服务不做鉴权——account 就是请求体里的一个普通字符串参数。
// 能构造请求体的人就能读写任意租户的数据。这个设计只有在「服务只跑在可信内网、
// 由调用方自己完成对最终用户的鉴权」这个前提下才成立。
//
// 问题在于，把服务直接摆到公网上是个太容易犯的错——改一行 nginx 配置就够了，
// 而代码本身不会报任何错。所以这里把前提变成一个必须显式承认的开关：
// 没配 trust_request_account = true 就拒绝启动。
//
// 用 fx.Invoke 而不是放在 main 里手写 if：这样 CLI 入口（InitCLI）也会过这道闸门，
// 三个入口一个都漏不掉。
func checkTrustRequestAccount(conf *config.Config) error {
	if !conf.Security.TrustRequestAccount {
		return apperrors.NewSysError(apperrors.CodeInternal,
			"拒绝启动：security.trust_request_account 未置为 true。\n"+
				"本服务不做鉴权，account 直接取自请求体，任何能构造请求的人都能读写任意租户。\n"+
				"确认它只对内网可信调用方开放（且调用方已自行完成用户鉴权）后，"+
				"在配置里显式写上 true。")
	}
	return nil
}

// ensureIndexOnStart 启动期把 ES 索引建好（§9.4）。
//
// 为什么放在启动期而不是写入路径：详见 §9.4 那张表。一句话版本——
// 写入路径建索引意味着「索引已存在」这个大前提下的代码从没在上线路径上跑过，
// 而恰恰是那条路径承载全部真实流量。
//
// 用 OnStart 而不是直接 Invoke：OnStart 是 fx 的生命周期回调，
// 失败会让 app.Run() 直接返回错误（进程起不来），正是我们要的——
// 索引建不出来的服务，起来也是个只会报错的空壳。
func ensureIndexOnStart(lc fx.Lifecycle, store knowledgerepo.IVectorStore) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := store.EnsureIndex(ctx); err != nil {
				return fmt.Errorf("启动期建索引失败: %w", err)
			}
			logsdk.Info(ctx, "ES 索引就绪")
			return nil
		},
	})
}
```

> **`EnsureIndex` 得进 `IVectorStore` 端口**（§10.1 里已经加了）。它不属于
> 「仓储该干的事」，但它是这个实现的启动期义务，而 fx 只认接口——
> 让装配层拿到具体类型 `*ESVectorStore` 也能做，代价是 `infrastructure` 包
> 要 import `persistence/knowledge` 的具体类型，多一层耦合。放接口上更省事。

> 另外两点：
>
> 1. **Redis 从 `core` 里删掉了**——限流去掉之后没有任何消费者（A4.9）。
>    Redis 客户端依赖留在 go.mod 里没关系，但不要再 provide 一个没人用的连接。
> 2. **`security` 配置块必须加**（A4.1）。这个字段的零值是 `false`，
>    也就是「忘记配 = 拒绝启动」，而不是「忘记配 = 裸奔」。

> ⚠️ **这一节曾经漏掉「把服务监听起来」这一步，而且漏得很隐蔽。**
>
> `ginsdk.HTTPServer` 只是个 `http.Server` 的壳，`Serve()` 必须由调用方自己
> 从生命周期钩子里拉起来；本节的 `Init` 里一个 `fx.Invoke(startHTTPServer)` 都没有。
> 后果是：进程正常启动、`[Fx] RUNNING` 打得漂漂亮亮、`/health` 却连不上——
> 因为没有 socket 被创建过。**编译过、依赖图装配过、单测全绿，全都发现不了**，
> 因为那三样都碰不到「真的 bind 了吗」。
>
> 修法是下面这段。两个细节值得留下：
>
> ```go
> // startHTTPServer 真正把端口监听起来。
> //
> // Serve 内部是阻塞的 ListenAndServe，所以只能丢进 goroutine；
> // 代价是「端口被占用」这类错误发生在 goroutine 里（panic，直接打崩进程），
> // OnStart 看不见——服务会照常进入 RUNNING。所以先自己 bind 一次问一句，
> // Serve 之后再回dial 一次：连得上才算启动成功。
> func startHTTPServer(lc fx.Lifecycle, srv *ginsdk.HTTPServer, conf *config.Config) {
> 	lc.Append(fx.Hook{
> 		OnStart: func(ctx context.Context) error {
> 			// 先自己 bind 一次再放开，只为拿到一句像样的错误
> 			listenAddr := conf.HTTP.Host + ":" + conf.HTTP.Port
> 			probe, err := net.Listen("tcp", listenAddr)
> 			if err != nil {
> 				return fmt.Errorf("HTTP 服务无法监听 %s: %w", listenAddr, err)
> 			}
> 			probe.Close()
>
> 			go srv.Serve(ctx)
>
> 			host := conf.HTTP.Host
> 			if host == "" {
> 				host = "127.0.0.1" // Host 为空表示监听 0.0.0.0，回dial 要用具体地址
> 			}
> 			addr := net.JoinHostPort(host, conf.HTTP.Port)
>
> 			deadline := time.Now().Add(2 * time.Second)
> 			for {
> 				conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
> 				if err == nil {
> 					conn.Close()
> 					logsdk.Info(ctx, "HTTP 服务已监听", logsdk.Any("addr", addr))
> 					return nil
> 				}
> 				if time.Now().After(deadline) {
> 					return fmt.Errorf("HTTP 服务未能在 %s 上监听: %w", addr, err)
> 				}
> 				select {
> 				case <-ctx.Done():
> 					return ctx.Err()
> 				case <-time.After(50 * time.Millisecond):
> 				}
> 			}
> 		},
> 		OnStop: func(ctx context.Context) error { return srv.Shutdown(ctx) },
> 	})
> }
> ```
>
> 1. **回dial 那一段是必需的，不是洁癖。** 只写 `go srv.Serve(ctx)` 的话，
>    「端口被别的进程占着」会表现成「启动日志一切正常、请求全部超时」——
>    这正是踩过的坑（`:8090` 上还挂着一个旧进程，新进程 bind 失败，
>    但 `[Fx] RUNNING` 照打，人眼完全看不出来是哪个进程在响应）。
> 2. **`go srv.Serve(ctx)` 里的 panic 会打崩整个进程**，且拿不到 fx 的回滚
>    （已经跑起来的 OnStart 不会被回滚）。所以宁可在这里返回 error。
>
> 这条也解释了为什么 `ensureIndexOnStart` 那种「起不来就别装起来」的写法
> 不够：**它只覆盖了 ES，没有任何东西覆盖「HTTP 端口本身」**。

> ⚠️ **这道探测的覆盖范围要说清楚，否则会误以为「端口冲突已经全防住了」。**
> 它挡得住的是**完全冲突**（两次 bind 落在同一个 IP 栈上）。
> 它挡不住**部分重叠**：macOS 允许 `*:8080`（IPv6 通配，双栈）与
> `127.0.0.1:8080`（IPv4 回环）同时 bind 成功，两个进程都报「已监听」。
>
> 实测过这个状态（本机 8080 上另有一个无关项目）：
>
> | 地址 | 实际应答 |
> |---|---|
> | `127.0.0.1:8080` | 另一个项目 |
> | `[::1]:8080` | **本服务** |
> | `localhost:8080` | **本服务**（`/etc/hosts` 里 `localhost` 同时有 `127.0.0.1` 和 `::1` 两行，curl 先走 IPv6） |
>
> 也就是说：日志说「HTTP 服务已监听」、`/ready` 全绿、浏览器打开 `localhost:8080`
> 看到的就是本服务——**一切都像正常的**，但任何按 IPv4 直连 `127.0.0.1:8080` 的
> 调用方拿到的都是另一个项目。两个人互相偷流量，谁也不报错。
>
> **结论：探测能防的是「起不来」，防不了「表面上起来了、实际只在半个地址上」。**
> 上线前请把端口占用的检查放到部署层（K8s 的 `hostPort` 冲突是硬失败，
> 不会给你半个地址），别指望进程自己探出来。

### A4.17 `cmd/server/main.go`（微调）

```go
package main

import (
	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	logsdk "github.com/PycMono/go-logger-sdk"
	"go.uber.org/fx"
)

func main() {
	logsdk.SetLogger(logsdk.NewLogrus(logsdk.Options{Module: "fastrag"}))

	app := fx.New(infrastructure.Init(config.MustLoad()))
	app.Run()
}
```

> `main` 保持这么薄是有意的：**启动顺序全部由 `Init` 里的生命周期钩子决定，
> 不在这里手写。** 「建索引」「监听端口」这两件事都属于「起不来就别装起来」，
> 写成 `fx.Invoke` + `OnStart` 才会让 `app.Run()` 真的返回错误，
> 而不是打一行日志然后继续往下走。
>
> `fx.New(infrastructure.Init(...))` —— `Init` 返回的是 `fx.Option`，
> 不是 `*fx.App`。这个区分不是风格问题：`fx.New` 才是构建 + 校验依赖图的地方，
> 让 `Init` 返回 `*fx.App` 会把「构造」和「运行」焊死，
> 装配测试就再也拿不到一个「装好了但没启动」的 App 去做路由断言了（T11）。

### A4.18 `infrastructure/controller/http/web/`（演示页）

设计文档里原本没有这一节——它不在任何一条业务链路上。加它的理由只有一个：
**§12.1 那条冒烟回路需要有人真的走一遍，而"打开终端敲三条 curl"是最劝退的一步。**

```go
// infrastructure/controller/http/web/web.go
package web

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// indexHTML 演示页。整页（样式 + 脚本）就这一个文件，没有构建步骤、没有外部依赖——
// 它要能在「clone 下来、起服务、打开浏览器」之后立刻用，这才是它存在的意义。
// 引 CDN 就得有网，用框架就得有 node_modules，两者都会让这个页面在任何一台
// 只能连内网的机器上直接白屏。
//
//go:embed index.html
var indexHTML []byte

type Controller struct{}

func NewController() *Controller { return &Controller{} }

// Index GET /
//
// 用 c.Data 而不是 gingext.Send：这里返回的是 HTML 而不是 JSON 信封，
// 拿统一响应包装它，浏览器只会把 {"code":0,...} 渲染成一串文本。
func (ctl *Controller) Index(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
}
```

`index.html` 是一个单文件页面，四块：

| 区块 | 做什么 |
|---|---|
| 连接参数 | `account` / `kb_no` 两个输入框，默认 `demo` / `demo-kb`，页面上明说「本服务不做鉴权」（§6.1） |
| 导入文档 | 拖拽 / 选择 `.md`、或直接粘贴；`doc_name` 默认取文件名（同名 = 重导入）；`refresh` 勾选框直接对应 §4.2 那个字段 |
| 检索 | `query` / `limit` / `dense_weight` 滑杆 / `rerank_switch`，对应 `dto.SearchDTO` |
| 结果 | 每条显示 `title`、`heading_path`、`score`、`order`、`chunk_id` 前 12 位，正文里高亮命中的词；底下折叠着原始 JSON |

几个刻意的决定：

- **`/` 挂在顶层，不进 `/api/v1`。** 它是给人看的 HTML，不是 API。
  混进 API 前缀里，会让「哪些路径是接口」这件事变得含糊——而这正是
  A4.15 那条「路由刻意做得很少」想守住的东西。
- **`c.Data` 而不是 `gingext.Send`。** 这条是架构 linter 规则 6 唯一放行的
  第二种写法（另一种是 health 探针）。规则 6 拦的是 `c.JSON(`，
  `c.Data` 不在其内，不需要额外开豁免——一个 HTML 页面本来就不该穿 JSON 信封。
- **高亮和转义都在前端做，服务端返回的正文一个字不改。** 高亮是展示层的事；
  要是让它污染 `content`，检索结果的字节就不可复现了。
- **它不改变任何接口约定。** 页面调的就是 `POST /api/v1/docs` 和
  `POST /api/v1/search` 两个公开接口，和外部调用方走同一条路——
  这也是它作为「冒烟回路」可信的前提：页面能用，说明接口能用。

> 演示页**不是**生产入口：它没有鉴权（本来也没有）、没有速率限制（§10.3 不做）、
> 会把原始 JSON 摊在屏幕上。上线前请把它摘掉，或者挡在内网之后。

---

---

## A5 · 对账命令（P1）

### A5.1 `cmd/reconcile/main.go`

```go
// 计数对账：重算某个知识库的 doc_count / chunk_count 并回写。
//
// 计数是用差值维护的（§5.1），删除和异常会让它慢慢漂移。这个命令是兜底：
// 定时（或人工）跑一次，把数字拉回与真实数据一致。
//
// 用法:
//
//	go run ./cmd/reconcile -account acc_001 -kb KB20261008001
package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/PycMono/FastRAG/application/service/ingest"
	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	logsdk "github.com/PycMono/go-logger-sdk"
	"go.uber.org/fx"
)

func main() {
	account := flag.String("account", "", "租户（必填）")
	kbNo := flag.String("kb", "", "知识库编号（必填）")
	flag.Parse()

	if *account == "" || *kbNo == "" {
		flag.Usage()
		return
	}

	logsdk.SetLogger(logsdk.NewLogrus(logsdk.Options{Module: "fastrag-reconcile"}))

	app := fx.New(
		infrastructure.InitCLI(config.MustLoad()),
		fx.Invoke(func(svc *ingest.Service) error {
			ctx := context.Background()

			res, err := svc.Reconcile(ctx, *kbNo, *account)
			if err != nil {
				logsdk.Error(ctx, "对账失败", logsdk.Err(err))
				return err
			}

			logsdk.Info(ctx, "对账完成",
				logsdk.Any("kb_no", res.KBNo),
				logsdk.Any("doc_count", fmt.Sprintf("%d -> %d", res.DocCountBefore, res.DocCountAfter)),
				logsdk.Any("chunk_count", fmt.Sprintf("%d -> %d", res.ChunkCountBefore, res.ChunkCountAfter)),
			)
			return nil
		}),
	)
	app.Run()
}
```

---

## A6 · 落盘时的注意事项

1. **代码已经编译、跑通、冒烟验收过了——但这份附录不是它的准确来源。**

   写这一版附录时，SDK 调用是按记忆里的签名写的，不是逐条核对源码。
   这个差别在 `go-elasticsearch/v9` 上真的爆了：附录里原本写的是 v8 时代的
   扁平调用（`IndicesExists` / `IndicesCreate` / `IndicesRefresh`），
   而 v9 把它们全都搬进了命名空间（`client.Indices.Exists(...)` /
   `.Create(...)` / `.Refresh(...)`），编译期直接报未定义。
   同样的事情也发生在 `ginsdk.HTTPServer` 上——它只是个 `http.Server` 的壳，
   自己不会 `ListenAndServe`，附录当时漏了这一句，服务起得来、端口却没人监听。

   `go-mysql-sdk` / `go-logger-sdk` 那几处反倒蒙对了，因为调用面窄、
   和骨架里既有的用法一致，照抄即可。

   所以：**附录 A 的正确性上限是「一个读过骨架的人凭记忆写出来的实现稿」。**
   仓库里已经落盘、且 `go build` / `go vet` / `go test` / 架构 linter 全过的
   那份代码，才是当前的事实来源；本附录与它有出入时，以代码为准。
   下面第 5 条列了本次真正跑通的清单。

2. **`go.mod` 要加一行**：`github.com/elastic/go-elasticsearch/v9 v9.4.2`。

   另有一处**已落盘时顺手升的版本**：`go-cache-sdk v1.0.3 → v1.0.4`。
   起因是启动后 stdout 每 5 秒冒一行 `获取正在获取断开连接的redis....`，
   与业务无关但很脏。追下去发现：`ginsdk` 的根包 import 了 `session`，
   `session` 又 import 了 `go-cache-sdk/redis/connect`，而那个包有个**包级 `init()`**
   起了个后台重连 goroutine，**在没有任何已注册 redis 客户端时也照打不误**——
   `fmt.Println` 就在 `getBrokenConn()` 的第一行，在遍历之前，无条件执行。
   我们按 A2/A0.2 的约定压根没 provide redis，所以这条日志全是噪声。

   > 这个 `init()` 是**跑得掉但躲不开**的：`HTTPServer` / `ServerOptions` / `StatusOK` /
   > `ErrCodeUnknown` / `HTTPJSONBody` 全在 `ginsdk` 同一个包里，只要用它就必然
   > 把这个 `init()` 链进来，配置层面关不掉。唯一的办法是改依赖本身。
   >
   > v1.0.4 的 diff 正好修的就是它：删掉那行 `fmt.Println`，间隔 5s → 30s，
   > 并把 `recover` 从函数级挪进循环内（原写法一旦某次 `Ping` panic、
   > 被 recover 掉之后协程直接退出，**之后永久失去重连监控**——那是个真 bug）。
   > 实测：22 秒窗口内 v1.0.4 打 0 次，v1.0.3 打 4 次。

3. **`es` 相关字段名一律引用常量**（`FieldAccount` 等）。散着写字符串的话，
   mapping 和查询体对不上不会报错，只会静默查不到——这是这个项目里
   最贵的一类 bug。

4. **§8.3 的 `overlap` 已按 P2 处理**：正文章节与 A2.16 都改成「本期不实现」，
   并写清了两条切片器取舍相反的原因（结构感知不该 overlap、兜底该有）。
   `ChunkOptions` 里没有 `Overlap` 字段，不是漏写。

5. **本次改动落在既有文件上的清单**（其余都是新建）。这几处不落就会编译不过
   或静默出错：

   | 文件 | 要做什么 | 不做的后果 |
   |---|---|---|
   | `infrastructure/persistence/migration/schema.sql` | **基本不动**——本期不加 `ver` 列（§4.1、§13 待定 9） | 无。加多了反而要回滚 |
   | `infrastructure/persistence/po/*.go`（`KnowledgeDoc`） | **不动**——不加 `Ver` 字段 | 无 |
   | `infrastructure/persistence/mapper/knowledge.go` | 删掉 `ToKnowledgeDoc` / `ToKnowledgeDocPO` 里的 `Ver` 映射 | 编译不过（实体已无 `Ver`） |
   | `infrastructure/persistence/knowledge/index_template.go` | 订正 §14→§13 过期注释 | 无功能影响，只是注释指向不存在的章节 |
   | `infrastructure/persistence/knowledge/index_template_test.go` | `TestBuildCreateIndexBody_AllDesignFieldsPresent` 字段清单补 `FieldBizTag` | `biz_tag` 漏建不会有人发现，`terms(biz_tag)` 过滤静默失效 |
   | `deploy/README.md` | 「③ 按设计文档 §4.2 建一次索引」改成**冒烟测试** | 手册会教人抢在服务启动前手建索引，而索引现在由启动期负责（§9.4） |
   | `infrastructure/driver/mysql/*` | `NewProvider` 返回值改成 `*sqlsdk.TransProvider` | A4.16 的两个 `fx.Provide` 拿不到具体类型 |
   | `common/errors/*` | 删 `CodeKnowledgeBaseMismatch`、`ErrKBMismatch` | 未使用的错误码会过 lint，但 403 语义已经不用了 |
   | `infrastructure/driver/gingext/*` | 删 403 错误码映射 | 同上 |
   | `infrastructure/controller/http/register.go` | 加 `/` 和 `/health`、`/ready` 三条顶层路由 + `webctl` 的 Provide（A4.15、A4.18） | 演示页打不开、K8s 探针全红 |
   | `infrastructure/init.go` | 加 `fx.Invoke(startHTTPServer)`（A4.16） | **服务打一屏 `[Fx] RUNNING` 然后不监听任何端口**——本次踩到的最大一个坑 |
   | `config.json` | `embedding` 块指向本机 Ollama 的 `bge-m3`（A4.1 的 `query_prefix` 留空，对称模型不加前缀） | 起得来，但每次导入都在等一个连不上的 8000 端口 |
   | `infrastructure/init_test.go` | `want` 路由清单补 4 条 | 测试红 |

6. **ES 对未在 mapping 里声明的字段是照收不报错的**（dynamic mapping 会自己猜类型）。
   本期的字段集合与 mapping 一一对应，所以不受影响；但将来加字段时记住这一点：
   漏建 mapping 不会报错，只会让查询悄悄匹配不到。A4.7 补 `FieldBizTag`
   正是为了堵这个口子——它现在就在列表外。

7. **落盘后第一件事是跑 §12.1 的冒烟回路，不是压测也不是评测集**。本期最需要真跑一次的、
   靠读代码确认不了的行为是**「重新导入同一文档后，旧切片确实消失了」**（§5.2 ② 的
   `DeleteExcept` 差集语义）。这是唯一一个「写对了也可能没生效」的环节：
   `_id` 差集算错、`must_not` 拼错位置、`refresh_interval` 没到，
   都会表现为「没报错，但旧切片还在」，只能靠 ES 里数一下 `doc_id` 的切片数来确认。

   > **只跑一遍不算过。** §5.2 ② 依赖之前那次刷新，把 ①→④ 连做两轮、间隔 < 30s，
   > 才验得出刷新这一步是不是真接上了。验收口径与判定表见 §12.1。
   >
   > **本条已被验证——而且正是它逮到了落盘期唯一的真 bug。**
   > 第一轮冒烟时「重导入后旧切片消失」时灵时不灵：重导入当下查是 5 条（新旧共存），
   > 隔 40s 再查又是 3 条。根因是 ES 的可见性有**两档**延迟，而代码只堵了一档——
   > 调用方的 `Refresh()` 在 `Save` 与 `DeleteExcept` 之间，是为了让
   > `delete_by_query` 看得见上一批旧切片；但**删除本身的结果同样要等下一次刷新
   > 才对检索可见**，这一档漏了。修法是 `DeleteExcept` 自己带上
   > `WithRefresh(true)`（A4.6）。窗口从「一个刷新周期」缩到「一次请求内的几十毫秒」。
   >
   > 事后看，这条预测准得有点刺眼：「写对了也可能没生效」「只能靠数一下切片数确认」
   > ——两条全中。**这正是它被排在压测和评测集前面的原因。**

8. **`search_mode` 在多库混查时按库分组，不取并集**（A3.3 的 `groupBySearchMode`）。
   一次请求里混着 `title` 和 `title_and_content` 两种库时，**分成两组各发一次 ES 请求**：
   `title` 组只搜 `title` + `heading_path`（向量用 `title_vec`），
   `title_and_content` 组才是三字段 + `content_vec`。

   > 早先这里的写法是「只要有一个库开了正文检索就全局按正文检索」。那是错的——
   > 它悄悄改写了那些建库时声明「只用标题」的库的语义，召回里会冒出标题对不上、
   > 正文里却有这个词的切片。§7.2 与 A3.3 已按分组实现，本节之前和它们对不上。

   代价是 N 种模式 = N 次 ES 请求（实际只有两种，可以并行发）。融合不受影响：
   各路各自按名次进 RRF，分组顺序不产生偏置（A3.2 的 `Fuse`）。

9. **`BatchIngest` 的批内同名预扫必须在起工作池之前同步跑完**（A3.1 的
   `duplicateDocNames`）。它看起来像是能塞进 goroutine 里的小事，但一旦挪进去就回到竞态了——
   而它要防的恰好是并发同名写（§5.5 的交集截断），挪进去等于没防。
   重复项走 `errors` 明细、不进工作池，整批仍返回 200（A4.13）。
   `docKey` 用 `kb_no` 而不是 `kb_id`：这一层还没查库，拿不到自增主键。

10. **`_refresh` 是索引级的，不是文档级**（A4.6 的 `Refresh`）。单索引多租户（D2）下
    它顺带刷到别的租户的段。所以它只出现在两条路径上——重导入（`existing != nil`，
    必刷，§5.2 ②）和调用方显式传 `refresh: true`（§4.2）——常规写入**不要**加它，
    否则等于把全租户的刷新周期改成「每次写」。
