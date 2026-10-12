package vo

// DocListItemVO 文档列表的一行。
//
// **刻意不返回 content**：列表是导航用的，一页几十篇、每篇可能上百 KB，
// 背正文会让这个接口的响应体没有上界。正文归详情接口。
// **也不返回 source_url**：它是库级的，同库每篇都一样，逐篇显示是抄 N 遍。
type DocListItemVO struct {
	DocID      uint64 `json:"doc_id"`
	DocName    string `json:"doc_name"`
	Title      string `json:"title"`
	ChunkCount int    `json:"chunk_count"`
	HasContent bool   `json:"has_content"` // format=chunks 进来的为 false，页面据此不摆「看原文」
	CreateTs   int64  `json:"create_ts"`
	UpdateTs   int64  `json:"update_ts"`
}

// DocDetailVO 单篇详情。
type DocDetailVO struct {
	DocID       uint64 `json:"doc_id"`
	DocName     string `json:"doc_name"`
	Title       string `json:"title"`
	Content     string `json:"content"` // 整篇原文；format=chunks 进来的是空串
	HasContent  bool   `json:"has_content"`
	SourceURL   string `json:"source_url"` // 库级来源，来自 KB 行；空串 = 该库没记来源
	ContentHash string `json:"content_hash"`
	ChunkCount  int    `json:"chunk_count"`
	KBID        uint64 `json:"kb_id"`
	CreateTs    int64  `json:"create_ts"`
	UpdateTs    int64  `json:"update_ts"`
}

// ChunkItemVO 一条切片。**不暴露 Score**：枚举不打分，那个字段恒为 0。
type ChunkItemVO struct {
	ChunkID     string `json:"chunk_id"`
	Order       int    `json:"order"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	HeadingPath string `json:"heading_path"`
}

// ChunkListVO 切片清单。Total 是**准确总数**（非封顶值），Items 是当页。
type ChunkListVO struct {
	Total  int64         `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
	Items  []ChunkItemVO `json:"items"`
}
