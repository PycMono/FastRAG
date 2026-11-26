package evaluation

import (
	"math"
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func TestDeterministicRetrievalDiagnostics(t *testing.T) {
	run := entity.EvalRun{
		Dataset: entity.EvalDataset{Cases: []entity.EvalCase{{
			ID: "q1", Answerable: true, ReferenceContextIDs: []string{"a", "b"},
		}}},
		Rows: []entity.EvalRow{{
			CaseID: "q1", Profile: "p", RetrievalStatus: "ok", RetrievalLatencyMS: 100,
			Retrieval: &entity.EvalRetrieval{Items: []entity.EvalHit{
				{ChunkID: "x", Content: "noise"}, {ChunkID: "b", Content: "evidence"},
				{ChunkID: "y", Content: "evidence"},
			}},
		}},
	}
	stats := retrievalDiagnostics(run)["p"]
	if *stats.RecallAt5 != .5 || *stats.PrecisionAt5 != .2 || *stats.MRRAt5 != .5 || *stats.SuccessAt5 != 1 {
		t.Fatalf("错误指标: %+v", stats)
	}
	expected := (1 / math.Log2(3)) / (1 + 1/math.Log2(3))
	if math.Abs(*stats.BinaryNDCGAt5-expected) > 1e-9 || stats.DuplicateRate != 1.0/3 {
		t.Fatal("nDCG或重复率错误")
	}
	run.Dataset.Cases[0].ReferenceContextIDs = nil
	if retrievalDiagnostics(run)["p"].RecallAt5 != nil {
		t.Fatal("缺标注不应伪造0分")
	}
}
