package interfaces

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity"
)

type IEvalAuthor interface {
	Sources(context.Context, string, string, entity.EvalSamplePolicy) ([]entity.EvalSourceChunk, error)
	Generate(context.Context, string) (entity.EvalGeneratedQuestion, error)
}
