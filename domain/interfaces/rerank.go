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

// NoRerankScore 是"这一条没有上游分"的哨兵。
//
// 两条路会给出它：候选内容为空（挑出来没发给上游），以及 rerank 未启用
// （nopRerank 原序返回）。调用方见到负分要保留自己原有的分数，不要写进展示字段。
const NoRerankScore = -1

// ScoredIndex 重排结果的一条：候选在原数组里的下标，加上上游给的相关性分。
//
// 为什么要把分数一并带回来：只回下标的话，调用方重排了顺序却无从更新 score，
// 页面上就会出现"0.0150 排在 0.0156 前面"——顺序是精排给的，分数还是旧的融合分，
// 自相矛盾，而且看不出精排到底有没有生效。
type ScoredIndex struct {
	Index int
	Score float64
}

// IRerank 重排能力。
type IRerank interface {
	Rerank(ctx context.Context, query string, cands []RerankCandidate, topN int) ([]ScoredIndex, error)
}

// IRerankRegistry 按名字取重排序实现。
//
// 与 IEmbeddingRegistry 的差别只有一处：配置里 enabled=false 时，
// 对**任何**名字都返回空实现且永不出错——保持"没配 rerank 服务也能正常启动
// 和检索"这个既有语义。
type IRerankRegistry interface {
	Get(name string) (IRerank, error)
}
