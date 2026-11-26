package interfaces

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity"
)

// IEvalSourceReader 从内部文档服务读取并抽样原文。
type IEvalSourceReader interface {
	SourcesForEvaluation(context.Context, string, string, entity.EvalSamplePolicy) ([]entity.EvalSourceChunk, error)
}

// IEvalGenerator 调用外部出题模型生成候选题。
type IEvalGenerator interface {
	Generate(context.Context, string) (entity.EvalGeneratedQuestion, error)
}
