package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/repository"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EvalReviewRepo 题库草稿、基线、验收报告与发布入库。
type EvalReviewRepo struct {
	provider sqlsdk.Provider
	tm       transaction.Manager
}

// NewEvalReviewRepo 构造题库标注仓储。
func NewEvalReviewRepo(
	provider sqlsdk.Provider,
	tm transaction.Manager,
) repository.IEvalReviewRepo {
	return &EvalReviewRepo{provider: provider, tm: tm}
}

// ListDrafts 返回全部草稿，按创建时间倒序。
func (r *EvalReviewRepo) ListDrafts(ctx context.Context) ([]entity.EvalDraft, error) {
	var records []entity.EvalDraftRecord
	if err := r.db(ctx).
		Model(&entity.EvalDraftRecord{}).
		Order("created_ms DESC").
		Find(&records).Error; err != nil {
		return nil, err
	}

	result := make([]entity.EvalDraft, 0, len(records))
	for _, record := range records {
		draft, err := draftFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, draft)
	}
	return result, nil
}

// GetDraft 读取单份草稿。
func (r *EvalReviewRepo) GetDraft(ctx context.Context, id string) (entity.EvalDraft, error) {
	var record entity.EvalDraftRecord
	err := r.db(ctx).Where("id = ?", id).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.EvalDraft{}, os.ErrNotExist
	}
	if err != nil {
		return entity.EvalDraft{}, err
	}
	return draftFromRecord(record)
}

// SaveDraft 按版本号插入或更新草稿，版本不符时返回冲突。
func (r *EvalReviewRepo) SaveDraft(ctx context.Context, draft entity.EvalDraft, expected int) error {
	items, err := encodeJSON(draft.Items)
	if err != nil {
		return err
	}

	if expected == 0 {
		record := entity.EvalDraftRecord{
			ID:              draft.ID,
			Tenant:          draft.Tenant,
			KBNo:            draft.KBNo,
			Version:         1,
			AutoGenerate:    draft.AutoGenerate,
			GenerationState: draft.GenerationState,
			GenerationError: draft.GenerationError,
			Items:           items,
			CreatedMS:       time.Now().UTC().UnixMilli(),
		}
		return r.db(ctx).Create(&record).Error
	}

	result := r.db(ctx).
		Model(&entity.EvalDraftRecord{}).
		Where("id = ? AND version = ?", draft.ID, expected).
		Updates(map[string]any{
			"auto_generate":    draft.AutoGenerate,
			"generation_state": draft.GenerationState,
			"generation_error": draft.GenerationError,
			"items":            items,
			"version":          expected + 1,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("草稿已修改，请重新加载")
	}
	return nil
}

// DeleteDraft 删除未发布草稿。
func (r *EvalReviewRepo) DeleteDraft(ctx context.Context, id string) error {
	result := r.db(ctx).
		Model(&entity.EvalDraftRecord{}).
		Where("id = ?", id).
		Delete(&entity.EvalDraftRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return os.ErrNotExist
	}
	return nil
}

// Publish 写入一个不可变题库版本。
func (r *EvalReviewRepo) Publish(ctx context.Context, dataset entity.EvalDataset) error {
	return publishDataset(ctx, r.db(ctx), dataset)
}

// SetBaseline 为题库设置或替换基线任务。
func (r *EvalReviewRepo) SetBaseline(ctx context.Context, dataset, runID string) error {
	record := entity.EvalBaselineRecord{
		DatasetName: dataset,
		RunID:       runID,
		CreatedMS:   time.Now().UTC().UnixMilli(),
	}
	return r.db(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "dataset_name"}},
			DoUpdates: clause.AssignmentColumns([]string{"run_id", "created_ms"}),
		}).
		Create(&record).Error
}

// Baseline 读取题库的基线任务 ID。
func (r *EvalReviewRepo) Baseline(ctx context.Context, dataset string) (string, error) {
	var record entity.EvalBaselineRecord
	err := r.db(ctx).Where("dataset_name = ?", dataset).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", os.ErrNotExist
	}
	if err != nil {
		return "", err
	}
	return record.RunID, nil
}

// SaveComparison 写入一份验收报告。
func (r *EvalReviewRepo) SaveComparison(ctx context.Context, report entity.EvalComparison) error {
	payload, err := encodeJSON(report)
	if err != nil {
		return err
	}
	record := entity.EvalComparisonRecord{
		RunID:      report.RunID,
		BaselineID: report.BaselineID,
		Verdict:    report.Verdict,
		Payload:    payload,
		CreatedMS:  time.Now().UTC().UnixMilli(),
	}
	return r.db(ctx).Create(&record).Error
}

// InvalidateDocument 删除关联草稿题、退休关联题库，不篡改历史任务。
func (r *EvalReviewRepo) InvalidateDocument(
	ctx context.Context,
	tenant, kbNo, docID string,
) error {
	return r.tm.Transaction(ctx, func(txCtx context.Context) error {
		if err := r.invalidateDrafts(txCtx, tenant, kbNo, docID); err != nil {
			return err
		}
		return r.retireDatasets(txCtx, tenant, kbNo, docID)
	})
}

// ── 内部实现 ──

// db 返回带上下文的查询入口。
func (r *EvalReviewRepo) db(ctx context.Context) *gorm.DB {
	return r.provider.UseDB(ctx)
}

// invalidateDrafts 删除文档关联的草稿题，保留其他文档。
func (r *EvalReviewRepo) invalidateDrafts(
	ctx context.Context,
	tenant, kbNo, docID string,
) error {
	var records []entity.EvalDraftRecord
	if err := r.db(ctx).
		Where("tenant = ? AND kb_no = ?", tenant, kbNo).
		Find(&records).Error; err != nil {
		return err
	}

	for _, record := range records {
		var items []entity.EvalCandidate
		if err := decodeJSON(record.Items, &items); err != nil {
			return err
		}

		remaining := make([]entity.EvalCandidate, 0, len(items))
		matched := false
		for _, item := range items {
			if docID == "" || item.DocID == docID {
				matched = true
				continue
			}
			remaining = append(remaining, item)
		}
		if !matched {
			continue
		}

		if len(remaining) == 0 {
			if err := r.db(ctx).
				Where("id = ?", record.ID).
				Delete(&entity.EvalDraftRecord{}).Error; err != nil {
				return err
			}
			continue
		}

		encoded, err := encodeJSON(remaining)
		if err != nil {
			return err
		}
		if err := r.db(ctx).
			Model(&entity.EvalDraftRecord{}).
			Where("id = ? AND version = ?", record.ID, record.Version).
			Updates(map[string]any{
				"items":   encoded,
				"version": record.Version + 1,
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

// retireDatasets 退休引用了该文档的题库版本。
func (r *EvalReviewRepo) retireDatasets(
	ctx context.Context,
	tenant, kbNo, docID string,
) error {
	var records []entity.EvalDatasetRecord
	if err := r.db(ctx).
		Select("name, cases").
		Where("retired = ?", false).
		Find(&records).Error; err != nil {
		return err
	}

	for _, record := range records {
		var cases []entity.EvalCase
		if err := decodeJSON(record.Cases, &cases); err != nil {
			return err
		}
		if !datasetContains(cases, tenant, kbNo, docID) {
			continue
		}
		if err := r.db(ctx).
			Model(&entity.EvalDatasetRecord{}).
			Where("name = ?", record.Name).
			Update("retired", true).Error; err != nil {
			return err
		}
	}
	return nil
}

// datasetContains 判断题库是否引用了指定租户、知识库和文档。
func datasetContains(
	cases []entity.EvalCase,
	tenant, kbNo, docID string,
) bool {
	for _, testCase := range cases {
		if testCase.Tenant != tenant || !containsString(testCase.KBNos, kbNo) {
			continue
		}
		for _, source := range testCase.ReferenceSources {
			if docID == "" || source.DocID == docID {
				return true
			}
		}
	}
	return false
}

// containsString 判断字符串切片是否包含目标值。
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// draftFromRecord 把草稿表记录还原成领域对象。
func draftFromRecord(record entity.EvalDraftRecord) (entity.EvalDraft, error) {
	var items []entity.EvalCandidate
	if err := decodeJSON(record.Items, &items); err != nil {
		return entity.EvalDraft{}, fmt.Errorf("草稿 %s 的题目数据损坏: %w", record.ID, err)
	}
	return entity.EvalDraft{
		AutoGenerate:    record.AutoGenerate,
		GenerationState: record.GenerationState,
		GenerationError: record.GenerationError,
		ID:              record.ID,
		Version:         record.Version,
		Tenant:          record.Tenant,
		KBNo:            record.KBNo,
		CreatedAt:       fromMillis(record.CreatedMS),
		Items:           items,
	}, nil
}

var _ repository.IEvalReviewRepo = (*EvalReviewRepo)(nil)
