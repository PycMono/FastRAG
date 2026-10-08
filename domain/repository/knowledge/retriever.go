package knowledge

import "context"

// RetrieveQuery 检索请求
type RetrieveQuery struct {
	KnowledgeBaseID string // 目标知识库
	Query           string // 检索语句
	TopK            int    // 返回条数
}

// RetrievedChunk 检索结果片段
type RetrievedChunk struct {
	DocID      string  // 所属文档 ID
	ChunkIndex int     // 文档内切片序号
	Content    string  // 切片正文
	Score      float64 // 相似度得分
}

// IKnowledgeRetriever 知识库检索能力
// 骨架阶段由 infrastructure/persistence/knowledge 的占位实现提供；
// 接入向量库时新增一个实现并在 persistence.Register 中替换即可，其余各层无需改动。
type IKnowledgeRetriever interface {
	Retrieve(ctx context.Context, q RetrieveQuery) ([]*RetrievedChunk, error)
}
