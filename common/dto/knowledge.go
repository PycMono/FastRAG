package dto

// CreateKnowledgeBaseDTO 创建知识库请求
type CreateKnowledgeBaseDTO struct {
	Name           string `json:"name" binding:"required,max=128"`
	Description    string `json:"description" binding:"max=512"`
	EmbeddingModel string `json:"embedding_model" binding:"max=64"`
}

// UpdateKnowledgeBaseDTO 更新知识库请求
// 字段为指针：nil 表示本次不更新该字段
type UpdateKnowledgeBaseDTO struct {
	Name           *string `json:"name" binding:"omitempty,max=128"`
	Description    *string `json:"description" binding:"omitempty,max=512"`
	EmbeddingModel *string `json:"embedding_model" binding:"omitempty,max=64"`
	Status         *int8   `json:"status" binding:"omitempty,oneof=0 1"`
}

// ListKnowledgeBaseQuery 知识库列表查询参数
type ListKnowledgeBaseQuery struct {
	PageQuery
}

// SearchKnowledgeBaseDTO 知识库检索请求
type SearchKnowledgeBaseDTO struct {
	Query string `json:"query" binding:"required"`
	TopK  int    `json:"top_k" binding:"omitempty,gte=1,lte=50"`
}
