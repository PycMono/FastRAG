package entity

// Knowledges 知识库集合。
type Knowledges []*Knowledge

// IndexByID 建 ID 索引，供检索结果回填 kb_name / kb_no。
func (ks Knowledges) IndexByID() map[uint64]*Knowledge {
	out := make(map[uint64]*Knowledge, len(ks))
	for _, kb := range ks {
		out[kb.ID] = kb
	}
	return out
}

// KnowledgeDocs 文档集合。
type KnowledgeDocs []*KnowledgeDoc

// AliveMap 返回召回到的文档行：ID → 文档。
//
// 存活校验（§7.3）就靠这一张表：**查不到就丢弃**——孤儿切片（文档行没落成）
// 与幽灵切片（文档行已删）都走这个出口。硬删之后「已删」等于「行不存在」，
// 所以这里不再需要任何判据，能进来的一律是活着的。
func (ds KnowledgeDocs) AliveMap() map[uint64]*KnowledgeDoc {
	out := make(map[uint64]*KnowledgeDoc, len(ds))
	for _, d := range ds {
		out[d.ID] = d
	}
	return out
}
