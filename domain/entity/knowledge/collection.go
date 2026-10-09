package knowledge

// KnowledgeBases 知识库集合。
type KnowledgeBases []*KnowledgeBase

// IndexByID 建 ID 索引，供检索结果回填 kb_name / kb_no。
func (ks KnowledgeBases) IndexByID() map[uint64]*KnowledgeBase {
	out := make(map[uint64]*KnowledgeBase, len(ks))
	for _, kb := range ks {
		out[kb.ID] = kb
	}
	return out
}

// FilterByBizTags 按业务标签过滤。
//
// 语义刻意定得很窄：请求带了 biz_tags，就只留标签精确命中的库。
// 不做「无标签的库视为通用、一律保留」——那种隐含规则会让调用方
// 搞不清自己为什么召回多了。
func (ks KnowledgeBases) FilterByBizTags(tags []string) KnowledgeBases {
	if len(tags) == 0 {
		return ks
	}
	want := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		want[t] = struct{}{}
	}

	out := make(KnowledgeBases, 0, len(ks))
	for _, kb := range ks {
		if _, ok := want[kb.BizTag]; ok {
			out = append(out, kb)
		}
	}
	return out
}

// KnowledgeDocs 文档集合。
type KnowledgeDocs []*KnowledgeDoc

// AliveMap 返回「未删除」文档的 ID → 文档。
//
// 存活校验（§7.3）就靠这一张表：查不到 = 孤儿切片，已软删 = 幽灵切片，
// 两种都丢掉。
func (ds KnowledgeDocs) AliveMap() map[uint64]*KnowledgeDoc {
	out := make(map[uint64]*KnowledgeDoc, len(ds))
	for _, d := range ds {
		if d.Deleted() {
			continue
		}
		out[d.ID] = d
	}
	return out
}
