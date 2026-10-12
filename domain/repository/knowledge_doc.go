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

	// FindByID 按主键加载**整行**（含整篇正文），供详情接口使用。
	//
	// 与 FindByIDs 分开，是因为两者要的列不同：详情要正文，存活校验不要。
	// 合成一个方法，就会让每次检索都把命中文档的全文读回内存（见 FindByIDs）。
	// 查不到返回 (nil, nil)，与 FindByName 同口径——是不是「不存在」由调用方判。
	FindByID(ctx context.Context, id uint64) (*entity.KnowledgeDoc, error)

	// FindByIDs 批量加载，供检索时的存活校验使用。
	//
	// 刻意**不加任何过滤**：调用方要的就是「这批 doc_id 里哪些还有行」。
	// 查不到 = 孤儿或幽灵切片，由 AliveMap 之后的判断丢弃。
	//
	// **刻意不返回 content**：一次检索会捞几十篇文档，而单篇正文上限
	// 8 MiB（constants.MaxContentBytes），带上它就是把几十 MB 读进内存，
	// 只为回答「这行还在不在」。要正文走 FindByID。
	FindByIDs(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error)

	// ListByKB 列某库下的文档，返回当页的行与总数，供分页使用。
	//
	// 排序 `name ASC`：uk_kb_name (kb_id, name) 的左前缀正好覆盖 kb_id 过滤、
	// 排序也走索引，翻页稳定、跨页不乱序。**不返回 content**——列表不背正文
	// （一页几十篇、每篇可能上百 KB，背正文会让响应体没有上界），正文归详情接口。
	// 有没有正文由 HasContent 给出，那也是 SQL 侧的表达式，不是把 content 读回来再判空。
	ListByKB(ctx context.Context, kbID uint64, offset, limit int) (entity.KnowledgeDocs, int64, error)

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
