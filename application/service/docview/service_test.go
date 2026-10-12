package docview

import (
	"context"
	"errors"
	"testing"

	"github.com/PycMono/FastRAG/common/constants"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/repository"
)

// ─── 桩子 ────────────────────────────────────────────────────────────────────
//
// 一律内嵌 nil 接口：没显式实现的方法一旦被调到就 panic，
// 正好说明用例走到了不该走的地方。

type stubKBRepo struct {
	repository.IKnowledgeRepo
	byNo    *entity.Knowledge
	byID    *entity.Knowledge
	byNoErr error
	byIDErr error
}

func (s *stubKBRepo) FindByNo(ctx context.Context, no, account string) (*entity.Knowledge, error) {
	if s.byNoErr != nil {
		return nil, s.byNoErr
	}
	return s.byNo, nil
}

func (s *stubKBRepo) FindByID(ctx context.Context, id uint64, account string) (*entity.Knowledge, error) {
	if s.byIDErr != nil {
		return nil, s.byIDErr
	}
	return s.byID, nil
}

type stubDocRepo struct {
	repository.IKnowledgeDocRepo
	byID   *entity.KnowledgeDoc
	kbDocs entity.KnowledgeDocs
	total  int64
}

// FindByID 是 liveDoc 唯一的入口。**不要**在这里补 FindByIDs——
// docview 不该走存活校验那条批量路径（它要正文，批量路径刻意不带 content）。
// 没实现的方法落到内嵌的 nil 接口上会 panic，正好抓住「走到了不该走的地方」。
func (s *stubDocRepo) FindByID(ctx context.Context, id uint64) (*entity.KnowledgeDoc, error) {
	return s.byID, nil
}

func (s *stubDocRepo) ListByKB(ctx context.Context, kbID uint64, offset, limit int) (entity.KnowledgeDocs, int64, error) {
	return s.kbDocs, s.total, nil
}

type stubStore struct {
	repository.IVectorStore
	hits      []repository.VectorHit
	total     int64
	listCalls int
	gotFilter repository.VectorFilter
	gotOffset int
	gotLimit  int
}

func (s *stubStore) ListByDoc(
	ctx context.Context, f repository.VectorFilter, offset, limit int,
) ([]repository.VectorHit, int64, error) {
	s.listCalls++
	s.gotFilter, s.gotOffset, s.gotLimit = f, offset, limit
	return s.hits, s.total, nil
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("期望报错，却拿到了 nil")
	}
	var ce apperrors.CodeError
	if !errors.As(err, &ce) {
		t.Fatalf("不是带错误码的错误：%v", err)
	}
	return ce.Code()
}

// ─── 列表 ────────────────────────────────────────────────────────────────────

func TestList_EmptyKBReturnsEmptyListNotNil(t *testing.T) {
	svc := NewService(
		&stubKBRepo{byNo: &entity.Knowledge{ID: 7, Account: "demo"}},
		&stubDocRepo{kbDocs: nil, total: 0},
		&stubStore{})

	res, err := svc.List(context.Background(), &dto.DocListQuery{Account: "demo", KBNo: "demo-kb"})
	if err != nil {
		t.Fatalf("List 报错：%v", err)
	}
	if res.Total != 0 {
		t.Errorf("total = %d，期望 0", res.Total)
	}
	if res.List == nil {
		t.Error("list 是 nil —— 会被序列化成 JSON null，前端 .map 会炸")
	}
	if len(res.List) != 0 {
		t.Errorf("list 有 %d 项，期望空", len(res.List))
	}
}

func TestList_KBOfAnotherAccountPropagates10101(t *testing.T) {
	// 仓储层把「库不存在」与「account 对不上」折叠成同一个错，服务层原样透传
	svc := NewService(
		&stubKBRepo{byNoErr: apperrors.ErrKnowledgeBaseNotFound},
		&stubDocRepo{}, &stubStore{})

	_, err := svc.List(context.Background(), &dto.DocListQuery{Account: "other", KBNo: "demo-kb"})
	if got := codeOf(t, err); got != apperrors.CodeKnowledgeBaseNotFound {
		t.Errorf("错误码 = %d，期望 %d", got, apperrors.CodeKnowledgeBaseNotFound)
	}
}

// ─── 详情 ────────────────────────────────────────────────────────────────────

func TestDetail_UnknownDocReturns10501(t *testing.T) {
	svc := NewService(&stubKBRepo{}, &stubDocRepo{byID: nil}, &stubStore{})

	_, err := svc.Detail(context.Background(), &dto.DocDetailQuery{Account: "demo", DocID: 42})
	if got := codeOf(t, err); got != apperrors.CodeDocNotFound {
		t.Errorf("错误码 = %d，期望 %d", got, apperrors.CodeDocNotFound)
	}
}

func TestDetail_DocOfAnotherAccountReturnsTheSame10501(t *testing.T) {
	// 行存在但 account 对不上，必须与「不存在」**同一个码**：
	// 分开就送出一个可枚举的越权探测接口
	svc := NewService(&stubKBRepo{},
		&stubDocRepo{byID: &entity.KnowledgeDoc{ID: 42, Account: "someone-else"}},
		&stubStore{})

	_, err := svc.Detail(context.Background(), &dto.DocDetailQuery{Account: "demo", DocID: 42})
	if got := codeOf(t, err); got != apperrors.CodeDocNotFound {
		t.Errorf("错误码 = %d，期望 %d（与「不存在」同一个码）", got, apperrors.CodeDocNotFound)
	}
}

func TestDetail_ChunksFormatDocHasNoContent(t *testing.T) {
	svc := NewService(
		&stubKBRepo{byID: &entity.Knowledge{ID: 7, Account: "demo", SourceURL: "https://example.com/a.pdf"}},
		&stubDocRepo{byID: &entity.KnowledgeDoc{
			ID: 42, KBID: 7, Account: "demo", Name: "a.md", Title: "a", Content: "", ChunkCount: 3,
		}},
		&stubStore{})

	res, err := svc.Detail(context.Background(), &dto.DocDetailQuery{Account: "demo", DocID: 42})
	if err != nil {
		t.Fatalf("Detail 报错：%v", err)
	}
	if res.HasContent {
		t.Error("has_content = true，但 content 是空串")
	}
	if res.Content != "" {
		t.Errorf("content = %q，期望空串", res.Content)
	}
	// source_url 来自 KB 行（库级）——文档表里根本没有这一列
	if res.SourceURL != "https://example.com/a.pdf" {
		t.Errorf("source_url = %q，期望取自 KB 行", res.SourceURL)
	}
	if res.KBID != 7 {
		t.Errorf("kb_id = %d，期望 7", res.KBID)
	}
}

// ─── 切片 ────────────────────────────────────────────────────────────────────

func TestChunks_ZeroTotalIsNotAnError(t *testing.T) {
	// 刚导入的文档：原文能看、切片还没进可检索视图（两档延迟）。total=0 合法
	st := &stubStore{hits: nil, total: 0}
	svc := NewService(&stubKBRepo{},
		&stubDocRepo{byID: &entity.KnowledgeDoc{ID: 42, Account: "demo"}}, st)

	res, err := svc.Chunks(context.Background(), &dto.ChunkListQuery{Account: "demo", DocID: 42})
	if err != nil {
		t.Fatalf("total=0 被当成错误了：%v", err)
	}
	if res.Total != 0 || len(res.Items) != 0 {
		t.Errorf("total/items = %d/%d，期望 0/0", res.Total, len(res.Items))
	}
	if res.Items == nil {
		t.Error("items 是 nil —— 会被序列化成 JSON null")
	}
	// 枚举条件必须同时带 account 与 doc_id
	if st.gotFilter.Account != "demo" || st.gotFilter.DocID != 42 {
		t.Errorf("枚举条件 = %+v，期望 account=demo doc_id=42", st.gotFilter)
	}
}

func TestChunks_LimitIsClampedNotRejected(t *testing.T) {
	st := &stubStore{}
	svc := NewService(&stubKBRepo{},
		&stubDocRepo{byID: &entity.KnowledgeDoc{ID: 42, Account: "demo"}}, st)

	_, err := svc.Chunks(context.Background(),
		&dto.ChunkListQuery{Account: "demo", DocID: 42, Limit: 9999})
	if err != nil {
		t.Fatalf("超限的 limit 被报成错误了：%v", err)
	}
	if st.gotLimit != constants.MaxChunkListLimit {
		t.Errorf("传给存储层的 limit = %d，期望钳到 %d", st.gotLimit, constants.MaxChunkListLimit)
	}
}

func TestChunks_NegativeOffsetBecomesZero(t *testing.T) {
	st := &stubStore{}
	svc := NewService(&stubKBRepo{},
		&stubDocRepo{byID: &entity.KnowledgeDoc{ID: 42, Account: "demo"}}, st)

	_, err := svc.Chunks(context.Background(),
		&dto.ChunkListQuery{Account: "demo", DocID: 42, Offset: -5})
	if err != nil {
		t.Fatalf("报错了：%v", err)
	}
	if st.gotOffset != 0 {
		t.Errorf("传给存储层的 offset = %d，期望归零", st.gotOffset)
	}
}

func TestChunks_TenantCheckedBeforeEnumerating(t *testing.T) {
	st := &stubStore{}
	svc := NewService(&stubKBRepo{},
		&stubDocRepo{byID: &entity.KnowledgeDoc{ID: 42, Account: "someone-else"}}, st)

	_, err := svc.Chunks(context.Background(), &dto.ChunkListQuery{Account: "demo", DocID: 42})
	if got := codeOf(t, err); got != apperrors.CodeDocNotFound {
		t.Errorf("错误码 = %d，期望 %d", got, apperrors.CodeDocNotFound)
	}
	if st.listCalls != 0 {
		t.Error("文档不属于该租户，却已经去 ES 枚举了 —— 租户校验必须在前")
	}
}

func TestChunks_EmptyDocIDListIsNotFound(t *testing.T) {
	// FindByID 返回 nil（硬删之后的幽灵文档）→ 与「不存在」同一个码
	svc := NewService(&stubKBRepo{}, &stubDocRepo{byID: nil}, &stubStore{})
	_, err := svc.Chunks(context.Background(), &dto.ChunkListQuery{Account: "demo", DocID: 42})
	if got := codeOf(t, err); got != apperrors.CodeDocNotFound {
		t.Errorf("错误码 = %d，期望 %d", got, apperrors.CodeDocNotFound)
	}
}
