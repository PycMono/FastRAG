// 本文件是导入服务的主体：Service 及其非导入类操作（删除）。
// 单文档导入见 ingest.go，批量导入见 batch.go。

package ingest

import (
	"context"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	domainservice "github.com/PycMono/FastRAG/domain/service"
	logsdk "github.com/PycMono/go-logger-sdk"
	"github.com/PycMono/go-mysql-sdk/transaction"
)

// Service 文档导入应用服务。
type Service struct {
	kbRepo     repository.IKnowledgeRepo
	docRepo    repository.IKnowledgeDocRepo
	store      repository.IVectorStore
	embeddings interfaces.IEmbeddingRegistry
	parsers    interfaces.IParserRegistry
	splitter   *domainservice.Splitter
	idGen      repository.IIDService
	tm         transaction.Manager
}

func NewService(
	kbRepo repository.IKnowledgeRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embeddings interfaces.IEmbeddingRegistry,
	parsers interfaces.IParserRegistry, // ← 新增
	splitter *domainservice.Splitter,
	idGen repository.IIDService,
	tm transaction.Manager,
) *Service {
	return &Service{
		kbRepo:     kbRepo,
		docRepo:    docRepo,
		store:      store,
		embeddings: embeddings,
		parsers:    parsers, // ← 新增
		splitter:   splitter,
		idGen:      idGen,
		tm:         tm,
	}
}

// DeleteDoc 硬删文档并清理 ES（MySQL 必须在前）。
func (s *Service) DeleteDoc(ctx context.Context, in *dto.DocDeleteDTO) (*vo.DocDeleteVO, error) {
	kb, err := s.kbRepo.FindByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return nil, err
	}

	var doc *entity.KnowledgeDoc
	if err := s.tm.Transaction(ctx, func(txCtx context.Context) error {
		d, err := s.docRepo.Delete(txCtx, kb.ID, in.DocName)
		if err != nil {
			return err
		}
		doc = d
		if err := s.kbRepo.ApplyChunkDelta(txCtx, kb.ID, -int64(d.ChunkCount)); err != nil {
			return err
		}
		return s.kbRepo.ApplyDocDelta(txCtx, kb.ID, -1)
	}); err != nil {
		return nil, err
	}

	// ES 清理放到事务之后：MySQL 是权威且即时的，ES 可以迟、可以重试。
	// 这一步失败只会留下幽灵切片——文档行已经硬删，存活校验查不到它，切片自然被丢弃
	deleted, err := s.store.DeleteByQuery(ctx, repository.VectorFilter{
		Account: kb.Account, DocID: doc.ID,
	})
	if err != nil {
		logsdk.Error(ctx, "清理 ES 切片失败，将留下幽灵切片",
			logsdk.Any("doc_id", doc.ID), logsdk.Err(err))
		// 不向上返回：MySQL 侧的删除已经生效，对调用方来说这次删除是成功的
	}

	return &vo.DocDeleteVO{DocName: doc.Name, DeletedChunks: deleted}, nil
}

// DeleteKB 清理某个知识库在本服务里的全部派生数据。
//
// KB 行本身由外部系统维护，这里只负责：本服务的文档硬删 + ES 切片清理。
// 一旦引入删除动作就必须走这里——只删 MySQL 的话，ES 里会堆满查不到来源的幽灵切片。
func (s *Service) DeleteKB(ctx context.Context, in *dto.KBDeleteDTO) error {
	kb, err := s.kbRepo.FindByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return err
	}

	if err := s.tm.Transaction(ctx, func(txCtx context.Context) error {
		_, err := s.docRepo.DeleteByKBID(txCtx, kb.ID, kb.Account)
		return err
	}); err != nil {
		return err
	}

	if _, err := s.store.DeleteByQuery(ctx, repository.VectorFilter{
		Account: kb.Account, KBID: kb.ID,
	}); err != nil {
		logsdk.Error(ctx, "清理 ES 切片失败", logsdk.Any("kb_id", kb.ID), logsdk.Err(err))
	}
	return nil
}
