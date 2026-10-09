package search

import (
	"fmt"

	appsvc "github.com/PycMono/FastRAG/application/service/search"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/gin-gonic/gin"
)

// Controller 检索 HTTP 控制器。
type Controller struct {
	service *appsvc.Service
}

func NewController(service *appsvc.Service) *Controller {
	return &Controller{service: service}
}

// Search POST /api/v1/search
func (ctl *Controller) Search(c *gin.Context) {
	var param dto.SearchDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.Search(c.Request.Context(), &param)
	gingext.Send(c, result, err)
}
