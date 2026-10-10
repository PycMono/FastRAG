package repository

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity"
)

// IKnowledgeBaseRepo 知识库仓储（只读）。
//
// KB 由外部预置，本服务不提供写方法。
// 所有方法都带 account 条件——租户条件不漏写由这一层保证。
type IKnowledgeBaseRepo interface {
	// FindByNo 按对外编号加载。
	//
	// account 不匹配与库不存在**返回同一个 ErrKnowledgeBaseNotFound**——
	// 分开的话就是送出一个可枚举的租户探测接口。
	FindByNo(ctx context.Context, no, account string) (*entity.KnowledgeBase, error)

	// FindByNos 批量加载，用于一次检索涉及多个 KB 的场景。
	// 返回结果只含属于 account 且未删除的 KB，缺失的静默跳过。
	FindByNos(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error)

	// ApplyChunkDelta 按差值更新 chunk_count。
	// 用差值而非无条件 +1：重导入时先扣旧值，避免计数漂移。
	ApplyChunkDelta(ctx context.Context, kbID uint64, delta int64) error

	// ApplyDocDelta 按差值更新 doc_count。
	ApplyDocDelta(ctx context.Context, kbID uint64, delta int64) error
}
