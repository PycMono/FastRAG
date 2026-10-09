# 多服务商模型注册表（model registry）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 embedding 和 rerank 各自持有**一组**服务商，导入与检索都能在请求里按名字选一家，不带名字时走配置里的默认。

**Architecture:** 配置从"整个服务一份"变成"外层默认值 + `models` 逐家覆盖"两层；启动时把每个 entry 与外层合并成一份无零值的参数，造出实现塞进按名字索引的注册表；应用层拿注册表而不是单个实现，在请求入口把名字解析成实现。协议差异（OpenAI 兼容 / DashScope 原生）只落在"请求体怎么拼"和"响应从哪取分数"两个函数里，分批、并发、重试、排序全部共用。

**Tech Stack:** Go 1.26、Uber FX、`net/http`、`encoding/json`、`golang.org/x/sync/errgroup`、`github.com/avast/retry-go/v4`。测试用标准库 `testing` + `httptest`，不引新依赖。

**Spec:** `docs/superpowers/specs/2026-10-09-model-registry-design.md`

## Global Constraints

以下每一条都是 spec 的硬性要求，每个任务都默认包含：

- **配置分层规则**：共用的字段放外层大家一起持有；只有逐家不同、或者要单独调的才放进 `models`。
- **`dim` 与 `top_n` 只在外层**，entry 里**没有**这两个字段——它们是服务级策略，不是某家的性质。
- **`query_prefix` 只在 entry 里**，外层没有——它是模型自身的性质（E5/GTE 要 `"query: "`，bge-m3 不要）。
- **换 embedding 不校验、不落痕、旧数据不做任何处理**；`knowledge_doc` 不加列。维度相同但向量空间不同的组合会静默退化成噪声排序，这是明确接受的代价。
- **DashScope 分批时发给上游的 `parameters.top_n` 恒等于本批 documents 数量**，绝不能用业务上的 topN。
- **两个响应解析器不能共用下标字段**：rerank 是 `index`，embedding 是 `text_index`。抄错一个的后果是所有向量都挂到第 0 条上，且不报错。
- **配置校验失败必须挡住启动**（`config.Load()` 返回错误），错误消息里要列出当前可用的名字。
- **不做配置热加载**；注册表启动时一次建好，之后只读、不加锁。
- **`protocol` 取值**：`""`（等价 openai）或 `"openai"` 或 `"dashscope"`，其余一律启动报错。
- 错误码沿用现有约定：参数错误 `CodeInvalidParam = 10001`，embedding 失败 `CodeEmbeddingFail = 10201`，rerank 失败 `CodeRerankFail = 10302`。
- **提交信息用中文**，格式 `type: 描述`；每个 commit 的正文写清"为什么"，结尾加 `Co-Authored-By: Claude Code <noreply@anthropic.com>`。
- 每个任务结束都要 `gofmt -l` 不新增脏文件。

**构建状态说明（重要）**：Task 1–3 会改动配置结构和实现体的构造签名，期间 `go build ./...` **会红**——这是刻意的，每个任务只跑自己那个包的测试。Task 4 结束时恢复全绿。不要为了"保持可编译"而临时保留旧字段，那正是 spec 明确拒绝的兼容层。

---

## Task 0: 先把在飞的重排实现固定下来

这一步**不是本方案的内容**，只是把工作区里那批已经全绿的改动提交掉，后面每个任务的 commit 才干净、才看得出改了什么。

当前工作区是绿的（`go build ./...` 无输出、`go test ./...` 全过），里面有：

- 新增：`infrastructure/serviceimpl/rerank.go`、`rerank_test.go`、`httpclient.go`、`application/service/search/service_test.go`、`config.example.json`
- 改写：`embedding_openai.go` → `embedding.go`（去掉厂商名）、`register.go`、`init.go`、`search/{service,options,tuning}.go`、`dto/search.go`、`errors.go`
- 删除：`rerank_stub.go`、`config.json`（改由 `config.example.json` 复制，已加进 `.gitignore`）

**Files:**
- 无新增

**Interfaces:**
- Consumes: 无
- Produces: 一个干净的 HEAD，后面所有任务都基于它

- [ ] **Step 1: 确认工作区是绿的**

Run:
```bash
go build ./... && go vet ./... && go test ./...
```
Expected: 全部无输出 / 全 ok。

- [ ] **Step 2: 提交**

```bash
git add -A
git status -s
```
确认暂存区里是本计划开头列出的那批文件，且**没有** `docs/superpowers/` 之外的其他文档改动混进来。

```bash
git commit -m "$(cat <<'EOF'
feat: 接入真实 rerank 实现，检索可按 retrieve_count 放大候选池

- 新增 OpenAI 兼容 /rerank 客户端（分批 + errgroup 并发 + retry-go 重试），
  embedding 与 rerank 共用一个 postJSON 传输层
- rerank 候选文本拼上 title 与 heading_path，让模型知道切片在原文中的位置
- 新增 retrieve_count：rerank 开启时先多召回一批候选再精排，
  默认 limit*2、上限 200，融合池大小随之放大
- 配置从 config.json 改为 config.example.json 复制（带本机连接信息的那份不入库）

Co-Authored-By: Claude Code <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 3: 确认干净**

Run: `git status -s`
Expected: 只剩 `??` 之外的输出为空（即工作区干净）。

---

## Task 1: 配置改成两层结构 + 启动校验

把 `embedding` / `rerank` 从"扁平一份"改成 `外层 + models`，并加上"外层与 entry 合并"和"启动期校验"两件事。

**Files:**
- Modify: `infrastructure/config/config.go`（第 46–69 行整块替换；`Load()` 里加一次校验；补三个辅助函数）
- Create: `infrastructure/config/config_test.go`
- Modify: `config.example.json`（整个文件替换）

**Interfaces:**
- Consumes: 无
- Produces:
  - `config.EmbeddingConfig{Default string; Dim int; BatchSize int; TimeoutMS int; Models map[string]EmbeddingEntry}`
  - `config.EmbeddingEntry{Protocol, URL, APIKey, Model string; BatchSize, TimeoutMS int; QueryPrefix string}`
  - `config.RerankConfig{Enabled bool; Default string; BatchSize, Concurrency, TimeoutMS, TopN int; Models map[string]RerankEntry}`
  - `config.RerankEntry{Protocol, URL, APIKey, Model string; BatchSize, Concurrency, TimeoutMS int}`
  - `config.EmbeddingParams` / `config.RerankParams`：合并后的完整参数（含从外层继承来的 `Dim`）
  - `(EmbeddingConfig) Params(name string) (EmbeddingParams, error)` / `(EmbeddingConfig) Names() []string`
  - `(RerankConfig) Params(name string) (RerankParams, error)` / `(RerankConfig) Names() []string`
  - `config.ProtocolOpenAI = "openai"`、`config.ProtocolDashScope = "dashscope"`
  - `(*Config) validate() error`（在 `Load()` 里被调用）

- [ ] **Step 1: 写失败的测试**

创建 `infrastructure/config/config_test.go`：

```go
package config

import (
	"strings"
	"testing"
)

// validConfig 一份能过校验的配置，各用例在它上面改坏一处。
//
// 「bge-m3」故意只写 url：本机 Ollama 那家除了地址什么都继承外层，
// 它同时也是「model 留空时取 key」这条规则的样本。
func validConfig() *Config {
	return &Config{
		Embedding: EmbeddingConfig{
			Default:   "bge-m3",
			Dim:       1024,
			BatchSize: 16,
			TimeoutMS: 60000,
			Models: map[string]EmbeddingEntry{
				"bge-m3": {URL: "http://127.0.0.1:11434/v1/embeddings"},
				"qwen": {
					Protocol:  ProtocolDashScope,
					URL:       "https://example.com/embed",
					APIKey:    "sk-x",
					Model:     "qwen3.7-text-embedding",
					BatchSize: 10,
				},
			},
		},
		Rerank: RerankConfig{
			Enabled:     true,
			Default:     "qwen3-rerank",
			BatchSize:   32,
			Concurrency: 4,
			TimeoutMS:   10000,
			TopN:        0,
			Models: map[string]RerankEntry{
				"qwen3-rerank": {
					Protocol: ProtocolDashScope,
					URL:      "https://example.com/rerank",
					APIKey:   "sk-x",
					Model:    "qwen3.7-text-rerank",
				},
			},
		},
	}
}

func TestConfig_Validate_EmbeddingModelsEmpty(t *testing.T) {
	c := validConfig()
	c.Embedding.Models = nil
	err := c.validate()
	if err == nil || !strings.Contains(err.Error(), "embedding.models 为空") {
		t.Fatalf("期望报 embedding.models 为空，得到 %v", err)
	}
}

func TestConfig_Validate_EmbeddingDefaultUnknown(t *testing.T) {
	c := validConfig()
	c.Embedding.Default = "nope"
	err := c.validate()
	if err == nil {
		t.Fatal("default 指向不存在的 key 必须报错")
	}
	// 报错消息要带可用名单，否则调用方不知道能填什么
	for _, want := range []string{"bge-m3", "qwen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错消息里应列出可用名字 %q，得到 %v", want, err)
		}
	}
}

func TestConfig_Validate_EmbeddingDefaultEmpty(t *testing.T) {
	c := validConfig()
	c.Embedding.Default = ""
	if err := c.validate(); err == nil {
		t.Fatal("embedding.default 为空必须报错")
	}
}

func TestConfig_Validate_EmbeddingBatchSizeZero(t *testing.T) {
	c := validConfig()
	c.Embedding.BatchSize = 0
	if err := c.validate(); err == nil {
		t.Fatal("外层 batch_size 为 0 必须报错：0 不是合法取值，只可能是漏配")
	}
}

func TestConfig_Validate_EmbeddingTimeoutZero(t *testing.T) {
	c := validConfig()
	c.Embedding.TimeoutMS = 0
	if err := c.validate(); err == nil {
		t.Fatal("外层 timeout_ms 为 0 必须报错")
	}
}

func TestConfig_Validate_BadProtocol(t *testing.T) {
	c := validConfig()
	e := c.Embedding.Models["bge-m3"]
	e.Protocol = "gemini"
	c.Embedding.Models["bge-m3"] = e
	if err := c.validate(); err == nil {
		t.Fatal("未知 protocol 必须报错")
	}
}

func TestConfig_Validate_EmptyURL(t *testing.T) {
	c := validConfig()
	e := c.Embedding.Models["bge-m3"]
	e.URL = "   "
	c.Embedding.Models["bge-m3"] = e
	if err := c.validate(); err == nil {
		t.Fatal("url 为空（含纯空白）必须报错")
	}
}

func TestConfig_Validate_RerankEnabledButModelsEmpty(t *testing.T) {
	c := validConfig()
	c.Rerank.Models = nil
	if err := c.validate(); err == nil {
		t.Fatal("rerank.enabled=true 但 models 为空必须报错")
	}
}

func TestConfig_Validate_RerankDisabledAllowsEmpty(t *testing.T) {
	c := validConfig()
	c.Rerank.Enabled = false
	c.Rerank.Models = nil
	c.Rerank.Default = ""
	if err := c.validate(); err != nil {
		t.Fatalf("enabled=false 时 rerank 整块允许为空（保持「没配也能启动」的语义），得到 %v", err)
	}
}

func TestConfig_Validate_RerankBatchSizeZero(t *testing.T) {
	c := validConfig()
	c.Rerank.BatchSize = 0
	if err := c.validate(); err == nil {
		t.Fatal("rerank 外层 batch_size 为 0 必须报错")
	}
}

func TestEmbeddingParams_InheritFromOuter(t *testing.T) {
	p, err := validConfig().Embedding.Params("bge-m3")
	if err != nil {
		t.Fatal(err)
	}
	if p.Dim != 1024 {
		t.Errorf("dim 应继承外层，得到 %d", p.Dim)
	}
	if p.BatchSize != 16 {
		t.Errorf("batch_size 应继承外层，得到 %d", p.BatchSize)
	}
	if p.TimeoutMS != 60000 {
		t.Errorf("timeout_ms 应继承外层，得到 %d", p.TimeoutMS)
	}
	if p.Model != "bge-m3" {
		t.Errorf("model 留空时应取 map 的 key，得到 %q", p.Model)
	}
	if p.Protocol != "" {
		t.Errorf("protocol 留空表示 openai，这里应原样为空串，得到 %q", p.Protocol)
	}
}

func TestEmbeddingParams_EntryOverridesOuter(t *testing.T) {
	p, err := validConfig().Embedding.Params("qwen")
	if err != nil {
		t.Fatal(err)
	}
	if p.BatchSize != 10 {
		t.Errorf("entry 写了 batch_size 应盖掉外层，得到 %d", p.BatchSize)
	}
	if p.TimeoutMS != 60000 {
		t.Errorf("entry 没写 timeout_ms 应继承外层，得到 %d", p.TimeoutMS)
	}
	if p.Model != "qwen3.7-text-embedding" {
		t.Errorf("entry 写了 model 就用它，得到 %q", p.Model)
	}
	if p.Name != "qwen" {
		t.Errorf("Name 应是 map 的 key，得到 %q", p.Name)
	}
}

func TestEmbeddingParams_EmptyNameUsesDefault(t *testing.T) {
	p, err := validConfig().Embedding.Params("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "bge-m3" {
		t.Errorf("空名字应回落到 default，得到 %q", p.Name)
	}
}

func TestEmbeddingParams_UnknownName(t *testing.T) {
	_, err := validConfig().Embedding.Params("nope")
	if err == nil {
		t.Fatal("未知名字必须报错，绝不能悄悄用 default 那家")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("报错消息里应带上传进来的名字，得到 %v", err)
	}
}

func TestRerankParams_InheritAndOverride(t *testing.T) {
	p, err := validConfig().Rerank.Params("qwen3-rerank")
	if err != nil {
		t.Fatal(err)
	}
	if p.BatchSize != 32 || p.Concurrency != 4 || p.TimeoutMS != 10000 {
		t.Errorf("三个外层参数都应被继承，得到 batch=%d conc=%d timeout=%d",
			p.BatchSize, p.Concurrency, p.TimeoutMS)
	}
	if p.TopN != 0 {
		t.Errorf("top_n 只在外层，应原样带过来，得到 %d", p.TopN)
	}
	if p.URL != "https://example.com/rerank" {
		t.Errorf("url 应原样带过来，得到 %q", p.URL)
	}
}

func TestNames_Sorted(t *testing.T) {
	names := validConfig().Embedding.Names()
	if len(names) != 2 || names[0] != "bge-m3" || names[1] != "qwen" {
		t.Errorf("Names 必须排序且完整，得到 %v", names)
	}
}
```

- [ ] **Step 2: 跑测试，确认编译失败**

Run: `go test ./infrastructure/config/`
Expected: FAIL — `undefined: ProtocolDashScope`、`c.validate undefined`、`EmbeddingConfig.Params undefined` 等。

- [ ] **Step 3: 改 config.go**

把 `infrastructure/config/config.go` 第 46–69 行（`EmbeddingConfig` 与 `RerankConfig` 两个结构体整块）替换成：

```go
// ─── 模型注册表：embedding / rerank ────────────────────────────────────────
//
// 两块都分两层：外层是全体共用的默认值，models 里是逐家不同的部分。
// 划分规则一句话——共用的放外层大家一起持有，只有逐家不同、或者要单独调的
// 才放进 models（设计文档 §3）。
//
// 下面两个字段是刻意只在外层的：
//   - dim   —— 索引的性质（content_vec 的 dims 建好就改不了），不是某家的性质
//   - top_n —— 服务级的策略（先全量拿分、最后自己截断），任何一家单独改了都不成立
// 而 query_prefix 反过来只在 entry 里，它是模型自身的性质。

// 协议名。空串等价于 ProtocolOpenAI。
const (
	ProtocolOpenAI    = "openai"
	ProtocolDashScope = "dashscope"
)

// EmbeddingConfig 向量化服务的注册表。
type EmbeddingConfig struct {
	Default   string                    `json:"default"` // 请求没带 model 时用哪个；必须是 models 的一个 key
	Dim       int                       `json:"dim"`     // 索引的性质，不是某家的性质
	BatchSize int                       `json:"batch_size"`
	TimeoutMS int                       `json:"timeout_ms"`
	Models    map[string]EmbeddingEntry `json:"models"`
}

// EmbeddingEntry 一家向量化服务，只放"逐家不同"的字段。
type EmbeddingEntry struct {
	Protocol    string `json:"protocol"`     // "" 或 "openai" | "dashscope"
	URL         string `json:"url"`          // 完整端点，后缀也要写
	APIKey      string `json:"api_key"`
	Model       string `json:"model"`        // 发给上游的真名；留空 = 用 map 的 key
	BatchSize   int    `json:"batch_size"`   // 0 = 继承外层
	TimeoutMS   int    `json:"timeout_ms"`   // 0 = 继承外层
	QueryPrefix string `json:"query_prefix"` // 不写 = 不加前缀
}

// EmbeddingParams 造一个 embedding 实现所需的全部参数：外层与 entry 合并后的结果。
//
// 合并发生在构造注册表的时候（启动期一次），所以实现体里读到的 BatchSize /
// TimeoutMS 一定是有效值，不必再判 0，也不必知道"继承"这回事。
type EmbeddingParams struct {
	Name        string // models 里的 key；日志与报错用
	Protocol    string
	URL         string
	APIKey      string
	Model       string
	Dim         int
	BatchSize   int
	TimeoutMS   int
	QueryPrefix string
}

// Params 取某个 embedding 的完整参数；name 为空时用 Default。
func (c EmbeddingConfig) Params(name string) (EmbeddingParams, error) {
	if name == "" {
		name = c.Default
	}
	e, ok := c.Models[name]
	if !ok {
		return EmbeddingParams{}, unknownModel("embedding", name, c.Names())
	}

	p := EmbeddingParams{
		Name:        name,
		Protocol:    e.Protocol,
		URL:         e.URL,
		APIKey:      e.APIKey,
		Model:       e.Model,
		Dim:         c.Dim,
		BatchSize:   e.BatchSize,
		TimeoutMS:   e.TimeoutMS,
		QueryPrefix: e.QueryPrefix,
	}
	if p.Model == "" {
		p.Model = name
	}
	if p.BatchSize <= 0 {
		p.BatchSize = c.BatchSize
	}
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = c.TimeoutMS
	}
	return p, nil
}

// Names 返回所有可用的名字，已排序——报错消息里的顺序不能每次都变。
func (c EmbeddingConfig) Names() []string { return sortedKeys(c.Models) }

// validate 校验 embedding 这块能不能用。
func (c EmbeddingConfig) validate() error {
	names := c.Names()
	if len(names) == 0 {
		return errors.New("embedding.models 为空：至少要配一家向量化服务")
	}
	if c.Default == "" {
		return fmt.Errorf("embedding.default 为空：请求不带 model 时没有兜底。可用：%v", names)
	}
	if _, ok := c.Models[c.Default]; !ok {
		return fmt.Errorf("embedding.default=%q 不在 models 里。可用：%v", c.Default, names)
	}
	if c.BatchSize <= 0 {
		return fmt.Errorf("embedding.batch_size 必须大于 0，当前 %d", c.BatchSize)
	}
	if c.TimeoutMS <= 0 {
		return fmt.Errorf("embedding.timeout_ms 必须大于 0，当前 %d", c.TimeoutMS)
	}
	for _, name := range names {
		e := c.Models[name]
		if err := validateEndpoint("embedding", name, e.Protocol, e.URL); err != nil {
			return err
		}
	}
	return nil
}

// RerankConfig 重排序服务的注册表。
//
// enabled=false 时整个实例不做重排：models 允许为空，default 也不要求，
// 任何名字都会拿到空实现（保持"没配 rerank 服务也能正常启动和检索"的语义）。
type RerankConfig struct {
	Enabled     bool                   `json:"enabled"`
	Default     string                 `json:"default"`
	BatchSize   int                    `json:"batch_size"`
	Concurrency int                    `json:"concurrency"`
	TimeoutMS   int                    `json:"timeout_ms"`
	TopN        int                    `json:"top_n"` // 0 = 全部返回，最后按 limit 截断
	Models      map[string]RerankEntry `json:"models"`
}

// RerankEntry 一家重排序服务，只放"逐家不同"的字段。
type RerankEntry struct {
	Protocol    string `json:"protocol"` // "" 或 "openai" | "dashscope"
	URL         string `json:"url"`      // 完整端点，后缀也要写
	APIKey      string `json:"api_key"`
	Model       string `json:"model"`
	BatchSize   int    `json:"batch_size"`  // 0 = 继承外层
	Concurrency int    `json:"concurrency"` // 0 = 继承外层
	TimeoutMS   int    `json:"timeout_ms"`  // 0 = 继承外层
}

// RerankParams 造一个 rerank 实现所需的全部参数：外层与 entry 合并后的结果。
type RerankParams struct {
	Name        string
	Protocol    string
	URL         string
	APIKey      string
	Model       string
	BatchSize   int
	Concurrency int
	TimeoutMS   int
	TopN        int
}

// Params 取某个 rerank 的完整参数；name 为空时用 Default。
func (c RerankConfig) Params(name string) (RerankParams, error) {
	if name == "" {
		name = c.Default
	}
	e, ok := c.Models[name]
	if !ok {
		return RerankParams{}, unknownModel("rerank", name, c.Names())
	}

	p := RerankParams{
		Name:        name,
		Protocol:    e.Protocol,
		URL:         e.URL,
		APIKey:      e.APIKey,
		Model:       e.Model,
		BatchSize:   e.BatchSize,
		Concurrency: e.Concurrency,
		TimeoutMS:   e.TimeoutMS,
		TopN:        c.TopN,
	}
	if p.Model == "" {
		p.Model = name
	}
	if p.BatchSize <= 0 {
		p.BatchSize = c.BatchSize
	}
	if p.Concurrency <= 0 {
		p.Concurrency = c.Concurrency
	}
	if p.TimeoutMS <= 0 {
		p.TimeoutMS = c.TimeoutMS
	}
	return p, nil
}

// Names 返回所有可用的名字，已排序。
func (c RerankConfig) Names() []string { return sortedKeys(c.Models) }

// validate 校验 rerank 这块能不能用。
func (c RerankConfig) validate() error {
	if !c.Enabled {
		// 整个实例不做重排：这一块允许整块为空，也不要求 default
		return nil
	}

	names := c.Names()
	if len(names) == 0 {
		return errors.New("rerank.enabled=true 但 rerank.models 为空：至少配一家，或把 enabled 改成 false")
	}
	if c.Default == "" {
		return fmt.Errorf("rerank.default 为空：请求不带 rerank_model 时没有兜底。可用：%v", names)
	}
	if _, ok := c.Models[c.Default]; !ok {
		return fmt.Errorf("rerank.default=%q 不在 models 里。可用：%v", c.Default, names)
	}
	if c.BatchSize <= 0 {
		return fmt.Errorf("rerank.batch_size 必须大于 0，当前 %d", c.BatchSize)
	}
	if c.Concurrency <= 0 {
		return fmt.Errorf("rerank.concurrency 必须大于 0，当前 %d", c.Concurrency)
	}
	if c.TimeoutMS <= 0 {
		return fmt.Errorf("rerank.timeout_ms 必须大于 0，当前 %d", c.TimeoutMS)
	}
	for _, name := range names {
		e := c.Models[name]
		if err := validateEndpoint("rerank", name, e.Protocol, e.URL); err != nil {
			return err
		}
	}
	return nil
}

// sortedKeys 取 map 的 key 并排序。列可用名字时用，顺序必须稳定。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unknownModel 组装"名字不在注册表里"的错误，带上可用名单。
func unknownModel(kind, name string, names []string) error {
	return fmt.Errorf("没有名为 %q 的 %s 模型。可用：%v", name, kind, names)
}

// validateEndpoint 校验一家服务商的端点：url 必填且写全，protocol 只能是已知的几个。
func validateEndpoint(kind, name, protocol, url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("%s.models[%s].url 为空：必须写完整端点（含后缀）", kind, name)
	}
	switch protocol {
	case "", ProtocolOpenAI, ProtocolDashScope:
		return nil
	}
	return fmt.Errorf("%s.models[%s].protocol=%q 不认识，只能是 %q 或 %q",
		kind, name, protocol, ProtocolOpenAI, ProtocolDashScope)
}

// validate 启动期校验（设计文档 §7）。任一条件不满足就返回错误，服务起不来。
func (c *Config) validate() error {
	if err := c.Embedding.validate(); err != nil {
		return err
	}
	return c.Rerank.validate()
}
```

再把 `Load()` 改成（`json.Unmarshal` 之后加一次校验）：

```go
	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}

	// 配置错了要在启动时就说清楚，不能等到第一次检索才发现（§7）
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config.json 校验失败: %w", err)
	}

	conf = c
	return c, nil
```

最后把 import 改成：

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)
```

- [ ] **Step 4: 跑测试，确认通过**

Run: `go test ./infrastructure/config/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 换掉 config.example.json**

整个文件替换为：

```json
{
  "app": "fastrag",
  "debug": true,
  "http": {
    "host": "",
    "port": "8080",
    "read_timeout": 120,
    "write_timeout": 120
  },
  "mysql": {
    "host": "127.0.0.1",
    "port": 3306,
    "database": "fastrag",
    "user": "root",
    "password": "123456",
    "max_open": 100,
    "max_idle": 10,
    "conn_lifetime": 3600,
    "conn_timeout": 3,
    "log_level": 3,
    "slow_threshold": 500
  },
  "redis": {
    "addr": [],
    "password": "",
    "db": 0,
    "pool_size": 5
  },
  "snowflake_node_id": 1,
  "es": {
    "addrs": ["http://127.0.0.1:9200"],
    "index": "fastrag",
    "username": "",
    "password": ""
  },

  "embedding": {
    "default": "bge-m3",
    "dim": 1024,
    "batch_size": 16,
    "timeout_ms": 60000,
    "models": {
      "bge-m3": {
        "url": "http://127.0.0.1:11434/v1/embeddings"
      }
    }
  },

  "rerank": {
    "enabled": false,
    "default": "",
    "batch_size": 32,
    "concurrency": 4,
    "timeout_ms": 10000,
    "top_n": 0,
    "models": {}
  },

  "search": {
    "bm25_top": 50,
    "knn_top": 50,
    "num_candidates": 200,
    "rank_constant": 60,
    "dense_weight": 0.5,
    "default_retrieve_count": 0
  },
  "security": {
    "trust_request_account": true
  }
}
```

> 示例里 `rerank.enabled=false` 是一个**能跑起来**的组合：`enabled=false` 时 `models` 允许为空。要看多服务商的完整样子，去设计文档 §3。

- [ ] **Step 6: 确认示例配置本身能过校验**

Run:
```bash
go run ./cmd/server -h 2>/dev/null; cp config.example.json /tmp/cfgcheck.json && go test ./infrastructure/config/ -run TestConfig_ExampleFile -v 2>/dev/null || echo "（示例配置的加载没有单测，靠 Step 8 的真栈验证）"
```
Expected: 不报错。真正的校验用 Step 8。

- [ ] **Step 7: 提交**

```bash
gofmt -l infrastructure/config/
git add infrastructure/config/config.go infrastructure/config/config_test.go config.example.json
git commit -m "$(cat <<'EOF'
feat(config): embedding/rerank 改成「外层 + models」两层，并加启动校验

共用的字段提到外层大家一起持有，只有逐家不同的才留在 models 里：
- embedding 外层持 default / dim / batch_size / timeout_ms
- rerank 外层持 enabled / default / batch_size / concurrency / timeout_ms / top_n

dim 与 top_n 刻意只在外层：它们一个是索引的性质、一个是服务级的策略，
任何一家单独改了都不成立。query_prefix 反过来只在 entry 里，它是模型自身的性质。

启动期校验（Load 里做）：models 非空、default 命中一个 key、url 非空、
protocol 只能是 openai / dashscope、外层的批大小与超时为正；
rerank.enabled=false 时整块允许为空，保持「没配也能启动」的语义。
报错消息里一律列出当前可用的名字。

Params() 负责把外层与 entry 合并成一份没有零值的参数，供注册表构造实现时使用。

Co-Authored-By: Claude Code <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: embedding 实现改收 Params + 加 DashScope 协议分支

`NewEmbedding(conf *config.Config)` 换成 `newEmbedding(p config.EmbeddingParams) *Embedding`，并把请求体与响应解析按协议分叉成两个函数。

**Files:**
- Modify: `infrastructure/serviceimpl/embedding.go`
- Create: `infrastructure/serviceimpl/embedding_test.go`

**Interfaces:**
- Consumes: `config.EmbeddingParams`、`config.ProtocolDashScope`（Task 1）
- Produces:
  - `newEmbedding(p config.EmbeddingParams) *Embedding`（包内可见）
  - `(*Embedding) buildBody(texts []string, textType string) any`
  - `(*Embedding) parseVectors(parsed *embedResponse, n int) [][]float32`
  - 常量 `textTypeQuery = "query"`、`textTypeDocument = "document"`
  - `Embedding` 结构体的字段变成 `client / protocol / url / apiKey / model / dim / batchSize / queryPrefix`（`baseURL` 改名 `url`，因为现在存的是完整端点）

- [ ] **Step 1: 写失败的测试**

创建 `infrastructure/serviceimpl/embedding_test.go`：

```go
package serviceimpl

import (
	"encoding/json"
	"testing"

	"github.com/PycMono/FastRAG/infrastructure/config"
)

// newTestEmbedding 造一个只用于拼请求体和解析响应的 embedding，不发任何请求。
func newTestEmbedding(protocol string) *Embedding {
	return &Embedding{protocol: protocol, model: "test-model"}
}

func TestEmbedding_BuildBody_OpenAI(t *testing.T) {
	body, err := json.Marshal(newTestEmbedding("").buildBody([]string{"a", "b"}, textTypeDocument))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	if got["model"] != "test-model" {
		t.Errorf("model 应原样带上，得到 %v", got["model"])
	}
	input, ok := got["input"].([]any)
	if !ok || len(input) != 2 {
		t.Fatalf("openai 的 input 应是裸数组，得到 %#v", got["input"])
	}
	if _, has := got["parameters"]; has {
		t.Error("openai 这一路不该发 parameters")
	}
}

func TestEmbedding_BuildBody_DashScope(t *testing.T) {
	body, err := json.Marshal(newTestEmbedding(config.ProtocolDashScope).
		buildBody([]string{"a"}, textTypeQuery))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	input, ok := got["input"].(map[string]any)
	if !ok {
		t.Fatalf("dashscope 的 input 应是 {texts:[...]}，得到 %#v", got["input"])
	}
	if texts, ok := input["texts"].([]any); !ok || len(texts) != 1 {
		t.Errorf("input.texts 应是一条，得到 %#v", input["texts"])
	}

	params, ok := got["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("dashscope 这一路必须带 parameters，得到 %#v", got["parameters"])
	}
	if params["text_type"] != textTypeQuery {
		t.Errorf("text_type 应是 %q，得到 %v", textTypeQuery, params["text_type"])
	}
}

// 实测响应：{"data":[{"index":..,"embedding":[..]}],...}
func TestEmbedding_ParseVectors_OpenAI(t *testing.T) {
	var parsed embedResponse
	if err := json.Unmarshal([]byte(
		`{"data":[{"index":1,"embedding":[0.1,0.2]},{"index":0,"embedding":[0.3,0.4]}],"usage":{}}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}

	got := newTestEmbedding("").parseVectors(&parsed, 2)
	if len(got) != 2 {
		t.Fatalf("应回填 2 条，得到 %d", len(got))
	}
	// 按 index 回填，不信返回数组的顺序
	if got[0][0] != 0.3 || got[1][0] != 0.1 {
		t.Errorf("应按 index 回填，得到 %v", got)
	}
}

// 实测响应：{"output":{"embeddings":[{"text_index":..,"embedding":[..]}]},...}
func TestEmbedding_ParseVectors_DashScope(t *testing.T) {
	var parsed embedResponse
	if err := json.Unmarshal([]byte(
		`{"output":{"embeddings":[{"text_index":1,"embedding":[0.1]},{"text_index":0,"embedding":[0.2]}]},`+
			`"usage":{},"request_id":"x"}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}

	got := newTestEmbedding(config.ProtocolDashScope).parseVectors(&parsed, 2)
	if len(got) != 2 {
		t.Fatalf("应回填 2 条，得到 %d", len(got))
	}
	if got[0][0] != 0.2 || got[1][0] != 0.1 {
		t.Errorf("应按 text_index 回填，得到 %v", got)
	}
}

// 两个协议的下标字段名不同：rerank 是 index，embedding 的 dashscope 是 text_index。
// 抄错一个的后果是所有向量都挂到第 0 条上，而且不报错（§4.1）。
func TestEmbedding_ParseVectors_IndexFieldsNotInterchangeable(t *testing.T) {
	// dashscope 的响应体里混进 openai 风格的 index 字段，dashscope 这一路必须无视它
	var parsed embedResponse
	if err := json.Unmarshal([]byte(
		`{"output":{"embeddings":[{"index":0,"embedding":[0.9]}]}}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}
	got := newTestEmbedding(config.ProtocolDashScope).parseVectors(&parsed, 1)
	if len(got[0]) != 0 {
		t.Errorf("dashscope 这一路不该认 index 字段，得到 %v", got[0])
	}

	// 反过来：openai 的响应体里混进 text_index，openai 这一路也必须无视它
	parsed = embedResponse{}
	if err := json.Unmarshal([]byte(
		`{"data":[{"text_index":0,"embedding":[0.9]}]}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}
	got = newTestEmbedding("").parseVectors(&parsed, 1)
	if len(got[0]) != 0 {
		t.Errorf("openai 这一路不该认 text_index 字段，得到 %v", got[0])
	}
}
```

- [ ] **Step 2: 跑测试，确认编译失败**

Run: `go test ./infrastructure/serviceimpl/ -run TestEmbedding_`
Expected: FAIL — `undefined: textTypeQuery`、`e.buildBody undefined`、`embedResponse` 没有 `Output` 字段等。

- [ ] **Step 3: 改 embedding.go 的头部与构造**

把 `infrastructure/serviceimpl/embedding.go` 开头那段厂商名注释改成：

```go
// 本文件实现向量化：一个实现，两处形状开关。
//
// 分批、按下标回填、维度校验全部共用，一行都不分叉；真正随协议变的只有
// "请求体怎么拼"（buildBody）和"响应从哪取向量、下标字段叫什么"（parseVectors）。
//
//	openai    POST {url}                       body {model, input:[...]}
//	                                          响应 data[].{index, embedding}
//	dashscope POST {url}（原生 /api/v1/services/...）
//	                                           body {model, input:{texts}, parameters:{text_type}}
//	                                          响应 output.embeddings[].{text_index, embedding}
//
// ⚠️ 两个协议的下标字段名不一样：openai 是 index，dashscope 是 text_index。
// 形状看着像，抄错一个就是把所有向量挂到第 0 条上，而且不报错（设计文档 §4.1）。
```

把 `Embedding` 结构体与构造函数换成：

```go
// text_type 是 dashscope 协议的一部分，不是可选装饰：它替代了 openai 那边
// 靠字符串前缀实现的 query/doc 区分（§4.2）。
const (
	textTypeQuery    = "query"
	textTypeDocument = "document"
)

// Embedding 向量化实现。
type Embedding struct {
	client      *http.Client
	protocol    string
	url         string // 完整端点，不再拼 /embeddings 后缀
	apiKey      string
	model       string
	dim         int
	batchSize   int
	queryPrefix string
}

// newEmbedding 从一份已合并好的参数构造实现。
//
// 只有注册表调它——"继承外层"在 Params() 里就填完了，这里不再判 0。
func newEmbedding(p config.EmbeddingParams) *Embedding {
	return &Embedding{
		client:      &http.Client{Timeout: time.Duration(p.TimeoutMS) * time.Millisecond},
		protocol:    p.Protocol,
		url:         p.URL,
		apiKey:      p.APIKey,
		model:       p.Model,
		dim:         p.Dim,
		batchSize:   p.BatchSize,
		queryPrefix: p.QueryPrefix,
	}
}

func (e *Embedding) Dim() int      { return e.dim }
func (e *Embedding) Model() string { return e.model }
```

- [ ] **Step 4: 改 embed / EmbedDocs / EmbedQuery**

`EmbedDocs` 里的调用改成传 `textTypeDocument`：

```go
		vecs, err := e.embed(ctx, texts[start:end], textTypeDocument)
```

`EmbedQuery` 里的调用改成传 `textTypeQuery`：

```go
	vecs, err := e.embed(ctx, []string{e.queryPrefix + text}, textTypeQuery)
```

`embedResponse` 与 `embed` 整块替换为：

```go
// embedResponse 两种协议的响应。data 是 openai 的，output.embeddings 是 dashscope 的；
// 按协议只认其中一个，另一个留空。
type embedResponse struct {
	apiError
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Output *struct {
		Embeddings []struct {
			TextIndex int       `json:"text_index"`
			Embedding []float32 `json:"embedding"`
		} `json:"embeddings"`
	} `json:"output"`
}

// buildBody 按协议拼请求体。
//
// textType 只有 dashscope 用得上（协议自带 query/document 之分）；openai 那边
// query 与 doc 的区别靠 queryPrefix 字符串前缀，见 EmbedQuery。
func (e *Embedding) buildBody(texts []string, textType string) any {
	if e.protocol == config.ProtocolDashScope {
		return map[string]any{
			"model": e.model,
			"input": map[string]any{"texts": texts},
			"parameters": map[string]any{
				"text_type": textType,
			},
		}
	}
	return map[string]any{"model": e.model, "input": texts}
}

// parseVectors 按协议取向量并按下标回填。
//
// ⚠️ 两种协议的向量结构长得几乎一样，下标字段名却不同（index / text_index）。
// 正因为像，才必须分成两条分支写死——共用一个解析器就会把所有向量都挂到第 0 条上。
func (e *Embedding) parseVectors(parsed *embedResponse, n int) [][]float32 {
	out := make([][]float32, n)

	if e.protocol == config.ProtocolDashScope {
		if parsed.Output != nil {
			for _, d := range parsed.Output.Embeddings {
				if d.TextIndex >= 0 && d.TextIndex < n {
					out[d.TextIndex] = d.Embedding
				}
			}
		}
		return out
	}

	for _, d := range parsed.Data {
		if d.Index >= 0 && d.Index < n {
			out[d.Index] = d.Embedding
		}
	}
	return out
}

func (e *Embedding) embed(ctx context.Context, texts []string, textType string) ([][]float32, error) {
	payload, err := json.Marshal(e.buildBody(texts, textType))
	if err != nil {
		return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	body, status, err := postJSON(ctx, e.client, e.url, e.apiKey,
		payload, maxEmbedResponseBytes, apperrors.ErrEmbeddingFailed)
	if err != nil {
		return nil, err
	}

	var parsed embedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
			"embedding 响应不是合法 JSON，HTTP %d: %s", status, truncateBody(body, 512)))
	}
	if err := parsed.apiError.check(status, apperrors.CodeEmbeddingFail, "embedding", body); err != nil {
		return nil, err
	}

	out := e.parseVectors(&parsed, len(texts))

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
```

- [ ] **Step 5: 跑测试**

Run: `go test ./infrastructure/serviceimpl/ -run TestEmbedding_ -v`
Expected: 全部 PASS。（`go build ./...` 这时仍是红的——`register.go` 还在 `fx.Provide(NewEmbedding)`，Task 4 修。）

- [ ] **Step 6: 提交**

```bash
gofmt -l infrastructure/serviceimpl/
git add infrastructure/serviceimpl/embedding.go infrastructure/serviceimpl/embedding_test.go
git commit -m "$(cat <<'EOF'
feat(embedding): 构造改收合并后的参数，并加 dashscope 原生协议分支

一个实现，两处形状开关：分批、按下标回填、维度校验全部共用，
分叉的只有 buildBody（input 是裸数组还是 {texts}、要不要带 text_type）
和 parseVectors（从 data 还是 output.embeddings 取）。

两个协议的下标字段名不同——openai 是 index，dashscope 是 text_index——
所以解析器刻意不共用。形状太像，共用一个就会把所有向量挂到第 0 条上且不报错。

构造函数改收 config.EmbeddingParams：继承在外层合并时就填完了，
实现体里读到的 batchSize / timeoutMS 一定是有效值，不必再判 0。

Co-Authored-By: Claude Code <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: rerank 实现改收 Params + 加 DashScope 协议分支

同上，但 rerank 这边多一条硬规则：**分批时发给上游的 `top_n` 恒等于本批长度**。

**Files:**
- Modify: `infrastructure/serviceimpl/rerank.go`
- Modify: `infrastructure/serviceimpl/rerank_test.go`（改 `newTestRerank` 的字段名 + 新增 dashscope 用例）

**Interfaces:**
- Consumes: `config.RerankParams`、`config.ProtocolDashScope`（Task 1）
- Produces:
  - `newRerank(p config.RerankParams) *Rerank`（包内可见）
  - `(*Rerank) buildBody(query string, docs []string) any`
  - `(*Rerank) parseScores(parsed *rerankResponse, n int) []float64`
  - `rerankResponse` / `rerankResult` 两个类型
  - `Rerank` 结构体字段变成 `client / protocol / url / apiKey / model / batchSize / concurrency / defaultTopN`（`baseURL` 改名 `url`）

- [ ] **Step 1: 改测试辅助函数并加新用例（先写失败的测试）**

在 `infrastructure/serviceimpl/rerank_test.go` 里，把 `newTestRerank` 换成：

```go
// newTestRerank 用 mock server 构造 Rerank。
//
// url 存的是完整端点，所以要自己带上 /rerank 后缀——
// 这正是新配置形状的要求（不再由实现拼后缀）。
func newTestRerank(server *httptest.Server, batchSize, concurrency int) *Rerank {
	return &Rerank{
		client:      http.DefaultClient,
		url:         server.URL + "/rerank",
		model:       "test-model",
		batchSize:   batchSize,
		concurrency: concurrency,
		defaultTopN: 0,
	}
}
```

在文件末尾追加（`import` 里补上 `"sync"` 和 `"github.com/PycMono/FastRAG/infrastructure/config"`）：

```go
func TestRerank_BuildBody_OpenAI(t *testing.T) {
	body, err := json.Marshal(newTestRerank(nil, 1, 1).buildBody("q", []string{"d1", "d2"}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	if got["query"] != "q" {
		t.Errorf("query 应原样带上，得到 %v", got["query"])
	}
	docs, ok := got["documents"].([]any)
	if !ok || len(docs) != 2 {
		t.Fatalf("openai 的 documents 应是裸数组，得到 %#v", got["documents"])
	}
	if _, has := got["parameters"]; has {
		t.Error("openai 这一路不发 parameters，也就不该有 top_n")
	}
}

func TestRerank_BuildBody_DashScope(t *testing.T) {
	body, err := json.Marshal(newTestRerank(nil, 1, 1).buildBody("q", []string{"d1", "d2"}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	_ = got
	_ = body

	r := newTestRerank(nil, 1, 1)
	r.protocol = config.ProtocolDashScope
	body, err = json.Marshal(r.buildBody("q", []string{"d1", "d2", "d3"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	input, ok := got["input"].(map[string]any)
	if !ok {
		t.Fatalf("dashscope 的 input 应是 {query, documents}，得到 %#v", got["input"])
	}
	if input["query"] != "q" {
		t.Errorf("input.query 应原样带上，得到 %v", input["query"])
	}
	if docs, ok := input["documents"].([]any); !ok || len(docs) != 3 {
		t.Errorf("input.documents 应是 3 条，得到 %#v", input["documents"])
	}

	params, ok := got["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("dashscope 这一路必须带 parameters，得到 %#v", got["parameters"])
	}
	// top_n 必须是本批长度，不是业务上的 topN
	if params["top_n"] != float64(3) {
		t.Errorf("top_n 应等于本批文档数 3，得到 %v", params["top_n"])
	}
}

// 实测响应：{"output":{"results":[{"index":..,"relevance_score":..}]},...}
func TestRerank_ParseScores_DashScope(t *testing.T) {
	var parsed rerankResponse
	if err := json.Unmarshal([]byte(
		`{"output":{"results":[{"index":1,"relevance_score":0.1},`+
			`{"index":0,"relevance_score":0.9}]},"usage":{},"request_id":"x"}`,
	), &parsed); err != nil {
		t.Fatal(err)
	}

	r := newTestRerank(nil, 1, 1)
	r.protocol = config.ProtocolDashScope
	got := r.parseScores(&parsed, 2)

	if len(got) != 2 || got[0] != 0.9 || got[1] != 0.1 {
		t.Errorf("应按 index 回填 output.results，得到 %v", got)
	}
}

// §4.3 那条坑：dashscope 的 parameters.top_n 是"只返回前 N 条"，
// 写小了这一批剩下的候选就没分，批与批之间的分数不再可比，合并后顺序是乱的——
// 而且不报错。所以分批时它必须恒等于本批长度。
func TestRerank_DashScope_TopNEqualsBatchSize(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []int
	)

	server, _ := newRerankTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Parameters struct {
				TopN int `json:"top_n"`
			} `json:"parameters"`
			Input struct {
				Documents []string `json:"documents"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		seen = append(seen, req.Parameters.TopN)
		if req.Parameters.TopN != len(req.Input.Documents) {
			t.Errorf("top_n=%d 与本批 documents=%d 不等", req.Parameters.TopN, len(req.Input.Documents))
		}
		mu.Unlock()

		results := make([]map[string]any, len(req.Input.Documents))
		for i := range req.Input.Documents {
			results[i] = map[string]any{"index": i, "relevance_score": 1.0 - float64(i)*0.1}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{"results": results},
		})
	})
	defer server.Close()

	r := newTestRerank(server, 2, 1)
	r.protocol = config.ProtocolDashScope
	r.url = server.URL // dashscope 用完整端点，不拼后缀

	cands := make([]interfaces.RerankCandidate, 5)
	for i := range cands {
		cands[i] = interfaces.RerankCandidate{ChunkID: fmt.Sprintf("c%d", i), Content: "内容"}
	}

	order, err := r.Rerank(context.Background(), "q", cands, 0)
	if err != nil {
		t.Fatalf("Rerank 失败: %v", err)
	}
	if len(order) != 5 {
		t.Fatalf("应返回 5 个下标，得到 %d", len(order))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("5 条候选按 batchSize=2 应发 3 批，实际 %d 批", len(seen))
	}
	for _, n := range seen {
		if n != 2 && n != 1 { // 最后一批只剩 1 条
			t.Errorf("top_n 只能是本批长度（2 或 1），得到 %d", n)
		}
	}
}
```

- [ ] **Step 2: 跑测试，确认失败**

Run: `go test ./infrastructure/serviceimpl/ -run TestRerank_`
Expected: FAIL — 编译不过（`Rerank` 没有 `url` / `protocol` 字段，没有 `buildBody` / `parseScores`）。

- [ ] **Step 3: 改 rerank.go 的头部、构造与结构体**

把文件开头那段注释改成：

```go
// 本文件实现重排序：一个实现，两处形状开关。
//
// 分批、errgroup 并发、retry-go 重试、空内容沉底、按分数稳定排序全部共用；
// 随协议变的只有"请求体怎么拼"（buildBody）和"响应从哪取分数"（parseScores）。
//
//	openai    POST {url}  body {model, query, documents, return_documents}
//	                      响应 results[].{index, relevance_score}
//	dashscope POST {url}（原生 /api/v1/services/rerank/text-rerank/text-rerank）
//	                      body {model, input:{query, documents}, parameters:{top_n, return_documents}}
//	                      响应 output.results[].{index, relevance_score}
//
// ⚠️ dashscope 的 parameters.top_n 必须等于本批 documents 数量，见 buildBody 的注释。
```

把 `Rerank` 结构体与构造函数换成：

```go
// Rerank 重排序实现。
type Rerank struct {
	client      *http.Client
	protocol    string
	url         string // 完整端点，不再拼 /rerank 后缀
	apiKey      string
	model       string
	batchSize   int
	concurrency int
	defaultTopN int
}

// newRerank 从一份已合并好的参数构造实现。
//
// 只有注册表调它——"继承外层"在 Params() 里就填完了，这里不再判 0。
func newRerank(p config.RerankParams) *Rerank {
	return &Rerank{
		client:      &http.Client{Timeout: time.Duration(p.TimeoutMS) * time.Millisecond},
		protocol:    p.Protocol,
		url:         p.URL,
		apiKey:      p.APIKey,
		model:       p.Model,
		batchSize:   p.BatchSize,
		concurrency: p.Concurrency,
		defaultTopN: p.TopN,
	}
}
```

- [ ] **Step 4: 加 buildBody / parseScores，改 score**

`rerankResponse` 换成：

```go
// rerankResult 一条候选的分数。两个协议的这个结构完全一样，
// 区别只在外层：openai 挂在 results，dashscope 挂在 output.results。
type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// rerankResponse 两种协议的响应。按协议只认其中一个，另一个留空。
type rerankResponse struct {
	apiError
	Results []rerankResult `json:"results"`
	Output  *struct {
		Results []rerankResult `json:"results"`
	} `json:"output"`
}

// buildBody 按协议拼请求体。
//
// ⚠️ dashscope 的 parameters.top_n 是"只返回前 N 条"，而我们**需要每一批的完整
// 分数**才能合并排序。示例里写 top_n: 5 照抄进分批逻辑，每批就只有 5 条候选拿到
// 分数，其余全部落回 0 分，批与批之间的分数不再可比，合并后顺序是乱的——而且不报错。
// 所以这里恒等于本批长度，业务上的 topN 永远只在最后自己截断（设计文档 §4.3）。
func (r *Rerank) buildBody(query string, docs []string) any {
	if r.protocol == config.ProtocolDashScope {
		return map[string]any{
			"model": r.model,
			"input": map[string]any{
				"query":     query,
				"documents": docs,
			},
			"parameters": map[string]any{
				"top_n":            len(docs),
				"return_documents": false,
			},
		}
	}
	return map[string]any{
		"model":            r.model,
		"query":            query,
		"documents":        docs,
		"return_documents": false,
	}
}

// parseScores 按协议取分数并按 index 回填，缺的留 0。
func (r *Rerank) parseScores(parsed *rerankResponse, n int) []float64 {
	results := parsed.Results
	if r.protocol == config.ProtocolDashScope && parsed.Output != nil {
		results = parsed.Output.Results
	}

	scores := make([]float64, n)
	seen := make(map[int]struct{}, len(results))
	for _, res := range results {
		if res.Index < 0 || res.Index >= n {
			continue
		}
		if _, ok := seen[res.Index]; ok {
			continue
		}
		seen[res.Index] = struct{}{}
		scores[res.Index] = res.RelevanceScore
	}
	return scores
}
```

`score` 换成：

```go
// score 单次调用，返回每条文档的相关性分数（不含重试）。
func (r *Rerank) score(
	ctx context.Context,
	query string,
	docs []string,
) ([]float64, error) {
	payload, err := json.Marshal(r.buildBody(query, docs))
	if err != nil {
		return nil, apperrors.ErrRerankFailed.Wrap(err)
	}

	body, status, err := postJSON(ctx, r.client, r.url, r.apiKey,
		payload, maxRerankResponseBytes, apperrors.ErrRerankFailed)
	if err != nil {
		return nil, err
	}

	var parsed rerankResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, apperrors.NewSysError(apperrors.CodeRerankFail, fmt.Sprintf(
			"rerank 响应不是合法 JSON，HTTP %d: %s", status, truncateBody(body, 512)))
	}
	if err := parsed.apiError.check(status, apperrors.CodeRerankFail, "rerank", body); err != nil {
		return nil, err
	}

	return r.parseScores(&parsed, len(docs)), nil
}
```

- [ ] **Step 5: 跑测试**

Run: `go test ./infrastructure/serviceimpl/ -run TestRerank_ -v`
Expected: 全部 PASS，包括原有的分批、并发、重试、空内容沉底那几条。

- [ ] **Step 6: 提交**

```bash
gofmt -l infrastructure/serviceimpl/
git add infrastructure/serviceimpl/rerank.go infrastructure/serviceimpl/rerank_test.go
git commit -m "$(cat <<'EOF'
feat(rerank): 构造改收合并后的参数，并加 dashscope 原生协议分支

一个实现，两处形状开关：分批、并发、重试、空内容沉底、稳定排序全部共用，
分叉的只有 buildBody 和 parseScores。

dashscope 这一路的 parameters.top_n 恒等于本批 documents 数量，不是业务上的 topN：
它是"只返回前 N 条"，写小了这批剩下的候选就没分，批与批之间的分数不再可比，
合并后顺序是乱的，而且不报错。专门加了一条用例盯着这个不变量。

构造函数改收 config.RerankParams，继承在外层合并时就填完了。

Co-Authored-By: Claude Code <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 两个注册表 + 装配（本任务结束时全仓恢复可编译）

**Files:**
- Create: `infrastructure/serviceimpl/registry.go`
- Create: `infrastructure/serviceimpl/registry_test.go`
- Modify: `domain/interfaces/embedding.go`（追加 `IEmbeddingRegistry`）
- Modify: `domain/interfaces/rerank.go`（追加 `IRerankRegistry`）
- Modify: `infrastructure/serviceimpl/register.go`（换掉两个 `fx.Provide`，删掉 `NewRerank`）

**Interfaces:**
- Consumes: `config.EmbeddingConfig` / `config.RerankConfig` 的 `Params` 与 `Names`（Task 1），`newEmbedding` / `newRerank`（Task 2、3）
- Produces:
  - `interfaces.IEmbeddingRegistry{ Get(name string) (IEmbedding, error); Names() []string }`
  - `interfaces.IRerankRegistry{ Get(name string) (IRerank, error); Names() []string }`
  - `NewEmbeddingRegistry(conf *config.Config) (interfaces.IEmbeddingRegistry, error)`
  - `NewRerankRegistry(conf *config.Config) (interfaces.IRerankRegistry, error)`
  - `(*embeddingRegistry)` / `(*rerankRegistry)` 两个未导出的实现

- [ ] **Step 1: 写失败的测试**

创建 `infrastructure/serviceimpl/registry_test.go`：

```go
package serviceimpl

import (
	"strings"
	"testing"

	"github.com/PycMono/FastRAG/infrastructure/config"
)

// registryTestConfig 两家 embedding + 一家 rerank，够覆盖取名字的全部路径。
// 地址都指向 127.0.0.1 的无人端口：注册表只构造不连网，不该发出任何请求。
func registryTestConfig() *config.Config {
	return &config.Config{
		Embedding: config.EmbeddingConfig{
			Default:   "a",
			Dim:       8,
			BatchSize: 4,
			TimeoutMS: 1000,
			Models: map[string]config.EmbeddingEntry{
				"a": {URL: "http://127.0.0.1:1/v1/embeddings"},
				"b": {Protocol: config.ProtocolDashScope, URL: "http://127.0.0.1:2/embed"},
			},
		},
		Rerank: config.RerankConfig{
			Enabled:     true,
			Default:     "r1",
			BatchSize:   2,
			Concurrency: 1,
			TimeoutMS:   1000,
			Models: map[string]config.RerankEntry{
				"r1": {URL: "http://127.0.0.1:3/rerank"},
			},
		},
	}
}

func TestEmbeddingRegistry_GetByName(t *testing.T) {
	reg, err := NewEmbeddingRegistry(registryTestConfig())
	if err != nil {
		t.Fatal(err)
	}

	impl, err := reg.Get("b")
	if err != nil {
		t.Fatal(err)
	}
	// entry 没写 model，按约定取 map 的 key
	if impl.Model() != "b" {
		t.Errorf("应取到 b 那家，得到 %q", impl.Model())
	}
	if impl.Dim() != 8 {
		t.Errorf("dim 应继承外层，得到 %d", impl.Dim())
	}
}

func TestEmbeddingRegistry_EmptyNameFallsBackToDefault(t *testing.T) {
	reg, err := NewEmbeddingRegistry(registryTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	impl, err := reg.Get("")
	if err != nil {
		t.Fatalf("空名字应回落到 default: %v", err)
	}
	if impl.Model() != "a" {
		t.Errorf("default 是 a，得到 %q", impl.Model())
	}
}

func TestEmbeddingRegistry_UnknownNameIsParamError(t *testing.T) {
	reg, err := NewEmbeddingRegistry(registryTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Get("nope")
	if err == nil {
		t.Fatal("未知名字必须报错，绝不能悄悄用默认那家")
	}
	// 消息里要有可用名单，调用方才知道能填什么
	for _, want := range []string{"nope", "a", "b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错消息里应含 %q，得到 %v", want, err)
		}
	}
}

func TestEmbeddingRegistry_Names(t *testing.T) {
	reg, err := NewEmbeddingRegistry(registryTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	names := reg.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("Names 应排序且完整，得到 %v", names)
	}
}

// enabled=false 时保持"没配 rerank 也能正常启动和检索"的语义：
// 任何名字都拿到空实现，且永不出错。
func TestRerankRegistry_DisabledReturnsNopForAnyName(t *testing.T) {
	conf := registryTestConfig()
	conf.Rerank.Enabled = false
	conf.Rerank.Models = nil
	conf.Rerank.Default = ""

	reg, err := NewRerankRegistry(conf)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "r1", "随便什么名字"} {
		impl, err := reg.Get(name)
		if err != nil {
			t.Fatalf("enabled=false 时不该出错，name=%q 得到 %v", name, err)
		}
		if _, ok := impl.(nopRerank); !ok {
			t.Errorf("enabled=false 时应返回空实现，name=%q 得到 %T", name, impl)
		}
	}
}

func TestRerankRegistry_EnabledResolvesByName(t *testing.T) {
	reg, err := NewRerankRegistry(registryTestConfig())
	if err != nil {
		t.Fatal(err)
	}

	impl, err := reg.Get("")
	if err != nil {
		t.Fatalf("空名字应回落到 default: %v", err)
	}
	if _, ok := impl.(*Rerank); !ok {
		t.Errorf("enabled=true 且名字合法时应返回真实实现，得到 %T", impl)
	}

	if _, err := reg.Get("nope"); err == nil {
		t.Fatal("enabled=true 时未知名字必须报错")
	}
}
```

- [ ] **Step 2: 跑测试，确认失败**

Run: `go test ./infrastructure/serviceimpl/ -run Registry`
Expected: FAIL — `undefined: NewEmbeddingRegistry`、`undefined: IEmbeddingRegistry`。

- [ ] **Step 3: 加两个端口**

在 `domain/interfaces/embedding.go` 末尾追加：

```go
// IEmbeddingRegistry 按名字取向量化实现。
//
// 名字来自请求体，取不到时返回参数错误——绝不能悄悄回落到默认那家：
// 调用方写错了名字却拿到一份"看起来正常"的结果，比直接报错难查得多。
type IEmbeddingRegistry interface {
	Get(name string) (IEmbedding, error) // name 为空 → 配置里的 default
	Names() []string                     // 报错时列出来，让调用方知道有哪些可用
}
```

在 `domain/interfaces/rerank.go` 末尾追加：

```go
// IRerankRegistry 按名字取重排序实现。
//
// 与 IEmbeddingRegistry 的差别只有一处：配置里 enabled=false 时，
// 对**任何**名字都返回空实现且永不出错——保持"没配 rerank 服务也能正常启动
// 和检索"这个既有语义。
type IRerankRegistry interface {
	Get(name string) (IRerank, error)
	Names() []string
}
```

- [ ] **Step 4: 写 registry.go**

创建 `infrastructure/serviceimpl/registry.go`：

```go
// 本文件是模型注册表：把配置里的一组服务商造成一组实现，按名字取用。
//
// 两个注册表都在**启动时一次性建好**，之后只读——map 建好之后再没写过，
// 天然并发安全，不需要锁（批量导入的 4 个 goroutine 各自 Get 互不干扰）。

package serviceimpl

import (
	"fmt"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// embeddingRegistry 按名字取向量化实现。
type embeddingRegistry struct {
	conf   config.EmbeddingConfig
	byName map[string]interfaces.IEmbedding
}

// NewEmbeddingRegistry 启动时把每个 entry 造一个实现塞进 map。
//
// 不连网、不懒加载：配置错在这一步就暴露，而不是等某次导入跑到一半才炸。
func NewEmbeddingRegistry(conf *config.Config) (interfaces.IEmbeddingRegistry, error) {
	c := conf.Embedding

	r := &embeddingRegistry{
		conf:   c,
		byName: make(map[string]interfaces.IEmbedding, len(c.Models)),
	}
	for _, name := range c.Names() {
		p, err := c.Params(name)
		if err != nil {
			return nil, err
		}
		r.byName[name] = newEmbedding(p)
	}
	return r, nil
}

func (r *embeddingRegistry) Get(name string) (interfaces.IEmbedding, error) {
	if name == "" {
		name = r.conf.Default
	}
	impl, ok := r.byName[name]
	if !ok {
		return nil, apperrors.NewParamError(fmt.Sprintf(
			"没有名为 %q 的 embedding 模型。可用：%v", name, r.conf.Names()))
	}
	return impl, nil
}

func (r *embeddingRegistry) Names() []string { return r.conf.Names() }

// rerankRegistry 按名字取重排序实现。
type rerankRegistry struct {
	conf   config.RerankConfig
	byName map[string]interfaces.IRerank
}

// NewRerankRegistry 启动时把每个 entry 造一个实现塞进 map。
//
// enabled=false 时返回一个 byName 为空的注册表，它的 Get 对任何名字都给空实现——
// "这个实例要不要做重排"是部署决定，与"有哪几家可选"无关，两者不必合并（§10）。
func NewRerankRegistry(conf *config.Config) (interfaces.IRerankRegistry, error) {
	c := conf.Rerank

	r := &rerankRegistry{
		conf:   c,
		byName: make(map[string]interfaces.IRerank, len(c.Models)),
	}
	if !c.Enabled {
		return r, nil
	}

	for _, name := range c.Names() {
		p, err := c.Params(name)
		if err != nil {
			return nil, err
		}
		r.byName[name] = newRerank(p)
	}
	return r, nil
}

func (r *rerankRegistry) Get(name string) (interfaces.IRerank, error) {
	if !r.conf.Enabled {
		return nopRerank{}, nil
	}
	if name == "" {
		name = r.conf.Default
	}
	impl, ok := r.byName[name]
	if !ok {
		return nil, apperrors.NewParamError(fmt.Sprintf(
			"没有名为 %q 的 rerank 模型。可用：%v", name, r.conf.Names()))
	}
	return impl, nil
}

func (r *rerankRegistry) Names() []string { return r.conf.Names() }
```

- [ ] **Step 5: 改 register.go**

把 `infrastructure/serviceimpl/register.go` 整个换成：

```go
package serviceimpl

import (
	"context"

	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"go.uber.org/fx"
)

// Register 注册 serviceimpl 层组件。
//
// embedding 与 rerank 都注册成"注册表"而不是单个实现：配置里可以同时存在
// 多组服务商，应用层在请求入口按名字取用。
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

	fx.Provide(NewEmbeddingRegistry),
	fx.Provide(NewRerankRegistry),
)

// nopRerank 未启用 rerank 时的空实现：原样返回候选顺序。
type nopRerank struct{}

func (nopRerank) Rerank(ctx context.Context, query string, cands []interfaces.RerankCandidate, topN int) ([]int, error) {
	out := make([]int, len(cands))
	for i := range out {
		out[i] = i
	}
	return out, nil
}

// 编译期断言：实现必须满足端口。
var (
	_ interfaces.IEmbedding         = (*Embedding)(nil)
	_ interfaces.IEmbeddingRegistry = (*embeddingRegistry)(nil)
	_ interfaces.IRerank            = (*Rerank)(nil)
	_ interfaces.IRerank            = (*nopRerank)(nil)
	_ interfaces.IRerankRegistry    = (*rerankRegistry)(nil)
)
```

- [ ] **Step 6: 跑测试，确认全仓恢复绿**

Run:
```bash
go build ./... && go vet ./... && go test ./...
```
Expected:
- `go build` 无输出。
- `go test` 全部 ok，包括 `infrastructure/serviceimpl`（含新的注册表用例与协议用例）。

如果 `application/service/*` 仍红，那是 Task 5、6 的范围——但本任务只要求 `serviceimpl` 与 `config` 两个包能编、能测。**确认此时 `go build ./application/...` 的报错只剩"服务构造函数收的是 IEmbedding 而不是注册表"这一类。**

- [ ] **Step 7: 提交**

```bash
gofmt -l infrastructure/serviceimpl/ domain/interfaces/
git add domain/interfaces/embedding.go domain/interfaces/rerank.go \
        infrastructure/serviceimpl/registry.go infrastructure/serviceimpl/registry_test.go \
        infrastructure/serviceimpl/register.go
git commit -m "$(cat <<'EOF'
feat: embedding/rerank 换成按名字取用的注册表

两个注册表都启动时一次性建好：把每个 entry 造成一个实现塞进 map，
之后只读、不加锁。不连网、不懒加载——配置错在启动时就暴露。

Get("") 回落到配置的 default；名字不在表里返回参数错误并列出可用名单，
绝不悄悄回落到默认那家。

rerank 的 enabled=false 单独处理：对任何名字都返回空实现且永不出错，
保持"没配 rerank 服务也能正常启动和检索"这个既有语义。

Co-Authored-By: Claude Code <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: 导入侧——DTO 加 model 字段，按名字取实现

**Files:**
- Modify: `common/dto/doc.go`
- Modify: `application/service/ingest/service.go`
- Create: `application/service/ingest/service_test.go`

**Interfaces:**
- Consumes: `interfaces.IEmbeddingRegistry`（Task 4）
- Produces:
  - `dto.DocIngestDTO.Model string`
  - `ingest.Service` 的字段与构造参数从 `embedder interfaces.IEmbedding` 换成 `embeddings interfaces.IEmbeddingRegistry`
  - `(*Service) embedChunks(ctx context.Context, e interfaces.IEmbedding, chunks entity.Chunks) (titleVecs, contentVecs [][]float32, err error)`

- [ ] **Step 1: 写失败的测试**

创建 `application/service/ingest/service_test.go`：

```go
package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
)

// stubKBRepo 只实现 LoadByNo，其余方法靠内嵌的 nil 接口兜着——
// 一旦被调到就 panic，正好说明用例走到了不该走的地方。
type stubKBRepo struct {
	repository.IKnowledgeBaseRepo
	kb *entity.KnowledgeBase
}

func (s stubKBRepo) LoadByNo(ctx context.Context, no, account string) (*entity.KnowledgeBase, error) {
	return s.kb, nil
}

// errEmbeddingRegistry 任何名字都取不到，模拟调用方传了个不存在的 model。
type errEmbeddingRegistry struct{ err error }

func (r errEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) { return nil, r.err }
func (r errEmbeddingRegistry) Names() []string                                 { return []string{"bge-m3"} }

// 名字写错必须**在写任何东西之前**就返回：不能先切片、先算向量、先写 ES，
// 白跑一大圈才告诉调用方"这个名字不存在"。
func TestIngest_UnknownModel_FailsBeforeAnyWrite(t *testing.T) {
	svc := NewService(
		stubKBRepo{kb: &entity.KnowledgeBase{}},
		nil, // docRepo：不该被调到
		nil, // store：不该被调到
		errEmbeddingRegistry{err: errors.New(`没有名为 "nope" 的 embedding 模型。可用：[bge-m3]`)},
		nil, // splitter
		nil, // idGen
		nil, // tm
	)

	_, err := svc.Ingest(context.Background(), &dto.DocIngestDTO{
		Account: "demo",
		KBNo:    "demo-kb",
		DocName: "a.md",
		Format:  "text",
		Content: "正文",
		Model:   "nope",
	})
	if err == nil {
		t.Fatal("未知 model 必须报错，不能悄悄用默认那家")
	}
}
```

- [ ] **Step 2: 跑测试，确认失败**

Run: `go test ./application/service/ingest/`
Expected: FAIL — `unknown field 'Model' in struct literal of type dto.DocIngestDTO`、`cannot use errEmbeddingRegistry as interfaces.IEmbedding`。

- [ ] **Step 3: 给 DTO 加字段**

在 `common/dto/doc.go` 的 `DocIngestDTO` 里，`SplitOptions` 之后插入：

```go
	// Model 这批数据用哪家算向量，取 config.embedding.models 里的 key。
	// 留空用配置的 default。名字不存在时直接返回参数错误，不回落。
	//
	// 换模型**不会**让旧数据变得不可用，但也**不会**保证新旧向量在同一个
	// 空间里——维度不同会被 ES 拦下，维度相同但向量空间不同则是静默的
	// 噪声排序。这是明确接受的代价（设计文档 §2、§8）。
	Model string `json:"model" binding:"omitempty,max=64"`
```

- [ ] **Step 4: 改 ingest/service.go**

把 `Service` 的字段与构造函数换成：

```go
// Service 文档导入应用服务。
type Service struct {
	kbRepo     repository.IKnowledgeBaseRepo
	docRepo    repository.IKnowledgeDocRepo
	store      repository.IVectorStore
	embeddings interfaces.IEmbeddingRegistry
	splitter   *domainservice.Splitter
	idGen      repository.IIDService
	tm         transaction.Manager
}

func NewService(
	kbRepo repository.IKnowledgeBaseRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embeddings interfaces.IEmbeddingRegistry,
	splitter *domainservice.Splitter,
	idGen repository.IIDService,
	tm transaction.Manager,
) *Service {
	return &Service{
		kbRepo:     kbRepo,
		docRepo:    docRepo,
		store:      store,
		embeddings: embeddings,
		splitter:   splitter,
		idGen:      idGen,
		tm:         tm,
	}
}
```

在 `Ingest` 里，① 定位 KB 之后、② 切片之前插入：

```go
	// ② 定这次用哪家算向量。
	//
	//    放在切片和任何写操作之前是刻意的：名字写错在这里就返回，
	//    不会白切一遍、白算一遍、白写一次 ES 才告诉调用方名字不存在。
	//
	//    解出来的实现是**逐请求**传给 embedChunks 的，不能存回 Service 的字段——
	//    Service 是单例，把逐请求的东西写进去就是数据竞争。BatchIngest 的
	//    4 个 goroutine 各自解各自的，互不影响。
	embedder, err := s.embeddings.Get(in.Model)
	if err != nil {
		return nil, err
	}
```

后面原来那行 `// ② 切片参数：...` 和 `// ③ 向量化。...` 的序号顺延改成 ③、④，`④ 定 doc_id` 起依次 +1。

把 `titleVecs, contentVecs, err := s.embedChunks(ctx, chunks)` 换成：

```go
	titleVecs, contentVecs, err := s.embedChunks(ctx, embedder, chunks)
```

把 `embedChunks` 换成：

```go
// embedChunks 用指定实现算两路向量。
//
// 实现是参数而不是 Service 的字段：Service 是单例，逐请求的东西不能往里写。
func (s *Service) embedChunks(
	ctx context.Context, e interfaces.IEmbedding, chunks entity.Chunks,
) (titleVecs, contentVecs [][]float32, err error) {
	titleVecs, err = e.EmbedDocs(ctx, chunks.Titles())
	if err != nil {
		return nil, nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	contentVecs, err = e.EmbedDocs(ctx, factory.EmbeddingTexts(chunks))
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
```

- [ ] **Step 5: 跑测试**

Run: `go build ./... && go test ./application/service/ingest/ -v`
Expected: `go build` 仍红在 `application/service/search`（Task 6 修），但 `go test ./application/service/ingest/` PASS。

- [ ] **Step 6: 提交**

```bash
gofmt -l common/dto/ application/service/ingest/
git add common/dto/doc.go application/service/ingest/service.go application/service/ingest/service_test.go
git commit -m "$(cat <<'EOF'
feat(ingest): 导入可按 model 选向量化服务商

请求体加 model 字段，取 config.embedding.models 的 key，留空用 default。

解析放在切片和任何写操作之前：名字写错立刻返回，不会白切一遍、
白算一遍、白写一次 ES。解出来的实现逐请求传给 embedChunks 而不是存回
Service 的字段——Service 是单例，写进去就是数据竞争。

Co-Authored-By: Claude Code <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: 检索侧——DTO 加两个字段，两处按名字取实现

**Files:**
- Modify: `common/dto/search.go`
- Modify: `application/service/search/service.go`
- Modify: `application/service/search/service_test.go`（两个 mock 与 `newSearchServiceForTest` 跟着改）

**Interfaces:**
- Consumes: `interfaces.IEmbeddingRegistry` / `interfaces.IRerankRegistry`（Task 4）
- Produces:
  - `dto.SearchDTO.EmbedModel string`、`dto.SearchDTO.RerankModel string`
  - `search.Service` 的字段与构造参数从 `embedder interfaces.IEmbedding` / `rerank interfaces.IRerank` 换成 `embeddings interfaces.IEmbeddingRegistry` / `reranks interfaces.IRerankRegistry`
  - `(*Service) rerankItems(ctx context.Context, reranker interfaces.IRerank, query string, items []*vo.SearchItemVO) ([]*vo.SearchItemVO, error)`

- [ ] **Step 1: 改测试（先写失败的测试）**

在 `application/service/search/service_test.go` 里，把 `mockEmbedding` / `mockRerank` 保留不动，追加两个注册表 mock：

```go
// mockEmbeddingRegistry 只有一个名字，且总是能取到——检索用例不关心取名字这件事。
type mockEmbeddingRegistry struct{ impl interfaces.IEmbedding }

func (m mockEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) { return m.impl, nil }
func (m mockEmbeddingRegistry) Names() []string                                 { return []string{"mock"} }

// mockRerankRegistry 同上。
type mockRerankRegistry struct{ impl interfaces.IRerank }

func (m mockRerankRegistry) Get(name string) (interfaces.IRerank, error) { return m.impl, nil }
func (m mockRerankRegistry) Names() []string                              { return []string{"mock"} }
```

把 `newSearchServiceForTest` 的签名与实现改成（参数从两个实现换成两个注册表，内部包一层）：

```go
func newSearchServiceForTest(
	kbRepo repository.IKnowledgeBaseRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embedder interfaces.IEmbedding,
	rerank interfaces.IRerank,
) *Service {
	return NewService(kbRepo, docRepo, store,
		mockEmbeddingRegistry{impl: embedder},
		mockRerankRegistry{impl: rerank},
		SearchTuning{
			BM25Top:       50,
			KNNTops:       50,
			NumCandidates: 200,
			RankConstant:  60,
			DenseWeight:   0.5,
		})
}
```

追加一条用例，盯住"名字写错要在这儿就报，不白跑一次 ES"：

```go
// 名字写错必须**在花掉一次 ES 往返之前**就返回。
func TestService_Search_UnknownModel_FailsBeforeES(t *testing.T) {
	kbRepo := &mockKBRepo{kbs: entity.KnowledgeBases{defaultKB()}}
	store := &mockVectorStore{}
	svc := NewService(kbRepo, &mockDocRepo{}, store, errEmbeddingRegistry{}, mockRerankRegistry{})

	_, err := svc.Search(context.Background(), &dto.SearchDTO{
		Account:    "demo",
		KBNos:      []string{"kb1"},
		Query:      "问题",
		EmbedModel: "nope",
	})
	if err == nil {
		t.Fatal("未知 embed_model 必须报错")
	}
	if store.searched {
		t.Error("名字解析失败时不该再发 ES 请求")
	}
}
```

（`errEmbeddingRegistry` 就定义在同一个文件里：）

```go
// errEmbeddingRegistry 任何名字都取不到。
type errEmbeddingRegistry struct{}

func (errEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) {
	return nil, apperrors.NewParamError("没有名为 " + name + " 的 embedding 模型")
}
func (errEmbeddingRegistry) Names() []string { return []string{"bge-m3"} }
```

`mockVectorStore` 需要一个 `searched bool` 字段，在 `Search` 方法里置 true——照着现有 `mockRerank.called` 的写法加。

- [ ] **Step 2: 跑测试，确认失败**

Run: `go test ./application/service/search/`
Expected: FAIL — `undefined: errEmbeddingRegistry` 之外，`NewService` 参数类型不匹配、`dto.SearchDTO` 没有 `EmbedModel`。

- [ ] **Step 3: 给 DTO 加两个字段**

在 `common/dto/search.go` 的 `SearchDTO` 里，`RerankSwitch` 之前插入：

```go
	// EmbedModel 查询向量用哪家算，取 config.embedding.models 里的 key。
	// 留空用配置的 default。必须与库里那批数据的生成方一致，否则检索会退化成
	// 噪声排序且不报错（设计文档 §2、§8）。
	EmbedModel string `json:"embed_model" binding:"omitempty,max=64"`

	// RerankModel 精排用哪家，取 config.rerank.models 里的 key。留空用 default。
	// 精排只在查询期发生、不落库，所以换它随时都安全。
	RerankModel string `json:"rerank_model" binding:"omitempty,max=64"`
```

- [ ] **Step 4: 改 search/service.go**

字段与构造函数：

```go
// Service 检索应用服务。
type Service struct {
	kbRepo   repository.IKnowledgeBaseRepo
	docRepo  repository.IKnowledgeDocRepo
	store    repository.IVectorStore
	embeddings interfaces.IEmbeddingRegistry
	reranks    interfaces.IRerankRegistry
	tuning   SearchTuning
}

func NewService(
	kbRepo repository.IKnowledgeBaseRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embeddings interfaces.IEmbeddingRegistry,
	reranks interfaces.IRerankRegistry,
	tuning SearchTuning,
) *Service {
	return &Service{
		kbRepo:     kbRepo,
		docRepo:    docRepo,
		store:      store,
		embeddings: embeddings,
		reranks:    reranks,
		tuning:     tuning.WithDefaults(),
	}
}
```

在 `Search` 里，`resolveOptions` 之后、① 之前插入：

```go
	// ⓪ 先把两个名字解出来。
	//
	//    放在最前面是刻意的：拼错的名字应当**立刻**报错，而不是先花一次 ES
	//    往返再报。代价是 dense_weight=0（压根不走向量路）时也会校验
	//    embed_model——这是想要的，传了一个用不上的错名字同样应该被指出。
	//
	//    rerank 那边 enabled=false 时注册表对任何名字都给空实现，不会在这里挡人。
	embedder, err := s.embeddings.Get(in.EmbedModel)
	if err != nil {
		return nil, err
	}
	reranker, err := s.reranks.Get(in.RerankModel)
	if err != nil {
		return nil, err
	}
```

步骤 ② 的 `s.embedder.EmbedQuery` 改成 `embedder.EmbedQuery`。

步骤 ⑥ 改成：

```go
	// ⑥ 可选重排。失败时退回融合原序，不阻断检索。
	//    注册表不会返回 nil，所以判据里不再有 s.rerank != nil。
	if opts.Rerank && len(items) > 1 {
		reranked, err := s.rerankItems(ctx, reranker, opts.Query, items)
		if err != nil {
			logsdk.Warn(ctx, "重排失败，退回融合原序", logsdk.Err(err))
		} else {
			items = reranked
		}
	}
```

`rerankItems` 改成：

```go
func (s *Service) rerankItems(
	ctx context.Context,
	reranker interfaces.IRerank,
	query string,
	items []*vo.SearchItemVO,
) ([]*vo.SearchItemVO, error) {
	cands := make([]interfaces.RerankCandidate, len(items))
	for i, it := range items {
		cands[i] = interfaces.RerankCandidate{
			ChunkID:     it.ChunkID,
			Title:       it.Title,
			HeadingPath: it.HeadingPath,
			Content:     it.Content,
		}
	}

	order, err := reranker.Rerank(ctx, query, cands, len(items))
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

- [ ] **Step 5: 跑测试，确认全仓绿**

Run:
```bash
go build ./... && go vet ./... && go test ./...
```
Expected: `go build`、`go vet` 无输出；`go test` 全部 ok。

- [ ] **Step 6: 提交**

```bash
gofmt -l common/dto/ application/service/search/
git add common/dto/search.go application/service/search/service.go application/service/search/service_test.go
git commit -m "$(cat <<'EOF'
feat(search): 检索可按 embed_model / rerank_model 分别选服务商

两个名字在 Search 最前面就解出来，拼错的立刻报错而不是先花一次 ES 往返。
代价是 dense_weight=0 时也会校验 embed_model——这是想要的，传了一个用不上的
错名字同样应该被指出。

重排判据去掉了 s.rerank != nil：注册表不会返回 nil，enabled=false 时给的是
空实现。rerank 失败仍然只记警告、退回融合原序。

Co-Authored-By: Claude Code <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: 文档收尾

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-10-09-model-registry-design.md`（删掉 §11 里指向已删文件的那条）

**Interfaces:**
- Consumes: 全部前置任务
- Produces: 无

- [ ] **Step 1: 改 README 的 API 示例**

把「## API 示例」里导入那条 curl 换成：

```bash
# 导入文档。format 取 markdown / text / chunks；
# refresh=true 表示写完立刻刷索引、返回即可搜（默认 false，按 30s 周期）
# model 指定这篇的向量用哪家算（config.embedding.models 的 key），省略则用 default
curl -X POST http://localhost:8080/api/v1/docs \
  -H 'Content-Type: application/json' \
  -d '{"account":"demo","kb_no":"demo-kb","doc_name":"产品手册.md","format":"text",
       "model":"bge-m3","content":"……正文……","refresh":true}'
```

把检索那条 curl 换成：

```bash
# 检索。dense_weight 省略则用配置默认值；<=0.01 只走 BM25，>=0.99 只走 kNN
# rerank_switch=true 且 config.rerank.enabled=true 时会调用精排；
# retrieve_count 控制精排前召回多少候选（默认 limit*2，上限 200）
# embed_model / rerank_model 分别选两路用哪家，省略则各用各的 default；
# 名字不存在返回 code=10001 并列出可用的名字，不会悄悄回落到默认那家
curl -X POST http://localhost:8080/api/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"account":"demo","kb_nos":["demo-kb"],"query":"如何配置",
       "limit":5,"dense_weight":0.5,"rerank_switch":false,
       "embed_model":"bge-m3","rerank_model":"qwen3-rerank"}'
```

- [ ] **Step 2: 在 README 里加一节配置说明**

在「## 常用命令」之后、「## 统一响应格式」之前插入：

```markdown
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
```

- [ ] **Step 3: 删掉设计文档里那条失效引用**

`docs/superpowers/specs/2026-10-09-model-registry-design.md` §11 的清单里，第 8 条指的是 `2026-10-09-rerank-implementation-plan.md`，该文件已不存在。把这一条整个删掉（连同它的编号），并把后面的 ⚠️ 段落保留。

- [ ] **Step 4: 全量复查**

Run:
```bash
go build ./... && go vet ./... && go test ./... && gofmt -l .
```
Expected: build / vet 无输出，test 全 ok，`gofmt -l` 只列出改动前就已经脏的那个文件（如果本来就有的话）。

- [ ] **Step 5: 真栈 A/B（人工，需要跑起来的 MySQL / Redis / ES / Ollama）**

1. 把 `config.example.json` 复制成 `config.json`，按设计文档 §3 的样子补上第二家 embedding 和一家 rerank。
2. 起服务，用两个不同的 `model` 各导一篇文档、各搜一次，确认走的是对应那家（看上游日志或返回内容的差异）。
3. 省略 `model` / `embed_model` / `rerank_model`，确认走的是各自的 `default`。
4. 传一个不存在的名字，确认返回 `code=10001` 且消息里列出可用名字。
5. 把某家 embedding 的 `dim` 外层值改错（比如 512），确认服务**起不来**且报的是配置校验错误。

- [ ] **Step 6: 提交**

```bash
git add README.md docs/superpowers/specs/2026-10-09-model-registry-design.md
git commit -m "$(cat <<'EOF'
docs: README 补模型配置一节与 model / embed_model / rerank_model 用法

新增「模型配置」小节：两层结构长什么样、加一家要写几行、dim 与 top_n
为什么只在外层，以及换 embedding 服务商那个静默退化的代价——
维度不同会被 ES 拦下，维度相同但向量空间不同则是噪声排序且不报错。

设计文档 §11 里指向已删除的 rerank 实现计划那条一并去掉。

Co-Authored-By: Claude Code <noreply@anthropic.com>
EOF
)"
```

---

## 自查记录

- **spec §1–§3（两层配置形状）** → Task 1。
- **spec §4（协议：一个实现两处开关 / top_n 规则 / text_type）** → Task 2、Task 3。
- **spec §5（两个注册表端口）** → Task 4。
- **spec §6（应用层改造：search / ingest / DTO）** → Task 5、Task 6。
- **spec §7（启动校验）** → Task 1 Step 3 的 `validate`，用例在 Task 1 Step 1。
- **spec §8（已接受的代价）** → Task 7 Step 2 写进 README；不做任何代码层面的处理，这是 spec 的明确决定。
- **spec §9（验证）** → 单测在 Task 1/2/3/4/5/6；真栈 A/B 在 Task 7 Step 5；回归在每个任务的收尾。
- **spec §10（不做的事）** → 全部体现在"没有任务"上；另外 `IEmbedding.Dim()` / `Model()` 目前只有测试在用，本次**不动**它们（不在 spec 范围内，属于无关重构）。
- **spec §11（落地顺序）** → 七个任务的顺序与之一致，只在两处收窄：把 `README.md` 与文档修正合并成 Task 7；原第 8 条引用的文件已不存在，删除。
