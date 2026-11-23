package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWorkbenchAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	ctl := NewController()
	r.GET("/", ctl.Index)
	r.GET("/assets/*filepath", ctl.Asset)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="app"`) {
		t.Fatal("expected Vue entry point", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("HTML must revalidate")
	}
	files, err := fs.Glob(staticFiles, "static/assets/*.js")
	if err != nil || len(files) == 0 {
		t.Fatal("missing bundled JavaScript", err)
	}
	for _, path := range files {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(path, "static/"), nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
			t.Fatal("invalid JavaScript response", w.Code, w.Header())
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
			t.Fatal("hashed asset should be immutable")
		}
	}
	for _, path := range []string{"/assets/missing.js", "/assets/../index.html"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 404 {
			t.Fatalf("%s: expected 404, got %d", path, w.Code)
		}
	}
}
