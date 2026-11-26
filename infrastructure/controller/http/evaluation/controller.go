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
	service  *evaluation.Service
	datasets *evaluation.DatasetService
}

// NewController 构造评测管理控制器。
func NewController(service *evaluation.Service, datasets *evaluation.DatasetService) *Controller {
	return &Controller{service: service, datasets: datasets}
}

// bindID 解析并校验路径中的评测 ID。
func bindID(c *gin.Context) (string, error) {
	var input dto.EvalIDURI
	if err := c.ShouldBindUri(&input); err != nil {
		return "", fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err)
	}
	return input.ID, nil
}

// Datasets 返回已发布题库列表。
func (ctl *Controller) Datasets(c *gin.Context) {
	result, err := ctl.service.ListDatasets(c.Request.Context())
	ginsdk.Send(c, result, err)
}

// List 返回评估任务列表。
func (ctl *Controller) List(c *gin.Context) {
	result, err := ctl.service.ListRuns(c.Request.Context())
	ginsdk.Send(c, result, err)
}

// Create 创建一次评估任务。
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

// Diagnose 创建单题诊断任务。
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

// Detail 返回任务详情和汇总。
func (ctl *Controller) Detail(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}

	result, err := ctl.service.RunDetail(c.Request.Context(), id)
	ginsdk.Send(c, result, err)
}

// Items 分页返回任务逐题结果。
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

// Cancel 取消当前运行中的任务。
func (ctl *Controller) Cancel(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}

	ginsdk.Send(c, nil, ctl.service.CancelRun(id))
}

// Delete 删除一条评估记录。
func (ctl *Controller) Delete(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	ginsdk.Send(c, nil, ctl.service.DeleteRun(c.Request.Context(), id))
}

// Rescore 只重评失败指标。
func (ctl *Controller) Rescore(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	result, err := ctl.service.RescoreFailedMetrics(c.Request.Context(), id)
	ginsdk.Send(c, result, err)
}
