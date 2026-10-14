package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func serve(t *testing.T, files fstest.MapFS, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(files).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHandler(t *testing.T) {
	files := fstest.MapFS{
		"index.html":       {Data: []byte("<app>")},
		"assets/app-1a.js": {Data: []byte("js")},
		".gitkeep":         {},
	}
	for path, want := range map[string]string{
		"/":                 "<app>",
		"/sessions/abc":     "<app>", // client-side route
		"/assets/app-1a.js": "js",
		"/assets/missing":   "<app>",
		"/.gitkeep":         "<app>",
		"/../index.html":    "<app>",
	} {
		rec := serve(t, files, http.MethodGet, path)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("GET %s = %d %q, want %q", path, rec.Code, rec.Body, want)
		}
	}
	if rec := serve(t, files, http.MethodGet, "/assets/app-1a.js"); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Error("assets must be cached")
	}
	if rec := serve(t, files, http.MethodGet, "/"); rec.Header().Get("Cache-Control") != "no-cache" {
		t.Error("index.html must not be cached")
	}
	if rec := serve(t, files, http.MethodPost, "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
}

func TestHandlerWithoutBuild(t *testing.T) {
	rec := serve(t, fstest.MapFS{".gitkeep": {}}, http.MethodGet, "/")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "make ui") {
		t.Errorf("= %d %q", rec.Code, rec.Body)
	}
}
