package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

type reviewMemory struct {
	draft     entity.EvalDraft
	published entity.EvalDataset
	baseline  string
}

func (m *reviewMemory) InvalidateDocument(context.Context, string, string, string) error {
	return nil
}

func (m *reviewMemory) ListDrafts(context.Context) ([]entity.EvalDraft, error) {
	return []entity.EvalDraft{m.draft}, nil
}

func (m *reviewMemory) GetDraft(context.Context, string) (entity.EvalDraft, error) {
	return m.draft, nil
}

func (m *reviewMemory) SaveDraft(_ context.Context, draft entity.EvalDraft, version int) error {
	draft.Version = version + 1
	m.draft = draft
	return nil
}

func (m *reviewMemory) DeleteDraft(context.Context, string) error {
	m.draft = entity.EvalDraft{}
	return nil
}

func (m *reviewMemory) Publish(_ context.Context, dataset entity.EvalDataset) error {
	m.published = dataset
	return nil
}

func (m *reviewMemory) SetBaseline(_ context.Context, dataset, runID string) error {
	m.baseline = runID
	return nil
}

func (m *reviewMemory) Baseline(context.Context, string) (string, error) {
	return m.baseline, nil
}

func (m *reviewMemory) SaveComparison(context.Context, entity.EvalComparison) error {
	return nil
}

type reviewAuthor struct {
	sources []entity.EvalSourceChunk
}

func (a reviewAuthor) Sources(context.Context, string, string, entity.EvalSamplePolicy) ([]entity.EvalSourceChunk, error) {
	return a.sources, nil
}

func (a reviewAuthor) Generate(context.Context, string) (entity.EvalGeneratedQuestion, error) {
	return entity.EvalGeneratedQuestion{Question: "问题", Reference: "参考"}, nil
}

func TestReviewRejectsUnapprovedAndChangedSources(t *testing.T) {
	ctx := context.Background()
	source := "实际原文"
	hash := sha256.Sum256([]byte(source))
	repo := &reviewMemory{draft: entity.EvalDraft{
		ID: "draft-test", Version: 1, Tenant: "t", KBNo: "kb",
		Items: []entity.EvalCandidate{
			{ID: "q1", DocID: "1", ChunkID: "c1", Question: "问题1", Reference: "参考", SourceHash: hex.EncodeToString(hash[:]), Status: "draft"},
			{ID: "q2", DocID: "1", ChunkID: "c2", Question: "问题2", Reference: "参考", SourceHash: hex.EncodeToString(hash[:]), Status: "draft"},
			{ID: "q3", DocID: "1", ChunkID: "c3", Question: "问题3", Reference: "参考", SourceHash: hex.EncodeToString(hash[:]), Status: "draft"},
		},
	}}
	author := reviewAuthor{sources: []entity.EvalSourceChunk{
		{ChunkID: "c1", Content: source}, {ChunkID: "c2", Content: source}, {ChunkID: "c3", Content: source},
	}}
	tasks := NewService(nil, nil, nil, nil, Options{Enabled: true})
	service := NewReviewService(tasks, author, repo)
	profile := entity.EvalProfile{Name: "hybrid", Limit: 5, RetrieveCount: 10, DenseWeight: 0.5}
	if _, err := service.Publish(ctx, "draft-test", 1, profile); err == nil {
		t.Fatal("未复核题被发布")
	}
	for index := range repo.draft.Items {
		repo.draft.Items[index].Status = "approved"
	}
	if _, err := service.Publish(ctx, "draft-test", 1, profile); err != nil {
		t.Fatal(err)
	}
	if !repo.published.Manifest.Reviewed || len(repo.published.Cases) != 3 {
		t.Fatal("未保存复核标记")
	}
	service.author = reviewAuthor{}
	if _, err := service.Publish(ctx, "draft-test", 1, profile); err == nil {
		t.Fatal("来源缺失仍允许发布")
	}
}

func TestBaselineRejectsUnreviewedAndIncomplete(t *testing.T) {
	ctx := context.Background()
	runs := &memoryRuns{values: map[string]entity.EvalRun{
		"demo":   {ID: "demo", Status: "succeeded", Purpose: "regression"},
		"failed": {ID: "failed", Status: "partial", Purpose: "regression", Dataset: entity.EvalDataset{Manifest: entity.EvalManifest{Reviewed: true}}},
	}, changed: make(chan entity.EvalRun, 10)}
	tasks := NewService(nil, runs, nil, nil, Options{Enabled: true})
	service := NewReviewService(tasks, nil, &reviewMemory{})
	for _, id := range []string{"demo", "failed"} {
		if err := service.SetBaseline(ctx, id); err == nil {
			t.Fatalf("不合格任务成为基线: %s", id)
		}
	}
}

type failingAuthor struct{ reviewAuthor }

func (failingAuthor) Generate(context.Context, string) (entity.EvalGeneratedQuestion, error) {
	return entity.EvalGeneratedQuestion{}, fmt.Errorf("model down")
}

func TestBackgroundGenerationAdvancesOneQuestionAndStops(t *testing.T) {
	ctx := context.Background()
	repo := &reviewMemory{draft: entity.EvalDraft{
		ID: "draft-test", Version: 1, Tenant: "t", KBNo: "kb",
		Items: []entity.EvalCandidate{
			{ID: "q1", Source: "原文一", Status: "draft"},
			{ID: "q2", Source: "原文二", Status: "draft"},
		},
	}}
	service := NewReviewService(NewService(nil, nil, nil, nil, Options{Enabled: true}), reviewAuthor{}, repo)
	draft, err := service.GenerateAll(ctx, "draft-test", 1)
	if err != nil || !draft.AutoGenerate || draft.GenerationState != "pending" {
		t.Fatalf("未进入后台生成: %+v err=%v", draft, err)
	}
	service.generateOne(ctx, draft.ID)
	if repo.draft.Items[0].Question == "" || repo.draft.Items[1].Question != "" {
		t.Fatal("每个周期只应生成一题")
	}
	service.generateOne(ctx, draft.ID)
	if repo.draft.Items[1].Question == "" {
		t.Fatal("第二题未生成")
	}
	service.generateOne(ctx, draft.ID)
	if repo.draft.GenerationState != "completed" {
		t.Fatalf("没有待生成题时应完成: %s", repo.draft.GenerationState)
	}

	repo.draft.GenerationState = "pending"
	repo.draft.Items[0].Question = ""
	if _, err := service.PauseGeneration(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	service.generateOne(ctx, draft.ID)
	if repo.draft.Items[0].Question != "" {
		t.Fatal("暂停后仍继续生成")
	}
}

func TestBackgroundGenerationStopsAfterRepeatedFailures(t *testing.T) {
	ctx := context.Background()
	repo := &reviewMemory{draft: entity.EvalDraft{
		ID: "draft-test", Version: 1, Tenant: "t", KBNo: "kb",
		Items: []entity.EvalCandidate{{ID: "q1", Source: "原文", Status: "draft"}},
	}}
	service := NewReviewService(NewService(nil, nil, nil, nil, Options{Enabled: true}), failingAuthor{}, repo)
	if _, err := service.GenerateAll(ctx, "draft-test", 1); err != nil {
		t.Fatal(err)
	}
	service.generateOne(ctx, "draft-test")
	if repo.draft.GenerationState != "pending" || repo.draft.Items[0].GenerationAttempts != 1 {
		t.Fatalf("单次失败不应停止整批: %+v", repo.draft)
	}
	service.generateOne(ctx, "draft-test")
	service.generateOne(ctx, "draft-test")
	service.generateOne(ctx, "draft-test")
	if repo.draft.GenerationState != "failed" || repo.draft.GenerationError == "" {
		t.Fatalf("连续失败应停止: %+v", repo.draft)
	}
}

func TestGenerateAllMarksCompletedWhenNothingMissing(t *testing.T) {
	repo := &reviewMemory{draft: entity.EvalDraft{
		ID: "draft-test", Version: 1, Tenant: "t", KBNo: "kb", GenerationState: "failed",
		Items: []entity.EvalCandidate{
			{ID: "q1", Status: "draft", Question: "问题", Reference: "参考"},
		},
	}}
	service := NewReviewService(NewService(nil, nil, nil, nil, Options{Enabled: true}), reviewAuthor{}, repo)
	draft, err := service.GenerateAll(context.Background(), "draft-test", 1)
	if err != nil || draft.GenerationState != "completed" {
		t.Fatalf("没有待生成题时应视为完成: %+v err=%v", draft, err)
	}
}

func TestQuickSelectionUsesOnlyApprovedAndBalancesBooks(t *testing.T) {
	items := []entity.EvalCandidate{}
	for index := 0; index < 20; index++ {
		book := "book-a"
		if index >= 10 {
			book = "book-b"
		}
		items = append(items, entity.EvalCandidate{
			ID: fmt.Sprint(index), DocID: book, Difficulty: "困难", Status: "approved",
		})
	}
	items = append(items, entity.EvalCandidate{ID: "unreviewed", Status: "draft"})
	selected, err := selectApprovedCandidates(items, 8)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, item := range selected {
		if item.Status != "approved" || seen[item.ID] {
			t.Fatal("选入未审核或重复题")
		}
		seen[item.ID] = true
		counts[item.DocID]++
	}
	if len(selected) != 8 || counts["book-a"] != 4 || counts["book-b"] != 4 {
		t.Fatalf("选取不均衡: %+v", counts)
	}
	if _, err := selectApprovedCandidates(items, 21); err == nil {
		t.Fatal("用未审核题补足")
	}
}

func TestComparePassFailAndInsufficient(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		value   float64
		version string
		verdict string
	}{
		{name: "pass", value: 0.9, version: "judge-v1", verdict: "pass"},
		{name: "regression", value: 0.5, version: "judge-v1", verdict: "fail"},
		{name: "judge-changed", value: 0.9, version: "judge-v2", verdict: "insufficient"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			makeRun := func(id string, value float64, judge string) entity.EvalRun {
				return entity.EvalRun{
					ID: id, Status: "succeeded", Purpose: "regression",
					Dataset:  entity.EvalDataset{Name: "reviewed", Manifest: entity.EvalManifest{Reviewed: true}},
					Metadata: map[string]any{"dataset_hash": "same"},
					Summary: map[string]entity.EvalSummary{
						"hybrid/ContextRecall": {Mean: &value, OK: 1},
					},
					Rows: []entity.EvalRow{{
						CaseID: "q1", RetrievalLatencyMS: 100,
						Score: &entity.EvalScoreOutput{
							Metadata: map[string]any{"evaluation_config_hash": judge},
							Metrics:  []entity.EvalMetric{{Name: "ContextRecall", Status: "ok", Value: &value}},
						},
					}},
				}
			}
			runs := &memoryRuns{values: map[string]entity.EvalRun{
				"baseline": makeRun("baseline", 0.9, "judge-v1"),
				"new":      makeRun("new", scenario.value, scenario.version),
			}}
			tasks := NewService(nil, runs, nil, nil, Options{Enabled: true})
			service := NewReviewService(tasks, nil, &reviewMemory{baseline: "baseline"})
			report, err := service.Compare(context.Background(), "new", entity.EvalPolicy{
				MaxRecallDrop: 0.05, MaxPrecisionDrop: 0.05, MaxLatencyRatio: 3,
			})
			if err != nil || report.Verdict != scenario.verdict {
				t.Fatalf("report=%+v err=%v", report, err)
			}
		})
	}
}
