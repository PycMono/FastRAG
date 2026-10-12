package serviceimpl

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// 映射不到的扩展名 → 参数错误，且在碰 client 之前返回（client 这里是零值）。
func TestTencentParser_UnsupportedExt_FailsEarly(t *testing.T) {
	p := &tencentParser{name: "tencent", formats: []string{"pdf"}}

	_, err := p.Parse(context.Background(), interfaces.ParseReq{
		URL: "https://example.com/a.exe", Filename: "a.exe",
	})
	if err == nil {
		t.Fatal(".exe 映射不到 FileType，必须报参数错误")
	}
	if _, ok := apperrors.AsBizError(err); !ok {
		t.Errorf("应归类为 BizError/ParamError（调用方的错），得到 %T", err)
	}
}

// zip 里的 .md 被取出来；json 与 images/ 被忽略。
func TestTencentParser_FetchMarkdown(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"doc.md":       "# 解析结果\n正文",
		"doc.json":     `{"不该被取到": true}`,
		"images/a.png": "binary",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	p := &tencentParser{name: "tencent", http: srv.Client()}
	md, err := p.fetchMarkdown(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if md != "# 解析结果\n正文" {
		t.Errorf("应取到 doc.md 的内容，得到 %q", md)
	}
}

// zip 里没有 .md → 报错，不能当成功返回空串。
func TestTencentParser_FetchMarkdown_NoMD(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("doc.json")
	_, _ = w.Write([]byte(`{}`))
	_ = zw.Close()
	raw := buf.Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	p := &tencentParser{name: "tencent", http: srv.Client()}
	if _, err := p.fetchMarkdown(context.Background(), srv.URL); err == nil {
		t.Fatal("包里没有 .md 必须报错")
	}
}

// 密钥缺失在构造函数就报错——它比 validate 更靠后，但早于任何一次真实调用。
func TestNewTencentParser_MissingCredential(t *testing.T) {
	for _, p := range []config.ParserParams{
		{Name: "t", Type: config.ParserTypeTencent, SecretKey: "k", TimeoutMS: 1000},
		{Name: "t", Type: config.ParserTypeTencent, SecretID: "id", TimeoutMS: 1000},
		{Name: "t", Type: config.ParserTypeTencent, TimeoutMS: 1000},
	} {
		if _, err := newTencentParser(p); err == nil {
			t.Errorf("缺 secret_id/secret_key 必须报错：%#v", p)
		}
	}
}

// 毫秒 → 秒的换算单测。ReqTimeout=0 在 SDK 里意味着**不设超时**
// （client.go: c.httpClient.Timeout = time.Duration(ReqTimeout) * time.Second），
// 配了 timeout_ms 却算出 0 就等于超时形同虚设，所以不足 1 秒必须兜到 1。
func TestReqTimeoutSeconds(t *testing.T) {
	cases := []struct{ ms, wantSec int }{
		{110000, 110},
		{1500, 1},
		{999, 1}, // 不能是 0
		{1, 1},   // 不能是 0
	}
	for _, tc := range cases {
		if got := reqTimeoutSeconds(tc.ms); got != tc.wantSec {
			t.Errorf("reqTimeoutSeconds(%d) 应为 %ds，得到 %ds", tc.ms, tc.wantSec, got)
		}
	}
}

// 上面那个换算确实接到了 SDK 的 profile 上，而且总预算仍是毫秒。
//
// 取用侧只钉 p.timeout：ocr v1.3.193 的 client 上没有 GetProfile()，
// 而换算这件事的真正守卫是 TestReqTimeoutSeconds，不必依赖 SDK 的取用侧 API。
func TestNewTencentParser_ReqTimeoutWired(t *testing.T) {
	for _, tc := range []struct{ ms, wantSec int }{
		{110000, 110},
		{999, 1},
	} {
		p, err := newTencentParser(config.ParserParams{
			Name: "t", Type: config.ParserTypeTencent,
			SecretID: "id", SecretKey: "key", TimeoutMS: tc.ms,
		})
		if err != nil {
			t.Fatal(err)
		}
		if p.timeout.Milliseconds() != int64(tc.ms) {
			t.Errorf("总预算应为 %dms，得到 %dms", tc.ms, p.timeout.Milliseconds())
		}
	}
}
