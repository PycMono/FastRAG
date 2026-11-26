package repository

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity"
)

type IEvalDatasetRepo interface {
	List(context.Context) ([]entity.EvalDatasetInfo, error)
	Load(context.Context, string) (entity.EvalDataset, error)
}

type IEvalRunRepo interface {
	Create(context.Context, entity.EvalRun) error
	Save(context.Context, entity.EvalRun) error
	Get(context.Context, string) (entity.EvalRun, error)
	List(context.Context) ([]entity.EvalRun, error)
	Delete(context.Context, string) error
	Recover(context.Context) error
}
