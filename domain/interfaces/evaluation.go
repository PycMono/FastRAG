package interfaces

import (
	"context"
	"errors"

	"github.com/PycMono/FastRAG/domain/entity"
)

var ErrEvalServiceBusy = errors.New("评分服务忙，请稍后重试")

type IEvalSearcher interface {
	SearchForEvaluation(context.Context, entity.EvalCase, entity.EvalProfile) (*entity.EvalRetrieval, error)
}

type IEvalScorer interface {
	ScoreBatch(context.Context, []entity.EvalScoreInput) ([]entity.EvalScoreOutput, error)
}
