package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// config.example.json 必须始终是一份能过校验的配置。
//
// 它是新用户照抄的模板：一旦漂了，第一次启动就失败，而失败信息看起来
// 像是"你自己配错了"，排查方向完全被带偏。
func TestConfig_ExampleFileIsValid(t *testing.T) {
	data, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatalf("读不到示例配置: %v", err)
	}

	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		t.Fatalf("示例配置不是合法 JSON: %v", err)
	}
	if err := c.validate(); err != nil {
		t.Fatalf("示例配置过不了自己的校验: %v", err)
	}
}

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
