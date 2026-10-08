package knowledge

import (
	"fmt"

	appsvc "github.com/PycMono/FastRAG/application/service/knowledge"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/PycMono/go-context-sdk/bizctx"
	"github.com/gin-gonic/gin"
)

// Controller 知识库 HTTP 控制器
type Controller struct {
	service *appsvc.Service
}

// NewController 创建控制器（fx 注入）
func NewController(service *appsvc.Service) *Controller {
	return &Controller{service: service}
}

// getUserID 从 bizctx 取用户标识
// 由 go-gin-sdk 的 middleware.Bizctx() 从请求头 X-Bizctx-UserID 注入
func getUserID(c *gin.Context) string {
	return bizctx.GetUserID(c.Request.Context())
}

// Create POST /api/v1/knowledge-bases
func (ctl *Controller) Create(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	var param dto.CreateKnowledgeBaseDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.Create(c.Request.Context(), userID, param)
	gingext.Send(c, result, err)
}

// List GET /api/v1/knowledge-bases
func (ctl *Controller) List(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	var query dto.ListKnowledgeBaseQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.List(c.Request.Context(), userID, query)
	gingext.Send(c, result, err)
}

// Get GET /api/v1/knowledge-bases/:id
func (ctl *Controller) Get(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	result, err := ctl.service.Get(c.Request.Context(), userID, c.Param("id"))
	gingext.Send(c, result, err)
}

// Update PUT /api/v1/knowledge-bases/:id
func (ctl *Controller) Update(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	var param dto.UpdateKnowledgeBaseDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	err := ctl.service.Update(c.Request.Context(), userID, c.Param("id"), param)
	gingext.Send(c, nil, err)
}

// Delete DELETE /api/v1/knowledge-bases/:id
func (ctl *Controller) Delete(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	err := ctl.service.Delete(c.Request.Context(), userID, c.Param("id"))
	gingext.Send(c, nil, err)
}

// Search POST /api/v1/knowledge-bases/:id/search
func (ctl *Controller) Search(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	var param dto.SearchKnowledgeBaseDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.Search(c.Request.Context(), userID, c.Param("id"), param)
	gingext.Send(c, result, err)
}
