// 本文件是单篇详情：GET /api/v1/docs/detail。

package docview

import (
	"context"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/common/vo"
)

// Detail 取单篇详情（含整篇原文与库级来源 URL）。
func (s *Service) Detail(
	ctx context.Context, q *dto.DocDetailQuery,
) (*vo.DocDetailVO, error) {
	doc, err := s.liveDoc(ctx, q.DocID, q.Tenant)
	if err != nil {
		return nil, err
	}

	// source_url 是**库级**的，只能回 KB 行取（文档表里根本没有这一列）。
	// KB 行查不到、或 tenant 不符 → 10101：一条查得到原文、却查不到所属库的
	// 文档本身就是不一致，不该假装正常返回
	kb, err := s.kbRepo.FindByID(ctx, doc.KBID, q.Tenant)
	if err != nil {
		return nil, err
	}

	return &vo.DocDetailVO{
		DocID:       doc.ID,
		DocName:     doc.Name,
		Title:       doc.Title,
		Content:     doc.Content,
		HasContent:  doc.Content != "",
		SourceURL:   kb.SourceURL,
		ContentHash: doc.ContentHash,
		ChunkCount:  doc.ChunkCount,
		KBID:        doc.KBID,
		CreateTs:    doc.CreateTs,
		UpdateTs:    doc.UpdateTs,
	}, nil
}
