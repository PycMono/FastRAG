package evaluation

import (
	"context"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
)

func TestRunProjectionDoesNotExposeSnapshot(t *testing.T) {
	run := entity.EvalRun{
		ID:       "run-1",
		Dataset:  fixture(),
		Metadata: map[string]any{"internal": "not-for-client"},
		Rows:     []entity.EvalRow{{Reference: "private reference"}},
	}
	result := runVO(run)
	if result.ID != run.ID {
		t.Fatal("任务投影丢失 ID")
	}
}

func TestRunItemsDoesNotMutateStoredRows(t *testing.T) {
	content := string(make([]rune, 300))
	runs := &memoryRuns{values: map[string]entity.EvalRun{
		"run-1": {
			ID: "run-1",
			Rows: []entity.EvalRow{{
				Reference: content,
				Retrieval: &entity.EvalRetrieval{Items: []entity.EvalHit{{Content: content}}},
			}},
		},
	}}
	service := NewService(nil, runs, nil, nil, Options{Enabled: true})
	result, err := service.RunItems(context.Background(), "run-1", &dto.EvalItemsQuery{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(result.Items[0].Reference)) != 201 {
		t.Fatal("预览未按约定截断")
	}
	stored, _ := runs.Get(context.Background(), "run-1")
	if stored.Rows[0].Retrieval.Items[0].Content != content {
		t.Fatal("页面投影修改了原始证据")
	}
}
