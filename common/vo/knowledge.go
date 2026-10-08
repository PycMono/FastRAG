package vo

// KnowledgeBaseVO 知识库响应
type KnowledgeBaseVO struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	EmbeddingModel string `json:"embedding_model"`
	DocCount       int    `json:"doc_count"`
	Status         int8   `json:"status"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// KnowledgeBaseListVO 知识库列表响应
type KnowledgeBaseListVO = PageResult[*KnowledgeBaseVO]

// RetrievedChunkVO 检索结果片段
type RetrievedChunkVO struct {
	DocID      string  `json:"doc_id"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
}
