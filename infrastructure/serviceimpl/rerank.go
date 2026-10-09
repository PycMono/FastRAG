// 本文件实现重排序：一个实现，两处形状开关。
//
// 分批、errgroup 并发、retry-go 重试、空内容沉底、按分数稳定排序全部共用；
// 随协议变的只有"请求体怎么拼"（buildBody）和"响应从哪取分数"（parseScores）。
//
//	openai    POST {url}  body {model, query, documents, return_documents}
//	                      响应 results[].{index, relevance_score}
//	dashscope POST {url}（原生 /api/v1/services/rerank/text-rerank/text-rerank）
//	                      body {model, input:{query, documents}, parameters:{top_n, return_documents}}
//	                      响应 output.results[].{index, relevance_score}
//
// ⚠️ dashscope 的 parameters.top_n 必须等于本批 documents 数量，见 buildBody 的注释。

package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/avast/retry-go/v4"
	"golang.org/x/sync/errgroup"
)

// maxRerankResponseBytes 响应体读取上限。
// rerank 响应很小（通常几 KB），16MB 已经非常宽裕；无上限 ReadAll 会被畸形响应打爆内存。
const maxRerankResponseBytes = 16 << 20

// defaultRerankRetryDelay 重试基础退避。
const defaultRerankRetryDelay = 100 * time.Millisecond

// Rerank 重排序实现。
//
// 文件名与类型名不带厂商名：同一个实现靠 protocol 开关对接所有服务商。
type Rerank struct {
	client      *http.Client
	protocol    string
	url         string // 完整端点，不再拼 /rerank 后缀
	apiKey      string
	model       string
	batchSize   int
	concurrency int
	defaultTopN int
}

// newRerank 从一份已合并好的参数构造实现。
//
// 只有注册表调它——"继承外层"在 Params() 里就填完了，这里不再判 0。
func newRerank(p config.RerankParams) *Rerank {
	return &Rerank{
		client:      &http.Client{Timeout: time.Duration(p.TimeoutMS) * time.Millisecond},
		protocol:    p.Protocol,
		url:         p.URL,
		apiKey:      p.APIKey,
		model:       p.Model,
		batchSize:   p.BatchSize,
		concurrency: p.Concurrency,
		defaultTopN: p.TopN,
	}
}

// Rerank 对候选列表重排序，返回原数组下标顺序。
//
// topN <= 0 时返回全部候选按相关性排序后的下标。
// 空内容候选自动给最低分，沉底但不丢失（保持下标完整性）。
func (r *Rerank) Rerank(
	ctx context.Context,
	query string,
	cands []interfaces.RerankCandidate,
	topN int,
) ([]int, error) {
	if len(cands) == 0 {
		return []int{}, nil
	}
	if topN <= 0 {
		topN = r.defaultTopN
	}
	if topN <= 0 || topN > len(cands) {
		topN = len(cands)
	}

	// 给每个候选记录原始下标，并过滤掉空内容（空内容不发给服务端，直接给最低分）
	infos := make([]candInfo, 0, len(cands))
	emptyScores := make([]scoredIdx, 0)
	for i, c := range cands {
		content := r.candidateText(c)
		if strings.TrimSpace(content) == "" {
			emptyScores = append(emptyScores, scoredIdx{idx: i, score: -1})
			continue
		}
		infos = append(infos, candInfo{origIdx: i, content: content})
	}

	// 全空则原序返回前 topN
	if len(infos) == 0 {
		out := make([]int, len(cands))
		for i := range out {
			out[i] = i
		}
		return out[:topN], nil
	}

	// 分批并发调用 /rerank
	scored, err := r.rerankBatches(ctx, query, infos)
	if err != nil {
		return nil, err
	}

	// 合并空内容候选，它们 score 为 -1 必然沉底
	scored = append(scored, emptyScores...)

	// 按 score 降序，score 相同保持原下标稳定（SliceStable）
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	out := make([]int, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.idx)
	}
	if len(out) > topN {
		out = out[:topN]
	}
	return out, nil
}

// scoredIdx 一个候选的原始下标与 rerank 分数。
type scoredIdx struct {
	idx   int
	score float64
}

// candInfo 一个有效候选（非空内容）的原始下标与待rerank文本。
type candInfo struct {
	origIdx int
	content string
}

// candidateText 把候选的标题、标题链、正文拼成一段文本。
//
// 标题链（heading_path）是结构感知切片产生的上下文，对 rerank 模型判断相关性有帮助；
// 直接拼入不会增加太多 token，但能让模型知道这段内容在原文中的位置。
func (r *Rerank) candidateText(c interfaces.RerankCandidate) string {
	var sb strings.Builder
	if c.Title != "" {
		sb.WriteString(c.Title)
	}
	if c.HeadingPath != "" {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(c.HeadingPath)
	}
	if c.Content != "" {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(c.Content)
	}
	return sb.String()
}

// rerankBatches 按 batchSize 切分并并发调用 /rerank。
//
// 不用 errgroup.WithContext：单批失败不取消其他批，在途请求自然跑完，
// 保持「首个错误胜出、其余照常完成」的语义。
func (r *Rerank) rerankBatches(
	ctx context.Context,
	query string,
	infos []candInfo,
) ([]scoredIdx, error) {
	batchCount := (len(infos) + r.batchSize - 1) / r.batchSize
	results := make([][]scoredIdx, batchCount)

	g := new(errgroup.Group)
	g.SetLimit(r.concurrency)

	for b := 0; b < batchCount; b++ {
		start := b * r.batchSize
		end := start + r.batchSize
		if end > len(infos) {
			end = len(infos)
		}
		batch := infos[start:end]

		g.Go(func() error {
			scores, err := r.scoreBatch(ctx, query, batch)
			if err != nil {
				return err
			}
			out := make([]scoredIdx, len(batch))
			for i, info := range batch {
				out[i] = scoredIdx{idx: info.origIdx, score: scores[i]}
			}
			results[b] = out
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	merged := make([]scoredIdx, 0, len(infos))
	for _, rs := range results {
		merged = append(merged, rs...)
	}
	return merged, nil
}

// scoreBatch 对单批调用 /rerank，失败时重试一次。
func (r *Rerank) scoreBatch(
	ctx context.Context,
	query string,
	batch []candInfo,
) ([]float64, error) {
	docs := make([]string, len(batch))
	for i, info := range batch {
		docs[i] = info.content
	}

	var scores []float64
	err := retry.Do(
		func() error {
			var err error
			scores, err = r.score(ctx, query, docs)
			return err
		},
		retry.Context(ctx), // ctx 取消时立即放弃，不再发起下一次
		retry.Attempts(2),  // 初次 + 重试一次，与原实现一致
		retry.Delay(defaultRerankRetryDelay),
		retry.DelayType(retry.FixedDelay), // 固定间隔退避，不用默认的指数+抖动
		retry.LastErrorOnly(true),         // 只透传最后一次的错误，不层层包装
	)
	if err != nil {
		return nil, err
	}
	return scores, nil
}

// rerankResult 一条候选的分数。两个协议的这个结构完全一样，
// 区别只在外层：openai 挂在 results，dashscope 挂在 output.results。
type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// rerankResponse 两种协议的响应。按协议只认其中一个，另一个留空。
type rerankResponse struct {
	apiError
	Results []rerankResult `json:"results"`
	Output  *struct {
		Results []rerankResult `json:"results"`
	} `json:"output"`
}

// buildBody 按协议拼请求体。
//
// ⚠️ dashscope 的 parameters.top_n 是"只返回前 N 条"，而我们**需要每一批的完整
// 分数**才能合并排序。示例里写 top_n: 5 照抄进分批逻辑，每批就只有 5 条候选拿到
// 分数，其余全部落回 0 分，批与批之间的分数不再可比，合并后顺序是乱的——而且不报错。
// 所以这里恒等于本批长度，业务上的 topN 永远只在最后自己截断（设计文档 §4.3）。
func (r *Rerank) buildBody(query string, docs []string) any {
	if r.protocol == config.ProtocolDashScope {
		return map[string]any{
			"model": r.model,
			"input": map[string]any{
				"query":     query,
				"documents": docs,
			},
			"parameters": map[string]any{
				"top_n":            len(docs),
				"return_documents": false,
			},
		}
	}
	return map[string]any{
		"model":            r.model,
		"query":            query,
		"documents":        docs,
		"return_documents": false,
	}
}

// parseScores 按协议取分数并按 index 回填，缺的留 0。
func (r *Rerank) parseScores(parsed *rerankResponse, n int) []float64 {
	results := parsed.Results
	if r.protocol == config.ProtocolDashScope && parsed.Output != nil {
		results = parsed.Output.Results
	}

	scores := make([]float64, n)
	seen := make(map[int]struct{}, len(results))
	for _, res := range results {
		if res.Index < 0 || res.Index >= n {
			continue
		}
		if _, ok := seen[res.Index]; ok {
			continue
		}
		seen[res.Index] = struct{}{}
		scores[res.Index] = res.RelevanceScore
	}
	return scores
}

// score 单次调用，返回每条文档的相关性分数（不含重试）。
func (r *Rerank) score(
	ctx context.Context,
	query string,
	docs []string,
) ([]float64, error) {
	payload, err := json.Marshal(r.buildBody(query, docs))
	if err != nil {
		return nil, apperrors.ErrRerankFailed.Wrap(err)
	}

	body, status, err := postJSON(ctx, r.client, r.url, r.apiKey,
		payload, maxRerankResponseBytes, apperrors.ErrRerankFailed)
	if err != nil {
		return nil, err
	}

	var parsed rerankResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, apperrors.NewSysError(apperrors.CodeRerankFail, fmt.Sprintf(
			"rerank 响应不是合法 JSON，HTTP %d: %s", status, truncateBody(body, 512)))
	}
	if err := parsed.apiError.check(status, apperrors.CodeRerankFail, "rerank", body); err != nil {
		return nil, err
	}

	return r.parseScores(&parsed, len(docs)), nil
}
