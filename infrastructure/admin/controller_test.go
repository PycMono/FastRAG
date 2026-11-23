package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PycMono/FastRAG/infrastructure/observability"
	"github.com/gin-gonic/gin"
)

func TestManagementAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logs, err := observability.NewStore(filepath.Join(t.TempDir(), "logs.jsonl"), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	token := "test-admin-token-with-at-least-24-chars"
	for _, tc := range []struct {
		name, configured, supplied string
		want                       int
	}{{"disabled", "", "", 503}, {"unauthorized", token, "", 401}, {"wrong", token, "wrong", 401}, {"authorized", token, token, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FASTRAG_ADMIN_TOKEN", tc.configured)
			ctl := NewController(logs)
			r := gin.New()
			r.GET("/status", ctl.Auth, ctl.Status)
			req := httptest.NewRequest(http.MethodGet, "/status", nil)
			if tc.supplied != "" {
				req.Header.Set("Authorization", "Bearer "+tc.supplied)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("want %d got %d", tc.want, w.Code)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("admin data must not be cached")
			}
		})
	}
}
