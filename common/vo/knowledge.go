package vo

// SearchItemVO 一条检索结果
//
// 带 Content：内容下沉在 ES（D1），这里不需要回表取正文，
// 只补了 DocName / KBName 两个展示字段（来自存活校验那一次批量 SQL）。
type SearchItemVO struct {
	ChunkID     string  `json:"chunk_id"`
	DocID       uint64  `json:"doc_id,string"`
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
//
// DocID 用 `,string` 序列化成 **JSON 字符串**，不是数字。雪花 ID 是 19 位，
// 超过 JS 的 2^53 精确范围：浏览器的 JSON.parse 会把它**悄悄舍入成另一个数**
// （2109838273063948288 → 2109838273063948300），再回传过来就是查无此文档。
// 所有对外吐 doc_id 的地方（本文件与 doc_read.go 共 4 处）都必须是字符串。
// 入参侧不用管：`doc_id` 走 query string，本来就是字符串。
type DocIngestVO struct {
	DocID      uint64 `json:"doc_id,string"`
	DocName    string `json:"doc_name"`
	ChunkCount int    `json:"chunk_count"`
	Created    bool   `json:"created"` // true = 新建，false = 覆盖已有同名文档
}

// DocDeleteVO 删除响应
type DocDeleteVO struct {
	DocName       string `json:"doc_name"`
	DeletedChunks int64  `json:"deleted_chunks"`
}
