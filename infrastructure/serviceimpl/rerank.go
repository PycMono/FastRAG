// 本文件基于 OpenAI 兼容 /rerank 协议实现重排序。
//
// 兼容面较宽：SiliconFlow / Jina / Cohere / 火山 / 通义 / vLLM rerank 扩展等
// 都支持 POST {base_url}/rerank，请求体 {model, query, documents, return_documents}，
// 响应 {results: [{index, relevance_score}]}。实现只依赖这个最小公约数。

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

// defaultRerankBatchSize 单次调用最大 documents 数。
// 取 32 是为了兼容多数平台限制（SiliconFlow 64、Jina 32、Cohere 96），保守且够用。
const defaultRerankBatchSize = 32

// defaultRerankConcurrency 分批最大并发数。
// 与 go-ai-knowledge 的 4 对齐，控制对上游服务的压力。
const defaultRerankConcurrency = 4

// defaultRerankTimeout 单批调用默认超时。
const defaultRerankTimeout = 10 * time.Second

// defaultRerankRetryDelay 重试基础退避。
const defaultRerankRetryDelay = 100 * time.Millisecond

// Rerank 基于 OpenAI 兼容 /rerank 协议的重排序实现。
//
// 文件名与类型名不带厂商名：同一实现可对接 SiliconFlow / Jina / Cohere / 火山 / 通义 / vLLM 等
// 所有支持 POST {base_url}/rerank 的服务，只需改配置即可切换。
type Rerank struct {
	client      *http.Client
	baseURL     string
	apiKey      string
	model       string
	batchSize   int
	concurrency int
	defaultTopN int
}

// NewRerankImpl 从配置构造 rerank 客户端。
func NewRerankImpl(conf *config.Config) interfaces.IRerank {
	c := conf.Rerank

	timeout := time.Duration(c.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultRerankTimeout
	}
	batch := c.BatchSize
	if batch <= 0 {
		batch = defaultRerankBatchSize
	}
	concurrency := c.Concurrency
	if concurrency <= 0 {
		concurrency = defaultRerankConcurrency
	}

	return &Rerank{
		client:      &http.Client{Timeout: timeout},
		baseURL:     strings.TrimRight(c.BaseURL, "/"),
		apiKey:      c.APIKey,
		model:       c.Model,
		batchSize:   batch,
		concurrency: concurrency,
		defaultTopN: c.TopN,
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
		retry.Context(ctx),                // ctx 取消时立即放弃，不再发起下一次
		retry.Attempts(2),                 // 初次 + 重试一次，与原实现一致
		retry.Delay(defaultRerankRetryDelay),
		retry.DelayType(retry.FixedDelay), // 固定间隔退避，不用默认的指数+抖动
		retry.LastErrorOnly(true),         // 只透传最后一次的错误，不层层包装
	)
	if err != nil {
		return nil, err
	}
	return scores, nil
}

type rerankResponse struct {
	apiError
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

// score 单次调用 /rerank，返回每条文档的相关性分数（不含重试）。
func (r *Rerank) score(
	ctx context.Context,
	query string,
	docs []string,
) ([]float64, error) {
	payload, err := json.Marshal(map[string]any{
		"model":            r.model,
		"query":            query,
		"documents":        docs,
		"return_documents": false,
	})
	if err != nil {
		return nil, apperrors.ErrRerankFailed.Wrap(err)
	}

	body, status, err := postJSON(ctx, r.client, r.baseURL+"/rerank", r.apiKey,
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

	scores := make([]float64, len(docs))
	seen := make(map[int]struct{}, len(parsed.Results))
	for _, res := range parsed.Results {
		if res.Index < 0 || res.Index >= len(docs) {
			continue
		}
		if _, ok := seen[res.Index]; ok {
			continue
		}
		seen[res.Index] = struct{}{}
		scores[res.Index] = res.RelevanceScore
	}
	return scores, nil
}