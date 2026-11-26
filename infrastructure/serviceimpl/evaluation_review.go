package serviceimpl

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

func (h *EvalHTTP) getEvaluationData(ctx context.Context, path string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(h.SearchURL, "/")+path, nil)
	if err != nil {
		return err
	}

	response, err := h.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("原文读取失败 HTTP %d", response.StatusCode)
	}

	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return fmt.Errorf("原文响应不可读取或过大")
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Code != 0 || len(envelope.Data) == 0 {
		return fmt.Errorf("原文读取业务失败")
	}
	return json.Unmarshal(envelope.Data, output)
}

// Sources 分页遍历原文，按文档均衡、章节分层、章节内位置分散取样。
func (h *EvalHTTP) Sources(ctx context.Context, tenant, kbNo string, policy entity.EvalSamplePolicy) ([]entity.EvalSourceChunk, error) {
	type sourceDocument struct {
		DocID   string `json:"doc_id"`
		DocName string `json:"doc_name"`
	}
	documents := []sourceDocument{}
	for page := 1; ; page++ {
		params := url.Values{
			"tenant": {tenant}, "kb_no": {kbNo},
			"page": {strconv.Itoa(page)}, "page_size": {"100"},
		}
		var response struct {
			Total int              `json:"total"`
			Items []sourceDocument `json:"list"`
		}
		if err := h.getEvaluationData(ctx, "/api/v1/docs?"+params.Encode(), &response); err != nil {
			return nil, err
		}
		documents = append(documents, response.Items...)
		if len(documents) >= response.Total {
			break
		}
		if len(response.Items) == 0 {
			return nil, fmt.Errorf("文档分页提前结束，拒绝不完整取样")
		}
	}

	groups := make([][]entity.EvalSourceChunk, 0, len(documents))
	seenContent := map[[32]byte]bool{}
	for _, document := range documents {
		group := []entity.EvalSourceChunk{}
		for offset := 0; ; {
			var response struct {
				Total int `json:"total"`
				Items []struct {
					ChunkID     string `json:"chunk_id"`
					Content     string `json:"content"`
					HeadingPath string `json:"heading_path"`
					Order       int    `json:"order"`
				} `json:"items"`
			}
			params := url.Values{
				"tenant": {tenant}, "doc_id": {document.DocID},
				"limit": {"200"}, "offset": {strconv.Itoa(offset)},
			}
			if err := h.getEvaluationData(ctx, "/api/v1/docs/chunks?"+params.Encode(), &response); err != nil {
				return nil, err
			}
			for _, chunk := range response.Items {
				if len([]rune(chunk.Content)) < 100 || len([]rune(chunk.Content)) > 6000 {
					continue
				}
				fingerprint := sha256.Sum256([]byte(strings.Join(strings.Fields(chunk.Content), " ")))
				if seenContent[fingerprint] {
					continue
				}
				seenContent[fingerprint] = true
				group = append(group, entity.EvalSourceChunk{
					DocID:       document.DocID,
					DocName:     document.DocName,
					ChunkID:     chunk.ChunkID,
					Content:     chunk.Content,
					HeadingPath: chunk.HeadingPath,
					Order:       chunk.Order,
				})
			}
			offset += len(response.Items)
			if offset >= response.Total {
				break
			}
			if len(response.Items) == 0 {
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

func sampleEvaluationSources(groups [][]entity.EvalSourceChunk, policy entity.EvalSamplePolicy) []entity.EvalSourceChunk {
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

func (h *EvalHTTP) Generate(ctx context.Context, source string) (entity.EvalGeneratedQuestion, error) {
	var output entity.EvalGeneratedQuestion
	judge, err := h.Judge()
	if err != nil {
		return output, err
	}
	input := struct {
		Source string                 `json:"source"`
		Judge  config.EvaluationJudge `json:"judge"`
	}{Source: source, Judge: judge}

	err = h.post(ctx, strings.TrimRight(h.ScoreURL, "/")+"/generate", h.Token, input, &output)
	return output, err
}
