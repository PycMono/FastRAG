package vo

// KnowledgeBaseVO 知识库对外表示（本服务只读，不提供写接口）
type KnowledgeBaseVO struct {
	ID            uint64 `json:"id"`
	No            string `json:"no"`
	Account       string `json:"account"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	KnowledgeType string `json:"knowledge_type"`
	SearchMode    string `json:"search_mode"`
	BizTag        string `json:"biz_tag"`
	DocCount      int64  `json:"doc_count"`
	ChunkCount    int64  `json:"chunk_count"`
}

// SearchItemVO 一条检索结果
//
// 带 Content：内容下沉在 ES（D1），这里不需要回表取正文，
// 只补了 DocName / KBName 两个展示字段（来自存活校验那一次批量 SQL）。
type SearchItemVO struct {
	ChunkID     string  `json:"chunk_id"`
	DocID       uint64  `json:"doc_id"`
	DocName     string  `json:"doc_name"`
	KBID        uint64  `json:"kb_id"`
	KBName      string  `json:"kb_name"`
	KBNo        string  `json:"kb_no"`
	Order       int     `json:"order"`
	Title       string  `json:"title"`
	Content     string  `json:"content"`
	HeadingPath string  `json:"heading_path"`
	Score       float64 `json:"score"`
}

// SearchResultVO 检索响应
//
// 没有 total / page：本期只做 top-N，不做深翻页（§7.4）。
type SearchResultVO struct {
	Items []*SearchItemVO `json:"items"`
}

// DocIngestVO 导入响应
type DocIngestVO struct {
	DocID      uint64 `json:"doc_id"`
	DocName    string `json:"doc_name"`
	ChunkCount int    `json:"chunk_count"`
	Created    bool   `json:"created"` // true = 新建，false = 覆盖已有同名文档
}

// DocDeleteVO 删除响应
type DocDeleteVO struct {
	DocName      string `json:"doc_name"`
	DeletedChunks int64 `json:"deleted_chunks"`
}
