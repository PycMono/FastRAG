package persistence

import (
	"context"
	"errors"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/repository"
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
// TransProvider 本来就同时实现了这两个接口，fx 里各 provide 一份即可。
type KnowledgeDocRepo struct {
	provider sqlsdk.Provider
	tm       transaction.Manager
}

func NewKnowledgeDocRepo(
	provider sqlsdk.Provider,
	tm transaction.Manager,
) repository.IKnowledgeDocRepo {
	return &KnowledgeDocRepo{provider: provider, tm: tm}
}

func (r *KnowledgeDocRepo) db(ctx context.Context) *gorm.DB {
	return r.provider.UseDB(ctx).Model(&entity.KnowledgeDoc{})
}

func (r *KnowledgeDocRepo) FindByName(
	ctx context.Context, kbID uint64, name string,
) (*entity.KnowledgeDoc, error) {
	var doc entity.KnowledgeDoc
	err := r.db(ctx).
		Where("kb_id = ? AND name = ? AND delete_ts = 0", kbID, name).
		First(&doc).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 「不存在」是重导入路径上的正常分支，不是错误。
		// 返回 error 会逼着每个调用方写一遍 errors.Is 判断
		return nil, nil
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return &doc, nil
}

func (r *KnowledgeDocRepo) FindByIDs(
	ctx context.Context, ids []uint64,
) (entity.KnowledgeDocs, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var docs entity.KnowledgeDocs
	// 这里刻意**不**加 delete_ts = 0：软删的也要捞回来，
	// 统一交给 AliveMap 判断。带上条件的话，「文档被删了」会退化成
	// 「文档不存在」，两条不同的异常路径混成一条，排查时看不出真相
	if err := r.db(ctx).Where("id IN ?", ids).Find(&docs).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return docs, nil
}

// Save 写入/更新文档行，返回**更新前**的切片数与「是否新建」。
//
// 语义是 upsert，键 (kb_id, name)。刻意不复用 GORM 的 clause.OnConflict 一把梭，
// 而是「先锁行读旧值 → 再 upsert」的两步——因为 oldChunkCount 必须和这次写入
// 原子地取值，否则调用方拿它去算 KB 计数差值时会算错。
//
//	SELECT ... FOR UPDATE  ← 拿行锁 + 旧 chunk_count
//	INSERT ... ON DUPLICATE KEY UPDATE content_hash/chunk_count/update_ts
//
// 只更新那三列，**绝不动 id 和 create_ts**：
//   - id 一动，ES 里那批切片的 doc_id 就指向一个不存在的行了；
//   - create_ts 一动，「文档创建时间」这个对外字段就变成"最后导入时间"。
func (r *KnowledgeDocRepo) Save(
	ctx context.Context, doc *entity.KnowledgeDoc,
) (oldChunkCount int, created bool, err error) {
	err = r.tm.Transaction(ctx, func(txCtx context.Context) error {
		// ① 锁行读旧值。FOR UPDATE 而不是普通 SELECT：后面要拿 oldChunkCount
		//    算差值，读到一半被并发写改了，差值就是错的
		var cur entity.KnowledgeDoc
		e := r.db(txCtx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("kb_id = ? AND name = ? AND delete_ts = 0", doc.KBID, doc.Name).
			First(&cur).Error

		switch {
		case errors.Is(e, gorm.ErrRecordNotFound):
			// 没有活着的同名行 → 本次插入一行新的。oldChunkCount 自然是 0
			//
			// 含「软删后同名重建」：软删那行 delete_ts != 0，不在这条查询的范围内，
			// 唯一键 (kb_id, name, delete_ts) 因此被让了出来，落库的是一行**新 id**
			// 的行。调用方那边 FindByName 同样查不到，于是分配了新雪花——两边一致。
			// 对调用方而言这确实是新建（doc_id 变了），报 created = true 是实话。
			created = true
		case e != nil:
			return apperrors.ErrInternal.Wrap(e)
		default:
			oldChunkCount = cur.ChunkCount
			// 查到了活着的同名行 → 本次是覆盖。
			//
			// 这里**不能**用「主键是不是我刚生成的那个」来判：重导入恰恰是
			// 复用库里那一行的 id，于是 cur.ID == doc.ID 恒成立，
			// 条件退化成恒真，每次重导入都会报 created = true。
			// 后果是 KB 的 doc_count 每重导入一次 +1，越滚越大。
			created = false
		}

		// ② upsert。走 GORM 的 OnConflict 是为了让「并发时在唯一键上等待」
		//    这件事交给数据库，而不是自己写 INSERT ... ON DUPLICATE KEY UPDATE 字符串。
		//
		//    直接 Create(doc)：实体就是持久化模型（合并 po 之后），不用再先拷一份。
		//    doc 是入参指针，GORM 不会改动它——ID 带了 primaryKey 但没有
		//    autoIncrement，没有回写。
		if err := r.db(txCtx).Clauses(clause.OnConflict{
			// MySQL 会忽略 Columns，实际走「任意唯一键冲突即更新」；
			// 写上是为了让意图在代码里读得出来
			Columns: []clause.Column{{Name: "kb_id"}, {Name: "name"}, {Name: "delete_ts"}},
			DoUpdates: clause.Assignments(map[string]any{
				"content_hash": doc.ContentHash,
				"chunk_count":  doc.ChunkCount,
				"update_ts":    doc.UpdateTs,
			}),
		}).Create(doc).Error; err != nil {
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
) (*entity.KnowledgeDoc, error) {
	var doc entity.KnowledgeDoc
	err := r.db(ctx).
		Where("kb_id = ? AND name = ? AND delete_ts = 0", kbID, name).
		First(&doc).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.NewParamError("文档不存在: " + name)
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	// 只置 delete_ts，不挪 name。唯一键是 (kb_id, name, delete_ts)，
	// 置了新时间戳就等于把老键让了出来，同名重导入自然会插一行新的
	if err := r.db(ctx).
		Where("id = ? AND delete_ts = 0", doc.ID).
		Updates(map[string]any{"delete_ts": now, "update_ts": now}).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	doc.DeleteTs, doc.UpdateTs = now, now
	return &doc, nil
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
