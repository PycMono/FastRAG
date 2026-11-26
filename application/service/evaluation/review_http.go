package evaluation

import (
	"context"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
)

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

func (s *ReviewService) ListDrafts(ctx context.Context) ([]vo.EvalDraftVO, error) {
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

func (s *ReviewService) DeleteDraft(ctx context.Context, id string) error {
	if !evaluationIDPattern.MatchString(id) {
		return apperrors.ErrInvalidParam
	}
	return responseError(s.Delete(ctx, id))
}

func (s *ReviewService) PrepareDraft(ctx context.Context, input *dto.EvalPrepareDTO) (*vo.EvalDraftVO, error) {
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

func (s *ReviewService) GenerateCandidate(ctx context.Context, id string, input *dto.EvalGenerateDTO) (*vo.EvalDraftVO, error) {
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
func (s *ReviewService) StartGeneration(ctx context.Context, id string, input *dto.EvalGenerationDTO) (*vo.EvalDraftVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}
	draft, err := s.GenerateAll(ctx, id, input.Version)
	if err != nil {
		return nil, responseError(err)
	}
	return draftVO(draft), nil
}

func (s *ReviewService) StopGeneration(ctx context.Context, id string) (*vo.EvalDraftVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}
	draft, err := s.PauseGeneration(ctx, id)
	if err != nil {
		return nil, responseError(err)
	}
	return draftVO(draft), nil
}

func (s *ReviewService) ReviewDraft(ctx context.Context, id string, input *dto.EvalReviewDTO) (*vo.EvalDraftVO, error) {
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

func (s *ReviewService) PublishDataset(ctx context.Context, id string, input *dto.EvalPublishDTO) (*vo.EvalPublishedVO, error) {
	if !evaluationIDPattern.MatchString(id) {
		return nil, apperrors.ErrInvalidParam
	}
	profile := entity.EvalProfile{
		Name:           input.Profile.Name,
		Limit:          input.Profile.Limit,
		RetrieveCount:  input.Profile.RetrieveCount,
		DenseWeight:    input.Profile.DenseWeight,
		RerankSwitch:   input.Profile.RerankSwitch,
		EmbedModel:     input.Profile.EmbedModel,
		RerankModel:    input.Profile.RerankModel,
		MinScore:       input.Profile.MinScore,
		FusionMinScore: input.Profile.FusionMinScore,
	}
	name, err := s.PublishSelection(ctx, id, input.Version, profile, input.Count)
	if err != nil {
		return nil, responseError(err)
	}
	return &vo.EvalPublishedVO{Dataset: name}, nil
}

func (s *ReviewService) SetRunBaseline(ctx context.Context, id string) error {
	if !evaluationIDPattern.MatchString(id) {
		return apperrors.ErrInvalidParam
	}
	return responseError(s.SetBaseline(ctx, id))
}

func (s *ReviewService) CompareRun(ctx context.Context, id string, input *dto.EvalCompareDTO) (*vo.EvalComparisonVO, error) {
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
