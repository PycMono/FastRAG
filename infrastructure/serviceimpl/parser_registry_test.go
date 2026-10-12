package serviceimpl

import (
	"testing"

	"github.com/PycMono/FastRAG/infrastructure/config"
)

// Get 给的就是 default 指的那家。
func TestParserRegistry_ReturnsDefault(t *testing.T) {
	conf := &config.Config{Parser: config.ParserConfig{
		Default: "docling",
		Backends: map[string]config.ParserEntry{
			"docling": {Type: config.ParserTypeDocling, BaseURL: "http://127.0.0.1:5001", TimeoutMS: 1000},
		},
	}}

	r, err := NewParserRegistry(conf)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Get().Name(); got != "docling" {
		t.Errorf("应取 default 那家，得到 %q", got)
	}
}

// 只造 default 那一家：非 default 的 entry 走 validate 的字段校验，但不构造。
//
// 这条是给 config.example.json 兜底的：示例里 tencent 条目的密钥是空的，
// 而构造函数会拒绝空密钥——若启动期把每个 entry 都构造一遍，「cp 示例再启动」
// 就直接起不来，示例文件也就失去了它的用途。
// 在 P0（还没有 tencent 的 case）这条同样成立，且失败得更早。
func TestParserRegistry_BuildsOnlyDefault(t *testing.T) {
	conf := &config.Config{Parser: config.ParserConfig{
		Default: "docling",
		Backends: map[string]config.ParserEntry{
			"docling": {Type: config.ParserTypeDocling, BaseURL: "http://127.0.0.1:5001", TimeoutMS: 1000},
			"other":   {Type: "tencent", TimeoutMS: 1000}, // 非 default：不该参与构造
		},
	}}

	r, err := NewParserRegistry(conf)
	if err != nil {
		t.Fatalf("非 default 的条目不该参与构造，得到 %v", err)
	}
	if got := r.Get().Name(); got != "docling" {
		t.Errorf("应取 default 那家，得到 %q", got)
	}
}

// 端点必须拼成 base_url + /v1/convert/source，且 base_url 结尾多写一个 / 也要能忍。
func TestParserRegistry_GetReturnsDoclingWithJoinedURL(t *testing.T) {
	conf := &config.Config{Parser: config.ParserConfig{
		Default: "d",
		Backends: map[string]config.ParserEntry{
			"d": {Type: config.ParserTypeDocling, BaseURL: "http://127.0.0.1:5001/", TimeoutMS: 1000},
		},
	}}

	r, err := NewParserRegistry(conf)
	if err != nil {
		t.Fatal(err)
	}
	dp, ok := r.Get().(*doclingParser)
	if !ok {
		t.Fatalf("期望 *doclingParser，得到 %T", r.Get())
	}
	if dp.url != "http://127.0.0.1:5001/v1/convert/source" {
		t.Errorf("端点应去掉多余的 / 再接后缀，得到 %q", dp.url)
	}
	if dp.Name() != "d" {
		t.Errorf("Name 应是配置里的 key，得到 %q", dp.Name())
	}
}

// default 指向一个没有实现的 type：注册表要在启动期就拒掉，不能造出个空壳。
func TestParserRegistry_UnknownType_FailsAtStartup(t *testing.T) {
	conf := &config.Config{Parser: config.ParserConfig{
		Default: "odd",
		Backends: map[string]config.ParserEntry{
			"odd": {Type: "odd", TimeoutMS: 1000},
		},
	}}

	if _, err := NewParserRegistry(conf); err == nil {
		t.Fatal("没有实现的 type 必须让启动失败")
	}
}

// default 指向 tencent：注册表要真的造出 *tencentParser，而不是落到 default 兜底。
// 这正是「配置选后端」这件事本身的钉子——少了它，删掉 tencent 分支也不会有测试红。
func TestParserRegistry_ReturnsTencentDefault(t *testing.T) {
	conf := &config.Config{Parser: config.ParserConfig{
		Default: "tx",
		Backends: map[string]config.ParserEntry{
			"tx": {
				Type:      config.ParserTypeTencent,
				SecretID:  "id",
				SecretKey: "key",
				Region:    "ap-guangzhou",
				TimeoutMS: 1000,
			},
		},
	}}

	r, err := NewParserRegistry(conf)
	if err != nil {
		t.Fatal(err)
	}
	tp, ok := r.Get().(*tencentParser)
	if !ok {
		t.Fatalf("期望 *tencentParser，得到 %T", r.Get())
	}
	if tp.Name() != "tx" {
		t.Errorf("Name 应是配置里的 key，得到 %q", tp.Name())
	}
}
