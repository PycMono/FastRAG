package dto

// SearchDTO 检索请求
//
// 只接受 limit，不接受 offset——RRF 融合在应用层，ES 的 from/size
// 作用不到融合后的排名（§7.4）。
type SearchDTO struct {
	Account string   `json:"account" binding:"required,max=64"`
	KBNos   []string `json:"kb_nos" binding:"required,min=1,max=10,dive,max=64"`
	Query   string   `json:"query" binding:"required,max=1000"`

	BizTags []string `json:"biz_tags" binding:"omitempty,max=10,dive,max=64"`

	Limit int `json:"limit" binding:"omitempty,gte=1,lte=100"`

	// RetrieveCount rerank 开启时，ES 召回多少条交给 rerank 精排。
	// nil 表示用 search.default_retrieve_count，再缺省则 limit*2。
	// 取值范围 [1, 200]；返回给用户的结果仍由 Limit 截断。
	RetrieveCount *int `json:"retrieve_count" binding:"omitempty,gte=1,lte=200"`

	// DenseWeight 向量路权重：<=0.01 只走 BM25，>=0.99 只走 kNN，其余两路加权融合。
	// nil 表示用配置里的默认值。
	DenseWeight *float64 `json:"dense_weight" binding:"omitempty,gte=0,lte=1"`

	RerankSwitch bool `json:"rerank_switch"`
}
