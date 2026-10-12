package serviceimpl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

func newTestDoclingParser(baseURL string) *doclingParser {
	return newDoclingParser(config.ParserParams{
		Name:      "docling",
		Type:      config.ParserTypeDocling,
		BaseURL:   baseURL,
		TimeoutMS: 5000,
		Formats:   []string{"pdf", "docx"},
	})
}

func TestDoclingParser_Parse_OK(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/convert/source" {
			t.Errorf("路径应为 /v1/convert/source，得到 %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"status":"success","document":{"md_content":"# 标题\n正文"}}`))
	}))
	defer srv.Close()

	res, err := newTestDoclingParser(srv.URL).
		Parse(context.Background(), interfaces.ParseReq{URL: "https://example.com/a.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Markdown != "# 标题\n正文" {
		t.Errorf("markdown 取值不对: %q", res.Markdown)
	}

	sources, _ := gotBody["sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["url"] != "https://example.com/a.pdf" {
		t.Errorf("sources[0].url 应原样带上请求的地址，得到 %#v", gotBody["sources"])
	}

	opts, _ := gotBody["options"].(map[string]any)
	if opts["image_export_mode"] != "placeholder" {
		t.Errorf("image_export_mode 必须默认 placeholder（不能是 embedded），得到 %v", opts["image_export_mode"])
	}
	to, _ := opts["to_formats"].([]any)
	if len(to) != 1 || to[0] != "md" {
		t.Errorf("to_formats 应为 [md]，得到 %#v", opts["to_formats"])
	}
}

// 配置里的 options 必须盖掉默认值（浅合并），而不是被默认值盖掉。
func TestDoclingParser_OptionsOverrideDefaults(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"status":"success","document":{"md_content":"x"}}`))
	}))
	defer srv.Close()

	p := newDoclingParser(config.ParserParams{
		Name:      "docling",
		BaseURL:   srv.URL,
		TimeoutMS: 5000,
		Options:   map[string]any{"do_ocr": false, "table_mode": "fast"},
	})
	if _, err := p.Parse(context.Background(), interfaces.ParseReq{URL: "https://example.com/a.pdf"}); err != nil {
		t.Fatal(err)
	}

	opts, _ := gotBody["options"].(map[string]any)
	if opts["do_ocr"] != false {
		t.Errorf("配置的 do_ocr=false 应盖掉默认的 true，得到 %v", opts["do_ocr"])
	}
	if opts["table_mode"] != "fast" {
		t.Errorf("配置的 table_mode 应盖掉默认值，得到 %v", opts["table_mode"])
	}
	// 没被覆盖的默认值要还在
	if opts["image_export_mode"] != "placeholder" {
		t.Errorf("未被覆盖的默认值应保留，得到 %v", opts["image_export_mode"])
	}
}

func TestDoclingParser_Parse_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`)) // 不是 JSON：状态码必须先于解 JSON 被检查
	}))
	defer srv.Close()

	_, err := newTestDoclingParser(srv.URL).
		Parse(context.Background(), interfaces.ParseReq{URL: "https://example.com/a.pdf"})
	if err == nil {
		t.Fatal("上游 500 必须报错")
	}
	if _, ok := apperrors.AsSysError(err); !ok {
		t.Errorf("应归类为 SysError，得到 %T", err)
	}
}

func TestDoclingParser_Parse_EmptyMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","document":{"md_content":""}}`))
	}))
	defer srv.Close()

	_, err := newTestDoclingParser(srv.URL).
		Parse(context.Background(), interfaces.ParseReq{URL: "https://example.com/a.pdf"})
	if err == nil {
		t.Fatal("md_content 为空必须报错，不能当成功返回")
	}
}

// max_retries=1 → 首次失败后再试一次，服务端共收到 2 次请求。
func TestDoclingParser_Parse_RetriesOnce(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","document":{"md_content":"第二次好了"}}`))
	}))
	defer srv.Close()

	p := newTestDoclingParser(srv.URL)
	p.maxRetries = 1
	p.retryWait = time.Millisecond

	res, err := p.Parse(context.Background(), interfaces.ParseReq{URL: "https://example.com/a.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Markdown != "第二次好了" {
		t.Errorf("第二次应成功，得到 %q", res.Markdown)
	}
	if calls != 2 {
		t.Errorf("期望 2 次请求（首次 + 1 次重试），得到 %d", calls)
	}
}

// max_retries=0 → 不重试，失败就是失败。
func TestDoclingParser_Parse_NoRetryWhenZero(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newTestDoclingParser(srv.URL)
	p.maxRetries = 0

	if _, err := p.Parse(context.Background(), interfaces.ParseReq{URL: "https://example.com/a.pdf"}); err == nil {
		t.Fatal("应报错")
	}
	if calls != 1 {
		t.Errorf("max_retries=0 时不该重试，得到 %d 次请求", calls)
	}
}

// Review Focus 第 4 条：ctx 已取消时不许空转重试。
// 配了 3 次重试，但取消的 ctx 必须让它在第一次就停——否则调用方断开后
// 解析后端仍被打满。
func TestDoclingParser_CanceledCtx_StopsRetrying(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newTestDoclingParser(srv.URL)
	p.maxRetries = 3
	p.retryWait = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Parse(ctx, interfaces.ParseReq{URL: "https://example.com/a.pdf"}); err == nil {
		t.Fatal("ctx 已取消必须报错")
	}
	if calls > 1 {
		t.Errorf("ctx 取消后不该继续重试，期望 ≤1 次请求，得到 %d", calls)
	}
}
