package search

import (
	"context"
	"errors"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
)

// ─── mock 实现 ───────────────────────────────────────────────────────────────

type mockKBRepo struct {
	load func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error)
}

func (m *mockKBRepo) FindByNo(ctx context.Context, no, account string) (*entity.KnowledgeBase, error) {
	return nil, nil
}
func (m *mockKBRepo) FindByNos(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
	return m.load(ctx, nos, account)
}
func (m *mockKBRepo) ApplyChunkDelta(ctx context.Context, kbID uint64, delta int64) error { return nil }
func (m *mockKBRepo) ApplyDocDelta(ctx context.Context, kbID uint64, delta int64) error   { return nil }

type mockDocRepo struct {
	load func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error)
}

func (m *mockDocRepo) FindByName(ctx context.Context, kbID uint64, name string) (*entity.KnowledgeDoc, error) {
	return nil, nil
}
func (m *mockDocRepo) FindByIDs(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
	return m.load(ctx, ids)
}
func (m *mockDocRepo) Save(ctx context.Context, doc *entity.KnowledgeDoc) (oldChunkCount int, created bool, err error) {
	return 0, false, nil
}
func (m *mockDocRepo) SoftDelete(ctx context.Context, kbID uint64, name string, now int64) (*entity.KnowledgeDoc, error) {
	return nil, nil
}
func (m *mockDocRepo) SoftDeleteByKBID(ctx context.Context, kbID uint64, account string, now int64) (int64, error) {
	return 0, nil
}

type mockVectorStore struct {
	search func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error)

	// searched 记录 Search 是否被调到过。用例用它断言"名字解析失败时
	// 不该再发 ES 请求"——mock 的 search 字段可能为 nil，靠这个布尔量观察。
	searched bool
}

func (m *mockVectorStore) Save(ctx context.Context, docs []repository.VectorDoc) error { return nil }
func (m *mockVectorStore) Refresh(ctx context.Context) error                           { return nil }
func (m *mockVectorStore) DeleteByQuery(ctx context.Context, filter repository.VectorFilter) (int64, error) {
	return 0, nil
}
func (m *mockVectorStore) DeleteExcept(ctx context.Context, filter repository.VectorFilter, keepIDs []string) (int64, error) {
	return 0, nil
}
func (m *mockVectorStore) Search(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
	m.searched = true
	if m.search == nil {
		return repository.SearchResult{}, nil
	}
	return m.search(ctx, req)
}

type mockEmbedding struct {
	dim int
}

func (m *mockEmbedding) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, nil
}
func (m *mockEmbedding) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	out := make([]float32, m.dim)
	return out, nil
}
func (m *mockEmbedding) Dim() int      { return m.dim }
func (m *mockEmbedding) Model() string { return "mock" }

type mockRerank struct {
	called bool
	order  []interfaces.ScoredIndex
	err    error
}

func (m *mockRerank) Rerank(ctx context.Context, query string, cands []interfaces.RerankCandidate, topN int) ([]interfaces.ScoredIndex, error) {
	m.called = true
	if m.err != nil {
		return nil, m.err
	}
	return m.order, nil
}

// mockEmbeddingRegistry 只有一个名字，且总是能取到——检索用例不关心取名字这件事。
type mockEmbeddingRegistry struct{ impl interfaces.IEmbedding }

func (m mockEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) { return m.impl, nil }
func (m mockEmbeddingRegistry) Names() []string                                { return []string{"mock"} }

// mockRerankRegistry 同上。
type mockRerankRegistry struct{ impl interfaces.IRerank }

func (m mockRerankRegistry) Get(name string) (interfaces.IRerank, error) { return m.impl, nil }
func (m mockRerankRegistry) Names() []string                             { return []string{"mock"} }

// errEmbeddingRegistry 任何名字都取不到。
type errEmbeddingRegistry struct{}

func (errEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) {
	return nil, apperrors.NewParamError("没有名为 " + name + " 的 embedding 模型")
}
func (errEmbeddingRegistry) Names() []string { return []string{"bge-m3"} }

// ─── 构造 Service 的辅助函数 ─────────────────────────────────────────────────

func newSearchServiceForTest(
	kbRepo repository.IKnowledgeBaseRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embedder interfaces.IEmbedding,
	rerank interfaces.IRerank,
) *Service {
	return NewService(kbRepo, docRepo, store,
		mockEmbeddingRegistry{impl: embedder},
		mockRerankRegistry{impl: rerank},
		SearchTuning{
			BM25Top:       10,
			KNNTops:       10,
			NumCandidates: 50,
			RankConstant:  60,
			DenseWeight:   0.5,
		})
}

func defaultKB() *entity.KnowledgeBase {
	return &entity.KnowledgeBase{
		ID:         1,
		No:         "demo-kb",
		Account:    "demo",
		Name:       "测试知识库",
		SearchMode: "title_and_content",
	}
}

func defaultDoc() *entity.KnowledgeDoc {
	return &entity.KnowledgeDoc{
		ID:       100,
		KBID:     1,
		Account:  "demo",
		Name:     "doc.md",
		DeleteTs: 0,
	}
}

// ─── 测试用例 ────────────────────────────────────────────────────────────────

func TestService_Search_NoRerank(t *testing.T) {
	kbRepo := &mockKBRepo{
		load: func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
			return entity.KnowledgeBases{defaultKB()}, nil
		},
	}
	docRepo := &mockDocRepo{
		load: func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
			return entity.KnowledgeDocs{defaultDoc()}, nil
		},
	}
	store := &mockVectorStore{
		search: func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
			return repository.SearchResult{
				BM25: []repository.VectorHit{
					{ChunkID: "c1", DocID: 100, KBID: 1, Score: 10.0, Content: "content 1"},
					{ChunkID: "c2", DocID: 100, KBID: 1, Score: 5.0, Content: "content 2"},
				},
				KNN: []repository.VectorHit{
					{ChunkID: "c2", DocID: 100, KBID: 1, Score: 0.8, Content: "content 2"},
					{ChunkID: "c1", DocID: 100, KBID: 1, Score: 0.6, Content: "content 1"},
				},
			}, nil
		},
	}
	reranker := &mockRerank{order: []interfaces.ScoredIndex{{Index: 1}, {Index: 0}}}
	svc := newSearchServiceForTest(kbRepo, docRepo, store, &mockEmbedding{dim: 3}, reranker)

	// rerank_switch=false 时不应调用 rerank
	res, err := svc.Search(context.Background(), &dto.SearchDTO{
		Account:      "demo",
		KBNos:        []string{"demo-kb"},
		Query:        "test",
		Limit:        5,
		DenseWeight:  floatPtr(0.5),
		RerankSwitch: false,
	})
	if err != nil {
		t.Fatalf("Search 不应失败: %v", err)
	}
	if reranker.called {
		t.Error("rerank_switch=false 时不应调用 rerank")
	}
	if len(res.Items) != 2 {
		t.Fatalf("期望返回 2 条，得到 %d 条", len(res.Items))
	}
}

func TestService_Search_RerankSuccess(t *testing.T) {
	kbRepo := &mockKBRepo{
		load: func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
			return entity.KnowledgeBases{defaultKB()}, nil
		},
	}
	docRepo := &mockDocRepo{
		load: func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
			return entity.KnowledgeDocs{defaultDoc()}, nil
		},
	}
	store := &mockVectorStore{
		search: func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
			return repository.SearchResult{
				BM25: []repository.VectorHit{
					{ChunkID: "c1", DocID: 100, KBID: 1, Score: 10.0, Content: "content 1"},
					{ChunkID: "c2", DocID: 100, KBID: 1, Score: 5.0, Content: "content 2"},
				},
				KNN: []repository.VectorHit{
					{ChunkID: "c2", DocID: 100, KBID: 1, Score: 0.8, Content: "content 2"},
					{ChunkID: "c1", DocID: 100, KBID: 1, Score: 0.6, Content: "content 1"},
				},
			}, nil
		},
	}
	reranker := &mockRerank{order: []interfaces.ScoredIndex{ // rerank 把 c2 排第一，并给出上游分
		{Index: 1, Score: 0.93},
		{Index: 0, Score: 0.21},
	}}
	svc := newSearchServiceForTest(kbRepo, docRepo, store, &mockEmbedding{dim: 3}, reranker)

	res, err := svc.Search(context.Background(), &dto.SearchDTO{
		Account:      "demo",
		KBNos:        []string{"demo-kb"},
		Query:        "test",
		Limit:        5,
		DenseWeight:  floatPtr(0.5),
		RerankSwitch: true,
	})
	if err != nil {
		t.Fatalf("Search 不应失败: %v", err)
	}
	if !reranker.called {
		t.Fatal("rerank_switch=true 时应调用 rerank")
	}
	if len(res.Items) != 2 {
		t.Fatalf("期望返回 2 条，得到 %d 条", len(res.Items))
	}
	// rerank 返回 order[1,0]，所以 c2 在前
	if res.Items[0].ChunkID != "c2" {
		t.Errorf("rerank 后第一条应为 c2，得到 %s", res.Items[0].ChunkID)
	}
	// 分数要跟着精排走。不换的话展示层会出现"0.0150 排在 0.0156 前面"
	// ——顺序是精排给的、分数还是旧融合分，两列自相矛盾。
	if res.Items[0].Score != 0.93 {
		t.Errorf("精排后分数应回填为上游分 0.93，得到 %v", res.Items[0].Score)
	}
	if res.Items[1].Score != 0.21 {
		t.Errorf("精排后第二条分数应为 0.21，得到 %v", res.Items[1].Score)
	}
}

// min_score 比的是**最终** Score。这里用精排分（0.93 / 0.21）喂进去，
// min_score=0.5 就该只留下 0.93 那条。
//
// 顺带盯住两件事：过滤排在截断之后（Limit=5 但只回 1 条，不足 Limit 是有意的），
// 以及全筛光时返回的是空切片而不是 nil（JSON 里是 [] 不是 null）。
func TestService_Search_MinScore_FiltersByFinalScore(t *testing.T) {
	newSvc := func() (*Service, *mockRerank) {
		kbRepo := &mockKBRepo{
			load: func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
				return entity.KnowledgeBases{defaultKB()}, nil
			},
		}
		docRepo := &mockDocRepo{
			load: func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
				return entity.KnowledgeDocs{defaultDoc()}, nil
			},
		}
		store := &mockVectorStore{
			search: func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
				return repository.SearchResult{
					BM25: []repository.VectorHit{
						{ChunkID: "c1", DocID: 100, KBID: 1, Score: 10.0, Content: "content 1"},
						{ChunkID: "c2", DocID: 100, KBID: 1, Score: 5.0, Content: "content 2"},
					},
					KNN: []repository.VectorHit{
						{ChunkID: "c2", DocID: 100, KBID: 1, Score: 0.8, Content: "content 2"},
						{ChunkID: "c1", DocID: 100, KBID: 1, Score: 0.6, Content: "content 1"},
					},
				}, nil
			},
		}
		reranker := &mockRerank{order: []interfaces.ScoredIndex{
			{Index: 1, Score: 0.93}, // c2
			{Index: 0, Score: 0.21}, // c1
		}}
		return newSearchServiceForTest(kbRepo, docRepo, store, &mockEmbedding{dim: 3}, reranker), reranker
	}

	search := func(t *testing.T, minScore float64) []*vo.SearchItemVO {
		t.Helper()
		svc, _ := newSvc()
		res, err := svc.Search(context.Background(), &dto.SearchDTO{
			Account:      "demo",
			KBNos:        []string{"demo-kb"},
			Query:        "test",
			Limit:        5,
			DenseWeight:  floatPtr(0.5),
			MinScore:     minScore,
			RerankSwitch: true,
		})
		if err != nil {
			t.Fatalf("Search 不应失败: %v", err)
		}
		return res.Items
	}

	t.Run("0 表示不启用", func(t *testing.T) {
		if got := search(t, 0); len(got) != 2 {
			t.Errorf("min_score=0 不该过滤，期望 2 条，得到 %d 条", len(got))
		}
	})

	t.Run("按最终分筛掉低分", func(t *testing.T) {
		got := search(t, 0.5)
		if len(got) != 1 {
			t.Fatalf("期望只留 1 条（不足 Limit=5 是有意的），得到 %d 条", len(got))
		}
		if got[0].ChunkID != "c2" {
			t.Errorf("留下的应是高分那条 c2，得到 %s", got[0].ChunkID)
		}
	})

	t.Run("全筛光时是空切片不是 nil", func(t *testing.T) {
		got := search(t, 0.99)
		if len(got) != 0 {
			t.Fatalf("阈值高于所有分，期望 0 条，得到 %d 条", len(got))
		}
		if got == nil {
			t.Error("应为空切片（JSON 里是 []），不能是 nil（会序列化成 null）")
		}
	})
}

// 哨兵分（空内容 / 未启用精排）不得覆盖融合分——否则展示层会突然出现一条 0 分，
// 把"没上游分"和"上游给了 0 分"混为一谈。
func TestService_Search_RerankSentinelScore_KeepsFusedScore(t *testing.T) {
	kbRepo := &mockKBRepo{
		load: func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
			return entity.KnowledgeBases{defaultKB()}, nil
		},
	}
	docRepo := &mockDocRepo{
		load: func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
			return entity.KnowledgeDocs{defaultDoc()}, nil
		},
	}
	store := &mockVectorStore{
		search: func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
			return repository.SearchResult{
				BM25: []repository.VectorHit{
					{ChunkID: "c1", DocID: 100, KBID: 1, Score: 10.0, Content: "content 1"},
					{ChunkID: "c2", DocID: 100, KBID: 1, Score: 5.0, Content: "content 2"},
				},
				KNN: []repository.VectorHit{},
			}, nil
		},
	}
	reranker := &mockRerank{order: []interfaces.ScoredIndex{
		{Index: 0, Score: interfaces.NoRerankScore},
		{Index: 1, Score: interfaces.NoRerankScore},
	}}
	svc := newSearchServiceForTest(kbRepo, docRepo, store, &mockEmbedding{dim: 3}, reranker)

	res, err := svc.Search(context.Background(), &dto.SearchDTO{
		Account:      "demo",
		KBNos:        []string{"demo-kb"},
		Query:        "test",
		Limit:        5,
		DenseWeight:  floatPtr(0.5),
		RerankSwitch: true,
	})
	if err != nil {
		t.Fatalf("Search 不应失败: %v", err)
	}
	for i, it := range res.Items {
		if it.Score <= 0 {
			t.Errorf("第 %d 条的融合分应被保留（>0），得到 %v——哨兵分不该写进展示字段", i, it.Score)
		}
	}
}

func TestService_Search_RerankFailed_Fallback(t *testing.T) {
	kbRepo := &mockKBRepo{
		load: func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
			return entity.KnowledgeBases{defaultKB()}, nil
		},
	}
	docRepo := &mockDocRepo{
		load: func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
			return entity.KnowledgeDocs{defaultDoc()}, nil
		},
	}
	store := &mockVectorStore{
		search: func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
			return repository.SearchResult{
				BM25: []repository.VectorHit{
					{ChunkID: "c1", DocID: 100, KBID: 1, Score: 10.0, Content: "content 1"},
					{ChunkID: "c2", DocID: 100, KBID: 1, Score: 5.0, Content: "content 2"},
				},
				KNN: []repository.VectorHit{},
			}, nil
		},
	}
	reranker := &mockRerank{err: errors.New("rerank down")}
	svc := newSearchServiceForTest(kbRepo, docRepo, store, &mockEmbedding{dim: 3}, reranker)

	res, err := svc.Search(context.Background(), &dto.SearchDTO{
		Account:      "demo",
		KBNos:        []string{"demo-kb"},
		Query:        "test",
		Limit:        5,
		DenseWeight:  floatPtr(0.0), // 纯 BM25
		RerankSwitch: true,
	})
	if err != nil {
		t.Fatalf("rerank 失败不应阻断搜索: %v", err)
	}
	if !reranker.called {
		t.Fatal("应尝试调用 rerank")
	}
	if len(res.Items) != 2 {
		t.Fatalf("期望返回 2 条，得到 %d 条", len(res.Items))
	}
	// 退回原序（BM25 原序 c1 在前）
	if res.Items[0].ChunkID != "c1" {
		t.Errorf("失败回退后第一条应为 c1，得到 %s", res.Items[0].ChunkID)
	}
}

func TestService_Search_RetrieveCount_Enabled(t *testing.T) {
	var gotFetchSize int
	kbRepo := &mockKBRepo{
		load: func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
			return entity.KnowledgeBases{defaultKB()}, nil
		},
	}
	docRepo := &mockDocRepo{
		load: func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
			return entity.KnowledgeDocs{defaultDoc()}, nil
		},
	}
	store := &mockVectorStore{
		search: func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
			gotFetchSize = req.BM25Top
			// 返回 30 条，让 rerank 有候选可挑
			hits := make([]repository.VectorHit, 30)
			for i := range hits {
				hits[i] = repository.VectorHit{
					ChunkID: string(rune('a' + i)),
					DocID:   100,
					KBID:    1,
					Score:   float64(30 - i),
					Content: "content",
				}
			}
			return repository.SearchResult{BM25: hits, KNN: []repository.VectorHit{}}, nil
		},
	}
	reranker := &mockRerank{order: []interfaces.ScoredIndex{{Index: 0}}}
	svc := newSearchServiceForTest(kbRepo, docRepo, store, &mockEmbedding{dim: 3}, reranker)

	retrieveCount := 30
	res, err := svc.Search(context.Background(), &dto.SearchDTO{
		Account:       "demo",
		KBNos:         []string{"demo-kb"},
		Query:         "test",
		Limit:         10,
		RetrieveCount: &retrieveCount,
		DenseWeight:   floatPtr(0.0),
		RerankSwitch:  true,
	})
	if err != nil {
		t.Fatalf("Search 不应失败: %v", err)
	}
	if gotFetchSize != 30 {
		t.Errorf("期望 ES 召回 30 条，实际 %d", gotFetchSize)
	}
	if len(res.Items) != 1 { // rerank mock 只返回 1 条
		t.Errorf("期望返回 1 条，得到 %d 条", len(res.Items))
	}
}

func TestService_Search_RetrieveCount_Default(t *testing.T) {
	var gotFetchSize int
	kbRepo := &mockKBRepo{
		load: func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
			return entity.KnowledgeBases{defaultKB()}, nil
		},
	}
	docRepo := &mockDocRepo{
		load: func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
			return entity.KnowledgeDocs{defaultDoc()}, nil
		},
	}
	store := &mockVectorStore{
		search: func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
			gotFetchSize = req.BM25Top
			return repository.SearchResult{BM25: []repository.VectorHit{}, KNN: []repository.VectorHit{}}, nil
		},
	}
	svc := newSearchServiceForTest(kbRepo, docRepo, store, &mockEmbedding{dim: 3}, &mockRerank{})

	_, err := svc.Search(context.Background(), &dto.SearchDTO{
		Account:      "demo",
		KBNos:        []string{"demo-kb"},
		Query:        "test",
		Limit:        10,
		DenseWeight:  floatPtr(0.0),
		RerankSwitch: true,
	})
	if err != nil {
		t.Fatalf("Search 不应失败: %v", err)
	}
	if gotFetchSize != 20 {
		t.Errorf("未指定 retrieve_count 时默认 limit*2，期望 20，实际 %d", gotFetchSize)
	}
}

func TestService_Search_RerankItems_CandidateComposition(t *testing.T) {
	reranker := &mockRerank{order: []interfaces.ScoredIndex{{Index: 0}}}
	svc := NewService(nil, nil, nil, nil, mockRerankRegistry{impl: reranker}, SearchTuning{})

	items := []*vo.SearchItemVO{
		{
			ChunkID:     "c1",
			Title:       "标题",
			HeadingPath: "A > B",
			Content:     "正文",
		},
	}
	_, err := svc.rerankItems(context.Background(), reranker, "query", items)
	if err != nil {
		t.Fatalf("rerankItems 不应失败: %v", err)
	}
	if !reranker.called {
		t.Fatal("应调用 rerank")
	}
}

// floatPtr 辅助函数。
func floatPtr(f float64) *float64 { return &f }

// 名字写错必须**在花掉一次 ES 往返之前**就返回。
//
// 注意即便 dense_weight=0（压根不走向量路）也要校验 embed_model——
// 传了一个用不上的错名字同样应该被指出，而不是被静默忽略。
func TestService_Search_UnknownModel_FailsBeforeES(t *testing.T) {
	kbRepo := &mockKBRepo{load: func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
		return entity.KnowledgeBases{defaultKB()}, nil
	}}
	store := &mockVectorStore{}
	svc := NewService(kbRepo, &mockDocRepo{}, store, errEmbeddingRegistry{}, mockRerankRegistry{}, SearchTuning{})

	_, err := svc.Search(context.Background(), &dto.SearchDTO{
		Account:    "demo",
		KBNos:      []string{"kb1"},
		Query:      "问题",
		EmbedModel: "nope",
	})
	if err == nil {
		t.Fatal("未知 embed_model 必须报错")
	}
	if store.searched {
		t.Error("名字解析失败时不该再发 ES 请求")
	}
}
