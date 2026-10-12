package ingest

import (
	"context"
	"strings"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
	"github.com/PycMono/FastRAG/domain/service"
	"github.com/PycMono/go-mysql-sdk/transaction"
)

// ─── 桩子 ────────────────────────────────────────────────────────────────────
//
// 一律内嵌 nil 接口：没显式实现的方法一旦被调到就 panic，
// 正好说明用例走到了不该走的地方（与 service_test.go 的 stubKBRepo 同款）。

// stubDocRepo 记录 FindByName 与 Save 的结果，其余方法 panic。
type stubDocRepo struct {
	repository.IKnowledgeDocRepo
	existing  *entity.KnowledgeDoc // FindByName 的返回；nil 表示库里没有
	saved     *entity.KnowledgeDoc // Save 收到的实体
	saveCalls int
}

func (s *stubDocRepo) FindByName(ctx context.Context, kbID uint64, name string) (*entity.KnowledgeDoc, error) {
	return s.existing, nil
}

func (s *stubDocRepo) Save(ctx context.Context, doc *entity.KnowledgeDoc) (int, bool, error) {
	s.saved = doc
	s.saveCalls++
	if s.existing == nil {
		return 0, true, nil // 新建
	}
	return s.existing.ChunkCount, false, nil // 更新，返回更新前的切片数
}

// stubStore 记录三次写入的顺序与内容。
type stubStore struct {
	repository.IVectorStore
	calls      []string
	savedDocs  []repository.VectorDoc
	keepIDs    []string
	refreshHit bool
}

func (s *stubStore) Save(ctx context.Context, docs []repository.VectorDoc) error {
	s.calls = append(s.calls, "save")
	s.savedDocs = docs
	return nil
}

func (s *stubStore) Refresh(ctx context.Context) error {
	s.calls = append(s.calls, "refresh")
	s.refreshHit = true
	return nil
}

func (s *stubStore) DeleteExcept(ctx context.Context, f repository.VectorFilter, keepIDs []string) (int64, error) {
	s.calls = append(s.calls, "delete_except")
	s.keepIDs = keepIDs
	return 0, nil
}

// stubKBRepoDelta 在 stubKBRepo 之上多实现两个计数方法。
type stubKBRepoDelta struct {
	stubKBRepo
	chunkDelta int64
	docDelta   int64
}

func (s *stubKBRepoDelta) ApplyChunkDelta(ctx context.Context, kbID uint64, delta int64) error {
	s.chunkDelta += delta
	return nil
}

func (s *stubKBRepoDelta) ApplyDocDelta(ctx context.Context, kbID uint64, delta int64) error {
	s.docDelta += delta
	return nil
}

// stubTm 直接跑回调（不起真实事务）。
//
// 内嵌 nil 的 transaction.Manager：Manager 有 3 个方法，本用例只该碰 Transaction，
// 其余方法被调到就 panic（同款约定见 service_test.go 的 stubKBRepo）。
type stubTm struct {
	transaction.Manager
	calls int
}

func (t *stubTm) Transaction(ctx context.Context, cb func(context.Context) error) error {
	t.calls++
	return cb(ctx)
}

// stubEmbedding 按输入条数返回同样条数的向量。
type stubEmbedding struct{ dim int }

func (e stubEmbedding) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		v := make([]float32, e.dim)
		v[0] = float32(i)
		out[i] = v
	}
	return out, nil
}

func (e stubEmbedding) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, e.dim), nil
}
func (e stubEmbedding) Dim() int      { return e.dim }
func (e stubEmbedding) Model() string { return "stub" }

// okEmbeddingRegistry 任何名字都返回同一个桩实现。
type okEmbeddingRegistry struct{ e interfaces.IEmbedding }

func (r okEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) { return r.e, nil }

// stubIDService 固定从 100 开始发号，便于断言。
type stubIDService struct{ next int64 }

func (s *stubIDService) NextID() string   { return "x" }
func (s *stubIDService) NextIntID() int64 { s.next += 100; return s.next }

// ─── 用例 ────────────────────────────────────────────────────────────────────

// 首次导入：库里没有这一行 → 分配新 id、写 ES、清差集、新建文档行并 +1 doc_count。
func TestEmbedAndStore_FirstImport(t *testing.T) {
	kb := &entity.KnowledgeBase{ID: 7, No: "kb-1", Account: "demo", BizTag: "sales"}
	docRepo := &stubDocRepo{existing: nil}
	store := &stubStore{}
	kbRepo := &stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}}
	tm := &stubTm{}

	svc := NewService(kbRepo, docRepo, store,
		okEmbeddingRegistry{e: stubEmbedding{dim: 4}},
		stubParserRegistry{}, // parsers：这两个用例走不到解析，给个空壳
		service.NewSplitter(), &stubIDService{}, tm)

	out, err := svc.Ingest(context.Background(), &dto.DocIngestDTO{
		Account: "demo", KBNo: "kb-1", DocName: "a.md",
		Format: "text", Content: "第一段正文。\n\n第二段正文。",
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.Created != true {
		t.Errorf("首次导入 Created 应为 true，得到 %v", out.Created)
	}
	if out.DocID != 100 {
		t.Errorf("首次导入应分配新 id（桩子发 100），得到 %d", out.DocID)
	}
	if want := []string{"save", "delete_except"}; strings.Join(store.calls, ",") != strings.Join(want, ",") {
		t.Errorf("ES 侧顺序应为 %v，得到 %v", want, store.calls)
	}
	if store.refreshHit {
		t.Error("首次导入且 refresh=false 时不该刷索引（没有旧切片要清）")
	}
	if len(store.savedDocs) == 0 || store.savedDocs[0].DocID != 100 {
		t.Errorf("切片应挂在 docID=100 上，得到 %#v", store.savedDocs)
	}
	if docRepo.saved == nil || docRepo.saved.ID != 100 {
		t.Errorf("文档行应落在 docID=100 上，得到 %#v", docRepo.saved)
	}
	if kbRepo.docDelta != 1 {
		t.Errorf("新建文档行应 +1 doc_count，得到 %d", kbRepo.docDelta)
	}
	if tm.calls != 1 {
		t.Errorf("第三次写入必须包在事务里，Transaction 调用次数 %d", tm.calls)
	}
}

// Review Focus 第 1 条：同名重导入必须**沿用库里那一行的 id**。
// id 一换就双向静默失效——新切片成孤儿、DeleteExcept 删不到旧切片。
func TestEmbedAndStore_Reimport_KeepsDocID(t *testing.T) {
	kb := &entity.KnowledgeBase{ID: 7, No: "kb-1", Account: "demo"}
	docRepo := &stubDocRepo{existing: &entity.KnowledgeDoc{ID: 42, KBID: 7, Name: "a.md", ChunkCount: 2}}
	store := &stubStore{}
	kbRepo := &stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}}

	svc := NewService(kbRepo, docRepo, store,
		okEmbeddingRegistry{e: stubEmbedding{dim: 4}},
		stubParserRegistry{}, // parsers：这两个用例走不到解析，给个空壳
		service.NewSplitter(), &stubIDService{}, &stubTm{})

	out, err := svc.Ingest(context.Background(), &dto.DocIngestDTO{
		Account: "demo", KBNo: "kb-1", DocName: "a.md",
		Format: "text", Content: "改过的正文",
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.DocID != 42 {
		t.Errorf("同名重导入必须沿用 42，得到 %d（换了 id 就删不掉旧切片）", out.DocID)
	}
	if docRepo.saved.ID != 42 {
		t.Errorf("文档行也该用 42，得到 %d", docRepo.saved.ID)
	}
	if !store.refreshHit {
		t.Error("重导入必刷索引：DeleteExcept 只作用在已刷新的段上")
	}
	if store.calls[0] != "save" || store.calls[1] != "refresh" {
		t.Errorf("顺序应为 先写后刷，得到 %v", store.calls)
	}
	if kbRepo.docDelta != 0 {
		t.Errorf("重导入不是新建，不该动 doc_count，得到 %d", kbRepo.docDelta)
	}
}

// countingEmbeddingRegistry 记下 Get 被调了几次、用的什么名字。
type countingEmbeddingRegistry struct {
	e     interfaces.IEmbedding
	calls int
	name  string
}

func (r *countingEmbeddingRegistry) Get(name string) (interfaces.IEmbedding, error) {
	r.calls++
	r.name = name
	return r.e, nil
}

// 向量实现是逐请求取的：两次导入各取一次，且用的是请求给的名字。
// 存回 Service 的字段上就是单例数据竞争（见 ingestDoc 的注释）。
func TestIngest_ResolvesEmbedderPerRequest(t *testing.T) {
	kb := &entity.KnowledgeBase{ID: 7, No: "kb-1", Account: "demo"}
	reg := &countingEmbeddingRegistry{e: stubEmbedding{dim: 4}}
	svc := NewService(
		&stubKBRepoDelta{stubKBRepo: stubKBRepo{kb: kb}},
		&stubDocRepo{}, &stubStore{},
		reg,
		stubParserRegistry{},
		service.NewSplitter(), &stubIDService{}, &stubTm{},
	)

	for _, model := range []string{"m1", "m2"} {
		if _, err := svc.Ingest(context.Background(), &dto.DocIngestDTO{
			Account: "demo", KBNo: "kb-1", DocName: "a.md",
			Format: "text", Content: "正文。", Model: model,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if reg.calls != 2 {
		t.Errorf("两次导入应各取一次实现（逐请求），得到 %d 次", reg.calls)
	}
	if reg.name != "m2" {
		t.Errorf("取实现要用请求给的名字，得到 %q", reg.name)
	}
}
