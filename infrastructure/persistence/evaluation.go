package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/repository"
	sqlsdk "github.com/PycMono/go-mysql-sdk"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EvalDatasetRepo 已发布题库；一个版本一行，发布后不覆盖。
type EvalDatasetRepo struct {
	provider sqlsdk.Provider
}

// NewEvalDatasetRepo 构造题库仓储。
func NewEvalDatasetRepo(provider sqlsdk.Provider) repository.IEvalDatasetRepo {
	return &EvalDatasetRepo{provider: provider}
}

// EvalRunRepo 评测任务元数据与逐题结果。
type EvalRunRepo struct {
	provider sqlsdk.Provider
}

// NewEvalRunRepo 构造任务仓储。
func NewEvalRunRepo(provider sqlsdk.Provider) repository.IEvalRunRepo {
	return &EvalRunRepo{provider: provider}
}

// List 返回未退休题库的列表信息，不加载完整快照。
func (d *EvalDatasetRepo) List(ctx context.Context) ([]entity.EvalDatasetInfo, error) {
	var records []entity.EvalDatasetRecord
	err := d.db(ctx).
		Select("name, version, tenant, kb_nos, cases_count, profiles_count, profile_summary, search_requests, created_ms").
		Where("retired = ?", false).
		Order("created_ms DESC, name ASC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}

	result := make([]entity.EvalDatasetInfo, 0, len(records))
	for _, record := range records {
		kbNos := []string{}
		if err := json.Unmarshal([]byte(record.KBNos), &kbNos); err != nil {
			return nil, fmt.Errorf("题库 %s 的知识库列表损坏: %w", record.Name, err)
		}
		result = append(result, entity.EvalDatasetInfo{
			Name:           record.Name,
			Version:        record.Version,
			Tenant:         record.Tenant,
			KBNos:          kbNos,
			Cases:          record.CasesCount,
			Profiles:       record.ProfilesCount,
			ProfileSummary: record.ProfileSummary,
			SearchRequests: record.SearchRequests,
		})
	}
	return result, nil
}

// Load 读取一个未退休题库的完整快照。
func (d *EvalDatasetRepo) Load(ctx context.Context, name string) (entity.EvalDataset, error) {
	var record entity.EvalDatasetRecord
	err := d.db(ctx).
		Where("name = ? AND retired = ?", name, false).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.EvalDataset{}, os.ErrNotExist
	}
	if err != nil {
		return entity.EvalDataset{}, err
	}

	var dataset entity.EvalDataset
	dataset.Name = record.Name
	if err := decodeJSON(record.Manifest, &dataset.Manifest); err != nil {
		return dataset, err
	}
	if err := decodeJSON(record.Profiles, &dataset.Profiles); err != nil {
		return dataset, err
	}
	if err := decodeJSON(record.Cases, &dataset.Cases); err != nil {
		return dataset, err
	}
	return dataset, nil
}

// Create 写入任务元数据和题库快照，不写逐题结果。
func (r *EvalRunRepo) Create(ctx context.Context, run entity.EvalRun) error {
	record, err := runRecord(run)
	if err != nil {
		return err
	}
	return r.db(ctx).Create(&record).Error
}

// SaveMeta 更新任务状态、进度、失败计数和汇总。
func (r *EvalRunRepo) SaveMeta(ctx context.Context, run entity.EvalRun) error {
	summary, err := encodeJSON(run.Summary)
	if err != nil {
		return err
	}

	var finished *int64
	if run.FinishedAt != nil {
		value := run.FinishedAt.UnixMilli()
		finished = &value
	}

	updates := map[string]any{
		"status":             run.Status,
		"error":              run.Error,
		"done":               run.Done,
		"retrieval_failures": run.RetrievalFailures,
		"boundary_failures":  run.BoundaryFailures,
		"rerank_degraded":    run.RerankDegraded,
		"scoring_failures":   run.ScoringFailures,
		"summary":            summary,
		"finished_ms":        finished,
	}
	return r.db(ctx).Where("id = ?", run.ID).Updates(updates).Error
}

// SaveRow 按 (run_id, case_id, profile) 写入或更新单题结果。
func (r *EvalRunRepo) SaveRow(ctx context.Context, runID string, row entity.EvalRow) error {
	payload, err := encodeJSON(row)
	if err != nil {
		return err
	}
	record := entity.EvalRunRowRecord{
		RunID:   runID,
		CaseID:  row.CaseID,
		Profile: row.Profile,
		Payload: payload,
	}
	return r.provider.UseDB(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "run_id"}, {Name: "case_id"}, {Name: "profile"}},
			DoUpdates: clause.AssignmentColumns([]string{"payload"}),
		}).
		Create(&record).Error
}

// Get 读取任务元数据、快照和逐题结果。
func (r *EvalRunRepo) Get(ctx context.Context, id string) (entity.EvalRun, error) {
	var record entity.EvalRunRecord
	err := r.db(ctx).Where("id = ?", id).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.EvalRun{}, os.ErrNotExist
	}
	if err != nil {
		return entity.EvalRun{}, err
	}

	run, err := runFromRecord(record)
	if err != nil {
		return run, err
	}

	var rows []entity.EvalRunRowRecord
	if err := r.provider.UseDB(ctx).
		Model(&entity.EvalRunRowRecord{}).
		Where("run_id = ?", id).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return run, err
	}
	run.Rows = make([]entity.EvalRow, 0, len(rows))
	for _, row := range rows {
		var value entity.EvalRow
		if err := decodeJSON(row.Payload, &value); err != nil {
			return run, fmt.Errorf("任务 %s 的逐题结果损坏: %w", id, err)
		}
		run.Rows = append(run.Rows, value)
	}
	return run, nil
}

// List 返回未退休题库的列表信息，不加载完整快照。
func (r *EvalRunRepo) List(ctx context.Context) ([]entity.EvalRun, error) {
	var records []entity.EvalRunRecord
	err := r.db(ctx).
		Select("id, dataset_name, experiment_name, purpose, status, error, profiles, expected, done, " +
			"retrieval_failures, boundary_failures, rerank_degraded, scoring_failures, summary, created_ms, finished_ms").
		Order("created_ms DESC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}

	result := make([]entity.EvalRun, 0, len(records))
	for _, record := range records {
		run, err := runBriefFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, nil
}

// Delete 删除任务；逐题结果、基线和报告由外键级联删除。
func (r *EvalRunRepo) Delete(ctx context.Context, id string) error {
	result := r.db(ctx).Where("id = ?", id).Delete(&entity.EvalRunRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return os.ErrNotExist
	}
	return nil
}

// Recover 把重启前未结束的任务标记为 interrupted。
func (r *EvalRunRepo) Recover(ctx context.Context) error {
	now := time.Now().UTC().UnixMilli()
	return r.db(ctx).
		Where("status IN ?", []string{"queued", "running"}).
		Updates(map[string]any{
			"status":      "interrupted",
			"error":       "服务已重启，不自动续跑",
			"finished_ms": now,
		}).Error
}

// Purge 删除保留期外且不是基线的任务。
func (r *EvalRunRepo) Purge(ctx context.Context, before time.Time) (int64, error) {
	baselines := r.provider.UseDB(ctx).
		Model(&entity.EvalBaselineRecord{}).
		Select("run_id")

	result := r.db(ctx).
		Where("created_ms < ? AND id NOT IN (?)", before.UnixMilli(), baselines).
		Delete(&entity.EvalRunRecord{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// ── 内部实现 ──

// db 返回带上下文的查询入口。
func (d *EvalDatasetRepo) db(ctx context.Context) *gorm.DB {
	return d.provider.UseDB(ctx).Model(&entity.EvalDatasetRecord{})
}

// publishDataset 写入一个不可变题库版本；同名版本已存在时保留旧版本。
func publishDataset(ctx context.Context, db *gorm.DB, dataset entity.EvalDataset) error {
	record, err := datasetRecord(dataset)
	if err != nil {
		return err
	}
	return db.Create(&record).Error
}

// datasetRecord 把题库快照转成表记录。
func datasetRecord(dataset entity.EvalDataset) (entity.EvalDatasetRecord, error) {
	manifest, err := encodeJSON(dataset.Manifest)
	if err != nil {
		return entity.EvalDatasetRecord{}, err
	}
	profiles, err := encodeJSON(dataset.Profiles)
	if err != nil {
		return entity.EvalDatasetRecord{}, err
	}
	cases, err := encodeJSON(dataset.Cases)
	if err != nil {
		return entity.EvalDatasetRecord{}, err
	}

	info := datasetInfo(dataset)
	kbNos, err := encodeJSON(info.KBNos)
	if err != nil {
		return entity.EvalDatasetRecord{}, err
	}
	return entity.EvalDatasetRecord{
		Name:           dataset.Name,
		Version:        info.Version,
		Tenant:         info.Tenant,
		KBNos:          kbNos,
		CasesCount:     info.Cases,
		ProfilesCount:  info.Profiles,
		ProfileSummary: info.ProfileSummary,
		SearchRequests: info.SearchRequests,
		Manifest:       manifest,
		Profiles:       profiles,
		Cases:          cases,
		Retired:        false,
		CreatedMS:      time.Now().UTC().UnixMilli(),
	}, nil
}

// datasetInfo 从题库快照提取列表字段。
func datasetInfo(dataset entity.EvalDataset) entity.EvalDatasetInfo {
	info := entity.EvalDatasetInfo{
		Name:           dataset.Name,
		Version:        dataset.Manifest.DatasetVersion,
		Cases:          len(dataset.Cases),
		Profiles:       len(dataset.Profiles),
		SearchRequests: len(dataset.Cases) * len(dataset.Profiles),
	}
	if len(dataset.Profiles) > 0 {
		info.ProfileSummary = profileSummary(dataset.Profiles)
	}
	if len(dataset.Cases) > 0 {
		info.Tenant = dataset.Cases[0].Tenant
		info.KBNos = dataset.Cases[0].KBNos
	}
	return info
}

// profileSummary 生成题库检索配置的一行摘要。
func profileSummary(profiles []entity.EvalProfile) string {
	if len(profiles) == 0 {
		return ""
	}
	profile := profiles[0]
	rerank := "精排关"
	if profile.RerankSwitch {
		rerank = "精排开"
	}
	summary := fmt.Sprintf(
		"%s · 返回 %d 条 · 召回 %d 条 · %s",
		profile.Name, profile.Limit, profile.RetrieveCount, rerank,
	)
	if len(profiles) > 1 {
		summary += fmt.Sprintf(" · 共 %d 组配置", len(profiles))
	}
	return summary
}

// db 返回带上下文的查询入口。
func (r *EvalRunRepo) db(ctx context.Context) *gorm.DB {
	return r.provider.UseDB(ctx).Model(&entity.EvalRunRecord{})
}

// runRecord 把任务转成表记录。
func runRecord(run entity.EvalRun) (entity.EvalRunRecord, error) {
	metadata, err := encodeJSON(run.Metadata)
	if err != nil {
		return entity.EvalRunRecord{}, err
	}
	snapshot, err := encodeJSON(run.Dataset)
	if err != nil {
		return entity.EvalRunRecord{}, err
	}
	profiles, err := encodeJSON(run.Dataset.Profiles)
	if err != nil {
		return entity.EvalRunRecord{}, err
	}
	summary, err := encodeJSON(run.Summary)
	if err != nil {
		return entity.EvalRunRecord{}, err
	}

	var finished *int64
	if run.FinishedAt != nil {
		value := run.FinishedAt.UnixMilli()
		finished = &value
	}

	return entity.EvalRunRecord{
		ID:                run.ID,
		DatasetName:       run.Dataset.Name,
		ExperimentName:    run.ExperimentName,
		Hypothesis:        run.Hypothesis,
		Purpose:           run.Purpose,
		Status:            run.Status,
		Error:             run.Error,
		ConfigHash:        run.ConfigHash,
		Metadata:          metadata,
		DatasetSnapshot:   snapshot,
		Profiles:          profiles,
		Expected:          run.Expected,
		Done:              run.Done,
		RetrievalFailures: run.RetrievalFailures,
		BoundaryFailures:  run.BoundaryFailures,
		RerankDegraded:    run.RerankDegraded,
		ScoringFailures:   run.ScoringFailures,
		Summary:           summary,
		CreatedMS:         run.CreatedAt.UnixMilli(),
		FinishedMS:        finished,
	}, nil
}

// runFromRecord 还原完整任务和快照。
func runFromRecord(record entity.EvalRunRecord) (entity.EvalRun, error) {
	run, err := runBriefFromRecord(record)
	if err != nil {
		return run, err
	}

	if err := decodeJSON(record.Metadata, &run.Metadata); err != nil {
		return run, err
	}
	if err := decodeJSON(record.DatasetSnapshot, &run.Dataset); err != nil {
		return run, err
	}
	run.Hypothesis = record.Hypothesis
	run.ConfigHash = record.ConfigHash
	return run, nil
}

// runBriefFromRecord 还原列表所需的任务字段，不加载逐题结果。
func runBriefFromRecord(record entity.EvalRunRecord) (entity.EvalRun, error) {
	var summary map[string]entity.EvalSummary
	if err := decodeJSON(record.Summary, &summary); err != nil {
		return entity.EvalRun{}, err
	}
	var profiles []entity.EvalProfile
	if err := decodeJSON(record.Profiles, &profiles); err != nil {
		return entity.EvalRun{}, err
	}

	run := entity.EvalRun{
		ID:                record.ID,
		ExperimentName:    record.ExperimentName,
		Purpose:           record.Purpose,
		Status:            record.Status,
		Error:             record.Error,
		CreatedAt:         fromMillis(record.CreatedMS),
		Dataset:           entity.EvalDataset{Name: record.DatasetName, Profiles: profiles},
		Expected:          record.Expected,
		Done:              record.Done,
		RetrievalFailures: record.RetrievalFailures,
		BoundaryFailures:  record.BoundaryFailures,
		RerankDegraded:    record.RerankDegraded,
		ScoringFailures:   record.ScoringFailures,
		Summary:           summary,
	}
	if record.FinishedMS != nil {
		finished := fromMillis(*record.FinishedMS)
		run.FinishedAt = &finished
	}
	return run, nil
}

// encodeJSON 把值编码成数据库里的 JSON 文本。
func encodeJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// decodeJSON 解析数据库里的 JSON 文本。
func decodeJSON(data string, target any) error {
	return json.Unmarshal([]byte(data), target)
}

// fromMillis 把毫秒时间戳转成 UTC 时间。
func fromMillis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

var (
	_ repository.IEvalDatasetRepo = (*EvalDatasetRepo)(nil)
	_ repository.IEvalRunRepo     = (*EvalRunRepo)(nil)
)
