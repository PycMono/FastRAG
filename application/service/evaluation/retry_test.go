package evaluation

import (
	"context"
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func TestRetryOnlyFailedMetricsWithoutRetrieval(t *testing.T) {
	value := 0.9
	old := entity.EvalRun{
		ID: "old", Status: "partial", Dataset: fixture(),
		Rows: []entity.EvalRow{{
			CaseID: "q1", Profile: "p1", RetrievalStatus: "ok",
			Input: &entity.EvalScoreInput{Metrics: []string{"ContextRelevance", "ContextRecall"}},
			Score: &entity.EvalScoreOutput{Metrics: []entity.EvalMetric{
				{Name: "ContextRelevance", Status: "ok", Value: &value},
				{Name: "ContextRecall", Status: "error", Error: "busy"},
			}},
		}},
	}
	runs := &memoryRuns{values: map[string]entity.EvalRun{"old": old}, changed: make(chan entity.EvalRun, 10)}
	scorer := scoreFunc(func(ctx context.Context, input entity.EvalScoreInput) (*entity.EvalScoreOutput, error) {
		if len(input.Metrics) != 1 || input.Metrics[0] != "ContextRecall" {
			t.Fatal("重复评分成功项")
		}
		return &entity.EvalScoreOutput{Metrics: []entity.EvalMetric{{Name: "ContextRecall", Status: "ok", Value: &value}}}, nil
	})
	service := NewService(nil, runs, nil, scorer, Options{Enabled: true})
	t.Cleanup(func() { _ = service.Stop(context.Background()) })
	if _, err := service.RetryScores(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	result := awaitTerminal(t, runs)
	if result.Status != "succeeded" || result.Metadata["retry_of"] != "old" {
		t.Fatalf("%+v", result)
	}
	stored, _ := runs.Get(context.Background(), "old")
	if stored.Rows[0].Score.Metrics[1].Status != "error" {
		t.Fatal("覆盖历史失败")
	}
}
