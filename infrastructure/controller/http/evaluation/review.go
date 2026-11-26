package evaluation

import (
	"fmt"
	"net/http"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/gin-gonic/gin"
)

func (ctl *Controller) Drafts(c *gin.Context) {
	result, err := ctl.review.ListDrafts(c.Request.Context())
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) Prepare(c *gin.Context) {
	var input dto.EvalPrepareDTO
	if err := c.ShouldBindJSON(&input); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.review.PrepareDraft(c.Request.Context(), &input)
	ginsdk.Send(c, result, err)
}

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

	result, err := ctl.review.GenerateCandidate(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}

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

	result, err := ctl.review.ReviewDraft(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) DeleteDraft(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}
	ginsdk.Send(c, nil, ctl.review.DeleteDraft(c.Request.Context(), id))
}

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

	result, err := ctl.review.StartGeneration(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) StopGeneration(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}

	result, err := ctl.review.StopGeneration(c.Request.Context(), id)
	ginsdk.Send(c, result, err)
}

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

	result, err := ctl.review.PublishDataset(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}

func (ctl *Controller) Baseline(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		ginsdk.Send(c, nil, err)
		return
	}

	ginsdk.Send(c, nil, ctl.review.SetRunBaseline(c.Request.Context(), id))
}

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

	result, err := ctl.review.CompareRun(c.Request.Context(), id, &input)
	ginsdk.Send(c, result, err)
}
