// 本文件是文档只读服务的主体：Service 与「列某库文档」这一条。
// 单篇详情见 detail.go，切片枚举见 chunks.go。

package docview

import (
	"context"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/repository"
)

// Service 文档只读应用服务：列文档、看整篇、枚举切片。
//
// 为什么单开一个包而不塞进 ingest：那边是**写**侧服务（先 ES 后 MySQL 的三次写 +
// 事务 + 差集清理），这边要的一组东西它一个都不用。「一个类型该住哪，看它在哪个
// 包的函数签名里被需要」——所以两条链路各管各的。
type Service struct {
	kbRepo  repository.IKnowledgeRepo
	docRepo repository.IKnowledgeDocRepo
	store   repository.IVectorStore
}

func NewService(
	kbRepo repository.IKnowledgeRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
) *Service {
	return &Service{kbRepo: kbRepo, docRepo: docRepo, store: store}
}

// List 列某库下的文档（分页）。
func (s *Service) List(
	ctx context.Context, q *dto.DocListQuery,
) (*vo.PageResult[vo.DocListItemVO], error) {
	// FindByNo 已经把「库不存在」与「account 对不上」折叠成同一个 10101，
	// 所以这里不再（也不该）自己判一次 account——多判一次就是多一处能漏的口子
	kb, err := s.kbRepo.FindByNo(ctx, q.KBNo, q.Account)
	if err != nil {
		return nil, err
	}

	// 先 Offset 再 Limit：Offset 会把 Page 规整到 ≥1，而 NewPageResult 要的是规整后的值
	offset := q.Offset()
	docs, total, err := s.docRepo.ListByKB(ctx, kb.ID, offset, q.Limit())
	if err != nil {
		return nil, err
	}

	// 显式初始化空切片：nil 会被序列化成 JSON null，前端 .map 直接炸
	list := make([]vo.DocListItemVO, 0, len(docs))
	for _, d := range docs {
		list = append(list, vo.DocListItemVO{
			DocID:      d.ID,
			DocName:    d.Name,
			Title:      d.Title,
			ChunkCount: d.ChunkCount,
			HasContent: d.HasContent,
			CreateTs:   d.CreateTs,
			UpdateTs:   d.UpdateTs,
		})
	}
	return vo.NewPageResult(total, list, q.Page, q.Limit()), nil
}

// liveDoc 按 doc_id 取一篇「属于该 account」的文档。
//
// 租户校验补在这里，是因为 FindByID **刻意不带 account 过滤**（底层仓储不该
// 替调用方做租户判断）。行不存在、或 account 对不上，都返回同一个
// ErrDocNotFound——分开就是告诉探测者「这个 id 是存在的」。
func (s *Service) liveDoc(
	ctx context.Context, docID uint64, account string,
) (*entity.KnowledgeDoc, error) {
	doc, err := s.docRepo.FindByID(ctx, docID)
	if err != nil {
		return nil, err
	}
	if doc == nil || doc.Account != account {
		// 不存在，或存在但不属于你：同一个错
		return nil, apperrors.ErrDocNotFound
	}
	return doc, nil
}
