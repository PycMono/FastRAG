package repository

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity"
)

// IKnowledgeDocRepo 文档仓储。
type IKnowledgeDocRepo interface {
	// FindByName 按 (kbID, name) 查文档。查不到返回 (nil, nil)，不是错误——
	// 「不存在」是重导入路径上的正常分支。
	FindByName(ctx context.Context, kbID uint64, name string) (*entity.KnowledgeDoc, error)

	// FindByIDs 批量加载，供检索时的存活校验使用。
	//
	// 刻意**不加任何过滤**：调用方要的就是「这批 doc_id 里哪些还有行」。
	// 查不到 = 孤儿或幽灵切片，由 AliveMap 之后的判断丢弃。
	FindByIDs(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error)

	// Save 写入/更新文档行。
	//
	// 语义是 upsert，键是 (kb_id, name)：doc 不存在则按 doc.ID 建行，
	// 已存在则更新 content_hash / chunk_count / update_ts。**不动 id**——
	// 重导入沿用同一个 doc_id，ES 那边的切片才接得上。
	//
	// 两个返回值都服务于 KB 层的冗余计数：
	//   created       这次是不是新建了行 → 调用方据此决定要不要 +1 KB 的 doc_count；
	//   oldChunkCount **更新前**的切片数 → 和本次的 len(chunks) 算差值。
	//
	// 实现要点：读旧值 + upsert 必须在**同一个事务**里（调用方套 s.tm.Transaction），
	// 否则中途挤进来的另一次导入会让差值算错，KB 计数就永久性偏了。
	Save(ctx context.Context, doc *entity.KnowledgeDoc) (oldChunkCount int, created bool, err error)

	// Delete 按 (kbID, name) 硬删，返回被删掉的那一行。
	//
	// 返回整行不是为了留痕——行已经没了——而是调用方需要它的 ChunkCount
	// 去扣 KB 的冗余计数。删完就取不到了，所以必须先查后删。
	Delete(ctx context.Context, kbID uint64, name string) (*entity.KnowledgeDoc, error)

	// DeleteByKBID 硬删某 KB 下的所有文档，返回删除的行数。
	DeleteByKBID(ctx context.Context, kbID uint64, account string) (int64, error)
}
