package knowledge

// KnowledgeDoc 文档聚合根。
//
// 切片不入 MySQL（D3），所以这个聚合只承载「文档级」状态：
// 身份、内容指纹、切片计数。
type KnowledgeDoc struct {
	ID          uint64 // 雪花 ID，写入前预分配
	KBID        uint64
	Account     string
	Name        string // 文档身份 = (KBID, Name)，同名重导入即更新
	ContentHash string // SHA256
	ChunkCount  int
	CreateTs    int64
	UpdateTs    int64
	DeleteTs    int64
}

// Deleted 是否已软删。存活校验靠它丢弃幽灵切片（§7.3）。
func (d *KnowledgeDoc) Deleted() bool { return d.DeleteTs > 0 }

// IsSameContent 内容是否未变（可跳过重灌）。
func (d *KnowledgeDoc) IsSameContent(hash string) bool {
	return d.ContentHash != "" && d.ContentHash == hash
}
