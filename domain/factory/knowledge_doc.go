package factory

import (
	"time"

	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
)

// NewDoc 建一个文档聚合。
//
// 只在**所有内容都定下来之后**调一次——ES 切片写完、contentHash 算完
// （§5.2 ③）。这样实体从诞生起就是终态，不存在"先建行、后填内容"的中间态，
// 也就不存在"ES 写失败但库里已经记了新计数"的谎报窗口。
//
// 禁止在别处直接 &KnowledgeDoc{}——CreateTs / DeleteTs 漏填就是脏数据。
func NewDoc(
	docID uint64,
	kb *knowledgeentity.KnowledgeBase,
	name, contentHash string,
	chunkCount int,
	now time.Time,
) *knowledgeentity.KnowledgeDoc {
	ts := now.UnixMilli()
	return &knowledgeentity.KnowledgeDoc{
		ID:          docID,
		KBID:        kb.ID,
		Account:     kb.Account,
		Name:        name,
		ContentHash: contentHash,
		ChunkCount:  chunkCount,
		CreateTs:    ts,
		UpdateTs:    ts,
		DeleteTs:    0,
	}
}
