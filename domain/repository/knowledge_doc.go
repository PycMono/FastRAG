package repository

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity"
)

// IKnowledgeDocRepo 文档仓储。
type IKnowledgeDocRepo interface {
	// LoadByName 按 (kbID, name) 查文档。查不到返回 (nil, nil)，不是错误——
	// 「不存在」是重导入路径上的正常分支。
	LoadByName(ctx context.Context, kbID uint64, name string) (*entity.KnowledgeDoc, error)

	// LoadByIDs 批量加载，供检索时的存活校验使用。
	//
	// 刻意**不过滤** delete_ts：调用方要靠返回的行区分「不存在」和「已删」两种情况，
	// SQL 里加 delete_ts = 0 会把它们混成一个 (nil, nil)，日志里就查不出是哪种（§7.3）。
	LoadByIDs(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error)

	// Save 写入/更新文档行（§5.2 ③）。
	//
	// 语义是 upsert，键是 (kb_id, name)：doc 不存在则按 doc.ID 建行，
	// 已存在则更新 content_hash / chunk_count / update_ts。**不动 id**——
	// 重导入沿用同一个 doc_id，ES 那边的切片才接得上（§5.3）。
	//
	// 两个返回值都服务于 KB 层的冗余计数：
	//   created       这次是不是新建了行 → 调用方据此决定要不要 +1 KB 的 doc_count；
	//   oldChunkCount **更新前**的切片数 → 和本次的 len(chunks) 算差值。
	//
	// 实现要点：读旧值 + upsert 必须在**同一个事务**里（调用方套 s.tm.Transaction），
	// 否则中途挤进来的另一次导入会让差值算错，KB 计数就永久性偏了（A4.4）。
	Save(ctx context.Context, doc *entity.KnowledgeDoc) (oldChunkCount int, created bool, err error)

	// SoftDelete 按 (kbID, name) 软删，返回受影响的文档。
	SoftDelete(ctx context.Context, kbID uint64, name string, now int64) (*entity.KnowledgeDoc, error)

	// SoftDeleteByKBID 软删某 KB 下所有文档，返回文档数。
	SoftDeleteByKBID(ctx context.Context, kbID uint64, account string, now int64) (int64, error)

	// CountByKBID 统计某 KB 下未删除的文档数，供计数校正使用。
	CountByKBID(ctx context.Context, kbID uint64) (int64, error)

	// SumChunkCountByKBID 汇总某 KB 下未删除文档的切片数，供计数校正使用。
	SumChunkCountByKBID(ctx context.Context, kbID uint64) (int64, error)
}
