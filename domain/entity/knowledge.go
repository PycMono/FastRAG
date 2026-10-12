package entity

// Knowledge 知识库聚合根。
//
// 本服务**只读**它：KB 由外部预置，这里不做创建/发布/版本（§1.2）。
//
// **本结构体直接就是 GORM 的持久化模型**：gorm tag 写在实体上，没有独立的
// po / mapper 两层（对齐 micro-framework 的写法）。这张表的 DDL 归**建库方**，
// 所以这里的 tag 只是**读**它时的映射——改 tag 不会去动那张表；表改名或列改名
// 时两边必须**同时**上线，否则本服务静默查不到数据。
//
// 没有版本指针列（published_ver / draft_ver）：本期不做发布与版本，
// 导入即生效，检索直接读当前内容。
//
// 没有 index_kind / index_name：索引策略是「单索引 + term(account) 过滤」
// （设计文档 D2），所有租户共用一个物理索引，不需要 per-KB 的索引路由列。
type Knowledge struct {
	ID          uint64 `gorm:"column:id;primaryKey;autoIncrement"`                         // 内部主键，join 用
	No          string `gorm:"column:no;type:varchar(64);not null"`                        // 对外编号，接口用
	Account     string `gorm:"column:account;type:varchar(64);not null;index:idx_account"` // 租户
	Name        string `gorm:"column:name;type:varchar(255);not null"`                     // 库的显示名
	Description string `gorm:"column:description;type:varchar(1024);not null;default:''"`  // 简介，供展示
	// SourceURL 库级来源 URL：**一个来源 URL 建一个库**，所以它挂在库上而不是文档上。
	// 归**建库方**维护（库就是照这个 URL 建起来的，他们建库时手里就有它），
	// 本服务**只读**它做展示——它不在我们回写的三列（doc_count/chunk_count/update_ts）里。
	SourceURL  string `gorm:"column:source_url;type:varchar(2048);not null;default:''"`
	DocCount   int64  `gorm:"column:doc_count;type:bigint;not null;default:0"`   // 冗余计数：本服务回写的文档数
	ChunkCount int64  `gorm:"column:chunk_count;type:bigint;not null;default:0"` // 冗余计数：本服务回写的切片数
	CreateTs   int64  `gorm:"column:create_ts;type:bigint;not null"`             // 建库方维护
	UpdateTs   int64  `gorm:"column:update_ts;type:bigint;not null"`             // 本服务每次导入/删除都会回写
}

func (Knowledge) TableName() string { return "knowledge" }
