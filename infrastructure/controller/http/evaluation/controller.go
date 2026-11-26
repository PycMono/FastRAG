package evaluation

import (
	"fmt"
	"net/http"

	"github.com/PycMono/FastRAG/application/service/evaluation"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/gin-gonic/gin"
)

// Controller 评测管理入口；认证由现有 admin 路由组施加。
type Controller struct {
	service *evaluation.Service
	review  *evaluation.ReviewService
}

func NewController(service *evaluation.Service, review *evaluation.ReviewService) *Controller {
	return &Controller{service: service, review: review}
}

func bindID(c *gin.Context) (string, error) {
	var input dto.EvalIDURI
	if err := c.ShouldBindUri(&input); err != nil {
		return "", fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err)
	}
	return input.ID, nil
}

func (ctl *Controller) Datasets(c *gin.Context) {
	result, err := ctl.service.ListDatasets(c.Request.Context())
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) List(c *gin.Context) {
	result, err := ctl.service.ListRuns(c.Request.Context())
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) Create(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var input dto.EvalCreateDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.CreateRun(c.Request.Context(), &input)
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) Diagnose(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var input dto.EvalDiagnoseDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.DiagnoseRun(c.Request.Context(), &input)
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) Detail(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}

	result, err := ctl.service.RunDetail(c.Request.Context(), id)
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) Items(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	var query dto.EvalItemsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.RunItems(c.Request.Context(), id, &query)
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) Cancel(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}

	ginsdk.Send(c, nil, ctl.service.CancelRun(id))
}

func (ctl *Controller) Delete(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	ginsdk.Send(c, nil, ctl.service.DeleteRun(c.Request.Context(), id))
}

func (ctl *Controller) Retry(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	result, err := ctl.service.RetryScores(c.Request.Context(), id)
	ginsdk.Send(c, result, err)
}
