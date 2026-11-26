package serviceimpl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PycMono/FastRAG/domain/entity"
)

func TestEvalHTTPValidatesScoringEnvelope(t *testing.T) {
	for _, body := range []string{
		`{"results":[]}`,
		`{"results":[{"request_id":"wrong","metrics":[]}]}`,
		`{"results":[{"request_id":"r1","metrics":[{"name":"ContextRecall","status":"ok","value":2}]}]}`,
		`{"results":[{"request_id":"r1","metrics":[{"name":"ContextRecall","status":"error","value":0}]}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing auth")
				}
				fmt.Fprint(w, body)
			}))
			defer srv.Close()

			client := &EvalHTTP{
				Client:   srv.Client(),
				ScoreURL: srv.URL,
				Token:    "token",
			}
			input := entity.EvalScoreInput{
				RequestID: "r1",
				Metrics:   []string{"ContextRecall"},
			}
			if _, err := client.ScoreBatch(context.Background(), []entity.EvalScoreInput{input}); err == nil {
				t.Fatal("invalid score accepted")
			}
		})
	}
}
