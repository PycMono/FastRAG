// 本文件是单文档导入：Ingest、与 Parse 共用的落库段 ingestDoc，及其内部步骤。
// 服务主体见 service.go，批量导入见 batch.go，基于 URL 的解析导入见 parse.go。

package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/PycMono/FastRAG/common/constants"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/factory"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	domainservice "github.com/PycMono/FastRAG/domain/service"
	logsdk "github.com/PycMono/go-logger-sdk"
)

// Ingest 导入一份文档。同名重导入视作「更新」。
//
// 编排：定位 KB → 定切片参数 → 把「切」交给 ingestDoc（它取向量实现、落库）。
// 三条路径（format=markdown/text/chunks）的差异只在切，落库完全同构。
func (s *Service) Ingest(ctx context.Context, in *dto.DocIngestDTO) (*vo.DocIngestVO, error) {
	// ① 定位 KB。kb_no 指向的库 account 不等于传入值，在这里就被挡掉
	kb, err := s.kbRepo.FindByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return nil, err
	}

	// ② 切片参数：请求给了就用，没给的由 Normalize 补默认并校验。
	//    放在这里而不是闭包里，是为了「坏参数先于贵操作报错」这条顺序不依赖闭包的实现。
	opts, err := in.ChunkOptions()
	if err != nil {
		return nil, err
	}

	return s.ingestDoc(ctx, kb, in.DocName, func() (entity.Chunks, error) {
		return s.split(in, opts)
	}, in.Model, in.Refresh)
}

// ingestSource 产出这次要落库的 chunks。两条导入路径的差异全在这里：
// Ingest 从请求体切（markdown/text 走切片器、chunks 走 Normalize），
// Parse 从解析出的 markdown 切。
type ingestSource func() (entity.Chunks, error)

// ingestDoc 把一份文档落库：产 chunks → 取这次的向量实现 → 向量化 → 写 ES → 清旧 → 事务提交计数。
//
// Ingest 与 Parse 共用这一段：两者的差异只在「chunks 从哪来」（就是 src），
// 之后的向量化与三次写入完全同构，没有理由各写一遍。序号沿用原 Ingest 的 ②、④～⑨，
// 便于与设计文档对照。
//
// 编排分三步写入：
//
//	⑥ ES 写新切片 → ⑧ ES 删旧切片（_id 差集）→ ⑨ MySQL 写文档行 + 计数
//
// 「更新」这条语义的落点是 doc_id 的复用（第 ⑤ 步）：重导入必须先 FindByName 取回
// 库里那一行的 id。切片、删差集、存活校验三处都以 doc_id 为轴，id 一换就全盘失效，
// 且失败是静默的——接口返回成功，用户搜到的还是旧内容。
func (s *Service) ingestDoc(
	ctx context.Context,
	kb *entity.KnowledgeBase,
	docName string,
	src ingestSource,
	model string,
	refresh bool,
) (*vo.DocIngestVO, error) {
	// 先拿到要落的 chunks——两条路径的切片行为差异全部封装在 src 里。
	chunks, err := src()
	if err != nil {
		return nil, err
	}
	if len(chunks) > constants.MaxIngestChunks {
		return nil, apperrors.NewParamError(fmt.Sprintf(
			"切片数 %d 超过上限 %d，请检查文档内容或切片参数", len(chunks), constants.MaxIngestChunks))
	}

	// ② 定这次用哪家算向量。放在任何写操作之前——名字写错在这里就返回，
	//    超限的文档也不会白算一遍向量。
	//    解出来的实现逐请求传给下游，不能存回 Service 的字段（它是单例，会数据竞争）。
	embedder, err := s.embeddings.Get(model)
	if err != nil {
		return nil, err
	}

	// ④ 向量化。放在任何写操作之前——失败时什么都没动，重试是干净的。
	//    索引的创建不在这里，也不由本服务负责。
	titleVecs, contentVecs, err := s.embedChunks(ctx, embedder, chunks)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	hash := chunks.ContentHash()

	// biz_tag 只认 KB 上的那一个值，文档级别不允许覆盖（否则这篇谁都搜不到）。
	bizTag := kb.BizTag

	// ⑤ 定 doc_id。同名重导入必须沿用库里那一行的 id。
	existing, err := s.docRepo.FindByName(ctx, kb.ID, docName)
	if err != nil {
		return nil, err
	}
	var docID uint64
	if existing != nil {
		docID = existing.ID // 重导入：沿用原主键
	} else {
		docID = uint64(s.idGen.NextIntID()) // 首次导入：雪花预分配
	}

	vectors := factory.BuildVectorDocs(kb, docID, bizTag, chunks, titleVecs, contentVecs, now)

	// ⑥ 【第一写】ES 写新切片。新内容先进去，旧切片原地不动。
	if err := s.store.Save(ctx, vectors); err != nil {
		logsdk.Error(ctx, "切片写入失败，文档内容未变",
			logsdk.Any("account", kb.Account),
			logsdk.Any("kb_no", kb.No),
			logsdk.Any("doc_id", docID),
			logsdk.Err(err),
		)
		return nil, err
	}

	// ⑦ 【刷新】把「刚写进去的」和「可能还没刷出来的旧切片」推进可检索视图。
	//    重导入（existing != nil）必刷，首次导入只在调用方显式要求时刷。
	//    刷新失败不致命，记警告，不把这次导入判失败。
	if existing != nil || refresh {
		if err := s.store.Refresh(ctx); err != nil {
			logsdk.Warn(ctx, "刷新索引失败，本次可能清不掉旧切片",
				logsdk.Any("doc_id", docID), logsdk.Err(err))
		}
	}

	// ⑧ 【第二写】ES 清掉这篇文档里 _id 不在新集合中的旧切片。只删差集。
	if _, err := s.store.DeleteExcept(ctx, repository.VectorFilter{
		Account: kb.Account, DocID: docID,
	}, factory.ChunkIDs(vectors)); err != nil {
		logsdk.Warn(ctx, "清理旧切片失败，不影响检索正确性",
			logsdk.Any("doc_id", docID), logsdk.Err(err))
	}

	// ⑨ 【第三写】MySQL 写文档行 + 回写 KB 计数。实体到这里才构造，不存在中间态。
	doc := factory.NewDoc(docID, kb, docName, hash, len(chunks), now)

	var created bool
	if err := s.tm.Transaction(ctx, func(txCtx context.Context) error {
		oldChunkCount, isNew, err := s.docRepo.Save(txCtx, doc)
		if err != nil {
			return err
		}
		created = isNew
		if err := s.kbRepo.ApplyChunkDelta(txCtx, kb.ID, int64(len(chunks)-oldChunkCount)); err != nil {
			return err
		}
		if isNew {
			return s.kbRepo.ApplyDocDelta(txCtx, kb.ID, 1)
		}
		return nil
	}); err != nil {
		logsdk.Error(ctx, "写入文档行失败，该文档当前不可检索，需重新导入",
			logsdk.Any("account", kb.Account),
			logsdk.Any("kb_no", kb.No),
			logsdk.Any("doc_id", docID),
			logsdk.Err(err),
		)
		return nil, err
	}

	return &vo.DocIngestVO{
		DocID:      docID,
		DocName:    docName,
		ChunkCount: len(chunks),
		Created:    created,
	}, nil
}

// ─── 单文档导入的内部步骤 ──────────────────────────────────────────────────────

func (s *Service) split(
	in *dto.DocIngestDTO, opts domainservice.ChunkOptions,
) (entity.Chunks, error) {
	if in.Format == constants.FormatChunks {
		inputs := make([]domainservice.ChunkInput, 0, len(in.Chunks))
		for _, c := range in.Chunks {
			inputs = append(inputs, domainservice.ChunkInput{Title: c.Title, Content: c.Content})
		}
		return s.splitter.Normalize(inputs)
	}

	return s.splitter.Split(in.Content, in.Format, opts)
}

// embedChunks 用指定实现算两路向量。
//
// 实现是参数而不是 Service 的字段：Service 是单例，逐请求的东西不能往里写。
//
// title_vec 与 content_vec 分开存：标题短、噪声低，适合精排；
// 正文长、信息全，适合召回。检索时按 search_mode 决定用哪一路。
func (s *Service) embedChunks(
	ctx context.Context, e interfaces.IEmbedding, chunks entity.Chunks,
) (titleVecs, contentVecs [][]float32, err error) {
	titleVecs, err = e.EmbedDocs(ctx, chunks.Titles()) // title 向量化
	if err != nil {
		return nil, nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	contentVecs, err = e.EmbedDocs(ctx, factory.EmbeddingTexts(chunks)) // 内容向量化
	if err != nil {
		return nil, nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	// 条数对不上说明实现有问题。不能放过去——错位的向量会静默地
	// 把 A 切片的向量挂到 B 切片上，检索出的结果全错但看起来一切正常
	if len(titleVecs) != len(chunks) || len(contentVecs) != len(chunks) {
		return nil, nil, apperrors.NewSysError(apperrors.CodeEmbeddingFail, fmt.Sprintf(
			"向量条数不匹配：标题 %d / 正文 %d，切片 %d",
			len(titleVecs), len(contentVecs), len(chunks)))
	}

	return titleVecs, contentVecs, nil
}
