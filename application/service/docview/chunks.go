// 本文件是按文档枚举切片：GET /api/v1/docs/chunks。

package docview

import (
	"context"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/repository"
)

// Chunks 枚举某文档在 ES 里的切片。
//
// ⚠️ total=0 是**合法状态**，不是错误：ES 的可见性有两档延迟，刚导入的文档
// 原文立刻能看、切片要等下一轮刷新（最长 30s）。页面要明说这件事，
// 而不是显示「没有切片」。
func (s *Service) Chunks(
	ctx context.Context, q *dto.ChunkListQuery,
) (*vo.ChunkListVO, error) {
	// 顺序不能反：先从文档行确认这篇在不在这个 tenant 下，再拿 tenant+doc_id
	// 去枚举。ES 侧的 buildFilter 只在三个条件**全空**时拒绝，不替我们保证
	// tenant 在场——租户隔离靠这里的调用约定
	if _, err := s.liveDoc(ctx, q.DocID, q.Tenant); err != nil {
		return nil, err
	}

	q.Normalize()
	hits, total, err := s.store.ListByDoc(ctx, repository.VectorFilter{
		Tenant: q.Tenant, DocID: q.DocID,
	}, q.Offset, q.Limit)
	if err != nil {
		return nil, err
	}

	items := make([]vo.ChunkItemVO, 0, len(hits))
	for _, h := range hits {
		items = append(items, vo.ChunkItemVO{
			ChunkID:     h.ChunkID,
			Order:       h.Order,
			Title:       h.Title,
			Content:     h.Content,
			HeadingPath: h.HeadingPath,
		})
	}
	return &vo.ChunkListVO{Total: total, Limit: q.Limit, Offset: q.Offset, Items: items}, nil
}
