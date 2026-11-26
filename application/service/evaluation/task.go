// 本文件是评测任务的创建、执行与接口：Create/Diagnose、单飞调度、逐题评分、分页结果。
// 任务查询与汇总见 service.go，失败指标重评见 rescore.go。

package evaluation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	logsdk "github.com/PycMono/go-logger-sdk"
)

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
func (s *Service) Diagnose(ctx context.Context, testCase entity.EvalCase) (string, error) {
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
				"scope":          "live-workspace",
				"limit":          10,
				"retrieve_count": 20,
				"dense_weight":   0.5,
				"rerank_switch":  true,
			},
		},
		Cases: []entity.EvalCase{testCase},
		Profiles: []entity.EvalProfile{{
			Name:          "hybrid",
			Limit:         10,
			RetrieveCount: 20,
			DenseWeight:   0.5,
			RerankSwitch:  true,
		}},
	}
	return s.createRun(
		ctx,
		dataset,
		"当前知识库单题诊断",
		"探索性检查，不作为发布基线",
		"diagnostic",
	)
}

// RunItems 分页返回任务逐题结果，诊断任务返回完整原文。
func (s *Service) RunItems(
	ctx context.Context,
	id string,
	query *dto.EvalItemsQuery,
) (*vo.EvalItemsVO, error) {
	if !evaluationIDPattern.MatchString(id) || query.Page < 1 || query.Page > 10000 {
		return nil, apperrors.ErrInvalidParam
	}

	run, err := s.Get(ctx, id)
	if err != nil {
		return nil, responseError(err)
	}

	pages := max(1, (len(run.Rows)+runItemsPageSize-1)/runItemsPageSize)
	if query.Page > pages {
		return nil, apperrors.ErrInvalidParam
	}
	start := min((query.Page-1)*runItemsPageSize, len(run.Rows))
	end := min(start+runItemsPageSize, len(run.Rows))

	result := &vo.EvalItemsVO{
		Items: []vo.EvalRowVO{},
		Total: len(run.Rows),
		Page:  query.Page,
		Limit: runItemsPageSize,
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
			item.Retrieval = &vo.EvalRetrievalVO{
				Items: make([]vo.EvalHitVO, 0, len(row.Retrieval.Items)),
			}
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
			item.Score = &vo.EvalScoreVO{
				Metrics: make([]vo.EvalMetricVO, 0, len(row.Score.Metrics)),
			}
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

// CreateRun 创建回归任务的接口入口。
func (s *Service) CreateRun(ctx context.Context, input *dto.EvalCreateDTO) (*vo.EvalCreatedVO, error) {
	id, err := s.Create(ctx, input.Dataset, input.ExperimentName, input.Hypothesis)
	if err != nil {
		return nil, responseError(err)
	}
	return &vo.EvalCreatedVO{RunID: id}, nil
}

// DiagnoseRun 创建单题诊断任务的接口入口。
func (s *Service) DiagnoseRun(ctx context.Context, input *dto.EvalDiagnoseDTO) (*vo.EvalCreatedVO, error) {
	testCase := entity.EvalCase{
		Tenant:   input.Tenant,
		KBNos:    []string{input.KBNo},
		Question: input.Question,
	}
	id, err := s.Diagnose(ctx, testCase)
	if err != nil {
		return nil, responseError(err)
	}
	return &vo.EvalCreatedVO{RunID: id}, nil
}

// ── 内部实现 ──

// createRun 校验输入、写入任务并启动单飞执行。
func (s *Service) createRun(
	ctx context.Context,
	testDataset entity.EvalDataset,
	name, hypothesis, purpose string,
) (string, error) {
	if !s.opts.Enabled {
		return "", ErrDisabled
	}
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 120 ||
		utf8.RuneCountInString(hypothesis) > 1000 {
		return "", ErrInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.readyLocked(); err != nil {
		return "", err
	}
	if err := validate(testDataset); err != nil {
		return "", err
	}

	run, err := newRun(testDataset, name, hypothesis, purpose, s.opts.Metadata)
	if err != nil {
		return "", err
	}
	if err := s.runs.Create(ctx, run); err != nil {
		return "", err
	}

	s.startLocked(run)
	return run.ID, nil
}

// readyLocked 统一检查任务执行器的可用状态；调用方需持有 s.mu。
func (s *Service) readyLocked() error {
	if s.closed {
		return ErrDisabled
	}
	if s.storageFailed {
		return fmt.Errorf("评测存储异常，请修复后重启服务")
	}
	if s.active != "" {
		return ErrBusy
	}
	return nil
}

// startLocked 启动单飞任务；调用方需持有 s.mu。
func (s *Service) startLocked(run entity.EvalRun) {
	work, cancel := context.WithTimeout(s.root, s.opts.RunTimeout)
	s.active, s.cancel = run.ID, cancel
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()
		defer cancel()
		defer s.releaseActive()

		s.execute(work, run)
	}()
}

// releaseActive 清空当前任务标记。
func (s *Service) releaseActive() {
	s.mu.Lock()
	s.active = ""
	s.cancel = nil
	s.mu.Unlock()
}

// newRun 生成任务 ID、配置哈希和初始元数据。
func newRun(
	dataset entity.EvalDataset,
	name, hypothesis, purpose string,
	baseMetadata map[string]any,
) (entity.EvalRun, error) {
	id, err := newRunID()
	if err != nil {
		return entity.EvalRun{}, err
	}

	configHash, err := hashJSON(struct {
		Profiles []entity.EvalProfile
		Settings map[string]any
	}{
		Profiles: dataset.Profiles,
		Settings: dataset.Manifest.RetrievalSettings,
	})
	if err != nil {
		return entity.EvalRun{}, err
	}
	datasetHash, err := hashJSON(dataset)
	if err != nil {
		return entity.EvalRun{}, err
	}

	metadata := make(map[string]any, len(baseMetadata)+1)
	for key, value := range baseMetadata {
		metadata[key] = value
	}
	metadata["dataset_hash"] = datasetHash

	return entity.EvalRun{
		ID:             id,
		ExperimentName: name,
		Hypothesis:     hypothesis,
		Purpose:        purpose,
		Status:         "queued",
		CreatedAt:      time.Now().UTC(),
		Dataset:        dataset,
		ConfigHash:     configHash,
		Metadata:       metadata,
		Expected:       len(dataset.Cases) * len(dataset.Profiles),
		Summary:        map[string]entity.EvalSummary{},
	}, nil
}

// newRunID 生成时间前缀加随机后缀的任务 ID。
func newRunID() (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(nonce[:]), nil
}

// hashJSON 对任意值做稳定 JSON 哈希。
func hashJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// execute 逐 profile、逐题执行检索和评分，并持续保存进度。
const scoreBatchSize = 3

func (s *Service) execute(ctx context.Context, run entity.EvalRun) {
	run.Status = "running"
	if s.saveMeta(run) != nil {
		return
	}

	failed := false
	pending := make([]entity.EvalRow, 0, scoreBatchSize)
	flush := func() bool {
		if len(pending) == 0 {
			return true
		}
		s.scorePending(ctx, pending)
		for _, row := range pending {
			run.Rows = append(run.Rows, row)
			run.Done++
			if s.recordFailure(&run, row) {
				failed = true
			}
			run.Summary = Summarize(run.Rows)
			if s.saveRow(run.ID, row) != nil {
				return false
			}
		}
		pending = pending[:0]
		return s.saveMeta(run) == nil
	}

loop:
	for _, profile := range run.Dataset.Profiles {
		for _, testCase := range run.Dataset.Cases {
			if ctx.Err() != nil {
				break loop
			}

			pending = append(pending, s.prepareRow(ctx, run, testCase, profile))
			if len(pending) >= scoreBatchSize && !flush() {
				return
			}
		}
	}
	if !flush() {
		return
	}

	run.Status = taskStatus(ctx, failed)
	now := time.Now().UTC()
	run.FinishedAt = &now
	_ = s.saveMeta(run)
}

// scorePending 批量评分当前已检索的题目；失败时逐题标记，不中断整批。
func (s *Service) scorePending(ctx context.Context, rows []entity.EvalRow) {
	inputs := make([]entity.EvalScoreInput, 0, len(rows))
	for _, row := range rows {
		if row.Input != nil && row.Score == nil {
			inputs = append(inputs, *row.Input)
		}
	}
	if len(inputs) == 0 {
		return
	}

	outputs, latency, err := s.scoreInputs(ctx, inputs)
	if err != nil {
		for index := range rows {
			if rows[index].Input == nil || rows[index].Score != nil {
				continue
			}
			rows[index].Score = transportFailureScore(
				ctx,
				*rows[index].Input,
				err,
			)
			rows[index].ScoringLatencyMS = latency / float64(len(inputs))
		}
		return
	}

	byRequest := make(map[string]entity.EvalScoreOutput, len(outputs))
	for _, output := range outputs {
		byRequest[output.RequestID] = output
	}
	for index := range rows {
		if rows[index].Input == nil || rows[index].Score != nil {
			continue
		}
		output, exists := byRequest[rows[index].Input.RequestID]
		if !exists {
			output = *transportFailureScore(
				ctx,
				*rows[index].Input,
				fmt.Errorf("批量评分结果缺少样本"),
			)
		}
		rows[index].Score = &output
		rows[index].ScoringLatencyMS = latency / float64(len(inputs))
	}
}

func (s *Service) scoreInputs(
	ctx context.Context,
	inputs []entity.EvalScoreInput,
) ([]entity.EvalScoreOutput, float64, error) {
	started := time.Now()
	outputs, err := s.scorer.ScoreBatch(ctx, inputs)
	return outputs, elapsedMS(started), err
}

// saveMeta 保存任务元数据和汇总。
func (s *Service) saveMeta(run entity.EvalRun) error {
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.runs.SaveMeta(saveCtx, run); err != nil {
		logsdk.Error(saveCtx, "评测任务保存失败，停止后续调用", logsdk.Err(err))
		s.markStorageFailed()
		return err
	}
	return nil
}

// saveRow 保存单题检索与评分结果。
func (s *Service) saveRow(runID string, row entity.EvalRow) error {
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.runs.SaveRow(saveCtx, runID, row); err != nil {
		logsdk.Error(saveCtx, "评测逐题结果保存失败，停止后续调用", logsdk.Err(err))
		s.markStorageFailed()
		return err
	}
	return nil
}

// markStorageFailed 标记存储异常，阻止后续任务。
func (s *Service) markStorageFailed() {
	s.mu.Lock()
	s.storageFailed = true
	s.mu.Unlock()
}

// recordFailure 把逐题失败累计到任务汇总，返回值表示这次评估是否出现失败。
func (s *Service) recordFailure(run *entity.EvalRun, row entity.EvalRow) bool {
	failed := false

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
			if rescorableMetric(metric) {
				run.ScoringFailures++
				failed = true
			}
		}
	}
	return failed
}

// scoringFailureCount 统计可重评的指标失败数。
func scoringFailureCount(rows []entity.EvalRow) int {
	count := 0
	for _, row := range rows {
		if row.Score == nil {
			continue
		}
		for _, metric := range row.Score.Metrics {
			if rescorableMetric(metric) {
				count++
			}
		}
	}
	return count
}

// prepareRow 执行单题检索、边界检查和评分输入组装。
func (s *Service) prepareRow(
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

	result, err := s.retrieve(ctx, testCase, profile, &row)
	if err != nil {
		return row
	}
	row.Retrieval = result
	if len(result.Items) == 0 {
		row.RetrievalStatus = "empty"
	}
	row.BoundaryViolation = boundaryViolated(testCase, result)
	if testCase.ExpectedRetrieval != "" ||
		len(testCase.ForbiddenChunkIDs) > 0 ||
		len(testCase.ForbiddenDocIDs) > 0 {
		return row
	}

	input := buildScoreInput(run, testCase, profile, result)
	row.Input = &input
	if len(result.Items) == 0 {
		row.Score = emptyRetrievalScore(input)
	}
	return row
}

// retrieve 调用检索并记录耗时和失败状态。
func (s *Service) retrieve(
	ctx context.Context,
	testCase entity.EvalCase,
	profile entity.EvalProfile,
	row *entity.EvalRow,
) (*entity.EvalRetrieval, error) {
	started := time.Now()
	result, err := s.search.SearchForEvaluation(ctx, testCase, profile)
	row.RetrievalLatencyMS = elapsedMS(started)
	if err != nil {
		row.RetrievalStatus = errorStatus(ctx, err)
		row.RetrievalError = "检索调用失败，请检查检索服务与配置"
		return nil, err
	}
	return result, nil
}

// boundaryViolated 检查返回结果是否越过禁止文档或预期空结果。
func boundaryViolated(testCase entity.EvalCase, result *entity.EvalRetrieval) bool {
	if testCase.ExpectedRetrieval == "empty" && len(result.Items) > 0 {
		return true
	}
	for _, hit := range result.Items {
		if slices.Contains(testCase.ForbiddenChunkIDs, hit.ChunkID) ||
			slices.Contains(testCase.ForbiddenDocIDs, hit.DocID) {
			return true
		}
	}
	return false
}

// buildScoreInput 组装评分请求，只包含该题需要的指标。
func buildScoreInput(
	run entity.EvalRun,
	testCase entity.EvalCase,
	profile entity.EvalProfile,
	result *entity.EvalRetrieval,
) entity.EvalScoreInput {
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
	return input
}

// emptyRetrievalScore 把空结果当作被测系统表现：覆盖记 0，其余不适用。
func emptyRetrievalScore(input entity.EvalScoreInput) *entity.EvalScoreOutput {
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
	return output
}

// transportFailureScore 为评分服务调用失败生成逐指标错误。
func transportFailureScore(
	ctx context.Context,
	input entity.EvalScoreInput,
	err error,
) *entity.EvalScoreOutput {
	score := &entity.EvalScoreOutput{
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
	return score
}

const runItemsPageSize = 20

// preview 把正文截成 200 字预览。
func preview(content string) string {
	text := []rune(content)
	if len(text) <= 200 {
		return content
	}
	return string(text[:200]) + "…"
}
