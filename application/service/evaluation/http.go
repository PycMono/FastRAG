package evaluation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
)

var evaluationIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// responseError 在应用边界转换底层错误，统一交给 ginsdk 处理和记录。
func responseError(err error) error {
	if err == nil {
		return nil
	}
	var coded apperrors.CodeError
	if errors.As(err, &coded) {
		return err
	}
	if os.IsNotExist(err) {
		return apperrors.ErrEvaluationNotFound.Wrap(err)
	}
	return apperrors.ErrEvaluationFailed.Wrap(err)
}

func runVO(run entity.EvalRun) vo.EvalRunVO {
	profileSummary := ""
	if len(run.Dataset.Profiles) > 0 {
		p := run.Dataset.Profiles[0]
		rerank := "精排关"
		if p.RerankSwitch {
			rerank = "精排开"
		}
		profileSummary = fmt.Sprintf(
			"%s · 返回 %d 条 · 召回 %d 条 · %s",
			p.Name, p.Limit, p.RetrieveCount, rerank,
		)
	}
	result := vo.EvalRunVO{
		Diagnostics:       retrievalDiagnostics(run),
		FailureReasons:    []string{},
		ID:                run.ID,
		Dataset:           run.Dataset.Name,
		ProfileSummary:    profileSummary,
		ExperimentName:    run.ExperimentName,
		Purpose:           run.Purpose,
		Status:            run.Status,
		Error:             run.Error,
		CreatedAt:         run.CreatedAt,
		FinishedAt:        run.FinishedAt,
		Expected:          run.Expected,
		Done:              run.Done,
		RetrievalFailures: run.RetrievalFailures,
		BoundaryFailures:  run.BoundaryFailures,
		RerankDegraded:    run.RerankDegraded,
		Summary:           make(map[string]vo.EvalSummaryVO, len(run.Summary)),
	}
	seenReasons := map[string]bool{}
	addReason := func(reason string) {
		if reason != "" && !seenReasons[reason] {
			seenReasons[reason] = true
			result.FailureReasons = append(result.FailureReasons, reason)
		}
	}
	addReason(run.Error)
	for _, row := range run.Rows {
		addReason(row.RetrievalError)
		if row.Score == nil {
			continue
		}
		for _, metric := range row.Score.Metrics {
			if metric.Status != "error" && metric.Status != "timeout" && metric.Status != "cancelled" {
				continue
			}
			result.ScoringFailures++
			reason := metric.Error
			if reason == "评分调用失败，请检查评分服务与配置" || reason == "judge failed or incompatible API" {
				reason = "历史评分失败：当时未保存具体错误原因，需结合对应时间的服务日志定位"
			}
			addReason(reason)
		}
	}
	for key, summary := range run.Summary {
		result.Summary[key] = vo.EvalSummaryVO{
			Mean:          summary.Mean,
			OK:            summary.OK,
			Errors:        summary.Errors,
			NotApplicable: summary.NotApplicable,
			Coverage:      summary.Coverage,
		}
	}
	return result
}

func (s *Service) ListDatasets(ctx context.Context) ([]vo.EvalDatasetInfoVO, error) {
	datasets, err := s.Datasets(ctx)
	if err != nil {
		return nil, responseError(err)
	}
	result := make([]vo.EvalDatasetInfoVO, 0, len(datasets))
	for _, dataset := range datasets {
		result = append(result, vo.EvalDatasetInfoVO{
			Name:           dataset.Name,
			Version:        dataset.Version,
			Tenant:         dataset.Tenant,
			KBNos:          dataset.KBNos,
			Cases:          dataset.Cases,
			Profiles:       dataset.Profiles,
			ProfileSummary: dataset.ProfileSummary,
			SearchRequests: dataset.SearchRequests,
		})
	}
	return result, nil
}

func (s *Service) ListRuns(ctx context.Context) ([]vo.EvalRunVO, error) {
	runs, err := s.List(ctx)
	if err != nil {
		return nil, responseError(err)
	}
	result := make([]vo.EvalRunVO, 0, min(len(runs), 100))
	for _, run := range runs[:min(len(runs), 100)] {
		result = append(result, runVO(run))
	}
	return result, nil
}

func (s *Service) RunDetail(ctx context.Context, id string) (*vo.EvalRunVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}
	run, err := s.Get(ctx, id)
	if err != nil {
		return nil, responseError(err)
	}
	result := runVO(run)
	return &result, nil
}

func preview(content string) string {
	text := []rune(content)
	if len(text) <= 200 {
		return content
	}
	return string(text[:200]) + "…"
}

func (s *Service) RunItems(ctx context.Context, id string, query *dto.EvalItemsQuery) (*vo.EvalItemsVO, error) {
	if !evaluationIDPattern.MatchString(id) || query.Page < 1 || query.Page > 10000 {
		return nil, apperrors.ErrInvalidParam
	}
	run, err := s.Get(ctx, id)
	if err != nil {
		return nil, responseError(err)
	}
	const pageSize = 20
	if query.Page > max(1, (len(run.Rows)+pageSize-1)/pageSize) {
		return nil, apperrors.ErrInvalidParam
	}
	start := min((query.Page-1)*pageSize, len(run.Rows))
	end := min(start+pageSize, len(run.Rows))
	result := &vo.EvalItemsVO{
		Items: []vo.EvalRowVO{},
		Total: len(run.Rows),
		Page:  query.Page,
		Limit: pageSize,
	}
	for _, row := range run.Rows[start:end] {
		item := vo.EvalRowVO{
			CaseID:             row.CaseID,
			Question:           row.Question,
			Reference:          preview(row.Reference),
			Profile:            row.Profile,
			RetrievalStatus:    row.RetrievalStatus,
			RetrievalError:     row.RetrievalError,
			RetrievalLatencyMS: row.RetrievalLatencyMS,
			ScoringLatencyMS:   row.ScoringLatencyMS,
			BoundaryViolation:  row.BoundaryViolation,
		}
		if row.Retrieval != nil {
			item.Retrieval = &vo.EvalRetrievalVO{Items: []vo.EvalHitVO{}}
			for _, hit := range row.Retrieval.Items {
				content := preview(hit.Content)
				if run.Purpose == "diagnostic" {
					content = hit.Content
				}
				item.Retrieval.Items = append(item.Retrieval.Items, vo.EvalHitVO{
					ChunkID: hit.ChunkID,
					DocID:   hit.DocID,
					Title:   hit.Title,
					Content: content,
				})
			}
		}
		if row.Score != nil {
			item.Score = &vo.EvalScoreVO{Metrics: []vo.EvalMetricVO{}}
			for _, metric := range row.Score.Metrics {
				item.Score.Metrics = append(item.Score.Metrics, vo.EvalMetricVO{
					ErrorDetails: metric.ErrorDetails,
					Name:         metric.Name,
					Status:       metric.Status,
					Value:        metric.Value,
					Error:        metric.Error,
					ComputedBy:   metric.ComputedBy,
				})
			}
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (s *Service) CreateRun(ctx context.Context, input *dto.EvalCreateDTO) (*vo.EvalCreatedVO, error) {
	id, err := s.Create(ctx, input.Dataset, input.ExperimentName, input.Hypothesis)
	if err != nil {
		return nil, responseError(err)
	}
	return &vo.EvalCreatedVO{RunID: id}, nil
}

func (s *Service) DiagnoseRun(ctx context.Context, input *dto.EvalDiagnoseDTO) (*vo.EvalCreatedVO, error) {
	id, err := s.Diagnose(ctx, entity.EvalCase{Tenant: input.Tenant, KBNos: []string{input.KBNo}, Question: input.Question})
	if err != nil {
		return nil, responseError(err)
	}
	return &vo.EvalCreatedVO{RunID: id}, nil
}

func (s *Service) DeleteRun(ctx context.Context, id string) error {
	if !evaluationIDPattern.MatchString(id) {
		return apperrors.ErrInvalidParam
	}
	return responseError(s.Delete(ctx, id))
}

func (s *Service) CancelRun(id string) error {
	if !evaluationIDPattern.MatchString(id) {
		return apperrors.ErrInvalidParam
	}
	return responseError(s.Cancel(id))
}
