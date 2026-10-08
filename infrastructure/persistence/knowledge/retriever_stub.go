package knowledge

import (
	"context"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
)

// RetrieverStub 检索能力占位实现
// 骨架阶段尚未接入向量库，统一返回「检索未实现」错误码。
// 接入向量库时：新增一个实现 knowledgerepo.IKnowledgeRetriever 的类型，
// 在 infrastructure/persistence/register.go 中替换本实现即可，其余各层无需改动。
type RetrieverStub struct{}

// NewRetrieverStub 创建检索占位实现
func NewRetrieverStub() knowledgerepo.IKnowledgeRetriever {
	return &RetrieverStub{}
}

// Retrieve 返回未实现错误
func (s *RetrieverStub) Retrieve(ctx context.Context, q knowledgerepo.RetrieveQuery) ([]*knowledgerepo.RetrievedChunk, error) {
	return nil, apperrors.ErrKnowledgeRetrievalNotImpl
}
