package entity

// KnowledgeDoc 文档聚合根。
//
// **本结构体直接就是 GORM 的持久化模型**（同 Knowledge）：gorm tag 写在实体上，
// 没有独立的 po / mapper 两层。这里的 tag 与 scripts/schema.sql 是同一份列定义的
// 两个写法——改一边就要改另一边。
//
// 切片不入 MySQL（D3），所以这个聚合只承载「文档级」状态：
// 身份、内容指纹、切片计数。
//
// 两条关键约定（设计文档 §4.1）：
//
//  1. ID 由应用侧预分配（雪花），**没有** autoIncrement。
//     写入先落 ES（§5.2），需要先知道 doc_id 才能组织切片；等自增 ID 会把顺序反过来。
//
//  2. 文档身份 = (KBID, Name)，不是 (KBID, ContentHash)。
//     同名重导入视作「更新」：ID 不变，只换 ContentHash。
//     若按 content_hash 建唯一键，同名改一个标点就会攒出一行僵尸文档。
//
// 没有 State 列：导入是同步的两写（ES → MySQL），没有「处理中/失败」的中间态。
// 中途失败要么什么都没写（ES 层），要么产生孤儿切片（MySQL 层，由查询侧存活校验兜住）。
type KnowledgeDoc struct {
	ID          uint64 `gorm:"column:id;primaryKey"`                               // 雪花 ID，写入前预分配（无 autoIncrement）
	KBID        uint64 `gorm:"column:kb_id;type:bigint unsigned;not null"`         // 所属库，对应 knowledge.id
	Tenant      string `gorm:"column:tenant;type:varchar(64);not null"`            // 租户，检索时 term(tenant) 过滤用
	Name        string `gorm:"column:name;type:varchar(512);not null"`             // 文档身份 = (KBID, Name)，同名重导入即更新
	Title       string `gorm:"column:title;type:varchar(255);not null;default:''"` // Title 只是展示字段，**不参与任何判定**——一旦它参与身份，「改标题」就会
	Content     string `gorm:"column:content;type:mediumtext;not null"`            // Content 整篇原文（markdown 或纯文本）。format=chunks 进来的存**空串**——
	HasContent  bool   `gorm:"column:has_content;->"`                              // HasContent **不是表上的列**：只由 ListByKB 的 `CHAR_LENGTH(content) > 0`
	ContentHash string `gorm:"column:content_hash;type:char(64);not null"`         // 内容指纹 SHA256，未变则跳过重灌
	ChunkCount  int    `gorm:"column:chunk_count;type:int;not null;default:0"`     // 切片数，本次灌进 ES 的切片条数
	CreateTs    int64  `gorm:"column:create_ts;type:bigint;not null"`              // 首次导入时间
	UpdateTs    int64  `gorm:"column:update_ts;type:bigint;not null"`              // 最近一次导入/更新时间
}

func (KnowledgeDoc) TableName() string { return "knowledge_doc" }

// IsSameContent 内容是否未变（可跳过重灌）。
func (d *KnowledgeDoc) IsSameContent(hash string) bool {
	return d.ContentHash != "" && d.ContentHash == hash
}
