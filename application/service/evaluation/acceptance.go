// 本文件是题库验收：基线设置、配对对比与门槛报告。
// 发布见 publish.go，草稿见 drafts.go。

package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
)

// SetBaseline 把完整成功的已复核任务设为题库基线。
func (s *DatasetService) SetBaseline(ctx context.Context, runID string) error {
	run, err := s.tasks.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status != "succeeded" ||
		!run.Dataset.Manifest.Reviewed ||
		run.Purpose != "regression" {
		return fmt.Errorf("%w: 只有已复核题集的完整成功任务可设基线", ErrInvalid)
	}
	for _, row := range run.Rows {
		if row.Score == nil || row.Score.Metadata["evaluation_config_hash"] == nil {
			return fmt.Errorf("%w: 缺少完整裁判版本，不能成为基线", ErrInvalid)
		}
	}
	return s.repo.SetBaseline(ctx, run.Dataset.Name, runID)
}

// Compare 把任务与基线按显式门槛做配对对比。
func (s *DatasetService) Compare(
	ctx context.Context,
	runID string,
	policy entity.EvalPolicy,
) (entity.EvalComparison, error) {
	report := entity.EvalComparison{
		RunID:     runID,
		Verdict:   "insufficient",
		Reasons:   []string{},
		Deltas:    map[string]float64{},
		Regressed: []string{},
		Policy:    policy,
		CreatedAt: time.Now().UTC(),
	}
	if math.IsNaN(policy.MaxRecallDrop) ||
		policy.MaxRecallDrop < 0 || policy.MaxRecallDrop > 1 ||
		math.IsNaN(policy.MaxPrecisionDrop) ||
		policy.MaxPrecisionDrop < 0 || policy.MaxPrecisionDrop > 1 ||
		math.IsNaN(policy.MaxLatencyRatio) ||
		policy.MaxLatencyRatio < 1 || policy.MaxLatencyRatio > 100 {
		return report, ErrInvalid
	}

	run, err := s.tasks.Get(ctx, runID)
	if err != nil {
		return report, err
	}

	baselineID, err := s.repo.Baseline(ctx, run.Dataset.Name)
	if err != nil {
		return report, fmt.Errorf("%w: 请先设置此题集的基线", ErrInvalid)
	}
	report.BaselineID = baselineID

	baseline, err := s.tasks.Get(ctx, baselineID)
	if err != nil {
		return report, err
	}

	if err := s.verifyComparisonSources(ctx, run, &report); err != nil {
		return report, err
	}
	if len(report.Reasons) > 0 {
		return report, s.repo.SaveComparison(ctx, report)
	}

	compareRuns(&report, run, baseline, baselineID, policy)
	return report, s.repo.SaveComparison(ctx, report)
}

// SetRunBaseline 设置基线的接口入口。
func (s *DatasetService) SetRunBaseline(ctx context.Context, id string) error {
	if !evaluationIDPattern.MatchString(id) {
		return apperrors.ErrInvalidParam
	}
	return responseError(s.SetBaseline(ctx, id))
}

// CompareRun 执行验收对比的接口入口。
func (s *DatasetService) CompareRun(
	ctx context.Context,
	id string,
	input *dto.EvalCompareDTO,
) (*vo.EvalComparisonVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}

	report, err := s.Compare(ctx, id, entity.EvalPolicy{
		MaxRecallDrop:    input.MaxRecallDrop,
		MaxPrecisionDrop: input.MaxPrecisionDrop,
		MaxLatencyRatio:  input.MaxLatencyRatio,
	})
	if err != nil {
		return nil, responseError(err)
	}
	return &vo.EvalComparisonVO{
		BaselineID: report.BaselineID,
		RunID:      report.RunID,
		Verdict:    report.Verdict,
		Reasons:    report.Reasons,
		Deltas:     report.Deltas,
		Regressed:  report.Regressed,
		CreatedAt:  report.CreatedAt,
	}, nil
}

// ── 内部实现 ──

// verifyComparisonSources 对比前核对当前原文，来源变化直接判证据不足。
func (s *DatasetService) verifyComparisonSources(
	ctx context.Context,
	run entity.EvalRun,
	report *entity.EvalComparison,
) error {
	markdownEvidence := len(run.Dataset.Cases) > 0 &&
		len(run.Dataset.Cases[0].ReferenceSources) > 0 &&
		run.Dataset.Cases[0].ReferenceSources[0].SourceType == "original_markdown"
	if len(run.Dataset.Cases) == 0 || s.sources == nil || markdownEvidence {
		return nil
	}

	testCase := run.Dataset.Cases[0]
	sources, err := s.sources.SourcesForEvaluation(
		ctx,
		testCase.Tenant,
		testCase.KBNos[0],
		entity.EvalSamplePolicy{Total: 10000},
	)
	if err != nil {
		return fmt.Errorf("验收前无法核验当前原文")
	}

	hashes := make(map[string]string, len(sources))
	for _, source := range sources {
		hash := sha256.Sum256([]byte(source.Content))
		hashes[source.ChunkID] = hex.EncodeToString(hash[:])
	}

	stored, _ := run.Dataset.Manifest.RetrievalSettings["source_hashes"].(map[string]any)
	if len(stored) == 0 {
		report.Reasons = append(report.Reasons, "缺少可核验的来源哈希")
		return nil
	}
	for chunkID, expected := range stored {
		if hashes[chunkID] != expected {
			report.Reasons = append(report.Reasons, "当前来源已改变: "+chunkID)
		}
	}
	return nil
}

// compareRuns 检查可比性并进入指标对比。
func compareRuns(
	report *entity.EvalComparison,
	run, baseline entity.EvalRun,
	baselineID string,
	policy entity.EvalPolicy,
) {
	switch {
	case run.ID == baselineID:
		report.Reasons = append(report.Reasons, "当前任务就是基线，请选择新的回归任务")
		return
	case run.Status != "succeeded" ||
		baseline.Status != "succeeded" ||
		!run.Dataset.Manifest.Reviewed:
		report.Reasons = append(report.Reasons, "任务未完整成功或题集未经复核")
		return
	case run.Metadata["dataset_hash"] == nil ||
		run.Metadata["dataset_hash"] != baseline.Metadata["dataset_hash"]:
		report.Reasons = append(report.Reasons, "题集或检索配置已变化，不能作配对验收")
		return
	case !compatibleRows(run, baseline):
		report.Reasons = append(report.Reasons, "配对样本或评估模型版本不一致")
		return
	}

	compareMetrics(report, run, baseline, policy)
}

// compatibleRows 检查逐题配对和裁判版本是否一致。
func compatibleRows(run, baseline entity.EvalRun) bool {
	if len(run.Rows) != len(baseline.Rows) {
		return false
	}
	for index, row := range run.Rows {
		previous := baseline.Rows[index]
		if row.CaseID != previous.CaseID || row.Score == nil || previous.Score == nil {
			return false
		}
		if row.Score.Metadata["evaluation_config_hash"] == nil ||
			row.Score.Metadata["evaluation_config_hash"] !=
				previous.Score.Metadata["evaluation_config_hash"] {
			return false
		}
	}
	return true
}

// compareMetrics 对比汇总、逐题退步和失败计数。
func compareMetrics(
	report *entity.EvalComparison,
	run, baseline entity.EvalRun,
	policy entity.EvalPolicy,
) {
	report.Verdict = "pass"
	if len(run.Summary) == 0 {
		report.Verdict = "insufficient"
		report.Reasons = append(report.Reasons, "没有可比较的评分")
	}

	compareSummaries(report, run, baseline, policy)
	compareRows(report, run, baseline, policy)

	if run.BoundaryFailures > 0 ||
		run.RetrievalFailures > 0 ||
		run.RerankDegraded > baseline.RerankDegraded {
		report.Verdict = "fail"
		report.Reasons = append(
			report.Reasons,
			"边界、检索失败或精排降级不满足门槛",
		)
	}
}

// compareSummaries 对比各指标的均值变化和门槛。
func compareSummaries(
	report *entity.EvalComparison,
	run, baseline entity.EvalRun,
	policy entity.EvalPolicy,
) {
	for key, summary := range run.Summary {
		previous := baseline.Summary[key]
		if summary.Mean == nil ||
			previous.Mean == nil ||
			summary.Errors > 0 ||
			summary.OK != previous.OK {
			report.Verdict = "insufficient"
			report.Reasons = append(report.Reasons, "有效评分不完整")
			continue
		}

		delta := *summary.Mean - *previous.Mean
		report.Deltas[key] = delta
		limit := 1.0
		if strings.HasSuffix(key, "/ContextRecall") {
			limit = policy.MaxRecallDrop
		}
		if strings.HasSuffix(key, "/ContextPrecision") {
			limit = policy.MaxPrecisionDrop
		}
		if delta < -limit {
			report.Verdict = "fail"
			report.Reasons = append(report.Reasons, key+" 下降超过配置门槛")
		}
	}
}

// compareRows 检查关键题覆盖退步和检索延迟倍率。
func compareRows(
	report *entity.EvalComparison,
	run, baseline entity.EvalRun,
	policy entity.EvalPolicy,
) {
	for index, row := range run.Rows {
		previous := baseline.Rows[index]

		if recallDropped(row.Score, previous.Score) {
			report.Regressed = append(report.Regressed, row.CaseID)
			for _, testCase := range run.Dataset.Cases {
				if testCase.ID == row.CaseID && testCase.Critical {
					report.Verdict = "fail"
					report.Reasons = append(report.Reasons, "关键题覆盖退步: "+row.CaseID)
				}
			}
		}
		if row.RetrievalLatencyMS >
			previous.RetrievalLatencyMS*policy.MaxLatencyRatio {
			report.Verdict = "fail"
			report.Reasons = append(report.Reasons, "检索延迟超出配置倍率: "+row.CaseID)
		}
	}
}

// recallDropped 判断单题覆盖是否低于基线。
func recallDropped(current, previous *entity.EvalScoreOutput) bool {
	if current == nil || previous == nil {
		return false
	}
	for _, metric := range current.Metrics {
		if metric.Name != "ContextRecall" || metric.Value == nil {
			continue
		}
		for _, old := range previous.Metrics {
			if old.Name == metric.Name && old.Value != nil && *metric.Value < *old.Value {
				return true
			}
		}
	}
	return false
}
