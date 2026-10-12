// 本文件实现 Docling Serve 解析后端。
//
// 契约（自部署服务，compose 里挂 profile "docling"，默认不启动——§2）：
//
//	POST {base_url}/v1/convert/source
//	body {"options":{...},"sources":[{"kind":"http","url":...}]}
//	响应 document.md_content
//
// 同步调用一次拿结果。若将来大文档经常撞 timeout_ms，改成
// /v1/convert/source/async + /v1/status/poll/{id} + /v1/result/{id}——
// 只改本文件，端口与配置形状都不变。

package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/avast/retry-go/v4"
)

// maxDoclingResponseBytes 响应体读取上限。没有上限地 ReadAll 迟早被畸形响应打爆内存。
const maxDoclingResponseBytes = 128 << 20

// doclingDefaultOptions Docling 的可调默认值；配置里的 options 覆盖它（浅合并）。
//
// image_export_mode 默认 placeholder 而不是 embedded：embedded 会把图片转成
// base64 内联进 markdown，那串东西会原样进 ES 的 content 字段，撑大索引又污染检索。
var doclingDefaultOptions = map[string]any{
	"to_formats":        []string{"md"},
	"do_ocr":            true,
	"table_mode":        "accurate",
	"pdf_backend":       "docling_parse",
	"image_export_mode": "placeholder",
	"abort_on_error":    false,
}

// doclingParser 按 Docling Serve 的 /v1/convert/source 解析。
type doclingParser struct {
	name       string
	client     *http.Client
	url        string // 完整端点：base_url + /v1/convert/source
	formats    []string
	options    map[string]any
	timeout    time.Duration // 整段重试的总预算，不是单次尝试的上限
	maxRetries uint
	retryWait  time.Duration
}

// doclingParser 必须满足解析后端端口。编译期断言，形状不对在这里先炸。
var _ interfaces.IParser = (*doclingParser)(nil)

// newDoclingParser 从一份已合并好的参数构造实现。只有注册表调它。
func newDoclingParser(p config.ParserParams) *doclingParser {
	opts := make(map[string]any, len(doclingDefaultOptions)+len(p.Options))
	for k, v := range doclingDefaultOptions {
		opts[k] = v
	}
	for k, v := range p.Options { // 配置覆盖默认
		opts[k] = v
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond
	return &doclingParser{
		name:       p.Name,
		client:     &http.Client{Timeout: timeout},
		url:        strings.TrimRight(p.BaseURL, "/") + "/v1/convert/source",
		formats:    p.Formats,
		options:    opts,
		timeout:    timeout,
		maxRetries: uint(p.MaxRetries),
		retryWait:  time.Duration(p.RetryWaitMS) * time.Millisecond,
	}
}

func (p *doclingParser) Name() string      { return p.name }
func (p *doclingParser) Formats() []string { return p.formats }

// doclingResponse 只取我们用得上的字段。
type doclingResponse struct {
	Status   string `json:"status"`
	Document struct {
		MDContent string `json:"md_content"`
	} `json:"document"`
}

// Parse 带重试的单次调用。与 embedding 一致：固定退避、只透传最后一次错误。
//
// 整个重试循环共享**一份**总预算（p.timeout），而不是每次尝试各算一份：
// http.Client.Timeout 是 per-attempt 的，重试会让总耗时翻倍，最终撞穿
// http.write_timeout——那时连接已被掐断，我们连错误都回不了包（§7）。
// 副作用是「快失败」（连不上、秒回 5xx）才留得下余量重试，正合我们要的语义。
func (p *doclingParser) Parse(ctx context.Context, req interfaces.ParseReq) (interfaces.ParseResult, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var result interfaces.ParseResult
	err := retry.Do(
		func() error {
			var err error
			result, err = p.parseOnce(ctx, req)
			return err
		},
		retry.Context(ctx),
		retry.Attempts(p.maxRetries+1), // 首次 + maxRetries 次
		retry.Delay(p.retryWait),
		retry.DelayType(retry.FixedDelay),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		return interfaces.ParseResult{}, err
	}
	return result, nil
}

// parseOnce 单次调用，不含重试。
func (p *doclingParser) parseOnce(ctx context.Context, req interfaces.ParseReq) (interfaces.ParseResult, error) {
	payload, err := json.Marshal(map[string]any{
		"options": p.options,
		"sources": []map[string]any{{"kind": "http", "url": req.URL}},
	})
	if err != nil {
		return interfaces.ParseResult{}, apperrors.ErrParseFailed.Wrap(err)
	}

	// Docling 默认无鉴权，apiKey 传空 → postJSON 不加 Authorization 头
	body, status, err := postJSON(ctx, p.client, p.url, "", payload,
		maxDoclingResponseBytes, apperrors.ErrParseFailed)
	if err != nil {
		return interfaces.ParseResult{}, err
	}

	// 先看状态码再看体：上游报错时返回的往往不是 JSON（比如一段 html 错误页），
	// 先解 JSON 会把「HTTP 500」误报成「响应不是合法 JSON」。
	if status != http.StatusOK {
		return interfaces.ParseResult{}, apperrors.NewSysError(apperrors.CodeParseFail, fmt.Sprintf(
			"docling 服务 HTTP %d: %s", status, truncateBody(body, 512)))
	}

	var parsed doclingResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return interfaces.ParseResult{}, apperrors.NewSysError(apperrors.CodeParseFail, fmt.Sprintf(
			"docling 响应不是合法 JSON，HTTP %d: %s", status, truncateBody(body, 512)))
	}
	if parsed.Document.MDContent == "" {
		return interfaces.ParseResult{}, apperrors.NewSysError(apperrors.CodeParseFail,
			"docling 返回的 document.md_content 为空，status="+parsed.Status)
	}
	return interfaces.ParseResult{Markdown: parsed.Document.MDContent}, nil
}
