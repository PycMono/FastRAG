// 本文件实现腾讯云 OCR「多模态文档解析」后端（P1）。
//
// 契约：同步接口 MultimodalDocParse（service=ocr，Version=2018-11-19）。
//
//	入参 FileUrl（公网可下载，下载时限 3 秒）+ FileType + ResultType=2（只要 markdown）
//	返回 ResultUrl：一个 zip 的临时下载地址（30 分钟有效），内含 <name>.md/.json/images/
//
// 适配器下载该 zip 并取出 .md。
//
// 为什么走同步接口：它只吃 FileUrl（异步接口能带 FileBase64 但上限 8MB）。
// 本方案的输入本来就是 URL，同步接口正好合适，不需要 base64，也不需要对象存储。
//
// 大小/页数上限由腾讯侧定：PDF/Word/PPT ≤150MB 且 ≤300 页、Excel/TXT ≤10MB、
// 图片 ≤30MB，单次调用最多 300 页。超限的错误由腾讯回给我们，归 SysError（§9）。
//
// 符号按 tencentcloud-sdk-go 的 ocr v1.3.193 核对过（含 HttpProfile.ReqTimeout 以秒为单位）。

package serviceimpl

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/avast/retry-go/v4"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	ocr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ocr/v20181119"
)

const (
	tencentResultTypeMarkdown = 2
	maxTencentZipBytes        = 256 << 20
	maxTencentMDBytes         = 64 << 20
)

// tencentFileType 扩展名 → 腾讯 FileType。腾讯要求显式给，映射不到就报参数错误。
var tencentFileType = map[string]int64{
	"pdf": 1, "doc": 2, "docx": 2, "ppt": 3, "pptx": 3,
	"xls": 4, "xlsx": 4, "md": 5, "txt": 6, "wps": 8,
	"png": 7, "jpg": 7, "jpeg": 7, "bmp": 7, "tif": 7, "tiff": 7,
}

// 配置里是毫秒，SDK 的 HttpProfile.ReqTimeout 是**秒**
// （client.go: c.httpClient.Timeout = time.Duration(ReqTimeout) * time.Second）。
// 不接上的话会静默用 SDK 默认的 60s，配置里的 timeout_ms 等于白写；
// 而 ReqTimeout=0 意味着**不设超时**，所以不足 1 秒也必须兜到 1。
func reqTimeoutSeconds(ms int) int {
	if secs := ms / 1000; secs > 0 {
		return secs
	}
	return 1
}

// tencentHTTPProfile 造 SDK 的 HTTP 配置：接入点 + 超时（秒）。
//
// 抽成纯函数是为了能被单测直接断言：SDK v1.3.193 没有 profile 的取用侧 API，
// 只有把构造变成能单独调用的函数，才测得到 ReqTimeout 到底有没有接到 profile 上。
func tencentHTTPProfile(p config.ParserParams) *profile.HttpProfile {
	hp := profile.NewHttpProfile()
	hp.Endpoint = "ocr.tencentcloudapi.com"
	hp.ReqTimeout = reqTimeoutSeconds(p.TimeoutMS)
	return hp
}

type tencentParser struct {
	name       string
	formats    []string
	client     *ocr.Client
	http       *http.Client
	timeout    time.Duration // 整段重试的总预算，不是单次尝试的上限
	maxRetries uint
	retryWait  time.Duration
}

// newTencentParser 造腾讯解析实现。密钥/区域缺失在这里就报错——它比 validate 更靠后一步，
// 但仍早于任何一次真实调用，符合「启动期暴露配置错」的一贯做法。
func newTencentParser(p config.ParserParams) (*tencentParser, error) {
	if strings.TrimSpace(p.SecretID) == "" || strings.TrimSpace(p.SecretKey) == "" {
		return nil, fmt.Errorf("解析后端 %q（tencent）缺 secret_id / secret_key", p.Name)
	}
	if strings.TrimSpace(p.Region) == "" {
		return nil, fmt.Errorf("解析后端 %q（tencent）缺 region", p.Name)
	}

	prof := profile.NewClientProfile()
	prof.HttpProfile = tencentHTTPProfile(p)

	// ocr.NewClient 从不给 err 赋值（生成代码：client.Init().WithCredential().WithProfile()
	// 后直接 return），所以这里没有可失败的分支。
	c, _ := ocr.NewClient(common.NewCredential(p.SecretID, p.SecretKey), p.Region, prof)

	timeout := time.Duration(p.TimeoutMS) * time.Millisecond
	return &tencentParser{
		name:       p.Name,
		formats:    p.Formats,
		client:     c,
		http:       &http.Client{Timeout: timeout},
		timeout:    timeout,
		maxRetries: uint(p.MaxRetries),
		retryWait:  time.Duration(p.RetryWaitMS) * time.Millisecond,
	}, nil
}

func (p *tencentParser) Name() string      { return p.name }
func (p *tencentParser) Formats() []string { return p.formats }

// Parse 带重试。总预算与 Docling 同款：罩住**整段**重试，而不是每次尝试各算一份。
// 解析是幂等的（同一个 URL 两次结果相同），所以重试安全。
func (p *tencentParser) Parse(ctx context.Context, req interfaces.ParseReq) (interfaces.ParseResult, error) {
	// 扩展名映射是纯参数检查：放在重试之外——它不该被重试，也不该消耗预算。
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(req.Filename), "."))
	ft, ok := tencentFileType[ext]
	if !ok {
		return interfaces.ParseResult{}, apperrors.NewParamError("腾讯解析不支持该文件类型: " + ext)
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var result interfaces.ParseResult
	err := retry.Do(
		func() error {
			var err error
			result, err = p.parseOnce(ctx, req, ft)
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
func (p *tencentParser) parseOnce(
	ctx context.Context, req interfaces.ParseReq, ft int64,
) (interfaces.ParseResult, error) {
	request := ocr.NewMultimodalDocParseRequest()
	request.FileUrl = common.StringPtr(req.URL)
	request.FileType = common.Int64Ptr(ft)
	request.ResultType = common.Int64Ptr(tencentResultTypeMarkdown)

	resp, err := p.client.MultimodalDocParseWithContext(ctx, request)
	if err != nil {
		return interfaces.ParseResult{}, apperrors.ErrParseFailed.Wrap(err)
	}
	if resp == nil || resp.Response == nil || resp.Response.ResultUrl == nil {
		return interfaces.ParseResult{}, apperrors.NewSysError(apperrors.CodeParseFail, "腾讯解析未返回 ResultUrl")
	}

	md, err := p.fetchMarkdown(ctx, *resp.Response.ResultUrl)
	if err != nil {
		return interfaces.ParseResult{}, err
	}
	return interfaces.ParseResult{Markdown: md}, nil
}

// fetchMarkdown 下载 ResultUrl 的 zip，取出里面的 .md。
func (p *tencentParser) fetchMarkdown(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", apperrors.ErrParseFailed.Wrap(err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return "", apperrors.ErrParseFailed.Wrap(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", apperrors.NewSysError(apperrors.CodeParseFail,
			fmt.Sprintf("下载腾讯结果包 HTTP %d", resp.StatusCode))
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTencentZipBytes))
	if err != nil {
		return "", apperrors.ErrParseFailed.Wrap(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", apperrors.NewSysError(apperrors.CodeParseFail, "腾讯结果包不是合法 zip")
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".md") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", apperrors.ErrParseFailed.Wrap(err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxTencentMDBytes))
		_ = rc.Close()
		if err != nil {
			return "", apperrors.ErrParseFailed.Wrap(err)
		}
		return string(content), nil
	}
	return "", apperrors.NewSysError(apperrors.CodeParseFail, "腾讯结果包里没有 .md")
}
