package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PycMono/FastRAG/domain/entity"
)

type memoryRuns struct {
	mu      sync.Mutex
	values  map[string]entity.EvalRun
	changed chan entity.EvalRun
}

func (m *memoryRuns) Create(ctx context.Context, run entity.EvalRun) error {
	return m.Save(ctx, run)
}

func (m *memoryRuns) Save(_ context.Context, v entity.EvalRun) error {
	// 与文件仓储一致，每次读写都是独立快照。
	b, _ := json.Marshal(v)
	var copy entity.EvalRun
	_ = json.Unmarshal(b, &copy)
	m.mu.Lock()
	m.values[v.ID] = copy
	m.mu.Unlock()
	m.changed <- copy
	return nil
}

func (m *memoryRuns) Get(_ context.Context, id string) (entity.EvalRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[id], nil
}
func (m *memoryRuns) List(context.Context) ([]entity.EvalRun, error) {
	return nil, nil
}

func (m *memoryRuns) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.values, id)
	m.mu.Unlock()
	return nil
}

func (m *memoryRuns) Recover(context.Context) error {
	return nil
}

type fixedDataset struct {
	value entity.EvalDataset
}

func (d fixedDataset) List(context.Context) ([]entity.EvalDatasetInfo, error) {
	return nil, nil
}

func (d fixedDataset) Load(context.Context, string) (entity.EvalDataset, error) {
	return d.value, nil
}

type searchFunc func(context.Context, entity.EvalCase, entity.EvalProfile) (*entity.EvalRetrieval, error)

func (f searchFunc) Search(ctx context.Context, c entity.EvalCase, p entity.EvalProfile) (*entity.EvalRetrieval, error) {
	return f(ctx, c, p)
}

type scoreFunc func(context.Context, entity.EvalScoreInput) (*entity.EvalScoreOutput, error)

func (f scoreFunc) Score(ctx context.Context, in entity.EvalScoreInput) (*entity.EvalScoreOutput, error) {
	return f(ctx, in)
}

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
func awaitTerminal(t *testing.T, m *memoryRuns) entity.EvalRun {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case r := <-m.changed:
			if r.Status != "queued" && r.Status != "running" {
				return r
			}
		case <-timer.C:
			t.Fatal("任务未结束")
			return entity.EvalRun{}
		}
	}
}
func TestEvaluationResults(t *testing.T) {
	for _, scenario := range []string{"empty", "search_error", "score_error", "ok"} {
		t.Run(scenario, func(t *testing.T) {
			m := &memoryRuns{values: map[string]entity.EvalRun{}, changed: make(chan entity.EvalRun, 10)}
			calls := 0
			search := searchFunc(func(context.Context, entity.EvalCase, entity.EvalProfile) (*entity.EvalRetrieval, error) {
				if scenario == "search_error" {
					return nil, errors.New("private error")
				}
				if scenario == "empty" {
					return &entity.EvalRetrieval{}, nil
				}
				return &entity.EvalRetrieval{Items: []entity.EvalHit{{ChunkID: "c1", Content: "证据"}}}, nil
			})
			score := scoreFunc(func(_ context.Context, in entity.EvalScoreInput) (*entity.EvalScoreOutput, error) {
				calls++
				if scenario == "score_error" {
					return nil, errors.New("secret")
				}
				value := 1.0
				out := &entity.EvalScoreOutput{RequestID: in.RequestID}
				for _, name := range in.Metrics {
					out.Metrics = append(out.Metrics, entity.EvalMetric{Name: name, Status: "ok", Value: &value})
				}
				return out, nil
			})
			s := NewService(fixedDataset{fixture()}, m, search, score, Options{Enabled: true})
			t.Cleanup(func() { _ = s.Stop(context.Background()) })
			if _, err := s.Create(context.Background(), "test", "pilot", ""); err != nil {
				t.Fatal(err)
			}
			r := awaitTerminal(t, m)
			if r.Done != 1 || len(r.Rows) != 1 {
				t.Fatalf("missing row: %+v", r)
			}
			if scenario == "empty" {
				if calls != 0 || *r.Summary["p1/ContextRecall"].Mean != 0 {
					t.Fatal("空结果不得调用模型，覆盖应为0")
				}
			}
			if scenario == "search_error" {
				if calls != 0 || r.RetrievalFailures != 1 || r.Status != "partial" {
					t.Fatal("检索失败统计错误")
				}
			}
			if scenario == "score_error" {
				if r.RetrievalFailures != 0 || r.Status != "partial" || r.Summary["p1/ContextRecall"].Mean != nil {
					t.Fatal("评分失败不能变检索0分")
				}
			}
			if scenario == "ok" && r.Status != "succeeded" {
				t.Fatal(r.Status)
			}
		})
	}
}
func TestEvaluationSingleFlightAndCancel(t *testing.T) {
	m := &memoryRuns{values: map[string]entity.EvalRun{}, changed: make(chan entity.EvalRun, 10)}
	started := make(chan struct{})
	search := searchFunc(func(ctx context.Context, _ entity.EvalCase, _ entity.EvalProfile) (*entity.EvalRetrieval, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	s := NewService(fixedDataset{fixture()}, m, search, nil, Options{Enabled: true})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	id, err := s.Create(context.Background(), "test", "pilot", "")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = s.Create(context.Background(), "test", "second", ""); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if err = s.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if r := awaitTerminal(t, m); r.Status != "cancelled" {
		t.Fatal(r.Status)
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
	v := 0.8
	rows := []entity.EvalRow{{
		Profile: "p",
		Score: &entity.EvalScoreOutput{
			Metrics: []entity.EvalMetric{
				{Name: "recall", Status: "ok", Value: &v},
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
