package serviceimpl

import (
	"strings"
	"testing"

	"github.com/PycMono/FastRAG/infrastructure/config"
)

func TestParserRegistry_DefaultAndUnknown(t *testing.T) {
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
	if _, err := r.Get(""); err != nil {
		t.Errorf("空名字应回落到 default，得到 %v", err)
	}

	_, err = r.Get("nope")
	if err == nil {
		t.Fatal("未知名字必须报错，不能悄悄用默认那家")
	}
	if !strings.Contains(err.Error(), "docling") {
		t.Errorf("报错消息里应列出可用名字，得到 %v", err)
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
	p, err := r.Get("d")
	if err != nil {
		t.Fatal(err)
	}
	dp, ok := p.(*doclingParser)
	if !ok {
		t.Fatalf("期望 *doclingParser，得到 %T", p)
	}
	if dp.url != "http://127.0.0.1:5001/v1/convert/source" {
		t.Errorf("端点应去掉多余的 / 再接后缀，得到 %q", dp.url)
	}
	if dp.Name() != "d" {
		t.Errorf("Name 应是配置里的 key，得到 %q", dp.Name())
	}
}
