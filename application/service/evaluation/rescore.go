// 本文件是失败指标重评（rescore）：复用原任务的检索输入，只重新调用评分模型，
// 不重新检索、不覆盖旧任务；它和网络或代码重试无关。任务执行见 task.go。

package evaluation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
)

// RescoreFailedMetrics 新建尝试，复用原始检索输入；不覆盖旧任务，也不重新检索。
func (s *Service) RescoreFailedMetrics(ctx context.Context, id string) (*vo.EvalCreatedVO, error) {
	if !s.opts.Enabled {
		return nil, ErrDisabled
	}
	if !evaluationIDPattern.MatchString(id) {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" {
		return nil, ErrBusy
	}
	if s.closed || s.storageFailed {
		return nil, ErrDisabled
	}
	run, err := s.runs.Get(ctx, id)
	if err != nil {
		return nil, responseError(err)
	}
	// 新尝试拥有独立快照，即便仓储实现复用内存也不能修改旧记录。
	data, err := json.Marshal(run)
	if err != nil {
		return nil, responseError(err)
	}
	var snapshot entity.EvalRun
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, responseError(err)
	}
	run = snapshot
	if run.Status == "running" || run.Status == "queued" {
		return nil, ErrBusy
	}
	eligible := 0
	for _, row := range run.Rows {
		if row.Input == nil || row.Score == nil {
			continue
		}
		for _, metric := range row.Score.Metrics {
			if rescorableMetric(metric) {
				eligible++
			}
		}
	}
	if eligible == 0 {
		return nil, fmt.Errorf("%w: 没有可重评指标；检索失败须重新运行", ErrInvalid)
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	run.ID = time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(nonce[:])
	run.Metadata = map[string]any{
		"retry_of":      id,
		"dataset_hash":  run.Metadata["dataset_hash"],
		"score_attempt": "new-judge-config",
	}
	run.ExperimentName = "失败项重评 · " + time.Now().UTC().Format("01-02 15:04")
	run.Status, run.Error = "queued", ""
	run.CreatedAt, run.FinishedAt = time.Now().UTC(), nil
	run.Done, run.Expected = 0, len(run.Rows)
	if err := s.runs.Create(ctx, run); err != nil {
		return nil, responseError(err)
	}
	work, cancel := context.WithTimeout(s.root, s.opts.RunTimeout)
	s.active, s.cancel = run.ID, cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() { s.mu.Lock(); s.active = ""; s.cancel = nil; s.mu.Unlock() }()
		s.rescoreRows(work, run)
	}()
	return &vo.EvalCreatedVO{RunID: run.ID}, nil
}

// ── 内部实现 ──

// rescoreRows 复用原任务检索输入，逐题重评失败指标并保存。
func (s *Service) rescoreRows(ctx context.Context, run entity.EvalRun) {
	run.Status = "running"
	if s.saveMeta(run) != nil {
		return
	}

	for index := range run.Rows {
		if ctx.Err() != nil {
			break
		}
		row := &run.Rows[index]
		if row.Input != nil && row.Score != nil {
			input := *row.Input
			if len(input.ReferenceContextIDs) == 0 {
				for _, testCase := range run.Dataset.Cases {
					if testCase.ID == row.CaseID {
						input.ReferenceContextIDs = testCase.ReferenceContextIDs
						break
					}
				}
			}
			input.RequestID = run.ID + "-" + row.CaseID + "-retry"
			input.Metrics = []string{}
			for _, metric := range row.Score.Metrics {
				if rescorableMetric(metric) {
					input.Metrics = append(input.Metrics, metric.Name)
				}
			}
			if len(input.Metrics) > 0 {
				started := time.Now()
				outputs, err := s.scorer.ScoreBatch(
					ctx,
					[]entity.EvalScoreInput{input},
				)
				var output *entity.EvalScoreOutput
				if err == nil {
					if len(outputs) != 1 {
						err = fmt.Errorf("批量评分结果数量不符")
					} else {
						output = &outputs[0]
					}
				}
				row.ScoringLatencyMS = elapsedMS(started)
				if err != nil {
					for position := range row.Score.Metrics {
						metric := &row.Score.Metrics[position]
						if rescorableMetric(*metric) {
							metric.Status = errorStatus(ctx, err)
							metric.Error = err.Error()
						}
					}
				} else {
					// 不同尝试的成功指标保留，但混合裁判版本不能用于基线验收。
					row.Score.Metadata = map[string]any{"rescore_metadata": output.Metadata, "mixed_attempts": true}
					for _, updated := range output.Metrics {
						for position := range row.Score.Metrics {
							if row.Score.Metrics[position].Name == updated.Name {
								row.Score.Metrics[position] = updated
							}
						}
					}
				}
			}
		}
		run.Done++
		run.Summary = Summarize(run.Rows)
		run.ScoringFailures = scoringFailureCount(run.Rows)
		if s.saveRow(run.ID, *row) != nil || s.saveMeta(run) != nil {
			return
		}
	}

	failed := false
	for _, row := range run.Rows {
		if row.RetrievalStatus == "error" || row.RetrievalStatus == "timeout" {
			failed = true
		}
		if row.Score != nil {
			for _, metric := range row.Score.Metrics {
				if rescorableMetric(metric) {
					failed = true
				}
			}
		}
	}
	run.Status = taskStatus(ctx, failed)
	now := time.Now().UTC()
	run.FinishedAt = &now
	_ = s.saveMeta(run)
}
