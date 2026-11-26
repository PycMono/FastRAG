// Package evaluation 编排离线检索评测，不参与正常检索链路。
package evaluation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	logsdk "github.com/PycMono/go-logger-sdk"
)

var (
	ErrDisabled = apperrors.ErrEvaluationDisabled
	ErrBusy     = apperrors.ErrEvaluationBusy
	ErrInvalid  = apperrors.ErrEvaluationInvalid
)

type Options struct {
	Enabled    bool
	RunTimeout time.Duration
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
	return s.runs.Recover(ctx)
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

func (s *Service) Datasets(ctx context.Context) ([]entity.EvalDatasetInfo, error) {
	if !s.opts.Enabled {
		return nil, ErrDisabled
	}
	return s.datasets.List(ctx)
}

func (s *Service) List(ctx context.Context) ([]entity.EvalRun, error) {
	if !s.opts.Enabled {
		return nil, ErrDisabled
	}
	return s.runs.List(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (entity.EvalRun, error) {
	if !s.opts.Enabled {
		return entity.EvalRun{}, ErrDisabled
	}
	return s.runs.Get(ctx, id)
}

func validate(dataset entity.EvalDataset) error {
	invalid := func(message string) error {
		return fmt.Errorf("%w: %s", ErrInvalid, message)
	}

	if dataset.Manifest.DatasetVersion == "" ||
		dataset.Manifest.AnnotationVersion == "" ||
		dataset.Manifest.CorpusSnapshot == "" ||
		dataset.Manifest.CorpusHash == "" ||
		len(dataset.Manifest.RetrievalSettings) == 0 {
		return invalid("manifest 不完整")
	}
	if len(dataset.Cases) == 0 || len(dataset.Cases) > 1000 ||
		len(dataset.Profiles) == 0 || len(dataset.Profiles) > 8 {
		return invalid("用例或配置数量不合法")
	}

	seenCaseIDs := make(map[string]bool, len(dataset.Cases))
	for _, testCase := range dataset.Cases {
		if testCase.ID == "" || len(testCase.ID) > 128 || seenCaseIDs[testCase.ID] ||
			strings.TrimSpace(testCase.Question) == "" || utf8.RuneCountInString(testCase.Question) > 1000 ||
			testCase.Tenant == "" || utf8.RuneCountInString(testCase.Tenant) > 64 ||
			len(testCase.KBNos) == 0 || len(testCase.KBNos) > 10 {
			return invalid("用例字段或 ID 不合法")
		}
		for _, kbNo := range testCase.KBNos {
			if strings.TrimSpace(kbNo) == "" || utf8.RuneCountInString(kbNo) > 64 {
				return invalid("知识库编号不合法")
			}
		}
		seenCaseIDs[testCase.ID] = true

		if testCase.Split != "dev" {
			return invalid("试点仅允许 dev，不运行保留集")
		}
		if testCase.Answerable && (strings.TrimSpace(testCase.Reference) == "" || len(testCase.ReferenceSources) == 0) {
			return invalid("缺少参考答案或出处")
		}
		for _, source := range testCase.ReferenceSources {
			if source.DocID == "" {
				return invalid("参考出处缺少文档 ID")
			}
		}

		if testCase.ExpectedRetrieval != "" && testCase.ExpectedRetrieval != "empty" {
			return invalid("试点边界预期仅支持 empty")
		}
		if !testCase.Answerable && testCase.NoAnswerReason == "" && testCase.ExpectedRetrieval == "" {
			return invalid("缺少无答案依据或边界预期")
		}
	}

	seenProfileNames := make(map[string]bool, len(dataset.Profiles))
	for _, profile := range dataset.Profiles {
		if profile.Name == "" || len(profile.Name) > 64 || seenProfileNames[profile.Name] ||
			profile.Limit < 1 || profile.Limit > 100 ||
			profile.RetrieveCount < 1 || profile.RetrieveCount > 200 ||
			profile.DenseWeight < 0 || profile.DenseWeight > 1 ||
			profile.MinScore < 0 || profile.FusionMinScore < 0 {
			return invalid("检索配置不合法")
		}
		seenProfileNames[profile.Name] = true
	}

	return nil
}

// Create 固定数据集快照后启动后台任务，不阻塞 HTTP 请求。
func (s *Service) Create(ctx context.Context, dataset, name, hypothesis string) (string, error) {
	if !s.opts.Enabled {
		return "", ErrDisabled
	}
	testDataset, err := s.datasets.Load(ctx, dataset)
	if err != nil {
		return "", fmt.Errorf("%w: 数据集不可用", ErrInvalid)
	}
	return s.createRun(ctx, testDataset, name, hypothesis, "regression")
}

// Diagnose 查询当前工作区的真实语料，无参考时仅判断相关性。
func (s *Service) Diagnose(
	ctx context.Context,
	testCase entity.EvalCase,
) (string, error) {
	testCase.ID = "single"
	testCase.Split = "dev"
	testCase.Answerable = false
	testCase.NoAnswerReason = "单题探索，尚未建立正式参考标注"

	dataset := entity.EvalDataset{
		Name: "workspace-diagnostic",
		Manifest: entity.EvalManifest{
			DatasetVersion:    "adhoc",
			AnnotationVersion: "unreviewed",
			CorpusSnapshot:    "live-es",
			CorpusHash:        "unverified",
			RetrievalSettings: map[string]any{
				"scope": "live-workspace", "limit": 10, "retrieve_count": 20,
				"dense_weight": 0.5, "rerank_switch": true,
			},
		},
		Cases: []entity.EvalCase{testCase},
		Profiles: []entity.EvalProfile{{
			Name: "hybrid", Limit: 10, RetrieveCount: 20,
			DenseWeight: 0.5, RerankSwitch: true,
		}},
	}
	return s.createRun(ctx, dataset, "当前知识库单题诊断", "探索性检查，不作为发布基线", "diagnostic")
}

func (s *Service) createRun(
	ctx context.Context,
	testDataset entity.EvalDataset,
	name, hypothesis, purpose string,
) (string, error) {
	if !s.opts.Enabled {
		return "", ErrDisabled
	}
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 120 || utf8.RuneCountInString(hypothesis) > 1000 {
		return "", ErrInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return "", ErrDisabled
	}
	if s.storageFailed {
		return "", fmt.Errorf("评测存储异常，请修复后重启服务")
	}
	if s.active != "" {
		return "", ErrBusy
	}

	if err := validate(testDataset); err != nil {
		return "", err
	}

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	id := time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(nonce[:])

	configBytes, err := json.Marshal(struct {
		Profiles []entity.EvalProfile
		Settings map[string]any
	}{
		Profiles: testDataset.Profiles,
		Settings: testDataset.Manifest.RetrievalSettings,
	})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(configBytes)

	datasetBytes, err := json.Marshal(testDataset)
	if err != nil {
		return "", err
	}
	datasetHash := sha256.Sum256(datasetBytes)

	metadata := make(map[string]any, len(s.opts.Metadata)+1)
	for key, value := range s.opts.Metadata {
		metadata[key] = value
	}
	metadata["dataset_hash"] = hex.EncodeToString(datasetHash[:])

	run := entity.EvalRun{
		ID:             id,
		ExperimentName: name,
		Hypothesis:     hypothesis,
		Purpose:        purpose,
		Status:         "queued",
		CreatedAt:      time.Now().UTC(),
		Dataset:        testDataset,
		ConfigHash:     hex.EncodeToString(hash[:]),
		Metadata:       metadata,
		Expected:       len(testDataset.Cases) * len(testDataset.Profiles),
		Summary:        map[string]entity.EvalSummary{},
	}
	if err = s.runs.Create(ctx, run); err != nil {
		return "", err
	}

	work, cancel := context.WithTimeout(s.root, s.opts.RunTimeout)
	s.active = id
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			s.active = ""
			s.cancel = nil
			s.mu.Unlock()
		}()

		s.execute(work, run)
	}()

	return id, nil
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

func (s *Service) execute(ctx context.Context, run entity.EvalRun) {
	persist := func() error {
		saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := s.runs.Save(saveCtx, run)
		if err != nil {
			logsdk.Error(saveCtx, "评测结果保存失败，停止后续调用", logsdk.Err(err))
			s.mu.Lock()
			s.storageFailed = true
			s.mu.Unlock()
		}
		return err
	}

	run.Status = "running"
	if persist() != nil {
		return
	}

	failed := false
outer:
	for _, profile := range run.Dataset.Profiles {
		for _, testCase := range run.Dataset.Cases {
			if ctx.Err() != nil {
				break outer
			}

			row := s.evaluate(ctx, run, testCase, profile)
			run.Rows = append(run.Rows, row)
			run.Done++

			if row.RetrievalStatus == "error" || row.RetrievalStatus == "timeout" {
				run.RetrievalFailures++
				failed = true
			}
			if row.BoundaryViolation {
				run.BoundaryFailures++
				failed = true
			}
			if row.Retrieval != nil && row.Retrieval.Meta["degraded"] == true {
				run.RerankDegraded++
			}
			if row.Score != nil {
				for _, metric := range row.Score.Metrics {
					if metric.Status == "error" || metric.Status == "timeout" || metric.Status == "cancelled" {
						failed = true
					}
				}
			}

			run.Summary = Summarize(run.Rows)
			if persist() != nil {
				return
			}
		}
	}

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		run.Status = "timed_out"
	case ctx.Err() != nil:
		run.Status = "cancelled"
	case failed:
		run.Status = "partial"
	default:
		run.Status = "succeeded"
	}

	now := time.Now().UTC()
	run.FinishedAt = &now
	_ = persist()
}

func (s *Service) evaluate(
	ctx context.Context,
	run entity.EvalRun,
	testCase entity.EvalCase,
	profile entity.EvalProfile,
) entity.EvalRow {
	row := entity.EvalRow{
		CaseID:          testCase.ID,
		Question:        testCase.Question,
		Reference:       testCase.Reference,
		Profile:         profile.Name,
		Category:        testCase.Category,
		Split:           testCase.Split,
		RetrievalStatus: "ok",
	}

	started := time.Now()
	result, err := s.search.Search(ctx, testCase, profile)
	row.RetrievalLatencyMS = elapsedMS(started)
	if err != nil {
		row.RetrievalStatus = errorStatus(ctx, err)
		row.RetrievalError = "检索调用失败，请检查检索服务与配置"
		return row
	}

	row.Retrieval = result
	if len(result.Items) == 0 {
		row.RetrievalStatus = "empty"
	}

	for _, hit := range result.Items {
		for _, id := range testCase.ForbiddenChunkIDs {
			if hit.ChunkID == id {
				row.BoundaryViolation = true
			}
		}
		for _, id := range testCase.ForbiddenDocIDs {
			if hit.DocID == id {
				row.BoundaryViolation = true
			}
		}
	}
	if testCase.ExpectedRetrieval == "empty" && len(result.Items) > 0 {
		row.BoundaryViolation = true
	}
	if testCase.ExpectedRetrieval != "" ||
		len(testCase.ForbiddenChunkIDs) > 0 ||
		len(testCase.ForbiddenDocIDs) > 0 {
		return row
	}

	input := entity.EvalScoreInput{
		RequestID:           run.ID + "-" + profile.Name + "-" + testCase.ID + "-a1",
		DatasetVersion:      run.Dataset.Manifest.DatasetVersion,
		AnnotationVersion:   run.Dataset.Manifest.AnnotationVersion,
		CaseID:              testCase.ID,
		UserInput:           testCase.Question,
		Reference:           testCase.Reference,
		RetrievedContexts:   []string{},
		RetrievedContextIDs: []string{},
		ReferenceContextIDs: testCase.ReferenceContextIDs,
		Metrics:             []string{"ContextRelevance"},
	}
	if testCase.Answerable {
		input.Metrics = append(input.Metrics, "ContextPrecision", "ContextRecall")
	}
	for _, hit := range result.Items {
		input.RetrievedContexts = append(input.RetrievedContexts, hit.Content)
		input.RetrievedContextIDs = append(input.RetrievedContextIDs, hit.ChunkID)
	}
	row.Input = &input

	// 空结果是被测系统表现，不花费模型调用，也不要求评分服务在线。
	if len(result.Items) == 0 {
		output := &entity.EvalScoreOutput{
			RequestID:     input.RequestID,
			OverallStatus: "succeeded",
		}
		for _, name := range input.Metrics {
			metric := entity.EvalMetric{
				Name:       name,
				Status:     "not_applicable",
				ComputedBy: "policy",
				Error:      "空检索结果",
			}
			if name == "ContextRecall" {
				zero := 0.0
				metric.Status = "ok"
				metric.Value = &zero
				metric.Error = ""
			}
			output.Metrics = append(output.Metrics, metric)
		}
		row.Score = output
		return row
	}

	started = time.Now()
	score, err := s.scorer.Score(ctx, input)
	row.ScoringLatencyMS = elapsedMS(started)
	if err != nil {
		score = &entity.EvalScoreOutput{
			RequestID:     input.RequestID,
			OverallStatus: "partial",
		}
		message := err.Error()
		if errors.Is(err, interfaces.ErrEvalServiceBusy) {
			message = "评分服务忙，尚未执行模型评分，请稍后重新运行"
		}
		for _, name := range input.Metrics {
			score.Metrics = append(score.Metrics, entity.EvalMetric{
				Name:       name,
				Status:     errorStatus(ctx, err),
				Error:      message,
				ComputedBy: "transport",
			})
		}
	}

	row.Score = score
	return row
}

func elapsedMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

func errorStatus(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
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
