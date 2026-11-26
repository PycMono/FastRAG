// 本文件是题库发布：抽样选题、来源哈希校验、固定不可变版本。
// 草稿见 drafts.go，验收见 acceptance.go。

package evaluation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
)

// PublishSelection 固定抽中的题目，后续重跑仍使用同一批 ID。
func (s *DatasetService) PublishSelection(
	ctx context.Context,
	id string,
	version int,
	profile entity.EvalProfile,
	count int,
) (string, error) {
	if !s.tasks.opts.Enabled {
		return "", ErrDisabled
	}
	if count < 0 || count > 1000 {
		return "", ErrInvalid
	}

	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return "", err
	}
	if draft.Version != version {
		return "", ErrInvalid
	}

	if count > 0 {
		selected, err := selectApprovedCandidates(draft.Items, count)
		if err != nil {
			return "", err
		}
		draft.Items = selected
	}

	hashes, err := s.currentSourceHashes(ctx, draft)
	if err != nil {
		return "", err
	}
	cases, err := buildCases(draft, hashes)
	if err != nil {
		return "", err
	}
	if len(cases) < 3 {
		return "", fmt.Errorf("%w: 至少复核通过 3 题才能发布", ErrInvalid)
	}

	name := id + fmt.Sprintf("-v%d", version)
	if count > 0 {
		var nonce [4]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", err
		}
		name += "-n" + fmt.Sprint(count) + "-" + hex.EncodeToString(nonce[:])
	}
	dataset := entity.EvalDataset{
		Name:     name,
		Profiles: []entity.EvalProfile{profile},
		Cases:    cases,
	}
	dataset.Manifest = buildManifest(draft, dataset, version)

	if err := validate(dataset); err != nil {
		return "", err
	}
	return name, s.repo.Publish(ctx, dataset)
}

// PublishDataset 发布题库的接口入口。
func (s *DatasetService) PublishDataset(
	ctx context.Context,
	id string,
	input *dto.EvalPublishDTO,
) (*vo.EvalPublishedVO, error) {
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

	name, err := s.PublishSelection(
		ctx,
		id,
		input.Version,
		profile,
		input.Count,
	)
	if err != nil {
		return nil, responseError(err)
	}
	return &vo.EvalPublishedVO{Dataset: name}, nil
}

// ── 内部实现 ──

// currentSourceHashes 再读一次当前片段；原文变化必须重新抽样复核。
func (s *DatasetService) currentSourceHashes(
	ctx context.Context,
	draft entity.EvalDraft,
) (map[string]string, error) {
	sources, err := s.sources.SourcesForEvaluation(
		ctx,
		draft.Tenant,
		draft.KBNo,
		entity.EvalSamplePolicy{Total: 10000},
	)
	if err != nil {
		return nil, err
	}

	hashes := make(map[string]string, len(sources))
	for _, source := range sources {
		hash := sha256.Sum256([]byte(source.Content))
		hashes[source.ChunkID] = hex.EncodeToString(hash[:])
	}
	return hashes, nil
}

// buildCases 把已通过且来源未变的候选题转成评测用例。
func buildCases(
	draft entity.EvalDraft,
	currentHashes map[string]string,
) ([]entity.EvalCase, error) {
	cases := make([]entity.EvalCase, 0, len(draft.Items))
	for _, candidate := range draft.Items {
		if candidate.Status != "approved" {
			continue
		}
		if err := verifyCandidateSource(candidate, currentHashes); err != nil {
			return nil, err
		}
		cases = append(cases, buildCase(draft, candidate))
	}
	return cases, nil
}

// verifyCandidateSource 校验候选来源哈希和 Markdown 证据完整性。
func verifyCandidateSource(
	candidate entity.EvalCandidate,
	currentHashes map[string]string,
) error {
	if candidate.SourceType == "original_markdown" {
		hash := sha256.Sum256([]byte(candidate.Source))
		if candidate.FileHash == "" ||
			candidate.StartLine < 1 ||
			candidate.EndLine < candidate.StartLine ||
			hex.EncodeToString(hash[:]) != candidate.SourceHash {
			return fmt.Errorf("%w: 原始 Markdown 证据记录不完整", ErrInvalid)
		}
		return nil
	}
	if currentHashes[candidate.ChunkID] != candidate.SourceHash {
		return fmt.Errorf("%w: 原文已变化或片段不再存在，请重新取样", ErrInvalid)
	}
	return nil
}

// buildCase 把候选题转成固定评测用例。
func buildCase(draft entity.EvalDraft, candidate entity.EvalCandidate) entity.EvalCase {
	testCase := entity.EvalCase{
		Difficulty: candidate.Difficulty,
		ID:         candidate.ID,
		Question:   candidate.Question,
		Reference:  candidate.Reference,
		Tenant:     draft.Tenant,
		KBNos:      []string{draft.KBNo},
		Category:   "reviewed-source",
		Split:      "dev",
		Answerable: true,
		Critical:   candidate.Critical,
		ReferenceSources: []entity.EvalSource{{
			DocID:        candidate.DocID,
			HeadingPath:  candidate.Section,
			SourceType:   candidate.SourceType,
			SourceFile:   candidate.SourceFile,
			FileHash:     candidate.FileHash,
			StartLine:    candidate.StartLine,
			EndLine:      candidate.EndLine,
			Evidence:     candidate.Source,
			EvidenceHash: candidate.SourceHash,
		}},
		ReferenceContextIDs: []string{candidate.ChunkID},
	}
	if candidate.SourceType == "original_markdown" {
		testCase.ReferenceContextIDs = nil
	}
	return testCase
}

// buildManifest 生成发布快照的 manifest。
func buildManifest(
	draft entity.EvalDraft,
	dataset entity.EvalDataset,
	version int,
) entity.EvalManifest {
	data, _ := json.Marshal(draft.Items)
	hash := sha256.Sum256(data)

	sourceHashes := map[string]string{}
	for _, candidate := range draft.Items {
		if candidate.Status == "approved" && candidate.SourceType != "original_markdown" {
			sourceHashes[candidate.ChunkID] = candidate.SourceHash
		}
	}

	return entity.EvalManifest{
		Reviewed:          true,
		DatasetVersion:    dataset.Name,
		AnnotationVersion: fmt.Sprintf("review-%d", version),
		CorpusSnapshot:    "live-source-sample",
		CorpusHash:        hex.EncodeToString(hash[:]),
		RetrievalSettings: map[string]any{
			"scope":         draft.KBNo,
			"snapshot_kind": "reviewed-source-sample-not-full-index",
			"source_hashes": sourceHashes,
		},
	}
}

// selectApprovedCandidates 只从已通过题中按文档和难度均衡抽取。
func selectApprovedCandidates(
	items []entity.EvalCandidate,
	count int,
) ([]entity.EvalCandidate, error) {
	groups, keys, total := groupApprovedCandidates(items)
	if count < 3 || count > total {
		return nil, fmt.Errorf(
			"%w: 选题数须为 3–%d，不能使用未审核题补足",
			ErrInvalid,
			total,
		)
	}

	// 分组和组内顺序都随机，避免小样本永远偏向第一本文档或第一种难度。
	if err := shuffleRandom(keys); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if err := shuffleRandom(groups[key]); err != nil {
			return nil, err
		}
	}
	return interleaveGroups(groups, keys, count), nil
}

// groupApprovedCandidates 按文档和难度分组已通过题。
func groupApprovedCandidates(
	items []entity.EvalCandidate,
) (map[string][]entity.EvalCandidate, []string, int) {
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
	return groups, keys, total
}

// shuffleRandom 使用密码学随机数打乱切片。
func shuffleRandom[T any](values []T) error {
	for index := len(values) - 1; index > 0; index-- {
		position, err := rand.Int(rand.Reader, big.NewInt(int64(index+1)))
		if err != nil {
			return err
		}
		values[index], values[position.Int64()] = values[position.Int64()], values[index]
	}
	return nil
}

// interleaveGroups 轮流从各分组取题，直到达到目标数量。
func interleaveGroups(
	groups map[string][]entity.EvalCandidate,
	keys []string,
	count int,
) []entity.EvalCandidate {
	result := make([]entity.EvalCandidate, 0, count)
	for offset := 0; len(result) < count; offset++ {
		for _, key := range keys {
			if offset >= len(groups[key]) {
				continue
			}
			result = append(result, groups[key][offset])
			if len(result) == count {
				return result
			}
		}
	}
	return result
}
