package search

import (
	"context"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	logsdk "github.com/PycMono/go-logger-sdk"
)

// Service 检索应用服务。
type Service struct {
	kbRepo     repository.IKnowledgeBaseRepo
	docRepo    repository.IKnowledgeDocRepo
	store      repository.IVectorStore
	embeddings interfaces.IEmbeddingRegistry
	reranks    interfaces.IRerankRegistry
	tuning     SearchTuning
}

func NewService(
	kbRepo repository.IKnowledgeBaseRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embeddings interfaces.IEmbeddingRegistry,
	reranks interfaces.IRerankRegistry,
	tuning SearchTuning,
) *Service {
	return &Service{
		kbRepo:     kbRepo,
		docRepo:    docRepo,
		store:      store,
		embeddings: embeddings,
		reranks:    reranks,
		tuning:     tuning.WithDefaults(),
	}
}

// Search 混合检索。
func (s *Service) Search(ctx context.Context, in *dto.SearchDTO) (*vo.SearchResultVO, error) {
	opts, err := s.resolveOptions(in)
	if err != nil {
		return nil, err
	}
	empty := &vo.SearchResultVO{Items: []*vo.SearchItemVO{}}

	// ⓪ 先把两个名字解出来。
	//
	//    放在最前面是刻意的：拼错的名字应当**立刻**报错，而不是先花一次 ES
	//    往返再报。代价是 dense_weight=0（压根不走向量路）时也会校验
	//    embed_model——这是想要的，传了一个用不上的错名字同样应该被指出。
	//
	//    rerank 那边 enabled=false 时注册表对任何名字都给空实现，不会在这里挡人。
	embedder, err := s.embeddings.Get(in.EmbedModel)
	if err != nil {
		return nil, err
	}
	reranker, err := s.reranks.Get(in.RerankModel)
	if err != nil {
		return nil, err
	}

	// ① 加载 KB。不属于该 account 的库在仓储层就被丢掉了（§6）
	kbs, err := s.kbRepo.LoadByNos(ctx, in.KBNos, in.Account)
	if err != nil {
		return nil, err
	}
	kbs = kbs.FilterByBizTags(opts.BizTags)
	if len(kbs) == 0 {
		return empty, nil
	}

	// ② 查询向量。dense_weight 为 0 时不发这一路，也就不必花这次调用
	var queryVec []float32
	if opts.useKNN() {
		queryVec, err = embedder.EmbedQuery(ctx, opts.Query)
		if err != nil {
			return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
		}
	}

	// ③ 分组检索：按 search_mode 把库分组，每组各发一次请求。
	//
	//    不取并集——那会悄悄把标题模式的库按正文搜，等于改写了它的语义（§7.2）。
	//    最多两组，组内字段一致所以能合成一次请求。
	//
	//    fetchSize 在 rerank 开启时大于 limit，让 ES 多召回一些候选交给 rerank 精排。
	fetchSize := opts.effectiveRetrieveCount()
	groups := groupBySearchMode(kbs)
	pool := make([]repository.SearchResult, 0, len(groups))
	for _, g := range groups {
		r, err := s.store.Search(ctx, s.buildReq(g, opts, queryVec, fetchSize))
		if err != nil {
			return nil, err
		}
		pool = append(pool, r)
	}

	// ④ 应用层加权 RRF 融合
	//
	//    逐组逐路喂进去，**不先把各组拼成一个大列表**——那样后一组的
	//    第一名会被排到前一组所有命中之后，等于按分组顺序给了个固定的偏置
	//    （A3.2 的 Fuse 注释）。每组两路各自从 rank=1 起算就不偏。
	routes := make([]Route, 0, 2*len(pool))
	for _, r := range pool {
		// 空的一路（dense_weight 把它短路掉了）不占位，权重为 0 的路 Fuse 会跳过
		if len(r.BM25) > 0 {
			routes = append(routes, Route{Hits: r.BM25, Weight: 1 - opts.DenseWeight})
		}
		if len(r.KNN) > 0 {
			routes = append(routes, Route{Hits: r.KNN, Weight: opts.DenseWeight})
		}
	}
	merged := Fuse(routes, s.tuning.RankConstant, fetchSize*fusionOversample)

	// ⑤ 存活校验 + 补展示字段（1 次批量 SQL，不取内容——D1）
	items, err := s.filterAlive(ctx, merged, kbs)
	if err != nil {
		return nil, err
	}

	// ⑥ 可选重排。失败时退回融合原序，不阻断检索。
	//
	//    判据里不再有 s.rerank != nil：注册表永远返回一个非 nil 的实现，
	//    enabled=false 时给的是空实现（原序返回），行为与以前一致。
	if opts.Rerank && len(items) > 1 {
		reranked, err := s.rerankItems(ctx, reranker, opts.Query, items)
		if err != nil {
			logsdk.Warn(ctx, "重排失败，退回融合原序", logsdk.Err(err))
		} else {
			items = reranked
		}
	}

	if len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return &vo.SearchResultVO{Items: items}, nil
}

func (s *Service) resolveOptions(in *dto.SearchDTO) (searchOptions, error) {
	weight := s.tuning.DenseWeight
	if in.DenseWeight != nil {
		weight = *in.DenseWeight
	}

	retrieveCount := 0
	if in.RetrieveCount != nil {
		retrieveCount = *in.RetrieveCount
	} else if s.tuning.DefaultRetrieveCount > 0 {
		retrieveCount = s.tuning.DefaultRetrieveCount
	}

	return searchOptions{
		Query:         in.Query,
		Limit:         in.Limit,
		RetrieveCount: retrieveCount,
		BizTags:       in.BizTags,
		DenseWeight:   weight,
		Rerank:        in.RerankSwitch,
	}.normalize()
}

// searchModeGroup 一组 search_mode 相同的库。
type searchModeGroup struct {
	titleOnly bool
	kbs       entity.KnowledgeBases
}

// groupBySearchMode 按「是否只查标题」把库分组。
//
// 为什么不取并集：只要有一个库开了正文检索，就全局按正文检索的话，
// 那些建库时明确声明「只用标题」的库就被按正文搜了——召回里会冒出
// 一堆标题对不上、正文里却有这个词的切片。那是静默改写语义（§7.2）。
func groupBySearchMode(kbs entity.KnowledgeBases) []searchModeGroup {
	var contentSearch, titleSearch entity.KnowledgeBases
	for _, kb := range kbs {
		if kb.UsesTitleOnly() {
			titleSearch = append(titleSearch, kb)
		} else {
			contentSearch = append(contentSearch, kb)
		}
	}

	groups := make([]searchModeGroup, 0, 2)
	if len(contentSearch) > 0 {
		groups = append(groups, searchModeGroup{titleOnly: false, kbs: contentSearch})
	}
	if len(titleSearch) > 0 {
		groups = append(groups, searchModeGroup{titleOnly: true, kbs: titleSearch})
	}
	return groups
}

func (s *Service) buildReq(
	g searchModeGroup,
	opts searchOptions,
	queryVec []float32,
	fetchSize int,
) repository.VectorSearchReq {
	ids := make([]uint64, 0, len(g.kbs))
	for _, kb := range g.kbs {
		ids = append(ids, kb.ID)
	}

	return repository.VectorSearchReq{
		// 组内必然同 account：LoadByNos 加载时已按 account 过滤过（§6）
		Account:       g.kbs[0].Account,
		KBIDs:         ids,
		BizTags:       opts.BizTags,
		Query:         opts.Query,
		QueryVec:      queryVec,
		TitleOnly:     g.titleOnly,
		DenseWeight:   opts.DenseWeight,
		BM25Top:       fetchSize,
		KNNTops:       fetchSize,
		NumCandidates: fetchSize * 5,
	}
}

// filterAlive 丢掉孤儿与幽灵切片（§7.3）。
//
// 两个要点：
//  1. doc_id 先去重再查库——召回 200 条通常只落在几十个文档上，
//     按召回条数去查数据库是纯浪费；
//  2. 内容不回表取（D1），这里只补 doc_name / kb_name 两个展示字段。
//
// 它同时兜住了「写一半失败」和「删一半失败」两种残留：
//   - 孤儿：切片写进 ES 了，但 MySQL 文档行没落（§5.2 ③ 失败）→ 查不到 doc，丢弃；
//   - 幽灵：MySQL 已软删，ES 还没清完（§5.4）→ 查不到 doc，丢弃。
//
// 它的前提是 doc_id 稳定：全靠切片带的 doc_id 去查行，所以 §5.1 第 ④ 步
// 必须复用已有行的 id（§5.3）。若重导入换了新 id，新切片查不到行被全部丢掉，
// 旧切片反而一路通行——那正是"导入成功却还搜到旧内容"。
//
// 但它**兜不住**重导入时的重复召回：新旧切片 doc_id 相同、文档行是同一行，
// 存活校验看不出差别（§11 场景 3/4）。写侧一致性本期不做（§13 待定 9）。
func (s *Service) filterAlive(
	ctx context.Context,
	hits []repository.VectorHit,
	kbs entity.KnowledgeBases,
) ([]*vo.SearchItemVO, error) {
	if len(hits) == 0 {
		return []*vo.SearchItemVO{}, nil
	}

	idSet := make(map[uint64]struct{}, len(hits))
	for _, h := range hits {
		idSet[h.DocID] = struct{}{}
	}
	ids := make([]uint64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	docs, err := s.docRepo.LoadByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	alive := docs.AliveMap()
	kbMap := kbs.IndexByID()

	items := make([]*vo.SearchItemVO, 0, len(hits))
	for _, h := range hits {
		doc, ok := alive[h.DocID]
		if !ok {
			continue // 孤儿 / 幽灵切片：静默丢弃，这正是 D4 想要的效果
		}
		item := &vo.SearchItemVO{
			ChunkID:     h.ChunkID,
			DocID:       h.DocID,
			DocName:     doc.Name,
			KBID:        h.KBID,
			Order:       h.Order,
			Title:       h.Title,
			Content:     h.Content,
			HeadingPath: h.HeadingPath,
			Score:       h.Score,
		}
		if kb, ok := kbMap[h.KBID]; ok {
			item.KBName = kb.Name
			item.KBNo = kb.No
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) rerankItems(
	ctx context.Context,
	reranker interfaces.IRerank,
	query string,
	items []*vo.SearchItemVO,
) ([]*vo.SearchItemVO, error) {
	cands := make([]interfaces.RerankCandidate, len(items))
	for i, it := range items {
		cands[i] = interfaces.RerankCandidate{
			ChunkID:     it.ChunkID,
			Title:       it.Title,
			HeadingPath: it.HeadingPath,
			Content:     it.Content,
		}
	}

	order, err := reranker.Rerank(ctx, query, cands, len(items))
	if err != nil {
		return nil, err
	}

	out := make([]*vo.SearchItemVO, 0, len(order))
	for _, idx := range order {
		if idx >= 0 && idx < len(items) {
			out = append(out, items[idx])
		}
	}
	return out, nil
}
