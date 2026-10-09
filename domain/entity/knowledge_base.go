package entity

import "github.com/PycMono/FastRAG/common/constants"

// KnowledgeBase 知识库聚合根。
//
// 本服务**只读**它：KB 由外部预置，这里不做创建/发布/版本（§1.2）。
//
// **本结构体直接就是 GORM 的持久化模型**：gorm tag 写在实体上，没有独立的
// po / mapper 两层（对齐 micro-framework 的写法）。因此这里的 tag 与
// scripts/schema.sql 是同一份列定义的两个写法——改一边就要改另一边。
//
// 没有版本指针列（published_ver / draft_ver）：本期不做发布与版本，
// 导入即生效，检索直接读当前内容。
//
// 没有 index_kind / index_name：索引策略是「单索引 + term(account) 过滤」
// （设计文档 D2），所有租户共用一个物理索引，不需要 per-KB 的索引路由列。
type KnowledgeBase struct {
	ID      uint64 `gorm:"column:id;primaryKey;autoIncrement"`                         // 内部主键，join 用
	No      string `gorm:"column:no;type:varchar(64);not null"`                        // 对外编号，接口用
	Account string `gorm:"column:account;type:varchar(64);not null;index:idx_account"` // 租户
	Name    string `gorm:"column:name;type:varchar(255);not null"`

	Description   string `gorm:"column:description;type:varchar(1024);not null;default:''"`
	KnowledgeType string `gorm:"column:knowledge_type;type:varchar(32);not null;default:'ordinary'"`
	SearchMode    string `gorm:"column:search_mode;type:varchar(32);not null;default:'title_and_content'"`
	BizTag        string `gorm:"column:biz_tag;type:varchar(64);not null;default:''"`

	// 切片参数快照。用 []byte 而不是 json.RawMessage，避免 GORM 的 JSON 序列化
	// 把已是 JSON 的内容再编码一层。
	SplitOptions []byte `gorm:"column:split_options;type:json"`

	// 冗余计数（列表页用，见 D3）。
	// 列是有符号 BIGINT，差值本身就能为负；仓储侧仍用
	// GREATEST(CAST(... AS SIGNED) + ?, 0) 兜底，怎么加都掉不成负数（见 ApplyChunkDelta）。
	DocCount   int64 `gorm:"column:doc_count;type:bigint;not null;default:0"`
	ChunkCount int64 `gorm:"column:chunk_count;type:bigint;not null;default:0"`

	IsSystem bool  `gorm:"column:is_system;type:tinyint;not null;default:0"`
	CreateTs int64 `gorm:"column:create_ts;type:bigint;not null"`
	UpdateTs int64 `gorm:"column:update_ts;type:bigint;not null"`
	DeleteTs int64 `gorm:"column:delete_ts;type:bigint;not null;default:0"`
}

func (KnowledgeBase) TableName() string { return "knowledge_base" }

// Deleted 是否已软删。
func (kb *KnowledgeBase) Deleted() bool { return kb.DeleteTs > 0 }

// UsesTitleOnly 检索模式是否只查标题。
func (kb *KnowledgeBase) UsesTitleOnly() bool {
	return kb.SearchMode == constants.SearchModeTitle
}
