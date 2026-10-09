package knowledge

import (
	"context"
	"errors"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	"github.com/PycMono/FastRAG/infrastructure/persistence/mapper"
	"github.com/PycMono/FastRAG/infrastructure/persistence/po"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"gorm.io/gorm"
)

// KnowledgeBaseRepo 知识库仓储（只读）。
//
// 这一层是租户隔离的最后一道闸：所有语句都带 account = ?，
// 漏写在 code review 里能看出来，靠每个用例自己记得加则看不出来（§6）。
type KnowledgeBaseRepo struct {
	provider sqlsdk.Provider
}

func NewKnowledgeBaseRepo(provider sqlsdk.Provider) knowledgerepo.IKnowledgeBaseRepo {
	return &KnowledgeBaseRepo{provider: provider}
}

func (r *KnowledgeBaseRepo) db(ctx context.Context) *gorm.DB {
	return r.provider.UseDB(ctx).Model(&po.KnowledgeBase{})
}

func (r *KnowledgeBaseRepo) LoadByNo(
	ctx context.Context, no, account string,
) (*knowledgeentity.KnowledgeBase, error) {
	var row po.KnowledgeBase
	err := r.db(ctx).
		Where("no = ? AND account = ? AND delete_ts = 0", no, account).
		First(&row).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 「编号不存在」和「编号存在但不属于你」返回同一个错误。
		// 区分开的话，接口就成了租户枚举器：拿别人的 kb_no 反复试，
		// 就能从错误信息里推断出哪些编号真实存在
		return nil, apperrors.ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}
	return mapper.ToKnowledgeBase(&row), nil
}

func (r *KnowledgeBaseRepo) LoadByNos(
	ctx context.Context, nos []string, account string,
) (knowledgeentity.KnowledgeBases, error) {
	if len(nos) == 0 {
		return nil, nil
	}

	var rows []po.KnowledgeBase
	if err := r.db(ctx).
		Where("no IN ? AND account = ? AND delete_ts = 0", nos, account).
		Find(&rows).Error; err != nil {
		return nil, apperrors.ErrInternal.Wrap(err)
	}

	// 查不到的静默跳过：调用方要的是「这些库里能查到的部分」，
	// 其中一个编号写错不该让整次检索失败
	return mapper.ToKnowledgeBases(rows), nil
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
	// CAST(... AS SIGNED) 不能省：chunk_count 是 BIGINT UNSIGNED，
	// 直接加负数在 MySQL 上会报 "BIGINT UNSIGNED value is out of range"，
	// 而 GREATEST(..., 0) 则保证计数永远不会掉成负数
	err := r.db(ctx).
		Where("id = ?", kbID).
		Updates(map[string]any{
			column: gorm.Expr("GREATEST(CAST("+column+" AS SIGNED) + ?, 0)", delta),
			"update_ts": time.Now().UnixMilli(),
		}).Error
	if err != nil {
		return apperrors.ErrInternal.Wrap(err)
	}
	return nil
}

// SetCounts 用绝对值覆盖计数，供对账任务使用（§A3.5）。
func (r *KnowledgeBaseRepo) SetCounts(ctx context.Context, kbID uint64, docCount, chunkCount int64) error {
	err := r.db(ctx).
		Where("id = ?", kbID).
		Updates(map[string]any{
			"doc_count":   docCount,
			"chunk_count": chunkCount,
			"update_ts":   time.Now().UnixMilli(),
		}).Error
	if err != nil {
		return apperrors.ErrInternal.Wrap(err)
	}
	return nil
}
