// 本文件是文档的只读接口：三个 GET，参数走 query string。
// 写侧见 controller.go（POST + JSON body）。

package doc

import (
	"fmt"

	"github.com/PycMono/FastRAG/application/service/docview"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/gin-gonic/gin"
)

// ReadController 文档只读 HTTP 控制器。
//
// 与写侧同包：读与写是同一个资源，不必为读再开一个包。
type ReadController struct {
	service *docview.Service
}

func NewReadController(service *docview.Service) *ReadController {
	return &ReadController{service: service}
}

// List GET /api/v1/docs
func (ctl *ReadController) List(c *gin.Context) {
	var q dto.DocListQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}
	result, err := ctl.service.List(c.Request.Context(), &q)
	ginsdk.Send(c, result, err)
}

// Detail GET /api/v1/docs/detail
func (ctl *ReadController) Detail(c *gin.Context) {
	var q dto.DocDetailQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}
	result, err := ctl.service.Detail(c.Request.Context(), &q)
	ginsdk.Send(c, result, err)
}

// Chunks GET /api/v1/docs/chunks
func (ctl *ReadController) Chunks(c *gin.Context) {
	var q dto.ChunkListQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}
	result, err := ctl.service.Chunks(c.Request.Context(), &q)
	ginsdk.Send(c, result, err)
}
