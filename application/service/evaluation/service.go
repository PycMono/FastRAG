// Package evaluation 编排离线检索评测，不参与正常检索链路。
//
// 文件分工：
//
//	service.go     任务主体与查询接口：启动/停止、读取/取消、校验与汇总
//	task.go        任务创建、逐题执行与任务接口
//	rescore.go     失败指标重评（rescore），不是网络或代码重试
//	drafts.go      题库草稿、后台出题、人工复核
//	publish.go     题库发布、选题与来源校验
//	acceptance.go  基线、配对对比与门槛报告
//	generation.go  文档导入联动与后台出题 worker
//	diagnostics.go 检索工程指标
package evaluation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	logsdk "github.com/PycMono/go-logger-sdk"
)

type Options struct {
	Enabled    bool
	RunTimeout time.Duration
	Retention  time.Duration
	Metadata   map[string]any
}

// Service 检索评测应用服务，负责单飞任务的创建、执行与取消。
type Service struct {
	datasets repository.IEvalDatasetRepo
	runs     repository.IEvalRunRepo
	search   interfaces.IEvalSearcher
	scorer   interfaces.IEvalScorer
	opts     Options

	mu            sync.Mutex
	active        string
	cancel        context.CancelFunc
	root          context.Context
	stop          context.CancelFunc
	wg            sync.WaitGroup
	closed        bool
	storageFailed bool
}

// NewService 构造评测应用服务，由基础设施装配依赖。
func NewService(
	datasets repository.IEvalDatasetRepo,
	runs repository.IEvalRunRepo,
	search interfaces.IEvalSearcher,
	scorer interfaces.IEvalScorer,
	opts Options,
) *Service {
	root, stop := context.WithCancel(context.Background())
	if opts.RunTimeout <= 0 {
		opts.RunTimeout = time.Hour
	}

	return &Service{
		datasets: datasets,
		runs:     runs,
		search:   search,
		scorer:   scorer,
		opts:     opts,
		root:     root,
		stop:     stop,
	}
}

// Start 恢复上次退出时未完成的任务状态，不自动重新调用模型。
func (s *Service) Start(ctx context.Context) error {
	if !s.opts.Enabled {
		return nil
	}
	if err := s.runs.Recover(ctx); err != nil {
		return err
	}
	s.startRetention()
	return nil
}

// Stop 停止调度并等待当前任务退出。
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.stop()
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Datasets 返回已发布题库列表。
func (s *Service) Datasets(ctx context.Context) ([]entity.EvalDatasetInfo, error) {
	if !s.opts.Enabled {
		return nil, ErrDisabled
	}
	return s.datasets.List(ctx)
}

// List 返回任务列表，不加载逐题结果。
func (s *Service) List(ctx context.Context) ([]entity.EvalRun, error) {
	if !s.opts.Enabled {
		return nil, ErrDisabled
	}
	return s.runs.List(ctx)
}

// Get 读取单个任务及其逐题结果。
func (s *Service) Get(ctx context.Context, id string) (entity.EvalRun, error) {
	if !s.opts.Enabled {
		return entity.EvalRun{}, ErrDisabled
	}
	return s.runs.Get(ctx, id)
}

// Delete 删除一条已结束的评估记录；运行中的任务必须先取消。
func (s *Service) Delete(ctx context.Context, id string) error {
	if !s.opts.Enabled {
		return ErrDisabled
	}
	if !evaluationIDPattern.MatchString(id) {
		return ErrInvalid
	}

	s.mu.Lock()
	active := s.active == id
	s.mu.Unlock()
	if active {
		return fmt.Errorf("%w: 正在运行的任务不能删除，请先取消", ErrInvalid)
	}
	return s.runs.Delete(ctx, id)
}

// Cancel 取消当前运行任务，不修改已经完成的历史结果。
func (s *Service) Cancel(id string) error {
	if !s.opts.Enabled {
		return ErrDisabled
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active != id || s.cancel == nil {
		return fmt.Errorf("%w: 任务未运行", ErrInvalid)
	}

	s.cancel()
	return nil
}

// Summarize 均值只统计有效评分；失败仍保留在覆盖率分母中。
func Summarize(rows []entity.EvalRow) map[string]entity.EvalSummary {
	summaries := map[string]entity.EvalSummary{}
	sums := map[string]float64{}

	for _, row := range rows {
		if row.Score == nil {
			continue
		}
		for _, metric := range row.Score.Metrics {
			key := row.Profile + "/" + metric.Name
			summary := summaries[key]

			switch metric.Status {
			case "ok":
				if metric.Value != nil {
					summary.OK++
					sums[key] += *metric.Value
				}
			case "not_applicable":
				summary.NotApplicable++
			default:
				summary.Errors++
			}
			summaries[key] = summary
		}
	}

	for key, summary := range summaries {
		if summary.OK > 0 {
			mean := sums[key] / float64(summary.OK)
			summary.Mean = &mean
		}
		if summary.OK+summary.Errors > 0 {
			summary.Coverage = float64(summary.OK) / float64(summary.OK+summary.Errors)
		}
		summaries[key] = summary
	}

	return summaries
}

// ListDatasets 是题库列表的接口投影。
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

// ListRuns 是任务列表的接口投影，不加载逐题结果。
func (s *Service) ListRuns(ctx context.Context) ([]vo.EvalRunVO, error) {
	runs, err := s.List(ctx)
	if err != nil {
		return nil, responseError(err)
	}

	limit := min(len(runs), 100)
	result := make([]vo.EvalRunVO, 0, limit)
	for _, run := range runs[:limit] {
		result = append(result, runVO(run, true))
	}
	return result, nil
}

// RunDetail 返回任务详情和逐题诊断。
func (s *Service) RunDetail(ctx context.Context, id string) (*vo.EvalRunVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}

	run, err := s.Get(ctx, id)
	if err != nil {
		return nil, responseError(err)
	}
	result := runVO(run, false)
	return &result, nil
}

// DeleteRun 删除任务，并校验任务 ID。
func (s *Service) DeleteRun(ctx context.Context, id string) error {
	if !evaluationIDPattern.MatchString(id) {
		return apperrors.ErrInvalidParam
	}
	return responseError(s.Delete(ctx, id))
}

// CancelRun 取消任务，并校验任务 ID。
func (s *Service) CancelRun(id string) error {
	if !evaluationIDPattern.MatchString(id) {
		return apperrors.ErrInvalidParam
	}
	return responseError(s.Cancel(id))
}

// ── 内部实现 ──

// 应用层只返回这几类错误，HTTP 边界再统一映射成业务码。
var (
	ErrDisabled = apperrors.ErrEvaluationDisabled
	ErrBusy     = apperrors.ErrEvaluationBusy
	ErrInvalid  = apperrors.ErrEvaluationInvalid
)

// 评测任务、草稿和数据集共用同一套安全标识。
var evaluationIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// startRetention 每天清理一次过期任务；保留期为 0 时不启动。
func (s *Service) startRetention() {
	if s.opts.Retention <= 0 {
		return
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.purgeOnce()

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-s.root.Done():
				return
			case <-ticker.C:
				s.purgeOnce()
			}
		}
	}()
}

// purgeOnce 删除超过保留期的任务；基线任务保留，报告随任务级联删除。
func (s *Service) purgeOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	before := time.Now().UTC().Add(-s.opts.Retention)
	deleted, err := s.runs.Purge(ctx, before)
	if err != nil {
		logsdk.Error(ctx, "清理过期评测任务失败", logsdk.Err(err))
		return
	}
	if deleted > 0 {
		logsdk.Info(ctx, "已清理过期评测任务", logsdk.Any("deleted", deleted))
	}
}

// validate 是发布和运行前的最后一道数据结构检查。
// 这里只校验本服务能判断的约束，不访问 MySQL / ES。
func validate(dataset entity.EvalDataset) error {
	invalid := func(message string) error {
		return fmt.Errorf("%w: %s", ErrInvalid, message)
	}

	manifest := dataset.Manifest
	if manifest.DatasetVersion == "" ||
		manifest.AnnotationVersion == "" ||
		manifest.CorpusSnapshot == "" ||
		manifest.CorpusHash == "" ||
		len(manifest.RetrievalSettings) == 0 {
		return invalid("manifest 不完整")
	}
	if len(dataset.Cases) == 0 || len(dataset.Cases) > 1000 ||
		len(dataset.Profiles) == 0 || len(dataset.Profiles) > 8 {
		return invalid("用例或配置数量不合法")
	}

	seenCases := make(map[string]bool, len(dataset.Cases))
	for _, testCase := range dataset.Cases {
		if testCase.ID == "" || len(testCase.ID) > 128 || seenCases[testCase.ID] {
			return invalid("用例字段或 ID 不合法")
		}
		if strings.TrimSpace(testCase.Question) == "" ||
			utf8.RuneCountInString(testCase.Question) > 1000 {
			return invalid("用例字段或 ID 不合法")
		}
		if testCase.Tenant == "" || utf8.RuneCountInString(testCase.Tenant) > 64 {
			return invalid("用例字段或 ID 不合法")
		}
		if len(testCase.KBNos) == 0 || len(testCase.KBNos) > 10 {
			return invalid("用例字段或 ID 不合法")
		}
		for _, kbNo := range testCase.KBNos {
			if strings.TrimSpace(kbNo) == "" || utf8.RuneCountInString(kbNo) > 64 {
				return invalid("知识库编号不合法")
			}
		}
		seenCases[testCase.ID] = true

		if testCase.Split != "dev" {
			return invalid("试点仅允许 dev，不运行保留集")
		}
		if testCase.Answerable {
			if strings.TrimSpace(testCase.Reference) == "" || len(testCase.ReferenceSources) == 0 {
				return invalid("缺少参考答案或出处")
			}
			for _, source := range testCase.ReferenceSources {
				if source.DocID == "" {
					return invalid("参考出处缺少文档 ID")
				}
			}
		}
		if testCase.ExpectedRetrieval != "" && testCase.ExpectedRetrieval != "empty" {
			return invalid("试点边界预期仅支持 empty")
		}
		if !testCase.Answerable && testCase.NoAnswerReason == "" && testCase.ExpectedRetrieval == "" {
			return invalid("缺少无答案依据或边界预期")
		}
	}

	seenProfiles := make(map[string]bool, len(dataset.Profiles))
	for _, profile := range dataset.Profiles {
		if profile.Name == "" || len(profile.Name) > 64 || seenProfiles[profile.Name] {
			return invalid("检索配置不合法")
		}
		if profile.Limit < 1 || profile.Limit > 100 ||
			profile.RetrieveCount < 1 || profile.RetrieveCount > 200 ||
			profile.DenseWeight < 0 || profile.DenseWeight > 1 ||
			profile.MinScore < 0 || profile.FusionMinScore < 0 {
			return invalid("检索配置不合法")
		}
		seenProfiles[profile.Name] = true
	}
	return nil
}

// responseError 把仓储错误转换成统一的评测业务错误。
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

// elapsedMS 把耗时换算成毫秒。
func elapsedMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

// errorStatus 把上下文和错误归类成 cancelled/timeout/error。
func errorStatus(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

// rescorableMetric 只把评分环节的失败视为可重评；检索失败要重跑任务。
func rescorableMetric(metric entity.EvalMetric) bool {
	return metric.Status == "error" || metric.Status == "timeout" || metric.Status == "cancelled"
}

// taskStatus 根据取消状态和失败标记生成任务终态。
func taskStatus(ctx context.Context, failed bool) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed_out"
	case ctx.Err() != nil:
		return "cancelled"
	case failed:
		return "partial"
	default:
		return "succeeded"
	}
}

// runVO 把任务投影成接口结构；brief 为列表模式，跳过逐题诊断。
func runVO(run entity.EvalRun, brief bool) vo.EvalRunVO {
	profileSummary := ""
	if len(run.Dataset.Profiles) > 0 {
		profile := run.Dataset.Profiles[0]
		rerank := "精排关"
		if profile.RerankSwitch {
			rerank = "精排开"
		}
		profileSummary = fmt.Sprintf(
			"%s · 返回 %d 条 · 召回 %d 条 · %s",
			profile.Name,
			profile.Limit,
			profile.RetrieveCount,
			rerank,
		)
		if len(run.Dataset.Profiles) > 1 {
			profileSummary += fmt.Sprintf(" · 共 %d 组配置", len(run.Dataset.Profiles))
		}
	}

	summary := make(map[string]vo.EvalSummaryVO, len(run.Summary))
	for key, value := range run.Summary {
		summary[key] = vo.EvalSummaryVO{
			Mean:          value.Mean,
			OK:            value.OK,
			Errors:        value.Errors,
			NotApplicable: value.NotApplicable,
			Coverage:      value.Coverage,
		}
	}

	result := vo.EvalRunVO{
		Diagnostics:       map[string]vo.EvalDiagnosticsVO{},
		FailureReasons:    []string{},
		ScoringFailures:   run.ScoringFailures,
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
		Summary:           summary,
	}
	if brief {
		return result
	}
	result.Diagnostics = retrievalDiagnostics(run)
	result.FailureReasons = failureReasons(run)
	return result
}

// failureReasons 汇总并去重任务中的检索和评分失败原因。
func failureReasons(run entity.EvalRun) []string {
	seen := map[string]bool{}
	reasons := []string{}
	add := func(reason string) {
		if reason == "" || seen[reason] {
			return
		}
		seen[reason] = true
		reasons = append(reasons, reason)
	}

	add(run.Error)
	for _, row := range run.Rows {
		add(row.RetrievalError)
		if row.Score == nil {
			continue
		}
		for _, metric := range row.Score.Metrics {
			if !rescorableMetric(metric) {
				continue
			}
			reason := metric.Error
			if reason == "评分调用失败，请检查评分服务与配置" ||
				reason == "judge failed or incompatible API" {
				reason = "历史评分失败：当时未保存具体错误原因，需结合对应时间的服务日志定位"
			}
			add(reason)
		}
	}
	return reasons
}
