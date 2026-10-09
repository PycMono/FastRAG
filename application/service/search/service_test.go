package search

import (
	"context"
	"errors"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/common/vo"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
)

// ─── mock 实现 ───────────────────────────────────────────────────────────────

type mockKBRepo struct {
	load func(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error)
}

func (m *mockKBRepo) LoadByNo(ctx context.Context, no, account string) (*entity.KnowledgeBase, error) {
	return nil, nil
}
func (m *mockKBRepo) LoadByNos(ctx context.Context, nos []string, account string) (entity.KnowledgeBases, error) {
	return m.load(ctx, nos, account)
}
func (m *mockKBRepo) ApplyChunkDelta(ctx context.Context, kbID uint64, delta int64) error { return nil }
func (m *mockKBRepo) ApplyDocDelta(ctx context.Context, kbID uint64, delta int64) error   { return nil }
func (m *mockKBRepo) SetCounts(ctx context.Context, kbID uint64, docCount, chunkCount int64) error {
	return nil
}

type mockDocRepo struct {
	load func(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error)
}

func (m *mockDocRepo) LoadByName(ctx context.Context, kbID uint64, name string) (*entity.KnowledgeDoc, error) {
	return nil, nil
}
func (m *mockDocRepo) LoadByIDs(ctx context.Context, ids []uint64) (entity.KnowledgeDocs, error) {
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
func (m *mockDocRepo) CountByKBID(ctx context.Context, kbID uint64) (int64, error)        { return 0, nil }
func (m *mockDocRepo) SumChunkCountByKBID(ctx context.Context, kbID uint64) (int64, error) { return 0, nil }

type mockVectorStore struct {
	search func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error)
}

func (m *mockVectorStore) Save(ctx context.Context, docs []repository.VectorDoc) error { return nil }
func (m *mockVectorStore) Refresh(ctx context.Context) error                            { return nil }
func (m *mockVectorStore) DeleteByQuery(ctx context.Context, filter repository.VectorFilter) (int64, error) {
	return 0, nil
}
func (m *mockVectorStore) DeleteExcept(ctx context.Context, filter repository.VectorFilter, keepIDs []string) (int64, error) {
	return 0, nil
}
func (m *mockVectorStore) Search(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
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
func (m *mockEmbedding) Dim() int     { return m.dim }
func (m *mockEmbedding) Model() string { return "mock" }

type mockRerank struct {
	called bool
	order  []int
	err    error
}

func (m *mockRerank) Rerank(ctx context.Context, query string, cands []interfaces.RerankCandidate, topN int) ([]int, error) {
	m.called = true
	if m.err != nil {
		return nil, m.err
	}
	return m.order, nil
}

// ─── 构造 Service 的辅助函数 ─────────────────────────────────────────────────

func newSearchServiceForTest(
	kbRepo repository.IKnowledgeBaseRepo,
	docRepo repository.IKnowledgeDocRepo,
	store repository.IVectorStore,
	embedder interfaces.IEmbedding,
	rerank interfaces.IRerank,
) *Service {
	return NewService(kbRepo, docRepo, store, embedder, rerank, SearchTuning{
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
	reranker := &mockRerank{order: []int{1, 0}}
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
	reranker := &mockRerank{order: []int{1, 0}} // rerank 把 c2 排第一
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
	reranker := &mockRerank{order: []int{0}}
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
	reranker := &mockRerank{order: []int{0}}
	svc := NewService(nil, nil, nil, nil, reranker, SearchTuning{})

	items := []*vo.SearchItemVO{
		{
			ChunkID:     "c1",
			Title:       "标题",
			HeadingPath: "A > B",
			Content:     "正文",
		},
	}
	_, err := svc.rerankItems(context.Background(), "query", items)
	if err != nil {
		t.Fatalf("rerankItems 不应失败: %v", err)
	}
	if !reranker.called {
		t.Fatal("应调用 rerank")
	}
}

// floatPtr 辅助函数。
func floatPtr(f float64) *float64 { return &f }