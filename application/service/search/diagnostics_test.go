package search

import (
	"context"
	"errors"
	"fmt"
	"github.com/PycMono/FastRAG/common/vo"
	"math"
	"strings"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	"github.com/PycMono/FastRAG/domain/entity"
	"github.com/PycMono/FastRAG/domain/interfaces"
	"github.com/PycMono/FastRAG/domain/repository"
)

type budgetRerank struct {
	count int
	fail  bool
	nop   bool
}

func (r *budgetRerank) Rerank(ctx context.Context, q string, c []interfaces.RerankCandidate, n int) ([]interfaces.ScoredIndex, error) {
	r.count = len(c)
	if r.fail {
		return nil, errors.New("private upstream failure")
	}
	out := make([]interfaces.ScoredIndex, len(c))
	for i := range c {
		score := 0.9 - float64(i)*0.1
		if r.nop {
			score = interfaces.NoRerankScore
		}
		out[i] = interfaces.ScoredIndex{Index: i, Score: score}
	}
	return out, nil
}

func TestRecallSettingsBudgetAndFallbackThreshold(t *testing.T) {
	for _, mode := range []string{"success", "failure", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			var actual repository.VectorSearchReq
			kb := &mockKBRepo{load: func(context.Context, []string, string) (entity.Knowledges, error) {
				return entity.Knowledges{defaultKB()}, nil
			}}
			docs := &mockDocRepo{load: func(context.Context, []uint64) (entity.KnowledgeDocs, error) {
				return entity.KnowledgeDocs{defaultDoc()}, nil
			}}
			store := &mockVectorStore{search: func(ctx context.Context, req repository.VectorSearchReq) (repository.SearchResult, error) {
				actual = req
				r := repository.SearchResult{}
				for i := 0; i < 6; i++ {
					r.BM25 = append(r.BM25, repository.VectorHit{ChunkID: fmt.Sprint("b", i), DocID: 100, KBID: 1, Content: "keyword", Score: 10 - float64(i)})
					r.KNN = append(r.KNN, repository.VectorHit{ChunkID: fmt.Sprint("v", i), DocID: 100, KBID: 1, Content: "semantic", Score: 0.9 - float64(i)*0.01})
				}
				return r, nil
			}}
			ranker := &budgetRerank{fail: mode == "failure", nop: mode == "disabled"}
			svc := NewService(kb, docs, store, mockEmbeddingRegistry{&mockEmbedding{3}}, mockRerankRegistry{ranker}, Tuning{BM25Top: 80, KNNTops: 90, NumCandidates: 700})
			budget := 3
			result, err := svc.Search(context.Background(), &dto.SearchDTO{Tenant: "demo", KBNos: []string{"demo-kb"}, Query: "test", Limit: 2, RetrieveCount: &budget, RerankSwitch: true, MinScore: 0.5})
			if err != nil {
				t.Fatal(err)
			}
			if actual.BM25Top != 80 || actual.KNNTops != 90 || actual.NumCandidates != 700 {
				t.Fatalf("configured recall ignored: %#v", actual)
			}
			if ranker.count != 3 {
				t.Fatalf("rerank budget exceeded: %d", ranker.count)
			}
			if result.Meta == nil {
				t.Fatal("missing score metadata")
			}
			if mode == "success" {
				if !result.Meta.RerankApplied || result.Meta.Degraded || result.Meta.ThresholdSkipped {
					t.Fatalf("success incorrectly degraded: %#v", result.Meta)
				}
			} else {
				if result.Meta.RerankApplied || !result.Meta.Degraded || !result.Meta.ThresholdSkipped || result.Meta.ScoreType != "rrf" {
					t.Fatalf("fallback metadata wrong: %#v", result.Meta)
				}
				for _, warning := range result.Meta.Warnings {
					if strings.Contains(warning, "private") {
						t.Fatal("upstream error leaked")
					}
				}
			}
			if len(result.Items) != 2 {
				t.Fatalf("threshold erased fallback results: %d", len(result.Items))
			}
		})
	}
}

func TestDiagnosticsRanksAndOptionalPayload(t *testing.T) {
	kb := &mockKBRepo{load: func(context.Context, []string, string) (entity.Knowledges, error) {
		return entity.Knowledges{defaultKB()}, nil
	}}
	docs := &mockDocRepo{load: func(context.Context, []uint64) (entity.KnowledgeDocs, error) {
		return entity.KnowledgeDocs{defaultDoc()}, nil
	}}
	store := &mockVectorStore{search: func(context.Context, repository.VectorSearchReq) (repository.SearchResult, error) {
		return repository.SearchResult{
			BM25: []repository.VectorHit{{ChunkID: "shared", DocID: 100, KBID: 1, Score: 8, Content: "match"}},
			KNN:  []repository.VectorHit{{ChunkID: "shared", DocID: 100, KBID: 1, Score: 0.8, Content: "match"}}, TimingsMS: map[string]float64{"bm25": 2, "knn": 3}}, nil
	}}
	svc := newSearchServiceForTest(kb, docs, store, &mockEmbedding{3}, &budgetRerank{})
	in := &dto.SearchDTO{Tenant: "demo", KBNos: []string{"demo-kb"}, Query: "match", Limit: 1, RerankSwitch: true, Diagnostics: true}
	res, err := svc.Search(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	d := res.Items[0].Diagnostics
	if d.BM25Rank != 1 || d.KNNRank != 1 || d.BM25Score == nil || *d.BM25Score != 8 || d.RerankScore == nil {
		t.Fatalf("missing score provenance: %#v", d)
	}
	if !res.Meta.RerankApplied || res.Meta.ScoreType != "rerank" || res.Diagnostics.Counts["fused"] != 1 || res.Diagnostics.TimingsMS["bm25"] != 2 {
		t.Fatalf("incorrect diagnostics: %#v", res)
	}
	in.Diagnostics = false
	res, err = svc.Search(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Diagnostics != nil || res.Items[0].Diagnostics != nil || res.Meta == nil {
		t.Fatal("diagnostic opt-out or metadata broken")
	}
	in.Diagnostics = true
	in.FusionMinScore = 1
	res, err = svc.Search(context.Background(), in)
	if err != nil || len(res.Items) != 0 {
		t.Fatal("fusion threshold not applied before rerank", err)
	}
}

func TestMalformedRerankDoesNotMutateFallback(t *testing.T) {
	ranker := &mockRerank{order: []interfaces.ScoredIndex{{Index: 0, Score: 0.8}, {Index: 1, Score: math.NaN()}}}
	svc := NewService(nil, nil, nil, nil, mockRerankRegistry{ranker}, Tuning{})
	items := []*vo.SearchItemVO{{Score: 0.01, Diagnostics: &vo.HitDiagnostics{FusionScore: 0.01}}, {Score: 0.009, Diagnostics: &vo.HitDiagnostics{FusionScore: 0.009}}}
	_, err := svc.rerankItems(context.Background(), ranker, "query", items)
	if err == nil {
		t.Fatal("invalid model score accepted")
	}
	if items[0].Score != 0.01 || items[0].Diagnostics.RerankScore != nil {
		t.Fatal("fallback score mutated before rerank failed")
	}
}
