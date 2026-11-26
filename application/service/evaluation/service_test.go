package evaluation

import (
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func fixture() entity.EvalDataset {
	return entity.EvalDataset{
		Manifest: entity.EvalManifest{
			DatasetVersion:    "v1",
			AnnotationVersion: "a1",
			CorpusSnapshot:    "s1",
			CorpusHash:        "hash",
			RetrievalSettings: map[string]any{"top": 50},
		},
		Cases: []entity.EvalCase{{
			ID:               "q1",
			Question:         "问题",
			Tenant:           "t1",
			KBNos:            []string{"kb"},
			Split:            "dev",
			Answerable:       true,
			Reference:        "证据",
			ReferenceSources: []entity.EvalSource{{DocID: "1"}},
		}},
		Profiles: []entity.EvalProfile{{
			Name:          "p1",
			Limit:         10,
			RetrieveCount: 20,
			DenseWeight:   0.5,
		}},
	}
}

func TestValidateRejectsHoldoutAndMissingReference(t *testing.T) {
	d := fixture()
	d.Cases[0].Split = "holdout"
	if validate(d) == nil {
		t.Fatal("holdout accepted")
	}

	d = fixture()
	d.Cases[0].Reference = ""
	if validate(d) == nil {
		t.Fatal("missing reference accepted")
	}
}

func TestSummaryDoesNotCountErrorsAsZero(t *testing.T) {
	value := 0.8
	rows := []entity.EvalRow{{
		Profile: "p",
		Score: &entity.EvalScoreOutput{
			Metrics: []entity.EvalMetric{
				{Name: "recall", Status: "ok", Value: &value},
				{Name: "recall", Status: "timeout"},
				{Name: "recall", Status: "not_applicable"},
			},
		},
	}}

	got := Summarize(rows)["p/recall"]
	if got.Mean == nil || *got.Mean != 0.8 || got.Coverage != 0.5 || got.Errors != 1 || got.NotApplicable != 1 {
		t.Fatalf("%+v", got)
	}
}
