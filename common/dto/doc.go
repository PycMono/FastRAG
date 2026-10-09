package dto

// ChunkDTO 调用方直接给出的切片（format = chunks 时使用）
type ChunkDTO struct {
	Title   string `json:"title" binding:"max=512"`
	Content string `json:"content" binding:"required"`
}

// SplitOptionsDTO 切片参数，可选，覆盖 KB 上的默认值
type SplitOptionsDTO struct {
	ChunkSize  int `json:"chunk_size" binding:"omitempty,gte=100,lte=4000"`
	SplitLevel int `json:"split_level" binding:"omitempty,gte=1,lte=6"`
	MinChunk   int `json:"min_chunk" binding:"omitempty,gte=0,lte=500"`
}

// DocIngestDTO 文档导入请求
//
// Account 是普通参数（§6），不做鉴权解析。
type DocIngestDTO struct {
	Account      string           `json:"account" binding:"required,max=64"`
	KBNo         string           `json:"kb_no" binding:"required,max=64"`
	DocName      string           `json:"doc_name" binding:"required,max=512"`
	Format       string           `json:"format" binding:"required,oneof=markdown text chunks"`
	Content      string           `json:"content"`
	Chunks       []ChunkDTO       `json:"chunks"`
	SplitOptions *SplitOptionsDTO `json:"split_options"`

	// Refresh 为 true 时，写完 ES 立刻刷新索引，返回即可检索（§4.2）。
	// 默认 false：常规导入按 30s 的刷新周期，不付这次刷新的钱。
	Refresh bool `json:"refresh"`

	// Model 这批数据用哪家算向量，取 config.embedding.models 里的 key。
	// 留空用配置的 default。名字不存在时直接返回参数错误，不回落。
	//
	// 换模型**不会**让旧数据变得不可用，但也**不会**保证新旧向量在同一个
	// 空间里——维度不同会被 ES 拦下，维度相同但向量空间不同则是静默的
	// 噪声排序。这是明确接受的代价（设计文档 §2、§8）。
	Model string `json:"model" binding:"omitempty,max=64"`

	// 这里**没有** biz_tag：切片上的 biz_tag 一律取自 KB（§7.2）。
	// 让文档覆盖它会让这个文档按 KB 的 tag 搜不到、按自己的 tag 库又被筛掉。
}

// DocDeleteDTO 文档删除请求
type DocDeleteDTO struct {
	Account string `json:"account" binding:"required,max=64"`
	KBNo    string `json:"kb_no" binding:"required,max=64"`
	DocName string `json:"doc_name" binding:"required,max=512"`
}

// KBDeleteDTO 知识库删除请求：软删 KB 及其全部文档，并清理 ES
type KBDeleteDTO struct {
	Account string `json:"account" binding:"required,max=64"`
	KBNo    string `json:"kb_no" binding:"required,max=64"`
}
