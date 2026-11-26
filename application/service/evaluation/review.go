package evaluation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
)

// ReviewService 仅管理标注闭环，复用既有任务执行器，不另建任务系统。
type ReviewService struct {
	lifecycleMu     sync.Mutex
	autoGenerate    bool
	autoSampleCount int
	workerCancel    context.CancelFunc
	workerDone      chan struct{}
	tasks           *Service
	author          interfaces.IEvalAuthor
	repo            repository.IEvalReviewRepo
}

func (s *ReviewService) ConfigureAutomaticGeneration(enabled bool, count int) {
	s.autoGenerate = enabled
	s.autoSampleCount = count
}

func NewReviewService(tasks *Service, author interfaces.IEvalAuthor, repo repository.IEvalReviewRepo) *ReviewService {
	return &ReviewService{tasks: tasks, author: author, repo: repo}
}

func (s *ReviewService) List(ctx context.Context) ([]entity.EvalDraft, error) {
	if !s.tasks.opts.Enabled {
		return nil, ErrDisabled
	}
	return s.repo.ListDrafts(ctx)
}

// Delete 删除未发布草稿；已发布题库和评估记录不受影响。
func (s *ReviewService) Delete(ctx context.Context, id string) error {
	if !s.tasks.opts.Enabled {
		return ErrDisabled
	}
	if !evaluationIDPattern.MatchString(id) {
		return ErrInvalid
	}
	return s.repo.DeleteDraft(ctx, id)
}

// Prepare 只抽取真实原文，不调用模型；生成按题执行，避免长 HTTP 阻塞。
func (s *ReviewService) Prepare(ctx context.Context, tenant, kbNo string, policy entity.EvalSamplePolicy) (entity.EvalDraft, error) {
	var draft entity.EvalDraft
	if !s.tasks.opts.Enabled {
		return draft, ErrDisabled
	}
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(kbNo) == "" ||
		policy.Total < 1 || policy.Total > 1000 || policy.PerDoc < 0 || policy.PerDoc > 1000 {
		return draft, ErrInvalid
	}
	sources, err := s.author.Sources(ctx, tenant, kbNo, policy)
	if err != nil {
		return draft, fmt.Errorf("读取当前知识库原文失败: %w", err)
	}
	if len(sources) == 0 {
		return draft, fmt.Errorf("%w: 当前库没有可采样的文本片段", ErrInvalid)
	}

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return draft, err
	}
	draft = entity.EvalDraft{
		ID: "draft-" + hex.EncodeToString(nonce[:]), Tenant: tenant, KBNo: kbNo,
		CreatedAt: time.Now().UTC(), Items: []entity.EvalCandidate{},
	}
	seen := map[string]bool{}
	for _, source := range sources {
		hash := sha256.Sum256([]byte(source.Content))
		if seen[hex.EncodeToString(hash[:])] {
			continue
		}
		seen[hex.EncodeToString(hash[:])] = true
		draft.Items = append(draft.Items, entity.EvalCandidate{
			ID: fmt.Sprintf("q%03d", len(draft.Items)+1), DocID: source.DocID,
			DocName: source.DocName, ChunkID: source.ChunkID, Source: source.Content,
			SourceHash: hex.EncodeToString(hash[:]), Status: "draft",
		})
	}
	if err := s.repo.SaveDraft(ctx, draft, 0); err != nil {
		return draft, err
	}
	draft.Version = 1
	return draft, nil
}

func generationSource(candidate entity.EvalCandidate) string {
	if candidate.Difficulty == "" {
		return candidate.Source
	}
	return "[出题难度目标：" + candidate.Difficulty + "；简单题考概念，中等题考条件辨析，困难题考本段内多证据推理；不得添加资料外信息]\n" + candidate.Source
}

// GenerateAll 把剩余题目交给后台 worker，页面关闭后仍会继续生成。
func (s *ReviewService) GenerateAll(ctx context.Context, id string, version int) (entity.EvalDraft, error) {
	if !s.tasks.opts.Enabled {
		return entity.EvalDraft{}, ErrDisabled
	}
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return draft, err
	}
	if draft.Version != version {
		return draft, fmt.Errorf("%w: 草稿版本已变化，请刷新后再试", ErrInvalid)
	}
	if !hasMissingQuestions(draft.Items) {
		if draft.GenerationState != "completed" {
			draft.GenerationState = "completed"
			draft.GenerationError = ""
			if err := s.repo.SaveDraft(ctx, draft, version); err != nil {
				return draft, err
			}
			draft.Version++
		}
		return draft, nil
	}
	// 显式重新开始等于再给失败题一次机会；已生成的题不受影响。
	for index := range draft.Items {
		if draft.Items[index].Status == "draft" && draft.Items[index].Question == "" {
			draft.Items[index].GenerationAttempts = 0
		}
	}
	draft.AutoGenerate = true
	draft.GenerationState = "pending"
	draft.GenerationError = ""
	if err := s.repo.SaveDraft(ctx, draft, version); err != nil {
		return draft, err
	}
	draft.Version++
	return draft, nil
}

// PauseGeneration 只停止后续题目，已生成内容保留。
func (s *ReviewService) PauseGeneration(ctx context.Context, id string) (entity.EvalDraft, error) {
	if !s.tasks.opts.Enabled {
		return entity.EvalDraft{}, ErrDisabled
	}
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return draft, err
	}
	if draft.GenerationState != "pending" {
		return draft, nil
	}
	draft.GenerationState = "stopped"
	if err := s.repo.SaveDraft(ctx, draft, draft.Version); err != nil {
		return draft, err
	}
	draft.Version++
	return draft, nil
}

func hasMissingQuestions(items []entity.EvalCandidate) bool {
	for _, candidate := range items {
		if candidate.Status == "draft" && candidate.Question == "" {
			return true
		}
	}
	return false
}

func (s *ReviewService) Generate(ctx context.Context, id, caseID string, version int) (entity.EvalDraft, error) {
	if !s.tasks.opts.Enabled {
		return entity.EvalDraft{}, ErrDisabled
	}
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return draft, err
	}
	if draft.Version != version {
		return draft, fmt.Errorf("%w: 草稿版本已变化", ErrInvalid)
	}
	for index := range draft.Items {
		candidate := &draft.Items[index]
		if candidate.ID != caseID {
			continue
		}
		if candidate.Status != "draft" || candidate.Question != "" {
			return draft, fmt.Errorf("%w: 已编辑的题目不自动覆盖", ErrInvalid)
		}
		generated, err := s.author.Generate(ctx, generationSource(*candidate))
		if err != nil {
			return draft, fmt.Errorf("候选题生成失败，请检查评估模型服务")
		}
		candidate.Question = generated.Question
		candidate.Reference = generated.Reference
		if err := s.repo.SaveDraft(ctx, draft, version); err != nil {
			return draft, err
		}
		draft.Version++
		return draft, nil
	}
	return draft, ErrInvalid
}

// Update 只接收可编辑字段，来源与哈希从服务器草稿恢复，避免伪造证据。
func (s *ReviewService) Update(
	ctx context.Context,
	id string,
	version int,
	edits []entity.EvalCandidate,
) (entity.EvalDraft, error) {
	if !s.tasks.opts.Enabled {
		return entity.EvalDraft{}, ErrDisabled
	}
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return draft, err
	}
	if version != draft.Version || len(edits) != len(draft.Items) {
		return draft, ErrInvalid
	}
	for index, edit := range edits {
		if edit.ID != draft.Items[index].ID || len([]rune(edit.Question)) > 1000 || len([]rune(edit.Reference)) > 10000 {
			return draft, ErrInvalid
		}
		if edit.Status != "draft" && edit.Status != "approved" && edit.Status != "rejected" {
			return draft, ErrInvalid
		}
		if edit.Status == "approved" && (strings.TrimSpace(edit.Question) == "" || strings.TrimSpace(edit.Reference) == "") {
			return draft, fmt.Errorf("%w: 通过的题目必须有问题和参考答案", ErrInvalid)
		}
		candidate := &draft.Items[index]
		candidate.Question, candidate.Reference = edit.Question, edit.Reference
		candidate.Status, candidate.Critical = edit.Status, edit.Critical
	}
	if err := s.repo.SaveDraft(ctx, draft, version); err != nil {
		return draft, err
	}
	draft.Version++
	return draft, nil
}

func (s *ReviewService) Publish(ctx context.Context, id string, version int, profile entity.EvalProfile) (string, error) {
	return s.PublishSelection(ctx, id, version, profile, 0)
}

// PublishSelection 固定抽中的题目，后续重跑仍使用同一批 ID。
func (s *ReviewService) PublishSelection(
	ctx context.Context,
	id string,
	version int,
	profile entity.EvalProfile,
	count int,
) (string, error) {
	if !s.tasks.opts.Enabled {
		return "", ErrDisabled
	}
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return "", err
	}
	if draft.Version != version {
		return "", ErrInvalid
	}
	if count < 0 || count > 1000 {
		return "", ErrInvalid
	}
	if count > 0 {
		selected, err := selectApprovedCandidates(draft.Items, count)
		if err != nil {
			return "", err
		}
		draft.Items = selected
	}
	// 再读当前片段进行哈希核验；原文变化必须重新抽样复核。
	sources, err := s.author.Sources(ctx, draft.Tenant, draft.KBNo, entity.EvalSamplePolicy{Total: 10000})
	if err != nil {
		return "", err
	}
	currentHashes := map[string]string{}
	for _, source := range sources {
		hash := sha256.Sum256([]byte(source.Content))
		currentHashes[source.ChunkID] = hex.EncodeToString(hash[:])
	}

	dataset := entity.EvalDataset{
		Name:     id + fmt.Sprintf("-v%d", version),
		Profiles: []entity.EvalProfile{profile}, Cases: []entity.EvalCase{},
	}
	if count > 0 {
		var nonce [4]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", err
		}
		dataset.Name += "-n" + fmt.Sprint(count) + "-" + hex.EncodeToString(nonce[:])
	}
	for _, candidate := range draft.Items {
		if candidate.Status != "approved" {
			continue
		}
		if candidate.SourceType == "original_markdown" {
			hash := sha256.Sum256([]byte(candidate.Source))
			if candidate.FileHash == "" || candidate.StartLine < 1 || candidate.EndLine < candidate.StartLine ||
				hex.EncodeToString(hash[:]) != candidate.SourceHash {
				return "", fmt.Errorf("%w: 原始 Markdown 证据记录不完整", ErrInvalid)
			}
		} else if currentHashes[candidate.ChunkID] != candidate.SourceHash {
			return "", fmt.Errorf("%w: 原文已变化或片段不再存在，请重新取样", ErrInvalid)
		}
		dataset.Cases = append(dataset.Cases, entity.EvalCase{
			Difficulty: candidate.Difficulty,
			ID:         candidate.ID, Question: candidate.Question, Reference: candidate.Reference,
			Tenant: draft.Tenant, KBNos: []string{draft.KBNo}, Category: "reviewed-source",
			Split: "dev", Answerable: true, Critical: candidate.Critical,
			ReferenceSources: []entity.EvalSource{{
				DocID: candidate.DocID, HeadingPath: candidate.Section,
				SourceType: candidate.SourceType, SourceFile: candidate.SourceFile,
				FileHash: candidate.FileHash, StartLine: candidate.StartLine, EndLine: candidate.EndLine,
				Evidence: candidate.Source, EvidenceHash: candidate.SourceHash,
			}},
			ReferenceContextIDs: []string{candidate.ChunkID},
		})
		if candidate.SourceType == "original_markdown" {
			dataset.Cases[len(dataset.Cases)-1].ReferenceContextIDs = nil
		}
	}
	if len(dataset.Cases) < 3 {
		return "", fmt.Errorf("%w: 至少复核通过 3 题才能发布", ErrInvalid)
	}
	data, _ := json.Marshal(draft.Items)
	hash := sha256.Sum256(data)
	dataset.Manifest = entity.EvalManifest{
		Reviewed: true, DatasetVersion: dataset.Name, AnnotationVersion: fmt.Sprintf("review-%d", version),
		CorpusSnapshot: "live-source-sample", CorpusHash: hex.EncodeToString(hash[:]),
		RetrievalSettings: map[string]any{"scope": draft.KBNo, "snapshot_kind": "reviewed-source-sample-not-full-index"},
	}
	sourceHashes := map[string]string{}
	for _, candidate := range draft.Items {
		if candidate.Status == "approved" && candidate.SourceType != "original_markdown" {
			sourceHashes[candidate.ChunkID] = candidate.SourceHash
		}
	}
	dataset.Manifest.RetrievalSettings["source_hashes"] = sourceHashes
	if err := validate(dataset); err != nil {
		return "", err
	}
	return dataset.Name, s.repo.Publish(ctx, dataset)
}

func selectApprovedCandidates(items []entity.EvalCandidate, count int) ([]entity.EvalCandidate, error) {
	groups := map[string][]entity.EvalCandidate{}
	keys := []string{}
	total := 0
	for _, item := range items {
		if item.Status != "approved" {
			continue
		}
		key := item.DocID + "/" + item.Difficulty
		if _, exists := groups[key]; !exists {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], item)
		total++
	}
	if count < 3 || count > total {
		return nil, fmt.Errorf("%w: 选题数须为 3–%d，不能使用未审核题补足", ErrInvalid, total)
	}
	for _, key := range keys {
		group := groups[key]
		for index := len(group) - 1; index > 0; index-- {
			position, err := rand.Int(rand.Reader, big.NewInt(int64(index+1)))
			if err != nil {
				return nil, err
			}
			group[index], group[position.Int64()] = group[position.Int64()], group[index]
		}
	}
	// 分组顺序随机，避免小样本永远偏向第一本文档或第一种难度。
	for index := len(keys) - 1; index > 0; index-- {
		position, err := rand.Int(rand.Reader, big.NewInt(int64(index+1)))
		if err != nil {
			return nil, err
		}
		keys[index], keys[position.Int64()] = keys[position.Int64()], keys[index]
	}
	result := make([]entity.EvalCandidate, 0, count)
	for offset := 0; len(result) < count; offset++ {
		for _, key := range keys {
			if offset < len(groups[key]) && len(result) < count {
				result = append(result, groups[key][offset])
			}
		}
	}
	return result, nil
}

func (s *ReviewService) SetBaseline(ctx context.Context, runID string) error {
	run, err := s.tasks.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status != "succeeded" || !run.Dataset.Manifest.Reviewed || run.Purpose != "regression" {
		return fmt.Errorf("%w: 只有已复核题集的完整成功任务可设基线", ErrInvalid)
	}
	for _, row := range run.Rows {
		if row.Score == nil || row.Score.Metadata["evaluation_config_hash"] == nil {
			return fmt.Errorf("%w: 缺少完整裁判版本，不能成为基线", ErrInvalid)
		}
	}
	return s.repo.SetBaseline(ctx, run.Dataset.Name, runID)
}

func (s *ReviewService) Compare(ctx context.Context, runID string, policy entity.EvalPolicy) (entity.EvalComparison, error) {
	report := entity.EvalComparison{
		RunID: runID, Verdict: "insufficient", Reasons: []string{}, Deltas: map[string]float64{},
		Regressed: []string{}, Policy: policy, CreatedAt: time.Now().UTC(),
	}
	if math.IsNaN(policy.MaxRecallDrop) || policy.MaxRecallDrop < 0 || policy.MaxRecallDrop > 1 ||
		math.IsNaN(policy.MaxPrecisionDrop) || policy.MaxPrecisionDrop < 0 || policy.MaxPrecisionDrop > 1 ||
		math.IsNaN(policy.MaxLatencyRatio) || policy.MaxLatencyRatio < 1 || policy.MaxLatencyRatio > 100 {
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
	markdownReference := len(run.Dataset.Cases) > 0 && len(run.Dataset.Cases[0].ReferenceSources) > 0 &&
		run.Dataset.Cases[0].ReferenceSources[0].SourceType == "original_markdown"
	if len(run.Dataset.Cases) > 0 && s.author != nil && !markdownReference {
		testCase := run.Dataset.Cases[0]
		sources, sourceErr := s.author.Sources(ctx, testCase.Tenant, testCase.KBNos[0], entity.EvalSamplePolicy{Total: 10000})
		if sourceErr != nil {
			return report, fmt.Errorf("验收前无法核验当前原文")
		}
		hashes := map[string]string{}
		for _, source := range sources {
			hash := sha256.Sum256([]byte(source.Content))
			hashes[source.ChunkID] = hex.EncodeToString(hash[:])
		}
		stored, _ := run.Dataset.Manifest.RetrievalSettings["source_hashes"].(map[string]any)
		if len(stored) == 0 {
			report.Reasons = append(report.Reasons, "缺少可核验的来源哈希")
		} else {
			for chunkID, expected := range stored {
				if hashes[chunkID] != expected {
					report.Reasons = append(report.Reasons, "当前来源已改变: "+chunkID)
				}
			}
		}
	}
	if len(report.Reasons) > 0 {
		return report, s.repo.SaveComparison(ctx, report)
	}
	if runID == baselineID {
		report.Reasons = append(report.Reasons, "当前任务就是基线，请选择新的回归任务")
	} else if run.Status != "succeeded" || baseline.Status != "succeeded" || !run.Dataset.Manifest.Reviewed {
		report.Reasons = append(report.Reasons, "任务未完整成功或题集未经复核")
	} else if run.Metadata["dataset_hash"] == nil || run.Metadata["dataset_hash"] != baseline.Metadata["dataset_hash"] {
		report.Reasons = append(report.Reasons, "题集或检索配置已变化，不能作配对验收")
	} else {
		compatible := true
		for index, row := range run.Rows {
			if index >= len(baseline.Rows) || row.CaseID != baseline.Rows[index].CaseID ||
				row.Score == nil || baseline.Rows[index].Score == nil {
				compatible = false
				break
			}
			if row.Score.Metadata["evaluation_config_hash"] == nil ||
				row.Score.Metadata["evaluation_config_hash"] != baseline.Rows[index].Score.Metadata["evaluation_config_hash"] {
				compatible = false
				break
			}
		}
		if !compatible || len(run.Rows) != len(baseline.Rows) {
			report.Reasons = append(report.Reasons, "配对样本或评估模型版本不一致")
		} else {
			report.Verdict = "pass"
			if len(run.Summary) == 0 {
				report.Verdict = "insufficient"
				report.Reasons = append(report.Reasons, "没有可比较的评分")
			}
			for key, summary := range run.Summary {
				previous := baseline.Summary[key]
				if summary.Mean == nil || previous.Mean == nil || summary.Errors > 0 || summary.OK != previous.OK {
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
			for index, row := range run.Rows {
				for _, metric := range row.Score.Metrics {
					if metric.Name != "ContextRecall" || metric.Value == nil {
						continue
					}
					for _, previous := range baseline.Rows[index].Score.Metrics {
						if previous.Name == metric.Name && previous.Value != nil && *metric.Value < *previous.Value {
							report.Regressed = append(report.Regressed, row.CaseID)
							for _, testCase := range run.Dataset.Cases {
								if testCase.ID == row.CaseID && testCase.Critical {
									report.Verdict = "fail"
									report.Reasons = append(report.Reasons, "关键题覆盖退步: "+row.CaseID)
								}
							}
						}
					}
				}
				if row.RetrievalLatencyMS > baseline.Rows[index].RetrievalLatencyMS*policy.MaxLatencyRatio {
					report.Verdict = "fail"
					report.Reasons = append(report.Reasons, "检索延迟超出配置倍率: "+row.CaseID)
				}
			}
			if run.BoundaryFailures > 0 || run.RetrievalFailures > 0 || run.RerankDegraded > baseline.RerankDegraded {
				report.Verdict = "fail"
				report.Reasons = append(report.Reasons, "边界、检索失败或精排降级不满足门槛")
			}
		}
	}
	if err := s.repo.SaveComparison(ctx, report); err != nil {
		return report, err
	}
	return report, nil
}
