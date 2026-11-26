package evaluation

import (
	"fmt"
	"net/http"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/gin-gonic/gin"
)

// Drafts 返回题库草稿列表。
func (ctl *Controller) Drafts(c *gin.Context) {
	result, err := ctl.datasets.ListDrafts(c.Request.Context())
	ginsdk.Send(c, result, err)
}

// Prepare 从当前知识库抽取原文并创建草稿。
func (ctl *Controller) Prepare(c *gin.Context) {
	var input dto.EvalPrepareDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.datasets.PrepareDraft(c.Request.Context(), &input)
	ginsdk.Send(c, result, err)
}

// Generate 为草稿中的单题生成问答。
func (ctl *Controller) Generate(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	var input dto.EvalGenerateDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.datasets.GenerateCandidate(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}

// Review 保存人工复核结果。
func (ctl *Controller) Review(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<20)
	var input dto.EvalReviewDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.datasets.ReviewDraft(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}

// DeleteDraft 删除一份草稿。
func (ctl *Controller) DeleteDraft(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	ginsdk.Send(c, nil, ctl.datasets.DeleteDraft(c.Request.Context(), id))
}

// StartGeneration 启动整份草稿的后台出题。
func (ctl *Controller) StartGeneration(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	var input dto.EvalGenerationDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.datasets.StartGeneration(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}

// StopGeneration 暂停后台出题。
func (ctl *Controller) StopGeneration(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}

	result, err := ctl.datasets.StopGeneration(c.Request.Context(), id)
	ginsdk.Send(c, result, err)
}

// Publish 发布题库为不可变版本。
func (ctl *Controller) Publish(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	var input dto.EvalPublishDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.datasets.PublishDataset(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}

// Baseline 把一个成功任务设为题库基线。
func (ctl *Controller) Baseline(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}

	ginsdk.Send(c, nil, ctl.datasets.SetRunBaseline(c.Request.Context(), id))
}

// Compare 把任务与基线按门槛对比。
func (ctl *Controller) Compare(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	var input dto.EvalCompareDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.datasets.CompareRun(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}
