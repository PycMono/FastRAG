package mapper

import (
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/infrastructure/persistence/po"
)

// ToKnowledgeBase PO → 实体。
//
// KB 是只读的，所以只有这一个方向——没有 ToKnowledgeBasePO。
// 真要写 KB 时再加，那时也该顺手想清楚「谁能改 KB」。
func ToKnowledgeBase(row *po.KnowledgeBase) *knowledgeentity.KnowledgeBase {
	return &knowledgeentity.KnowledgeBase{
		ID:            row.Id,
		No:            row.No,
		Account:       row.Account,
		Name:          row.Name,
		Description:   row.Description,
		KnowledgeType: row.KnowledgeType,
		SearchMode:    row.SearchMode,
		BizTag:        row.BizTag,
		SplitOptions:  row.SplitOptions,
		DocCount:      row.DocCount,
		ChunkCount:    row.ChunkCount,
		IsSystem:      row.IsSystem == 1,
		CreateTs:      row.CreateTs,
		UpdateTs:      row.UpdateTs,
		DeleteTs:      row.DeleteTs,
	}
}

func ToKnowledgeBases(rows []po.KnowledgeBase) knowledgeentity.KnowledgeBases {
	out := make(knowledgeentity.KnowledgeBases, 0, len(rows))
	for i := range rows {
		out = append(out, ToKnowledgeBase(&rows[i]))
	}
	return out
}

func ToKnowledgeDoc(row *po.KnowledgeDoc) *knowledgeentity.KnowledgeDoc {
	return &knowledgeentity.KnowledgeDoc{
		ID:          row.Id,
		KBID:        row.KbId,
		Account:     row.Account,
		Name:        row.Name,
		ContentHash: row.ContentHash,
		ChunkCount:  int(row.ChunkCount), // PO 是 int32（对齐 DDL 的 INT），实体统一用 int
		CreateTs:    row.CreateTs,
		UpdateTs:    row.UpdateTs,
		DeleteTs:    row.DeleteTs,
	}
}

func ToKnowledgeDocs(rows []po.KnowledgeDoc) knowledgeentity.KnowledgeDocs {
	out := make(knowledgeentity.KnowledgeDocs, 0, len(rows))
	for i := range rows {
		out = append(out, ToKnowledgeDoc(&rows[i]))
	}
	return out
}

func ToKnowledgeDocPO(doc *knowledgeentity.KnowledgeDoc) *po.KnowledgeDoc {
	return &po.KnowledgeDoc{
		Id:          doc.ID,
		KbId:        doc.KBID,
		Account:     doc.Account,
		Name:        doc.Name,
		ContentHash: doc.ContentHash,
		ChunkCount:  int32(doc.ChunkCount),
		CreateTs:    doc.CreateTs,
		UpdateTs:    doc.UpdateTs,
		DeleteTs:    doc.DeleteTs,
	}
}
