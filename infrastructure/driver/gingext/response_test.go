package gingext

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/gin-gonic/gin"
)

// newTestContext 构造 gin 测试上下文与响应记录器
func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, rec
}

// decodeBody 解析统一响应体，返回 code / msg / data 原文
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) (int, string, string) {
	t.Helper()
	var body struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v, body=%s", err, rec.Body.String())
	}
	return body.Code, body.Msg, string(body.Data)
}

func TestSend_Success(t *testing.T) {
	c, rec := newTestContext()

	Send(c, nil, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态码期望 200，实际 %d", rec.Code)
	}
	code, msg, data := decodeBody(t, rec)
	if code != ginsdk.StatusOK || msg != "success" {
		t.Fatalf("期望 code=0 msg=success，实际 code=%d msg=%s", code, msg)
	}
	if data != "{}" {
		t.Fatalf("data 期望归一化为 {}，实际 %s", data)
	}
}

func TestSend_PassesDataThrough(t *testing.T) {
	c, rec := newTestContext()

	Send(c, map[string]string{"id": "42"}, nil)

	code, _, data := decodeBody(t, rec)
	if code != ginsdk.StatusOK {
		t.Fatalf("期望 code=0，实际 %d", code)
	}
	if !strings.Contains(data, `"id":"42"`) {
		t.Fatalf("data 未透传业务数据，实际 %s", data)
	}
}

func TestSend_WrappedBizErrorKeepsCode(t *testing.T) {
	c, rec := newTestContext()

	// Controller 惯用写法：包装后动态类型是 *fmt.wrapError，
	// 类型断言会失败，必须靠 errors.As 沿错误链取码。
	err := fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, errors.New("name is required"))
	Send(c, nil, err)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HTTP 状态码期望 400，实际 %d", rec.Code)
	}
	code, msg, data := decodeBody(t, rec)
	if code != apperrors.CodeInvalidParam {
		t.Fatalf("期望 code=%d，实际 %d", apperrors.CodeInvalidParam, code)
	}
	if msg != "invalid parameter" {
		t.Fatalf("期望 msg=invalid parameter，实际 %s", msg)
	}
	if data != "{}" {
		t.Fatalf("data 期望归一化为 {}，实际 %s", data)
	}
}

func TestSend_SentinelNotFoundMapsTo404(t *testing.T) {
	c, rec := newTestContext()

	Send(c, nil, apperrors.ErrKnowledgeBaseNotFound)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("HTTP 状态码期望 404，实际 %d", rec.Code)
	}
	code, msg, _ := decodeBody(t, rec)
	if code != apperrors.CodeKnowledgeBaseNotFound {
		t.Fatalf("期望 code=%d，实际 %d", apperrors.CodeKnowledgeBaseNotFound, code)
	}
	if msg != "knowledge base not found" {
		t.Fatalf("期望 msg=knowledge base not found，实际 %s", msg)
	}
}

func TestSend_PlainErrorFallsBackToUnknownCode(t *testing.T) {
	c, rec := newTestContext()

	Send(c, nil, errors.New("boom"))

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP 状态码期望 200，实际 %d", rec.Code)
	}
	code, msg, _ := decodeBody(t, rec)
	if code != ginsdk.ErrCodeUnknown {
		t.Fatalf("期望 code=%d，实际 %d", ginsdk.ErrCodeUnknown, code)
	}
	if msg != "boom" {
		t.Fatalf("期望 msg=boom，实际 %s", msg)
	}
}
