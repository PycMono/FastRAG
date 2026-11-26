package repository

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity"
)

type IEvalReviewRepo interface {
	ListDrafts(context.Context) ([]entity.EvalDraft, error)
	GetDraft(context.Context, string) (entity.EvalDraft, error)
	SaveDraft(context.Context, entity.EvalDraft, int) error
	DeleteDraft(context.Context, string) error
	Publish(context.Context, entity.EvalDataset) error
	SetBaseline(context.Context, string, string) error
	Baseline(context.Context, string) (string, error)
	SaveComparison(context.Context, entity.EvalComparison) error
	InvalidateDocument(context.Context, string, string, string) error
}
