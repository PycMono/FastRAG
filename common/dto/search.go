package dto

// SearchDTO 检索请求
//
// 只接受 limit，不接受 offset——RRF 融合在应用层，ES 的 from/size
// 作用不到融合后的排名（§7.4）。
type SearchDTO struct {
	Tenant string   `json:"tenant" binding:"required,max=64"`
	KBNos  []string `json:"kb_nos" binding:"required,min=1,max=10,dive,max=64"`
	Query  string   `json:"query" binding:"required,max=1000"`
	Limit  int      `json:"limit" binding:"omitempty,gte=1,lte=100"`

	// RetrieveCount 融合与存活校验后，最多交给精排模型的候选数量。
	// nil 表示用 search.default_retrieve_count，再缺省则 limit*2。
	// 取值范围 [1, 200]；返回给用户的结果仍由 Limit 截断。
	RetrieveCount *int `json:"retrieve_count" binding:"omitempty,gte=1,lte=200"`

	// DenseWeight 向量路权重：<=0.01 只走 BM25，>=0.99 只走 kNN，其余两路加权融合。
	// nil 表示用配置里的默认值。
	DenseWeight *float64 `json:"dense_weight" binding:"omitempty,gte=0,lte=1"`

	// MinScore 分数阈值：>0 时只返回 Score >= MinScore 的结果，0（默认）不过滤。
	//
	// ⚠️ 这里比的分数，量纲随 rerank_switch 走：
	//   精排开启 → 精排模型给的相关性分，大致 0~1；
	//   精排关闭 → 应用层 RRF 融合分 Σ w/(rank_constant+rank)，恒在 0.01~0.02
	//              这个量级（见 rrf.go），给个 0.9 会把结果全筛光。
	// 精排请求降级或候选没有模型分时跳过此阈值；显式关闭精排时保留 RRF 阈值的兼容语义。
	// 新调用方建议使用独立的 FusionMinScore 过滤融合分。
	MinScore float64 `json:"min_score" binding:"omitempty,gte=0"`

	RerankSwitch bool `json:"rerank_switch"`
	// Diagnostics includes per-route ranks, scores, counts and stage durations.
	Diagnostics bool `json:"diagnostics"`
	// FusionMinScore filters RRF scores before reranking, independently of MinScore.
	FusionMinScore float64 `json:"fusion_min_score" binding:"omitempty,gte=0"`

	// EmbedModel 查询向量用哪家算，取 config.embedding.models 里的 key。
	// 留空用配置的 default。必须与库里那批数据的生成方一致，否则检索会退化成
	// 噪声排序且不报错
	EmbedModel string `json:"embed_model" binding:"omitempty,max=64"`

	// RerankModel 精排用哪家，取 config.rerank.models 里的 key。留空用 default。
	// 精排只在查询期发生、不落库，所以换它随时都安全。
	RerankModel string `json:"rerank_model" binding:"omitempty,max=64"`
}
