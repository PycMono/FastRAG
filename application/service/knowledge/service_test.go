package knowledge

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
)

// ─── 测试替身 ────────────────────────────────────────────────────────────────

// fakeRepo 内存仓储
type fakeRepo struct {
	items     map[string]*knowledgeentity.KnowledgeBase
	createErr error
	updateErr error
	deleteErr error
	listErr   error

	// lastPage/lastPageSize 记录最后一次 ListByUserID 收到的分页参数。
	// 断言必须落在仓储边界：返回值里的 page/pageSize 由 vo.NewPageResult 原样回显，
	// 而它对非法值另有兜底，只看返回值发现不了 service 的归一化被删掉。
	lastPage     int
	lastPageSize int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{items: make(map[string]*knowledgeentity.KnowledgeBase)}
}

func (r *fakeRepo) Create(ctx context.Context, kb *knowledgeentity.KnowledgeBase) error {
	if r.createErr != nil {
		return r.createErr
	}
	cp := *kb
	r.items[kb.ID] = &cp
	return nil
}

func (r *fakeRepo) GetByIDAndUserID(ctx context.Context, id, userID string) (*knowledgeentity.KnowledgeBase, error) {
	kb, ok := r.items[id]
	if !ok || kb.UserID != userID {
		return nil, apperrors.ErrKnowledgeBaseNotFound
	}
	cp := *kb
	return &cp, nil
}

func (r *fakeRepo) ListByUserID(ctx context.Context, userID string, page, pageSize int) (int64, []*knowledgeentity.KnowledgeBase, error) {
	if r.listErr != nil {
		return 0, nil, r.listErr
	}
	r.lastPage, r.lastPageSize = page, pageSize
	var all []*knowledgeentity.KnowledgeBase
	for _, kb := range r.items {
		if kb.UserID == userID {
			cp := *kb
			all = append(all, &cp)
		}
	}
	total := int64(len(all))
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	if start > len(all) {
		start = len(all)
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return total, all[start:end], nil
}

func (r *fakeRepo) Update(ctx context.Context, kb *knowledgeentity.KnowledgeBase) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	cp := *kb
	r.items[kb.ID] = &cp
	return nil
}

func (r *fakeRepo) DeleteByIDAndUserID(ctx context.Context, id, userID string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	kb, ok := r.items[id]
	if !ok || kb.UserID != userID {
		return nil
	}
	delete(r.items, id)
	return nil
}

// fakeRetriever 检索替身
type fakeRetriever struct {
	chunks []*knowledgerepo.RetrievedChunk
	err    error

	// lastQuery 记录最后一次收到的检索入参。
	// 只断言返回的 chunks 无法发现「知识库 ID 传错」「查询词丢失」这类缺陷——
	// 而那正是 §6.4 向量库扩展点最容易出、也最难看见的错。
	lastQuery knowledgerepo.RetrieveQuery
}

func (f *fakeRetriever) Retrieve(ctx context.Context, q knowledgerepo.RetrieveQuery) ([]*knowledgerepo.RetrievedChunk, error) {
	f.lastQuery = q
	return f.chunks, f.err
}

// fakeIDService 自增 ID 生成器
type fakeIDService struct{ n int }

func (f *fakeIDService) NextID() string { f.n++; return "id-" + strconv.Itoa(f.n) }

func (f *fakeIDService) NextIntID() int64 { f.n++; return int64(f.n) }

func newTestService(repo knowledgerepo.IKnowledgeBaseRepo, retriever knowledgerepo.IKnowledgeRetriever) *Service {
	return NewService(repo, retriever, &fakeIDService{})
}

// ─── Create ─────────────────────────────────────────────────────────────────

func TestService_Create(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	got, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a", Description: "d"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.ID == "" {
		t.Fatal("Create() should assign an ID")
	}
	if got.UserID != "u1" {
		t.Fatalf("UserID = %q, want %q", got.UserID, "u1")
	}
	if got.Status != knowledgeentity.StatusEnabled {
		t.Fatalf("Status = %d, want %d", got.Status, knowledgeentity.StatusEnabled)
	}
	if _, ok := repo.items[got.ID]; !ok {
		t.Fatal("Create() should persist the record")
	}
}

func TestService_Create_WrapsRepoError(t *testing.T) {
	repo := newFakeRepo()
	repo.createErr = errors.New("connection refused")
	svc := newTestService(repo, &fakeRetriever{})

	_, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})

	sys, ok := apperrors.AsSysError(err)
	if !ok {
		t.Fatalf("expected a SysError, got %v", err)
	}
	if sys.Code() != apperrors.CodeKnowledgeBaseCreateFail {
		t.Fatalf("Code() = %d, want %d", sys.Code(), apperrors.CodeKnowledgeBaseCreateFail)
	}
}

// ─── Get ────────────────────────────────────────────────────────────────────

func TestService_Get_NotFound(t *testing.T) {
	svc := newTestService(newFakeRepo(), &fakeRetriever{})

	_, err := svc.Get(context.Background(), "u1", "missing")

	if !errors.Is(err, apperrors.ErrKnowledgeBaseNotFound) {
		t.Fatalf("Get() error = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

func TestService_Get_OtherUserCannotRead(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = svc.Get(context.Background(), "u2", created.ID)

	if !errors.Is(err, apperrors.ErrKnowledgeBaseNotFound) {
		t.Fatalf("Get() by another user error = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

// ─── List ───────────────────────────────────────────────────────────────────

func TestService_List_Paginates(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	for _, name := range []string{"a", "b", "c"} {
		if _, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: name}); err != nil {
			t.Fatalf("Create(%s) error = %v", name, err)
		}
	}

	got, err := svc.List(context.Background(), "u1", dto.ListKnowledgeBaseQuery{
		PageQuery: dto.PageQuery{Page: 1, PageSize: 2},
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got.Total != 3 {
		t.Fatalf("Total = %d, want 3", got.Total)
	}
	if len(got.List) != 2 {
		t.Fatalf("len(List) = %d, want 2", len(got.List))
	}
	if got.Pages != 2 {
		t.Fatalf("Pages = %d, want 2", got.Pages)
	}
}

// service 被直接调用（未经 HTTP 绑定层）时，零值与越界分页必须归一化后再落到仓储。
//
// 注意这不是 HTTP 路径的覆盖：`PageQuery` 上的 gte/lte 绑定标签已经在绑定层把
// ?page=0&page_size=0 拒成 400，所以本条守的是 service 自身的归一化逻辑。
func TestService_List_NormalizesZeroPageQueryBeforeRepo(t *testing.T) {
	for _, tc := range []struct {
		name             string
		in               dto.PageQuery
		wantPage, wantPS int
	}{
		{"零值取默认", dto.PageQuery{Page: 0, PageSize: 0}, 1, 10},
		{"越界取上限", dto.PageQuery{Page: 2, PageSize: 1000}, 2, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			svc := newTestService(repo, &fakeRetriever{})

			got, err := svc.List(context.Background(), "u1", dto.ListKnowledgeBaseQuery{PageQuery: tc.in})
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if repo.lastPage != tc.wantPage || repo.lastPageSize != tc.wantPS {
				t.Fatalf("repo received page/pageSize = %d/%d, want %d/%d",
					repo.lastPage, repo.lastPageSize, tc.wantPage, tc.wantPS)
			}
			if got.Page != tc.wantPage || got.PageSize != tc.wantPS {
				t.Fatalf("Page/PageSize = %d/%d, want %d/%d", got.Page, got.PageSize, tc.wantPage, tc.wantPS)
			}
		})
	}
}

// ─── Update ─────────────────────────────────────────────────────────────────

func TestService_Update_OnlyProvidedFields(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{
		Name: "old", Description: "keep-me", EmbeddingModel: "text-embedding-3",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	newName := "new"
	if err := svc.Update(context.Background(), "u1", created.ID, dto.UpdateKnowledgeBaseDTO{Name: &newName}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := svc.Get(context.Background(), "u1", created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "new" {
		t.Fatalf("Name = %q, want %q", got.Name, "new")
	}
	if got.Description != "keep-me" {
		t.Fatalf("Description = %q, want %q (unset field must be preserved)", got.Description, "keep-me")
	}
	if got.EmbeddingModel != "text-embedding-3" {
		t.Fatalf("EmbeddingModel = %q, want it preserved", got.EmbeddingModel)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	svc := newTestService(newFakeRepo(), &fakeRetriever{})

	name := "x"
	err := svc.Update(context.Background(), "u1", "missing", dto.UpdateKnowledgeBaseDTO{Name: &name})

	if !errors.Is(err, apperrors.ErrKnowledgeBaseNotFound) {
		t.Fatalf("Update() error = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

func TestService_Update_WrapsRepoError(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	repo.updateErr = errors.New("deadlock")

	name := "x"
	err = svc.Update(context.Background(), "u1", created.ID, dto.UpdateKnowledgeBaseDTO{Name: &name})

	sys, ok := apperrors.AsSysError(err)
	if !ok {
		t.Fatalf("expected a SysError, got %v", err)
	}
	if sys.Code() != apperrors.CodeKnowledgeBaseUpdateFail {
		t.Fatalf("Code() = %d, want %d", sys.Code(), apperrors.CodeKnowledgeBaseUpdateFail)
	}
}

// ─── Delete ─────────────────────────────────────────────────────────────────

func TestService_Delete(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := svc.Delete(context.Background(), "u1", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, ok := repo.items[created.ID]; ok {
		t.Fatal("Delete() should remove the record")
	}
}

func TestService_Delete_WrapsRepoError(t *testing.T) {
	repo := newFakeRepo()
	repo.deleteErr = errors.New("lock wait timeout")
	svc := newTestService(repo, &fakeRetriever{})

	err := svc.Delete(context.Background(), "u1", "anything")

	sys, ok := apperrors.AsSysError(err)
	if !ok {
		t.Fatalf("expected a SysError, got %v", err)
	}
	if sys.Code() != apperrors.CodeKnowledgeBaseDeleteFail {
		t.Fatalf("Code() = %d, want %d", sys.Code(), apperrors.CodeKnowledgeBaseDeleteFail)
	}
}

// ─── Search（检索扩展点）─────────────────────────────────────────────────────

func TestService_Search_ReturnsNotImplementedFromStub(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{err: apperrors.ErrKnowledgeRetrievalNotImpl})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = svc.Search(context.Background(), "u1", created.ID, dto.SearchKnowledgeBaseDTO{Query: "hello"})

	if !errors.Is(err, apperrors.ErrKnowledgeRetrievalNotImpl) {
		t.Fatalf("Search() error = %v, want ErrKnowledgeRetrievalNotImpl", err)
	}
}

func TestService_Search_RejectsUnknownKnowledgeBase(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{
		chunks: []*knowledgerepo.RetrievedChunk{{DocID: "d1", Content: "c"}},
	})

	_, err := svc.Search(context.Background(), "u1", "missing", dto.SearchKnowledgeBaseDTO{Query: "hello"})

	if !errors.Is(err, apperrors.ErrKnowledgeBaseNotFound) {
		t.Fatalf("Search() error = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

func TestService_Search_MapsChunksToVO(t *testing.T) {
	repo := newFakeRepo()
	retriever := &fakeRetriever{
		chunks: []*knowledgerepo.RetrievedChunk{{DocID: "d1", ChunkIndex: 2, Content: "body", Score: 0.87}},
	}
	svc := newTestService(repo, retriever)

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := svc.Search(context.Background(), "u1", created.ID, dto.SearchKnowledgeBaseDTO{Query: "hello", TopK: 3})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(got))
	}
	if got[0].DocID != "d1" || got[0].ChunkIndex != 2 || got[0].Content != "body" || got[0].Score != 0.87 {
		t.Fatalf("unexpected chunk mapping: %+v", got[0])
	}

	// 检索器收到的入参必须原样来自本次调用：知识库 ID 传错、查询词丢失都不会报错，
	// 只会在接入真实向量库后表现为「检索结果不对」。§6.4 的扩展点靠这个断言兜住。
	if retriever.lastQuery.KnowledgeBaseID != created.ID {
		t.Fatalf("RetrieveQuery.KnowledgeBaseID = %q, want %q", retriever.lastQuery.KnowledgeBaseID, created.ID)
	}
	if retriever.lastQuery.Query != "hello" {
		t.Fatalf("RetrieveQuery.Query = %q, want %q", retriever.lastQuery.Query, "hello")
	}
	if retriever.lastQuery.TopK != 3 {
		t.Fatalf("RetrieveQuery.TopK = %d, want 3", retriever.lastQuery.TopK)
	}
}
