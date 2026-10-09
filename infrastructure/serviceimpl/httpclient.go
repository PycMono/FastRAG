// 本文件是 OpenAI 兼容协议的共享传输层。
//
// embedding 与 rerank 走的是同一条链：POST JSON → 可选 Bearer 头 →
// 限读响应 → 解析错误信封。两个协议的差异只有路径与各自的响应结构，
// 这条公共链只写一份，各实现只保留协议差异部分。

package serviceimpl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	apperrors "github.com/PycMono/FastRAG/common/errors"
)

// apiError OpenAI 兼容服务的通用错误信封，各响应结构内嵌使用。
// 多数厂商失败时返回 {"error": {"message": ...}}。
type apiError struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// check 校验已解析响应的错误信封与 HTTP 状态码，返回 nil 表示响应可用。
//
// 调用方在 JSON 解析失败时自行报「不是合法 JSON」；这里只看信封与状态码，
// body 仅用于组装错误文案（截断后附带）。
func (e *apiError) check(status, code int, action string, body []byte) error {
	if e.Error != nil {
		return apperrors.NewSysError(code, action+" 服务返回错误: "+e.Error.Message)
	}
	if status != http.StatusOK {
		return apperrors.NewSysError(code, fmt.Sprintf(
			"%s 服务 HTTP %d: %s", action, status, truncateBody(body, 512)))
	}
	return nil
}

// postJSON 发起一次 POST，返回限读后的响应体与 HTTP 状态码。
//
// maxBytes 是响应体读取上限（embedding 与 rerank 的上限不同，由调用方传入）；
// 没有上限地 ReadAll 迟早会被一个畸形响应打爆内存。
// wrapErr 是各服务自己的错误模板，建请求、发送、读体这三类网络失败统一用它包装。
func postJSON(
	ctx context.Context,
	client *http.Client,
	url, apiKey string,
	payload []byte,
	maxBytes int64,
	wrapErr *apperrors.SysError,
) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, wrapErr.Wrap(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, wrapErr.Wrap(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, 0, wrapErr.Wrap(err)
	}
	return body, resp.StatusCode, nil
}

// truncateBody 截断响应体用于日志/错误信息，避免泄露大段文本。
func truncateBody(body []byte, n int) string {
	runes := []rune(strings.TrimSpace(string(body)))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "...(截断)"
}