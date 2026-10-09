package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/infrastructure/persistence/mapper"
	"github.com/PycMono/FastRAG/infrastructure/persistence/po"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KnowledgeDocRepo 文档仓储。
//
// 为什么还要注入 transaction.Manager：Save 是「锁行读旧值 → upsert」两步，
// 必须整体在一个事务里（见 Save 注释）。sqlsdk.Provider 只暴露 UseDB，
// 给不了事务，所以额外依赖 Manager。
// TransProvider 本来就同时实现了这两个接口，fx 里各 provide 一份即可（A4.16）。
type KnowledgeDocRepo struct {
	provider sqlsdk.Provider
	tm       transaction.Manager
}

func NewKnowledgeDocRepo(
	provider sqlsdk.Provider,
	tm transaction.Manager,
) knowledgerepo.IKnowledgeDocRepo {
	return &KnowledgeDocRepo{provider: provider, tm: tm}
}

func (r *KnowledgeDocRepo) db(ctx context.Context) *gorm.DB {
	return r.provider.UseDB(ctx).Model(&po.KnowledgeDoc{})
}

func (r *KnowledgeDocRepo) LoadByName(
	ctx context.Context, kbID uint64, name string,
) (*knowledgeentity.KnowledgeDoc, error) {
	var row po.KnowledgeDoc
	err := r.db(ctx).
		Where("kb_id = ? AND name = ? AND delete_ts = 0", kbID, name).
		First(&row).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 「不存在」是重导入路径上的正常分支，不是错误。
		// 返回 error 会逼着每个调用方写一遍 errors.Is 判断
		return nil, nil
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return mapper.ToKnowledgeDoc(&row), nil
}

func (r *KnowledgeDocRepo) LoadByIDs(
	ctx context.Context, ids []uint64,
) (knowledgeentity.KnowledgeDocs, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var rows []po.KnowledgeDoc
	// 这里刻意**不**加 delete_ts = 0：软删的也要捞回来，
	// 统一交给 AliveMap 判断。带上条件的话，「文档被删了」会退化成
	// 「文档不存在」，两条不同的异常路径混成一条，排查时看不出真相
	if err := r.db(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return mapper.ToKnowledgeDocs(rows), nil
}

// Save 写入/更新文档行，返回**更新前**的切片数与「是否新建」（§5.2 ③）。
//
// 语义是 upsert，键 (kb_id, name)。刻意不复用 GORM 的 clause.OnConflict 一把梭，
// 而是「先锁行读旧值 → 再 upsert」的两步——因为 oldChunkCount 必须和这次写入
// 原子地取值，否则调用方拿它去算 KB 计数差值时会算错（§5.1 第 ⑧ 步）。
//
//	SELECT ... FOR UPDATE  ← 拿行锁 + 旧 chunk_count
//	INSERT ... ON DUPLICATE KEY UPDATE content_hash/chunk_count/update_ts
//
// 只更新那三列，**绝不动 id 和 create_ts**：
//   - id 一动，ES 里那批切片的 doc_id 就指向一个不存在的行了；
//   - create_ts 一动，「文档创建时间」这个对外字段就变成"最后导入时间"。
func (r *KnowledgeDocRepo) Save(
	ctx context.Context, doc *knowledgeentity.KnowledgeDoc,
) (oldChunkCount int, created bool, err error) {
	row := mapper.ToKnowledgeDocPO(doc)

	err = r.tm.Transaction(ctx, func(txCtx context.Context) error {
		// ① 锁行读旧值。FOR UPDATE 而不是普通 SELECT：后面要拿 oldChunkCount
		//    算差值，读到一半被并发写改了，差值就是错的
		var cur po.KnowledgeDoc
		e := r.db(txCtx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("kb_id = ? AND name = ? AND delete_ts = 0", doc.KBID, doc.Name).
			First(&cur).Error

		switch {
		case errors.Is(e, gorm.ErrRecordNotFound):
			// 行不存在 → 新建。oldChunkCount 自然是 0
			created = true
		case e != nil:
			return apperrors.ErrInternal.Wrap(e)
		default:
			oldChunkCount = cur.ChunkCount
			// created 的判据是「主键是不是我刚生成的那个」，而不是"查不到行"——
			// 一行被软删后同名重建时同样查不到，但那不是新建，是重导
			created = cur.Id == row.Id
		}

		// ② upsert。走 GORM 的 OnConflict 是为了让「并发时在唯一键上等待」
		//    这件事交给数据库，而不是自己写 INSERT ... ON DUPLICATE KEY UPDATE 字符串
		if err := r.db(txCtx).Clauses(clause.OnConflict{
			// MySQL 会忽略 Columns，实际走「任意唯一键冲突即更新」；
			// 写上是为了让意图在代码里读得出来
			Columns: []clause.Column{{Name: "kb_id"}, {Name: "name"}, {Name: "delete_ts"}},
			DoUpdates: clause.Assignments(map[string]any{
				"content_hash": doc.ContentHash,
				"chunk_count":  doc.ChunkCount,
				"update_ts":    doc.UpdateTs,
			}),
		}).Create(row).Error; err != nil {
			return apperrors.ErrInternal.Wrap(err)
		}
		return nil
	})

	if err != nil {
		return 0, false, err
	}
	return oldChunkCount, created, nil
}

func (r *KnowledgeDocRepo) SoftDelete(
	ctx context.Context, kbID uint64, name string, now int64,
) (*knowledgeentity.KnowledgeDoc, error) {
	var row po.KnowledgeDoc
	err := r.db(ctx).
		Where("kb_id = ? AND name = ? AND delete_ts = 0", kbID, name).
		First(&row).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.NewParamError("文档不存在: " + name)
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	// 只置 delete_ts，不挪 name。唯一键是 (kb_id, name, delete_ts)，
	// 置了新时间戳就等于把老键让了出来，同名重导入自然会插一行新的
	if err := r.db(ctx).
		Where("id = ? AND delete_ts = 0", row.Id).
		Updates(map[string]any{"delete_ts": now, "update_ts": now}).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	row.DeleteTs, row.UpdateTs = now, now
	return mapper.ToKnowledgeDoc(&row), nil
}

func (r *KnowledgeDocRepo) SoftDeleteByKBID(
	ctx context.Context, kbID uint64, account string, now int64,
) (int64, error) {
	tx := r.db(ctx).
		Where("kb_id = ? AND account = ? AND delete_ts = 0", kbID, account).
		Updates(map[string]any{"delete_ts": now, "update_ts": now})
	if tx.Error != nil {
		return 0, apperrors.ErrInternal.Wrap(tx.Error)
	}
	return tx.RowsAffected, nil
}

func (r *KnowledgeDocRepo) CountByKBID(ctx context.Context, kbID uint64) (int64, error) {
	var n int64
	if err := r.db(ctx).
		Where("kb_id = ? AND delete_ts = 0", kbID).
		Count(&n).Error; err != nil {
		return 0, apperrors.ErrInternal.Wrap(err)
	}
	return n, nil
}

func (r *KnowledgeDocRepo) SumChunkCountByKBID(ctx context.Context, kbID uint64) (int64, error) {
	// COALESCE 不能省：一个文档都没有时 SUM 返回 NULL，
	// 扫进 int64 会直接报 "converting NULL to int64 is unsupported"
	var sum sql.NullInt64
	if err := r.db(ctx).
		Select("COALESCE(SUM(chunk_count), 0) AS total").
		Where("kb_id = ? AND delete_ts = 0", kbID).
		Scan(&sum).Error; err != nil {
		return 0, apperrors.ErrInternal.Wrap(err)
	}
	return sum.Int64, nil
}
