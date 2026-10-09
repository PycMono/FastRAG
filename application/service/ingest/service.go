package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
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
	"github.com/PycMono/go-mysql-sdk/transaction"
)

// batchConcurrency 批量导入的并发度。
//
// 压得很低是刻意的：真正的瓶颈在 embedding 服务，并发开大只会把下游打挂，
// 结果整体更慢（§5.5）。
const batchConcurrency = 4

// Service 文档导入应用服务。
type Service struct {
	kbRepo     repository.IKnowledgeBaseRepo
	docRepo    repository.IKnowledgeDocRepo
	store      repository.IVectorStore
	embeddings interfaces.IEmbeddingRegistry
	splitter   *domainservice.Splitter
	idGen      repository.IIDService
	tm         transaction.Manager
}

func NewService(
	kbRepo repository.IKnowledgeBaseRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embeddings interfaces.IEmbeddingRegistry,
	splitter *domainservice.Splitter,
	idGen repository.IIDService,
	tm transaction.Manager,
) *Service {
	return &Service{
		kbRepo:     kbRepo,
		docRepo:    docRepo,
		store:      store,
		embeddings: embeddings,
		splitter:   splitter,
		idGen:      idGen,
		tm:         tm,
	}
}

// Ingest 导入一份文档。同名重导入视作「更新」。
//
// 编排见 §5.1，三步写入次序见 §5.2：
//
//	① ES 写新切片 → ② ES 删旧切片（_id 差集）→ ③ MySQL 写文档行 + 计数
//
// 「更新」这条语义的落点是 **doc_id 的复用**（第 ⑤ 步）：重导入必须先
// LoadByName 取回库里那一行的 id，而不是每次都分配新雪花。切片、删差集、
// 存活校验三处都以 doc_id 为轴，id 一换就全盘失效，且失败是静默的——
// 接口返回成功，用户搜到的还是旧内容（§5.3）。
//
// 顺序是"先写后删、MySQL 最后"：ES 里先有内容、再清旧的，所以中途失败时
// 这篇文档总能查到点什么，不会因为一次失败的写入把内容清空。
// MySQL 放最后是因为文档行是存活校验的依据（§7.3）——它没落，切片就算孤儿，
// 宁可查不到也不要返回错内容。
//
// **并发同名导入不在本方法的保护范围内**（§5.5）：两次并发调用可能互相删掉
// 对方的切片，只剩交集。调用方必须按 doc_name 去重。
func (s *Service) Ingest(ctx context.Context, in *dto.DocIngestDTO) (*vo.DocIngestVO, error) {
	// ① 定位 KB。kb_no 指向的库 account 不等于传入值，在这里就被挡掉（§6）
	kb, err := s.kbRepo.LoadByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return nil, err
	}

	// ② 定这次用哪家算向量。
	//
	//    放在切片和任何写操作之前是刻意的：名字写错在这里就返回，
	//    不会白切一遍、白算一遍、白写一次 ES 才告诉调用方名字不存在。
	//
	//    解出来的实现是**逐请求**传给 embedChunks 的，不能存回 Service 的字段——
	//    Service 是单例，把逐请求的东西写进去就是数据竞争。BatchIngest 的
	//    4 个 goroutine 各自解各自的，互不影响。
	embedder, err := s.embeddings.Get(in.Model)
	if err != nil {
		return nil, err
	}

	// ③ 切片参数：KB 快照 → 请求覆盖 → 归一化
	opts, err := s.resolveOptions(kb, in.SplitOptions)
	if err != nil {
		return nil, err
	}

	chunks, err := s.split(in, opts)
	if err != nil {
		return nil, err
	}
	if len(chunks) > constants.MaxIngestChunks {
		return nil, apperrors.NewParamError(fmt.Sprintf(
			"切片数 %d 超过上限 %d，请检查切片参数或文档格式", len(chunks), constants.MaxIngestChunks))
	}

	// ④ 向量化。放在任何写操作之前——失败时什么都没动，重试是干净的。
	//    索引的创建**不在这里**，那是启动期做一次的事（§9.4）。
	titleVecs, contentVecs, err := s.embedChunks(ctx, embedder, chunks)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	hash := contentHash(chunks)

	// biz_tag **只认 KB 上的那一个值**，文档级别不允许覆盖。
	//
	// 允许覆盖会让文档变得「谁都搜不到」：检索侧先用 KB 的 tag 筛库
	// （§7.1 ①），再在 ES 里 terms(biz_tag) 过滤。一个把 tag 覆盖成 finance
	// 的文档，落在 kb.biz_tag = sales 的库里——按 sales 搜，库进来了但这条被
	// ES 过滤掉；按 finance 搜，库本身就被筛掉了，这条根本没机会。
	// 两头都够不着，而且不报错（§7.2）。
	bizTag := kb.BizTag

	// ⑤ 定 doc_id。**同名重导入必须沿用库里那一行的 id。**
	//
	//    切片、DeleteExcept、存活校验全都以 doc_id 为轴。换一个新 id 的后果是
	//    双向失效：新内容变成一批没有文档行的孤儿（存活校验全丢掉），
	//    而 DeleteExcept 按新 id 又什么也删不到，旧切片原样留在 ES 里继续被检索——
	//    表现出来就是「导入返回成功，用户却还搜到旧内容」。
	//
	//    只有库里确实没有这一行时才分配雪花 id。
	existing, err := s.docRepo.LoadByName(ctx, kb.ID, in.DocName)
	if err != nil {
		return nil, err
	}
	var docID uint64
	if existing != nil {
		docID = existing.ID // 重导入：沿用原主键
	} else {
		docID = uint64(s.idGen.NextIntID()) // 首次导入：雪花预分配，不需要 DB 往返
	}

	vectors := factory.BuildVectorDocs(kb, docID, bizTag, chunks, titleVecs, contentVecs, now)

	// ⑥ 【第一写】ES 写新切片（§5.2 ①）
	//
	//    新内容先进去，旧切片原地不动——所以这一步失败时这篇文档仍然可查
	//    （只是内容还是旧的），重试导入即可。
	if err := s.store.Save(ctx, vectors); err != nil {
		logsdk.Error(ctx, "切片写入失败，文档内容未变",
			logsdk.Any("account", kb.Account),
			logsdk.Any("kb_no", kb.No),
			logsdk.Any("doc_id", docID),
			logsdk.Err(err),
		)
		return nil, err
	}

	// ⑦ 【刷新】把「刚写进去的」和「可能还没刷出来的旧切片」推进可检索视图
	//
	//    delete_by_query 只能作用在**已刷新的段**上，而 ES 默认 30s 才刷新一次
	//    （§4.2）。不刷新会在两处踩空：
	//
	//      · 重导入间隔 < 一个刷新周期时，上一批旧切片还没进段 → ⑧ 根本看不见
	//        它们，于是删了个寂寞，旧切片一直留到再下一次重导入才被清掉。
	//        也就是说 §12.1 那条「重导入后旧切片被清掉」的验收会**时灵时不灵**；
	//      · 调用方要「导入即可搜」时（§4.2），新切片同样还没进段。
	//
	//    两种触发条件对应这两种用途：重导入（existing != nil）必刷，
	//    首次导入只在调用方显式要求时刷——常规首次导入没有旧切片要清，
	//    没必要付这次刷新的钱。
	//
	//    刷新失败不致命：新内容会在下一次定期刷新后可见，旧切片则要等下一次
	//    重导入才清掉。记警告，不把这次导入判失败。
	if existing != nil || in.Refresh {
		if err := s.store.Refresh(ctx); err != nil {
			logsdk.Warn(ctx, "刷新索引失败，本次可能清不掉旧切片",
				logsdk.Any("doc_id", docID), logsdk.Err(err))
		}
	}

	// ⑧ 【第二写】ES 清掉这篇文档里 _id 不在新集合中的旧切片（§5.2 ②）
	//
	//    只删差集，不 DeleteByQuery(doc_id) 全删：内容没变的切片 _id 不变，
	//    留着即可，全删再写回来是白放大一次写入。
	//
	//    失败不影响正确性：旧切片多留一会儿，下次重导入会再清一遍。
	//    代价是这篇文档可能短暂地返回新旧两份内容（§11 场景 3）。
	if _, err := s.store.DeleteExcept(ctx, repository.VectorFilter{
		Account: kb.Account, DocID: docID,
	}, factory.ChunkIDs(vectors)); err != nil {
		logsdk.Warn(ctx, "清理旧切片失败，不影响检索正确性",
			logsdk.Any("doc_id", docID),
			logsdk.Err(err),
		)
	}

	// ⑨ 【第三写】MySQL 写文档行 + 回写 KB 计数（§5.2 ③）
	//
	//    实体到这里才构造：所有字段都已定稿，不存在"先建行、后填内容"的中间态，
	//    也就不会出现"ES 写失败但库里已经记了新计数"的谎报（A2.6）。
	//
	//    重导入时这里传的 CreateTs 是本次的时间，但 Save 的 upsert 分支
	//    只写 content_hash / chunk_count / update_ts 三列（A4.4），
	//    create_ts 保持库里原值——文档的创建时间不会因为重导入而漂移。
	doc := factory.NewDoc(docID, kb, in.DocName, hash, len(chunks), now)

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
		// 刻意不吞这个错误。文档行没落 → 刚才写进 ES 的切片成了孤儿，
		// 被 §7.3 的存活校验挡在检索之外。这是「可过滤」的一侧：
		// 不会返回错内容，只是这篇暂时搜不到。等对账重算（§11 场景 2）
		logsdk.Error(ctx, "写入文档行失败，该文档当前不可检索，等对账重算",
			logsdk.Any("account", kb.Account),
			logsdk.Any("kb_no", kb.No),
			logsdk.Any("doc_id", docID),
			logsdk.Err(err),
		)
		return nil, err
	}

	return &vo.DocIngestVO{
		DocID:      docID,
		DocName:    in.DocName,
		ChunkCount: len(chunks),
		Created:    created,
	}, nil
}

// DeleteDoc 软删文档并清理 ES（§5.4：MySQL 必须在前）。
func (s *Service) DeleteDoc(ctx context.Context, in *dto.DocDeleteDTO) (*vo.DocDeleteVO, error) {
	kb, err := s.kbRepo.LoadByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()

	var doc *entity.KnowledgeDoc
	if err := s.tm.Transaction(ctx, func(txCtx context.Context) error {
		d, err := s.docRepo.SoftDelete(txCtx, kb.ID, in.DocName, now)
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
	// 这一步失败只会留下幽灵切片，被存活校验按 doc.Deleted() 过滤掉（§5.4）
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
// KB 行本身由外部系统维护（§1.2），这里只负责：本服务的文档软删 + ES 切片清理。
// 一旦引入删除动作就必须走这里——只删 MySQL 的话，ES 里会堆满查不到来源的幽灵切片。
func (s *Service) DeleteKB(ctx context.Context, in *dto.KBDeleteDTO) error {
	kb, err := s.kbRepo.LoadByNo(ctx, in.KBNo, in.Account)
	if err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	if err := s.tm.Transaction(ctx, func(txCtx context.Context) error {
		_, err := s.docRepo.SoftDeleteByKBID(txCtx, kb.ID, kb.Account, now)
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

// ─── 批量导入（P1，§5.5）─────────────────────────────────────────────────────

// BatchResult 批量导入结果。
type BatchResult struct {
	Total   int               `json:"total"`
	Success int               `json:"success"`
	Failed  int               `json:"failed"`
	Items   []*vo.DocIngestVO `json:"items"`
	Errors  []*BatchError     `json:"errors"`
}

// BatchError 单条失败明细。
type BatchError struct {
	DocName string `json:"doc_name"`
	Message string `json:"message"`
}

// docKey 文档的同一性，与 MySQL 的唯一键 (kb_id, name) 对齐。
//
// 用 kb_no 而不是 kb_id：这一层还没查库，拿不到自增主键。
// kb_no 到 kb_id 是一对一（§4.1），所以在这里做去重等价。
type docKey struct {
	Account string
	KBNo    string
	DocName string
}

// duplicateDocNames 找出批次内出现多次的文档。
//
// 为什么是拒绝而不是「后一条覆盖前一条」：同名两条该留哪一份是业务决策，
// 服务端替调用方定夺，只会让「我明明传了正确的那份」变成无从解释。
// 也不能靠并发写去碰运气——同一个 doc_id 上两路 DeleteExcept 会互相删掉
// 对方刚写的切片，最终只剩两边集合的交集，内容被截断（§5.5）。
//
// 必须**同步**跑完再起工作池：放进 goroutine 里就回到竞态了。
func duplicateDocNames(items []*dto.DocIngestDTO) map[docKey]struct{} {
	count := make(map[docKey]int, len(items))
	for _, in := range items {
		count[docKey{in.Account, in.KBNo, in.DocName}]++
	}
	dup := make(map[docKey]struct{})
	for k, n := range count {
		if n > 1 {
			dup[k] = struct{}{}
		}
	}
	return dup
}

// BatchIngest 批量导入：受限并发 + 单条错误隔离。
//
// 同步执行，受 HTTP 超时约束；超大文档由上游分批调用。
// 不用事务包整批：一条失败不该把已经灌好的几十条一起回滚。
func (s *Service) BatchIngest(ctx context.Context, items []*dto.DocIngestDTO) *BatchResult {
	res := &BatchResult{Total: len(items)}

	// 批次内同名的先挑出来标记失败并排除。整批不因它们返回 400——
	// 控制器已约定「恒返回 200 + 明细」（A4.13），合法的那几十条照常导。
	dup := duplicateDocNames(items)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, batchConcurrency)

	for _, item := range items {
		if _, isDup := dup[docKey{item.Account, item.KBNo, item.DocName}]; isDup {
			res.Failed++
			res.Errors = append(res.Errors, &BatchError{
				DocName: item.DocName,
				Message: "同一批次内 doc_name 重复，无法判定以哪一份为准，本批全部跳过",
			})
			continue
		}

		wg.Add(1)
		go func(in *dto.DocIngestDTO) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				res.Failed++
				res.Errors = append(res.Errors, &BatchError{
					DocName: in.DocName, Message: ctx.Err().Error(),
				})
				mu.Unlock()
				return
			}

			out, err := s.Ingest(ctx, in)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Failed++
				res.Errors = append(res.Errors, &BatchError{
					DocName: in.DocName, Message: err.Error(),
				})
				return
			}
			res.Success++
			res.Items = append(res.Items, out)
		}(item)
	}
	wg.Wait()

	// 并发完成顺序不稳定，返回前定序——否则同一批请求两次返回的顺序都不一样
	sort.Slice(res.Items, func(i, j int) bool { return res.Items[i].DocName < res.Items[j].DocName })
	sort.Slice(res.Errors, func(i, j int) bool { return res.Errors[i].DocName < res.Errors[j].DocName })

	return res
}

// ─── 内部 ────────────────────────────────────────────────────────────────────

func (s *Service) resolveOptions(
	kb *entity.KnowledgeBase, override *dto.SplitOptionsDTO,
) (domainservice.ChunkOptions, error) {
	opts := domainservice.ParseSplitOptions(kb.SplitOptions)
	if override != nil {
		opts = opts.Override(&domainservice.ChunkOptions{
			ChunkSize:  override.ChunkSize,
			SplitLevel: override.SplitLevel,
			MinChunk:   override.MinChunk,
		})
	}
	return opts.Normalize()
}

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
// 正文长、信息全，适合召回。检索时按 search_mode 决定用哪一路（§7.2）。
func (s *Service) embedChunks(
	ctx context.Context, e interfaces.IEmbedding, chunks entity.Chunks,
) (titleVecs, contentVecs [][]float32, err error) {
	titleVecs, err = e.EmbedDocs(ctx, chunks.Titles())
	if err != nil {
		return nil, nil, apperrors.ErrEmbeddingFailed.Wrap(err)
	}

	contentVecs, err = e.EmbedDocs(ctx, factory.EmbeddingTexts(chunks))
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

// contentHash 对**切片结果**取指纹，而不是对原文取。
//
// 判断依据应该是「最终索引内容有没有变」：原文改了排版、空白但切出来一样，
// 重灌一遍纯属浪费 embedding 调用。
func contentHash(chunks entity.Chunks) string {
	h := sha256.New()
	for _, c := range chunks {
		h.Write([]byte(c.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
