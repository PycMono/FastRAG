package interfaces

import "context"

// RerankCandidate 待重排候选。
//
// Title / HeadingPath 供实现层选择是否拼入传给 rerank 模型的文本。
// 它们不强制参与 score 计算，但保留后 search.Service 可以把标题链一起喂给模型。
type RerankCandidate struct {
	ChunkID     string
	Title       string
	HeadingPath string
	Content     string
}

// IRerank 重排能力（P2，本期只留接口与空实现）。
type IRerank interface {
	Rerank(ctx context.Context, query string, cands []RerankCandidate, topN int) ([]int, error)
}
