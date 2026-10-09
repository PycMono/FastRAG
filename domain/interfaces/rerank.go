package interfaces

import "context"

// RerankCandidate 待重排候选。
type RerankCandidate struct {
	ChunkID string
	Content string
}

// IRerank 重排能力（P2，本期只留接口与空实现）。
type IRerank interface {
	Rerank(ctx context.Context, query string, cands []RerankCandidate, topN int) ([]int, error)
}
