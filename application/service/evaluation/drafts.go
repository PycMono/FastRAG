// 本文件是题库草稿：抽样、后台出题、人工复核，以及草稿接口。
// 发布见 publish.go，验收见 acceptance.go。

package evaluation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
)

// DatasetService 管理题库标注闭环，复用任务执行器，不另建任务系统。
type DatasetService struct {
	lifecycleMu     sync.Mutex
	autoGenerate    bool
	autoSampleCount int
	workerCancel    context.CancelFunc
	workerDone      chan struct{}
	tasks           *Service
	sources         interfaces.IEvalSourceReader
	generator       interfaces.IEvalGenerator
	repo            repository.IEvalReviewRepo
}

// NewDatasetService 构造题库标注服务。
func NewDatasetService(
	tasks *Service,
	sources interfaces.IEvalSourceReader,
	generator interfaces.IEvalGenerator,
	repo repository.IEvalReviewRepo,
) *DatasetService {
	return &DatasetService{
		tasks:     tasks,
		sources:   sources,
		generator: generator,
		repo:      repo,
	}
}

// ConfigureAutomaticGeneration 设置导入后自动出题的开关和数量。
func (s *DatasetService) ConfigureAutomaticGeneration(enabled bool, count int) {
	s.autoGenerate = enabled
	s.autoSampleCount = count
}

// List 返回全部题库草稿。
func (s *DatasetService) List(ctx context.Context) ([]entity.EvalDraft, error) {
	if !s.tasks.opts.Enabled {
		return nil, ErrDisabled
	}
	return s.repo.ListDrafts(ctx)
}

// Delete 删除未发布草稿。
func (s *DatasetService) Delete(ctx context.Context, id string) error {
	if !s.tasks.opts.Enabled {
		return ErrDisabled
	}
	if !evaluationIDPattern.MatchString(id) {
		return ErrInvalid
	}
	return s.repo.DeleteDraft(ctx, id)
}

// Prepare 只抽取真实原文，不调用模型；生成按题执行，避免长 HTTP 阻塞。
func (s *DatasetService) Prepare(
	ctx context.Context,
	tenant, kbNo string,
	policy entity.EvalSamplePolicy,
) (entity.EvalDraft, error) {
	var empty entity.EvalDraft

	if !s.tasks.opts.Enabled {
		return empty, ErrDisabled
	}
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(kbNo) == "" {
		return empty, ErrInvalid
	}
	if policy.Total < 1 || policy.Total > 1000 ||
		policy.PerDoc < 0 || policy.PerDoc > 1000 {
		return empty, ErrInvalid
	}

	sources, err := s.sources.SourcesForEvaluation(ctx, tenant, kbNo, policy)
	if err != nil {
		return empty, fmt.Errorf("读取当前知识库原文失败: %w", err)
	}
	if len(sources) == 0 {
		return empty, fmt.Errorf("%w: 当前库没有可采样的文本片段", ErrInvalid)
	}

	draft, err := newDraft(tenant, kbNo, sources)
	if err != nil {
		return empty, err
	}
	if err := s.repo.SaveDraft(ctx, draft, 0); err != nil {
		return empty, err
	}

	draft.Version = 1
	return draft, nil
}

// GenerateAll 把剩余题目交给后台 worker，页面关闭后仍会继续生成。
func (s *DatasetService) GenerateAll(
	ctx context.Context,
	id string,
	version int,
) (entity.EvalDraft, error) {
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
		if draft.GenerationState == "completed" {
			return draft, nil
		}
		draft.GenerationState = "completed"
		draft.GenerationError = ""
		return s.saveDraftVersion(ctx, draft, version)
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
	return s.saveDraftVersion(ctx, draft, version)
}

// PauseGeneration 只停止后续题目，已生成内容保留。
func (s *DatasetService) PauseGeneration(
	ctx context.Context,
	id string,
) (entity.EvalDraft, error) {
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
	return s.saveDraftVersion(ctx, draft, draft.Version)
}

// Generate 生成指定题目，不覆盖已编辑或已通过的题。
func (s *DatasetService) Generate(
	ctx context.Context,
	id, caseID string,
	version int,
) (entity.EvalDraft, error) {
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

	var candidate *entity.EvalCandidate
	for index := range draft.Items {
		if draft.Items[index].ID == caseID {
			candidate = &draft.Items[index]
			break
		}
	}
	if candidate == nil {
		return draft, ErrInvalid
	}
	if candidate.Status != "draft" || candidate.Question != "" {
		return draft, fmt.Errorf("%w: 已编辑的题目不自动覆盖", ErrInvalid)
	}

	generated, err := s.generator.Generate(ctx, generationSource(*candidate))
	if err != nil {
		return draft, fmt.Errorf("候选题生成失败，请检查评估模型服务")
	}

	candidate.Question = generated.Question
	candidate.Reference = generated.Reference
	return s.saveDraftVersion(ctx, draft, version)
}

// Update 只接收可编辑字段，来源与哈希从服务器草稿恢复，避免伪造证据。
func (s *DatasetService) Update(
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
		if edit.ID != draft.Items[index].ID {
			return draft, ErrInvalid
		}
		if len([]rune(edit.Question)) > 1000 || len([]rune(edit.Reference)) > 10000 {
			return draft, ErrInvalid
		}
		if edit.Status != "draft" && edit.Status != "approved" && edit.Status != "rejected" {
			return draft, ErrInvalid
		}
		if edit.Status == "approved" &&
			(strings.TrimSpace(edit.Question) == "" || strings.TrimSpace(edit.Reference) == "") {
			return draft, fmt.Errorf("%w: 通过的题目必须有问题和参考答案", ErrInvalid)
		}

		target := &draft.Items[index]
		target.Question = edit.Question
		target.Reference = edit.Reference
		target.Status = edit.Status
		target.Critical = edit.Critical
	}
	return s.saveDraftVersion(ctx, draft, version)
}

// ListDrafts 是草稿列表的接口投影。
func (s *DatasetService) ListDrafts(
	ctx context.Context,
) ([]vo.EvalDraftVO, error) {
	drafts, err := s.List(ctx)
	if err != nil {
		return nil, responseError(err)
	}

	result := make([]vo.EvalDraftVO, 0, len(drafts))
	for _, draft := range drafts {
		result = append(result, *draftVO(draft))
	}
	return result, nil
}

// DeleteDraft 删除草稿，并校验草稿 ID。
func (s *DatasetService) DeleteDraft(ctx context.Context, id string) error {
	if !evaluationIDPattern.MatchString(id) {
		return apperrors.ErrInvalidParam
	}
	return responseError(s.Delete(ctx, id))
}

// PrepareDraft 从请求参数构造抽样策略并创建草稿。
func (s *DatasetService) PrepareDraft(
	ctx context.Context,
	input *dto.EvalPrepareDTO,
) (*vo.EvalDraftVO, error) {
	policy := entity.EvalSamplePolicy{Total: input.Count}
	if input.PerDoc > 0 {
		policy.PerDoc = input.PerDoc
		policy.Total = input.TotalCap
	}

	draft, err := s.Prepare(ctx, input.Tenant, input.KBNo, policy)
	if err != nil {
		return nil, responseError(err)
	}
	return draftVO(draft), nil
}

// GenerateCandidate 生成单题的接口入口。
func (s *DatasetService) GenerateCandidate(
	ctx context.Context,
	id string,
	input *dto.EvalGenerateDTO,
) (*vo.EvalDraftVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}

	draft, err := s.Generate(ctx, id, input.CaseID, input.Version)
	if err != nil {
		return nil, responseError(err)
	}
	return draftVO(draft), nil
}

// StartGeneration 把整份草稿交给后台 worker，客户端可以关闭页面。
func (s *DatasetService) StartGeneration(
	ctx context.Context,
	id string,
	input *dto.EvalGenerationDTO,
) (*vo.EvalDraftVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}

	draft, err := s.GenerateAll(ctx, id, input.Version)
	if err != nil {
		return nil, responseError(err)
	}
	return draftVO(draft), nil
}

// StopGeneration 暂停整份草稿的后台生成。
func (s *DatasetService) StopGeneration(
	ctx context.Context,
	id string,
) (*vo.EvalDraftVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}

	draft, err := s.PauseGeneration(ctx, id)
	if err != nil {
		return nil, responseError(err)
	}
	return draftVO(draft), nil
}

// ReviewDraft 保存人工复核的接口入口。
func (s *DatasetService) ReviewDraft(
	ctx context.Context,
	id string,
	input *dto.EvalReviewDTO,
) (*vo.EvalDraftVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}

	edits := make([]entity.EvalCandidate, 0, len(input.Items))
	for _, item := range input.Items {
		edits = append(edits, entity.EvalCandidate{
			ID:        item.ID,
			Question:  item.Question,
			Reference: item.Reference,
			Status:    item.Status,
			Critical:  item.Critical,
		})
	}

	draft, err := s.Update(ctx, id, input.Version, edits)
	if err != nil {
		return nil, responseError(err)
	}
	return draftVO(draft), nil
}

// ── 内部实现 ──

// newDraft 按原文内容去重，题目 ID 在草稿内连续编号。
func newDraft(
	tenant, kbNo string,
	sources []entity.EvalSourceChunk,
) (entity.EvalDraft, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return entity.EvalDraft{}, err
	}

	draft := entity.EvalDraft{
		ID:        "draft-" + hex.EncodeToString(nonce[:]),
		Tenant:    tenant,
		KBNo:      kbNo,
		CreatedAt: time.Now().UTC(),
		Items:     []entity.EvalCandidate{},
	}

	seen := map[string]bool{}
	for _, source := range sources {
		hash := sha256.Sum256([]byte(source.Content))
		digest := hex.EncodeToString(hash[:])
		if seen[digest] {
			continue
		}
		seen[digest] = true

		draft.Items = append(draft.Items, entity.EvalCandidate{
			ID:         fmt.Sprintf("q%03d", len(draft.Items)+1),
			DocID:      source.DocID,
			DocName:    source.DocName,
			ChunkID:    source.ChunkID,
			Source:     source.Content,
			SourceHash: digest,
			Status:     "draft",
		})
	}
	return draft, nil
}

// generationSource 给原文加上难度目标后交给出题模型。
func generationSource(candidate entity.EvalCandidate) string {
	if candidate.Difficulty == "" {
		return candidate.Source
	}
	return "[出题难度目标：" + candidate.Difficulty +
		"；简单题考概念，中等题考条件辨析，困难题考本段内多证据推理；" +
		"不得添加资料外信息]\n" +
		candidate.Source
}

// hasMissingQuestions 判断是否还有未生成的问题。
func hasMissingQuestions(items []entity.EvalCandidate) bool {
	for _, candidate := range items {
		if candidate.Status == "draft" && candidate.Question == "" {
			return true
		}
	}
	return false
}

// saveDraftVersion 按版本号保存草稿并递增内存版本。
func (s *DatasetService) saveDraftVersion(
	ctx context.Context,
	draft entity.EvalDraft,
	expected int,
) (entity.EvalDraft, error) {
	if err := s.repo.SaveDraft(ctx, draft, expected); err != nil {
		return draft, err
	}
	draft.Version = expected + 1
	return draft, nil
}

// draftVO 把草稿投影成接口结构。
func draftVO(draft entity.EvalDraft) *vo.EvalDraftVO {
	result := &vo.EvalDraftVO{
		AutoGenerate:    draft.AutoGenerate,
		GenerationState: draft.GenerationState,
		GenerationError: draft.GenerationError,
		ID:              draft.ID,
		Version:         draft.Version,
		Tenant:          draft.Tenant,
		KBNo:            draft.KBNo,
		CreatedAt:       draft.CreatedAt,
		Items:           make([]vo.EvalCandidateVO, 0, len(draft.Items)),
	}
	for _, candidate := range draft.Items {
		result.Items = append(result.Items, vo.EvalCandidateVO{
			SourceType: candidate.SourceType,
			SourceFile: candidate.SourceFile,
			FileHash:   candidate.FileHash,
			StartLine:  candidate.StartLine,
			EndLine:    candidate.EndLine,
			Section:    candidate.Section,
			Region:     candidate.Region,
			Difficulty: candidate.Difficulty,
			ID:         candidate.ID,
			Question:   candidate.Question,
			Reference:  candidate.Reference,
			DocID:      candidate.DocID,
			DocName:    candidate.DocName,
			ChunkID:    candidate.ChunkID,
			Source:     candidate.Source,
			SourceHash: candidate.SourceHash,
			Status:     candidate.Status,
			Critical:   candidate.Critical,
		})
	}
	return result
}
