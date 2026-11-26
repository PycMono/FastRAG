package persistence

import (
	"context"
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func TestReviewVersionAndImmutablePublication(t *testing.T) {
	ctx := context.Background()
	files := evalFiles(t)
	repo := &EvalReviews{Files: files}
	draft := entity.EvalDraft{ID: "draft-1"}
	if err := repo.SaveDraft(ctx, draft, 0); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveDraft(ctx, draft, 0); err == nil {
		t.Fatal("过期版本覆盖了草稿")
	}
	dataset := entity.EvalDataset{Name: "reviewed-v1", Cases: []entity.EvalCase{{ID: "q1"}}}
	if err := repo.Publish(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	if err := repo.Publish(ctx, dataset); err == nil {
		t.Fatal("覆盖已发布版本")
	}
	if err := repo.SetBaseline(ctx, dataset.Name, "run-1"); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.Baseline(ctx, dataset.Name); err != nil || id != "run-1" {
		t.Fatalf("基线读取错误: %s %v", id, err)
	}
}

func TestDocumentRemovalPreservesOtherSourcesAndRetiresDataset(t *testing.T) {
	ctx := context.Background()
	files := evalFiles(t)
	repo := &EvalReviews{Files: files}
	draft := entity.EvalDraft{
		ID: "draft-mixed", Tenant: "t", KBNo: "kb",
		Items: []entity.EvalCandidate{{ID: "q1", DocID: "1"}, {ID: "q2", DocID: "2"}},
	}
	if err := repo.SaveDraft(ctx, draft, 0); err != nil {
		t.Fatal(err)
	}
	dataset := entity.EvalDataset{
		Name:  "published-v1",
		Cases: []entity.EvalCase{{ID: "q1", Tenant: "t", KBNos: []string{"kb"}, ReferenceSources: []entity.EvalSource{{DocID: "1"}}}},
	}
	if err := repo.Publish(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	if err := repo.InvalidateDocument(ctx, "t", "kb", "1"); err != nil {
		t.Fatal(err)
	}
	remaining, err := repo.GetDraft(ctx, draft.ID)
	if err != nil || len(remaining.Items) != 1 || remaining.Items[0].DocID != "2" {
		t.Fatalf("误删其他文档的题: %+v %v", remaining, err)
	}
	if _, err := (&EvalDatasets{Files: files}).Load(ctx, dataset.Name); err == nil {
		t.Fatal("失效题集仍可运行")
	}
}
