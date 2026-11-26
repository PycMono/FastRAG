package evaluation

import (
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func TestRunProjectionDoesNotExposeSnapshot(t *testing.T) {
	run := entity.EvalRun{
		ID:       "run-1",
		Dataset:  fixture(),
		Metadata: map[string]any{"internal": "not-for-client"},
		Rows:     []entity.EvalRow{{Reference: "private reference"}},
	}
	result := runVO(run, false)
	if result.ID != run.ID {
		t.Fatal("任务投影丢失 ID")
	}
}
