package interfaces

import "context"

// IEmbedding 向量化能力。
//
// 它留在 interfaces/ 而不是 repository/：它不存任何东西，
// 是无状态的外部 API 调用（§3.1 的判据）。
type IEmbedding interface {
	// EmbedDocs 批量编码文档。返回顺序与入参一一对应。
	EmbedDocs(ctx context.Context, texts []string) ([][]float32, error)

	// EmbedQuery 编码查询串。
	//
	// 必须与 EmbedDocs 分开：多数模型（bge-m3 / E5 / GTE）检索时
	// 要给 query 加指令前缀（如 "query: "），doc 侧不加，混用会显著掉分。
	EmbedQuery(ctx context.Context, text string) ([]float32, error)

	Dim() int
	Model() string
}
