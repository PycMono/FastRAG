package gingext

import (
	"errors"
	"net/http"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/gin-gonic/gin"
)

// Send 统一响应封装
// 参考 go-gin-sdk 的 DefaultSender，支持标准 gin 上下文。
// 用 errors.As 而非类型断言提取业务错误码：Controller 惯用
// fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err) 包装绑定错误，
// 包装后动态类型为 *fmt.wrapError，类型断言会失败。
func Send(c *gin.Context, data interface{}, err error) {
	if data == nil {
		data = struct{}{}
	}

	body := &ginsdk.HTTPJSONBody{
		Code: ginsdk.StatusOK,
		Msg:  "success",
		Data: data,
	}

	if err != nil {
		body.Data = struct{}{}

		// 优先使用 CodeError 的错误码
		var codeErr apperrors.CodeError
		if errors.As(err, &codeErr) {
			body.Code = codeErr.Code()
			body.Msg = codeErr.Message()
			c.JSON(httpStatusForCode(codeErr.Code()), body)
			return
		}

		// 兼容标准 error（fallback）
		body.Code = ginsdk.ErrCodeUnknown
		body.Msg = err.Error()
		c.JSON(http.StatusOK, body)
		return
	}

	c.JSON(http.StatusOK, body)
}

// httpStatusForCode 根据业务错误码映射 HTTP 状态码
func httpStatusForCode(code int) int {
	switch code {
	case apperrors.CodeInvalidParam:
		return http.StatusBadRequest
	case apperrors.CodeUnauthorized:
		return http.StatusUnauthorized
	case apperrors.CodeForbidden:
		return http.StatusForbidden
	case apperrors.CodeNotFound, apperrors.CodeKnowledgeBaseNotFound:
		return http.StatusNotFound
	case apperrors.CodeRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusOK
	}
}
