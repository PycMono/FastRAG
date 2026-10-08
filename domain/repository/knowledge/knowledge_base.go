package knowledge

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity/knowledge"
)

// IKnowledgeBaseRepo 知识库仓储接口
// 除 Create 外，所有按 ID 的操作都必须同时带上 userID 条件，避免越权访问
type IKnowledgeBaseRepo interface {
	Create(ctx context.Context, kb *knowledge.KnowledgeBase) error
	GetByIDAndUserID(ctx context.Context, id, userID string) (*knowledge.KnowledgeBase, error)
	ListByUserID(ctx context.Context, userID string, page, pageSize int) (total int64, list []*knowledge.KnowledgeBase, err error)
	Update(ctx context.Context, kb *knowledge.KnowledgeBase) error
	DeleteByIDAndUserID(ctx context.Context, id, userID string) error
}
