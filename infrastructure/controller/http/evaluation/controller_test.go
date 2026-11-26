package evaluation

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	app "github.com/PycMono/FastRAG/application/service/evaluation"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/gin-gonic/gin"
)

// TestDisabledAndInvalidID 验证评测关闭和非法 ID 都返回业务错误。
func TestDisabledAndInvalidID(t *testing.T) {
	router := gin.New()
	controller := NewController(app.NewService(nil, nil, nil, nil, app.Options{}), nil)
	router.GET("/datasets", controller.Datasets)
	router.GET("/runs/:id", controller.Detail)

	tests := []struct {
		path string
		code int
	}{
		{path: "/datasets", code: 10601},
		{path: "/runs/bad.id", code: apperrors.CodeInvalidParam},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", test.path, nil))
		var body struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || body.Code != test.code {
			t.Fatalf("%s: HTTP %d code %d", test.path, response.Code, body.Code)
		}
	}
}

// TestSDKRecordsBusinessErrors 验证业务错误会进入 ginsdk 的日志/追踪链路。
func TestSDKRecordsBusinessErrors(t *testing.T) {
	router := gin.New()
	controller := NewController(app.NewService(nil, nil, nil, nil, app.Options{}), nil)
	var recorded bool
	router.Use(func(c *gin.Context) {
		c.Next()
		recorded = len(c.Errors) > 0
	})
	router.GET("/datasets", controller.Datasets)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/datasets", nil))
	if !recorded {
		t.Fatal("业务错误没有通过 ginsdk 进入请求日志/追踪链路")
	}
}
