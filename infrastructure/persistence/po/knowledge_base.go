package po

// KnowledgeBase 知识库
//
// **只读实体**：知识库由骨架既有的管理模块 / 上游预置，本服务只读它
// （拿 Id / No / Account / Name / SearchMode / BizTag），
// 不提供建库、发布、版本管理的接口（设计文档 §1.2）。
//
// 没有版本指针列（published_ver / draft_ver）：本期不做发布与版本，
// 导入即生效，检索直接读当前内容。
//
// 没有 index_kind / index_name：索引策略是「单索引 + term(account) 过滤」
// （设计文档 D2），所有租户共用一个物理索引，不需要 per-KB 的索引路由列。
type KnowledgeBase struct {
	Id      uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	No      string `gorm:"column:no;type:varchar(64);not null"`
	Account string `gorm:"column:account;type:varchar(64);not null;index:idx_account"`
	Name    string `gorm:"column:name;type:varchar(255);not null"`

	Description   string `gorm:"column:description;type:varchar(1024);not null;default:''"`
	KnowledgeType string `gorm:"column:knowledge_type;type:varchar(32);not null;default:'ordinary'"`
	SearchMode    string `gorm:"column:search_mode;type:varchar(32);not null;default:'title_and_content'"`
	BizTag        string `gorm:"column:biz_tag;type:varchar(64);not null;default:''"`

	// 切片参数快照。用 []byte 而不是 json.RawMessage，避免 GORM 的 JSON 序列化
	// 把已是 JSON 的内容再编码一层。
	SplitOptions []byte `gorm:"column:split_options;type:json"`

	DocCount   int64 `gorm:"column:doc_count;type:bigint;not null;default:0"`
	ChunkCount int64 `gorm:"column:chunk_count;type:bigint;not null;default:0"`

	IsSystem int8  `gorm:"column:is_system;type:tinyint;not null;default:0"`
	CreateTs int64 `gorm:"column:create_ts;type:bigint;not null"`
	UpdateTs int64 `gorm:"column:update_ts;type:bigint;not null"`
	DeleteTs int64 `gorm:"column:delete_ts;type:bigint;not null;default:0"`
}

func (KnowledgeBase) TableName() string { return "knowledge_base" }
