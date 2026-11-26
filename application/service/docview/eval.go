// 本文件是 docview 服务对评测的进程内适配：直接读取文档和切片，不经过 HTTP。

package docview

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
)

// SourcesForEvaluation 分页遍历原文，按文档均衡、章节分层、章节内位置分散取样。
func (s *Service) SourcesForEvaluation(
	ctx context.Context,
	tenant, kbNo string,
	policy entity.EvalSamplePolicy,
) ([]entity.EvalSourceChunk, error) {
	type sourceDocument struct {
		ID   uint64
		Name string
	}

	documents := []sourceDocument{}
	for page := 1; ; page++ {
		result, err := s.List(ctx, &dto.DocListQuery{
			Tenant: tenant,
			KBNo:   kbNo,
			PageQuery: dto.PageQuery{
				Page:     page,
				PageSize: 100,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("读取文档列表失败: %w", err)
		}
		for _, doc := range result.List {
			documents = append(documents, sourceDocument{ID: doc.DocID, Name: doc.DocName})
		}
		if len(documents) >= int(result.Total) {
			break
		}
		if len(result.List) == 0 {
			return nil, fmt.Errorf("文档分页提前结束，拒绝不完整取样")
		}
	}

	groups := make([][]entity.EvalSourceChunk, 0, len(documents))
	seenContent := map[[32]byte]bool{}
	for _, document := range documents {
		group := []entity.EvalSourceChunk{}
		for offset := 0; ; {
			result, err := s.Chunks(ctx, &dto.ChunkListQuery{
				Tenant: tenant,
				DocID:  document.ID,
				Limit:  200,
				Offset: offset,
			})
			if err != nil {
				return nil, fmt.Errorf("读取文档切片失败: %w", err)
			}
			for _, chunk := range result.Items {
				if len([]rune(chunk.Content)) < 100 || len([]rune(chunk.Content)) > 6000 {
					continue
				}
				fingerprint := sha256.Sum256([]byte(strings.Join(strings.Fields(chunk.Content), " ")))
				if seenContent[fingerprint] {
					continue
				}
				seenContent[fingerprint] = true
				group = append(group, entity.EvalSourceChunk{
					DocID:       strconv.FormatUint(document.ID, 10),
					DocName:     document.Name,
					ChunkID:     chunk.ChunkID,
					Content:     chunk.Content,
					HeadingPath: chunk.HeadingPath,
					Order:       chunk.Order,
				})
			}
			offset += len(result.Items)
			if offset >= int(result.Total) {
				break
			}
			if len(result.Items) == 0 {
				return nil, fmt.Errorf("切片分页提前结束，拒绝不完整取样")
			}
		}
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Order == group[j].Order {
				return group[i].ChunkID < group[j].ChunkID
			}
			return group[i].Order < group[j].Order
		})
		groups = append(groups, group)
	}
	return sampleEvaluationSources(groups, policy), nil
}

func sampleEvaluationSources(
	groups [][]entity.EvalSourceChunk,
	policy entity.EvalSamplePolicy,
) []entity.EvalSourceChunk {
	quotas := make([]int, len(groups))
	caps := make([]int, len(groups))
	for index, group := range groups {
		caps[index] = len(group)
		if policy.PerDoc > 0 && policy.PerDoc < caps[index] {
			caps[index] = policy.PerDoc
		}
	}
	remaining := policy.Total
	for remaining > 0 {
		changed := false
		for index := range groups {
			if remaining > 0 && quotas[index] < caps[index] {
				quotas[index]++
				remaining--
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	result := []entity.EvalSourceChunk{}
	for index, group := range groups {
		chapters := map[string][]entity.EvalSourceChunk{}
		names := []string{}
		for _, source := range group {
			if _, exists := chapters[source.HeadingPath]; !exists {
				names = append(names, source.HeadingPath)
			}
			chapters[source.HeadingPath] = append(chapters[source.HeadingPath], source)
		}
		chapterCount := min(quotas[index], len(names))
		selectedNames := make([]string, chapterCount)
		for position := range selectedNames {
			selectedNames[position] = names[(2*position+1)*len(names)/(2*chapterCount)]
		}
		chapterQuotas := make([]int, chapterCount)
		remaining := quotas[index]
		for remaining > 0 {
			changed := false
			for position, name := range selectedNames {
				if remaining > 0 && chapterQuotas[position] < len(chapters[name]) {
					chapterQuotas[position]++
					remaining--
					changed = true
				}
			}
			if !changed {
				break
			}
		}
		for position, name := range selectedNames {
			items := chapters[name]
			quota := chapterQuotas[position]
			for offset := 0; offset < quota; offset++ {
				result = append(result, items[(2*offset+1)*len(items)/(2*quota)])
			}
		}
	}
	return result
}

var _ interfaces.IEvalSourceReader = (*Service)(nil)
