package knowledge

import "github.com/PycMono/FastRAG/common/constants"

// KnowledgeBase 知识库聚合根。
//
// 本服务**只读**它：KB 由外部预置，这里不做创建/发布/版本（§1.2）。
type KnowledgeBase struct {
	ID            uint64 // 内部主键，join 用
	No            string // 对外编号，接口用
	Account       string // 租户
	Name          string
	Description   string
	KnowledgeType string
	SearchMode    string
	BizTag        string
	SplitOptions  []byte // 切片参数快照（JSON 原文）
	DocCount      int64
	ChunkCount    int64
	IsSystem      bool
	CreateTs      int64
	UpdateTs      int64
	DeleteTs      int64
}

// Deleted 是否已软删。
func (kb *KnowledgeBase) Deleted() bool { return kb.DeleteTs > 0 }

// UsesTitleOnly 检索模式是否只查标题。
func (kb *KnowledgeBase) UsesTitleOnly() bool {
	return kb.SearchMode == constants.SearchModeTitle
}
