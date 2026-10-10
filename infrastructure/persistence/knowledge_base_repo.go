package persistence

import (
	"context"
	"errors"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/repository"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"gorm.io/gorm"
)

// KnowledgeBaseRepo 知识库仓储（只读）。
//
// 这一层是租户隔离的最后一道闸：所有语句都带 account = ?，
// 漏写在 code review 里能看出来，靠每个用例自己记得加则看不出来。
type KnowledgeBaseRepo struct {
	provider sqlsdk.Provider
}

func NewKnowledgeBaseRepo(provider sqlsdk.Provider) repository.IKnowledgeBaseRepo {
	return &KnowledgeBaseRepo{provider: provider}
}

func (r *KnowledgeBaseRepo) db(ctx context.Context) *gorm.DB {
	return r.provider.UseDB(ctx).Model(&entity.KnowledgeBase{})
}

func (r *KnowledgeBaseRepo) FindByNo(
	ctx context.Context, no, account string,
) (*entity.KnowledgeBase, error) {
	var kb entity.KnowledgeBase
	err := r.db(ctx).
		Where("no = ? AND account = ? AND delete_ts = 0", no, account).
		First(&kb).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 「编号不存在」和「编号存在但不属于你」返回同一个错误。
		// 区分开的话，接口就成了租户枚举器：拿别人的 kb_no 反复试，
		// 就能从错误信息里推断出哪些编号真实存在
		return nil, apperrors.ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return &kb, nil
}

func (r *KnowledgeBaseRepo) FindByNos(
	ctx context.Context, nos []string, account string,
) (entity.KnowledgeBases, error) {
	if len(nos) == 0 {
		return nil, nil
	}

	// 直接 Find 进 KnowledgeBases（[]*KnowledgeBase），GORM 逐个分配指针，
	// 省掉「先扫进 []struct 再转一遍指针」的循环
	var kbs entity.KnowledgeBases
	if err := r.db(ctx).
		Where("no IN ? AND account = ? AND delete_ts = 0", nos, account).
		Find(&kbs).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	// 查不到的静默跳过：调用方要的是「这些库里能查到的部分」，
	// 其中一个编号写错不该让整次检索失败
	return kbs, nil
}

func (r *KnowledgeBaseRepo) ApplyChunkDelta(ctx context.Context, kbID uint64, delta int64) error {
	if delta == 0 {
		return nil
	}
	return r.applyDelta(ctx, kbID, "chunk_count", delta)
}

func (r *KnowledgeBaseRepo) ApplyDocDelta(ctx context.Context, kbID uint64, delta int64) error {
	if delta == 0 {
		return nil
	}
	return r.applyDelta(ctx, kbID, "doc_count", delta)
}

func (r *KnowledgeBaseRepo) applyDelta(ctx context.Context, kbID uint64, column string, delta int64) error {
	// GREATEST(..., 0) 是这里真正的兜底：「计数只增不减」这个业务假设一旦破了
	// （重导入的负增量比现有值还大），计数也只会停在 0，不会掉成负数。
	//
	// CAST(... AS SIGNED) 目前是冗余的 —— 列本身就是有符号 BIGINT（见
	// scripts/schema.sql）。留着是为了将来万一有人把列改成 UNSIGNED：那时少了它，
	// 负增量会直接报 "BIGINT UNSIGNED value is out of range"
	err := r.db(ctx).
		Where("id = ?", kbID).
		Updates(map[string]any{
			column:      gorm.Expr("GREATEST(CAST("+column+" AS SIGNED) + ?, 0)", delta),
			"update_ts": time.Now().UnixMilli(),
		}).Error
	if err != nil {
		return apperrors.ErrInternal.Wrap(err)
	}
	return nil
}
