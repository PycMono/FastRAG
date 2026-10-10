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
