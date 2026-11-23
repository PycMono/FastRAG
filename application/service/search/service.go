package search

import (
	"context"
	"math"
	"sort"
	"time"

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
	kbRepo     repository.IKnowledgeRepo
	docRepo    repository.IKnowledgeDocRepo
	store      repository.IVectorStore
	embeddings interfaces.IEmbeddingRegistry
	reranks    interfaces.IRerankRegistry
	tuning     Tuning
}

func NewService(
	kbRepo repository.IKnowledgeRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embeddings interfaces.IEmbeddingRegistry,
	reranks interfaces.IRerankRegistry,
	tuning Tuning,
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
	started := time.Now()
	meta := &vo.SearchMeta{ScoreType: "rrf", RerankRequested: opts.Rerank, Warnings: []string{}}
	diagnostic := &vo.SearchDiagnostics{BM25Top: s.tuning.BM25Top, KNNTop: s.tuning.KNNTops, NumCandidates: max(s.tuning.NumCandidates, s.tuning.KNNTops), RerankBudget: opts.effectiveRetrieveCount(), DenseWeight: opts.DenseWeight, RankConstant: s.tuning.RankConstant, Counts: map[string]int{}, TimingsMS: map[string]float64{}}
	finish := func(items []*vo.SearchItemVO) *vo.SearchResultVO {
		diagnostic.Counts["returned"] = len(items)
		diagnostic.TimingsMS["total"] = durationMS(started)
		result := &vo.SearchResultVO{Items: items, Meta: meta}
		if in.Diagnostics {
			result.Diagnostics = diagnostic
		} else {
			for _, it := range items {
				it.Diagnostics = nil
			}
		}
		return result
	}

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

	// ① 加载 KB。不属于该 tenant 的库在仓储层就被丢掉了
	stage := time.Now()
	kbs, err := s.kbRepo.FindByNos(ctx, in.KBNos, in.Tenant)
	if err != nil {
		return nil, err
	}
	diagnostic.TimingsMS["knowledge_lookup"] = durationMS(stage)
	if len(kbs) == 0 {
		return finish([]*vo.SearchItemVO{}), nil
	}

	// ② 查询向量。dense_weight 为 0 时不发这一路，也就不必花这次调用
	stage = time.Now()
	var queryVec []float32
	if opts.useKNN() {
		queryVec, err = embedder.EmbedQuery(ctx, opts.Query)
		if err != nil {
			logsdk.Error(ctx, "知识库检索报错", logsdk.Err(err))
			return nil, apperrors.ErrEmbeddingFailed.Wrap(err)
		}
	}

	diagnostic.TimingsMS["embedding"] = durationMS(stage)
	// ③ 检索：仓储内并行执行 BM25 与 kNN 两个请求。
	//
	// 两路召回量读取服务配置；精排预算独立控制模型输入数量。
	fetchSize := opts.effectiveRetrieveCount()
	stage = time.Now()
	r, err := s.store.Search(ctx, s.buildReq(kbs, opts, queryVec))
	if err != nil {
		return nil, err
	}

	diagnostic.TimingsMS["retrieval"] = durationMS(stage)
	for key, value := range r.TimingsMS {
		diagnostic.TimingsMS[key] = value
	}
	diagnostic.Counts["bm25"] = len(r.BM25)
	diagnostic.Counts["knn"] = len(r.KNN)
	stage = time.Now()
	// ④ 应用层加权 RRF 融合
	//
	//    两路各自从 rank=1 起算、按权重加权（见 Fuse 的注释）——不把其中一路
	//    拼到另一路后面，那等于按路的顺序给了个固定的偏置。
	// 空的一路（dense_weight 把它短路掉了）不占位，权重为 0 的路 Fuse 会跳过
	routes := make([]Route, 0, 2)
	if len(r.BM25) > 0 {
		routes = append(routes, Route{Hits: r.BM25, Weight: 1 - opts.DenseWeight})
	}
	if len(r.KNN) > 0 {
		routes = append(routes, Route{Hits: r.KNN, Weight: opts.DenseWeight})
	}
	merged := Fuse(routes, s.tuning.RankConstant, 0)
	diagnostic.Counts["fused"] = len(merged)
	diagnostic.TimingsMS["fusion"] = durationMS(stage)
	stage = time.Now()

	// ⑤ 存活校验 + 补展示字段（1 次批量 SQL，不取内容）
	items, err := s.filterAlive(ctx, merged, kbs)
	if err != nil {
		return nil, err
	}

	diagnostic.TimingsMS["alive_filter"] = durationMS(stage)
	diagnostic.Counts["alive"] = len(items)
	explainHits(items, r)
	if in.FusionMinScore > 0 {
		kept := items[:0]
		for _, it := range items {
			if it.Score >= in.FusionMinScore {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	diagnostic.Counts["after_fusion_threshold"] = len(items)
	// ⑥ 精排预算在融合、存活校验和融合分过滤之后应用。
	if opts.Rerank && len(items) > fetchSize {
		items = items[:fetchSize]
	}
	diagnostic.Counts["rerank_candidates"] = 0
	stage = time.Now()
	// 精排失败时保留融合原序，并明确标记评分与阈值语义。
	//
	//    判据里不再有 s.rerank != nil：注册表永远返回一个非 nil 的实现，
	//    enabled=false 时给的是空实现（原序返回），行为与以前一致。
	if opts.Rerank && len(items) > 0 {
		diagnostic.Counts["rerank_candidates"] = len(items)
		reranked, err := s.rerankItems(ctx, reranker, opts.Query, items)
		if err != nil {
			logsdk.Warn(ctx, "重排失败，退回融合原序", logsdk.Err(err))
			meta.Degraded = true
			meta.Warnings = append(meta.Warnings, "精排失败，已使用融合排序。")
		} else {
			items = reranked
			for _, it := range items {
				if it.Diagnostics.RerankScore != nil {
					meta.RerankApplied = true
				}
			}
			if meta.RerankApplied {
				meta.ScoreType = "rerank"
				for _, it := range items {
					if it.Diagnostics.RerankScore == nil {
						meta.ScoreType = "mixed"
						meta.Degraded = true
						meta.Warnings = append(meta.Warnings, "部分候选未取得精排分数，保留其融合分。")
						break
					}
				}
			} else {
				meta.Degraded = true
				meta.Warnings = append(meta.Warnings, "精排未实际产生模型分数，已使用融合排序。")
			}
		}
	}
	diagnostic.TimingsMS["rerank"] = durationMS(stage)
	// Individual unscored candidates keep their RRF score and bypass a rerank threshold.
	if opts.Rerank && opts.MinScore > 0 {
		for _, it := range items {
			if it.Diagnostics.RerankScore == nil {
				meta.ThresholdSkipped = true
				break
			}
		}
		if meta.ThresholdSkipped {
			meta.Warnings = append(meta.Warnings, "未取得精排分数的结果跳过精排阈值；可单独设置融合分阈值。")
		}
	}
	stage = time.Now()

	// ⑦ 分数阈值过滤。MinScore<=0 表示不启用。
	//
	// 精排阈值仅用于已取得精排分的候选；显式关闭精排时保留旧的 RRF 阈值语义。
	if opts.MinScore > 0 {
		kept := make([]*vo.SearchItemVO, 0, len(items))
		for _, it := range items {
			if it.Score >= opts.MinScore || (opts.Rerank && it.Diagnostics.RerankScore == nil) {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	if len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	diagnostic.TimingsMS["final_filter"] = durationMS(stage)
	return finish(items), nil
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
		DenseWeight:   weight,
		MinScore:      in.MinScore,
		Rerank:        in.RerankSwitch,
	}.normalize()
}

func (s *Service) buildReq(
	kbs entity.Knowledges,
	opts searchOptions,
	queryVec []float32,
) repository.VectorSearchReq {
	ids := make([]uint64, 0, len(kbs))
	for _, kb := range kbs {
		ids = append(ids, kb.ID)
	}

	return repository.VectorSearchReq{
		// 所有库同 tenant：FindByNos 加载时已按 tenant 过滤过
		Tenant:        kbs[0].Tenant,
		KBIDs:         ids,
		Query:         opts.Query,
		QueryVec:      queryVec,
		DenseWeight:   opts.DenseWeight,
		BM25Top:       s.tuning.BM25Top,
		KNNTops:       s.tuning.KNNTops,
		NumCandidates: max(s.tuning.NumCandidates, s.tuning.KNNTops),
	}
}

// filterAlive 丢掉孤儿与幽灵切片。
//
// 两个要点：
//  1. doc_id 先去重再查库——召回 200 条通常只落在几十个文档上，
//     按召回条数去查数据库是纯浪费；
//  2. 内容不回表取，这里只补 doc_name / kb_name 两个展示字段。
//
// 它同时兜住了「写一半失败」和「删一半失败」两种残留：
//   - 孤儿：切片写进 ES 了，但 MySQL 文档行没落（第三写失败）→ 查不到 doc，丢弃；
//   - 幽灵：文档行已硬删，ES 还没清完 → 查不到 doc，丢弃。
//
// 它的前提是 doc_id 稳定：全靠切片带的 doc_id 去查行，所以导入时必须
// 复用已有行的 id。若重导入换了新 id，新切片查不到行被全部丢掉，
// 旧切片反而一路通行——那正是"导入成功却还搜到旧内容"。
//
// 但它**兜不住**重导入时的重复召回：新旧切片 doc_id 相同、文档行是同一行，
// 存活校验看不出差别。写侧一致性本期不做。
func (s *Service) filterAlive(
	ctx context.Context,
	hits []repository.VectorHit,
	kbs entity.Knowledges,
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

	docs, err := s.docRepo.FindByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	alive := docs.AliveMap()
	kbMap := kbs.IndexByID()

	items := make([]*vo.SearchItemVO, 0, len(hits))
	for _, h := range hits {
		doc, ok := alive[h.DocID]
		if !ok {
			continue // 孤儿 / 幽灵切片：静默丢弃，这正是存活校验想要的效果
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
	for _, s := range order {
		if s.Index < 0 || s.Index >= len(items) {
			continue
		}
		if math.IsNaN(s.Score) || math.IsInf(s.Score, 0) {
			return nil, apperrors.ErrRerankFailed
		}
		// Work on copies so a malformed response cannot partially mutate the fallback.
		copyItem := *items[s.Index]
		it := &copyItem
		if it.Diagnostics != nil {
			detail := *it.Diagnostics
			it.Diagnostics = &detail
		}
		// 精排分替换融合分。不换的话页面就会出现"0.0150 排在 0.0156 前面"——
		// 顺序是精排给的，分数还是旧的 RRF 分，两列自相矛盾，也看不出精排生效没有。
		//
		// 负分是哨兵（空内容 / 未启用精排），保留原融合分不动。
		if s.Score >= 0 {
			if it.Diagnostics == nil {
				it.Diagnostics = &vo.HitDiagnostics{FusionScore: it.Score}
			}
			score := s.Score
			it.Diagnostics.RerankScore = &score
			it.Score = s.Score
		}
		out = append(out, it)
	}
	if len(out) == 0 && len(items) > 0 {
		return nil, apperrors.ErrRerankFailed
	}
	scored := false
	for _, it := range out {
		if it.Diagnostics != nil && it.Diagnostics.RerankScore != nil {
			scored = true
		}
	}
	if !scored {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	}
	return out, nil
}

func durationMS(start time.Time) float64 { return float64(time.Since(start).Microseconds()) / 1000 }

func explainHits(items []*vo.SearchItemVO, routes repository.SearchResult) {
	byID := map[string]*vo.HitDiagnostics{}
	for _, it := range items {
		it.Diagnostics = &vo.HitDiagnostics{FusionScore: it.Score}
		byID[it.ChunkID] = it.Diagnostics
	}
	for i, h := range routes.BM25 {
		if d := byID[h.ChunkID]; d != nil {
			score := h.Score
			d.BM25Rank = i + 1
			d.BM25Score = &score
		}
	}
	for i, h := range routes.KNN {
		if d := byID[h.ChunkID]; d != nil {
			score := h.Score
			d.KNNRank = i + 1
			d.KNNScore = &score
		}
	}
}
