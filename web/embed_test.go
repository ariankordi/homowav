package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerServesFilesAndFallsBackToIndex(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	handler := handlerFor(dist)
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}
	for _, target := range []string{"/", "/settings", "/assets/"} {
		if rec := get(target); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app</html>") {
			t.Errorf("%s: %d %q, want the index page", target, rec.Code, rec.Body.String())
		}
	}
	rec := get("/assets/app.js")
	if rec.Body.String() != "console.log(1)" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %q, Cache-Control %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
}

func TestHandlerExplainsAMissingBuild(t *testing.T) {
	rec := httptest.NewRecorder()
	handlerFor(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "npm run build") {
		t.Errorf("unbuilt UI: %d %q", rec.Code, rec.Body.String())
	}
}
