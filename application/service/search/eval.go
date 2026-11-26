// 本文件是 search 服务对评测的进程内适配：直接复用生产检索，不经过 HTTP。

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
)

// SearchForEvaluation 用生产检索配置执行一次评测查询。
func (s *Service) SearchForEvaluation(
	ctx context.Context,
	testCase entity.EvalCase,
	profile entity.EvalProfile,
) (*entity.EvalRetrieval, error) {
	request := &dto.SearchDTO{
		Tenant:         testCase.Tenant,
		KBNos:          testCase.KBNos,
		Query:          testCase.Question,
		Limit:          profile.Limit,
		RetrieveCount:  &profile.RetrieveCount,
		DenseWeight:    &profile.DenseWeight,
		RerankSwitch:   profile.RerankSwitch,
		Diagnostics:    true,
		EmbedModel:     profile.EmbedModel,
		RerankModel:    profile.RerankModel,
		MinScore:       profile.MinScore,
		FusionMinScore: profile.FusionMinScore,
	}

	result, err := s.Search(ctx, request)
	if err != nil {
		return nil, err
	}

	items := make([]entity.EvalHit, 0, len(result.Items))
	seen := make(map[string]bool, len(result.Items))
	for _, hit := range result.Items {
		if hit.ChunkID == "" || seen[hit.ChunkID] {
			return nil, fmt.Errorf("检索结果片段 ID 缺失或重复")
		}
		seen[hit.ChunkID] = true
		items = append(items, entity.EvalHit{
			ChunkID:     hit.ChunkID,
			DocID:       strconv.FormatUint(hit.DocID, 10),
			Content:     hit.Content,
			Title:       hit.Title,
			HeadingPath: hit.HeadingPath,
			Score:       hit.Score,
		})
	}

	return &entity.EvalRetrieval{
		Items:       items,
		Meta:        toEvalMap(result.Meta),
		Diagnostics: toEvalMap(result.Diagnostics),
	}, nil
}

func toEvalMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return map[string]any{}
	}
	return result
}

var _ interfaces.IEvalSearcher = (*Service)(nil)
