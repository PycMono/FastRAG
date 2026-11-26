package evaluation

import (
	"context"
	"strings"
	"testing"
)

func TestImportPreparesOriginalTextWithoutCallingModel(t *testing.T) {
	repo := &reviewMemory{}
	tasks := NewService(nil, nil, nil, nil, Options{Enabled: true})
	service := NewReviewService(tasks, nil, repo)
	service.ConfigureAutomaticGeneration(true, 50)
	content := strings.Repeat("这是完整原文中的事实和条件。\n\n", 1000)
	if err := service.Imported(context.Background(), "tenant", "kb", "1", "book.md", content); err != nil {
		t.Fatal(err)
	}
	if len(repo.draft.Items) == 0 || !repo.draft.AutoGenerate || repo.draft.GenerationState != "pending" {
		t.Fatalf("未持久化待生成草稿: %+v", repo.draft)
	}
	for _, candidate := range repo.draft.Items {
		if candidate.SourceType != "original_markdown" || candidate.ChunkID != "" || candidate.Question != "" {
			t.Fatal("自动导入错误地使用切片或直接通过题目")
		}
	}
}
