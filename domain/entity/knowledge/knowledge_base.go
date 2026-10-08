package knowledge

// 知识库状态
const (
	StatusDisabled int8 = 0 // 停用
	StatusEnabled  int8 = 1 // 启用
)

// KnowledgeBase 知识库实体
// 一个知识库归属于一个用户，后续的文档、切片、向量都以知识库为聚合根
type KnowledgeBase struct {
	ID             string `gorm:"primaryKey;size:32"`     // 雪花 ID
	UserID         string `gorm:"index;size:32;not null"` // 归属用户
	Name           string `gorm:"size:128;not null"`      // 知识库名称
	Description    string `gorm:"size:512;default:''"`    // 描述
	EmbeddingModel string `gorm:"size:64;default:''"`     // 预留：embedding 模型标识
	DocCount       int    `gorm:"default:0"`              // 预留：文档数统计
	Status         int8   `gorm:"default:1"`              // 1=启用 0=停用
	CreatedAt      int64  `gorm:"column:created_at;autoCreateTime:milli"`
	UpdatedAt      int64  `gorm:"column:updated_at;autoUpdateTime:milli"`
}

// TableName 返回表名
func (KnowledgeBase) TableName() string {
	return "knowledge_bases"
}
